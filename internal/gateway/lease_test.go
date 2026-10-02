package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// fakeLease is a Lease the tests script. Grant returns max(need, size)
// plus whatever extra says, unless err or gate say otherwise.
type fakeLease struct {
	mu       sync.Mutex
	size     pricing.Micros
	extra    func(need pricing.Micros) pricing.Micros
	err      func(need pricing.Micros) error // non-nil refuses or fails
	gate     chan struct{}                   // non-nil: Grant waits for it (or its ctx)
	entered  chan struct{}                   // non-nil: Grant signals entry
	needs    []pricing.Micros
	granted  pricing.Micros
	inGrant  int
	maxIn    int
	released []pricing.Micros
	relErr   error
	reports  []StageReport
	ctxErrs  []error
}

func (l *fakeLease) Grant(ctx context.Context, need pricing.Micros) (pricing.Micros, error) {
	l.mu.Lock()
	l.needs = append(l.needs, need)
	l.inGrant++
	l.maxIn = max(l.maxIn, l.inGrant)
	gate, entered := l.gate, l.entered
	l.mu.Unlock()
	defer func() { l.mu.Lock(); l.inGrant--; l.mu.Unlock() }()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			l.mu.Lock()
			l.ctxErrs = append(l.ctxErrs, ctx.Err())
			l.mu.Unlock()
			return 0, ctx.Err()
		}
	}
	if l.err != nil {
		if err := l.err(need); err != nil {
			return 0, err
		}
	}
	g := max(need, l.size)
	if l.extra != nil {
		g += l.extra(need)
	}
	l.mu.Lock()
	l.granted += g
	l.mu.Unlock()
	return g, nil
}

func (l *fakeLease) Report(_ context.Context, r StageReport) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reports = append(l.reports, r)
	return nil
}

func (l *fakeLease) Release(_ context.Context, unused pricing.Micros) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released = append(l.released, unused)
	return l.relErr
}

func (l *fakeLease) grantCalls() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.needs) }

func withLease(l Lease, mode Mode) func(*Options) {
	return func(o *Options) { o.Lease, o.Mode, o.Cap = l, mode, 0 }
}

func haltOf(t *testing.T, h *harness) Halt {
	t.Helper()
	select {
	case halt := <-h.gw.Halted():
		return halt
	case <-time.After(2 * time.Second):
		t.Fatal("Halted() didn't fire")
	}
	return Halt{}
}

func noHalt(t *testing.T, h *harness) {
	t.Helper()
	select {
	case halt := <-h.gw.Halted():
		t.Fatalf("the run halted: %+v", halt)
	default:
	}
}

var leaseBody = msg(sonnet, 1000, `"stream":true`)

func TestLeaseGrowsGrantedOnly(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 800}
	w := worst(t, sonnet, leaseBody, 1000, "")
	l := &fakeLease{size: 3 * w}
	h := newHarnessWith(t, withLease(l, Enforce), anthropicfake.StreamOK(sonnet, u), anthropicfake.StreamOK(sonnet, u))
	if g := h.gw.Ledger().Granted; g != 0 {
		t.Fatalf("granted %d before any grant", g)
	}
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("first call: %d %s", resp.StatusCode, b)
	}
	led := h.gw.Ledger()
	if led.Granted != 3*w || led.Used != cost(t, sonnet, u) || led.Reserved != 0 {
		t.Fatalf("ledger %+v, want granted %d", led, 3*w)
	}
	if l.grantCalls() != 1 || l.needs[0] != w {
		t.Errorf("grants %v, want one grant needing %d", l.needs, w)
	}
	// The second call fits the lease: no new grant, Granted unchanged.
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("second call: %d %s", resp.StatusCode, b)
	}
	if l.grantCalls() != 1 || h.gw.Ledger().Granted != 3*w {
		t.Errorf("granted moved without a grant: %d grants, ledger %+v", l.grantCalls(), h.gw.Ledger())
	}
	noHalt(t, h)
}

