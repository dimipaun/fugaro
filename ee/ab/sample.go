// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab is model A/B testing (docs/model-ab-testing): which of a
// project's first runs are paired with a challenger, which model that
// challenger may be, how the pair is launched, and what its two records
// score. A sampled launch starts the run the developer gets — the champion,
// which publishes exactly as an unsampled run does — beside a shadow
// challenger: the same task, at the same commit, on another coder model,
// which pushes nothing and opens nothing (D554, D555).
//
// What is here is what the core does not carry. Nothing outside ee/ imports
// this package, and a build without it samples nothing at all: no launch
// planner is registered for one (A3.0), and a task carrying an `ab` block is
// refused by a runner that predates the field rather than run as a
// publishing challenger.
//
// This file is the sampler (plan task A3.1): the three questions a launcher
// asks before it writes a pair's two tasks — may this launch be sampled at
// all (Eligible), does its run ID land under the project's rate (Sampled),
// and has the repository already been sampled for as many pairs as its day
// allows (UnderDailyCount). Nothing here launches anything and nothing here
// writes a default (R3): a sample is a question, and the pair launch (A3.3)
// is what acts on the answer.
package ab

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// Sampled reports whether a run ID is drawn at rate (D557): the share of
// eligible runs a project pairs with a challenger, written in the project
// layer as ab.rate and never above config.ABMaxRate.
//
// The draw is a hash of the run ID and never a random number, so the same ID
// answers the same way for every reader, at every moment: a launch that is
// read twice — by the launcher, by an auditor, by a later reader of the
// records — is sampled the same way both times, and a person auditing one
// recomputes the answer from the ID alone: sha256 of the run ID, its first
// eight bytes read big-endian as a fraction of 2^64, sampled when that
// fraction is below the rate.
//
// It is also why an ID a caller chose is not eligible at all (Eligible): a
// draw from a hash is a draw a developer can steer by trying IDs until one
// lands under the rate.
//
// rate 0 — the default, and what a repository's own ab: block may write to
// opt out — samples nothing. A rate of 1 or more samples everything, and a
// rate that is neither (NaN, which no comparison is true for) samples
// nothing: sampling nothing is the direction that spends nothing, and no
// rate outside [0, 1] is one a project layer may write anyway.
func Sampled(runID string, rate float64) bool {
	if !(rate > 0) {
		return false
	}
	if rate >= 1 {
		return true
	}
	sum := sha256.Sum256([]byte(runID))
	return float64(binary.BigEndian.Uint64(sum[:8]))/idSpace < rate
}

// idSpace is 2^64: how many values the eight bytes the draw reads can hold,
// so one value's share of it is the fraction a rate is compared with.
const idSpace = float64(1 << 64)

// LaunchFlags are the launch-time facts D558 excludes that a task cannot
// carry: a run ID the caller chose (fugaro run --run-id), a retry of a stored
// task (--retry), and a best-of-N batch (--attempts).
//
// They are passed beside the task rather than read from it because none of
// the three leaves a trace there: a retry launches the stored task exactly as
// it stands, an attempts batch stamps a batch label a single --batch launch
// carries too, and a chosen run ID has the same shape as a generated one. The
// one the task could never show is also the one that matters most — the
// sample is a hash of the run ID (Sampled), so an ID the caller may choose is
// an ID the caller may choose to land under the rate.
type LaunchFlags struct {
	// ChosenRunID is whether the caller named the run ID itself.
	ChosenRunID bool
	// Retry is whether this launch re-launches the task of a run that stored
	// one and never started.
	Retry bool
	// Attempts is how many arms the launch asks for: 0 or 1 is a single
	// launch, and N above 1 is a best-of-N batch, whose attempts are several
	// runs of one task already.
	Attempts int
}

