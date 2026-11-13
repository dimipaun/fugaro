package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
)

// update_test.go is `fugaro update`'s own behaviour: the door that re-converges
// what exists, creates nothing, and — D12's answer of 2026-10-20 — covers every
// step it owns under --yes. The stage-level rows are setup_refusals_test.go's;
// the four call sites D12 moves are asserted here, at the functions that take
// the steps.

// updateRig is a rig with an installation already there (the state bucket, and
// a plan that reads back empty, so a converge of it changes nothing), run
// outside any checkout: `update`'s own stages are what is under test here, and
// a checkout adds the plugin and repository stages, which have their own
// files.
func updateRig(t *testing.T) *initRig {
	t.Helper()
	releaseBuild(t, "0.2.0")
	r := newInitRig(t)
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	t.Chdir(t.TempDir())
	return r
}

// TestUpdateRefusesCreateProjectAndLinkBilling: the two steps that may never be
// relaxed — creating the project and linking the billing account — are
// `setup`'s, and `update` refuses each by name, before any cloud call, in the
// sentence that names the door that owns it. The project stage itself is not
// in update's run at all: what a door does not own it cannot take.
func TestUpdateRefusesCreateProjectAndLinkBilling(t *testing.T) {
	for _, tc := range []struct{ name, arg, flag string }{
		{"creating the project", "--create-project", "--create-project"},
		{"linking the billing account", "--link-billing=0123AB-4567CD-89EF01", "--link-billing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateRig(t)
			out, errOut, err := executeStdin(t, "", "update", tc.arg, "--yes")
			if err == nil {
				t.Fatalf("fugaro update %s exited 0:\n%s", tc.flag, out)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			// The sentence itself, not the two words in it: another guard
			// about --create-project also says "fugaro setup" (the missing
			// --gcp-project it needs), so a door that stopped refusing the flag
			// by name would still satisfy a looser check — which is the bug
			// this arm is for.
			got := out + errOut + ErrorText(err)
			want := "fugaro update does not take " + tc.flag + ": that is fugaro setup's"
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not name the flag and the door that owns it: %q, want %q in it", got, want)
			}
			noCloudTouched(t, r, "fugaro update "+tc.flag)
		})
	}
	e := rigEngine(t, newInitRig(t), &initOptions{door: updateDoor.name})
	if _, ok := stageNamed(initStages(e), initflow.Project); ok {
		t.Error("update registers the project stage")
	}
}

// TestUpdateWithoutAnInstallationNamesSetup: `update` creates nothing, so a
// state bucket that is not there is no installation to re-converge. The one
// refusal names the bucket it looked for and the door that makes one, and
// nothing is created on the way to it — no apply, no bucket write.
func TestUpdateWithoutAnInstallationNamesSetup(t *testing.T) {
	releaseBuild(t, "0.2.0")
	r := newInitRig(t) // no state bucket: nothing was ever installed here
	t.Chdir(t.TempDir())
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil {
		t.Fatalf("fugaro update without an installation exited 0:\n%s", out)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "there is no Terraform state bucket gs://"+initStateBucket) || !strings.Contains(got, "fugaro setup") {
		t.Errorf("the refusal does not name the state bucket and the door that makes one: %q", got)
	}
	for _, call := range r.calls(t) {
		// The environment check's own `terraform version -json` is the only
		// call a run with nothing to converge may make.
		if len(call) == 0 || call[0] != "version" {
			t.Errorf("terraform ran for a run with no installation: %q", call)
		}
	}
	for _, rq := range r.gcs.Requests() {
		if rq.Method != "GET" {
			t.Errorf("update wrote where it found no installation: %s %s", rq.Method, rq.Path)
		}
	}
}

