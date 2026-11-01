package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
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

// staleBudgetNodeAt is that node with another ceiling: what another writer's
// publish left behind while this run was in flight.
func staleBudgetNodeAt(usd float64) map[string]any {
	n := staleBudgetNode()
	n["budget"] = map[string]any{"mode": "enforce", "per_run_usd": usd}
	return n
}

// nodeETag is the ETag the database holds the policy node at, read through a
// client of this test's own: the generation a run records is compared against
// this, and comparing it against the run's own read would pin nothing.
func nodeETag(t *testing.T, f *projectFixture) string {
	t.Helper()
	db, err := newBudgetClient(t.Context(), f.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	etag, found, _, err := readPolicyNode(t.Context(), db, lc)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	return etag
}

// blockBudgetConfig rewrites the fixture's budget block from the flow style
// projectConfigText writes into the block style one yamledit splice can reach
// into. It runs after the rig is built and before the stage plans anything, so
// the stage's own read of the config is the file this helper wrote.
func blockBudgetConfig(t *testing.T, f *projectFixture) {
	t.Helper()
	flow := "budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + f.db.URL + " }"
	block := "budget:\n  mode: enforce\n  per_run_usd: 5\n  rtdb_url: " + f.db.URL + "\n  firebase_project: proj-1234"
	setConfig(t, f, func(s string) string {
		if !strings.Contains(s, flow) {
			t.Fatalf("the fixture's budget block is not the one this helper rewrites:\n%s", s)
		}
		return strings.Replace(s, flow, block, 1)
	})
}

// nodeCeiling is the per-run ceiling the published node carries. The database
// hands numbers back as json.Number when the write came over HTTP and as
// float64 when the fake was seeded directly, and both shapes are read here.
func nodeCeiling(t *testing.T, f *projectFixture) float64 {
	t.Helper()
	n, ok := f.db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T (%v)", projectpolicy.PathShared(), f.db.Value(projectpolicy.PathShared()), f.db.Value(projectpolicy.PathShared()))
	}
	b, ok := n["budget"].(map[string]any)
	if !ok {
		t.Fatalf("the node's budget is %T (%v)", n["budget"], n["budget"])
	}
	switch v := b["per_run_usd"].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		usd, err := v.Float64()
		if err != nil {
			t.Fatalf("the node's budget.per_run_usd is not a number: %v", v)
		}
		return usd
	default:
		t.Fatalf("the node's budget.per_run_usd is %T (%v)", b["per_run_usd"], b["per_run_usd"])
		return 0
	}
}

// TestUpdatePolicyStageWritesNothingWhenTheNodeMatches: a node that carries
// what the local config states is left exactly as it is, down to the bytes —
// the stage is not a publisher that rewrites the document every run — and a
// file that already records the node's own generation is left as it is too.
// What the stage must not do is record nothing at all here: the record belongs
// to the agreement between the file and the node, so a stale or absent one is
// refreshed even when no node is written (see
// TestUpdatePolicyStageRecordsTheGenerationWhenTheNodeMatches).
func TestUpdatePolicyStageWritesNothingWhenTheNodeMatches(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	// The explicit door records the generation of its own write, so the update
	// stage meets a file and a node that agree, with the generation already
	// recorded: the reference the stage must leave alone.
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Fatalf("the publish recorded %q, want the node's own generation %q", gen, want)
	}
	// The rig writes the installation's firebase_project into the file, so the
	// reference for "the stage wrote nothing" is the file as the rig leaves it.
	h := newMigrationRig(t, f, &initOptions{})
	fileBefore, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}

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
	fileAfter, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(fileBefore) != string(fileAfter) {
		t.Errorf("a run with nothing to publish wrote the file:\nbefore %s\nafter  %s", fileBefore, fileAfter)
	}
	if gen := recordedGeneration(t, f); gen != nodeETag(t, f) {
		t.Errorf("the file records %q and the node is at %q: the run left the record disagreeing with the node", gen, nodeETag(t, f))
	}
}

// TestUpdatePolicyStageWritesUnderTheETag: the write carries the ETag the read
// returned, and it is held in flight here while another writer's node lands
// under it. A write that carried no precondition, or one made against an ETag
// read a second time on the way in, would overwrite a node it never read; this
// one is refused and the other writer's node stands. The hold is what makes the
// two writes overlap — without it the publish would simply have read a node
// that did not carry the change yet.
func TestUpdatePolicyStageWritesUnderTheETag(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}

	stamped, release := f.db.HoldNext(projectpolicy.PathShared())
	done := make(chan struct{})
	var out string
	var err error
	go func() {
		defer close(done)
		out, err = h.apply(t, true)
	}()
	<-stamped
	f.db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(7))
	release()
	<-done

	if err == nil {
		t.Fatalf("the publish overwrote a node that moved while its write was in flight:\n%s", out)
	}
	if !strings.Contains(err.Error(), "changed while this publish was being confirmed") {
		t.Errorf("the refusal is not the ETag one: %v", err)
	}
	raw, jerr := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if !strings.Contains(string(raw), "7") {
		t.Errorf("the other writer's node is gone: %s", raw)
	}
}

