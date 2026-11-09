package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The escalation's activation wiring (D1507): the two seams the runner calls
// it at, and the plan the loop that follows walks. security_test.go pins the
// escalation's own body — what it reads, what it decides and what it records —
// and these two cases are the half no body-level test can see: a call site
// removed, or a plan the loop captured before the escalation rebuilt it, would
// leave every one of those green while a real run records an escalation and
// never spends it.

// securityLoopRecipe is a loop recipe with the first line and the senior review
// and no adversarial step: level 3 is off for the run it starts, so the only
// adversarial stage a case below reaches is the one the escalation inserted in
// the plan (planRecipe's O7 insertion).
const securityLoopRecipe = "version: 1\nname: security\nsteps:\n  - first_line: {}\n  - review: {}\n"

// securityLoopFiles is the repository file map for securityLoopRecipe.
var securityLoopFiles = map[string]string{".fugaro/recipes/security.yaml": securityLoopRecipe}

// securityLoopCfg is a Startup repository — level 2 on, level 3 off — with the
// security recipe and the first line on, so the plan the run starts with is the
// first line and the senior review and nothing else.
func securityLoopCfg(t *testing.T) string {
	t.Helper()
	return modeCfg(t, "startup", 2, "  first_line_review: on\n  recipe: security\n")
}

// implementOnAListedPath is implement's step with the commit touching a path
// the list in force matches: what the post-implement seam classifies.
func implementOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Work on the credentials tree'")
	verifyTest(t, ctx, req)
	pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
	if err := os.WriteFile(pr, []byte("# Work on the credentials tree\n\nAdds internal/creds/state.go."), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 1}, nil
}

// fixOnAListedPath is a fix that adds a path the implement round did not touch,
// which is the case D1507's second seam exists for.
func fixOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

// TestRunEscalationRunsTheAdversarialStepThroughTheImplementSeam: the
// escalation fires in the post-implement window, and the plan the loop then
// walks carries level 3's step — the run has to spend the level it recorded,
// not merely record it (D1507, D1509).
func TestRunEscalationRunsTheAdversarialStepThroughTheImplementSeam(t *testing.T) {
	h := newHarnessFiles(t, securityLoopCfg(t), nil, securityLoopFiles)
	rec, err := h.run(t,
		implementOnAListedPath,
		review("ship", 0), // review_first
		review("ship", 0), // review: level 2 ships
		review("ship", 0), // adversarial: the escalation's own step
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the senior review", got)
	}
	if rec.Security == nil {
		t.Fatal("the run recorded no escalation, so the adversarial stage proves nothing about the seam")
	}
	if rec.Security.At != runstore.SecurityAtImplement || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the implement seam", rec.Security)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q): every enabled level shipped", rec.Outcome, rec.Reason)
	}
}

