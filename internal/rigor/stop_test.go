package rigor

import (
	"slices"
	"testing"
)

// The at-the-stop row (the owner's ruling of 2026-11-08): what each mode does
// with the findings a run still has when it stops — its review rounds spent or
// the time threshold reached, whichever comes first — without a ship verdict.
// Every case below cites the sentence it holds: spec.md's findings policy per
// mode, the severity override, and the ruling's two modes that accept. The
// names carry Stop so the task's own command — `-run '…|Stop|…'` — is the whole
// file.

// Finding shapes the cases below are written against, each named for the
// sentence it exercises.
var (
	// a docDriftFinding is the shape the ruling names: a documentation-drift
	// finding of impact 2–3 and likelihood 3–5, which Startup's floor blocks and
	// its stop row accepts.
	docDrift = Score{Likelihood: 4, Impact: 2, FixCost: 1, Class: Correctness}
	// likelyAndMaterial is what Startup's floor is about, and the stop row
	// accepts it too: at the stop the axes are not consulted by the accepting
	// rows at all.
	likelyAndMaterial = Score{Likelihood: 5, Impact: 4, FixCost: 1, Class: Correctness}
	// aSecurityFinding and aDataLossFinding are the two classes the spec calls
	// catastrophic; impact5 is the top of the severity axis, the third half of
	// the override.
	aSecurityFinding = Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: Security}
	aDataLossFinding = Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: DataLoss}
	impact5          = Score{Likelihood: 1, Impact: 5, FixCost: 1, Class: Correctness}
	// aNit is the mildest finding the scale can express.
	aNit = Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: Cosmetic}
)

// TestStopRowsAreTheModesOwnAnswers pins each mode's row: the two that accept
// at the stop accept everything the override does not cover, Hotfix keeps its
// own security gate, and the two that keep a draft block everything. A row that
// widened Pro's or Critical's answer fails here rather than in a run, which is
// the direction the ruling is explicit about.
func TestStopRowsAreTheModesOwnAnswers(t *testing.T) {
	cases := []struct {
		mode Mode
		// accepted are the findings the mode accepts at the stop; blocked the
		// ones it still blocks on.
		accepted, blocked []Score
	}{
		{
			// POC's row keeps the catastrophic, exactly as its round does: the
			// stop row states the reading the mode already reviews under rather
			// than a second, wider one.
			mode:     POC,
			accepted: []Score{docDrift, likelyAndMaterial, aNit},
			blocked:  []Score{aSecurityFinding, aDataLossFinding, impact5, Score{}},
		},
		{
			mode:     Startup,
			accepted: []Score{docDrift, likelyAndMaterial, aNit},
			blocked:  []Score{aSecurityFinding, aDataLossFinding, impact5, Score{}},
		},
		{
			// Pro accepts at the stop exactly what it accepts in a round — a
			// finding the mode's own floor lets through is not one its stop row
			// takes back — and nothing else: a round the mode blocked is still a
			// draft when the rounds are gone.
			mode:     Pro,
			accepted: []Score{aNit},
			blocked:  []Score{docDrift, likelyAndMaterial, aSecurityFinding, Score{}},
		},
		{
			mode:     Critical,
			accepted: nil,
			blocked:  []Score{aNit, docDrift, likelyAndMaterial, aSecurityFinding, Score{}},
		},
		{
			mode:     Hotfix,
			accepted: []Score{aNit, docDrift, likelyAndMaterial, impact5, aDataLossFinding},
			blocked:  []Score{aSecurityFinding, Score{}},
		},
	}
	for _, c := range cases {
		for _, s := range c.accepted {
			if BlocksAtStop(c.mode, s) {
				t.Errorf("%s blocks %+v at the stop; its row accepts it", c.mode, s)
			}
		}
		for _, s := range c.blocked {
			if !BlocksAtStop(c.mode, s) {
				t.Errorf("%s accepts %+v at the stop; its row blocks it", c.mode, s)
			}
		}
	}
}

// TestStopRowCoversEveryModeInTheVocabulary: the case list above is written by
// hand, so this is what keeps a mode added to the vocabulary from reaching the
// stop without a case: every mode is in the table and in the list, and the two
// are the same vocabulary (D20's closed set).
func TestStopRowCoversEveryModeInTheVocabulary(t *testing.T) {
	for _, m := range Modes {
		if _, ok := thresholds[m]; !ok {
			t.Fatalf("%q has no row in the threshold table", m)
		}
		if !Known(m) {
			t.Fatalf("%q is not in the vocabulary", m)
		}
	}
	if len(Modes) != 5 {
		t.Fatalf("the vocabulary has %d modes; TestStopRowsAreTheModesOwnAnswers lists one case per mode", len(Modes))
	}
}

// TestStopStartupAcceptsTheFindingsItsFloorBlocks is the ruling's own case: a
// documentation-drift finding of impact 2–3 and likelihood 3–5 is what Startup's
// floor spends its rounds on and what it accepts once they are gone. Both halves
// are asked, so a row that widened the floor itself — rather than the stop —
// fails the first pair.
func TestStopStartupAcceptsTheFindingsItsFloorBlocks(t *testing.T) {
	for _, s := range []Score{
		{Likelihood: 3, Impact: 3, FixCost: 1, Class: Correctness},
		{Likelihood: 5, Impact: 2, FixCost: 1, Class: Maintainability},
		docDrift,
	} {
		if !Blocks(Startup, s) {
			t.Fatalf("Startup tolerates %+v in a round; the case below is about the stop", s)
		}
		if BlocksAtStop(Startup, s) {
			t.Errorf("Startup blocks %+v at the stop; the ruling accepts it there", s)
		}
	}
}

