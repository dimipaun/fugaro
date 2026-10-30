// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance_test

// The stage tripwire's own tests, moved here with the code (plan task E2.3)
// and driven through internal/runner/runnertest, which is the minimal stage
// harness this package can reach: internal/runner's own harness lives in its
// _test files. The package is external and blank-imports ee/register, so the
// run in every test below is the tagged one — the policy and the tripwire
// ee/register installed — which is what makes these the same assertions they
// were in the core before the move.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/governance"
	_ "github.com/dimipaun/fugaro/ee/register"
	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner/runnertest"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// stageTimeoutCfg is the fixture repository's file with the workflow's
// timeouts replaced and a governance: block appended.
func stageTimeoutCfg(t *testing.T, timeouts, governance string) string {
	t.Helper()
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }", timeouts, 1)
	return cfg + governance
}

// fixtureYAML is the fixture repository's fugaro.yaml, unchanged.
func fixtureYAML(t *testing.T) string {
	t.Helper()
	return testutil.FixtureFiles(t)["fugaro.yaml"]
}

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

// layerSpec is the harness's default task with the project layer a launcher
// would hand it: the text, its sum and a generation.
func layerSpec(text string) *task.Spec {
	spec := runnertest.Task()
	spec.ProjectLayer = runnertest.Layer(text)
	return spec
}

// finishedAtTheBound is implement's work done, whose result the stage returns
// as the bound that ends it fires: the race the runner must read as work done
// rather than as a stop. Which bound it is is the test's own configuration.
func finishedAtTheBound(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	res, err := runnertest.Implement("feature")(t, ctx, req)
	<-ctx.Done()
	return res, err
}

// failedOnItsOwn is an agent that fails its stage with an error of its own,
// well inside every bound the run has.
func failedOnItsOwn(_ *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
	return agent.Result{CostUSD: 1}, errors.New("the agent gave up")
}

// TestTaggedRunnerHaltsAsBefore is the headline of the move: a build that
// compiled ee/register enforces governance exactly as main did before the
// policy, the bound and the halt moved to ee/governance (G10). A stage the
// run's own governance bounds stops the run as a governance halt with a
// checkpoint — not as a failed stage — and the bound it stops at is the one
// the merge resolved out of the block.
//
// It is one test rather than the pair of hook checks because the two halves
// are registered together and have to answer together: dropping either
// runner.SetStage or policy.SetGovernance from ee/register/governance.go
// leaves a tagged binary that carries the code and not the enforcement, and
// this fails.
func TestTaggedRunnerHaltsAsBefore(t *testing.T) {
	if got := policy.Merge(policy.Layer{Governance: policy.Governance{}.WithDefaults()}).Governance; got != governance.Defaults() {
		t.Fatalf("the tagged binary resolved %+v, want the shipped defaults %+v: ee/register did not install the policy half", got, governance.Defaults())
	}
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
		rec.Halt.Reason != runstore.HaltStageBudget || rec.Halt.Detail != "time" {
		t.Fatalf("record = %+v, halt = %+v: the tagged runner did not halt on the stage bound", rec, rec.Halt)
	}
	if cp := rec.Checkpoint; cp == nil || cp.Trigger != string(runstore.HaltStageBudget) {
		t.Fatalf("checkpoint = %+v, want the stage_budget one", cp)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.Calls()))
	}
}

