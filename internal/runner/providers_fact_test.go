package runner

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// R12's fact at the runner (KS5): a job whose environment carries it is a job
// whose repository's provider keys the installation's credentials service
// holds, so this run's provider calls are the service's and its own environment
// holds no provider key at all. A job that does not carry it keeps its mount,
// which is every job an installation behind this release planned — release A's
// `ready` does not change.

// providerFactModel is the model the fact tests pin: one the fixture provider
// claims, so gatewayRoutes builds exactly one route from it.
const providerFactModel = "deepseek/deepseek-v4-flash"

// providerFactKey is the placeholder a job of the mount path mounts as the
// provider's key. It is not a credential and never was one: nothing here talks
// to a provider, and ProviderCredential asks only that a value of the right
// shape is present.
const providerFactKey = "sk-or-v1-0123456789abcdef-not-a-real-key"

// providerFactRun is the minimal run the fact is read on: a run whose model
// calls reach the credentials service (auth cascade, not local), one provider
// serving every pin this run has, and the job's environment exactly as the case
// spells it.
func providerFactRun(env ...string) *run {
	return &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth: config.AuthCascade,
			Models: config.ModelRoles{
				Coder: providerFactModel, Reviewer: providerFactModel, Background: providerFactModel,
			},
		}},
		d: Deps{
			// A sink, so the run's own logging does not reach a nil logger.
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Env: env,
			Providers: map[string]config.ModelProvider{
				"openrouter": {
					Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
					Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
					AllowDataTo: []string{"acme/app"}, RouteFeePct: 5,
				},
			},
		},
	}
}

// providerFactEnv is the fact as a job carries it, beside whatever else the
// case mounts.
func providerFactEnv(more ...string) []string {
	return append([]string{config.ProviderKeysServedEnv + "=" + config.ServedValue}, more...)
}

// TestARunWithoutTheFactUsesItsMount is the first half of R12's rule: a run
// whose job does not carry the fact reads its provider key from the
// environment and routes as it always did — and one that carries it where the
// service is not reached keeps its mount too, which is R12's own sentence
// ("uses the service when it can and the mount when it must") rather than a
// ladder of its own.
//
// Hand mutations this fails on: reading the fact alone, without asking whether
// this run's calls reach the service at all (a local run's route would be
// served, and a served route is admitted beside the service upstream alone —
// the gateway would refuse to start); serving a repository the switch does not
// carry.
func TestARunWithoutTheFactUsesItsMount(t *testing.T) {
	key := config.ProviderKeyEnv("openrouter-api-key") + "=" + providerFactKey

	for _, tc := range []struct {
		name string
		env  []string
		edit func(*run)
	}{
		{"no fact at all", []string{key}, nil},
		// The fact is set and the service is out of reach: a local invocation
		// (D149's local run has no service at all), and a repository whose
		// Anthropic calls go straight out, where a served route would be a
		// route the gateway cannot start with.
		{"the fact, local", providerFactEnv(key), func(r *run) { r.d.Local = true }},
		{"the fact, and auth no switch carries", providerFactEnv(key), func(r *run) {
			r.cfg.Agent.Auth = "api-key"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := providerFactRun(tc.env...)
			if tc.edit != nil {
				tc.edit(r)
			}
			if r.providerKeysThroughService() {
				t.Fatal("this run's routed calls are not the service's, and the fact was read as if they were")
			}
			routes, err := r.gatewayRoutes()
			if err != nil {
				t.Fatalf("gatewayRoutes: %v", err)
			}
			if len(routes) != 1 {
				t.Fatalf("gatewayRoutes() = %+v, want the one route the run's model names", routes)
			}
			got := routes[0]
			if got.Served {
				t.Errorf("a route with no service to reach is served: %+v", got)
			}
			if got.Credential == nil {
				t.Fatalf("a route of the mount path carries no credential source: %+v", got)
			}
			k, err := got.Credential()
			if err != nil || k != providerFactKey {
				t.Errorf("the route's key = %q, %v, want the mounted one", k, err)
			}
			if !slices.Contains(r.secrets, providerFactKey) {
				t.Errorf("the mounted key is not registered for redaction: %v", r.secrets)
			}
		})
	}

	// The mount path still refuses a run whose key is not mounted, at the same
	// place and with the same sentence: the fact is what turns that refusal
	// off, and nothing else has.
	if _, err := providerFactRun().gatewayRoutes(); err == nil || !strings.Contains(err.Error(), "is not mounted") {
		t.Errorf("a run with no key and no fact built its routes: %v", err)
	}
}

