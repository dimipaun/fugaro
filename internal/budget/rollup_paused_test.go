package budget

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRollupCountsPausedApart is model-credentials-routing PR11's half for this
// package: a run that suspended itself to be resumed by its chain
// (contract-pause, R15) is neither a failure nor a success, so the day's
// figures keep a paused run in a count of its own — the one a person reads to
// see how many runs stopped for a credential — and count it in neither of the
// two endings beside it nor in "other", which is where a word this build does
// not know lands.
func TestRollupCountsPausedApart(t *testing.T) {
	const paused = string(runstore.StatusPaused)

	in := RollupInput{
		Day:    day,
		Repos:  map[string]RepoDay{"a-1": {Counters: Counters{Spent: 700}, Repo: "acme/a"}},
		Shares: map[string]map[string]RunLedger{"a-1": {"r1": {Spent: 700}}},
		Outcomes: map[string]map[string]Outcome{
			"a-1": {"r1": {Status: paused, RequestedBy: "ann@x.io"}},
		},
		Runs: []RunFact{{Slug: "a-1", Run: "r1", StartDay: day, RequestedBy: "ann@x.io"}},
	}
	recs := Rollup(in)
	if len(recs) != 1 {
		t.Fatalf("%d records: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Outcomes[paused] != 1 {
		t.Errorf("the paused run is counted %d times under %q, want once: %v", r.Outcomes[paused], paused, r.Outcomes)
	}
	for _, k := range []string{"succeeded", "failed", "halted", "cancelled", "infra_error", "other"} {
		if r.Outcomes[k] != 0 {
			t.Errorf("the paused run is also counted under %q: %v", k, r.Outcomes)
		}
	}
	// Every key is present whether or not it counted, so a reader of the day's
	// figures reads a zero rather than a missing node.
	if len(r.Outcomes) != len(outcomeKeys) {
		t.Errorf("outcomes hold %d keys, want all %d: %v", len(r.Outcomes), len(outcomeKeys), r.Outcomes)
	}
	// Counted, not dropped: the record is the spending one, so a reader that
	// dropped the status would leave the day's figures short of a run.
	if r.Runs != 1 || r.SpentMicros != 700 {
		t.Errorf("the paused run's own day reads runs=%d spent=%d, want 1 and 700", r.Runs, r.SpentMicros)
	}

	// The rules accept the word, so a paused run's outcome reaches the rollup
	// as itself rather than being rewritten to an infrastructure error at the
	// end of the session (registry.go's Finish).
	if !outcomeStatuses[paused] {
		t.Errorf("outcome statuses do not hold %q, so a paused run's outcome is filed as an infrastructure error", paused)
	}

	// And it survives the wire shape: a record written and read back keeps the
	// paused count under the same key.
	back, err := FromFields(r.ToFields())
	if err != nil {
		t.Fatal(err)
	}
	if back.Outcomes[paused] != 1 {
		t.Errorf("the record read back counts %d paused runs, want 1: %v", back.Outcomes[paused], back.Outcomes)
	}
}
