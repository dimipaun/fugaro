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
	// both "no churn" and "could not compare".
	Churn Kind
	// HeadSHA is the head the ladder is looking at, and the key rung 0 is
	// spent by.
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

// Decide is the ladder's decision, and it is not implemented yet: Slice A
// lands the vocabulary, its thresholds and this signature.
func Decide(f Facts, th Thresholds) (Step, Reason) {
	return Step{Rung: R1}, ""
}
