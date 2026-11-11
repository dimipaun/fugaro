package escalation

import (
	"fmt"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// specTable is spec.md's "Default thresholds per rigor mode" written out a
// second time, on purpose: the table test holds the numbers the ladder ships
// with independently of the code that ships them, so a row that drifts from
// the spec fails here rather than in the daemon.
var specTable = map[rigor.Mode]struct {
	runs    int
	hours   float64
	grade   Grade
	ceiling float64
	action  CeilingAction
}{
	rigor.POC:      {runs: 1, hours: 3, grade: Strong, ceiling: 5, action: CeilingAutoMerge},
	rigor.Startup:  {runs: 2, hours: 6, grade: Strong, ceiling: 9, action: CeilingAutoMerge},
	rigor.Pro:      {runs: 3, hours: 9, grade: Strong, ceiling: 12, action: CeilingAutoMergeNotify},
	rigor.Critical: {runs: 3, hours: 9, grade: Strongest, ceiling: 12, action: CeilingWaitPerson},
	rigor.Hotfix:   {runs: 0, hours: 0, grade: Strongest, ceiling: 3, action: CeilingAutoMergeNotify},
}

func atHours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

// settled is the facts of a stalled item whose rebase already ran at the head
// under judgement: a case sets the clock, the counts and the rest. The churn
// kind is the comparator's answer and a case names it too, because the zero
// Kind is a caller that never asked and refuses the decision.
func settled(m rigor.Mode) Facts {
	return Facts{Mode: m, Churn: KindNone, HeadSHA: "head-1", RebasedHeads: []string{"head-1"}}
}

// young is a stalled item the cheap step has not run for yet, at a head of its
// own.
func young(m rigor.Mode) Facts {
	return Facts{Mode: m, Churn: KindNone, HeadSHA: "head-2", RebasedHeads: []string{"head-1"}}
}

// on returns settled's facts with one field changed.
func on(m rigor.Mode, edit func(*Facts)) Facts {
	f := settled(m)
	edit(&f)
	return f
}

// clockReason is what the test expects of a transition the clock caused: a
// time threshold only the halved clock reached is the critical path's doing.
func clockReason(criticalPath bool) Reason {
	if criticalPath {
		return ReasonCriticalPath
	}
	return ReasonAge
}

// TestDecideTable is the spec's table per mode: every mode, with and without
// the critical-path factor, at the points its own row names.
func TestDecideTable(t *testing.T) {
	th := DefaultThresholds()
	for _, mode := range rigor.Modes {
		row := specTable[mode]
		hotfix := mode == rigor.Hotfix
		for _, cp := range []bool{false, true} {
			scale, path := 1.0, ""
			if cp {
				scale, path = th.CriticalPathFactor, "/CriticalPath"
			}
			grade := row.grade
			if cp {
				grade = Strongest
			}
			// straight is the Hotfix reading of a case below the ceiling: the
			// row is "straight to Tier 2", so its answer comes from the mode
			// and not from the threshold the case is about.
			straight := func(s Step, r Reason) (Step, Reason) {
				if hotfix {
					return Step{Rung: R2, Grade: Strongest}, ReasonHotfix
				}
				return s, r
			}
			cases := []struct {
				name       string
				facts      Facts
				want       Step
				wantReason Reason
				// atCeiling marks the case the mode's own ceiling answers:
				// Hotfix's row is asked after it, so this one is not read
				// through straight.
				atCeiling bool
			}{
				{
					name:       "YoungStalledRebases",
					facts:      young(mode),
					want:       Step{Rung: R0},
					wantReason: ReasonRebase,
				},
				{
					name:       "RebaseSpentResumes",
					facts:      settled(mode),
					want:       Step{Rung: R1},
					wantReason: "",
				},
				{
					name: "AtR2Hours",
					facts: on(mode, func(f *Facts) {
						f.Elapsed = atHours(row.hours * scale)
					}),
					want:       Step{Rung: R2, Grade: grade},
					wantReason: clockReason(cp),
				},
				{
					name: "AtR2Attempts",
					facts: on(mode, func(f *Facts) {
						f.Attempts = row.runs
					}),
					want:       Step{Rung: R2, Grade: grade},
					wantReason: ReasonAttempts,
				},
				{
					name: "AtCeiling",
					facts: on(mode, func(f *Facts) {
						f.Elapsed = atHours(row.ceiling * scale)
					}),
					want:       Step{Rung: R3, AtCeiling: row.action},
					wantReason: clockReason(cp),
					atCeiling:  true,
				},
			}
			// Every case of the critical-path pass carries the flag: it is
			// what the halved clock and the strongest grade are read from.
			for i := range cases {
				cases[i].facts.BlocksOthers = cp
			}
			for _, tc := range cases {
				if !tc.atCeiling {
					tc.want, tc.wantReason = straight(tc.want, tc.wantReason)
				}
				t.Run(string(mode)+path+"/"+tc.name, func(t *testing.T) {
					got, reason := Decide(tc.facts, th)
					if got != tc.want || reason != tc.wantReason {
						t.Fatalf("Decide(%+v) = %+v, %q; want %+v, %q", tc.facts, got, reason, tc.want, tc.wantReason)
					}
				})
			}
		}
	}
}

// TestDecideCostCeiling: the optional ceiling over the item's own run records
// triggers rung 3 the way the hard ceiling does, and only when a person set
// one (D2038, G9).
func TestDecideCostCeiling(t *testing.T) {
	th := DefaultThresholds()
	th.CostCeiling = 40

	over := on(rigor.Pro, func(f *Facts) { f.CostUSD = 40 })
	under := on(rigor.Pro, func(f *Facts) { f.CostUSD = 39.99 })
	if got, reason := Decide(over, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonCost {
		t.Errorf("Decide at the ceiling = %+v, %q; want rung 3, %q", got, reason, ReasonCost)
	}
	if got, reason := Decide(under, th); got.Rung != R1 || reason != "" {
		t.Errorf("Decide under the ceiling = %+v, %q; want rung 1", got, reason)
	}

	// The cost ceiling is not a time: the critical-path factor never scales
	// it, so a half-priced critical-path item still reports cost.
	expensive := on(rigor.Pro, func(f *Facts) { f.CostUSD = 40; f.BlocksOthers = true })
	if _, reason := Decide(expensive, th); reason != ReasonCost {
		t.Errorf("reason = %q; want %q", reason, ReasonCost)
	}

	th.CostCeiling = 0
	if got, _ := Decide(over, th); got.Rung != R1 {
		t.Errorf("rung with no ceiling = %q; want %q", got.Rung, R1)
	}

	// The ceiling triggers whatever the mode's clock says, including inside a
	// mode whose rung 2 is immediate.
	hot := on(rigor.Hotfix, func(f *Facts) { f.CostUSD = 40 })
	if got, reason := Decide(hot, th); got.Rung != R2 || reason != ReasonHotfix {
		t.Errorf("hotfix with no ceiling = %+v, %q; want rung 2", got, reason)
	}
	th.CostCeiling = 40
	if got, reason := Decide(hot, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonCost {
		t.Errorf("hotfix at the cost ceiling = %+v, %q; want rung 3, %q", got, reason, ReasonCost)
	}
}

// TestDecideChurnNewGoesToR3: a reviewer raising new minor findings every round
// escalates to rung 3 whatever the clock says (spec.md "Churn detection"), and
// the cheap step is not asked about first.
func TestDecideChurnNewGoesToR3(t *testing.T) {
	th := DefaultThresholds()
	burnt := young(rigor.Pro)
	burnt.Churn = KindNewMinor
	got, reason := Decide(burnt, th)
	if got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonChurnNew {
		t.Fatalf("Decide(%+v) = %+v, %q; want rung 3, %q", burnt, got, reason, ReasonChurnNew)
	}
	// A critical-path item's churn reaches rung 3 too, and rung 3 is where the
	// mode decides who is told (D2030).
	path := burnt
	path.BlocksOthers = true
	if got, _ := Decide(path, th); got.AtCeiling != CeilingAutoMergeNotify {
		t.Errorf("at ceiling = %q; want %q", got.AtCeiling, CeilingAutoMergeNotify)
	}
}

// TestDecideSameFindingR2ThenR4: the same unresolved finding repeating is one
// rung 2 run, and D1246's split when it repeats again (D2036).
func TestDecideSameFindingR2ThenR4(t *testing.T) {
	th := DefaultThresholds()
	first := on(rigor.Startup, func(f *Facts) { f.Churn = KindSame })
	got, reason := Decide(first, th)
	if got != (Step{Rung: R2, Grade: Strong}) || reason != ReasonChurnSame {
		t.Fatalf("first = %+v, %q; want rung 2, %q", got, reason, ReasonChurnSame)
	}
	again := first
	again.R2Runs = 1
	if got, reason := Decide(again, th); got != (Step{Rung: R4}) || reason != ReasonChurnSame {
		t.Fatalf("again = %+v, %q; want rung 4, %q", got, reason, ReasonChurnSame)
	}
	// A critical-path item runs its one rung 2 at the strongest grade.
	path := first
	path.BlocksOthers = true
	if got, _ := Decide(path, th); got.Grade != Strongest {
		t.Errorf("grade = %q; want %q", got.Grade, Strongest)
	}
}

// TestDecideAfterTheOnePass: what the single rung 2 run left behind is what
// the ladder reads next (spec.md "Tier 2": "If the review is clean, merge. If
// it finds issues, go to Tier 3 instead of looping").
func TestDecideAfterTheOnePass(t *testing.T) {
	th := DefaultThresholds()
	pass := func(edit func(*Facts)) Facts {
		return on(rigor.Pro, func(f *Facts) {
			f.R2Runs = 1
			edit(f)
		})
	}
	blocked := pass(func(f *Facts) { f.LastReviewBlocking = true })
	if got, reason := Decide(blocked, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonR2Findings {
		t.Errorf("blocked = %+v, %q; want rung 3, %q", got, reason, ReasonR2Findings)
	}

	clean := pass(func(f *Facts) { f.Elapsed = time.Hour })
	if got, reason := Decide(clean, th); got != (Step{Rung: R1}) || reason != "" {
		t.Errorf("clean = %+v, %q; want rung 1", got, reason)
	}
	// A converged item that has waited out the ceiling is the one rung 3
	// exists for (F17), and the clock that says so is the mode's own.
	waited := pass(func(f *Facts) { f.Elapsed = atHours(12) })
	if got, reason := Decide(waited, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonAge {
		t.Errorf("waited = %+v, %q; want rung 3, %q", got, reason, ReasonAge)
	}
	path := pass(func(f *Facts) { f.Elapsed = atHours(6.5); f.BlocksOthers = true })
	if got, reason := Decide(path, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonCriticalPath {
		t.Errorf("critical path = %+v, %q; want rung 3, %q", got, reason, ReasonCriticalPath)
	}

	// Whatever it left behind, the pass is never launched a second time.
	for _, f := range []Facts{blocked, clean, waited, path} {
		for attempts := 0; attempts < 6; attempts++ {
			f.Attempts = attempts
			if got, _ := Decide(f, th); got.Rung == R2 {
				t.Errorf("Decide(%+v) = rung 2; the pass is spent", f)
			}
		}
	}
}

// TestDecideHotfixStraightToR2: Hotfix spends no run before rung 2, takes the
// strongest grade, and does not stop for the cheap step (spec.md).
func TestDecideHotfixStraightToR2(t *testing.T) {
	th := DefaultThresholds()
	burnt := young(rigor.Hotfix)
	got, reason := Decide(burnt, th)
	if got != (Step{Rung: R2, Grade: Strongest}) || reason != ReasonHotfix {
		t.Fatalf("Decide(%+v) = %+v, %q; want rung 2 at %q, %q", burnt, got, reason, Strongest, ReasonHotfix)
	}
	// Its own ceiling still wins: three hours is production on fire's hard
	// stop, and past it the answer is rung 3 with the mode's notification.
	late := on(rigor.Hotfix, func(f *Facts) { f.Elapsed = atHours(3) })
	if got, reason := Decide(late, th); got != (Step{Rung: R3, AtCeiling: CeilingAutoMergeNotify}) || reason != ReasonAge {
		t.Fatalf("Decide(%+v) = %+v, %q; want rung 3, %q", late, got, reason, ReasonAge)
	}
}

// TestDecideUnknownModeRefuses: a fact or a threshold this code cannot place
// refuses and asks, never a default (G5).
func TestDecideUnknownModeRefuses(t *testing.T) {
	th := DefaultThresholds()
	edit := func(f func(*Thresholds)) Thresholds {
		out := DefaultThresholds()
		f(&out)
		return out
	}
	editRow := func(m rigor.Mode, f func(*Row)) Thresholds {
		out := DefaultThresholds()
		row := out.Rows[m]
		f(&row)
		out.Rows[m] = row
		return out
	}
	cases := []struct {
		name string
		f    Facts
		th   Thresholds
	}{
		{name: "NoMode", f: settled(""), th: th},
		{name: "UnknownMode", f: settled("prod"), th: th},
		{name: "ModeWithoutRow", f: settled(rigor.Pro), th: edit(func(t *Thresholds) { delete(t.Rows, rigor.Pro) })},
		{name: "NoThresholds", f: settled(rigor.Pro), th: Thresholds{}},
		{name: "GradeUnset", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.Grade = "" })},
		{name: "GradeUnknown", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.Grade = "opus" })},
		{name: "CeilingUnset", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.CeilingHours = 0 })},
		{name: "RunsNegative", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.RunsBeforeR2 = -1 })},
		{name: "HoursNegative", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.R2Hours = -1 })},
		{name: "R2AtTheCeiling", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.R2Hours = r.CeilingHours })},
		{name: "ActionUnknown", f: settled(rigor.Pro), th: editRow(rigor.Pro, func(r *Row) { r.AtCeiling = "merge" })},
		{name: "FactorZero", f: settled(rigor.Pro), th: edit(func(t *Thresholds) { t.CriticalPathFactor = 0 })},
		{name: "FactorAboveOne", f: settled(rigor.Pro), th: edit(func(t *Thresholds) { t.CriticalPathFactor = 1.5 })},
		{name: "CostNegative", f: settled(rigor.Pro), th: edit(func(t *Thresholds) { t.CostCeiling = -1 })},
		{name: "ChurnUnset", f: func() Facts {
			f := settled(rigor.Pro)
			f.Churn = ""
			return f
		}(), th: th},
		{name: "ChurnUnknown", f: on(rigor.Pro, func(f *Facts) { f.Churn = "churning" }), th: th},
		{name: "HeadMissing", f: on(rigor.Pro, func(f *Facts) { f.HeadSHA = "" }), th: th},
		{name: "ReachedUnknown", f: on(rigor.Pro, func(f *Facts) { f.Reached = "R9" }), th: th},
		{name: "ElapsedNegative", f: on(rigor.Pro, func(f *Facts) { f.Elapsed = -time.Hour }), th: th},
		{name: "AttemptsNegative", f: on(rigor.Pro, func(f *Facts) { f.Attempts = -1 }), th: th},
		{name: "CostFactNegative", f: on(rigor.Pro, func(f *Facts) { f.CostUSD = -1 }), th: th},
		{name: "R2RunsNegative", f: on(rigor.Pro, func(f *Facts) { f.R2Runs = -1 }), th: th},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := Decide(tc.f, tc.th)
			if !got.Refuse || got.Rung != "" || reason != "" {
				t.Fatalf("Decide(%+v, %+v) = %+v, %q; want a refusal", tc.f, tc.th, got, reason)
			}
		})
	}
}

