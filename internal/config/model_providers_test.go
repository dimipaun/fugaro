package config

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

func okProvider() ModelProvider {
	return ModelProvider{
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", RouteFeePct: 5.5, Models: []string{"deepseek/*"},
		AllowDataTo: []string{"edgeappinc/fugarosandbox", "dimipaun/fugaro"},
	}
}

func problemsText(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Path + ": " + p.Message + "\n")
	}
	return b.String()
}

func TestProviderValid(t *testing.T) {
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": okProvider()}); len(ps) != 0 {
		t.Fatalf("problems: %s", problemsText(ps))
	}
}

func TestProviderOverlapRejected(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"deepseek/*", "deepseek/deepseek-v4-flash"},
		{"deepseek/*", "deepseek/v4*"},
		{"qwen/qwen3", "qwen/qwen3"},
	} {
		a, b := okProvider(), okProvider()
		a.Models, b.Models = []string{tc.a}, []string{tc.b}
		b.Secret = "other-key"
		ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b})
		if !strings.Contains(problemsText(ps), "overlaps") {
			t.Errorf("%q vs %q: not rejected: %q", tc.a, tc.b, problemsText(ps))
		}
	}
	a, b := okProvider(), okProvider()
	b.Models, b.Secret = []string{"qwen/*"}, "other-key"
	if ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b}); len(ps) != 0 {
		t.Errorf("disjoint patterns rejected: %s", problemsText(ps))
	}
}

func TestProviderHTTPRefusedExceptLoopback(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://openrouter.ai/api": true, "http://127.0.0.1:8123": true, "http://localhost:9": true, "http://[::1]:9": true,
		"http://openrouter.ai/api": false, "http://10.0.0.1": false, "ftp://x.test": false, "openrouter.ai": false,
		"https://user:pw@openrouter.ai": false, "https://openrouter.ai/?k=v": false,
	} {
		p := okProvider()
		p.BaseURL = url
		got := len(ValidateModelProviders(map[string]ModelProvider{"p": p})) == 0
		if got != ok {
			t.Errorf("base_url %q accepted = %v, want %v", url, got, ok)
		}
	}
}

func TestFeeBounds(t *testing.T) {
	for fee, ok := range map[float64]bool{0: true, 5.5: true, MaxRouteFeePct: true, -0.1: false, MaxRouteFeePct + 1: false} {
		p := okProvider()
		p.RouteFeePct = fee
		if got := len(ValidateModelProviders(map[string]ModelProvider{"p": p})) == 0; got != ok {
			t.Errorf("fee %v accepted = %v, want %v", fee, got, ok)
		}
	}
}

func TestProviderFieldRules(t *testing.T) {
	for name, mut := range map[string]func(*ModelProvider){
		"kind":          func(p *ModelProvider) { p.Kind = "openai" },
		"auth":          func(p *ModelProvider) { p.Auth = "basic" },
		"secret value":  func(p *ModelProvider) { p.Secret = "sk-or-v1-ABC DEF" },
		"reserved":      func(p *ModelProvider) { p.Secret = "anthropic-api-key" },
		"no models":     func(p *ModelProvider) { p.Models = nil },
		"claude":        func(p *ModelProvider) { p.Models = []string{"claude-*"} },
		"claude prefix": func(p *ModelProvider) { p.Models = []string{"c*"} },
		"wildcard":      func(p *ModelProvider) { p.Models = []string{"*"} },
		"bad repo":      func(p *ModelProvider) { p.AllowDataTo = []string{"fugaro"} },
	} {
		p := okProvider()
		mut(&p)
		if ps := ValidateModelProviders(map[string]ModelProvider{"p": p}); len(ps) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	a, b := okProvider(), okProvider()
	b.Models = []string{"qwen/*"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b}); !strings.Contains(problemsText(ps), "own key") {
		t.Errorf("shared secret not rejected: %q", problemsText(ps))
	}
}

func TestProviderFor(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	if n, p, ok := ProviderFor(ps, "deepseek/deepseek-v4-flash"); !ok || n != "openrouter" || !p.AllowsData("dimipaun/fugaro") || p.AllowsData("edgeappinc/edgeweb") {
		t.Errorf("ProviderFor = %q %v", n, ok)
	}
	if _, _, ok := ProviderFor(ps, "claude-sonnet-5-5"); ok {
		t.Error("a Claude model was claimed")
	}
}

func TestOAuthWithProviderModelRefused(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "oauth", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5", Background: "claude-haiku-4-5"}}
	got := problemsText(CheckProviderAuth(a, ps))
	if !strings.Contains(got, "agent.models.coder") || !strings.Contains(got, "oauth") || !strings.Contains(got, "api-key") || strings.Contains(got, "reviewer") {
		t.Errorf("problems = %q", got)
	}
}

func TestVertexWithProviderModelRefused(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "vertex", Model: "deepseek/deepseek-v4-flash"}
	got := problemsText(CheckProviderAuth(a, ps))
	if !strings.Contains(got, "vertex") || !strings.Contains(got, "agent.models.coder") {
		t.Errorf("problems = %q", got)
	}
}

func TestAPIKeyWithProviderModelAllowed(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5"}}
	if got := CheckProviderAuth(a, ps); len(got) != 0 {
		t.Errorf("problems = %v", got)
	}
	if got := CheckProviderAuth(Agent{Auth: "oauth", Model: "claude-sonnet-5-5"}, ps); len(got) != 0 {
		t.Errorf("an all-Claude oauth run refused: %v", got)
	}
}

func TestProviderKeyEnv(t *testing.T) {
	p := okProvider()
	p.Secret = "openrouter-api-key"
	if got := p.SecretEnv(); got != "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY" || !IsProviderKeyEnv(got) || !IsProviderKeyEnv("fugaro_provider_key_x") {
		t.Errorf("SecretEnv = %q", got)
	}
	// A workflow secret can never be mounted as a provider key's variable.
	if !ReservedEnv(p.SecretEnv()) {
		t.Errorf("%s is not a reserved variable", p.SecretEnv())
	}
	if IsProviderKeyEnv("ANTHROPIC_API_KEY") {
		t.Error("ANTHROPIC_API_KEY is not a provider key variable")
	}
	if n, ok := ProviderBySecret(map[string]ModelProvider{"openrouter": p}, "openrouter-api-key"); !ok || n != "openrouter" {
		t.Errorf("ProviderBySecret = %q %v", n, ok)
	}
	if _, ok := ProviderBySecret(map[string]ModelProvider{"openrouter": p}, "npm-token"); ok {
		t.Error("npm-token is a provider secret")
	}
}

func TestAllowsDataIgnoresCase(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"EdgeAppInc/FugaroSandbox"}}
	for repo, want := range map[string]bool{
		"EdgeAppInc/FugaroSandbox": true, "edgeappinc/fugarosandbox": true,
		"edgeappinc/edgeweb": false, "": false,
	} {
		if got := p.AllowsData(repo); got != want {
			t.Errorf("AllowsData(%q) = %v", repo, got)
		}
	}
}

// providerPrices is the table the program builds for okProvider's route: the
// embedded rows, the built-in catalog's rows for the models the route claims,
// then the owner's overrides last — PriceTable, the one builder every surface
// prices from. A bare pricing.Embedded() prices no provider model any more:
// the placeholder row is gone (P6), so a test that pins one needs the
// catalog's row, exactly as a run would.
func providerPrices(t *testing.T) *pricing.Table {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	// The clock is the catalog's own date, a day on, not today's: which end
	// of a tier a row is charged turns on the catalog's age, so a test that
	// read the wall clock would assert the cheapest end today and the dearest
	// one from the day the shipped file passes its 30 days
	// (catalog.StaleAfter).
	checked, err := time.Parse("2006-01-02", cat.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := PriceTable(map[string]ModelProvider{"openrouter": okProvider()}, nil, cat, checked.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

// A repository's fugaro.yaml cannot carry a provider, a base URL, a key or a
// price: the strict decode refuses the keys, so the owner's local config is
// the only place a provider exists.
func TestRepoCannotAddProvider(t *testing.T) {
	for _, doc := range []string{
		"providers:\n  evil:\n    kind: anthropic-compat\n    base_url: https://evil.example\n",
		"agent:\n  base_url: https://evil.example\n",
		"agent:\n  provider: {base_url: https://evil.example}\n",
		"model_prices:\n  deepseek/deepseek-v4-flash: {input: 0}\n",
		"budget:\n  providers: {}\n",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
		}
	}
}

func TestAllowDataToRequired(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5", Background: "claude-haiku-4-5"}}
	prices := providerPrices(t)
	if got := CheckProviderPolicy(a, nil, "dimipaun/fugaro", ps, prices); len(got) != 0 {
		t.Errorf("an allowed repository refused: %v", got)
	}
	if got := CheckProviderPolicy(a, nil, "DimiPaun/Fugaro", ps, prices); len(got) != 0 {
		t.Errorf("repository match is case-sensitive: %v", got)
	}
	got := problemsText(CheckProviderPolicy(a, nil, "edgeappinc/edgeweb", ps, prices))
	if !strings.Contains(got, "agent.models.coder") || !strings.Contains(got, "allow_data_to") || !strings.Contains(got, "edgeappinc/edgeweb") {
		t.Errorf("problems = %q", got)
	}
	// An absent allow_data_to (the zero value) now means every repository
	// (owner's final ruling, 2026-10-13, design §3a), so nothing is refused
	// -- CheckProviderPolicy reads AllowsData, which changed.
	p := okProvider()
	p.AllowDataTo = nil
	if got := CheckProviderPolicy(a, nil, "dimipaun/fugaro", map[string]ModelProvider{"openrouter": p}, prices); len(got) != 0 {
		t.Errorf("absent allow_data_to: %v", got)
	}
}

func TestProviderPolicyUnclaimedAndUnpriced(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	prices := providerPrices(t)
	for _, tc := range []struct {
		model, want string
	}{
		{"openai/gpt-9", "no provider"},
		{"deepseek/deepseek-v4-flash:free", "variant"},
		{"deepseek/unpriced-model", "no price"},
	} {
		a := Agent{Auth: "api-key", Model: tc.model}
		got := problemsText(CheckProviderPolicy(a, nil, "dimipaun/fugaro", ps, prices))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems = %q, want %q", tc.model, got, tc.want)
		}
	}
	// Claude IDs and aliases are not this rule's business.
	if got := CheckProviderPolicy(Agent{Model: "sonnet"}, nil, "x/y", ps, prices); len(got) != 0 {
		t.Errorf("alias refused: %v", got)
	}
}

// TestProviderPolicySaysWhyTheCatalogRefused pins that a model a provider
// route claims and the built-in provider price catalog cannot price is
// reported with the catalog's own reason — the switch to flip and both ways
// out — and not only with "add it under model_prices": the owner reading
// validate is looking at the route's switches, which is what the refusal is
// about (design D9, P8). The shipped file gives qwen/qwen3.8-flash no ZDR
// tier, so a require_zdr route claims it and cannot price it.
func TestProviderPolicySaysWhyTheCatalogRefused(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	ps := providers(func(p *ModelProvider) { p.Models = []string{"qwen/*"} })
	tbl := mustTable(t, ps, nil, cat)
	got := problemsText(CheckProviderPolicy(Agent{Auth: "api-key", Model: "qwen/qwen3.8-flash"}, nil, "dimipaun/fugaro", ps, tbl))
	for _, want := range []string{"require_zdr: false", "model_prices"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q: %q", want, got)
		}
	}
	// A model no catalog row exists for at all has no recorded reason, and
	// keeps the generic answer: the refusal is not the only path.
	got = problemsText(CheckProviderPolicy(Agent{Auth: "api-key", Model: "qwen/qwen-nothing"}, nil, "dimipaun/fugaro", ps, tbl))
	if !strings.Contains(got, "no price") || !strings.Contains(got, "model_prices") {
		t.Errorf("an unpriced model without a recorded refusal: %q", got)
	}
}

func TestProviderPolicyAllowedModelsEntries(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Model: "claude-sonnet-5-5"}
	got := problemsText(CheckProviderPolicy(a, []string{"claude-sonnet-5-5", "deepseek/deepseek-v4-flash"}, "edgeappinc/edgeweb", ps, providerPrices(t)))
	if !strings.Contains(got, "budget.allowed_models[1]") || !strings.Contains(got, "allow_data_to") {
		t.Errorf("problems = %q", got)
	}
}

// claimID is the ID the claim tests route: one explicit Claude model through
// a provider route, the shape the mixed-credentials opt-in allows and the
// only shape it allows.
const claimID = "anthropic/claude-sonnet-5.5"

// claimPrices is the table a run has once the owner has priced the claimed ID
// under model_prices, built by the one builder every surface prices from. The
// price table never asks who may route a model (P4), which is why the row
// exists here before any route claims anything.
func claimPrices(t *testing.T, ps map[string]ModelProvider) *pricing.Table {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked, err := time.Parse("2006-01-02", cat.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := PriceTable(ps, pricing.Overrides{claimID: pricing.Rates{InputPerM: 2, OutputPerM: 10}}, cat, checked.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

// TestUnclaimedClaudeIDStillRefused pins Review Focus 1 and 5: the pricing
// half moving (Task 4) does not make an anthropic/<id> pinnable, and the
// refusal an owner meets tells the truth about the model. A route that does
// not name the ID leaves the pin refused by the rule that holds the providers
// map; a route that names exactly that ID claims it, and then the pin is
// served, priced and allowed.
func TestUnclaimedClaudeIDStillRefused(t *testing.T) {
	const repo = "dimipaun/fugaro"
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: claimID}}
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	got := problemsText(CheckProviderPolicy(a, nil, repo, ps, claimPrices(t, ps)))
	for _, want := range []string{
		"agent.models.reviewer", claimID, "is a Claude model", "go direct to Anthropic",
		"no provider serves it", "naming that exact ID in a route's models",
		// The other way the owner may make the ID servable: the sentence
		// that names the layer key is part of the refusal too.
		"the project layer's claude_models",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q: %q", want, got)
		}
	}
	// The route names the ID: the same pin is served, priced (by the same
	// table) and inside allow_data_to, so nothing is refused.
	p := okProvider()
	p.Models = []string{"deepseek/*", claimID}
	ps = map[string]ModelProvider{"openrouter": p}
	if got := problemsText(CheckProviderPolicy(a, nil, repo, ps, claimPrices(t, ps))); got != "" {
		t.Errorf("a claimed, priced Claude ID was refused: %q", got)
	}
	// Claimed but unpriced: the price lookup is what refuses it, not the pin
	// rules -- the whole point of P4's separation, and the half CheckPins
	// still does under its own name.
	if got := problemsText(CheckProviderPolicy(a, nil, repo, ps, providerPrices(t))); !strings.Contains(got, "has no price") {
		t.Errorf("a claimed ID with no price was accepted: %q", got)
	}
	// And a wildcard claim cannot stand in for the explicit entry: the route
	// itself is refused, so the pin never reaches a run.
	p.Models = []string{"deepseek/*", "anthropic/*"}
	if msg := CheckModelPattern("anthropic/*"); msg == "" {
		t.Error("a wildcard claim was accepted")
	} else if got := problemsText(ValidateModelProviders(map[string]ModelProvider{"openrouter": p})); !strings.Contains(got, "anthropic/*") {
		t.Errorf("the route with a wildcard claim was not refused: %q", got)
	}
}

// TestUnclaimedClaudePriceKeyRefused pins the contrast the opt-in turns on
// (P4, Review Focus 6): the price table's acceptance is not permission. The
// key is inert on its own -- it changes nothing about who may route the model
// -- and the pin is what is refused while no route claims the ID.
func TestUnclaimedClaudePriceKeyRefused(t *testing.T) {
	if _, err := pricing.ParseOverrides(`{"` + claimID + `": {"input_per_m": 2, "output_per_m": 10}}`); err != nil {
		t.Fatalf("a model_prices key for the claimed ID was refused: %v", err)
	}
	// The spelling a claim must never admit is still refused as a price key,
	// so the two grammars cannot drift apart.
	for _, bad := range []string{"anthropic/*", "anthropic/claude-sonnet-5.5:free", "anthropic/claude-sonnet-5.5[1m]"} {
		if _, err := pricing.ParseOverrides(`{"` + bad + `": {"input_per_m": 2, "output_per_m": 10}}`); err == nil {
			t.Errorf("model_prices accepted %q", bad)
		}
	}
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Model: claimID}
	got := problemsText(CheckProviderPolicy(a, nil, "dimipaun/fugaro", ps, claimPrices(t, ps)))
	if !strings.Contains(got, "no provider serves it") || !strings.Contains(got, claimID) {
		t.Errorf("a priced but unclaimed ID was not refused with no price problem: %q", got)
	}
}

// TestRepoCannotEnableARoute pins Review Focus 3: a repository's own
// fugaro.yaml can choose the model and can never add or enable a route. The
// keys that would do it are unknown to Config, so the strict decode refuses
// them; and a file that pins a claimed-shaped ID while no route claims it is
// refused by CheckProviderPolicy. claude_models is the key the project layer
// gains in Task 7: it is a layer key, and must stay one -- a repository
// carrying it is refused here, which is what keeps the claim out of a
// repository's reach.
func TestRepoCannotEnableARoute(t *testing.T) {
	// The file may pin the model; whether that pin is served is the owner's
	// decision, and no route claims it here. This base is VALID on its own,
	// and the assertion below is what says so: every document in the loop
	// below is this file plus one key, so each refusal is that key's and
	// never another problem in the file. Mutation that makes this fail: add
	// a `ClaudeModels map[string][]string \`yaml:"claude_models"\`` field to
	// Config, which is exactly the key a repository must not reach.
	const pinned = `
version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  models: { coder: deepseek/deepseek-v4-flash, reviewer: anthropic/claude-sonnet-5.5 }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	if _, ps := Parse([]byte(pinned)); len(ps) > 0 {
		t.Fatalf("the base file is not valid, so the refusals below are not the added key's: %v", ps)
	}
	for _, doc := range []string{
		pinned + "claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\"]\n",
		pinned + "providers:\n  evil:\n    kind: anthropic-compat\n    base_url: https://evil.example\n    models: [\"anthropic/claude-sonnet-5.5\"]\n",
		pinned + "model_prices:\n  anthropic/claude-sonnet-5.5: {input_per_m: 2, output_per_m: 10}\n",
		strings.Replace(pinned, "agent:\n", "agent:\n  base_url: https://openrouter.ai/api\n", 1),
		strings.Replace(pinned, "agent:\n", "agent:\n  provider: {base_url: https://openrouter.ai/api}\n", 1),
	} {
		if _, ps := Parse([]byte(doc)); len(ps) == 0 {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
		}
	}
	cfg, problems := Parse([]byte(pinned))
	if len(problems) > 0 {
		t.Fatalf("a repository pinning the model was refused at parse time: %v", problems)
	}
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	got := problemsText(CheckProviderPolicy(cfg.Agent, nil, "dimipaun/fugaro", ps, claimPrices(t, ps)))
	if !strings.Contains(got, "agent.models.reviewer") || !strings.Contains(got, "no provider serves it") {
		t.Errorf("the pin was not refused: %q", got)
	}
}

// TestClaimedClaudeRouteKeepsEveryOtherRefusal pins the carried review item
// the claim must not erode: a route that names a Claude ID grants passage
// through the *claim* rule and nothing else. The three refusals that guard a
// provider model -- the repository's allow_data_to, the credential
// (agent.auth api-key only) and the budget mode (enforce only) -- are asked
// of a claimed Claude pin exactly as they are of any other provider model, so
// a claim-shaped carve-out in any of them fails here. The coder stays a plain
// provider model, so each row asserts both paths and a carve-out in either
// direction is caught.
func TestClaimedClaudeRouteKeepsEveryOtherRefusal(t *testing.T) {
	const repo, blocked = "dimipaun/fugaro", "edgeappinc/edgeweb"
	claiming := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: claimID}}
	ps := providers(func(p *ModelProvider) { p.Models = []string{"deepseek/*", claimID} })
	prices := claimPrices(t, ps)

	for _, tc := range []struct {
		name  string
		got   string
		paths []string
		want  string
	}{
		{
			// The claim decides who may route the ID; it never decides
			// whose code may be sent there.
			name:  "allow_data_to",
			got:   problemsText(CheckProviderPolicy(claiming, nil, blocked, ps, prices)),
			paths: []string{"agent.models.coder", "agent.models.reviewer"},
			want:  "allow_data_to",
		},
		{
			// An oauth token is a Claude subscription credential; it cannot
			// carry a provider key however Claude the pin looks.
			name:  "oauth",
			got:   problemsText(CheckProviderAuth(Agent{Auth: "oauth", Models: claiming.Models}, ps)),
			paths: []string{"agent.models.coder", "agent.models.reviewer"},
			want:  "oauth",
		},
		{
			// observe never refuses a call and a provider's calls are real
			// dollars, so the mode must be enforce whatever is pinned.
			name:  "observe",
			got:   problemsText(CheckProviderGateway(claiming, "", ps, "observe")),
			paths: []string{"agent.models.coder", "agent.models.reviewer"},
			want:  "enforce",
		},
	} {
		for _, p := range tc.paths {
			if !strings.Contains(tc.got, p) {
				t.Errorf("%s: %s was not refused: %q", tc.name, p, tc.got)
			}
		}
		if !strings.Contains(tc.got, tc.want) {
			t.Errorf("%s: the refusal does not say %q: %q", tc.name, tc.want, tc.got)
		}
	}

	// All three conditions holding is the only shape that passes: the claim
	// buys the pin a route and the repository is inside allow_data_to, the
	// credential is api-key and the budget enforces.
	if got := CheckProviderPolicy(claiming, nil, repo, ps, prices); len(got) != 0 {
		t.Errorf("a claimed Claude route inside allow_data_to was refused: %s", problemsText(got))
	}
	if got := CheckProviderAuth(claiming, ps); len(got) != 0 {
		t.Errorf("a claimed Claude route under api-key was refused: %s", problemsText(got))
	}
	if got := CheckProviderGateway(claiming, "", ps, "enforce"); len(got) != 0 {
		t.Errorf("a claimed Claude route under enforce was refused: %s", problemsText(got))
	}
}

// TestTheNameDirectIsRefused is the review of this task's routing work: a
// resolved route is a provider entry's *name* (RoutingClaudeFor answers the
// entry, not the entry's kind), and the direct route is spelled with the same
// string (RouteDirect, the value of routing.claude.via that means "call
// Anthropic"). providerNameRE allows "direct", so nothing refused a provider
// entry called that, and a switch routing a Claude ID to it resolved to
// RouteDirect: the runner reads a route of RouteDirect as a call that went
// direct and halts an otherwise valid run ("the project's routing switch is not
// usable", internal/runner/routing.go), and a pin carrying the entry's own
// route ID is refused as a direct pin (internal/config/pins.go).
//
// The collision is not asserted here, because the resolver no longer answers
// one: a later change fixed it where the route is decided rather than where the
// name is written (reservedRouteName, in RoutingClaudeFor and CheckRouting), so
// this test pins what the three places that answer for the name do now — the
// resolver failing closed first, because that is what makes a route of
// RouteDirect unreachable even for a caller that never asked CheckRouting.
func TestTheNameDirectIsRefused(t *testing.T) {
	p := okProvider()
	p.Models = []string{"anthropic/claude-sonnet-5.5"}
	providers := map[string]ModelProvider{RouteDirect: p}
	sw := &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}
	if route, sent, ok := RoutingClaudeFor(sw, "claude-sonnet-5-5", providers); ok || route != "" || sent != "" {
		t.Errorf("RoutingClaudeFor(an entry named %q) = %q, %q, %v: %q is the direct route to every caller, so the resolver must fail closed rather than answer a route no caller can tell from direct",
			RouteDirect, route, sent, ok, RouteDirect)
	}
	if got := problemsText(CheckRouting(&Routing{Claude: sw}, providers, "routing")); !strings.Contains(got, "Rename the provider entry") {
		t.Errorf("CheckRouting accepted a switch routed through an entry named %q: %s", RouteDirect, got)
	}
	ps := ValidateModelProviders(providers)
	if len(ps) == 0 {
		t.Fatalf("a provider entry named %q was accepted: a call routed to it reads as a call that went direct", RouteDirect)
	}
	got := problemsText(ps)
	for _, want := range []string{"providers." + RouteDirect, "direct", "routing.claude.via", "halt"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q: %s", want, got)
		}
	}
}
