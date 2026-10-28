package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	if r.haltDone != nil {
		// Once, and only here: the check above means r.halt was nil, so this
		// is the run's first halt and the close cannot happen twice. It wakes
		// a caller waiting outside a stage — the credential wait (C1.17),
		// whose sleep may be hours long and hears no halt otherwise. The
		// channel stays where it is rather than being nil'd: a caller that
		// reads it after the halt (waitForCredential reads it once, then
		// selects) must find it closed, not nil, or the select would block on
		// a nil channel for the whole sleep.
		close(r.haltDone)
	}
	r.failLocked((&HaltError{h}).Error())
	return true
}

// haltCh is closed once a halt is recorded, and open until then: a caller
// that may have to wait outside a stage watches it to stop at once
// (waitForCredential). It is read under r.mu, and the channel itself is never
// replaced, so a select on it is decided by the halt whether the halt came
// before or after the read. It is nil only before Run creates it.
func (r *run) haltCh() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.haltDone
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
		return "the budget backend could not be reached for the whole grace period"
	case runstore.HaltBudgetTokenExpired:
		return "the run's budget token had expired"
	case runstore.HaltNoCredential:
		return "no credential became usable before the job's own timeout"
	case runstore.HaltCredsRulesDenied:
		return "the database's rules refused this run's credential state"
	case runstore.HaltStageBudget:
		return "a stage passed its own budget"
	case runstore.HaltReviewRatio:
		return "review spent more calls than the ratio allows for the implementation it read"
	case runstore.HaltFanout:
		return "a stage used more sub-agents than the maximum"
	case runstore.HaltReviewScope:
		return "the review range covers files this run never changed"
	case runstore.HaltExpectedExceeded:
		return "the run passed the cost its tier expected of it"
	}
	return string(h.Reason)
}

// governanceHalt stops the run at a governance tripwire (T6.1, D329, G7): it
// records the halt, writes the checkpoint report into the run record and puts
// that report on the pull request, which is opened as a draft when the run has
// none yet. It reports whether this call is the one that halted the run, the
// way haltNow does.
//
// detail is the runner's own account of what the tripwire saw — a count, a
// figure, a stage name — and never task text. The checkpoint's trigger,
// why_over and estimate_usd are the runner's own too: the reason it was
// handed, its words for that reason, and the model spend the record had
// reached. done, next and hard_instructions stay empty until the stopping
// agent answers the checkpoint prompt (T7.2, wired in T7.3).
//
// It runs on the run's own goroutine: the pull-request state it writes through
// belongs to that goroutine (prflow.go), and the tripwires that call it read
// the stage's own accounting, which that goroutine owns.
func (r *run) governanceHalt(ctx context.Context, reason runstore.HaltReason, detail string) bool {
	h := runstore.Halt{Reason: reason, Scope: "run", At: r.d.Now().UTC(), Detail: detail}
	if !r.haltNow(h) {
		return false
	}
	// The report has to reach the record and the pull request whatever happens
	// next: the halt recorded above is what the run's halt watcher acts on, and
	// it cancels the stage after its grace period — so the context this was
	// called with, the stage's own when the tripwire was a stage's, can be done
	// before either write lands.
	ctx = context.WithoutCancel(ctx)
	// The record is read and written under r.mu: the stage's checkpoint poll
	// goroutine is still alive here (the halt stops it, but only once the
	// watcher's grace period is over) and it snapshots the whole record under
	// this same lock, so a write outside it is one a save can copy halfway.
	//
	// The record's own model figure is the only money figure a checkpoint can
	// carry before a tier's expectation exists (T8.1), and it is what a
	// continuation starts from.
	r.mu.Lock()
	r.rec.Checkpoint = runstore.NewCheckpoint(runstore.Checkpoint{
		Trigger:     string(h.Reason),
		WhyOver:     haltReasonText(h),
		EstimateUSD: r.rec.CostUSD,
	})
	r.mu.Unlock()
	r.save(ctx)
	r.checkpointPR(ctx, reason)
	return true
}

