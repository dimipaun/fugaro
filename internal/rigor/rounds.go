package rigor

// The loop counts each mode runs by default: the inner (first-line) rounds and
// the senior rounds a run of that mode spends when nothing else sets them
// (the owner's ruling of 2026-10-31, which answers O3 —
// docs/rigor-command/reconciliation.md §4).
//
// They are **defaults, not caps**. The number a run's review step actually
// runs comes from the precedence first — the task's Overrides.ReviewRounds,
// else the recipe's max_rounds, else the repository's agent.review_rounds,
// else the project layer's — and the mode's row supplies the number only where
// none of the four set one, up or down. A task override of one round is one
// round at Critical too, and a repository's `agent.review_rounds: 8` is eight
// at POC; the recipe's own bounds (recipe.MaxFirstLineRounds,
// recipe.MaxReviewRounds) are the only ceiling either number is held under.
//
// That is why the names say "default" and not "ceiling": the table this
// replaced (roundCeilings, whose rows only ever lowered the precedence) meant
// something else, and a name never changes meaning.

// Rounds is one mode's loop counts: Inner is the first-line review's rounds,
// Senior the senior review's.
type Rounds struct {
	// Inner is the rounds of the first-line loop — review level 1, the
	// coder's own model (agent.first_line_rounds).
	Inner int
	// Senior is the rounds of the senior review and its fixes — review level
	// 2, the reviewer's model (agent.review_rounds).
	Senior int
}

// roundDefaults is the table, one row per mode, on the axis the modes sit on:
// the stricter the mode, the more rounds it spends by default. Hotfix is off
// that axis (spec.md) and takes the cheapest row, the same one POC takes: it
// is the run with the least room to spend, and the ruling puts it beside POC.
//
// Critical's row is the whole scale, which is what keeps a Critical run the
// run main produced where a repository left the knobs alone (plan §5).
var roundDefaults = map[Mode]Rounds{
	POC:      {Inner: 1, Senior: 1},
	Startup:  {Inner: 2, Senior: 2},
	Pro:      {Inner: 3, Senior: 3},
	Critical: {Inner: 3, Senior: 5},
	Hotfix:   {Inner: 1, Senior: 1},
}

// DefaultRounds is the loop counts mode m supplies when no other layer, the
// recipe or the task set either number.
//
// A mode the table does not hold — the zero Mode, a name outside the
// vocabulary — takes the strictest row there is, the way table.go blocks
// everything for a mode it cannot place: a value this code cannot read must
// never be the lenient answer. The resolution path only ever answers a known
// mode (Resolve checks Known before it reads a layer or a request), so this is
// the guard behind it rather than a run's behaviour, and the rows it can reach
// are inside the recipe's bounds either way.
func DefaultRounds(m Mode) Rounds {
	if r, ok := roundDefaults[m]; ok {
		return r
	}
	return roundDefaults[Critical]
}

// AdversarialDefault is the rounds the adversarial step — review level 3 —
// supplies for mode m when nothing else sets them (D726, L2.2).
//
// It is the mode's own senior row: the ruling gives the modes' loop counts two
// cells, and the adversarial pass is a review pass on the same axis, run after
// the senior one on the same budget, so the number a mode starts from is the
// one it already spends on the review it follows. The step's own max_rounds and
// the layer key the senior loop reads (agent.review_rounds, and the project
// layer's default for it) still set another number, up or down, as they do
// there: this is a default and not a ceiling, and the recipe's bounds are what
// holds it. An agent.adversarial_rounds key does not exist yet — L1.3 of
// docs/review-levels-ab-jobmax/implementation-plan.md is where it would land —
// so no key of the step's own is read here.
//
// A mode the table cannot place takes DefaultRounds' answer for it, which is
// the strictest row — an unplaced mode raises nothing and drops nothing.
func AdversarialDefault(m Mode) int {
	return DefaultRounds(m).Senior
}
