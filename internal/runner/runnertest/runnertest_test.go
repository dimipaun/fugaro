package runnertest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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

// blocked is a stage that runs until its context ends, so what a test asserts
// on is the bound the run resolved and not the agent's own work.
func blocked(_ *testing.T, ctx context.Context, _ agent.Request) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{CostUSD: 1}, ctx.Err()
}

// TestHarnessRunsOneStage: the harness drives a real run of the fixture
// repository, so a package outside internal/runner can assert on the record
// the runner wrote, the run log and the stages that ran. The bound below is
// the run's own timeouts.stage: what the stage ends on is not the subject
// here, only that the run happened and its record and log came back.
func TestHarnessRunsOneStage(t *testing.T) {
	h := runnertest.New(t, tight, nil)
	rec, err := h.Run(t, blocked)
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
	if !strings.Contains(h.Log.String(), "stage started") {
		t.Errorf("the run log came back without the stage the run started:\n%s", h.Log.String())
	}
}
