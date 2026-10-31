package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rigor"
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
	return []byte(`{"version": 4, "providers": {"openrouter": ` + entry + `}}`)
}

func TestDecodeIgnoresUnknownKeys(t *testing.T) {
	// A newer Fugaro's policy must not brick this runner on a field it does
	// not know (P8): unknown keys at the TOP LEVEL are ignored. Version gates
	// a newer policy; the budget block is strict
	// (TestDecodeRefusesABudgetWhoseKeysAllMiss), and so is a providers entry
	// (TestDecodeRefusesAMisspeltProviderKey). `routing` is neither: a member
	// this Fugaro does not model is ignored beside a written `claude`, the way
	// an unknown top-level key is, but a block that names no `claude` switch is
	// refused (validRoutingMembers, TestRoutingCommentsMatchTheCode), and the
	// switch's own values are checked (TestDecodeRefusesAnUnknownVia) — see the
	// package doc for why the shape of a known block is not the same case as a
	// key this binary cannot name.
	p, found, err := Decode([]byte(`{
		"version": 4,
		"budget": {"mode": "observe"},
		"providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}},
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

// TestDecodeRefusesAMisspeltProviderKey: a key inside a providers entry that
// this Fugaro does not know is refused rather than ignored, exactly as
// internal/config refuses one in fugaro.yaml. The two keys below are the ones
// where ignoring the misspelling fails OPEN, which is what the old tolerant
// decode did: "allow_data_tp" leaves nil, and a nil allow_data_to means every
// repository (config.ModelProvider.AllowsData) — the widest data-sharing
// setting there is, and the one ValidateModelProviders' ambiguity check never
// sees through JSON, because the marker it reads is set by the YAML
// unmarshaller. "route_fee_pct_" drops the owner's surcharge. A refusal is a
// refusal: there is no policy to read the field back from.
func TestDecodeRefusesAMisspeltProviderKey(t *testing.T) {
	// The correct spelling, first, so the refusals below are about the key.
	p, found, err := Decode(providerPolicy(`{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"], "route_fee_pct": 3}`))
	if err != nil || !found {
		t.Fatalf("the correctly spelled entry was refused: %v", err)
	}
	got := p.Providers["openrouter"]
	if got.AllowsData("other/repo") || got.OpenToEveryRepo() {
		t.Errorf("the correctly spelled allow_data_to = %v, want acme/app only", got.AllowDataTo)
	}
	if got.RouteFeePct != 3 {
		t.Errorf("the correctly spelled route_fee_pct = %v, want 3", got.RouteFeePct)
	}

	for _, tc := range []struct{ name, entry string }{
		{"a misspelt allow_data_to, whose zero value means every repository", `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_tp": ["acme/app"]}`},
		{"a misspelt route_fee_pct, which would drop the surcharge", `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "route_fee_pct_": 3}`},
		{"a key a newer release might add", `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "future_provider_key": true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode(providerPolicy(tc.entry))
			if err == nil {
				t.Fatalf("Decode(%s) = (%v, nil), want a refusal: an ignored key here is the field it was meant to set left at its zero value", tc.entry, p)
			}
			if !strings.Contains(err.Error(), "openrouter") {
				t.Errorf("the refusal does not name the provider it is about: %v", err)
			}
		})
	}
}

// TestDecodeRefusesAProviderNamedOrSpelledTwice: a providers entry is read
// into a map — the block keyed by provider name, the entry keyed by member —
// and encoding/json keeps the last of a repeated key, so a document that names
// one twice is read as its second half, silently. Both of the members that
// fail open are here: a second allow_data_to replaces the first (so a node can
// widen an allow-list by repeating the key), and a second route_fee_pct
// replaces the fee. The block's own keys are held to it too: a provider named
// twice is one provider to every reader here, and the entry kept is the whole
// of what it allows.
func TestDecodeRefusesAProviderNamedOrSpelledTwice(t *testing.T) {
	entry := `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]`
	for _, tc := range []struct{ name, raw string }{
		{"allow_data_to twice, the second widened", string(providerPolicy(entry + `, "allow_data_to": ["acme/app"], "allow_data_to": ["*"]}`))},
		{"route_fee_pct twice, the second dropped", string(providerPolicy(entry + `, "route_fee_pct": 3, "route_fee_pct": 0}`))},
		{"the same member twice spelled in two cases", string(providerPolicy(entry + `, "allow_data_to": ["acme/app"], "ALLOW_DATA_TO": ["*"]}`))},
		{"the provider named twice", `{"version": 4, "providers": {"openrouter": ` + entry + `, "allow_data_to": ["acme/app"]}, "openrouter": ` + entry + `, "allow_data_to": ["*"]}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(tc.raw))
			if err == nil || p != nil {
				t.Fatalf("Decode(%s) = (%v, %v), want a refusal: a repeated key is read as the last of the two, whichever value the document meant", tc.raw, p, err)
			}
			if !found {
				t.Error("the node exists and cannot be used, so found must be true (fail closed)")
			}
		})
	}
	// The other side: the same document with each key written once decodes,
	// and it decodes to the narrower list — the refusal above is about the
	// repeat, not about the values.
	p, found, err := Decode(providerPolicy(entry + `, "allow_data_to": ["acme/app"], "route_fee_pct": 3}`))
	if err != nil || !found {
		t.Fatalf("the entry written once was refused: %v", err)
	}
	if got := p.Providers["openrouter"]; got.AllowsData("other/repo") || got.RouteFeePct != 3 {
		t.Errorf("the entry written once = %+v, want acme/app and a fee of 3", got)
	}
}

