package budget_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
)

func refusalOf(t *testing.T, err error) *gateway.Refusal {
	t.Helper()
	var r *gateway.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a *gateway.Refusal", err)
	}
	return r
}

func TestLeaseTopUpAndRelease(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	d := today()

	got, err := s.Lease().Grant(ctx, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	// 5% of the smallest headroom ($20 per run), clamped to $0.25..$2.
	if got != 1_000_000 {
		t.Fatalf("granted %d, want 1,000,000 (5%% of $20)", got)
	}
	for name, v := range map[string]int64{
		"run reserved": f.run("reserved"), "share reserved": f.share(d, "reserved"),
		"repo counted": f.repo(d, "counted"), "global counted": f.global(d, "counted"),
	} {
		if v != 1_000_000 {
			t.Errorf("%s = %d, want 1,000,000", name, v)
		}
	}
	if _, err := s.Lease().Grant(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// 5% of the $19 headroom that is left.
	if f.run("reserved") != 1_950_000 || f.global(d, "counted") != 1_950_000 {
		t.Fatalf("second lease: run %d, global %d", f.run("reserved"), f.global(d, "counted"))
	}

	// Another run's counters are only ever moved by this run's own delta.
	f.db.Set(budget.PathSpendGlobal(d)+"/counted", 1_950_000+7_000_000)
	if err := s.Lease().Release(ctx, 400_000); err != nil {
		t.Fatal(err)
	}
	if f.run("released") != 400_000 || f.share(d, "released") != 400_000 {
		t.Fatalf("released: run %d, share %d", f.run("released"), f.share(d, "released"))
	}
	if f.repo(d, "counted") != 1_550_000 || f.global(d, "counted") != 8_550_000 {
		t.Fatalf("counters after release: repo %d, global %d (a release moves them by exactly its amount)", f.repo(d, "counted"), f.global(d, "counted"))
	}
	if s.GrantedMicros() != 1_950_000 || s.ReleasedMicros() != 400_000 {
		t.Fatalf("granted %d, released %d", s.GrantedMicros(), s.ReleasedMicros())
	}
}

// TestReleaseEqualsUnused: what the gateway gives back is exactly what the
// counters lose, never more than the run holds unspent.
func TestReleaseEqualsUnused(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	used := budget.Micros(300_000)
	s.SetUsage(func() budget.Micros { return used })
	// The usage is reported first (the gateway reports at the end of a
	// stage), then the unused part of the lease is released.
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 3, Used: used, ByModel: map[string]pricing.Micros{"claude-sonnet-5-5": used}, Tokens: 1200}); err != nil {
		t.Fatal(err)
	}
	if got := f.run("spent"); got != 300_000 {
		t.Fatalf("spent = %d", got)
	}
	if err := s.Lease().Release(ctx, 700_000); err != nil {
		t.Fatal(err)
	}
	if f.run("released") != 700_000 || f.repo(d, "counted") != 300_000 || f.global(d, "counted") != 300_000 {
		t.Fatalf("released %d, repo counted %d, global counted %d", f.run("released"), f.repo(d, "counted"), f.global(d, "counted"))
	}
	// More than the run holds unspent cannot be released.
	err := s.Lease().Release(ctx, 500_000)
	if err == nil {
		t.Fatal("released more than the run holds")
	}
	if f.run("released") != 700_000 || f.global(d, "counted") != 300_000 {
		t.Fatalf("an over-release changed the database: released %d, global %d", f.run("released"), f.global(d, "counted"))
	}
}

func TestHeartbeatMovesSpentNotCounted(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 250_000 })
	waitFor(t, "a heartbeat to report the spend", func() bool { return f.run("spent") == 250_000 })
	if f.repo(d, "spent") != 250_000 || f.global(d, "spent") != 250_000 || f.share(d, "spent") != 250_000 {
		t.Fatalf("spent: repo %d, global %d, share %d", f.repo(d, "spent"), f.global(d, "spent"), f.share(d, "spent"))
	}
	// Moving spend from outstanding to spent never changes `counted`.
	if f.repo(d, "counted") != 1_000_000 || f.global(d, "counted") != 1_000_000 || f.run("reserved") != 1_000_000 {
		t.Fatalf("counted moved: repo %d, global %d, run reserved %d", f.repo(d, "counted"), f.global(d, "counted"), f.run("reserved"))
	}
	// And the registry shows it.
	entry, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
	if entry == nil || entry["requestedBy"] != rb || entry["repo"] != "acme/app" {
		t.Fatalf("registry entry = %v", entry)
	}
	waitFor(t, "the entry's spend", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["spent"] != nil && e["spent"].(interface{ Int64() (int64, error) }) != nil
	})
}

