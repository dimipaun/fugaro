package budget_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
)

// entryChildren reads the children the generated document names under
// agents/<slug>/$run: the node's closed allowlist, as the database applies it.
// It reads rules.Generate() rather than the golden for the reason
// TestRulesPreexistingPathsUnchanged reads the document rather than the
// template: a regenerated golden is exactly how a child disappears unnoticed,
// and this test's own red step is "the rules refuse the member".
func entryChildren(t *testing.T) map[string]any {
	t.Helper()
	raw, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	root, _ := doc["rules"].(map[string]any)
	agents, _ := root["agents"].(map[string]any)
	slugNode, _ := agents["$slug"].(map[string]any)
	run, _ := slugNode["$run"].(map[string]any)
	if run == nil {
		t.Fatal("the generated document has no agents/$slug/$run node")
	}
	return run
}

// TestTheEntryCarriesTheSource is CS9's own claim about the registry entry
// (model-credentials-routing, D3070): the live entry gains `source` and
// `fallback`, the two facts the record carries per stage, so `fugaro watch` can
// show what a running stage goes out on.
//
// Three things have to hold at once, and each fails on its own mutation:
//
//   - the document names both children under agents/$slug/$run. That node is a
//     closed allowlist (`$other: .validate false`), so a member
//     budget.AgentEntry marshals and the template does not name is a write the
//     database refuses whole — the run's registry freezes, not just the member.
//     This is the red step: before the template change the two are refused.
//   - a session writes both, and the entry that lands carries them as written.
//   - the two are one fact and travel together: a run writes `fallback` only
//     with a `source`, which is why the view's dash for an absent source and
//     its absent flag mean the same thing.
func TestTheEntryCarriesTheSource(t *testing.T) {
	children := entryChildren(t)
	for _, child := range []string{"source", "fallback"} {
		if _, ok := children[child]; !ok {
			t.Fatalf("the generated rules do not name /agents/$slug/$run/%s: the entry's $other guard refuses the whole write that carries it", child)
		}
	}
	// The entry's own guard stays closed: a member the template does not name
	// is refused, which is what makes the two children above load-bearing.
	if v, ok := children["$other"].(map[string]any); !ok || v[".validate"] != false {
		t.Fatalf("agents/$slug/$run no longer closes itself with $other: %v", children["$other"])
	}

	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement", Source: "pool:team", Fallback: true})
	entry := func() map[string]any {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e
	}
	waitFor(t, "the entry to carry the source and its flag", func() bool {
		e := entry()
		return e != nil && e["source"] == "pool:team" && e["fallback"] == true
	})
	// A later stage that goes out on the first source of its chain clears the
	// flag: the entry is one object, written whole, so the omitted member is
	// what the PATCH removes. A flag that outlived its stage would mark every
	// later stage of the run as fallen back.
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Source, e.Fallback = "review", "anthropic", false })
	waitFor(t, "the flag to clear with the stage that fell back", func() bool {
		e := entry()
		return e != nil && e["stage"] == "review" && e["source"] == "anthropic" && e["fallback"] == nil
	})
}

