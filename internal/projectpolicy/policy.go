// Package projectpolicy is the project policy object of the project-centric
// model (docs/design/project-centric.md §5): one readable node,
// config/policy/shared, its per-repository children under
// config/policy/repos/$slug, and the admin-only config/admin.
//
// Nothing reads this package yet. Phase 1 task 1.1 is the object, its paths,
// its version and its matcher only: the runner's fail-closed read is task 1.3
// and the CLI's is task 1.6, both held back until this shape is reviewed. It
// imports internal/task and never internal/budget, internal/runner or
// internal/cli, so a policy can be decoded anywhere without a cycle.
//
// Decoding is tolerant in one direction and strict in the other. Unknown JSON
// keys are ignored, so a policy a newer Fugaro wrote does not brick an older
// runner on a field it does not know. In exchange two rules keep that
// tolerance from turning a typo into a silent zero ceiling: every field
// carries the documented snake_case json tag, so only the documented spellings
// decode (a camelCase policy would otherwise decode to a zero per-run cap and
// a zero token ceiling, both of which are enforced); and a budget object that
// carries keys but none of its four documented ones is an error rather than an
// all-zero Budget. Version is the only field that can refuse a node (P8).
//
// The readable node holds nothing personal and no secret values: every run
// token can read it, and a prompt-injected agent can take the token through
// /proc (§5(2)). Repository-specific parts live under a $slug node the way
// config/caps/repos/$slug does, a provider entry names a secret (never carries
// one) and owner metadata belongs in the admin-only config/admin node.
package projectpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Policy is the shared, readable node, decoded as JSON.
//
// The `budget` block is the M9a policy (mode, per-run ceiling, allowed models,
// token ceiling); it is all-or-nothing only in the sense Decode documents: a
// block that names none of its four keys is an error, a block that names some
// of them leaves the rest at zero, exactly as fugaro.yaml's own budget: block
// does.
type Policy struct {
	Version    int                 `json:"version"`
	Budget     Budget              `json:"budget"`
	AllowRepos []string            `json:"allow_repos"`
	Providers  map[string]Provider `json:"providers,omitempty"`
	Prices     map[string]Price    `json:"prices,omitempty"`  // model_prices, the owner's overrides
	Routing    *Routing            `json:"routing,omitempty"` // phase 1b writes it; nil = direct (D1b)
}

// Budget is the ceiling a run is held to. An absent field is zero and means
// "this policy does not set it", the same as a fugaro.yaml budget: block.
type Budget struct {
	Mode          string   `json:"mode"`
	PerRunUSD     float64  `json:"per_run_usd"`
	AllowedModels []string `json:"allowed_models"`
	MaxRunTokens  int64    `json:"max_run_tokens"`
}

// BudgetKeys are the four documented keys of Budget, in the spelling the json
// tags use. Decode refuses a budget object that carries keys and none of
// these (a camelCase or otherwise mistyped block would otherwise decode to a
// zero per-run cap and a zero token ceiling, and those are enforced).
var BudgetKeys = []string{"mode", "per_run_usd", "allowed_models", "max_run_tokens"}

// Provider is one model provider entry. It carries the secret's NAME, never a
// value (§5): the node is readable by every run token.
type Provider struct {
	BaseURL     string   `json:"base_url,omitempty"`
	AuthStyle   string   `json:"auth_style,omitempty"`
	Secret      string   `json:"secret,omitempty"` // a secret name in Secret Manager, never a value
	Models      []string `json:"models,omitempty"`
	AllowDataTo []string `json:"allow_data_to,omitempty"`
	RouteFeePct float64  `json:"route_fee_pct,omitempty"`
	RequireZDR  bool     `json:"require_zdr,omitempty"`
	PricePolicy string   `json:"price_policy,omitempty"`
}