// TestUpdatePolicyStageRefusesWhenTheNodeMovedUnderIt: the same guard, without
// the overlap — the node another writer published between the read the diff was
// made against and this apply. Nothing is written, and the node that stands is
// the other writer's, because the ETag this run read is no longer the node's.
func TestUpdatePolicyStageRefusesWhenTheNodeMovedUnderIt(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(7))

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the publish overwrote a node that changed under it:\n%s", out)
	}
	if !strings.Contains(err.Error(), "changed while this publish was being confirmed") {
		t.Errorf("the refusal is not the ETag one, or it names the wrong write: %v", err)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want the version %d the other writer left", got, projectpolicy.Version)
	}
	if got := nodeCeiling(t, f); got != 7 {
		t.Errorf("the node's ceiling is %v: the other writer's value is gone", got)
	}
}

// TestUpdatePolicyStageRefusesWhenThePublishedGenerationIsNewer: D854. This
// machine's file records the generation its policy keys were written from, the
// node has moved past it — a second machine's publish, or the console — and the
// local config states something the node does not carry. Publishing from here
// would replace the newer node with an older file, so the stage refuses, prints
// the three-way state, and offers the re-seed instead: the file is written from
// the node, under the same confirmation, with a backup of what it replaced.
func TestUpdatePolicyStageRefusesWhenThePublishedGenerationIsNewer(t *testing.T) {
	f := newProjectFixture(t, "")
	setConfig(t, f, func(s string) string { return s + "policy_generation: etag-from-an-earlier-publish\n" })
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("a file behind the published node is %s %q, want the pending re-seed", st.State, st.Detail)
	}
	for _, want := range []string{"moved past the generation", "etag-from-an-earlier-publish"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the check does not name %q: %q", want, st.Detail)
		}
	}
	// The fixture's budget block is the flow style projectConfigText writes,
	// and a member inside a flow collection is one yamledit refuses to splice
	// (TestUpdatePolicyReseedRefusesAMemberItCannotSplice): the re-seed this
	// test is about is the one that lands.
	blockBudgetConfig(t, f)

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the refusal to publish over a newer node was not answerable: %v\n%s", err, out)
	}
	// The node is exactly as the other machine left it: nothing of this older
	// file was published.
	if got := nodeCeiling(t, f); got != 9 {
		t.Errorf("the node's ceiling is %v, want the other machine's 9", got)
	}
	// The file was re-seeded from it, under a backup of what it replaced.
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if lc.Budget == nil || lc.Budget.PerRunUSD != 9 {
		t.Errorf("the local config states %+v, want the published node's ceiling after the re-seed", lc.Budget)
	}
	backups, err := filepath.Glob(f.path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) == 0 {
		t.Errorf("the re-seed replaced %s with no backup beside it", f.path)
	}
	for _, want := range []string{"re-seeded", "budget.per_run_usd"} {
		if !strings.Contains(out, want) {
			t.Errorf("the re-seed does not say %q:\n%s", want, out)
		}
	}
	// The three-way state points the way a re-seed moves the values — the node's
	// into this machine's file. The publish's own arrow would tell the operator
	// the reverse, which is the one thing the re-seed exists to stop.
	if !strings.Contains(out, "(this machine) re-seeded from") {
		t.Errorf("the re-seed does not say which way its values move:\n%s", out)
	}
	if strings.Contains(out, "(this machine) -> ") {
		t.Errorf("the re-seed is worded as a publish:\n%s", out)
	}
	// And the file's policy keys are now written from the generation that is
	// published, so the next run has nothing to refuse or to do.
	gen := recordedGeneration(t, f)
	if gen != nodeETag(t, f) {
		t.Errorf("policy_generation = %q, want the ETag the node is at (%q)", gen, nodeETag(t, f))
	}
	h.s.forget()
	st, err = h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the run after the re-seed is %s %q, want nothing to do", st.State, st.Detail)
	}
}

