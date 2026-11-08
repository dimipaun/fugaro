package budget

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// leaseNow is a fixed noon; the day counters it names are that day's.
var leaseNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// leaseSlug is the repository the lease fixtures belong to.
const leaseSlug = "aurora-app"

func testCaps(mode string, perRun, repoDaily, globalDaily, maxReserve Micros) Caps {
	caps := Caps{Mode: mode, Limits: &Limits{MaxReserveMicros: &maxReserve}}
	if perRun > 0 {
		caps.Global = &GlobalCaps{PerRunMicros: &perRun}
	}
	if globalDaily > 0 {
		if caps.Global == nil {
			caps.Global = &GlobalCaps{}
		}
		caps.Global.DailyMicros = &globalDaily
	}
	if repoDaily > 0 {
		caps.Repo = &RepoCaps{DailyMicros: &repoDaily}
	}
	return caps
}

func openLease(now time.Time, reserved Micros) BuildLease {
	return BuildLease{
		State:       BuildLeaseOpen,
		Repo:        leaseSlug,
		By:          "alice@example.invalid",
		MachineType: "E2_HIGHCPU_8",
		Region:      "us-central1",
		Reserved:    reserved,
		SKUs:        []string{"SKU-E2-CPU-USC1", "SKU-E2-RAM-USC1"},
		StartedAt:   now.UnixMilli(),
		Exp:         now.Add(time.Hour).UnixMilli(),
	}
}

// TestBuildLeasePathIsItsOwnKeySpace: a build lease never shares a key with a
// run (F22 — every record reader would read it as one), and its slug is
// escaped like every other path segment.
func TestBuildLeasePathIsItsOwnKeySpace(t *testing.T) {
	if got, want := PathBuildLease("aurora-app", "b-1"), "buildleases/aurora-app/b-1"; got != want {
		t.Errorf("PathBuildLease = %q, want %q", got, want)
	}
	if got, want := PathBuildLeases("aurora-app"), "buildleases/aurora-app"; got != want {
		t.Errorf("PathBuildLeases = %q, want %q", got, want)
	}
	for _, p := range []string{PathBuildLease("aurora-app", "b-1"), PathBuildLeases("aurora-app")} {
		if strings.HasPrefix(p, "runs/") || strings.Contains(p, "/runs/") {
			t.Errorf("%q is under the runs family: a build lease would be read as a run", p)
		}
	}
	if got, want := PathBuildLease("a/b", "x.y"), "buildleases/a%2Fb/x%2Ey"; got != want {
		t.Errorf("PathBuildLease escaped = %q, want %q", got, want)
	}
}

// TestBuildLeaseSettlesAndReleases: the lease holds its reservation until the
// build ends. Settling records what the build really spent and gives the rest
// back; releasing gives all of it back. Neither can give back more than the
// lease holds.
func TestBuildLeaseSettlesAndReleases(t *testing.T) {
	l := openLease(leaseNow, 936_000)
	if l.Outstanding() != 936_000 || l.Unsettled() != 936_000 {
		t.Fatalf("a fresh lease holds %d outstanding, %d unsettled", l.Outstanding(), l.Unsettled())
	}
	settled, err := l.Settle(400_000)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != BuildLeaseSettled || settled.Settled != 400_000 || settled.Released != 536_000 {
		t.Errorf("settled lease = %+v, want settled 400000 µ$ and the 536000 µ$ it did not spend released", settled)
	}
	if settled.Outstanding() != 400_000 || settled.Unsettled() != 0 {
		t.Errorf("a settled lease holds %d outstanding, %d unsettled, want what it spent and nothing to give back", settled.Outstanding(), settled.Unsettled())
	}
	if _, err := l.Settle(936_001); err == nil {
		t.Error("a settle larger than the lease holds was accepted")
	}
	if _, err := l.Settle(-1); err == nil {
		t.Error("a negative settle was accepted")
	}

	released, err := l.Release()
	if err != nil {
		t.Fatal(err)
	}
	if released.State != BuildLeaseReleased || released.Released != 936_000 || released.Outstanding() != 0 {
		t.Errorf("released lease = %+v, want the whole reservation given back", released)
	}
	// A lease is settled or released once: settling one that is closed is a
	// caller bug, not a second write.
	if _, err := released.Release(); err == nil {
		t.Error("a released lease was released again")
	}
	if _, err := settled.Settle(1); err == nil {
		t.Error("a settled lease was settled again")
	}
}

