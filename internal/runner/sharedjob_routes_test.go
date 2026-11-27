package runner_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// sharedJobPolicy is a shared project job's node: the profile fixture's own
// policy plus the owner's provider routes and price row, as `fugaro project
// policy publish` writes them (internal/cli's policyProviders copies the
// operator's own providers block into the node whole).
func sharedJobPolicy(baseURL string) string {
	return strings.Replace(projectPolicyFor(`["github/acme/*"]`, ""), `{"version":9,`,
		`{"version":9,"providers":{"openrouter":{"kind":"anthropic-compat","base_url":"`+baseURL+`","auth":"bearer","secret":"openrouter-api-key","models":["deepseek/*"]}},`+
			`"model_prices":{"`+deepseek+`":{"input_per_m":1,"output_per_m":2}},`, 1)
}

// A shared project job's deployment carries no route table — one repository's
// routes in it would be every repository's — so the run takes them, and the
// prices, from the owner's published policy. A coder only a provider serves
// then passes bootstrap's model approval and its call reaches that provider,
// exactly as it does on a per-repository job (check 580).
func TestSharedJobTakesItsRoutesFromThePolicyNode(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	compat, up := anthropicfake.New(t, anthropicfake.Compat{}.MessageOK(deepseek, use))
	b := projectRunCfg(t, projectJobOf(), providerPinned(t), "enforce", "50", sharedJobPolicy(up.URL))
	b.compat = compat
	withProviderKey(b.harness)
	if len(b.deps.Providers) != 0 {
		t.Fatalf("the fixture's job carries %d routes: the job is the one thing that must not carry them", len(b.deps.Providers))
	}
	projectHost(t, b)
	var status int
	var body string
	rec, err := b.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: a shared job's run must take the node's routes (%s)", rec, err, body)
	}
	if status != 200 {
		t.Errorf("the provider call answered %d: %s", status, body)
	}
	if compat.Count() != 1 {
		t.Errorf("the node's provider saw %d calls, want 1", compat.Count())
	}
}

// The job's environment is not the source of a shared job's routes or prices.
// A launcher that wrote one would choose the host the profile's provider key
// and the repository's code go to, and could price a call below the owner's
// ceiling: both are the node's, so a route and a price on the execution are
// ignored — the decoy provider is never called, and the cost is the node's.
func TestSharedJobIgnoresTheModelEnvironmentOnTheExecution(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	compat, up := anthropicfake.New(t, anthropicfake.Compat{}.MessageOK(deepseek, use))
	decoy, decoyUp := anthropicfake.New(t)
	b := projectRunCfg(t, projectJobOf(), providerPinned(t), "enforce", "50", sharedJobPolicy(up.URL))
	b.compat = compat
	withProviderKey(b.harness)
	b.deps.Providers = map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: decoyUp.URL, Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"nothing/*"},
	}}
	raw, err := json.Marshal(b.deps.Providers)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		runner.BudgetModeEnv:     "enforce",
		runner.ModelProvidersEnv: string(raw),
		runner.ModelPricesEnv:    `{"` + deepseek + `":{"input_per_m":0.0001,"output_per_m":0.0001}}`,
	}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	b.deps.Spend = spend
	projectHost(t, b)
	var status int
	var body string
	rec, err := b.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the execution's route must not reach bootstrap's model approval (%s)", rec, err, body)
	}
	if status != 200 {
		t.Errorf("the provider call answered %d: %s", status, body)
	}
	if n := decoy.Count(); n != 0 {
		t.Errorf("the route the execution carried was called %d times: the node's is the run's only route", n)
	}
	if !near(rec.CostUSD, 0.002) {
		t.Errorf("cost = $%v, want $0.002: the node's price, not the execution's", rec.CostUSD)
	}
}
