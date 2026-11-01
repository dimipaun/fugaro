package cli

import (
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// init_policy_publish_test.go is the publish half of the policy stage
// (init_policy_publish.go): the node every run of the project is admitted by,
// built from the local config and written by the one command the user runs.

// staleBudgetNode is the node a release before this one left published: at the
// version this Fugaro writes, so nothing has to be migrated, carrying a
// per-run ceiling the local config no longer states. It is the state D854 is
// about — the node is at this version and is not what the file says.
func staleBudgetNode() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 9},
		"allow_repos": []string{"bitbucket/acme/other", "github/acme/app"},
	}
}

// TestUpdatePublishesThePolicyNodeWhenItIsUnpublished: the stage builds the
// node the local config states and writes it, so a config edit reaches every
// run of the project through the one command the user runs.
func TestUpdatePublishesThePolicyNodeWhenItIsUnpublished(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatalf("the check refused an installation whose node is unpublished: %v", err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the check says %s %q, want a pending publish", st.State, st.Detail)
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the publish was refused: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.Budget.PerRunUSD != 5 || p.Budget.Mode != "enforce" {
		t.Errorf("the published budget is %+v, want the local config's mode and ceiling", p.Budget)
	}
	if p.Version != projectpolicy.Version {
		t.Errorf("the published node is version %d, want %d", p.Version, projectpolicy.Version)
	}
	// The allow-list the node carried is not rebuilt: a publish never re-adds
	// or drops a name (P4), and the seed is the first publish's business.
	if want := []string{"bitbucket/acme/other", "github/acme/app"}; len(p.AllowRepos) != len(want) {
		t.Errorf("allow_repos = %v, want the node's own list", p.AllowRepos)
	}
	// And the generation this machine's file is now written from is recorded,
	// so the next run can tell a local edit from another machine's publish
	// (D854).
	rec := recordedGeneration(t, f)
	if rec == "" {
		t.Errorf("the publish recorded no policy_generation in %s", f.path)
	}
}

// recordedGeneration is the generation the local config records as the one its
// own file was written from, read off the file the stage wrote.
func recordedGeneration(t *testing.T, f *projectFixture) string {
	t.Helper()
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return lc.PolicyGeneration
}

// TestUpdatePolicyStageWritesNothingWhenTheNodeMatches: a node that carries
// what the local config states is left exactly as it is, down to the bytes —
// the stage is not a publisher that rewrites the document every run.
func TestUpdatePolicyStageWritesNothingWhenTheNodeMatches(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Fatalf("a node matching the local config is %s %q, want nothing to do", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the node was rewritten:\nbefore %s\nafter  %s", before, after)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("a run with nothing to publish recorded a generation (%q): the record belongs to a write", gen)
	}
}
