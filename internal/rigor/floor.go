package rigor

// Floor reports whether a finding blocks at mode m for a reason the triage
// judge may not move: an unscored finding, or one the mode's own reading of
// the spec's severity override blocks (D1840, F3).
//
// It is the table's floor named once rather than a second rule beside it: the
// two reasons below are Blocks' own first two, read in its order and off the
// same rows, so Floor(m, s) is true only where Blocks(m, s) is — the judge may
// only *add* deferrals among findings the table blocks — and every finding the
// override reads is floor. A mode the table does not hold blocks on everything
// (D4's fail-closed reading of a value this code cannot place), so it is floor
// on everything: the leniency such a value must never get is not the judge's
// to hand out either.
//
// What it leaves outside is the part of a mode's rule a judge may weigh: the
// risk axis and the escape each row states beside it — Startup's, Pro's and
// Critical's rows are where those live, and where triage's own reach begins
// (D1857). Hotfix has the narrowest floor of the five, because its row reads
// the override as the security gate alone (F28): a data_loss finding and an
// impact of 5 are tolerated there by the table itself, so they are not floor
// and a judge is not what keeps them open.
func Floor(m Mode, s Score) bool {
	row, known := thresholds[m]
	if !known {
		return true
	}
	return !s.Scored() || row.Override.blocks(s)
}
