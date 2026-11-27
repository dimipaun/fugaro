package report

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is the reporter's clock under a test's hand: the elapsed time the
// liveness lines carry is read from it, so a step that has to run for a minute
// is a step the test has moved a minute forward. It is guarded, because the
// reporter's own goroutine reads it.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// syncBuf is a writer a test reads while the reporter's own goroutine writes
// it: the line in flight and the heartbeat are drawn from a ticker.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForOutput blocks until out holds at, or fails the test.
func waitForOutput(t *testing.T, out *syncBuf, at string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), at) {
		if time.Now().After(deadline) {
			t.Fatalf("%q was never written:\n%q", at, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// lastFrame is what is on the terminal's own line: everything after the last
// carriage return, which is where a redraw returns to.
func lastFrame(s string) string {
	if i := strings.LastIndex(s, "\r"); i >= 0 {
		return s[i:]
	}
	return s
}

// TestSpinnerShowsElapsed: on a terminal the line in flight carries how long
// the step has been running, and it keeps up with the clock as the frames
// redraw (D3436).
func TestSpinnerShowsElapsed(t *testing.T) {
	was := spinInterval
	spinInterval = time.Millisecond
	t.Cleanup(func() { spinInterval = was })

	clock := &fakeClock{at: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)}
	var out syncBuf
	r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal, Now: clock.now})
	step := r.Step("Applying infrastructure changes")
	if frame := lastFrame(out.String()); !strings.Contains(frame, "Applying infrastructure changes (0s)") {
		t.Errorf("the line in flight does not carry the elapsed time: %q", frame)
	}
	clock.advance(61 * time.Second)
	waitForOutput(t, &out, "(1m01s)")
	frame := lastFrame(out.String())
	if !strings.Contains(frame, "Applying infrastructure changes (1m01s)") {
		t.Errorf("the line in flight is %q, want the step's name and its elapsed time", frame)
	}
	if strings.HasSuffix(frame, "\n") {
		t.Errorf("the line in flight ended on a newline: %q", frame)
	}
	step.Done("")
}

// TestHeartbeatWithoutTTY: where no line in flight may be drawn, a step that
// runs on says so once a minute, with the time it has taken, and it stops
// saying so when the step ends (D3436).
func TestHeartbeatWithoutTTY(t *testing.T) {
	was := heartbeatInterval
	heartbeatInterval = time.Millisecond
	t.Cleanup(func() { heartbeatInterval = was })

	clock := &fakeClock{at: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)}
	var out syncBuf
	r := New(Options{Out: &out, Getenv: noEnv, Now: clock.now})
	step := r.Step("Applying infrastructure changes")
	if got := out.String(); strings.Contains(got, "\r") {
		t.Fatalf("a step in flight drew a carriage return off a terminal: %q", got)
	}
	clock.advance(61 * time.Second)
	waitForOutput(t, &out, "still Applying infrastructure changes (1m01s)")
	if got := out.String(); !strings.HasPrefix(got, "still ") {
		t.Errorf("the first line of a long step is %q, want the still line", got)
	}
	step.Done("")
	after := out.String()
	time.Sleep(20 * time.Millisecond)
	if out.String() != after {
		t.Errorf("the heartbeat outlived the step it belongs to:\n%q", out.String())
	}
}

// TestElapsedTextIsAClock: the elapsed time is drawn the way a person reads a
// clock, not as a stopwatch: whole seconds under a minute, then minutes.
func TestElapsedTextIsAClock(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{9 * time.Second, "9s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m00s"},
		{61 * time.Second, "1m01s"},
		{2*time.Hour + 3*time.Minute + 4*time.Second, "2h03m"},
		{-time.Second, "0s"},
	}
	for _, c := range cases {
		if got := elapsedText(c.d); got != c.want {
			t.Errorf("elapsedText(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
