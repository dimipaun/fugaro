// The run-side half of the daemon's wait (D2708, D2725).
//
// A run writes nothing under `orchestrator/<id>`: it lives in `agents/<slug>`
// while it is in flight and leaves one entry under `outcomes/<day>` as it ends
// (internal/budget's registry). A wait that listened to the state node alone —
// which is what the daemon did before E1 — therefore learned of a run's end at
// its next cycle, up to the ceiling later, and the ceiling is a person's flag.
// What this file adds is a stream per run-side node and the one wake of the
// level-triggered tick they raise (D1229): the daemon converges on the state it
// finds, and this is what tells it to look now rather than in thirty seconds.
//
// What a write means differs by node, and it is the only thing that differs:
//
//   - `agents/<slug>`: an entry's creation or deletion is the change. Every run
//     in flight rewrites its entry every fifteen seconds (budget.Session's
//     heartbeat), and on the wire a heartbeat and a creation are the same event
//     — a whole-value put of the entry at its own path — so the two are told
//     apart by what the wait has already seen below that node and not by the
//     event. Waking on every heartbeat would tick the daemon several times a
//     second (F45), which is why the presence set exists at all.
//   - `outcomes/<day>`: any write is the change. The node is written once per
//     run, as it ends, so there is no repeat to filter out.
//   - the daemon's own node: any write is the change, as it was before.
//
// A wake is debounced by WakeDebounce: the writes one run's end produces — its
// outcome, its entry going, the next run's start — arrive within moments of
// each other, and one cycle is what they all ask for.
//
// An entry that disappears with no outcome beside it is the run that died
// without reporting (a job the platform killed never writes its outcome, and
// its entry is what something else clears): it wakes the tick like any other
// change, and it is named in Wake.Vanished so that the cycle it wakes checks
// that run's execution through the path the cycle already has for a run that
// stopped reporting. The budget's own five-minute stale sweep stays the
// backstop behind that.
//
// Nothing here reads a model, a store or a bucket, and nothing here writes
// anything: a Wakes is a set of streams, a debounce and the runs whose entry
// vanished, which is what makes a wake a unit test rather than a cloud
// experiment. The remote reads a cycle makes — the merge read and the issue
// sync — keep their own cadence (D2742) whatever woke it: a wake asks for a
// cycle, and a cycle is what the tick's own cadence lives inside.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// WakeDebounce is how long a wake waits for the rest of the burst that raised
// it: writes arriving while it waits join the same wake, so one run's end is
// one cycle and not three (F45).
const WakeDebounce = 2 * time.Second

// wakeDayEvery is how often a wait with no event at all re-reads the day. The
// outcomes node of a new UTC day has to be opened without a write to raise it,
// and a wait that is quiet all night still has to be listening under the new
// day before the first run ends there. A minute of slack costs nothing: it is a
// clock check, not a read, and a run that ends in the seconds just after
// midnight is inside the wait's own ceiling anyway. It is a variable so that a
// test can reach the roll without a night.
var wakeDayEvery = time.Minute

// Streamer is the streaming half of the Realtime Database client
// (rtdb.Client.Stream): one node's events until ctx ends. It is an interface
// for the reason CAS is (store.go): a wait is held to streams a test drives,
// and nothing here reaches for a transport of its own.
type Streamer interface {
	Stream(ctx context.Context, path string) <-chan rtdb.Event
}

// AgentRef is one run's registry location: `agents/<Slug>/<Run>`, and the run
// under `outcomes/<day>/<Slug>/<Run>`.
type AgentRef struct {
	Slug string
	Run  string
}

// Wake is one wake of the wait: the run-side writes that ended it and the runs
// whose registry entry vanished with no outcome beside it (D2725).
type Wake struct {
	// Vanished are the entries that disappeared while no outcome for that run
	// was under the day's outcomes node — a run that most likely died without
	// reporting. The cycle this wakes checks each one's execution through the
	// path it already has for a run that stopped reporting, and the budget's
	// stale sweep stays the backstop behind it.
	Vanished []AgentRef
}

