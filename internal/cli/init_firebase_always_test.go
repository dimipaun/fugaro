package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// AB7: the Firebase stage runs with no flag, on `setup` and `update`, and it
// acts only to adopt the budget database this installation already has or to
// create one where there is none (F3, R2). Everything in this file is about
// that door and about the four things that decide what it may do: the database
// this machine already records (F34), the Firebase state (F23, F24), the
// project's own IAM answer (R2) and the price of its confirmation (F4), plus
// the three stages that were skipped with the flag and are not any more (F29).

// alwaysRig is the installation the no-flag stage meets: it exists in its own
// project, its Firebase project's fakes are there, and nothing on this machine
// records a budget database. The plan reports no changes, so the run is about
// the stage's own decision and not about the applies behind it.
func alwaysRig(t *testing.T) *fbRig {
	t.Helper()
	r := newFBRig(t)
	r.installationState(t)
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	t.Chdir(t.TempDir())
	return r
}

// mayCreate grants this credential the project's IAM changes, which is what a
// project owner holds: the fake answers nothing held by default, which is the
// "if not, the stage is skipped with one line saying who must run" arm of R2.
func mayCreate(t *testing.T, r *fbRig) {
	t.Helper()
	r.crm.SetHeldPermissions(fpID, infra.DatabaseCreatorPermissions...)
}

// noDatabaseYet takes the Firebase project's Realtime Database away: a project
// with none is the case this stage creates in, and the rig's own
// DEFAULT_DATABASE is the case F32 is about (a database that is there while
// this machine records none).
func noDatabaseYet(t *testing.T, r *fbRig) {
	t.Helper()
	r.fbdb.RemoveInstances(fpID)
}

// tfRan are this run's terraform calls of one subcommand in one root, in
// order: what the run did in the Firebase root is a question about a root the
// other rig helpers answer only for applies.
func tfRan(t *testing.T, r *initRig, root, sub string) []tfCall {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []tfCall
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var c tfCall
		if err := json.Unmarshal(line, &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) > 0 && c.Args[0] == sub && filepath.Base(c.Dir) == root {
			out = append(out, c)
		}
	}
	return out
}

// noStepLeftBehind fails on a run that stopped for anything but a step left for
// the person: `fugaro update` closes such a run with "fugaro update is not
// finished", and the Firestore location's note is one of those steps (D3027 —
// the database is written before it, which is what these two tests are about).
func noStepLeftBehind(t *testing.T, err error) {
	t.Helper()
	if err != nil && !strings.Contains(err.Error(), "step(s) left for you") {
		t.Fatalf("the run did not reach its own step: %v", err)
	}
}

// TestUpdateCreatesTheDatabaseWithNoFlag is F3 at the door it names: a plain
// `fugaro update` on an installation with no database creates one, seeds it as
// today's (observe, F31), records it in the local config and sets the switch
// the installation root's `enable_budget` is — with no --firebase-project
// anywhere.
func TestUpdateCreatesTheDatabaseWithNoFlag(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	noDatabaseYet(t, r)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("a plain update did not create the budget database: %v\n%s", err, out)
	}
	if r.db.Value(budget.PathMark) == nil || r.db.Value(budget.PathProject) != initProjectName {
		t.Errorf("the database holds mark %v and project %v, want this installation's:\n%s", r.db.Value(budget.PathMark), r.db.Value(budget.PathProject), out)
	}
	b := r.localConfig(t).Budget
	if b == nil || b.FirebaseProject != fpID || b.RTDBURL != r.db.URL {
		t.Errorf("the local config records budget %+v, want the database this run made (%s, %s)", b, fpID, r.db.URL)
	}
	if v := r.tfvars(t)["enable_budget"]; v != true {
		t.Errorf("enable_budget is %v, want true once the database exists (F28)", v)
	}
	if !strings.Contains(out, "wrote the database") {
		t.Errorf("the run does not account for the database it wrote:\n%s", out)
	}
}

