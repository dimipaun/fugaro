package escalation

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// minorScore is a finding both modes the comparator reads tolerate: a risk of
// 1, below Startup's floor of 8 and Pro's of 4, and nothing the spec's
// severity override covers. Whether it is minor is the modes' own tables'
// answer and never a number written here.
func minorScore() rigor.Score {
	return rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic}
}

// blockingScore is a finding every mode blocks on: an impact at the top of the
// scale is the spec's severity override, whatever the axes say (D4).
func blockingScore() rigor.Score {
	return rigor.Score{Likelihood: 1, Impact: 5, FixCost: 1, Class: rigor.Correctness}
}

// unscored is a finding whose class is outside rigor's vocabulary — a shape
// the reviewer's own schema permits (D3). It is the unscored finding of D4:
// there is no score the table can read, so every mode blocks on it.
func unscored() rigor.Score {
	return rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Class("nitpick")}
}

// TestChurnSameFinding: the same finding raised round after round is "same" —
// the spec's "the same unresolved issue repeating", which D2036 sends to rung
// 2 once and then to D1246's split.
func TestChurnSameFinding(t *testing.T) {
	f := Finding{Key: "9f2a1c", File: "internal/runner/runner.go", Score: minorScore()}
	rounds := []Round{{f}, {f}, {f}}
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		if got := Churn(mode, rounds); got != KindSame {
			t.Errorf("Churn(%s, the same finding in every round) = %q; want %q", mode, got, KindSame)
		}
	}
}

// TestChurnNewMinorEveryRound: a new minor finding every round is the churn
// the spec sends straight to rung 3 "whatever the clock says". Each round
// names a file of its own, so the fallback identity does not match the rounds
// either and the findings are new on both readings.
func TestChurnNewMinorEveryRound(t *testing.T) {
	rounds := []Round{
		{{Key: "k1", File: "internal/runner/a.go", Score: minorScore()}},
		{{Key: "k2", File: "internal/runner/b.go", Score: minorScore()}},
		{{Key: "k3", File: "internal/runner/c.go", Score: minorScore()}},
	}
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		if got := Churn(mode, rounds); got != KindNewMinor {
			t.Errorf("Churn(%s, a new minor finding every round) = %q; want %q", mode, got, KindNewMinor)
		}
	}
}

// TestChurnNewBlockingIsNone: a new finding the mode blocks on is a reviewer
// doing its job, not a reviewer churning. The spec's rule is about new *minor*
// findings, and "minor" is rigor.Blocks' answer for the mode (F11) — so a
// round of new findings the mode blocks on is the normal path.
func TestChurnNewBlockingIsNone(t *testing.T) {
	rounds := []Round{
		{{Key: "k1", File: "internal/runner/a.go", Score: blockingScore()}},
		{{Key: "k2", File: "internal/runner/b.go", Score: blockingScore()}},
	}
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		if got := Churn(mode, rounds); got != KindNone {
			t.Errorf("Churn(%s, a new blocking finding every round) = %q; want %q", mode, got, KindNone)
		}
	}
}

// TestChurnBlockingFirstRoundIsNone: the spec's rule is that *each* round
// raises new minor findings, and the first round is one of them. An item whose
// first review raised something the mode blocks on is a reviewer that found
// something real, so the new minor findings after it are not churn — the first
// round is a baseline for identity, because there is nothing before it to be
// the same as, and not for severity. An unscored finding is one of these: it
// carries no score the table can read, so it blocks in every mode (D4).
func TestChurnBlockingFirstRoundIsNone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first rigor.Score
	}{
		{"a finding the mode blocks on", blockingScore()},
		{"an unscored finding, which blocks in every mode", rigor.Score{}},
	} {
		rounds := []Round{
			{{Key: "k1", File: "internal/runner/a.go", Score: tc.first}},
			{{Key: "k2", File: "internal/runner/b.go", Score: minorScore()}},
			{{Key: "k3", File: "internal/runner/c.go", Score: minorScore()}},
		}
		for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
			if got := Churn(mode, rounds); got != KindNone {
				t.Errorf("Churn(%s, a first round with %s) = %q; want %q", mode, tc.name, got, KindNone)
			}
		}
	}
}

// TestChurnMixedIsSame: a round that raises a new finding beside a repeat is
// "same" and not "new minor". The repeat is what the ladder acts on, and it
// wins wherever it falls in the rounds — the last round here, after two rounds
// that read as churn on their own.
func TestChurnMixedIsSame(t *testing.T) {
	rounds := []Round{
		{{Key: "k1", File: "internal/runner/a.go", Score: minorScore()}},
		{{Key: "k2", File: "internal/runner/b.go", Score: minorScore()}},
		{{Key: "k3", File: "internal/runner/c.go", Score: minorScore()},
			{Key: "k1", File: "internal/runner/a.go", Score: minorScore()}},
	}
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		if got := Churn(mode, rounds); got != KindSame {
			t.Errorf("Churn(%s, a repeat beside new findings) = %q; want %q", mode, got, KindSame)
		}
	}
}

