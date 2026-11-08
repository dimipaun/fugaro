package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

func TestBudget(t *testing.T) {
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	now := start
	b := Budget{Start: start, Total: 10 * time.Minute, Reserve: 2 * time.Minute, Stage: 5 * time.Minute, Now: func() time.Time { return now }}

	ctx, cancel := b.StageContext(context.Background())
	if dl, _ := ctx.Deadline(); !dl.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("first stage deadline = %s", dl)
	}
	cancel()

	now = start.Add(4 * time.Minute)
	ctx, cancel = b.StageContext(context.Background())
	if dl, _ := ctx.Deadline(); !dl.Equal(start.Add(8 * time.Minute)) {
		t.Errorf("late stage deadline = %s, want capped at total-reserve", dl)
	}
	cancel()

	if b.Exhausted() {
		t.Error("exhausted too early")
	}
	now = start.Add(8 * time.Minute)
	if !b.Exhausted() {
		t.Error("not exhausted at total-reserve")
	}
}

// TestBudgetStopFraction: the stop's time half is the share of Total the
// owner's ruling of 2026-11-08 names, read between rounds only. The zero
// value takes config.DefaultReviewStopFraction, so a caller that says
// nothing gets the rule; a run with no time budget has no threshold, so an
// unset workflow cannot stop every run at its first round.
func TestBudgetStopFraction(t *testing.T) {
	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	now := start
	b := Budget{Start: start, Total: 10 * time.Minute, Reserve: time.Minute, Now: func() time.Time { return now }}

	if now = start.Add(8*time.Minute - time.Second); b.PastStop() {
		t.Error("the default stop fired before 8m of a 10m budget")
	}
	// The threshold includes its own instant, as Exhausted() includes
	// finalizeAt: the fraction has elapsed when the clock reaches it.
	if now = start.Add(8 * time.Minute); !b.PastStop() {
		t.Error("the default stop did not fire at 8m of a 10m budget")
	}
	if now = start.Add(9 * time.Minute); !b.PastStop() {
		t.Error("the default stop did not fire past its fraction")
	}

	// The threshold sits before the reserve's own deadline, which is what
	// makes it useful: a run that waits for Exhausted has no room left to
	// finalize a stop it did not plan.
	now = start.Add(8 * time.Minute)
	if b.Exhausted() {
		t.Error("the budget was exhausted at the stop threshold: the two are 8m and 9m here")
	}

	b.StopFraction = 0.5
	if now = start.Add(5*time.Minute - time.Second); b.PastStop() {
		t.Error("a 0.5 fraction fired before half the budget")
	}
	if now = start.Add(5 * time.Minute); !b.PastStop() {
		t.Error("a 0.5 fraction did not fire at half the budget")
	}

	b.StopFraction = 1
	if now = start.Add(9 * time.Minute); b.PastStop() {
		t.Error("a fraction of 1 fired before the whole budget: it names the very end, so the reserve is what stops the run")
	}
	if now = start.Add(10 * time.Minute); !b.PastStop() {
		t.Error("a fraction of 1 never fired")
	}

	for _, f := range []float64{0, -1, math.NaN()} {
		b.StopFraction = f
		if now = start.Add(8*time.Minute - time.Second); b.PastStop() {
			t.Errorf("fraction %v fired before the default's 8m", f)
		}
		if now = start.Add(8 * time.Minute); !b.PastStop() {
			t.Errorf("fraction %v did not take the default", f)
		}
	}

	// No time budget at all: no threshold. Every stage is already outside
	// finalizeAt (Total 0 leaves nothing), which is the pre-existing reading;
	// the stop must not add a second, blanket one.
	b.StopFraction = 0
	b.Total = 0
	b.Reserve = 0
	if now = start.Add(time.Hour); b.PastStop() {
		t.Error("a run with no time budget stopped past a threshold it does not have")
	}
	if !b.Exhausted() {
		t.Error("a run with no time budget is not exhausted")
	}
}

