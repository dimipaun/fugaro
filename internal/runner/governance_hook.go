package runner

import (
	"errors"
	"time"

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

// StageView is what the runner hands a stage hook: the stage that is starting,
// the governance the run resolved, and the run's own bound on it.
type StageView struct {
	Stage      string
	Governance policy.Governance
	RunStage   time.Duration
}

// StageDecision is what a tripwire decided: whether to stop the stage, and the
// halt it leaves. The halt itself (governanceHalt, the checkpoint report)
// stays core.
type StageDecision struct {
	Halt   bool
	Reason runstore.HaltReason
	Detail string
}

// StageFuncs is the runner's stage hook. Core default: today's implementation,
// so nothing this build enforces changes until the move to ee/governance flips
// it (G10).
type StageFuncs struct {
	// Timeout is the stage's own wall-clock ceiling; 0 means none.
	Timeout func(v StageView) time.Duration
	// TimedOut is the decision when that ceiling fires.
	TimedOut func(v StageView) StageDecision
}

// stageHooks is the installed pair. Only ee/register may call SetStage: a
// second caller could halve the enforcement a tagged binary registered, and
// the core never sets it, so an untagged build enforces exactly what it
// always did.
var stageHooks = coreStageFuncs()

// stageHooksSet records that a caller installed a pair, which only SetStage
// does. The test-only seam that installs the untagged build's pair clears it
// again, so the rule this enforces is "one production caller", not "one
// caller in the process".
var stageHooksSet bool

// SetStage installs the runner's stage hook, refusing a second call: the
// edition of a build is decided once, by ee/register, and a hook that could be
// replaced after it would be a way to drop enforcement from a tagged binary.
// A pair with a nil function is refused for the same reason: the loop calls
// both, and a nil one is a panic rather than a decision.
func SetStage(f StageFuncs) error {
	if stageHooksSet {
		return errors.New("runner: the stage hook is already set")
	}
	if f.Timeout == nil || f.TimedOut == nil {
		return errors.New("runner: the stage hook needs both Timeout and TimedOut")
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

// coreStageFuncs is today's stage decision: the stage's own
// governance.stage_timeout, and the stage_budget halt by time when it fires.
// The two are one pair because they are one tripwire: a ceiling nobody stops
// at would be no ceiling.
func coreStageFuncs() StageFuncs {
	return StageFuncs{
		Timeout: func(v StageView) time.Duration { return stageTimeoutOf(v.Governance, v.Stage) },
		TimedOut: func(StageView) StageDecision {
			return StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}
		},
	}
}

// stageView is what a stage's hook is handed: the stage's own name, the
// governance the one merge resolved, and the run's own timeouts.stage, which
// bounds every stage whatever its governance says.
func (r *run) stageView(stage string) StageView {
	return StageView{Stage: stage, Governance: r.policy.Governance, RunStage: r.budget.Stage}
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
