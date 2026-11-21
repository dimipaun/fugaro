package projectpolicy

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

// workspace_credentials_migrate_test.go is the workspace credentials node's
// migration (workspace_credentials_migrate.go): the version-1 document the
// release before this one wrote, carried forward in place on the owner's one
// command, and every class of node the chain refuses untouched.

// credentialsV1Node is a version-1 node as the release before this one wrote
// it: one member with one job account and one pool entry offered to that
// member, and neither of the two columns version 2 adds.
const credentialsV1Node = `{
  "version": 1,
  "name": "workspace-alpha",
  "members": [
    {
      "project": "member-b",
      "firebase_project": "member-b-fb",
      "database_region": "europe-west1",
      "fp": "member-b",
      "job_accounts": ["job-account-b@member-b-gcp.iam.gserviceaccount.com"],
      "exchange_url": "https://exchange.example.com/v1"
    }
  ],
  "shared": [{"name": "claude-oauth-token-team", "projects": ["member-b"]}]
}`

// TestNodeMigratesInPlace is the migration this task exists for, and it is the
// record's own (TestMigrateWorkspaceCarriesVersion0ToVersion1) read on the other
// document the workspace owner's `fugaro update` publishes: a workspace whose
// owner already holds a version-1 credentials node converges on the one command
// rather than being asked to publish again, and the two columns version 2 adds
// are the version's own silence and never an invented declaration — a job
// account carries no repositories (rule 3's state, which the scope refuses a run
// on with the reason that names this update, R19), and a pool entry is the model
// credential the pool held before the kind was a column (F15).
func TestNodeMigratesInPlace(t *testing.T) {
	in := []byte(credentialsV1Node)
	out, changes, err := MigrateWorkspaceCredentials(in)
	if err != nil {
		t.Fatalf("a version-1 node was not migrated: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("the migration reports %d steps, want 1", len(changes))
	}
	ch := changes[0]
	if ch.From != 1 || ch.To != WorkspaceCredentialsVersion {
		t.Errorf("the step moves %d to %d, want 1 to %d", ch.From, ch.To, WorkspaceCredentialsVersion)
	}
	for _, kept := range []string{"name", "members", "shared"} {
		if !slices.Contains(ch.Kept, kept) {
			t.Errorf("the step reports keeping %v, and %s is what it must keep", ch.Kept, kept)
		}
	}
	if !strings.Contains(ch.Changed, SharedKindModel) || !strings.Contains(ch.Changed, "version") {
		t.Errorf("the step says it changed %q, and the version field and the kind of every pool entry are the whole of what it moves", ch.Changed)
	}
	w, found, err := DecodeWorkspaceCredentials(out)
	if err != nil || !found {
		t.Fatalf("the migrated node is not one the current reader reads: (%v, %v)", found, err)
	}
	if w.Version != WorkspaceCredentialsVersion {
		t.Errorf("the migrated node is version %d, want %d", w.Version, WorkspaceCredentialsVersion)
	}
	if w.UnknownKeys != nil {
		t.Errorf("the migrated node carries unknown keys %v: the step writes the columns this Fugaro knows and nothing else", w.UnknownKeys)
	}
	if len(w.Members) != 1 {
		t.Fatalf("the migrated node carries %d rows, want the one member", len(w.Members))
	}
	row := w.Members[0]
	if row.Project != "member-b" || row.FirebaseProject != "member-b-fb" || row.DatabaseRegion != "europe-west1" ||
		row.FP != "member-b" || row.ExchangeURL != "https://exchange.example.com/v1" {
		t.Errorf("the migrated row is %+v, want every value the version-1 document stated", row)
	}
	if !slices.Equal(row.JobAccounts, []string{"job-account-b@member-b-gcp.iam.gserviceaccount.com"}) {
		t.Errorf("the migrated row's accounts are %v, want the one the version-1 document stated", row.JobAccounts)
	}
	if len(row.JobAccountRepos) != 0 {
		t.Errorf("the migrated row's job_account_repos are %+v, want none: version 1 never said which repositories an account is a job of, and that empty list is rule 3's state — the scope refuses such an account's runs with the reason that names this update — rather than a declaration a step invented", row.JobAccountRepos)
	}
	if len(w.Shared) != 1 {
		t.Fatalf("the migrated node carries %d pool entries, want the one", len(w.Shared))
	}
	entry := w.Shared[0]
	if entry.Name != "claude-oauth-token-team" || !slices.Equal(entry.Projects, []string{"member-b"}) {
		t.Errorf("the migrated entry is %+v, want the name and the projects the version-1 document stated", entry)
	}
	if entry.Kind != SharedKindModel {
		t.Errorf("the migrated entry's kind is %q, want %q: the pool held model credentials and nothing else before the kind was a column (F15)", entry.Kind, SharedKindModel)
	}
	if entry.AllMembers {
		t.Error("the migrated entry says all_members: version 1 had no way to say it, and a step that turned the projects it names into every member would widen the pool by a version bump")
	}
	if len(entry.Covers) != 0 {
		t.Errorf("the migrated entry covers %v, and a model entry covers no repository", entry.Covers)
	}

	// The node already at this Fugaro's version comes back byte for byte: it is
	// not judged here (that is DecodeWorkspaceCredentials' answer), and
	// re-encoding it would rewrite the owner's document on every run.
	cur, err := WorkspaceCredentialsNode(w)
	if err != nil {
		t.Fatal(err)
	}
	out, changes, err = MigrateWorkspaceCredentials(cur)
	if err != nil {
		t.Fatalf("a node at this version was refused: %v", err)
	}
	if len(changes) != 0 || !bytes.Equal(out, cur) {
		t.Errorf("a node at this version was re-encoded: %s with %d changes, want it byte for byte", out, len(changes))
	}

	// A node this writer cannot produce is refused rather than half-migrated,
	// and comes back exactly as it was: the account's repositories are one
	// declaration with the accounts, so a version-1 document whose step would
	// land on a row the reader refuses is a refusal and never a write.
	bad := []byte(strings.Replace(credentialsV1Node, `"job_accounts": ["job-account-b@member-b-gcp.iam.gserviceaccount.com"]`, `"job_accounts": ["not-an-account"]`, 1))
	out, changes, err = MigrateWorkspaceCredentials(bad)
	if err == nil {
		t.Error("a version-1 node naming an account that is not a service account address was migrated")
	}
	if !bytes.Equal(out, bad) || len(changes) != 0 {
		t.Errorf("a refused node must come back untouched, got %s with %d changes", out, len(changes))
	}
}

// TestMigrateCredentialsRefusesANewerNodeUntouched: a node written by a newer
// Fugaro is refused rather than rewritten, so a caller that ignored the error
// could not write a half-migrated document over the one every run of every
// member is admitted against.
func TestMigrateCredentialsRefusesANewerNodeUntouched(t *testing.T) {
	raw := []byte(`{"version":3,"name":"workspace-alpha","members":[],"shared":[]}`)
	out, changes, err := MigrateWorkspaceCredentials(raw)
	var ve *WorkspaceVersionError
	if !errors.As(err, &ve) {
		t.Fatalf("MigrateWorkspaceCredentials = %v, want a *WorkspaceVersionError", err)
	}
	if ve.Found != 3 || ve.Understood != WorkspaceCredentialsVersion {
		t.Errorf("the refusal carries (%d, %d), want (3, %d)", ve.Found, ve.Understood, WorkspaceCredentialsVersion)
	}
	if !bytes.Equal(out, raw) || len(changes) != 0 {
		t.Errorf("a refused node must come back untouched, got %s with %d changes", out, len(changes))
	}
}

// TestMigrateCredentialsRefusesWhatItCannotReadUntouched: the classes of node no
// version can be read from. None is a rewrite, none reaches a step, and the two
// an absent or zero version stands for are refusals here — there is no file
// shape below version 1 to carry forward, so a node that carries none was not
// written by a release.
func TestMigrateCredentialsRefusesWhatItCannotReadUntouched(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"empty", ``},
		{"null", `null`},
		{"not an object", `[]`},
		{"no version key", `{"name":"workspace-alpha","members":[],"shared":[]}`},
		{"version zero", `{"version":0,"name":"workspace-alpha","members":[],"shared":[]}`},
		{"a version that is not an integer", `{"version":"1","name":"workspace-alpha","members":[],"shared":[]}`},
		{"a version below zero", `{"version":-1,"name":"workspace-alpha","members":[],"shared":[]}`},
		{"a key written twice", `{"version":1,"name":"workspace-alpha","name":"workspace-beta","members":[],"shared":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changes, err := MigrateWorkspaceCredentials([]byte(tc.raw))
			if err == nil {
				t.Fatalf("MigrateWorkspaceCredentials(%s) succeeded, and %s is a node no version can be read from", tc.raw, tc.name)
			}
			var ne *WorkspaceNodeError
			if !errors.As(err, &ne) {
				t.Errorf("MigrateWorkspaceCredentials(%s) = %v, want a *WorkspaceNodeError", tc.raw, err)
			}
			if !bytes.Equal(out, []byte(tc.raw)) {
				t.Error("a refused node must come back untouched")
			}
			if len(changes) != 0 {
				t.Errorf("a refused node reports %d changes, want none", len(changes))
			}
		})
	}
}

// TestEveryOlderWorkspaceCredentialsVersionHasAMigrator is the version-bump
// guard, the record's own test read on this chain: a bump of
// WorkspaceCredentialsVersion that adds no step leaves every installation
// holding the older node with no way forward but a delete and a republish, which
// is the move `fugaro update` is not allowed to need.
func TestEveryOlderWorkspaceCredentialsVersionHasAMigrator(t *testing.T) {
	for v := 1; v < WorkspaceCredentialsVersion; v++ {
		if _, ok := workspaceCredentialsMigrators[v]; !ok {
			t.Errorf("workspace credentials node version %d has no migrator, and every version from 1 below %d needs one: the owner's one command carries the node forward, and a release must not ship a delete and a republish", v, WorkspaceCredentialsVersion)
		}
	}
	for v := range workspaceCredentialsMigrators {
		if v < 1 || v >= WorkspaceCredentialsVersion {
			t.Errorf("the chain holds a step from version %d, which is not a version below %d this Fugaro reads: a step outside the chain is one the loop never reaches", v, WorkspaceCredentialsVersion)
		}
	}
}