// TestDecideNeverLoops: the ladder's bounds hold however its own answers are
// fed back, and a walk ends in a fixed point rather than in a rung it has
// already spent (G4, D1246).
func TestDecideNeverLoops(t *testing.T) {
	th := DefaultThresholds()
	for _, mode := range rigor.Modes {
		// The walk is what a ticking daemon does: it enters a rung, the run
		// that rung launched ends (a new head, a spent attempt, an hour
		// later), and the ladder is asked again.
		f := Facts{Mode: mode, Churn: KindNone, HeadSHA: "head-0"}
		var rebases, r2s, head int
		last := Rung("")
		// pushed is what a run of the rung just entered leaves behind: a new
		// head of its own, which is what spends rung 0 once per head.
		pushed := func() {
			head++
			f.HeadSHA = fmt.Sprintf("head-%d", head)
		}
		for tick := 0; tick < 32; tick++ {
			step, _ := Decide(f, th)
			if step.Refuse {
				t.Fatalf("%s: refused %+v", mode, f)
			}
			if (last == R3 || last == R4) && step.Rung != last {
				t.Fatalf("%s: left the fixed point %q for %q", mode, last, step.Rung)
			}
			switch step.Rung {
			case R0:
				rebases++
				if rebases > MaxRebases {
					t.Fatalf("%s: rung 0 entered %d times", mode, rebases)
				}
				pushed()
				f.RebasedHeads = append(f.RebasedHeads, f.HeadSHA)
			case R1:
				f.Attempts++
				pushed()
			case R2:
				r2s++
				if r2s > 1 {
					t.Fatalf("%s: rung 2 entered %d times", mode, r2s)
				}
				f.R2Runs++
				f.LastReviewBlocking = true
				pushed()
			case R3, R4:
			default:
				t.Fatalf("%s: unknown rung %q", mode, step.Rung)
			}
			if step.Rung.Rank() > f.Reached.Rank() {
				f.Reached = step.Rung
			}
			last = step.Rung
			f.Elapsed += time.Hour
		}
		if last != R3 {
			t.Fatalf("%s: the walk ended at %q; want rung 3", mode, last)
		}
	}
	// Rung 4 is absorbing: a split never relaunches the same spec, so the
	// ladder stays there however the clock moves.
	split := on(rigor.Pro, func(f *Facts) {
		f.Reached = R4
		f.Elapsed = atHours(48)
		f.Churn = KindNewMinor
	})
	if got, _ := Decide(split, th); got.Rung != R4 {
		t.Errorf("after a split = %+v; want rung 4", got)
	}
}
