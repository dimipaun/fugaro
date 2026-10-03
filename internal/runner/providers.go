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
// in it, each with allow_data_to cut down to that repository. fugaro init
// --repo sets it from the local config; a repository's fugaro.yaml has no
// say in it.
const ModelProvidersEnv = "FUGARO_MODEL_PROVIDERS"

// maxProvidersBytes bounds the variable: a job's environment is not a
// database.
const maxProvidersBytes = 16 << 10

// ProvidersEnv is the value of ModelProvidersEnv for the providers that may
// receive repo's code, "" when there are none.
func ProvidersEnv(providers map[string]config.ModelProvider, repo string) (string, error) {
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

// gatewayRoutes are the routes of the providers the run's models name, with
// their keys, which are registered for redaction. A run that names no
// provider model has none, so a provider's key is not even read. A key that
// is not mounted fails the run (an infra_error) before the gateway starts.
func (r *run) gatewayRoutes() ([]gateway.Route, error) {
	a := r.cfg.Agent
	used := map[string]bool{}
	for _, m := range []string{a.ModelFor(config.RoleCoder), a.ModelFor(config.RoleReviewer), r.backgroundModel()} {
		if name, _, ok := config.ProviderFor(r.d.Providers, m); ok {
			used[name] = true
		}
	}
	var routes []gateway.Route
	for _, name := range slices.Sorted(maps.Keys(used)) {
		p := r.d.Providers[name]
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
	return config.EffectiveBackground(r.cfg.Agent, r.d.Providers)
}
