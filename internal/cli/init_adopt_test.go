package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// installationState puts an installation's state object in the rig's state
// bucket: someone's init has applied it.
func (r *initRig) installationState(t *testing.T) {
	t.Helper()
	r.stateBucket()
	w, err := r.gcs.Bucket(t, initStateBucket).NewWriter(context.Background(), infra.StatePrefixInstallation+"/default.tfstate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// adoptEngine is the engine of a run on a machine with no local config for
// an installation that exists: the file to write is new (old is nil).
func adoptEngine(t *testing.T, r *initRig, o *initOptions) *initEngine {
	t.Helper()
	e := rigEngine(t, r, o)
	e.old = nil
	e.path = filepath.Join(r.dir, "projects", "aurora.yaml")
	return e
}

// An installation that exists, with no local config here: the installation
// stage writes the config from the installation's outputs and applies nothing
// (no plan, no apply, no bucket created), and the config keeps the
// installation's launchers and operators so a later plan does not undo them.
func TestAdoptModeWritesConfigOnly(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"group:eng@example.com"}, "operators": []string{"user:ann@example.com"}})}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if st := res.Stages[1]; st.State != initflow.Changed || !strings.Contains(st.Detail, "applied nothing") {
		t.Fatalf("stages %+v", res.Stages)
	}
	if n := len(r.ran(t, "apply")) + len(r.ran(t, "plan")); n != 0 {
		t.Errorf("adopt applied or planned: %q", r.calls(t))
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatalf("the local config was not written: %v", err)
	}
	for _, want := range []string{"name: aurora", "group:eng@example.com", "user:ann@example.com", "state_bucket: " + initStateBucket} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the local config lacks %q:\n%s", want, data)
		}
	}
	// The same rerun, now with the config there, is the ordinary converge:
	// the plan is empty and the stage says "No changes".
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	e = rigEngine(t, r, &initOptions{yes: true})
	res, err = initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options())
	if err != nil || res.Stages[1].State != initflow.Done || !strings.Contains(res.Stages[1].Detail, "No changes") {
		t.Fatalf("rerun: %v %+v", err, res.Stages)
	}
	// --plan-only adopts nothing either: it says what it would do.
	e = adoptEngine(t, r, &initOptions{planOnly: true})
	e.path = filepath.Join(r.dir, "projects", "other.yaml")
	if _, err = initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.path); err == nil {
		t.Error("--plan-only wrote the local config")
	}
}

// After adopting, the person is told which roles they may lack and the one
// line an owner runs, with the members that hold the roles today (the flags
// replace the lists).
func TestAdoptModeNamesMissingRole(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"group:eng@example.com"}, "operators": []string{}})}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	var out strings.Builder
	e.r.w = &out
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	if _, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"launcher role, held by: group:eng@example.com",
		"ask an owner to run: fugaro init --launcher group:eng@example.com --launcher user:<your-email>",
		"operator role, held by: nobody",
		"ask an owner to run: fugaro init --operator user:<your-email>",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q:\n%s", want, out.String())
		}
	}
	for _, line := range adoptNotes(infra.InstallationOutputs{}) {
		if strings.Contains(line, "\n") || strings.Contains(line, "\\") {
			t.Errorf("not one line: %q", line)
		}
	}
}

// A rerun on an installation an older init made (its config, its state, its
// marks) adopts it without a change: every stage is done or skipped, none
// changed, and the plan is empty; with the plugin already wired at this
// release the plugin stage says No changes too. The engines' plans are the
// ones they always made.
func TestAdoptOlderInstallationReportsNoChanges(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	releaseBuild(t, "0.2.0")
	// A checkout of a repository this project has not onboarded, whose
	// settings already carry the plugin at this release.
	settings := wiringCheckoutAt(t, "https://bitbucket.org/acme/sandbox.git", "")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := rigEngine(t, r, &initOptions{yes: true})
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e), newPluginStage(e), newRepositoryStage(e)}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("a rerun applied: %q", r.calls(t))
	}
	for _, s := range res.Stages {
		if s.State != "done" && s.State != "skipped" {
			t.Errorf("stage %s is %s (%s), want done or skipped", s.Name, s.State, s.Detail)
		}
		if (s.Name == "installation" || s.Name == "plugin") && !strings.Contains(s.Detail, "No changes") {
			t.Errorf("stage %s: %q does not say No changes", s.Name, s.Detail)
		}
	}
	if stateOf(res, "installation") != "done" || stateOf(res, "plugin") != "done" || stateOf(res, "repository") != "skipped" {
		t.Errorf("stages %+v", res.Stages)
	}
}

