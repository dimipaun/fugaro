package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
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

// nodeCeiling is the per-run ceiling the published node carries.
func nodeCeiling(t *testing.T, f *projectFixture) float64 {
	t.Helper()
	return ceilingOn(t, f.db)
}

// accountMembersOn is the published node's account-choice members as the
// database stores the document: the project's default, and the stage accounts by
// stage. A member the node carries nothing for is left out, which is what a
// refusal that wrote nothing reads as.
func accountMembersOn(t *testing.T, db *gcpfake.RTDB) (string, map[string]string) {
	t.Helper()
	n, ok := db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T (%v)", projectpolicy.PathShared(), db.Value(projectpolicy.PathShared()), db.Value(projectpolicy.PathShared()))
	}
	account, _ := n["default_claude_account"].(string)
	stages := map[string]string{}
	if raw, ok := n["claude_account_stages"].(map[string]any); ok {
		for stage, v := range raw {
			if name, ok := v.(string); ok {
				stages[stage] = name
			}
		}
	}
	return account, stages
}

// ceilingOn is nodeCeiling for a database held without a fixture — the upgrade
// door's tests hold the fake itself. The database hands numbers back as
// json.Number when the write came over HTTP and as float64 when the fake was
// seeded directly, and both shapes are read here.
func ceilingOn(t *testing.T, db *gcpfake.RTDB) float64 {
	t.Helper()
	n, ok := db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T (%v)", projectpolicy.PathShared(), db.Value(projectpolicy.PathShared()), db.Value(projectpolicy.PathShared()))
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

// accountChoiceConfigYAML is the account-choice keys as this machine's own file
// writes them: one account, named for the project and for the review stage
// (docs/design/mixed-credentials.md §11, rungs 2 and 4). The account is defined
// here because a key that names an account this file does not define is refused
// at load, and no profile names it because the keys are what the runner reads
// it off and not a profile's list.
const accountChoiceConfigYAML = "claude_accounts:\n  team-b:\n    allow_data_to: [acme/app]\n" +
	"default_claude_account: team-b\n" +
	"stages:\n  review:\n    claude_account: team-b\n"

// accountChoiceFixture is the installation B7 is about: the published node is
// what this machine's config stated BEFORE the keys were set, built by the
// publisher's own builder from that config, so the account members are the only
// difference between the two documents — and the file now carries both keys. A
// node current in every other member is what makes the stage's own comparison
// the only thing that can see the gap: without it, a budget ceiling would carry
// the publish and the test would pass whatever the account members did.
func accountChoiceFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := policyNodeForPublish(t.Context(), lc, nil, nil, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathShared(), node)
	rewriteProjectConfig(t, f, accountChoiceConfigYAML)
	return f
}

// TestUpdateConvergesANodeThatLacksTheAccountChoiceKeys is B7's own test and
// the owner's rule of 2026-10-30 (HARD RULE, CLAUDE.md) at this state: the node
// at the shared path carries no account member, this machine's file sets both
// keys, and one `fugaro update` has to converge it — the runner reads those
// keys off the node and nothing else (resolveMixedStages, mixedDefaultAccount),
// and a second command is the operator step the rule forbids.
//
// Red before the drift comparison: the stage found nothing to do with a node
// that is current in every other member, so the keys stayed local — a launch
// that admits the mix and a job that mounts the token beside a runner that
// never sees the account and serves the review stage on the gateway.
//
// The confirmation is the class of the widening it is, so the run is at the
// operator's own terminal with the project's name typed, and the second half of
// the test is the convergence itself: the next run reads the node, finds the
// keys and has nothing left to do.
func TestUpdateConvergesANodeThatLacksTheAccountChoiceKeys(t *testing.T) {
	f := accountChoiceFixture(t)
	h := typedRig(t, f)
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the stage says %s %q, want the publish these keys are the reason for", st.State, st.Detail)
	}
	for _, member := range []string{"default_claude_account", "claude_account_stages.review"} {
		if !strings.Contains(st.Detail, member) {
			t.Errorf("the check line does not name %s, the member that is the whole reason for this publish: %s", member, st.Detail)
		}
	}
	out, err := h.apply(t, false)
	if err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the publish: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.DefaultClaudeAccount != "team-b" || p.ClaudeAccountStages["review"] != "team-b" {
		t.Errorf("the published node states default_claude_account %q and review %q, want the config's team-b: the runner reads the mix off nothing else",
			p.DefaultClaudeAccount, p.ClaudeAccountStages["review"])
	}

	// The convergence: a run of the same command afterwards reads a node that
	// carries what the file states and has nothing to do.
	next := newMigrationRig(t, f, &initOptions{})
	nextSt, err := next.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if nextSt.State != initflow.Done {
		t.Errorf("the run after the publish says %s %q, want the stage done: the installation is not converged", nextSt.State, nextSt.Detail)
	}
}

