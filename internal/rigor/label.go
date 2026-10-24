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
	// High is a finding above the floor of every mode that has one: Startup,
	// Pro and Critical all block it, and only POC and Hotfix, which block
	// nothing but the catastrophic, tolerate it.
	High Label = "high"
	// Medium is the band Startup's floor starts at: the mildest finding that
	// mode blocks, and one Pro and Critical block as well.
	Medium Label = "medium"
	// Low is the band Pro's floor starts at: the mildest finding the default
	// mode blocks, and the only band its fix-cost escape can reach — the
	// escape's likelihood of 1 caps it at a risk of 4.
	Low Label = "low"
	// Nit is below every floor there is: POC, Hotfix, Startup and Pro all
	// tolerate it, and Critical, whose floor is the bottom of the scale,
	// blocks it with everything else.
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
// Two of the bands' lower edges are floors of the table read back (table.go):
// the low band starts where Pro's floor is and the medium band where
// Startup's is, so moving either floor is a change here too — that coupling is
// what makes the two files one system rather than two opinions about the same
// score, and what TestTheLabelAndTheTableAgree holds.
//
// The other three bands start at no floor, and the five of them are not five
// modes' thresholds: the blocker band is the spec's override, which no threshold
// reaches however low a mode sets one; the high band is the risk above
// Startup's floor, which no floor starts because the mode above Startup needs
// none — Critical blocks everything, so its floor is the bottom of the scale
// rather than a rung higher up; and a nit is below every floor there is. So a
// band is where a floor happens to fall, never the reason a floor is where it
// is: an editor moving a floor to "match" a band has the relation backwards.
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
