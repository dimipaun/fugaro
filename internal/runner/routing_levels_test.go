package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// levelsRun is the minimal run the review levels' routing needs: an api-key
// run under an enforcing budget, so the gateway is on and a stage has pins at
// all, with the models, the per-level blocks and the routes the test gives it.
func levelsRun(a config.Agent, providers map[string]config.ModelProvider) *run {
	return &run{
		cfg:   &config.Config{Agent: a},
		spend: Spend{Mode: "enforce"},
		d:     Deps{Providers: providers},
	}
}

// levelRoutes are the routes the level tests pin models on: two vendors, the
// way an installation with a cheap first line and a second opinion from
// another family has them.
func levelRoutes() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{
		"openrouter": {
			Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret:      "openrouter-api-key",
			Models:      []string{"deepseek/*", "qwen/*"},
			AllowDataTo: []string{"acme/site"}, RouteFeePct: 5,
		},
	}
}

// TestRoutingL1ModelDistinctFromCoder: review level 1 runs its own model. The
// first-line stage has always run the coder's model (config.StageRole maps
// review_first to the coder), and a level 1 another layer names a model for
// must run that one — in the pins the agent's process is given and in the set
// the gateway admits for the stage, which have to name the same model or the
// stage's own calls are refused as not pinned.
//
// The red step: the first-line stage's pins name the coder's model.
func TestRoutingL1ModelDistinctFromCoder(t *testing.T) {
	const coder, reviewer, firstLine = "claude-sonnet-5-5", "claude-opus-5", "deepseek/deepseek-v4-pro"
	r := levelsRun(config.Agent{
		Auth:   "api-key",
		Models: config.ModelRoles{Coder: coder, Reviewer: reviewer},
		Levels: config.ReviewLevels{L1: &config.LevelSetting{Model: firstLine}},
	}, nil)
	pins := r.stagePins("review_first")
	for _, k := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if got := pins[k]; got != firstLine {
			t.Errorf("review_first %s = %q, want the level's own model %q: level 1 is the coder's stage, not the coder's model", k, got, firstLine)
		}
	}
	if got := r.stageSpec("review_first").Model; got != firstLine {
		t.Errorf("the gateway's model for review_first = %q, want %q: a set that disagrees with the stage's pins refuses every call of it", got, firstLine)
	}
	// A level's model reaches its own stage and no other: the senior review and
	// the coder keep theirs.
	if got := r.stagePins("review")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != reviewer {
		t.Errorf("review pins = %q, want the reviewer's model %q", got, reviewer)
	}
	if got := r.stagePins("implement")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != coder {
		t.Errorf("implement pins = %q, want the coder's model %q", got, coder)
	}
}

// TestRoutingL3ProviderModelGetsRouteVars: a level 3 whose model a provider
// claims is a provider stage, so its process gets the provider route's
// variables — thinking off and the experimental betas dropped — exactly as a
// provider coder's does. A Claude stage keeps neither: the route's variables
// reach the stage whose model the provider claims and no other.
func TestRoutingL3ProviderModelGetsRouteVars(t *testing.T) {
	const coder, reviewer, adversarial = "claude-sonnet-5-5", "claude-opus-5", "deepseek/deepseek-v4-pro"
	r := levelsRun(config.Agent{
		Auth:   "api-key",
		Models: config.ModelRoles{Coder: coder, Reviewer: reviewer},
		Levels: config.ReviewLevels{L3: &config.LevelSetting{Model: adversarial}},
	}, levelRoutes())
	pins := r.stagePins(stageAdversarial)
	if got := pins["CLAUDE_CODE_SUBAGENT_MODEL"]; got != adversarial {
		t.Errorf("the adversarial stage's model = %q, want the level's own %q", got, adversarial)
	}
	for k, want := range agent.ProviderRouteVars() {
		if got := pins[k]; got != want {
			t.Errorf("the adversarial stage's %s = %q, want %q: a provider serves %s", k, got, want, adversarial)
		}
	}
	senior := r.stagePins("review")
	for k := range agent.ProviderRouteVars() {
		if got, ok := senior[k]; ok {
			t.Errorf("the senior review's %s = %q, want it unset: no provider serves %s", k, got, reviewer)
		}
	}
}

// TestRoutingRefusesLevelModelOutsideAllowedModels: a level's own model is
// judged by the run's allow-list, and the refusal reaches the run through the
// seam it resolves its levels with. What this pins is the half the runner owns:
// a refused resolution writes nothing, so no stage is ever pinned with the
// model the allow-list refused and the run cannot go on and call it.
func TestRoutingRefusesLevelModelOutsideAllowedModels(t *testing.T) {
	const coder, reviewer, adversarial = "claude-sonnet-5-5", "claude-opus-5", "deepseek/deepseek-v4-pro"
	r := levelsRun(config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: coder, Reviewer: reviewer}}, levelRoutes())
	_, _, err := r.applyLevels(config.LevelInputs{
		Mode:          rigor.Critical,
		Task:          &config.ReviewLevels{L3: &config.LevelSetting{Model: adversarial}},
		AllowedModels: []string{coder, reviewer},
	})
	if err == nil {
		t.Fatalf("level 3's model %s is outside the run's allowed_models (%s, %s) and was accepted", adversarial, coder, reviewer)
	}
	if want := "l3.model " + adversarial + " is not in the run's allowed_models"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is %q, want it to say %q", err, want)
	}
	if got := r.levelModel(policy.LevelL3); got != "" {
		t.Errorf("the refused level's model was written into the run's configuration (%q): nothing may pin a model the allow-list refuses", got)
	}
	if got := r.stageSpec(stageAdversarial).Model; got != reviewer {
		t.Errorf("the adversarial stage's model = %q, want the reviewer's own %q: the level is off, so its stage is the reviewer's", got, reviewer)
	}
}

