package rigor

import "testing"

// TestDefaultLevelsPerMode pins D722's built-in defaults (L2.2): the answer a
// run starts from before the project layer, the repository, the task or the
// owner's floor has spoken. POC turns level 2 off — the spec's "level 1 only" —
// and every other mode keeps it on, Critical adding level 3. Hotfix is off the
// spec's table, and O10's ruling (2026-10-30) gives it Pro's set, so the two
// rows are compared here rather than trusted to stay in step.
func TestDefaultLevelsPerMode(t *testing.T) {
	want := map[Mode]Levels{
		POC:      {L1: LevelOn, L2: LevelOff},
		Startup:  {L2: LevelOn},
		Pro:      {L2: LevelOn},
		Critical: {L2: LevelOn, L3: LevelOn},
		Hotfix:   {L2: LevelOn},
	}
	if len(want) != len(Modes) {
		t.Fatalf("the table holds %d modes; want one per mode, %d", len(want), len(Modes))
	}
	for _, m := range Modes {
		got, err := DefaultLevels(m)
		if err != nil {
			t.Fatalf("DefaultLevels(%s): %v", m, err)
		}
		if got != want[m] {
			t.Errorf("DefaultLevels(%s) = %+v; want %+v", m, got, want[m])
		}
	}
	pro, err := DefaultLevels(Pro)
	if err != nil {
		t.Fatalf("DefaultLevels(%s): %v", Pro, err)
	}
	hotfix, err := DefaultLevels(Hotfix)
	if err != nil {
		t.Fatalf("DefaultLevels(%s): %v", Hotfix, err)
	}
	if hotfix != pro {
		t.Errorf("DefaultLevels(%s) = %+v; want Pro's %+v (O10)", Hotfix, hotfix, pro)
	}
}

// TestDefaultLevelsLeaveL1ToFirstLineReview: level 1 is not the rigor mode's to
// set. It follows `agent.first_line_review` (config.Agent.FirstLineOn), which
// this package cannot read and so must not guess at, and a default of `on` or
// `off` here would override the repository's own setting at every mode. POC is
// the exception: level 2 is off there, and D723 keeps a review in the run, so
// level 1 is turned on and decides readiness.
func TestDefaultLevelsLeaveL1ToFirstLineReview(t *testing.T) {
	for _, m := range Modes {
		got, err := DefaultLevels(m)
		if err != nil {
			t.Fatalf("DefaultLevels(%s): %v", m, err)
		}
		want := LevelUnset
		if m == POC {
			want = LevelOn
		}
		if got.L1 != want {
			t.Errorf("DefaultLevels(%s) = %+v; want L1 %s, the other two levels %s and %s",
				m, got, want, got.L2, got.L3)
		}
	}
}

// TestDefaultLevelsUnknownModeErrs: a mode outside the vocabulary is refused
// rather than answered with a default. A run this package cannot place a level
// for must not silently get one mode's levels, the way table.go blocks a
// finding it cannot read and RoundCeiling answers a mode it cannot place with
// the widest ceiling.
func TestDefaultLevelsUnknownModeErrs(t *testing.T) {
	for _, m := range []Mode{"", "Critical", "poc ", "fast"} {
		got, err := DefaultLevels(m)
		if err == nil {
			t.Errorf("DefaultLevels(%q) = %+v, %v; want an error and no levels", m, got, err)
			continue
		}
		if got != (Levels{}) {
			t.Errorf("DefaultLevels(%q) = %+v with an error; want the zero Levels", m, got)
		}
	}
}