// TestStageTimeoutHaltsWithCheckpoint: a stage that reaches its own
// governance.stage_timeout stops the run as a governance halt with a
// checkpoint report — the reason and the detail the runner's own account of
// the bound gives it, the report on the record, and a draft pull request
// saying so — and not as a failed stage (T7.6, D329, G7). The stage ends at
// its own bound, well inside the run's timeouts.stage, and no later stage
// runs.
func TestStageTimeoutHaltsWithCheckpoint(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget || rec.Halt.Scope != "run" || rec.Halt.Detail != "time" {
		t.Fatalf("halt = %+v", rec.Halt)
	}
	if want := "halted: stage_budget: time"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.Calls()))
	}
	// The checkpoint's why_over names the bound that fired and not the other
	// one of the two stage_budget trips: money is not what stopped this stage
	// (T7.6).
	cp := rec.Checkpoint
	if cp == nil || cp.Trigger != string(runstore.HaltStageBudget) || cp.WhyOver != "a stage passed its own time bound" || cp.EstimateUSD != 1 {
		t.Fatalf("checkpoint = %+v", cp)
	}
	pr := h.PR(t)
	if !pr.Draft {
		t.Error("the halt's pull request is ready")
	}
	// The status section the halt wrote mid-stage says the same thing in the
	// vocabulary the record uses, and the report finalize posted carries the
	// runner's words for the stop, the detail it gave it and the reason.
	for _, want := range []string{"**Halted**", "checkpoint `stage\\_budget`", "a stage passed its own time bound"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the pull request lacks %q:\n%s", want, pr.Body)
		}
	}
	if len(pr.Comments) == 0 {
		t.Fatal("no comment on the PR")
	}
	report := pr.Comments[len(pr.Comments)-1]
	for _, want := range []string{"**Halted:** a stage passed its own time bound (time)", "halted: stage_budget: time"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
}

// TestStageTimeoutNeverAboveTimeoutsStage: a stage's own timeout only ever
// shortens it. One above the run's timeouts.stage leaves that bound in
// charge, and the stage that reaches it is the failure it has always been —
// no governance stop, no checkpoint — because the run's own time budget is
// not a governance tripwire.
func TestStageTimeoutNeverAboveTimeoutsStage(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 1s, verify: 30s, finalize_reserve: 10s }",
		"governance:\n  stage_timeout: { implement: 5m }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 1s" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage cut by timeouts.stage is a governance stop: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.Calls()))
	}
	if !h.PR(t).Draft {
		t.Error("a failed run's pull request is not a draft")
	}
}

// TestStageTimeoutAboveTheRunBoundIsRecorded: the value the run drops is not
// dropped in silence. A stage_timeout at or above the run's own
// timeouts.stage leaves that bound in charge, and the value the layers wrote
// is recorded as one key of result.json's policy.ignored — so the report's
// policy line and the digest carry it the way they carry a value of
// fugaro.yaml a stricter layer dropped — and warned about on the run log,
// which is where an operator watching a run sees it (T7.6). Both layers a
// stage timeout can come from are covered: the repository's own file, and the
// project layer's governance block, which is the run's ceiling and the one
// case a ceiling value is dropped.
func TestStageTimeoutAboveTheRunBoundIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		from      string
		effective string
	}{
		{"the repository's own file", policy.SourceBranch, "1s"},
		{"the project layer's governance block", policy.SourceCeiling, "1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h *runnertest.Harness
			if tc.from == policy.SourceCeiling {
				// The layer's own profile carries the run's timeouts.stage, so
				// the bound that wins is the run's and the value dropped is the
				// ceiling's.
				layer := strings.Replace(runnerLayer,
					"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
					"timeouts: { total: 5m, stage: 1s, verify: 1m, finalize_reserve: 30s }", 1) +
					"governance: { stage_timeout: { implement: 5m } }\n"
				h = runnertest.New(t, layeredRepo, layerSpec(layer))
			} else {
				h = runnertest.New(t, stageTimeoutCfg(t,
					"timeouts: { total: 1m, stage: 1s, verify: 30s, finalize_reserve: 10s }",
					"governance:\n  stage_timeout: { implement: 5m }\n"), nil)
			}
			rec, err := h.Run(t, runnertest.Blocked(1))
			if err != nil {
				t.Fatal(err)
			}
			// The run's own bound is the one in charge: the stage fails the
			// way a stage cut by timeouts.stage always has, and the dropped
			// value is not a governance stop.
			if rec.Status != runstore.StatusFailed || rec.Halt != nil || rec.Checkpoint != nil {
				t.Fatalf("record = %+v, halt = %+v, checkpoint = %+v", rec, rec.Halt, rec.Checkpoint)
			}
			pol := rec.Policy
			if pol == nil || pol.Sources[policy.KeyStageTimeoutImplement] != tc.from {
				t.Fatalf("policy = %+v, want the stage timeout sourced as %s", pol, tc.from)
			}
			var found bool
			for _, ig := range pol.Ignored {
				if ig.Key != policy.KeyStageTimeoutImplement {
					continue
				}
				found = true
				// Source is the layer the run's own timeouts.stage came
				// from, which here is the same one that wrote the dropped
				// governance value: the branch's file or the layer's profile.
				// Clamped is what separates this entry from the merge's
				// looser values, which the same key can carry.
				if ig.Value != "5m0s" || ig.Effective != tc.effective ||
					ig.Source != tc.from || ig.From != tc.from || !ig.Clamped {
					t.Errorf("ignored = %+v", ig)
				}
			}
			if !found {
				t.Fatalf("the stage timeout the run does not apply was dropped without being recorded: %+v", pol.Ignored)
			}
			if got := h.Log.String(); !strings.Contains(got, "a stage timeout at or above the run's timeouts.stage was ignored") {
				t.Errorf("the run log does not warn about the dropped stage timeout:\n%s", got)
			}
		})
	}
}

