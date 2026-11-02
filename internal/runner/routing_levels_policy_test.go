package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// levelPinned is gwConfig with one review level's own model set and the
// coder, the reviewer and the background left on Claude's own models: the
// level's model is then the only provider-served model in the run, so a
// refusal that names it can only be about the level and not about a pin.
func levelPinned(t *testing.T, level, model string) string {
	t.Helper()
	return gwConfig(t, "  levels: { "+level+": { model: "+model+" } }\n")
}

// TestLevelModelAllowDataToRefused pins the data-policy half of L4.5's fix
// round: a review level's own model is a pin of the stage it runs in, and the
// owner's data policy judges the pins, so a provider whose allow_data_to does
// not name the repository may not serve a level either. A level 3 model reads
// the repository's code as surely as the coder's does, and the check that
// stops the coder's model is the one that has to stop it. The refusal names
// the key the owner wrote the model under, not the role slot the check read
// it from, because that key is the line to change.
//
// Both slots are covered: level 1 sits in the coder's stage and level 3 in the
// reviewer's, and a level judged in the wrong slot would be refused only by
// accident of which pins the run happens to share with it.
func TestLevelModelAllowDataToRefused(t *testing.T) {
	t.Run("a level in the reviewer's slot", func(t *testing.T) {
		g := providerGW(t, levelPinned(t, "l3", deepseek), "acme/other")
		refusedBeforeAnyCall(t, g, "allow_data_to", "agent.levels.l3.model", deepseek, "acme/app")
	})
	t.Run("a level in the coder's slot", func(t *testing.T) {
		g := providerGW(t, levelPinned(t, "l1", deepseek), "acme/other")
		refusedBeforeAnyCall(t, g, "allow_data_to", "agent.levels.l1.model", deepseek, "acme/app")
	})
}

// TestLevelModelProviderAuthRefused: a level's model a provider serves needs
// agent.auth: api-key exactly as a pin does. An oauth token is a Claude
// subscription credential and is never sent to another provider, whichever
// stage would have sent it.
func TestLevelModelProviderAuthRefused(t *testing.T) {
	cfg := strings.Replace(levelPinned(t, "l3", deepseek), "auth: api-key", "auth: oauth", 1)
	if cfg == levelPinned(t, "l3", deepseek) {
		t.Fatal("the fixture has no auth line")
	}
	g := providerGW(t, cfg, "acme/app")
	refusedBeforeAnyCall(t, g, "agent.levels.l3.model", deepseek, "never sent to another provider", "agent.auth: api-key")
}

// TestLevelModelProviderGatewayRefused: observe never refuses a call and a
// provider's calls are real dollars, so a level's provider model needs
// budget.mode enforce like any other routed model. The pins here are Claude's
// own, so the level's model is the only provider-served model in the run and
// the refusal this run reports is about the level.
//
// The runner reports this refusal by its message alone (checkProviderModels
// returns ps[0].Message), so the level's key is not in it — the model is,
// which is what makes the case about the level. The key is asserted where the
// problem is built, in the allow_data_to and auth cases above.
func TestLevelModelProviderGatewayRefused(t *testing.T) {
	g := providerRun(t, levelPinned(t, "l3", deepseek), "observe", "50", nil, "acme/app")
	refusedBeforeAnyCall(t, g, deepseek, "observe never refuses a call", "budget.mode enforce")
}

// TestLevelModelNotInTheAllowListRefused: the owner's allowed_models bounds
// every model a run may use, and a review level's own model is one of them. A
// repository that names a level model must not reach one the owner's list
// leaves off: the list is about which models may run, not about which role
// slots name them, and a level model is exactly the way round a repository's
// own file would otherwise reach one.
//
// The run's own pins are all on the list (the coder and the reviewer on
// sonnet, the background on haiku) and the level's model is the only model off
// it, so the refusal can only be about the level.
func TestLevelModelNotInTheAllowListRefused(t *testing.T) {
	cfg := gwConfig(t, "  first_line_review: off\n  levels: { l2: { model: "+opus+" } }\n")
	g := newGW(t, cfg, "enforce", "50")
	g.deps.Spend = ceilingOf(t, runner.BudgetModeEnv, "enforce", runner.MaxRunUSDEnv, "50",
		runner.AllowedModelsEnv, sonnet+","+haiku)
	refusedBeforeAnyCall(t, g, "agent.levels.l2.model", opus, "allowed_models from ceiling")
}

// TestLevelModelAliasRefused: CheckPins is the rule that a run under a budget
// names model IDs and never aliases, so the price it charges is the price in
// the table. A level's own model is judged by it too, and an alias is the
// case only that check catches: a Claude alias carries no "/" and no provider
// claims it, so the provider checks pass it through by design.
func TestLevelModelAliasRefused(t *testing.T) {
	g := providerGW(t, levelPinned(t, "l3", "opus"), "acme/app")
	refusedBeforeAnyCall(t, g, "agent.levels.l3.model", "opus is an alias")
}

// levelCall is a stage that makes one call on the model the runner asked for —
// what Claude Code does with the --model the runner passes it — and records
// that model with the call's status and body.
func levelCall(model *string, status *int, body *string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*model = req.Model
		*status, *body = post(t, req, req.Model, 4096)
		return inner(t, ctx, req)
	}
}

// TestLevelModelIsTheModelTheStageAsksFor: the model a stage asks the agent
// for is the model the gateway admits for that stage. The gateway admits a
// call on the stage's own model and nothing else (gateway.Stage.Model, which
// stageModel answers), so a review level whose model a layer named has to be
// the model its stage requests. Where the two disagree every call of the stage
// is refused as not pinned for it, and the run dies in its review — the pins
// name the level's model, the set the gateway holds names the level's model,
// and only the request is left asking for the role's.
//
// The level 2 model here is Claude's own opus, a model the built-in table
// prices, so the run needs no route: what this test is about is the stage's
// request, not the provider half.
func TestLevelModelIsTheModelTheStageAsksFor(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	cfg := gwConfig(t, "  first_line_review: off\n  levels: { l2: { model: "+opus+" } }\n")
	g := newGW(t, cfg, "enforce", "50",
		anthropicfake.MessageOK(sonnet, use), anthropicfake.MessageOK(opus, use))

	var coderModel, reviewModel string
	var coderStatus, reviewStatus int
	var reviewBody string
	rec, err := g.run(t,
		levelCall(&coderModel, &coderStatus, new(string), implement("feature")),
		levelCall(&reviewModel, &reviewStatus, &reviewBody, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if coderModel != sonnet {
		t.Errorf("the implement stage asked for %q, want the coder's own model %q", coderModel, sonnet)
	}
	if reviewModel != opus {
		t.Errorf("the review stage asked for %q, want the level's own model %q: the stage must ask for the model the gateway admits for it", reviewModel, opus)
	}
	if coderStatus != 200 || reviewStatus != 200 {
		t.Fatalf("statuses = %d, %d: a stage's own call was not admitted\nreviewer: %s", coderStatus, reviewStatus, reviewBody)
	}
	if n := g.fake.Count(); n != 2 {
		t.Errorf("Anthropic saw %d calls, want the two stages'", n)
	}
}
