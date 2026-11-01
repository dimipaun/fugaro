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
// test record on finalSHA with a clean tree passed and the last review either
// shipped or left nothing the run's mode blocks on (T3.1, D7). A clean or
// fixed check gate (gate.go) never reaches Decide: a clean gate ends the run
// as succeeded with outcome none before any PR exists, and a fixed one goes on
// to review as usual.
//
// pol is the mode's findings policy, and the one answer it can change is a
// `changes` verdict whose every finding it tolerates (D9). Every other answer
// is the one this function gave before modes existed, reason string included:
// the reasons above the tolerance are the gates, and a mode cannot make an
// unverified or unreviewed run ready.
//
// A mode raised after the review round the verdict came from is the one
// exception to that tolerance (T3.5): a round the raised mode tolerates is
// still a draft when no review was held at the mode the run ends at, and the
// reason names the raise — the mode, the rule that raised it and the moment
// (D22's third moment is the case a run cannot buy rounds with).
func Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary, pol FindingsPolicy) (bool, string) {
	latest := latestVerifiedTest(records, finalSHA)
	switch {
	case latest == nil:
		return false, ReasonNoVerifiedTest
	case !latest.Passed:
		return false, "tests failing on the final commit"
	case last == nil:
		return false, "no review verdict"
	case last.Verdict == "none":
		// A review round that produced no parseable verdict, after the retry
		// the loop gives it (seniorLoop), is not a tolerance of findings: it
		// asked for nothing, so nothing is fabricated into a blocker, no fix
		// round is spent on it and the mode has no say in it. It is not a
		// review either, and a run nobody read is not one this rule marks
		// ready — the round's answer comes from a diff the run's own coder
		// wrote, and a coder that can make the reviewer answer nothing twice
		// would otherwise have its work shipped unread. The pull request stays
		// a draft and the reason says which round it was (reviewsPart).
		return false, fmt.Sprintf("review round %d returned no parseable verdict after a retry", last.Round)
	case last.Verdict != "ship":
		if pol.tolerates(last) && !pol.raisedAfterReview() {
			return true, fmt.Sprintf("review round %d left %s, tolerated at %s", last.Round, Plural(len(pol.Tolerated), "finding"), pol.Mode)
		}
		if pol.Raised != nil {
			return false, pol.raiseReason(last)
		}
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
	// Raised is the raise the run's mode took during the run, nil when it never
	// rose (T3.5, D19). It is what makes a raised mode decide readiness and
	// name itself in the reason.
	Raised *RigorRaise
}

// RigorRaise is the raise a run's mode took during the run (T3.5, D22): the
// mode it rose to, the rule the moment that raised it matched when it matched
// one, and D22's moment it rose at. The moment that took it records it
// (noteRigorRaise, rigor.go), because the block keeps no per-moment history and
// the moment is what says whether any review round was held at the mode the run
// ends at. A run whose mode never rose has none, which is D19.
type RigorRaise struct {
	// Mode is the mode this raise reached, which is the mode the reason names.
	Mode rigor.Mode
	// Area is the rule the raising moment matched — its own strictest, which
	// D23 makes the deciding one — and "" when the layer that decided it is not
	// an area: a raise by a layer, or by a built-in floor outside every layer
	// (D18), has no rule to name.
	Area string
	// At is D22's moment the raise happened at: a round, or finalize, the one
	// moment no review round follows.
	At string
}

// raisedAfterReview reports whether the mode rose after the last review round:
// D22's third moment, before the pull request is marked ready. Tolerating that
// round's findings would make the pull request ready on a review nobody held at
// the mode the run is judged by, which is what D22's "too late to buy rounds"
// forbids.
func (pol FindingsPolicy) raisedAfterReview() bool {
	return pol.Raised != nil && pol.Raised.At == runstore.RigorAtFinalize
}

// raiseReason is Decide's reason for a run its raised mode keeps a draft: the
// raise first — the mode, the rule that raised it and the moment — and, when
// the raised mode does not tolerate the round either, the round's own count
// ahead of it. A reader of the reason can go and look at the rule the run's own
// work matched rather than guess what changed.
func (pol FindingsPolicy) raiseReason(last *runstore.ReviewSummary) string {
	reason := fmt.Sprintf("the mode was raised to %s", pol.Raised.Mode)
	if pol.Raised.Area != "" {
		reason += fmt.Sprintf(" by area %s", pol.Raised.Area)
	}
	if pol.Raised.At == runstore.RigorAtFinalize {
		reason += " at finalize, after the last review round"
	} else {
		reason += " after a review round"
	}
	if !pol.tolerates(last) {
		reason = fmt.Sprintf("review round %d still has %s, and %s", last.Round, Plural(last.Findings, "finding"), reason)
	}
	return reason
}

// tolerates reports whether the mode lets the whole round through: the split
// Decide was given is the one that round produced, every finding the record
// counts sits in its tolerated half, and there is at least one.
//
// The count is checked against the record rather than trusted, and a round
// that left no finding is not tolerated: a `changes` verdict that names
// nothing is a review which failed to say what it wanted, and D4's rule is
// that a policy which cannot decide blocks.
func (pol FindingsPolicy) tolerates(last *runstore.ReviewSummary) bool {
	return last.Findings > 0 &&
		pol.Round == last.Round &&
		len(pol.Blocking) == 0 &&
		len(pol.Tolerated) == last.Findings
}

// findingsPolicyOf is the policy Decide is given for one review round: the
// mode's split of that round's findings, or the zero policy where the run may
// not tolerate anything at all.
//
// D9 is the bound. A mode may not let a finding through where the repository
// has not asked for the mode, so the split is taken only where both hold: the
// repository opted in (rigor.Config.Configured — an opt-in read from the base
// branch, D25) and the mode's rigor.Files row tolerates. Every other run gets
// the zero policy and decides by the count on the record, exactly as it did
// before modes existed — the unconfigured repository, which D19 resolves Pro
// for its budget, and POC, whose account of what it left open is its pull
// request body's own line (D30). A tolerated finding is recorded in the run's
// record (Tolerated, T3.4) and named in the pull request's status section,
// which is all the run does with it: nothing is written into the repository
// for it (finalize).
func findingsPolicyOf(cfg rigor.Config, mode rigor.Mode, round int, findings []rigor.Score) FindingsPolicy {
	if !cfg.Configured() || !rigor.Files(mode).Tolerated {
		return FindingsPolicy{}
	}
	blocking, tolerated := rigor.Split(mode, findings)
	return FindingsPolicy{Mode: mode, Round: round, Blocking: blocking, Tolerated: tolerated}
}

// findingsPolicy is findingsPolicyOf for this run: the last senior review
// round as the loop read it, the mode the run resolved, the rigor
// configuration that mode was read from, and the raise the run's mode took
// during it (noteRigorRaise, rigor.go).
func (r *run) findingsPolicy() FindingsPolicy {
	pol := findingsPolicyOf(r.rigorCfg, r.reachedRigor(), r.lastSenior.Round, scoresOf(r.lastSenior.Findings))
	pol.Raised = r.rigorRaise
	return pol
}

// toleratedRecord is what the run's record keeps of the findings the mode let
// through (docs/rigor-modes/implementation-plan.md T3.4, D9, F19): one entry
// per tolerated finding, with the reviewer's own words for it. It is nil when
// the policy tolerates nothing, so the record carries no block at all rather
// than an empty one.
//
// The pairing is the split's own, without asking the table a second time.
// rigor.Split appends a finding's score to Tolerated in the reviewer's order,
// and whether a finding blocks is a function of its score alone (rigor.Blocks
// reads nothing else), so a tolerated score is tolerated at every finding that
// carries it: walking the round's findings and taking each tolerated score at
// the next finding it equals pairs the two lists exactly, and the record
// cannot disagree with the readiness rule about which findings were tolerated.
func (r *run) toleratedRecord(pol FindingsPolicy) *runstore.ToleratedFindings {
	if len(pol.Tolerated) == 0 {
		return nil
	}
	entries := make([]runstore.ToleratedFinding, 0, len(pol.Tolerated))
	next := 0
	for _, f := range r.lastSenior.Findings {
		if next == len(pol.Tolerated) {
			break
		}
		s := scoreOf(f)
		if s != pol.Tolerated[next] {
			continue
		}
		entries = append(entries, runstore.ToleratedFinding{
			Title:      r.redact(f.Summary),
			Mode:       pol.Mode,
			Likelihood: s.Likelihood,
			Impact:     s.Impact,
			FixCost:    s.FixCost,
			Class:      s.Class,
		})
		next++
	}
	return runstore.NewTolerated(entries)
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
		out = append(out, scoreOf(f))
	}
	return out
}

// scoreOf is one finding as the table reads it.
func scoreOf(f Finding) rigor.Score {
	return rigor.Score{Likelihood: f.Likelihood, Impact: f.Impact, FixCost: f.FixCost, Class: rigor.Class(f.Class)}
}

// Plural formats "1 finding" / "3 findings".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
