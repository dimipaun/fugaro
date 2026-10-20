package creds

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// setPlacement seeds both halves of a placement, as an earlier selection left
// them.
func (f *fakeStore) setPlacement(r Run, name string, l Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l.Slug, l.Run = r.Slug, r.ID
	f.leases[key(r)] = l
	f.current[key(r)] = name
}

func (f *fakeStore) mark(name string) Mark {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marks[name]
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

func (f *fakeStore) Leases(context.Context, time.Time) ([]Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Lease, 0, len(f.leases))
	for _, l := range f.leases {
		out = append(out, l)
	}
	return out, nil
}

// SetPlacement writes the current credential and the lease under one lock, so a
// reader through this store can never see the torn pair a real two-write
// implementation could leave: the atomicity the real one gets from one Patch is
// what this mirrors.
func (f *fakeStore) SetPlacement(_ context.Context, r Run, name string, l Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	l.Slug, l.Run = r.Slug, r.ID
	f.leases[key(r)] = l
	f.current[key(r)] = name
	return nil
}

func (f *fakeStore) Current(_ context.Context, r Run) (string, bool, error) {
	n, ok := f.currentOf(r)
	return n, ok, nil
}

// setRoundRobin seeds the tie-breaking counter, so a test can state the value a
// hand-edit or a wrapped counter left behind.
func (f *fakeStore) setRoundRobin(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rr = n
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

// The wait-or-switch boundary at its exact value: a wait EQUAL to the threshold
// waits, and the paid tier is not spent. spec.md's rule is a credential that
// "resets within a threshold" and C1.11's is a wait "under the threshold"; the
// code takes the spec's inclusive reading, and this is the case that says so —
// without it, either comparison could be flipped to a strict one and the whole
// suite would still pass.
func TestWaitOrSwitchAtTheThresholdExactly(t *testing.T) {
	st := newFakeStore()
	threshold := 15 * time.Minute
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(threshold)})

	set := noWindow()
	set.WaitThreshold = threshold
	set.Fallback = Auto

	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1, b1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) || wait != threshold {
		t.Fatalf("a wait of exactly the %s threshold = %s, %v; want the wait", threshold, wait, err)
	}
	if !eu.At.Equal(now.Add(threshold)) {
		t.Fatalf("the answer's time = %s; want the reset %s", eu.At, now.Add(threshold))
	}
	// The run idles rather than spending: nothing was selected, so nothing was
	// placed on the paid key.
	if len(st.leases) != 0 || len(st.current) != 0 {
		t.Fatalf("the paid key was taken for a wait of exactly the threshold: %d leases, %d placements", len(st.leases), len(st.current))
	}

	// One second past it, the same fixture fails over — the boundary is the
	// threshold and not a little more.
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(threshold + time.Second)})
	c, _, err := selector(st, set).Select(ctx, run, []Credential{a1, b1})
	if err != nil || c.Name != b1.Name {
		t.Fatalf("a wait one second past the threshold = %q, %v; want the paid key", c.Name, err)
	}
}

// A probe is due at the exact instant a backed-off mark's wait ends, not one
// tick later: NeedsProbe's comparison is inclusive, and its other form would
// delay every probe by an instant. Review finding: this equality had no test.
func TestAProbeIsDueExactlyAtTheMarksWait(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Mark{State: StateBackedOff, Until: now, Backoff: 15 * time.Minute, Since: now.Add(-15 * time.Minute)})

	_, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("a backed-off mark whose wait ends exactly now = %v; want an ExhaustedUntil answer", err)
	}
	if !eu.Probe {
		t.Fatal("a mark whose wait ends exactly now was not handed off for a probe: the comparison is inclusive")
	}
	if eu.Wait != 0 || !eu.At.Equal(now) {
		t.Fatalf("the answer = %s until %s; want no wait, at now", eu.Wait, eu.At)
	}
}

