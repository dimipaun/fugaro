package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// workflowSecretsFixture is a record of the shape `fugaro update` writes for a
// repository whose app workflow mounts two secrets and whose api workflow
// mounts one. The IDs are containers, never values: they are the short names
// gcp.SecretID and gcp.ProjectSecretID answer, one of each so that both
// spellings of F33 are in the fixture.
func workflowSecretsFixture() WorkflowSecrets {
	return WorkflowSecrets{
		Version: WorkflowSecretsVersion,
		Workflows: map[string][]WorkflowSecret{
			"app": {
				{Env: "NPM_TOKEN", ID: "fugaro-acme-app-npm-token-1a2b"},
				{Env: "SIGNING_KEY", ID: "fugaro-p-init-project-signing-key-9f8e"},
			},
			"api": {{Env: "DB_PASSWORD", ID: "fugaro-acme-app-db-password-4c5d"}},
		},
	}
}

// TestWorkflowSecretsRecordHoldsNoValue: the record is the closed shape of
// names G18 allows under creds/ — a version, the workflows, and for each secret
// the variable and the container — and the test states it as that shape rather
// than as an absence, so a member that could carry a value is an edit a
// reviewer sees here. A node carrying one more member is refused by the read
// rather than half understood, which is the fail-closed direction: a record
// this release cannot read whole is one it must not decide a container from.
//
// What makes the shape enough is what the members ARE: the ID is the name of a
// container the service reads with its own account at call time, and the env is
// the variable the value becomes in the job. Neither is a value, and the test
// below writes the record through the writer that a repository's own update
// calls, so the shape it reads back is the one a real installation holds.
func TestWorkflowSecretsRecordHoldsNoValue(t *testing.T) {
	db := newFakeDB()
	if err := PutWorkflowSecrets(ctx, db, run.Slug, workflowSecretsFixture()); err != nil {
		t.Fatalf("writing the record: %v", err)
	}

	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, PathWorkflowSecrets(run.Slug)), &node); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	got := make([]string, 0, len(node))
	for k := range node {
		got = append(got, k)
	}
	slices.Sort(got)
	if want := []string{"version", "workflows"}; !slices.Equal(got, want) {
		t.Fatalf("the record node carries %v; want exactly %v — the record names the containers a repository may fetch and holds no value", got, want)
	}
	var workflows map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(node["workflows"], &workflows); err != nil {
		t.Fatalf("decoding the record's workflows: %v", err)
	}
	for name, rows := range workflows {
		if len(rows) == 0 {
			t.Fatalf("workflow %s is recorded with no secrets: a workflow with none is absent from the map", name)
		}
		for _, row := range rows {
			keys := make([]string, 0, len(row))
			for k := range row {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if want := []string{"env", "id"}; !slices.Equal(keys, want) {
				t.Fatalf("workflow %s records a secret with %v; want exactly %v", name, keys, want)
			}
		}
	}

	// The names it carries are the caller's own, unchanged, and the containers
	// are the two spellings F33 resolves to.
	read, found, err := ReadWorkflowSecrets(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	want := workflowSecretsFixture()
	if !slices.Equal(read.Workflows["app"], want.Workflows["app"]) || !slices.Equal(read.Workflows["api"], want.Workflows["api"]) {
		t.Fatalf("the record reads back as %v; want %v", read.Workflows, want.Workflows)
	}

	// The member a value would have to arrive in is refused, and refused by the
	// read: an unreadable record must fail closed, never be read as the half of
	// itself this release models.
	var withValue map[string]any
	if err := json.Unmarshal(rawNode(t, db, PathWorkflowSecrets(run.Slug)), &withValue); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	withValue["value"] = "x"
	db.put(PathWorkflowSecrets(run.Slug), withValue)
	if _, _, err := ReadWorkflowSecrets(ctx, db, run.Slug); err == nil {
		t.Fatal("a record node carrying a member this release does not model was read as a record: an unreadable record must fail closed")
	}
}

// TestWorkflowSecretsReplacesWhatTheNodeHeld: the record says what a
// repository's workflows mount NOW, so a write replaces the node whole — a
// workflow this run no longer mounts, and a secret a workflow dropped, are gone
// from the answer rather than merged into it. A merge would leave the service
// serving a container the job stopped mounting, which is the reach G19 bounds,
// and it is the shape a reader cannot see: the node would look like a record of
// the current mounts.
func TestWorkflowSecretsReplacesWhatTheNodeHeld(t *testing.T) {
	db := newFakeDB()
	if err := PutWorkflowSecrets(ctx, db, run.Slug, workflowSecretsFixture()); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	next := WorkflowSecrets{Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{
		"app": {{Env: "NPM_TOKEN", ID: "fugaro-acme-app-npm-token-1a2b"}},
	}}
	if err := PutWorkflowSecrets(ctx, db, run.Slug, next); err != nil {
		t.Fatalf("replacing the record: %v", err)
	}
	read, found, err := ReadWorkflowSecrets(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if _, ok := read.Workflows["api"]; ok {
		t.Errorf("the replaced record still holds the api workflow: %v", read.Workflows)
	}
	if got := read.Workflows["app"]; len(got) != 1 || got[0].Env != "NPM_TOKEN" {
		t.Errorf("the replaced app workflow holds %v; want the one secret the write named", got)
	}

	// A record that holds no workflow secrets at all is written rather than
	// deleted: an absent node is the previous update's answer to a reader that
	// cannot tell it from "this release never ran here", and the empty map says
	// the repository mounts none. The version on the node is the writer's
	// whatever the caller passed: the format is this release's.
	if err := PutWorkflowSecrets(ctx, db, run.Slug, WorkflowSecrets{}); err != nil {
		t.Fatalf("clearing the record: %v", err)
	}
	read, found, err = ReadWorkflowSecrets(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the cleared record: found=%v err=%v", found, err)
	}
	if len(read.Workflows) != 0 {
		t.Errorf("the cleared record holds %v; want none", read.Workflows)
	}
	if read.Version != WorkflowSecretsVersion {
		t.Errorf("the cleared record's version is %d, want %d", read.Version, WorkflowSecretsVersion)
	}
}

// TestWorkflowSecretsAbsentAndUnreadable: the three states a reader has to tell
// apart. A repository no update recorded is found=false and no error — the
// service serves nothing, which is the fail-closed answer G19 asks for. A node
// written by a release this one does not know, and one that is not a record at
// all, are errors: neither may be read as a record, because the members this
// release recognises are not the whole of a document it did not write.
func TestWorkflowSecretsAbsentAndUnreadable(t *testing.T) {
	db := newFakeDB()
	if _, found, err := ReadWorkflowSecrets(ctx, db, run.Slug); err != nil || found {
		t.Fatalf("an unwritten record reads as found=%v err=%v; want found=false and no error", found, err)
	}
	// A slug that names no repository builds no path at all: an empty segment
	// would collapse the node onto another repository's.
	if _, _, err := ReadWorkflowSecrets(ctx, db, ""); !errors.Is(err, ErrNoWorkflowSecrets) {
		t.Errorf("reading an empty slug: err=%v, want ErrNoWorkflowSecrets", err)
	}
	if err := PutWorkflowSecrets(ctx, db, "", WorkflowSecrets{Version: WorkflowSecretsVersion}); !errors.Is(err, ErrNoWorkflowSecrets) {
		t.Errorf("writing an empty slug: err=%v, want ErrNoWorkflowSecrets", err)
	}

	db.put(PathWorkflowSecrets(run.Slug), map[string]any{"version": WorkflowSecretsVersion + 1, "workflows": map[string]any{}})
	if _, _, err := ReadWorkflowSecrets(ctx, db, run.Slug); !errors.Is(err, ErrWorkflowSecretsVersion) {
		t.Errorf("a record of a newer version: err=%v, want ErrWorkflowSecretsVersion", err)
	}
	db.put(PathWorkflowSecrets(run.Slug), "not a record")
	if _, _, err := ReadWorkflowSecrets(ctx, db, run.Slug); err == nil {
		t.Error("a node that is not a record was read as one")
	}
}

// TestWorkflowSecretsWriterRefusesARecordNothingCanBeDecidedFrom: the writer
// holds the record to what a reader needs — a workflow with a name, a variable
// with a container — and refuses a workflow that names one variable twice,
// which is an answer no reader could decide from. Fail closed where the mistake
// is, rather than at every reader after it.
func TestWorkflowSecretsWriterRefusesARecordNothingCanBeDecidedFrom(t *testing.T) {
	for name, rec := range map[string]WorkflowSecrets{
		"a workflow with no name":   {Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{"": {{Env: "A_TOKEN", ID: "x"}}}},
		"a secret with no variable": {Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{"app": {{ID: "x"}}}},
		"a secret with no container": {Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{
			"app": {{Env: "A_TOKEN"}},
		}},
		"one variable named twice": {Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{
			"app": {{Env: "A_TOKEN", ID: "x"}, {Env: "A_TOKEN", ID: "y"}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			db := newFakeDB()
			err := PutWorkflowSecrets(ctx, db, run.Slug, rec)
			if err == nil {
				t.Fatalf("the writer accepted %+v", rec)
			}
			if !errors.Is(err, ErrWorkflowSecretsMalformed) {
				t.Errorf("err = %v, want ErrWorkflowSecretsMalformed", err)
			}
			if _, found, err := ReadWorkflowSecrets(ctx, db, run.Slug); err != nil || found {
				t.Errorf("a refused write left the node at found=%v err=%v; want nothing written", found, err)
			}
		})
	}
}

// TestWorkflowSecretsAreWrittenInEnvOrder: the same mounts give the same node
// whatever order the config listed them in, so a record is a value a reader can
// compare and a diff can be read. The writer orders the rows, not the caller.
func TestWorkflowSecretsAreWrittenInEnvOrder(t *testing.T) {
	db := newFakeDB()
	rec := WorkflowSecrets{Version: WorkflowSecretsVersion, Workflows: map[string][]WorkflowSecret{
		"app": {{Env: "Z_TOKEN", ID: "z"}, {Env: "A_TOKEN", ID: "a"}, {Env: "M_TOKEN", ID: "m"}},
	}}
	if err := PutWorkflowSecrets(ctx, db, run.Slug, rec); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	read, _, err := ReadWorkflowSecrets(ctx, db, run.Slug)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	got := make([]string, 0, 3)
	for _, s := range read.Workflows["app"] {
		got = append(got, s.Env)
	}
	if want := []string{"A_TOKEN", "M_TOKEN", "Z_TOKEN"}; !slices.Equal(got, want) {
		t.Errorf("the record holds %v; want %v", got, want)
	}
	// The caller's own slice is not reordered under it.
	if rec.Workflows["app"][0].Env != "Z_TOKEN" {
		t.Errorf("the writer reordered the caller's record: %v", rec.Workflows["app"])
	}
}
