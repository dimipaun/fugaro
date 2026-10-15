package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func TestDecodeAbsentIsNotFound(t *testing.T) {
	for _, raw := range []string{"", "  \n", "null", " null "} {
		p, found, err := Decode([]byte(raw))
		if p != nil || found || err != nil {
			t.Errorf("Decode(%q) = (%v, %v, %v), want (nil, false, nil): an absent node is today's behaviour", raw, p, found, err)
		}
	}
}

// okProvider is one provider entry that config.ValidateModelProviders accepts,
// which is what every provider fixture below has to be before a refusal can be
// about the field the test names.
const okProvider = `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`

// providerPolicy is one provider entry under the name openrouter.
func providerPolicy(entry string) []byte {
	return []byte(`{"version": 1, "providers": {"openrouter": ` + entry + `}}`)
}

func TestDecodeIgnoresUnknownKeys(t *testing.T) {
	// A newer Fugaro's policy must not brick this runner on a field it does
	// not know (P8): unknown keys outside the budget block are ignored.
	// Version gates a newer policy; the budget block is strict
	// (TestDecodeRefusesABudgetWhoseKeysAllMiss), and a provider entry is
	// strict about one thing (TestDecodeRefusesAnUnknownProviderKeyItCould-
	// HaveMeantAsAllowDataTo below).
	p, found, err := Decode([]byte(`{
		"version": 1,
		"budget": {"mode": "observe"},
		"providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"], "future_provider_key": true}},
		"future": {"anything": [1, 2, 3]}
	}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe", p.Budget.Mode)
	}
	if p.Budget.PerRunUSD != 0 || p.Budget.MaxRunTokens != 0 {
		t.Errorf("an absent documented key must stay zero, got %+v", p.Budget)
	}
	if p.Providers["openrouter"].Secret != "openrouter-key" {
		t.Errorf("Providers = %v, want the openrouter entry", p.Providers)
	}
}

// TestDecodeRefusesAnUnknownProviderKeyWithoutAllowDataTo: the P8 tolerance of
// an unknown key inside a provider entry is bounded by the one field whose
// absence is permissive. Every other field of an entry carries a zero value
// config.ValidateModelProviders refuses (a misspelt "kind", "base_url", "auth",
// "secret" or "models" leaves a value that is refused), but a misspelt
// allow_data_to leaves the nil slice that means every repository
// (ModelProvider.AllowsData), so "allow_data_too": ["acme/app"] used to decode,
// validate and route code to every repository with nothing said. An entry that
// spells out allow_data_to keeps the tolerance — a field a newer Fugaro adds
// to it is still ignored.
func TestDecodeRefusesAnUnknownProviderKeyWithoutAllowDataTo(t *testing.T) {
	const spelling = `"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]`
	for name, entry := range map[string]string{
		"a misspelt allow_data_to":                        `{` + spelling + `, "allow_data_too": ["acme/app"]}`,
		"a misspelt allow_data_to, spelled in camel case": `{` + spelling + `, "allowDataTo": ["acme/app"]}`,
		"an unknown key beside it":                        `{` + spelling + `, "future_provider_key": true}`,
	} {
		p, found, err := Decode(providerPolicy(entry))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal on an existing node: the entry would be read as open to every repository", name, entry, p, found, err)
			continue
		}
		if !strings.Contains(err.Error(), "allow_data_to") {
			t.Errorf("%s: the refusal does not name allow_data_to: %v", name, err)
		}
	}
	// The same entries with the key spelled out decode, and the unknown key
	// beside it is still ignored.
	p, found, err := Decode(providerPolicy(`{` + spelling + `, "allow_data_to": ["acme/app"], "allow_data_too": ["acme/other"], "future_provider_key": true}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy with the documented key spelled out", p, found, err)
	}
	if got := p.Providers["openrouter"].AllowDataTo; len(got) != 1 || got[0] != "acme/app" {
		t.Errorf("AllowDataTo = %v, want the documented key's value", got)
	}
}

func TestDecodeRefusesBadVersion(t *testing.T) {
	for name, raw := range map[string]string{
		"no version":     `{"budget": {"mode": "observe"}}`,
		"version 0":      `{"version": 0}`,
		"version 2":      `{"version": 2}`,
		"a string":       `{"version": "1"}`,
		"a float":        `{"version": 1.5}`,
		"a bool":         `{"version": true}`,
		"not an object":  `[1, 2]`,
		"a bare number":  `7`,
		"truncated json": `{"version": 1`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil {
			t.Errorf("%s: Decode(%s) = (%v, %v), want an error and no policy", name, raw, p, err)
		}
		if !found {
			t.Errorf("%s: the node exists and cannot be used, so found must be true (fail closed)", name)
		}
	}
	// The version message is the shared constant, so the runner and the CLI
	// print the same sentence.
	_, _, err := Decode([]byte(`{"version": 2}`))
	if err == nil || !strings.Contains(err.Error(), "policy is version 2") {
		t.Errorf("the version refusal must use Skew, got %v", err)
	}
}

// TestDecodeReadsTheDocumentedSnakeCaseKeys is the test that would have caught
// a missing json tag: every documented key, field by field.
func TestDecodeReadsTheDocumentedSnakeCaseKeys(t *testing.T) {
	p, found, err := Decode([]byte(`{
		"version": 1,
		"budget": {"mode": "enforce", "per_run_usd": 12.5, "allowed_models": ["claude-sonnet-5"], "max_run_tokens": 250000},
		"allow_repos": ["github/acme/*", "bitbucket/acme/app"]
	}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Version != 1 {
		t.Errorf("Version = %d, want 1", p.Version)
	}
	if p.Budget.Mode != "enforce" {
		t.Errorf("Mode = %q, want enforce", p.Budget.Mode)
	}
	if p.Budget.PerRunUSD != 12.5 {
		t.Errorf("PerRunUSD = %v, want 12.5", p.Budget.PerRunUSD)
	}
	if len(p.Budget.AllowedModels) != 1 || p.Budget.AllowedModels[0] != "claude-sonnet-5" {
		t.Errorf("AllowedModels = %v", p.Budget.AllowedModels)
	}
	if p.Budget.MaxRunTokens != 250000 {
		t.Errorf("MaxRunTokens = %d, want 250000", p.Budget.MaxRunTokens)
	}
	if len(p.AllowRepos) != 2 || p.AllowRepos[0] != "github/acme/*" {
		t.Errorf("AllowRepos = %v", p.AllowRepos)
	}
	if p.Routing != nil {
		t.Errorf("Routing = %v, want nil (absent is direct, D1b)", p.Routing)
	}
}

// TestDecodeRefusesABudgetWhoseKeysAllMiss: a hand-authored or cross-version
// policy written in camelCase must not decode to a zero per-run cap and a zero
// token ceiling, both of which the runner would enforce. The budget block is
// decoded strictly, the way internal/config decodes fugaro.yaml's budget block
// ("a misspelt key is an error, never \"no policy\"").
func TestDecodeRefusesABudgetWhoseKeysAllMiss(t *testing.T) {
	for name, raw := range map[string]string{
		"camelCase per_run_usd":    `{"version": 1, "budget": {"mode": "enforce", "perRunUsd": 5}}`,
		"camelCase max_run_tokens": `{"version": 1, "budget": {"mode": "enforce", "MaxRunTokens": 1000}}`,
		"a mistyped mode":          `{"version": 1, "budget": {"mode": "observe", "perRunUsd": 5}}`,
		"not an object":            `{"version": 1, "budget": 5}`,
		"a list":                   `{"version": 1, "budget": []}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error on an existing node", name, raw, p, found, err)
		}
	}
}

// TestDecodeRefusesABadBudgetValue: the values are enforced, not advisory, so
// Decode checks them the way internal/config's validateBudget does. An
// unrecognised mode is the dangerous one: policy.Merge treats it as unset, the
// run falls back to off, and enforcement silently never turns on.
func TestDecodeRefusesABadBudgetValue(t *testing.T) {
	for name, raw := range map[string]string{
		"an unknown mode":       `{"version": 1, "budget": {"mode": "enforc"}}`,
		"a Go-ish mode":         `{"version": 1, "budget": {"mode": "Enforce"}}`,
		"a boolean mode":        `{"version": 1, "budget": {"mode": true}}`,
		"a negative cap":        `{"version": 1, "budget": {"mode": "enforce", "per_run_usd": -1}}`,
		"an absurd cap":         `{"version": 1, "budget": {"mode": "enforce", "per_run_usd": 1e9}}`,
		"a cap rounding to 0":   `{"version": 1, "budget": {"mode": "enforce", "per_run_usd": 0.0000001}}`,
		"a negative token cap":  `{"version": 1, "budget": {"mode": "enforce", "max_run_tokens": -1}}`,
		"an empty allow-list":   `{"version": 1, "budget": {"mode": "enforce", "allowed_models": []}}`,
		"a token cap as string": `{"version": 1, "budget": {"mode": "enforce", "max_run_tokens": "1000"}}`,
		// H1 (security review of PR #379): a budget block is there and names
		// no mode. The mode would otherwise come from the job environment,
		// which the policy's own reader cannot see, so a node published for
		// its caps would enforce whatever the deploy happened to say — the
		// silent "enforcement never turned on" this strict decode exists to
		// prevent. A policy that sets no budget key leaves the block out
		// entirely.
		"a cap with no mode":       `{"version": 1, "budget": {"per_run_usd": 0.000001}}`,
		"an empty budget block":    `{"version": 1, "budget": {}}`,
		"a token cap with no mode": `{"version": 1, "budget": {"max_run_tokens": 1000}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error", name, raw, p, found, err)
		}
	}
	for _, raw := range []string{
		`{"version": 1, "budget": {"mode": "off"}}`,
		`{"version": 1, "budget": {"mode": "observe", "per_run_usd": 12.5, "max_run_tokens": 250000, "allowed_models": ["claude-sonnet-5"]}}`,
		`{"version": 1, "budget": {"mode": "enforce", "per_run_usd": 0.000001}}`,
		`{"version": 1}`,
	} {
		if _, _, err := Decode([]byte(raw)); err != nil {
			t.Errorf("Decode(%s) = %v, want a valid budget", raw, err)
		}
	}
}

// TestDecodeAcceptsAPartialBudget: a budget block that names some of its keys
// leaves the rest at zero, exactly as fugaro.yaml's own budget: block does —
// the run keeps the job environment's value for each key the node is silent
// about (H1) — and the block always names the mode, so a node published for
// its caps cannot enforce a mode nobody can read out of it. A policy that sets
// no budget key at all leaves the block out, which is the "sets no budget"
// spelling the decoder accepts.
func TestDecodeAcceptsAPartialBudget(t *testing.T) {
	p, _, err := Decode([]byte(`{"version": 1, "budget": {"mode": "observe"}}`))
	if err != nil {
		t.Fatalf("a partial budget must be valid: %v", err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 0 || p.Budget.MaxRunTokens != 0 {
		t.Errorf("a partial budget = %+v, want observe with zero ceilings", p.Budget)
	}
	p, _, err = Decode([]byte(`{"version": 1}`))
	if err != nil {
		t.Fatalf("a policy with no budget block must be valid: %v", err)
	}
	if !reflect.DeepEqual(p.Budget, Budget{}) {
		t.Errorf("no budget block = %+v, want the zero Budget", p.Budget)
	}
}

func TestDecodeRefusesBadAllowRepoPattern(t *testing.T) {
	for _, pattern := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/x y"} {
		if _, _, err := Decode([]byte(`{"version": 1, "allow_repos": ["` + pattern + `"]}`)); err == nil {
			t.Errorf("allow_repos [%q] must be refused", pattern)
		}
	}
	if _, _, err := Decode([]byte(`{"version": 1, "allow_repos": "github/acme/*"}`)); err == nil {
		t.Error("allow_repos that is not a list of strings must be refused")
	}
	// A pattern carrying an unreasonable number of wildcards reaches the same
	// check through Decode: the node is what the runner reads, so the refusal
	// must be there too (ValidPattern caps them; TestValidPatternCapsWildcards).
	if _, _, err := Decode([]byte(`{"version": 1, "allow_repos": ["github/acme/*****"]}`)); err == nil {
		t.Error("allow_repos with more wildcards than ValidPattern allows must be refused")
	}
	// A single "*" is the design's explicit choice: every repository.
	p, _, err := Decode([]byte(`{"version": 1, "allow_repos": ["*"]}`))
	if err != nil || len(p.AllowRepos) != 1 || p.AllowRepos[0] != "*" {
		t.Errorf("allow_repos [\"*\"] must be valid, got (%v, %v)", p, err)
	}
}

// TestDecodeChecksAProviderSecretIsASecretName: the node is readable by every
// run token, and a provider entry carries a secret NAME (§5), so the entry is
// held to the repository's own secret-name rule (config.SecretNameRE, the rule
// internal/config/providers.go applies to fugaro.yaml's providers block). A
// resource path is a pointer into Secret Manager, not a name, and is refused;
// a value that happens to be spelled like a name cannot be told apart here, so
// only the CLI (task 1.6) and the review can catch that one.
func TestDecodeChecksAProviderSecretIsASecretName(t *testing.T) {
	for name, secret := range map[string]string{
		"a resource path":  "projects/p/secrets/openrouter-key/versions/3",
		"a path":           "openrouter/key",
		"upper case":       "OpenRouter-Key",
		"a space":          "openrouter key",
		"a leading dash":   "-openrouter-key",
		"an underscore":    "openrouter_key",
		"a trailing colon": "openrouter-key:",
		"an empty-ish dot": ".",
		"too long":         "k" + strings.Repeat("e", 63) + "y",
	} {
		raw := []byte(`{"version": 1, "providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "` + secret + `", "models": ["deepseek/*"]}}}`)
		p, found, err := Decode(raw)
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(secret %q) = (%v, %v, %v), want a refusal: only a secret name may sit on the readable node", name, secret, p, found, err)
		}
		if err != nil && !strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the refusal is %v, want it to name the secret (the fixture is otherwise valid, so nothing else may be the reason)", name, err)
		}
	}
	// The name itself is carried through unchanged.
	p, _, err := Decode(providerPolicy(okProvider))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Providers["openrouter"].Secret; got != "openrouter-key" {
		t.Errorf("Secret = %q, want the name unchanged", got)
	}
}

// TestDecodeValidatesProviderEntriesLikeTheCarrier: a providers entry on the
// policy node is the document internal/runner's ProvidersFromEnv decodes out of
// FUGARO_MODEL_PROVIDERS, and internal/config refuses the same document in
// fugaro.yaml. The node is what a run is actually routed and metered by, so the
// same values are refused here rather than decoded and enforced.
func TestDecodeValidatesProviderEntriesLikeTheCarrier(t *testing.T) {
	for name, entry := range map[string]string{
		"no kind":             `{"base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"an unknown kind":     `{"kind": "openai-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"an http base URL":    `{"kind": "anthropic-compat", "base_url": "http://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no base URL":         `{"kind": "anthropic-compat", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"a mistyped auth":     `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "Authorization", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no auth":             `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no models":           `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key"}`,
		"an empty data list":  `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": []}`,
		"a null data list":    `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": null}`,
		"a bad data entry":    `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme"]}`,
		"an absurd route fee": `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "route_fee_pct": 51}`,
		"an alias pattern":    `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["claude-*"]}`,
		"a misspelt policy":   `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "price_policy": "cheaper"}`,
	} {
		p, found, err := Decode(providerPolicy(entry))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal on an existing node", name, entry, p, found, err)
		}
	}
	// Two providers may not claim the same model: the run would route to one of
	// them arbitrarily, and the charge would be whichever one it was.
	if _, _, err := Decode([]byte(`{"version": 1, "providers": {
		"one": {"kind": "anthropic-compat", "base_url": "https://one.example/v1", "auth": "bearer", "secret": "one-key", "models": ["deepseek/*"]},
		"two": {"kind": "anthropic-compat", "base_url": "https://two.example/v1", "auth": "x-api-key", "secret": "two-key", "models": ["deepseek/deepseek-v4.1-flash"]}}}`)); err == nil {
		t.Error("two providers claiming one model must be refused")
	}
	// The whole entry as fugaro.yaml spells it decodes, including the switches.
	p, found, err := Decode(providerPolicy(`{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "x-api-key", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"], "route_fee_pct": 5, "require_zdr": false, "price_policy": "highest"}`))
	if err != nil || !found {
		t.Fatalf("a valid entry must decode: %v", err)
	}
	got := p.Providers["openrouter"]
	if got.BaseURL != "https://openrouter.ai/api/v1" || got.Auth != "x-api-key" || got.Kind != "anthropic-compat" ||
		got.RouteFeePct != 5 || len(got.AllowDataTo) != 1 || !got.AllowsData("acme/app") {
		t.Errorf("the entry decoded as %+v, want the yaml spelling carried through", got)
	}
	if got.RequireZDROn() || got.EffectivePricePolicy() != PricePolicyHighest {
		t.Errorf("switches = %v/%q, want false/highest", got.RequireZDROn(), got.EffectivePricePolicy())
	}
}

// TestDecodeRequireZDRIsDefaultTrue: require_zdr defaults to true (tool design
// D3), so "unset" and "false" are different documents and the field has to tell
// them apart: a plain bool decodes an omitted key as false — the non-default —
// and omitempty then writes the default and drops the non-default, the inverse
// of the design's rule that a default is omitted from a job's environment
// (D11).
func TestDecodeRequireZDRIsDefaultTrue(t *testing.T) {
	p, _, err := Decode(providerPolicy(okProvider))
	if err != nil {
		t.Fatal(err)
	}
	unset := p.Providers["openrouter"]
	if unset.RequireZDR != nil {
		t.Errorf("RequireZDR = %v, want nil: the key was not written", *unset.RequireZDR)
	}
	if !unset.RequireZDROn() {
		t.Error("an omitted require_zdr must read as true: the ZDR tier is the conservative price")
	}
	if unset.EffectivePricePolicy() != PricePolicyCheapest {
		t.Errorf("EffectivePricePolicy = %q, want cheapest", unset.EffectivePricePolicy())
	}

	p, _, err = Decode(providerPolicy(`{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "require_zdr": false, "price_policy": "cheapest"}`))
	if err != nil {
		t.Fatal(err)
	}
	off := p.Providers["openrouter"]
	if off.RequireZDR == nil || *off.RequireZDR {
		t.Errorf("RequireZDR = %v, want false", off.RequireZDR)
	}
	if off.RequireZDROn() {
		t.Error("an explicit require_zdr false must read as false")
	}

	// Written back: an unwritten switch stays unwritten and a written one is
	// written, which is what keeps an unset key off the wire (D11). A plain bool
	// gets this backwards, which is why the field is a *bool.
	zdron, zdroff := true, false
	for name, tc := range map[string]struct {
		p    Provider
		want string // the key and value the marshalled entry must carry, "" for none of them
	}{
		"unset":   {Provider{}, ""},
		"true":    {Provider{RequireZDR: &zdron}, `"require_zdr":true`},
		"false":   {Provider{RequireZDR: &zdroff}, `"require_zdr":false`},
		"highest": {Provider{PricePolicy: PricePolicyHighest}, `"price_policy":"highest"`},
	} {
		got, err := json.Marshal(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tc.want == "" {
			if strings.Contains(string(got), `"require_zdr"`) || strings.Contains(string(got), `"price_policy"`) {
				t.Errorf("%s: marshal = %s, want neither switch written: an unset switch is the default and is omitted", name, got)
			}
			continue
		}
		if !strings.Contains(string(got), tc.want) {
			t.Errorf("%s: marshal = %s, want it to carry %s", name, got, tc.want)
		}
	}
}

// TestDecodeRefusesABadAllowedModel: allowed_models is enforced by the run, so
// its entries are checked the way internal/config's validateBudget checks
// fugaro.yaml's (CheckModelID, DuplicateModel) — an alias names no model, and a
// model listed twice is a mistake that would otherwise be enforced silently.
func TestDecodeRefusesABadAllowedModel(t *testing.T) {
	for name, list := range map[string]string{
		"an alias":           `["sonnet"]`,
		"the wildcard":       `["claude-*"]`,
		"a -latest pointer":  `["deepseek/deepseek-v4-flash-latest"]`,
		"a :variant":         `["deepseek/deepseek-v4-flash:free"]`,
		"an anthropic alias": `["anthropic/claude-sonnet-5.5"]`,
		"empty entry":        `[""]`,
		"whitespace":         `["claude sonnet"]`,
		"listed twice":       `["claude-sonnet-5-5", "claude-sonnet-5-5"]`,
		"too long":           `["` + strings.Repeat("m", 101) + `"]`,
		"null entry":         `[null]`,
		"a number entry":     `[5]`,
	} {
		p, found, err := Decode([]byte(`{"version": 1, "budget": {"mode": "enforce", "allowed_models": ` + list + `}}`))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(allowed_models %s) = (%v, %v, %v), want a refusal", name, list, p, found, err)
		}
	}
	p, _, err := Decode([]byte(`{"version": 1, "budget": {"mode": "enforce", "allowed_models": ["claude-sonnet-5-5", "deepseek/deepseek-v4.1-flash"]}}`))
	if err != nil {
		t.Fatalf("two explicit model IDs must be valid: %v", err)
	}
	if len(p.Budget.AllowedModels) != 2 {
		t.Errorf("AllowedModels = %v, want both", p.Budget.AllowedModels)
	}
}

// run, not a data store, so a node past MaxPolicyBytes is refused rather than
// decoded.
func TestDecodeRefusesAnOversizedNode(t *testing.T) {
	pad := func(n int) []byte {
		return []byte(`{"version": 1, "allow_repos": ["*"], "pad": "` + strings.Repeat("a", n) + `"}`)
	}
	small := pad(MaxPolicyBytes - 100)
	if len(small) > MaxPolicyBytes {
		t.Fatalf("the fixture is %d bytes, past the bound it is meant to sit under", len(small))
	}
	if _, _, err := Decode(small); err != nil {
		t.Errorf("Decode(%d bytes) = %v, want a valid policy", len(small), err)
	}
	big := pad(MaxPolicyBytes)
	if len(big) <= MaxPolicyBytes {
		t.Fatalf("the fixture is %d bytes, not past MaxPolicyBytes (%d)", len(big), MaxPolicyBytes)
	}
	p, found, err := Decode(big)
	if err == nil || p != nil || !found {
		t.Errorf("Decode(%d bytes) = (%v, %v, %v), want a refusal on an existing node", len(big), p, found, err)
	}
}

// TestDecodeModelPricesAgreeWithTheCatalog: the price overrides are the
// document fugaro.yaml calls model_prices, so Decode keeps its spelling and
// its pointer semantics (an omitted optional rate takes the list default, it
// is never priced at zero) and reads the block with the catalogue's own
// parser. This test is the guard that the two cannot drift.
func TestDecodeModelPricesAgreeWithTheCatalog(t *testing.T) {
	// One entry with every optional rate written, one that omits them: the
	// catalogue must read both exactly as the policy does, and the omitted
	// ones must stay unset (nil) rather than become a price of zero.
	const block = `{
		"claude-sonnet-5-5": {"input_per_m": 3, "output_per_m": 15},
		"claude-opus-5-5": {"input_per_m": 5, "output_per_m": 25, "cache_write_5m": 2, "cache_write_1h": 3, "cache_read": 0.25, "web_search_per_1k": 7}
	}`
	p, _, err := Decode([]byte(`{"version": 1, "model_prices": ` + block + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Prices) != 2 {
		t.Fatalf("Prices = %v, want two entries", p.Prices)
	}
	ov, err := pricing.ParseOverrides(block)
	if err != nil {
		t.Fatal(err)
	}

	flat := p.Prices["claude-sonnet-5-5"]
	if flat.InputPerM == nil || *flat.InputPerM != 3 || flat.OutputPerM == nil || *flat.OutputPerM != 15 {
		t.Errorf("Price = %+v, want input 3 and output 15", flat)
	}
	for name, got := range map[string]*float64{"cache_write_5m": flat.CacheWrite5m, "cache_write_1h": flat.CacheWrite1h,
		"cache_read": flat.CacheRead, "web_search_per_1k": flat.WebSearchPer1k} {
		if got != nil {
			t.Errorf("%s = %v, want nil: an omitted optional rate stays unset and takes the list default", name, *got)
		}
	}
	full := p.Prices["claude-opus-5-5"]
	if full.CacheRead == nil || *full.CacheRead != 0.25 || full.WebSearchPer1k == nil || *full.WebSearchPer1k != 7 {
		t.Errorf("Price = %+v, want the written cache_read and web_search_per_1k", full)
	}

	// The catalogue's reading of the same bytes, field by field.
	want := map[string]pricing.Rates{"claude-sonnet-5-5": ov["claude-sonnet-5-5"], "claude-opus-5-5": ov["claude-opus-5-5"]}
	for id, r := range want {
		got := p.Prices[id]
		if *got.InputPerM != r.InputPerM || *got.OutputPerM != r.OutputPerM {
			t.Errorf("%s: the catalogue reads input %v output %v, the policy %v/%v", id, r.InputPerM, r.OutputPerM, *got.InputPerM, *got.OutputPerM)
		}
		for name, pair := range map[string][2]*float64{
			"cache_write_5m": {got.CacheWrite5m, &r.CacheWrite5m}, "cache_write_1h": {got.CacheWrite1h, &r.CacheWrite1h},
			"cache_read": {got.CacheRead, &r.CacheRead}, "web_search_per_1k": {got.WebSearchPer1k, &r.WebSearchPer1k},
		} {
			if pair[0] != nil && *pair[0] != *pair[1] {
				t.Errorf("%s %s: the catalogue reads %v, the policy %v", id, name, *pair[1], *pair[0])
			}
		}
	}
	if r := ov["claude-sonnet-5-5"]; r.CacheRead != pricing.DefaultCacheRead || r.WebSearchPer1k != pricing.DefaultWebSearchPer1k {
		t.Errorf("the catalogue's defaults are %v/%v, want %v/%v", r.CacheRead, r.WebSearchPer1k,
			pricing.DefaultCacheRead, pricing.DefaultWebSearchPer1k)
	}
}

func TestDecodeRefusesABadPrice(t *testing.T) {
	for name, raw := range map[string]string{
		"input_per_m missing":          `{"version": 1, "model_prices": {"claude-sonnet-5-5": {"output_per_m": 15}}}`,
		"a camelCase rate":             `{"version": 1, "model_prices": {"claude-sonnet-5-5": {"input_per_m": 3, "output_per_m": 15, "cacheRead": 0.1}}}`,
		"an alias key":                 `{"version": 1, "model_prices": {"anthropic/claude-sonnet-5.5": {"input_per_m": 3, "output_per_m": 15}}}`,
		"a negative rate":              `{"version": 1, "model_prices": {"claude-sonnet-5-5": {"input_per_m": -3, "output_per_m": 15}}}`,
		"a bare number":                `{"version": 1, "model_prices": 5}`,
		"a list":                       `{"version": 1, "model_prices": []}`,
		"a long_context without rates": `{"version": 1, "model_prices": {"claude-sonnet-5-5": {"input_per_m": 3, "output_per_m": 15, "long_context": {"above_input_tokens": 200000}}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error", name, raw, p, found, err)
		}
	}
	// An empty block and an absent one are both "no overrides".
	for _, raw := range []string{`{"version": 1}`, `{"version": 1, "model_prices": {}}`, `{"version": 1, "model_prices": null}`} {
		p, _, err := Decode([]byte(raw))
		if err != nil {
			t.Errorf("Decode(%s) = %v, want no overrides", raw, err)
		} else if len(p.Prices) != 0 {
			t.Errorf("Decode(%s) = %v, want no overrides", raw, p.Prices)
		}
	}
}

// TestVersionErrorCarriesBothVersions: Decode returns the versions, not a
// rendered message, so the runner (task 1.3) prints RunnerSkew and the CLI
// (task 1.6) CLISkew from the same error without matching on the text.
func TestVersionErrorCarriesBothVersions(t *testing.T) {
	_, found, err := Decode([]byte(`{"version": 7}`))
	if !found {
		t.Error("the node exists and cannot be used, so found must be true")
	}
	var ve *VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a *VersionError", err)
	}
	if ve.Found != 7 || ve.Understood != Version {
		t.Errorf("VersionError = %+v, want found 7 and understood %d", ve, Version)
	}
	if got, want := err.Error(), fmt.Sprintf(Skew, 7, Version); got != want {
		t.Errorf("Error() = %q, want the shared sentence %q", got, want)
	}
	if got := fmt.Sprintf(RunnerSkew, ve.Found, ve.Understood); !strings.Contains(got, "runner image") {
		t.Errorf("RunnerSkew = %q", got)
	}
	if got := fmt.Sprintf(CLISkew, ve.Found, ve.Understood); !strings.Contains(got, "upgrade Fugaro") || strings.Contains(got, "runner image") {
		t.Errorf("CLISkew = %q, want the CLI's remedy only", got)
	}
}