// TestChurnNoKeyIsNone: the key is the finding's identity and the triage stage
// fills it in Startup and Pro only (F11, D1850), so a round carrying none — the
// shadow stage, a record written before it — is one this comparator cannot
// compare. Every shape of it answers the normal path rather than a guess,
// including the two the other rules would otherwise answer: new minor findings
// every round, and the same file and class in both.
func TestChurnNoKeyIsNone(t *testing.T) {
	const file = "internal/runner/runner.go"
	for _, tc := range []struct {
		name   string
		rounds []Round
	}{
		{"new minor findings every round", []Round{
			{{File: "internal/runner/a.go", Score: minorScore()}},
			{{File: "internal/runner/b.go", Score: minorScore()}},
		}},
		{"the same file and class every round", []Round{
			{{File: file, Score: minorScore()}},
			{{File: file, Score: minorScore()}},
		}},
		{"one round keyed and one not", []Round{
			{{Key: "k1", File: file, Score: minorScore()}},
			{{File: file, Score: minorScore()}},
		}},
	} {
		if got := Churn(rigor.Pro, tc.rounds); got != KindNone {
			t.Errorf("Churn(Pro, %s, with no key) = %q; want %q", tc.name, got, KindNone)
		}
	}
}

// TestChurnRewordedSameFileClassIsSame: the key is a hash of the file and the
// summary, so a reviewer that rewords a finding between rounds changes it.
// D1246's rule is the fallback — the same file and class in two rounds is the
// same finding — and it is what keeps a reworded finding from reading as a new
// minor one (F11).
func TestChurnRewordedSameFileClassIsSame(t *testing.T) {
	const file = "internal/runner/runner.go"
	rounds := []Round{
		{{Key: "9f2a1c", File: file, Score: minorScore()}},
		{{Key: "1b7d40", File: file, Score: minorScore()}},
	}
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		if got := Churn(mode, rounds); got != KindSame {
			t.Errorf("Churn(%s, the same finding reworded) = %q; want %q", mode, got, KindSame)
		}
	}
}

// TestChurnFallbackNeedsARealFileAndClass: D1246's fallback is "the same file
// and class", and neither half is something a finding that has none can
// supply. A finding the reviewer named no file for sits nowhere, and one whose
// class is not in rigor's vocabulary carries no class at all — it is unscored
// (rigor.Score.Scored) — so two of either are not one finding raised twice,
// and the rounds are read on their keys alone. Both shapes are ones the
// reviewer's own schema permits, so a guard here is a guard on records this
// code really reads.
func TestChurnFallbackNeedsARealFileAndClass(t *testing.T) {
	const file = "internal/runner/runner.go"
	for _, tc := range []struct {
		name   string
		rounds []Round
		want   Kind
	}{
		{
			// Two findings with no file and the same class sit in no place at
			// all: matching them would be the fail-open direction, and these
			// are new minor findings.
			"no file",
			[]Round{
				{{Key: "k1", Score: minorScore()}},
				{{Key: "k2", Score: minorScore()}},
			},
			KindNewMinor,
		},
		{
			// An out-of-vocabulary class is no class, so "the same file and
			// class" is not something two of these can be — and the finding
			// blocks, so the answer is the normal path.
			"a class outside the vocabulary",
			[]Round{
				{{Key: "k1", File: file, Score: unscored()}},
				{{Key: "k2", File: file, Score: unscored()}},
			},
			KindNone,
		},
	} {
		if got := Churn(rigor.Pro, tc.rounds); got != tc.want {
			t.Errorf("Churn(Pro, keyed findings with %s) = %q; want %q", tc.name, got, tc.want)
		}
	}
}

// TestChurnOffInPOCCriticalHotfix: the comparator is off in the three modes
// whose runs do not fill the key, whatever the rounds look like. It is a fact
// about the records and not a preference: a comparison made without the key
// would be a guess about which findings are the same, and both of this
// comparator's answers spend a run.
func TestChurnOffInPOCCriticalHotfix(t *testing.T) {
	rounds := []Round{
		{{Key: "k1", File: "internal/runner/a.go", Score: minorScore()}},
		{{Key: "k2", File: "internal/runner/b.go", Score: minorScore()}},
	}
	for _, mode := range []rigor.Mode{rigor.POC, rigor.Critical, rigor.Hotfix} {
		if got := Churn(mode, rounds); got != KindNone {
			t.Errorf("Churn(%s, a new minor finding every round) = %q; want %q: the key is filled in Startup and Pro only", mode, got, KindNone)
		}
	}
	// A mode outside the vocabulary is off for the same reason and not by a
	// second rule: it is not one the key is written in.
	if got := Churn(rigor.Mode(""), rounds); got != KindNone {
		t.Errorf("Churn(the zero mode, a new minor finding every round) = %q; want %q", got, KindNone)
	}
}

// TestChurnNothingToCompareIsNone: the spec's rule compares rounds, so there
// has to be something to compare. One round, and a round the reviewer raised
// nothing in, both answer the normal path: an empty round raised nothing, and
// a round that raised nothing is not a round that raised something new.
func TestChurnNothingToCompareIsNone(t *testing.T) {
	keyed := Finding{Key: "k1", File: "internal/runner/a.go", Score: minorScore()}
	for _, tc := range []struct {
		name   string
		rounds []Round
	}{
		{"no rounds", nil},
		{"one round", []Round{{keyed}}},
		{"an empty first round", []Round{{}, {keyed}}},
		{"an empty later round", []Round{{keyed}, {}}},
	} {
		if got := Churn(rigor.Pro, tc.rounds); got != KindNone {
			t.Errorf("Churn(Pro, %s) = %q; want %q", tc.name, got, KindNone)
		}
	}
}