// TestUpdateLeavesAnUnenrolledRepositoryAloneAndExitsOne: a checkout whose
// repository the project does not list is not update's to enrol **by asking**
// (D5). Update's converge never puts the typed owner/name question and never
// offers --onboard-repo, so the run states in one clause that the repository
// is not the project's, leaves the step for the user, exits 1, and never puts
// the typed owner/name: there is nothing for this run to type. The door that
// does enrol it (`fugaro setup`) is the left-for-you line, which the run
// reaches only at that stage — the second half of this test, over the stages
// themselves, pins that line, because an apply that stops at the installation
// prints no left-for-you at all (report.Brief keeps one clause of a step's
// reason; the whole of it is in the log the run names). --yes is a different
// matter, and TestUpdateAppliesTheFourStepsItOwns pins it: D12 makes the step
// a covered one, which is why the sentence above says "by asking".
func TestUpdateLeavesAnUnenrolledRepositoryAloneAndExitsOne(t *testing.T) {
	// The rig of an installation that converges with nothing to do, not a bare
	// one: this test has to reach the stages that work on the checkout for its
	// assertions to see what they print. With a plan that fails, the run stops
	// at the installation, left_for_you is empty and the --onboard-repo arm
	// below passes without ever having a line to look at — which is how the
	// secrets stage kept offering the flag after the other two stopped.
	r := updateRig(t)
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, ""))
	t.Chdir(dir)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("fugaro update onboarded an unlisted repository:\n%s", out)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	// The plan view names the repository stage and says why it is left, and the
	// plan view is the run's own account of itself: it is in the log, and the
	// screen is the steps (§9.2).
	all := out + errOut + ErrorText(err) + newestLog(t, "update")
	if !strings.Contains(all, "is not one of project "+initProjectName+"'s yet") {
		t.Errorf("the run does not say the checkout's repository is not the project's:\n%s", all)
	}
	if strings.Contains(all, "Type acme/app") {
		t.Errorf("update asked the typed repository question:\n%s", all)
	}
	if strings.Contains(all, "--onboard-repo") {
		t.Errorf("update offered the opt-in flag on a step it does not own:\n%s", all)
	}
	if calls := r.ran(t, "apply"); len(calls) != 0 {
		t.Errorf("an apply ran:\n%q", calls)
	}

	// The two stages that work on the checkout's repository — secrets and
	// repository — leave ONE line each in ONE result, and it is the same line. A
	// run that offered --onboard-repo at one of them would be telling the reader
	// to pass a flag its own door refuses to put, beside the other stage naming
	// the door that asks; the human run above cannot see that, because a run
	// whose apply stops at the installation prints no left-for-you at all.
	//
	// The plugin stage is no longer one of them (D955, U8): the plugin it
	// installs is this machine's own, the same one for every checkout, so here —
	// a checkout whose repository the machine's config does not list, which is
	// the state the red step names — it has the install to make and no line
	// naming fugaro setup. The checkout's own gate is read only where the
	// checkout carries a pin the removal would take out, and a gate authorises a
	// write: this stage makes none here.
	//
	// A plan-only run is the one that reaches every stage of the list (a
	// needs-you stage blocks the ones behind it otherwise), and it confirms
	// nothing: conditions() reads --yes as false under --plan-only.
	e, _ := stageEngine(t, "", &initOptions{door: updateDoor.name, planOnly: true})
	unknownRepo(e)
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{newSecretsStage(e), newPluginStage(e), rs}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{initflow.Secrets, initflow.Repository} {
		if stateOf(res, name) != initflow.NeedsYou {
			t.Fatalf("%s is %+v, want needs-you: the repository is not the project's and this door does not ask", name, res.Stages)
		}
		lf, ok := leftOf(res, name)
		if !ok {
			t.Fatalf("%s left nothing for the user: %+v", name, res.Left)
		}
		if lf.Kind != initflow.LeftCommand || !strings.HasSuffix(lf.Text, " setup") {
			t.Errorf("%s leaves %+v, want one command naming the door that enrols", name, lf)
		}
	}
	if len(res.Left) != 2 {
		t.Errorf("the run left %d lines, want one per stage that asked: %+v", len(res.Left), res.Left)
	}
	if got := stageDetailOf(res, initflow.Plugin); !strings.Contains(got, "installs the plugin in Claude Code") || strings.Contains(got, "setup") {
		t.Errorf("the plugin stage does not install the machine's own plugin in a checkout the machine's config does not list: %q", got)
	}
	if _, ok := leftOf(res, initflow.Plugin); ok {
		t.Errorf("the plugin stage left something for the user: %+v", res.Left)
	}
}

