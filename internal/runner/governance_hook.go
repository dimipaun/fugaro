package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The stage half of governance (plan task E1.2, design ee-boundary-moves.md
// §5.3). Governance is not a package to move: it is fields and branches in
// packages the MIT build keeps, and the tripwires that read them are what
// moves to ee/governance (R2, D521). This file is the seam between the two:
// the runner asks for a stage's own bound and for the decision when that bound
// fires, and this build answers both out of the core until ee/register sets
// the pair.
//
// What does not move is as load-bearing as what does. The halt itself stays
// here — governanceHalt, the checkpoint report, the reason vocabulary — so an
// ee/ hook cannot invent a stop the core does not know how to record
// (D521, D529). And the ordering the halt path needs stays with the caller:
// the decision is taken on the run goroutine, after the stage's spend and
// history are recorded, so a cancel that landed first still wins (§5.3).

// StageView is what the runner hands a stage hook: the stage that is starting
// or that just ended, the governance the run resolved, and the run's own bound
// on it. The four fields below Stage are the facts a decision is taken on, and
// each is set only for the question it belongs to: a hook reads them and never
// writes them.
type StageView struct {
	Stage      string
	Governance policy.Governance
	RunStage   time.Duration
	// Stop is the stop the gateway's stage limit recorded for the stage that
	// just ended (T7.2's stage budgets, review ratio and fan-out), the zero
	// value when it recorded none. It is the gateway's own record of the
	// stop, carried whole: the reason it refused the stage with and the
	// figures it saw.
	Stop gateway.StageStop
	// ReviewScope is the comparison between the paths the run's own commits
	// changed and the paths the review stage about to run reads (diffbase.go,
	// readReviewScope), nil when the run has no comparison to hand — no
	// checkout, no record, or a range that was never narrowed. It is taken
	// again for each review round, so a fix round that moved the branch off
	// the commit the range was narrowed to is in it (reviewScopeNow).
	ReviewScope *runstore.ReviewScope
	// Narrowed and ReviewRev are the two revisions that comparison was read
	// over: the commit the run's own range was narrowed to ("" when it was
	// not) and the revision the review stage reads. They are what a stop's
	// detail is made of, since the comparison itself carries paths and not
	// the range they were read from.
	Narrowed  string
	ReviewRev string
}

// StageDecision is what a tripwire decided: whether to stop the stage, and the
// halt it leaves. The halt itself (governanceHalt, the checkpoint report)
// stays core.
type StageDecision struct {
	Halt   bool
	Reason runstore.HaltReason
	Detail string
	// Checkpoint is whether the stop's checkpoint report is answered by the
	// stopping agent: the runner runs the checkpoint stage after it, which
	// fills done, next and hard_instructions from a fresh session, and the
	// report's other fields are the runner's own (T7.2, T7.3). A stop whose
	// decision leaves it false keeps the runner's own fields alone, which is
	// every stop no checkpoint call may follow — a stage's own timeout, the
	// review-scope stop, and every stop of a run with no gateway.
	Checkpoint bool
}

// StageFuncs is the runner's stage hook. The core default is the zero quad —
// no bound and no halt — so an untagged build applies no governance to a stage
// whatever its layers wrote (D522, G10).
type StageFuncs struct {
	// Timeout is the stage's own wall-clock ceiling; 0 means none.
	Timeout func(v StageView) time.Duration
	// TimedOut is the decision when that ceiling fires.
	TimedOut func(v StageView) StageDecision
	// Stopped is the decision when the gateway's stage limit stopped a stage:
	// the gateway refuses the stage's calls and the runner asks here whether
	// that stop is a governance halt it records (T7.3). The view's Stop is
	// the gateway's own record of it, and the reason the gateway refused with
	// is what the halt's reason is unless the hook names another.
	Stopped func(v StageView) StageDecision
	// Before is the decision before a review stage runs, over the comparison
	// the review range is read from: the review-scope stop (T7.3). It is
	// asked once per review round, on the run goroutine, before the round's
	// agent call.
	Before func(v StageView) StageDecision
}

// stageHooks is the installed pair. Only ee/register may call SetStage: a
// second caller could halve the enforcement a tagged binary registered, and
// the core never sets it, so an untagged build bounds no stage by governance.
var stageHooks = coreStageFuncs()

// stageHooksSet records that a caller installed a pair, which only SetStage
// does. The test-only seam that installs the untagged build's pair clears it
// again, so the rule this enforces is "one production caller", not "one
// caller in the process".
var stageHooksSet bool

// SetStage installs the runner's stage hook, refusing a second call: the
// edition of a build is decided once, by ee/register, and a hook that could be
// replaced after it would be a way to drop enforcement from a tagged binary.
// A set with a nil function is refused for the same reason: the loop calls
// every one of them, and a nil one is a panic rather than a decision.
func SetStage(f StageFuncs) error {
	if stageHooksSet {
		return errors.New("runner: the stage hook is already set")
	}
	if f.Timeout == nil || f.TimedOut == nil || f.Stopped == nil || f.Before == nil {
		return errors.New("runner: the stage hook needs Timeout, TimedOut, Stopped and Before")
	}
	stageHooks, stageHooksSet = f, true
	return nil
}

// stageHook is the installed pair, read at every stage.
func stageHook() StageFuncs { return stageHooks }

// governanceHaltReasons are the reasons a stage hook may leave: the five
// tripwires D521 keeps in the core, the ones result.schema.json's halt enum
// carries and haltReasonText has words for. A hook that named anything else
// would put a value no reader of the record knows into result.json, and the
// halt would have no account of itself, so the vocabulary is checked rather
// than trusted (D521, D529).
var governanceHaltReasons = map[runstore.HaltReason]bool{
	runstore.HaltStageBudget:      true,
	runstore.HaltReviewRatio:      true,
	runstore.HaltFanout:           true,
	runstore.HaltReviewScope:      true,
	runstore.HaltExpectedExceeded: true,
}

// ignoredGovernanceLine is what a build says once, in the run log, about a
// governance bound it was handed and does not apply: a statement about what
// this build does with the block, and never a notice about a licence (D522,
// D523).
const ignoredGovernanceLine = "this build ignores the governance block"

// noteIgnoredStageBound writes that line when the run's own blocks asked for a
// bound on this stage and the stage hook applied none. Silence about a switch
// somebody set is what D522 exists to prevent, and the run log is where the
// person who wrote stage_timeout will look.
//
// What was asked is read off the layers the merge consumed and not off the
// effective policy, because a build whose hooks ignore governance resolves no
// governance at all: the effective policy cannot tell a block that was written
// from one that was not, and a line that fired for every run would be a lie
// about a run nobody configured. A run whose blocks asked for nothing gets
// nothing, in every build, and a build that applies a bound for this stage
// says nothing either — the value speaks for itself, and the same key of
// result.json's policy.ignored carries the bound that was dropped for being
// looser than the run's own (noteStageTimeoutClamped).
func (r *run) noteIgnoredStageBound(stage string, applied time.Duration) {
	if applied != 0 || r.d.Log == nil || !stageBoundAsked(r.govLayers, stage) {
		return
	}
	r.mu.Lock()
	said := r.ignoredGovernanceSaid
	r.ignoredGovernanceSaid = true
	r.mu.Unlock()
	if !said {
		r.d.Log.Warn(ignoredGovernanceLine)
	}
}

// stageBoundAsked reports whether any of the layer blocks a run merged asked
// for a wall-clock bound on this stage, whatever the merge made of them.
func stageBoundAsked(layers []policy.Layer, stage string) bool {
	for _, l := range layers {
		if stageTimeoutOf(l.Governance, stage) > 0 {
			return true
		}
	}
	return false
}

// coreStageFuncs is the untagged build's stage decision: no bound of its own
// and no halt. A stage runs under the run's own timeouts.stage alone, and the
// governance block the run resolved is recorded as ignored rather than
// enforced (D522, §5.3). A build that links ee/register replaces the set with
// the tripwire ee/governance carries.
//
// The set is still a set of functions and not nil: the stage loop calls every
// one of them, so the default has to answer, and the answer is that this build
// has no stage bound to apply and no stop to leave — not for a stage's own
// ceiling, not for a stop the gateway made, and not before a review stage.
func coreStageFuncs() StageFuncs {
	return StageFuncs{
		Timeout:  func(StageView) time.Duration { return 0 },
		TimedOut: func(StageView) StageDecision { return StageDecision{} },
		Stopped:  func(StageView) StageDecision { return StageDecision{} },
		Before:   func(StageView) StageDecision { return StageDecision{} },
	}
}

// stageView is what a stage's hook is handed: the stage's own name, the
// governance the one merge resolved, and the run's own timeouts.stage, which
// bounds every stage whatever its governance says.
func (r *run) stageView(stage string) StageView {
	return StageView{Stage: stage, Governance: r.policy.Governance, RunStage: r.budget.Stage}
}

// gatewayStop records the stop the gateway's stage limit made as the halt the
// run ends on (T7.3). The stage hook decides whether the stop is a governance
// halt it knows how to record; a stop it leaves alone is the gateway's own
// halt, which is what every stop was before the hook existed.
//
// The reason is the core's to check, exactly as it is for a stage's own
// timeout (stageOnce): a hook that named a reason outside the vocabulary
// result.schema.json carries would put a value no reader of the record knows
// into result.json, and the halt would have no account of itself. Here the
// gateway's own halt is recorded instead — the stop is real whatever the hook
// made of it, and a run must never be left running by a hook that misbehaved.
//
// It reports whether the halt recorded is the one that stopped the run, the
// way haltNow does.
func (r *run) gatewayStop(ctx context.Context, stage string, gh gateway.Halt) bool {
	v := r.stageView(stage)
	v.Stop = gateway.StageStop{Reason: gh.Reason, Detail: gh.Detail}
	d := stageHook().Stopped(v)
	if !d.Halt {
		return r.haltNow(gatewayHalt(gh))
	}
	if !governanceHaltReasons[d.Reason] {
		r.d.Log.Error("the stage hook left the halt reason of a gateway stop outside the vocabulary the record carries; the gateway's own halt is recorded instead",
			"stage", stage, "reason", string(d.Reason), "gateway_reason", gh.Reason)
		return r.haltNow(gatewayHalt(gh))
	}
	if !r.governanceHalt(ctx, d.Reason, d.Detail) {
		return false
	}
	if d.Checkpoint {
		r.wantCheckpointCall()
	}
	return true
}

// beforeReview asks the stage hook whether the review stage about to run may
// run at all, over the comparison that round's range is read from (T7.3): the
// review-scope stop. It reports whether the run may go on.
//
// The comparison is read here, for the round about to run, and not taken off
// the record as T5.2 wrote it after implement: the record's is a statement
// about the range that run had, and a fix round can move the branch off the
// commit the range was narrowed to (reviewScopeNow, diffbase.go). The stop is
// about the range this round would be handed.
//
// A hook that names a reason outside the vocabulary fails the stage, as the
// stage's own timeout does: nothing else can be recorded for it.
func (r *run) beforeReview(ctx context.Context, stage string) bool {
	v := r.stageView(stage)
	v.Narrowed, v.ReviewRev = r.narrowed(), r.reviewRev(ctx)
	v.ReviewScope = r.reviewScopeNow(ctx, v.Narrowed, v.ReviewRev)
	d := stageHook().Before(v)
	if !d.Halt {
		return true
	}
	if !governanceHaltReasons[d.Reason] {
		r.fail(fmt.Sprintf("stage %s: the stage hook left the halt reason %q, which is not a governance stop this runner can record", stage, d.Reason))
		return false
	}
	return r.governanceHalt(ctx, d.Reason, d.Detail)
}

// wantCheckpointCall records that the halt just recorded is one the stopping
// agent answers: the checkpoint stage runs after the mode loop for it, and
// only for it (T7.2, T7.3).
func (r *run) wantCheckpointCall() {
	r.mu.Lock()
	r.checkpointCall = true
	r.mu.Unlock()
}

// checkpointWanted is whether the halt this run ended on left a checkpoint
// report for the stopping agent to answer.
func (r *run) checkpointWanted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkpointCall
}