// TestRulesConvergeWithTheSource is the convergence test CS9 ships with its
// rules change: an installation whose document predates the two children is
// brought to this one by the upgrade it already runs, and no run is broken
// while that happens.
//
// Three things make that true, and the test holds each:
//
//   - the document moved, so `fugaro update --firebase-project` redeploys it.
//     The deploy PUTs when the generated document differs from the deployed one
//     (internal/infra's SameJSON), which is the migration every rules change of
//     this shape takes; the marker stays at CredsRulesVersion, so nothing
//     refuses the deploy and nothing is asked of the owner.
//   - the document moved by exactly the two children. A previous document is
//     the generated one without them, and the two are equal in every other
//     path — so an installation at the previous version converges by gaining
//     the two and changing nothing it already had.
//   - a run in flight under the previous document keeps its registry. The
//     children are refused with the entry and accepted without it, so the
//     session drops the two once, warns naming the redeploy, and goes on: no
//     halt, and the entry keeps landing. That is the shape the withdrawn
//     credentials-belong-to-models plan settled for the same member (CM12: the
//     runner "drops the member and writes the rest, as it does for recipe").
func TestRulesConvergeWithTheSource(t *testing.T) {
	prev := previousSourceDocument(t)
	cur, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(prev, mustDoc(t, cur)) {
		t.Fatal("the document did not move: an installation at the previous version would never gain the two children")
	}
	if !sameExceptTheSource(prev, mustDoc(t, cur)) {
		t.Fatal("the two documents differ somewhere other than the entry's source and fallback: the convergence above is the whole change")
	}
	if rules.CredsRulesVersion != 1 || rules.PathCredsRulesMarker() != "config/limits/credsRules" {
		t.Fatalf("the family's generation moved: CredsRulesVersion = %d at %q, so an installation at the previous document would be refused a deploy it needs (marker.go says why this change does not move it)",
			rules.CredsRulesVersion, rules.PathCredsRulesMarker())
	}

	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	s.Update(func(e *budget.AgentEntry) { e.Source, e.Fallback = "pool:team", true })
	f.db.DenyKeys("source", "fallback") // the previous document: no child, and $other refuses the whole entry
	waitFor(t, "the fallback warning naming the redeploy", func() bool {
		return strings.Contains(logs.String(), "fugaro update --firebase-project")
	})
	waitFor(t, "the entry to settle without the two children", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "implement" && e["source"] == nil && e["fallback"] == nil
	})
	if n := strings.Count(logs.String(), "fugaro update"); n != 1 {
		t.Fatalf("warned %d times, want once: %s", n, logs.String())
	}
	// Dropped for the rest of the session, like the recipe and the rigor mode:
	// a stage that goes out on a new source does not bring the member back.
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Source = "review", "anthropic" })
	waitFor(t, "the stage change despite the reappearing member", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "review" && e["source"] == nil
	})
	if h, ok := f.nextHalt(50 * time.Millisecond); ok {
		t.Fatalf("the run halted under the previous document (%+v): the two are the dashboard's, and dropping them is what a run under older rules does", h)
	}
}

// TestHeartbeatDropsOnlyTheRefusedChild is the other half of the fallback: the
// heartbeat's probes ask the newest child first, and each of them answers only
// its own question.
//
// A run started by this release against the previous release's document meets
// it with a source AND a rigor mode in its entry. The first probe's write lands
// — the document is missing the routing children and nothing else — so the
// refusal is answered, and the probes below it must not read that same refusal
// as their own: the rigor mode is a child these rules know, and a beat that
// dropped it anyway would tell the operator the rules predate the mode while
// the row lost a mode the console could have drawn. This is the shape Start
// keeps by reassigning err inside each branch.
func TestHeartbeatDropsOnlyTheRefusedChild(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "implement", Recipe: "claude-solo", Mode: "critical", From: "area", Source: "pool:team"})
	f.db.DenyKeys("source", "fallback") // the previous document: no child, and $other refuses the whole entry
	waitFor(t, "the entry to settle without the refused children", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "implement" && e["source"] == nil && e["fallback"] == nil
	})
	if e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any); e["mode"] != "critical" || e["from"] != "area" {
		t.Fatalf("the rigor mode went with a refusal that was about the routing source: %v", e)
	}
	// The write lands before the beat logs its warning, so the settled entry
	// does not yet mean the line is there.
	waitFor(t, "the fallback warning", func() bool {
		return strings.Contains(logs.String(), "fugaro update --firebase-project")
	})
	if strings.Contains(logs.String(), "predate the rigor mode") {
		t.Fatalf("the heartbeat blamed the wrong child:\n%s", logs.String())
	}
	if n := strings.Count(logs.String(), "fugaro update --firebase-project"); n != 1 {
		t.Fatalf("warned %d times, want once: %s", n, logs.String())
	}
	// The session goes on with the mode kept: a later stage still carries it,
	// and the children the document does know are not dropped for the rest of
	// the run.
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Mode, e.From = "review", "hotfix", "repo" })
	waitFor(t, "a heartbeat carrying the new stage", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "review"
	})
	if e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any); e["mode"] != "hotfix" || e["from"] != "repo" || e["recipe"] != "claude-solo" {
		t.Fatalf("the entry after the next stage is not the one the run wrote: %v", e)
	}
	if h, ok := f.nextHalt(50 * time.Millisecond); ok {
		t.Fatalf("the run halted under the previous document (%+v): the two are the dashboard's, and dropping them is what a run under older rules does", h)
	}
}

