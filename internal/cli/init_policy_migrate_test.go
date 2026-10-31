package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// init_policy_migrate_test.go is the policy node's migration in place
// (init_policy_migrate.go): the stage `fugaro update` runs and the step
// `fugaro upgrade` runs, driven here directly — the two front doors' own
// tests are update_test.go's and upgrade_test.go's, and what this file is
// about is what either of them does to the node.
//
// Every test is hermetic: the gcpfake RTDB of the fixture, the fixture's own
// local config, and no cloud.

// migrationRig is the stage, its engine and the command a run's options were
// taken from.
type migrationRig struct {
	f   *projectFixture
	s   *policyMigrateStage
	cmd *cobra.Command
	out *syncBuf
}

func newMigrationRig(t *testing.T, f *projectFixture, o *initOptions) *migrationRig {
	t.Helper()
	if o.door == "" {
		o.door = updateDoor.name
	}
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	run := &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader("")), cmd: cmd}
	run.setProject(lc)
	return &migrationRig{f: f, s: newPolicyMigrateStage(&initEngine{r: run, lc: lc, path: f.path}), cmd: cmd, out: out}
}

func (h *migrationRig) check() error {
	_, err := h.s.Check(context.Background())
	return err
}

func (h *migrationRig) apply(t *testing.T, yes bool) (string, error) {
	t.Helper()
	_, err := h.s.Apply(context.Background(), initflow.Env{Yes: yes, Interactive: !yes})
	return h.out.String(), err
}

// nodeVersionOn is the version the node at the shared path carries, read as the
// fake database stores it: the assertion every refusal in this file makes is
// that the node did not move.
func nodeVersionOn(t *testing.T, db *gcpfake.RTDB) int {
	t.Helper()
	v := db.Value(projectpolicy.PathShared())
	n, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T, not an object: %v", projectpolicy.PathShared(), v, v)
	}
	switch ver := n["version"].(type) {
	case float64:
		return int(ver)
	case int:
		return ver
	case json.Number:
		i, err := ver.Int64()
		if err != nil {
			t.Fatalf("the node's version is not an integer: %v", err)
		}
		return int(i)
	default:
		t.Fatalf("the node's version is %T (%v)", n["version"], n["version"])
		return 0
	}
}

// v3Node is a node a release before the version-4 bump wrote: the model, with
// an allow-list the owner has edited (P4's un-enrolment is a removal) and a
// block no release of this CLI writes.
func v3Node(extra map[string]any) map[string]any {
	n := map[string]any{
		"version":     3,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app", "github/acme/hand-added"},
	}
	for k, v := range extra {
		n[k] = v
	}
	return n
}

// v3NodeMountingTheBitbucketToken is a version-3 node whose only profile
// mounts the project's Bitbucket credential: the node whose migration cannot
// state the block that credential is bounded by, and so the node that needs
// the local config (Migrate's own note says so).
func v3NodeMountingTheBitbucketToken() map[string]any {
	return map[string]any{
		"profiles": map[string]any{
			"standard": map[string]any{
				"provider": "bitbucket", "cpu": 1, "memory": "2Gi", "timeout_class": "6h",
				"allow_repos": []string{"bitbucket/workspace-a/repo-a"},
				"secrets":     []string{"bitbucket-token"},
			},
		},
	}
}

func TestUpdateMigratesAV3PolicyNodeAfterConfirmation(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})
	fakeTerminal(t)
	h.cmd.SetIn(strings.NewReader("aurora\n"))

	out, err := h.apply(t, false)
	if err != nil {
		t.Fatalf("the migration was refused: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the node is version %d, want %d", got, projectpolicy.Version)
	}
	if !strings.Contains(out, "published (version 3) and rewritten in place as version 4") {
		t.Errorf("the confirmation diff does not say what it rewrote and as what:\n%s", out)
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora)") {
		t.Errorf("the write was not confirmed:\n%s", out)
	}
	if p := publishedPolicy(t, f); !slices.Contains(p.AllowRepos, "github/acme/hand-added") {
		t.Errorf("the migration dropped the owner's own allow-list entry: %v", p.AllowRepos)
	}
}

