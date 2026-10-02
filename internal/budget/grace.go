package budget

import (
	"sync"
	"time"
)

// DefaultGrace is how long a run may lose its budget backend before it halts
// (decision D14).
const DefaultGrace = 3 * time.Minute

// Grace is the run's outage clock (design R6). Every call to the backend
// reports its outcome under a source name ("lease", "heartbeat", "kill",
// "exchange", ...). A source starts a clock at its first failure; the clock
// stops when that same source succeeds; the grace expires when any clock
// reaches its window. Expiry is sticky: the run halts, whatever happens next.
//
// Sources are independent on purpose. One clock reset by "any success" would
// let a healthy heartbeat hide a lease that has been failing for hours; here
// every failing source must recover by itself. When the whole backend is
// down every source fails together and the outcome is the one the design
// describes, a halt after three minutes.
type Grace struct {
	d     time.Duration
	after func(time.Duration, func()) func() bool

	mu      sync.Mutex
	failing map[string]*downtime
	done    chan struct{}
	fired   bool
	src     string
	err     error
}

type downtime struct {
	stop func() bool
	err  error
}

// NewGrace returns a Grace of window d; zero means DefaultGrace.
func NewGrace(d time.Duration) *Grace {
	return newGrace(d, func(d time.Duration, f func()) func() bool {
		t := time.AfterFunc(d, f)
		return t.Stop
	})
}

func newGrace(d time.Duration, after func(time.Duration, func()) func() bool) *Grace {
	if d <= 0 {
		d = DefaultGrace
	}
	return &Grace{d: d, after: after, failing: map[string]*downtime{}, done: make(chan struct{})}
}

// Fail reports that a call of source failed. The first failure of a source
// starts its clock; later ones only update the cause.
func (g *Grace) Fail(source string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if o, ok := g.failing[source]; ok {
		o.err = err
		return
	}
	o := &downtime{err: err}
	g.failing[source] = o
	o.stop = g.after(g.d, func() { g.expire(source, o) })
}

func (g *Grace) expire(source string, o *downtime) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fired || g.failing[source] != o {
		return
	}
	g.fired, g.src, g.err = true, source, o.err
	close(g.done)
}

// OK reports that a call of source succeeded.
func (g *Grace) OK(source string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if o, ok := g.failing[source]; ok {
		if o.stop != nil {
			o.stop()
		}
		delete(g.failing, source)
	}
}

// Failing reports whether any source is in an outage.
func (g *Grace) Failing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.failing) > 0
}

// Expired is closed when the grace has run out.
func (g *Grace) Expired() <-chan struct{} { return g.done }

// Cause is the source that ran out and its last error, once Expired is closed.
func (g *Grace) Cause() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.src, g.err
}

// Stop cancels every running clock (the run is over).
func (g *Grace) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, o := range g.failing {
		if o.stop != nil {
			o.stop()
		}
		delete(g.failing, k)
	}
}