// The top-up asks for the shortfall, not the whole call.
func TestTopUpAsksForTheShortfall(t *testing.T) {
	w := worst(t, sonnet, leaseBody, 1000, "")
	l := &fakeLease{size: w / 2}
	h := newHarnessWith(t, withLease(l, Enforce), anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 1, Output: 1}), anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 1, Output: 1}))
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	// granted = w (need w, size w/2); used tiny; the second call needs more.
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if n := l.grantCalls(); n != 2 {
		t.Fatalf("%d grants, want 2", n)
	}
	led := h.gw.Ledger()
	if led.Granted != l.granted {
		t.Errorf("granted %d != sum of grants %d", led.Granted, l.granted)
	}
}

func TestLedgerNeverExceedsGrantedWithLease(t *testing.T) {
	const workers, perWorker = 50, 4
	var (
		mu       sync.Mutex
		breaches []string
	)
	l := &fakeLease{extra: func(pricing.Micros) pricing.Micros {
		return pricing.Micros(rand.IntN(500_000))
	}}
	h := newHarnessWith(t, withLease(l, Enforce))
	h.fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		return anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 100, Output: 100})
	}
	h.gw.onLedger = func(led Ledger, overrun pricing.Micros) {
		mu.Lock()
		defer mu.Unlock()
		if led.Used < 0 || led.Reserved < 0 || led.Used+led.Reserved > led.Granted+overrun {
			breaches = append(breaches, fmt.Sprintf("%+v overrun %d", led, overrun))
		}
	}
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), 11))
			for range perWorker {
				body := msg(sonnet, int64(1+rng.IntN(8000)), `"stream":true`)
				resp, _ := h.post(body)
				if resp.StatusCode != 200 && resp.StatusCode != http.StatusTooManyRequests {
					t.Errorf("status %d", resp.StatusCode)
				}
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(breaches) > 0 {
		t.Fatalf("ledger breached the lease %d times, first: %s", len(breaches), breaches[0])
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if led := h.gw.Ledger(); led.Granted != l.granted {
		t.Errorf("granted %d, but the grants sum to %d", led.Granted, l.granted)
	}
	if l.maxIn > 1 {
		t.Errorf("%d grants ran at once", l.maxIn)
	}
}

func TestTopUpSingleFlight(t *testing.T) {
	w := worst(t, sonnet, leaseBody, 1000, "")
	l := &fakeLease{size: 20 * w, gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	script := make([]anthropicfake.Reply, 10)
	for i := range script {
		script[i] = anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 10, Output: 10})
	}
	h := newHarnessWith(t, withLease(l, Enforce), script...)
	codes := make(chan int, 10)
	for range 10 {
		go func() { resp, _ := h.post(leaseBody); codes <- resp.StatusCode }()
	}
	<-l.entered
	// Let the others queue behind the one grant.
	time.Sleep(150 * time.Millisecond)
	close(l.gate)
	for range 10 {
		if c := <-codes; c != 200 {
			t.Errorf("status %d", c)
		}
	}
	if n := l.grantCalls(); n != 1 {
		t.Errorf("%d Grant calls for 10 parallel calls, want 1", n)
	}
}

func TestRefusalHalts(t *testing.T) {
	l := &fakeLease{err: func(pricing.Micros) error {
		return &Refusal{Reason: "repo_daily_cap", Scope: "repo", Detail: "this repository's daily cap $5.00 reached"}
	}}
	h := newHarnessWith(t, withLease(l, Enforce), okReplies(1)...)
	resp, b := h.post(leaseBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Should-Retry") != "false" {
		t.Errorf("x-should-retry %q", resp.Header.Get("X-Should-Retry"))
	}
	if _, m := apiError(t, b); !strings.Contains(m, "this repository's daily cap $5.00 reached") || !strings.HasPrefix(m, "fugaro: budget halted: ") {
		t.Errorf("message %q", m)
	}
	halt := haltOf(t, h)
	if halt.Reason != "repo_daily_cap" || halt.Scope != "repo" || !strings.Contains(halt.Detail, "daily cap") || halt.At.IsZero() {
		t.Errorf("halt %+v", halt)
	}
	// Sticky, and nothing is asked of the lease or sent upstream again.
	n := l.grantCalls()
	if resp, _ := h.post(leaseBody); resp.StatusCode != 403 {
		t.Errorf("after the halt: %d", resp.StatusCode)
	}
	if l.grantCalls() != n || h.fake.Count() != 0 {
		t.Errorf("grants %d (was %d), upstream calls %d", l.grantCalls(), n, h.fake.Count())
	}
	if led := h.gw.Ledger(); led.Granted != 0 || led.Reserved != 0 || led.Used != 0 {
		t.Errorf("ledger %+v", led)
	}
}

