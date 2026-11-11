package escalation

import (
	"math"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// Kind is what the findings of the review rounds look like compared with the
// rounds before them (spec.md "Churn detection"). The comparator that produces
// it reads the finding keys, files and classes of the item's own run records
// (E9); the decision only reads the answer, and never re-derives it, so a run
// whose records cannot be compared is KindNone — the normal path, not a guess.
type Kind string

const (
	// KindNone is no churn, and also a comparison that could not be made.
	KindNone Kind = "none"
	// KindSame is the same unresolved finding round after round.
	KindSame Kind = "same"
	// KindNewMinor is a new minor finding every round: the reviewer is
	// churning rather than converging.
	KindNewMinor Kind = "new_minor"
)

// Kinds is the whole vocabulary. It is closed: a kind outside it refuses the
// decision rather than being read as "no churn", which would be the fail-open
// direction for a value this code cannot place (G5).
var Kinds = []Kind{KindNone, KindSame, KindNewMinor}

// KnownKind reports whether k is one of Kinds. The zero Kind — a caller that
// never asked the comparator — is not.
func KnownKind(k Kind) bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

// Reason is why the ladder entered the rung it answers with. It is a closed
// code and not a sentence: the ledger records it verbatim and the digest
// counts it (E7, E12, G8), so it holds no path, no finding text and no model.
// Reasons is the whole vocabulary, and KnownReason is what a writer asks
// before it stores one.
type Reason string

const (
	// ReasonAge is a time threshold: rung 2's own hours or the hard ceiling.
	ReasonAge Reason = "age"
	// ReasonAttempts is rung 2's count of the item's normal runs.
	ReasonAttempts Reason = "attempts"
	// ReasonCost is the optional cost ceiling (D2038, G9).
	ReasonCost Reason = "cost"
	// ReasonChurnNew is a reviewer raising new minor findings every round,
	// which the spec sends straight to rung 3 "whatever the clock says".
	ReasonChurnNew Reason = "churn_new"
	// ReasonChurnSame is the same unresolved finding repeating: rung 2 once,
	// then D1246's split (D2036).
	ReasonChurnSame Reason = "churn_same"
	// ReasonHotfix is Hotfix's own row: no runs before rung 2 and the
	// strongest grade, because the mode is production on fire (spec.md).
	ReasonHotfix Reason = "hotfix"
	// ReasonCriticalPath is a time threshold that fired only because the item
	// blocks other work and the factor halved it (D2032, F15).
	ReasonCriticalPath Reason = "critical_path"
	// ReasonRebase is rung 0: the item is stalled at a head its rebase has
	// not run at yet, and the cheap step comes before any escalation
	// (spec.md "Tier 0").
	ReasonRebase Reason = "rebase"
	// ReasonR2Clean is the merge after the one rung 2 run, whose review left
	// nothing blocking: the spec's "If the review is clean, merge"
	// (spec.md "Tier 2", F17).
	ReasonR2Clean Reason = "r2_clean"
	// ReasonR2Findings is rung 3 straight after the one rung 2 run, whose
	// review still leaves blocking findings (spec.md "Tier 3", D2036).
	ReasonR2Findings Reason = "r2_findings"
)

// Reasons is the whole vocabulary: the seven the plan names, then the three
// the plan describes without naming a code — the cheap step, and what the one
// rung 2 run left behind. It is closed, and in the order above so the two
// groups stay readable: a reason outside it is refused where it is written
// (KnownReason), never stored as a string nothing can place (E7, G8).
var Reasons = []Reason{
	ReasonAge, ReasonAttempts, ReasonCost,
	ReasonChurnNew, ReasonChurnSame, ReasonHotfix, ReasonCriticalPath,
	ReasonRebase, ReasonR2Clean, ReasonR2Findings,
}

// KnownReason reports whether r is one of Reasons. The zero Reason — a
// transition with no cause recorded — is not.
func KnownReason(r Reason) bool { return slices.Contains(Reasons, r) }

// Step is the ladder's answer: the rung to enter, and what that rung runs at.
//
// The zero Step is the refusal Refuse names: no rung, and the item is left to
// a person. It is the only answer this code gives for facts or thresholds it
// cannot read, and it is never a default (G5).
type Step struct {
	// Rung is the rung to enter. It is empty when Refuse is set.
	Rung Rung
	// Grade is the model grade the rung runs at, and only rung 2 has one: a
	// rung 0, 1, 3 or 4 runs at the item's own configuration.
	Grade Grade
	// AtCeiling is what rung 3 does in this mode — merge, merge and notify,
	// or wait for a person — and only rung 3 reads it.
	AtCeiling CeilingAction
	// Refuse says the ladder will not decide: a mode it has no row for, a
	// grade no layer defines, a kind it cannot place, a threshold that cannot
	// be read. The daemon asks a person rather than escalating on a guess.
	Refuse bool
}

// Facts is what the ladder knows about one item: what the run records say
// about its age, its attempts and its cost, what the state says about the
// rungs it has already entered, and the two things neither can answer — the
// verdict it is waiting on, and the head it is looking at.
//
// It is gathered by the daemon each tick (E10a) and is deliberately flat: a
// fact the ladder needs is a field here, so the decision stays a pure function
// of what a caller can build in a test.
type Facts struct {
	// Mode is the item's rigor mode, already resolved by the layers that may
	// set it (rigor.Resolve). A mode with no threshold row refuses.
	Mode rigor.Mode
	// Elapsed is the item's wall clock: the chain's elapsed time where it has
	// one, else the time since its first run record (D2038, F7, F18).
	Elapsed time.Duration
	// Attempts is how many normal runs the item has spent. Ladder runs do not
	// count: ApprovalAttempts counts normal runs only (D2042, G15).
	Attempts int
	// CostUSD is the sum of the item's own run records, the chain's figure
	// where a chain exists, never a second meter (D2038, G9).
	CostUSD float64
	// BlocksOthers is the critical-path flag: another approved, non-terminal
	// item names this one in its DependsOn. It is computed each tick and
	// never typed in, because an unapproved item an agent added must not
	// halve a pull request's thresholds (F15, G7).
	BlocksOthers bool
	// Churn is what the last rounds' findings look like (E9). KindNone is
	// both "no churn" and "could not compare"; the zero Kind is a caller that
	// never asked, and it refuses rather than reading as no churn.
	Churn Kind
	// HeadSHA is the head the ladder is looking at, and the key rung 0 is
	// spent by. An item whose head cannot be read refuses: without it the
	// cheap step has no way to be spent once.
	HeadSHA string
	// LastReviewBlocking is whether the item's last completed review left
	// blocking findings. It is what tells an item whose one rung 2 run
	// converged from one whose run came back with work still to do.
	LastReviewBlocking bool
	// Reached is the highest rung the item has entered, from the ledger
	// (E7). The zero Rung is an item that has entered none.
	Reached Rung
	// RebasedHeads holds every head an R0 run has produced, oldest first: a
	// head in it has already had its rebase, so the ladder does not rebase it
	// again, and MaxRebases of them spent rung 0 for the item (D2042, G4).
	RebasedHeads []string
	// R2Runs is how many rung 2 runs the item has spent. At most one ever
	// runs; a second is what the same finding repeating answers with R4.
	R2Runs int
}

// Decide answers which rung the item enters next, and why.
//
// The order below is the rule, and each step of it is one the spec states:
//
//   - A fact or a threshold this code cannot read refuses, and the item is
//     left to a person (G5). Nothing here has a default: an unset grade, a
//     mode with no row, a threshold that is not a finite number and a
//     threshold of zero are all the fail-open direction if they are answered,
//     so they are not answered.
//   - A split stands. Rung 4 is where D1246 leaves an item whose spec could
//     not be salvaged, and the ladder never relaunches the same spec.
//   - Churn comes before the clock, "whatever the clock says": new minor
//     findings every round go straight to rung 3, and the same unresolved
//     finding is one rung 2 and then the split (D2036). It comes before the
//     cheap step too, because a rebase does not answer a finding the reviewer
//     keeps raising.
//   - What the one rung 2 run left behind is read next, before the clock, the
//     mode and the cheap step. A review that still blocks is the spec's
//     "straight after a Tier 2 run that still has findings"; a review that
//     left nothing blocking is the spec's merge, and it comes at once (F17)
//     because the item has nothing left to escalate — a rebase instead would
//     push a head the verdict no longer covers (G2, G13) and un-converge an
//     item the ladder is done with.
//   - The cost ceiling is the person's own stop on spend (D2038, G9), so it
//     is read before the cheap step: a rung 0 run is a run. The hard ceiling
//     is the mode's stop (D2030, D2037) and is read after it: the spec puts
//     the cheap step "before any escalation", and an item already older than
//     its ceiling when the daemon first ticks it is exactly the stalled pull
//     request the rebase is for.
//   - Hotfix's row is read whole, before the cheap step: the mode is
//     production on fire, so it takes neither the rebase ("straight to Tier
//     2") nor a wait past its own ceiling, the shortest of the table.
//   - Rung 2's own thresholds: the count of normal runs first, then its hours
//     — an item that has spent its runs is answering to what it did, not to
//     the clock (spec.md "past a time or attempt threshold").
//   - Anything else is today's behaviour, rung 1.
func Decide(f Facts, th Thresholds) (Step, Reason) {
	if !readable(f, th) {
		return Step{Refuse: true}, ""
	}
	row := th.Rows[f.Mode]

	if f.Reached == R4 {
		return Step{Rung: R4}, ""
	}

	switch f.Churn {
	case KindNewMinor:
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonChurnNew
	case KindSame:
		if f.R2Runs == 0 {
			return Step{Rung: R2, Grade: r2Grade(f, row)}, ReasonChurnSame
		}
		return Step{Rung: R4}, ReasonChurnSame
	}

	if f.R2Runs > 0 {
		if f.LastReviewBlocking {
			return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonR2Findings
		}
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonR2Clean
	}

	if th.CostCeiling > 0 && f.CostUSD >= th.CostCeiling {
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonCost
	}

	if f.Mode == rigor.Hotfix {
		if f.Elapsed >= clock(f, row.CeilingHours, th) {
			return Step{Rung: R3, AtCeiling: row.AtCeiling}, timeReason(f, row.CeilingHours)
		}
		return Step{Rung: R2, Grade: r2Grade(f, row)}, ReasonHotfix
	}

	if rebaseAvailable(f) {
		return Step{Rung: R0}, ReasonRebase
	}

	if f.Elapsed >= clock(f, row.CeilingHours, th) {
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, timeReason(f, row.CeilingHours)
	}

	if f.Attempts >= row.RunsBeforeR2 {
		return Step{Rung: R2, Grade: r2Grade(f, row)}, ReasonAttempts
	}
	if row.R2Hours > 0 && f.Elapsed >= clock(f, row.R2Hours, th) {
		return Step{Rung: R2, Grade: r2Grade(f, row)}, timeReason(f, row.R2Hours)
	}

	return Step{Rung: R1}, ""
}

// maxHours is the largest number of hours a row may name and still be read as
// a time: clock multiplies by time.Hour, and past this the conversion
// overflows into a negative duration, which would read every item as long past
// its ceiling. It is about 292 years, so no threshold a person writes comes
// near it, and a row that does is a row this code cannot read.
const maxHours = float64(math.MaxInt64 / time.Hour)

// readable reports whether the facts and the thresholds hold everything the
// decision reads.
//
// Every refusal here is one shape of the fail-closed rule (G5): a mode the
// rigor vocabulary does not know or a table with no row for it, a row whose
// count, hours, grade or action cannot be read — including one whose rung 2
// arrives no sooner than its ceiling, which would silently drop a rung — a
// critical-path factor that is no scale at all (zero escalates on a clock
// nobody set, above one lets a critical-path item wait longer than any other),
// a kind or a rung outside the vocabulary, and a head the ladder cannot spend
// the cheap step by.
//
// The two shapes a comparison would let through are the reason each numeric
// guard is written the way it is: NaN fails every comparison, so a "factor <=
// 0 || factor > 1" would read a NaN factor as a scale, and a NaN threshold
// silently never fires. Non-finite and out-of-range values are refused
// instead, because a threshold nobody can read is a threshold nobody set.
func readable(f Facts, th Thresholds) bool {
	row, ok := th.Rows[f.Mode]
	if !rigor.Known(f.Mode) || !ok {
		return false
	}
	if !finite(row.R2Hours) || !finite(row.CeilingHours) || row.RunsBeforeR2 < 0 || row.R2Hours < 0 || row.CeilingHours <= 0 {
		return false
	}
	if row.R2Hours >= row.CeilingHours || row.CeilingHours > maxHours {
		return false
	}
	if !KnownGrade(row.Grade) || !KnownCeilingAction(row.AtCeiling) {
		return false
	}
	if !(th.CriticalPathFactor > 0 && th.CriticalPathFactor <= 1) {
		return false
	}
	if !finite(th.CostCeiling) || th.CostCeiling < 0 {
		return false
	}
	if !KnownKind(f.Churn) {
		return false
	}
	if f.Reached != "" && !KnownRung(f.Reached) {
		return false
	}
	if f.HeadSHA == "" {
		return false
	}
	return f.Elapsed >= 0 && f.Attempts >= 0 && f.CostUSD >= 0 && f.R2Runs >= 0
}

// finite reports whether a threshold is a number a person could have set: NaN
// and the infinities are the shapes that pass a range test written the other
// way round, and the shapes no JSON config can hold.
func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// clock is a threshold in hours read at the factor: an item that blocks other
// work has every time halved, and every other item has none of them scaled
// (spec.md "Urgency modifiers", D2032).
func clock(f Facts, hours float64, th Thresholds) time.Duration {
	if !f.BlocksOthers {
		return time.Duration(hours * float64(time.Hour))
	}
	return time.Duration(hours * th.CriticalPathFactor * float64(time.Hour))
}

// timeReason names a transition the clock caused. A threshold only the halved
// clock reached is the critical path's doing and says so, so the ledger and
// the digest show why an item escalated before its mode's own clock (F15).
func timeReason(f Facts, hours float64) Reason {
	if f.BlocksOthers && f.Elapsed < time.Duration(hours*float64(time.Hour)) {
		return ReasonCriticalPath
	}
	return ReasonAge
}

// r2Grade is the grade rung 2 runs at: the row's own, raised to the strongest
// for an item that blocks other work — the same flag that halves its times
// (spec.md "Urgency modifiers").
func r2Grade(f Facts, row Row) Grade {
	if f.BlocksOthers {
		return Strongest
	}
	return row.Grade
}

// rebaseAvailable reports whether the cheap step is still the item's to take:
// once per head, because a rebase run pushes a head of its own, and at most
// MaxRebases times per item, which is what ends the loop a new head would
// otherwise reopen (D2042, G4).
func rebaseAvailable(f Facts) bool {
	if len(f.RebasedHeads) >= MaxRebases {
		return false
	}
	for _, head := range f.RebasedHeads {
		if head == f.HeadSHA {
			return false
		}
	}
	return true
}
