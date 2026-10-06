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
// bucket), and not a plan that also imports something discovery never
// listed (a stale imports.tf.json, a hand edit): that apply asks its own
// typed name and says why.
func TestInstallationImportsCoveredOnlyAsDiscovered(t *testing.T) {
	const runs = "module.installation.google_storage_bucket.runs"
	runsID := initProject + "/" + initRunsBucket
	const stray = "module.installation.google_service_account.scheduler"
	strayID := "projects/" + initProject + "/serviceAccounts/fugaro-scheduler@" + initProject + ".iam.gserviceaccount.com"

	t.Run("discovery's import", func(t *testing.T) {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		r.setPlan(t, importing(change(runs, "no-op"), runsID))
		fakeTerminal(t)
		out, _, err := executeStdin(t, names(1), "init")
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
	for name, tc := range map[string]struct {
		stdin string
		apply int
	}{"its own answer": {names(3), 1}, "only the review's": {names(1), 0}} {
		t.Run("an import discovery did not write/"+name, func(t *testing.T) {
			r := newInitRig(t)
			r.markedRuns(t, initProjectName, initProject)
			r.setPlan(t, importing(change(runs, "no-op"), runsID), importing(change(stray, "no-op"), strayID))
			fakeTerminal(t)
			out, _, err := executeStdin(t, tc.stdin, "init")
			if got := len(r.ran(t, "apply")); got != tc.apply {
				t.Fatalf("%d applies, want %d (err %v)\n%s", got, tc.apply, err, out)
			}
			want := "not covered by the run confirmation: the plan is outside what the review announced (" + stray + " (imports " + strayID + ", which this run's discovery did not find))"
			if run, _ := prompts(out); run != 1 || !strings.Contains(out, want) || !strings.Contains(out, "⚠ CONFIRM") {
				t.Fatalf("want %q in\n%s", want, out)
			}
		})
	}
}

// repoPlan runs the repository root's discover, plan and confirmation
// (planRepo) for the rig's acme/sandbox after an earlier step took the run
// confirmation, its registry existing with this repository's marks (so
// discovery imports it), and the plan show -json reports being changes.
// It returns the output, the error and the workdir's root.
func repoPlan(t *testing.T, stdin string, changes func(spec infra.RepoSpec) []planChange) (string, error, *initRig, string) {
	t.Helper()
	ri := newInitRig(t)
	ri.appendConfig(t, "base_images: {web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc}\n")
	lc, err := localcfg.Load(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: bitbucket, base_branch: master }\nworkflows:\n  web: { base: web-node, commands: { build: sh b.sh, test: sh t.sh } }\n"))
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
	_, err = r.planRepo(t.Context(), c, tfr, wd, spec, infra.RepoRootOptions{}, true)
	return out.String(), err, ri, wd.Root
}

// The repository root: the run confirmation covers a plan whose import is
// exactly the one discovery wrote (the marked registry), and not a plan that
// also imports something discovery never listed.
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
		out, err, ri, root := repoPlan(t, names(1), func(spec infra.RepoSpec) []planChange { return []planChange{registryChange(spec)} })
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := rootImports(t, root); len(got) != 1 || got[0].To != registry {
			t.Fatalf("discovery wrote %v", got)
		}
		if run, step := prompts(out); run != 1 || step != 0 || strings.Contains(out, "not covered") || len(ri.ran(t, "apply")) != 1 {
			t.Fatalf("%d run prompt(s), %d per-step, %d applies\n%s", run, step, len(ri.ran(t, "apply")), out)
		}
	})
	for name, tc := range map[string]struct {
		stdin string
		apply int
	}{"its own answer": {names(2), 1}, "only the review's": {names(1), 0}} {
		t.Run("an import discovery did not write/"+name, func(t *testing.T) {
			var id string
			out, err, ri, _ := repoPlan(t, tc.stdin, func(spec infra.RepoSpec) []planChange {
				id = strayID(spec)
				return []planChange{registryChange(spec), importing(planChange{"address": stray, "type": "google_service_account",
					"change": map[string]any{"actions": []string{"no-op"}, "before": map[string]any{}, "after": map[string]any{}}}, id)}
			})
			if got := len(ri.ran(t, "apply")); got != tc.apply || (err != nil) != (tc.apply == 0) {
				t.Fatalf("%d applies, want %d (err %v)\n%s", got, tc.apply, err, out)
			}
			want := "not covered by the run confirmation: the plan is outside what the review announced (" + stray + " (imports " + id + ", which this run's discovery did not find))"
			if run, _ := prompts(out); run != 1 || !strings.Contains(out, want) || !strings.Contains(out, "⚠ CONFIRM") {
				t.Fatalf("want %q in\n%s", want, out)
			}
		})
	}
}

// The Firebase root, in the installation's own project (verified as Fugaro's
// with it): the run confirmation covers a plan whose import is exactly the
// one discovery wrote (the existing default database), and not a plan that
// also imports something discovery never listed.
func TestFirebaseImportsCoveredOnlyAsDiscovered(t *testing.T) {
	db := infra.Import{To: "module.firebase.google_firebase_database_instance.this", ID: "projects/" + initProject + "/locations/us-central1/instances/" + initProject + "-default-rtdb"}
	stray := infra.Import{To: "module.firebase.google_service_account.signer", ID: "projects/" + initProject + "/serviceAccounts/fugaro-token-signer@" + initProject + ".iam.gserviceaccount.com"}
	run := func(t *testing.T, stdin string, imports ...infra.Import) (string, error, *fbRig) {
		t.Helper()
		r := newFBRigFor(t, initProject)
		r.markedRuns(t, initProjectName, initProject)
		r.fbPlan(t, imports, fbCreates, nil)
		out, err := r.runAdoptIn(t, stdin, "--budget-mode", "observe")
		return out, err, r
	}

	t.Run("discovery's import", func(t *testing.T) {
		out, err, r := run(t, names(1)+infra.FirestoreLocation+"\n", db)
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
	for name, tc := range map[string]struct {
		stdin   string
		applied bool
	}{"its own answer": {names(2) + infra.FirestoreLocation + "\n", true}, "only the review's": {names(1), false}} {
		t.Run("an import discovery did not write/"+name, func(t *testing.T) {
			out, err, r := run(t, tc.stdin, db, stray)
			if got := slices.Contains(r.applies(t), "firebase"); got != tc.applied {
				t.Fatalf("firebase applied %v, want %v (err %v)\n%s", got, tc.applied, err, out)
			}
			want := "not covered by the run confirmation: the plan is outside what the review announced (" + stray.To + " (imports " + stray.ID + ", which this run's discovery did not find))"
			if runP, _ := prompts(out); runP != 1 || !strings.Contains(out, want) || !strings.Contains(out, "⚠ CONFIRM") {
				t.Fatalf("want %q in\n%s", want, out)
			}
		})
	}
}