// TestUpdateMigrationDoesNotWriteWithoutTheTypedName: the write is behind the
// same confirmation `publish --delete` puts it behind, and --yes is the flag
// of the same path (the in-flight refusal below is the one it does not cover).
func TestUpdateMigrationDoesNotWriteWithoutTheTypedName(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})
	fakeTerminal(t)
	h.cmd.SetIn(strings.NewReader("not-the-project\n"))

	out, err := h.apply(t, false)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("typing something else was accepted: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d on a refusal", got)
	}
}

func TestUpdatePolicyMigrationPreservesHandEditedAllowList(t *testing.T) {
	f := newProjectFixture(t, "")
	node := v3Node(nil)
	node["clone_urls"] = map[string]any{"github%2Facme%2Fapp": "https://github.com/acme/app.git"}
	f.db.Set(projectpolicy.PathShared(), node)
	h := newMigrationRig(t, f, &initOptions{})

	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the node is version %d, want %d", got, projectpolicy.Version)
	}
	p := publishedPolicy(t, f)
	if !slices.Contains(p.AllowRepos, "github/acme/hand-added") {
		t.Errorf("the owner's allow-list entry is gone: %v", p.AllowRepos)
	}
	if p.CloneURLs["github/acme/app"] != "https://github.com/acme/app.git" {
		t.Errorf("the clone_urls block no release writes is not carried over: %v", p.CloneURLs)
	}
}

func TestUpdatePolicyMigrationRefusesANewerNode(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version + 1, "budget": map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	h := newMigrationRig(t, f, &initOptions{})

	err := h.check()
	if err == nil {
		t.Fatal("a node written by a newer Fugaro was accepted")
	}
	// Migrate answers with the shared sentence alone; the remedy is the
	// caller's to word (projectpolicy.SkewSentence), and a refusal with no
	// move in it leaves the operator with nothing to do.
	for _, want := range []string{fmt.Sprint(projectpolicy.Version + 1), "upgrade Fugaro (fugaro upgrade)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version+1 {
		t.Errorf("the node moved to version %d", got)
	}
	out, err2 := h.apply(t, true)
	if err2 == nil {
		t.Errorf("the write went ahead over a newer node:\n%s", out)
	}
}

// TestUpdatePolicyMigrationRefusesAVersionNoMigratorReaches: a node below the
// chain's first step is one 0.8.0 or 0.8.5 wrote, and no step reaches it
// (projectpolicy.VersionSince). The migration cannot move it, and Migrate
// refuses it as the same older-than-this-reader skew a publish does — so the
// remedy the operator reads has to be the one that works there, not a bare
// sentence with no move.
func TestUpdatePolicyMigrationRefusesAVersionNoMigratorReaches(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": 1, "budget": map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	h := newMigrationRig(t, f, &initOptions{})

	err := h.check()
	if err == nil {
		t.Fatal("a version-1 node, which no migrator reaches, was accepted")
	}
	for _, want := range []string{"version 1", "fugaro project policy publish --delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := nodeVersionOn(t, f.db); got != 1 {
		t.Errorf("the node moved to version %d", got)
	}
	out, err2 := h.apply(t, true)
	if err2 == nil {
		t.Errorf("a node no migrator reaches was written over:\n%s", out)
	}
}