// D152 is a rule about spending, and it has to hold on every path out of
// Select — not only where the wait-or-switch threshold is applied. A dead
// subscription token is exactly the case it exists for: the platform refuses
// and says why, rather than starting to spend the project's money on the API
// key it was also offered.
func TestOAuthOnlyNeverReachesThePaidTier(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Break(now))

	// The zero Settings is oauth-only (state.go), and the paid key is on offer:
	// the call is refused, and refused with the error that says a policy wrote
	// this and only a policy changes it.
	_, _, err := selector(st, Settings{}).Select(ctx, run, []Credential{a1, b1})
	if !errors.Is(err, ErrFallbackForbidden) {
		t.Fatalf("with every shared token broken under oauth-only the selection = %v; want ErrFallbackForbidden", err)
	}

	// The same state under `auto` takes the paid key — so what the assertion
	// above pins is the policy and not the broken mark.
	c, _, err := selector(st, Settings{Fallback: Auto}).Select(ctx, run, []Credential{a1, b1})
	if err != nil || c.Name != b1.Name {
		t.Fatalf("the same call under auto = %q, %v; want the paid %q", c.Name, err, b1.Name)
	}

	// Offered nothing but the paid key, oauth-only refuses rather than serving.
	_, _, err = selector(st, Settings{}).Select(ctx, Run{Slug: run.Slug, ID: "r2"}, []Credential{b1})
	if !errors.Is(err, ErrFallbackForbidden) {
		t.Fatalf("a call only the paid key could serve = %v; want ErrFallbackForbidden", err)
	}

	// A run already placed on the paid key — by a policy that was `auto` — does
	// not keep spending after the policy is turned back: its call moves to a
	// shared credential (D152's rule is not weaker for being mid-run).
	sticky := newFakeStore()
	if err := sticky.SetPlacement(ctx, run, b1.Name, Lease{Credential: b1.Name, Until: now.Add(DefaultLeaseTTL)}); err != nil {
		t.Fatal(err)
	}
	c, _, err = selector(sticky, Settings{}).Select(ctx, run, []Credential{a1, b1})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("a run sticky on the paid key = %q, %v; want it moved to %q", c.Name, err, a1.Name)
	}
	if cur, _, err := sticky.Current(ctx, run); err != nil || cur != a1.Name {
		t.Fatalf("the run's current credential = %q, %v; want %q", cur, err, a1.Name)
	}
}

// ErrFallbackForbidden says the credentials that could serve the call are all of
// a tier this policy does not admit, and it has to be true when it is said: a
// paid key that is broken, or one whose mark carries no time to wait for, cannot
// serve the call and no `fallback: auto` would make it. Naming the policy there
// sends the operator to a change that changes nothing; the honest answer is
// ErrNoCredential, the one that names what stands in the way.
func TestAnUnusablePaidKeyIsNotAPolicyRefusal(t *testing.T) {
	for _, tc := range []struct {
		what string
		mark Mark
	}{
		{"broken", Break(now)},
		{"held with no time to wait for", Mark{State: StateExhausted}},
	} {
		st := newFakeStore()
		st.setMark(a1.Name, Break(now))
		st.setMark(b1.Name, tc.mark)

		_, _, err := selector(st, Settings{}).Select(ctx, run, []Credential{a1, b1})
		if !errors.Is(err, ErrNoCredential) {
			t.Fatalf("with a %s paid key under oauth-only the selection = %v; want ErrNoCredential", tc.what, err)
		}
	}

	// The case the policy error is for, and the reason the two are worth telling
	// apart: the paid key could serve the call, so the refusal names the policy.
	// (Pinned in full by TestOAuthOnlyNeverReachesThePaidTier.)
	st := newFakeStore()
	st.setMark(a1.Name, Break(now))
	_, _, err := selector(st, Settings{}).Select(ctx, run, []Credential{a1, b1})
	if !errors.Is(err, ErrFallbackForbidden) {
		t.Fatalf("with a usable paid key under oauth-only the selection = %v; want ErrFallbackForbidden", err)
	}
}

// ExhaustedUntil.Error() is the message C1.10 puts on the wire and C1.13 writes
// to the audit line, and it renders the two facts by value: the wait, and the
// time it ends at. Both forms are asserted, because a reworded or broken message
// would otherwise pass the whole suite — and the Probe form is the one an
// earlier review round had to correct for claiming the opposite of what D142's
// control does (a probe is due, and the credential can still have a real wait).
func TestTheExhaustedUntilMessageRendersTheAnswer(t *testing.T) {
	at := now.Add(30 * time.Minute)

	waited := (&ExhaustedUntil{At: at, Wait: 30 * time.Minute}).Error()
	for _, want := range []string{"30m0s", at.UTC().Format(time.RFC3339)} {
		if !strings.Contains(waited, want) {
			t.Fatalf("the wait message %q does not carry %q", waited, want)
		}
	}
	if strings.Contains(waited, "probe") {
		t.Fatalf("the message for a real wait mentions a probe: %q", waited)
	}

	probed := (&ExhaustedUntil{At: at, Wait: 30 * time.Minute, Probe: true}).Error()
	for _, want := range []string{"probe", "30m0s", at.UTC().Format(time.RFC3339)} {
		if !strings.Contains(probed, want) {
			t.Fatalf("the probe message %q does not carry %q", probed, want)
		}
	}
	if !strings.Contains(probed, "serve work in") {
		t.Fatalf("the probe message does not say when the credential can serve work: %q", probed)
	}
	// A zero wait renders as one too, rather than as an absence: "0s" is the
	// answer that says the probe is all that is owed.
	if zero := (&ExhaustedUntil{At: now, Probe: true}).Error(); !strings.Contains(zero, "0s") {
		t.Fatalf("a probe message with no wait = %q; want it to render 0s", zero)
	}
}