// TestUpdatePolicyPublishRefusesANodeItDoesNotModel: P8 at the publish's own
// write. A node at this version that carries a top-level key this Fugaro knows
// nothing about is not one a publish may rewrite — the document it builds does
// not carry that member, so the write would drop it, and a newer Fugaro's
// policy is not this binary's to narrow. The refusal names the count of those
// keys and never a value, and nothing is written.
func TestUpdatePolicyPublishRefusesANodeItDoesNotModel(t *testing.T) {
	f := newProjectFixture(t, "")
	node := staleBudgetNode()
	node["future_block"] = map[string]any{"a": 1}
	f.db.Set(projectpolicy.PathShared(), node)
	h := newMigrationRig(t, f, &initOptions{})
	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the publish rewrote a node it does not model:\n%s", out)
	}
	for _, want := range []string{"1 key(s) this Fugaro does not know", "brew upgrade dimipaun/tap/fugaro", "update again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The keys are named — it is the values a refusal never carries, since a
	// node may hold anything under them — so the operator knows which block is
	// the newer Fugaro's.
	if !strings.Contains(err.Error(), "future_block") {
		t.Errorf("the refusal does not name the key: %v", err)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the node changed:\nbefore %s\nafter  %s", before, after)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("a refused publish recorded policy_generation = %q", gen)
	}
}

// TestUpdatePolicyReseedRefusesAMemberItCannotSplice: the re-seed writes the
// published node's values with yamledit, one splice per member, and a member
// the editor cannot place is one it refuses rather than rewrites around: a
// budget block written in flow style takes a scalar splice now, but a list the
// block does not hold yet would be a flow sequence inside a flow mapping, which
// stays refused. The refusal is the whole of
// what makes the record safe: a run that recorded the generation anyway would
// leave the older file in place with a baseline claiming it was written from
// the newer node, which is the overwrite D854 exists to prevent.
func TestUpdatePolicyReseedRefusesAMemberItCannotSplice(t *testing.T) {
	f := newProjectFixture(t, "")
	setConfig(t, f, func(s string) string { return s + "policy_generation: etag-from-an-earlier-publish\n" })
	node := staleBudgetNode()
	node["budget"] = map[string]any{"mode": "enforce", "per_run_usd": 9, "allowed_models": []string{"claude-sonnet-5-5"}}
	f.db.Set(projectpolicy.PathShared(), node)
	h := newMigrationRig(t, f, &initOptions{})
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the re-seed wrote a member it cannot splice:\n%s", out)
	}
	for _, want := range []string{"budget.allowed_models", "cannot be re-seeded", "the file was left as it is"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	after, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the file was written behind a refusal:\nbefore %s\nafter  %s", before, after)
	}
	if gen := recordedGeneration(t, f); gen != "etag-from-an-earlier-publish" {
		t.Errorf("policy_generation = %q: the refusal recorded a generation for a file it did not write", gen)
	}
	if got := nodeCeiling(t, f); got != 9 {
		t.Errorf("the node's ceiling is %v, want the other machine's 9: the publish was made anyway", got)
	}
	if backups, err := filepath.Glob(f.path + ".bak-*"); err != nil {
		t.Fatal(err)
	} else if len(backups) != 0 {
		t.Errorf("a refused re-seed left backups behind: %v", backups)
	}
}

// TestProjectPolicyPublishRecordsTheGenerationOfItsOwnWrite: the explicit door
// writes one node, so it keeps the record the stage keeps (D854), in the file it
// built that node from. The record is what the operator's next config edit is
// read against: a door that recorded nothing would refuse that edit against the
// node it had just written, and offer a re-seed as the way out that would revert
// the edit.
func TestProjectPolicyPublishRecordsTheGenerationOfItsOwnWrite(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the ETag of the node the door wrote (%q)", gen, want)
	}

	// The operator's next edit. The node the door wrote is the generation the
	// file is written from, so this is the publish it asks for — not the state
	// that refuses it and hands back a re-seed of the file over the edit. The
	// edit raises the ceiling, which is D846's direction, so it is typed.
	setConfig(t, f, func(s string) string { return strings.Replace(s, "per_run_usd: 5", "per_run_usd: 7", 1) })
	h := typedRig(t, f)
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "the local config states what the published policy does not carry") {
		t.Fatalf("the edit after the door's own publish is %s %q, want the publish it asks for", st.State, st.Detail)
	}
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the edit after the door's own publish was refused: %v\n%s", err, out)
	}
	if got := nodeCeiling(t, f); got != 7 {
		t.Errorf("the node's ceiling is %v, want the edit's 7", got)
	}
}

