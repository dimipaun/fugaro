package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// projectSecretsFixture is a record of the shape `fugaro secrets allow` writes:
// a name one workspace's repositories may fetch, a name whose list a `deny`
// emptied (the entry stays, F23), and a name every repository of the project
// may fetch (all_repos).
func projectSecretsFixture() ProjectSecrets {
	return ProjectSecrets{
		Version: ProjectSecretsVersion,
		Secrets: map[string]ProjectSecretRule{
			"deploy-token":    {Repos: []string{"acme/app", "acme/web"}},
			"signing-key":     {Repos: []string{}},
			"workspace-token": {AllRepos: true},
		},
	}
}

// TestProjectSecretsRoundTrip: what WriteProjectSecrets puts at
// creds/projectsecrets is what ReadProjectSecrets reads back, entry for entry —
// including the entry a deny emptied, which is present with an empty list
// rather than gone: update seeds a name only when the record holds no entry for
// it, so an entry that vanished would be re-seeded from the operator's local
// block and the deny undone (F23).
func TestProjectSecretsRoundTrip(t *testing.T) {
	db := newFakeDB()
	want := projectSecretsFixture()
	if err := WriteProjectSecrets(ctx, db, func(ProjectSecrets, bool) (ProjectSecrets, error) {
		return want, nil
	}); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	got, found, err := ReadProjectSecrets(ctx, db)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if got.Version != ProjectSecretsVersion {
		t.Errorf("the record's version is %d, want %d", got.Version, ProjectSecretsVersion)
	}
	if !slices.Equal(got.Secrets["deploy-token"].Repos, []string{"acme/app", "acme/web"}) {
		t.Errorf("deploy-token's list = %v, want [acme/app acme/web]", got.Secrets["deploy-token"].Repos)
	}
	empty, held := got.Secrets["signing-key"]
	if !held || len(empty.Repos) != 0 || empty.AllRepos {
		t.Errorf("signing-key = %+v (held=%v); want an entry with an empty list — the state a deny leaves", empty, held)
	}
	if rule := got.Secrets["workspace-token"]; !rule.AllRepos {
		t.Errorf("workspace-token = %+v, want all_repos", rule)
	}
	// The node's own members, so a member that could carry a value is an edit a
	// reviewer sees here: the record is a version and the names, nothing else.
	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, pathProjectSecrets), &node); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	members := make([]string, 0, len(node))
	for k := range node {
		members = append(members, k)
	}
	slices.Sort(members)
	if want := []string{"secrets", "version"}; !slices.Equal(members, want) {
		t.Fatalf("the record node carries %v; want exactly %v", members, want)
	}
}