// TestDecodeRefusesANullRouteFeePct: route_fee_pct is a float64, and a written
// null decodes into it as 0 — no surcharge at all. The key is there, so the
// operator (or a tool that serialized an absent value explicitly) meant to set
// a fee; a misspelling of the same name is refused one key over, and letting
// the null through would price every request on that route without the
// surcharge while the node says otherwise. A null require_zdr or price_policy
// is the same zero value the absent key gives, so neither is refused.
func TestDecodeRefusesANullRouteFeePct(t *testing.T) {
	entry := `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]`
	p, found, err := Decode(providerPolicy(entry + `, "route_fee_pct": null}`))
	if err == nil || p != nil {
		t.Fatalf("Decode(%s) = (%v, %v), want a refusal", entry, p, err)
	}
	if !found {
		t.Error("found must be true: the node exists and cannot be used")
	}
	if !strings.Contains(err.Error(), "route_fee_pct") {
		t.Errorf("the refusal does not name the field it is about: %v", err)
	}
	// A fee of 0 written as 0 is not the same document and is not refused: the
	// refusal is for the null, which says nothing about what was meant.
	if _, _, err := Decode(providerPolicy(entry + `, "route_fee_pct": 0}`)); err != nil {
		t.Errorf("an explicit 0 was refused: %v", err)
	}
	// And the members whose null IS their default keep it.
	if _, _, err := Decode(providerPolicy(entry + `, "require_zdr": null, "price_policy": null}`)); err != nil {
		t.Errorf("a null require_zdr or price_policy was refused: %v", err)
	}
}

// TestDecodeRefusesADuplicatedTopLevelKey: Decode reads the node's keys out of
// a map, and encoding/json keeps the last of a repeated one. The hazard is not
// the value that wins but the block that loses: {"budget": {...}, "budget":
// null} is a node whose budget is empty, so the run keeps the ceiling its job
// environment was deployed with while the operator's node appears to carry one
// — with no diagnostic anywhere, because the node reads as valid. A modelled
// key spelled in another case is the same silence one step over: nothing reads
// "Budget", so the block under it is not read either, and the key is not
// listed in UnknownKeys as a misspelling of this Fugaro's own name is
// (TestDecodeIgnoresUnknownKeys holds the other half: a key this Fugaro does
// not model at all is still tolerated).
func TestDecodeRefusesADuplicatedTopLevelKey(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"budget twice, the second null", `{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 50}, "budget": null}`},
		{"budget twice, the second narrower", `{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 50}, "budget": {"mode": "observe"}}`},
		{"providers twice", `{"version": 4, "providers": {}, "providers": null}`},
		{"a modelled key in another case", `{"version": 4, "Budget": {"mode": "enforce"}}`},
		{"version twice", `{"version": 4, "version": 4}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(tc.raw))
			if err == nil || p != nil {
				t.Fatalf("Decode(%s) = (%v, %v), want a refusal: the second key is read and the first is dropped, whatever it held", tc.raw, p, err)
			}
			if !found {
				t.Error("found must be true: the node exists and cannot be used")
			}
		})
	}
	// The document with each key written once, in the same order, decodes: the
	// refusal is the repeat, not the shape.
	p, found, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 50}}`))
	if err != nil || !found || p.Budget.PerRunUSD != 50 {
		t.Fatalf("Decode = (%+v, %v, %v), want the budget it names", p, found, err)
	}
}

