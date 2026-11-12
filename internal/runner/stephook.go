package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// A registered step is a step kind this build parses but does not carry: the
// run's own machinery — the record, the bucket, the gateway, the endings — is
// this package's, and what the step does with it is the build's own code
// (D550). The core's half is this file; `ee/register` installs the function
// and `ee/ab` holds it.
//
// A registered step is reached through a mode: evaluate recipe, whose whole
// shape is one such step and nothing else (recipe.orderEvaluate): no agent, no
// checkout of the repository's own code, no repository command and no pull
// request. The step is handed no repository handle and no git credential, so a
// step cannot run code or push a branch even by mistake — what it may do is
// read the run's own objects in the bucket and make model calls through the
// run's gateway (DirectCall).

// StepCall is one model call a registered step asks for: the request a stage
// would otherwise make through an agent, with nothing an agent brings — no
// tools, no session, no stream.
type StepCall struct {
	// Model is the model to call. The gateway admits it only if the stage
	// DirectCall opens names it (gateway.RequestPin), so a step cannot call a
	// model the run resolved.
	Model string
	// System and Prompt are the two halves of the request's text.
	System, Prompt string
	// MaxTokens bounds the reply. A call that asks for more than the run's
	// own stage limit is refused by the gateway, as a stage's would be.
	MaxTokens int64
}

// StepAnswer is the model's reply: the assistant's text, joined.
type StepAnswer struct{ Text string }

// StepFunc is what a registered step is: the whole of its kind's work, given
// the run's own machinery and nothing of the repository.
type StepFunc func(ctx context.Context, v StepView) error

// StepView is what a registered step is handed.
type StepView struct {
	// RunID is this run's ID and Slug its repository's storage slug.
	RunID, Slug string
	// Store is this run's own prefix in the runs bucket: the only prefix the
	// step writes under, and where the launcher left the manifest it reads.
	Store *runstore.Store
	// AB is the project layer's ab: block as this run resolved it, nil when
	// the layer carries none. It is the owner's: a repository may not set
	// these keys (config.validateRepoAB).
	AB *config.AB
	// Call makes one model call through this run's gateway, which prices it,
	// routes it and charges it to the run's budget like any other call.
	Call func(ctx context.Context, req StepCall) (StepAnswer, error)
	// Log is the run's own logger, for the lines a step writes.
	Log *slog.Logger
}

// steps are the step kinds this build carries, by kind. The registry is
// process-wide and filled from an init, the way internal/cli's own seams are
// (§5.5 of docs/design/ee-boundary-moves.md); the tagged binary registers one
// function for the one kind its recipes hold.
var steps = map[recipe.StepKind]StepFunc{}

// RegisterStep installs the function a registered step kind runs. It refuses a
// kind the parser does not know — a kind registered here and not there is a
// recipe no build could parse, which is the one thing this seam may not leave
// half-built — and it refuses a second registration for one kind, so nothing
// can quietly replace what a tagged build installed.
func RegisterStep(kind recipe.StepKind, f StepFunc) error {
	if !recipe.StepKindRegistered(kind) {
		return fmt.Errorf("%s is not a step kind this build parses, and a step is registered with the parser before it is registered here", kind)
	}
	if f == nil {
		return fmt.Errorf("registering the step %s: no function", kind)
	}
	if _, dup := steps[kind]; dup {
		return fmt.Errorf("registering the step %s: it is already registered", kind)
	}
	steps[kind] = f
	return nil
}

// registeredStep is the function a step kind runs, and whether this build
// carries one.
func registeredStep(kind recipe.StepKind) (StepFunc, bool) {
	f, ok := steps[kind]
	return f, ok
}

// evaluateStage is the stage a registered step's calls are charged to: the
// gateway admits a call only inside a stage, and this is the name its report
// and the run's stage timings write.
const evaluateStage = "judge"

