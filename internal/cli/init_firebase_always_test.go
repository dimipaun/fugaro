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
	"github.com/dimipaun/fugaro/internal/runindex"
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
// finished" (D3027 — the budget database is written before the run history's
// own, which is what these two tests are about).
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
	// The triple is self-consistent and names other-fp in all three parts, so
	// the project pin in CheckFirebaseOutputsFor is the ONLY check that can
	// refuse it: an rtdb_url naming yet another project would be refused by
	// that function's URL branch just as well, and this test would pass with
	// the pin removed, which is the one thing it exists to pin.
	r.script["output@firebase"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"rtdb_url": "https://other-fp-default-rtdb.firebaseio.com", "firebase_api_key": fpAPIKey,
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
	if !strings.Contains(out, "the gate admitted the plan") {
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
	//
	// The state bucket holds no Firebase state here, so the check's own read
	// (RI2) answers "not read" and F34's line stands: the two states that skip
	// this stage with a database recorded are this one and the indexed one, and
	// TestAConvergedInstallationWithTheIndexesSkipsTheStage is the other. The
	// one that does NOT skip it is an installation whose state does not carry
	// the whole set the module declares: none of them
	// (TestAPlainUpdateCreatesTheRunIndexesOfAConvergedInstallation), and the
	// ten of an installation the release before left one short
	// (TestAConvergedInstallationMissingOneIndexRunsTheStage).
}

// convergedFirebase puts the rig in the state a converged installation is in
// as RI2 found it: the machine's local config records the budget database (F34,
// so this stage has been skipped since AB7), the Firebase root's state object
// is in the state bucket, the Realtime Database holds the nodes an apply left
// in it, and the Firestore database — the one the run index's composite indexes
// are created under — is there.
func convergedFirebase(t *testing.T, r *fbRig) {
	t.Helper()
	r.firebaseState(t)
	r.fs.SetDatabase("us-east5")
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.db.Set(budget.PathProject, initProjectName)
	r.db.Set(budget.PathMode, budget.ModeEnforce)
	r.db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 100000, rtdb_url: "+r.db.URL+", firebase_project: \""+fpID+"\" }\n")
}

// TestAPlainUpdateCreatesTheRunIndexesOfAConvergedInstallation is RI2 at the
// door the release note names. The eleven composite indexes of the run index
// are the Firebase root's — no job and no other root creates them (F10) — and a
// plain `fugaro update` has skipped that root on every installation whose
// config records the budget database since AB7 (F34). The skip is the stage's
// own check, and it holds only while the root's state already carries one of
// them: this installation's does not, so one plain update is what applies the
// root that creates them, with the gate the module needs (the database is
// there, so its for_each is the eleven and not an empty set).
//
// Red before the check read the state: the run ended with the Firebase stage
// skipped, "the project config already records the budget database", and no
// apply in that root at all — only `fugaro update --firebase-project` reached
// it, which is a different invocation from the one the release note names.
//
// The credential is the owner's (mayCreate): the apply that creates the indexes
// is the Firebase root's, and that apply makes the project's IAM changes, so a
// run that may not make them keeps F34's skip instead
// (TestAnOperatorsPlainUpdateSkipsTheStageAndConverges).
func TestAPlainUpdateCreatesTheRunIndexesOfAConvergedInstallation(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	mayCreate(t, r)
	// The state manages nothing of the run index, and the plan the apply is made
	// from is a create in that root. The eleven themselves are the module's and
	// are pinned by the terraform-tagged TestFirebaseModulePlansRunIndexes: what
	// this test is about is that a plain update reaches the root at all.
	r.fbPlan(t, nil, nil, nil)
	r.script["plan@firebase"] = map[string]any{"exit": 2, "stdout": planJSON(t, planChange{
		"address": `module.firebase.google_firestore_index.run["role_updated_at"]`, "type": "google_firestore_index",
		"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{}}})}
	r.save(t)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("a plain update of a converged installation did not converge: %v\n%s", err, out)
	}
	if got := r.applies(t); !slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied in none of %v, so the run index's composite indexes were never created (F10)\n%s", got, out)
	}
	if v := r.fbVars(t)["firestore_database_exists"]; v != true {
		t.Errorf("the Firebase root is applied with firestore_database_exists = %v, want true: the database the indexes are created under is there (RI2)", v)
	}
}

// TestAnOperatorsPlainUpdateSkipsTheStageAndConverges is F34's own run, and the
// one the state read must not turn into a failure: the credential is an
// operator's, which the fake answers by holding none of the permissions the
// Firebase root's apply makes the project's IAM changes with (mayCreate is what
// a project owner holds). The indexes are therefore not this run's to create —
// the same installation as the test above, with a credential that may not apply
// that root — and it says so in one line and converges, exactly as the skip it
// has always taken did.
//
// Red before the skip asked this: the state read answered "no index" and the run
// took the whole Firebase flow, whose apply is the one this credential may not
// make, so an operator's update failed instead of converging.
func TestAnOperatorsPlainUpdateSkipsTheStageAndConverges(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("an operator's plain update did not converge: %v\n%s", err, out)
	}
	l := stepLineOf(reportLines(out), initflow.Firebase)
	if !strings.HasPrefix(l, markSkipped+" "+initflow.Firebase) || !strings.Contains(l, "already records the budget database") {
		t.Errorf("the Firebase stage is %q, want the skip that says the database is known\n%s", l, out)
	}
	if !strings.Contains(l+newestLog(t, "update"), "may make project") {
		t.Errorf("the run does not say the indexes wait for a credential that may make the project's IAM changes:\n%s", l+newestLog(t, "update"))
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied by a credential that may not make the project's IAM changes: %v", got)
	}
	if plan := tfRan(t, r.initRig, "firebase", "plan"); len(plan) != 0 {
		t.Errorf("a skipped stage planned the Firebase root: %v", plan)
	}
}

