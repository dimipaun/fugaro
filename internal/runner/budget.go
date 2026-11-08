package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// ErrCancelled is the context cause when a cancel marker was seen.
var ErrCancelled = errors.New("run cancelled")

// Budget tracks the run's time: every stage must end before Total-Reserve so
// finalize always has Reserve left (design §4.5).
type Budget struct {
	Start   time.Time
	Total   time.Duration
	Reserve time.Duration
	Stage   time.Duration
	// ReviewPct is the share of Total, in percent, the review and fix
	// rounds may spend (config.Timeouts.ReviewBudgetPct, default 80). Past
	// it the loop starts no further round and the run finalizes with the
	// last round's findings still open rather than failing (BF1). It is 0
	// for a Budget nobody filled in — every test that builds one by hand —
	// and 0 reads as the default, so such a Budget stops at the same share
	// a run does.
	ReviewPct int
	Now       func() time.Time
}

func (b Budget) finalizeAt() time.Time { return b.Start.Add(b.Total - b.Reserve) }

// Exhausted reports whether only the finalize reserve is left.
func (b Budget) Exhausted() bool { return !b.Now().Before(b.finalizeAt()) }

// reviewStopAt is the instant no further review or fix round may start at:
// ReviewPct of Total into the run, never later than finalizeAt — a share
// past the reserve asks for nothing the reserve does not already do, and
// 100% with no reserve left is exactly that instant.
func (b Budget) reviewStopAt() time.Time {
	at := b.Start.Add(time.Duration(float64(b.Total) * float64(b.reviewPct()) / 100))
	if f := b.finalizeAt(); f.Before(at) {
		return f
	}
	return at
}

// reviewPct is the share reviewStopAt reads, with an unset or unreadable
// value taken as the default: it is the number the record's open_findings
// block carries (runstore.OpenFindings.ReviewPct), so what a reader is told
// is the share the run was actually held to.
func (b Budget) reviewPct() int {
	if b.ReviewPct <= 0 || b.ReviewPct > config.MaxReviewBudgetPct {
		return config.DefaultReviewBudgetPct
	}
	return b.ReviewPct
}

// ReviewBudgetSpent reports whether the run has spent its review share: past
// it the review loop starts no further review or fix round (BF1). It is not
// Exhausted — that is the finalize reserve, which ends a stage wherever it
// is, mid-round included.
func (b Budget) ReviewBudgetSpent() bool { return !b.Now().Before(b.reviewStopAt()) }

// StageContext bounds one stage by the stage timeout and the finalize reserve.
func (b Budget) StageContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline := b.Now().Add(b.Stage)
	if f := b.finalizeAt(); f.Before(deadline) {
		deadline = f
	}
	return context.WithDeadline(parent, deadline)
}

// StepDeadline bounds one non-model step — the render, and the probe a later
// release adds — by its own timeout d and by the run's finalize reserve, the
// same two limits StageContext gives a stage: d from now, or the reserve's
// deadline when that comes first. It is separate because such a step is not a
// stage: no model call, no stage timeout, its own timeouts.render (design
// artifact-runs.md §3, §6: the timeout is the compute cap).
//
// byReserve says which of the two bounds applied, so a step that ends at this
// deadline can say which one cut it: the reserve holds whatever d asks for
// (design §4.5), and reporting such a step as a timeout of d would blame a
// bound that never fired (runRender).
func (b Budget) StepDeadline(d time.Duration) (deadline time.Time, byReserve bool) {
	deadline = b.Now().Add(d)
	if f := b.finalizeAt(); f.Before(deadline) {
		return f, true
	}
	return deadline, false
}

// WatchCancel returns a context cancelled with ErrCancelled once check
// reports a cancel request. The returned stop function ends the watch.
//
// onCancel, when not nil, is called just before the cancel, so the run
// records it the moment the marker is seen. It returns false when a halt
// was recorded first: the context is still cancelled, but the run's
// outcome stays the halt.
func WatchCancel(parent context.Context, check func(context.Context) (bool, error), every time.Duration, onCancel func() bool) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if ok, err := check(ctx); err == nil && ok {
					if onCancel != nil {
						onCancel()
					}
					cancel(ErrCancelled)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}

// interruptedPrefix starts the reason of a stage stopped by the execution
// being stopped (SIGTERM), when no cancel marker says why.
const interruptedPrefix = "interrupted (execution stopped)"

// cancelNowPrefix starts the reason when the marker is there and the
// execution was stopped at once: fugaro cancel --now.
const cancelNowPrefix = "cancelled (stopped by fugaro cancel --now)"

// StageError explains why a stage ended early, for the run record and draft PR.
func StageError(stage string, stageCtx context.Context, b Budget, err error) string {
	var halt *HaltError
	switch {
	case errors.As(context.Cause(stageCtx), &halt):
		return fmt.Sprintf("halted during %s: %s", stage, halt.Halt.Detail)
	case errors.Is(context.Cause(stageCtx), ErrCancelled):
		return "cancelled during " + stage
	case errors.Is(stageCtx.Err(), context.DeadlineExceeded) && b.Exhausted():
		return "time budget exhausted during " + stage
	case errors.Is(stageCtx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("stage %s timed out after %s", stage, b.Stage)
	case stageCtx.Err() != nil && errors.Is(context.Cause(stageCtx), context.Canceled):
		// The runner's own context was cancelled from outside (SIGTERM),
		// with no cancel marker read here: not a stage failure.
		return interruptedPrefix + " during " + stage
	default:
		return fmt.Sprintf("stage %s failed: %v", stage, err)
	}
}