func TestSpentNeverExceedsWhatIsHeld(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	// A call that cost more than its reservation (overrun) pushes the
	// gateway's used figure past the lease; the report is clamped to what the
	// run holds, or the rules would deny it for good.
	s.SetUsage(func() budget.Micros { return 1_300_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 1_300_000, Overrun: 300_000}); err != nil {
		t.Fatal(err)
	}
	if f.run("spent") != 1_000_000 || f.run("overrun") != 300_000 {
		t.Fatalf("spent %d, overrun %d", f.run("spent"), f.run("overrun"))
	}
}

func TestRepoDailyCapRefuses(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	d := today()
	f.db.Set(budget.PathSpendRepo(d, slug)+"/counted", 59_900_000)
	_, err := s.Lease().Grant(context.Background(), 500_000)
	r := refusalOf(t, err)
	if r.Reason != "repo_daily_cap" || r.Scope != "repo" {
		t.Fatalf("refusal = %+v", r)
	}
	if f.run("reserved") != 0 {
		t.Fatal("a refused lease wrote")
	}
}

func TestLeaseSizeShrinksNearACap(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	d := today()
	f.db.Set(budget.PathSpendRepo(d, slug)+"/counted", 59_000_000) // $1.00 of headroom
	got, err := s.Lease().Grant(context.Background(), 100_000)
	if err != nil {
		t.Fatal(err)
	}
	// 5% of $1.00 is below the $0.25 floor; the floor fits.
	if got != 250_000 {
		t.Fatalf("granted %d, want 250,000", got)
	}
	// With $0.10 of headroom the floor no longer fits but the call does.
	f.db.Set(budget.PathSpendRepo(d, slug)+"/counted", 59_900_000+int64(got)-int64(got))
	f.db.Set(budget.PathSpendRepo(d, slug)+"/counted", 59_900_000)
	got, err = s.Lease().Grant(context.Background(), 80_000)
	if err != nil {
		t.Fatal(err)
	}
	if got != 80_000 {
		t.Fatalf("granted %d, want exactly the call's 80,000", got)
	}
}

func TestGlobalDailyCapRefuses(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	f.db.Set(budget.PathSpendGlobal(today())+"/counted", 149_900_000)
	_, err := s.Lease().Grant(context.Background(), 500_000)
	if r := refusalOf(t, err); r.Reason != "global_daily_cap" || r.Scope != "global" {
		t.Fatalf("refusal = %+v", r)
	}
}

func TestRunCapFromRTDBMin(t *testing.T) {
	f := newFixture(t)
	// The repository allows $20 per run, the project $0.30: the lower rules.
	f.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: ptr(150_000_000), PerRunMicros: ptr(300_000)})
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	got, err := s.Lease().Grant(ctx, 100_000)
	if err != nil || got != 250_000 {
		t.Fatalf("granted %d, %v; want the $0.25 floor, which fits under the $0.30 minimum", got, err)
	}
	_, err = s.Lease().Grant(ctx, 100_000)
	if r := refusalOf(t, err); r.Reason != "run_cap" || r.Scope != "run" {
		t.Fatalf("refusal = %+v", r)
	}
}

func TestCommittedDayCapHalts(t *testing.T) {
	f := newFixture(t)
	f.cfg.CommittedDaily = 1_000_000
	f.cfg.LocalEnforce = true
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	f.db.Set(budget.PathSpendRepo(today(), slug)+"/counted", 900_000)
	_, err := s.Lease().Grant(ctx, 200_000)
	r := refusalOf(t, err)
	if r.Reason != "repo_daily_cap" || !strings.Contains(r.Detail, "fugaro.yaml") {
		t.Fatalf("refusal = %+v: the detail must name fugaro.yaml", r)
	}
}

// The committed cap is the repository's own: other repositories' counters do
// not count against it, only this one's.
func TestCommittedDayCapIsRepositoryScoped(t *testing.T) {
	f := newFixture(t)
	f.cfg.CommittedDaily = 1_000_000
	f.cfg.LocalEnforce = true
	s := f.started(budget.AgentEntry{})
	f.db.Set(budget.PathSpendGlobal(today())+"/counted", 100_000_000) // other repositories' spend
	if _, err := s.Lease().Grant(context.Background(), 200_000); err != nil {
		t.Fatalf("global spend counted against the repository's committed cap: %v", err)
	}
}