// TestAnAbandonedBuildLeaseIsReclaimed: a CLI that dies mid-build leaves an
// open lease behind. Past its deadline the lease is abandoned, and reclaiming
// it gives the whole reservation back — it is never held forever (F19).
func TestAnAbandonedBuildLeaseIsReclaimed(t *testing.T) {
	l := openLease(leaseNow, 936_000)
	if l.Abandoned(leaseNow) {
		t.Error("a lease inside its deadline is abandoned")
	}
	if l.Abandoned(leaseNow.Add(time.Hour)) {
		t.Error("a lease is abandoned at the moment its deadline passes, not after it")
	}
	if !l.Abandoned(leaseNow.Add(time.Hour + time.Second)) {
		t.Error("a lease past its deadline is not abandoned: a build that died would hold its reservation forever")
	}
	reclaimed, err := l.Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.State != BuildLeaseReleased || reclaimed.Released != l.Reserved || reclaimed.Outstanding() != 0 {
		t.Errorf("reclaimed lease = %+v, want a released lease holding nothing", reclaimed)
	}
	// Only an open lease is abandoned: a settled one has already accounted for
	// its spend, and a released one holds nothing to reclaim.
	settled, err := l.Settle(1_000)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Abandoned(leaseNow.Add(48 * time.Hour)) {
		t.Error("a settled lease is reported abandoned: its spend is already counted")
	}
	if _, err := settled.Reclaim(); err == nil {
		t.Error("a settled lease was reclaimed")
	}
	// A lease with no deadline is never reclaimed: the reclaim would be a
	// guess about a build that may still be running.
	noExp := openLease(leaseNow, 1_000)
	noExp.Exp = 0
	if noExp.Abandoned(leaseNow.Add(1000 * time.Hour)) {
		t.Error("a lease with no deadline is abandoned")
	}
}

// TestBuildLeaseCountsAgainstTheRunCaps: the build lease is judged by the same
// predicate the rules are generated from, with the lease's own ledger standing
// in for a run's — so a build is bounded by the per-run cap, by the day
// counters and by the switches exactly as a job is, and in observe mode the
// refusal it would meet under enforce comes back as an advisory instead
// (D1548).
func TestBuildLeaseCountsAgainstTheRunCaps(t *testing.T) {
	l := openLease(leaseNow, 936_000)
	day := Day(leaseNow)
	st := BuildState{
		Repo:   Counters{Counted: 100_000},
		Global: Counters{Counted: 500_000},
	}
	day2 := st
	take := func(caps Caps, kills Kills, s BuildState) Decision {
		return EvaluateBuild(Write{Op: OpLease, Day: day, Amount: l.Reserved}, s, caps, kills, leaseNow)
	}

	// A per-run cap below the estimate refuses, and the refusal is the rules'
	// own: the same reason code a run's lease would get.
	d := take(testCaps(ModeEnforce, 500_000, 10_000_000, 10_000_000, 100_000_000), Kills{}, st)
	if d.Allow || d.Reason != ReasonRunCap || d.Scope != ScopeRun {
		t.Errorf("over the per-run cap: %+v, want run_cap refused", d)
	}
	// A cap between one and two times the estimate is room for the build: the
	// lease's node does not exist yet, so the reservation is counted once, as
	// what this take adds, and never as held as well. Both caps are named
	// because 2_000_000 is just over twice the estimate — the one width at
	// which a doubled count still fits.
	if d := take(testCaps(ModeEnforce, 1_500_000, 10_000_000, 10_000_000, 100_000_000), Kills{}, st); !d.Allow {
		t.Errorf("a cap between one and two times the estimate: %+v, want allowed (the reservation is counted once)", d)
	}
	if d := take(testCaps(ModeEnforce, 2_000_000, 10_000_000, 10_000_000, 100_000_000), Kills{}, st); !d.Allow {
		t.Errorf("under the cap: %+v, want allowed", d)
	}
	// The day counters are the day's, build leases included: what is already
	// counted leaves less room.
	busy := day2
	busy.Repo.Counted = 900_000
	if d := take(testCaps(ModeEnforce, 100_000_000, 1_000_000, 0, 100_000_000), Kills{}, busy); d.Allow || d.Reason != ReasonRepoDailyCap {
		t.Errorf("with the settled builds counted, the lease is over the repository's day cap: %+v", d)
	}
	// The switches bite in every mode, and maxReserve does too.
	killed := take(testCaps(ModeObserve, 0, 0, 0, 100_000_000), Kills{Global: &Kill{On: true, By: "alice@example.invalid"}}, st)
	if killed.Allow || killed.Reason != ReasonKillSwitch {
		t.Errorf("with the global switch on: %+v, want kill_switch", killed)
	}
	small := take(testCaps(ModeObserve, 0, 0, 0, 100_000), Kills{}, st)
	if small.Allow || small.Reason != ReasonMaxReserve {
		t.Errorf("over maxReserve in observe mode: %+v, want max_reserve (it bites in every mode)", small)
	}
	// Observe records and proceeds: the cap refusal comes back as an advisory
	// on an allowed decision, which is what the CLI logs before building.
	observed := take(testCaps(ModeObserve, 500_000, 10_000_000, 10_000_000, 100_000_000), Kills{}, st)
	if !observed.Allow || observed.Advisory == nil || observed.Advisory.Reason != ReasonRunCap {
		t.Errorf("observe mode: %+v, want an allowed lease with the cap refusal as an advisory", observed)
	}
}

