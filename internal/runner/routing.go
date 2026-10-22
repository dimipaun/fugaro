package runner

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
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
// ID it returns comes from the checked-in canonical map (internal/pricing's
// claudeRoutes, unexported and reachable only through RouteIDFor): the run
// never derives a spelling, so a route entry that spells the model its own way
// is refused rather than trusted.
//
// A switch that names a pin which cannot be resolved — an ID with no map row,
// no route entry claiming it, a route whose allow_data_to is not written, or two
// claiming it — halts the run, naming the model and the reason. It never falls
// back to the direct route: D1b's default is what an *absent* switch means,
// never what a written one means when its route is gone, and the fall-through
// would send the call to Anthropic and charge the direct Claude rate for a call
// the owner routed elsewhere.
//
// Whether the switch reaches this run at all is RoutingInertReason, one
// decision shared with the readers that have to say what a run does without
// running one (`fugaro validate`, `fugaro doctor`): a second reading of the
// ladder is how validate came to report a halt no run makes — the gateway-off
// one, and then via: auto under a Vertex credential — and a resolution no run
// takes.
func (r *run) applyRouting(pp *projectpolicy.Policy, found bool) error {
	auth := ""
	if r.cfg != nil {
		auth = r.cfg.Agent.Auth
	}
	if auth == config.AuthCascade {
		// O2 (the owner's ruling of 2026-10-17): a repository may ask for the
		// cascade, and only the operator-written project policy enables it. A
		// cascade workflow whose policy does not carry the switch is refused
		// here, before the run opens anything, naming exactly what to set —
		// `fugaro validate` refuses the same file on the author's own machine
		// (C1.1's cascadeProblems), and this is the run's own side of it.
		var via string
		if found && pp != nil && pp.Routing != nil && pp.Routing.Claude != nil {
			via = pp.Routing.Claude.ViaValue()
		}
		if via != config.RouteCascade {
			return fmt.Errorf("agent.auth is %s and this project's policy does not turn the cascade on: set routing.claude: {via: %s} in the project policy (%s) and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)", config.AuthCascade, config.RouteCascade, projectpolicy.PathShared())
		}
	}
	if !found || pp == nil || pp.Routing == nil || pp.Routing.Claude == nil {
		return nil // absent means direct (D1b)
	}
	block, sw := pp.Routing, pp.Routing.Claude
	why := RoutingInertReason(sw, auth, r.gatewayOn(), envLookup(r.d.Env, "ANTHROPIC_API_KEY") != "")
	if why != RoutingActs {
		if why == RoutingInertAutoVertex {
			r.d.Log.Info("routing.claude: via auto does not route a Vertex run: Vertex serves Claude under this project's own credential, so the pins keep Claude's own IDs (a provider route needs agent.auth: api-key)",
				"code", "routing_auto_vertex")
		}
		// Every skip lands here, before CheckRouting, for one reason: a switch
		// this run has nothing to act on does not halt it either.
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

// GatewayOn reports whether a configuration routes its calls through the
// gateway: the budget is on (observe or enforce; "" and off are off) and the
// credential is a model API's — api-key or vertex, since an oauth run's own
// token never goes through it. It is run.gatewayOn, the run's own predicate,
// spelled for the readers that hold a mode and an auth rather than a run
// (validate, doctor): the gateway is what decides whether the switch has
// anything to act on at all (design §6.2, D11).
func GatewayOn(auth, mode string) bool {
	switch auth {
	case "api-key", "vertex", config.AuthCascade:
	default:
		return false
	}
	return mode == policy.ModeObserve || mode == policy.ModeEnforce
}

// RoutingInert is why the project's routing switch reaches no run of a
// configuration; the zero value, RoutingActs, means it does. applyRouting
// branches on this answer — it is the one place a run applies the switch — and
// a reader that has to say what a run does without running one (validate,
// doctor) asks it rather than re-deriving the ladder.
type RoutingInert string

const (
	// RoutingActs is no reason: the run applies the switch, so it reaches
	// config.CheckRouting and the per-pin resolution below, and a switch it
	// cannot carry out halts it.
	RoutingActs RoutingInert = ""
	// RoutingInertNoSwitch: nothing is published. An absent switch is D1b's
	// default, which is direct.
	RoutingInertNoSwitch RoutingInert = "no-switch"
	// RoutingInertNoGateway: routing exists only with the gateway (design §6.2,
	// D11; plan Task 1b.2 step 3). The gateway is what routes a call to a
	// provider and prices it, and with it off nothing changes — stagePins
	// returns nil and the run's pins reach the agent as --model, where a
	// route's own ID is a model Anthropic does not serve. A budget-off run
	// therefore keeps the direct spelling, and a switch it cannot resolve does
	// not halt it either: the switch has nothing to act on.
	RoutingInertNoGateway RoutingInert = "no-gateway"
	// RoutingInertViaDirect: the switch is written with via: direct, which
	// routes nothing — a legal spelling (D1b's default said out loud).
	RoutingInertViaDirect RoutingInert = "via-direct"
	// RoutingInertAutoVertex: via: auto routes only when the Claude call has
	// nowhere else to go, and it asks that by the credential the run already
	// has. A Vertex run has one — Vertex serves Claude under the owner's own
	// GCP project, and its jobs carry no Anthropic API key — so auto leaves its
	// pins alone rather than reading the absent key as "no route but the
	// provider" and sending the prompt to a third party the switch never chose
	// for this credential. That is a skip, not a refusal: the same policy
	// serves a project's api-key jobs too, and the owner wrote auto to follow
	// the credential. via: openrouter still routes a Vertex run — and is
	// refused downstream for the reason it always is, that a run does not mix
	// credentials (config.CheckProviderAuth).
	RoutingInertAutoVertex RoutingInert = "auto-vertex"
	// RoutingInertAutoKey: via: auto is key-presence driven, as the design
	// spells it. With an Anthropic key stored in the job the direct route is
	// taken and the pins are left alone (D1b); with none the provider route is
	// taken.
	RoutingInertAutoKey RoutingInert = "auto-key"
	// RoutingInertCascadeOtherAuth: the switch is via: cascade and this run's
	// agent.auth is not cascade. The cascade decides which credential carries
	// a call inside the credentials service (D133, D150) and names no model,
	// so it routes nothing for a run that did not ask for it: an installation
	// switches it on for its cascade workflows alone, and every other auth of
	// the same project keeps the direct route it had.
	RoutingInertCascadeOtherAuth RoutingInert = "cascade-other-auth"
)

// RoutingInertReason is the run's own decision about the switch: whether it
// acts, and which of the states above it is in. applyRouting returns on
// RoutingInertReason != RoutingActs before it reaches config.CheckRouting or
// rewrites a pin, and the readers that cannot run a run ask the same function,
// so all of them answer one question one way.
//
// The facts it takes are the run's own: the credential, whether the gateway is
// on (GatewayOn, and run.gatewayOn for a run), and whether the job has an
// Anthropic API key stored. A reader that is not the run does not know the last
// one — the key is a secret version in the cloud, and neither validate nor
// doctor reads the job's environment — so it asks this twice, with the key
// stored and without, and says which of the two states its answer is about
// rather than picking one.
func RoutingInertReason(sw *config.RoutingClaude, auth string, gatewayOn, anthropicKeyStored bool) RoutingInert {
	if sw == nil {
		return RoutingInertNoSwitch
	}
	if !gatewayOn {
		return RoutingInertNoGateway
	}
	if sw.ViaValue() == config.RouteDirect {
		return RoutingInertViaDirect
	}
	if sw.ViaValue() == config.RouteCascade && auth != config.AuthCascade {
		// The cascade is a credential switch (D133, D150): it names no model
		// and routes no call, so it acts only on the workflow that asked for
		// the cascade. For every other auth it is inert, exactly as via:
		// direct is — nothing changes for a run that did not opt in, which is
		// the whole reason O3 chose a spelling of its own over via: auto.
		return RoutingInertCascadeOtherAuth
	}
	if sw.ViaValue() == config.RouteAuto {
		if auth == "vertex" {
			return RoutingInertAutoVertex
		}
		if anthropicKeyStored {
			return RoutingInertAutoKey
		}
	}
	return RoutingActs
}

// routingSwitch is the routing switch this run carries, or nil: the policy
// node's block when the run read one (projectPolicy, called by resolvePolicy),
// and nothing when there is no node. It reads nothing itself — the node's read
// is memoized and resolvePolicy is the one caller — so a later caller asks
// about the switch that was applied, not about a second read that could have
// answered differently.
func (r *run) routingSwitch() *config.Routing {
	if !r.projPolicy.found || r.projPolicy.policy == nil {
		return nil
	}
	return r.projPolicy.policy.Routing
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