func TestPolicyRunCapIsClientSide(t *testing.T) {
	f := newFixture(t)
	f.cfg.PolicyCap = 1_500_000 // tighter than the database's $20
	f.cfg.LocalEnforce = true
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	got, err := s.Lease().Grant(ctx, 100_000)
	if err != nil || got != 75_000+175_000 && got != 250_000 {
		// 5% of $1.50 is $0.075, floored to $0.25.
		t.Fatalf("granted %d, %v", got, err)
	}
	if _, err := s.Lease().Grant(ctx, 1_400_000); err == nil {
		t.Fatal("a lease past the policy's per-run cap was granted")
	} else if r := refusalOf(t, err); r.Reason != "run_cap" {
		t.Fatalf("refusal = %+v", r)
	}
}

func TestObserveNeverRefusesForCaps(t *testing.T) {
	f := newFixture(t)
	f.db.Set(budget.PathMode, "observe")
	f.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: ptr(1_000), PerRunMicros: ptr(1_000)})
	f.db.Set(budget.PathCapsRepo(slug), nil)
	s := f.started(budget.AgentEntry{})
	for i := 0; i < 3; i++ {
		if _, err := s.Lease().Grant(context.Background(), 1_000_000); err != nil {
			t.Fatalf("observe refused for a cap: %v", err)
		}
	}
	if f.run("reserved") != 6_000_000 { // $2 leases: the caps' headroom does not shrink them
		t.Fatalf("observe still accounts: reserved %d", f.run("reserved"))
	}
}

func TestObserveStillHonoursKill(t *testing.T) {
	f := newFixture(t)
	f.db.Set(budget.PathMode, "observe")
	s := f.started(budget.AgentEntry{})
	f.db.Set(budget.PathKillRepo(slug), budget.Kill{On: true, By: "admin@example.invalid", Reason: "runaway"})
	_, err := s.Lease().Grant(context.Background(), 100_000)
	if r := refusalOf(t, err); r.Reason != "kill_switch" || r.Scope != "repo" {
		t.Fatalf("refusal = %+v", r)
	}
}

func TestMidRunKillHaltsThroughTheStream(t *testing.T) {
	f := newFixture(t)
	f.started(budget.AgentEntry{})
	waitFor(t, "the kill streams", func() bool { return f.db.Streams() == 2 })
	f.db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "admin@example.invalid", Reason: "stop it", At: 1})
	h, ok := f.nextHalt(3 * time.Second)
	if !ok || h.Reason != budget.ReasonKillSwitch || h.Scope != budget.ScopeGlobal {
		t.Fatalf("halt = %+v, %v", h, ok)
	}
	if !contains(h.Detail, "admin@example.invalid") || !contains(h.Detail, "stop it") {
		t.Fatalf("detail %q names neither who nor why", h.Detail)
	}
}

// The heartbeat re-reads the switches in case a stream is silently stale.
func TestKillPollIsABackstop(t *testing.T) {
	f := newFixture(t)
	f.cfg.KillRestart = time.Hour
	f.started(budget.AgentEntry{})
	waitFor(t, "the streams", func() bool { return f.db.Streams() == 2 })
	f.db.SendCancel() // the server withdraws both listens; no events follow
	waitFor(t, "the streams to close", func() bool { return f.db.Streams() == 0 })
	f.db.Set(budget.PathKillRepo(slug), budget.Kill{On: true, By: "a"})
	h, ok := f.nextHalt(3 * time.Second)
	if !ok || h.Reason != budget.ReasonKillSwitch || h.Scope != budget.ScopeRepo {
		t.Fatalf("halt = %+v, %v", h, ok)
	}
}

func TestAdmitKillAndNoCap(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fixture)
		reason budget.Reason
		scope  budget.Scope
	}{
		{"global kill", func(f *fixture) { f.db.Set(budget.PathKillGlobal, budget.Kill{On: true}) }, budget.ReasonKillSwitch, budget.ScopeGlobal},
		{"repo kill", func(f *fixture) { f.db.Set(budget.PathKillRepo(slug), budget.Kill{On: true}) }, budget.ReasonKillSwitch, budget.ScopeRepo},
		{"no repo caps", func(f *fixture) { f.db.Set(budget.PathCapsRepo(slug), nil) }, budget.ReasonNoCap, budget.ScopeRepo},
		{"no global caps", func(f *fixture) { f.db.Set(budget.PathCapsGlobal, nil) }, budget.ReasonNoCap, budget.ScopeGlobal},
		{"no max reserve", func(f *fixture) { f.db.Set(budget.PathLimits, nil) }, budget.ReasonNoCap, budget.ScopeGlobal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(f)
			s := f.open()
			h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true})
			if err != nil || h == nil || h.Reason != c.reason || h.Scope != c.scope {
				t.Fatalf("halt = %+v, %v", h, err)
			}
		})
	}
}