func TestWatchCancel(t *testing.T) {
	var flag atomic.Bool
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return flag.Load(), nil }, 10*time.Millisecond, nil)
	defer stop()
	flag.Store(true)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel marker not noticed")
	}
	if !errors.Is(context.Cause(ctx), ErrCancelled) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}

func TestWatchCancelStop(t *testing.T) {
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return false, nil }, time.Hour, nil)
	stop()
	<-ctx.Done()
	if errors.Is(context.Cause(ctx), ErrCancelled) {
		t.Fatal("stop must not look like a cancel request")
	}
}

func TestStageError(t *testing.T) {
	now := time.Now()
	fresh := Budget{Start: now, Total: time.Hour, Reserve: time.Minute, Stage: 40 * time.Minute, Now: time.Now}
	spent := Budget{Start: now.Add(-time.Hour), Total: time.Hour, Reserve: time.Minute, Stage: 40 * time.Minute, Now: time.Now}

	cancelled, cancel := context.WithCancelCause(context.Background())
	cancel(ErrCancelled)
	sigterm, cancel3 := context.WithCancel(context.Background())
	cancel3()
	expired, cancel2 := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel2()

	cases := []struct {
		ctx  context.Context
		b    Budget
		err  error
		want string
	}{
		{cancelled, fresh, context.Canceled, "cancelled during implement"},
		{sigterm, fresh, context.Canceled, "interrupted (execution stopped) during implement"},
		{expired, spent, context.DeadlineExceeded, "time budget exhausted during implement"},
		{expired, fresh, context.DeadlineExceeded, "stage implement timed out after 40m0s"},
		{context.Background(), fresh, errors.New("exec: claude not found"), "stage implement failed: exec: claude not found"},
	}
	for _, tc := range cases {
		if got := StageError("implement", tc.ctx, tc.b, tc.err); got != tc.want {
			t.Errorf("StageError = %q, want %q", got, tc.want)
		}
	}
}

func TestLoggerShape(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "run_id", "r1")
	log.Warn("careful", "stage", "implement")
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["severity"] != "WARNING" || entry["message"] != "careful" || entry["run_id"] != "r1" || entry["stage"] != "implement" {
		t.Fatalf("entry = %v", entry)
	}
}

func TestLineWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewLineWriter(NewLogger(&buf), "agent")
	w.Write([]byte("one\ntw"))
	w.Write([]byte("o\n"))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"message":"two"`) || !strings.Contains(lines[0], `"stream":"agent"`) {
		t.Fatalf("lines = %q", lines)
	}
}

func TestWatchCancelOnCancelHook(t *testing.T) {
	var flag atomic.Bool
	var calls atomic.Int32
	var ctxDoneAtCall atomic.Bool
	var ctx context.Context
	ready := make(chan struct{})
	var stop func()
	ctx, stop = WatchCancel(context.Background(), func(context.Context) (bool, error) { return flag.Load(), nil }, 10*time.Millisecond,
		func() bool {
			<-ready
			calls.Add(1)
			ctxDoneAtCall.Store(ctx.Err() != nil)
			return false // a halt came first: the context is cancelled all the same
		})
	defer stop()
	close(ready)
	flag.Store(true)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel marker not noticed")
	}
	if calls.Load() != 1 || ctxDoneAtCall.Load() {
		t.Fatalf("hook calls = %d, context already done at the call = %v", calls.Load(), ctxDoneAtCall.Load())
	}
	if !errors.Is(context.Cause(ctx), ErrCancelled) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}

func TestStageErrorHalted(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(&HaltError{Halt: runstore.Halt{Reason: runstore.HaltRunCap, Detail: "spent $5.00"}})
	b := Budget{Start: time.Now(), Total: time.Hour, Reserve: time.Minute, Stage: time.Minute, Now: time.Now}
	if got, want := StageError("fix", ctx, b, context.Canceled), "halted during fix: spent $5.00"; got != want {
		t.Fatalf("StageError = %q, want %q", got, want)
	}
}
