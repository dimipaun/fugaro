package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// levelsRun is the minimal run the review levels' routing needs: an api-key
// run under an enforcing budget, so the gateway is on and a stage has pins at
// all, with the models and the per-level blocks the test gives it.
func levelsRun(a config.Agent, providers map[string]config.ModelProvider) *run {
	return &run{
		cfg:   &config.Config{Agent: a},
		spend: Spend{Mode: "enforce"},
		d:     Deps{Providers: providers},
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
	// A level's model reaches its own stage and no other: the senior review and
	// the coder keep theirs.
	if got := r.stagePins("review")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != reviewer {
		t.Errorf("review pins = %q, want the reviewer's model %q", got, reviewer)
	}
	if got := r.stagePins("implement")["CLAUDE_CODE_SUBAGENT_MODEL"]; got != coder {
		t.Errorf("implement pins = %q, want the coder's model %q", got, coder)
	}
}
