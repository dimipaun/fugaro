package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// callDeepseek is a step that makes one call on the provider's model (as
// Claude Code would), records the status and the body the agent saw, and
// writes the body to its transcript as Claude Code does.
func callDeepseek(status *int, body *string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*status, *body = post(t, req, deepseek, 1000)
		fmt.Fprintf(req.Transcript, "{\"type\":\"user\",\"message\":%q}\n", *body)
		return inner(t, ctx, req)
	}
}

// TestProviderRunEndsInPR is the end to end of design m10-multi-model.md: a
// run whose coder is deepseek/deepseek-v4-flash goes through the gateway to
// the provider, which gets the provider's key (and only it), and ends in a
// PR with the cost to the micro in result.json.
func TestProviderRunEndsInPR(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "observe", "",
		[]anthropicfake.Reply{anthropicfake.Compat{Cost: 0.0021}.MessageOK(deepseek, use)}, "acme/app")
	prov := g.deps.Providers["openrouter"]
	prov.RouteFeePct = 10
	g.deps.Providers["openrouter"] = prov

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if status != 200 {
		t.Fatalf("the provider call got %d: %s", status, body)
	}
	onlyPR(t, g.provider)

	// The provider got its own key and nothing of the agent's.
	if n := g.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls", n)
	}
	if got := g.compat.Header(0, "Authorization"); got != "Bearer "+providerKey {
		t.Fatalf("the provider saw Authorization %q", got)
	}
	if got := g.compat.Header(0, "X-Api-Key"); got != "" {
		t.Fatalf("the provider saw X-Api-Key %q", got)
	}
	if g.fake.Count() != 0 {
		t.Fatalf("Anthropic saw %d calls of a provider-model run", g.fake.Count())
	}
	// Anthropic's key reaches the agent in no form and the provider's key
	// never does (the agent env is the gateway's token and URL).
	for i, c := range g.agent.calls {
		for _, kv := range c.Env {
			if strings.Contains(kv, providerKey) || strings.Contains(kv, plantedKey) {
				t.Errorf("call %d: a real key is in the agent's env: %s", i, kv)
			}
		}
	}

	// $1/M in, $4/M out, 10% route fee: (1000*1 + 500*4) = 3000 micros, 3300 with the fee.
	m, ok := pricing.Embedded().Lookup(deepseek)
	if !ok {
		t.Fatal("no price")
	}
	want := (float64(use.Input)*m.Rates.InputPerM + float64(use.Output)*m.Rates.OutputPerM) / 1e6 * 1.10
	if rec.Cost == nil || !near(rec.CostUSD, want) || !near(rec.Cost.RouteBy["openrouter"], want) || !near(rec.Cost.ModelBy[deepseek], want) {
		t.Fatalf("want %v: cost_usd = %v, cost = %+v", want, rec.CostUSD, rec.Cost)
	}
	if !near(rec.Cost.ReportedUSD, 0.0021) {
		t.Fatalf("reported_usd = %v, want the provider's figure, which is never charged", rec.Cost.ReportedUSD)
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || !near(stored.Cost.RouteBy["openrouter"], want) || !near(stored.Cost.ReportedUSD, 0.0021) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the provider key is stored in %v", keys)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Fatal("the log has the provider key")
	}
}

// Provider models are real dollars but observe never refuses a call: the run
// says so once at bootstrap (and not under enforce, nor for a Claude-only run).
func TestProviderObserveModeWarns(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	for mode, cap := range map[string]string{"observe": "", "enforce": "5"} {
		g := providerRun(t, providerPinned(t), mode, cap, []anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
		var status int
		var body string
		if rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("%s: rec = %+v, err = %v", mode, rec, err)
		}
		got := strings.Count(g.logs.String(), "code=provider_observe")
		if want := map[string]int{"observe": 1, "enforce": 0}[mode]; got != want {
			t.Errorf("%s: %d observe warnings, want %d:\n%s", mode, got, want, g.logs.String())
		}
	}
	g := newGW(t, gwConfig(t, ""), "observe", "", capScript(1)...)
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.logs.String(), "provider_observe") {
		t.Error("a Claude-only run warned about provider spend")
	}
}