// TestUpdatePolicyMigrationSaysWhenNothingIsPublished: a project with a budget
// backend and no node in it is a state of its own, and both doors word it the
// way the delete and show paths do. "the published policy is version 4" would
// send the operator looking for a policy that is not there.
func TestUpdatePolicyMigrationSaysWhenNothingIsPublished(t *testing.T) {
	f := newProjectFixture(t, "")
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done || !strings.Contains(st.Detail, "no policy is published") {
		t.Fatalf("the check says %s %q, want the no-policy detail", st.State, st.Detail)
	}
	if strings.Contains(st.Detail, fmt.Sprintf("version %d", projectpolicy.Version)) {
		t.Errorf("the check claims a node that is not there: %q", st.Detail)
	}
	plan, err := h.s.Plan(context.Background(), initflow.Env{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NothingToDo || !strings.Contains(plan.Detail, "no policy is published") {
		t.Errorf("the plan says %+v, want the no-policy detail", plan)
	}
	if v := f.db.Value(projectpolicy.PathShared()); v != nil {
		t.Errorf("the stage published a node of its own: %v", v)
	}
}

func TestUpdatePolicyMigrationLeavesAMalformedNodeUntouched(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{"version": 0})
	h := newMigrationRig(t, f, &initOptions{})

	err := h.check()
	if err == nil {
		t.Fatal("a node with no version a release ever wrote was accepted")
	}
	if !strings.Contains(err.Error(), "its version is 0") {
		t.Errorf("the refusal does not say which version it read: %v", err)
	}
	if got := nodeVersionOn(t, f.db); got != 0 {
		t.Errorf("the node moved to version %d", got)
	}
	out, err2 := h.apply(t, true)
	if err2 == nil {
		t.Errorf("a malformed node was written over:\n%s", out)
	}
}

func TestUpdatePolicyMigrationSurfacesRunsInFlight(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
	h := newMigrationRig(t, f, &initOptions{})

	// --yes is the flag the in-flight refusal tells the operator to pass
	// once they have read what the migration does, exactly as --delete
	// does with it: without it, a run that read the node stops the write.
	out, err := h.apply(t, false)
	if err == nil {
		t.Fatalf("a run in flight did not stop the migration:\n%s", out)
	}
	if !strings.Contains(err.Error(), "run(s) are in flight") || !strings.Contains(err.Error(), "fugaro runs ls") {
		t.Errorf("the refusal is not the in-flight one: %v", err)
	}
	// The refusal is the whole of what the operator gets: the check is before
	// the diff (policyMigrationConfirm), which is what the upgrade step runs
	// too, so the two doors refuse one state in the same words and print the
	// same nothing — a diff shown for a write that does not happen is a
	// document the operator cannot act on without a second run.
	if strings.Contains(out, "rewritten in place as version") {
		t.Errorf("the diff was printed for a write that was refused:\n%s", out)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d while a run was in flight", got)
	}
}

// TestUpdatePolicyMigrationTakesYesWhileARunIsInFlight pins the other half of
// that refusal: --yes is the way past it, which is what the refusal's own words
// tell the operator to use, and the same reading `publish --delete` has. The
// two doors and their help text have to say this one way.
func TestUpdatePolicyMigrationTakesYesWhileARunIsInFlight(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("--yes did not confirm the migration a run in flight stops without it: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want %d", got, projectpolicy.Version)
	}
}

// TestUpdatePolicyMigrationVerifiesTheNodeItWrote: Verify is the read-back the
// write ends with, and the version is what it watches: a write that did not
// land, or a node another writer moved back under it, is a policy this Fugaro
// would keep rewriting on every run.
func TestUpdatePolicyMigrationVerifiesTheNodeItWrote(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := h.s.Verify(context.Background()); err != nil {
		t.Fatalf("the node the migration has just written does not verify: %v", err)
	}
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	err := h.s.Verify(context.Background())
	if err == nil {
		t.Fatal("a node back at version 3 verified")
	}
	if !strings.Contains(err.Error(), "still version 3") {
		t.Errorf("the refusal is not the version one: %v", err)
	}
}

// TestUpdatePolicyMigrationVerifyReadsTheNodeBack: the version alone is not the
// verification. Migrate short-circuits on a node already at this version, so a
// run of it over the bytes read back never reaches the reader gate — the gate
// it ran judged the document the walk was about to write, not what the database
// now holds. This node is version 4 and unreadable (the budget block is strict),
// which is the write a version-only check would have called verified.
func TestUpdatePolicyMigrationVerifyReadsTheNodeBack(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version, "budget": map[string]any{"mode": "enforce", "per_run_usd": 5, "no_such_field": 1},
		"allow_repos": []string{"github/acme/app"},
	})
	err := h.s.Verify(context.Background())
	if err == nil {
		t.Fatal("a node at this version that the reader refuses verified")
	}
	for _, want := range []string{"cannot be read back", "no_such_field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

func TestUpdatePolicyMigrationRefusedInAgentSession(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})
	t.Setenv("CLAUDECODE", "1")

	out, err := h.apply(t, true)
	var ae *initflow.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("a coding agent's session was allowed to write the policy: %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "coding agent") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d in an agent session", got)
	}
}

