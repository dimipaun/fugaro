// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Timeout is the stage's own wall-clock ceiling, read off the governance the
// run resolved for it: the effective stage_timeout of the one merge every
// governance key goes through, so the project layer's block is the ceiling and
// the default branch's file and the repository's own may only tighten it. Zero
// means the run resolved none, and the stage is bounded by the run's own
// timeouts.stage alone.
//
// It is the pair the runner's stage loop already performed before the move,
// and it is one function with runner.StageTimeoutOf rather than a second copy
// of the mapping: the bound a stage is given and the bound the core's
// ignored-block line reads out of the layers it merged have to agree (D522).
func Timeout(v runner.StageView) time.Duration {
	return runner.StageTimeoutOf(v.Governance, v.Stage)
}

// TimedOut is the decision when that ceiling fires: the stage_budget halt by
// time, which the core records, reports and words (governanceHalt, the
// checkpoint report). The halt itself stays core, so a hook cannot invent a
// stop the core does not know how to record (D521, D529).
//
// Timeout and TimedOut are one tripwire and are registered as one pair: a
// ceiling nobody stops at would be no ceiling.
func TimedOut(runner.StageView) runner.StageDecision {
	return runner.StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}
}

// Stopped is the decision when the run's gateway stopped a stage (T7.2, T7.3):
// the per-stage budgets, the review ratio and the fan-out are the guardrails
// G7 names as stops a person decides about rather than silent kills, so the
// halt they leave carries a checkpoint report and the draft pull request that
// goes with it (Checkpoint true), and the runner asks the stopping agent for
// the report's three model-written fields after it.
//
// The stop arrives whole in v.Stop: the gateway's own reason for it and the
// figures it saw. The reason is the halt's unless this function names another,
// and it is the gateway's sentence that is carried as the detail — the halt's
// detail is what a reader of the record has to explain the stop with, and the
// gateway counted the dollars, the calls and the sub-agents the sentence names.
//
// Only the three governance reasons get a checkpoint call. A stop outside them
// is not this hook's to decide: it is returned unhalted, which leaves the
// gateway's own halt to the core (governance_hook.go, gatewayStop), and the
// core's vocabulary check would refuse a reason this hook invented anyway.
func Stopped(v runner.StageView) runner.StageDecision {
	reason := runstore.HaltReason(v.Stop.Reason)
	switch reason {
	case runstore.HaltStageBudget, runstore.HaltReviewRatio, runstore.HaltFanout:
	default:
		return runner.StageDecision{}
	}
	return runner.StageDecision{Halt: true, Reason: reason, Detail: v.Stop.Detail, Checkpoint: true}
}

// Before is the decision before a review round runs: the review-scope stop
// (T5.2, T7.3). A range that was narrowed to the run's own commits and then
// moved off them hands the reviewer files the run never changed, which is the
// one comparison that is a mismatch rather than a fact about the run, and the
// round is not bought at all.
//
// What it reads is the record's own review_scope, re-read for the round about
// to run (diffbase.go, reviewScopeNow): Applicable is the field the stop is
// made on, and WhyNot is why it is false. Every other reason a comparison does
// not apply is a range kept wider on purpose — the wide range of a branch that
// merged its base in, a follow-up's, a review run's, an autofix's, a git too
// old to say which commits are the run's, a run with no commits of its own —
// and is a fact about the run and never a mismatch (D332). A comparison that
// could not be read at all is handed over as nothing, and an absent comparison
// is never an empty one (G5): the round runs.
//
// A moved range that handed the reviewer nothing implement had not written is
// not a mismatch either, which is the same rule the digest's review-scope flag
// is raised under (internal/digest, T1.4): the two read one record, and a stop
// that fired where the report is silent would be a stop nothing explains.
// A truncated comparison is the same case — Outside is taken over two bounded
// lists, so a path it names may be one the run did change.
func Before(v runner.StageView) runner.StageDecision {
	sc := v.ReviewScope
	if sc == nil || sc.Applicable || sc.Truncated {
		return runner.StageDecision{}
	}
	if sc.WhyNot != runstore.ReviewScopeRangeMoved || len(sc.Outside) == 0 {
		return runner.StageDecision{}
	}
	return runner.StageDecision{
		Halt:   true,
		Reason: runstore.HaltReviewScope,
		Detail: reviewScopeDetail(v, sc),
	}
}

// reviewScopeDetail is the halt's own sentence for a moved review range: the
// two revisions the comparison was read over and how many of the range's paths
// the run never wrote. The count and the revisions are the runner's own — a
// commit SHA, a count and a validated branch name — and never a path, so
// nothing a repository named reaches the record's halt detail through here.
//
// A path list is deliberately not quoted: the paths are already in the record
// and on the digest's flag, the detail is one line, and the reviewer that would
// read them has been stopped rather than handed them.
func reviewScopeDetail(v runner.StageView, sc *runstore.ReviewScope) string {
	range_ := "the recorded base"
	if v.ReviewRev != "" {
		range_ = v.ReviewRev
	}
	return fmt.Sprintf("the review range starts at %s, not at the commit %s the run's own commits start after: %d path(s) in the range were never changed by this run",
		range_, v.Narrowed, len(sc.Outside))
}