// A provider that reports more than 10% above the charge is logged once at
// the end of the run (warn level, no key); a report below the charge is not.
func TestReportedAboveChargeWarnsOnce(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500} // charged 3000 micros at the starter prices
	for _, c := range []struct {
		cost float64
		warn int
	}{{0.0100, 1}, {0.0031, 0}, {0.0010, 0}} {
		g := providerRun(t, providerPinned(t), "observe", "", []anthropicfake.Reply{anthropicfake.Compat{Cost: c.cost}.MessageOK(deepseek, use)}, "acme/app")
		var status int
		var body string
		if rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if n := strings.Count(g.logs.String(), "code=reported_gap"); n != c.warn {
			t.Errorf("reported %v: %d warnings, want %d:\n%s", c.cost, n, c.warn, g.logs.String())
		}
		if strings.Contains(g.logs.String(), providerKey) {
			t.Error("the log has the provider key")
		}
	}
}

// TestBackgroundDefaultsToCoder: with a provider coder and no background
// model, Claude Code's haiku role is pinned to the coder's model, so no
// hidden Claude call happens; a background model that is set is kept, and a
// Claude coder is untouched (its background is still required by the budget).
func TestBackgroundDefaultsToCoder(t *testing.T) {
	noBackground := strings.Replace(providerPinned(t), ", background: "+haiku, "", 1)
	if noBackground == providerPinned(t) {
		t.Fatal("fixture has no background")
	}
	g := providerRun(t, noBackground, "observe", "", nil, "acme/app")
	if rec, err := g.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for i, c := range g.agent.calls {
		for _, k := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
			if got := envValue(c.Env, k); i == 0 && got != deepseek {
				t.Errorf("stage %d: %s = %q, want the coder's model", i, k, got)
			}
		}
	}

	// An explicit background wins.
	g = providerRun(t, providerPinned(t), "observe", "", nil, "acme/app")
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if got := envValue(g.agent.calls[0].Env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != haiku {
		t.Errorf("an explicit background became %q", got)
	}

	// A Claude coder with no background is refused by the budget, as before.
	g = newGW(t, strings.Replace(gwConfig(t, ""), ", background: "+haiku, "", 1), "observe", "")
	if rec, err := g.run(t); err == nil || !strings.Contains(rec.Reason, "agent.models.background") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestProviderModelNeedsTheGateway: without a budget there is no gateway, and
// a call on a provider model would go to Anthropic: refused, before any call.
func TestProviderModelNeedsTheGateway(t *testing.T) {
	g := providerRun(t, providerPinned(t), "off", "", nil, "acme/app")
	refusedBeforeAnyCall(t, g, "gateway", "budget.mode")
}

// TestMissingProviderKeyIsInfraError: the key is not mounted, so the run does
// not start (no agent), naming the variable and never a value.
func TestMissingProviderKeyIsInfraError(t *testing.T) {
	g := providerRun(t, providerPinned(t), "observe", "", nil, "acme/app")
	g.deps.Env = slices.DeleteFunc(g.deps.Env, func(kv string) bool { return strings.HasPrefix(kv, orProvider.SecretEnv()+"=") })
	refusedBeforeAnyCall(t, g, "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY", "not mounted")
}

// TestHaltOnProviderRun: the cap holds a provider run as it does a Claude
// one: a call whose reservation does not fit is refused at the gateway and
// never forwarded, and the run halts.
func TestHaltOnProviderRun(t *testing.T) {
	g := providerRun(t, providerPinned(t), "enforce", "0.001", nil, "acme/app")
	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")))
	if err != nil {
		t.Fatal(err)
	}
	if status != 403 || g.compat.Count() != 0 {
		t.Fatalf("status = %d, provider calls = %d", status, g.compat.Count())
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v", rec)
	}
}

// TestUpstreamErrorEchoingKeyIsRedacted: a provider that echoes the key it
// was sent in a 401 must not put it in the agent's hands, the transcript,
// result.json, the report, the PR or the logs; neither may a fake claude that
// echoes the key itself. (The gateway half is in internal/gateway.)
func TestUpstreamErrorEchoingKeyIsRedacted(t *testing.T) {
	g := providerRun(t, providerPinned(t), "observe", "",
		[]anthropicfake.Reply{anthropicfake.Error(401, "authentication_error", "invalid api key "+providerKey+" (bearer "+providerKey+")")}, "acme/app")
	var status int
	var body string
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Stderr.Write([]byte("claude: " + providerKey + "\n"))
		res, err := implement("feature")(t, ctx, req)
		res.Text = "done " + providerKey
		return res, err
	}
	rec, err := g.run(t, callDeepseek(&status, &body, leak), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if status != 401 || strings.Contains(body, providerKey) {
		t.Fatalf("the agent saw %d %s", status, body)
	}
	if g.compat.Count() != 1 {
		t.Fatalf("the provider saw %d calls", g.compat.Count())
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v", rec)
	}
	if got, _ := json.Marshal(onlyPR(t, g.provider)); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the PR has the key: %s", got)
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the key is stored in %v", keys)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the record has the key: %s", got)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Fatal("the log has the key")
	}
}

