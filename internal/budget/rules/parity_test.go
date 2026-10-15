//go:build firebase

package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func get[T any](ctx context.Context, c *rtdb.Client, path string) (*T, error) {
	var v T
	found, err := c.Get(ctx, path, &v)
	if err != nil || !found {
		return nil, err
	}
	return &v, nil
}

func mustGet(t *testing.T, h *harness, path string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(h.value(path)), out); err != nil {
		t.Fatalf("/%s: %v", path, err)
	}
}

// readState reads what the lease client reads before it writes: the run's
// ledger and day share, the repository's and the global counters, the caps,
// the mode and the switches, all with the run's own token.
func readState(ctx context.Context, c *rtdb.Client, slug, run string, day int64) (st budget.State, caps budget.Caps, kills budget.Kills, err error) {
	var run1, share *budget.RunLedger
	var repo, global *budget.Counters
	if run1, err = get[budget.RunLedger](ctx, c, budget.PathRun(slug, run)); err != nil {
		return
	}
	if share, err = get[budget.RunLedger](ctx, c, budget.PathSpendRun(day, slug, run)); err != nil {
		return
	}
	if repo, err = get[budget.Counters](ctx, c, budget.PathSpendRepo(day, slug)); err != nil {
		return
	}
	if global, err = get[budget.Counters](ctx, c, budget.PathSpendGlobal(day)); err != nil {
		return
	}
	for _, p := range []*struct {
		path string
		into any
	}{{budget.PathCapsGlobal, &caps.Global}, {budget.PathCapsDefaults, &caps.Defaults}, {budget.PathCapsRepo(slug), &caps.Repo},
		{budget.PathLimits, &caps.Limits}, {budget.PathKillGlobal, &kills.Global}, {budget.PathKillRepo(slug), &kills.Repo}} {
		var raw json.RawMessage
		found, gerr := c.Get(ctx, p.path, &raw)
		if gerr != nil {
			err = gerr
			return
		}
		if found {
			if err = json.Unmarshal(raw, p.into); err != nil {
				return
			}
		}
	}
	if _, err = c.Get(ctx, budget.PathMode, &caps.Mode); err != nil {
		return
	}
	if run1 != nil {
		st.Run = *run1
	}
	if share != nil {
		st.DayRun = *share
	}
	if repo != nil {
		st.Repo = *repo
	}
	if global != nil {
		st.Global = *global
	}
	return
}

// parityCase is one random lease or release against a random database.
type parityCase struct {
	w   world
	op  budget.Op
	amt budget.Micros
}

func (p parityCase) String() string {
	b, _ := json.Marshal(struct {
		Op       budget.Op
		Amount   budget.Micros
		Day, Now int64
		State    budget.State
		Caps     budget.Caps
		Kills    budget.Kills
	}{p.op, p.amt, p.w.day, today(), p.w.st, p.w.caps, p.w.kills})
	return string(b)
}

const unit = usd / 2