// TestUpdateTakesTheStageOffAnAccountTheFileNoLongerNames is the other
// direction of the same comparison, and it is carried because the publisher
// copies these members from the file rather than keeping the node's: a node that
// names an account this machine's file is silent about is a node no run of the
// project states, and the publish that follows is what takes the stage off it.
// The removal is the tightening direction, so --yes confirms it as it confirms
// any other publish that narrows.
func TestUpdateTakesTheStageOffAnAccountTheFileNoLongerNames(t *testing.T) {
	f := accountChoiceFixture(t)
	h := typedRig(t, f)
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the publish was refused: %v\n%s", err, out)
	}
	// This machine's file is rewritten without the keys, the way another
	// machine's file that never had them reads: the node is the source the
	// config no longer states, and the publish that follows takes the stage off
	// the account rather than leaving the node and the file disagreeing.
	rewriteProjectConfig(t, f, "")
	back := newMigrationRig(t, f, &initOptions{})
	if err := back.check(); err != nil {
		t.Fatal(err)
	}
	// The removal direction asks for no typed name — it takes a credential
	// away — so --yes carries it, and the node it writes names no account.
	if out, err := back.apply(t, true); err != nil {
		t.Fatalf("--yes did not confirm a publish that drops the account members: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.DefaultClaudeAccount != "" || len(p.ClaudeAccountStages) != 0 {
		t.Errorf("the published node still names %q / %v and the file names none", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}
}

// TestUpdatePublishAccountKeysRefuseYesAlone: setting an account for the
// project is a widening of who holds the review credential, so the publish
// takes the stage's Typed class (D846) and --yes alone does not stand in for
// the name — the sentence names the member and the account it would set.
func TestUpdatePublishAccountKeysRefuseYesAlone(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := accountChoiceFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone published the accounts the project's review credential goes out on:\n%s", out)
	}
	for _, want := range []string{"default_claude_account", "claude_account_stages.review", "(not set)", "team-b", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if p := publishedPolicy(t, f); p.DefaultClaudeAccount != "" || len(p.ClaudeAccountStages) != 0 {
		t.Errorf("the refused publish wrote the keys anyway: %q / %v", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}
}

// TestPolicyLooseningCoversEveryCeiling is the comparison itself, member by
// member. Each kind of ceiling is one member and each way of loosening one is a
// case, so a member dropped from the list and a direction the comparison stops
// seeing fail here rather than in a scenario that only ever raises one number.
//
// The direction is the point in both halves. 0 is the value that sets no cap at
// all, so a ceiling taken to 0 loosens whatever it replaces and a cap put back
// over 0 tightens; a model list is widened by growing and never by the same
// members in another order; and the same node loosens nothing. The whole entry
// is asserted, not just the key, because the removal case is a member whose new
// value the node states nothing for — a spelling nothing else in the node
// carries.
//
// The orchestrator's guardrails are ceilings of the same rule (§4.1 rule 6, "a
// command that would raise a cap is refused, whichever command it is"), so they
// are members here too, with the two differences the block's own semantics
// bring: a node that states no key is held to the shipped guardrail rather than
// to no cap (WithDefaults, so 60 a minute over an unstated rate is a raise), and
// a class's model tightens by moving UP the tier order, which is the one member
// a larger number does not make looser.
func TestPolicyLooseningCoversEveryCeiling(t *testing.T) {
	node := func(mode string, usd float64, tokens int64, models ...string) *projectpolicy.Policy {
		return &projectpolicy.Policy{Budget: projectpolicy.Budget{
			Mode: mode, PerRunUSD: usd, MaxRunTokens: tokens, AllowedModels: models,
		}}
	}
	guardrails := func(o *policy.Orchestrator) *projectpolicy.Policy {
		// A budget block that names nothing but the mode, and no caps of its
		// own (0 is "no cap at all"), so that a case about the orchestrator
		// block is a case about that block and never about the four members
		// beside it: the pair below compares like with like, and the one case
		// whose other side is `node("enforce", 0, 0)` spells them the same way.
		return &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: "enforce"}, Orchestrator: o}
	}
	for _, tc := range []struct {
		name     string
		old, new *projectpolicy.Policy
		want     []string
	}{
		{"a weaker mode", node("enforce", 5, 100, "m"), node("observe", 5, 100, "m"),
			[]string{"budget.mode enforce -> observe"}},
		{"the mode removed", node("enforce", 5, 100, "m"), node("", 5, 100, "m"),
			[]string{"budget.mode enforce -> (removed)"}},
		{"the mode raised to enforce, which tightens", node("observe", 5, 100, "m"), node("enforce", 5, 100, "m"), nil},
		{"the per-run ceiling raised", node("enforce", 5, 100, "m"), node("enforce", 7, 100, "m"),
			[]string{"budget.per_run_usd 5 -> 7"}},
		{"the per-run ceiling taken to 0, which is no cap at all", node("enforce", 5, 100, "m"), node("enforce", 0, 100, "m"),
			[]string{"budget.per_run_usd 5 -> 0"}},
		{"a cap put back over 0, which tightens", node("enforce", 0, 100, "m"), node("enforce", 3, 100, "m"), nil},
		{"a token ceiling raised", node("enforce", 5, 100, "m"), node("enforce", 5, 200, "m"),
			[]string{"budget.max_run_tokens 100 -> 200"}},
		{"a token ceiling taken to 0", node("enforce", 5, 100, "m"), node("enforce", 5, 0, "m"),
			[]string{"budget.max_run_tokens 100 -> 0"}},
		{"a wider model list", node("enforce", 5, 100, "a"), node("enforce", 5, 100, "a", "b"),
			[]string{"budget.allowed_models a -> a, b"}},
		{"the model list emptied", node("enforce", 5, 100, "a", "b"), node("enforce", 5, 100),
			[]string{"budget.allowed_models a, b -> (removed)"}},
		{"the same models in another order, which is no widening", node("enforce", 5, 100, "a", "b"), node("enforce", 5, 100, "b", "a"), nil},
		{"a narrower model list, which tightens", node("enforce", 5, 100, "a", "b"), node("enforce", 5, 100, "a"), nil},
		{"the node unchanged", node("enforce", 5, 100, "a"), node("enforce", 5, 100, "a"), nil},
		{"every ceiling at once", node("enforce", 5, 100, "a"), node("observe", 0, 0),
			[]string{
				"budget.mode enforce -> observe",
				"budget.per_run_usd 5 -> 0",
				"budget.max_run_tokens 100 -> 0",
				"budget.allowed_models a -> (removed)",
			}},

		{"a spawn rate raised", guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), guardrails(&policy.Orchestrator{SpawnPerMinute: 60}),
			[]string{"orchestrator.spawn_per_minute 1 -> 60"}},
		{"a spawn rate lowered, which tightens", guardrails(&policy.Orchestrator{SpawnPerMinute: 60}), guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), nil},
		{"a spawn rate raised over the shipped guardrail the node stated none of",
			guardrails(&policy.Orchestrator{BatchSize: 10}), guardrails(&policy.Orchestrator{BatchSize: 10, SpawnPerMinute: 60}),
			[]string{"orchestrator.spawn_per_minute 2 -> 60"}},
		{"a spawn rate given back to the shipped guardrail, which tightens",
			guardrails(&policy.Orchestrator{SpawnPerHour: 40}), guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), nil},
		{"the batch size raised over the shipped one", guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), guardrails(&policy.Orchestrator{SpawnPerMinute: 1, BatchSize: 25}),
			[]string{"orchestrator.batch_size 10 -> 25"}},
		{"the running cap raised", guardrails(&policy.Orchestrator{MaxRunning: 4}), guardrails(&policy.Orchestrator{MaxRunning: 8}),
			[]string{"orchestrator.max_running 4 -> 8"}},
		{"the running cap removed, which hands it back to the person's own limit", guardrails(&policy.Orchestrator{MaxRunning: 4}), guardrails(&policy.Orchestrator{SpawnPerMinute: 1}),
			[]string{"orchestrator.max_running 4 -> (removed)"}},
		{"a running cap over none, which the person's own max_parallel still bounds",
			node("enforce", 0, 0), guardrails(&policy.Orchestrator{MaxRunning: 8}), nil},
		{"a class's model moved down the order", guardrails(&policy.Orchestrator{DecisionModelPerClass: map[policy.DecisionClass]string{policy.DecisionRoutine: "opus"}}),
			guardrails(&policy.Orchestrator{DecisionModelPerClass: map[policy.DecisionClass]string{policy.DecisionRoutine: "sonnet"}}),
			[]string{"orchestrator.decision_model_per_class.routine opus -> sonnet"}},
		{"a class's model raised, which tightens", guardrails(&policy.Orchestrator{DecisionModelPerClass: map[policy.DecisionClass]string{policy.DecisionRoutine: "haiku"}}),
			guardrails(&policy.Orchestrator{DecisionModelPerClass: map[policy.DecisionClass]string{policy.DecisionRoutine: "opus"}}), nil},
		{"a class's model given back to its floor", guardrails(&policy.Orchestrator{DecisionModelPerClass: map[policy.DecisionClass]string{policy.DecisionRoutine: "sonnet"}}),
			guardrails(&policy.Orchestrator{SpawnPerMinute: 1}),
			[]string{"orchestrator.decision_model_per_class.routine sonnet -> haiku"}},
		{"the guardrails unchanged, state them or not",
			guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), guardrails(&policy.Orchestrator{SpawnPerMinute: 1}), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range policyLoosening(tc.old, tc.new) {
				got = append(got, c.Member+" "+c.Was+" -> "+c.Now)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the publish loosens %v, want %v", got, tc.want)
			}
		})
	}
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

