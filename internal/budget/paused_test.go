package budget_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// pausedClause is PR1's disjunct in the outcome's status validator, with the
// spacing the template writes it in (model-credentials-routing, D3185, R15). It
// is spelled here rather than read from the rules package because the rule is a
// string in a document and this test's claim is about that string: the word has
// to be in the deployed validator, or the database refuses the outcome of the
// one run that suspends itself.
const pausedClause = " || newData.val() == 'paused'"

// outcomeStatusRule reads the outcome's status validator out of the generated
// document: /outcomes/$day/$slug/$run/status's `.validate`. It reads
// rules.Generate() rather than the golden for the reason the entry tests do —
// a regenerated golden is exactly how a word disappears unnoticed — and the
// document rather than the template because what the database applies is the
// document.
func outcomeStatusRule(t *testing.T) (doc map[string]any, rule string) {
	t.Helper()
	raw, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc = mustDoc(t, raw)
	outcomes, _ := doc["rules"].(map[string]any)["outcomes"].(map[string]any)
	day, _ := outcomes["$day"].(map[string]any)
	slugNode, _ := day["$slug"].(map[string]any)
	run, _ := slugNode["$run"].(map[string]any)
	status, _ := run["status"].(map[string]any)
	if status == nil {
		t.Fatal("the generated document has no outcomes/$day/$slug/$run/status node: the outcome's status is unvalidated")
	}
	rule, _ = status[".validate"].(string)
	if rule == "" {
		t.Fatalf("the outcome's status carries no validator: %v", status)
	}
	return doc, rule
}

// TestTheRulesAdmitPaused is PR1's own claim about the deployed document: a run
// that suspends itself writes its outcome as `paused`, and the rules have to
// accept that word (model-credentials-routing, D3185, R15).
//
// Two things have to hold at once, and each fails on its own mutation:
//
//   - the generated document's outcome status validator accepts `paused`. This
//     is the red step: before the template change the validator is the closed
//     enumeration of the words a release knew, and the word is refused.
//   - the session files the word it is given. internal/budget's outcome
//     vocabulary is derived from runstore.Statuses, so a status the rules
//     accept and this package does not know would be rewritten as an
//     infrastructure error — a fault that never happened — and the dashboard
//     would show it. The two halves are the same set read twice, which is why
//     they are asserted together: TestOutcomeRuleAcceptsEveryRunStatus
//     (internal/budget/rules) holds the rule to the vocabulary, and this holds
//     the vocabulary's end of it to the rule.
//
// A run that suspends itself writes its outcome and ends (R12): the outcome
// node is the one the write already goes through, so the change here is a
// widening of one validator and not a new reach.
func TestTheRulesAdmitPaused(t *testing.T) {
	_, rule := outcomeStatusRule(t)
	if !strings.Contains(rule, "newData.val() == '"+string(runstore.StatusPaused)+"'") {
		t.Fatalf("the outcome's status validator does not accept %q: a run that suspends itself writes that outcome and the database refuses it, so the run is left with no outcome at all\nrule: %s", runstore.StatusPaused, rule)
	}
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	s.Finish(context.Background(), string(runstore.StatusPaused))
	out, _ := f.db.Value(budget.PathOutcome(today(), slug, runID)).(map[string]any)
	if out == nil || out["status"] != string(runstore.StatusPaused) {
		t.Fatalf("outcome = %v, want the run's own status %q: a word the rules accept and this package rewrites is a figure that contradicts the run's record", out, runstore.StatusPaused)
	}
}