// TestStageTimeoutStandsWhenTheResultRacesIt: a stage whose own deadline
// fired but which returned a result of its own is a stage that finished, the
// same convention the run's own timeouts.stage has. The run does not stop
// and no checkpoint is written: only a stage the bound actually cut is a
// governance halt, which is what the agent's error says.
func TestStageTimeoutStandsWhenTheResultRacesIt(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 1s }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, finishedAtTheBound, runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage that returned a result as its own deadline fired was stopped: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.Calls()) != 2 {
		t.Fatalf("%d stages ran, want implement and review", len(h.Calls()))
	}
}

// TestStageTimeoutStandsWhenTheRunBoundIsReached: the run's own timeouts.stage
// has the same convention, and the stage's own bound is read the same way — a
// stage that returns a result as the bound fires is a stage that finished, so
// the run does not fail it and the loop goes on to the next stage.
func TestStageTimeoutStandsWhenTheRunBoundIsReached(t *testing.T) {
	h := runnertest.New(t, stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 1s, verify: 5s, finalize_reserve: 5s }", ""), nil)
	rec, err := h.Run(t, finishedAtTheBound, runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage that returned a result as the run's own bound fired was stopped: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.Calls()) != 2 {
		t.Fatalf("%d stages ran, want implement and review", len(h.Calls()))
	}
}

// TestStageTimeoutKeepsAnAgentFailureOfItsOwn: an agent that fails its stage
// with an error of its own, its own deadline never reached, keeps that
// failure. The bound did not fire, so the stage is not a stop: the record
// names the stage's own error and no checkpoint is written.
func TestStageTimeoutKeepsAnAgentFailureOfItsOwn(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 1s }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, failedOnItsOwn)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement failed: the agent gave up" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a failure of the agent's own was read as the stage's timeout: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
}

// TestStageTimeoutIsNotTheRunsOwnBound: the run's own bound can cut a stage
// before the stage's own does — here the finalize reserve leaves the stage
// less time than its governance.stage_timeout asks for — and a stage cut by
// the run's own bound is the failure it has always been, whatever a stage
// timeout would have said had it been reached (T7.6). Reporting it as a stop
// would blame a tripwire that never fired and draft a pull request over a
// run that simply ran out of its own time.
func TestStageTimeoutIsNotTheRunsOwnBound(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 30s, stage: 10s, verify: 5s, finalize_reserve: 27s }",
		"governance:\n  stage_timeout: { implement: 9s }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "time budget exhausted") {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage cut by the run's own bound is a governance stop: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.Calls()))
	}
}