func TestUpdatePolicyMigrationETagConflictWritesNothing(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	// Another publish lands between the read the diff was made against and
	// the write: the node the operator read is not the node that is there,
	// so the write refuses rather than replacing it.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": 3, "budget": map[string]any{"mode": "enforce", "per_run_usd": 9},
		"allow_repos": []string{"github/acme/app"},
	})

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the migration overwrote a node that changed under it:\n%s", out)
	}
	if !strings.Contains(err.Error(), "changed while this migration was being confirmed") {
		t.Errorf("the refusal is not the ETag one: %v", err)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d", got)
	}
	raw, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "9") {
		t.Errorf("the write landed anyway, and the other writer's node is gone: %s", raw)
	}
}

func TestUpdateMigrationDerivesTheBitbucketBlockFromLocalConfig(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenConfig)
	f.db.Set(projectpolicy.PathShared(), v3Node(v3NodeMountingTheBitbucketToken()))
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("a version-3 node mounting the Bitbucket credential was not migrated: %v\n%s", err, out)
	}
	bt := publishedPolicy(t, f).BitbucketToken
	if bt == nil {
		t.Fatalf("the migrated node carries no bitbucket_token block, so nothing states what the token reaches:\n%s", out)
	}
	if want := []string{"bitbucket/workspace-a/repo-a"}; !slices.Equal(bt.AllowRepos, want) {
		t.Errorf("bitbucket_token.allow_repos = %v, want %v", bt.AllowRepos, want)
	}
	if !strings.Contains(out, "derived from the project's local config, as a publish derives them: bitbucket_token") {
		t.Errorf("the diff does not say what the local config supplied:\n%s", out)
	}
	// The republish Migrate asks for is answered here rather than passed on:
	// the block is in the node now, so a diff telling the operator to
	// republish would be a move that undoes the migration.
	if strings.Contains(out, "republish to derive the Bitbucket block") {
		t.Errorf("the diff asks for a republish the derivation just made unnecessary:\n%s", out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want %d", got, projectpolicy.Version)
	}
}

// TestUpdateMigrationCarriesBitbucketAPIRoots: the owner's API-root allow-list
// is the other member a publish copies into the node rather than derives, and
// the run reads it there — so a migrated node has to carry it, and the diff has
// to say where it came from. A node that states its own list keeps it: that
// list is the node's, and this machine's config is not its to replace.
func TestUpdateMigrationCarriesBitbucketAPIRoots(t *testing.T) {
	const roots = "bitbucket_api_roots: [https://api.bitbucket.example/2.0]\n"
	f := newProjectFixture(t, roots)
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{"https://api.bitbucket.example/2.0"}
	if got := publishedPolicy(t, f).BitbucketAPIRoots; !slices.Equal(got, want) {
		t.Errorf("bitbucket_api_roots = %v, want %v: the run sends the project's Bitbucket token to these roots, and the node is where it reads them", got, want)
	}
	if !strings.Contains(out, "derived from the project's local config, as a publish derives them: "+bitbucketAPIRootsKey) {
		t.Errorf("the diff does not say the local config supplied the allow-list:\n%s", out)
	}

	node := v3Node(nil)
	node[bitbucketAPIRootsKey] = []string{"https://api.bitbucket.other/2.0"}
	f = newProjectFixture(t, roots)
	f.db.Set(projectpolicy.PathShared(), node)
	h = newMigrationRig(t, f, &initOptions{})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := publishedPolicy(t, f).BitbucketAPIRoots; !slices.Equal(got, []string{"https://api.bitbucket.other/2.0"}) {
		t.Errorf("bitbucket_api_roots = %v: the migration replaced the node's own list with this machine's config", got)
	}
}

func TestUpdateMigrationKeepsHandEditedAllowListWhileDeriving(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenConfig)
	node := v3Node(v3NodeMountingTheBitbucketToken())
	node["allow_repos"] = []string{"github/acme/app", "github/acme/un-enrolled-on-purpose"}
	f.db.Set(projectpolicy.PathShared(), node)
	h := newMigrationRig(t, f, &initOptions{})

	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.BitbucketToken == nil {
		t.Fatal("the derived block is not in the migrated node")
	}
	if !slices.Contains(p.AllowRepos, "github/acme/un-enrolled-on-purpose") {
		t.Errorf("deriving the block rewrote the owner's allow-list: %v", p.AllowRepos)
	}
}

