package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The two step kinds this file registers: one this build carries a function
// for, and one it deliberately does not, so the half-landed case — the parser
// knows a kind and the runner has nothing to run it with — has a recipe to be
// run. A build that owns a step's code is also the build that teaches the
// parser the kind (recipe.RegisterStepKind, whose production caller is
// ee/register's init), so a test that owns a step registers both halves the
// same way (recipe's RegisterStepKind says so in as many words).
const (
	stepProbeKind  recipe.StepKind = "judge_probe"
	stepOrphanKind recipe.StepKind = "judge_orphan"
	stepProbeName                  = "judge-probe"
	stepOrphanName                 = "judge-orphan"
)

const (
	stepProbeYAML  = "version: 1\nname: " + stepProbeName + "\nmode: evaluate\nsteps:\n  - judge_probe: {}\n"
	stepOrphanYAML = "version: 1\nname: " + stepOrphanName + "\nmode: evaluate\nsteps:\n  - judge_orphan: {}\n"
)

// stepProbeObject is what probeStep writes under the run's own prefix: what it
// was handed, and the answer it got back. A test reads it after the run rather
// than taking the step's word for what it saw, since what it saw is the run's
// own machinery.
const stepProbeObject = "probe.json"

func init() {
	for _, kind := range []recipe.StepKind{stepProbeKind, stepOrphanKind} {
		if err := recipe.RegisterStepKind(kind, recipe.ModeEvaluate); err != nil {
			panic("runner_test: " + err.Error())
		}
	}
	if err := runner.RegisterStep(stepProbeKind, probeStep); err != nil {
		panic("runner_test: " + err.Error())
	}
}

// probeStep is what this build carries for stepProbeKind: one model call
// through the run's own gateway, and what it was handed written to the run's
// own prefix. It is the whole shape of a registered step — code this package
// does not hold, reached by a mode: evaluate recipe, handed the run's
// machinery and nothing of the repository.
func probeStep(ctx context.Context, v runner.StepView) error {
	if v.Store == nil || v.Log == nil || v.Call == nil {
		return errors.New("the step was handed no store, no logger or no way to make a call")
	}
	answer, err := v.Call(ctx, runner.StepCall{
		Model: sonnet, System: "You are a probe.", Prompt: "Answer with one word.", MaxTokens: 64,
	})
	if err != nil {
		return err
	}
	seen, err := json.Marshal(map[string]any{
		"run_id": v.RunID, "slug": v.Slug, "prefix": v.Store.Prefix(),
		"ab": v.AB != nil, "answer": answer.Text,
	})
	if err != nil {
		return err
	}
	return v.Store.PutFile(ctx, stepProbeObject, seen, "application/json")
}

// evaluateSpec is a task spec for a mode: evaluate recipe, with the mode in the
// task exactly as the launcher writes it and the recipe embedded the way the
// CLI embeds one it resolved.
func evaluateSpec(name, yaml string) *task.Spec {
	s := recipeSpec(embeddedTask(name, yaml))
	s.Mode = recipe.ModeEvaluate
	return s
}

