package runner

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// diagnoseRouteRun is the minimal run gatewayRoutes needs for the diagnose
// stage's model: pins on Claude IDs no route claims, and one provider serving
// the family the diagnose stage names, with its key mounted as a job's
// environment has it (the placeholder is not a credential, and gatewayRoutes
// refuses to build a route without one).
func diagnoseRouteRun(diagnose bool, diagnoseModel string) *run {
	r := &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth: "api-key",
			Models: config.ModelRoles{
				Coder: "claude-sonnet-5-5", Reviewer: "claude-sonnet-5-5",
				Background: "claude-sonnet-5-5", Diagnose: diagnoseModel,
			},
		}},
		diagnose: diagnose,
		d: Deps{
			// A sink, so the run's own logging does not reach a nil logger.
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Env: []string{config.ProviderKeyEnv("openrouter-api-key") + "=" + routedProviderKey},
			Providers: map[string]config.ModelProvider{
				"openrouter": {
					Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
					Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
					AllowDataTo: []string{"acme/app"},
				},
			},
		},
	}
	return r
}

// TestGatewayRoutesCarryTheDiagnoseModelsRoute: the diagnose stage's model is a
// model this run calls, so a provider serving it has to have its route
// registered, or the call is admitted and charged and then goes direct to
// Anthropic on an ID no Anthropic endpoint answers to — where the endpoint the
// owner approved is the one it never reaches. A run that does not execute the
// diagnose step registers no route for it, so a provider's key is not read for
// a stage that will not run.
//
// Hand mutations this fails on: dropping the diagnose model from the list
// (no route is registered and the call goes direct); adding it unconditionally
// (a run with no diagnose step reads a key it never uses).
func TestGatewayRoutesCarryTheDiagnoseModelsRoute(t *testing.T) {
	const providerModel = "deepseek/deepseek-v4-flash"
	withStage := diagnoseRouteRun(true, providerModel)
	routes, err := withStage.gatewayRoutes()
	if err != nil {
		t.Fatalf("gatewayRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != "openrouter" {
		t.Fatalf("routes = %+v, want the diagnose model's own route", routes)
	}
	if key, err := routes[0].Credential(); err != nil || key != routedProviderKey {
		t.Errorf("the route's credential = %q, %v, want the mounted key", key, err)
	}
	if got := withStage.redact("using " + routedProviderKey); strings.Contains(got, routedProviderKey) {
		t.Errorf("the route's key is not redacted from the run's logs: %q", got)
	}
	withoutStage := diagnoseRouteRun(false, providerModel)
	routes, err = withoutStage.gatewayRoutes()
	if err != nil {
		t.Fatalf("gatewayRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("routes = %+v, want none: this run runs no diagnose stage", routes)
	}
	if len(withoutStage.secrets) != 0 {
		t.Errorf("a run with no diagnose step read the provider's key %v", withoutStage.secrets)
	}
}