// Price is one model's owner-set rates, spelled as the price catalog spells
// model_prices (internal/pricing/overrides.go wireRates): an absent field is
// zero and means "not set". Prices is the model_prices override map.
type Price struct {
	InputPerM      float64    `json:"input_per_m"`
	OutputPerM     float64    `json:"output_per_m"`
	CacheWrite5m   float64    `json:"cache_write_5m,omitempty"`
	CacheWrite1h   float64    `json:"cache_write_1h,omitempty"`
	CacheRead      float64    `json:"cache_read,omitempty"`
	WebSearchPer1k float64    `json:"web_search_per_1k,omitempty"`
	LongContext    *PriceTier `json:"long_context,omitempty"`
}

// PriceTier is a long-context price that replaces the base rates for the whole
// call once its input passes a threshold.
type PriceTier struct {
	AboveInputTokens int64   `json:"above_input_tokens"`
	InputPerM        float64 `json:"input_per_m"`
	OutputPerM       float64 `json:"output_per_m"`
}

// Routing is phase 1b's routing layer (D1a: an object, not a scalar). nil is
// today's behaviour, direct (D1b).
type Routing struct {
	Claude *ClaudeRoute `json:"claude,omitempty"`
}

// ClaudeRoute is the Claude routing switch: an explicit list of Claude IDs,
// never a pattern (D1a).
type ClaudeRoute struct {
	Via    string   `json:"via"` // "direct", "openrouter" or "auto"
	Models []string `json:"models"`
}

// Decode reads the shared node. It returns (p, found, err):
//
//   - found=false, err=nil: the node is absent or empty, which is today's
//     behaviour (the runner keeps the policy from the job env);
//   - p != nil, err=nil: a policy this code understands;
//   - found=true, err!=nil: the node exists and cannot be used, which the
//     runner must treat as a halt, never as an empty ceiling (§5(3)).
//
// Unknown JSON keys are ignored; version is not (see Version).
func Decode(raw []byte) (*Policy, bool, error) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || string(t) == "null" {
		return nil, false, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is not a JSON object: %w", err)
	}

	var p Policy
	vraw, ok := doc["version"]
	if !ok {
		return nil, true, errors.New("projectpolicy: the policy has no version")
	}
	if err := json.Unmarshal(vraw, &p.Version); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy version is not an integer: %w", err)
	}
	if p.Version != Version {
		return nil, true, fmt.Errorf("projectpolicy: "+RunnerSkew, p.Version, Version)
	}

	if braw, ok := doc["budget"]; ok {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(braw, &keys); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget is not an object: %w", err)
		}
		if len(keys) > 0 && !hasBudgetKey(keys) {
			return nil, true, fmt.Errorf("projectpolicy: budget carries no documented key (want one of %s): a mistyped key would become a zero ceiling",
				strings.Join(BudgetKeys, ", "))
		}
		if err := json.Unmarshal(braw, &p.Budget); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget: %w", err)
		}
	}

	if araw, ok := doc["allow_repos"]; ok {
		if err := json.Unmarshal(araw, &p.AllowRepos); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: allow_repos: %w", err)
		}
		for i, pattern := range p.AllowRepos {
			if err := ValidPattern(pattern); err != nil {
				return nil, true, fmt.Errorf("projectpolicy: allow_repos[%d]: %w", i, err)
			}
		}
	}

	for key, into := range map[string]any{"providers": &p.Providers, "prices": &p.Prices, "routing": &p.Routing} {
		if rraw, ok := doc[key]; ok {
			if err := json.Unmarshal(rraw, into); err != nil {
				return nil, true, fmt.Errorf("projectpolicy: %s: %w", key, err)
			}
		}
	}
	return &p, true, nil
}

// hasBudgetKey reports whether keys names at least one documented Budget key.
func hasBudgetKey(keys map[string]json.RawMessage) bool {
	for _, k := range BudgetKeys {
		if _, ok := keys[k]; ok {
			return true
		}
	}
	return false
}