// reposDoorThenEdit is the three allow-list doors' one test: the node is
// published, the door edits the allow-list, one member of the config is edited,
// and the stage is asked what it makes of that edit. The edit changes one member
// of the config, so the answer must be the publish it asks for — not the
// generation refusal with a re-seed of the file over it, which is what a record
// left behind by the door leaves the operator with.
//
// The allow-list edit is a PutIfMatch of the whole node, so the node's
// generation — the ETag of the document, which is what the record holds — moves
// exactly as a publish's does.
func reposDoorThenEdit(t *testing.T, door func(t *testing.T, f *projectFixture)) {
	t.Helper()
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	door(t, f)
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Fatalf("policy_generation = %q, want the generation the allow-list edit wrote (%q)", gen, want)
	}

	setConfig(t, f, func(s string) string { return strings.Replace(s, "per_run_usd: 5", "per_run_usd: 7", 1) })
	h := typedRig(t, f)
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "the local config states what the published policy does not carry") {
		t.Fatalf("the edit after the allow-list write is %s %q, want the publish it asks for", st.State, st.Detail)
	}
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the edit after the allow-list write was refused: %v\n%s", err, out)
	}
	if got := nodeCeiling(t, f); got != 7 {
		t.Errorf("the node's ceiling is %v, want the edit's 7", got)
	}
}

func TestReposAddThenEditThenUpdatePublishes(t *testing.T) {
	reposDoorThenEdit(t, func(t *testing.T, f *projectFixture) {
		if _, _, err := execute(t, "project", "repos", "add", "github/acme/new"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReposRmThenEditThenUpdatePublishes(t *testing.T) {
	reposDoorThenEdit(t, func(t *testing.T, f *projectFixture) {
		typed(t)
		if _, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/app"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReposSyncThenEditThenUpdatePublishes(t *testing.T) {
	reposDoorThenEdit(t, func(t *testing.T, f *projectFixture) {
		typed(t)
		newSyncRig(t, "acme/app", "Acme/New")
		if _, _, err := executeStdin(t, "aurora\n", "project", "sync", "--github-app-id", "12345", "--github-app-key"); err != nil {
			t.Fatal(err)
		}
	})
}

// TestReposAddLeavesAStaleRecordStale is the other half of that rule: the door
// records the generation it wrote only when this machine's file was written from
// the one it wrote against. A file that already names another generation is one
// whose keys are older than the node — another machine published in between —
// and refreshing the record there would hide exactly what D854's refusal exists
// to catch: the operator's next edit would publish an older file over a newer
// node, with no question asked.
func TestReposAddLeavesAStaleRecordStale(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// Another machine's publish: the node moves to a generation this file does
	// not name.
	stale := recordedGeneration(t, f)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(9))
	if now := nodeETag(t, f); now == stale {
		t.Fatalf("the other machine's node carries the generation this file records (%q): nothing is stale in this fixture", now)
	}
	out, _, err := execute(t, "project", "repos", "add", "github/acme/new")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if gen := recordedGeneration(t, f); gen != stale {
		t.Errorf("policy_generation = %q, want the stale %q left as it is: a record written here would hide the other machine's publish from the next update", gen, stale)
	}

	// And the edit after it is the refusal D854 answers, not a publish over the
	// newer node.
	setConfig(t, f, func(s string) string { return strings.Replace(s, "per_run_usd: 5", "per_run_usd: 7", 1) })
	h := newMigrationRig(t, f, &initOptions{})
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "moved past the generation") {
		t.Fatalf("the edit after the stale record is %s %q, want the generation refusal", st.State, st.Detail)
	}
	// With the flow splice the re-seed can run under --yes: it is a write to the
	// local file (with a backup), never to the node, so what this pins is the
	// node, whatever the re-seed does to the file.
	_, _ = h.apply(t, true)
	if got := nodeCeiling(t, f); got != 9 {
		t.Errorf("the node's ceiling is %v, want the other machine's 9: the file's edit was published over it", got)
	}
}

// TestProjectPolicyPublishSaysTheWriteLandedWhenItCannotRecord: the record is
// made after the write, so every way the record can fail is a way that leaves
// the node moved. The door reports the failure — the operator has to put the two
// back in step — and the failure has to carry that fact: a sentence that read as
// a publish that failed would send them looking for a write that is already
// there, and the file's own next edit is the one that pays for it. The read-back
// is the read that fails here: it is denied while it is in flight, which is the
// shape of a database that stops answering between the two requests.
func TestProjectPolicyPublishSaysTheWriteLandedWhenItCannotRecord(t *testing.T) {
	f := newProjectFixture(t, "")
	// Three requests to the node, in order: the publish's own read, the write,
	// and the read the generation is recorded from.
	stampedRead, releaseRead := f.db.HoldNext(projectpolicy.PathShared())
	stampedWrite, releaseWrite := f.db.HoldNext(projectpolicy.PathShared())
	stampedRecord, releaseRecord := f.db.HoldNext(projectpolicy.PathShared())
	done := make(chan struct{})
	var out string
	var err error
	go func() {
		defer close(done)
		out, _, err = execute(t, "project", "policy", "publish")
	}()
	<-stampedRead
	releaseRead()
	<-stampedWrite
	releaseWrite()
	<-stampedRecord
	f.db.DenyRead(projectpolicy.PathShared())
	releaseRecord()
	<-done

	if err == nil {
		t.Fatalf("the publish claimed a record it could not make:\n%s", out)
	}
	if !strings.Contains(err.Error(), "was written, but the generation to record") {
		t.Errorf("the failure does not say the node was written: %v", err)
	}
	if !strings.Contains(out, "set "+projectpolicy.PathShared()) {
		t.Errorf("the door does not show the write that landed:\n%s", out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the door's 5: the write this failure is about did not land", got)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("policy_generation = %q: a generation was recorded for a read that failed", gen)
	}
}

// TestUpdatePolicyStageRefreshesAStaleRecordWhenTheNodeMatches: the record
// belongs to the agreement between the file and the node, not to this run's own
// write. A file whose policy keys are exactly what the node carries, whose
// record names another generation — an earlier release's migration-only write,
// the explicit door's publish, a hand-fixed re-seed refusal — is the state that
// refuses the operator's next legitimate edit and offers a re-seed of the file
// over it. So the run refreshes the record, writes no node, and every other byte
// of the file stays where it is.
func TestUpdatePolicyStageRefreshesAStaleRecordWhenTheNodeMatches(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	gen := recordedGeneration(t, f)
	if gen == "" {
		t.Fatal("the door recorded no generation, so this test is not about a stale record")
	}
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "policy_generation: "+gen, "policy_generation: etag-from-an-earlier-publish", 1)
	})
	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("a file whose record names another generation is %s %q, want the record it asks for", st.State, st.Detail)
	}
	for _, want := range []string{"carries what the local config states", "etag-from-an-earlier-publish", nodeETag(t, f)} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the check does not say %q: %q", want, st.Detail)
		}
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the record was not refreshed: %v\n%s", err, out)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the generation of the node it agrees with (%q)", gen, want)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the node was written for a record that belongs to the file:\nbefore %s\nafter  %s", before, after)
	}
	if len(out) == 0 || !strings.Contains(out, "recorded the published policy node's generation") {
		t.Errorf("the run does not say what it recorded:\n%s", out)
	}
	if backups, err := filepath.Glob(f.path + ".bak-*"); err != nil {
		t.Fatal(err)
	} else if len(backups) == 0 {
		t.Errorf("the record replaced %s with no backup beside it", f.path)
	}

	h.s.forget()
	st, err = h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the run after the refresh is %s %q, want nothing to do", st.State, st.Detail)
	}
}