// stageDetailOf is one stage's own line out of a converge result, "" when the
// run stopped before reaching it.
func stageDetailOf(res *initflow.Result, name string) string {
	for _, s := range res.Stages {
		if s.Name == name {
			return s.Detail
		}
	}
	return ""
}

// TestUpdateUnderYesCoversEveryStageItOwns: `update --yes` is total over its
// own stage set (D12): every stage it registers is planned and applied or
// skipped with no typed confirmation, nothing is left for the user and the run
// exits 0. The project stage is not in it, and neither is the Batch backend's.
func TestUpdateUnderYesCoversEveryStageItOwns(t *testing.T) {
	r := updateRig(t)
	out, _, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	var names []string
	for _, st := range res.Stages {
		names = append(names, st.Name)
		switch st.State {
		case "needs-you", "blocked", "failed":
			t.Errorf("%s is %s under --yes: %s", st.Name, st.State, st.Detail)
		}
	}
	if len(res.Left) != 0 {
		t.Errorf("--yes left something for the user: %+v", res.Left)
	}
	for _, want := range []string{initflow.Preflight, initflow.Installation, initflow.Firebase, initflow.Images,
		initflow.Installation2, initflow.Secrets, initflow.Plugin, initflow.Repository} {
		if !contains(names, want) {
			t.Errorf("update did not run %s: %v", want, names)
		}
	}
	for _, never := range []string{initflow.Project, initflow.Compute} {
		if contains(names, never) {
			t.Errorf("update ran %s, which it does not register: %v", never, names)
		}
	}
	if calls := r.ran(t, "apply"); len(calls) != 0 {
		t.Errorf("a run whose plan has nothing to do applied something: %q", calls)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestUpdateAppliesTheFourStepsItOwns is D12's call-site half, at the four
// functions that take the steps. `update --yes` covers all four: the Firestore
// database's location, a billable first image build, replacing a registry tag
// and onboarding an unlisted repository. `setup` keeps exactly one of them —
// the location, the door that asks for it as a first-time question — and the
// other three are covered in both doors, which is the answer's own row-by-row
// statement of what moves.
//
// Planting the bug in either direction fails it: a call site relaxed for
// `setup` (the location) fails the setup arm here, and one left Typed for
// `update` fails the update arm — for the build and the tag, whose arms run
// both doors, that is the same mutation seen twice.
func TestUpdateAppliesTheFourStepsItOwns(t *testing.T) {
	t.Run("the Firestore location", func(t *testing.T) {
		for _, tc := range []struct {
			door    string
			covered bool
		}{{setupDoor.name, false}, {updateDoor.name, true}} {
			t.Run(tc.door, func(t *testing.T) {
				e, out := stageEngine(t, infra.FirestoreLocation+"\n", &initOptions{door: tc.door, yes: true, firebase: "aurora-fp"})
				err := e.r.confirmLocation()
				if !tc.covered {
					// The class is Typed for `setup`, so --yes confirms nothing
					// here and the run leaves the permanent location for the
					// user: T7, nothing is typed at it, and the banner below is
					// the note, which names the location.
					if err == nil || !strings.Contains(err.Error(), "the Firestore database's permanent location") {
						t.Fatalf("setup confirmed the permanent location: %v\n%s", err, out)
					}
					if !strings.Contains(out.String(), infra.FirestoreLocation) {
						t.Errorf("the setup banner does not name the location it leaves:\n%s", out)
					}
					if strings.Contains(out.String(), "confirmed by --yes") {
						t.Errorf("setup --yes confirmed the location:\n%s", out)
					}
					return
				}
				if err != nil {
					t.Fatalf("update --yes did not cover the location: %v\n%s", err, out)
				}
				if !strings.Contains(out.String(), "confirmed by --yes") {
					t.Errorf("the covered step does not say how it was confirmed:\n%s", out)
				}
			})
		}
	})

	t.Run("a billable first image build", func(t *testing.T) {
		cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {}}}
		spec := infra.RepoSpec{Name: "acme/app", Slug: "acme-app", RegistryPath: "r",
			Workflows: map[string]infra.WorkflowSpec{"app": {}}}
		lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			BaseImages: map[string]string{"base": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3"}}
		// Covered in BOTH front doors (D12: "become ordinary covered rows in
		// both front doors"), so the arm that plants the bug is the one that
		// leaves it Typed.
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			t.Run(door+" --yes", func(t *testing.T) {
				fb := useFakeBuilder(t)
				fakeTerminal(t)
				e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: door, yes: true})
				e.r.setProject(lc)
				built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
				if err != nil || built != 1 || len(fb.specs) != 1 {
					t.Fatalf("%s --yes did not build: built %d, err %v\n%s", door, built, err, out)
				}
				if !strings.Contains(out.String(), "confirmed by --yes") {
					t.Errorf("the covered build does not say how it was confirmed:\n%s", out)
				}
				if len(e.r.buildsLeft) != 0 {
					t.Errorf("a covered build was left for the user: %v", e.r.buildsLeft)
				}
			})
		}
		// A terminal with no --yes types it, in either door.
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			fb := useFakeBuilder(t)
			fakeTerminal(t)
			e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: door})
			e.r.setProject(lc)
			built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
			if err != nil || built != 1 || len(fb.specs) != 1 || strings.Contains(out.String(), "confirmed by --yes") {
				t.Errorf("%s at a terminal: built %d, err %v\n%s", door, built, err, out)
			}
		}
		// A run that cannot take it — no terminal and no --yes — leaves the
		// build, in either door, and the warning names --yes as the way to
		// give it (it is one now).
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			t.Run(door+" with no terminal", func(t *testing.T) {
				noTerminal(t)
				fb := useFakeBuilder(t)
				e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: door})
				e.r.setProject(lc)
				built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
				if err != nil || built != 0 || len(fb.specs) != 0 {
					t.Fatalf("%s with no terminal and no --yes built: built %d, err %v\n%s", door, built, err, out)
				}
				if len(e.r.buildsLeft) != 1 || !strings.Contains(out.String(), "is billable and was not confirmed") || !strings.Contains(out.String(), "and --yes does") {
					t.Errorf("the run does not leave the build, saying --yes gives it: %v\n%s", e.r.buildsLeft, out)
				}
			})
		}
		// Except a coding agent's session, which reaches this warning in a
		// --repo run (nothing gates the build in front of it there) and whose
		// own session refuses --yes: naming it would be a recipe for getting
		// past the check, in text the agent reads (AgentAdvice). The clause is
		// chosen by the marker, as convergeOptIn's is.
		t.Run("a coding agent's session is not handed --yes", func(t *testing.T) {
			noTerminal(t)
			fb := useFakeBuilder(t)
			e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: setupDoor.name})
			// stageEngine clears the markers (the test session may be an
			// agent's), so the marker is set after it, where the run reads it.
			t.Setenv("CLAUDECODE", "1")
			e.r.setProject(lc)
			built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
			if err != nil || built != 0 || len(fb.specs) != 0 {
				t.Fatalf("built %d, err %v\n%s", built, err, out)
			}
			if !strings.Contains(out.String(), "is billable and was not confirmed") {
				t.Fatalf("the run does not leave the build:\n%s", out)
			}
			if strings.Contains(out.String(), "--yes") {
				t.Errorf("the agent's session is handed --yes, which its own session refuses:\n%s", out)
			}
		})
	})

	t.Run("replacing a registry tag", func(t *testing.T) {
		pl := []planned{{item: imageItem{label: "base", kind: "base", dst: mirror.Ref{Host: "h", Repo: "r", Tag: "t"}},
			plan: &mirror.Plan{Digest: "sha256:" + strings.Repeat("d", 64), DestDigest: "sha256:" + strings.Repeat("e", 64)}}}
		// Covered in BOTH front doors, as the build is.
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			t.Run(door+" --yes", func(t *testing.T) {
				e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: door, yes: true})
				st := &imagesStage{e: e}
				if err := st.confirmReplace(pl); err != nil {
					t.Fatalf("%s --yes did not cover the replacement: %v\n%s", door, err, out)
				}
				if !strings.Contains(out.String(), "confirmed by --yes") {
					t.Errorf("the covered replacement does not say how it was confirmed:\n%s", out)
				}
			})
		}
		// And a run that cannot take it leaves it, in either door: nothing is
		// typed at the step any more (T7), so what is left is the command
		// whose own confirmation it takes.
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			e, out := stageEngine(t, initProjectName+"\n", &initOptions{door: door})
			st := &imagesStage{e: e}
			err := st.confirmReplace(pl)
			if err == nil || !strings.Contains(err.Error(), "asks you nothing and replaces base") {
				t.Fatalf("%s replaced a tag with no confirmation: %v\n%s", door, err, out)
			}
		}
	})

	t.Run("onboarding an unlisted repository", func(t *testing.T) {
		dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, ""))
		for _, door := range []string{setupDoor.name, updateDoor.name} {
			t.Run(door+" --yes", func(t *testing.T) {
				t.Chdir(dir)
				fakeTerminal(t)
				e, out := stageEngine(t, "", &initOptions{door: door, yes: true, githubAppID: "12345"})
				unknownRepo(e)
				ran := false
				rs := newRepositoryStage(e)
				rs.engineStage.run = func(context.Context) error { ran = true; return nil }
				res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
				if err != nil || !ran || stateOf(res, "repository") != initflow.Done {
					t.Fatalf("%s --yes did not onboard an unlisted repository: %v ran %v %+v\n%s", door, err, ran, res.Stages, out)
				}
				if strings.Contains(out.String(), "Type acme/app") {
					t.Errorf("%s --yes asked the typed question:\n%s", door, out)
				}
			})
		}
		// The covered case is an origin whose identity is unambiguous: a host
		// that is not a provider's is the gate's own question, and --yes cannot
		// answer it.
		t.Chdir(repoCheckout(t, "https://evil.example/acme/app.git", checkoutYAML("github", "oauth", initProjectName, "")))
		e, _ := stageEngine(t, "", &initOptions{yes: true, githubAppID: "12345"})
		rs := newRepositoryStage(e)
		rs.engineStage.run = neverRun(t)
		res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
		if err != nil || stateOf(res, "repository") != initflow.NeedsYou {
			t.Fatalf("%v %+v", err, res.Stages)
		}
	})
}