// runEvaluateMode runs a mode: evaluate recipe: its one registered step, and
// then an ending. It opens no pull request, pushes no branch and commits
// nothing (r.skipPR), and the run's record is the whole of what it leaves
// beside the objects the step wrote under its own prefix.
func (r *run) runEvaluateMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			// The same priority every other end branch in a mode gives a
			// halt: it decides the run whatever else is true, a panic in the
			// step included.
			if h := r.haltValue(); h != nil {
				r.endEvaluate(ctx, runstore.StatusHalted, haltClass(h.Reason), (&HaltError{*h}).Error(), h)
				return
			}
			r.endEvaluate(ctx, runstore.StatusFailed, failure.StageErrorUnexplained,
				fmt.Sprintf("the %s step panicked: %v", r.evaluateStep(), p), nil)
		}
	}()
	kind := r.evaluateStep()
	step, ok := registeredStep(kind)
	if !ok {
		// The recipe parser refuses a step kind it has not been taught, so
		// this is a build whose registration and whose recipe came from two
		// halves that did not land together (ee/register's init). Reported as
		// a failure rather than run as "nothing to do": a judge run that
		// succeeded without judging would be counted as a judged pair by
		// everything that reads the runs afterwards.
		r.endEvaluate(ctx, runstore.StatusFailed, failure.ConfigInvalid,
			fmt.Sprintf("the %s step is part of this recipe, but this build carries nothing to run it", kind), nil)
		return
	}
	started := r.d.Now()
	err := step(ctx, StepView{
		RunID: r.rec.RunID,
		Slug:  r.d.Store.Slug(),
		Store: r.d.Store,
		AB:    r.ab,
		Call:  r.DirectCall,
		Log:   r.d.Log,
	})
	r.rec.Stages = append(r.rec.Stages, r.stepTiming(string(kind), started))
	r.updateCost()
	switch {
	case err == nil:
		r.endEvaluate(ctx, runstore.StatusSucceeded, "", "", nil)
	case r.haltValue() != nil:
		h := r.haltValue()
		r.endEvaluate(ctx, runstore.StatusHalted, haltClass(h.Reason), (&HaltError{*h}).Error(), h)
	case r.isCancelled():
		r.endEvaluate(ctx, runstore.StatusCancelled, failure.Cancelled, "cancelled during "+string(kind), nil)
	default:
		r.endEvaluate(ctx, runstore.StatusFailed, failure.StageErrorUnexplained, r.redact(err.Error()), nil)
	}
}

// stepTiming is the stage timing the step's stage is recorded with. The
// record's stage list is what says what each stage cost and which model it ran
// on (cost.go's stageTiming), and its figures are exact: the stages sum to
// cost.model_usd, with the "unattributed" residual holding only spend that
// settled after its stage's wait ran out. A registered step's calls are the
// only model calls a mode: evaluate run makes, so leaving them out of its stage
// would put the run's whole model figure in no stage and break that sum — which
// is this stage's own reason to carry them, and not a courtesy.
//
// Model is left empty, and that is a figure too: nothing pinned a model for a
// step — the step named the model it called, which is no role's slot — and
// naming the coder's would be a model this call did not run on. Agents and
// Repeats are zero because the stage cannot have any: a registered step is
// handed no agent to spawn one and no way to run a command, and 0 is the count
// of what the runner counted it doing, not a count nobody kept.
func (r *run) stepTiming(name string, started time.Time) runstore.StageTiming {
	r.mu.Lock()
	sp := r.stepSpend
	r.mu.Unlock()
	zero := 0
	return runstore.StageTiming{
		Name:             name,
		StartedAt:        started.UTC(),
		DurationS:        r.d.Now().Sub(started).Seconds(),
		ModelUSD:         (sp.used + sp.notional).USD(),
		ModelNotionalUSD: sp.notional.USD(),
		Calls:            &sp.calls,
		Agents:           &zero,
		Repeats:          &zero,
		ServedBy:         runstore.CredentialForAuth(r.modelAuth()),
	}
}

// evaluateStep is the kind of the mode's one step. orderEvaluate has already
// refused a recipe with none or more than one by the time a run is dispatched.
func (r *run) evaluateStep() recipe.StepKind {
	if len(r.rcp.Steps) == 0 {
		return ""
	}
	return r.rcp.Steps[0].Kind
}

// endEvaluate ends an evaluate run: the record's ending, the halt when there is
// one, and r.skipPR, which is what keeps finalize from trying to push a branch
// this run never had.
func (r *run) endEvaluate(ctx context.Context, status runstore.Status, class failure.Class, reason string, halt *runstore.Halt) {
	if halt != nil {
		r.rec.Halt = halt
	}
	r.recordEnd(status, runstore.OutcomeNone, class, reason)
	r.skipPR = true
	if status != runstore.StatusSucceeded {
		r.d.Log.Warn("evaluate run ended", "status", string(status), "reason", reason)
	}
	r.save(ctx)
}
