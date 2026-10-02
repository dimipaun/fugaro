package runner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
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
	// The detail is published (the record, the report, diagnose): it may
	// quote what a provider or the gateway reported.
	h.Detail = r.redact(h.Detail)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recordHaltLocked(h)
}

// recordHaltLocked is haltNow's decision for a caller holding r.mu and
// having redacted h.
func (r *run) recordHaltLocked(h runstore.Halt) bool {
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
	tokens, limit := r.tokens, r.spend.MaxRunTokens
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
		if h.Scope == "global" {
			return "the project's kill switch was on"
		}
		return "the repository's kill switch was on"
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
		return "the budget backend could not be reached for the whole grace period of 3 minutes"
	case runstore.HaltBudgetTokenExpired:
		return "the run's budget token had expired"
	}
	return string(h.Reason)
}

// haltLines is the report's halted line and how to continue. The spend is
// left out of a subscription run's line: its model figure is notional.
func haltLines(rec *runstore.Record) string {
	h := rec.Halt
	var b strings.Builder
	fmt.Fprintf(&b, "**Halted:** %s", haltReasonText(*h))
	if h.Detail != "" {
		fmt.Fprintf(&b, " (%s)", h.Detail)
	}
	fmt.Fprintf(&b, " at %s", h.At.UTC().Format("2006-01-02 15:04:05 UTC"))
	if rec.Cost == nil || rec.Cost.ModelBasis != runstore.BasisSubscription {
		fmt.Fprintf(&b, " — this run spent $%.2f", rec.CostUSD)
	}
	b.WriteString(".\n\n")
	// A halt before anything was pushed has no pull request to continue on.
	again := "run `fugaro run --pr N`"
	if rec.PR != nil {
		again = fmt.Sprintf("run `fugaro run --pr %d`", rec.PR.Number)
	} else if rec.Outcome == runstore.OutcomeNone {
		again = "start the run again"
	}
	switch h.Reason {
	case runstore.HaltTokenCap:
		fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyMaxRunTokens, "`agent.max_run_tokens`"), again)
	case runstore.HaltKillSwitch:
		fmt.Fprintf(&b, "To continue: an admin turns the switch off with `fugaro budget resume`, then %s.\n\n", again)
	case runstore.HaltRepoDailyCap, runstore.HaltGlobalDailyCap:
		if strings.Contains(h.Detail, "fugaro.yaml") {
			fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyPerDayUSD, "`budget.per_day_usd`"), again)
		} else {
			fmt.Fprintf(&b, "To continue: an admin raises the cap with `fugaro budget set` (or wait for the next UTC day), then %s.\n\n", again)
		}
	case runstore.HaltBudgetUnavailable:
		fmt.Fprintf(&b, "To continue: once the budget backend is reachable again, %s.\n\n", again)
	case runstore.HaltBudgetTokenExpired:
		b.WriteString("To continue: start the run again (the run's budget token is minted at launch and lasts an hour; a run that queues longer cannot start).\n\n")
	case runstore.HaltRunCap, runstore.HaltNoCap:
		if rec.Budget != nil && h.Reason == runstore.HaltNoCap {
			fmt.Fprintf(&b, "To continue: an admin sets the missing cap with `fugaro budget set`, then %s.\n\n", again)
			break
		}
		fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyPerRunUSD, "`budget.per_run_usd`"), again)
	default:
		fmt.Fprintf(&b, "To continue: once the limit that halted this run is lifted, %s.\n\n", again)
	}
	return b.String()
}

// raiseAdvice says where to raise a limit, by the layer that set it: a value
// on the run's own branch can only tighten the ceiling and the default
// branch's file, so raising it there changes nothing.
func raiseAdvice(rec *runstore.Record, key, name string) string {
	src := ""
	if rec.Policy != nil {
		src = rec.Policy.Sources[key]
	}
	switch src {
	case policy.SourceCeiling:
		return fmt.Sprintf("raise %s in the project config and run `fugaro init --repo` (a raise in any fugaro.yaml is ignored: it can only tighten the ceiling)", name)
	case policy.SourceDefaultBranch:
		return fmt.Sprintf("merge a change to %s in `fugaro.yaml` on the default branch (a raise on the run's own branch is ignored)", name)
	case policy.SourceBranch:
		return fmt.Sprintf("raise %s in `fugaro.yaml` on this run's branch (it cannot exceed the project config's ceiling or the default branch's value)", name)
	}
	if key == policy.KeyPerRunUSD {
		return fmt.Sprintf("raise %s in the project config and run `fugaro init --repo`", name)
	}
	return fmt.Sprintf("raise %s in `fugaro.yaml`", name)
}