// TestUpdateMigrationDerivesTheBlockWhenNoProfileMountsTheToken: the block a
// publish derives is not a statement about a profile mounting the token — a
// proxied per-repository job holds no credential at all and reads the block as
// its whole admission — so a node with no profile mounting it still has to come
// out of the migration carrying it. Without the derivation the node migrates
// cleanly and the project's Bitbucket runs fail closed afterwards.
func TestUpdateMigrationDerivesTheBlockWhenNoProfileMountsTheToken(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenWithoutAProfile)
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.BitbucketToken == nil {
		t.Fatalf("the migrated node carries no bitbucket_token block, and this config derives one:\n%s", out)
	}
	if want := []string{"bitbucket/workspace-a/api-*", "bitbucket/workspace-a/repo-a"}; !slices.Equal(p.BitbucketToken.AllowRepos, want) {
		t.Errorf("bitbucket_token.allow_repos = %v, want %v", p.BitbucketToken.AllowRepos, want)
	}
}

// bitbucketTokenTheNodeCannotRead is a local config the loader accepts and the
// policy's own reader does not: config's allow_data_to rules cap neither the
// length of an entry nor a segment of it, while a policy's allow_repos entry is
// held to projectpolicy.MaxPatternLen — and the derivation is where the second
// rule is applied (DeriveBitbucketToken), which is the sentence a publish
// refuses this config with. The migration reads its config through the same
// function, so it is the fixture for "the two doors refuse one config the same
// way".
var bitbucketTokenTheNodeCannotRead = "project_secrets:\n  bitbucket-token: { allow_data_to: [workspace-a/" +
	strings.Repeat("r", projectpolicy.MaxPatternLen) + "] }\n"

// TestUpdateMigrationRefusesWhenBitbucketDerivationFails: the block this stage
// patches into the node is derived from the local config, and a config the
// derivation refuses is one a publish refuses too — the entry would become an
// allow_repos pattern the node's own decoder rejects. A migration that carried
// on would write the node without the block, which is the state a proxied
// run's whole admission is missing from: so the derivation's error is the
// write's refusal, whether or not Migrate had one of its own.
func TestUpdateMigrationRefusesWhenBitbucketDerivationFails(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenTheNodeCannotRead)
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	err := h.check()
	if err == nil {
		t.Fatal("a config whose Bitbucket block cannot be derived was accepted: the node migrates without the block a publish would write")
	}
	for _, want := range []string{"project_secrets.bitbucket-token.allow_data_to", "would publish the policy entry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d on a refusal", got)
	}
	if out, err := h.apply(t, true); err == nil {
		t.Errorf("the migration wrote a node the derivation could not complete:\n%s", out)
	}

	// The parity the refusal claims: a publish of the same config is refused
	// with the derivation's own sentence, so the operator reads one answer
	// about one config whichever door they came through.
	f2 := newProjectFixture(t, bitbucketTokenTheNodeCannotRead)
	_, _, perr := execute(t, "project", "policy", "publish")
	if perr == nil {
		t.Fatal("a publish of this config was allowed, so the migration's refusal is not the parity it claims")
	}
	if !strings.Contains(perr.Error(), "would publish the policy entry") {
		t.Errorf("the publish refuses with different words: %v", perr)
	}
	if v := f2.db.Value(projectpolicy.PathShared()); v != nil {
		t.Errorf("the publish wrote a node anyway: %v", v)
	}
}

// TestUpdateMigrationWithoutABitbucketCredentialDerivesNothing: a config that
// states no Bitbucket credential has nothing to derive from, so the migrated
// node carries no block and the diff claims none.
func TestUpdateMigrationWithoutABitbucketCredentialDerivesNothing(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "derived from the project's local config") {
		t.Errorf("the diff claims a derivation this config has nothing to derive from:\n%s", out)
	}
	raw, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{bitbucketTokenKey, bitbucketAPIRootsKey} {
		if _, found, _ := jsonMember(raw, key); found {
			t.Errorf("the migrated node carries %s, and this project's local config states none", key)
		}
	}
}

// currentPolicyNode is a node at the version this Fugaro writes: the state of
// every installation that already ran a fugaro update of this release.
func currentPolicyNode() map[string]any {
	return map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": "enforce", "per_run_usd": 5},
	}
}

