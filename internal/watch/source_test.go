package watch

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// clk is a fake clock the tests move.
type clk struct{ ns atomic.Int64 }

func newClk(t time.Time) *clk      { c := &clk{}; c.ns.Store(t.UnixNano()); return c }
func (c *clk) Now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *clk) Add(d time.Duration) { c.ns.Add(int64(d)) }

type rig struct {
	t      *testing.T
	f      *gcpfake.RTDB
	clk    *clk
	cancel context.CancelFunc
	sup    *Supervisor
	db     *rtdb.Client
	done   chan struct{}

	mu sync.Mutex
	st *State
}

type blockSSE struct{ http.RoundTripper }

func (b blockSSE) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Accept") == "text/event-stream" {
		return nil, fmt.Errorf("proxy buffers event streams")
	}
	return b.RoundTripper.RoundTrip(r)
}

// start runs a supervisor against f whose clock is c; seed runs before it.
func start(t *testing.T, f *gcpfake.RTDB, c *clk, o Options, block bool) *rig {
	t.Helper()
	tr := &http.Transport{DisableKeepAlives: true}
	var rt http.RoundTripper = tr
	if block {
		rt = blockSSE{tr}
	}
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "tok" }},
		rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond), rtdb.WithHTTPClient(&http.Client{Transport: rt}))
	if err != nil {
		t.Fatal(err)
	}
	o.Now, o.Tick = c.Now, 5*time.Millisecond
	if o.Interval == 0 {
		o.Interval = 20 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, f: f, clk: c, cancel: cancel, st: NewState(), done: make(chan struct{})}
	r.db = db
	r.sup = Start(ctx, db, o)
	go func() {
		defer close(r.done)
		for u := range r.sup.Updates() {
			r.mu.Lock()
			r.st.Handle(u)
			r.mu.Unlock()
		}
	}()
	t.Cleanup(func() { cancel(); <-r.done; tr.CloseIdleConnections() })
	return r
}

func (r *rig) view() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Build(r.st, r.clk.Now(), Config{})
}

func (r *rig) until(what string, ok func(View) bool) View {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := r.view()
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s; view %+v", what, v)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func runIDs(v View) (ids []string) {
	for _, b := range v.Repos {
		for _, r := range b.Runs {
			ids = append(ids, r.Run)
		}
	}
	return ids
}

func seed(f *gcpfake.RTDB, day int64, runs ...string) {
	f.Set("config/mode", "enforce")
	for _, id := range runs {
		f.Set("agents/lib/"+id, map[string]any{"repo": "lib", "title": "t-" + id, "startedAt": t0.UnixMilli(), "updatedAt": t0.UnixMilli()})
	}
	f.Set(budget.PathSpendGlobal(day), map[string]any{"spent": 1000000, "counted": 1000000})
	f.Set(budget.PathSpendRepo(day, "lib"), map[string]any{"spent": 1000000, "counted": 1000000})
}

func TestFourStreamsOpened(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClk(t0)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, c, Options{}, false)
	v := r.until("all four trees", func(v View) bool {
		return v.Mode == "enforce" && v.Project.Spent == usd && len(runIDs(v)) == 1 && len(v.Repos) == 1 && v.Repos[0].Spent == usd
	})
	if v.Conn.Kind != ConnLive || f.Streams() != 4 {
		t.Fatalf("conn %v streams %d", v.Conn, f.Streams())
	}
}

func TestPutAfterReconnectReplacesTree(t *testing.T) { // supervisor flavour: an outage hides a finished run
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1", "r2")
	r := start(t, f, newClk(t0), Options{}, false)
	r.until("two runs", func(v View) bool { return len(runIDs(v)) == 2 })
	f.Refuse(503, "UNAVAILABLE", "", "down")
	f.DropStreams()
	r.until("offline", func(v View) bool { return v.Conn.Kind == ConnOffline })
	f.Set("agents/lib/r2", nil) // finished during the outage
	f.Refuse(0, "", "", "")
	v := r.until("reconnected without r2", func(v View) bool {
		return (v.Conn.Kind == ConnLive || v.Conn.Kind == ConnPolling) && len(runIDs(v)) == 1 // the outage may also trip the polling fallback
	})
	if runIDs(v)[0] != "r1" {
		t.Fatalf("runs %v", runIDs(v))
	}
}

