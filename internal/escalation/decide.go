package escalation

import (
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
	// ReasonR2Findings is rung 3 straight after the one rung 2 run, whose
	// review still leaves blocking findings (spec.md "Tier 3", D2036).
	ReasonR2Findings Reason = "r2_findings"
)

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
//     mode with no row and a threshold of zero are all the fail-open direction
//     if they are answered, so they are not answered.
//   - A split stands. Rung 4 is where D1246 leaves an item whose spec could
//     not be salvaged, and the ladder never relaunches the same spec.
//   - Churn comes before the clock, "whatever the clock says": new minor
//     findings every round go straight to rung 3, and the same unresolved
//     finding is one rung 2 and then the split (D2036).
//   - The one rung 2 run, once spent, decides on what it left behind: a review
//     that still blocks ends the escalation at once — the spec's "straight
//     after a Tier 2 run that still has findings" — while a review that left
//     nothing blocking is a converged item, which is not an escalation at all
//     and falls through to the ceilings and then to today's behaviour.
//   - The cost ceiling triggers rung 3 the way the hard ceiling does (D2038,
//     G9). It is asked first of the two because it is the person's own stop,
//     where the clock's is the mode's.
//   - The hard ceiling is the mode's stop: at or past it comes rung 3 with
//     the action the row names (D2030, D2037). It is what merges the item
//     that converged long ago and was never merged (F17).
//   - Hotfix's row is the mode's own rule: no runs before rung 2, and no stop
//     for the cheap step on the way.
//   - The cheap step is what an item below every threshold gets first, at a
//     head it has not had it at, at most MaxRebases times (D2042, G4).
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

	if f.R2Runs > 0 && f.LastReviewBlocking {
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonR2Findings
	}

	if th.CostCeiling > 0 && f.CostUSD >= th.CostCeiling {
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, ReasonCost
	}

	if f.Elapsed >= clock(f, row.CeilingHours, th) {
		return Step{Rung: R3, AtCeiling: row.AtCeiling}, timeReason(f, row.CeilingHours)
	}

	// The one escalation pass is spent and its review left nothing blocking:
	// the item converged, so there is no rung above it to enter and no second
	// pass to launch. It resumes on the normal path below, and is deliberately
	// not read as "merge" here — the daemon asks the ladder about stalled
	// items, and a clean review is not a stall; what does merge a converged
	// item is the ceiling above, where it has waited long enough to be worth
	// the judge.
	if f.R2Runs > 0 {
		return Step{Rung: R1}, ""
	}

	if f.Mode == rigor.Hotfix {
		return Step{Rung: R2, Grade: r2Grade(f, row)}, ReasonHotfix
	}

	if rebaseAvailable(f) {
		return Step{Rung: R0}, ReasonRebase
	}

	if f.Attempts >= row.RunsBeforeR2 {
		return Step{Rung: R2, Grade: r2Grade(f, row)}, ReasonAttempts
	}
	if row.R2Hours > 0 && f.Elapsed >= clock(f, row.R2Hours, th) {
		return Step{Rung: R2, Grade: r2Grade(f, row)}, timeReason(f, row.R2Hours)
	}

	return Step{Rung: R1}, ""
}

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
func readable(f Facts, th Thresholds) bool {
	row, ok := th.Rows[f.Mode]
	if !rigor.Known(f.Mode) || !ok {
		return false
	}
	if row.RunsBeforeR2 < 0 || row.R2Hours < 0 || row.CeilingHours <= 0 {
		return false
	}
	if row.R2Hours >= row.CeilingHours {
		return false
	}
	if !KnownGrade(row.Grade) || !KnownCeilingAction(row.AtCeiling) {
		return false
	}
	if th.CriticalPathFactor <= 0 || th.CriticalPathFactor > 1 || th.CostCeiling < 0 {
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