// TestHeartbeatDropsBothFamiliesWhenTheRulesKnowNeither: a document older than
// both of the entry's late children — the routing table's source and the rigor
// mode's pair — refuses every beat, and the run must still land its registry
// entry, because the usage report travels in the same patch as the entry: a
// beat that lands nothing is a live row that freezes for the rest of the run
// and a grace clock the refusals run out.
//
// The two children are not independent refusals. Each fallback write must carry
// what the writes before it left out: a probe that dropped only its own family
// would send the other's refused child and be refused for it, so under this
// document neither write lands and every beat after them is refused the same
// way. Start meets the same document with one entry it drops from cumulatively,
// which is what the heartbeat's write has to be.
func TestHeartbeatDropsBothFamiliesWhenTheRulesKnowNeither(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "implement", Recipe: "claude-solo", Mode: "critical", From: "area", Source: "pool:team"})
	f.db.DenyKeys("source", "fallback", "mode", "from") // a document older than both families
	entry := func() map[string]any {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e
	}
	waitFor(t, "the entry to settle without either family", func() bool {
		e := entry()
		return e != nil && e["stage"] == "implement" && e["source"] == nil && e["mode"] == nil
	})
	if e := entry(); e["fallback"] != nil || e["from"] != nil {
		t.Fatalf("a child the document refused came back: %v", e)
	}
	if e := entry(); e["recipe"] != "claude-solo" {
		t.Fatalf("the recipe went with them, and the document knows it: %v", e)
	}
	// The write lands before the beat logs its warnings, so the settled entry
	// does not yet mean the lines are there.
	waitFor(t, "both warnings", func() bool {
		return strings.Contains(logs.String(), "predate the routing table's source") && strings.Contains(logs.String(), "predate the rigor mode")
	})
	// One line per family, once: each names the child it is about and the one
	// command that fixes it.
	for _, want := range []string{"predate the routing table's source", "predate the rigor mode"} {
		if n := strings.Count(logs.String(), want); n != 1 {
			t.Fatalf("%q logged %d times, want once:\n%s", want, n, logs.String())
		}
	}
	// The run goes on: the beats after this one carry neither family and are
	// accepted, so what the row shows keeps moving. A halt here would be the
	// budget-unavailable the drop exists to prevent, over two facts the
	// dashboard only ever shows.
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Mode, e.From = "review", "hotfix", "repo" })
	waitFor(t, "a heartbeat carrying the new stage", func() bool {
		e := entry()
		return e != nil && e["stage"] == "review"
	})
	if e := entry(); e["mode"] != nil || e["from"] != nil || e["source"] != nil {
		t.Fatalf("a dropped family came back: %v", e)
	}
	if h, ok := f.nextHalt(200 * time.Millisecond); ok {
		t.Fatalf("the run halted under the older document (%+v): the two are the dashboard's, and dropping them is what a run under older rules does", h)
	}
}

// previousSourceDocument is the document an installation at the version before
// CS9 holds: the generated one with the entry's two children removed. It is
// derived rather than frozen, so it follows a change to any other rule and
// still states the one difference this test is about — the same shape the
// build-lease convergence test uses for its own addition (T8's
// previousRulesDocument, internal/cli/init_firebase_test.go).
func previousSourceDocument(t *testing.T) map[string]any {
	t.Helper()
	cur, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := mustDoc(t, cur)
	run, _ := doc["rules"].(map[string]any)["agents"].(map[string]any)["$slug"].(map[string]any)["$run"].(map[string]any)
	if run == nil {
		t.Fatal("the generated document has no agents/$slug/$run node")
	}
	for _, child := range []string{"source", "fallback"} {
		if _, ok := run[child]; !ok {
			t.Fatalf("the generated document does not carry /agents/$slug/$run/%s", child)
		}
		delete(run, child)
	}
	return doc
}

// sameExceptTheSource reports whether a and b are equal with the entry's two
// children removed from b: the convergence above is the whole change, so a
// second difference is either an unannounced rules change or a member this test
// would wave through.
func sameExceptTheSource(prev, cur map[string]any) bool {
	run, _ := cur["rules"].(map[string]any)["agents"].(map[string]any)["$slug"].(map[string]any)["$run"].(map[string]any)
	added := map[string]any{}
	for _, child := range []string{"source", "fallback"} {
		added[child] = run[child]
		delete(run, child)
	}
	defer func() {
		for child, v := range added {
			run[child] = v
		}
	}()
	return reflect.DeepEqual(prev, cur)
}

func mustDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
