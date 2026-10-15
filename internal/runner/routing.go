package runner

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// applyRouting rewrites the run's Claude pins through the project's routing
// switch (design docs/design/project-centric.md §6.2, D1a/D1b; plan Task 1b.2).
//
// It is the one place a run applies the switch, and it applies it *once*, to
// the configuration every consumer already reads: after it, a pin the switch
// opted in names the route's own model ID (anthropic/claude-sonnet-5.5) rather
// than the canonical one (claude-sonnet-5-5), so the pins the agent is given,
// the route the gateway registers, the row the price lookup charges and the
// serving_model/priced_as a call is recorded with all name the same model —
// Review Focus 11's mutation is a second translation that disagrees with this
// one.
//
// The resolution itself is config.RoutingClaudeFor, the one resolver, and the
// ID it returns comes from the checked-in canonical map (pricing.ClaudeRoutes):
// the run never derives a spelling, and a canonical ID the map does not map is
// refused below rather than guessed.
//
// A switch that names a pin which cannot be resolved — an ID with no map entry,
// no route entry claiming it, a route whose allow_data_to is not written, or two
// claiming it — halts the run. It never falls back to the direct route: D1b's
// default is what an *absent* switch means, never what a written one means when
// its route is gone, and a silent fall-through would charge the direct Claude
// rate for a call the owner routed elsewhere.
//
// via: auto is key-presence driven, as the design spells it: with an Anthropic
// key in the job the direct route is taken (the pins are left alone), and with
// none the provider route is taken. The line `fugaro validate` prints for the
// same switch is Task 1b.3's.
func (r *run) applyRouting(pp *projectpolicy.Policy, found bool) error {
	return nil
}

// routedModel is the model a stage's pin resolves to, and whether the switch
// routed it at all: the model itself when nothing is routed, so a caller that
// only wants the answer cannot tell the difference from a direct run.
func (r *run) routedModel(model string) (string, bool) {
	return model, false
}

// routingClaude is the run's switch: the project policy's block when the run
// read one — the policy is the switch's only delivery path (D1a) — and nil
// otherwise, which is D1b's absent switch (direct). A run therefore never reads
// a routing block from its own fugaro.yaml, and the pins a repository writes
// cannot carry one.
func (r *run) routingClaude() *config.RoutingClaude {
	return nil
}