// Calls in flight over-reserve, so a refusal while they hold lease the
// run could spend again is a retry, as with M9a's static cap; once they
// settle, a refusal with nothing left to spend halts.
func TestRefusalWithCallsInFlightIsRetryable(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	slow := anthropicfake.StreamOK(sonnet, u)
	slow.EventDelay = 100 * time.Millisecond
	w := worst(t, sonnet, leaseBody, 1000, "")
	first := true
	l := &fakeLease{err: func(pricing.Micros) error {
		if first { // the first grant is the lease
			first = false
			return nil
		}
		return &Refusal{Reason: "run_cap", Scope: "run", Detail: "run cap reached"}
	}, size: 2*w - 1}
	h := newHarnessWith(t, withLease(l, Enforce), slow, anthropicfake.StreamOK(sonnet, u))
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", leaseBody))
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)

	resp2, b := h.post(leaseBody)
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call: %d %s, want 429", resp2.StatusCode, b)
	}
	if resp2.Header.Get("X-Should-Retry") == "false" || resp2.Header.Get("Retry-After") == "" {
		t.Errorf("the 429 must be retryable: %v", resp2.Header)
	}
	noHalt(t, h)
	_, _ = io.ReadAll(r)
	resp.Body.Close()
	waitFor(t, "the first call to settle", func() bool { return h.gw.Ledger().Reserved == 0 })
	if resp3, b := h.post(leaseBody); resp3.StatusCode != 200 {
		t.Fatalf("the retry: %d %s, want 200", resp3.StatusCode, b)
	}
}

func TestRefusalWithNothingLeftHaltsEvenIfCallsInFlight(t *testing.T) {
	// The call's own worst case doesn't fit what the run has spent plus
	// the lease: used + w > granted. In flight or not, that is a halt.
	u := pricing.Usage{Input: 100, Output: 100}
	slow := anthropicfake.StreamOK(sonnet, u)
	slow.EventDelay = 100 * time.Millisecond
	w := worst(t, sonnet, leaseBody, 1000, "")
	n := 0
	l := &fakeLease{err: func(pricing.Micros) error {
		n++
		if n == 1 {
			return nil
		}
		return &Refusal{Reason: "global_daily_cap", Scope: "global", Detail: "the project's daily cap reached"}
	}, size: w}
	h := newHarnessWith(t, withLease(l, Enforce), slow)
	_ = n
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", leaseBody))
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)
	// A call far larger than anything the lease could ever hold.
	big := msg(sonnet, 100000, `"stream":true`)
	if resp2, b := h.post(big); resp2.StatusCode != 403 {
		t.Fatalf("status %d %s, want 403", resp2.StatusCode, b)
	}
	if halt := haltOf(t, h); halt.Reason != "global_daily_cap" || halt.Scope != "global" {
		t.Errorf("halt %+v", halt)
	}
	rest, _ := io.ReadAll(r)
	resp.Body.Close()
	if !strings.Contains(string(rest), "message_stop") {
		t.Errorf("the in-flight call was cut by a refusal: %q", rest)
	}
}