// StageTimeoutOf is stageTimeoutOf for the packages outside this one: the
// stage hook's own Timeout is ee/governance's after the tripwires move, and it
// answers the bound this way — off the governance the view carries — while the
// function itself stays in the core, where the untagged build's ignored-block
// line reads the same mapping out of the layers it merged (stageBoundAsked,
// D522). One mapping, so the bound a stage is given and the bound the core
// says was asked for cannot disagree.
func StageTimeoutOf(g policy.Governance, stage string) time.Duration {
	return stageTimeoutOf(g, stage)
}

// stageTimeoutOf is a stage's own wall-clock ceiling off the governance a view
// carries: the effective stage_timeout of the one merge every governance key
// goes through, so the project layer's block is the ceiling, the default
// branch's file and the repository's own may only tighten it, and a value that
// would loosen an upper layer is recorded in result.json and warned about like
// any other (policy.Merge, PolicyKeyPath). Zero means the run resolved none,
// and the stage is bounded by the run's own timeouts.stage alone.
//
// Only the three stages governance bounds have one: an artifact run's plan and
// render steps are not among them (T7.1).
func stageTimeoutOf(g policy.Governance, stage string) time.Duration {
	t := g.StageTimeout
	switch stageTimeoutKey(stage) {
	case policy.KeyStageTimeoutImplement:
		return t.Implement
	case policy.KeyStageTimeoutReview:
		return t.Review
	case policy.KeyStageTimeoutFix:
		return t.Fix
	}
	return 0
}