func TestDecodeRefusesBadVersion(t *testing.T) {
	for name, raw := range map[string]string{
		"no version":     `{"budget": {"mode": "observe"}}`,
		"version 0":      `{"version": 0}`,
		"version 5":      `{"version": 5}`,
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
	_, _, err := Decode([]byte(`{"version": 5}`))
	if err == nil || !strings.Contains(err.Error(), "policy is version 5") {
		t.Errorf("the version refusal must use Skew, got %v", err)
	}
}

// TestDecodeReadsTheDocumentedSnakeCaseKeys is the test that would have caught
// a missing json tag: every documented key, field by field.
func TestDecodeReadsTheDocumentedSnakeCaseKeys(t *testing.T) {
	p, found, err := Decode([]byte(`{
		"version": 4,
		"budget": {"mode": "enforce", "per_run_usd": 12.5, "allowed_models": ["claude-sonnet-5"], "max_run_tokens": 250000},
		"allow_repos": ["github/acme/*", "bitbucket/acme/app"]
	}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("Version = %d, want %d", p.Version, Version)
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
		"camelCase per_run_usd":    `{"version": 4, "budget": {"mode": "enforce", "perRunUsd": 5}}`,
		"camelCase max_run_tokens": `{"version": 4, "budget": {"mode": "enforce", "MaxRunTokens": 1000}}`,
		"a mistyped mode":          `{"version": 4, "budget": {"mode": "observe", "perRunUsd": 5}}`,
		"not an object":            `{"version": 4, "budget": 5}`,
		"a list":                   `{"version": 4, "budget": []}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error on an existing node", name, raw, p, found, err)
		}
	}
}

// The strict decode above refuses a budget key no field answers to, and passes
// one that answers to a field in another case: encoding/json matches a member
// name case-insensitively, so {"mode": "enforce", "MODE": "off"} decodes to
// Mode "off" with no error at all — the last key the document writes wins —
// and {"mode": "enforce", "mode": "observe"} is the same document order
// deciding the same way. The block is enforced, not advisory (validBudget), and
// internal/config refuses the same spelling in fugaro.yaml, so the names are
// held to the documented ones here too.
func TestDecodeRefusesCaseFoldedBudgetMembers(t *testing.T) {
	for name, raw := range map[string]string{
		"a case-folded mode beside the real one": `{"version": 4, "budget": {"mode": "enforce", "MODE": "off"}}`,
		"mode written twice":                     `{"version": 4, "budget": {"mode": "enforce", "mode": "observe"}}`,
		"a case-folded allowed_models":           `{"version": 4, "budget": {"mode": "enforce", "ALLOWED_MODELS": ["claude-sonnet-5"]}}`,
		"a case-folded per_run_usd":              `{"version": 4, "budget": {"mode": "enforce", "PER_RUN_USD": 5}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal", name, raw, p, found, err)
			continue
		}
		if !strings.Contains(err.Error(), "budget") {
			t.Errorf("%s: err = %v, want it to name the block", name, err)
		}
	}
	// The documented spelling is read as before, values and all.
	p, found, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 12.5, "allowed_models": ["claude-sonnet-5"]}}`))
	if err != nil || !found || p == nil || p.Budget.Mode != "enforce" || p.Budget.PerRunUSD != 12.5 {
		t.Fatalf("a correctly spelled budget was refused: (%v, %v, %v)", p, found, err)
	}
}

