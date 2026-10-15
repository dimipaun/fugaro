package runner

import (
	"fmt"
	"strings"

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
// Resolving into the run's own config, rather than at each consumer, is what
// makes the whole run agree. Every rule that judges a pin — the allow-list, the
// provider's allow_data_to, the pin's price — reads the same Agent, and a
// consumer that resolved on its own would leave those rules judging the
// canonical ID while the gateway charged the route's: a routed call under an
// allow-list that names no route would be allowed by a decision about a model
// nobody sent.
//
// The resolution itself is config.RoutingClaudeFor, the one resolver, and the
// ID it returns comes from the checked-in canonical map (pricing.ClaudeRoutes):
// the run never derives a spelling, so a route entry that spells the model its
// own way is refused rather than trusted.
//
// A switch that names a pin which cannot be resolved — an ID with no map row,
// no route entry claiming it, a route whose allow_data_to is not written, or two
// claiming it — halts the run, naming the model and the reason. It never falls
// back to the direct route: D1b's default is what an *absent* switch means,
// never what a written one means when its route is gone, and the fall-through
// would send the call to Anthropic and charge the direct Claude rate for a call
// the owner routed elsewhere.
//
// via: auto is key-presence driven, as the design spells it: with an Anthropic
// key in the job the direct route is taken (the pins are left alone), and with
// none the provider route is taken. The line `fugaro validate` prints for the
// same switch is Task 1b.3's.
func (r *run) applyRouting(pp *projectpolicy.Policy, found bool) error {
	if !found || pp == nil || pp.Routing == nil || pp.Routing.Claude == nil {
		return nil // absent means direct (D1b)
	}
	if !r.gatewayOn() {
		// Routing exists only with the gateway (design §6.2, D11; plan Task
		// 1b.2 step 3): the gateway is what routes a call to a provider and
		// prices it, and with it off nothing changes — stagePins returns nil
		// and the run's pins reach the agent as --model, where a route's own
		// ID is a model Anthropic does not serve. A budget-off run therefore
		// keeps the direct spelling, and a switch it cannot resolve does not
		// halt it either: the switch has nothing to act on.
		return nil
	}
	block, sw := pp.Routing, pp.Routing.Claude
	if sw.ViaValue() == config.RouteDirect {
		return nil
	}
	// The block is judged against the routes this run has, exactly as the
	// config loader and the policy reader judge it: the run must not act on a
	// switch it cannot carry out.
	if ps := config.CheckRouting(block, r.modelProviders(), "routing"); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return fmt.Errorf("the project's routing switch is not usable: %s", strings.Join(msgs, "; "))
	}
	if sw.ViaValue() == config.RouteAuto && envLookup(r.d.Env, "ANTHROPIC_API_KEY") != "" {
		// auto routes only when the call has nowhere else to go: a stored key
		// is the direct path, and the pins stay as they are (D1b).
		return nil
	}
	for _, p := range r.pinSlots() {
		model := p.get()
		if model == "" || !sw.Names(model) {
			continue // the list is the opt-in: an ID it does not name is untouched
		}
		route, sent, ok := config.RoutingClaudeFor(sw, model, r.modelProviders())
		if !ok || route == config.RouteDirect || sent == model {
			// CheckRouting passed, so this is the belt to its braces: the
			// switch asked for a route and there is none to take.
			return fmt.Errorf("routing.claude names %s, and this run cannot resolve its route: the switch routes nothing rather than sending the call direct (D1b)", model)
		}
		p.set(sent)
	}
	return nil
}

// pinSlot is one of the run's three pins: the model the configuration answers
// for it, and where that answer is written. A pin is set through the field its
// effective model came from, so an agent that names only agent.model keeps its
// coder pin there rather than gaining an explicit one (config.Agent.ModelFor).
type pinSlot struct {
	get func() string
	set func(string)
}

// pinSlots are the run's three pins, in PinnedModels' order: what the agent is
// given as the coder, the reviewer and the background model.
func (r *run) pinSlots() []pinSlot {
	a := &r.cfg.Agent
	// A role reads models.<role>, else agent.model: the rewrite goes back to
	// whichever of the two answered, so the effective model moves and nothing
	// else about the configuration does.
	slot := func(role *string, roleModel config.Role) pinSlot {
		return pinSlot{
			get: func() string { return a.ModelFor(roleModel) },
			set: func(m string) {
				if *role == "" {
					a.Model = m
					return
				}
				*role = m
			},
		}
	}
	bg := pinSlot{get: func() string { return a.Models.Background }, set: func(m string) { a.Models.Background = m }}
	return []pinSlot{slot(&a.Models.Coder, config.RoleCoder), slot(&a.Models.Reviewer, config.RoleReviewer), bg}
}
