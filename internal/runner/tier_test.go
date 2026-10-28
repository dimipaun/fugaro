package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestRunRecordCarriesTheTier: the tier the launcher dispatched the run at
// travels in the task, and the runner folds it into the record at bootstrap
// with the expectation the run's own governance resolved for it
// (docs/task-governance/implementation-plan.md T8.1, D330).
//
// The repository's governance.tiers block tightens the shipped figures, so the
// numbers the block carries are the resolved expectation and not a table the
// runner keeps of its own: a record written from a copy of Fugaro's defaults
// would show $1/30m where the repository asked for $0.50/5m.
func TestRunRecordCarriesTheTier(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"governance:\n  tiers:\n    small: { expected_usd: 0.5, expected_minutes: 5 }\nagent:\n", 1)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature", Tier: tier.Small}
	h := newHarness(t, cfg, spec)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier == nil {
		t.Fatal("the record of a tiered run carries no tier block")
	}
	if rec.Tier.Tier != tier.Small || rec.Tier.ExpectedUSD != 0.5 || rec.Tier.ExpectedMinutes != 5 {
		t.Errorf("the block = %+v; want the tier the task carried and the expectation the repository's governance resolved", rec.Tier)
	}
}

// TestRunRecordWithoutATier: a run that carried no tier records no block — the
// field is absent, never a block with an empty tier — so a reader can tell a
// run nobody dispatched an expectation for from one that carries a tier.
func TestRunRecordWithoutATier(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier != nil {
		t.Fatalf("the record of an untiered run carries %+v", rec.Tier)
	}
}