func genCase(r *rand.Rand) parityCase {
	u := func(lo, hi int) budget.Micros { return budget.Micros(lo+r.Intn(hi-lo+1)) * unit }
	maybe := func(p float64) bool { return r.Float64() < p }
	w := world{slug: "aurora", run: "r1"}
	op := budget.OpLease
	if maybe(0.4) {
		op = budget.OpRelease
	}
	days := []int64{today(), today(), today(), today(), today(), today(), today(), today() - 1, today() + 1}
	if op == budget.OpRelease {
		days = []int64{today(), today(), today() - 1, today() - 1, today() + 1, today() - 2}
	}
	w.day = days[r.Intn(len(days))]
	// Ledgers: reserved >= released + spent most of the time.
	ledger := func() budget.RunLedger {
		l := budget.RunLedger{Reserved: u(0, 16)}
		l.Released = budget.Micros(r.Int63n(int64(l.Reserved)/int64(unit)+1)) * unit
		l.Spent = budget.Micros(r.Int63n(int64(l.Reserved-l.Released)/int64(unit)+1)) * unit
		if maybe(0.03) { // an inconsistent ledger: spent beyond what is left
			l.Spent = l.Reserved - l.Released + unit
		}
		return l
	}
	w.st.Run, w.st.DayRun = ledger(), ledger()
	w.st.Repo.Counted = w.st.DayRun.Outstanding() + u(0, 60)
	w.st.Global.Counted = w.st.Repo.Counted + u(0, 120)
	capv := func(p float64, lo, hi int) *budget.Micros {
		if !maybe(p) {
			return nil
		}
		v := u(lo, hi)
		return &v
	}
	w.caps.Global = &budget.GlobalCaps{DailyMicros: capv(0.93, 4, 160), PerRunMicros: capv(0.93, 2, 24)}
	w.caps.Defaults = &budget.DefaultCaps{RepoDailyMicros: capv(0.9, 2, 80), RepoPerRunMicros: capv(0.9, 2, 24)}
	if maybe(0.3) {
		w.caps.Repo = &budget.RepoCaps{DailyMicros: capv(0.5, 0, 140), PerRunMicros: capv(0.5, 0, 50)}
	}
	if maybe(0.04) {
		w.caps.Global = nil
	}
	if maybe(0.04) {
		w.caps.Defaults = nil
	}
	w.caps.Limits = &budget.Limits{MaxReserveMicros: capv(0.95, 2, 14)}
	w.caps.Mode = []string{budget.ModeEnforce, budget.ModeEnforce, budget.ModeEnforce, budget.ModeObserve, "", "bogus"}[r.Intn(6)]
	if maybe(0.06) {
		w.kills.Global = &budget.Kill{On: true, By: "alice"}
	}
	if maybe(0.06) {
		w.kills.Repo = &budget.Kill{On: true}
	}
	if maybe(0.1) { // a switch that exists and is off
		w.kills.Repo = &budget.Kill{On: false}
	}
	amt := u(1, 8)
	if op == budget.OpRelease {
		amt = u(1, 4)
	}
	return parityCase{w: w, op: op, amt: amt}
}

// checkParity writes one case against the seeded world and returns whether
// the rules accepted it, together with Evaluate's decision. It fails when
// the two disagree.
func checkParity(t *testing.T, h *harness, name string, c parityCase) (rulesAllow bool, d budget.Decision) {
	t.Helper()
	h.seed(c.w)
	d = budget.Evaluate(c.w.st, budget.Write{Op: c.op, Day: c.w.day, Amount: c.amt}, c.w.caps, c.w.kills, time.Now())
	var updates map[string]any
	if c.op == budget.OpLease {
		updates = leaseUpdates(c.w, c.amt)
	} else {
		updates = releaseUpdates(c.w, c.amt)
	}
	err := c.w.client(h).Patch(context.Background(), "", updates)
	switch {
	case err == nil:
		rulesAllow = true
	case errors.Is(err, rtdb.ErrPermission):
	default:
		t.Fatalf("%s: %v", name, err)
	}
	if rulesAllow != d.Allow {
		t.Fatalf("%s: the rules allow=%v but Evaluate allow=%v (%s: %s)\n%s", name, rulesAllow, d.Allow, d.Reason, d.Detail, c)
	}
	return rulesAllow, d
}

// directedCase is one hand-built parity case and the outcome the design
// says it must have.
type directedCase struct {
	name      string
	c         parityCase
	wantAllow bool
}

// directedCases are the per-run fallback shapes of D6: a repository with no
// per-run cap of its own, bounded by the default or, failing that, by the
// project's global cap, and the fail-closed shape with no per-run cap set
// anywhere. The random generator reaches them only by chance, so they run
// at every FUGARO_PARITY_CASES.
func directedCases() []directedCase {
	// A world holding a $9 lease already, with the caps of the case.
	w := func(caps budget.Caps, reserved budget.Micros) world {
		caps.Limits = &budget.Limits{MaxReserveMicros: mp(5 * usd)}
		caps.Mode = budget.ModeEnforce
		return world{slug: "aurora", run: "r1", day: today(), caps: caps,
			st: budget.State{
				Run:    budget.RunLedger{Reserved: reserved},
				DayRun: budget.RunLedger{Reserved: reserved},
				Repo:   budget.Counters{Counted: reserved},
				Global: budget.Counters{Counted: reserved},
			}}
	}
	lease := func(w world, amt budget.Micros) parityCase {
		return parityCase{w: w, op: budget.OpLease, amt: amt}
	}
	// The project's $150 daily cap is always set: these cases are about the
	// per-run side only.
	dayCaps := func(def *budget.DefaultCaps) budget.Caps {
		return budget.Caps{Global: &budget.GlobalCaps{DailyMicros: mp(150 * usd), PerRunMicros: mp(20 * usd)}, Defaults: def}
	}
	return []directedCase{
		// No repository node and no default per-run cap: the global $20
		// holds 9 + 1 and refuses 19 + 1.
		{"the global per-run cap alone bounds the run", lease(w(dayCaps(&budget.DefaultCaps{RepoDailyMicros: mp(60 * usd)}), 9*usd), usd), true},
		{"... and refuses past it", lease(w(dayCaps(&budget.DefaultCaps{RepoDailyMicros: mp(60 * usd)}), 20*usd), usd), false},
		// The default below the global one binds first.
		{"the default per-run cap is the smaller side", lease(w(dayCaps(&budget.DefaultCaps{RepoDailyMicros: mp(60 * usd), RepoPerRunMicros: mp(10 * usd)}), 9*usd), usd), true},
		{"... and refuses where the global cap would allow", lease(w(dayCaps(&budget.DefaultCaps{RepoDailyMicros: mp(60 * usd), RepoPerRunMicros: mp(10 * usd)}), 10*usd), usd), false},
		// Nothing set anywhere: still denied (fail closed).
		{"no per-run cap anywhere", lease(w(budget.Caps{Global: &budget.GlobalCaps{DailyMicros: mp(150 * usd)}, Defaults: &budget.DefaultCaps{RepoDailyMicros: mp(60 * usd)}}, 0), usd), false},
	}
}

