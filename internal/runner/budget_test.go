package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestWatchCancel(t *testing.T) {
	var flag atomic.Bool
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return flag.Load(), nil }, 10*time.Millisecond)
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
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return false, nil }, time.Hour)
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
	expired, cancel2 := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel2()

	cases := []struct {
		ctx  context.Context
		b    Budget
		err  error
		want string
	}{
		{cancelled, fresh, context.Canceled, "cancelled during implement"},
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
