package runnertest_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner/runnertest"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// tight is the fixture's file with a stage bound short enough to run out
// inside a test, so a blocked stage ends on the run's own timeouts.stage.
const tight = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
workflows:
  app:
    commands:
      build: sh build.sh
      test: sh test.sh
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 1m, stage: 300ms, verify: 5s, finalize_reserve: 5s }
`

// layerProfile is a project layer a repository names: the same workflow the
// fixture declares, with a stage bound of its own, which is what a harness
// that seeds the project layer sets the run's timeouts.stage from.
const layerProfile = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key, review_rounds: 2 }
profiles:
  app:
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed: { command: sh test.sh, each: "{id}" }
      reports: ["build/test-results/*.xml"]
    timeouts: { total: 1m, stage: 300ms, verify: 5s, finalize_reserve: 5s }
default_profile: app
`

// layeredRepo names the layer's profile and adds the fixture's repo-only
// secret, and carries a budget key of its own: the keys the merge records are
// read back off the record, and a budget key is not a governance one, so what
// a test proves about reading them holds in every build.
const layeredRepo = `version: 1
project: aurora
gcp_project: proj-1234
budget: { per_run_usd: 9 }
workflows:
  app:
    profile: app
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
`

// TestHarnessRunsTheStagesATestScripted: the two steps the moved tests script
// a run with — an implement stage that commits, verifies and writes pr.md, and
// a review stage that answers with a verdict — drive a run to a ready pull
// request, which a test outside internal/runner reads back off the harness.
func TestHarnessRunsTheStagesATestScripted(t *testing.T) {
	h := runnertest.New(t, "", nil)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v, want a ready run", rec)
	}
	var ran []string
	for _, st := range rec.Stages {
		ran = append(ran, st.Name)
	}
	if strings.Join(ran, ",") != "implement,review" {
		t.Fatalf("stages = %v, want implement then review", ran)
	}
	if len(h.Calls()) != 2 {
		t.Fatalf("%d agent calls, want one per stage", len(h.Calls()))
	}
	pr := h.PR(t)
	if pr.Draft {
		t.Error("the pull request of a run that finished is a draft")
	}
	if !strings.Contains(pr.Body, "Adds feature.txt.") {
		t.Errorf("the pull request body came back without what the stage wrote:\n%s", pr.Body)
	}
	if len(pr.Comments) == 0 {
		t.Fatal("the run posted no report on the pull request")
	}
	if report := pr.Comments[len(pr.Comments)-1]; !strings.Contains(report, "**Cost:** model $1.50") {
		t.Errorf("the report does not carry the run's own spend:\n%s", report)
	}
}

// TestHarnessBlocksAStageUntilTheContextEnds: the blocking step is a stage
// that never answers by itself, so what ends it is the bound the run resolved
// — here the run's own timeouts.stage, which is not a governance tripwire and
// is the same in every build. The stage fails, the record carries no halt and
// no checkpoint, and both are read back off the record a package outside
// internal/runner gets: the runner's own account of a governance stop is
// assertable from there, which is what the moved tripwire tests need.
func TestHarnessBlocksAStageUntilTheContextEnds(t *testing.T) {
	h := runnertest.New(t, tight, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 300ms" {
		t.Fatalf("record = %+v, want the failed implement stage the run's own bound leaves", rec)
	}
	if len(rec.Stages) != 1 || rec.Stages[0].Name != "implement" {
		t.Fatalf("stages = %+v, want only implement", rec.Stages)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d agent calls, want only implement", len(h.Calls()))
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage cut by timeouts.stage is a governance stop: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if !strings.Contains(h.Log.String(), "stage started") {
		t.Errorf("the run log came back without the stage the run started:\n%s", h.Log.String())
	}
}

// TestHarnessSeedsTheProjectLayer: the task a test builds carries the project
// layer it resolved, and the run applies it — the layer's profile supplies the
// workflow the repository names, its stage bound is the run's own, and its
// record says the layer was applied. The policy object the record carries
// comes back too, key by key: here the repository's own budget key, which no
// layer tightened, is sourced to the branch and the run is held to it.
func TestHarnessSeedsTheProjectLayer(t *testing.T) {
	spec := runnertest.Task()
	spec.ProjectLayer = runnertest.Layer(layerProfile)
	h := runnertest.New(t, layeredRepo, spec)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 300ms" {
		t.Fatalf("record = %+v, want the stage bounded by the layer's profile", rec)
	}
	pl := rec.ProjectLayer
	if pl == nil || !pl.Applied || pl.SHA256 != config.LayerSum([]byte(layerProfile)) || pl.Generation != 3 {
		t.Fatalf("project_layer = %+v, want the layer the task carried, applied", pl)
	}
	pol := rec.Policy
	if pol == nil || pol.Sources[policy.KeyPerRunUSD] != policy.SourceBranch {
		t.Fatalf("policy = %+v, want the repository's own per-run cap sourced to the branch", pol)
	}
}