// With no cap anywhere but a project that only observes, a run begins;
// oauth is checked for kill switches alone.
func TestAdmitObserveAndOAuth(t *testing.T) {
	f := newFixture(t)
	f.db.Set(budget.PathMode, "observe")
	f.db.Set(budget.PathCapsRepo(slug), nil)
	f.db.Set(budget.PathCapsGlobal, nil)
	if h, err := f.open().Admit(context.Background(), budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatalf("observe with no caps: %+v, %v", h, err)
	}
	f2 := newFixture(t)
	f2.db.Set(budget.PathCapsRepo(slug), nil)
	f2.db.Set(budget.PathCapsGlobal, nil)
	if h, err := f2.open().Admit(context.Background(), budget.AdmitOptions{Gateway: false}); h != nil || err != nil {
		t.Fatalf("oauth with no caps: %+v, %v (oauth is uncapped for dollars)", h, err)
	}
	f2.db.Set(budget.PathKillGlobal, budget.Kill{On: true})
	if h, _ := f2.open().Admit(context.Background(), budget.AdmitOptions{Gateway: false}); h == nil || h.Reason != budget.ReasonKillSwitch {
		t.Fatalf("oauth under a kill switch: %+v", h)
	}
}

func TestStaleWriteRetriesAndSucceeds(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	f.db.DenyNext(3) // three writes lost a race with other runs
	got, err := s.Lease().Grant(context.Background(), 100_000)
	if err != nil || got == 0 {
		t.Fatalf("granted %d, %v", got, err)
	}
	if f.run("reserved") != int64(got) {
		t.Fatalf("reserved %d, want %d (a stale write must not leave half a lease)", f.run("reserved"), got)
	}
}

// TestStaleRetriesThenGrace: a write that is denied although the local
// predicate says it should pass, eight times in a row, is not a refusal. It
// is an unavailable backend, and the grace decides.
func TestStaleRetriesThenGrace(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 150 * time.Millisecond
	s := f.started(budget.AgentEntry{})
	f.db.DenyNext(1000)
	_, err := s.Lease().Grant(context.Background(), 100_000)
	if err == nil {
		t.Fatal("granted despite every write being denied")
	}
	var r *gateway.Refusal
	if errors.As(err, &r) {
		t.Fatalf("stale denials were reported as a refusal: %v", err)
	}
	// Reads still work, so the backend is reachable and the grace does not
	// run (I1): the run keeps its held lease and the agent retries.
	f.db.DenyNext(0)
	if h, ok := f.nextHalt(400 * time.Millisecond); ok {
		t.Fatalf("a reachable backend halted the run: %+v", h)
	}
}

func TestLeaseAcrossMidnight(t *testing.T) {
	f := newFixture(t)
	late := time.Date(2026, 10, 2, 23, 59, 58, 0, time.UTC)
	f.db.SetClock(func() time.Time { return late })
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	d0 := budget.Day(late)
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	if f.share(d0, "reserved") != 1_000_000 {
		t.Fatalf("the lease was not written to day %d", d0)
	}
	// Midnight passes. A lease taken now belongs to the new day; the old one
	// stays where it was granted.
	f.db.SetClock(func() time.Time { return late.Add(5 * time.Second) })
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil { // refreshes the clock from the response
		t.Fatal(err)
	}
	d1 := d0 + 1
	if f.share(d0, "reserved") != 1_000_000 || f.share(d1, "reserved") != 1_000_000 {
		t.Fatalf("shares: day %d %d, day %d %d", d0, f.share(d0, "reserved"), d1, f.share(d1, "reserved"))
	}
	if f.repo(d0, "counted") != 1_000_000 || f.repo(d1, "counted") != 1_000_000 {
		t.Fatalf("counters: %d, %d", f.repo(d0, "counted"), f.repo(d1, "counted"))
	}
	// Usage is spent against the oldest lease first.
	s.SetUsage(func() budget.Micros { return 1_200_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 2, Used: 1_200_000}); err != nil {
		t.Fatal(err)
	}
	if f.share(d0, "spent") != 1_000_000 || f.share(d1, "spent") != 200_000 {
		t.Fatalf("spent by day: %d, %d", f.share(d0, "spent"), f.share(d1, "spent"))
	}
	// Unused money goes back to the day it was granted on: today's lease
	// first, then yesterday's.
	if err := s.Lease().Release(ctx, 800_000); err != nil {
		t.Fatal(err)
	}
	if f.share(d1, "released") != 800_000 || f.repo(d1, "counted") != 200_000 || f.repo(d0, "counted") != 1_000_000 {
		t.Fatalf("release: new day released %d, new counted %d, old counted %d", f.share(d1, "released"), f.repo(d1, "counted"), f.repo(d0, "counted"))
	}
}