// WakeOptions is what one wait's streams are built from.
type WakeOptions struct {
	// DB opens the streams.
	DB Streamer
	// Own are the daemon's own nodes: a write to one of them — a person's
	// patch, the session-start cache, another writer's import — is a wake.
	Own []string
	// Agents are the repository slugs the state names: the wait streams
	// `agents/<slug>` (AgentsPath) for each of them, because the node a run
	// writes its entry under is built here and not by the caller.
	Agents []string
	// Now is the clock the day's outcomes node is read from. It is the
	// database server's clock where the daemon has one (D1230's rule for
	// every stamp under this subtree), and the local one only where the
	// database has not been read yet. Nil is time.Now.
	Now func() time.Time
	// Log is where a stream that failed leaves its line. Nil says nothing.
	Log func(error)
	// Debounce is how long a wake waits for the rest of its burst. Zero is
	// WakeDebounce; a test shortens it.
	Debounce time.Duration
}

// AgentsPath is `agents/<slug>`: the node one repository's runs hold their
// entries under, and therefore the parent budget.PathAgent builds an entry
// from. It is built from budget's own escaper so that the node a stream
// listens to and the node a run writes are the same key.
func AgentsPath(slug string) string { return "agents/" + budget.Key(slug) }

// OutcomesPath is `outcomes/<day>`: the node one UTC day's outcomes land under,
// the parent of budget.PathOutcome's `<slug>/<run>`.
func OutcomesPath(day int64) string { return "outcomes/" + budget.DayKey(day) }

// Wakes is one wait's streams and the wakes they raise.
type Wakes struct {
	db       Streamer
	own      []string
	agents   []string
	now      func() time.Time
	log      func(error)
	debounce time.Duration
}

// NewWakes builds the waker of one wait.
func NewWakes(o WakeOptions) *Wakes {
	w := &Wakes{db: o.DB, own: o.Own, agents: o.Agents, now: o.Now, log: o.Log, debounce: o.Debounce}
	if w.now == nil {
		w.now = time.Now
	}
	if w.log == nil {
		w.log = func(error) {}
	}
	if w.debounce <= 0 {
		w.debounce = WakeDebounce
	}
	return w
}

// Run opens the streams and sends one Wake per burst of run-side writes until
// ctx ends, when the channel is closed.
//
// A stream that fails is not the wait's failure: the client reconnects with a
// fresh snapshot of the node (rtdb.Client.Stream), so an outage costs the
// wakes that happen inside it and nothing else — the caller's own ceiling is
// what runs a cycle in the meantime, and the next snapshot is passed over as
// the opening snapshot it is, because what it holds is read by the cycle the
// ceiling then runs.
func (w *Wakes) Run(ctx context.Context) <-chan Wake {
	wakes := make(chan Wake, 1)
	go func() {
		defer close(wakes)
		w.run(ctx, wakes)
	}()
	return wakes
}

// wakeKind is which of the three nodes a stream is over: what its events mean
// is decided by this and by nothing else.
type wakeKind int

const (
	wakeOwn wakeKind = iota
	wakeAgents
	wakeOutcomes
)

// wakeStream is one open stream and what its own events have told the wait.
type wakeStream struct {
	kind wakeKind
	path string
	slug string // wakeAgents: the repository whose entries are below this node
	day  int64  // wakeOutcomes: the day this node is
	// opening is set until the connection's first snapshot has been read: the
	// snapshot is the node as it already stands, not news, and every
	// connection — the first and each one after a failure — opens with one.
	opening bool
	// present is the set of entries the stream has seen below its path. It is
	// what tells a run's creation from its heartbeat, which the same put of
	// the same entry carries (the package comment).
	present map[AgentRef]bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// wakeEvent is one event of one stream, on its way to the wait's own select.
type wakeEvent struct {
	st *wakeStream
	ev rtdb.Event
}

func (w *Wakes) run(ctx context.Context, wakes chan<- Wake) {
	r := &wakeRun{w: w, wakes: wakes, in: make(chan wakeEvent), streams: map[*wakeStream]bool{}, day: budget.Day(w.now())}
	sctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		r.wg.Wait()
	}()
	for _, p := range w.own {
		r.open(sctx, &wakeStream{kind: wakeOwn, path: p})
	}
	for _, slug := range w.agents {
		r.open(sctx, &wakeStream{kind: wakeAgents, path: AgentsPath(slug), slug: slug})
	}
	r.outcomes = r.openOutcomes(sctx)
	r.loop(sctx)
}

// wakeRun is one Run call: the open streams, what they have shown, and the
// wake the debounce is holding back.
type wakeRun struct {
	w     *Wakes
	wakes chan<- Wake
	in    chan wakeEvent
	wg    sync.WaitGroup
	// streams is every open stream, so that a closed one's late events are
	// dropped rather than acted on.
	streams map[*wakeStream]bool
	// outcomes is today's outcomes node, which the day's roll replaces.
	outcomes *wakeStream
	day      int64
	// due is the debounce's timer, and vanished are the entries whose deletion
	// has been seen: which of them the wake reports is decided when it is
	// sent, because a run that ends writes its outcome first and clears its
	// entry after, and the two arrive on two streams.
	vanished map[AgentRef]bool
	timer    *time.Timer
	due      <-chan time.Time
}