// TestADatabaseUnknownHereIsAdopted is F23 and F24's arm: the Firebase root's
// state names a database of the installation's own project, so the run adopts
// it — the local config records it, and the Firebase root is read for its
// outputs and for nothing else: no plan of it and no apply in it at all.
func TestADatabaseUnknownHereIsAdopted(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	r.firebaseState(t)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b := r.localConfig(t).Budget
	if b == nil || b.FirebaseProject != fpID || b.RTDBURL != r.db.URL || b.FirebaseAPIKey != fpAPIKey || b.TokenSigner != fpSigner {
		t.Errorf("the local config records budget %+v, want the state's own outputs", b)
	}
	if !strings.Contains(out, "is the one this installation already has") {
		t.Errorf("the run does not say it adopted the database it found:\n%s", out)
	}
	if plan := tfRan(t, r.initRig, "firebase", "plan"); len(plan) != 0 {
		t.Errorf("an adoption planned the Firebase root: %v", plan)
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("an adoption applied the Firebase root: %v", got)
	}
}

// TestAStateNamingAForeignProjectIsNotAdopted is F24: the state is anyone's to
// write, so a state whose outputs name another project is not this
// installation's database. It is neither adopted nor built over — a second
// database is the thing this path exists to avoid (F32) — and the run says so
// in one line and carries on.
func TestAStateNamingAForeignProjectIsNotAdopted(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	r.firebaseState(t)
	r.script["output@firebase"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"rtdb_url": "https://someone-elses-default-rtdb.firebaseio.com", "firebase_api_key": fpAPIKey,
		"token_signer": "fugaro-token-signer@other-fp.iam.gserviceaccount.com", "firebase_project": "other-fp",
	})}
	r.save(t)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := r.localConfig(t).Budget; b != nil && b.RTDBURL != "" {
		t.Errorf("a foreign state's database was recorded: %+v", b)
	}
	// The terminal gets the brief account (report.Brief) and the run's log the
	// step's whole sentence, which is where a line this long is read.
	whole := out + newestLog(t, "update")
	if !strings.Contains(whole, "no budget database was recorded") || !strings.Contains(whole, "other-fp") {
		t.Errorf("the run does not say why it adopted nothing:\n%s", whole)
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("the run applied the Firebase root over a foreign state: %v", got)
	}
	if r.db.Value(budget.PathMark) != nil {
		t.Errorf("the run wrote into a database it did not adopt")
	}
	// The rest of the update carried on: the stages behind this one ran.
	for _, stage := range []string{initflow.Images, initflow.Installation2} {
		if stepLineOf(reportLines(out), stage) == "" {
			t.Errorf("the run stopped at the Firebase stage: no %s line in\n%s", stage, out)
		}
	}
}

// TestAnUpdateThatCannotCreateTheDatabaseSaysWhoMust is R2's other arm: the
// credential may not make the project's IAM changes, or the project has no
// billing to hold a database at all. The stage changes nothing, says in one
// line who must run the command or that billing must be linked, and the rest
// of the update carries on.
func TestAnUpdateThatCannotCreateTheDatabaseSaysWhoMust(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		arm        func(*testing.T, *fbRig)
	}{
		{"the credential may not make the project's IAM changes", "a project owner runs", func(*testing.T, *fbRig) {}},
		{"the project has no billing", "billing", func(t *testing.T, r *fbRig) {
			mayCreate(t, r)
			r.billing.SetBilling(fpID, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := alwaysRig(t)
			noDatabaseYet(t, r)
			tc.arm(t, r)
			fakeTerminal(t)

			out, err := updateRun(t, r.initRig, "update")
			if err != nil {
				t.Fatalf("the update stopped instead of saying its line: %v\n%s", err, out)
			}
			// The terminal gets the brief account and the log the whole
			// sentence, which is where a line naming the permission, the
			// command and the billing is read.
			whole := out + newestLog(t, "update")
			if !strings.Contains(whole, "no budget database was created") || !strings.Contains(whole, tc.want) {
				t.Errorf("the run does not say why it created nothing:\n%s", whole)
			}
			if strings.Contains(out, "wrote the database") || r.db.Value(budget.PathMark) != nil {
				t.Errorf("the database was written by a run that may not create one:\n%s", out)
			}
			if b := r.localConfig(t).Budget; b != nil && b.RTDBURL != "" {
				t.Errorf("a database that was not created was recorded: %+v", b)
			}
			// The rest of the update carries on: the stages behind this one ran.
			for _, stage := range []string{initflow.Images, initflow.Installation2} {
				if stepLineOf(reportLines(out), stage) == "" {
					t.Errorf("the run stopped at the Firebase stage: no %s line in\n%s", stage, out)
				}
			}
		})
	}
}