// TestUpdatePolicyRecordsTheGenerationOfAMigrationOnlyWrite: the migration
// writes the same node the publish writes, so the record follows it too. A run
// that recorded only when it published would leave the file behind the node it
// had just migrated, and the operator's next edit refused against it.
func TestUpdatePolicyRecordsTheGenerationOfAMigrationOnlyWrite(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	if gen := recordedGeneration(t, f); gen != "" {
		t.Fatalf("the fixture records a generation (%q) and this test is about a file that records none", gen)
	}
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Detail, "is version 3 and this Fugaro writes version") {
		t.Fatalf("the check is %q, want the migration's own line: this test is about a run that publishes nothing", st.Detail)
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the node is version %d, want %d", got, projectpolicy.Version)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the generation of the node this run migrated to (%q)", gen, want)
	}
}

// TestUpdatePolicyReseedWithAMigrationRecordsTheGenerationItWrote: D854's
// re-seed and a pending migration are one run, and the record belongs to the
// node the run ends at. A run that recorded the generation the file was
// re-seeded from would leave the file naming the node as it was before its own
// write — and the next edit refused against the node this very run wrote.
func TestUpdatePolicyReseedWithAMigrationRecordsTheGenerationItWrote(t *testing.T) {
	f := newProjectFixture(t, "")
	setConfig(t, f, func(s string) string { return s + "policy_generation: etag-from-an-earlier-publish\n" })
	f.db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
		"budget": map[string]any{"mode": "enforce", "per_run_usd": 9},
	}))
	h := newMigrationRig(t, f, &initOptions{})
	// The fixture's budget block is the flow style projectConfigText writes, and
	// a member inside a flow collection is one yamledit refuses to splice: the
	// re-seed this test is about is the one that lands.
	blockBudgetConfig(t, f)

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "moved past the generation") {
		t.Fatalf("a file behind a node that is also older than this version is %s %q, want the re-seed it asks for", st.State, st.Detail)
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the re-seed and the migration did not finish: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the node is version %d, want %d: the migration beside the re-seed never ran", got, projectpolicy.Version)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the generation of the node this run wrote (%q)", gen, want)
	}

	// The operator's next edit is read against the node this run wrote: the file
	// and this node agree, and the node has a new ETag, so nothing may refuse it.
	h.s.forget()
	st, err = h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the run after the re-seed and the migration is %s %q, want nothing to do", st.State, st.Detail)
	}
}