func TestOfflineGreysFrame(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClk(t0)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, c, Options{}, false)
	r.until("live", func(v View) bool { return v.Conn.Kind == ConnLive && len(runIDs(v)) == 1 })
	f.Refuse(503, "UNAVAILABLE", "", "backend down")
	f.DropStreams()
	r.until("offline", func(v View) bool { return v.Conn.Kind == ConnOffline })
	c.Add(20 * time.Second)
	v := r.until("offline with age", func(v View) bool { return v.Conn.Kind == ConnOffline && v.Conn.Age >= 20*time.Second })
	if v.Conn.Reason == "" || len(runIDs(v)) != 1 || v.Project.Spent != usd {
		t.Fatalf("the last frame must stay, with a reason: %+v", v)
	}
}

func TestStaleBanner(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClk(t0)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, c, Options{}, false)
	r.until("live", func(v View) bool { return v.Conn.Kind == ConnLive && len(runIDs(v)) == 1 })
	c.Add(44 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if k := r.view().Conn.Kind; k != ConnLive {
		t.Fatalf("44 s of silence is not stale yet: %v", k)
	}
	c.Add(2 * time.Second)
	v := r.until("stale", func(v View) bool { return v.Conn.Kind == ConnStale })
	if v.Conn.Age < 46*time.Second {
		t.Fatalf("age %v", v.Conn.Age)
	}
	f.SendKeepAlive() // a keep-alive is proof of life
	r.until("live again", func(v View) bool { return v.Conn.Kind == ConnLive })
}

func TestMidnightResubscribe(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	d := budget.Day(t0)
	c := newClk(time.UnixMilli((d+1)*86_400_000 - 2000)) // two seconds before midnight
	seed(f, d, "r1")
	f.Set(budget.PathSpendGlobal(d+1), map[string]any{"spent": 7000000})
	f.Set(budget.PathSpendRepo(d, "old"), map[string]any{"spent": 5})
	r := start(t, f, c, Options{}, false)
	r.until("yesterday", func(v View) bool { return v.Project.Spent == usd && v.Day == budget.DayDate(d) })
	c.Add(3 * time.Second)
	v := r.until("today", func(v View) bool { return v.Day == budget.DayDate(d+1) && v.Project.Spent == 7*usd })
	if len(v.Repos) != 1 || v.Repos[0].Name != "lib" { // lib has agents; "old" and yesterday's counters are gone
		t.Fatalf("repos %+v", v.Repos)
	}
	if v.Repos[0].Spent != 0 {
		t.Fatalf("yesterday's repo counter leaked: %+v", v.Repos[0])
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.Streams() != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("%d streams open, want 4 (the old pair must be closed)", f.Streams())
		}
		time.Sleep(5 * time.Millisecond)
	}
	live := func(day int64, spent int) { // a live write, which the fake streams (Set does not)
		if err := r.db.Patch(context.Background(), budget.PathSpendGlobal(day), map[string]any{"spent": spent}); err != nil {
			t.Fatal(err)
		}
	}
	live(d+1, 8000000)
	r.until("new day updates", func(v View) bool { return v.Project.Spent == 8*usd })
	live(d, 99) // the old day is no longer listened to
	time.Sleep(50 * time.Millisecond)
	if r.view().Project.Spent != 8*usd {
		t.Fatal("still listening to yesterday")
	}
}

func TestPermissionErrorStopsRetrySpam(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	f.Refuse(403, "PERMISSION_DENIED", "", "no")
	r := start(t, f, newClk(t0), Options{}, false)
	r.until("refused", func(v View) bool { return v.Conn.Kind == ConnRefused })
	time.Sleep(100 * time.Millisecond)
	n := len(f.Requests())
	time.Sleep(300 * time.Millisecond)
	if m := len(f.Requests()); m != n {
		t.Fatalf("kept retrying: %d -> %d requests", n, m)
	}
	if r.view().Conn.Kind != ConnRefused {
		t.Fatal("the refusal must stay shown")
	}
}