// TestStageTimeoutLooserInTheRepositoryIsRecorded: a stage timeout is one key
// of the one policy merge, so a repository file that writes a looser value
// than the project layer's is not applied and is not silently dropped either:
// the effective bound stays the layer's, and result.json carries the ignored
// value under the key it was written as (T7.1, D331, docs/project-layer.md).
func TestStageTimeoutLooserInTheRepositoryIsRecorded(t *testing.T) {
	layer := runnerLayer + "governance: { stage_timeout: { implement: 200ms } }\n"
	repo := layeredRepo + "governance: { stage_timeout: { implement: 5m } }\n"
	h := runnertest.New(t, repo, layerSpec(layer))
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
	pol := rec.Policy
	if pol == nil || pol.Sources[policy.KeyStageTimeoutImplement] != policy.SourceCeiling {
		t.Fatalf("policy = %+v, want the layer's value as the ceiling's", pol)
	}
	var found bool
	for _, ig := range pol.Ignored {
		if ig.Key == policy.KeyStageTimeoutImplement {
			found = true
			// The same shape a clamp writes — this key, the branch asking,
			// the ceiling in force — and not a clamp: a tighter layer capped a
			// looser value, so Clamped stays false and the report keeps it in
			// the "looser than the project's limits" sentence.
			if ig.Value != "5m0s" || ig.Effective != "200ms" || ig.From != policy.SourceBranch || ig.Clamped {
				t.Errorf("ignored = %+v", ig)
			}
		}
	}
	if !found {
		t.Fatalf("the looser value in the repository's file was dropped without being recorded: %+v", pol.Ignored)
	}
}

// TestStageTimeoutFromTheProjectLayer: the project layer's governance block is
// the ceiling a stage's own timeout is read from, and the one an operator sets
// for every repository of a project (T7.1, T7.4). A run whose layer sets it,
// with nothing in the repository's own file, is bounded by it.
func TestStageTimeoutFromTheProjectLayer(t *testing.T) {
	layer := runnerLayer + "governance: { stage_timeout: { implement: 200ms } }\n"
	h := runnertest.New(t, layeredRepo, layerSpec(layer))
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget ||
		rec.Halt.Detail != "time" || rec.Checkpoint == nil {
		t.Fatalf("record = %+v, halt = %+v, checkpoint = %+v", rec, rec.Halt, rec.Checkpoint)
	}
	if len(h.Calls()) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.Calls()))
	}
}

// TestStageTimeoutClampNamesBothLayers: From is the layer that wrote the
// dropped stage_timeout and Source is the layer the run's own timeouts.stage
// came from, and the warning spells them the way recordPolicy does ("from"
// the asking layer, "limit_from" the one in force). Here the layer's profile
// sets the run's bound and the repository's own file asks for the looser
// stage timeout, so the two differ and a swap is visible.
func TestStageTimeoutClampNamesBothLayers(t *testing.T) {
	layer := strings.Replace(runnerLayer,
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 5m, stage: 1s, verify: 1m, finalize_reserve: 30s }", 1)
	repo := layeredRepo + "governance: { stage_timeout: { implement: 5m } }\n"
	h := runnertest.New(t, repo, layerSpec(layer))
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	pol := rec.Policy
	var found bool
	for _, ig := range pol.Ignored {
		if ig.Key != policy.KeyStageTimeoutImplement {
			continue
		}
		found = true
		if ig.From != policy.SourceBranch || ig.Source != policy.SourceCeiling {
			t.Errorf("ignored = %+v, want From the branch, Source the ceiling", ig)
		}
	}
	if !found {
		t.Fatalf("the dropped value was not recorded: %+v", pol.Ignored)
	}
	if got := h.Log.String(); !strings.Contains(got, "from=branch") || !strings.Contains(got, "limit_from=ceiling") {
		t.Errorf("the warning does not spell the two layers as recordPolicy does:\n%s", got)
	}
}