// open starts one stream of the wait and funnels it into the wait's own
// channel. The goroutine drains the stream until the client closes it, so no
// goroutine of this package outlives the wait.
func (r *wakeRun) open(parent context.Context, st *wakeStream) {
	sctx, cancel := context.WithCancel(parent)
	st.cancel, st.done, st.opening = cancel, make(chan struct{}), true
	if st.present == nil {
		st.present = map[AgentRef]bool{}
	}
	ch := r.w.db.Stream(sctx, st.path)
	r.streams[st] = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(st.done)
		for ev := range ch {
			if sctx.Err() != nil {
				continue
			}
			select {
			case r.in <- wakeEvent{st: st, ev: ev}:
			case <-sctx.Done():
			}
		}
	}()
}

// closeStream ends one stream and waits for its goroutine, so a snapshot of the
// stream it replaces can never be read as the new one's.
func (r *wakeRun) closeStream(st *wakeStream) {
	delete(r.streams, st)
	st.cancel()
	<-st.done
}

func (r *wakeRun) openOutcomes(ctx context.Context) *wakeStream {
	st := &wakeStream{kind: wakeOutcomes, path: OutcomesPath(r.day), day: r.day}
	r.open(ctx, st)
	return st
}

// roll opens today's outcomes node when the day has moved under the wait: a run
// that ends after midnight writes under the new day, and a stream left on
// yesterday's node would carry nothing until the ceiling (D2708).
func (r *wakeRun) roll(ctx context.Context) {
	if d := budget.Day(r.w.now()); d != r.day {
		r.day = d
		r.closeStream(r.outcomes)
		r.outcomes = r.openOutcomes(ctx)
	}
}

func (r *wakeRun) loop(ctx context.Context) {
	day := time.NewTicker(wakeDayEvery)
	defer day.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-day.C:
			r.roll(ctx)
		case <-r.due:
			r.deliver(ctx)
		case e := <-r.in:
			r.roll(ctx)
			r.event(e)
		}
	}
}

// deliver sends the wake the debounce has been holding, and reports which of
// the entries it saw vanish really had no outcome written for them by then.
func (r *wakeRun) deliver(ctx context.Context) {
	r.timer.Stop()
	r.due = nil
	var wake Wake
	for ref := range r.vanished {
		if !r.outcomes.present[ref] {
			wake.Vanished = append(wake.Vanished, ref)
		}
	}
	sort.Slice(wake.Vanished, func(i, j int) bool {
		if wake.Vanished[i].Slug != wake.Vanished[j].Slug {
			return wake.Vanished[i].Slug < wake.Vanished[j].Slug
		}
		return wake.Vanished[i].Run < wake.Vanished[j].Run
	})
	r.vanished = nil
	select {
	case r.wakes <- wake:
	case <-ctx.Done():
	}
}

// raise starts the debounce, or joins the burst already waiting in it: a wake
// is not postponed by the writes that follow it.
func (r *wakeRun) raise() {
	if r.due != nil {
		return
	}
	r.timer = time.NewTimer(r.w.debounce)
	r.due = r.timer.C
}

// vanish records an entry whose deletion the wait has seen, without deciding
// yet whether the run died or ended (deliver).
func (r *wakeRun) vanish(ref AgentRef) {
	if r.vanished == nil {
		r.vanished = map[AgentRef]bool{}
	}
	r.vanished[ref] = true
}

func (r *wakeRun) event(e wakeEvent) {
	st := e.st
	if !r.streams[st] || (st.kind == wakeOutcomes && st.day != r.day) {
		return
	}
	switch e.ev.Type {
	case "put", "patch":
	case "error", "auth_revoked":
		// The client reconnects itself; the next snapshot is passed over as
		// the opening snapshot it is, and the wait's ceiling runs the cycle
		// that catches up.
		st.opening = true
		if e.ev.Err != nil {
			r.w.log(fmt.Errorf("the stream at %s failed: %w", st.path, e.ev.Err))
		}
		return
	case "cancel":
		// The server withdrew the listen — a rule no longer allows it — and
		// the client ends the stream: the wait falls back to its ceiling, and
		// says so once per stream rather than once per cycle.
		r.w.log(fmt.Errorf("the stream at %s was cancelled by the database: the wait falls back to its ceiling", st.path))
		return
	default:
		return
	}
	if st.opening {
		st.opening = false
		st.learn(e.ev, false)
		return
	}
	switch st.kind {
	case wakeOwn:
		r.raise()
	case wakeOutcomes:
		st.learn(e.ev, false)
		r.raise()
	default:
		added, removed := st.learn(e.ev, true)
		for _, ref := range removed {
			r.vanish(ref)
		}
		if len(added) > 0 || len(removed) > 0 {
			r.raise()
		}
	}
}

