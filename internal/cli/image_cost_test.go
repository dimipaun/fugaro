package cli

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// fakePricer is the price lookup without a catalog: the book a test hands it,
// or the refusal it hands back.
type fakePricer struct {
	book budget.PriceBook
	err  error
}

func (p fakePricer) Rate(context.Context, string, string) (budget.BuildRate, error) {
	if p.err != nil {
		return budget.BuildRate{}, p.err
	}
	return budget.BuildCost(p.book, "E2_HIGHCPU_8", "us-east5")
}

// pricedBook prices the machine type and region the fixture's build config
// names (us-east5, the fixture project's region), at the rates
// internal/budget/buildcost_test.go's fixture uses, so one e2-highcpu-8 build
// reserves $0.94 here too.
func pricedBook() budget.PriceBook {
	row := func(id, desc string, micros budget.Micros) budget.BuildSKU {
		return budget.BuildSKU{ID: id, Description: desc, Unit: "min", Currency: "USD",
			Regions: []string{"us-east5"}, MicrosPerUnit: micros}
	}
	return budget.PriceBook{
		ServiceID: budget.BuildBillingServiceID,
		ReadAt:    time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		SKUs: []budget.BuildSKU{
			row("SKU-E2-CPU-USE5", "E2 CPU utilization per minute in us-east5", 1_500),
			row("SKU-E2-RAM-USE5", "E2 ram utilization per minute in us-east5", 450),
		},
	}
}

// imageCostDay is the noon the lease fixtures live at.
var imageCostDay = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func imageCostDayNo() int64 { return budget.Day(imageCostDay) }

// imageCostFixture is a checkout whose project config points at the RTDB fake,
// with the caps a test wants, and a build-lease writer over it.
func imageCostFixture(t *testing.T, pricer buildPricer) (*budgetFixture, *imageCost) {
	t.Helper()
	f := newBudgetFixture(t, "")
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := newBudgetClient(context.Background(), f.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathMode, budget.ModeEnforce)
	f.db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 100 * usd1})
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"dailyMicros": 5 * usd1})
	f.db.Set(budget.PathCapsGlobal, map[string]any{"perRunMicros": 50 * usd1, "dailyMicros": 100 * usd1})
	c := newImageCost(lc, db, "acme/app", appSlug, "someone@example.com", pricer)
	c.now = func() time.Time { return imageCostDay }
	return f, c
}