// TestReleaseTakesTodaysLeaseFirst: with room on both days, a release goes
// to the newest lease and only the rest to yesterday's.
func TestReleaseTakesTodaysLeaseFirst(t *testing.T) {
	f := newFixture(t)
	late := time.Date(2026, 10, 2, 23, 59, 58, 0, time.UTC)
	f.db.SetClock(func() time.Time { return late })
	s := f.started(budget.AgentEntry{})
	ctx := context.Background()
	d0, d1 := budget.Day(late), budget.Day(late)+1
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	f.db.SetClock(func() time.Time { return late.Add(5 * time.Second) })
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 600_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 600_000}); err != nil {
		t.Fatal(err)
	}
	if err := s.Lease().Release(ctx, 1_100_000); err != nil {
		t.Fatal(err)
	}
	// Today's share holds 1,000,000 unspent: released first. Yesterday's
	// holds 400,000 unspent: it gives back the remaining 100,000.
	if f.share(d1, "released") != 1_000_000 || f.share(d0, "released") != 100_000 {
		t.Fatalf("released: new day %d, old day %d", f.share(d1, "released"), f.share(d0, "released"))
	}
	if f.repo(d1, "counted") != 0 || f.repo(d0, "counted") != 900_000 {
		t.Fatalf("counted: new day %d, old day %d", f.repo(d1, "counted"), f.repo(d0, "counted"))
	}
	if f.run("released") != 1_100_000 {
		t.Fatalf("lifetime released = %d", f.run("released"))
	}
}

func TestGlobalKillRefusesLease(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	f.db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "admin@example.invalid"})
	_, err := s.Lease().Grant(context.Background(), 100_000)
	if r := refusalOf(t, err); r.Reason != "kill_switch" || r.Scope != "global" {
		t.Fatalf("refusal = %+v", r)
	}
	if f.run("reserved") != 0 {
		t.Fatal("a lease was written under a kill switch")
	}
}

// The committed daily cap is the repository's own guardrail: it bites when
// this run's merged mode is enforce even in a project that only observes,
// and is advisory when the run itself only observes.
func TestCommittedDayCapFollowsTheRunsMode(t *testing.T) {
	for _, c := range []struct {
		name         string
		projectMode  string
		localEnforce bool
		refused      bool
	}{
		{"project observes, run enforces", "observe", true, true},
		{"project enforces, run observes", "enforce", false, true}, // rules mode enforce: bites regardless
		{"both observe", "observe", false, false},
		{"project mode unset, run enforces", "", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.db.Set(budget.PathMode, nilIfEmpty(c.projectMode))
			f.cfg.CommittedDaily, f.cfg.LocalEnforce = 1_000_000, c.localEnforce
			s := f.started(budget.AgentEntry{})
			f.db.Set(budget.PathSpendRepo(today(), slug)+"/counted", 900_000)
			_, err := s.Lease().Grant(context.Background(), 200_000)
			if c.refused {
				if r := refusalOf(t, err); r.Reason != "repo_daily_cap" || !strings.Contains(r.Detail, "fugaro.yaml") {
					t.Fatalf("refusal = %+v", r)
				}
			} else if err != nil {
				t.Fatalf("an advisory cap refused: %v", err)
			}
		})
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestOutageStartsTheGraceAndHaltsRun(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 200 * time.Millisecond
	f.started(budget.AgentEntry{})
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	h, ok := f.nextHalt(3 * time.Second)
	if !ok || h.Reason != budget.ReasonBudgetUnavailable || h.Scope != budget.ScopeRun {
		t.Fatalf("halt = %+v, %v", h, ok)
	}
	if !contains(h.Detail, "could not be reached") {
		t.Fatalf("detail = %q", h.Detail)
	}
}

func TestGraceResetsOnSuccess(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 400 * time.Millisecond
	f.started(budget.AgentEntry{})
	for i := 0; i < 3; i++ { // three short outages, each shorter than the grace
		f.db.Refuse(503, "UNAVAILABLE", "", "down")
		time.Sleep(250 * time.Millisecond)
		f.db.Refuse(0, "", "", "")
		time.Sleep(150 * time.Millisecond)
	}
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a recovered backend halted the run: %+v", h)
	}
}

