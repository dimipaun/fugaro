package rigor

import (
	"slices"
	"testing"
)

// The floor (docs/post-review-triage/implementation-plan.md T-TR1): the part
// of the table a triage judge may not move — an unscored finding and the
// mode's own reading of the spec's severity override — as one predicate beside
// it (F3, D1840). The judge defers only among the findings the table blocks
// and this predicate does not name, so every statement below is about the
// boundary between the two: nothing the table tolerates is floor, the override
// is floor wherever its row reads it, and Hotfix's narrowed row is Hotfix's
// floor. Every name carries Floor, so the task's own command — `-run
// 'TestFloor'` — is the whole file.

// TestFloorMatchesBlocksOverrides: Floor is Blocks' first two reasons read as
// a predicate, so over every mode and every cell of everyScore the two agree
// in the direction that matters — a floor finding always blocks, because a
// judge may only *add* deferrals among findings the table blocks (D1840) — and
// every finding the mode's override reads is floor. The sweep is the whole
// table rather than chosen cells (table_test.go's everyScore), and the second
// half is what the class labels are for: a coder that steers a reviewer's
// wording cannot steer a catastrophic finding out of the floor.
func TestFloorMatchesBlocksOverrides(t *testing.T) {
	for _, m := range Modes {
		for _, s := range everyScore() {
			if Floor(m, s) && !Blocks(m, s) {
				t.Fatalf("Floor(%q, %+v) is true and Blocks(%q, …) is false; the floor is inside the table", m, s, m)
			}
			if thresholds[m].Override.blocks(s) && !Floor(m, s) {
				t.Errorf("Blocks(%q, %+v) reads the severity override and Floor does not", m, s)
			}
		}
	}
	// A mode the table does not hold blocks on everything (D4's fail-closed
	// reading), and it is floor on everything: the leniency a value the table
	// cannot place must never get is not the judge's to hand out either.
	for _, m := range []Mode{"", "bogus"} {
		tolerable := Score{Likelihood: 1, Impact: 1, FixCost: 5, Class: Cosmetic}
		if !Blocks(m, tolerable) {
			t.Fatalf("Blocks(%q, %+v) is false; a mode the table does not hold blocks", m, tolerable)
		}
		if !Floor(m, tolerable) {
			t.Errorf("Floor(%q, %+v) is false; a mode the table does not hold is floor on everything", m, tolerable)
		}
	}
}

// TestFloorNeverTrueWhenTableTolerates: the other direction, which is what
// makes the floor a floor rather than a second rule. A finding the mode's row
// lets through is already non-blocking, so it is not floor and never was: the
// predicate adds no finding to what the table blocks. It is stated over the
// sweep and then at the two rows whose escape it must not touch — POC's, which
// blocks on the override alone, and Pro's, whose fix-cost escape is exactly
// the corner case its spec sentence names.
func TestFloorNeverTrueWhenTableTolerates(t *testing.T) {
	for _, m := range Modes {
		for _, s := range everyScore() {
			if !Blocks(m, s) && Floor(m, s) {
				t.Fatalf("the table tolerates %+v at %q and it is floor; only a blocking finding may never be deferred", s, m)
			}
		}
	}
	// POC "Ignore (except catastrophic)": everything its override does not
	// read is tolerated there, so nothing else is floor — including a finding
	// the stricter modes block on, which is their row's business and the
	// judge's to weigh where they block it.
	for _, s := range []Score{
		{Likelihood: 5, Impact: 4, FixCost: 5, Class: Correctness},
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: Cosmetic},
	} {
		if Floor(POC, s) {
			t.Errorf("Floor(%q, %+v) is true; POC tolerates everything but the override", POC, s)
		}
	}
	// Pro's own escape — "extremely rare, high-effort issues may stay
	// unfixed" — is below the floor and stays there: the row tolerates the
	// finding, so the judge is not the one who keeps it open.
	rareAndExpensive := Score{Likelihood: 1, Impact: 4, FixCost: 5, Class: Correctness}
	if Blocks(Pro, rareAndExpensive) {
		t.Fatalf("Pro blocks %+v; the case is about Pro's escape", rareAndExpensive)
	}
	if Floor(Pro, rareAndExpensive) {
		t.Errorf("Floor(%q, %+v) is true; the row's escape is not the override", Pro, rareAndExpensive)
	}
}

// TestFloorHotfixKeepsSecurityOnly: Hotfix's row is the one that reads the
// override as the security gate alone (overrideSecurity, the owner's ruling of
// 2026-10-26), so its floor is the narrowest of the five — a security finding
// whatever its axes say, and neither of the two reasons the ruling gave up. A
// finding of class data_loss or an impact of 5 blocks in POC and in every mode
// above it, and this is the one mode where it is not floor: a judge cannot
// defer it at Hotfix because the table already tolerates it there, and where
// the table blocks it the judge may not move it.
func TestFloorHotfixKeepsSecurityOnly(t *testing.T) {
	for _, s := range []Score{
		{Likelihood: 1, Impact: 1, FixCost: 5, Class: Security},
		{Likelihood: 5, Impact: 5, FixCost: 1, Class: Security},
	} {
		if !Floor(Hotfix, s) {
			t.Errorf("Floor(%q, %+v) is false; the security gate is what the row keeps", Hotfix, s)
		}
	}
	for _, s := range []Score{
		{Likelihood: 5, Impact: 5, FixCost: 1, Class: DataLoss},
		{Likelihood: 5, Impact: 5, FixCost: 1, Class: Correctness},
	} {
		if Floor(Hotfix, s) {
			t.Errorf("Floor(%q, %+v) is true; the ruling narrowed the row to the security gate", Hotfix, s)
		}
		// The same finding in the mode the override is whole in, so the two
		// statements above are about Hotfix's row and not about the vocabulary.
		if !Floor(POC, s) {
			t.Errorf("Floor(%q, %+v) is false; POC reads the whole override", POC, s)
		}
	}
}

// TestFloorUnscoredFinding: D4's rule at the floor — a finding the table
// cannot read is the one reason that holds in every mode, past the override
// and past Hotfix's narrowed row. An absent score, an axis outside the scale
// and a class outside the vocabulary are all unscored (Score.Scored), and none
// of them may become a deferral: the judge would be reading a finding the
// reviewer never scored against a class the table does not know.
func TestFloorUnscoredFinding(t *testing.T) {
	unscored := []struct {
		what string
		s    Score
	}{
		{"a finding with no score at all", Score{}},
		{"a likelihood below the scale", Score{Likelihood: 0, Impact: 1, FixCost: 1, Class: Cosmetic}},
		{"an impact above the scale", Score{Likelihood: 1, Impact: 6, FixCost: 1, Class: Cosmetic}},
		{"a fix cost below the scale", Score{Likelihood: 1, Impact: 1, FixCost: 0, Class: Cosmetic}},
		{"a class outside the vocabulary", Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: "style"}},
		{"the zero class", Score{Likelihood: 1, Impact: 1, FixCost: 1}},
	}
	for _, c := range unscored {
		if c.s.Scored() {
			t.Fatalf("%s reads as scored: %+v", c.what, c.s)
		}
		for _, m := range append(slices.Clone(Modes), "", "bogus") {
			if !Floor(m, c.s) {
				t.Errorf("Floor(%q, %+v) is false for %s; an unscored finding is the floor in every mode", m, c.s, c.what)
			}
		}
	}
}