// raisedGuardrailFixture is the review of O4's world on this door: the node
// carries the guardrail this machine published — one spawn a minute — and the
// file now states sixty. Nothing else differs, so the publish is the raise and
// nothing but the raise.
func raisedGuardrailFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":      projectpolicy.Version,
		"budget":       map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos":  []string{"bitbucket/acme/other", "github/acme/app"},
		"orchestrator": map[string]any{"spawn_per_minute": 1},
	})
	rewriteProjectConfig(t, f, "orchestrator: {spawn_per_minute: 60}\n")
	return f
}

// TestUpdatePublishRaisedGuardrailNeedsTheTypedName: the guardrails are caps of
// §4.1 rule 6 — a command that would raise a cap is refused, whichever command
// it is — and they travel on the node and nowhere else, so a `fugaro update`
// that raises one raises a ceiling every run of the project is admitted by.
// That is the widening D846 keeps for the project's own hand: the explicit door
// refuses it in an agent's session and this one asks for the typed name, which
// is one decision and not two.
//
// Before the fix, the block was a member no rule judged on either door: the
// stage raised a spawn rate with the ordinary YesOrTyped class, so an agent's
// session in a managed project published it with nothing asked and no record of
// anything but a step that passed.
func TestUpdatePublishRaisedGuardrailNeedsTheTypedName(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := raisedGuardrailFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone raised the project's spawn rate:\n%s", out)
	}
	for _, want := range []string{"orchestrator.spawn_per_minute", "1", "60", "fugaro update", "needs a real terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if !strings.Contains(out, "orchestrator differs") {
		t.Errorf("the diff the refusal stands on does not name the block that differs:\n%s", out)
	}
	if p := publishedPolicy(t, f); p.Orchestrator == nil || p.Orchestrator.SpawnPerMinute != 1 {
		t.Errorf("the node carries %+v, want the published 1: the raise was written behind the refusal", p.Orchestrator)
	}
	if gen := recordedGeneration(t, f); gen != "" {
		t.Errorf("policy_generation = %q, and the refused publish recorded a generation for a file it did not write", gen)
	}

	h = typedRig(t, f)
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the publish: %v\n%s", err, out)
	}
	if p := publishedPolicy(t, f); p.Orchestrator == nil || p.Orchestrator.SpawnPerMinute != 60 {
		t.Errorf("the node carries %+v, want the typed 60", p.Orchestrator)
	}
}

