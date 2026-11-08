package runner

import (
	"context"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// stopForTime is the loop's check before it starts another review or fix
// round: past the run's review share (Budget.ReviewBudgetSpent, BF1) it
// records what the last round left open and answers true, and the caller
// returns false instead of buying the round.
//
// It is the one site that turns a spent budget into a record, and it is asked
// at every round the loop would start after its first: before the fix round a
// changes verdict asks for — which is also before the review round that fix
// would be followed by, since a round after the first is only ever reached
// through a fix round — and before the cheap loop a bounce starts. A stage
// the finalize reserve cut mid-round is not this: that one still fails the run
// with "time budget exhausted during <stage>" (StageError), because a round
// that started is a round the run promised, and this check only ever runs
// between two of them.
func (r *run) stopForTime(ctx context.Context) bool {
	if !r.budget.ReviewBudgetSpent() {
		return false
	}
	round := r.lastSenior.Round
	r.rec.OpenFindings = r.openRecord(round)
	r.save(ctx)
	r.d.Log.Warn("the review budget is spent: no further review round starts, and the run finalizes with the findings still open",
		"round", round, "open", r.rec.OpenFindings.Total, "review_budget_pct", r.budget.reviewPct())
	return true
}

// openRecord is the record's block for a run that stopped at its review
// share (BF1): the last round's findings, each with the word its score reads
// and whether the path the reviewer named is one of the repository's own
// security paths.
//
// Both are read here rather than by a later reader: the label is derived from
// the score and never stored as the reviewer wrote it (D2), and the security
// floor is the live one this run's own build holds
// (internal/orchestrator/sensitivity), which a reader of the record — a
// console, a coordinator — has no way to reach.
func (r *run) openRecord(round int) *runstore.OpenFindings {
	floor := sensitivity.Floor()
	findings := make([]runstore.OpenFinding, 0, len(r.lastSenior.Findings))
	for _, f := range r.lastSenior.Findings {
		findings = append(findings, runstore.OpenFinding{
			Title:      r.redact(f.Summary),
			File:       r.redact(f.File),
			Sensitive:  sensitivePath(f.File, floor),
			Likelihood: f.Likelihood,
			Impact:     f.Impact,
			FixCost:    f.FixCost,
			Class:      rigor.Class(f.Class),
		})
	}
	return runstore.NewOpenFindings(findings, round, r.budget.reviewPct())
}

// sensitivePath reports whether path is one of the repository's own security
// paths: a path the review floor matches (sensitivity.Floor, the list
// `fugaro doctor --security` reports). A finding that names no path is not
// one: the floor can only be read against a path, and a finding is the
// reviewer's own claim about an untrusted diff, so a claim with nothing to
// check is a claim about no file rather than about every file.
func sensitivePath(path string, floor []sensitivity.Pattern) bool {
	return path != "" && len(sensitivity.Match([]string{path}, floor)) > 0
}
