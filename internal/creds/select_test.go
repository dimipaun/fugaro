package creds

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// now is the instant every selection test states, so nothing depends on the
// clock a test machine happens to have. It is a Tuesday midday UTC.
var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// fakeStore is a Store in memory: the same four nodes, the same whole-subtree
// reads, none of the network. It is deliberately not a second implementation of
// the state — the state's rules live in Marks/Usable/Free and are exercised
// through it — and the ETag path is proved separately, against a fake DB
// (TestMarkCASRetriesOnPrecondition).
type fakeStore struct {
	mu      sync.Mutex
	marks   map[string]Mark
	leases  map[string]Lease
	current map[string]string
	rr      int64

	updates int // how many times UpdateMark called its function
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		marks:   map[string]Mark{},
		leases:  map[string]Lease{},
		current: map[string]string{},
	}
}

func key(r Run) string { return r.Slug + "\x00" + r.ID }

func (f *fakeStore) setMark(name string, m Mark) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks[name] = m
}

func (f *fakeStore) setLease(r Run, l Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l.Slug, l.Run = r.Slug, r.ID
	f.leases[key(r)] = l
}

func (f *fakeStore) lease(r Run) (Lease, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.leases[key(r)]
	return l, ok
}

func (f *fakeStore) currentOf(r Run) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.current[key(r)]
	return n, ok
}

func (f *fakeStore) Marks(context.Context) (map[string]Mark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]Mark, len(f.marks))
	for k, v := range f.marks {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) UpdateMark(_ context.Context, name string, fn func(prev Mark, found bool) (Mark, error)) (Mark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	prev, found := f.marks[name]
	next, err := fn(prev, found)
	if err != nil {
		return Mark{}, err
	}
	f.marks[name] = next
	return next, nil
}

func (f *fakeStore) Leases(context.Context) ([]Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Lease, 0, len(f.leases))
	for _, l := range f.leases {
		out = append(out, l)
	}
	return out, nil
}

func (f *fakeStore) SetLease(_ context.Context, r Run, l Lease) error {
	f.setLease(r, l)
	return nil
}

func (f *fakeStore) Current(_ context.Context, r Run) (string, bool, error) {
	n, ok := f.currentOf(r)
	return n, ok, nil
}

func (f *fakeStore) SetCurrent(_ context.Context, r Run, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current[key(r)] = name
	return nil
}

func (f *fakeStore) RoundRobin(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used := f.rr
	f.rr++
	return used, nil
}

// selector is a Selector over a fake store with the test's clock.
func selector(st Store, set Settings) *Selector {
	return &Selector{Store: st, Settings: set, Now: func() time.Time { return now }}
}

// noWindow is the settings of a project that turned D142's first control off:
// an exhausted credential is held only for the reset it reported.
func noWindow() Settings {
	return Settings{StopAfterExhaustionPerWindow: ptr(false)}
}

func ptr[T any](v T) *T { return &v }

var (
	ctx = context.Background()
	run = Run{Slug: "gh-acme-app", ID: "r1"}
	a1  = Credential{Name: "shared-1", Tier: TierA}
	a2  = Credential{Name: "shared-2", Tier: TierA}
	a3  = Credential{Name: "shared-3", Tier: TierA}
	b1  = Credential{Name: "project-key", Tier: TierB}
)