func TestHeldLeaseIsNotTouchedByAnOutage(t *testing.T) {
	// The lease already granted is the run's money: an outage does not take
	// it back, and it is not revoked before the grace runs out.
	f := newFixture(t)
	f.cfg.Grace = 2 * time.Second
	s := f.started(budget.AgentEntry{})
	if _, err := s.Lease().Grant(context.Background(), 1_000_000); err != nil {
		t.Fatal(err)
	}
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	time.Sleep(100 * time.Millisecond)
	if _, ok := f.nextHalt(50 * time.Millisecond); ok {
		t.Fatal("halted inside the grace")
	}
	f.db.Refuse(0, "", "", "")
	if f.run("reserved") != 1_000_000 {
		t.Fatal("the held lease changed")
	}
}

func TestRegistryEntryWrittenAndDeleted(t *testing.T) {
	f := newFixture(t)
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if f.db.Value(budget.PathAgent(slug, runID)) != nil {
		t.Fatal("the entry exists before Start")
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app", Workflow: "default", Title: strings.Repeat("x", 500), Auth: "api-key", Stage: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
	if e == nil || e["requestedBy"] != rb || e["workflow"] != "default" || e["auth"] != "api-key" || e["stage"] != "bootstrap" {
		t.Fatalf("entry = %v", e)
	}
	if got := len(e["title"].(string)); got != 200 {
		t.Fatalf("title is %d bytes, want it clipped to the rules' 200", got)
	}
	if f.run("exp") == 0 {
		t.Fatal("the ledger has no exp")
	}
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Round = "review", 2 })
	waitFor(t, "a heartbeat with the new stage", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "review"
	})
	s.Finish(context.Background(), "succeeded")
	if f.db.Value(budget.PathAgent(slug, runID)) != nil {
		t.Fatal("the entry is still there after Finish")
	}
}

func TestOutcomeWrittenOnce(t *testing.T) {
	f := newFixture(t)
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app"}); err != nil {
		t.Fatal(err)
	}
	s.Finish(context.Background(), "halted")
	out, _ := f.db.Value(budget.PathOutcome(today(), slug, runID)).(map[string]any)
	if out == nil || out["status"] != "halted" || out["requestedBy"] != rb {
		t.Fatalf("outcome = %v", out)
	}
	// A second Finish, and a second session of the same run, do not rewrite it.
	s.Finish(context.Background(), "succeeded")
	f.db.Deny(budget.PathOutcome(today(), slug, runID)) // the rules' !data.exists()
	s2 := f.open()
	s2.Finish(context.Background(), "failed")
	out, _ = f.db.Value(budget.PathOutcome(today(), slug, runID)).(map[string]any)
	if out["status"] != "halted" {
		t.Fatalf("the outcome was rewritten: %v", out)
	}
}

func TestOAuthReportsNotional(t *testing.T) {
	f := newFixture(t)
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: false}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app", Auth: "oauth"}); err != nil {
		t.Fatal(err)
	}
	defer s.Finish(context.Background(), "succeeded")
	d := today()
	err := s.AddNotional(context.Background(), 1_250_000, map[string]budget.ModelUse{"claude-sonnet-5-5": {Micros: 1_000_000, In: 100, Out: 50, CR: 7, CW: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotional(context.Background(), 500_000, nil); err != nil {
		t.Fatal(err)
	}
	if f.run("notional") != 1_750_000 || f.repo(d, "notional") != 1_750_000 || f.global(d, "notional") != 1_750_000 {
		t.Fatalf("notional: run %d, repo %d, global %d", f.run("notional"), f.repo(d, "notional"), f.global(d, "notional"))
	}
	if got := f.num(budget.PathByModel(d, slug, "claude-sonnet-5-5") + "/in"); got != 100 {
		t.Fatalf("byModel in = %d", got)
	}
	// A notional figure is never a lease: nothing is counted.
	if f.repo(d, "counted") != 0 || f.global(d, "counted") != 0 || f.run("reserved") != 0 {
		t.Fatal("an oauth run counted against the caps")
	}
	waitFor(t, "the registry to show the notional", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["spent"] != nil
	})
}

func TestUnavailableNotionalIsKeptAndRetried(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 5 * time.Second
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app", Auth: "oauth"}); err != nil {
		t.Fatal(err)
	}
	defer s.Finish(context.Background(), "succeeded")
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	if err := s.AddNotional(context.Background(), 400_000, nil); err == nil {
		t.Fatal("no error while the backend is down")
	}
	f.db.Refuse(0, "", "", "")
	waitFor(t, "the heartbeat to send the kept figure", func() bool { return f.run("notional") == 400_000 })
}

