//go:build firebase

package rules

// The paired device's read of `outcomes` (mobile identity-pairing task J1,
// finding F30, decision D2797, O3 answered by the owner on 2026-11-18), against
// the two documents a phone really meets: the generation the release behind
// deployed, where the outcome node carries no read at all, and the one this
// binary deploys, where it carries `USER` alone. The emulator is the only place
// the claim can be made — the golden reads the same template's text — and it is
// the one the task's acceptance names: a paired device's token reads `outcomes`
// and keeps its read of `spend/$day/runs/$slug/$run`, nothing wider, so the
// phone can derive a run that is still going as a run entry with no outcome.
//
// The document behind is testdata/rules.generation6.json, the frozen copy the
// neighbours use, and neither it nor generation 7 carries a read here: the node
// was a run's write and nothing else from generation 3 on (the older fixture is
// the one on disk, and the claim under test is about a read that appears only in
// this binary's document).

import (
	"os"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

func TestEmulatorRunsADeviceReadsOutcomes(t *testing.T) {
	previous, err := os.ReadFile("testdata/rules.generation6.json")
	if err != nil {
		t.Fatalf("reading the generation-6 fixture: %v", err)
	}
	current, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithRules(t, previous)

	// Today, this project's repository, and two runs of it: one that has
	// finished (the record the view reads) and one that is still going (the run
	// the phone has to name by its spend entry alone, which is the whole point
	// of the grant — it has no outcome to read).
	day := budget.Day(time.Now())
	const slug = "aurora-app"
	const finished, running = "20261119-100000-ab12", "20261119-130000-cd34"
	outcome := budget.PathOutcome(day, slug, finished)

	// The write half is the run's and does not move: the run files its own
	// outcome under the document behind, which is what puts a record at the node
	// for the read cases below to be about. A write is not enough to read it
	// back — that is the gap J1 closes — so the phone's read of the very node
	// the run just wrote is what the old document refuses.
	h.mustAllow(h.run(slug, finished), "the run's own outcome under the document behind, filed once as it ends",
		map[string]any{outcome: anOutcome()})

	alice := h.phone(mbAlice)
	// The positive control, and it is the acceptance's parenthetical: the
	// device's read of `spend/$day/runs/$slug/$run` is older than this change
	// (generation 2's family, `RUN || USER`) and survives it. A phone under the
	// old document reads a running run's spend and not its record.
	h.set(budget.PathSpendRun(day, slug, running), map[string]any{"reserved": 1, "spent": 0})
	h.mustRead(alice, budget.PathSpendRun(day, slug, running))
	h.mustReadDenied(alice, outcome)

	// The generation this binary deploys, put there the way `fugaro update` puts
	// one: the same database, the same namespace, one document later. The wipe
	// takes the outcome, the spend node and the device allowlist with it, so the
	// run files again, the day counter is written again, and alice pairs again
	// (her token is unchanged; `fugaro pair` writes the node USER requires).
	h.wipe()
	h.deployRules(current)
	h.mustAllow(h.run(slug, finished), "the run's own outcome under this binary's document", map[string]any{outcome: anOutcome()})
	h.set(budget.PathSpendRun(day, slug, running), map[string]any{"reserved": 1, "spent": 0})
	h.pairDevice(mbAlice)

	// The acceptance: the paired device reads the run's outcome, and the child
	// under it, which is what an RTDB read grant on the node does — it cascades
	// down and no further.
	h.mustRead(alice, outcome)
	h.mustRead(alice, outcome+"/status")

	// Nothing wider. The two parents of the granted node are not granted, so
	// the app reads a run it can already name and no list of days or runs; the
	// run's own ledger is still the run's, which is the row the acceptance
	// spells out; and the grant is the *claim's*, so an account with no device
	// node reads none of it (D166) — the node's presence is what admits a
	// phone, and the read does not slip past it.
	h.mustReadDenied(alice, "outcomes")
	h.mustReadDenied(alice, "outcomes/"+budget.DayKey(day))
	h.mustReadDenied(alice, "outcomes/"+budget.DayKey(day)+"/"+slug)
	h.mustReadDenied(alice, budget.PathRun(slug, finished))
	h.mustReadDenied(h.unpaired(mbBob), outcome)

	// And it is a read, not a write, on either node: the outcome is the run's to
	// file once and the ledger is the run's to move, so the phone that can read
	// them writes neither.
	h.mustDeny(alice, "a phone filing an outcome", map[string]any{outcome: anOutcome()})
	h.mustDeny(alice, "a phone rewriting an outcome's status", map[string]any{outcome + "/status": "failed"})
	h.mustDeny(alice, "a phone deleting an outcome", map[string]any{outcome: nil})
	h.mustDeny(alice, "a phone writing the run's ledger", map[string]any{budget.PathRun(slug, finished) + "/reserved": 1})

	// The run's own reach did not widen with the device's: `USER` alone is the
	// grant, and a run token carries no mu claim, so the parent the run never
	// read is still refused to it.
	h.mustReadDenied(h.run(slug, finished), "outcomes")
}

// anOutcome is a well-formed outcome record: the two fields the node's
// validator requires, with `requestedBy` naming the token's own rb (RBOK).
func anOutcome() map[string]any {
	return map[string]any{"status": "succeeded", "requestedBy": "alice@example.invalid"}
}