// TestUpdateNonInteractiveWithoutYesPlansOnly: `update --non-interactive`
// without --yes checks and plans every stage and applies nothing — the rule the
// loop has always had, and the mode a CI job reads before it decides to pass
// --yes.
func TestUpdateNonInteractiveWithoutYesPlansOnly(t *testing.T) {
	r := updateRig(t)
	out, _, err := executeStdin(t, "", "update", "--non-interactive", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.Applied || len(r.ran(t, "apply")) != 0 {
		t.Errorf("--non-interactive without --yes applied something: %+v, calls %q", res, r.calls(t))
	}
	for _, st := range res.Stages {
		switch st.State {
		case "done", "will-do", "skipped":
		default:
			t.Errorf("%s is %s, want a plan and nothing applied: %s", st.Name, st.State, st.Detail)
		}
	}
}

// TestUpdateConvergesTwiceAndSaysNoChanges: an installation update has nothing
// left to do converges saying "No changes" and exits 0, twice: the second run
// is the first one's own state, not a weaker check.
func TestUpdateConvergesTwiceAndSaysNoChanges(t *testing.T) {
	r := updateRig(t)
	for i := 1; i <= 2; i++ {
		out, _, err := executeStdin(t, "", "update", "--yes")
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
		// The step line is the screen's; "No changes" is what the run says about
		// the step it decided not to take, so it is read where the run keeps it —
		// and -v is what brings it back to the terminal (§9.2).
		whole := out + newestLog(t, "update")
		if !strings.Contains(whole, markDone+" done") || !strings.Contains(whole, "No changes") {
			t.Errorf("run %d does not report a converged installation:\n%s", i, whole)
		}
		if strings.Contains(out, markLeft+" ") || strings.Contains(out, markWaiting+" ") {
			t.Errorf("run %d leaves something for the user:\n%s", i, out)
		}
	}
	if calls := r.ran(t, "apply"); len(calls) != 0 {
		t.Errorf("a run with nothing to do applied: %q", calls)
	}
}

// TestUpdateNeverCreatesAnything: the project-creation path is never reached by
// `update`, whatever the run is given: Cloud Resource Manager's create is not
// called, and no project appears in the project it was pointed at.
func TestUpdateNeverCreatesAnything(t *testing.T) {
	r := updateRig(t)
	before := len(r.crm.Requests())
	if out, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, rq := range r.crm.Requests()[before:] {
		if rq.Method == "POST" && strings.Contains(rq.Path, "projects") {
			t.Errorf("update created a project: %s %s", rq.Method, rq.Path)
		}
	}
}

// TestUpdateConvergesAnInstallationAtThePreviousRelease: the HARD RULE — one
// `fugaro update` is the whole upgrade — at the change M12 made to the
// installation's module. An installation applied by the release behind this one
// is converged by one run, and what that run renders serves exactly what the old
// one served: no front door.
//
// The door's opt-in is the module's own `var.mcp_server`
// (deploy/terraform/gcp/modules/installation/variables.tf), and **no writer in
// this CLI sets it**: D934's answer is that an installation has a front door
// only once its operator asked for one, so a run that wrote the variable would
// be a release that mounted a public endpoint on every installation at once.
// The variable is read by the module as the presence of the door's whole
// configuration — there is deliberately no second switch
// (ee/coordinator/service.go) — and the module renders nothing under it, so the
// convergence this test measures is that absence.
//
// Red before M12 shipped the module change with the wiring: a CLI that turned
// the door on for every installation, which is the mistake a release makes once
// and cannot take back — the endpoint is public the moment the binding lands.
func TestUpdateConvergesAnInstallationAtThePreviousRelease(t *testing.T) {
	r, _ := updateBudgetRig(t)
	vars := filepath.Join(r.root(), infra.VarsFile)
	out, _, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("one fugaro update did not converge an installation at the previous release: %v\n%s", err, out)
	}
	b, err := os.ReadFile(vars)
	if err != nil {
		t.Fatalf("the converged run wrote no %s, so there is no plan to read what it serves: %v", infra.VarsFile, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		t.Fatalf("the converged run wrote an empty %s:\n%s", infra.VarsFile, out)
	}
	// And the file is the plan's own variables and not some other file the run
	// wrote: a reader of the assertion below has to know it read the installation
	// root's plan rather than an empty document.
	if !strings.Contains(string(b), `"project"`) {
		t.Fatalf("%s does not carry the installation's own variables, so it is not the plan this run rendered:\n%s", infra.VarsFile, b)
	}
	if strings.Contains(string(b), "mcp_server") {
		t.Errorf("fugaro update turned the front door on for an installation that never asked for one (D934: off is the absence of the opt-in), and the front door is a public endpoint the moment it is served:\n%s", b)
	}
}