// TestReadBuildStateFailsClosed: the lease's judgement reads the same nodes the
// rules do, and an unreadable or missing one is an error or a refusal, never a
// pass.
func TestReadBuildStateFailsClosed(t *testing.T) {
	day := Day(leaseNow)
	perRun, repoDaily, globalDaily := Micros(2_000_000), Micros(5_000_000), Micros(9_000_000)
	maxReserve := Micros(1_000_000)
	nodes := map[string]any{
		PathMode:                      ModeObserve,
		PathCapsGlobal:                GlobalCaps{PerRunMicros: &perRun, DailyMicros: &globalDaily},
		PathCapsRepo(leaseSlug):       RepoCaps{DailyMicros: &repoDaily},
		PathLimits:                    Limits{MaxReserveMicros: &maxReserve},
		PathKillGlobal:                Kill{On: true, By: "alice@example.invalid"},
		PathSpendGlobal(day):          Counters{Counted: 7_000},
		PathSpendRepo(day, leaseSlug): Counters{Counted: 3_000},
	}
	got, err := ReadBuildState(context.Background(), mapReader{nodes: nodes}, leaseSlug, day)
	if err != nil {
		t.Fatal(err)
	}
	if got.Caps.Mode != ModeObserve || !got.Kills.Global.On {
		t.Errorf("read %+v, want the mode and the switch", got)
	}
	if got.State.Global.Counted != 7_000 || got.State.Repo.Counted != 3_000 {
		t.Errorf("counters read as %+v, want the day's nodes", got.State)
	}
	if cap, ok := got.Caps.PerRun(); !ok || cap != 2_000_000 {
		t.Errorf("per-run cap = %d, want the project's", cap)
	}

	// A missing node reads as unset, and the predicate refuses it: enforce
	// with no cap at all is a refusal, not a build that goes ahead.
	empty, err := ReadBuildState(context.Background(), mapReader{nodes: map[string]any{}}, leaseSlug, day)
	if err != nil {
		t.Fatal(err)
	}
	if d := EvaluateBuild(Write{Op: OpLease, Day: day, Amount: 1_000}, empty.State, empty.Caps, empty.Kills, leaseNow); d.Allow || d.Reason != ReasonMaxReserve {
		t.Errorf("a lease with maxReserve unset: %+v, want a refusal", d)
	}
}

// TestReadBuildStateFailsClosedOnABrokenRead: a read error is an error, never
// an empty state a lease would be allowed against.
func TestReadBuildStateFailsClosedOnABrokenRead(t *testing.T) {
	boom := errors.New("the database is unreachable")
	if _, err := ReadBuildState(context.Background(), mapReader{err: boom}, leaseSlug, Day(leaseNow)); !errors.Is(err, boom) {
		t.Fatalf("ReadBuildState = %v, want the read's own failure", err)
	}
}