func TestCrashedRunStaysCounted(t *testing.T) {
	// A run that dies releases nothing: its outstanding amount stays in the
	// shared counters (errs high), for the sweeper to mark.
	f := newFixture(t)
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lease().Grant(context.Background(), 1_000_000); err != nil {
		t.Fatal(err)
	}
	// No Release, no Finish: the process is gone.
	if f.repo(today(), "counted") != 1_000_000 || f.global(today(), "counted") != 1_000_000 {
		t.Fatal("the counters lost the run's lease")
	}
	if f.db.Value(budget.PathAgent(slug, runID)) == nil {
		t.Fatal("the registry entry vanished without Finish")
	}
	s.Finish(context.Background(), "infra_error") // test cleanup only
}

func TestOpenTokenExpired(t *testing.T) {
	f := newFixture(t)
	tok := f.customToken(runID)
	f.itk.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	cfg := f.cfg
	cfg.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	_, err := budget.Open(context.Background(), cfg, tok)
	if err == nil || !errors.Is(err, tokenExpired()) {
		t.Fatalf("err = %v, want an expired token", err)
	}
}

func TestOpenRetriesUntilGrace(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 200 * time.Millisecond
	f.itk.Refuse(503, "UNAVAILABLE", "", "down")
	_, err := budget.Open(context.Background(), f.cfg, f.customToken(runID))
	if !errors.Is(err, budget.ErrGraceExpired) {
		t.Fatalf("err = %v, want the grace to expire", err)
	}
}

func TestOpenRecoversInsideGrace(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 3 * time.Second
	f.itk.Refuse(503, "UNAVAILABLE", "", "down")
	go func() { time.Sleep(150 * time.Millisecond); f.itk.Refuse(0, "", "", "") }()
	s, err := budget.Open(context.Background(), f.cfg, f.customToken(runID))
	if err != nil || s == nil {
		t.Fatalf("Open: %v", err)
	}
	if f.itk.Exchanges() != 1 {
		t.Fatalf("exchanges = %d", f.itk.Exchanges())
	}
}

func TestOpenRejectsUnsafeIdentities(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []struct{ slug, run string }{{"", runID}, {slug, ""}, {"a.b", runID}, {slug, "a/b"}} {
		cfg := f.cfg
		cfg.Slug, cfg.Run = bad.slug, bad.run
		if _, err := budget.Open(context.Background(), cfg, "x.y.z"); err == nil {
			t.Errorf("Open(%q, %q) succeeded; a path builder would panic or alias another node", bad.slug, bad.run)
		}
	}
}

func TestSecretsAreRegisteredBeforeUse(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{})
	_ = s
	reg := f.registered()
	var haveAPIKey, haveCustom bool
	for _, r := range reg {
		haveAPIKey = haveAPIKey || r == apiKey
		haveCustom = haveCustom || strings.Count(r, ".") == 2 && strings.HasPrefix(r, "eyJ") && len(r) > 100
	}
	if !haveAPIKey || !haveCustom || len(reg) < 4 {
		t.Fatalf("registered %d secrets; want the custom token, the API key, an ID token and a refresh token", len(reg))
	}
	// The ID token travels as ?auth=; every request the fake saw carried
	// the run's token and nothing else.
	for _, c := range f.db.Credentials() {
		if !strings.HasPrefix(c, "auth:") {
			t.Fatalf("a request carried %q", c)
		}
	}
}

