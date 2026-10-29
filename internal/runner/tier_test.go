package runner_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
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

// TestRunRecordCarriesTheShippedTierDefault: a repository that writes no
// governance.tiers block — the common case, and every repository that never
// heard of tiers — dispatches under the figures Fugaro ships for that tier
// (policy.DefaultTierExpectations: $1 and 30 minutes for small). The project
// layer's block is the one layer that may loosen the defaults and every layer
// above it may only tighten, so the defaults are the ceiling a run starts
// under and not something a repository has to repeat.
//
// A record written from the zero expectation instead says the run is expected
// to cost nothing and take no time, which is not a decision anybody made: it
// is the absence of one, and T8.2's expected-cost checkpoint would stop such a
// run at once.
func TestRunRecordCarriesTheShippedTierDefault(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature", Tier: tier.Small}
	h := newHarness(t, "", spec)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier == nil {
		t.Fatal("the record of a tiered run carries no tier block")
	}
	want := policy.DefaultTierExpectations().Small
	if rec.Tier.Tier != tier.Small || rec.Tier.ExpectedUSD != want.ExpectedUSD || rec.Tier.ExpectedMinutes != want.ExpectedMinutes {
		t.Errorf("the block = %+v, want small at the shipped $%v/%vm for a repository that writes no governance.tiers",
			rec.Tier, want.ExpectedUSD, want.ExpectedMinutes)
	}
}

// TestRunRecordIgnoresALooserTierInTheRepository: with no project layer the
// ceiling carries Fugaro's own figures, so the repository's file — the branch
// the run checks out, which the agent can edit mid-run — may only tighten
// them. An expectation above the shipped one is dropped, recorded as one key
// of policy.ignored and warned about, exactly as a looser budget key is, and
// the record carries the figure the run is actually held to ($1/30m for
// small).
//
// The defaults have to be in the ceiling for that to happen at all: with a
// ceiling that carried none, the repository's own $2 would be the first value
// written for the key and would win with it, silently inverting tighten-only
// for a whole tier (T7.1, D331).
func TestRunRecordIgnoresALooserTierInTheRepository(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"governance:\n  tiers:\n    small: { expected_usd: 2 }\nagent:\n", 1)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature", Tier: tier.Small}
	h := newHarness(t, cfg, spec)
	var logs lockedBuf
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	want := policy.DefaultTierExpectations().Small
	if rec.Tier == nil || rec.Tier.ExpectedUSD != want.ExpectedUSD || rec.Tier.ExpectedMinutes != want.ExpectedMinutes {
		t.Errorf("the block = %+v, want the shipped small expectation %+v: a repository's looser figure is not the run's", rec.Tier, want)
	}
	pol := rec.Policy
	if pol == nil {
		t.Fatal("the record of a run that dropped a value carries no policy")
	}
	var found bool
	for _, ig := range pol.Ignored {
		if ig.Key != policy.TierKey(tier.Small, policy.KeyExpectedUSD) {
			continue
		}
		found = true
		if ig.Value != "2" || ig.Effective != "1" || ig.Source != policy.SourceCeiling || ig.From != policy.SourceBranch {
			t.Errorf("ignored = %+v", ig)
		}
	}
	if !found {
		t.Fatalf("the repository's looser tier expectation was dropped without being recorded: %+v", pol.Ignored)
	}
	got := logs.String()
	if !strings.Contains(got, "a looser value in fugaro.yaml was ignored") || !strings.Contains(got, "governance.tiers.small.expected_usd") {
		t.Errorf("the run log does not warn about the dropped tier figure:\n%s", got)
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