// checkpointPR puts the checkpoint report on the pull request, opening the
// draft when the run has none yet: a governance stop must leave a person
// something to read, and the draft is where they read it. It never asks for
// the pull request to be made ready. A failure only warns — finalize settles
// the pull request and writes the same report into its final section.
func (r *run) checkpointPR(ctx context.Context, reason runstore.HaltReason) {
	// Which pull request, which tip and which stage, taken under r.mu for the
	// reason governanceHalt writes the checkpoint there: the stage's checkpoint
	// poll goroutine is still alive and snapshots the record under the same
	// lock, so these are read where its save cannot be reading them.
	r.mu.Lock()
	pr, pushed, stage := r.rec.PR, r.rec.PushedHead, r.rec.Stage
	r.mu.Unlock()
	if pr == nil {
		if pushed == "" || !r.cfg.Git.PR.EarlyDraftOn() {
			// Nothing to open on: no commit of this run is on the branch yet,
			// so finalize is what opens the pull request. A run with the early
			// draft off has no mid-run status section by design.
			return
		}
		r.openDraftPR(ctx, stage, true)
		return
	}
	// during: the stage this was called from is still unwinding, so the
	// section says a checkpoint landed inside it rather than after it.
	err := r.writeStatus(ctx, pr.Number, r.redact(r.runningSection(stage, true)), statusCallTimeout)
	switch {
	case err == nil:
		r.noteStatusWritten(ctx, r.d.Now())
	case errors.Is(err, gitprov.ErrPRNotOpen):
		// The flag that stops later status updates is left to the poll's own
		// next write (prflow.go): it reads r.pr every tick from its own
		// goroutine, and the run is ending within the halt's grace period.
		r.d.Log.Warn("the pull request is no longer open; the checkpoint was not written to it", "pr", pr.Number)
	default:
		r.d.Log.Warn("writing the checkpoint to the pull request failed; finalize writes the report",
			"pr", pr.Number, "reason", string(reason), "err", r.redact(err.Error()))
	}
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
	if rec.Cost == nil || !rec.Cost.AllNotional() {
		// The money the run spent: rec.CostUSD is the whole model figure, and
		// a cascade run's tier-A dollars are the subscription's, not the
		// run's (C1.16, P12). Without a breakdown (a record from before it)
		// the figure is all there is.
		spent := rec.CostUSD
		if rec.Cost != nil {
			spent = rec.Cost.ModelMoneyUSD()
		}
		fmt.Fprintf(&b, " — this run spent $%.2f", spent)
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
		fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyMaxRunTokens, "`agent.max_run_tokens`", "`budget.max_run_tokens`"), again)
	case runstore.HaltKillSwitch:
		fmt.Fprintf(&b, "To continue: an admin turns the switch off with `fugaro budget resume`, then %s.\n\n", again)
	case runstore.HaltRepoDailyCap, runstore.HaltGlobalDailyCap:
		if strings.Contains(h.Detail, "fugaro.yaml") {
			fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyPerDayUSD, "`budget.per_day_usd`", "`budget.per_day_usd`"), again)
		} else {
			fmt.Fprintf(&b, "To continue: an admin raises the cap with `fugaro budget set` (or wait for the next UTC day), then %s.\n\n", again)
		}
	case runstore.HaltBudgetUnavailable:
		fmt.Fprintf(&b, "To continue: once the budget backend is reachable again, %s.\n\n", again)
	case runstore.HaltBudgetTokenExpired:
		b.WriteString("To continue: start the run again (the run's budget token is minted at launch and lasts an hour; a run that queues longer cannot start).\n\n")
	case runstore.HaltNoCredential:
		fmt.Fprintf(&b, "To continue: %s once a credential is usable again (`fugaro accounts ls` shows each admitted account's state; the project policy's `credentials` decides whether another tier may be used meanwhile).\n\n", again)
	case runstore.HaltCredsRulesDenied:
		fmt.Fprintf(&b, "To continue: an operator re-runs `fugaro update --firebase-project <gcp-project-id>` with this release, which deploys the rules the cascade's fields need, then %s.\n\n", again)
	case runstore.HaltRunCap, runstore.HaltNoCap:
		if rec.Budget != nil && h.Reason == runstore.HaltNoCap {
			fmt.Fprintf(&b, "To continue: an admin sets the missing cap with `fugaro budget set`, then %s.\n\n", again)
			break
		}
		fmt.Fprintf(&b, "To continue: %s, then %s.\n\n", raiseAdvice(rec, policy.KeyPerRunUSD, "`budget.per_run_usd`", "`budget.per_run_usd`"), again)
	default:
		fmt.Fprintf(&b, "To continue: once the limit that halted this run is lifted, %s.\n\n", again)
	}
	return b.String()
}

// raiseAdvice says where to raise a limit, by the layer that set it: a value
// on the run's own branch can only tighten the ceiling and the default
// branch's file, so raising it there changes nothing.
//
// yamlName and nodeName are the key's spelling in each place it can live, and
// they are not always the same one: the token cap is agent.max_run_tokens in
// fugaro.yaml, but a member of the budget block in the project config and in
// the published policy node (localcfg.Config.Budget, projectpolicy.Budget),
// where it is budget.max_run_tokens. Naming the fugaro.yaml spelling for a
// limit a budget block set sends the operator to a key that does not exist
// there — the same reason the dollar caps already name budget.*_usd.
func raiseAdvice(rec *runstore.Record, key, yamlName, nodeName string) string {
	src := ""
	if rec.Policy != nil {
		src = rec.Policy.Sources[key]
	}
	switch src {
	case policy.SourceCeiling:
		return fmt.Sprintf("raise %s in the project config and run `fugaro update` (a raise in any fugaro.yaml is ignored: it can only tighten the ceiling)", nodeName)
	case policy.SourcePolicy:
		return fmt.Sprintf("raise %s in the project's policy at %s (a raise in any fugaro.yaml is ignored: it can only tighten the ceiling)", nodeName, projectpolicy.PathShared())
	case policy.SourceDefaultBranch:
		return fmt.Sprintf("merge a change to %s in `fugaro.yaml` on the default branch (a raise on the run's own branch is ignored)", yamlName)
	case policy.SourceBranch:
		return fmt.Sprintf("raise %s in `fugaro.yaml` on this run's branch (it cannot exceed the project config's ceiling or the default branch's value)", yamlName)
	}
	if key == policy.KeyPerRunUSD {
		return fmt.Sprintf("raise %s in the project config and run `fugaro update`", nodeName)
	}
	return fmt.Sprintf("raise %s in `fugaro.yaml`", yamlName)
}
