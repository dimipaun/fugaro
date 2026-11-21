package runner

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// ModelProvidersEnv carries the owner's model providers (the local config's
// providers block) to a workflow job: compact JSON, an object keyed by the
// provider's name. It holds no key, only the name of the secret that has it,
// and only the providers that list the job's repository in allow_data_to are
// in it, each with allow_data_to cut down to that repository. fugaro update
// --repo sets it from the local config; a repository's fugaro.yaml has no
// say in it.
const ModelProvidersEnv = "FUGARO_MODEL_PROVIDERS"

// maxProvidersBytes bounds the variable: a job's environment is not a
// database.
const maxProvidersBytes = 16 << 10

// ProvidersEnv is the value of ModelProvidersEnv for the providers that may
// receive repo's code, "" when there are none.
func ProvidersEnv(providers map[string]config.ModelProvider, repo string) (string, error) {
	out := ProvidersFor(providers, repo)
	if len(out) == 0 {
		return "", nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	if len(b) > maxProvidersBytes {
		return "", fmt.Errorf("the model providers are %d bytes as %s, more than the %d allowed", len(b), ModelProvidersEnv, maxProvidersBytes)
	}
	return string(b), nil
}

// ProvidersFor is the set of providers a run of repo has, as values: exactly
// the ones ProvidersEnv writes into the job's environment (and the ones a run's
// own routing, pricing and gateway see), each with allow_data_to cut down to
// the repository it is there for.
//
// It is exported for the readers that have to answer a question about a run
// they are not running — `fugaro doctor`'s routing check asks which route a
// repository's runs take — so that answer comes from this filter rather than
// from a second copy of it: a resolution read over the unfiltered block is a
// route a run may not carry, and printing it is the false confidence the check
// exists to remove. repo is the canonical owner/name a run is launched for; an
// empty repo matches only the providers written for every repository ([]string
// {"*"} or an absent allow_data_to), never the ones written for a list.
func ProvidersFor(providers map[string]config.ModelProvider, repo string) map[string]config.ModelProvider {
	out := map[string]config.ModelProvider{}
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		if p := providers[name]; p.AllowsData(repo) {
			// The run only needs to know it is allowed: the other
			// repositories the owner listed stay out of the job's
			// environment.
			p.AllowDataTo = []string{repo}
			out[name] = p
		}
	}
	return out
}

// ProvidersFromEnv reads ModelProvidersEnv. Unset is no providers; a value
// that is not valid is an error (never a silent none: the run would refuse
// every provider model with a misleading reason), reported at bootstrap.
func ProvidersFromEnv(lookup func(string) (string, bool)) (map[string]config.ModelProvider, error) {
	v, ok := lookup(ModelProvidersEnv)
	if !ok {
		return nil, nil
	}
	if len(v) > maxProvidersBytes {
		return nil, fmt.Errorf("%s is %d bytes, more than the %d allowed", ModelProvidersEnv, len(v), maxProvidersBytes)
	}
	dec := json.NewDecoder(strings.NewReader(v))
	dec.DisallowUnknownFields()
	var out map[string]config.ModelProvider
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", ModelProvidersEnv, err)
	}
	if ps := config.ValidateModelProviders(out); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return nil, fmt.Errorf("%s: %s", ModelProvidersEnv, strings.Join(msgs, "; "))
	}
	return out, nil
}

// GatewayRoute is the gateway's route for provider name: built from the
// config's provider alone, so the models the config claims (ProviderFor) and
// the models the gateway routes are the same patterns. cred is the key
// source (ProviderCredential).
func GatewayRoute(name string, p config.ModelProvider, cred func() (string, error)) gateway.Route {
	return gateway.Route{
		Name: name, Models: slices.Clone(p.Models), BaseURL: p.BaseURL, Auth: p.Auth,
		FeePct: p.RouteFeePct, Credential: cred,
	}
}

// ServedGatewayRoute is GatewayRoute for a provider whose key the credentials
// service holds (R12's fact, gateway.Route.Served): the route carries no
// credential of this job's at all, and the gateway reaches the service's
// provider route with the provider's name instead of the provider's own
// address. Everything else about the route — what it claims, its fee, and the
// price the run charges its calls at — is the same, because the service
// attaches the key and forwards and the gateway keeps its own routing, price
// check and settle (R1, KS5).
func ServedGatewayRoute(name string, p config.ModelProvider) gateway.Route {
	r := GatewayRoute(name, p, nil)
	r.Served = true
	return r
}

