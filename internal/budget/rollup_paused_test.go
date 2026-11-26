package budget

import (
	"reflect"
	"sort"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRollupCountsPausedApart is model-credentials-routing PR11's half for this
// package: a run that suspended itself to be resumed by its chain
// (contract-pause, R15) is neither a failure nor a success, so the day's
// figures count it apart from both — in the bucket a status outside the six
// lands in, which is where the rest of the vocabulary's readers put it
// (internal/userstats' `Other`, the digest's "no class").
//
// It is counted, not dropped, and it is not a seventh key. That second half is
// the load-bearing one: the day record's shape is the shape every document
// already stored has, and the rollover's prune reads a stored document back
// against its own derivation and never rewrites one that is final
// (rollover.go's writeDay, figuresEqual). A seventh key would leave every
// document written before the pause unequal to its derivation, so the day would
// never be retired and the daily job would refuse it forever, with no migration
// in `fugaro update`. R15 names what gains the word — the result schema, the
// budget rules and the console's set — and the rules gain it in PR1: the
// outcome node carries `paused`, and this package files that outcome as itself
// rather than rewriting it.
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
	if r.Outcomes["other"] != 1 {
		t.Errorf("the paused run is counted %d times in %q, want once: %v", r.Outcomes["other"], "other", r.Outcomes)
	}
	for _, k := range []string{"succeeded", "failed", "halted", "cancelled", "infra_error", paused} {
		if n := r.Outcomes[k]; n != 0 {
			t.Errorf("the paused run also counts as %q: %d in %v", k, n, r.Outcomes)
		}
	}
	// The vocabulary is the one every stored document already has: six keys,
	// each present whether or not it counted, so a reader of the day's figures
	// reads a zero rather than a missing node.
	if len(outcomeKeys) != 6 || len(r.Outcomes) != len(outcomeKeys) {
		t.Errorf("the day's figures hold %d keys over a vocabulary of %d, want six: %v", len(r.Outcomes), len(outcomeKeys), r.Outcomes)
	}
	// Counted, not dropped: the record is the spending one, so a reader that
	// dropped the status would leave the day's figures short of a run.
	if r.Runs != 1 || r.SpentMicros != 700 {
		t.Errorf("the paused run's own day reads runs=%d spent=%d, want 1 and 700", r.Runs, r.SpentMicros)
	}

	// The rules accept the word, so a paused run's outcome reaches the rollup
	// as itself rather than being rewritten to an infrastructure error at the
	// end of the session (registry.go's Finish) — which is the difference
	// between a pause nothing has to look at and a fault that never happened.
	if !outcomeStatuses[paused] {
		t.Errorf("outcome statuses do not hold %q, so a paused run's outcome is filed as an infrastructure error", paused)
	}

	// And the wire shape a paused day writes is the shape every other day
	// writes — the same keys — so the prune's read-back compares like with like.
	back, err := FromFields(r.ToFields())
	if err != nil {
		t.Fatal(err)
	}
	if back.Outcomes["other"] != 1 || len(back.Outcomes) != len(r.Outcomes) {
		t.Errorf("the record read back counts %v over %d keys, want the paused run in %q over %d keys",
			back.Outcomes, len(back.Outcomes), "other", len(r.Outcomes))
	}
	sameDay := in
	sameDay.Outcomes = map[string]map[string]Outcome{
		"a-1": {"r1": {Status: string(runstore.StatusSucceeded), RequestedBy: "ann@x.io"}},
	}
	other := Rollup(sameDay)
	if len(other) != 1 {
		t.Fatalf("%d records for the same day ending succeeded: %+v", len(other), other)
	}
	if got, want := documentKeys(r.ToFields()), documentKeys(other[0].ToFields()); !reflect.DeepEqual(got, want) {
		t.Errorf("a paused day's document carries %v and the same day ending succeeded carries %v: the pause moves no shape", got, want)
	}
}

// documentKeys names the keys of a document's outcomes object, in order.
func documentKeys(fields map[string]any) []string {
	outs, _ := fields["outcomes"].(map[string]any)
	keys := make([]string, 0, len(outs))
	for k := range outs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