// TestStepHookRunsRegisteredStep: a mode: evaluate run is its one registered
// step and nothing else. The step runs, is handed the run's own machinery —
// its ID, its slug, its prefix and a way to call a model — and the run ends
// succeeded with nothing pushed, no pull request and no agent started (D550).
func TestStepHookRunsRegisteredStep(t *testing.T) {
	g := newGWFiles(t, gwConfig(t, ""), "observe", "", nil,
		anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 30000, Output: 40}))
	ctx := context.Background()
	if err := g.store.WriteTask(ctx, evaluateSpec(stepProbeName, stepProbeYAML)); err != nil {
		t.Fatal(err)
	}
	rec, err := g.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone || rec.Halt != nil {
		t.Fatalf("rec = %+v", rec)
	}
	if r := rec.Recipe; r == nil || r.Name != stepProbeName || r.Source != string(recipe.SourceCatalog) {
		t.Fatalf("recipe = %+v", r)
	}
	// What the step was handed: the run's own prefix, under the slug the run
	// was homed on, and no ab: block (the fixture's project layer has none).
	raw, err := g.store.ReadFile(ctx, stepProbeObject)
	if err != nil {
		t.Fatalf("the step wrote nothing: %v", err)
	}
	var seen struct {
		RunID  string `json:"run_id"`
		Slug   string `json:"slug"`
		Prefix string `json:"prefix"`
		Answer string `json:"answer"`
		AB     bool   `json:"ab"`
	}
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatal(err)
	}
	if seen.RunID != rec.RunID || seen.Slug != g.store.Slug() || seen.Prefix != g.store.Prefix() || seen.AB {
		t.Fatalf("the step was handed %+v", seen)
	}
	// Its call went through the run's own gateway: the fake upstream is
	// reachable only through it, and the gateway's own figure for the stage is
	// what the record carries.
	if g.fake.Count() != 1 {
		t.Fatalf("the upstream saw %d calls, want the step's one", g.fake.Count())
	}
	if len(rec.Stages) != 1 || rec.Stages[0].Name != string(stepProbeKind) {
		t.Fatalf("stages = %+v", rec.Stages)
	}
	if calls := rec.Stages[0].Calls; calls == nil || *calls != 1 {
		t.Fatalf("the step's stage counted %v call(s), want 1", calls)
	}
	// The stage list is what says what each stage cost, and the run's figures
	// hold exactly: the stages plus the "unattributed" residual are model_usd
	// (stageTiming, cost.go). A run whose only stage carried no cost would
	// break it with no stage to blame.
	if micros(t, rec.Cost.ModelUSD) == 0 || micros(t, rec.Stages[0].ModelUSD) == 0 {
		t.Fatalf("the call cost nothing: cost = %+v, stage = %+v", rec.Cost, rec.Stages[0])
	}
	if got := stageMicros(t, rec.Stages); got != micros(t, rec.Cost.ModelUSD) {
		t.Fatalf("the stages carry %v µ$ of the run's %v µ$ (%+v)", got, micros(t, rec.Cost.ModelUSD), rec.Stages)
	}
	if rec.Cost.ModelBy["unattributed"] != 0 {
		t.Fatalf("unattributed = %v, want none: the step's spend has a stage", rec.Cost.ModelBy["unattributed"])
	}
	if rec.Stages[0].Model != "" {
		t.Errorf("the stage names the model %q: nothing pinned one for a registered step", rec.Stages[0].Model)
	}
	// Everything a mode: evaluate run does not do.
	if len(g.agent.calls) != 0 {
		t.Errorf("the run started %d agent stage(s)", len(g.agent.calls))
	}
	if len(g.provider.State.PRs) != 0 {
		t.Errorf("the run opened %d pull request(s)", len(g.provider.State.PRs))
	}
	if refs := testutil.Git(t, g.remote, "for-each-ref", "refs/heads/fugaro/"); refs != "" {
		t.Errorf("the run pushed %s", refs)
	}
}

// TestEvaluateStepThisBuildCannotRun: a recipe whose step kind this build
// parses and carries no function for — the two halves of a registration that
// did not land together — fails the run, and names the step. It is refused
// rather than run as "nothing to do", because a judge run that succeeded
// without judging is a judged pair to everything that reads the runs after it.
func TestEvaluateStepThisBuildCannotRun(t *testing.T) {
	g := newGWFiles(t, gwConfig(t, ""), "observe", "", nil)
	if err := g.store.WriteTask(context.Background(), evaluateSpec(stepOrphanName, stepOrphanYAML)); err != nil {
		t.Fatal(err)
	}
	rec, err := g.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.FailureClass != failure.ConfigInvalid {
		t.Fatalf("rec = %+v", rec)
	}
	if !strings.Contains(rec.Reason, string(stepOrphanKind)) {
		t.Fatalf("reason = %q, want the step it cannot run named in it", rec.Reason)
	}
	if len(rec.Stages) != 0 {
		t.Errorf("stages = %+v, want none: no step ran", rec.Stages)
	}
	if len(g.provider.State.PRs) != 0 || testutil.Git(t, g.remote, "for-each-ref", "refs/heads/fugaro/") != "" {
		t.Error("the run pushed or opened a pull request")
	}
}

// TestStepHookRefusesWhatItCannotRegister: the two refusals that keep the two
// halves of a registration together — a kind the parser does not know, which
// would be a recipe no build could read, and a second function for one kind,
// which would quietly replace what a tagged build installed (§5.5).
func TestStepHookRefusesWhatItCannotRegister(t *testing.T) {
	if err := runner.RegisterStep("judge_unparsed", probeStep); err == nil {
		t.Error("a step kind the parser does not know was registered")
	}
	if err := runner.RegisterStep(stepProbeKind, probeStep); err == nil {
		t.Error("a second registration for one kind was accepted")
	}
	if err := runner.RegisterStep(stepOrphanKind, nil); err == nil {
		t.Error("a registration with no function was accepted")
	}
}
