package cli

import (
	"context"
	"testing"
	"time"
)

func TestZZDebugRecipe(t *testing.T) {
	r := batchLaunchWorld(t, batchOptInLine, ticketAnswers(time.Now().Add(time.Hour)))
	ctx := context.Background()
	cfg, probs := checkoutParse(ctx, "acme/app")
	t.Logf("cfg=%v probs=%v", cfg != nil, probs)
	if cfg != nil {
		t.Logf("agent=%q", cfg.Agent.Recipe)
		t.Logf("workflows=%v", cfg.Workflows)
	}
	env := fileEnv(t, r.f.cloudFixture)
	spec := raceSpec(t, env)
	t.Logf("spec.Repo=%q ref=%q workflow=%q", spec.Repo, spec.Ref, spec.Workflow)
	t.Logf("recipe=%v", recipeForLaunch(ctx, cfg, spec))
}
