package rigor

import "testing"

// TestFilesIsD30sTable pins the per-level table T4.6 exists for, one row per
// level (docs/rigor-levels/reconciliation.md D30, implementation-plan.md T4.6):
// POC and Critical file nothing of their own, Startup and Pro file the
// findings they tolerated, and Hotfix files one for what it deferred whatever
// else it left open.
func TestFilesIsD30sTable(t *testing.T) {
	want := map[Rigor]Filing{
		POC:      {},
		Startup:  {Tolerated: true},
		Pro:      {Tolerated: true},
		Critical: {},
		Hotfix:   {Tolerated: true, Deferred: true},
	}
	if len(want) != len(Rigors) {
		t.Fatalf("this test has %d rows for %d levels: the table is one row per level (D30)", len(want), len(Rigors))
	}
	for _, m := range Rigors {
		w, ok := want[m]
		if !ok {
			t.Fatalf("this test has no row for %q: the table is one row per level (D30)", m)
		}
		if got := Files(m); got != w {
			t.Errorf("Files(%q) = %+v; want %+v", m, got, w)
		}
	}
	// The two levels that file nothing are named here rather than left to the
	// map above, because they are the whole point of the table: POC's account
	// is its pull request body's line and the run record, and Critical
	// tolerates nothing, so neither has anything of its own to file (D7, D30).
	for _, m := range []Rigor{POC, Critical} {
		if Files(m).Any() {
			t.Errorf("Files(%q) files something of its own; want nothing (D30)", m)
		}
	}
}

// TestFilesFallsToTheDefaultForAnUnresolvedLevel: the zero Rigor — what a layer
// that set nothing carries — and a value outside the vocabulary are not rows
// of the table. They get D19's default, Pro, whose row files the findings it
// tolerated, so a run that resolved no level files rather than dropping what it
// left open; a finding that disappears is the failure this feature risks
// (plan §7.5).
func TestFilesFallsToTheDefaultForAnUnresolvedRigor(t *testing.T) {
	if !Files(Default).Tolerated {
		t.Fatalf("the default's row files nothing: D19 makes Pro the default and D30's Pro row files the findings it tolerated")
	}
	for _, m := range []Rigor{"", "bogus", "Poc", "POC"} {
		if got, want := Files(m), Files(Default); got != want {
			t.Errorf("Files(%q) = %+v; want the default's row %+v", m, got, want)
		}
	}
}

// TestAnyIsTheRowsThatFile: Any is what the run's filing step asks first, so
// it has to be the two bits and nothing else — a row that files for one reason
// must not answer false, and a row that files for none must not answer true.
func TestAnyIsTheRowsThatFile(t *testing.T) {
	for _, m := range Rigors {
		f := Files(m)
		if got, want := f.Any(), f.Tolerated || f.Deferred; got != want {
			t.Errorf("Files(%q).Any() = %v; want %v", m, got, want)
		}
	}
}