// ErrNoCredential's message is what an operator reads while diagnosing, and the
// code answers it for three shapes, not one: a broken mark, a mark held with no
// time to wait for, and a cap that admits nobody. The message names all three,
// and this pins that it does — the sibling ErrFallbackForbidden is careful about
// the same kind of distinction.
func TestNoCredentialSaysWhatTheCodeDoes(t *testing.T) {
	for _, want := range []string{"broken", "no time", "cap"} {
		if !strings.Contains(ErrNoCredential.Error(), want) {
			t.Fatalf("ErrNoCredential %q does not name %q, which is one of the shapes it is returned for", ErrNoCredential, want)
		}
	}
}

// The probe handoff (C1.12) is a field of the answer and nothing else carries
// it: a caller that reads "usable in 0s" as "go" sends real work to a token
// that answered 429. So a backed-off credential whose wait has passed answers
// Probe=true with no sleep, and a run holding its slot does not turn that into
// a sleep either — both facts are true and both are reported.
func TestBackedOffMarkAsksForAProbeNotASleep(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Exhaust(Mark{}, now.Add(-16*time.Minute), time.Time{},
		Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}))

	// With D142's first control off — the one setting under which the wait is
	// the whole of the hold — the answer is the probe and no sleep at all.
	_, wait, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("the selection = %v; want an ExhaustedUntil answer", err)
	}
	if !eu.Probe {
		t.Fatal("a backed-off credential whose wait has passed was not handed off for a probe (C1.12)")
	}
	if wait != 0 {
		t.Fatalf("the wait = %s; want 0: the wait has passed, and a caller given a negative or positive sleep here probes nothing", wait)
	}

	// The same credential under the default settings, which is the control ON:
	// the probe is still what is due (the mark's own wait has passed), and the
	// credential cannot serve work before the subscription window its exhaustion
	// opened ends — so the answer is both facts, not the shorter one. This is
	// review finding M3 at the selection level: answering 0 here would send the
	// probe's success straight back into rotation inside the window.
	_, wait, err = selector(st, Settings{}).Select(ctx, run, []Credential{a1})
	const window = 5 * time.Hour // DefaultBackoffCap, and so the default window
	if !errors.As(err, &eu) || !eu.Probe {
		t.Fatalf("the selection under the window control = %v, probe=%v; want the probe", err, eu.Probe)
	}
	if want := window - 16*time.Minute; wait != want || !eu.At.Equal(now.Add(want)) {
		t.Fatalf("the wait under the window control = %s until %s; want %s: the window the exhaustion opened, not the 15m wait", wait, eu.At, want)
	}

	// The same credential, now held by another run, control off: the probe is
	// still the instruction, and the wait is when it could at best be used.
	st.setLease(Run{Slug: run.Slug, ID: "other"}, Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
	_, wait, err = selector(st, noWindow()).Select(ctx, run, []Credential{a1})
	if !errors.As(err, &eu) || !eu.Probe || wait != 30*time.Minute {
		t.Fatalf("a backed-off credential held by another run = %s, probe=%v; want the probe and the lease's 30m", wait, eu.Probe)
	}
}