// TestAManagedProjectCreatesItsDatabaseUnderTheGate is D3028 and R11: on a
// managed project the database is created like any other converging step —
// under the gate, with nobody confirming, and from a coding agent's session
// too.
func TestAManagedProjectCreatesItsDatabaseUnderTheGate(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	noDatabaseYet(t, r)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	t.Setenv("CLAUDECODE", "1")
	updating(t, r.initRig)

	out, err := updateRun(t, r.initRig, "update")
	noStepLeftBehind(t, err)
	if r.db.Value(budget.PathMark) == nil {
		t.Errorf("no database was written under the gate:\n%s", out)
	}
	if b := r.localConfig(t).Budget; b == nil || b.RTDBURL != r.db.URL {
		t.Errorf("the local config records budget %+v, want the database this run made", b)
	}
	if !strings.Contains(out, "passed the deterministic gate") {
		t.Errorf("the database's step does not say the gate decided it:\n%s", out)
	}
	if strings.Contains(out, "Type "+initProjectName) {
		t.Errorf("a managed project's own run asked a question:\n%s", out)
	}
}

// TestTheNewDatabaseStartsInObserve is F31: a database created with no mode
// chosen is seeded observe, with no config/modeSet, so doctor keeps saying the
// mode was seeded and not chosen.
func TestTheNewDatabaseStartsInObserve(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	noDatabaseYet(t, r)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.db.Value(budget.PathMode); got != budget.ModeObserve {
		t.Errorf("the new database's %s is %v, want %s", budget.PathMode, got, budget.ModeObserve)
	}
	if got := r.db.Value(budget.PathModeSet); got != nil {
		t.Errorf("the new database records %s = %v, and nobody chose the mode: a seeded mode is not a choice", budget.PathModeSet, got)
	}
}

// TestADeletedStateDoesNotLeadToASecondDatabase is F32: the Firebase state can
// be deleted while the database stays. The run asks the project and never the
// history account, so it finds a database this machine records none of, says
// so, and adopts what is there rather than creating a second one.
func TestADeletedStateDoesNotLeadToASecondDatabase(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	updating(t, r.initRig)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already has a Realtime Database") || !strings.Contains(out, "rather than creating a second one") {
		t.Errorf("the run does not say it found a database this machine records none of:\n%s", out)
	}
	if !strings.Contains(out, "adopting the existing Realtime Database") {
		t.Errorf("the Firebase root's own apply does not adopt the database that is there:\n%s", out)
	}
	if b := r.localConfig(t).Budget; b == nil || b.RTDBURL != r.db.URL {
		t.Errorf("the local config records budget %+v, want the database that is there", b)
	}
	// And no second database was made: the Firebase Management fake answers a
	// create with an error (gcpfake.FirebaseDB handles the list call only), so
	// the adoption above is the whole of what this run did to the project.
}

// TestTheStagesBehindTheFlagRunWithTheDatabase is F29: the three readers of
// --firebase-project that skipped their work without it run with the database
// now — the second installation stage, which deploys the history job, the
// history image's mirror, and the budget admins' review.
func TestTheStagesBehindTheFlagRunWithTheDatabase(t *testing.T) {
	r := newImagesRigFor(t, "1.2.3", fpID)
	r.installationState(t)
	mayCreate(t, r.fbRig)
	noDatabaseYet(t, r.fbRig)
	updating(t, r.initRig)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("a plain update did not reach the stages behind the flag: %v\n%s", err, out)
	}
	if got := r.applies(t); !slices.Contains(got, "installation") {
		t.Errorf("the second installation stage applied nothing: %v", got)
	}
	h, _ := r.tfvars(t)["history"].(map[string]any)
	if h["deploy_job"] != true || h["image"] != historyImagePath {
		t.Fatalf("the history job was not deployed with the mirrored image: %v\n%s", h, out)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] == "" {
		t.Errorf("the history image was not mirrored: %v", r.dst.tags)
	}
	// The third reader, the budget admins' review: it follows the backend's
	// presence and not the flag.
	e := rigEngine(t, r.initRig, &initOptions{door: updateDoor.name})
	if !slices.ContainsFunc(e.accessLists(t.Context()), func(a access) bool { return a.what == "budget admins" }) {
		t.Errorf("the review does not list the budget admins of an installation this run gave a database")
	}
}

