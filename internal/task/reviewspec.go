package task

import (
	"fmt"
	"slices"
	"strings"
)

// ReviewSpecSource is where the spec a review is checked against came from:
// the three values ReviewSpec.Source may carry (docs/review-against-task
// reconciliation R2). It is a named string type with one exported list, as
// rigor.Rigor and tier.Tier are, because a near miss is a launcher's bug
// rather than a review's opinion about what it was handed.
type ReviewSpecSource string

const (
	// ReviewSpecRun is a pull request a Fugaro run produced: the spec is the
	// root run's stored task, which the launcher read out of the runs bucket
	// and wrote here for the review job to judge against (R1). It is the
	// launcher's claim and not a proof — an earlier run of the same
	// repository can rewrite that record — which is why the artifact's
	// description says so (R16).
	ReviewSpecRun ReviewSpecSource = "run"
	// ReviewSpecFile is a spec a person handed in by hand (fugaro review
	// --spec FILE): the one way an orphan pull request — one no run produced
	// — is reviewed against something (spec §4).
	ReviewSpecFile ReviewSpecSource = "file"
	// ReviewSpecNone is no spec at all: the review judges the code alone, and
	// Reason says why (--code-only, no run found, pick of different tasks,
	// own-step recipe).
	ReviewSpecNone ReviewSpecSource = "none"
)

// ReviewSpecSources is the whole vocabulary, in the order a message names it.
// It is closed the way ABArms is: a value outside it is refused where it is
// written, never matched as an unknown source later.
var ReviewSpecSources = []ReviewSpecSource{ReviewSpecRun, ReviewSpecFile, ReviewSpecNone}

// KnownReviewSpecSource reports whether s is one of ReviewSpecSources. The
// zero source — a review_spec that names none at all — is not: the field is
// required whenever the object is present, which this refusal and the
// schema's own required list say together.
func KnownReviewSpecSource(s ReviewSpecSource) bool { return slices.Contains(ReviewSpecSources, s) }

// MaxReviewSpecBytes bounds each text a review's spec carries
// (ReviewSpec.Text, ReviewSpec.Followup and Spec.OwnText): R4's 16 KiB. The
// three are quoted into an agent's prompt, so each needs a bound of its own —
// task.Spec.Task has none — and the bound is the same for all three because
// they are the same kind of thing: the brief, the follow-up's text and the
// person's own words on a findings follow-up.
//
// The launcher CLIPS a longer root task at a line with a note naming what was
// dropped, as clipBaseInstructions does, so a spec it writes never exceeds
// what Validate accepts; a --spec FILE over the cap is refused at launch with
// its size instead, since a person can trim a file and a stored task cannot
// be (R4).
const MaxReviewSpecBytes = 16 << 10

// ReviewSpec is the spec a launched review is checked against (Spec.
// ReviewSpec, R2): the root task a pull request was written for, the
// follow-up's text beside it, and the plan task whose section of its plan the
// reviewer is also pointed at. The launcher writes it for a launched review —
// `fugaro review` and `fugaro run --recipe review-only`, which share one
// resolver (D2934) — and the review job's runner reads it and nothing else;
// an in-run review carries no block at all, because its runner reads the
// run's own task itself.
//
// Every text here is DATA the reviewer compares the diff against, never an
// instruction it follows (R3): the runner quotes it redacted, cleaned and
// nonce-fenced, and the prompt says what it is.
type ReviewSpec struct {
	// Source is where the spec came from: ReviewSpecRun, ReviewSpecFile or
	// ReviewSpecNone. It is required whenever the block is present.
	Source ReviewSpecSource `json:"source"`
	// Reason is why there is no spec, and it belongs to Source
	// ReviewSpecNone alone: Validate refuses a none source without one. The
	// values a launcher writes are --code-only, no run found, pick of
	// different tasks and own-step recipe (R2, F35).
	Reason string `json:"reason,omitempty"`
	// Run is the root run's ID, present when Source is ReviewSpecRun and
	// refused beside any other source: it names the run whose stored task
	// Text holds, so a reader can say which record the review was judged
	// against (R16).
	Run string `json:"run,omitempty"`
	// Text is the spec itself: the root task's text, or the text of a
	// --spec FILE. Bounded by MaxReviewSpecBytes.
	Text string `json:"text,omitempty"`
	// Followup is the latest follow-up run's own text, added beside Text so a
	// pull request a follow-up wrote is judged against what it was for and
	// not only against the fix instructions (spec §2). Empty when the pull
	// request has no follow-up, and bounded by MaxReviewSpecBytes.
	Followup string `json:"followup,omitempty"`
	// PlanTask is the plan task copied off the root task (Spec.PlanTask),
	// held to PlanTaskProblem's rule here as it is there: when it is set, the
	// reviewer is also pointed at that task's section of its implementation
	// plan (D2932). It is a copy and not a second claim — nothing joins it
	// into a path.
	PlanTask string `json:"plan_task,omitempty"`
}

// ReviewSpecProblem reports what is wrong with rs — "" when nothing is — for
// the two places one is taken in: the launcher, before it writes a task, and
// the runner, reading a stored task back (Spec.Validate). One list with two
// callers, the rule TitleProblem and PlanTaskProblem already follow in this
// package, and for the same reason: a block the launcher accepts has to be
// one the runner accepts, or the launch writes a task the run then refuses at
// bootstrap, bytes in the bucket under a run that never starts.
//
// The rules are R2's: a source from the closed vocabulary; a run source with
// the root run's ID and nothing else; a file source carrying no run id at
// all, since the text beside it is a person's own; a none source with a
// reason and without text; and each text within MaxReviewSpecBytes. A nil
// block is no spec block at all and is valid.
func ReviewSpecProblem(rs *ReviewSpec) string {
	if rs == nil {
		return ""
	}
	switch {
	case !KnownReviewSpecSource(rs.Source):
		return fmt.Sprintf("source %q must be %s, %s or %s", rs.Source, ReviewSpecRun, ReviewSpecFile, ReviewSpecNone)
	case rs.Source == ReviewSpecRun && !ValidRunID(rs.Run):
		return fmt.Sprintf("a run source needs the root run's ID in run, and %q is not one", rs.Run)
	case rs.Source == ReviewSpecFile && rs.Run != "":
		return fmt.Sprintf("a file source carries no run id, and this one names %q", rs.Run)
	case rs.Source == ReviewSpecNone && strings.TrimSpace(rs.Reason) == "":
		return "a none source needs a reason: it is why the review judges the code alone"
	case rs.Source == ReviewSpecNone && rs.Text != "":
		return "a none source carries no text: the review judges the code alone, and the reason says why"
	}
	if p := ReviewSpecTextProblem("text", rs.Text); p != "" {
		return p
	}
	if p := ReviewSpecTextProblem("followup", rs.Followup); p != "" {
		return p
	}
	if rs.PlanTask != "" {
		if p := PlanTaskProblem(rs.PlanTask); p != "" {
			return "plan_task " + p
		}
	}
	return ""
}

// ReviewSpecTextProblem reports what is wrong with one text a review's spec
// carries — "" when nothing is: it is longer than MaxReviewSpecBytes, which
// is R4's cap. It is one function for the three texts because the three are
// the same kind of thing and their cap is the same number; the launcher
// clips rather than refuses the two it reads out of a stored task, and
// refuses the one a person handed in.
func ReviewSpecTextProblem(what, text string) string {
	if len(text) > MaxReviewSpecBytes {
		return fmt.Sprintf("%s is longer than %d bytes", what, MaxReviewSpecBytes)
	}
	return ""
}
