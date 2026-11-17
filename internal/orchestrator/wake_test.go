package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The fixture of these tests: a Wakes held to streams a test drives, so that a
// wake — which in production is a database connection and a person's patience —
// is an event on a channel and a clock the test moves.
const (
	wakeSlug   = "acme-app"
	wakeState  = "orchestrator/aurora/state"
	wakeRunOne = "20261117-101500-abcd"
	wakeRunTwo = "20261117-101600-beef"
	// wakeDebounce is the debounce these tests run with: the same shape as
	// WakeDebounce, shortened so that "the wake waits" is a millisecond count.
	wakeDebounce = 40 * time.Millisecond
)

type wakeFixture struct {
	t     *testing.T
	src   *wakeSource
	clock *wakeClock
	day   int64
	log   *wakeLog
	wakes <-chan Wake
}

func newWakeFixture(t *testing.T) *wakeFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clock := &wakeClock{at: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
	logged := &wakeLog{}
	f := &wakeFixture{t: t, src: newWakeSource(), clock: clock, day: budget.Day(clock.now()), log: logged}
	f.wakes = NewWakes(WakeOptions{
		DB:       f.src,
		Own:      []string{wakeState},
		Agents:   []string{wakeSlug},
		Now:      clock.now,
		Log:      logged.add,
		Debounce: wakeDebounce,
	}).Run(ctx)
	f.src.waitOpen(t, 3)
	return f
}

// open sends the snapshot every connection opens with: the node as it stands,
// which is what the wait's presence set starts from.
func (f *wakeFixture) open() {
	f.t.Helper()
	f.snapshot(wakeState, "null")
	f.snapshot(AgentsPath(wakeSlug), "null")
	f.snapshot(OutcomesPath(f.day), "null")
}

func (f *wakeFixture) snapshot(path, data string) {
	f.t.Helper()
	f.send(path, rtdb.Event{Type: "put", Path: "/", Data: json.RawMessage(data)})
}

func (f *wakeFixture) put(path, rel, data string) {
	f.t.Helper()
	f.send(path, rtdb.Event{Type: "put", Path: rel, Data: json.RawMessage(data)})
}

func (f *wakeFixture) send(path string, ev rtdb.Event) {
	f.t.Helper()
	f.src.feed(f.t, path).send(ev)
}

func (f *wakeFixture) agents() string   { return AgentsPath(wakeSlug) }
func (f *wakeFixture) outcomes() string { return OutcomesPath(f.day) }

func (f *wakeFixture) wakeWithin(d time.Duration) (Wake, bool) {
	f.t.Helper()
	select {
	case w, ok := <-f.wakes:
		if !ok {
			f.t.Fatal("the wait's channel closed, so the daemon has no ceiling to fall back to")
		}
		return w, true
	case <-time.After(d):
		return Wake{}, false
	}
}

func (f *wakeFixture) expectWake(d time.Duration) Wake {
	f.t.Helper()
	w, ok := f.wakeWithin(d)
	if !ok {
		f.t.Fatalf("no wake within %s", d)
	}
	return w
}

func (f *wakeFixture) expectNoWake(d time.Duration) {
	f.t.Helper()
	if w, ok := f.wakeWithin(d); ok {
		f.t.Fatalf("a wake came within %s, and nothing that wakes the tick happened: %+v", d, w)
	}
}

// TestOutcomeWritesWakeTheTick: a run's outcome is the write E1 exists for. It
// lands under `outcomes/<day>/<slug>/<run>` (budget.PathOutcome, the registry's
// own path, which the wait listens above), and until this task the daemon
// learned of it at its next cycle — up to the whole ceiling later.
func TestOutcomeWritesWakeTheTick(t *testing.T) {
	f := newWakeFixture(t)
	f.open()
	f.expectNoWake(3 * wakeDebounce)
	path := budget.PathOutcome(f.day, wakeSlug, wakeRunOne)
	if !strings.HasPrefix(path, f.outcomes()+"/") {
		t.Fatalf("an outcome is not below the node the wait listens to: %s is not under %s", path, f.outcomes())
	}
	f.put(f.outcomes(), strings.TrimPrefix(path, f.outcomes()), `{"status":"succeeded"}`)
	if w := f.expectWake(2 * time.Second); len(w.Vanished) != 0 {
		t.Fatalf("an outcome's write named a vanished run: %+v", w.Vanished)
	}
}