// Eligible reports whether a launch is one D558 samples: a first run of a
// code task, at POC to Critical, whose run ID nobody chose.
//
// recipeMode is the resolved recipe's mode (recipe.ModeImplement,
// recipe.ModeReview, recipe.ModeArtifact), and "" for a launch that left the
// recipe to the runner, which resolves the repository's own default — an
// implement recipe. mode is the rigor mode the launch resolved
// (rigor.Resolve), which is always one of rigor.Modes.
//
// Both halves of the rule are read here: the flags and the resolved mode the
// launcher knows, and the fields the task carries. The second half is not
// redundant — a task is bucket data, and a replayed or hand-edited one is
// judged by what it says about itself, the way the runner re-checks a
// challenger at bootstrap rather than trusting the task (A2.1) — and it is
// the only half that reads for a launch whose task was written by something
// else.
func Eligible(spec task.Spec, recipeMode string, mode rigor.Mode, flags LaunchFlags) bool {
	if flags.ChosenRunID || flags.Retry || flags.Attempts > 1 {
		return false
	}
	// Hotfix is off the rigor axis and D558 excludes it: a hotfix is rare,
	// time-bound, and its quality bar ("the smallest safe change") is not
	// what a pair measures, so the group it filled would be one that may
	// never reach a minimum sample. A mode outside the vocabulary is not one
	// of the four the axis holds either, and a launch that resolved none is
	// not a launch this build may spend a second run on.
	if !rigor.Known(mode) || mode == rigor.Hotfix {
		return false
	}
	// A code task: an implement recipe. A review run's deliverable is a
	// comment on somebody else's pull requests, and an artifact run's is a
	// file set in the run's bucket; neither makes the change a champion's
	// pull request carries, so neither is a pair anything could score.
	switch recipeMode {
	case "", recipe.ModeImplement:
	default:
		return false
	}
	// The task's own half of the same rules, field by field. A follow-up
	// continues a pull request rather than making a first attempt (D558: a
	// pair of them would compare fixes to different first attempts), and a
	// triage follow-up is work an earlier run left behind rather than a task
	// a developer launched — the conservative reading of "first runs of a
	// code task", and the one that spends nothing on a task nobody asked
	// for.
	if spec.PR != 0 || spec.Branch != "" || spec.PreviousRun != "" || spec.FollowUpOf != nil {
		return false
	}
	if len(spec.ReviewPRs) > 0 || len(spec.Inputs) > 0 {
		return false
	}
	// A report run reads this repository's own run records: its manifest is
	// accepted under no other recipe (task.Validate), and its recipe's text
	// is the other way a task says it is one — the two the retry path reads,
	// and for the same reason. A repository recipe carries its name alone
	// (its YAML is empty), so a report run launched from one is caught by
	// its manifest alone, which is the only marker such a task has.
	if len(spec.DigestRuns) > 0 || (spec.Recipe != nil && task.RecipeUsesDigest(spec.Recipe.YAML)) {
		return false
	}
	// The recipe's mode as the launcher wrote it into the task: an artifact
	// launch carries it so the runner selects the run's shape before it
	// resolves the recipe itself, and nothing else is written (implement and
	// review leave it empty).
	if spec.Mode != "" && spec.Mode != recipe.ModeImplement {
		return false
	}
	return true
}

// UnderDailyCount reports whether a repository may still be sampled today
// (D557): n is how many challenger records today's launches already wrote
// under the repository's prefix of the runs bucket, as the launcher counted
// them, and max is the project layer's ab.max_pairs_per_day
// (config.AB.MaxPairs, which fills in the count Fugaro ships).
//
// It is a soft cap by construction: the count is the caller's read of the
// bucket and reserves nothing, so two launches at the same moment can both
// pass it and the day can overshoot by however many launches are in flight.
// That is the whole of the design — no counter of ours is written anywhere,
// and the overshoot is bounded by how many launches a person can start at
// once — and the per-run cap and the daily cap in the budget database are
// what bound the money either way (D566).
//
// max at or below 0 is no room at all: the launcher passes
// config.AB.MaxPairs(), which never answers 0, so a 0 here is a caller that
// resolved nothing, and sampling nothing is the direction that spends
// nothing.
func UnderDailyCount(n, max int) bool { return n < max }