// A release that could not be made (the backend was down at Close) is tried
// once more when the session finishes; what still cannot go back stays
// counted, which errs high.
func TestFailedReleaseIsRetriedAtFinish(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 10 * time.Second
	s := f.open()
	ctx := context.Background()
	if h, err := s.Admit(ctx, budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(ctx, budget.AgentEntry{Repo: "acme/app"}); err != nil {
		t.Fatal(err)
	}
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	if err := s.Lease().Release(ctx, 400_000); err == nil {
		t.Fatal("a release succeeded against a backend that is down")
	}
	f.db.Refuse(0, "", "", "")
	if f.repo(d, "counted") != 1_000_000 {
		t.Fatal("the failed release changed the counters")
	}
	s.Finish(ctx, "succeeded")
	if f.run("released") != 400_000 || f.repo(d, "counted") != 600_000 || f.global(d, "counted") != 600_000 {
		t.Fatalf("after Finish: released %d, repo counted %d, global counted %d", f.run("released"), f.repo(d, "counted"), f.global(d, "counted"))
	}
}

// A database that refuses the run's credential is a configuration error (the
// wrong database, a project mismatch), not an outage to wait out.
func TestBootstrapPermissionDeniedIsNotAnOutage(t *testing.T) {
	f := newFixture(t)
	s := f.open()
	f.db.Refuse(401, "UNAUTHENTICATED", "", "Permission denied")
	start := time.Now()
	_, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true})
	if !errors.Is(err, budget.ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatalf("a refused credential was waited out for %s", time.Since(start))
	}
}

// I1: a lease or report that failed once must not leave a clock running that
// only another lease or report can stop. After the backend recovers, the
// heartbeat and every other successful call prove it reachable, and a long
// gap before the next model call (verify, an agent that gave up) is no outage.
func TestOneFailedGrantDoesNotHaltAHealthyRun(t *testing.T) {
	f := newFixture(t)
	f.cfg.Grace = 300 * time.Millisecond
	s := f.started(budget.AgentEntry{})
	f.db.Refuse(503, "UNAVAILABLE", "", "blip")
	if _, err := s.Lease().Grant(context.Background(), 100_000); err == nil {
		t.Fatal("granted during an outage")
	}
	if err := s.Lease().Report(context.Background(), gateway.StageReport{Calls: 1}); err == nil {
		t.Fatal("reported during an outage")
	}
	f.db.Refuse(0, "", "", "")
	time.Sleep(900 * time.Millisecond) // three graces of healthy heartbeats
	if h, ok := f.nextHalt(50 * time.Millisecond); ok {
		t.Fatalf("a healthy backend halted the run: %+v", h)
	}
}

// ... and a total outage still halts at the window (TestOutageStartsTheGraceAndHaltsRun).

// I3: the committed per-run cap follows the strictest of the project's mode
// and the run's, like the committed daily cap.
func TestPolicyRunCapFollowsTheStrictestMode(t *testing.T) {
	for _, c := range []struct {
		name         string
		projectMode  string
		localEnforce bool
		refused      bool
	}{
		{"project enforces, run observes", "enforce", false, true},
		{"project observes, run enforces", "observe", true, true},
		{"both observe", "observe", false, false},
		{"both enforce", "enforce", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.db.Set(budget.PathMode, c.projectMode)
			f.cfg.PolicyCap, f.cfg.LocalEnforce = 300_000, c.localEnforce
			s := f.started(budget.AgentEntry{})
			if _, err := s.Lease().Grant(context.Background(), 250_000); err != nil {
				t.Fatal(err)
			}
			_, err := s.Lease().Grant(context.Background(), 250_000)
			if c.refused {
				if r := refusalOf(t, err); r.Reason != "run_cap" {
					t.Fatalf("refusal = %+v", r)
				}
			} else if err != nil {
				t.Fatalf("an advisory cap refused: %v", err)
			}
		})
	}
}

// M1: without limits.maxReserveMicros nothing can be reported, oauth included:
// said at bootstrap, not after three minutes of failing heartbeats.
func TestAdmitNeedsMaxReserveInEveryMode(t *testing.T) {
	for _, gw := range []bool{true, false} {
		f := newFixture(t)
		f.db.Set(budget.PathLimits, nil)
		h, err := f.open().Admit(context.Background(), budget.AdmitOptions{Gateway: gw})
		if err != nil || h == nil || h.Reason != budget.ReasonNoCap || !strings.Contains(h.Detail, "maxReserveMicros") {
			t.Fatalf("gateway=%v: halt = %+v, %v", gw, h, err)
		}
	}
}