// TestHeartbeatDoesNotWake: every run in flight rewrites its registry entry
// every fifteen seconds, and a heartbeat is the same put of the same entry a
// run's start is — so a wait that woke on it would tick several times a second
// (F45). What tells the two apart is what the wait has already seen.
func TestHeartbeatDoesNotWake(t *testing.T) {
	f := newWakeFixture(t)
	entry := `{"repo":"acme/app","execution":"jobs/abc"}`
	f.open()
	f.put(f.agents(), "/"+wakeRunOne, entry)
	f.expectWake(2 * time.Second) // the run's start, which is a wake
	for i := 0; i < 5; i++ {
		f.put(f.agents(), "/"+wakeRunOne, entry)
	}
	f.expectNoWake(5 * wakeDebounce)
	// The next run's start is the same write at another path, and that one is
	// a wake: the wait is not asleep, it is filtering.
	f.put(f.agents(), "/"+wakeRunTwo, entry)
	f.expectWake(2 * time.Second)
}

// TestWakesAreDebounced: one run's end is several writes — its outcome, its
// entry going, the next run's start — and they are one cycle's business. The
// wake waits out the burst it was raised in, and does not restart its wait for
// each write that joins it.
func TestWakesAreDebounced(t *testing.T) {
	f := newWakeFixture(t)
	f.open()
	f.expectNoWake(3 * wakeDebounce)
	start := time.Now()
	for i := 0; i < 5; i++ {
		f.put(f.outcomes(), fmt.Sprintf("/%s/run-%d", wakeSlug, i), `{"status":"succeeded"}`)
		time.Sleep(wakeDebounce / 8)
	}
	f.expectWake(2 * time.Second)
	if d := time.Since(start); d < wakeDebounce {
		t.Fatalf("the wake came %s after the first write, inside its own debounce", d)
	}
	f.expectNoWake(5 * wakeDebounce)
}

// TestVanishedAgentEntryWakesAndChecks: a registry entry that disappears is
// either a run that reported (its outcome came first — the budget's registry
// writes the outcome and clears the entry after it) or one that died without
// reporting, and only the second is a run whose execution the woken cycle has
// to check (D2725). Which one it is, is decided when the wake is sent, because
// the two writes arrive on two streams.
func TestVanishedAgentEntryWakesAndChecks(t *testing.T) {
	f := newWakeFixture(t)
	f.open()
	f.put(f.agents(), "/"+wakeRunOne, `{"repo":"acme/app"}`)
	f.expectWake(2 * time.Second)

	// The entry of a run that never wrote an outcome: it vanished, and the
	// wake names it so that the cycle that follows checks its execution.
	f.put(f.agents(), "/"+wakeRunOne, "null")
	w := f.expectWake(2 * time.Second)
	if len(w.Vanished) != 1 || w.Vanished[0] != (AgentRef{Slug: wakeSlug, Run: wakeRunOne}) {
		t.Fatalf("a vanished entry woke the wait with %+v, want %s/%s", w.Vanished, wakeSlug, wakeRunOne)
	}

	// The two writes of one ordinary end, read in the order the streams
	// happened to deliver them: the entry goes first and the outcome — which
	// the run wrote before it — arrives inside the wake's own debounce. The
	// run reported, so nothing about it is left to check.
	f.put(f.agents(), "/"+wakeRunTwo, `{"repo":"acme/app"}`)
	f.expectWake(2 * time.Second)
	f.put(f.agents(), "/"+wakeRunTwo, "null")
	f.put(f.outcomes(), "/"+wakeSlug+"/"+wakeRunTwo, `{"status":"succeeded"}`)
	if w := f.expectWake(2 * time.Second); len(w.Vanished) != 0 {
		t.Fatalf("a run that wrote its outcome was reported vanished: %+v", w.Vanished)
	}
}

// TestOpeningSnapshotDoesNotWake: every connection starts with a put of the
// node as it stands, and that is not news — the cycle that just ran read the
// same node. Waking on it would end every wait in microseconds and spin the
// loop at a cadence it never honours.
func TestOpeningSnapshotDoesNotWake(t *testing.T) {
	f := newWakeFixture(t)
	f.snapshot(wakeState, `{"schema_version":7,"work_items":[]}`)
	f.snapshot(f.agents(), `{"`+wakeRunOne+`":{"repo":"acme/app"}}`)
	f.snapshot(f.outcomes(), `{"`+wakeSlug+`":{"`+wakeRunTwo+`":{"status":"succeeded"}}}`)
	f.expectNoWake(5 * wakeDebounce)
}