// TestUpdateCurrentNodeWithUnderivableBitbucketDoesNotFail: a node already at
// this version is not migrated, so nothing is derived for it either, and the
// config's own refusal (an allow_data_to entry the policy's reader rejects,
// which is what stops a publish) has nothing to refuse here. Before this, the
// derivation ran for every node and its error failed the policy stage of every
// fugaro update of an installation whose node was current.
func TestUpdateCurrentNodeWithUnderivableBitbucketDoesNotFail(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenTheNodeCannotRead)
	f.db.Set(projectpolicy.PathShared(), currentPolicyNode())
	h := newMigrationRig(t, f, &initOptions{})

	if err := h.check(); err != nil {
		t.Fatalf("a node already at version %d failed the check over a config only a publish would refuse: %v", projectpolicy.Version, err)
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("a node already at version %d was not left alone: %v\n%s", projectpolicy.Version, err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node's version is %d, want %d", got, projectpolicy.Version)
	}
}

// TestUpdateMigrationRefusedOnOldRulesMarker: the write is the publisher's, so
// it holds the publisher's gate — a node written under rules that predate the
// policy model is a policy no run reads, and the migration refuses it with the
// publisher's own sentence rather than writing it. The node is left exactly as
// it was: the refusal is before the diff and the confirmation, so nobody
// confirms a document they do not get.
func TestUpdateMigrationRefusedOnOldRulesMarker(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	f.db.Set(projectpolicy.PathRulesMarker(), nil) // the rules a deploy that never ran left: no marker
	h := newMigrationRig(t, f, &initOptions{})

	err := h.check()
	if err == nil {
		t.Fatal("a policy node was migrated under rules that carry no policy marker: every run of the project would read a policy its rules cannot serve")
	}
	for _, want := range []string{"predate the policy model", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// And the move it names is the plain door: `fugaro update`'s rules stage
	// deploys this binary's generation over rules whose marker is absent, in the
	// same run as the migration, so sending the operator to --firebase-project
	// (the door that adopts a Firebase project) would be a command they need not
	// run (init_rules_stage.go).
	if strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("the refusal still sends the operator to --firebase-project, which fugaro update no longer needs: %v", err)
	}
	if out, err := h.apply(t, true); err == nil {
		t.Errorf("the migration wrote the node anyway:\n%s", out)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d on a refusal", got)
	}
}

// TestUpdateLeavesACurrentNodeAloneWhateverTheRulesAre: the gate is the
// migration's, not the stage's: a node already at this version is not written,
// so an installation whose rules are older than the policy model still
// converges (its rules are what fugaro update --firebase-project and doctor's
// policy-rules-skew are about, and neither is this stage's to fix).
func TestUpdateLeavesACurrentNodeAloneWhateverTheRulesAre(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), currentPolicyNode())
	f.db.Set(projectpolicy.PathRulesMarker(), nil)
	h := newMigrationRig(t, f, &initOptions{})

	if err := h.check(); err != nil {
		t.Fatalf("a node already at this version was refused over the deployed rules: %v", err)
	}
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node's version is %d, want %d", got, projectpolicy.Version)
	}
}

func TestUpdatePolicyMigrationRefusesOverUnknownFields(t *testing.T) {
	f := newProjectFixture(t, "")
	node := v3Node(nil)
	node["future_key"] = true
	f.db.Set(projectpolicy.PathShared(), node)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("a node carrying a key this Fugaro does not know was written over:\n%s", out)
	}
	for _, want := range []string{"1 key(s) this Fugaro does not know", "fugaro upgrade", "update again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "true") {
		t.Errorf("the refusal prints a value out of the node: %v", err)
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d", got)
	}
}

