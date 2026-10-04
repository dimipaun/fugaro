package cli

import (
	"context"
	"encoding/json"
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
	for _, line := range roleNotes(infra.InstallationOutputs{}) {
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
	settings := wiringCheckout(t, "")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.Applied || len(r.ran(t, "apply")) != 0 {
		t.Errorf("a rerun applied: %q", r.calls(t))
	}
	for _, s := range res.Stages {
		if s.State != "done" && s.State != "skipped" {
			t.Errorf("stage %s is %s (%s), want done or skipped", s.Name, s.State, s.Detail)
		}
	}
	if res.state("installation") != "done" || res.state("plugin") != "done" || res.state("repository") != "skipped" {
		t.Errorf("stages %+v", res.Stages)
	}
	for _, s := range res.Stages {
		if (s.Name == "installation" || s.Name == "plugin") && !strings.Contains(s.Detail, "No changes") {
			t.Errorf("stage %s: %q does not say No changes", s.Name, s.Detail)
		}
	}
}