// TestProjectSecretsRefusesUnknownFields: a node carrying a member this release
// does not model is refused whole rather than read as the members it recognises
// — the fail-closed direction, because a record this release cannot read whole
// is one it must not admit a repository from. It is the same rule
// ReadWorkflowSecrets holds, and it is what makes a `repos` a reader cannot
// understand a refusal rather than an empty list.
func TestProjectSecretsRefusesUnknownFields(t *testing.T) {
	for name, raw := range map[string]string{
		"a member of the record": `{"version":1,"secrets":{},"admins":["acme"]}`,
		"a member of the rule":   `{"version":1,"secrets":{"deploy-token":{"repos":["acme/app"],"all_repos":false,"owner":"acme"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := newFakeDB()
			db.put(pathProjectSecrets, json.RawMessage(raw))
			if _, _, err := ReadProjectSecrets(ctx, db); err == nil {
				t.Fatal("a node with a member this release does not model was read as the members it recognises")
			}
		})
	}
	// And an absent node is not an error: it is an installation whose owner has
	// allow-listed nothing yet, which the service serves nothing from.
	db := newFakeDB()
	if _, found, err := ReadProjectSecrets(ctx, db); err != nil || found {
		t.Fatalf("an absent record: found=%v err=%v; want found=false and no error", found, err)
	}
}

// TestProjectSecretsVersionRefused: a record a newer release wrote is refused
// by the reader and by the writer, never rewritten as this release's — the
// writer's check is the one decodeStrict cannot make, since the members it
// happens to decode are not the whole of what was recorded.
func TestProjectSecretsVersionRefused(t *testing.T) {
	db := newFakeDB()
	db.put(pathProjectSecrets, ProjectSecrets{Version: ProjectSecretsVersion + 1, Secrets: map[string]ProjectSecretRule{}})
	if _, _, err := ReadProjectSecrets(ctx, db); !errors.Is(err, ErrProjectSecretsVersion) {
		t.Fatalf("reading a newer record = %v, want ErrProjectSecretsVersion", err)
	}
	err := WriteProjectSecrets(ctx, db, func(cur ProjectSecrets, _ bool) (ProjectSecrets, error) { return cur, nil })
	if !errors.Is(err, ErrProjectSecretsVersion) {
		t.Fatalf("writing over a newer record = %v, want ErrProjectSecretsVersion — a rewrite would drop the members this release does not model", err)
	}
}

// TestProjectSecretsAllows: the record's own answer to "may this repository
// fetch this name" — the check the service makes against a run's verified
// repository (D3333). AllRepos admits any repository; a pattern admits what it
// matches, with the local block's rule; a name the record does not hold admits
// nobody; and the empty entry a deny leaves admits nobody either, whatever the
// switch once said.
func TestProjectSecretsAllows(t *testing.T) {
	rec := projectSecretsFixture()
	cases := []struct {
		name, repo string
		want       bool
	}{
		{"deploy-token", "acme/app", true},
		{"deploy-token", "acme/other", false},
		{"workspace-token", "anyone/anything", true},
		{"signing-key", "acme/app", false},
		{"not-in-the-record", "acme/app", false},
	}
	for _, tc := range cases {
		if got := rec.Allows(tc.name, tc.repo); got != tc.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", tc.name, tc.repo, got, tc.want)
		}
	}
	if !(ProjectSecrets{Secrets: map[string]ProjectSecretRule{"deploy-token": {Repos: []string{"acme/*"}}}}).Allows("deploy-token", "acme/anything") {
		t.Error("a workspace pattern does not admit the workspace's repository")
	}
	if (ProjectSecrets{Secrets: map[string]ProjectSecretRule{"deploy-token": {Repos: []string{"*/app"}}}}).Allows("deploy-token", "acme/app") {
		t.Error("a wildcard owner admitted a repository: that is the reach O2 keeps out of the block's rule")
	}
}

// TestProjectSecretsLosesNoConcurrentWrite is the compare-and-swap's own case:
// a writer that lost the race re-reads the node and applies its change to what
// the rival wrote, so a name another operator allow-listed between the read and
// the write is still there when the second write lands. Without the re-read,
// `secrets allow` on one name would drop another operator's entry.
func TestProjectSecretsLosesNoConcurrentWrite(t *testing.T) {
	db := newFakeDB()
	rival := ProjectSecrets{Version: ProjectSecretsVersion, Secrets: map[string]ProjectSecretRule{
		"rival-token": {Repos: []string{"rival/app"}},
	}}
	db.conflicts, db.rival = 1, rival

	if err := WriteProjectSecrets(ctx, db, func(cur ProjectSecrets, _ bool) (ProjectSecrets, error) {
		return cur.WithRule("deploy-token", ProjectSecretRule{Repos: []string{"acme/app"}}), nil
	}); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	got, found, err := ReadProjectSecrets(ctx, db)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if _, held := got.Rule("rival-token"); !held {
		t.Fatalf("the rival's entry was lost in the race: %+v", got.Secrets)
	}
	if rule, held := got.Rule("deploy-token"); !held || !slices.Equal(rule.Repos, []string{"acme/app"}) {
		t.Fatalf("deploy-token = %+v (held=%v), want [acme/app]", rule, held)
	}
}

// TestProjectSecretsWithRuleCopies: WithRule answers a record a caller can hand
// to the writer without mutating the one it read, which is what makes the
// callback passed to WriteProjectSecrets safe to call again after a conflict.
func TestProjectSecretsWithRuleCopies(t *testing.T) {
	base := ProjectSecrets{Version: ProjectSecretsVersion, Secrets: map[string]ProjectSecretRule{"a": {Repos: []string{"acme/app"}}}}
	next := base.WithRule("b", ProjectSecretRule{AllRepos: true})
	if _, held := base.Rule("b"); held {
		t.Fatal("WithRule wrote through to the record it was called on")
	}
	if rule, held := next.Rule("b"); !held || !rule.AllRepos {
		t.Fatalf("the answer does not hold the entry: %+v", next.Secrets)
	}
}
