package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// stagePinsRun builds the minimal run stagePins needs: the gateway on
// (api-key auth, budget enforcing) with coder, reviewer and background
// models, and providers claiming whichever of them start with "vendor/".
func stagePinsRun(t *testing.T, coder, reviewer, background string) *run {
	t.Helper()
	return &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth:   "api-key",
			Models: config.ModelRoles{Coder: coder, Reviewer: reviewer, Background: background},
		}},
		spend: Spend{Mode: "enforce"},
		d: Deps{Providers: map[string]config.ModelProvider{
			"acme": {Kind: config.ModelProviderKind, Models: []string{"vendor/*"}},
		}},
	}
}

// TestStagePinsProviderRouteVars: a stage whose role model is served by a
// provider gets CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS and
// MAX_THINKING_TOKENS=0 added to its pins (the gateway refuses both fields on
// a provider route, forward.go's routedKeys); an all-Claude stage gets
// neither, so its request shape is untouched.
func TestStagePinsProviderRouteVars(t *testing.T) {
	const providerVar1, providerVar2 = "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "MAX_THINKING_TOKENS"

	// All-Claude: coder and reviewer are Claude models, no background set.
	r := stagePinsRun(t, "claude-sonnet-5-5", "claude-opus-5", "")
	for _, stage := range []string{"implement", "review"} {
		pins := r.stagePins(stage)
		if _, ok := pins[providerVar1]; ok {
			t.Errorf("stage %s (all-Claude): %s is set, want unset", stage, providerVar1)
		}
		if _, ok := pins[providerVar2]; ok {
			t.Errorf("stage %s (all-Claude): %s is set, want unset", stage, providerVar2)
		}
	}

	// Mixed, OWNER RULING mirror case (docs/multi-model.md §7): the coder is
	// a provider model, the reviewer stays Claude, and an explicit Claude
	// background keeps config.EffectiveBackground from defaulting the
	// reviewer's background to the provider coder's model too. With the
	// reviewer's own role model and its background both Claude, its stage
	// gets neither variable.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "claude-haiku-4-5")
	pins := r.stagePins("implement")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("coder (provider) stage pins = %+v, want both set", pins)
	}
	pins = r.stagePins("review")
	if _, ok := pins[providerVar1]; ok {
		t.Errorf("reviewer (Claude, explicit Claude background) stage: %s is set, want unset", providerVar1)
	}
	if _, ok := pins[providerVar2]; ok {
		t.Errorf("reviewer (Claude, explicit Claude background) stage: %s is set, want unset", providerVar2)
	}

	// Mixed, OWNER RULING (docs/multi-model.md §7): the same coder/reviewer
	// split, but agent.models.background is left empty. config.
	// EffectiveBackground then defaults the background to the provider
	// coder's model (TestBackgroundDefaultsToCoder), so the Claude reviewer
	// stage's own Claude Code process still makes background calls on the
	// provider route and would be refused the same way without the
	// variables. The decision is "any model this stage's Claude Code process
	// uses", not "the stage's own role model": kept as is, on purpose, not a
	// bug to fix.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "")
	pins = r.stagePins("review")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("reviewer stage with no explicit background (defaults to the provider coder) pins = %+v, want both set", pins)
	}

	// The background model alone being a provider's is enough: the review
	// stage's role (reviewer) is Claude, but its background call is not.
	r = stagePinsRun(t, "claude-sonnet-5-5", "claude-opus-5", "vendor/model-b")
	pins = r.stagePins("review")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("reviewer stage with a provider background pins = %+v, want both set", pins)
	}

	// Without the gateway on, no pins at all, provider or not.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "")
	r.spend = Spend{Mode: "off"}
	if pins := r.stagePins("implement"); pins != nil {
		t.Errorf("gateway off: pins = %+v, want nil", pins)
	}
}
