package rigor

import "slices"

// The five words (D2): blocker, high, medium, low, nit. They are a label
// derived from the axes, never an input to a decision — the table reads the
// three axes and the class (table.go) — so a finding cannot be labelled `low`
// and scored 5×5, and the vocabulary stays one this repository defines rather
// than a word a model chose freely.
//
// A label is what a person reads: the pull request body names each finding a
// mode left open with its label (T3.3), and `fugaro diagnose` prints it. It is
// not stored on the finding — `severity` stays the free-form string it is
// (D3), and the label is computed from the score, so the two cannot drift.

// Label is one of the five words.
type Label string

// The five words, most serious first.
const (
	// Blocker is a finding no mode tolerates: the catastrophic class or the
	// top of the impact scale, and — fail closed, like the table — a finding
	// whose score cannot be read at all (D4).
	Blocker Label = "blocker"
	// High is a finding a mode that fixes what is common and material blocks
	// on, and a mode that weighs the fix cost may still leave open.
	High Label = "high"
	// Medium is a finding on the boundary of Startup's floor: the worst thing
	// that mode tolerates.
	Medium Label = "medium"
	// Low is a finding on the boundary of Pro's floor: what the default mode
	// fixes unless the fix is expensive.
	Low Label = "low"
	// Nit is a finding every on-axis mode's floor is above.
	Nit Label = "nit"
)

// Labels is the whole vocabulary, most serious first.
var Labels = []Label{Blocker, High, Medium, Low, Nit}

// KnownLabel reports whether l is one of Labels. The zero Label is not.
func KnownLabel(l Label) bool { return slices.Contains(Labels, l) }

// Label is the word a person reads for a finding: the severity of the score
// itself, which is the same word whatever mode the finding met, because the
// mode decides what happens to a finding and not how bad it is.
//
// The bands are the table's floors read back (table.go): a finding this calls
// a nit is one Startup's and Pro's floors are both above, one it calls a
// blocker is one no mode tolerates, and each of the three words between them
// is where one of the modes' floors starts. That is what makes the two files
// one system rather than two opinions about the same score.
func (s Score) Label() Label {
	switch {
	case !s.Scored() || s.catastrophic():
		return Blocker
	case s.Risk() >= 12:
		return High
	case s.Risk() >= 8:
		return Medium
	case s.Risk() >= 4:
		return Low
	default:
		return Nit
	}
}