// TestUpdatePolicyStageAdoptsTheGenerationWhenNothingIsRecorded: the other half
// of D854, and the state of every installation before its first publish of this
// release — a file that records no generation has no baseline to compare
// against, so refusing would ask a question with no information in it. The run
// publishes, and records the generation it read as the one the file it wrote is
// now from.
func TestUpdatePolicyStageAdoptsTheGenerationWhenNothingIsRecorded(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	if gen := recordedGeneration(t, f); gen != "" {
		t.Fatalf("the fixture records a generation (%q) and this test is about a file that records none", gen)
	}

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("a file with no baseline refused to adopt the published node: %v\n%s", err, out)
	}
	// The adoption is said: it is the answer to the question a reader of the
	// file has afterwards — where the generation that appeared in it came from.
	if !strings.Contains(out, "adopts the one it publishes") {
		t.Errorf("the run does not say it adopted the published node's generation:\n%s", out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the local config's 5", got)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the ETag the published node is at (%q)", gen, want)
	}

	// A second run: the file's keys are what the node carries and its record is
	// the node's own generation, so there is nothing to publish again.
	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	h.s.forget()
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the run after the adoption is %s %q, want nothing to do", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the second run rewrote the node:\nbefore %s\nafter  %s", before, after)
	}
}

// TestUpdatePolicyStageAppliesNothingInAnAgentSessionEvenWithYes: the publish
// half of the refusal the migration half already holds
// (TestUpdatePolicyMigrationRefusedInAgentSession), and the half a scripted
// session would reach first: a config edit with a node that carries the old
// value needs no migration at all. --yes is the flag a script passes, and it is
// not a way past this: the write is the operator's, and the file's own record of
// the generation is not written either.
func TestUpdatePolicyStageAppliesNothingInAnAgentSessionEvenWithYes(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	t.Setenv("CLAUDECODE", "1")

	before, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	var ae *initflow.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("a coding agent's session published the policy with --yes: %v\n%s", err, out)
	}
	if ae.Marker == "" || !strings.Contains(err.Error(), "coding agent") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the node was written in a coding agent's session:\nbefore %s\nafter  %s", before, after)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("a coding agent's session recorded policy_generation = %q", gen)
	}
}

// TestUpdateStageAndTheExplicitDoorBuildTheSameNode: the stage and
// `fugaro project policy publish` write one node from one local config, byte for
// byte. They are two doors onto the same build (policyNodeForPublish), and a
// second build path appearing later is a node whose shape depends on which door
// the operator came through.
func TestUpdateStageAndTheExplicitDoorBuildTheSameNode(t *testing.T) {
	stage := newProjectFixture(t, "")
	stage.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, stage, &initOptions{})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("the stage refused to publish: %v\n%s", err, out)
	}

	door := newProjectFixture(t, "")
	door.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}

	byStage := nodeJSON(t, stage, projectpolicy.PathShared())
	byDoor := nodeJSON(t, door, projectpolicy.PathShared())
	if byStage != byDoor {
		t.Errorf("the two doors built two nodes from one config:\nupdate   %s\npublish  %s", byStage, byDoor)
	}
}