// TestGovernanceLooserInTheRepositoryIsIgnored: with no project layer the
// ceiling carries Fugaro's own guardrails (policy.Governance.WithDefaults
// fills them in, T7.1), so the repository's fugaro.yaml — the branch the run
// checks out, which the agent can edit mid-run — may only tighten them. Every
// governance key that has a shipped figure is held to it: a looser value is
// dropped, recorded in policy.ignored with the figure the run is actually
// held to and the layer that holds it, and warned about, exactly as a looser
// budget key is (T7.1, D331).
//
// Before the defaults were in the ceiling this was the inversion the review
// found: the repository's own block was the first value written for the key,
// so it won with it, silently and with nothing recorded. Nothing reads these
// figures yet (T7.2 will), which is why this is the moment to pin it.
//
// The keys are listed one by one rather than read off the ceiling: a key the
// list forgets is a key nothing proves is held. The table is checked against
// policy.DefaultGovernanceKeys below, so a governance key added without a case
// here fails rather than passing quietly.
func TestGovernanceLooserInTheRepositoryIsIgnored(t *testing.T) {
	cases := []struct {
		name      string
		block     string // appended to the fixture's fugaro.yaml
		key       string
		value     string
		effective string
	}{
		{"review_ratio", "governance:\n  review_ratio: 9\n", policy.KeyReviewRatio, "9", "5"},
		{"review_floor_calls", "governance:\n  review_floor_calls: 40\n", policy.KeyReviewFloorCalls, "40", "20"},
		{"max_subagents", "governance:\n  max_subagents: 9\n", policy.KeyMaxSubagents, "9", "5"},
		{"stage_budget.implement", "governance:\n  stage_budget: { implement: 0.9 }\n", policy.KeyStageBudgetImplement, "0.9", "0.7"},
		{"stage_budget.review", "governance:\n  stage_budget: { review: 0.9 }\n", policy.KeyStageBudgetReview, "0.9", "0.5"},
		{"stage_budget.fix", "governance:\n  stage_budget: { fix: 0.9 }\n", policy.KeyStageBudgetFix, "0.9", "0.5"},
		{"tiers.trivial.expected_usd", "governance:\n  tiers: { trivial: { expected_usd: 0.5 } }\n", policy.TierKey(tier.Trivial, policy.KeyExpectedUSD), "0.5", "0.25"},
		{"tiers.trivial.expected_minutes", "governance:\n  tiers: { trivial: { expected_minutes: 20 } }\n", policy.TierKey(tier.Trivial, policy.KeyExpectedMinutes), "20", "10"},
		{"tiers.trivial.max_subagents", "governance:\n  tiers: { trivial: { max_subagents: 9 } }\n", policy.TierKey(tier.Trivial, policy.KeyMaxSubagents), "9", "3"},
		{"tiers.small.expected_usd", "governance:\n  tiers: { small: { expected_usd: 2 } }\n", policy.TierKey(tier.Small, policy.KeyExpectedUSD), "2", "1"},
		{"tiers.small.expected_minutes", "governance:\n  tiers: { small: { expected_minutes: 60 } }\n", policy.TierKey(tier.Small, policy.KeyExpectedMinutes), "60", "30"},
		{"tiers.small.max_subagents", "governance:\n  tiers: { small: { max_subagents: 9 } }\n", policy.TierKey(tier.Small, policy.KeyMaxSubagents), "9", "3"},
		{"tiers.medium.expected_usd", "governance:\n  tiers: { medium: { expected_usd: 4 } }\n", policy.TierKey(tier.Medium, policy.KeyExpectedUSD), "4", "3"},
		{"tiers.medium.expected_minutes", "governance:\n  tiers: { medium: { expected_minutes: 120 } }\n", policy.TierKey(tier.Medium, policy.KeyExpectedMinutes), "120", "90"},
		{"tiers.medium.max_subagents", "governance:\n  tiers: { medium: { max_subagents: 9 } }\n", policy.TierKey(tier.Medium, policy.KeyMaxSubagents), "9", "5"},
		{"tiers.large.expected_usd", "governance:\n  tiers: { large: { expected_usd: 9 } }\n", policy.TierKey(tier.Large, policy.KeyExpectedUSD), "9", "8"},
		{"tiers.large.expected_minutes", "governance:\n  tiers: { large: { expected_minutes: 300 } }\n", policy.TierKey(tier.Large, policy.KeyExpectedMinutes), "300", "240"},
		{"tiers.large.max_subagents", "governance:\n  tiers: { large: { max_subagents: 9 } }\n", policy.TierKey(tier.Large, policy.KeyMaxSubagents), "9", "5"},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.key] = true
	}
	for key := range governance.Defaults().DefaultGovernanceKeys() {
		if !covered[key] {
			t.Errorf("the shipped default for %q is not covered here, so nothing proves a repository cannot loosen it", key)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := runnertest.New(t, fixtureYAML(t)+c.block, nil)
			rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			pol := rec.Policy
			if pol == nil {
				t.Fatalf("the record of a run that dropped %s carries no policy", c.key)
			}
			var found bool
			for _, ig := range pol.Ignored {
				if ig.Key != c.key {
					continue
				}
				found = true
				if ig.Value != c.value || ig.Effective != c.effective || ig.Source != policy.SourceCeiling || ig.From != policy.SourceBranch {
					t.Errorf("ignored = %+v, want the repository's %s dropped for the ceiling's %s", ig, c.value, c.effective)
				}
			}
			if !found {
				t.Fatalf("the repository's looser %s was dropped without being recorded: %+v", c.key, pol.Ignored)
			}
			got := h.Log.String()
			if !strings.Contains(got, "a looser value in fugaro.yaml was ignored") || !strings.Contains(got, "governance."+c.key) {
				t.Errorf("the run log does not warn about the dropped %s:\n%s", c.key, got)
			}
		})
	}
}