// runIndexAddresses is the run index's composite indexes as the Firebase root's
// state holds them once an apply has created them (RI2, runindex.Indexes).
func runIndexAddresses() []string {
	out := make([]string, 0, len(runindex.Indexes))
	for _, key := range runindex.Indexes {
		out = append(out, runindex.IndexAddress(key))
	}
	return out
}

// TestAConvergedInstallationWithTheIndexesSkipsTheStage is F34 kept: the same
// installation one update later, its Firebase root's state now carrying the
// whole set the module declares, is what the skip has always been for. The
// stage says its line, plans nothing in that root and applies nothing there.
//
// This rig has the Firebase project's fakes, so a stage that ran would be seen:
// the assertion is not "the run survived" but "the root was never reached".
func TestAConvergedInstallationWithTheIndexesSkipsTheStage(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	r.fbPlan(t, nil, nil, runIndexAddresses())
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l := stepLineOf(reportLines(out), initflow.Firebase)
	if !strings.HasPrefix(l, markSkipped+" "+initflow.Firebase) || !strings.Contains(l, "already records the budget database") {
		t.Errorf("the Firebase stage is %q, want the skip that says the state has the indexes\n%s", l, out)
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied for an installation whose state carries its indexes: %v", got)
	}
	if plan := tfRan(t, r.initRig, "firebase", "plan"); len(plan) != 0 {
		t.Errorf("a skipped stage planned the Firebase root: %v", plan)
	}
}