// TestUpdatePublishOfAReplacedBlockAsksForTheMemberItDrops: the publish copies
// the file's block whole (buildProjectPolicy), so a file that states one member
// takes the node's other members away — and where the node's figure was the
// tighter one, that is a raise of a cap like any other: the daemon goes from one
// spawn a minute to the shipped two. The gate reads the block it is about to
// write against the block the node holds (policyCeilingValues fills both sides
// with the shipped guardrails), so the member the file leaves out is compared at
// the default it falls back to and the typed name is asked for it.
//
// Without that the replace-not-merge call would be silent: a file stating a
// lower running cap would publish a raised spawn rate on the ordinary class, and
// an agent's session on a managed project would write it with nothing asked.
func TestUpdatePublishOfAReplacedBlockAsksForTheMemberItDrops(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":      projectpolicy.Version,
		"budget":       map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos":  []string{"bitbucket/acme/other", "github/acme/app"},
		"orchestrator": map[string]any{"max_running": 4, "spawn_per_minute": 1},
	})
	rewriteProjectConfig(t, f, "orchestrator: {max_running: 2}\n")
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone published a block that drops spawn_per_minute 1 back to the shipped 2:\n%s", out)
	}
	for _, want := range []string{"orchestrator.spawn_per_minute", "1", "2", "needs a real terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if p := publishedPolicy(t, f); p.Orchestrator == nil || p.Orchestrator.SpawnPerMinute != 1 || p.Orchestrator.MaxRunning != 4 {
		t.Errorf("the node carries %+v, want the published block untouched behind the refusal", p.Orchestrator)
	}

	// The typed name closes it, and what reaches the node is the file's block
	// whole — the member it does not state is not carried over from the node's.
	h = typedRig(t, f)
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the publish: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.Orchestrator == nil || p.Orchestrator.MaxRunning != 2 || p.Orchestrator.SpawnPerMinute != 0 {
		t.Errorf("the node carries %+v, want the file's own block", p.Orchestrator)
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

// TestUpgradePublishLooserNodeRefusesYesAlone: the other door that writes a
// ceiling is `fugaro upgrade`'s policy step, and it runs the same confirmation.
// --yes is the flag the whole upgrade is run with, so this is where a loosening
// publish would be confirmed by it and nowhere else — the step refuses, names
// the member that loosens and the command that reaches the same publish at a
// terminal, and leaves the node where it was.
func TestUpgradePublishLooserNodeRefusesYesAlone(t *testing.T) {
	noTerminal(t)
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	r := upgradeRigWithGitHubAppID(t)
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMode, budget.ModeEnforce)
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
	// The local project config states a per-run ceiling above the published
	// one, and the node is already at this version under deployed rules: the
	// whole of this run's write would be the loosening publish.
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 7, rtdb_url: "+db.URL+", firebase_project: \""+initProject+"\" }\n")
	db.SetRules([]byte(`{"rules":{".read":false}}`))
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": "enforce", "per_run_usd": 5},
	}))

	got, out := runUpgradePolicyStep(t, r.root)
	if got.state != stepFailed || got.code != ExitUserError {
		t.Fatalf("--yes alone on the upgrade door is %s (exit %d): %s\n%s", got.state, got.code, got.reason, out)
	}
	for _, want := range []string{"budget.per_run_usd", "5", "7", "fugaro update", "needs a real terminal"} {
		if !strings.Contains(got.reason, want) {
			t.Errorf("the refusal does not say %q: %s", want, got.reason)
		}
	}
	// The sentence is said once: what the step printed and the reason the
	// command reports are one block to the person reading them, and a block
	// that repeats the sentence reads as two findings.
	if n := strings.Count(out+got.reason, "does not stand in"); n != 1 {
		t.Errorf("the loosening sentence appears %d times in what the upgrade prints, want once:\n%s%s", n, out, got.reason)
	}
	if got := ceilingOn(t, db); got != 5 {
		t.Errorf("the node's ceiling is %v, want the published 5: the upgrade step wrote the loosening", got)
	}
}