// TestGovernanceStageTimeoutWithNoProjectLayerIsTheRepositorys: stage_timeout
// is the one governance key with no shipped figure (Governance.WithDefaults
// leaves it zero: a stage with no timeout of its own is bounded by the run's
// own timeouts.stage), so with no project layer there is nothing for a
// repository's value to be looser than — it is the only value there is, and it
// stands. The ceiling carrying the defaults must not invent one for it: a
// ceiling that did would silently drop every repository's own stage_timeout
// and, since a stage timeout only ever shortens timeouts.stage (T7.6), would
// leave the run with a bound nobody set.
func TestGovernanceStageTimeoutWithNoProjectLayerIsTheRepositorys(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Detail != "time" {
		t.Fatalf("record = %+v, halt = %+v: the repository's own stage_timeout did not bound the stage", rec, rec.Halt)
	}
	if pol := rec.Policy; pol != nil {
		for _, ig := range pol.Ignored {
			if ig.Key == policy.KeyStageTimeoutImplement {
				t.Errorf("the repository's stage_timeout was dropped for a ceiling figure that does not exist: %+v", ig)
			}
		}
	}
}

// TestGovernanceLooserInTheProjectLayerHolds: the project layer is the one
// layer that may loosen Fugaro's defaults (plan §7, D331), and it does so by
// writing the looser figure itself: WithDefaults keeps a key the layer set
// exactly as it stands, so the layer's 20 is the ceiling every repository of
// the project is held to, sourced to the ceiling and with nothing ignored.
// A tier's own figure works the same way, which is what makes a project able
// to say its repositories' small runs are expected to cost $5 where Fugaro
// ships $1.
func TestGovernanceLooserInTheProjectLayerHolds(t *testing.T) {
	layer := runnerLayer + "governance: { review_ratio: 20, tiers: { small: { expected_usd: 5 } } }\n"
	spec := layerSpec(layer)
	spec.Tier = tier.Small
	h := runnertest.New(t, layeredRepo, spec)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pol := rec.Policy
	if pol == nil || pol.Sources[policy.KeyReviewRatio] != policy.SourceCeiling {
		t.Fatalf("policy = %+v, want the project layer's review_ratio as the ceiling's", pol)
	}
	for _, ig := range pol.Ignored {
		if ig.Key == policy.KeyReviewRatio || ig.Key == policy.TierKey(tier.Small, policy.KeyExpectedUSD) {
			t.Errorf("the project layer's own looser value was dropped: %+v", ig)
		}
	}
	if rec.Tier == nil || rec.Tier.Tier != tier.Small || rec.Tier.ExpectedUSD != 5 {
		t.Fatalf("tier = %+v, want the project layer's own $5 for small", rec.Tier)
	}
}