// TestRunEscalationKeepsAPOCRunFromStoppingOnTheCheapLoop: at POC the cheap
// loop is the level that decides readiness (TestLevelsPOCL1Decides), and a fix
// inside it can turn level 3 on (the fix seam above). From that round on the
// loop is no longer the decider — level 3 is — so a cheap round whose findings
// are all the owner's is handed to the level that decides instead of stopping
// the run (OD1, owner.go). Readiness is the same question: a loop that still
// thought it decided would stop here and the escalation's adversarial step,
// which is the whole of what it bought, would never run.
func TestRunEscalationKeepsAPOCRunFromStoppingOnTheCheapLoop(t *testing.T) {
	cfg := modeCfg(t, "poc", 2, "  first_line_rounds: 2\n  first_line_review: on\n  recipe: security\n")
	h := newHarnessFiles(t, cfg, nil, securityLoopFiles)
	rec, err := h.run(t,
		implement("feature"), // nothing on the list: the implement seam escalates nothing
		review("changes", 1), // review_first round 1: asks for changes
		fixOnAListedPath,     // the fix adds a path on the list: the fix seam turns level 3 on
		reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")), // review_first round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review_first", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the cheap round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if rec.Halt != nil {
		t.Fatalf("the run halted on %q: the cheap loop stopped deciding the moment the escalation turned level 3 on", rec.Halt.Reason)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: the adversarial round is the one that shipped", rec.Outcome, rec.Reason)
	}
}

// TestRunEscalationRunsTheAdversarialStepWhenTheFixRaisesTheMode: the fix that
// matches the list is the same fix that raises the rigor mode — roundRigor runs
// first, then the escalation, so the escalation resolves the levels the raise
// left (security.go, D22). At POC the mode's own row is what turns level 1 on
// (rigor.defaultLevels), so a raise out of POC takes the first_line step away:
// the plan the escalation rebuilds is the plan the loop is walking with a step
// the loop has already run removed from the front, and every step after it
// moves up one place. A loop that walks the plan by index therefore steps over
// the adversarial step it just recorded: the escalation is on the record, level
// 3 is on, and no round of it is ever asked — the run ends a draft, because the
// level whose verdict readiness needs never ran.
func TestRunEscalationRunsTheAdversarialStepWhenTheFixRaisesTheMode(t *testing.T) {
	base := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg := strings.Replace(base, "agent:\n",
		"rigor:\n  default: poc\n  areas:\n    - { name: app, match: \"feature.txt\", mode: critical }\nagent:\n", 1)
	if cfg == base {
		t.Fatal("the fixture has no agent: block to put rigor: before, so the repository would not opt in")
	}
	// Level 2 is on at POC through a layer of its own (the mode's row turns it
	// off), and the catalog's own `default` recipe supplies the first_line step
	// the mode's row turns on. Level 1 is on by that row alone and on no model
	// of the run's: the run names none, so nothing writes level 1 into its own
	// configuration (applyLevels) and the raise out of POC takes the step with
	// it.
	raised := strings.Replace(cfg, "  review_rounds: 2\n",
		"  review_rounds: 2\n  first_line_rounds: 1\n  levels: { l2: { model: senior-model } }\n", 1)
	if raised == cfg {
		t.Fatal("the fixture has no review_rounds line to put the levels beside")
	}
	h := newHarness(t, raised, nil)
	rec, err := h.run(t,
		implement("feature"),           // the file the area rule matches, and nothing on the list
		review("ship", 0),              // review_first: the step the mode's POC row inserted
		review("changes", 1),           // review round 1: asks for changes
		fixRaisingTheModeOnAListedPath, // the fix matches the area and the list: the mode and level 3 move at once
		review("ship", 0),              // review round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the fix's review round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: the adversarial round is the one that shipped", rec.Outcome, rec.Reason)
	}
}

// fixRaisingTheModeOnAListedPath is the fix that moves both the rigor mode and
// the security escalation at the same seam: it writes the file the area rule
// matches, so roundRigor raises the mode, and commits a path on the list, so
// the escalation beside it turns level 3 on.
func fixRaisingTheModeOnAListedPath(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo more >> feature.txt && mkdir -p internal/creds && echo 'package creds' > internal/creds/state.go && git add -A && git commit -qm 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

// TestRunEscalationRunsTheAdversarialStepThroughTheFixSeam: the second seam is
// inside the loop that is already walking the plan, so the step the escalation
// adds has to reach that loop. A loop that captured the plan once would run the
// fix's escalation, record it, and never ask the level — the run would claim
// the escalation and not spend it (D1507).
func TestRunEscalationRunsTheAdversarialStepThroughTheFixSeam(t *testing.T) {
	h := newHarnessFiles(t, securityLoopCfg(t), nil, securityLoopFiles)
	rec, err := h.run(t,
		implement("feature"), // nothing on the list: the implement seam escalates nothing
		review("ship", 0),    // review_first
		review("changes", 1), // review round 1: asks for changes
		fixOnAListedPath,     // the fix adds a path the implement round did not touch
		review("ship", 0),    // review round 2
		review("ship", 0),    // adversarial: the escalation's own step
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review", "adversarial") {
		t.Fatalf("stages = %v, want the escalation's adversarial step after the fix's review round", got)
	}
	if rec.Security == nil {
		t.Fatal("the fix added a path on the list and the run recorded no escalation")
	}
	if rec.Security.At != runstore.SecurityAtFix || rec.Security.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the fix seam", rec.Security)
	}
	if n := countStage(rec, "adversarial"); n != 1 {
		t.Fatalf("adversarial stages = %d, want 1", n)
	}
}

// listedLeftoverPath is a path the list in force matches — the credentials row
// — that a stage leaves in the checkout without committing it.
const listedLeftoverPath = "internal/creds/state.go"

// listedLeftover is the run's own work on that path, left uncommitted: written
// after the suite ran, so the run's verified-test record still describes the
// commit it earned, and carried onto the branch by nothing but finalize's own
// leftover commit. Both of the escalation's seams read the committed diff
// (gitops' ChangedFiles diffs workBase...HEAD), so the file is invisible to
// them and reaches the run's diff for the first time at finalize.
func listedLeftover(t *testing.T, req agent.Request, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(req.Dir, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	plainText(t, req, name)
}

// TestRunRecordsTheRefusalOnlyFinalizeCanSee: the escalation's third reading,
// which is D1507's own gap — the two seams classify what the agent committed,
// and finalize is what commits the work it left behind. A run whose only
// sensitive path arrives that way must not read like a run whose diff met
// nothing at all: level 3 off, no adversarial step, and an empty member are
// exactly that record (D1509).
func TestRunRecordsTheRefusalOnlyFinalizeCanSee(t *testing.T) {
	h := newHarnessFiles(t, securityLoopCfg(t), nil, securityLoopFiles)
	rec, err := h.run(t,
		implementLeaving(listedLeftoverPath, listedLeftover), // committed by finalize, never by a stage
		review("ship", 0), // review_first
		review("ship", 0), // review: level 2 ships
	)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Security
	if got == nil {
		t.Fatal("the run's whole diff matched the list and the record carries nothing, so it reads like a run whose diff met nothing")
	}
	if !got.Refused {
		t.Fatalf("the escalation was refused at finalize and the record claims it happened: %+v", got)
	}
	if got.At != runstore.SecurityAtFinalize || got.Pattern != "credentials" {
		t.Errorf("the record's escalation = %+v, want the credentials row at the finalize reading", got)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), listedLeftoverPath) {
		t.Errorf("the branch does not carry the work the refusal is about: %v", branchFiles(t, h, rec.Branch))
	}
	if rec.Outcome == runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want the run out of ready", rec.Outcome, rec.Reason)
	}
	// The refusal's own sentence, not the leftover commit's: the run that
	// matched the list at finalize matched it *because* it committed work
	// nobody reviewed, and the member's `at` is what says so.
	if rec.Reason != runner.ReasonSecurityRefused {
		t.Fatalf("reason = %q, want the refusal's own sentence", rec.Reason)
	}
}

