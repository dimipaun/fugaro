package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
)

// These are triageUnserved's own cases: the predicate reads four facts about a
// run and a configuration, and the integration cases in
// triage_stage_test.go exercise it through a whole run for the shapes a run can
// be configured into. The one below is the fact a whole run cannot be put in,
// because the configuration itself refuses it long before the loop starts
// (config.CheckProviderPolicy): a pin on a "vendor/model" ID no route claims.

// TestTriageUnservedSparesAModelNoAnthropicUpstreamServes is the
// config.IsClaudeModel guard's case, and the reason it exists: the refusal the
// clause stops is the Anthropic upstream's for the key it does not hold
// (internal/gateway's missingAnthropicKey), so it is only ever the refusal of a
// call that upstream would answer. A model that is no Claude one is not one
// that upstream ever serves, whatever the run's key is, and the clause must
// leave it alone and ask the judge as before.
//
// The run below is otherwise exactly the shape the clause stops on — api-key,
// the gateway on, no stage account, no Anthropic key in its environment, no
// route claiming the model — so deleting the IsClaudeModel guard turns this
// round into a skip and fails here. A whole run cannot be put in this shape:
// CheckProviderPolicy refuses a "vendor/model" pin no route claims before the
// loop starts (unclaimedModelRefusal), which is why the predicate is asked
// directly and the models are read from the run's own configuration
// (stageModel).
func TestTriageUnservedSparesAModelNoAnthropicUpstreamServes(t *testing.T) {
	const providerModel = "deepseek/deepseek-v4-flash"
	r := svcRun(t, "api-key", func(r *run) {
		r.spend.Mode = policy.ModeObserve
		r.cfg.Agent.Models = config.ModelRoles{Coder: providerModel, Reviewer: providerModel}
	})
	if got := r.stageModel(stageTriage); got != providerModel {
		t.Fatalf("the triage stage runs %q, want the reviewer's own pin %q: the case is about that model", got, providerModel)
	}
	if config.IsClaudeModel(providerModel) {
		t.Fatalf("config.IsClaudeModel says %q is a Claude model, so this case tests nothing", providerModel)
	}
	if r.triageUnserved() {
		t.Errorf("the triage round was skipped for %q: the key the clause looks for is the Anthropic upstream's, which such a model never reaches, so the judge is asked (config.IsClaudeModel)", providerModel)
	}
}