func TestPolicyPublishRefusesOverUnknownFields(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version, "budget": map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"}, "future_key": true,
	})
	_, _, err := execute(t, "project", "policy", "publish", "--yes")
	if err == nil {
		t.Fatal("the publish went ahead over a node carrying a key it does not know")
	}
	for _, want := range []string{"1 key(s) this Fugaro does not know", "publishing would drop them", "fugaro upgrade", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

// TestUpgradePolicyStepSkipsTheDatabaseTheRulesStageSkips is the second door of
// the same write: `fugaro upgrade`'s policy step reaches the migration with no
// rules stage in front of it at all (the step is last, and the rules stage is
// `fugaro update`'s), so the step is where a gate the rules stage holds and the
// migration does not is invisible. It answers the same two shapes as the update
// probe (unadoptedShapes, the config recording no Firebase project, and one
// recording the installation's own with a database that is not its own) through
// one shared predicate, and the probe is the same: every fact the write needs,
// and another installation's node exactly as it was.
func TestUpgradePolicyStepSkipsTheDatabaseTheRulesStageSkips(t *testing.T) {
	for _, s := range unadoptedShapes {
		t.Run(s.name, func(t *testing.T) {
			_, db, root := unadoptedRig(t, s)
			out := &syncBuf{}
			cmd := NewRootCmd()
			cmd.SetOut(out)
			cmd.SetErr(&syncBuf{})
			u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: root}}
			// --yes, or the step stops at the typed confirmation and the probe
			// never reaches the write it is about.
			u.o.yes = true

			r := policyStep(context.Background(), u)
			if r.state != stepSkipped {
				t.Fatalf("the policy step is %s, want skipped: %s", r.state, r.reason)
			}
			for _, want := range s.wants {
				if !strings.Contains(r.reason, want) {
					t.Errorf("the skip does not name %q: %q", want, r.reason)
				}
			}
			unadoptedAssertions(t, db)
		})
	}
}

// TestUpgradeRunsThePolicyMigration: the upgrade step is the same migration,
// in the same place in the sequence (the owner's rule of 2026-10-30: the
// whole upgrade is one command, whichever door the user is in).
func TestUpgradeRunsThePolicyMigration(t *testing.T) {
	names := make([]string, len(upgradeSteps))
	for i, s := range upgradeSteps {
		names[i] = s.name
	}
	if want := []string{"pin", "plugin", "cloud", "policy"}; !slices.Equal(names, want) {
		t.Fatalf("the steps are %v, want %v", names, want)
	}
	if got := reflect.ValueOf(upgradeSteps[3].run).Pointer(); got != reflect.ValueOf(policyStep).Pointer() {
		t.Error("the policy step is registered under some other function")
	}
}

// TestUpgradePolicyCheckMakesNoCloudCall: --check's own description, the
// command's help and the upgrade skill all promise it makes no cloud call and
// needs no credentials. The published node is not in the checkout — it is in
// the project's Realtime Database — so the step says it cannot tell instead of
// reading it. The read is denied on the fake for the length of this test and
// the stage's own check is run under the same denial, so the skip above is a
// skip and not a denial that failed quietly.
func TestUpgradePolicyCheckMakesNoCloudCall(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	f.db.DenyRead(projectpolicy.PathShared())
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: f.dir}}
	u.o.check = true

	r := policyStep(context.Background(), u)
	if r.state != stepSkipped {
		t.Fatalf("the step is %s, want skipped: %s", r.state, r.reason)
	}
	if !strings.Contains(out.String(), "makes no cloud call") || !strings.Contains(out.String(), "cannot tell") {
		t.Errorf("--check does not say it cannot tell:\n%s", out.String())
	}
	if got := nodeVersionOn(t, f.db); got != 3 {
		t.Errorf("the node moved to version %d under --check", got)
	}
	if err := newMigrationRig(t, f, &initOptions{}).check(); err == nil {
		t.Fatal("the denied read went through, so the skip above proves nothing")
	}
}

// TestUpgradePolicyStepSkipsWhereItCannotWrite: the step is skipped, not
// failed, in the two runs that never write the cloud here — and both say
// where the write happens instead.
func TestUpgradePolicyStepSkipsWhereItCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name, marker string
		local        bool
		want         string
	}{
		{"--local", "", true, "--local"},
		{"an agent session", "CLAUDECODE", false, "coding agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), v3Node(nil))
			out := &syncBuf{}
			cmd := NewRootCmd()
			cmd.SetOut(out)
			u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: f.dir},
				agent: agentMarker(func(string) string { return tc.marker })}
			u.o.local = tc.local
			r := policyStep(context.Background(), u)
			if r.state != stepSkipped {
				t.Fatalf("the step is %s, want skipped: %s", r.state, r.reason)
			}
			if !strings.Contains(r.reason, tc.want) {
				t.Errorf("the skip does not say why (%q): %s", tc.want, r.reason)
			}
			if got := nodeVersionOn(t, f.db); got != 3 {
				t.Errorf("the node moved to version %d", got)
			}
		})
	}
}