// TestInstallationAtThePreviousVersionConvergesPolicyStage: the HARD RULE's own
// test for this stage. An installation whose node predates this release ends the
// update with it at this version, carrying what the local config states, and the
// run after that writes nothing at all: `fugaro update` is the whole upgrade,
// and running it again is not a second repair. The node here has both halves
// pending — the version move and a member the config states otherwise — and they
// are one write.
func TestInstallationAtThePreviousVersionConvergesPolicyStage(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
		"budget": map[string]any{"mode": "enforce", "per_run_usd": 9},
	}))
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("an installation at the previous version did not converge: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the node is version %d after the update, want %d", got, projectpolicy.Version)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the local config's 5", got)
	}
	// The allow-list the node carried is the node's own (P4): the migration and
	// the publish are not a re-seed of it.
	p := publishedPolicy(t, f)
	if want := []string{"github/acme/app", "github/acme/hand-added"}; len(p.AllowRepos) != len(want) {
		t.Errorf("allow_repos = %v, want the node's own list %v", p.AllowRepos, want)
	}
	if gen := recordedGeneration(t, f); gen != nodeETag(t, f) {
		t.Errorf("policy_generation = %q, want the ETag the node is at (%q)", gen, nodeETag(t, f))
	}

	settled, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	h.s.forget()
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the converged installation is %s %q, want nothing to do", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(settled) != string(after) {
		t.Errorf("a second update rewrote the converged node:\nbefore %s\nafter  %s", settled, after)
	}
}

// convergeJSONOf reads the --json result out of a run's whole stdout. The
// document is the tail of it (initRun.printResult writes it last, indented two
// spaces) and the stages print their own diffs ahead of it, so decoding the
// stream from its first byte makes a stage that printed where it should have
// skipped fail as malformed JSON instead of as the state it is — which is the
// assertion this helper exists for.
func convergeJSONOf(t *testing.T, out string) convergeJSON {
	t.Helper()
	start := 0
	if i := strings.LastIndex(out, "\n{"); i >= 0 {
		start = i + 1
	} else if !strings.HasPrefix(out, "{") {
		t.Fatalf("the run printed no JSON result:\n%s", out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
		t.Fatalf("the tail of the run's output is not the JSON result (%v):\n%s", err, out)
	}
	return res
}

// TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips: the publish half
// holds #1460's adoption gate, and it holds the same one the migration half
// does. The node here is already at this version and the local config states
// something it does not carry, so nothing but the publish would write: a
// publish that opened a path of its own — its own gate, its own read — is the
// second writer #1460's review found, and this is the database it would write
// into. The other half of the same test is policyNodeUnreachable: a project
// whose config names no budget backend at all has no node to read and none to
// publish, and the stage skips rather than failing the run.
func TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips(t *testing.T) {
	for _, s := range unadoptedShapes {
		t.Run(s.name, func(t *testing.T) {
			_, db, _ := unadoptedRig(t, s)
			db.Set(projectpolicy.PathShared(), staleBudgetNode())

			out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
			if err != nil {
				t.Fatalf("a database the pair does not prove is ours stopped the whole update: %v\n%s%s", err, out, errOut)
			}
			res := convergeJSONOf(t, out)
			if got := res.state(initflow.Policy); got != "skipped" {
				t.Errorf("the policy stage is %q, want skipped: the publish wrote the node into a database this installation never adopted\n%s", got, out)
			}
			detail := res.stageDetail(t, initflow.Policy)
			for _, want := range s.wants {
				if !strings.Contains(detail, want) {
					t.Errorf("the skip does not name %q: %q", want, detail)
				}
			}
			unadoptedPublishAssertions(t, db)
		})
	}

	t.Run("no budget backend at all", func(t *testing.T) {
		f := newProjectFixture(t, "")
		setConfig(t, f, func(s string) string {
			return strings.Replace(s, "budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+f.db.URL+" }\n", "", 1)
		})
		h := newMigrationRig(t, f, &initOptions{})

		st, err := h.s.Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if st.State != initflow.Skipped || st.Detail != policyNoDBReason {
			t.Errorf("a project with no budget backend is %s %q, want the skip of a project with no node to read", st.State, st.Detail)
		}
		if _, err := h.apply(t, true); err != nil {
			t.Errorf("the skip is an error: %v", err)
		}
		if v := f.db.Value(projectpolicy.PathShared()); v != nil {
			t.Errorf("a publish wrote %v into a database the config does not name", v)
		}
	})
}

// unadoptedPublishAssertions is what unadoptedAssertions asserts for the state
// the publish half is about: the node is at this version and carries the other
// installation's ceiling, and neither it nor the mark moved. The node is read
// member by member rather than by version alone, because a publish that landed
// leaves the version where it is — only the members it writes would say so.
func unadoptedPublishAssertions(t *testing.T, db *gcpfake.RTDB) {
	t.Helper()
	node, ok := db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is gone: %v", projectpolicy.PathShared(), db.Value(projectpolicy.PathShared()))
	}
	if got := nodeVersionOn(t, db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want the %d the other installation left", got, projectpolicy.Version)
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "9") {
		t.Errorf("the publish wrote this machine's ceiling into a database this installation never adopted: %s", raw)
	}
	if repos, _ := node["allow_repos"].([]any); len(repos) != 2 {
		t.Errorf("another installation's allow-list was rewritten: %s", raw)
	}
	if got := db.Value(budget.PathMark); got == nil || got.(map[string]any)["project"] != "workspace-a" {
		t.Errorf("another installation's mark moved: %v", got)
	}
}