// TestRulesParity: for random databases and writes the rules accept the
// write if and only if budget.Evaluate does (the lease client relies on this
// to tell a stale read from a refusal). The delta equations and the token
// checks are not part of Evaluate; the writes here are correct ones.
func TestRulesParity(t *testing.T) {
	h := newHarness(t)
	seed := int64(20261002) // fixed, so CI is reproducible; FUGARO_PARITY_SEED explores others
	if s := fmt.Sprint(seedFromEnv()); s != "0" {
		fmt.Sscan(s, &seed)
	}
	t.Logf("seed %d (set FUGARO_PARITY_SEED to replay)", seed)
	r := rand.New(rand.NewSource(seed))
	var allowed, denied, advisory int
	reasons := map[budget.Reason]int{}
	cases := 500
	if n := os.Getenv("FUGARO_PARITY_CASES"); n != "" {
		fmt.Sscan(n, &cases)
	}
	// The generator reaches the fallback shapes of D6 only by chance (a
	// repository with no per-run cap of its own needs three independent
	// draws), and FUGARO_PARITY_CASES can be set to a handful of cases: run
	// them directed, at every case count.
	for _, dc := range directedCases() {
		_, d := checkParity(t, h, dc.name, dc.c)
		if d.Allow != dc.wantAllow {
			t.Errorf("directed %s: allow=%v (%s), want %v", dc.name, d.Allow, d.Reason, dc.wantAllow)
		}
		t.Logf("directed %s: allow=%v (%s)", dc.name, d.Allow, d.Reason)
	}
	for i := 0; i < cases; i++ {
		c := genCase(r)
		rulesAllow, d := checkParity(t, h, fmt.Sprintf("case %d (seed %d)", i, seed), c)
		if !d.Allow {
			reasons[d.Reason]++
		} else if d.Advisory != nil {
			advisory++
		}
		switch {
		case rulesAllow:
			allowed++
		default:
			denied++
		}
	}
	t.Logf("%d cases:", cases)
	t.Logf("cases: %d allowed, %d denied", allowed, denied)
	t.Logf("denials by Evaluate's reason: %v; allowed as advisory in observe: %d", reasons, advisory)
	for _, want := range []budget.Reason{budget.ReasonKillSwitch, budget.ReasonRunCap, budget.ReasonRepoDailyCap, budget.ReasonGlobalDailyCap,
		budget.ReasonNoCap, budget.ReasonMaxReserve, budget.ReasonNotToday, budget.ReasonOldDay, budget.ReasonOverRelease} {
		if reasons[want] < 3 {
			t.Errorf("the generator exercised %q only %d times", want, reasons[want])
		}
	}
	if advisory < 5 {
		t.Errorf("only %d advisory (observe) allows", advisory)
	}
	if allowed < 80 || denied < 80 {
		t.Fatalf("the generator is lopsided: %d allowed, %d denied", allowed, denied)
	}
}

func seedFromEnv() int64 {
	var n int64
	fmt.Sscan(os.Getenv("FUGARO_PARITY_SEED"), &n)
	return n
}