// learn walks one event into the entries below the stream's path and reports
// the entries it added and removed. With report false nothing is reported —
// the opening snapshot — but the presence set is still built from it, which is
// what the snapshot is for.
func (st *wakeStream) learn(ev rtdb.Event, report bool) (added, removed []AgentRef) {
	entries, whole := st.named(ev)
	named := map[AgentRef]bool{}
	for _, en := range entries {
		named[en.ref] = en.here
		if !report {
			continue
		}
		if en.here && !st.present[en.ref] {
			added = append(added, en.ref)
		}
		if !en.here && st.present[en.ref] {
			removed = append(removed, en.ref)
		}
	}
	if whole {
		if report {
			for ref := range st.present {
				if _, ok := named[ref]; !ok {
					removed = append(removed, ref)
				}
			}
		}
		st.present = map[AgentRef]bool{}
	}
	for _, en := range entries {
		if en.here {
			st.present[en.ref] = true
			continue
		}
		delete(st.present, en.ref)
	}
	return added, removed
}

// wakeEntry is one entry an event names, and whether the entry is there after
// it: a write of JSON null deletes.
type wakeEntry struct {
	ref  AgentRef
	here bool
}

// named is the entries one event names and whether the event is a write of the
// whole node — a put of the node itself replaces what is below it, so every
// entry the value does not hold is gone.
//
// Anything deeper than an entry names nothing: a run rewriting one field of its
// own entry is a heartbeat, and the entry is already in the presence set. For
// the same reason nothing here reads the fields of an entry: which run it is,
// is the whole of what the wait needs to know about it.
func (st *wakeStream) named(ev rtdb.Event) ([]wakeEntry, bool) {
	segs := relSegments(ev.Path)
	depth := 1
	if st.kind == wakeOutcomes {
		depth = 2
	}
	if len(segs) == 0 {
		var held map[string]json.RawMessage
		if json.Unmarshal(ev.Data, &held) != nil {
			return nil, ev.Type == "put"
		}
		return st.children(held, depth), ev.Type == "put"
	}
	if depth == 1 {
		return []wakeEntry{{AgentRef{Slug: st.slug, Run: segs[0]}, !(len(segs) == 1 && isNull(ev.Data))}}, false
	}
	if len(segs) == 1 {
		// A patch of one repository's runs names the runs it carries as its
		// keys. Anything else at this depth (a put of one repository's whole
		// set, which nothing in the tree does) is read as what its data holds.
		var runs map[string]json.RawMessage
		if json.Unmarshal(ev.Data, &runs) != nil {
			return nil, false
		}
		return st.children(runs, 1), false
	}
	return []wakeEntry{{AgentRef{Slug: segs[0], Run: segs[1]}, !(len(segs) == 2 && isNull(ev.Data))}}, false
}

// children is the entries a map of one node's children names, at the depth of
// the entries below this stream's path: one level for `agents/<slug>`, two for
// `outcomes/<day>`.
func (st *wakeStream) children(m map[string]json.RawMessage, depth int) []wakeEntry {
	out := make([]wakeEntry, 0, len(m))
	if depth == 1 {
		for run, v := range m {
			out = append(out, wakeEntry{AgentRef{Slug: st.slug, Run: run}, !isNull(v)})
		}
		return out
	}
	for slug, raw := range m {
		var runs map[string]json.RawMessage
		if json.Unmarshal(raw, &runs) != nil {
			continue
		}
		for run, v := range runs {
			out = append(out, wakeEntry{AgentRef{Slug: slug, Run: run}, !isNull(v)})
		}
	}
	return out
}

// relSegments is an event's path as its segments below the streamed node: "/"
// — the node itself — is none, "/a/b" is two.
func relSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// isNull is the database's deletion: a value of JSON null removes the node it
// is written at.
func isNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}
