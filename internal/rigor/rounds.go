package rigor

// The review-round ceiling each mode puts on a run's fix rounds (D7b,
// docs/rigor-modes/implementation-plan.md T3.2). It is the mode's own number,
// held beside the threshold table (table.go) so the run loop reads one table
// rather than a condition inside itself.
//
// The ceiling only ever lowers. The review step's rounds come from F6's
// precedence first — the task's Overrides.ReviewRounds, else the recipe's
// max_rounds, else the repository's agent.review_rounds — and the ceiling is spent
// under that answer, never above, so no mode buys a run more rounds than its
// own configuration asked for.
//
// POC and Hotfix would each spend a single round, the two modes the spec puts
// cheapest and fastest. Whether a mode may go below today's default of two is
// the owner's open question O3 (docs/rigor-modes/reconciliation.md), and until
// it is answered the table keeps that number and minRoundCeiling clamps it —
// here, so the answer is one edit in one place.

// minRoundCeiling is O3's floor: no mode spends fewer than today's default of
// two rounds while the owner's question stands.
const minRoundCeiling = 2

// roundCeilings is the table, one row per mode, on the axis the modes sit on:
// the cheaper the mode, the fewer the rounds it will spend. Critical's row is
// the whole scale — widestRoundCeiling — which is what keeps a Critical run the
// run main produced (plan §5). A mode the table does not hold — the zero Mode,
// a name outside the vocabulary — takes widestRoundCeiling too, the way
// table.go blocks everything for a mode it cannot place: a value this code
// cannot read must never be the lenient answer.
var roundCeilings = map[Mode]int{
	POC:      1,
	Startup:  2,
	Pro:      3,
	Critical: widestRoundCeiling,
	Hotfix:   1,
}

// widestRoundCeiling is the most review rounds a step may carry at all, and
// what RoundCeiling answers for a mode it cannot place: recipe.MaxReviewRounds,
// pinned to it by this package's TestRoundCeilingRowsAreWithinTheRecipeBound
// and TestRoundCeilingNeverLowersABelowTheRecipeBound, so a mode can never
// reach above the recipe's own bound.
const widestRoundCeiling = 10

// RoundCeiling is the most review rounds mode m allows: the table's number,
// held under O3's floor (minRoundCeiling) and inside the recipe's bound
// (widestRoundCeiling). A row below the first would be a mode spending fewer
// rounds than today's default before the owner has answered O3; one above the
// second would be a mode raising what a recipe may carry.
func RoundCeiling(m Mode) int {
	n, ok := roundCeilings[m]
	if !ok {
		return widestRoundCeiling
	}
	return min(max(n, minRoundCeiling), widestRoundCeiling)
}

// AdversarialCeiling is the most rounds the adversarial step — review level 3 —
// may carry for mode m (D726, L2.2).
//
// It is held at minRoundCeiling for every mode while rigor O3 is open: the
// adversarial pass runs after the review, on the same budget, and the owner's
// question is the same one the review's own floor answers, so a mode cannot
// spend on it what no mode may spend on the review. It is stated as the
// minimum of the two rather than as the constant so that the day O3 is
// answered above, a Critical run takes its own review ceiling and this
// function follows the table instead of drifting from it. A mode the table
// cannot place takes RoundCeiling's widest answer, which the minimum lowers
// like any other — an unplaced mode raises nothing either.
func AdversarialCeiling(m Mode) int {
	return min(RoundCeiling(m), minRoundCeiling)
}
