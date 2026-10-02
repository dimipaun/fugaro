package budget

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestPanicFailsClosed: a panic on one of the session's goroutines (a path
// builder given an empty segment, say) must never take the process down,
// nor leave the run unbudgeted: it halts the run as unavailable.
func TestPanicFailsClosed(t *testing.T) {
	halts := make(chan Halt, 2)
	s := &Session{cfg: Config{OnHalt: func(h Halt) { halts <- h }}, log: discardLog()}
	s.safely("test", func() { panic("budget: empty slug in a database path") })
	select {
	case h := <-halts:
		if h.Reason != ReasonBudgetUnavailable || h.Scope != ScopeRun {
			t.Fatalf("halt = %+v", h)
		}
	default:
		t.Fatal("a panic did not halt the run")
	}
}

// TestPathBuildersPanicOnEmptySegments pins why Open validates the identity:
// an empty slug or run id would alias another node (runs//r1 is runs/r1).
func TestPathBuildersPanicOnEmptySegments(t *testing.T) {
	for name, f := range map[string]func(){
		"run":   func() { PathRun("", "r") },
		"agent": func() { PathAgent("s", "") },
		"model": func() { PathByModel(1, "s", "") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic on an empty segment", name)
				}
			}()
			f()
		}()
	}
}

// I2: a kill stream that fails counts toward the grace only while the REST
// poll fails too.
func TestKillStreamErrorsCountOnlyWithAFailingPoll(t *testing.T) {
	g := NewGrace(time.Hour)
	s := &Session{grace: g, log: discardLog()}
	s.streamFailed("kill-global", errors.New("sse cut"))
	if g.Failing() {
		t.Fatal("a stream error alone started the grace")
	}
	g.Fail("kill-poll", errors.New("503"))
	s.streamFailed("kill-global", errors.New("sse cut"))
	if !g.FailingSource("kill-global") {
		t.Fatal("a stream error with a failing poll did not count")
	}
}