// TestAConvergedInstallationMissingOneIndexRunsTheStage is the row a later
// release adds. The indexes are a closed set the module declares, and an
// installation whose state carries ten of the eleven is NOT one whose update
// may skip that root: the row it lacks would never reach it, and the query that
// needs it would be refused for ever — which is the one thing `fugaro update`
// may not be. The stage therefore runs, exactly as it does for an installation
// that carries none of them.
//
// Red before the skip asked which of them the state carries (it asked whether
// it carried any): the state's ten were read as an installation that has the
// indexes, so the stage was skipped and the eleventh was never created.
func TestAConvergedInstallationMissingOneIndexRunsTheStage(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	mayCreate(t, r)
	addresses := runIndexAddresses()
	missing := runindex.Indexes[len(runindex.Indexes)-1]
	r.fbPlan(t, nil, nil, addresses[:len(addresses)-1])
	// The plan the apply is made from is the root's answer for that state: the
	// one index the state does not carry, planned as a create.
	r.script["plan@firebase"] = map[string]any{"exit": 2, "stdout": planJSON(t, planChange{
		"address": runindex.IndexAddress(missing), "type": "google_firestore_index",
		"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{}}})}
	r.save(t)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("a plain update of an installation that is one index short did not converge: %v\n%s", err, out)
	}
	if got := r.applies(t); !slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied in none of %v, so the run index's composite indexes it lacks were never created (F10)\n%s", got, out)
	}
	if v := r.fbVars(t)["firestore_database_exists"]; v != true {
		t.Errorf("the Firebase root is applied with firestore_database_exists = %v, want true: the database the indexes are created under is there (RI2)", v)
	}
}

// memberPin is the config of a project that joined a workspace, exactly as
// `fugaro workspace add` writes it: the membership key and the four values the
// pin is refused without (localcfg.validateWorkspace). The pin names no
// service address, so the run publishes no credential route and the stage this
// test is about is the only one that has something to do.
const memberPin = "workspace: workspace-alpha\n" +
	"workspace_pin:\n" +
	"  firebase_project: workspace-alpha-1234\n" +
	"  issuer: https://securetoken.google.com/workspace-alpha-1234\n" +
	"  assertion_key: marker-assertion-key\n" +
	"  service_account: fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com\n"

// memberServiceAccount is the account memberPin names: the one the root binds
// the read to.
const memberServiceAccount = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"

// TestAPlainUpdateBindsAWorkspaceMembersRead is D2988 at the door the operator
// note names: a member's own step is a plain `fugaro update`, with no flag and
// no second command. The member's config records its database (F34) and the
// Firebase root's state carries the run index's composite indexes, so RI2 has
// nothing to ask for — and the state carries no binding for the workspace
// service account the pin names, because the pin was written by `fugaro
// workspace add` and no root has been applied with it since. The skip is
// answered no on the second question and the apply that follows is the one
// that creates the binding (modules/firebase/workspace_access.tf), with the
// pin's account in its vars.
//
// Red before the check asked it: the member's plain update was skipped with
// "the project config already records the budget database", the root was never
// applied, and the workspace's service could not read that member's ledger —
// only `fugaro update --firebase-project`, which the owner's ruling of
// 2026-11-24 removed from the member's step, reached the root.
func TestAPlainUpdateBindsAWorkspaceMembersRead(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	mayCreate(t, r)
	r.appendConfig(t, memberPin)
	r.fbPlan(t, nil, nil, runIndexAddresses())
	// The plan the apply is made from is the root's answer for that state: the
	// binding the state does not carry, planned as a create.
	r.script["plan@firebase"] = map[string]any{"exit": 2, "stdout": planJSON(t, planChange{
		"address": "module.firebase.google_project_iam_member.workspace_read[0]", "type": "google_project_iam_member",
		"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{}}})}
	r.save(t)
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("a member's plain update did not converge: %v\n%s", err, out)
	}
	if got := r.applies(t); !slices.Contains(got, "firebase") {
		t.Fatalf("the Firebase root was applied in none of %v, so the workspace's read of this member's database was never bound and its workspace-served runs fail (D2988)\n%s", got, out)
	}
	if v := r.fbVars(t)["workspace_service_account"]; v != memberServiceAccount {
		t.Errorf("the Firebase root is applied with workspace_service_account = %v, want the account this member's own pin names (%s): the binding is the pin's account and nothing the workspace publishes (W7)", v, memberServiceAccount)
	}
}

// TestAConvergedMemberWithTheBindingSkipsTheStage is F34 kept for a member: one
// update later the root's state carries the binding the run above created, so
// the member's update has nothing left in that root — RI2's indexes are there
// and D2988's read is there — and the skip the installation has had since AB7
// stands. Without it every member's update would apply the Firebase root for
// ever, which is the confirmation the skip exists to save.
func TestAConvergedMemberWithTheBindingSkipsTheStage(t *testing.T) {
	r := alwaysRig(t)
	convergedFirebase(t, r)
	mayCreate(t, r)
	r.appendConfig(t, memberPin)
	r.fbPlan(t, nil, nil, append(runIndexAddresses(), "module.firebase.google_project_iam_member.workspace_read[0]"))
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l := stepLineOf(reportLines(out), initflow.Firebase)
	if !strings.HasPrefix(l, markSkipped+" "+initflow.Firebase) || !strings.Contains(l, "already records the budget database") {
		t.Errorf("the Firebase stage is %q, want the skip that says the state carries what the root declares\n%s", l, out)
	}
	if got := r.applies(t); slices.Contains(got, "firebase") {
		t.Errorf("the Firebase root was applied for a member whose state carries the binding: %v", got)
	}
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

// TestTheBudgetDatabaseIsWrittenBeforeTheFirestoreDatabase is D3027: the budget
// database and the local config that records it come first, and the run
// history's own database — the only one whose location is permanent — is
// created after them. No step confirms that location any more (LP10, D3090;
// O2): the line saying where is printed and the database is created, in this
// coding agent's session as anywhere else.
func TestTheBudgetDatabaseIsWrittenBeforeTheFirestoreDatabase(t *testing.T) {
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
	loc := fsLocation(defaultRegion)
	if got, _ := r.fs.Database(); got != loc {
		t.Errorf("the Firestore database is in %q, want %s, created with nothing asked:\n%s", got, loc, out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "⚠ CONFIRM") && strings.Contains(l, "Firestore") {
			t.Errorf("the database's location is still a note of its own:\n%s", l)
		}
	}
	line, written := strings.Index(out, "the Firestore database is created in "+loc), strings.Index(out, "wrote the database")
	switch {
	case written < 0:
		t.Errorf("no line says the budget database was written:\n%s", out)
	case line < 0:
		t.Errorf("no line says where the Firestore database is created:\n%s", out)
	case written > line:
		t.Errorf("the Firestore database's line comes before the budget database's write:\n%s", out)
	}
}
