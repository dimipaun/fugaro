package rigor

import "testing"

// TestHandBackIsLD5sAndLD7sTable pins the per-level hand-back table LV4.3
// exists for, one row per level (levels-spec.md §2, levels-reconciliation.md
// LD5 and LD7): POC hands back its prototype line and nothing else, Startup and
// Pro the agent's deferred list, Critical its verification and its open risks,
// Hotfix the revert plan and the morning note.
func TestHandBackIsLD5sAndLD7sTable(t *testing.T) {
	want := map[Rigor]HandBack{
		POC:      {Prototype: true},
		Startup:  {Deferred: true},
		Pro:      {Deferred: true},
		Critical: {Verification: true, OpenRisks: true},
		Hotfix:   {RevertPlan: true, MorningNote: true},
	}
	if len(want) != len(Rigors) {
		t.Fatalf("this test has %d rows for %d levels: the table is one row per level (LD5, LD7)", len(want), len(Rigors))
	}
	for _, m := range Rigors {
		w, ok := want[m]
		if !ok {
			t.Fatalf("this test has no row for %q: the table is one row per level (LD5, LD7)", m)
		}
		if got := HandBackOf(m); got != w {
			t.Errorf("HandBackOf(%q) = %+v; want %+v", m, got, w)
		}
	}
	// Named rather than left to the map above, because these are the rows the
	// level's own persona grounds: POC keeps no deferred list ("you do not know
	// what you skipped", personas/poc.md) and hands back a line the runner
	// writes, and Critical tolerates nothing, so what it owes the reader is the
	// proof of the work and the risks it could not close (D7, LF9).
	if got := HandBackOf(POC); !got.Prototype || got.Deferred || got.Verification || got.OpenRisks || got.RevertPlan || got.MorningNote {
		t.Errorf("HandBackOf(poc) = %+v; want the prototype line and nothing else", got)
	}
	if got := HandBackOf(Critical); got.Deferred {
		t.Errorf("HandBackOf(critical) = %+v; want no deferred list: Critical tolerates nothing (D7)", got)
	}
}

// TestHandBackFallsToTheDefaultForAnUnresolvedLevel: the zero Rigor — what a
// record carrying no block has — and a value outside the vocabulary are not
// rows of the table. They get D19's default, Pro, whose row is the agent's
// deferred list, which is the widest of the five: a value that arrived
// unparsed shows the reader more of what the agent wrote, never less.
func TestHandBackFallsToTheDefaultForAnUnresolvedRigor(t *testing.T) {
	if got := HandBackOf(Default); got != (HandBack{Deferred: true}) {
		t.Fatalf("HandBackOf(Default) = %+v; want Pro's row, the deferred list (D19)", got)
	}
	for _, m := range []Rigor{"", "bogus", "Poc", "POC"} {
		if got, want := HandBackOf(m), HandBackOf(Default); got != want {
			t.Errorf("HandBackOf(%q) = %+v; want the default's row %+v", m, got, want)
		}
	}
}

// TestAnyIsAnySectionOrThePrototypeLine: Any is what a caller asks before it
// renders anything, so it has to be true for every row the table gives and
// false only for a row that hands back nothing at all.
func TestAnyIsAnySectionOrThePrototypeLine(t *testing.T) {
	if (HandBack{}).Any() {
		t.Errorf("the empty row says it hands something back")
	}
	for _, m := range Rigors {
		if !HandBackOf(m).Any() {
			t.Errorf("HandBackOf(%q).Any() = false; every level of the vocabulary owes its reader something", m)
		}
	}
	for _, h := range []HandBack{
		{Prototype: true}, {Deferred: true}, {Verification: true},
		{OpenRisks: true}, {RevertPlan: true}, {MorningNote: true},
	} {
		if !h.Any() {
			t.Errorf("%+v says it hands nothing back; want true", h)
		}
	}
}

// TestHandBackIsNotTheFilingTable: LV4.3 carries the agent's sections into the
// account the run publishes and files no item (the runner stopped filing,
// D2716 supersedes D26–D32), so the two tables beside each other must not have
// been collapsed into one: Files still answers what a level may tolerate (D9)
// and HandBackOf answers what its reader is owed.
func TestHandBackIsNotTheFilingTable(t *testing.T) {
	// The two rows of one level answer different questions, and they differ:
	// Files(Critical) is empty because Critical tolerates nothing (D7) while
	// HandBackOf(Critical) carries the verification and the risks it owes the
	// reader (LF9), and Files(Startup) tolerates a finding while HandBackOf
	// says nothing about tolerance at all. One table would have to give one
	// answer to both, which is why there are two.
	if Files(Critical).Any() || !HandBackOf(Critical).Any() {
		t.Errorf("Critical: Files.Any() = %v, HandBackOf.Any() = %v; want false and true",
			Files(Critical).Any(), HandBackOf(Critical).Any())
	}
	if !Files(Startup).Tolerated || HandBackOf(Startup) != (HandBack{Deferred: true}) {
		t.Errorf("Startup: Files = %+v, HandBackOf = %+v; want its tolerance and its deferred list apart",
			Files(Startup), HandBackOf(Startup))
	}
}