// TestAKnownDatabaseSkipsTheStageAsToday is F34: an installation whose
// database this machine records does what it did — the stage is skipped, with
// its line, and nothing of the Firebase root is read or applied.
func TestAKnownDatabaseSkipsTheStageAsToday(t *testing.T) {
	r, _ := updateBudgetRigFor(t, fpID)
	fakeTerminal(t)

	out, err := updateRun(t, r, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l := stepLineOf(reportLines(out), initflow.Firebase)
	if !strings.HasPrefix(l, markSkipped+" "+initflow.Firebase) || !strings.Contains(l, "already records the budget database") {
		t.Errorf("the Firebase stage is %q, want the skip that says the database is known\n%s", l, out)
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied for a known database: %v", got)
	}
	// This rig has no Firebase project's clients at all (no Cloud Billing, no
	// Realtime Database fake): a stage that ran would fail rather than end 0,
	// which is what makes the exit 0 an assertion of its own.
}

// TestWithNothingToAnswerTheStageIsSkippedWithItsLine is F4 and R2: a run
// whose ordinary confirmation nothing would answer creates no database. It is
// the one line, and the run carries on — the stages behind the Firebase stage
// still ran, which is what the stop the line replaces would have taken away.
//
// The run is a coding agent's session at no terminal, which is F4's own case:
// a pipe or a session never gives the confirmation a creation costs, and the
// line is written where the answer is known to be absent (the stage's check)
// rather than left to the loop, which stops the whole update at a stage whose
// steps nobody can answer.
func TestWithNothingToAnswerTheStageIsSkippedWithItsLine(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	noDatabaseYet(t, r)
	noTerminal(t)
	t.Setenv("CLAUDECODE", "1")

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l := stepLineOf(reportLines(out), initflow.Firebase)
	if !strings.HasPrefix(l, markSkipped+" "+initflow.Firebase) {
		t.Errorf("the Firebase stage is %q, want the skip that says what nothing answers\n%s", l, out)
	}
	whole := out + newestLog(t, "update")
	if !strings.Contains(whole, "nothing answers this run's confirmation") || !strings.Contains(whole, "a project owner runs") {
		t.Errorf("the run does not say why it created nothing:\n%s", whole)
	}
	if r.db.Value(budget.PathMark) != nil {
		t.Errorf("a run nothing can confirm for created the database")
	}
	if plan := tfRan(t, r.initRig, "firebase", "plan"); len(plan) != 0 {
		t.Errorf("the Firebase stage ran for a run that may not create anything: %v", plan)
	}
	for _, stage := range []string{initflow.Images, initflow.Installation2} {
		if stepLineOf(reportLines(out), stage) == "" {
			t.Errorf("the run stopped at the Firebase stage: no %s line in\n%s", stage, out)
		}
	}
}

// TestTheBudgetDatabaseIsWrittenBeforeTheLocationNote is D3027: the budget
// database and the local config that records it come first, and a Firestore
// location left for a person holds back the run history and nothing else.
func TestTheBudgetDatabaseIsWrittenBeforeTheLocationNote(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	noDatabaseYet(t, r)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	t.Setenv("CLAUDECODE", "1")
	updating(t, r.initRig)

	out, err := updateRun(t, r.initRig, "update")
	noStepLeftBehind(t, err)
	if r.db.Value(budget.PathMark) == nil {
		t.Fatalf("the budget database was not written:\n%s", out)
	}
	if b := r.localConfig(t).Budget; b == nil || b.RTDBURL != r.db.URL {
		t.Errorf("the local config records budget %+v, want the database this run made", b)
	}
	if loc, _ := r.fs.Database(); loc != "" {
		t.Errorf("the Firestore database was created in %s, and this run left its location for a person", loc)
	}
	written, note := strings.Index(out, "wrote the database"), strings.Index(out, "⚠ CONFIRM (project ")
	switch {
	case written < 0:
		t.Errorf("no line says the budget database was written:\n%s", out)
	case note < 0:
		t.Errorf("the Firestore location's note is not in the run at all, so the order this test is about was never reached:\n%s", out)
	case written > note:
		t.Errorf("the location's note comes before the budget database's write:\n%s", out)
	}
}
