package credsvc

// The request check (C1.11 follow-up): the service refuses a call the gateway
// would refuse, before the bundle is loaded.
//
// The service answers a run's calls with a credential the run does not hold,
// and an agent inside the job holds the job's own ID token — so it can call the
// service directly, past the in-runner gateway, past the gateway's pins and past
// the binding ledger. C1.11 forwarded any body it was handed; this is the other
// half: the rules are the gateway's own (gateway.RequestRefusal, which the
// gateway's request path applies through the same function) and they are
// applied before the one bundle that holds every credential is read, so a
// refused request never reaches a credential.
//
// The pin is what this service can honestly know. The gateway pins a call to
// the stage's own models, which come from the repository's configuration and
// never reach this service; the ceiling the run's pins are themselves held to
// is the project's published policy (budget.allowed_models, config.CheckAllowed
// — H1), and that is what a call is pinned to here. A project that publishes no
// allow-list leaves this service no model rule at all, and the ceiling the run
// is then held to is the job's own configuration — which this service cannot
// read — so the call is REFUSED rather than pinned to nothing (the owner's
// ruling of 2026-10-22): the service fails closed, and the refusal names the
// way out. Everything else in the check — the server-tool refusal, the shape
// refusal, the model's own max_tokens bound — applies to every call this
// service does serve, alongside the pin. The gateway's
// own behaviour for an empty list is unchanged: its path pins a call to the
// stage's models, and reading the project's policy is this service's job and
// not the gateway's.

