package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// update_budget_mode_test.go holds task T16 of docs/no-prompt-policy: on a
// managed project `fugaro update` raises the project-wide budget mode to
// enforce where the installation's owner has not chosen one (D1548's answer —
// an absent mode is observe — plus D1554's nightly catalog-check lease, which
// only bites in enforce; T10 follows this task).
//
// What is asserted here is the state the database holds afterwards, read back
// through the fake, and the record beside the mode that says who chose it: the
// raise may never touch a mode a person chose, or the next update would undo
// the owner's own setting.

// budgetModeUpdateRig is a managed installation that converges, with its
// budget backend on the RTDB fake and the mode node seeded as the test says.
// A seeded observe carries no config/modeSet: that is the state every
// installation reaches this change in — the mode was seeded by its own
// converge and nobody chose it.
func budgetModeUpdateRig(t *testing.T, mode, modeSet string) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := updateRig(t)
	// The installation is managed — both marks — because the step under test
	// fires on a managed project alone (D1548, T6(b)), and managedRuns is the
	// rig every other managed test builds its world with. Its gated steps
	// record what they did, so the credentials that account names are
	// isolated, exactly as updateManagedRig isolates them.
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	// The database's own two facts: the mark the ownership question reads
	// (budgetDBUnadopted), and the name every reader compares the project
	// with. Without the mark the database holds data and is not ours, which is
	// the refusal this step must never meet on a converged installation.
	db.Set(budget.PathMark, auroraMark())
	db.Set(budget.PathProject, initProjectName)
	if mode != "" {
		db.Set(budget.PathMode, mode)
	}
	if modeSet != "" {
		db.Set(budget.PathModeSet, modeSet)
	}
	if !managedFlagIsManaged(t, r) {
		t.Fatal("the rig is not a managed project, so the step under test would not fire on it")
	}
	return r, db
}

// budgetModeCaps seeds the two project-wide caps the mode enforce needs (D6).
func budgetModeCaps(db *gcpfake.RTDB) {
	db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 150 * usd1, "perRunMicros": 20 * usd1})
}

// TestUpdateRaisesASeededObserveToEnforce: one `fugaro update` over a managed
// installation whose mode was seeded observe — no one chose it — leaves the
// project enforcing and records that the update is who set it, so the owner's
// own setting is the only thing a later update respects.
func TestUpdateRaisesASeededObserveToEnforce(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeObserve, "")
	budgetModeCaps(db)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeEnforce {
		t.Errorf("%s = %v after the update, want %s: a seeded observe is nobody's choice and this is the run that raises it (D1548, T6(b))", budget.PathMode, got, budget.ModeEnforce)
	}
	if got := db.Value(budget.PathModeSet); got != budget.ChosenByUpdate {
		t.Errorf("%s = %v, want %q: the raise must record that the update made it, not that the owner asked for it", budget.PathModeSet, got, budget.ChosenByUpdate)
	}
	said := out + errOut
	if !strings.Contains(said, budget.ModeEnforce) {
		t.Errorf("the run does not say it raised the mode:\n%s", said)
	}
	// The raise is nobody's confirmation and nothing is left for the owner:
	// the same rule the self-flag follows (the HARD RULE), and here it also
	// means the run may not end unfinished over it.
	if strings.Contains(said, "type ") {
		t.Errorf("the run asked a question of a closed stdin:\n%s", said)
	}
}

// TestAnOwnerChosenObserveIsNeverRaised: config/modeSet = owner is the record
// of a person's choice, and the update leaves it alone — ever after, whatever
// the mode is. Without this half the step would undo the owner's own setting
// on every run, which is the one thing the mode's "changed only on request"
// rule exists to prevent.
func TestAnOwnerChosenObserveIsNeverRaised(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeObserve, budget.ChosenByOwner)
	budgetModeCaps(db)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeObserve {
		t.Errorf("%s = %v after the update, want %s: the owner chose it", budget.PathMode, got, budget.ModeObserve)
	}
	if got := db.Value(budget.PathModeSet); got != budget.ChosenByOwner {
		t.Errorf("%s = %v, want %q: the update does not write beside a mode its owner chose", budget.PathModeSet, got, budget.ChosenByOwner)
	}
	if said := out + errOut; strings.Contains(said, "raised") {
		t.Errorf("the run claims to have raised a mode its owner chose:\n%s", said)
	}
}

// TestPreviousModeConvergesOnUpdate is the HARD RULE's own test for this step:
// an installation from before this change — the mode seeded observe, no
// config/modeSet at all — converges to enforcing in ONE `fugaro update`, and
// `fugaro doctor` afterwards has nothing left to say about the mode. A step
// that needed a second command, or that left doctor naming the raise, is the
// operator step the release may not ship.
func TestPreviousModeConvergesOnUpdate(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeObserve, "")
	budgetModeCaps(db)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	if db.Value(budget.PathMode) != budget.ModeEnforce || db.Value(budget.PathModeSet) != budget.ChosenByUpdate {
		t.Fatalf("after one update %s = %v and %s = %v, want %s / %q",
			budget.PathMode, db.Value(budget.PathMode), budget.PathModeSet, db.Value(budget.PathModeSet), budget.ModeEnforce, budget.ChosenByUpdate)
	}
	if c, ok := doctorChecksByID(t)[budgetModeCheckID]; ok && !c.OK {
		t.Errorf("doctor still has work to report after the update: %+v", c)
	}
}