// An installation adopted in this run is not applied to from the same run:
// the person is probably a teammate without the owner's roles, and a plan from
// their empty defaults would try to undo the installation. Every stage that
// would change the cloud after the adoption is the user's, --yes or not.
func TestNothingIsAppliedAfterAnAdoptInTheSameRun(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	ran := false
	// An engine stage that would apply (the Firebase root, say).
	later := &engineStage{e: e, name: initflow.Firebase, skip: func() string { return "" },
		run: func(context.Context) error { ran = true; return nil }, left: promptLeft(initflow.Firebase, "type the name", "init")}
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e), later}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]initflow.State{}
	for _, s := range res.Stages {
		got[s.Name] = s.State
	}
	if got[initflow.Installation] != initflow.Changed || got[initflow.Firebase] != initflow.NeedsYou || ran {
		t.Fatalf("stages %+v, ran %v", res.Stages, ran)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("applied after an adopt: %q", r.calls(t))
	}
	if len(res.Left) != 1 || !strings.Contains(res.Left[0].Text, "adopted") || res.ExitCode() != 1 {
		t.Errorf("left %+v", res.Left)
	}
	// Each guarded stage refuses on its own, too.
	for _, name := range []string{initflow.Installation2, initflow.Images, initflow.Secrets, initflow.Repository} {
		if err := e.adoptGuard(name); err == nil {
			t.Errorf("%s: not refused after an adopt", name)
		}
	}
	if err := e.adoptGuard(initflow.Plugin); err != nil {
		t.Errorf("the plugin wiring changes no cloud resource: %v", err)
	}
	e.adopted = ""
	if err := e.adoptGuard(initflow.Installation2); err != nil {
		t.Errorf("a rerun is the ordinary converge: %v", err)
	}
}

// firebaseState puts the Firebase root's state object in the rig's state
// bucket: someone's init --firebase has applied it.
func (r *initRig) firebaseState(t *testing.T) {
	t.Helper()
	w, err := r.gcs.Bucket(t, initStateBucket).NewWriter(context.Background(), infra.StatePrefixFirebase+"/default.tfstate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// adoptRun adopts the rig's installation (which has the budget backend's
// history account) and returns the config it wrote and the run's output.
func adoptRun(t *testing.T, r *initRig, firebaseOutput string) (cfg, out string) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"history_service_account": historyAccount, "history_job": nil})}
	if firebaseOutput != "" {
		r.script["output@firebase"] = map[string]any{"stdout": firebaseOutput}
	}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	var buf strings.Builder
	e.r.w = &buf
	if _, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatalf("the local config was not written: %v\n%s", err, buf.String())
	}
	return string(data), buf.String()
}

// Adopt mode fills the budget section from the Firebase root's outputs, read
// only, so a teammate can use watch and runs without being an owner. None of
// it is a secret, and nothing that is not in the outputs is written.
func TestAdoptFillsTheBudgetSectionFromTheFirebaseOutputs(t *testing.T) {
	r := newFBRig(t)
	r.installationState(t)
	r.firebaseState(t)
	cfg, out := adoptRun(t, r.initRig, "")
	for _, want := range []string{"rtdb_url: " + r.db.URL, "firebase_project: " + fpID, "firebase_api_key: " + fpAPIKey, "token_signer: fugaro-token-signer@" + fpID + ".iam.gserviceaccount.com"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the local config lacks %q:\n%s", want, cfg)
		}
	}
	// Not the mode, the cap or anything else: the outputs do not say them.
	for _, not := range []string{"mode:", "per_run_usd: 1", "allowed_models", "max_run_tokens"} {
		if strings.Contains(cfg, not) {
			t.Errorf("the local config holds %q, which no output says:\n%s", not, cfg)
		}
	}
	if !strings.Contains(out, "budget") {
		t.Errorf("the run does not say it filled the budget section:\n%s", out)
	}
	// Read only: terraform ran output (and init) in the Firebase root, never apply or plan.
	for _, c := range append(r.ran(t, "apply"), r.ran(t, "plan")...) {
		t.Errorf("adopt ran %v", c)
	}
}

// With no Firebase state (no budget backend yet), or one that cannot be read,
// the section stays empty and the adopt still succeeds, saying why.
func TestAdoptWithoutFirebaseOutputs(t *testing.T) {
	r := newFBRig(t)
	r.installationState(t)
	cfg, _ := adoptRun(t, r.initRig, "")
	if strings.Contains(cfg, "rtdb_url") || strings.Contains(cfg, "firebase_api_key") {
		t.Errorf("a budget section without a Firebase state:\n%s", cfg)
	}
	// State there, outputs unusable: a warning, nothing written.
	r2 := newFBRig(t)
	r2.installationState(t)
	r2.firebaseState(t)
	cfg, out := adoptRun(t, r2.initRig, `{"rtdb_url":{"value":"not a url","type":"string"}}`)
	if strings.Contains(cfg, "rtdb_url") || !strings.Contains(out, "budget") {
		t.Errorf("unusable outputs:\n%s\n%s", cfg, out)
	}
}
