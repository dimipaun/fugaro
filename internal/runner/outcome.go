package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// latestVerifiedTest returns the last test record on sha with a clean tree.
func latestVerifiedTest(records []verify.Record, sha string) *verify.Record {
	var latest *verify.Record
	for i := range records {
		r := &records[i]
		if r.Kind == verify.KindTest && r.HeadSHA == sha && r.CleanTree {
			latest = r
		}
	}
	return latest
}

// ReasonNoVerifiedTest is Decide's reason when no clean-tree test record
// exists for the final commit.
const ReasonNoVerifiedTest = "no verified test run on the final commit"

// Decide applies the PR outcome rule (design §4.2): ready only if the latest
// test record on finalSHA with a clean tree passed and the last review
// shipped. A clean or fixed check gate (gate.go) never reaches Decide: a
// clean gate ends the run as succeeded with outcome none before any PR
// exists, and a fixed one goes on to review as usual.
func Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary, pol FindingsPolicy) (bool, string) {
	latest := latestVerifiedTest(records, finalSHA)
	switch {
	case latest == nil:
		return false, ReasonNoVerifiedTest
	case !latest.Passed:
		return false, "tests failing on the final commit"
	case last == nil:
		return false, "no review verdict"
	case last.Verdict != "ship":
		return false, fmt.Sprintf("review round %d still has %s", last.Round, Plural(last.Findings, "finding"))
	}
	return true, ""
}

// FindingsPolicy is the mode's findings policy as Decide is given it
// (docs/rigor-modes/implementation-plan.md T3.1): the last senior review
// round's findings, split by the mode's threshold table (rigor.Split, D2,
// D7a), and the round they were split from.
//
// It is how the mode's thresholds act without readiness gaining a second
// answer (F4): the split is computed at the call site, from the table in
// internal/rigor, and Decide is the only code that reads it.
type FindingsPolicy struct {
	// Mode is the mode the run resolved (D22's fold). A tolerated round's
	// reason names it, so the record says which mode let the findings through.
	Mode rigor.Mode
	// Round is the review round the split was made from. Decide checks it
	// against the summary it was given, so a split from another round is a
	// policy it cannot read — and a policy that cannot decide blocks (F3).
	Round int
	// Blocking and Tolerated are rigor.Split's two halves of that round's
	// findings, in the reviewer's own order. Both are empty for a run that may
	// tolerate nothing at all: one that configured no rigor, or a mode whose
	// rigor.Files row files no tolerated finding (D9).
	Blocking, Tolerated []rigor.Score
}

// findingsPolicyOf is the policy Decide is given for one review round: the
// mode's split of that round's findings, or the zero policy where the run may
// not tolerate anything at all.
//
// D9 is the bound. A mode may not tolerate a finding it cannot file, so the
// split is taken only where the filing can happen: the repository opted in
// (rigor.Config.Configured, the gate fileOpenItems reads as well) and the
// mode's rigor.Files row files what it tolerates. Every other run gets the
// zero policy and decides by the count on the record, exactly as it did
// before modes existed — the unconfigured repository, which D19 resolves Pro
// for its budget but which files nothing, and POC, whose account is its pull
// request body's own line (D30) and which therefore files nothing either.
func findingsPolicyOf(cfg rigor.Config, mode rigor.Mode, round int, findings []rigor.Score) FindingsPolicy {
	if !cfg.Configured() || !rigor.Files(mode).Tolerated {
		return FindingsPolicy{}
	}
	blocking, tolerated := rigor.Split(mode, findings)
	return FindingsPolicy{Mode: mode, Round: round, Blocking: blocking, Tolerated: tolerated}
}

// findingsPolicy is findingsPolicyOf for this run: the last senior review
// round as the loop read it, the mode the run resolved, and the rigor
// configuration that mode was read from.
func (r *run) findingsPolicy() FindingsPolicy {
	return findingsPolicyOf(r.rigorCfg, r.reachedRigor(), r.lastSenior.Round, scoresOf(r.lastSenior.Findings))
}

// seniorRound is one senior review round as the loop read it: the round number
// the record carries, and the findings the summary beside it only counts. The
// findings are kept whole because the mode's threshold table reads their
// scores, and a count is not enough for it (T3.1, D7a).
type seniorRound struct {
	Round    int
	Findings []Finding
}

// scoresOf presents the reviewer's findings to the threshold table: the three
// anchored axes and the class it reads, never the free-form severity string
// beside them (D2). A finding with no score reads as the zero Score, which
// blocks in every mode (D4).
func scoresOf(findings []Finding) []rigor.Score {
	out := make([]rigor.Score, 0, len(findings))
	for _, f := range findings {
		out = append(out, rigor.Score{Likelihood: f.Likelihood, Impact: f.Impact, FixCost: f.FixCost, Class: rigor.Class(f.Class)})
	}
	return out
}

// Plural formats "1 finding" / "3 findings".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
