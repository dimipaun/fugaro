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
// allow-list sets no model rule, and this service then applies no model rule
// either: the ceiling is the job's own configuration, which is not a thing this
// service can read. Everything else in the check — the server-tool refusal, the
// shape refusal, the model's own max_tokens bound — applies either way.

import (
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
// is the project's allow-list and its routes are nil — see below.
//
// The routes are nil, so the two rules that are about a *provider route* — the
// variant suffix that changes a route's routing and price, and the foreign
// field a route does not take — cannot fire: this service serves the Anthropic
// upstream, where an accepted call goes whatever its model is called
// (internal/credsvc's forward). A caller that routes nothing has no routed
// call.
//
// A body the gateway cannot read at all is a refusal too — the gateway's own
// path answers parseRequest's error and check's violation alike, as a 400 — so
// it comes back as the refusal's sentence. An error is the service's own state
// and not the caller's: the project's prices could not be read, which the
// caller cannot fix and which is answered as the unavailability it is.
func (s *Server) requestRefusal(shared *projectpolicy.Policy, body []byte) (string, error) {
	table, err := requestPriceTable(shared, s.now())
	if err != nil {
		return "", err
	}
	refusal, err := gateway.RequestRefusal(body, "", requestPin(shared), table, nil)
	if err != nil {
		return err.Error(), nil
	}
	return refusal, nil
}

// requestPin is the model rule this service holds a call to: the models the
// project's published policy allows, and no stage's own bound (there is no
// stage here).
//
// The list is the policy's own, as the operator wrote it, and the gateway's
// comparison reads it the way the gateway reads a stage's pins (sameModel: the
// same ID, or two spellings the price table resolves to one model). An empty
// list is no model rule, which is what a policy that publishes none means: the
// run's ceiling is then the job's own configuration (H1) and this service
// cannot read it. Nothing here narrows or widens the list — a rule invented
// here would be a second allow-list, which is the thing this file exists to
// avoid.
func requestPin(p *projectpolicy.Policy) gateway.RequestPin {
	var models []string
	if p != nil {
		models = p.Budget.AllowedModels
	}
	return gateway.RequestPin{Models: models, Where: requestPinWhere}
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