// TestFirstLineErrorEchoingKeyIsRedacted: a first-line stage that fails with
// an error (and stderr) carrying the provider key is skipped with a log line,
// and the key reaches no log, record or stored object.
func TestFirstLineErrorEchoingKeyIsRedacted(t *testing.T) {
	g := providerRun(t, providerFirstLine(t, "on"), "observe", "", nil, "acme/app")
	broken := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Stderr.Write([]byte("claude: " + providerKey + "\n"))
		return agent.Result{}, errors.New("upstream said: bad key " + providerKey)
	}
	rec, err := g.run(t, implement("feature"), broken, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := tiers(rec); !eq(got, "first:none", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if !strings.Contains(g.logs.String(), "the first-line review failed") {
		t.Fatal("the skip was not logged")
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Fatal("the log has the key")
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the key is stored in %v", keys)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the record has the key: %s", got)
	}
}

// TestGatewayRoutesMatchTheConfig: every model the config claims
// (ProviderFor) is routed by the gateway to that provider and no other, and a
// model the config does not claim goes to no provider: the routes are built
// from the config's patterns (runner.GatewayRoute), so they cannot drift.
// Each model is sent through a real gateway to one fake upstream per
// provider.
func TestGatewayRoutesMatchTheConfig(t *testing.T) {
	patterns := []string{"deepseek/*", "exact/model-1", "z-ai/glm-4.*", "x/y*"}
	models := []string{
		"deepseek/deepseek-v4-flash", "deepseek/a", "deepseek-x/a", "exact/model-1", "exact/model-10", "exact/model",
		"z-ai/glm-4.", "z-ai/glm-4.6", "z-ai/glm-5", "x/y", "x/yz", "x/w",
	}
	providers := map[string]config.ModelProvider{}
	fakes := map[string]*anthropicfake.Fake{}
	var routes []gateway.Route
	for i, pat := range patterns {
		name := fmt.Sprintf("p%d", i)
		fk, up := anthropicfake.New(t, replies(len(models))...)
		fakes[name] = fk
		providers[name] = config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: up.URL, Auth: "bearer", Secret: "key-" + name, Models: []string{pat}, RouteFeePct: 1}
		routes = append(routes, runner.GatewayRoute(name, providers[name], func() (string, error) { return "provider-key-0123", nil }))
	}
	if err := config.ValidateModelProviders(providers); len(err) > 0 {
		t.Fatal(err)
	}
	prices := map[string]pricing.Rates{}
	for _, m := range models {
		prices[m] = pricing.Rates{InputPerM: 1, OutputPerM: 1}
	}
	table, err := pricing.Embedded().With(prices)
	if err != nil {
		t.Fatal(err)
	}
	anth, anthUp := anthropicfake.New(t, replies(len(models))...)
	srv, err := gateway.Start(context.Background(), gateway.Options{
		Upstream: gateway.Upstream{Kind: "anthropic", BaseURL: anthUp.URL, APIKey: plantedKey},
		Routes:   routes, Prices: table, Mode: gateway.Observe,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	for _, m := range models {
		srv.BeginStage(gateway.Stage{Name: "implement", Model: m})
		before := map[string]int{}
		for n, fk := range fakes {
			before[n] = fk.Count()
		}
		beforeAnth := anth.Count()
		body := fmt.Sprintf(`{"model":%q,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, m)
		postBody(t, srv.URL()+"/v1/messages", srv.Token(), body)
		srv.EndStage()
		got := ""
		for n, fk := range fakes {
			if fk.Count() > before[n] {
				got += n
			}
		}
		want, claimed := "", false
		if name, _, ok := config.ProviderFor(providers, m); ok {
			want, claimed = name, true
		}
		if got != want {
			t.Errorf("%q: the config claims it for %q (%v), the gateway routed it to %q", m, want, claimed, got)
		}
		if !claimed && anth.Count() == beforeAnth {
			t.Errorf("%q: not claimed, yet it reached nobody", m)
		}
	}
}

// replies are n small replies, for a fake that is asked more than once.
func replies(n int) []anthropicfake.Reply {
	out := make([]anthropicfake.Reply, n)
	for i := range out {
		out[i] = anthropicfake.MessageOK("m", pricing.Usage{Output: 1})
	}
	return out
}