// providerKeysServed is R12's first fact as this job carries it (C1's name,
// config.ProviderKeysServedEnv): the installation's credentials service holds
// this repository's provider keys, so a routed call is sent to the service's
// provider route rather than out with a key the job mounted (KS5).
//
// Only the contract's own true value answers true (config.ServedValue): an
// absent entry is not served, which is every job planned before the fact
// existed, and the reason a run whose job does not carry it uses its mount as
// it always has.
func (r *run) providerKeysServed() bool {
	return envLookup(r.d.Env, config.ProviderKeysServedEnv) == config.ServedValue
}

// providerKeysThroughService reports whether THIS run's routed calls are the
// credentials service's: the job carries R12's fact (providerKeysServed) and
// this run's own gateway reaches the service at all (serviceModelPath).
//
// The second half is R12's own sentence — "the gateway uses the service when
// it can and the mount when it must": a job whose fact is set but whose calls
// go straight to Anthropic (a local run, or a repository that is not on the
// switch) has no service to send a routed call to, and a served route is
// admitted beside the service upstream alone (gateway.validateRoutes) — so it
// keeps the key its job mounted, as it did before the fact existed. The fact
// is what the owner's update recorded about the BUNDLE, and it is not a claim
// that this job was pointed at the service.
//
// It is one function rather than two readers so that the routes the gateway
// gets and the pool members this run counts as held answer the same question
// the same way (poolMemberHeld).
func (r *run) providerKeysThroughService() bool {
	return r.providerKeysServed() && r.serviceModelPath()
}

// gatewayRoutes are the routes of the providers the run's models name, with
// their keys, which are registered for redaction. A run that names no
// provider model has none, so a provider's key is not even read. A key that
// is not mounted fails the run (an infra_error) before the gateway starts.
//
// Where the job carries R12's fact and this run's calls reach the service
// (providerKeysThroughService), a route is served instead: its key is the
// service's, the gateway sends its calls to the service's provider route with
// the provider's name on them (KS5), and this runner reads the provider's key
// out of its environment on the other path alone (R12) — so a served
// repository's job may mount no provider key at all.
func (r *run) gatewayRoutes() ([]gateway.Route, error) {
	a := r.cfg.Agent
	// Every model a stage of this run runs: the three pins, each review
	// level's own model, which is the model its stage runs (stageModel, L4.5)
	// rather than the role slot the level sits in, and the diagnose stage's
	// when the run executes the diagnose step. A level's model is a pin of its
	// own, so a provider serving one serves this run, and a run that left it
	// out of this list would register no route for that provider at all: the
	// level's call is admitted and charged, and it goes direct to Anthropic.
	// The diagnose model is the same question with more at stake: a provider
	// serving it and no route registered would carry the evidence the run
	// collected to Anthropic on an ID no Anthropic endpoint answers to — a
	// failure, where the level's is a silent charge — and the endpoint that
	// would have been the owner's own approved route is never reached.
	models := []string{a.ModelFor(config.RoleCoder), a.ModelFor(config.RoleReviewer), r.backgroundModel()}
	for _, s := range reviewStages {
		models = append(models, r.stageModel(s.stage))
	}
	if r.diagnose {
		models = append(models, r.stageModel(stageDiagnose))
	}
	used := map[string]bool{}
	for _, m := range models {
		if name, _, ok := config.ProviderFor(r.modelProviders(), m); ok {
			used[name] = true
		}
	}
	served := r.providerKeysThroughService()
	var routes []gateway.Route
	for _, name := range slices.Sorted(maps.Keys(used)) {
		p := r.modelProviders()[name]
		if served {
			routes = append(routes, ServedGatewayRoute(name, p))
			continue
		}
		cred, value, err := ProviderCredential(r.d.Env, p)
		if err != nil {
			return nil, fmt.Errorf("starting the gateway: provider %s: %w", name, err)
		}
		r.addSecret(value)
		routes = append(routes, GatewayRoute(name, p, cred))
	}
	return routes, nil
}

// backgroundModel is config.EffectiveBackground for the run: after
// checkProviderModels it is also agent.models.background itself.
func (r *run) backgroundModel() string {
	return config.EffectiveBackground(r.cfg.Agent, r.modelProviders())
}