// The answer carries the EARLIEST time a candidate becomes usable — the number
// C1.10 puts on the wire and D152's threshold compares — over both reasons a
// candidate is not usable: its mark, and the leases holding it.
func TestWaitAnswersTheEarliestOfTheCandidates(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(40 * time.Minute)})
	st.setMark(a2.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(10 * time.Minute)})

	// The window control off, so what each credential waits for is the reset it
	// reported and nothing else: this test is about which of the two times is
	// answered, not about the window.
	set := noWindow()
	set.WaitThreshold = time.Hour
	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1, a2})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("the selection = %v; want an ExhaustedUntil answer", err)
	}
	if want := now.Add(10 * time.Minute); wait != 10*time.Minute || !eu.At.Equal(want) {
		t.Fatalf("the wait = %s until %s; want 10m until %s — the earliest reset, not the latest", wait, eu.At, want)
	}

	// Two runs hold one credential under the default cap of ONE, so the lapse
	// that frees a slot is the later of the two: Free asks for strictly fewer
	// than one holder, and after the first run's lease ends there is still a
	// holder. This is review finding m10 — the earlier shape answered the first
	// expiry, and a caller that waited for it would wake up to a credential that
	// was still full.
	held := newFakeStore()
	held.setLease(Run{Slug: run.Slug, ID: "other-1"}, Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
	held.setLease(Run{Slug: run.Slug, ID: "other-2"}, Lease{Credential: a1.Name, Until: now.Add(5 * time.Minute)})
	_, wait, err = selector(held, Settings{}).Select(ctx, run, []Credential{a1})
	if !errors.As(err, &eu) || wait != 30*time.Minute || !eu.At.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("the wait for a credential two runs hold under a cap of one = %s until %s; want the LATER expiry, 30m", wait, eu.At)
	}

	// Under a cap of two, the first expiry is the one that makes room: two runs
	// are all the cap allows, so one of them ending is enough for a third.
	two := Settings{StopAfterExhaustionPerWindow: ptr(false), ConcurrencyPerToken: ptr(2)}
	_, wait, err = selector(held, two).Select(ctx, run, []Credential{a1})
	if !errors.As(err, &eu) || wait != 5*time.Minute || !eu.At.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("the wait for a credential two runs hold under a cap of two = %s until %s; want the earlier expiry, 5m", wait, eu.At)
	}

	// The comparison itself, so a flipped one is caught here and not only
	// through a selection.
	leases := []Lease{
		{Slug: run.Slug, Run: "other-1", Credential: a1.Name, Until: now.Add(30 * time.Minute)},
		{Slug: run.Slug, Run: "other-2", Credential: a1.Name, Until: now.Add(5 * time.Minute)},
	}
	if at, ok := SlotFrees(leases, a1.Name, now, run, 1); !ok || !at.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("SlotFrees under a cap of one = %s, %v; want the later of the two expiries", at, ok)
	}
	if at, ok := SlotFrees(leases, a1.Name, now, run, 2); !ok || !at.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("SlotFrees under a cap of two = %s, %v; want the earlier expiry", at, ok)
	}
	// A lapsed lease is nobody's hold, and one run's own expired hold is not a
	// wait either.
	lapsed := append(leases, Lease{Slug: run.Slug, Run: "gone", Credential: a1.Name, Until: now.Add(-time.Minute)})
	if at, ok := SlotFrees(lapsed, a1.Name, now, run, 2); !ok || !at.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("SlotFrees with a lapsed lease = %s, %v; want 5m", at, ok)
	}
	// The asking run's own hold is not in its way: asked as other-1 under a cap
	// of one, the only holder that counts is other-2, so the answer is its 5m
	// and not the 30m its own lease would have added.
	if at, ok := SlotFrees(leases, a1.Name, now, Run{Slug: run.Slug, ID: "other-1"}, 1); !ok || !at.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("SlotFrees for the holder itself = %s, %v; want the other run's 5m", at, ok)
	}
	// No cap, nothing to wait for: an answer of "no time" rather than a time.
	if _, ok := SlotFrees(leases, a1.Name, now, run, 0); ok {
		t.Fatal("SlotFrees answered a time under no cap at all")
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

// The Selector's write surface is the API C1.11 and C1.12 drive: a usage limit,
// a failed probe, a 401 and a clear, each recorded through the store's
// compare-and-set, each computed from the mark the store holds NOW rather than
// from one a caller read earlier.
func TestSelectorWritesTheMarksItOwns(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	st := newFakeStore()
	s := selector(st, set)

	// A usage limit carrying the reset the response reported.
	if _, err := s.Exhausted(ctx, a1.Name, TierA, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); m.State != StateExhausted || !m.Until.Equal(now.Add(time.Hour)) || !m.Since.Equal(now) {
		t.Fatalf("the mark a usage limit left = %+v", m)
	}

	// A failed probe while that reset still holds the credential changes
	// nothing: an exhausted mark is not probed at all (C1.12 probes a
	// backed-off one), so this is a probe that was in flight when the reset
	// landed, and its failure must not replace an hour's reset with fifteen
	// minutes — the exhausted side of the in-flight race.
	if _, err := s.ProbeFailed(ctx, a1.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); m.State != StateExhausted || !m.Until.Equal(now.Add(time.Hour)) {
		t.Fatalf("a failed probe inside a reset left %+v; want the reset it did not shorten", m)
	}

	// A failed probe on a backed-off mark is C1.12's doubling, and it holds the
	// exhaustion's date rather than restarting it.
	if _, err := s.Cleared(ctx, a1.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exhausted(ctx, a1.Name, TierA, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); m.State != StateBackedOff || m.Backoff != 15*time.Minute {
		t.Fatalf("the no-reset exhaustion left %+v; want a 15m backoff", m)
	}
	if _, err := s.ProbeFailed(ctx, a1.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); m.Backoff != 30*time.Minute {
		t.Fatalf("the second failed probe left a wait of %s; want 30m (doubling from the mark, not restarting)", m.Backoff)
	}

	// A successful probe of a tier B credential forgets the wait in force: the
	// project's own paid key is held by its reset and by nothing else (forTier,
	// D142's controls are the shared tier's).
	if _, err := s.Probed(ctx, a1.Name, TierB); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Available() || m.Backoff != 0 {
		t.Fatalf("the mark a successful tier B probe left = %+v; want available, remembering nothing", m)
	}

	// The same probe of a tier A credential, under the settings above (D142's
	// first control on, and the window equal to the hour's cap): the wait is
	// over, and the token is still held for the rest of the window its
	// exhaustion opened — review finding M3, and the difference C1.12's probe
	// would otherwise be the way around.
	if _, err := s.Exhausted(ctx, a1.Name, TierA, time.Time{}); err != nil {
		t.Fatal(err)
	}
	backedOff := st.mark(a1.Name)
	if backedOff.State != StateBackedOff || !backedOff.Since.Equal(now) {
		t.Fatalf("the no-reset exhaustion left %+v; want a backoff dated now", backedOff)
	}
	got, err := s.Probed(ctx, a1.Name, TierA)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateExhausted || !got.Until.Equal(now.Add(time.Hour)) || !got.Since.Equal(now) {
		t.Fatalf("a successful tier A probe inside the window left %+v; want it held until the window ends", got)
	}
	// Cleared is the operator's (C1.21), and it is not what a probe writes.
	if _, err := s.Cleared(ctx, a1.Name); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Available() || m.Backoff != 0 {
		t.Fatalf("the operator's clear left %+v; want available, remembering nothing", m)
	}

	// A 401, and then the two transitions a busy token can report while the 401
	// is on the credential: neither takes it off (C1.21).
	if _, err := s.Broken(ctx, a1.Name); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Broken() || !m.Since.Equal(now) {
		t.Fatalf("the mark a 401 left = %+v", m)
	}
	if _, err := s.Exhausted(ctx, a1.Name, TierA, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProbeFailed(ctx, a1.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Broken() {
		t.Fatalf("a usage limit and a failed probe after a 401 left %+v; want the broken mark untouched", m)
	}
	if _, _, err := s.Select(ctx, run, []Credential{a1}); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("the selection of the credential = %v; want the refusal that needs an operator", err)
	}
	if _, err := s.Cleared(ctx, a1.Name); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Available() {
		t.Fatalf("the operator's clear left %+v; want available", m)
	}

	// Mark and Record are handed what the store holds at the write, and Record
	// is handed the settings in force beside it: a closure that computed from a
	// value the caller read earlier is how one call's mark erases another's.
	st.setMark(a2.Name, Mark{State: StateExhausted, Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})
	if _, err := s.Mark(ctx, a2.Name, func(prev Mark, found bool) (Mark, error) {
		if !found {
			return Mark{}, errors.New("the closure was handed a mark that was not found")
		}
		if !prev.Since.Equal(now.Add(-time.Hour)) {
			return Mark{}, fmt.Errorf("the closure was handed %s; want the mark the store holds", prev.Since)
		}
		return Cleared(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a2.Name); !m.Available() {
		t.Fatalf("Mark left %+v", m)
	}
	if _, err := s.Record(ctx, a2.Name, func(prev Mark, _ bool, in Settings) (Mark, error) {
		// Window is the policy's backoff cap unless a caller sets it (review
		// finding m7), so with a one-hour cap in force the window is an hour and
		// not the package's five-hour default.
		if in.BackoffCap != time.Hour || in.Window != time.Hour || !in.stops() {
			return Mark{}, fmt.Errorf("the closure was handed %+v; want the settings the selector enforces", in)
		}
		return Exhaust(prev, now, time.Time{}, in), nil
	}); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a2.Name); m.State != StateBackedOff || m.Backoff != 15*time.Minute {
		t.Fatalf("Record left %+v", m)
	}

	// The guards: a nameless credential and an absent change are refused before
	// the store is touched.
	writes := st.updates
	if _, err := s.Mark(ctx, "", func(Mark, bool) (Mark, error) { return Cleared(), nil }); err == nil {
		t.Fatal("a mark was written for a credential with no name")
	}
	if _, err := s.Mark(ctx, a1.Name, nil); err == nil {
		t.Fatal("a mark was written with no change to make")
	}
	if _, err := s.Exhausted(ctx, "", TierA, now); err == nil {
		t.Fatal("a usage limit was recorded for a credential with no name")
	}
	if st.updates != writes {
		t.Fatalf("the store was written to %d times by refused calls", st.updates-writes)
	}
}

// D142's two controls are the SHARED tier's, and the state of a credential is
// not what decides that (Settings.forTier; review findings M1 and M2). Both are
// owner headroom over donated subscription quota: the window is the subscription
// window an exhaustion costs, and the cap counts runs sharing one token. Neither
// has a subject on tier B, the project's own paid API key — a rate limit that
// named a one-minute reset must not take the paid key out for a five-hour
// subscription window, and a cap of one run at a time on a key the project pays
// for is an outage it wrote itself.
func TestTheSharedControlsDoNotReachThePaidTier(t *testing.T) {
	// A usage limit a minute ago whose reset has passed, which under the window
	// control holds a shared token for the rest of a five-hour window; and two
	// other runs holding the credential, which under a cap of one excludes a
	// third.
	since := now.Add(-time.Minute)
	state := func() *fakeStore {
		st := newFakeStore()
		st.setMark(a1.Name, Mark{State: StateExhausted, Since: since, Until: now.Add(-time.Second)})
		st.setMark(b1.Name, Mark{State: StateExhausted, Since: since, Until: now.Add(-time.Second)})
		st.setLease(Run{Slug: run.Slug, ID: "other-1"}, Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
		st.setLease(Run{Slug: run.Slug, ID: "other-2"}, Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
		st.setLease(Run{Slug: run.Slug, ID: "other-1"}, Lease{Credential: b1.Name, Until: now.Add(30 * time.Minute)})
		st.setLease(Run{Slug: run.Slug, ID: "other-2"}, Lease{Credential: b1.Name, Until: now.Add(30 * time.Minute)})
		return st
	}

	// The paid key, under the default settings: the window control is on and the
	// cap is one, and the selection goes there anyway.
	set := Settings{Fallback: Auto}
	c, wait, err := selector(state(), set).Select(ctx, run, []Credential{b1})
	if err != nil || wait != 0 || c.Name != b1.Name {
		t.Fatalf("a tier B selection = %q, %s, %v; want the paid key: neither D142 control reaches tier B", c.Name, wait, err)
	}

	// The same state on a shared token is held, by the window and by the cap:
	// that is what makes the assertion above about the tier rather than about
	// the state it was in.
	_, _, err = selector(state(), set).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("the same state on a shared token = %v; want an ExhaustedUntil answer", err)
	}
	if want := since.Add(DefaultBackoffCap); !eu.At.Equal(want) {
		t.Fatalf("the shared token's answer = %s; want %s, the end of the window", eu.At, want)
	}
}

// The writer and the reader must measure the same window. A tier B key whose
// reset has passed is usable as far as the reader is concerned — free and
// earliest read it through forTier, with D142's controls off — so the next call
// really does go to it, and the 429 it answers with is a fresh exhaustion that
// must back the key off. Judged with the raw settings instead, the shared tier's
// five-hour window makes the writer call that 429 an in-flight call and leave the
// mark exhausted with a past Until: still usable, so still selected, denied again
// and never backed off, for the rest of a window nobody wrote for that key. That
// is the retry storm a backoff exists to stop, on the tier that spends money.
//
// The shared-token half of the same sequence is the contrast that makes this
// about the tier: there the window really is in force, and the second 429 really
// is an in-flight call.
func TestAFreshUsageLimitBacksOffATierBKey(t *testing.T) {
	reset := now.Add(time.Minute)
	// The reset has passed, so the key is in rotation again.
	at := reset.Add(time.Minute)
	newStore := func(name string) *fakeStore {
		st := newFakeStore()
		st.setMark(name, Mark{State: StateExhausted, Until: reset, Since: now})
		return st
	}
	clock := func() time.Time { return at }

	// The reader agrees the paid key is usable — that is why the call went there.
	st := newStore(b1.Name)
	s := &Selector{Store: st, Settings: Settings{Fallback: Auto}, Now: clock}
	if c, _, err := s.Select(ctx, run, []Credential{b1}); err != nil || c.Name != b1.Name {
		t.Fatalf("the paid key after its reset = %q, %v; want it selected", c.Name, err)
	}

	// And the 429 it answers with carries no reset header: a backoff, not the
	// reset it is standing on.
	m, err := s.Exhausted(ctx, b1.Name, TierB, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateBackedOff || m.Backoff <= 0 || !m.Until.After(at) {
		t.Fatalf("a fresh no-reset 429 on the paid key left %+v; want a backoff from %s", m, at)
	}

	// The same sequence on a shared token is held: the window its exhaustion
	// opened is still running, so the second 429 is an in-flight call.
	shared := newStore(a1.Name)
	ss := &Selector{Store: shared, Settings: Settings{}, Now: clock}
	if m, err := ss.Exhausted(ctx, a1.Name, TierA, time.Time{}); err != nil {
		t.Fatal(err)
	} else if m.State != StateExhausted || !m.Until.Equal(reset) {
		t.Fatalf("a no-reset 429 inside a shared token's window left %+v; want the reset it did not shorten", m)
	}
}

// The same disagreement on the probe's side: a failed probe on a tier B key
// whose reset has passed must leave the first backoff, not be swallowed by a
// window the reader does not apply to that key.
func TestAFailedProbeBacksOffATierBKey(t *testing.T) {
	reset := now.Add(time.Minute)
	at := reset.Add(time.Minute)
	st := newFakeStore()
	st.setMark(b1.Name, Mark{State: StateExhausted, Until: reset, Since: now})

	s := &Selector{Store: st, Settings: Settings{Fallback: Auto}, Now: func() time.Time { return at }}
	m, err := s.ProbeFailed(ctx, b1.Name, TierB)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateBackedOff || m.Backoff != DefaultBackoffInitial || !m.Until.After(at) {
		t.Fatalf("a failed probe on the paid key after its reset left %+v; want a %s backoff", m, DefaultBackoffInitial)
	}
}

// The exhausted-side in-flight race, through the public API: a credential
// exhausted for four hours, and a 429 arriving from a call that was already in
// flight carrying no reset header. The mark, and therefore the wait Select
// answers with, must stay the reset's.
func TestARacingUsageLimitKeepsTheResetInTheAnswer(t *testing.T) {
	st := newFakeStore()
	reset := now.Add(4 * time.Hour)
	st.setMark(b1.Name, Mark{State: StateExhausted, Since: now, Until: reset})

	// Tier B — the tier the review reproduced this on, and the one whose early
	// hand-back spends money.
	s := selector(st, Settings{Fallback: Auto})
	_, _, err := s.Select(ctx, run, []Credential{b1})
	var eu *ExhaustedUntil
	if !errors.As(err, &eu) {
		t.Fatalf("a selection against a four-hour reset = %v; want an ExhaustedUntil answer", err)
	}
	if eu.Wait != 4*time.Hour || !eu.At.Equal(reset) {
		t.Fatalf("the answer = %+v; want the reset %s four hours out", eu, reset)
	}

	// The racing report changes nothing: not the state, not the answer.
	if _, err := s.Exhausted(ctx, b1.Name, TierB, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := st.mark(b1.Name); got != (Mark{State: StateExhausted, Since: now, Until: reset}) {
		t.Fatalf("a racing no-reset 429 left %+v; want the reset %s", got, reset)
	}
	_, _, err = s.Select(ctx, run, []Credential{b1})
	if !errors.As(err, &eu) || eu.Wait != 4*time.Hour {
		t.Fatalf("the answer after the racing 429 = %v; want the reset's four hours", err)
	}
}

// A hand-built negative concurrency cap fails closed wherever it is read. Free
// has always refused it ("no room at all"), and the time answer must agree: the
// credential is refused, so "usable now, wait 0" is not an answer to give — a
// caller would retry immediately against a credential the cap refuses again.
// (Unreachable through the policy, which refuses a negative; withDefaults and
// concurrency both document the hand-built one as failing closed.)
func TestANegativeCapIsNotUsableNow(t *testing.T) {
	st := newFakeStore()
	st.setLease(Run{Slug: run.Slug, ID: "other"}, Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
	set := noWindow()
	set.ConcurrencyPerToken = ptr(-1)

	_, wait, err := selector(st, set).Select(ctx, run, []Credential{a1})
	var eu *ExhaustedUntil
	if errors.As(err, &eu) {
		t.Fatalf("a negative cap answered %+v; want a refusal: the cap refuses the credential, so no lapse frees it", eu)
	}
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("a negative cap = %q after %s; want ErrNoCredential", err, wait)
	}
}

// A selection needs the run identity it places: the slug and the run id are the
// run's own nodes under creds/, so a run without them is a caller's mistake and
// never a credential chosen for nobody. (Review finding M5: this guard had no
// test, so Run.valid could be deleted without a red test.)
func TestSelectRefusesARunWithNoIdentity(t *testing.T) {
	st := newFakeStore()
	s := selector(st, noWindow())
	for _, bad := range []Run{{}, {Slug: run.Slug}, {ID: run.ID}} {
		if _, _, err := s.Select(ctx, bad, []Credential{a1}); err == nil {
			t.Fatalf("a selection for the run %+v was made; want a refusal: the run's nodes are its slug and id", bad)
		}
	}
	if len(st.leases) != 0 || len(st.current) != 0 {
		t.Fatalf("a refused selection wrote %d leases and %d current nodes", len(st.leases), len(st.current))
	}
}

// The two fail-closed refusals of a candidate this release does not model: an
// unknown tier (a tier a newer release or a hand-built list carries) and a
// candidate with no name. Neither may be skipped quietly — a skipped candidate
// is a call served from a credential nobody admitted, and an unnamed one has no
// mark and no node to record one under. Review finding: neither branch had a
// test, so neither was ever executed by the suite.
func TestSelectRefusesAnUnmodelledCandidate(t *testing.T) {
	st := newFakeStore()
	s := selector(st, noWindow())

	_, _, err := s.Select(ctx, run, []Credential{{Name: "third-party", Tier: Tier("d")}})
	if err == nil || !strings.Contains(err.Error(), "not a tier this Fugaro knows") || !strings.Contains(err.Error(), "third-party") {
		t.Fatalf("a candidate of an unknown tier = %v; want the refusal that names the tier rule", err)
	}
	_, _, err = s.Select(ctx, run, []Credential{{Tier: TierA}})
	if err == nil || !strings.Contains(err.Error(), "has no name") {
		t.Fatalf("a candidate with no name = %v; want the refusal that says so", err)
	}
	// Nothing was taken for either refusal: a refused list is not a selection.
	if len(st.leases) != 0 || len(st.current) != 0 || len(st.marks) != 0 {
		t.Fatalf("a refused candidate list wrote something: %d leases, %d placements, %d marks", len(st.leases), len(st.current), len(st.marks))
	}
}

// One credential offered twice is refused rather than counted twice: a duplicate
// in a tie is a worse round robin for a reason nobody could see, and a
// duplicate's second lease would count against the cap as another run. (Review
// finding M5: this refusal had no test either.)
func TestSelectRefusesADuplicateCandidate(t *testing.T) {
	st := newFakeStore()
	_, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1, a1})
	if err == nil || !strings.Contains(err.Error(), a1.Name) {
		t.Fatalf("a candidate list holding %q twice = %v; want the refusal that names it", a1.Name, err)
	}
	if len(st.leases) != 0 {
		t.Fatal("a refused candidate list took a lease")
	}
}

// The tie-breaking counter is read from the database, so a hand-edit can leave
// it anywhere — including negative, and including the wrapped value a counter
// incremented at its maximum lands on. Neither may index outside the tied set.
// (Review finding M5: the clamp had no test.)
func TestTheTieBreakSurvivesACounterOutOfRange(t *testing.T) {
	for _, seed := range []int64{-1, math.MinInt64, math.MaxInt64} {
		st := newFakeStore()
		st.setRoundRobin(seed)
		set := noWindow()
		set.ConcurrencyPerToken = ptr(0)
		s := selector(st, set)

		c, _, err := s.Select(ctx, run, []Credential{a1, a2})
		if err != nil {
			t.Fatalf("a selection with the tie-breaking counter at %d = %v", seed, err)
		}
		if c.Name != a1.Name && c.Name != a2.Name {
			t.Fatalf("a selection with the counter at %d took %q, which is not one of the candidates", seed, c.Name)
		}
		// And the next selection still works: the counter's value is clamped
		// where it is read, not where it is written.
		if _, _, err := s.Select(ctx, Run{Slug: run.Slug, ID: "r2"}, []Credential{a1, a2}); err != nil {
			t.Fatalf("the selection after the counter reached %d = %v", seed, err)
		}
	}
}

// The sticky path takes the slot again rather than only answering: a run that
// keeps its credential but refreshes no lease loses its slot to the cap while it
// is still working, and its next call finds the credential full — of its own
// lapsed hold. (Review finding M5: with the take removed from this path, nothing
// went red, because the first selection's lease was still fresh.)
func TestTheStickyPathRefreshesTheLease(t *testing.T) {
	st := newFakeStore()
	// The run is on shared-1 with a lease that is live but nearly out.
	st.setPlacement(run, a1.Name, Lease{Credential: a1.Name, Until: now.Add(time.Minute)})

	// shared-2 is the one a fresh run would take (shared-1 has been exhausted),
	// so staying on shared-1 is the stickiness and not the ordering.
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})
	c, _, err := selector(st, noWindow()).Select(ctx, run, []Credential{a1, a2})
	if err != nil || c.Name != a1.Name {
		t.Fatalf("the sticky selection = %q, %v; want %q", c.Name, err, a1.Name)
	}
	l, ok := st.lease(run)
	if !ok || l.Credential != a1.Name || !l.Until.Equal(now.Add(DefaultLeaseTTL)) {
		t.Fatalf("the sticky selection left the lease %+v, %v; want it refreshed to %s", l, ok, now.Add(DefaultLeaseTTL))
	}
	if cur, _, err := st.Current(ctx, run); err != nil || cur != a1.Name {
		t.Fatalf("the sticky selection left the run on %q, %v; want %q", cur, err, a1.Name)
	}
}