// The run's own credential wins, even when every rule below it would choose
// another one: stickiness is what keeps the prompt cache (D154).
func TestSelectSticky(t *testing.T) {
	st := newFakeStore()
	s := selector(st, noWindow())

	// The run is placed on shared-1 by its first call.
	c, _, err := s.Select(ctx, run, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("the first selection = %q, %v; want %q", c.Name, err, a1.Name)
	}

	// A usage limit an hour back, whose reset has passed, leaves shared-1 the
	// less attractive of the two: it has been exhausted, shared-2 never has.
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})

	c, _, err = s.Select(ctx, run, []Credential{a1, a2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != a1.Name {
		t.Fatalf("a run that is already on %q was moved to %q: stickiness is what keeps its prompt cache (D154)", a1.Name, c.Name)
	}

	// And a run that has no current credential does take shared-2, so the
	// assertion above is stickiness and not "shared-1 is always chosen".
	c, _, err = s.Select(ctx, Run{Slug: run.Slug, ID: "r2"}, []Credential{a1, a2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != a2.Name {
		t.Fatalf("a new run took %q; want the never-exhausted %q", c.Name, a2.Name)
	}
}

// A new run takes the credential exhausted longest ago (D154): its window
// rolled over first, so it is the one with the most room.
func TestSelectLeastRecentlyExhausted(t *testing.T) {
	st := newFakeStore()
	set := Settings{Window: time.Hour}
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now.Add(-3 * time.Hour), Until: now.Add(-2 * time.Hour)})
	st.setMark(a2.Name, Mark{State: StateExhausted, Since: now.Add(-2 * time.Hour), Until: now.Add(-time.Hour)})

	c, _, err := selector(st, set).Select(ctx, run, []Credential{a1, a2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != a1.Name {
		t.Fatalf("the new run took %q; want %q, the one exhausted longest ago", c.Name, a1.Name)
	}
}

// Credentials exhausted equally recently — of which the commonest case is a set
// that has never been exhausted at all — are spread over by the counter in
// creds/, so two new runs do not land on one token.
func TestSelectRoundRobinTies(t *testing.T) {
	st := newFakeStore()
	// No cap, so the tied set is the same three credentials for every run: with
	// the policy's default of one run per token, the second run's set would
	// already have lost the one the first took, and this test is about the
	// counter, not the cap.
	set := noWindow()
	set.ConcurrencyPerToken = ptr(0)
	s := selector(st, set)

	var got []string
	for i, id := range []string{"r1", "r2", "r3", "r4"} {
		c, _, err := s.Select(ctx, Run{Slug: run.Slug, ID: id}, []Credential{a1, a2, a3})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c.Name)
		if want := []string{a1.Name, a2.Name, a3.Name, a1.Name}[i]; c.Name != want {
			t.Fatalf("selection %d of the tied set = %q; want %q (the round robin wraps)", i+1, c.Name, want)
		}
	}
	if got[0] == got[1] || got[1] == got[2] {
		t.Fatalf("the round robin gave %v: two runs took the same credential", got)
	}
}

// D142's first control: once a shared token has hit its limit, it is out for
// the rest of its window, whatever reset the response reported. Turned off, the
// reset is all that holds it.
func TestStopAfterOneExhaustionPerWindow(t *testing.T) {
	st := newFakeStore()
	// Exhausted two minutes ago, reporting a reset a minute ago: the reset has
	// passed, so nothing but the control holds the credential.
	since := now.Add(-2 * time.Minute)
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: since, Until: now.Add(-time.Minute)})

	// Off: the reset is the whole hold, and it is behind us — the credential is
	// in rotation again a minute after it hit its limit.
	c, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("with the control off the selection = %q, %v; want %q", c.Name, err, a1.Name)
	}

	// On (the policy's absent-key default): one exhaustion costs the token the
	// rest of the window it opened, whatever reset the response reported. The
	// run is already on this credential, and the rule reaches it too: stickiness
	// is not an exemption from an exhaustion.
	set := Settings{Window: 5 * time.Hour}
	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("with the control on the selection = %v; want an ExhaustedUntil answer", err)
	}
	if want := 5*time.Hour - 2*time.Minute; wait != want {
		t.Fatalf("the wait = %s; want %s (the rest of the window, not the reset)", wait, want)
	}
	if want := since.Add(5 * time.Hour); !eu.At.Equal(want) {
		t.Fatalf("the reset = %s; want %s", eu.At, want)
	}
}

// D142's second control: at most N runs hold one credential at a time. The hold
// is a lease, so a run that dies releases its slot without anything sweeping
// it, and the wait for a full credential is the wait for the lease to lapse.
func TestConcurrencyCapLeases(t *testing.T) {
	st := newFakeStore()
	other := Run{Slug: run.Slug, ID: "other"}
	until := now.Add(20 * time.Minute)
	st.setLease(other, Lease{Credential: a1.Name, Until: until})

	// One run at a time is the policy's own default, so the cap needs no member
	// set: only a project that wrote one changes it.
	set := noWindow()

	// The only candidate is held by another run: the answer is the wait for its
	// lease, not a refusal.
	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) || wait != 20*time.Minute || !eu.At.Equal(until) {
		t.Fatalf("with the cap full the selection = %s, %v; want a wait of 20m until %s", wait, err, until)
	}

	// A second credential that is free is used instead of waiting.
	c, _, err := selector(st, set).Select(ctx, run, []Credential{a1, a2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != a2.Name {
		t.Fatalf("the selection = %q; want %q, the one no other run holds", c.Name, a2.Name)
	}

	// A written 0 is no cap at all (D142's documented opt-out), the one value
	// of the two controls that widens rather than bounds.
	set.ConcurrencyPerToken = ptr(0)
	c, _, err = selector(st, set).Select(ctx, Run{Slug: run.Slug, ID: "r3"}, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("with concurrency_per_token 0 the selection = %q, %v; want %q", c.Name, err, a1.Name)
	}
}

// A lease whose time has passed is an instance that died mid-call (or was
// replaced by a deploy): it holds no slot, and the run that takes the
// credential writes a lease of its own.
func TestLeaseExpiresAfterInstanceDeath(t *testing.T) {
	st := newFakeStore()
	dead := Run{Slug: run.Slug, ID: "dead"}
	st.setLease(dead, Lease{Credential: a1.Name, Until: now.Add(-time.Minute)})

	c, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("a lapsed lease blocked the selection: %q, %v", c.Name, err)
	}
	l, ok := st.lease(run)
	if !ok {
		t.Fatal("the run holds no lease after being placed on a credential")
	}
	if want := now.Add(DefaultLeaseTTL); !l.Until.Equal(want) {
		t.Fatalf("the lease runs to %s; want %s (the TTL from now)", l.Until, want)
	}
	if l.Credential != a1.Name {
		t.Fatalf("the lease names %q; want %q", l.Credential, a1.Name)
	}
}