func TestKillRefusalAlwaysHalts(t *testing.T) {
	for _, mode := range []Mode{Enforce, Observe} {
		t.Run(string(mode), func(t *testing.T) {
			u := pricing.Usage{Input: 100, Output: 100}
			slow := anthropicfake.StreamOK(sonnet, u)
			slow.EventDelay = 100 * time.Millisecond
			w := worst(t, sonnet, leaseBody, 1000, "")
			n := 0
			l := &fakeLease{size: 2*w - 1, err: func(pricing.Micros) error {
				n++
				if n == 1 {
					return nil
				}
				return &Refusal{Reason: "kill_switch", Scope: "global", Detail: "the global kill switch is on (admin: incident)"}
			}}
			h := newHarnessWith(t, withLease(l, mode), slow)
			resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", leaseBody))
			if err != nil {
				t.Fatal(err)
			}
			r := bufio.NewReader(resp.Body)
			readEvent(t, r)
			// Fits nothing more, with a call in flight: a kill halts anyway.
			if resp2, b := h.post(leaseBody); resp2.StatusCode != 403 {
				t.Fatalf("status %d %s, want 403", resp2.StatusCode, b)
			}
			if halt := haltOf(t, h); halt.Reason != "kill_switch" || halt.Scope != "global" {
				t.Errorf("halt %+v", halt)
			}
			_, _ = io.ReadAll(r)
			resp.Body.Close()
		})
	}
}

func TestUnavailableIsNotAHalt(t *testing.T) {
	fail := true
	var mu sync.Mutex
	l := &fakeLease{err: func(pricing.Micros) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("rtdb: unreachable")
		}
		return nil
	}}
	h := newHarnessWith(t, withLease(l, Enforce), anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 1, Output: 1}))
	resp, b := h.post(leaseBody)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d %s, want 503", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Should-Retry") == "false" || resp.Header.Get("Retry-After") == "" {
		t.Errorf("the 503 must be retryable: %v", resp.Header)
	}
	if strings.Contains(b, "rtdb: unreachable") {
		t.Errorf("the lease's error text reached the agent: %s", b)
	}
	noHalt(t, h)
	if h.fake.Count() != 0 || h.gw.Ledger().Reserved != 0 {
		t.Errorf("a call without a grant was served or reserved: %+v", h.gw.Ledger())
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("after recovery: %d %s", resp.StatusCode, b)
	}
}

// A lease that grants nothing, or less than asked, never lets a call
// through unbacked, and never spins.
func TestShortOrEmptyGrantsNeverServeUnbacked(t *testing.T) {
	w := worst(t, sonnet, leaseBody, 1000, "")
	t.Run("zero", func(t *testing.T) {
		h := newHarnessWith(t, withLease(&zeroLease{fakeLease: &fakeLease{}}, Enforce), okReplies(1)...)
		if resp, _ := h.post(leaseBody); resp.StatusCode != 503 {
			t.Errorf("status %d, want 503", resp.StatusCode)
		}
		if h.fake.Count() != 0 || h.gw.Ledger().Granted != 0 {
			t.Errorf("served on nothing: %+v", h.gw.Ledger())
		}
	})
	t.Run("short", func(t *testing.T) {
		s := &shortLease{fakeLease: &fakeLease{}, give: w / 4}
		h := newHarnessWith(t, withLease(s, Enforce), anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 1, Output: 1}))
		resp, b := h.post(leaseBody)
		// Several short grants add up to the call, or it is told to retry;
		// either way the ledger holds.
		if led := h.gw.Ledger(); led.Used+led.Reserved > led.Granted {
			t.Errorf("ledger %+v", led)
		}
		if resp.StatusCode != 200 && resp.StatusCode != 503 {
			t.Errorf("status %d %s", resp.StatusCode, b)
		}
	})
	t.Run("negative", func(t *testing.T) {
		h := newHarnessWith(t, withLease(&negLease{&fakeLease{}}, Enforce), okReplies(1)...)
		if resp, _ := h.post(leaseBody); resp.StatusCode != 503 {
			t.Errorf("status %d, want 503", resp.StatusCode)
		}
		if h.gw.Ledger().Granted != 0 {
			t.Errorf("granted %d", h.gw.Ledger().Granted)
		}
	})
}

type zeroLease struct{ *fakeLease }

func (z *zeroLease) Grant(context.Context, pricing.Micros) (pricing.Micros, error) { return 0, nil }

type negLease struct{ *fakeLease }

func (*negLease) Grant(context.Context, pricing.Micros) (pricing.Micros, error) { return -5, nil }

type shortLease struct {
	*fakeLease
	give pricing.Micros
}

