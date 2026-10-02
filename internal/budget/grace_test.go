package budget

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTimers is a hand-wound clock for Grace: After registers a callback to
// run once the clock has advanced by d.
type fakeTimers struct {
	mu  sync.Mutex
	now time.Duration
	ts  []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (c *fakeTimers) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now + d, f: f}
	c.ts = append(c.ts, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.stopped && !t.fired
		t.stopped = true
		return was
	}
}

func (c *fakeTimers) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeTimer
	for _, t := range c.ts {
		if !t.stopped && !t.fired && t.at <= c.now {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

func expired(g *Grace) bool {
	select {
	case <-g.Expired():
		return true
	default:
		return false
	}
}

func TestGraceExpiresAfterTheWindow(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(3*time.Minute, c.after)
	g.Fail("lease", errors.New("down"))
	c.advance(2*time.Minute + 59*time.Second)
	if expired(g) {
		t.Fatal("expired before 3 minutes")
	}
	c.advance(time.Second)
	if !expired(g) {
		t.Fatal("not expired at 3 minutes")
	}
	if src, err := g.Cause(); src != "lease" || err == nil {
		t.Fatalf("cause = %q, %v", src, err)
	}
}

func TestGraceResetsOnSuccess(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(3*time.Minute, c.after)
	g.Fail("lease", errors.New("down"))
	c.advance(2 * time.Minute)
	g.OK("lease")
	c.advance(5 * time.Minute)
	if expired(g) {
		t.Fatal("a recovered backend must not halt the run")
	}
	// A new outage starts a new clock, not the old remainder.
	g.Fail("lease", errors.New("down again"))
	c.advance(2*time.Minute + 59*time.Second)
	if expired(g) {
		t.Fatal("the second outage inherited the first one's clock")
	}
	c.advance(time.Second)
	if !expired(g) {
		t.Fatal("the second outage never expired")
	}
}

func TestGraceClockStartsAtTheFirstFailure(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(time.Minute, c.after)
	g.Fail("heartbeat", errors.New("a"))
	c.advance(40 * time.Second)
	g.Fail("heartbeat", errors.New("b")) // a later failure does not restart it
	c.advance(20 * time.Second)
	if !expired(g) {
		t.Fatal("repeated failures restarted the clock")
	}
}

// A source that keeps failing is not forgiven because another source works.
func TestGraceSourcesAreIndependent(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(time.Minute, c.after)
	g.Fail("lease", errors.New("down"))
	for i := 0; i < 6; i++ {
		c.advance(10 * time.Second)
		g.OK("heartbeat")
	}
	if !expired(g) {
		t.Fatal("a healthy heartbeat masked a lease that never recovered")
	}
}

func TestGraceExpiryIsSticky(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(time.Second, c.after)
	g.Fail("kill", errors.New("x"))
	c.advance(time.Second)
	g.OK("kill")
	if !expired(g) {
		t.Fatal("recovery after the halt must not un-expire it")
	}
}

func TestGraceDefault(t *testing.T) {
	if DefaultGrace != 3*time.Minute {
		t.Fatalf("DefaultGrace = %v, want 3m (D14)", DefaultGrace)
	}
	if g := NewGrace(0); g.d != DefaultGrace {
		t.Fatalf("zero grace = %v", g.d)
	}
}

func TestGraceFailing(t *testing.T) {
	c := &fakeTimers{}
	g := newGrace(time.Minute, c.after)
	if g.Failing() {
		t.Fatal("fresh grace is failing")
	}
	g.Fail("a", errors.New("x"))
	if !g.Failing() {
		t.Fatal("not failing after a failure")
	}
	g.OK("a")
	if g.Failing() {
		t.Fatal("still failing after recovery")
	}
}