// TestPolicyConfigDriftSeesTheDerivedAccounts: the accounts block and each
// profile's list of the accounts it serves are members the publish derives from
// the local config (D3), and a config edit that only declares an account is a
// config the node has to learn. Comparing the members the file states and
// leaving the derived ones out is what made `fugaro accounts add` write the
// secret, clear the mark and report "nothing else to run" while the node the
// run is admitted by named no account at all (A2's red step).
//
// The comparison is the derivation and never the profiles block: a profile's
// other members are the node's own (`fugaro project repos add` writes its
// allow_repos), so a node whose profile differs only there is not a reason to
// rewrite what every run of the project is admitted by.
func TestPolicyConfigDriftSeesTheDerivedAccounts(t *testing.T) {
	lc := func() *localcfg.Config {
		return &localcfg.Config{
			Budget:         &localcfg.Budget{Mode: "enforce", PerRunUSD: 5},
			ClaudeAccounts: map[string]config.ClaudeAccount{"team-a": {AllowDataTo: []string{"owner/repo-a"}}},
			Profiles: map[string]config.JobProfile{"standard": {
				Provider: "github", ClaudeAccounts: []string{"team-a"},
			}},
		}
	}
	profile := func(mut func(*projectpolicy.Profile)) map[string]projectpolicy.Profile {
		pr := projectpolicy.Profile{JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"team-a"}}}
		if mut != nil {
			mut(&pr)
		}
		return map[string]projectpolicy.Profile{"standard": pr}
	}
	node := func(accounts map[string]projectpolicy.Account, profiles map[string]projectpolicy.Profile) *projectpolicy.Policy {
		return &projectpolicy.Policy{
			Version:  projectpolicy.Version,
			Budget:   projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5},
			Accounts: accounts,
			Profiles: profiles,
		}
	}
	cases := []struct {
		name string
		node *projectpolicy.Policy
		want []string
	}{
		{
			name: "the node names no account the config declares",
			node: node(nil, profile(nil)),
			want: []string{"accounts"},
		},
		{
			name: "the node's account list is what the config derives",
			node: node(map[string]projectpolicy.Account{"team-a": {AllowRepos: []string{"github/owner/repo-a"}}}, profile(nil)),
			want: nil,
		},
		{
			name: "the profile does not say it serves the account",
			node: node(map[string]projectpolicy.Account{"team-a": {AllowRepos: []string{"github/owner/repo-a"}}}, profile(func(pr *projectpolicy.Profile) { pr.ClaudeAccounts = nil })),
			want: []string{"profiles.standard.claude_accounts"},
		},
		{
			name: "the profile's own allow_repos is the node's, not the config's",
			node: node(map[string]projectpolicy.Account{"team-a": {AllowRepos: []string{"github/owner/repo-a"}}}, profile(func(pr *projectpolicy.Profile) { pr.AllowRepos = []string{"github/owner/other"} })),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, d := range policyConfigDrift(lc(), tc.node) {
				got = append(got, d.Member)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("policyConfigDrift = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPolicyConfigDriftSeesTheAccountChoiceKeys is the comparison itself, both
// keys and both directions. The file is the only store of these members and the
// publish is what carries them (§11): a config that sets one while the node
// carries none is the gap `fugaro update` has to close, and a node that names
// one the file does not is the same difference from the other side — the
// publisher copies these keys and keeps none of them, so the publish takes the
// stage off the account.
//
// The members are asserted by name, so a comparison that fires on the wrong
// member (or on the whole block) fails here rather than in a scenario that only
// ever sets the keys the local config states.
func TestPolicyConfigDriftSeesTheAccountChoiceKeys(t *testing.T) {
	config := func(mut func(*localcfg.Config)) *localcfg.Config {
		lc := &localcfg.Config{Budget: &localcfg.Budget{Mode: "enforce", PerRunUSD: 5}}
		if mut != nil {
			mut(lc)
		}
		return lc
	}
	node := func(defaultAccount string, stages map[string]string) *projectpolicy.Policy {
		return &projectpolicy.Policy{
			Version:              projectpolicy.Version,
			Budget:               projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5},
			DefaultClaudeAccount: defaultAccount,
			ClaudeAccountStages:  stages,
		}
	}
	for _, tc := range []struct {
		name string
		lc   *localcfg.Config
		node *projectpolicy.Policy
		want []string
	}{
		{
			name: "the file names a default the node does not carry",
			lc:   config(func(lc *localcfg.Config) { lc.DefaultClaudeAccount = "team-b" }),
			node: node("", nil),
			want: []string{"default_claude_account"},
		},
		{
			name: "the file names a stage account the node does not carry",
			lc:   config(func(lc *localcfg.Config) { lc.ClaudeAccountStages = accountStages("review", "team-b") }),
			node: node("", nil),
			want: []string{"claude_account_stages.review"},
		},
		{
			name: "the node names an account this file's own replaces",
			lc:   config(func(lc *localcfg.Config) { lc.DefaultClaudeAccount = "team-b" }),
			node: node("team-a", map[string]string{"adversarial": "team-a"}),
			want: []string{"default_claude_account", "claude_account_stages.adversarial"},
		},
		{
			name: "both, as this file states them",
			lc: config(func(lc *localcfg.Config) {
				lc.DefaultClaudeAccount = "team-b"
				lc.ClaudeAccountStages = accountStages("review", "team-b")
			}),
			node: node("team-b", map[string]string{"review": "team-b"}),
			want: nil,
		},
		{
			name: "a node that names one this file is silent about",
			lc:   config(nil),
			node: node("team-b", map[string]string{"review": "team-b"}),
			want: []string{"default_claude_account", "claude_account_stages.review"},
		},
		{
			name: "neither side names one",
			lc:   config(nil),
			node: node("", nil),
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, d := range policyConfigDrift(tc.lc, tc.node) {
				got = append(got, d.Member)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("policyConfigDrift = %v, want %v", got, tc.want)
			}
		})
	}
}

// accountStages is a local config's stages block with one account named, the way
// the file spells it (`stages.<stage>.claude_account`, localcfg.StageChoice).
func accountStages(stage, account string) map[string]localcfg.StageChoice {
	return map[string]localcfg.StageChoice{stage: {ClaudeAccount: account}}
}

// TestPolicyAccountWideningCoversTheAccountKeys is the classification, member
// by member: naming an account where the node named none or named another is
// the widening the typed name guards — the credential a mixed run's review goes
// out on, chosen for the project — and the removal is the case that is NOT one,
// because a node that stops naming an account takes the credential away.
func TestPolicyAccountWideningCoversTheAccountKeys(t *testing.T) {
	node := func(defaultAccount string, stages map[string]string) *projectpolicy.Policy {
		return &projectpolicy.Policy{DefaultClaudeAccount: defaultAccount, ClaudeAccountStages: stages}
	}
	for _, tc := range []struct {
		name     string
		old, new *projectpolicy.Policy
		want     []string
	}{
		{"the default set where the node named none", node("", nil), node("team-b", nil),
			[]string{"default_claude_account (not set) -> team-b"}},
		{"the default set where the node named another", node("team-a", nil), node("team-b", nil),
			[]string{"default_claude_account team-a -> team-b"}},
		{"the default unchanged", node("team-b", nil), node("team-b", nil), nil},
		{"the default removed, which tightens", node("team-b", nil), node("", nil), nil},
		{"a stage account set", node("", nil), node("", map[string]string{"review": "team-b"}),
			[]string{"claude_account_stages.review (not set) -> team-b"}},
		{"a stage account removed, which tightens", node("", map[string]string{"review": "team-b"}), node("", nil), nil},
		{"both at once, in the order the diff reads", node("", nil), node("team-b", map[string]string{"adversarial": "team-c", "review": "team-b"}),
			[]string{
				"default_claude_account (not set) -> team-b",
				"claude_account_stages.adversarial (not set) -> team-c",
				"claude_account_stages.review (not set) -> team-b",
			}},
		{"the node unchanged", node("team-b", map[string]string{"review": "team-b"}), node("team-b", map[string]string{"review": "team-b"}), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range policyAccountWidening(tc.old, tc.new) {
				got = append(got, c.Member+" "+c.Was+" -> "+c.Now)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the publish widens %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPolicyConfigDriftRefusesToCompareAnUnpublishableAccount: a config that
// names an account no field defines, or one whose allow_data_to is empty, has
// no pair of documents to compare — the derivation refuses the config itself.
// The entry stands for the member then, so the run goes to the publisher, whose
// refusal names the file and the entry to fix, rather than the stage deciding
// there is nothing to do about a config it cannot read.
func TestPolicyConfigDriftRefusesToCompareAnUnpublishableAccount(t *testing.T) {
	lc := &localcfg.Config{
		Budget:   &localcfg.Budget{Mode: "enforce", PerRunUSD: 5},
		Profiles: map[string]config.JobProfile{"standard": {Provider: "github", ClaudeAccounts: []string{"team-a"}}},
	}
	var got []string
	for _, d := range policyConfigDrift(lc, &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5}}) {
		got = append(got, d.Member)
	}
	if want := []string{"accounts"}; !slices.Equal(got, want) {
		t.Errorf("policyConfigDrift = %v, want %v: an account the config cannot publish is a member to publish", got, want)
	}
}

// TestRecordPublishedGenerationFoldsAnEmptyAllowList is #1658's red step. The
// generation a publish records is checked against the node read back, and the
// Realtime Database keeps no member for an empty list — an RTDB list is an
// object with numeric keys, so `allow_repos: []` is an empty object and is
// pruned. A publish from a config that lists no repository therefore wrote a
// member the read-back can never carry, and the comparison reported a concurrent
// writer that did not exist: no generation was recorded, and the operator was
// sent looking for a re-seed that never came. The read-back here is the node as
// the database stores that document, with the empty allow-list member gone.
func TestRecordPublishedGenerationFoldsAnEmptyAllowList(t *testing.T) {
	f := newProjectFixture(t, "")
	db, err := newBudgetClient(t.Context(), f.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// The document the publish wrote spells allow_repos as an empty list; the
	// database keeps no member for it.
	payload := []byte(fmt.Sprintf(`{"version":%d,"allow_repos":[]}`, projectpolicy.Version))
	f.db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version})

	if err := recordPublishedGeneration(t.Context(), io.Discard, db, lc, f.path, payload); err != nil {
		t.Fatalf("the record was refused for a member the database drops: %v", err)
	}
	if gen, want := recordedGeneration(t, f), nodeETag(t, f); gen != want {
		t.Errorf("policy_generation = %q, want the node's own generation %q", gen, want)
	}
}

// setConfigWithNoRepos rewrites the fixture's config with an empty repos map: a
// project whose local config lists no repository, which is the state a publish
// with an empty allow-list is about.
func setConfigWithNoRepos(t *testing.T, f *projectFixture) {
	t.Helper()
	text := f.configText(t, projectConfigText(f.dir, f.db.URL, projectProvidersYAML, ""))
	text = strings.Replace(text,
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n  acme/other: { provider: bitbucket, workflows: [web] }\n",
		"repos: {}\n", 1)
	if !strings.Contains(text, "repos: {}\n") {
		t.Fatal("the fixture's config does not carry the repositories this helper replaces")
	}
	if err := os.WriteFile(f.path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestConfirmEmptyAllowListRefusesAnAgentSessionBeforeAnyPrompt pins the guard
// directly: no route of the publish reaches it from a coding agent's session
// today (a managed project keeps the published allow-list), so a later route
// that did would otherwise accept a name the session piped into stdin.
func TestConfirmEmptyAllowListRefusesAnAgentSessionBeforeAnyPrompt(t *testing.T) {
	typed(t)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("aurora\n"))
	err := confirmEmptyAllowList(cmd, &localcfg.Config{Name: "aurora"}, "CLAUDECODE")
	if err == nil {
		t.Fatal("an agent session's typed name confirmed an empty allow-list")
	}
	if strings.Contains(out.String(), "Type ") {
		t.Errorf("the session was prompted for the name:\n%s", out.String())
	}
}

// TestProjectPolicyPublishWarnsBeforeAnEmptyAllowList is #1674 under the
// no-prompt policy (T7): a publish from a config that lists no repository
// writes an allow-list that admits nothing, and from that moment every run of
// the project stops before it starts (doctor's allow-list-empty). The diff
// shows only `+ allow_repos: []`, which reads as harmless, so the consequence
// is said before the write — the ⚠ CONFIRM banner — and nothing is typed under
// it: the person at the terminal is the whole of the answer, and a session
// without one is refused with the same sentence, so a script or a cron entry
// cannot write the node on the operator's behalf. A coding agent's session is
// refused too, before the node is even built.
func TestProjectPolicyPublishWarnsBeforeAnEmptyAllowList(t *testing.T) {
	f := newProjectFixture(t, "")
	setConfigWithNoRepos(t, f)

	// With no terminal — and no --ask this door has — nobody can have read the
	// consequence, so the publish is refused: the guard the typed name used to
	// be, and nothing is written.
	noTerminal(t)
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a publish that admits no repository went through with no terminal:\n%s", out)
	}
	for _, want := range []string{"needs a real terminal", "every run stops before it starts", "fugaro project repos add provider/owner/name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Fatal("the refused publish wrote the node")
	}

	// A person at the terminal answers it with nothing typed: the warning is
	// printed and the publish lands.
	fakeTerminal(t)
	rd := &readSpy{Reader: strings.NewReader("")}
	cmd := NewRootCmd()
	var outBuf, errOut bytes.Buffer
	cmd.SetIn(rd)
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs([]string{"project", "policy", "publish"}))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("the publish was refused at a terminal: %v\n%s%s", err, outBuf.String(), errOut.String())
	}
	for _, want := range []string{"⚠ CONFIRM (project aurora)", "every run stops before it starts", "fugaro project repos add provider/owner/name"} {
		if !strings.Contains(outBuf.String(), want) {
			t.Errorf("the publish does not say %q:\n%s", want, outBuf.String())
		}
	}
	if rd.read {
		t.Errorf("the publish read stdin, and nothing is typed at a confirmation:\n%s", outBuf.String())
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Fatal("the publish wrote no node")
	}

	// The control: a coding agent's session writes none of it (the door's own
	// guard refuses it before the node is built, and where the read of the
	// project's marks cannot be made at all it fails closed: either way the
	// error comes back and no node is written).
	f2 := newProjectFixture(t, "")
	setConfigWithNoRepos(t, f2)
	t.Setenv(agentMarkers[0], "1")
	out2, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("an agent session's publish went through:\n%s", out2)
	}
	if f2.db.Value(projectpolicy.PathShared()) != nil {
		t.Fatal("an agent session wrote the node")
	}
}

