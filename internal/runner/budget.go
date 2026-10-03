package runner

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	Now     func() time.Time
}

func (b Budget) finalizeAt() time.Time { return b.Start.Add(b.Total - b.Reserve) }

// Exhausted reports whether only the finalize reserve is left.
func (b Budget) Exhausted() bool { return !b.Now().Before(b.finalizeAt()) }

// StageContext bounds one stage by the stage timeout and the finalize reserve.
func (b Budget) StageContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline := b.Now().Add(b.Stage)
	if f := b.finalizeAt(); f.Before(deadline) {
		deadline = f
	}
	return context.WithDeadline(parent, deadline)
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
