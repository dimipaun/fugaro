package escalation

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// tolerated is a finding both modes whose triage stage fills a key tolerate: a
// risk of 2 (likelihood 1 × impact 2) is under Startup's floor of 8 and Pro's
// of 4, and correctness is not a class the severity override covers. It is the
// comparator's "minor".
func tolerated(key, file string) Finding {
	return Finding{Key: key, File: file, Score: rigor.Score{Likelihood: 1, Impact: 2, FixCost: 1, Class: rigor.Correctness}}
}

// held is a finding both of those modes block: a risk of 8 sits on Startup's
// floor and well past Pro's.
func held(key, file string) Finding {
	return Finding{Key: key, File: file, Score: rigor.Score{Likelihood: 4, Impact: 2, FixCost: 1, Class: rigor.Correctness}}
}

// round is one review round of one mode with the findings given.
func round(m rigor.Mode, findings ...Finding) Round {
	return Round{Mode: m, Findings: findings}
}

// TestChurnSameFinding: the same unresolved finding round after round is
// KindSame, which the ladder answers with rung 2 and then D1246's split
// (D2036).
func TestChurnSameFinding(t *testing.T) {
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
	}); got != KindSame {
		t.Errorf("the same key in the next round = %q; want %q", got, KindSame)
	}

	// The key is what carries the finding, not the score: a re-review that
	// scores the one it keeps raising differently is still the same unresolved
	// issue, and it is the key the triage stage wrote that says so (D1850).
	if got := Churn([]Round{
		round(rigor.Startup, held("k-1", "internal/a.go")),
		round(rigor.Startup, tolerated("k-1", "internal/a.go")),
	}); got != KindSame {
		t.Errorf("the same key re-scored = %q; want %q", got, KindSame)
	}

	// It need not repeat in consecutive rounds: D1246's rule is the same
	// finding in two or more of the record's reviews, and a round that raised
	// something else in between does not make the issue new.
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-2", "internal/b.go")),
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
	}); got != KindSame {
		t.Errorf("the key coming back two rounds later = %q; want %q", got, KindSame)
	}
}

// TestChurnNewMinorEveryRound: a reviewer raising a new, minor finding every
// round is churning, and the ladder takes that straight to rung 3 (spec.md
// "Churn detection").
func TestChurnNewMinorEveryRound(t *testing.T) {
	churning := []Round{
		round(rigor.Startup, tolerated("k-1", "internal/a.go")),
		round(rigor.Startup, tolerated("k-2", "internal/b.go")),
		round(rigor.Startup, tolerated("k-3", "internal/c.go")),
	}
	if got := Churn(churning); got != KindNewMinor {
		t.Fatalf("three rounds of new minor findings = %q; want %q", got, KindNewMinor)
	}

	// Two rounds are enough: what the spec compares is one round with the ones
	// before it, and there is one before the second.
	if got := Churn(churning[:2]); got != KindNewMinor {
		t.Errorf("two rounds of new minor findings = %q; want %q", got, KindNewMinor)
	}

	// "Minor" is rigor.Blocks' answer for the mode and not a scale of its own:
	// the same two rounds churn in Startup, where the mode tolerates the
	// finding, and are the normal path in Pro, where it blocks it.
	four := []Round{
		round(rigor.Startup, Finding{Key: "k-1", File: "internal/a.go", Score: rigor.Score{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness}}),
		round(rigor.Startup, Finding{Key: "k-2", File: "internal/b.go", Score: rigor.Score{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness}}),
	}
	if got := Churn(four); got != KindNewMinor {
		t.Errorf("risk 4 in Startup = %q; want %q", got, KindNewMinor)
	}
	for i := range four {
		four[i].Mode = rigor.Pro
	}
	if got := Churn(four); got != KindNone {
		t.Errorf("risk 4 in Pro = %q; want %q", got, KindNone)
	}

	// A finding the mode blocks is a reviewer doing its job, not churn: the
	// work is real and the ladder's normal path is where it belongs.
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, held("k-2", "internal/b.go")),
	}); got != KindNone {
		t.Errorf("a new blocking finding = %q; want %q", got, KindNone)
	}

	// A round that raised nothing is a round the reviewer converged, so the
	// rounds after the first are not all raising new findings and this is not
	// a pattern: KindNone is the normal path (G5).
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro),
	}); got != KindNone {
		t.Errorf("a round that raised nothing = %q; want %q", got, KindNone)
	}

	// One round has nothing to be compared with, and an item reviewed once has
	// not churned.
	if got := Churn(churning[:1]); got != KindNone {
		t.Errorf("one round = %q; want %q", got, KindNone)
	}
}