// TestOverCapErrorNamesEstimateCapSpentAndTheCommand: a build that would pass
// the day's cap is not submitted, and the refusal names the four things the
// operator needs — what the build reserves at most, the cap, what the day
// already counts (jobs and settled builds together), and the one command that
// raises it — and exits non-zero.
func TestOverCapErrorNamesEstimateCapSpentAndTheCommand(t *testing.T) {
	f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	// $4.90 of the repository's $5.00 daily cap is already counted, by the
	// day's jobs and its settled builds.
	f.db.Set(budget.PathSpendRepo(imageCostDayNo(), appSlug), map[string]any{"counted": 490 * usd1 / 100})

	_, advisory, err := c.take(context.Background(), "build-1")
	if err == nil {
		t.Fatal("a build over the repository's daily cap was leased")
	}
	if advisory != nil {
		t.Errorf("a refused build carries the advisory %+v", advisory)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit code %d, want %d: the operator can raise the cap", got, ExitUserError)
	}
	var over *BuildOverCapError
	if !errors.As(err, &over) {
		t.Fatalf("the refusal %v is not a *BuildOverCapError", err)
	}
	if over.Estimate != 936_000 {
		t.Errorf("Estimate = %d µ$, want the estimator's $0.94 for an e2-highcpu-8 build at the fixed timeout", over.Estimate)
	}
	if over.Cap != budget.Micros(5*usd1) || over.Scope != budget.ScopeRepo {
		t.Errorf("the cap named is %d µ$ in scope %s, want the repository's $5.00", over.Cap, over.Scope)
	}
	if over.Spent != budget.Micros(490*usd1/100) {
		t.Errorf("Spent = %d µ$, want the $4.90 the day counts", over.Spent)
	}
	if !strings.Contains(over.Command, "--repo acme/app --daily") {
		t.Errorf("the command named is %q, want the repository's own daily flag", over.Command)
	}
	msg := err.Error()
	for _, want := range []string{"$0.94", "$5.00", "$4.90", "jobs and its settled builds", "fugaro budget set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not name %q", msg, want)
		}
	}
	// Nothing was leased and nothing was counted: a refused build leaves the
	// day exactly as it was.
	if got := f.db.Value(budget.PathBuildLease(appSlug, "build-1")); got != nil {
		t.Errorf("a lease was written for a refused build: %v", got)
	}
	if got := repoCounter(t, c, "counted"); got != budget.Micros(490*usd1/100) {
		t.Errorf("the day's counter moved on a refused build: %d", got)
	}

	// Every flag the refusal names is a flag `fugaro budget set` really has.
	set, _, err := NewRootCmd().Find([]string{"budget", "set"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range flagsNamed(msg) {
		if set.Flags().Lookup(flag) == nil {
			t.Errorf("the refusal names --%s, which `fugaro budget set` does not have: %q", flag, msg)
		}
	}
}

// TestOverCapRefusalNamesTheSideThatBoundIt: the repository's daily cap is its
// own or the defaults' for repositories without one, and the command named is
// the side the predicate compared against — raising another side would change
// nothing about this build. The project's cap and the per-run cap get their own.
func TestOverCapRefusalNamesTheSideThatBoundIt(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(f *budgetFixture)
		wantCap budget.Micros
		wantCmd string
	}{
		{"the repository's own daily cap", func(f *budgetFixture) {
			f.db.Set(budget.PathSpendRepo(imageCostDayNo(), appSlug), map[string]any{"counted": 490 * usd1 / 100})
		}, budget.Micros(5 * usd1), "fugaro budget set --repo acme/app --daily <usd>"},
		{"the default for repositories without one", func(f *budgetFixture) {
			f.db.Set(budget.PathCapsRepo(appSlug), nil)
			f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 2 * usd1})
			f.db.Set(budget.PathSpendRepo(imageCostDayNo(), appSlug), map[string]any{"counted": 2 * usd1})
		}, budget.Micros(2 * usd1), "fugaro budget set --defaults --daily <usd>"},
		{"the project's daily cap", func(f *budgetFixture) {
			f.db.Set(budget.PathCapsRepo(appSlug), nil)
			f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 10 * usd1})
			f.db.Set(budget.PathSpendGlobal(imageCostDayNo()), map[string]any{"counted": 100 * usd1})
		}, budget.Micros(100 * usd1), "fugaro budget set --global --daily <usd>"},
		{"the per-run cap", func(f *budgetFixture) {
			f.db.Set(budget.PathCapsGlobal, map[string]any{"perRunMicros": 500_000, "dailyMicros": 100 * usd1})
		}, 500_000, "fugaro budget set --global --per-run <usd>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
			tc.seed(f)
			_, _, err := c.take(context.Background(), "build-1")
			var over *BuildOverCapError
			if !errors.As(err, &over) {
				t.Fatalf("the refusal is %v, want a *BuildOverCapError", err)
			}
			if over.Cap != tc.wantCap || over.Command != tc.wantCmd {
				t.Errorf("the refusal names cap %d and %q, want %d and %q", over.Cap, over.Command, tc.wantCap, tc.wantCmd)
			}
		})
	}
}

// TestUnknownPriceRefusesTheBuild: a price the catalog does not carry refuses
// the build, and no configuration may supply one (D1550). It is the operator's
// refusal (exit 1: fix the machine type), not the network's.
func TestUnknownPriceRefusesTheBuild(t *testing.T) {
	f, c := imageCostFixture(t, fakePricer{err: &budget.NoPriceError{MachineType: "E2_HIGHCPU_8", Region: "us-east5",
		Why: "no row's description matches"}})
	if _, _, err := c.take(context.Background(), "build-1"); err == nil {
		t.Fatal("a build with no price in the catalog was leased")
	} else if ExitCode(err) != ExitUserError {
		t.Errorf("exit code %d, want %d", ExitCode(err), ExitUserError)
	}
	if got := f.db.Value(budget.PathBuildLease(appSlug, "build-1")); got != nil {
		t.Errorf("a lease was written with no price: %v", got)
	}
}

