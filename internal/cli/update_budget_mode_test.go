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

// budgetModeUpdateRigUnmanaged is the same installation with the one fact
// removed: the project is Fugaro's own — the runs bucket exists and carries the
// installation's mark — and it is NOT flagged as managed. Its budget backend is
// configured and reachable all the same, which is the state no rig combined
// with an unmanaged installation until now: without it, deleting the step's
// `!managed` guard costs nothing, because every other rig either has the marks
// or has no database for the raise to land in.
func budgetModeUpdateRigUnmanaged(t *testing.T, mode string) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := updateRig(t)
	// The installation's own runs bucket and mark, and neither of the two
	// management facts (managedRuns writes the label and the object): this is
	// a project nobody flagged, which is the whole of what makes the step
	// leave it alone (D1553).
	r.markedRuns(t, initProjectName, initProject)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	// Everything the raise would need is here and correct, so a step that
	// reached the write would succeed at it: what keeps the mode observe is
	// the project not being managed, and nothing else.
	db.Set(budget.PathMark, auroraMark())
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMode, mode)
	budgetModeCaps(db)
	if managedFlagIsManaged(t, r) {
		t.Fatal("the rig is a managed project, so the step under test would fire on it whatever its guard says")
	}
	return r, db
}

// TestAnUnmanagedProjectKeepsItsSeededObserve: the raise's precondition is the
// project being managed, and on one that is not it is not a warning, a
// refusal or a question — the step declines in silence and the mode is exactly
// what it found. D1553: `fugaro managed flag` is what makes an installation
// managed, never the update, so an owner's own run may not move a mode the
// owner never asked this command to touch.
func TestAnUnmanagedProjectKeepsItsSeededObserve(t *testing.T) {
	_, db := budgetModeUpdateRigUnmanaged(t, budget.ModeObserve)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	said := out + errOut
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s", err, said)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeObserve {
		t.Errorf("%s = %v after an update of an unmanaged project, want %s: this step raises a mode on a managed project alone", budget.PathMode, got, budget.ModeObserve)
	}
	if got := db.Value(budget.PathModeSet); got != nil {
		t.Errorf("%s = %v: the update records a choice only where it raised the mode", budget.PathModeSet, got)
	}
	if strings.Contains(said, "budget mode") {
		t.Errorf("the run says something about the mode of a project that is not managed:\n%s", said)
	}
}

// budgetModeCaps seeds every cap the mode enforce compares a lease against:
// the project-wide pair (D6, RequireCapsForEnforce), and the default daily cap
// a repository with none of its own is compared against — the side the
// project-wide caps do not cover, without which enforce halts on
// noRepoDailyCap instead of bounding anything (budget.Caps.RepoDaily).
func budgetModeCaps(db *gcpfake.RTDB) {
	db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 150 * usd1, "perRunMicros": 20 * usd1})
	db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 60 * usd1})
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

// TestUpdateLeavesASeededObserveWhenTheRepositoryDailyCapIsMissing: the caps
// enforce compares every lease against are three, and the third is not one of
// the project-wide pair — it is the daily cap a repository's day counter is
// compared against, which is the repository's own or the default and never the
// project-wide one (budget.Caps.RepoDaily; its absence is evaluate.go's
// noRepoDailyCap). An installation whose caps were set with `fugaro budget set
// --global --daily … --per-run …` alone is therefore exactly the installation
// the raise must refuse: every repository without a daily cap of its own would
// halt with no_cap, and doctor would fall silent afterwards, because an
// installation that enforces is one it says nothing about.
func TestUpdateLeavesASeededObserveWhenTheRepositoryDailyCapIsMissing(t *testing.T) {
	_, db := budgetModeUpdateRig(t, budget.ModeObserve, "")
	db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 150 * usd1, "perRunMicros": 20 * usd1})
	db.Set(budget.PathCapsDefaults, map[string]any{"repoPerRunMicros": 20 * usd1})
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	said := out + errOut
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s", err, said)
	}
	if got := db.Value(budget.PathMode); got != budget.ModeObserve {
		t.Errorf("%s = %v, want %s: enforce with no repository daily cap halts every repository that has none of its own", budget.PathMode, got, budget.ModeObserve)
	}
	if got := db.Value(budget.PathModeSet); got != nil {
		t.Errorf("%s = %v: a raise that did not land records nothing", budget.PathModeSet, got)
	}
	for _, want := range []string{"repoDailyMicros", budget.PathCapsDefaults, "fugaro budget set --defaults --daily", "unbounded"} {
		if !strings.Contains(said, want) {
			t.Errorf("the run does not say %q about the raise it could not make:\n%s", want, said)
		}
	}
	// The line doctor leaves for this state names the same cap and the same
	// command (TestDoctorNamesTheMode's own rig), so a reader who does what
	// either line says is left with an installation the next update raises.
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
		// Both repairs, not just the update: the update raises the mode on a
		// managed project alone, so a line whose only fix were `fugaro update`
		// could never clear on an installation that is not managed. The owner's
		// own command works wherever the installation is.
		for _, want := range []string{budget.ModeObserve, budget.ModeEnforce, "fugaro update", "fugaro budget set --global --mode " + budget.ModeEnforce} {
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
		for _, want := range []string{"dailyMicros", "perRunMicros", "repoDailyMicros", "unbounded"} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("the line does not name %q: %+v", want, c)
			}
		}
		if !strings.Contains(c.Fix, "fugaro budget set --global") {
			t.Errorf("the line does not name the command that sets the caps: %+v", c)
		}
		// The third cap is not one of the project-wide pair and has a command of
		// its own: a reader told only about `--global` would set the two it
		// names and be left in exactly the state the update refuses to raise.
		if !strings.Contains(c.Fix, "fugaro budget set --defaults --daily") {
			t.Errorf("the line does not name the command that sets the cap a repository without one is compared against: %+v", c)
		}
		// Setting the caps is not the whole repair: with them set the mode still
		// has to be raised, and the line hands the reader the command that does
		// it on an installation the update does not raise it on.
		if !strings.Contains(c.Fix, "fugaro budget set --global --mode "+budget.ModeEnforce) {
			t.Errorf("the line does not name the command that raises the mode: %+v", c)
		}
	})
	t.Run("the default daily cap is named on its own", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		// The state the project-wide commands alone leave behind: both caps
		// set, and no cap a repository without one of its own is compared
		// against. The update refuses to raise, so this line is the one that
		// names what is missing.
		db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 150 * usd1, "perRunMicros": 20 * usd1})
		c, ok := doctorChecksByID(t)[budgetModeCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check", budgetModeCheckID)
		}
		if c.OK {
			t.Fatalf("doctor passes an installation whose caps enforce needs are missing: %+v", c)
		}
		for _, want := range []string{"repoDailyMicros", budget.PathCapsDefaults, "fugaro budget set --defaults --daily"} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("the line does not name %q: %+v", want, c)
			}
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
