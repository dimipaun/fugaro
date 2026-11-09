package triage

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// The apply step (docs/post-review-triage/implementation-plan.md T-TR6): the
// judge's verdict read inside the floor (D1840). It may defer only among the
// findings the mode's table blocks and the floor does not name (D1857), it is
// untrusted data that can never move a `needs_owner` finding (D1856), and a
// call that failed, an answer that cannot be placed, and a finding the answer
// left out all block (D1846). Every name carries TestApply, which is the
// task's own command.

// The keys below stand in for the hash of a finding's file and summary the
// runner derives (verdict_test.go's `key` is the shape the schema accepts; the
// apply step never reads them, only matches one to the finding it came from).
const (
	keyCorrect = "0f1e2d3c4b5a6978"
	keyOther   = "1122334455667788"
	keyThird   = "99aabbccddeeff00"
	keyUnknown = "abcdefabcdefabcd"
)

// onAxis is a finding the table's own axes block and the floor does not name:
// correctness at likelihood 4 × impact 4, a risk of 16, which Startup (8) and
// Pro (4) block, which no mode's severity override reads, and which Pro's
// rare-expensive escape does not reach (likelihood 4, not 1). It is the
// judge's reach (D1857), and what a verdict actually decides.
func onAxis() rigor.Score {
	return rigor.Score{Likelihood: 4, Impact: 4, FixCost: 1, Class: rigor.Correctness}
}

// trivial is a finding no mode's table blocks: correctness at risk 1. The
// table lets it through, and a judge has nothing to say about it.
func trivial() rigor.Score {
	return rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}
}

// verdict wraps entries in a document this package's own reader would accept,
// so a test never passes one shape here and another to Decode.
func verdict(entries ...Entry) Verdict {
	return Verdict{Version: Version, Findings: entries}
}

func TestApplyNeverDefersAFloorFinding(t *testing.T) {
	// Every shape the floor holds: the two classes the severity override names,
	// the top of the impact scale, and a finding the reviewer left unscored.
	floorScores := []rigor.Score{
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Security},
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.DataLoss},
		{Likelihood: 1, Impact: 5, FixCost: 1, Class: rigor.Correctness},
		{},
	}
	for _, m := range rigor.Modes {
		for _, s := range floorScores {
			if !rigor.Floor(m, s) {
				// Hotfix's row keeps the security gate alone (F28), so a
				// data_loss finding and an impact of 5 are the table's own
				// leniency there and not the floor's.
				continue
			}
			in := []Input{{Score: s, Key: keyCorrect}}
			out := Apply(m, in, verdict(Entry{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "looks minor"}), true)
			if len(out.Deferred) != 0 {
				t.Errorf("Apply(%q, %+v) deferred a floor finding: %+v", m, s, out.Deferred)
			}
			if len(out.Blocking) != 1 {
				t.Fatalf("Apply(%q, %+v) blocked %d findings, want 1", m, s, len(out.Blocking))
			}
			if p := out.Blocking[0]; p.Verdict != Block || p.Source != SourceTable || p.Key != keyCorrect {
				t.Errorf("Apply(%q, %+v) placed the floor finding as %+v, want a block by the table", m, s, p)
			}
		}
	}
}

func TestApplyNeverDefersANeedsOwnerFinding(t *testing.T) {
	// A finding the table blocks and the floor does not name, and one the table
	// tolerates: the flag holds both, and the tolerated one is where a reader
	// would otherwise see a deferral, because the table's own answer is to let
	// it through (F22; Decide reads the same flag above every mode's tolerance).
	for _, s := range []rigor.Score{onAxis(), trivial()} {
		if rigor.Floor(rigor.Startup, s) {
			t.Fatalf("Startup's floor holds %+v; this test would pass without the owner rule", s)
		}
		for _, ok := range []bool{true, false} {
			in := []Input{{Score: s, NeedsOwner: true, Key: keyCorrect}}
			out := Apply(rigor.Startup, in, verdict(Entry{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "looks minor"}), ok)
			if len(out.Deferred) != 0 {
				t.Errorf("Apply(ok=%v, %+v) deferred a finding only the owner can settle: %+v", ok, s, out.Deferred)
			}
			if len(out.Blocking) != 1 {
				t.Fatalf("Apply(ok=%v, %+v) blocked %d findings, want 1", ok, s, len(out.Blocking))
			}
			if p := out.Blocking[0]; p.Index != 0 || p.Key != keyCorrect || p.Verdict != Block || p.Source != SourceTable || p.Reason != reasonNeedsOwner {
				t.Errorf("Apply(ok=%v, %+v) placed the owner's finding as %+v, want a block the flag holds", ok, s, p)
			}
		}
	}
}