// TestUpdateIsANoOpAfterTheRaise: the second run finds the mode the update
// itself recorded and writes nothing — no second write of the same two nodes,
// and nothing said about the mode. The database refuses both writes, so a run
// that tried one would say so.
func TestUpdateIsANoOpAfterTheRaise(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeEnforce, budget.ChosenByUpdate)
	budgetModeCaps(db)
	db.Deny(budget.PathMode)
	db.Deny(budget.PathModeSet)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeEnforce {
		t.Errorf("%s = %v, want %s", budget.PathMode, got, budget.ModeEnforce)
	}
	if said := out + errOut; strings.Contains(said, budget.PathMode) || strings.Contains(said, "budget mode") {
		t.Errorf("the run acted on a mode the update itself set:\n%s", said)
	}
}

// TestUpdateLeavesASeededObserveWhenTheCapsAreMissing: enforce needs the two
// project-wide caps (D6) — an absent daily cap is a refusal in the rules, so
// every lease halts, and an absent per-run cap leaves a run with no cap of its
// own nothing to compare against. Without them the raise cannot land, and the
// run says which are missing and that the installation is unbounded rather
// than failing the update (the update itself did everything else it had to).
func TestUpdateLeavesASeededObserveWhenTheCapsAreMissing(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeObserve, "")
	db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 150 * usd1})
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeObserve {
		t.Errorf("%s = %v, want %s: enforce without the caps halts every run", budget.PathMode, got, budget.ModeObserve)
	}
	if got := db.Value(budget.PathModeSet); got != nil {
		t.Errorf("%s = %v: a raise that did not land records nothing", budget.PathModeSet, got)
	}
	said := out + errOut
	for _, want := range []string{"perRunMicros", "unbounded"} {
		if !strings.Contains(said, want) {
			t.Errorf("the run does not say %q about the raise it could not make:\n%s", want, said)
		}
	}
}

// TestUpdateRaisesTheModeInTheRunThatNamesTheBackend: the run whose own
// arguments shape the installation — `update --firebase-project` — is the one
// that gives it a budget backend, and that run is admitted without the managed
// gate (shapesTheInstallation), so it has no gate read to take the answer from
// and asks the fact itself. Everything else is the same step: the database it
// has just deployed holds a seeded observe that nobody chose, and the caps a
// fresh one carries from `budget set --global`, so the run that made the
// database is the run that raises its mode. Left to the next update instead,
// the installation would sit unbounded for as long as the owner takes to run
// the command a second time, which is the one-run rule the HARD RULE states.
func TestUpdateRaisesTheModeInTheRunThatNamesTheBackend(t *testing.T) {
	r := newFBRig(t)
	r.db.Set(budget.PathMark, auroraMark())
	r.fs.SetDatabase("us-east5")
	// The project's managed marks, or the step would correctly leave the mode
	// alone: managedRuns writes the label and the object the read compares.
	r.managedRuns(t, initProjectName, initProject)
	budgetModeCaps(r.db)
	fakeTerminal(t)
	// No --budget-mode: the door seeds config/mode = observe and, per the
	// record's whole point, writes no config/modeSet with it.
	out, errOut, err := executeStdin(t, initProjectName+"\n", "update", "--firebase-project", fpID, "--yes")
	said := out + errOut
	if err != nil {
		t.Fatalf("update --firebase-project failed: %v\n%s", err, said)
	}
	if got := r.db.Value(budget.PathMode); got != budget.ModeEnforce {
		t.Errorf("%s = %v in the database the run has just deployed, want %s\n%s", budget.PathMode, got, budget.ModeEnforce, said)
	}
	if got := r.db.Value(budget.PathModeSet); got != budget.ChosenByUpdate {
		t.Errorf("%s = %v, want %q\n%s", budget.PathModeSet, got, budget.ChosenByUpdate, said)
	}
}

// TestDoctorNamesTheMode: doctor's one line about the project-wide mode
// (T16). It exists for the state the update exists for — a seeded observe,
// which the next `fugaro update` raises to enforce — it names the caps whose
// absence would keep the raise from landing, and it says nothing at all once
// the installation enforces.
func TestDoctorNamesTheMode(t *testing.T) {
	t.Run("a seeded observe is named with the update that raises it", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		budgetModeCaps(db)
		c, ok := doctorChecksByID(t)[budgetModeCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check for a seeded observe", budgetModeCheckID)
		}
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = ok %v, severity %q, want a warning", budgetModeCheckID, c.OK, c.Severity)
		}
		for _, want := range []string{budget.ModeObserve, budget.ModeEnforce, "fugaro update"} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("the line does not name %q: %+v", want, c)
			}
		}
	})
	t.Run("missing caps are named as the reason the raise cannot land", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		c, ok := doctorChecksByID(t)[budgetModeCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check", budgetModeCheckID)
		}
		for _, want := range []string{"dailyMicros", "perRunMicros", "unbounded"} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("the line does not name %q: %+v", want, c)
			}
		}
		if !strings.Contains(c.Fix, "fugaro budget set --global") {
			t.Errorf("the line does not name the command that sets the caps: %+v", c)
		}
	})
	t.Run("an enforcing installation has nothing to say", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeEnforce)
		budgetModeCaps(db)
		if c, ok := doctorChecksByID(t)[budgetModeCheckID]; ok && !c.OK {
			t.Errorf("doctor reports %+v on an installation that enforces", c)
		}
	})
	t.Run("a mode the owner chose is left alone", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		db.Set(budget.PathModeSet, budget.ChosenByOwner)
		budgetModeCaps(db)
		if c, ok := doctorChecksByID(t)[budgetModeCheckID]; ok && !c.OK {
			t.Errorf("doctor reports %+v on a mode its owner chose", c)
		}
	})
}