// securityRefusedCfg is securityLoopCfg with level 3 named a model and an
// effort of its own, and securityRefusedSpec the task that turns the level off
// — the one layer a level-3 off-switch has to come from for the block below to
// be a configuration nothing checks. The configuration's block is not what the
// run starts with (the task's none answers first, and only a level that runs is
// checked), and the escalation's forced resolution is what reads it: it clears
// the task's switch (raise-only, D1517) and the configuration's block comes
// back into force, naming a model a route serves and an effort a provider stage
// cannot send — refused there rather than at bootstrap (D725, levels.go's
// check).
func securityRefusedCfg(t *testing.T) string {
	t.Helper()
	cfg := strings.Replace(securityLoopCfg(t),
		"  models: { coder: coder-model, reviewer: senior-model }\n",
		"  models: { coder: "+sonnet+", reviewer: "+opus+", background: "+haiku+" }\n", 1)
	if cfg == securityLoopCfg(t) {
		t.Fatal("the fixture has no models line to name the run's own models under the budget")
	}
	out := strings.Replace(cfg, "  first_line_review: on\n",
		"  first_line_review: on\n  levels: { l3: { model: "+deepseek+", effort: high } }\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no first_line_review line to put the level-3 block beside")
	}
	return out
}

// securityRefusedSpec is the task securityRefusedCfg is read against: an
// ordinary run whose own review_levels turns level 3 off, which is a legal
// configuration at startup and the reason the block above never reaches a
// check before the escalation does.
func securityRefusedSpec() *task.Spec {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	spec.ReviewLevels = &config.ReviewLevels{L3: &config.LevelSetting{Off: true}}
	return spec
}