// TestStreamFailureFallsBackToCeiling: a stream that fails is the database's
// problem and not the daemon's. The client reconnects with a fresh snapshot of
// the node rather than ending the stream, and a stream the server withdrew ends
// — either way the wait keeps running and keeps its ceiling, and the cycle that
// ceiling runs is what catches up on what the outage missed.
func TestStreamFailureFallsBackToCeiling(t *testing.T) {
	f := newWakeFixture(t)
	f.open()
	f.expectNoWake(3 * wakeDebounce)

	f.send(f.outcomes(), rtdb.Event{Type: "error", Err: errors.New("connection reset by peer")})
	f.expectNoWake(3 * wakeDebounce)
	if n := f.log.count(); n == 0 {
		t.Fatal("a stream that failed said nothing")
	}
	// The reconnect's own snapshot, holding the run that wrote while the wait
	// was down: the ceiling's cycle reads it, and reading it again is not a
	// reason to wake.
	f.snapshot(f.outcomes(), `{"`+wakeSlug+`":{"`+wakeRunOne+`":{"status":"succeeded"}}}`)
	f.expectNoWake(3 * wakeDebounce)
	f.put(f.outcomes(), "/"+wakeSlug+"/"+wakeRunTwo, `{"status":"succeeded"}`)
	f.expectWake(2 * time.Second)

	// A stream the server withdraws (a rule no longer allows the listen) ends
	// where a failure does not: the wait is left with its ceiling.
	f.send(f.agents(), rtdb.Event{Type: "cancel"})
	f.src.feed(t, f.agents()).close()
	f.expectNoWake(3 * wakeDebounce)
	f.put(f.outcomes(), "/"+wakeSlug+"/run-three", `{"status":"succeeded"}`)
	f.expectWake(2 * time.Second)
}

// TestDayChangeReopensOutcomes: outcomes are per UTC day, so a wait that
// opened today's node and went quiet is listening to yesterday's after
// midnight — and a run that ends then writes under a node nobody is watching
// until the ceiling brings the cycle round. The day is re-read on the wait's
// own clock, without a write to raise it.
func TestDayChangeReopensOutcomes(t *testing.T) {
	was := wakeDayEvery
	wakeDayEvery = 20 * time.Millisecond
	// Registered before the fixture, so it runs after the fixture's own
	// cleanup has stopped the wait that reads it.
	t.Cleanup(func() { wakeDayEvery = was })

	f := newWakeFixture(t)
	f.open()
	yesterday := f.src.feed(t, f.outcomes())
	f.expectNoWake(3 * wakeDebounce)

	f.clock.add(24 * time.Hour)
	f.src.waitOpen(t, 4)
	next := OutcomesPath(f.day + 1)
	f.snapshot(next, "null")
	f.expectNoWake(3 * wakeDebounce)

	f.put(next, "/"+wakeSlug+"/"+wakeRunOne, `{"status":"succeeded"}`)
	f.expectWake(2 * time.Second)
	if !yesterday.isClosed() {
		t.Fatal("the wait is still listening to yesterday's outcomes node")
	}
}

// wakeSource is the scripted Streamer of these tests: one feed per Stream call,
// addressed by the path it was opened for.
type wakeSource struct {
	mu     sync.Mutex
	feeds  map[string][]*wakeFeed
	opened int
}

func newWakeSource() *wakeSource { return &wakeSource{feeds: map[string][]*wakeFeed{}} }

func (s *wakeSource) Stream(ctx context.Context, path string) <-chan rtdb.Event {
	f := &wakeFeed{ch: make(chan rtdb.Event, 32)}
	s.mu.Lock()
	s.feeds[path] = append(s.feeds[path], f)
	s.opened++
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.close()
	}()
	return f.ch
}

func (s *wakeSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

// waitOpen waits for the wait to have opened n streams: the paths a wait
// listens to are decided by the caller's state, and a test that wrote an event
// before the stream existed would be writing into nothing.
func (s *wakeSource) waitOpen(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the wait opened %d streams, want %d", s.count(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *wakeSource) feed(t *testing.T, path string) *wakeFeed {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	fs := s.feeds[path]
	if len(fs) == 0 {
		t.Fatalf("the wait never opened a stream at %s", path)
	}
	return fs[len(fs)-1]
}

// wakeFeed is one stream a test writes events into. A stream ends where the
// real one does — when its context does — and a write after that is dropped
// rather than a panic, so a test needs no ordering of its own.
type wakeFeed struct {
	ch     chan rtdb.Event
	mu     sync.Mutex
	closed bool
}

func (f *wakeFeed) send(ev rtdb.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.ch <- ev
}

func (f *wakeFeed) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.ch)
}

func (f *wakeFeed) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// wakeClock is the clock the wait reads: the database server's in production
// (D1230), and a test's own here, because the day's roll is otherwise a night
// long.
type wakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *wakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *wakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// wakeLog is what the wait says about its streams.
type wakeLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *wakeLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, err.Error())
}

func (l *wakeLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func (l *wakeLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}