func (s *shortLease) Grant(context.Context, pricing.Micros) (pricing.Micros, error) {
	return s.give, nil
}

func TestHaltExternalCancelsInFlight(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	slow := anthropicfake.StreamOK(sonnet, u)
	slow.EventDelay = 300 * time.Millisecond
	l := &fakeLease{size: 10_000_000}
	h := newHarnessWith(t, withLease(l, Enforce), slow)
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", leaseBody))
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)

	if !h.gw.HaltExternal(Halt{Reason: "kill_switch", Scope: "global", Detail: "the global kill switch is on"}) {
		t.Fatal("HaltExternal reported no effect")
	}
	if h.gw.HaltExternal(Halt{Reason: "budget_unavailable", Scope: "run", Detail: "second"}) {
		t.Error("a second HaltExternal took effect: the first halt must stand")
	}
	start := time.Now()
	rest, _ := io.ReadAll(r)
	resp.Body.Close()
	if strings.Contains(string(rest), "message_stop") || time.Since(start) > 2*time.Second {
		t.Errorf("the in-flight call wasn't cancelled (%s): %q", time.Since(start), rest)
	}
	waitFor(t, "the cancelled call to settle", func() bool { return h.gw.Ledger().Reserved == 0 })
	// Cancelled after the stream started: charged as a client cancel is
	// (input and the reserved output), never nothing.
	if led := h.gw.Ledger(); led.Used == 0 {
		t.Errorf("a cancelled in-flight call was charged nothing: %+v", led)
	}

	resp2, b := h.post(leaseBody)
	if resp2.StatusCode != http.StatusForbidden || resp2.Header.Get("X-Should-Retry") != "false" {
		t.Fatalf("after the halt: %d %v %s", resp2.StatusCode, resp2.Header, b)
	}
	if _, m := apiError(t, b); m != "fugaro: budget halted: the global kill switch is on" {
		t.Errorf("message %q", m)
	}
	if h.fake.Count() != 1 {
		t.Errorf("forwarded %d calls", h.fake.Count())
	}
	// count_tokens and the rest see the same halt.
	if resp, _ := h.do(h.request(context.Background(), "/v1/messages/count_tokens", `{"model":"claude-haiku-4-5","messages":[]}`)); resp.StatusCode != 403 {
		t.Errorf("count_tokens after the halt: %d", resp.StatusCode)
	}
}