// TestStopBlocksTheCatastrophicInEveryAcceptingMode: the exception the ruling
// writes is the one the spec's override writes, asked of both modes that accept
// at the stop and of the third half of it — an impact of 5 in a class the spec
// does not call catastrophic — which Startup reads the same way its rounds do.
func TestStopBlocksTheCatastrophicInEveryAcceptingMode(t *testing.T) {
	if BlocksAtStop(Hotfix, aDataLossFinding) || BlocksAtStop(Hotfix, impact5) {
		t.Error("Hotfix blocks a data loss finding or an impact of 5 at the stop; its own row keeps the security gate alone")
	}
	for _, m := range []Mode{POC, Startup} {
		for _, s := range []Score{aSecurityFinding, aDataLossFinding, impact5} {
			if !BlocksAtStop(m, s) {
				t.Errorf("%s accepts %+v at the stop: a catastrophic finding ends the run a draft in every mode the ruling names", m, s)
			}
		}
	}
}

// TestStopWidensAndNeverNarrowsTheRound: the stop row is read over the mode's
// ordinary one (BlocksAtStop), so a finding a mode tolerates in a round is one
// it accepts at the stop, whatever its row says. The property is swept over the
// whole table rather than sampled: a mode whose stop row was written instead of
// its round's would take a finding away at the stop, which is the fail-closed
// direction the ruling does not ask for.
func TestStopWidensAndNeverNarrowsTheRound(t *testing.T) {
	modes := append(slices.Clone(Modes), "", "bogus")
	for _, m := range modes {
		for _, s := range everyScore() {
			if !Blocks(m, s) && BlocksAtStop(m, s) {
				t.Fatalf("%s tolerates %+v in a round and blocks it at the stop", m, s)
			}
		}
	}
}

// TestStopIsFailClosedWithoutARow: a mode the table does not hold, and the zero
// Mode a record written before the modes existed carries, block at the stop
// exactly as they do in a round — the value this code cannot place is never the
// lenient answer. It is the ruling's own fail-closed clause read at the table:
// a mode that says nothing keeps today's behaviour, which is the draft.
func TestStopIsFailClosedWithoutARow(t *testing.T) {
	for _, m := range []Mode{"", "bogus"} {
		for _, s := range everyScore() {
			if !BlocksAtStop(m, s) {
				t.Fatalf("BlocksAtStop(%q, %+v) is false; a mode without a stop row keeps the draft", m, s)
			}
		}
		// And the split is the one Decide reads: nothing accepted, so no
		// tolerance can apply to the round the run stopped on.
		blocking, accepted := SplitAtStop(m, []Score{aNit, docDrift})
		if len(blocking) != 2 || len(accepted) != 0 {
			t.Fatalf("SplitAtStop(%q, …) = %+v, %+v; want every finding blocking", m, blocking, accepted)
		}
	}
}

// TestStopSplitKeepsTheReviewersOrder: what the run accepts is printed in the
// reviewer's own order and filed one item per finding (D30), so the split keeps
// each group's order exactly as Split does, and a verdict with nothing in it
// answers with nothing rather than with two empty slices.
func TestStopSplitKeepsTheReviewersOrder(t *testing.T) {
	findings := []Score{
		aNit,
		aSecurityFinding,
		docDrift,
		impact5,
		likelyAndMaterial,
	}
	blocking, accepted := SplitAtStop(Startup, findings)
	if want := []Score{findings[1], findings[3]}; !slices.Equal(blocking, want) {
		t.Errorf("blocking = %+v; want %+v", blocking, want)
	}
	if want := []Score{findings[0], findings[2], findings[4]}; !slices.Equal(accepted, want) {
		t.Errorf("accepted = %+v; want %+v", accepted, want)
	}
	if b, a := SplitAtStop(Startup, nil); b != nil || a != nil {
		t.Errorf("SplitAtStop of no findings = %+v, %+v; want nothing in each", b, a)
	}
	for _, s := range everyScore() {
		b, a := SplitAtStop(Startup, []Score{s})
		if got, want := len(b) == 1, BlocksAtStop(Startup, s); got != want {
			t.Fatalf("SplitAtStop(Startup, %+v) blocked=%v and BlocksAtStop=%v", s, got, want)
		}
		if len(b)+len(a) != 1 {
			t.Fatalf("SplitAtStop(Startup, %+v) lost the finding", s)
		}
	}
}

// TestStopAndSplitAreTheSameRule: Split reads Blocks and SplitAtStop reads
// BlocksAtStop, so the property Split's own test holds for the round has to hold
// at the stop too — a caller that wanted the count of accepted findings, or the
// ones to file, cannot disagree with the readiness rule about which they are.
func TestStopAndSplitAreTheSameRule(t *testing.T) {
	for _, m := range append(slices.Clone(Modes), "", "bogus") {
		for _, s := range everyScore() {
			b, a := SplitAtStop(m, []Score{s})
			if got, want := len(b) == 1, BlocksAtStop(m, s); got != want {
				t.Fatalf("SplitAtStop(%s, %+v) blocked=%v and BlocksAtStop=%v", m, s, got, want)
			}
			if len(b)+len(a) != 1 {
				t.Fatalf("SplitAtStop(%s, %+v) lost the finding", m, s)
			}
		}
	}
}