// TestGovernanceAtTheShippedFigureIsNotAPolicy: a repository that writes the
// figure Fugaro already ships changes nothing, and its record says so: no
// ignored entry (the value is not looser than the ceiling's) and no source for
// the key, because a key still at the shipped figure is not a decision any
// layer made (H1). Writing it is not an error — a repository is free to spell
// out the guardrail it runs under — but it must not read as a policy of its
// own.
func TestGovernanceAtTheShippedFigureIsNotAPolicy(t *testing.T) {
	d := governance.Defaults()
	block := "governance:\n  review_ratio: " + strconv.FormatInt(d.ReviewRatio, 10) +
		"\n  stage_budget: { implement: " + strconv.FormatFloat(d.StageBudget.Implement, 'f', -1, 64) + " }\n"
	h := runnertest.New(t, fixtureYAML(t)+block, nil)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if pol := rec.Policy; pol != nil {
		if _, ok := pol.Sources[policy.KeyReviewRatio]; ok {
			t.Errorf("the record sources review_ratio to %q, want no source: the value is Fugaro's own", pol.Sources[policy.KeyReviewRatio])
		}
		for _, ig := range pol.Ignored {
			t.Errorf("a value equal to the shipped figure was recorded as ignored: %+v", ig)
		}
	}
}

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
	cfg := strings.Replace(fixtureYAML(t), "agent:\n",
		"governance:\n  tiers:\n    small: { expected_usd: 0.5, expected_minutes: 5 }\nagent:\n", 1)
	spec := runnertest.Task()
	spec.Tier = tier.Small
	h := runnertest.New(t, cfg, spec)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
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
// (TierExpectations: $1 and 30 minutes for small). The project layer's block
// is the one layer that may loosen the defaults and every layer above it may
// only tighten, so the defaults are the ceiling a run starts under and not
// something a repository has to repeat.
//
// A record written from the zero expectation instead says the run is expected
// to cost nothing and take no time, which is not a decision anybody made: it
// is the absence of one, and T8.2's expected-cost checkpoint would stop such a
// run at once.
func TestRunRecordCarriesTheShippedTierDefault(t *testing.T) {
	spec := runnertest.Task()
	spec.Tier = tier.Small
	h := runnertest.New(t, "", spec)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier == nil {
		t.Fatal("the record of a tiered run carries no tier block")
	}
	want := governance.Defaults().Tiers.Small
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
	cfg := strings.Replace(fixtureYAML(t), "agent:\n",
		"governance:\n  tiers:\n    small: { expected_usd: 2 }\nagent:\n", 1)
	spec := runnertest.Task()
	spec.Tier = tier.Small
	h := runnertest.New(t, cfg, spec)
	rec, err := h.Run(t, runnertest.Implement("feature"), runnertest.Review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	want := governance.Defaults().Tiers.Small
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
	got := h.Log.String()
	if !strings.Contains(got, "a looser value in fugaro.yaml was ignored") || !strings.Contains(got, "governance.tiers.small.expected_usd") {
		t.Errorf("the run log does not warn about the dropped tier figure:\n%s", got)
	}
}