// TestProviderKeysServedRoutesThroughTheService is KS5's acceptance at the
// runner: with the fact set and the service reachable, gatewayRoutes returns a
// SERVED route — no credential of this job's, and the same name, models, base
// and fee the config holds — and neither the route's key nor the refusal of
// its absence is read out of the environment. A served repository's job may
// mount no provider key at all, which is the whole point of the fact, and a
// pool member of that provider counts as available by the fact rather than by
// a key in the environment (poolMemberHeld).
//
// Both ways a run reaches the service are pinned: a cascade run's model path,
// and a switched repository's run (the case R12's fact is really about), whose
// service upstream is the one the gateway was already built with.
//
// Hand mutations this fails on: building the route through GatewayRoute with a
// credential it cannot read (the run fails before its gateway starts);
// requiring the key's presence on the served path (a served repository's job
// refuses every stage); leaving poolMemberHeld on the environment question (a
// stage the run can be served by is refused before it opens).
func TestProviderKeysServedRoutesThroughTheService(t *testing.T) {
	mount := config.ProviderKeyEnv("openrouter-api-key") + "=" + providerFactKey

	for _, tc := range []struct {
		name string
		env  []string
		edit func(*run)
	}{
		{"a cascade run", providerFactEnv(), nil},
		{"a switched repository's run", providerFactEnv(mount), func(r *run) {
			r.cfg.Agent.Auth = "api-key"
			r.d.Env = append(r.d.Env, credentialsServiceURLEnv+"=https://creds.example")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := providerFactRun(tc.env...)
			if tc.edit != nil {
				tc.edit(r)
			}
			if !r.providerKeysThroughService() {
				t.Fatal("a job carrying the fact on the service path is not served by it")
			}
			routes, err := r.gatewayRoutes()
			if err != nil {
				t.Fatalf("gatewayRoutes: %v", err)
			}
			if len(routes) != 1 {
				t.Fatalf("gatewayRoutes() = %+v, want the one route the run's model names", routes)
			}
			got := routes[0]
			if !got.Served {
				t.Errorf("the fact is set and the route is not served: %+v", got)
			}
			if got.Credential != nil {
				t.Error("a served route carries a credential source of this job's own")
			}
			// Everything the route still decides is the config's: what it
			// claims, where the service forwards (F45), and the fee the run
			// charges with (R1's "keeps its own ... fee, price check and
			// settle").
			p := r.modelProviders()["openrouter"]
			if got.Name != "openrouter" || got.FeePct != p.RouteFeePct || got.BaseURL != p.BaseURL || len(got.Models) != 1 || got.Models[0] != p.Models[0] {
				t.Errorf("the served route = %+v, want the config's own route for %s", got, p.Secret)
			}
			// The runner reads a provider key out of its environment on the
			// other path alone (R12): nothing was read here, and nothing was
			// registered for redaction either, because there is nothing to
			// redact.
			if len(r.secrets) != 0 {
				t.Errorf("a served route registered %d secret(s): %v", len(r.secrets), r.secrets)
			}
			// A pool member of that provider is available by the fact, not by
			// a key: this container holds none, and asking the environment
			// would refuse a stage the service can serve.
			if !r.poolMemberHeld(config.PoolMember{Name: "openrouter", Type: config.CredentialTypeOpenRouter}) {
				t.Error("a pool member of a served provider is not held by a run whose job carries the fact")
			}
		})
	}

	// The control the two cases above are a change from: without the fact the
	// same run, on the same service path, reads the mounted key and its pool
	// member is held by that key's presence.
	plain := providerFactRun(mount)
	if plain.providerKeysThroughService() {
		t.Fatal("the control run is served, so it pins nothing")
	}
	if !plain.poolMemberHeld(config.PoolMember{Name: "openrouter", Type: config.CredentialTypeOpenRouter}) {
		t.Error("a pool member whose key is mounted is not held")
	}
	if r := providerFactRun(); r.poolMemberHeld(config.PoolMember{Name: "openrouter", Type: config.CredentialTypeOpenRouter}) {
		t.Error("a pool member whose key is not mounted is held")
	}
}
