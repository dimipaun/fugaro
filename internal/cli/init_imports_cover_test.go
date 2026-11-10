package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// importing is c with an import of id, as show -json reports one.
func importing(c planChange, id string) planChange {
	c["change"].(map[string]any)["importing"] = map[string]any{"id": id}
	return c
}

// rootImports are the rows of the imports.tf.json in dir.
func rootImports(t *testing.T, dir string) []infra.Import {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, infra.ImportsFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Import []infra.Import `json:"import"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return doc.Import
}

// The installation root: in a marked project the run confirmation covers a
// plan whose import is exactly the one discovery wrote (the marked runs
// bucket), and not a plan that also imports something discovery never listed
// (a stale imports.tf.json, a hand edit): that step says it is not covered,
// and a person at the terminal is what applies it, with nothing typed under
// its banner (T7). Where nobody can confirm it, nothing is applied.
func TestInstallationImportsCoveredOnlyAsDiscovered(t *testing.T) {
	const runs = "module.installation.google_storage_bucket.runs"
	runsID := initProject + "/" + initRunsBucket
	const stray = "module.installation.google_service_account.scheduler"
	strayID := "projects/" + initProject + "/serviceAccounts/fugaro-scheduler@" + initProject + ".iam.gserviceaccount.com"
	strayChanges := []planChange{importing(change(runs, "no-op"), runsID), importing(change(stray, "no-op"), strayID)}
	wantStray := "not covered by the run confirmation: the plan is outside what the review announced (" + stray + " (imports " + strayID + ", which this run's discovery did not find))"

	t.Run("discovery's import", func(t *testing.T) {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		r.setPlan(t, importing(change(runs, "no-op"), runsID))
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "setup")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := rootImports(t, r.root()); len(got) != 1 || got[0] != (infra.Import{To: runs, ID: runsID}) {
			t.Fatalf("discovery wrote %v", got)
		}
		if run, step := prompts(out); run != 1 || step != 0 || strings.Contains(out, "not covered") || len(r.ran(t, "apply")) != 1 {
			t.Fatalf("%d run prompt(s), %d per-step, %d applies\n%s", run, step, len(r.ran(t, "apply")), out)
		}
	})

	t.Run("an import discovery did not write", func(t *testing.T) {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		r.setPlan(t, strayChanges...)
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "setup")
		if err != nil {
			t.Fatalf("a person at the terminal is the answer: %v\n%s", err, out)
		}
		if got := len(r.ran(t, "apply")); got != 1 {
			t.Fatalf("%d applies, want 1\n%s", got, out)
		}
		if run, step := prompts(out); run != 1 || step != 1 || !strings.Contains(out, wantStray) || !strings.Contains(out, "⚠ CONFIRM") {
			t.Fatalf("%d run prompt(s), %d per-step: want %q in\n%s", run, step, wantStray, out)
		}
	})

	// The same plan where nobody can confirm it: the step is left for the
	// user, and the installation is not applied.
	t.Run("an import discovery did not write, no terminal", func(t *testing.T) {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		r.setPlan(t, strayChanges...)
		noTerminal(t)
		out, _, err := executeStdin(t, "", "setup")
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("err = %v\n%s", err, out)
		}
		if got := len(r.ran(t, "apply")); got != 0 {
			t.Fatalf("%d applies with no terminal\n%s", got, out)
		}
	})
}

// repoPlan runs the repository root's discover, plan and confirmation
// (planRepo) for the rig's acme/sandbox after an earlier step took the run
// confirmation, its registry existing with this repository's marks (so
// discovery imports it), and the plan show -json reports being changes.
// It returns the output, the error and the workdir's root.
func repoPlan(t *testing.T, stdin string, changes func(spec infra.RepoSpec) []planChange) (string, error, *initRig, string) {
	t.Helper()
	ri := newInitRig(t)
	ri.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")
	lc, err := localcfg.Load(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: bitbucket, base_branch: master }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	r, _, out, _, _ := condition{"a terminal", initOptions{}, true, ""}.reviewRun(t, stdin, true)
	// An earlier step of the run takes the run confirmation (the first line).
	if err := r.confirmOrdinary("applies an earlier step", "nothing was applied", ""); err != nil {
		t.Fatal(err)
	}
	wd, tfr, err := r.terraform("terraform", t.TempDir(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tfr.Output(t.Context()) // the rig's installation outputs
	if err != nil {
		t.Fatal(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := infra.Repo(infra.Inputs{LC: lc, Repo: "acme/sandbox", Cfg: cfg, RepoURL: "https://bitbucket.org/acme/sandbox.git", Installation: outs})
	if err != nil {
		t.Fatal(err)
	}
	ri.ar.AddRepository(initProject, "us-east5", spec.Registry.RepositoryID, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label})
	ri.setPlan(t, changes(spec)...)
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.planRepo(t.Context(), c, tfr, wd, spec, lc, infra.RepoRootOptions{}, true)
	return out.String(), err, ri, wd.Root
}

// The repository root: the run confirmation covers a plan whose import is
// exactly the one discovery wrote (the marked registry), and not a plan that
// also imports something discovery never listed: that step says it is not
// covered, and a person at the terminal is what applies it, with nothing
// typed under its banner (T7).
func TestRepositoryImportsCoveredOnlyAsDiscovered(t *testing.T) {
	const registry = "module.repo.google_artifact_registry_repository.images"
	const stray = "module.repo.google_service_account.build"
	registryChange := func(spec infra.RepoSpec) planChange {
		return importing(planChange{"address": registry, "type": "google_artifact_registry_repository",
			"change": map[string]any{"actions": []string{"no-op"}, "before": map[string]any{}, "after": map[string]any{}}},
			"projects/"+initProject+"/locations/us-east5/repositories/"+spec.Registry.RepositoryID)
	}
	strayID := func(spec infra.RepoSpec) string {
		return "projects/" + initProject + "/serviceAccounts/" + spec.BuildServiceAccountEmail
	}

	t.Run("discovery's import", func(t *testing.T) {
		out, err, ri, root := repoPlan(t, "", func(spec infra.RepoSpec) []planChange { return []planChange{registryChange(spec)} })
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := rootImports(t, root); len(got) != 1 || got[0].To != registry {
			t.Fatalf("discovery wrote %v", got)
		}
		if _, step := prompts(out); step != 0 || strings.Contains(out, "not covered") ||
			!strings.Contains(out, "covered by the run confirmation") || len(ri.ran(t, "apply")) != 1 {
			t.Fatalf("%d per-step prompt(s), %d applies\n%s", step, len(ri.ran(t, "apply")), out)
		}
	})

	t.Run("an import discovery did not write", func(t *testing.T) {
		var id string
		out, err, ri, _ := repoPlan(t, "", func(spec infra.RepoSpec) []planChange {
			id = strayID(spec)
			return []planChange{registryChange(spec), importing(planChange{"address": stray, "type": "google_service_account",
				"change": map[string]any{"actions": []string{"no-op"}, "before": map[string]any{}, "after": map[string]any{}}}, id)}
		})
		if err != nil {
			t.Fatalf("a person at the terminal is the answer: %v\n%s", err, out)
		}
		if got := len(ri.ran(t, "apply")); got != 1 {
			t.Fatalf("%d applies, want 1\n%s", got, out)
		}
		want := "not covered by the run confirmation: the plan is outside what the review announced (" + stray + " (imports " + id + ", which this run's discovery did not find))"
		if _, step := prompts(out); step != 1 || !strings.Contains(out, want) || !strings.Contains(out, "⚠ CONFIRM") {
			t.Fatalf("%d per-step prompt(s): want %q in\n%s", step, want, out)
		}
	})
}

// The Firebase root, in the installation's own project (verified as Fugaro's
// with it): the run confirmation covers a plan whose import is exactly the
// one discovery wrote (the existing default database), and not a plan that
// also imports something discovery never listed: that step says it is not
// covered, and a person at the terminal is what applies it, with nothing
// typed under its banner (T7). Where nobody can confirm it, nothing is
// applied.
func TestFirebaseImportsCoveredOnlyAsDiscovered(t *testing.T) {
	db := infra.Import{To: "module.firebase.google_firebase_database_instance.this", ID: "projects/" + initProject + "/locations/us-central1/instances/" + initProject + "-default-rtdb"}
	stray := infra.Import{To: "module.firebase.google_service_account.signer", ID: "projects/" + initProject + "/serviceAccounts/fugaro-token-signer@" + initProject + ".iam.gserviceaccount.com"}
	strayWant := "not covered by the run confirmation: the plan is outside what the review announced (" + stray.To + " (imports " + stray.ID + ", which this run's discovery did not find))"
	run := func(t *testing.T, stdin string, imports ...infra.Import) (string, error, *fbRig) {
		t.Helper()
		r := newFBRigFor(t, initProject)
		r.markedRuns(t, initProjectName, initProject)
		r.fbPlan(t, imports, fbCreates, nil)
		out, err := r.runAdoptIn(t, stdin, "--budget-mode", "observe")
		return out, err, r
	}

	t.Run("discovery's import", func(t *testing.T) {
		out, err, r := run(t, "", db)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := r.importRows(t); len(got) != 1 || got[0] != db {
			t.Fatalf("discovery wrote %v", got)
		}
		if runP, step := prompts(out); runP != 1 || step != 0 || strings.Contains(out, "not covered") || !slices.Contains(r.applies(t), "firebase") {
			t.Fatalf("%d run prompt(s), %d per-step, applies %v\n%s", runP, step, r.applies(t), out)
		}
	})

	t.Run("an import discovery did not write", func(t *testing.T) {
		out, err, r := run(t, "", db, stray)
		if err != nil {
			t.Fatalf("a person at the terminal is the answer: %v\n%s", err, out)
		}
		if !slices.Contains(r.applies(t), "firebase") {
			t.Fatalf("firebase was left (err %v)\n%s", err, out)
		}
		if runP, _ := prompts(out); runP != 1 || !strings.Contains(out, strayWant) || !strings.Contains(out, "⚠ CONFIRM") {
			t.Fatalf("%d run prompt(s): want %q in\n%s", runP, strayWant, out)
		}
	})

	// The same plan where nobody can confirm it: the step is left for the
	// user, and the Firebase root is never applied.
	t.Run("an import discovery did not write, no terminal", func(t *testing.T) {
		r := newFBRigFor(t, initProject)
		r.markedRuns(t, initProjectName, initProject)
		r.fbPlan(t, []infra.Import{db, stray}, fbCreates, nil)
		r.historyImage()
		noTerminal(t)
		out, _, err := executeStdin(t, "", "setup", "--firebase-project", r.fp, "--budget-mode", "observe")
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("err = %v\n%s", err, out)
		}
		if slices.Contains(r.applies(t), "firebase") {
			t.Errorf("the Firebase root was applied with no terminal:\n%s", out)
		}
	})
}