// contextCatalogYAML is the local config's image catalog with one entry whose
// source names a build context: the shape CB1's grammar accepts and no released
// runner reads yet, which is the member policyContextRefusal is about. The
// second constant is the same catalog with the member left off, so a test can
// tell the member's refusal from a refusal of catalogs.
const contextCatalogYAML = "image_catalog:\n" +
	"  entries:\n" +
	"    - name: tools\n" +
	"      tags: [node]\n" +
	"      source: { kind: dockerfile, repo: acme/app, path: ci/Dockerfile, context: ci/ }\n"

const noContextCatalogYAML = "image_catalog:\n" +
	"  entries:\n" +
	"    - name: tools\n" +
	"      tags: [node]\n" +
	"      source: { kind: dockerfile, repo: acme/app, path: ci/Dockerfile }\n"

// contextCatalogPolicy is that catalog as a node: the member is judged on the
// node about to be published and not on the config that states it, so a test
// has to be able to hand one in directly — that is the state a machine whose
// config says nothing about the catalog meets when the published node carries
// it.
func contextCatalogPolicy(context string) *projectpolicy.Policy {
	return &projectpolicy.Policy{ImageCatalog: &config.Catalog{Entries: []config.Entry{{
		Name: "tools",
		Tags: []string{"node"},
		Source: config.Source{
			Kind: config.SourceDockerfile, Repo: "acme/app", Path: "ci/Dockerfile", Context: context,
		},
	}}}}
}