// TestChurnMixedIsSame: a round raising the unresolved finding again beside a
// new one is the spec's "same unresolved issue". Answering new-minor there
// would take the item to rung 3 while its rung 2 had not run (D2036).
func TestChurnMixedIsSame(t *testing.T) {
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-1", "internal/a.go"), tolerated("k-2", "internal/b.go")),
	}); got != KindSame {
		t.Errorf("a repeat beside a new finding = %q; want %q", got, KindSame)
	}

	// The order inside the round is the reviewer's and not a fact, so the new
	// finding first is the same round.
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go"), tolerated("k-2", "internal/b.go")),
		round(rigor.Pro, tolerated("k-2", "internal/b.go"), held("k-1", "internal/a.go")),
	}); got != KindSame {
		t.Errorf("the new finding first = %q; want %q", got, KindSame)
	}

	// A match on the file and the class is a repeat too, so a reworded
	// repeat beside a new minor finding is the same case (D1246).
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-9", "internal/a.go"), tolerated("k-2", "internal/b.go")),
	}); got != KindSame {
		t.Errorf("a reworded repeat beside a new finding = %q; want %q", got, KindSame)
	}
}

// TestChurnRewordedSameFileClassIsSame: the key the triage stage writes is a
// hash of the finding's file and summary, so a reviewer that rewords the
// finding it keeps raising gives it a new one. D1246's rule catches it — the
// same file and class in two rounds is the same finding — and is the reason the
// comparator matches on either.
func TestChurnRewordedSameFileClassIsSame(t *testing.T) {
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-2", "internal/a.go")),
	}); got != KindSame {
		t.Errorf("the same file and class under a new key = %q; want %q", got, KindSame)
	}

	// The class is half of that rule: the same file with another class is
	// another finding, and — both of them minor — the rounds are churn.
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, Finding{Key: "k-2", File: "internal/a.go", Score: rigor.Score{Likelihood: 1, Impact: 2, FixCost: 1, Class: rigor.Cosmetic}}),
	}); got != KindNewMinor {
		t.Errorf("the same file under another class = %q; want %q", got, KindNewMinor)
	}

	// And the file is the other half: another file with the same class is
	// another finding.
	if got := Churn([]Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-2", "internal/b.go")),
	}); got != KindNewMinor {
		t.Errorf("another file under the same class = %q; want %q", got, KindNewMinor)
	}
}

// TestChurnNoKeyIsNone: a finding carries no key only when the triage stage
// did not run or could not place it, and file-and-class alone would be a guess
// at the identity. The comparison is off and the ladder falls back to its
// normal path (F11).
func TestChurnNoKeyIsNone(t *testing.T) {
	keyed := []Round{
		round(rigor.Pro, tolerated("k-1", "internal/a.go")),
		round(rigor.Pro, tolerated("k-2", "internal/b.go")),
	}
	if got := Churn(keyed); got != KindNewMinor {
		t.Fatalf("the same rounds with keys = %q; want %q", got, KindNewMinor)
	}

	for name, rounds := range map[string][]Round{
		"a finding with no key": {
			round(rigor.Pro, tolerated("k-1", "internal/a.go")),
			round(rigor.Pro, tolerated("", "internal/b.go")),
		},
		"a keyless baseline": {
			round(rigor.Pro, tolerated("", "internal/a.go")),
			round(rigor.Pro, tolerated("k-2", "internal/b.go")),
		},
		"a keyless repeat": {
			round(rigor.Pro, tolerated("k-1", "internal/a.go")),
			round(rigor.Pro, tolerated("", "internal/a.go")),
		},
	} {
		if got := Churn(rounds); got != KindNone {
			t.Errorf("%s = %q; want %q", name, got, KindNone)
		}
	}
}

// TestChurnOffInPOCCriticalHotfix: the triage stage fills the key for Startup
// and Pro only (D1851), so a round in another mode is not one this comparator
// reads — the item stays on the ladder's normal path rather than escalating on
// a guess (F11).
func TestChurnOffInPOCCriticalHotfix(t *testing.T) {
	for _, mode := range []rigor.Mode{rigor.POC, rigor.Critical, rigor.Hotfix, ""} {
		rounds := []Round{
			round(mode, tolerated("k-1", "internal/a.go")),
			round(mode, tolerated("k-2", "internal/b.go")),
		}
		if got := Churn(rounds); got != KindNone {
			t.Errorf("new minor findings in %q = %q; want %q", mode, got, KindNone)
		}
	}

	// The control: the same rounds in the two modes the stage runs for are
	// churn, so the loop above is the mode's doing and not the rounds'.
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro} {
		rounds := []Round{
			round(mode, tolerated("k-1", "internal/a.go")),
			round(mode, tolerated("k-2", "internal/b.go")),
		}
		if got := Churn(rounds); got != KindNewMinor {
			t.Errorf("new minor findings in %q = %q; want %q", mode, got, KindNewMinor)
		}
	}
}
