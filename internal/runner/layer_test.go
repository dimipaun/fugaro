package runner_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// runnerLayer's profile app is the fixture's workflow app, minus its
// secrets, which are repo-only.
const runnerLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key, review_rounds: 2 }
profiles:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed: { command: sh test.sh, each: "{id}" }
      reports: ["build/test-results/*.xml"]
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
default_profile: app
`

// layeredRepo names profile app and adds the fixture's repo-only secret.
const layeredRepo = `version: 1
project: aurora
gcp_project: proj-1234
workflows:
  app:
    profile: app
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
`

func layerSpec(text string) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature",
		ProjectLayer: &task.ProjectLayer{SHA256: config.LayerSum([]byte(text)), Generation: 3, YAML: text}}
}

// Review Focus 1.
func TestRunnerRecordsTheLayer(t *testing.T) {
	h := newHarness(t, layeredRepo, layerSpec(runnerLayer))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pl := rec.ProjectLayer
	if pl == nil || !pl.Applied || pl.SHA256 != config.LayerSum([]byte(runnerLayer)) || pl.Generation != 3 {
		t.Fatalf("project_layer = %+v", pl)
	}
	want, _, ps := config.Resolve([]byte(layeredRepo), mustRunnerLayer(t))
	if len(ps) > 0 || rec.ConfigSHA256 != want.SHA256() || rec.Workflow != "app" {
		t.Fatalf("config_sha256 = %s, want %s (workflow %s)", rec.ConfigSHA256, want.SHA256(), rec.Workflow)
	}
}

// Review Focus 5: a launch from outside a checkout may carry the layer for
// a file that is not anchored; the runner ignores it and says so.
func TestRunnerIgnoresTheLayerForAnUnanchoredFile(t *testing.T) {
	h := newHarness(t, "", layerSpec(runnerLayer)) // the fixture's own fugaro.yaml: no gcp_project
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectLayer == nil || rec.ProjectLayer.Applied || rec.ConfigSHA256 == "" {
		t.Fatalf("project_layer = %+v, config_sha256 %q", rec.ProjectLayer, rec.ConfigSHA256)
	}
}

// Review Focus 3 (Task 7 follow-up): the run record still names the
// invalid layer's sha256 and generation, so ls and diagnose (Task 17) can
// show it even though it was never applied.
func TestRunnerRefusesAnInvalidLayer(t *testing.T) {
	bad := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  budget: { per_run_usd: 1 }\n"
	h := newHarness(t, layeredRepo, layerSpec(bad))
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "project layer") ||
		!strings.Contains(rec.Reason, "budget.per_run_usd may only be set in: repo") || len(h.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(h.agent.calls))
	}
	pl := rec.ProjectLayer
	if pl == nil || pl.Applied || pl.SHA256 != config.LayerSum([]byte(bad)) || pl.Generation != 3 {
		t.Fatalf("project_layer = %+v", pl)
	}
}

// Review Focus 2 (Task 7 follow-up): the layer is anchored to the job's own
// project (FUGARO_PROJECT), not just to the repository's fugaro.yaml; a
// layer published for another project is refused outright, with no
// fallback to running without it.
func TestRunnerRefusesALayerForAnotherProject(t *testing.T) {
	other := "version: 1\nproject: other\ngcp_project: proj-1234\n"
	h := newHarness(t, layeredRepo, layerSpec(other))
	h.deps.Project = "aurora"
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "project layer") ||
		!strings.Contains(rec.Reason, `project: is "other", but it is read for project "aurora"`) || len(h.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(h.agent.calls))
	}
	pl := rec.ProjectLayer
	if pl == nil || pl.Applied || pl.SHA256 != config.LayerSum([]byte(other)) {
		t.Fatalf("project_layer = %+v", pl)
	}
}

func TestRunnerWithoutALayerRecordsOnlyTheConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectLayer != nil || len(rec.ConfigSHA256) != 64 {
		t.Fatalf("project_layer = %+v, config_sha256 %q", rec.ProjectLayer, rec.ConfigSHA256)
	}
}

func mustRunnerLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte(runnerLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return l
}

// followLayer differs from runnerLayer (review_rounds 4, not 2) and carries
// its own generation: a follow-up must resolve over this text, read from
// its own task.json, never over the first run's runnerLayer (plan ruling
// L15).
const followLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key, review_rounds: 4 }
profiles:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed: { command: sh test.sh, each: "{id}" }
      reports: ["build/test-results/*.xml"]
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
default_profile: app
`

// Review Focus 1 (Task 7 follow-up): a follow-up resolves fugaro.yaml at
// the base branch over the CURRENT layer embedded in its own task.json
// (plan ruling L15), not the first run's. The two runs are given different
// layer texts (runnerLayer, then followLayer) to prove it is not reusing
// the first run's.
func TestFollowUpResolvesOverItsOwnLayer(t *testing.T) {
	h := newHarness(t, layeredRepo, layerSpec(runnerLayer))
	rec1, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec1.PR == nil || rec1.PushedHead == "" {
		t.Fatalf("first run: rec = %+v, err = %v", rec1, err)
	}

	const followID = "20260927-090000-f001"
	spec := &task.Spec{Version: 1, RunID: followID, Repo: "acme/app", Ref: "main", Workflow: rec1.Workflow, Task: "Tidy up.",
		Branch: rec1.Branch, PR: rec1.PR.Number, PreviousRun: runID,
		ProjectLayer: &task.ProjectLayer{SHA256: config.LayerSum([]byte(followLayer)), Generation: 7, YAML: followLayer}}
	store := runstore.Open(h.bucket, "acme-app", followID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	h.store, h.deps.Store = store, store
	for _, dir := range []string{h.deps.WorkDir, h.deps.StateDir} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	h.deps.Env = append(filterEnv(h.deps.Env, "HOME"), "HOME="+t.TempDir())
	h.agent = &scriptedAgent{t: t}
	h.deps.Agent = h.agent

	rec2, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pl := rec2.ProjectLayer
	if pl == nil || !pl.Applied || pl.Generation != 7 || pl.SHA256 != config.LayerSum([]byte(followLayer)) {
		t.Fatalf("project_layer = %+v", pl)
	}
	layer, ps := config.ParseProjectLayer([]byte(followLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	want, _, ps := config.Resolve([]byte(layeredRepo), layer)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if rec2.ConfigSHA256 != want.SHA256() {
		t.Fatalf("config_sha256 = %s, want %s (follow-up's own layer)", rec2.ConfigSHA256, want.SHA256())
	}
}