// A broken credential is never selected, and it is not a wait either: nothing
// clears a broken mark but the operator (C1.21). It also never becomes a probe
// target (C1.12), so a caller can tell the two apart.
func TestBrokenNeverSelected(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Break(now))

	// A healthy sibling serves the call.
	c, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1, a2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != a2.Name {
		t.Fatalf("the selection = %q; want the healthy %q", c.Name, a2.Name)
	}

	// Alone, it is a refusal rather than a wait: there is no time at which a
	// broken credential becomes usable.
	_, _, err = selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("the selection of a broken credential = %v; want ErrNoCredential", err)
	}

	// And a 401 is not a backed-off state a probe could clear.
	if Break(now).NeedsProbe(now) {
		t.Fatal("a broken mark reports that it needs a probe: only the operator clears one (C1.21)")
	}
}

// Tier C is reserved: this release does not select a third-party route for a
// Claude model (D150, C3.6). A caller offering one is refused rather than
// quietly served from another tier.
func TestSelectRefusesReservedTierC(t *testing.T) {
	st := newFakeStore()
	_, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{b1, {Name: "openrouter", Tier: TierC}})
	if err == nil || errors.Is(err, ErrNoCredential) {
		t.Fatalf("a tier C candidate was not refused: %v", err)
	}
	// Refused by name, and for the reason it was not built: a caller that
	// offered a tier C route is told that, and not merely that its tier is
	// unrecognised — a name that reaches the operator must say what to change.
	for _, want := range []string{"tier C", "openrouter", "D150"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not say %q", err, want)
		}
	}
}

// The candidate list is the caller's: a selection with nothing to select from
// is a refusal, never an "everything is exhausted" wait that would hide the
// caller's mistake behind a pause.
func TestSelectRefusesAnEmptyCandidateList(t *testing.T) {
	_, _, err := selector(newFakeStore(), noWindow()).Select(ctx, run, nil)
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("an empty candidate list = %v; want ErrNoCandidates", err)
	}
}

// D152's wait-or-switch rule at the tier boundary: under auto a short wait is
// waited out and a long one fails over to the paid tier; under oauth-only the
// run always waits, because there is nothing to fail over to.
func TestWaitOrSwitchAtTheTierBoundary(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(5 * time.Minute)})

	set := noWindow()
	set.WaitThreshold = 15 * time.Minute
	set.Fallback = Auto

	// Inside the threshold: wait for the shared token, do not spend on tier B.
	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1, b1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) || wait != 5*time.Minute {
		t.Fatalf("a 5m wait under a 15m threshold = %s, %v; want the wait", wait, err)
	}

	// Beyond it: the call moves to the API key rather than idling.
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(20 * time.Minute)})
	c, _, err := selector(st, set).Select(ctx, run, []Credential{a1, b1})
	if err != nil || c.Name != b1.Name {
		t.Fatalf("a 20m wait under a 15m threshold = %q, %v; want %q", c.Name, err, b1.Name)
	}

	// oauth-only ignores the threshold: the run waits for the reset whatever it
	// is, because spending the project's money is not something it may do
	// (D152). A run of its own, so what is asserted is the rule and not the
	// stickiness the call above just recorded on the API key.
	set.Fallback = OAuthOnly
	_, wait, err = selector(st, set).Select(ctx, Run{Slug: run.Slug, ID: "r3"}, []Credential{a1, b1})
	if !errors.As(err, &eu) || wait != 20*time.Minute {
		t.Fatalf("under oauth-only a 20m wait = %s, %v; want the wait", wait, err)
	}
}

// A run's own hold on a credential must never be the reason it cannot stay on
// it: the sticky path of the run that holds the last slot under the cap.
func TestOwnLeaseNeverBlocksTheRunThatHoldsIt(t *testing.T) {
	st := newFakeStore()
	set := Settings{StopAfterExhaustionPerWindow: ptr(false), ConcurrencyPerToken: ptr(1)}

	s := selector(st, set)
	c, _, err := s.Select(ctx, run, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("the first selection = %q, %v", c.Name, err)
	}
	c, _, err = s.Select(ctx, run, []Credential{a1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("the second selection = %q, %v; want the run to keep the credential it holds the last slot of", c.Name, err)
	}
}
