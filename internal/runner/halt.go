package runner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// ErrHalted is wrapped by every HaltError.
var ErrHalted = errors.New("run halted")

// HaltError says a budget or token limit halted the run.
type HaltError struct{ Halt runstore.Halt }

func (e *HaltError) Error() string {
	if e.Halt.Detail == "" {
		return fmt.Sprintf("halted: %s", e.Halt.Reason)
	}
	return fmt.Sprintf("halted: %s: %s", e.Halt.Reason, e.Halt.Detail)
}

func (e *HaltError) Unwrap() error { return ErrHalted }

// leftoverHaltMessage is the commit message for work a halted stage left
// uncommitted: the stage was stopped, so files may be incomplete.
const leftoverHaltMessage = "fugaro: uncommitted work at halt (the stage was stopped; files may be incomplete)"

// bootstrapHalt, when set, is asked right after the project check, before
// the branch lock, whether the run must halt at bootstrap. It is nil in
// production until a budget check is wired in, and set by tests.
var bootstrapHalt func() *runstore.Halt

// strictHaltCheck makes finalize panic, instead of logging, when both a
// halt and a cancel are recorded. Tests set it.
var strictHaltCheck bool

// onNewRun, when set, is called with each run as Run starts it. Tests use
// it to reach the run's halt and cancel hooks.
var onNewRun func(*run)

// haltNow records h and its reason unless a cancel or an earlier halt was
// recorded first, and reports whether it did. It does not stop the running
// stage: cancelHaltedStage does, after the grace period.
func (r *run) haltNow(h runstore.Halt) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancelled || r.halt != nil {
		return false
	}
	r.halt = &h
	r.failLocked((&HaltError{h}).Error())
	return true
}

// cancelHaltedStage cancels the running stage with the halt as its cause,
// which stops the agent's process group. It does nothing between stages.
func (r *run) cancelHaltedStage(h runstore.Halt) {
	r.mu.Lock()
	cancel := r.haltStage
	r.mu.Unlock()
	if cancel != nil {
		cancel(&HaltError{h})
	}
}

// markCancelled records a cancel unless a halt came first, and reports
// whether the cancel is the one recorded.
func (r *run) markCancelled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.halt != nil {
		return false
	}
	r.cancelled = true
	return true
}

// haltValue is a copy of the recorded halt, or nil.
func (r *run) haltValue() *runstore.Halt {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.halt == nil {
		return nil
	}
	h := *r.halt
	return &h
}

func (r *run) isCancelled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancelled
}

func (r *run) failure() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failReason
}

// countTokens adds a finished stage's tokens to the run's total: the larger
// of the result event's count and the gateway's.
func (r *run) countTokens(res agent.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens += max(res.StageTokens(), r.gatewayTokens)
	r.gatewayTokens = 0
}

// capReached applies the token cap after a stage that succeeded, and
// reports whether the run is halted, by this cap or by an earlier halt. A
// stage that failed never calls it: its failure is already the run's
// reason, and a cap must not turn it into a halt.
func (r *run) capReached() bool {
	r.mu.Lock()
	tokens, limit := r.tokens, r.cfg.Agent.MaxRunTokens
	r.mu.Unlock()
	if limit > 0 && tokens >= limit {
		r.haltNow(runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run", At: r.d.Now().UTC(),
			Detail: fmt.Sprintf("run used %d tokens of %d", tokens, limit)})
	}
	return r.haltValue() != nil
}

// haltReasonText says in words why each kind of halt stopped a run.
func haltReasonText(h runstore.Halt) string {
	switch h.Reason {
	case runstore.HaltKillSwitch:
		return "the budget kill switch was on"
	case runstore.HaltRunCap:
		return "the per-run dollar cap was reached"
	case runstore.HaltRepoDailyCap:
		return "the repository's daily dollar cap was reached"
	case runstore.HaltGlobalDailyCap:
		return "the project's daily dollar cap was reached"
	case runstore.HaltNoCap:
		return "an api-key run in enforce mode has no per-run dollar cap"
	case runstore.HaltTokenCap:
		return "the run's token cap was reached"
	case runstore.HaltBudgetUnavailable:
		return "the budget could not be read"
	case runstore.HaltBudgetTokenExpired:
		return "the budget token expired"
	}
	return string(h.Reason)
}

// haltLines is the report's halted line and how to continue.
func haltLines(rec *runstore.Record) string {
	h := rec.Halt
	var b strings.Builder
	fmt.Fprintf(&b, "**Halted:** %s", haltReasonText(*h))
	if h.Detail != "" {
		fmt.Fprintf(&b, " (%s)", h.Detail)
	}
	fmt.Fprintf(&b, " at %s — this run spent $%.2f.\n\n", h.At.UTC().Format("2006-01-02 15:04:05 UTC"), rec.CostUSD)
	switch h.Reason {
	case runstore.HaltTokenCap:
		b.WriteString("To continue: raise `agent.max_run_tokens` in `fugaro.yaml`, then run `fugaro run --pr N`.\n\n")
	case runstore.HaltRunCap, runstore.HaltNoCap:
		b.WriteString("To continue: raise `budget.per_run_usd` in the project config and run `fugaro init --repo`, then run `fugaro run --pr N`.\n\n")
	default:
		b.WriteString("To continue: once the limit that halted this run is lifted, run `fugaro run --pr N`.\n\n")
	}
	return b.String()
}