// A call waiting on a top-up when the halt lands gets the halt at once,
// and the grant that lands afterwards is still counted and released.
func TestHaltExternalWhileTopUpWaits(t *testing.T) {
	l := &fakeLease{size: 5_000_000, gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	h := newHarnessWith(t, withLease(l, Enforce), okReplies(1)...)
	code := make(chan int, 1)
	go func() { resp, _ := h.post(leaseBody); code <- resp.StatusCode }()
	<-l.entered
	h.gw.HaltExternal(Halt{Reason: "budget_unavailable", Scope: "run", Detail: "the budget backend is unreachable"})
	select {
	case c := <-code:
		if c != 403 {
			t.Errorf("status %d, want 403", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting call didn't see the halt")
	}
	if h.fake.Count() != 0 {
		t.Error("forwarded after the halt")
	}
	close(l.gate)
	waitFor(t, "the grant to land", func() bool { return h.gw.Ledger().Granted == 5_000_000 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.gw.Close(ctx); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.released) != 1 || l.released[0] != 5_000_000 {
		t.Errorf("released %v, want the whole late grant", l.released)
	}
}

func TestReleaseOnCloseOnce(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 800}
	w := worst(t, sonnet, leaseBody, 1000, "")
	l := &fakeLease{size: 3 * w}
	h := newHarnessWith(t, withLease(l, Enforce), anthropicfake.StreamOK(sonnet, u))
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	used := cost(t, sonnet, u)
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.gw.Close(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.released) != 1 || l.released[0] != 3*w-used {
		t.Fatalf("released %v, want exactly one release of %d", l.released, 3*w-used)
	}
	// Used + released never exceeds what was granted.
	if used+l.released[0] != l.granted {
		t.Errorf("used %d + released %d != granted %d", used, l.released[0], l.granted)
	}
}

func TestReleaseSkippedWhenNothingUnused(t *testing.T) {
	l := &fakeLease{}
	h := newHarnessWith(t, withLease(l, Enforce))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.gw.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(l.released) != 0 {
		t.Errorf("released %v with nothing granted", l.released)
	}
}

// A lease overspent by an overrun never releases a negative amount; a
// failed release is reported by Close and not retried.
func TestReleaseFailureIsReported(t *testing.T) {
	l := &fakeLease{size: 1_000_000, relErr: errors.New("rtdb: down")}
	h := newHarnessWith(t, withLease(l, Enforce), okReplies(1)...)
	if resp, b := h.post(msg(sonnet, 10)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := h.gw.Close(ctx)
	if err == nil || !strings.Contains(err.Error(), "releasing") {
		t.Errorf("Close error %v, want the failed release", err)
	}
	if len(l.released) != 1 {
		t.Errorf("release attempted %d times", len(l.released))
	}
}

func TestReportAtEndStage(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 800}
	l := &fakeLease{size: 10_000_000}
	h := newHarnessWith(t, withLease(l, Enforce), anthropicfake.StreamOK(sonnet, u))
	if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	rep := h.gw.EndStage()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.reports) != 1 || l.reports[0].Used != rep.Used || l.reports[0].Calls != 1 {
		t.Errorf("reports %+v, want the stage's %+v", l.reports, rep)
	}
}

func TestNilLeaseIsM9a(t *testing.T) {
	// M9a's whole suite runs with no Lease; here the surface that changed.
	h := newHarnessWith(t, enforceCap(7_000_000), okReplies(1)...)
	if led := h.gw.Ledger(); led.Granted != 7_000_000 {
		t.Errorf("granted %d, want the static cap", led.Granted)
	}
	if resp, _ := h.post(msg(sonnet, 10)); resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.gw.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), Options{Upstream: Upstream{Kind: "anthropic", APIKey: testKey}, Prices: pricing.Embedded(), Mode: Enforce}); err == nil {
		t.Error("enforce with no cap and no lease must still be refused")
	}
}

func TestObserveLeaseNeverRefusesForCaps(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 10}
	l := &fakeLease{size: 1} // observe leases still back every call
	script := make([]anthropicfake.Reply, 30)
	for i := range script {
		script[i] = anthropicfake.StreamOK(sonnet, u)
	}
	h := newHarnessWith(t, withLease(l, Observe), script...)
	for i := range 30 {
		if resp, b := h.post(leaseBody); resp.StatusCode != 200 {
			t.Fatalf("call %d: %d %s", i, resp.StatusCode, b)
		}
	}
	noHalt(t, h)
	if rep := h.gw.EndStage(); rep.WouldHalt != 0 {
		t.Errorf("would-halt %d", rep.WouldHalt)
	}

	// A cap refusal an observe lease should never return: the call isn't
	// served unbacked, the run doesn't halt, and it is counted.
	l2 := &fakeLease{err: func(pricing.Micros) error {
		return &Refusal{Reason: "repo_daily_cap", Scope: "repo", Detail: "cap"}
	}}
	h2 := newHarnessWith(t, withLease(l2, Observe), okReplies(1)...)
	resp, b := h2.post(leaseBody)
	if resp.StatusCode != 503 || resp.Header.Get("X-Should-Retry") == "false" {
		t.Fatalf("status %d %v %s, want a retryable 503", resp.StatusCode, resp.Header, b)
	}
	noHalt(t, h2)
	if h2.fake.Count() != 0 {
		t.Error("served without a grant")
	}
	if rep := h2.gw.EndStage(); rep.WouldHalt != 1 {
		t.Errorf("would-halt %d, want 1", rep.WouldHalt)
	}
}

func TestLedgerGrantedWithLeaseIsGrants(t *testing.T) {
	l := &fakeLease{size: 123_456}
	h := newHarnessWith(t, withLease(l, Observe), okReplies(1)...)
	if led := h.gw.Ledger(); led.Granted != 0 {
		t.Errorf("observe+lease granted %d before a grant (not unbounded)", led.Granted)
	}
}