func TestApplyDropsAVerdictForAnUnknownFinding(t *testing.T) {
	in := []Input{
		{Score: onAxis(), Key: keyCorrect},
		{Score: trivial(), Key: keyOther},
	}
	out := Apply(rigor.Startup, in, verdict(
		Entry{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "doc drift"},
		Entry{Index: 7, Key: keyUnknown, Verdict: Defer, Reason: "a finding this round does not have"},
	), true)
	if len(out.Blocking) != 0 {
		t.Errorf("an entry naming no finding blocked something: %+v", out.Blocking)
	}
	if len(out.Deferred) != 2 {
		t.Fatalf("deferred %d findings, want the judge's deferral and the table's: %+v", len(out.Deferred), out.Deferred)
	}
	if p := out.Deferred[0]; p.Index != 0 || p.Source != SourceAgent {
		t.Errorf("the judge's deferral was placed as %+v, want finding 0 by the agent", p)
	}
	if p := out.Deferred[1]; p.Index != 1 || p.Source != SourceTable {
		t.Errorf("the tolerated finding was placed as %+v, want finding 1 by the table", p)
	}
	for _, p := range append(out.Blocking, out.Deferred...) {
		if p.Index == 7 || p.Key == keyUnknown {
			t.Errorf("the entry naming no finding reached the outcome: %+v", p)
		}
	}
}

func TestApplyMissingVerdictBlocks(t *testing.T) {
	in := []Input{
		{Score: onAxis(), Key: keyCorrect},
		{Score: onAxis(), Key: keyOther},
		{Score: onAxis(), Key: keyThird},
	}
	out := Apply(rigor.Pro, in, verdict(
		Entry{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "doc drift"},
		Entry{Index: 2, Key: keyThird, Verdict: Block, Reason: "the main path breaks"},
	), true)
	if len(out.Deferred) != 1 {
		t.Fatalf("deferred %d findings, want the one the judge deferred: %+v", len(out.Deferred), out.Deferred)
	}
	if p := out.Deferred[0]; p.Index != 0 || p.Verdict != Defer || p.Source != SourceAgent || p.Reason != "doc drift" {
		t.Errorf("the judge's deferral was placed as %+v, want finding 0 as the judge wrote it", p)
	}
	if len(out.Blocking) != 2 {
		t.Fatalf("blocked %d findings, want the silent one and the judge's block: %+v", len(out.Blocking), out.Blocking)
	}
	if p := out.Blocking[0]; p.Index != 1 || p.Key != keyOther || p.Verdict != Block || p.Source != SourceTable || p.Reason != reasonUnanswered {
		t.Errorf("the finding the verdict left out was placed as %+v, want finding 1 blocked by the table", p)
	}
	if p := out.Blocking[1]; p.Index != 2 || p.Source != SourceAgent || p.Reason != "the main path breaks" {
		t.Errorf("the judge's block was placed as %+v, want finding 2 as the judge wrote it", p)
	}
}