// looserPublishFixture is the state D846's second door is about: the node is
// published at a ceiling the local config now states a higher one for, so the
// publish this run would make raises what every run of the project is admitted
// by. The file's own edit is written the way an operator writes it — the config
// text, not the node — so the drift the stage reads is the one a real edit
// leaves.
func looserPublishFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	setConfig(t, f, func(s string) string { return strings.Replace(s, "per_run_usd: 5", "per_run_usd: 7", 1) })
	f.db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(5))
	return f
}

// typedRig is newMigrationRig with an operator at a terminal who types the
// project's name: the confirmation a publish that loosens the node asks for, and
// the one --yes never stands in for (D846).
func typedRig(t *testing.T, f *projectFixture) *migrationRig {
	t.Helper()
	typed(t)
	h := newMigrationRig(t, f, &initOptions{})
	h.cmd.SetIn(strings.NewReader("aurora\n"))
	return h
}

// TestUpdatePublishLooserNodeNeedsTheTypedName: D846 reaches the second door
// that writes a ceiling. The node is published at 5 and the local config states
// 7, so this publish raises what every run of the project is admitted by —
// exactly the widening `settings set` reserves for the project's own hand — and
// --yes alone does not confirm it. The name typed at a terminal does.
func TestUpdatePublishLooserNodeNeedsTheTypedName(t *testing.T) {
	f := looserPublishFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if out, err := h.apply(t, true); err == nil {
		t.Fatalf("--yes alone published a node looser than the published one:\n%s", out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Fatalf("the node's ceiling is %v, want the published 5: the loosening was written behind the refusal", got)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("policy_generation = %q, and the refused publish recorded a generation for a file it did not write", gen)
	}

	h = typedRig(t, f)
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the publish: %v\n%s", err, out)
	}
	if got := nodeCeiling(t, f); got != 7 {
		t.Errorf("the node's ceiling is %v, want the typed 7", got)
	}
}

// TestUpdatePublishLooserNodeRefusesYesAlone: the refusal is what an operator
// running the one upgrade command from a script — or an agent's session — reads
// instead of the write, so it names the member that loosens and the command that
// reaches the same publish at a terminal. A refusal that named neither would
// leave them with the loosening unapplied and no move.
func TestUpdatePublishLooserNodeRefusesYesAlone(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := looserPublishFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone published a node looser than the published one:\n%s", out)
	}
	for _, want := range []string{"budget.per_run_usd", "5", "7", "fugaro update", "needs a real terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if !strings.Contains(out, "budget.per_run_usd") {
		t.Errorf("the diff the refusal stands on does not name the member that loosens:\n%s", out)
	}
	if v := f.db.Value(projectpolicy.PathShared()); v == nil {
		t.Fatal("the node is gone")
	} else if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the published 5: the loosening was written", got)
	}
}

// TestUpdatePublishTighterNodeKeepsYes: the safe direction keeps the
// confirmation it has. The node is published at 9 and the local config states 5,
// so the publish lowers a ceiling every run of the project is admitted by — the
// ordinary case this stage exists for, and one --yes confirms as it always did.
func TestUpdatePublishTighterNodeKeepsYes(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("--yes no longer confirms a publish that lowers a ceiling: %v\n%s", err, out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the config's 5", got)
	}
}

// TestUpdatePublishUnchangedNodeAsksNothingNew: a node that already carries what
// the local config states raises nothing and removes nothing, so the run that
// only records its generation asks for no confirmation at all — and the member
// the typed gate reads is not reached, because there is no publish to read it
// from.
func TestUpdatePublishUnchangedNodeAsksNothingNew(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The record is made stale, so this run has the one write it can make — the
	// generation record in this machine's file — and no publish at all.
	gen := recordedGeneration(t, f)
	if gen == "" {
		t.Fatal("the door recorded no generation, so this test is not about an unchanged node meeting the stage")
	}
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "policy_generation: "+gen, "policy_generation: etag-from-an-earlier-publish", 1)
	})
	h := newMigrationRig(t, f, &initOptions{})
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "carries what the local config states") {
		t.Fatalf("an unchanged node with a stale record is %s %q, want the record-only run", st.State, st.Detail)
	}
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the record-only run was refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "CONFIRM") {
		t.Errorf("a run with nothing to publish asked for a confirmation:\n%s", out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the untouched 5", got)
	}
}