// TestRulesConvergeWithPaused is the convergence test PR1 ships with its rules
// change: an installation whose document predates the word is brought to this
// one by the upgrade it already runs, and no run is broken while that happens.
//
// Three things make that true, and the test holds each:
//
//   - the document moved, so `fugaro update --firebase-project` redeploys it.
//     The deploy PUTs when the generated document differs from the deployed one
//     (internal/infra's SameJSON), which is the migration every rules change of
//     this shape takes; the marker stays at CredsRulesVersion, so nothing
//     refuses the deploy and nothing is asked of the owner. A bump is what the
//     family's marker is *not* for here (marker.go says why): the word is one
//     more value in a validator a run already writes through, and a bump would
//     refuse the deploy — and a cascade launch — on every installation whose
//     owner has not run the update yet.
//   - the document moved by exactly the one clause. A previous document is the
//     generated one without it, and the two are equal in every other path — so
//     an installation at the previous version converges by gaining the word and
//     changing nothing it already had.
//   - a run that suspends itself under the previous document is not broken by
//     the refusal. The outcome write is refused (rules: the closed
//     enumeration), and Finish's own best effort logs it and goes on — the run
//     still ends `paused` and its record, a bucket object no rule binds, still
//     says so; the figure the dashboard is missing arrives with the deploy.
//     That is the shape the credentials children's convergence test takes for
//     the members a run writes, one step milder: a member the entry refuses
//     takes the entry with it, while this refusal costs the figure alone.
func TestRulesConvergeWithPaused(t *testing.T) {
	prev := previousPausedDocument(t)
	cur, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(prev, mustDoc(t, cur)) {
		t.Fatal("the document did not move: an installation at the previous version would never gain the word, and its runs' outcomes would be refused")
	}
	if !sameExceptThePausedWord(prev, mustDoc(t, cur)) {
		t.Fatal("the two documents differ somewhere other than the outcome's paused clause: the convergence above is the whole change")
	}
	if rules.CredsRulesVersion != 1 || rules.PathCredsRulesMarker() != "config/limits/credsRules" {
		t.Fatalf("the family's generation moved: CredsRulesVersion = %d at %q, so an installation at the previous document would be refused a deploy it needs (marker.go says why this change does not move it)",
			rules.CredsRulesVersion, rules.PathCredsRulesMarker())
	}

	// The run in flight: a session under the previous document, whose outcome
	// write the closed enumeration refuses.
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	f.db.Deny(budget.PathOutcome(today(), slug, runID)) // the previous document: the word is not in the enumeration
	s.Finish(context.Background(), string(runstore.StatusPaused))
	if !strings.Contains(logs.String(), "writing the outcome failed") {
		t.Fatalf("the refused outcome was not logged as the best-effort failure it is:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "the outcome status is not one this build writes") {
		t.Fatalf("the word was rewritten as an infrastructure error rather than sent as the run's own status:\n%s", logs.String())
	}
	if h, ok := f.nextHalt(50 * time.Millisecond); ok {
		t.Fatalf("the run halted under the previous document (%+v): a refused outcome is not a halt, and the run's record — a bucket object no rule binds — still says it ended paused", h)
	}
}

// previousPausedDocument is the document an installation at the version before
// PR1 holds: the generated one with the paused clause removed from the
// outcome's status validator. It is derived rather than frozen, so it follows a
// change to any other rule and still states the one difference this test is
// about — the same shape the entry's own convergence test uses
// (previousSourceDocument, internal/budget/source_test.go).
func previousPausedDocument(t *testing.T) map[string]any {
	t.Helper()
	doc, rule := outcomeStatusRule(t)
	if !strings.Contains(rule, pausedClause) {
		t.Fatalf("the outcome's status validator does not carry %q: there is nothing for the previous document to lack\nrule: %s", pausedClause, rule)
	}
	status := doc["rules"].(map[string]any)["outcomes"].(map[string]any)["$day"].(map[string]any)["$slug"].(map[string]any)["$run"].(map[string]any)["status"].(map[string]any)
	status[".validate"] = strings.Replace(rule, pausedClause, "", 1)
	return doc
}

// sameExceptThePausedWord reports whether a and b are equal with the paused
// clause removed from b: the convergence above is the whole change, so a second
// difference is either an unannounced rules change or a word this test would
// wave through.
func sameExceptThePausedWord(prev, cur map[string]any) bool {
	status, _ := cur["rules"].(map[string]any)["outcomes"].(map[string]any)["$day"].(map[string]any)["$slug"].(map[string]any)["$run"].(map[string]any)["status"].(map[string]any)
	rule, _ := status[".validate"].(string)
	status[".validate"] = strings.Replace(rule, pausedClause, "", 1)
	return reflect.DeepEqual(prev, cur)
}