// TestPolicyContextRefusalHoldsTheGateBothWays asks the guard directly, the way
// the skew gates' tests ask theirs: while imageContextShipped is false an entry
// naming a context is refused with the member and the entry named, an entry
// naming none is not (the gate is the member, not catalogs), and once the
// release that reads the member ships (shipped true) the same node publishes.
//
// A guard that only refused, or only admitted, would pass half of this and lose
// the other: the refusal is what keeps D1403's failure out of a published node,
// and the two admissions are what keep this from becoming a gate on the
// catalog.
func TestPolicyContextRefusalHoldsTheGateBothWays(t *testing.T) {
	if imageContextShipped {
		t.Fatal("imageContextShipped is true: this test is written for the release that does not read the member yet, and the flip's own commit rewrites it")
	}
	withContext := contextCatalogPolicy("ci/")
	withoutContext := contextCatalogPolicy("")
	if err := policyContextRefusal(nil, imageContextShipped); err != nil {
		t.Errorf("a project with no catalog at all: %v, want no refusal", err)
	}
	if err := policyContextRefusal(withoutContext, imageContextShipped); err != nil {
		t.Errorf("an entry naming no context: %v, want no refusal (the member is what the gate is about)", err)
	}
	err := policyContextRefusal(withContext, imageContextShipped)
	if err == nil {
		t.Fatal("an entry naming a context was published while no release reads the member: every run of the project would fail on the older runner's strict decode of the node")
	}
	for _, want := range []string{"tools", "context", "no fugaro release reads that key yet", "Leave context: off the entry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if err := policyContextRefusal(withContext, true); err != nil {
		t.Errorf("the release that reads the member still refuses it: %v", err)
	}
}

