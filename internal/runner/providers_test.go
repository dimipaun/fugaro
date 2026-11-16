package runner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestProvidersEnvRoundTrip(t *testing.T) {
	p := orProvider
	p.AllowDataTo = []string{"acme/app", "acme/private-one"}
	q := orProvider
	q.Secret, q.Models, q.AllowDataTo = "other-key", []string{"qwen/*"}, []string{"acme/else"}
	all := map[string]config.ModelProvider{"openrouter": p, "other": q}
	v, err := runner.ProvidersEnv(all, "Acme/App")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v, "acme/private-one") {
		t.Fatalf("another repository the owner listed is in the job's env: %s", v)
	}
	if strings.Contains(v, "other-key") {
		t.Fatalf("a provider the repo may not use: %s", v)
	}
	got, err := runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: v}))
	if err != nil || len(got) != 1 || got["openrouter"].Secret != p.Secret || got["openrouter"].Models[0] != "deepseek/*" || !got["openrouter"].AllowsData("acme/app") || len(got["openrouter"].AllowDataTo) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if v, _ := runner.ProvidersEnv(all, "acme/none"); v != "" {
		t.Fatalf("no provider allows it, got %s", v)
	}
	if got, err := runner.ProvidersFromEnv(lookup(nil)); got != nil || err != nil {
		t.Fatalf("unset: %v %v", got, err)
	}
}

// envProvider is the fixture route a job carries: orProvider, which lists
// this repository, so ProvidersEnv writes it.
func envProvider() config.ModelProvider {
	p := orProvider
	p.AllowDataTo = []string{"acme/app"}
	return p
}

// TestProvidersEnvOmitsDefaultSwitchKeys pins plan P4 and Review Focus 6: a
// provider that sets neither of the catalog's switches writes neither key, so
// the job's value is byte-identical to what a pre-catalog fugaro update wrote
// and an older base image — which decodes this strictly — still starts. A
// non-default value is written, because the run has to see it.
func TestProvidersEnvOmitsDefaultSwitchKeys(t *testing.T) {
	v, err := runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": envProvider()}, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer",` +
		`"secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	if v != want {
		t.Errorf("ProvidersEnv =\n%s\nwant\n%s", v, want)
	}
	no := false
	p := envProvider()
	p.RequireZDR, p.PricePolicy = &no, config.PricePolicyHighest
	v, err = runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": p}, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"require_zdr":false`, `"price_policy":"highest"`} {
		if !strings.Contains(v, want) {
			t.Errorf("a non-default switch is not in the job's environment (%s): %s", want, v)
		}
	}
}