func TestFallsBackToPolling(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1", "r2")
	r := start(t, f, newClk(t0), Options{SSERetry: time.Hour}, true)
	v := r.until("polling with data", func(v View) bool { return v.Conn.Kind == ConnPolling && len(runIDs(v)) == 2 })
	_ = v
	f.Set("agents/lib/r2", nil)
	f.Set(budget.PathSpendGlobal(budget.Day(t0)), map[string]any{"spent": 3000000})
	r.until("poll picks up changes", func(v View) bool {
		return len(runIDs(v)) == 1 && v.Project.Spent == 3*usd && v.Conn.Kind == ConnPolling
	})
}

func TestPollFlagNeverStreams(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, newClk(t0), Options{Poll: true, SSERetry: 10 * time.Millisecond}, false)
	r.until("polling", func(v View) bool { return v.Conn.Kind == ConnPolling && len(runIDs(v)) == 1 })
	time.Sleep(100 * time.Millisecond)
	if n := f.Streams(); n != 0 {
		t.Fatalf("--poll opened %d streams", n)
	}
}

func TestRetriesStreamFromPolling(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	tr := &http.Transport{DisableKeepAlives: true}
	var block atomic.Bool
	block.Store(true)
	rt := roundTrip(func(r *http.Request) (*http.Response, error) {
		if block.Load() && r.Header.Get("Accept") == "text/event-stream" {
			return nil, fmt.Errorf("buffered")
		}
		return tr.RoundTrip(r)
	})
	db, _ := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "t" }}, rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond), rtdb.WithHTTPClient(&http.Client{Transport: rt}))
	ctx, cancel := context.WithCancel(context.Background())
	sup := Start(ctx, db, Options{Now: newClk(t0).Now, Tick: 5 * time.Millisecond, Interval: 10 * time.Millisecond, SSERetry: 50 * time.Millisecond})
	defer func() {
		cancel()
		for range sup.Updates() {
		}
		tr.CloseIdleConnections()
	}()
	st := NewState()
	var sawPoll bool
	timeout := time.After(10 * time.Second)
	for {
		select {
		case u := <-sup.Updates():
			st.Handle(u)
			if u.Kind == UpdPolling && u.On {
				sawPoll = true
				block.Store(false) // the proxy goes away: the next trial succeeds
			}
			if u.Kind == UpdPolling && !u.On && sawPoll && f.Streams() > 0 {
				return
			}
		case <-timeout:
			t.Fatal("never went back to streaming")
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCancelClosesStreams(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, newClk(t0), Options{}, false)
	r.until("live", func(v View) bool { return len(runIDs(v)) == 1 && v.Project.Spent == usd })
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Updates not closed after cancel")
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.Streams() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d streams left open", f.Streams())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	d := budget.Day(t0)
	seed(f, d, "r1")
	c := newClk(time.UnixMilli((d+1)*86_400_000 - 1000))
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	r := start(t, f, c, Options{}, false)
	r.until("live", func(v View) bool { return len(runIDs(v)) == 1 })
	c.Add(2 * time.Second) // a midnight rollover
	r.until("rolled", func(v View) bool { return v.Day == budget.DayDate(d+1) })
	f.DropStreams()
	f.SendKeepAlive()
	time.Sleep(50 * time.Millisecond)
	r.cancel()
	<-r.done
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= before+1 { // +1: the rig's own test cleanup
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines %d -> %d\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A panic in the supervisor goroutine must surface as an offline state, never
// crash the process (and with it the terminal).
func TestSupervisorPanicSurfacesOffline(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "tok" }})
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := Start(ctx, db, Options{Tick: 5 * time.Millisecond, Now: func() time.Time {
		if n.Add(1) == 3 {
			panic("boom")
		}
		return t0
	}})
	st := NewState()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case u, ok := <-sup.Updates():
			if !ok {
				t.Fatal("updates closed without an error state")
			}
			st.Handle(u)
			if c := Build(st, t0, Config{}).Conn; c.Kind == ConnOffline && strings.Contains(c.Reason, "internal error") {
				return
			}
		case <-deadline:
			t.Fatalf("no offline state: %+v", Build(st, t0, Config{}).Conn)
		}
	}
}