// TestObserveModeRecordsAndProceeds: in observe mode — and with the mode node
// absent, which is observe too — the build proceeds, the estimate is recorded
// as a lease and in the day's counters, and the cap refusal comes back as the
// advisory the caller prints (D1548).
func TestObserveModeRecordsAndProceeds(t *testing.T) {
	for _, mode := range []any{budget.ModeObserve, nil} {
		t.Run("mode", func(t *testing.T) {
			f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
			f.db.Set(budget.PathMode, mode)
			// Far past the cap: observe records it rather than refusing.
			f.db.Set(budget.PathSpendRepo(imageCostDayNo(), appSlug), map[string]any{"counted": 400 * usd1})

			l, advisory, err := c.take(context.Background(), "build-1")
			if err != nil {
				t.Fatal(err)
			}
			if advisory == nil || advisory.Reason != budget.ReasonRepoDailyCap {
				t.Errorf("the advisory is %+v, want the cap refusal observe let through", advisory)
			}
			if l.State != budget.BuildLeaseOpen || l.Reserved != 936_000 || l.Repo != appSlug || l.ID != "build-1" {
				t.Errorf("the lease recorded is %+v", l)
			}
			if l.Exp <= c.now().UnixMilli() {
				t.Errorf("the lease has no deadline after it: %d", l.Exp)
			}
			if l.PriceReadAt != pricedBook().ReadAt.UnixMilli() {
				t.Errorf("PriceReadAt = %d, want %d: the lease records when the price was read", l.PriceReadAt, pricedBook().ReadAt.UnixMilli())
			}
			var stored budget.BuildLease
			if _, err := c.db.Get(context.Background(), budget.PathBuildLease(appSlug, "build-1"), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Reserved != 936_000 || len(stored.SKUs) != 2 || stored.MachineType != "E2_HIGHCPU_8" {
				t.Errorf("the recorded lease is %+v, want the estimate and the rows it came from", stored)
			}
			// The counters carry it: a build's money is the day's money, beside
			// the jobs'.
			if got, want := repoCounter(t, c, "counted"), budget.Micros(400*usd1+936_000); got != want {
				t.Errorf("the repository counts %d µ$, want %d", got, want)
			}
			if got, want := globalCounter(t, c, "counted"), budget.Micros(936_000); got != want {
				t.Errorf("the project counts %d µ$, want %d", got, want)
			}
			if got := repoCounter(t, c, "spent"); got != 0 {
				t.Errorf("a build that has not settled wrote %d as spent", got)
			}
		})
	}
}

// TestSwitchesAndTheLimitBiteInEveryMode: the kill switches and maxReserve are
// the two things observe mode does not soften (D1548), so a build meets them
// even there.
func TestSwitchesAndTheLimitBiteInEveryMode(t *testing.T) {
	t.Run("a kill switch", func(t *testing.T) {
		f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
		f.db.Set(budget.PathMode, budget.ModeObserve)
		f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "someone@example.com", "reason": "spike"})
		_, _, err := c.take(context.Background(), "build-1")
		if err == nil {
			t.Fatal("a build was leased with the global switch on")
		}
		if !strings.Contains(err.Error(), "kill switch") {
			t.Errorf("the refusal is %v, want the switch named", err)
		}
	})
	t.Run("maxReserve", func(t *testing.T) {
		f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
		f.db.Set(budget.PathMode, budget.ModeObserve)
		f.db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 100_000})
		_, _, err := c.take(context.Background(), "build-1")
		if err == nil {
			t.Fatal("a build over maxReserve was leased")
		}
		if !strings.Contains(err.Error(), "fugaro budget set --global --max-reserve") {
			t.Errorf("the refusal %v does not name the command that raises the limit", err)
		}
	})
}