// TestDecodeRefusesABadBudgetValue: the values are enforced, not advisory, so
// Decode checks them the way internal/config's validateBudget does. An
// unrecognised mode is the dangerous one: policy.Merge treats it as unset, the
// run falls back to off, and enforcement silently never turns on.
func TestDecodeRefusesABadBudgetValue(t *testing.T) {
	for name, raw := range map[string]string{
		"an unknown mode":       `{"version": 4, "budget": {"mode": "enforc"}}`,
		"a Go-ish mode":         `{"version": 4, "budget": {"mode": "Enforce"}}`,
		"a boolean mode":        `{"version": 4, "budget": {"mode": true}}`,
		"a negative cap":        `{"version": 4, "budget": {"mode": "enforce", "per_run_usd": -1}}`,
		"an absurd cap":         `{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 1e9}}`,
		"a cap rounding to 0":   `{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 0.0000001}}`,
		"a negative token cap":  `{"version": 4, "budget": {"mode": "enforce", "max_run_tokens": -1}}`,
		"an empty allow-list":   `{"version": 4, "budget": {"mode": "enforce", "allowed_models": []}}`,
		"a token cap as string": `{"version": 4, "budget": {"mode": "enforce", "max_run_tokens": "1000"}}`,
		// H1 (security review of PR #379): a budget block is there and names
		// no mode. The mode would otherwise come from the job environment,
		// which the policy's own reader cannot see, so a node published for
		// its caps would enforce whatever the deploy happened to say — the
		// silent "enforcement never turned on" this strict decode exists to
		// prevent. A policy that sets no budget key leaves the block out
		// entirely.
		"a cap with no mode":       `{"version": 4, "budget": {"per_run_usd": 0.000001}}`,
		"an empty budget block":    `{"version": 4, "budget": {}}`,
		"a token cap with no mode": `{"version": 4, "budget": {"max_run_tokens": 1000}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error", name, raw, p, found, err)
		}
	}
	for _, raw := range []string{
		`{"version": 4, "budget": {"mode": "off"}}`,
		`{"version": 4, "budget": {"mode": "observe", "per_run_usd": 12.5, "max_run_tokens": 250000, "allowed_models": ["claude-sonnet-5"]}}`,
		`{"version": 4, "budget": {"mode": "enforce", "per_run_usd": 0.000001}}`,
		`{"version": 4}`,
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
	p, _, err := Decode([]byte(`{"version": 4, "budget": {"mode": "observe"}}`))
	if err != nil {
		t.Fatalf("a partial budget must be valid: %v", err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 0 || p.Budget.MaxRunTokens != 0 {
		t.Errorf("a partial budget = %+v, want observe with zero ceilings", p.Budget)
	}
	p, _, err = Decode([]byte(`{"version": 4}`))
	if err != nil {
		t.Fatalf("a policy with no budget block must be valid: %v", err)
	}
	if !reflect.DeepEqual(p.Budget, Budget{}) {
		t.Errorf("no budget block = %+v, want the zero Budget", p.Budget)
	}
}

func TestDecodeRefusesBadAllowRepoPattern(t *testing.T) {
	for _, pattern := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/x y"} {
		if _, _, err := Decode([]byte(`{"version": 4, "allow_repos": ["` + pattern + `"]}`)); err == nil {
			t.Errorf("allow_repos [%q] must be refused", pattern)
		}
	}
	if _, _, err := Decode([]byte(`{"version": 4, "allow_repos": "github/acme/*"}`)); err == nil {
		t.Error("allow_repos that is not a list of strings must be refused")
	}
	// A pattern carrying an unreasonable number of wildcards reaches the same
	// check through Decode: the node is what the runner reads, so the refusal
	// must be there too (ValidPattern caps them; TestValidPatternCapsWildcards).
	if _, _, err := Decode([]byte(`{"version": 4, "allow_repos": ["github/acme/*****"]}`)); err == nil {
		t.Error("allow_repos with more wildcards than ValidPattern allows must be refused")
	}
	// A single "*" is the design's explicit choice: every repository.
	p, _, err := Decode([]byte(`{"version": 4, "allow_repos": ["*"]}`))
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
		raw := []byte(`{"version": 4, "providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "` + secret + `", "models": ["deepseek/*"]}}}`)
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
		"no kind":            `{"base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"an unknown kind":    `{"kind": "openai-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"an http base URL":   `{"kind": "anthropic-compat", "base_url": "http://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no base URL":        `{"kind": "anthropic-compat", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"a mistyped auth":    `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "Authorization", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no auth":            `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"no models":          `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key"}`,
		"an empty data list": `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": []}`,
		"a null data list":   `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": null}`,
		// encoding/json matches a member to its field case-insensitively, so
		// the two spellings below used to decode into the very fields the
		// documented ones name — while the checks that read the raw document
		// (validProviders' allow_data_to lookup) and the carrier's own
		// fugaro.yaml rules (ModelProvider.UnmarshalYAML, exact names) do not
		// see them. The null one published "every repository".
		"a case-variant null data list": `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "Allow_Data_To": null}`,
		"a case-variant member name":    `{"Kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"a bad data entry":              `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme"]}`,
		"an absurd route fee":           `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "route_fee_pct": 51}`,
		// A repeated member name: encoding/json keeps the last one it reads, so
		// the second allow_data_to decided the entry — ["acme/app"] then ["*"]
		// decoded as ["*"], every repository, with nothing left in the map to
		// refuse. The name is read off the document as written
		// (objectMemberNames) so the repetition is visible, exactly as it is in
		// `budget`, in a model_prices entry, in a profile entry
		// (checkWrittenProfileFields) and in the routing switch.
		"a repeated data list": `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"], "allow_data_to": ["*"]}`,
		"a repeated kind":      `{"kind": "anthropic-compat", "kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"]}`,
		"an alias pattern":     `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["claude-*"]}`,
		"a misspelt policy":    `{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "price_policy": "cheaper"}`,
	} {
		p, found, err := Decode(providerPolicy(entry))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal on an existing node", name, entry, p, found, err)
		}
	}
	// A case-variant member is refused by its spelling, and the refusal names
	// the one to write: the point of the row above is that this spelling is a
	// spelling of allow_data_to, and an operator who reads "unknown field
	// Allow_Data_To" would be told the wrong thing about a key this Fugaro
	// knows.
	if _, _, err := Decode(providerPolicy(`{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "Allow_Data_To": null}`)); err == nil ||
		!strings.Contains(err.Error(), "Allow_Data_To") || !strings.Contains(err.Error(), "allow_data_to") {
		t.Errorf("the case-variant null list = %v, want a refusal naming both the spelling written and the one to write", err)
	}
	// The refusal of a repeated member names the member and says which value
	// was read: the row above is about the hole (["*"] out of a narrower first
	// write), this one is about the operator being able to find it.
	if _, _, err := Decode(providerPolicy(`{"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"], "allow_data_to": ["*"]}`)); err == nil ||
		!strings.Contains(err.Error(), "providers.openrouter") || !strings.Contains(err.Error(), `"allow_data_to"`) || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a repeated allow_data_to = %v, want a refusal naming the entry and the member written twice", err)
	}
	// The same walk one level up: a provider defined twice on the block is the
	// entry the decoder kept, which is the last one written, so the node could
	// be routed and metered by a definition no reader of the document sees.
	if _, _, err := Decode([]byte(`{"version": 4, "providers": {
		"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["acme/app"]},
		"openrouter": {"kind": "anthropic-compat", "base_url": "https://evil.example/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["*"]}}}`)); err == nil ||
		!strings.Contains(err.Error(), "providers names") || !strings.Contains(err.Error(), `"openrouter"`) || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a provider entry written twice = %v, want a refusal naming the entry written twice", err)
	}
	// Two providers may not claim the same model: the run would route to one of
	// them arbitrarily, and the charge would be whichever one it was.
	if _, _, err := Decode([]byte(`{"version": 4, "providers": {
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
		"true":    {Provider{ModelProvider: config.ModelProvider{RequireZDR: &zdron}}, `"require_zdr":true`},
		"false":   {Provider{ModelProvider: config.ModelProvider{RequireZDR: &zdroff}}, `"require_zdr":false`},
		"highest": {Provider{ModelProvider: config.ModelProvider{PricePolicy: PricePolicyHighest}}, `"price_policy":"highest"`},
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
		"an alias":                 `["sonnet"]`,
		"the wildcard":             `["claude-*"]`,
		"a -latest pointer":        `["deepseek/deepseek-v4-flash-latest"]`,
		"a :variant":               `["deepseek/deepseek-v4-flash:free"]`,
		"an anthropic wildcard":    `["anthropic/*"]`,
		"an anthropic claude glob": `["anthropic/claude-*"]`,
		"empty entry":              `[""]`,
		"whitespace":               `["claude sonnet"]`,
		"listed twice":             `["claude-sonnet-5-5", "claude-sonnet-5-5"]`,
		"too long":                 `["` + strings.Repeat("m", 101) + `"]`,
		"null entry":               `[null]`,
		"a number entry":           `[5]`,
	} {
		p, found, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "allowed_models": ` + list + `}}`))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(allowed_models %s) = (%v, %v, %v), want a refusal", name, list, p, found, err)
		}
	}
	p, _, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "allowed_models": ["claude-sonnet-5-5", "deepseek/deepseek-v4.1-flash"]}}`))
	if err != nil {
		t.Fatalf("two explicit model IDs must be valid: %v", err)
	}
	// An explicit anthropic/<id> is the claim form (P4: a price or an allowed
	// model is not a permission): it is ACCEPTED where it is written and
	// enables nothing; only the wildcard and glob spellings above are refused.
	// The decoded policy is read back, not only the absence of an error: the
	// entry has to be *there*, whole, or the acceptance above says nothing
	// about what a run would enforce (the review's finding — the result used
	// to be discarded here and the assertion below read the two-model decode
	// from above, so an anthropic entry that decoded to nothing was accepted
	// and never checked).
	//
	// Mutation (run, restored): drop every entry beginning "anthropic/" where
	// Decode fills Budget.AllowedModels. This assertion fails and the
	// two-model one below still passes, which is the gap it was written for.
	ap, _, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "allowed_models": ["anthropic/claude-sonnet-5.5"]}}`))
	if err != nil {
		t.Fatalf("an explicit anthropic/<id> allowed model must be accepted (a price is not a permission): %v", err)
	}
	if got := ap.Budget.AllowedModels; len(got) != 1 || got[0] != "anthropic/claude-sonnet-5.5" {
		t.Errorf("AllowedModels = %v, want exactly the anthropic entry as it was written", got)
	}
	if len(p.Budget.AllowedModels) != 2 {
		t.Errorf("AllowedModels = %v, want both", p.Budget.AllowedModels)
	}
}

// run, not a data store, so a node past MaxPolicyBytes is refused rather than
// decoded.
func TestDecodeRefusesAnOversizedNode(t *testing.T) {
	pad := func(n int) []byte {
		return []byte(`{"version": 4, "allow_repos": ["*"], "pad": "` + strings.Repeat("a", n) + `"}`)
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
	p, _, err := Decode([]byte(`{"version": 4, "model_prices": ` + block + `}`))
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
		"input_per_m missing":          `{"version": 4, "model_prices": {"claude-sonnet-5-5": {"output_per_m": 15}}}`,
		"a camelCase rate":             `{"version": 4, "model_prices": {"claude-sonnet-5-5": {"input_per_m": 3, "output_per_m": 15, "cacheRead": 0.1}}}`,
		"an anthropic glob key":        `{"version": 4, "model_prices": {"anthropic/claude-*": {"input_per_m": 3, "output_per_m": 15}}}`,
		"a negative rate":              `{"version": 4, "model_prices": {"claude-sonnet-5-5": {"input_per_m": -3, "output_per_m": 15}}}`,
		"a bare number":                `{"version": 4, "model_prices": 5}`,
		"a list":                       `{"version": 4, "model_prices": []}`,
		"a long_context without rates": `{"version": 4, "model_prices": {"claude-sonnet-5-5": {"input_per_m": 3, "output_per_m": 15, "long_context": {"above_input_tokens": 200000}}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want an error", name, raw, p, found, err)
		}
	}
	// An explicit anthropic/<id> key is a PRICE for the claim form (P4: a price
	// is not a permission): accepted where it is written, enabling nothing.
	if p, _, err := Decode([]byte(`{"version": 4, "model_prices": {"anthropic/claude-sonnet-5.5": {"input_per_m": 3, "output_per_m": 15}}}`)); err != nil || len(p.Prices) != 1 {
		t.Errorf("an explicit anthropic/<id> price key must be accepted: err = %v", err)
	}
	// An empty block and an absent one are both "no overrides".
	for _, raw := range []string{`{"version": 4}`, `{"version": 4, "model_prices": {}}`, `{"version": 4, "model_prices": null}`} {
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

// TestPolicyCarriesRouting: the switch is delivered by the node — `project
// policy publish` copies the operator's routing block into config/policy/shared
// and a run reads it back byte for byte — and the node is held to the same
// shape rule the operator's own config is: an explicit list of Claude IDs,
// never a pattern (D1a). An absent block stays absent, which is direct (D1b).
func TestPolicyCarriesRouting(t *testing.T) {
	// The node carries the route entry the switch resolves through: one
	// explicit "anthropic/<id>" per Claude ID, with allow_data_to written
	// (D1b) — without them the switch names a route nothing claims, which the
	// decoder refuses below.
	pol := &Policy{
		Version: Version,
		Budget:  Budget{Mode: "enforce", PerRunUSD: 5},
		Providers: map[string]Provider{"openrouter": {ModelProvider: config.ModelProvider{
			Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api/v1", Auth: "bearer",
			Secret: "openrouter-key", Models: []string{"anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5"},
			AllowDataTo: []string{"*"}, PricePolicy: PricePolicyHighest,
		}}},
		Routing: &Routing{Claude: &ClaudeRoute{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5", "claude-opus-5-5"}}},
	}
	// The delivery path: the CLI marshals the policy onto the node, exactly as
	// project policy publish writes it.
	raw, err := json.Marshal(pol)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"claude-sonnet-5-5"`) {
		t.Fatalf("the node does not carry the IDs: %s", raw)
	}
	got, found, err := Decode(raw)
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v)", got, found, err)
	}
	if got.Routing == nil || got.Routing.Claude == nil {
		t.Fatalf("the routing block did not survive the node: %+v", got.Routing)
	}
	if got.Routing.Claude.Via != RouteOpenRouter || len(got.Routing.Claude.Models) != 2 || got.Routing.Claude.Models[0] != "claude-sonnet-5-5" {
		t.Errorf("routing = %+v", got.Routing.Claude)
	}
	// A node without one decodes to no block: direct (D1b).
	if p, _, err := Decode([]byte(`{"version": 4}`)); err != nil || p.Routing != nil {
		t.Errorf("an absent block = (%+v, %v), want nil and no error", p.Routing, err)
	}
	// And the shapes D1a refuses are refused on the node too, with the key
	// named: a pattern, an empty list under via: openrouter, and a via this
	// Fugaro cannot act on.
	routes := `{"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["anthropic/claude-sonnet-5.5"], "allow_data_to": ["*"]}}`
	for _, tc := range []struct{ name, routing, want string }{
		{"a glob", `{"claude": {"via": "openrouter", "models": ["claude-*"]}}`, "explicit list"},
		{"an empty list", `{"claude": {"via": "openrouter"}}`, "routing.claude.models"},
		{"an empty via", `{"claude": {"via": ""}}`, "routing.claude.via"},
	} {
		raw := []byte(`{"version": 4, "providers": ` + routes + `, "routing": ` + tc.routing + `}`)
		p, found, err := Decode(raw)
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode = (%v, %v, %v), want a refusal", tc.name, p, found, err)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
	// D1b is D1b on the node too (the security review of this slice): a node
	// that routes a Claude ID through a provider which does not claim it, or
	// which claims it without allow_data_to written, is refused — the node is
	// the artifact every run reads, and "the switch is there but inert" is not
	// a state a run reports.
	for _, tc := range []struct{ name, providers, want string }{
		{"no provider claims the ID", `{"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["deepseek/*"], "allow_data_to": ["*"]}}`, "no provider entry claims it"},
		{"the claim has no allow_data_to", `{"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["anthropic/claude-sonnet-5.5"]}}`, "allow_data_to"},
		{"no providers block at all", `{}`, "no provider entry claims it"},
	} {
		raw := []byte(`{"version": 4, "providers": ` + tc.providers + `, "routing": {"claude": {"via": "openrouter", "models": ["claude-sonnet-5-5"]}}}`)
		p, found, err := Decode(raw)
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode = (%v, %v, %v), want a refusal", tc.name, p, found, err)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

// TestDecodeRefusesFoldEqualMemberNames: encoding/json does not fold a member
// name with strings.ToLower — it folds it with unicode.SimpleFold (fold.go's
// foldName, whose contract is bytes.EqualFold), and SimpleFold has orbits
// ToLower does not reach. U+017F (ſ, LATIN SMALL LETTER LONG S) is the one that
// costs: ToLower leaves it as ſ, while the decoder reads it as 's', so a block
// naming allowed_models and allowed_modelſ was two names to a check built on
// ToLower and one member to the decoder — which keeps the last of the two, so
// the document's visible allow-list was not the one the run was bounded by.
//
// U+212A (K, KELVIN SIGN) is the same fold from the other side and is the
// regression guard: it is fold-equal to 'k', and a folding built on an orbit
// minimum must still refuse it.
//
// The routing switch is where it matters most: `{"via": "direct", "modelſ":
// [...]}` reads as a switch with a model list, from a document that spells the
// member as nothing this Fugaro documents.
func TestDecodeRefusesFoldEqualMemberNames(t *testing.T) {
	const longS = "ſ"  // ſ, fold-equal to s
	const kelvin = "K" // K, fold-equal to k
	for name, raw := range map[string]string{
		"the long s in an allow-list member":       `{"version": 4, "budget": {"mode": "enforce", "allowed_models": ["claude-sonnet-5"], "allowed_model` + longS + `": ["claude-opus-5-5"]}}`,
		"the long s alone in an allow-list member": `{"version": 4, "budget": {"mode": "enforce", "allowed_model` + longS + `": ["claude-opus-5-5"]}}`,
		"the long s in routing.claude.models":      `{"version": 4, "routing": {"claude": {"via": "direct", "model` + longS + `": ["claude-sonnet-5-5"]}}}`,
		"the long s beside routing.claude.models":  `{"version": 4, "routing": {"claude": {"via": "direct", "models": ["claude-sonnet-5-5"], "model` + longS + `": ["claude-opus-5-5"]}}}`,
		"the kelvin sign in a rate":                `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "output_per_m": 1, "web_search_per_1k": 0.01, "web_search_per_1` + kelvin + `": 9}}}`,
		"the kelvin sign alone in a rate":          `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "output_per_m": 1, "web_search_per_1` + kelvin + `": 9}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal", name, raw, p, found, err)
			continue
		}
		if !strings.Contains(err.Error(), "fold") && !strings.Contains(err.Error(), "spells") && !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("%s: err = %v, want it to say why the name is refused", name, err)
		}
	}
	// The documented spelling is still read, ſ nowhere in it.
	p, found, err := Decode([]byte(`{"version": 4, "budget": {"mode": "enforce", "allowed_models": ["claude-sonnet-5"]}, "routing": {"claude": {"via": "direct", "models": ["claude-sonnet-5-5"]}}}`))
	if err != nil || !found || p == nil || len(p.Budget.AllowedModels) != 1 || len(p.Routing.Claude.Models) != 1 {
		t.Fatalf("a correctly spelled node was refused: (%v, %v, %v)", p, found, err)
	}
}

// TestReviewFloorDecodeStrict: the owner's review-level floor is the one
// member of the node that bounds how much review a launcher-written layer may
// turn off (D722, G1), so a name this Fugaro cannot read is refused rather
// than dropped — a dropped level is a run reviewing less than the owner
// required, and the node is hand-writable. It also pins the shape a project
// with no floor publishes: no member at all, so the node of such a project is
// what it was before the member existed.
func TestReviewFloorDecodeStrict(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": 4, "review_floor": {"pro": ["l2"], "critical": ["l2", "l3"]}}`))
	if err != nil || !found {
		t.Fatalf("the correctly spelled floor was refused: %v", err)
	}
	want := map[rigor.Mode][]policy.Level{rigor.Pro: {policy.LevelL2}, rigor.Critical: {policy.LevelL2, policy.LevelL3}}
	if !reflect.DeepEqual(p.ReviewFloor, want) {
		t.Errorf("ReviewFloor = %v, want %v", p.ReviewFloor, want)
	}
	if p.UnknownKeys != nil {
		t.Errorf("review_floor is listed as an unknown key: %v", p.UnknownKeys)
	}

	for _, tc := range []struct{ name, body, want string }{
		{"an unknown mode", `{"prod": ["l2"]}`, "is not a rigor mode"},
		{"a mode in another case", `{"PRO": ["l2"]}`, "is not a rigor mode"},
		{"an unknown level", `{"pro": ["l4"]}`, "is not a review level"},
		{"a level in another case", `{"pro": ["L2"]}`, "is not a review level"},
		{"a repeated level", `{"pro": ["l2", "l2"]}`, "is listed twice"},
		{"a mode with no level", `{"pro": []}`, "a floor with no level in it says nothing"},
		{"a mode named twice", `{"pro": ["l2"], "pro": ["l3"]}`, "names \"pro\" twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(`{"version": 4, "review_floor": ` + tc.body + `}`))
			if err == nil || p != nil || !found || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Decode = (%v, %v, %v), want a refusal naming %q", p, found, err, tc.want)
			}
		})
	}

	// A written empty block is no floor, and a policy that sets none marshals
	// no member: the publisher writes this struct, so the member's absence is
	// what keeps a floorless project's node byte for byte what it was.
	empty, found, err := Decode([]byte(`{"version": 4, "review_floor": {}}`))
	if err != nil || !found || len(empty.ReviewFloor) != 0 {
		t.Fatalf("Decode({\"review_floor\": {}}) = (%v, %v, %v), want no floor", empty, found, err)
	}
	for _, raw := range []string{`{"version": 4}`, `{"version": 4, "review_floor": null}`} {
		q, _, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if q.ReviewFloor != nil {
			t.Errorf("Decode(%s).ReviewFloor = %v, want nil", raw, q.ReviewFloor)
		}
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "review_floor") {
			t.Errorf("a policy with no floor marshals one: %s", b)
		}
	}
}