// TestRoutingLevelsReachTheStageAndTheGateway: the models a resolution writes
// are the ones the stages run. Every stage's pins and the set the gateway
// admits for it name one model — the level's own where a layer named one, the
// role slot's where none did — and the two answers are the same one, because
// the gateway refuses a call whose model is not its stage's.
func TestRoutingLevelsReachTheStageAndTheGateway(t *testing.T) {
	const coder, reviewer, firstLine, adversarial = "claude-sonnet-5-5", "claude-opus-5", "deepseek/deepseek-v4-pro", "qwen/qwen3"
	r := levelsRun(config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: coder, Reviewer: reviewer}}, levelRoutes())
	levels, warns, err := r.applyLevels(config.LevelInputs{
		Mode: rigor.Critical,
		Task: &config.ReviewLevels{
			L1: &config.LevelSetting{Model: firstLine},
			L3: &config.LevelSetting{Model: adversarial},
		},
		AllowedModels: []string{coder, reviewer, firstLine, adversarial},
	})
	if err != nil {
		t.Fatalf("applyLevels: %v", err)
	}
	if len(warns) > 0 {
		t.Logf("the resolution warned: %v", warns)
	}
	if !levels.L3.On {
		t.Fatal("level 3 is off at critical: the mode's own row turns it on")
	}
	for _, c := range []struct{ stage, want string }{
		{"review_first", firstLine},
		{"review", reviewer},
		{stageAdversarial, adversarial},
		{"implement", coder},
	} {
		if got := r.stagePins(c.stage)["CLAUDE_CODE_SUBAGENT_MODEL"]; got != c.want {
			t.Errorf("%s pins = %q, want %q", c.stage, got, c.want)
		}
		if got := r.stageSpec(c.stage).Model; got != c.want {
			t.Errorf("the gateway's model for %s = %q, want %q: the set and the pins name one model", c.stage, got, c.want)
		}
	}
	// Level 2 named no model of its own: the resolution leaves it the
	// reviewer's, and that is what its stage runs.
	if got := r.levelModel(policy.LevelL2); got != reviewer {
		t.Errorf("level 2's model = %q, want the reviewer's %q: a level no layer names a model for runs its role's", got, reviewer)
	}
}

// TestRoutingRoutesALevelsOwnModel: a level's own model is a pin of its own,
// so a switch naming it moves the level's stage the way it moves the role slot
// the level sits in. A level left behind would keep the canonical ID while the
// gateway registered the route's, and its calls would go direct — the one
// outcome the switch exists to prevent.
func TestRoutingRoutesALevelsOwnModel(t *testing.T) {
	const coder, reviewer, level1, routedLevel1 = "claude-sonnet-5-5", "claude-opus-5", "claude-fable-5-1", "anthropic/claude-fable-5.1"
	r := routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{level1}}, coder, reviewer)
	r.setLevelModel(policy.LevelL1, level1)
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("applyRouting: %v", err)
	}
	if got := r.levelModel(policy.LevelL1); got != routedLevel1 {
		t.Errorf("level 1's own model = %q, want the route's own ID %q", got, routedLevel1)
	}
	if got := r.stagePins("review_first")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != routedLevel1 {
		t.Errorf("review_first pins = %q, want %q", got, routedLevel1)
	}
	if got := r.stageSpec("review_first").Model; got != routedLevel1 {
		t.Errorf("the gateway's model for review_first = %q, want %q", got, routedLevel1)
	}
	// The coder's own pin is a Claude ID the switch does not name: the level's
	// model moves without moving the slot the level sits in.
	if got := r.stagePins("implement")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != coder {
		t.Errorf("implement pins = %q, want the untouched coder pin %q", got, coder)
	}
}

// TestStageRoleAgreesWithConfig: the runner's own answer for the stages
// config.StageRole knows is that function's, and the adversarial stage — the
// one it does not know — is the reviewer's, because level 3 is a review. A
// stage whose role disagreed with its pins would take its output limit and its
// model from two different answers.
func TestStageRoleAgreesWithConfig(t *testing.T) {
	for _, stage := range []string{"review_first", "review", "implement", "fix", "plan"} {
		if got, want := stageRole(stage), config.StageRole(stage); got != want {
			t.Errorf("stageRole(%q) = %q, config.StageRole says %q", stage, got, want)
		}
	}
	if got := stageRole(stageAdversarial); got != config.RoleReviewer {
		t.Errorf("stageRole(%q) = %q, want the reviewer's role", stageAdversarial, got)
	}
	if stageAdversarial != "adversarial" {
		t.Errorf("the adversarial stage is %q: it is the recipe step's own name", stageAdversarial)
	}
}