func TestApplyFailSafeBlocksAll(t *testing.T) {
	in := []Input{
		{Score: rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Security}, Key: keyCorrect},
		{Score: onAxis(), Key: keyOther},
		{Score: trivial(), Key: keyThird},
	}
	// ok == false: the call failed, timed out or answered something this
	// package's reader refused (D1846). Whatever verdict the caller holds is
	// not an answer, so it steers nothing.
	out := Apply(rigor.Startup, in, verdict(Entry{Index: 1, Key: keyOther, Verdict: Defer, Reason: "doc drift"}), false)
	if len(out.Blocking) != 2 {
		t.Fatalf("a failed call blocked %d findings, want both the table blocks: %+v", len(out.Blocking), out.Blocking)
	}
	for i, p := range out.Blocking {
		if p.Index != i || p.Verdict != Block || p.Source != SourceFailSafe || p.Reason != reasonFailSafe {
			t.Errorf("finding %d was placed as %+v, want a fail-safe block", i, p)
		}
	}
	if len(out.Deferred) != 1 {
		t.Fatalf("a failed call deferred %d findings, want only the one no mode blocks: %+v", len(out.Deferred), out.Deferred)
	}
	if p := out.Deferred[0]; p.Index != 2 || p.Source != SourceTable {
		t.Errorf("the tolerated finding was placed as %+v, want finding 2 by the table", p)
	}
}

func TestApplyTableToleratedStaysDeferred(t *testing.T) {
	in := []Input{{Score: trivial(), Key: keyCorrect}}
	// Whatever the judge answers about a finding the table tolerates, the
	// table's own answer stands: the judge's reach is the findings the table
	// blocks (D1857), so a deferral adds nothing and a block it does not hold
	// is not the judge's to give.
	for _, e := range []Entry{
		{},
		{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "doc drift"},
		{Index: 0, Key: keyCorrect, Verdict: Block, Reason: "I would hold it"},
	} {
		out := Apply(rigor.Startup, in, verdict(e), true)
		if len(out.Blocking) != 0 {
			t.Errorf("a tolerated finding was blocked: %+v", out.Blocking)
		}
		if len(out.Deferred) != 1 {
			t.Fatalf("deferred %d findings, want the tolerated one: %+v", len(out.Deferred), out.Deferred)
		}
		if p := out.Deferred[0]; p.Index != 0 || p.Key != keyCorrect || p.Verdict != Defer || p.Source != SourceTable || p.Reason != reasonTolerated {
			t.Errorf("the tolerated finding was placed as %+v, want a deferral by the table", p)
		}
	}
}

func TestApplyVerdictWithDisagreeingIndexAndKeyBlocks(t *testing.T) {
	// One entry whose key names the finding at index 1 while its index names
	// the finding at index 0: a reader taking the index and one taking the key
	// would defer different findings, so neither may be deferred (D1850).
	in := []Input{
		{Score: onAxis(), Key: keyCorrect},
		{Score: onAxis(), Key: keyOther},
	}
	out := Apply(rigor.Pro, in, verdict(Entry{Index: 0, Key: keyOther, Verdict: Defer, Reason: "doc drift"}), true)
	if len(out.Deferred) != 0 {
		t.Errorf("an entry whose index and key disagree deferred a finding: %+v", out.Deferred)
	}
	if len(out.Blocking) != 2 {
		t.Fatalf("blocked %d findings, want both the entry could name: %+v", len(out.Blocking), out.Blocking)
	}
	for _, p := range out.Blocking {
		if p.Verdict != Block || p.Source != SourceFailSafe {
			t.Errorf("a finding the entry could name was placed as %+v, want a fail-safe block", p)
		}
	}
}

func TestApplyTwoVerdictsForOneFindingBlock(t *testing.T) {
	in := []Input{{Score: onAxis(), Key: keyCorrect}}
	// Two answers for one finding: the word the round acts on would be whichever
	// a reader took first, so the finding blocks (D1846's strict direction).
	out := Apply(rigor.Pro, in, verdict(
		Entry{Index: 0, Key: keyCorrect, Verdict: Defer, Reason: "doc drift"},
		Entry{Index: 0, Key: keyCorrect, Verdict: Block, Reason: "the main path breaks"},
	), true)
	if len(out.Deferred) != 0 {
		t.Errorf("two verdicts for one finding deferred it: %+v", out.Deferred)
	}
	if len(out.Blocking) != 1 {
		t.Fatalf("blocked %d findings, want 1: %+v", len(out.Blocking), out.Blocking)
	}
	if p := out.Blocking[0]; p.Index != 0 || p.Verdict != Block || p.Source != SourceFailSafe {
		t.Errorf("the contested finding was placed as %+v, want a fail-safe block", p)
	}
}