// securityRefusedProviders is the owner's route for the model the level-3
// block names, so the forced resolution refuses its effort rather than finding
// no route to judge. The base URL is the marketplace's own, which is what makes
// the route catalog-eligible and the level's model priced (config.PriceTable):
// a route the catalog may not price leaves the level's model unpriced, and the
// run is refused for that at bootstrap whichever layer turned the level off.
func securityRefusedProviders() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api/v1",
		Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
	}}
}

// securityRefusedGateway puts the run in budget mode enforce with a per-run cap
// against a fake upstream. A provider-served model is admitted only on a run
// that enforces the budget (config's CheckProviderGateway): without it the pin
// would be a call to Anthropic. Nothing here makes a model call — the level
// that would is the one the escalation is refused.
func securityRefusedGateway(t *testing.T, h *harness) {
	t.Helper()
	_, up := anthropicfake.New(t)
	withProviderKey(h)
	// The routes ride the job's environment too (what `fugaro update` writes):
	// the budget's own price table is built from them, and a level's model the
	// table cannot price is refused at bootstrap whatever layer turned the
	// level off.
	raw, err := json.Marshal(h.deps.Providers)
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	env := map[string]string{
		runner.BudgetModeEnv: "enforce", runner.MaxRunUSDEnv: "50",
		runner.ModelProvidersEnv: string(raw),
	}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend, h.deps.GatewayUpstream = spend, up.URL
}

// TestRunRefusedEscalationKeepsAnOtherwiseReadyRunOutOfReady is the finalize
// call site of the escalation's readiness rule (D1509), which no body-level
// test can reach. This run is ready by every answer finalize's own records give
// it — it committed its own work, its suite passed on that commit, and its
// senior review shipped — and the one thing that is not is the level the
// classification asked for and the forced resolution refused. Without the rule
// the run ends ready and its pull request opens for review under a diff the
// owner's list called sensitive, so a call site deleted in silence is a review
// nobody gets.
func TestRunRefusedEscalationKeepsAnOtherwiseReadyRunOutOfReady(t *testing.T) {
	h := newHarnessFiles(t, securityRefusedCfg(t), securityRefusedSpec(), securityLoopFiles)
	h.deps.Providers = securityRefusedProviders()
	securityRefusedGateway(t, h)
	rec, err := h.run(t,
		implementOnAListedPath, // the post-implement seam sees the path, and level 3 is refused for it
		review("ship", 0),      // review_first
		review("ship", 0),      // review: level 2 ships
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Security; got == nil || !got.Refused {
		t.Fatalf("the escalation was not refused, so this case never reaches the rule it pins: %+v", got)
	}
	if rec.Outcome == runstore.OutcomeReady || rec.Status != runstore.StatusFailed {
		t.Fatalf("record = %s/%s (reason %q), want a draft: the review the record shows never covered a diff the list called sensitive",
			rec.Status, rec.Outcome, rec.Reason)
	}
	if rec.Reason != runner.ReasonSecurityRefused {
		t.Fatalf("reason = %q, want the refusal's own sentence", rec.Reason)
	}
	if pr := onlyPR(t, h.provider); !pr.Draft {
		t.Fatal("the pull request was opened ready for review under a refused escalation")
	}
}