// TestPolicyPublishRefusesACatalogContextMember is the door's own half: the
// node both publish doors build is refused while the member is unread, whether
// the config is where the member came from or the published node it replaces —
// a config silent about the catalog keeps the node's, so both sides reach these
// bytes. Without the call policyNodeForPublish makes, this test fails: the
// publish returns a body and nothing else in the package asks about the member.
func TestPolicyPublishRefusesACatalogContextMember(t *testing.T) {
	f := newProjectFixture(t, contextCatalogYAML)
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := policyNodeForPublish(t.Context(), lc, &projectpolicy.Policy{}, nil, false, false, false)
	if err == nil {
		t.Fatalf("the publish built the node from a config naming a context: %s", body)
	}
	for _, want := range []string{"tools", "context", "no fugaro release reads that key yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The same member reaching the node from the other side: this machine's
	// config states no catalog, so the node about to be published is the one it
	// replaces — and the operator's older runner reads that node.
	g := newProjectFixture(t, "")
	lc, err = localcfg.Load(g.path)
	if err != nil {
		t.Fatal(err)
	}
	carried := contextCatalogPolicy("ci/")
	body, _, _, err = policyNodeForPublish(t.Context(), lc, carried, nil, false, false, false)
	if err == nil {
		t.Fatalf("the publish kept a node carrying a context member: %s", body)
	}
	if !strings.Contains(err.Error(), "no fugaro release reads that key yet") {
		t.Errorf("the refusal of a carried member does not name the gate: %v", err)
	}

	// The control: the same catalog without the member publishes, so the gate is
	// the member and not the catalog.
	h := newProjectFixture(t, noContextCatalogYAML)
	lc, err = localcfg.Load(h.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := policyNodeForPublish(t.Context(), lc, &projectpolicy.Policy{}, nil, false, false, false); err != nil {
		t.Fatalf("the same catalog with no context member was refused: %v", err)
	}
}