// TestProvidersFromEnvAcceptsOldAndNewShapes pins the other side of P4: a
// value written before the two switches existed decodes to their defaults
// (require_zdr on, cheapest), and one that carries them is read.
func TestProvidersFromEnvAcceptsOldAndNewShapes(t *testing.T) {
	const old = `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer",` +
		`"secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	got, err := runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: old}))
	if err != nil {
		t.Fatal(err)
	}
	if p := got["openrouter"]; !p.RequireZDROn() || p.PricePolicySet() != config.PricePolicyCheapest {
		t.Errorf("an old value decoded to require_zdr %v and policy %q", p.RequireZDROn(), p.PricePolicySet())
	}
	const new = `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer",` +
		`"secret":"openrouter-api-key","require_zdr":false,"price_policy":"highest","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	got, err = runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: new}))
	if err != nil {
		t.Fatal(err)
	}
	if p := got["openrouter"]; p.RequireZDROn() || p.PricePolicySet() != config.PricePolicyHighest {
		t.Errorf("a new value decoded to require_zdr %v and policy %q", p.RequireZDROn(), p.PricePolicySet())
	}
}

// TestRunnerSeesCatalogRow pins Review Focus 10 for the runner: the table the
// run charges from carries a catalog row for the route's model, so a surface
// left on a bare pricing.Embedded() fails here rather than pricing a
// different number than the gateway.
func TestSurfaceSeesCatalogRow(t *testing.T) {
	v, err := runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": envProvider()}, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	s, err := runner.SpendFromEnv(lookup(map[string]string{runner.BudgetModeEnv: "observe", runner.ModelProvidersEnv: v}))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := s.Prices.Lookup("deepseek/deepseek-v4.1-flash")
	if !ok {
		t.Fatal("the run's table does not price the route's model")
	}
	if m.Catalog == nil || m.Catalog.Tier != "zdr" || m.Catalog.CheckedAt == "" {
		t.Errorf("the run's row is not catalog-priced: %+v", m.Catalog)
	}
	// A route the catalog does not price — a model it has no row for —
	// leaves the table exactly as it was, so the existing refusal stands.
	q := envProvider()
	q.Models = []string{"deepseek/deepseek-not-in-the-catalog"}
	v, err = runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": q}, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	s, err = runner.SpendFromEnv(lookup(map[string]string{runner.BudgetModeEnv: "observe", runner.ModelProvidersEnv: v}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Prices.Lookup("deepseek/deepseek-not-in-the-catalog"); ok {
		t.Error("a model the catalog does not price got a row")
	}
	if len(s.Prices.Models) != len(pricing.Embedded().Models) {
		t.Errorf("the table has %d rows, want the embedded table's %d", len(s.Prices.Models), len(pricing.Embedded().Models))
	}
}

// A damaged variable is an error, never "no providers".
func TestProvidersFromEnvRefusesDamage(t *testing.T) {
	for name, v := range map[string]string{
		"not json":      `{`,
		"empty":         ``,
		"unknown field": `{"a":{"kind":"anthropic-compat","base_url":"https://x.example","auth":"bearer","secret":"a-key","models":["a/*"],"key":"sk-123"}}`,
		"invalid":       `{"a":{"kind":"anthropic-compat","base_url":"http://x.example","auth":"bearer","secret":"a-key","models":["a/*"]}}`,
	} {
		if got, err := runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: v})); err == nil {
			t.Errorf("%s: accepted %+v", name, got)
		}
	}
}

// TestAVisionModelIsRoutedAndPriced (V7, D2002, D2008): the model a run names
// for images is a pin like any other, so the route that claims it is registered
// off the run's pins (gatewayRoutes) and the run's table prices its calls from
// the route's own row. The coder here is a Claude model the owner has priced
// away its images (an explicit 0, the table's own "images refused"), so the run
// reaches the provider only through the vision model: without the rewrite the
// route would never be registered, because no pin of the run would name a model
// it claims.
//
// Mutation (skip the coder's slot in the rewrite): the stage runs the coder's
// own model, whose images the owner priced away, and its call is refused before
// any route is in play — no call reaches the provider and this test is red.
func TestAVisionModelIsRoutedAndPriced(t *testing.T) {
	const vision = "deepseek/deepseek-v4-vision"
	prices := `{"` + sonnet + `": {"input_per_m": 2, "output_per_m": 10, "image_tokens": 0},` +
		`"` + vision + `": {"input_per_m": 1, "output_per_m": 2, "image_tokens": 1500}}`
	use := pricing.Usage{Input: 1000, Output: 500}
	cfg := withVision(t, gwConfig(t, "  first_line_review: off\n  first_line_rounds: 1\n"), vision)
	g := routeRun(t, cfg, "enforce", "50", []string{"deepseek/*"}, prices,
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(vision, use)}, "acme/app")
	if m, ok := g.deps.Spend.Prices.Lookup(vision); !ok || m.ImageTokens == 0 {
		t.Fatalf("the run's table has no image rate for %s: the route's row is what makes it a model a stage may run for an image", vision)
	}
	var status int
	var body string
	reads := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		status, body = postImage(t, req, vision)
		if status != 200 {
			return agent.Result{}, fmt.Errorf("the image call was refused (%d): %s", status, body)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, reads, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the image call was answered %d %s; rec = %+v, err = %v", status, body, rec, err)
	}
	// The provider's own endpoint saw it, and Anthropic's did not: a routed
	// model whose route is missing goes direct, on a key that cannot serve it.
	if g.compat.Count() != 1 || g.fake.Count() != 0 {
		t.Fatalf("the provider saw %d calls and Anthropic %d, want the vision model's one call routed to its provider", g.compat.Count(), g.fake.Count())
	}
	m, ok := g.deps.Spend.Prices.Lookup(vision)
	if !ok {
		t.Fatalf("no price row for %s", vision)
	}
	one := m.Rates.Cost(use).USD()
	if len(rec.Stages) == 0 || rec.Stages[0].Model != vision || micros(t, rec.Stages[0].ModelUSD) != micros(t, one) {
		t.Fatalf("stages = %+v, want the coder's stage on %s for $%v", rec.Stages, vision, one)
	}
}
