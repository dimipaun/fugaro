package runner_test

import (
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

func TestRunnerRefusesAnInvalidLayer(t *testing.T) {
	bad := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  budget: { per_run_usd: 1 }\n"
	h := newHarness(t, layeredRepo, layerSpec(bad))
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "project layer") ||
		!strings.Contains(rec.Reason, "budget.per_run_usd may only be set in: repo") || len(h.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(h.agent.calls))
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