// TestABuildIdNamesOneSubmission: a lease already under an id is a command run
// twice with the same name, and the second one refuses rather than counting the
// same money twice.
func TestABuildIdNamesOneSubmission(t *testing.T) {
	_, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	if _, _, err := c.take(context.Background(), "build-1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.take(context.Background(), "build-1")
	if err == nil {
		t.Fatal("a second lease was taken under an id that already holds one")
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the refusal is %v (exit %d), want the operator's own error naming the lease", err, ExitCode(err))
	}
	if got := repoCounter(t, c, "counted"); got != 936_000 {
		t.Errorf("the repository counts %d, want the one reservation", got)
	}
}

// TestSettleRecordsTheSpendAndGivesTheRestBack: a build that finishes early
// settles on what it really spent, and the day gets the rest back.
func TestSettleRecordsTheSpendAndGivesTheRestBack(t *testing.T) {
	_, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	l, _, err := c.take(context.Background(), "build-1")
	if err != nil {
		t.Fatal(err)
	}
	settled, err := c.settle(context.Background(), l, 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != budget.BuildLeaseSettled || settled.Settled != 200_000 || settled.Released != 736_000 {
		t.Errorf("the settled lease is %+v", settled)
	}
	var stored budget.BuildLease
	if _, err := c.db.Get(context.Background(), budget.PathBuildLease(appSlug, "build-1"), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.State != budget.BuildLeaseSettled || stored.Settled != 200_000 {
		t.Errorf("the stored lease is %+v", stored)
	}
	if got, want := repoCounter(t, c, "counted"), budget.Micros(200_000); got != want {
		t.Errorf("the repository counts %d µ$, want %d: the day holds what the build spent", got, want)
	}
	if got := repoCounter(t, c, "spent"); got != 200_000 {
		t.Errorf("the day's spent = %d, want the build's 200000", got)
	}
	if got := globalCounter(t, c, "spent"); got != 200_000 {
		t.Errorf("the project's spent = %d, want the same", got)
	}
	// Settling twice is a caller bug and writes nothing.
	if _, err := c.settle(context.Background(), settled, 1); err == nil {
		t.Error("a settled lease was settled again")
	} else if ExitCode(err) != ExitUserError {
		t.Errorf("settling twice exits %d, want %d", ExitCode(err), ExitUserError)
	}

	// A lease another process settled while this one was building is refused
	// from the stale copy, not written over: the count happens once.
	l2, _, err := c.take(context.Background(), "build-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.settle(context.Background(), l2, 100_000); err != nil {
		t.Fatal(err)
	}
	if _, err := c.settle(context.Background(), l2, 50_000); err == nil {
		t.Error("a lease already settled in the database was settled again from a stale copy")
	}
	if got, want := repoCounter(t, c, "counted"), budget.Micros(300_000); got != want {
		t.Errorf("the repository counts %d, want %d: two builds, 200000 and 100000 spent", got, want)
	}
}

// TestReleaseGivesEverythingBack: a build that never ran writes no spend and
// gives the whole reservation back.
func TestReleaseGivesEverythingBack(t *testing.T) {
	_, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	l, _, err := c.take(context.Background(), "build-1")
	if err != nil {
		t.Fatal(err)
	}
	released, err := c.release(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if released.State != budget.BuildLeaseReleased || released.Released != 936_000 {
		t.Errorf("the released lease is %+v", released)
	}
	if got := repoCounter(t, c, "counted"); got != 0 {
		t.Errorf("the repository still counts %d", got)
	}
	if got := repoCounter(t, c, "spent"); got != 0 {
		t.Errorf("a released build wrote %d as spent", got)
	}
	if got := globalCounter(t, c, "counted"); got != 0 {
		t.Errorf("the project still counts %d", got)
	}
}

// TestAnAbandonedBuildLeaseIsReclaimed: a CLI that died mid-build leaves an
// open lease behind. The next build in that repository gives it back — the
// money returns to the day it was counted in — while a lease whose build may
// still be running is left alone (F19).
func TestAnAbandonedBuildLeaseIsReclaimed(t *testing.T) {
	f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	dead := budget.BuildLease{
		State: budget.BuildLeaseOpen, Repo: appSlug, ID: "build-dead", By: "someone@example.com",
		Reserved: 936_000, StartedAt: imageCostDay.Add(-2 * time.Hour).UnixMilli(),
		Exp: imageCostDay.Add(-time.Hour).UnixMilli(),
	}
	f.db.Set(budget.PathBuildLease(appSlug, "build-dead"), dead)
	running := budget.BuildLease{
		State: budget.BuildLeaseOpen, Repo: appSlug, ID: "build-running", By: "someone@example.com",
		Reserved: 400_000, StartedAt: imageCostDay.UnixMilli(), Exp: imageCostDay.Add(time.Hour).UnixMilli(),
	}
	f.db.Set(budget.PathBuildLease(appSlug, "build-running"), running)
	f.db.Set(budget.PathSpendRepo(imageCostDayNo(), appSlug), map[string]any{"counted": 936_000 + 400_000})
	f.db.Set(budget.PathSpendGlobal(imageCostDayNo()), map[string]any{"counted": 936_000 + 400_000})

	n, err := c.reclaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d leases, want the one whose build is over", n)
	}
	var got budget.BuildLease
	if _, err := c.db.Get(context.Background(), budget.PathBuildLease(appSlug, "build-dead"), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != budget.BuildLeaseReleased || got.Released != 936_000 {
		t.Errorf("the abandoned lease is %+v, want it released, holding nothing", got)
	}
	if _, err := c.db.Get(context.Background(), budget.PathBuildLease(appSlug, "build-running"), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != budget.BuildLeaseOpen {
		t.Errorf("the lease of a build that may still be running is %+v", got)
	}
	if w, want := repoCounter(t, c, "counted"), budget.Micros(400_000); w != want {
		t.Errorf("the repository counts %d, want %d: the dead build's money came back", w, want)
	}
	if w, want := globalCounter(t, c, "counted"), budget.Micros(400_000); w != want {
		t.Errorf("the project counts %d, want %d", w, want)
	}
	// Reclaiming again finds nothing to do: the release is written once.
	if n, err := c.reclaim(context.Background()); err != nil || n != 0 {
		t.Errorf("the second reclaim closed %d leases (%v), want none", n, err)
	}
}

// TestALeaseFromAClosedDayIsClosedWithoutACounterWrite: a lease abandoned days
// ago reserved from a day no cap compares against any more. It is closed — the
// lease family does not grow forever — and the counters of that day are left as
// they are, because a release the predicate refuses for its age is not a write
// this may make anyway.
func TestALeaseFromAClosedDayIsClosedWithoutACounterWrite(t *testing.T) {
	f, c := imageCostFixture(t, fakePricer{book: pricedBook()})
	old := imageCostDay.Add(-72 * time.Hour)
	oldNo := budget.Day(old)
	dead := budget.BuildLease{
		State: budget.BuildLeaseOpen, Repo: appSlug, ID: "build-ancient", By: "someone@example.com",
		Reserved: 936_000, StartedAt: old.UnixMilli(), Exp: old.Add(time.Hour).UnixMilli(),
	}
	f.db.Set(budget.PathBuildLease(appSlug, "build-ancient"), dead)
	f.db.Set(budget.PathSpendRepo(oldNo, appSlug), map[string]any{"counted": 936_000})
	f.db.Set(budget.PathSpendGlobal(oldNo), map[string]any{"counted": 936_000})

	n, err := c.reclaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d leases, want the one whose day has closed", n)
	}
	var got budget.BuildLease
	if _, err := c.db.Get(context.Background(), budget.PathBuildLease(appSlug, "build-ancient"), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != budget.BuildLeaseReleased || got.Released != 936_000 {
		t.Errorf("the closed lease is %+v, want it released", got)
	}
	var v budget.Micros
	if _, err := c.db.Get(context.Background(), budget.PathSpendRepo(oldNo, appSlug)+"/counted", &v); err != nil {
		t.Fatal(err)
	}
	if v != 936_000 {
		t.Errorf("the closed day's counter is %d, want the %d it held: a day no cap reads is not paid back", v, 936_000)
	}
}

// repoCounter and globalCounter read the fixture day's counter leaves.
func repoCounter(t *testing.T, c *imageCost, leaf string) budget.Micros {
	t.Helper()
	var v budget.Micros
	if _, err := c.db.Get(context.Background(), budget.PathSpendRepo(imageCostDayNo(), appSlug)+"/"+leaf, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func globalCounter(t *testing.T, c *imageCost, leaf string) budget.Micros {
	t.Helper()
	var v budget.Micros
	if _, err := c.db.Get(context.Background(), budget.PathSpendGlobal(imageCostDayNo())+"/"+leaf, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// flagsNamed is every --flag a message names.
func flagsNamed(msg string) []string {
	var out []string
	for _, f := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(msg, -1) {
		out = append(out, strings.TrimPrefix(f, "--"))
	}
	return out
}