import (
	"maps"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// requestPinWhere names the pin in the refusal. It is the gateway's own
// sentence with this service's list in it: "model X is not pinned for the
// project's model allow-list".
const requestPinWhere = "the project's model allow-list"

// requestNoAllowList is the refusal a project that publishes no model
// allow-list gets (the owner's ruling of 2026-10-22): this service has no model
// rule to pin the call to, and it will not forward under none, so it refuses —
// fail closed — and says the one thing the caller can act on, which is where
// the allow-list belongs. The project layer is the only layer that publishes
// it (docs/project-layer.md: `budget.allowed_models` is policy, and the
// repository may only tighten it), so naming it is naming the way out.
//
// It is a sentence and not a value: nothing of the request and no credential
// value is in it.
const requestNoAllowList = "the project publishes no budget.allowed_models; add the allow-list to the project layer"

// requestNoAllowListRefusal is the model rule this service applies when the
// project's published policy carries none: the refusal above, and "" when the
// policy does publish a list.
//
// It is the service's own rule and not the gateway's: the gateway's check takes
// an empty pin as "no model rule" (gateway.RequestPin) and that reading is
// unchanged, because the gateway's path has the stage's own pins and never the
// project's. This service has only the project's, so an empty list here is not
// a caller that pins nothing — it is a ceiling nobody published, which is
// exactly the call an agent holding the job's own ID token could make past the
// in-runner gateway.
//
// The list is read the one way requestPin reads it, so the refusal and the pin
// can never disagree about which projects publish a model rule.
func requestNoAllowListRefusal(p *projectpolicy.Policy) string {
	if len(requestModels(p)) > 0 {
		return ""
	}
	return requestNoAllowList
}

// requestRefusal is the gateway's own request check over this call's body:
// the model pin, the server-tool refusal, the shape refusal, the variant
// refusal and the max_tokens bounds, as internal/gateway spells them.
//
// It is the gateway's function and not a copy of it — the whole point of the
// export — so a rule that changes for the gateway changes here, and a body the
// gateway would forward to the same upstream under the same pin is a body this
// service forwards. The pin is where the two part company: a body the gateway
// forwards to a *provider route* (a model the stage's pins admit only through
// the routing switch) is refused here as unpinned, because this service's pin
// is the project's allow-list — which is the same and only rule a routed call
// is pinned to (the owner's ruling of 2026-10-22, and R11 for the provider
// route).
//
// The pin and the routes are the caller's, because the two paths this service
// serves a call under hold different ones: a Messages call to the Anthropic
// upstream is pinned to the project's allow-list and routes nothing (a caller
// that routes nothing has no routed call, so the two rules that are about a
// provider route — the variant suffix, the foreign field — cannot fire), and a
// routed call is pinned to the same list and carries the policy's own providers
// as its routes (providerRoutes), which is what makes those two rules fire for
// it. Neither argument is a rule this file invents: both are facts the caller
// read, and the check itself is unchanged whichever it is handed.
//
// A body the gateway cannot read at all is a refusal too — the gateway's own
// path answers parseRequest's error and check's violation alike, as a 400 — so
// it comes back as the refusal's sentence. An error is the service's own state
// and not the caller's: the project's prices could not be read, which the
// caller cannot fix and which is answered as the unavailability it is.
func (s *Server) requestRefusal(shared *projectpolicy.Policy, body []byte, pin gateway.RequestPin, routes []gateway.Route) (string, error) {
	table, err := requestPriceTable(shared, s.now())
	if err != nil {
		return "", err
	}
	refusal, err := gateway.RequestRefusal(body, "", pin, table, routes)
	if err != nil {
		return err.Error(), nil
	}
	return refusal, nil
}

// providerRoutes is the published policy's providers block as the gateway's own
// routes (R11): one route per provider the project names, claiming exactly the
// models that provider's block claims, which is what "the service builds the
// provider routes from the published policy" means.
//
// They are the routes the request check is run against (requestRefusal), so the
// two rules that are about a provider route — a model ID carrying a variant
// suffix, and a top-level field a provider route does not take — fire for a
// routed call exactly as they fire for a call the run's own gateway forwards to
// a provider. That is the half that keeps a HAND-BUILT call to this route from
// escaping the rules: an agent holding the job's own token can compose a body
// and post it here, and a route that judged it by none of the rules a route
// carries would forward whatever it composed (D3003).
//
// What a route carries beyond its name and its claims is deliberately absent.
// This is the shape a REQUEST is judged by — gateway.RequestRefusal is the only
// reader, and it reads those two fields and no other (Route.matches) — and the
// facts a call actually needs to go out are the bundle entry's: where the
// provider is and how its key is sent (D1418). The policy does not carry them
// and must not; a route built here that named an address or held a credential
// would be a route one could forward with, and the one path that forwards to a
// provider builds its own from the entry (providerRoute, forward.go).
//
// The order is the providers sorted by name, so a model two providers claim —
// which the policy's own check refuses to publish (validProviders) — would still
// be judged the same way twice, and a refusal cannot depend on a map's
// iteration order.
func providerRoutes(p *projectpolicy.Policy) []gateway.Route {
	if p == nil || len(p.Providers) == 0 {
		return nil
	}
	names := slices.Sorted(maps.Keys(p.Providers))
	out := make([]gateway.Route, 0, len(names))
	for _, name := range names {
		out = append(out, gateway.Route{Name: name, Models: slices.Clone(p.Providers[name].Models)})
	}
	return out
}

// providerRequestRefusal is the gateway's own request check over a routed
// call's body, and the type the refusal is: which of the two the policy's rules
// answered and which the request's own shape (R11, D3003).
//
// The check is the gateway's (requestRefusal) and it is run with the routes the
// published policy builds, so a routed call is judged by the two rules a
// provider route adds (providerRoutes). What this function adds is the TYPE,
// and it exists because the wire has one word for each half and the gateway
// answers one sentence for whichever rule failed first without saying which:
// `provider_not_allowed` is a call the published policy refuses, and
// `request_refused` is one the gateway's request check refuses on any path.
//
// Which half answered is read by asking the same check the same question with
// the pin taken away (an empty RequestPin is "no model rule", whose reading is
// the gateway's own). A body that check ACCEPTS was refused by the project's
// model rule and by nothing else — a variant suffix, a foreign field, a shape
// violation and a max_tokens bound all survive a pin-less read — so the type is
// the policy's. The second read happens only where the call is refused anyway,
// and it is the same function over the same body, so the two can never disagree
// about which rules exist or about which of them a body meets.
func (s *Server) providerRequestRefusal(shared *projectpolicy.Policy, body []byte) (typ string, refusal string, err error) {
	routes := providerRoutes(shared)
	refusal, err = s.requestRefusal(shared, body, requestPin(shared), routes)
	if err != nil || refusal == "" {
		return "", refusal, err
	}
	shape, err := s.requestRefusal(shared, body, gateway.RequestPin{}, routes)
	if err != nil {
		return "", "", err
	}
	if shape != "" {
		return ErrorRequestRefused, shape, nil
	}
	return ErrorProviderNotAllowed, refusal, nil
}

// requestPin is the model rule this service holds a call to: the models the
// project's published policy allows, and no stage's own bound (there is no
// stage here).
//
// The list is the policy's own, as the operator wrote it, and the gateway's
// comparison reads it the way the gateway reads a stage's pins (sameModel: the
// same ID, or two spellings the price table resolves to one model). Nothing
// here narrows or widens the list — a rule invented here would be a second
// allow-list, which is the thing this file exists to avoid.
//
// It is only ever built for a policy that publishes a list:
// requestNoAllowListRefusal refuses the call before this pin is used, so the
// empty list the gateway reads as "no model rule" is never handed to it from
// here.
func requestPin(p *projectpolicy.Policy) gateway.RequestPin {
	return gateway.RequestPin{Models: requestModels(p), Where: requestPinWhere}
}

// requestModels is the project's published model allow-list, and nil for a
// policy that publishes none. It is the one reader of the field, so the pin and
// the refusal above are about the same list.
func requestModels(p *projectpolicy.Policy) []string {
	if p == nil {
		return nil
	}
	return p.Budget.AllowedModels
}

// requestPriceTable is the price table the check reads a model's own bounds
// from: the embedded catalogue with the project's own model_prices overrides,
// built through config.PriceTable, the one place a table is built (the routes
// and the built-in catalogue are not this service's to add — it has neither).
//
// It is the table a run's own gateway prices and bounds a call by, which is
// what makes this service's "max_tokens is above the model's maximum" the same
// refusal the run would have got — for a model the embedded catalogue knows,
// which is the ordinary case: the overrides are the owner's, and a model they
// price is priced by their rates here as it is there.
//
// The one place it is not the same table: pricing.Overrides carries rates only
// (pricing.Rates), so a model the project prices that the embedded catalogue
// does not have lands here on a rate-only row with no output maximum, and this
// service bounds it by pricing.DefaultMaxOutputTokens. A run's table is built
// with the catalogue, so it bounds that same model by the catalogue's own
// limit. The bound here can therefore be the looser of the two, and a
// max_tokens the run would have refused can be forwarded by this service.
func requestPriceTable(p *projectpolicy.Policy, now time.Time) (*pricing.Table, error) {
	var overrides pricing.Overrides
	if p != nil && len(p.Prices) > 0 {
		raw, err := p.Prices.CatalogJSON()
		if err != nil {
			return nil, err
		}
		if overrides, err = pricing.ParseOverrides(string(raw)); err != nil {
			return nil, err
		}
	}
	return config.PriceTable(nil, overrides, nil, now)
}
