package escalation

import (
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// TestDefaultThresholdsIsTheSpecTable pins the shipped rows against spec.md's
// "Default thresholds per rigor mode" cell by cell, and against the default
// factor of 0.5 the spec's "Urgency modifiers" name.
func TestDefaultThresholdsIsTheSpecTable(t *testing.T) {
	th := DefaultThresholds()
	if th.CriticalPathFactor != 0.5 {
		t.Errorf("CriticalPathFactor = %v; want 0.5", th.CriticalPathFactor)
	}
	if th.CostCeiling != 0 {
		t.Errorf("CostCeiling = %v; want 0 (the key has no default)", th.CostCeiling)
	}
	want := map[rigor.Mode]Row{
		rigor.POC:      {RunsBeforeR2: 1, R2Hours: 3, Grade: Strong, CeilingHours: 5, AtCeiling: CeilingAutoMerge},
		rigor.Startup:  {RunsBeforeR2: 2, R2Hours: 6, Grade: Strong, CeilingHours: 9, AtCeiling: CeilingAutoMerge},
		rigor.Pro:      {RunsBeforeR2: 3, R2Hours: 9, Grade: Strong, CeilingHours: 12, AtCeiling: CeilingAutoMergeNotify},
		rigor.Critical: {RunsBeforeR2: 3, R2Hours: 9, Grade: Strongest, CeilingHours: 12, AtCeiling: CeilingWaitPerson},
		rigor.Hotfix:   {RunsBeforeR2: 0, R2Hours: 0, Grade: Strongest, CeilingHours: 3, AtCeiling: CeilingAutoMergeNotify},
	}
	if len(th.Rows) != len(want) {
		t.Fatalf("rows = %d; want %d", len(th.Rows), len(want))
	}
	for mode, row := range want {
		got, ok := th.Rows[mode]
		if !ok {
			t.Errorf("%s has no row", mode)
			continue
		}
		if got != row {
			t.Errorf("%s = %+v; want %+v", mode, got, row)
		}
	}
}

// TestDefaultThresholdsHasEveryMode: no mode is left without a row. A missing
// row is what makes the decision refuse (G5), so a mode added to the rigor
// vocabulary has to be given one here in the same change.
func TestDefaultThresholdsHasEveryMode(t *testing.T) {
	th := DefaultThresholds()
	for _, mode := range rigor.Modes {
		if _, ok := th.Rows[mode]; !ok {
			t.Errorf("%s has no row", mode)
		}
	}
}

// TestDefaultThresholdsIsFresh: the table is data a caller edits — E3's local
// keys write each row into it — so one caller's edits must not reach the next
// caller's table.
func TestDefaultThresholdsIsFresh(t *testing.T) {
	first := DefaultThresholds()
	row := first.Rows[rigor.Pro]
	row.CeilingHours = 1
	first.Rows[rigor.Pro] = row
	delete(first.Rows, rigor.POC)
	first.CriticalPathFactor = 1

	second := DefaultThresholds()
	if got := second.Rows[rigor.Pro]; got.CeilingHours != 12 {
		t.Errorf("Pro ceiling = %v; want 12", got.CeilingHours)
	}
	if _, ok := second.Rows[rigor.POC]; !ok {
		t.Error("POC is missing from a fresh table")
	}
	if second.CriticalPathFactor != 0.5 {
		t.Errorf("CriticalPathFactor = %v; want 0.5", second.CriticalPathFactor)
	}
}

// TestVocabulary: the closed lists, in order, and their own two answers. Each
// is what the ledger, the task field and the local keys spell, so a name that
// leaves one has to fail somewhere: Known* is where the writers ask.
func TestVocabulary(t *testing.T) {
	if want := []Rung{"R0", "R1", "R2", "R3", "R4"}; !reflect.DeepEqual(Rungs, want) {
		t.Errorf("Rungs = %v; want %v", Rungs, want)
	}
	if want := []Grade{"strong", "strongest"}; !reflect.DeepEqual(Grades, want) {
		t.Errorf("Grades = %v; want %v", Grades, want)
	}
	if want := []Kind{"none", "same", "new_minor"}; !reflect.DeepEqual(Kinds, want) {
		t.Errorf("Kinds = %v; want %v", Kinds, want)
	}
	if want := []CeilingAction{"auto_merge", "auto_merge_notify", "wait_person"}; !reflect.DeepEqual(CeilingActions, want) {
		t.Errorf("CeilingActions = %v; want %v", CeilingActions, want)
	}

	for _, r := range Rungs {
		if !KnownRung(r) {
			t.Errorf("KnownRung(%q) is false", r)
		}
	}
	for _, g := range Grades {
		if !KnownGrade(g) {
			t.Errorf("KnownGrade(%q) is false", g)
		}
	}
	for _, k := range Kinds {
		if !KnownKind(k) {
			t.Errorf("KnownKind(%q) is false", k)
		}
	}
	for _, a := range CeilingActions {
		if !KnownCeilingAction(a) {
			t.Errorf("KnownCeilingAction(%q) is false", a)
		}
	}

	if KnownRung("") || KnownRung("R5") || KnownRung("r0") {
		t.Error("KnownRung accepts a name outside the vocabulary")
	}
	if KnownGrade("") || KnownGrade("opus-5-5") {
		t.Error("KnownGrade accepts a name outside the vocabulary")
	}
	if KnownKind("") || KnownKind("churning") {
		t.Error("KnownKind accepts a name outside the vocabulary")
	}
	if KnownCeilingAction("") || KnownCeilingAction("merge") {
		t.Error("KnownCeilingAction accepts a name outside the vocabulary")
	}
}

// TestRungRank is the ladder's own order, and the floor under it: the zero
// Rung is below every real one, never above.
func TestRungRank(t *testing.T) {
	for i, r := range Rungs {
		if got := r.Rank(); got != i {
			t.Errorf("%s.Rank() = %d; want %d", r, got, i)
		}
	}
	if got := Rung("").Rank(); got != -1 {
		t.Errorf("zero Rung ranks %d; want -1", got)
	}
	if got := Rung("R9").Rank(); got != -1 {
		t.Errorf("unknown rung ranks %d; want -1", got)
	}
}

// TestMaxRebases: D2042 bounds rung 0 at two runs per item, which is the cap
// that makes the rebase loop terminate.
func TestMaxRebases(t *testing.T) {
	if MaxRebases != 2 {
		t.Errorf("MaxRebases = %d; want 2", MaxRebases)
	}
}
