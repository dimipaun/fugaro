// Package projectpolicy is the project policy object of the project-centric
// model (docs/design/project-centric.md §5): one readable node,
// config/policy/shared, its per-repository children under
// config/policy/repos/$slug, and the admin-only config/admin.
//
// Read is the package's entry point, and both sides of the model call it: the
// runner reads the node at run start and fails closed — a node that exists and
// cannot be read or decoded halts the run rather than falling back to the
// ceiling the job's environment was deployed with (internal/runner's
// projectPolicy, task 1.3) — and the launcher reads it before it decides
// whether to mint a run's budget identity and whether a job may be deployed at
// all (internal/cli's policyReadOnce, which the launch gate and init.go's
// readiness gate both call). Phase 1's
// shape is task 1.1 (the object, its paths, its version and its matcher) and
// task 1.2 (the nodes and their rules); the CLI's own `project policy publish`
// is task 1.6 and is not part of this. Beyond those two readers, internal/infra
// writes PathRulesMarker after a rules deploy, and internal/budget/rules' tests
// hold the template's paths in step with the Path* functions
// (TestTemplatePolicyPathsAgreeWithProjectpolicy,
// TestPolicyPathsEscapeASlugLikeBudgetKey — the drift guard lives in that
// package because internal/projectpolicy must not import internal/budget). It
// imports internal/rtdb (the client Read takes), internal/task, internal/policy,
// internal/pricing and internal/config (the carrier a provider entry is decoded
// and validated as, and the rules the budget block is held to), and never
// internal/budget, internal/runner or internal/cli, so a policy can be decoded
// anywhere without a cycle.
//
// Decoding is tolerant in one direction and strict in the other. Unknown JSON
// keys are ignored at the top level, so a policy a newer Fugaro wrote does not
// brick an older runner on a field it does not know; a newer Fugaro that adds
// a *budget* key bumps Version, the one field that can refuse a node (P8).
//
// The strict half exists because a mistake here is silently enforced, not
// merely dropped:
//
//   - every field carries the documented snake_case json tag, so only the
//     documented spellings decode;
//   - the budget block is decoded strictly, the way internal/config decodes
//     fugaro.yaml's budget block ("a misspelt key is an error, never 'no
//     policy'"), and its values — allowed_models entry by entry — are checked
//     as validateBudget checks them: a mode outside off/observe/enforce is
//     refused, because policy.Merge skips a mode it does not recognise and the
//     run then falls back to off — enforcement that silently never turns on,
//     the exact failure this package promises to prevent;
//   - model_prices is read with the catalogue's own parser and keeps the
//     catalogue's pointer spelling, so an omitted optional rate keeps its list
//     default instead of being priced at zero;
//   - a providers entry is the document internal/runner's ProvidersFromEnv
//     decodes out of FUGARO_MODEL_PROVIDERS (Provider embeds
//     config.ModelProvider, so the spelling cannot drift) and is checked by
//     internal/config's own ValidateModelProviders, so a base URL that is not
//     https, a kind that is not anthropic-compat, an auth style that is not
//     bearer or x-api-key, an ambiguous empty allow_data_to, a route_fee_pct
//     over the cap or a models list two providers both claim is refused here
//     rather than reaching a run, and so is price_policy, through the same
//     carrier.
//     Unknown keys *inside* an entry are refused too, unlike the top level's,
//     because an entry's fields are the ones a run is routed, shared and
//     metered by and three of them have a permissive zero: a misspelt
//     allow_data_to would publish "every repository", a misspelt route_fee_pct
//     would charge no fee, and a misspelt price_policy would price at the
//     cheapest end without anyone having chosen it. Tolerating them made this
//     node the one carrier where a typo flips a default, since fugaro.yaml and
//     FUGARO_MODEL_PROVIDERS both refuse the same key.
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
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// Policy is the shared, readable node, decoded as JSON.
//
// The `budget` block is the M9a policy (mode, per-run ceiling, allowed models,
// token ceiling); its keys are all-or-nothing in the sense Decode documents: a
// block that names some of them leaves the rest at zero, exactly as
// fugaro.yaml's own budget: block does, and a key that is not documented is an
// error rather than a silently-dropped zero.
type Policy struct {
	Version    int                 `json:"version"`
	Budget     Budget              `json:"budget"`
	AllowRepos []string            `json:"allow_repos"`
	Providers  map[string]Provider `json:"providers,omitempty"`
	Prices     map[string]Price    `json:"model_prices,omitempty"` // the owner's price overrides, as fugaro.yaml's model_prices spells them
	Routing    *Routing            `json:"routing,omitempty"`      // phase 1b writes it; nil = direct (D1b)

	// UnknownKeys lists the top-level keys of the node this Fugaro does not
	// know, sorted. Decode fills it in and never writes it back (json:"-"):
	// the keys are tolerated so that a policy a newer Fugaro wrote does not
	// brick an older reader (P8), but "tolerated" and "silently absent" look
	// the same in a decoded policy — a misspelt per_run_usdc leaves the cap
	// unset and the run then reports no ceiling where the owner set one. The
	// runner logs them (it must keep going: they may be a newer Fugaro's, and
	// it enforces every value it does understand); the CLI refuses them,
	// because the operator is the one who can fix them.
	UnknownKeys []string `json:"-"`
}

// knownTopLevelKeys are the keys Decode reads. Every other key is tolerated
// and listed in Policy.UnknownKeys.
var knownTopLevelKeys = map[string]bool{
	"version": true, "budget": true, "allow_repos": true,
	"providers": true, "model_prices": true, "routing": true,
}

// Budget is the ceiling a run is held to. An absent field is zero and means
// "this policy does not set it", the same as a fugaro.yaml budget: block.
type Budget struct {
	Mode          string   `json:"mode"`
	PerRunUSD     float64  `json:"per_run_usd"`
	AllowedModels []string `json:"allowed_models"`
	MaxRunTokens  int64    `json:"max_run_tokens"`
}

// Provider is one model provider entry (§5). It carries the secret's NAME,
// never a value: the node is readable by every run token, and a
// prompt-injected agent can take that token through /proc.
//
// It is config.ModelProvider, embedded rather than respelled, so the policy's
// provider document is byte-for-byte the one internal/runner's
// ProvidersFromEnv decodes out of FUGARO_MODEL_PROVIDERS: kind, base_url,
// auth, secret, models, allow_data_to, route_fee_pct, and the two
// price-catalogue switches require_zdr and price_policy (docs/design/
// provider-price-catalog.md D3). A respelling here would be a second carrier
// for one document, and the two would drift — a kind of drift no test can see,
// because each side would be self-consistent.
//
// It declares no field of its own, and that is a fix rather than an accident:
// an earlier revision re-declared RequireZDR and PricePolicy here, which
// shadowed the carrier's own copies at depth 0. The values a published node
// carried were decoded into the outer fields; the map validProviders hands
// to config.ValidateModelProviders is built from the inner one, so the
// price_policy check there validated a field that was always empty — a check
// that could not fail, which is the drift this type exists to prevent, in the
// direction nobody looks. The switches are the carrier's now, their accessors
// (RequireZDROn, PricePolicySet) come with the embedding, and their JSON names
// come from the carrier's own tags.
type Provider struct {
	config.ModelProvider
}

// Price is one model's owner-set rates, spelled exactly as the price catalogue
// spells model_prices (internal/pricing/overrides.go's wireRates, and
// internal/localcfg.ModelPrice): input_per_m and output_per_m are required,
// and the optional rates are pointers so that a field left out keeps the list
// default (cache_write_5m 1.25, cache_write_1h 2, cache_read 0.1,
// web_search_per_1k 10), never 0. Decode checks the block with the catalogue's
// own pricing.ParseOverrides as well, so the two spellings cannot drift
// (TestDecodeModelPricesAgreeWithTheCatalog).
type Price struct {
	InputPerM      *float64   `json:"input_per_m"`
	OutputPerM     *float64   `json:"output_per_m"`
	CacheWrite5m   *float64   `json:"cache_write_5m,omitempty"`
	CacheWrite1h   *float64   `json:"cache_write_1h,omitempty"`
	CacheRead      *float64   `json:"cache_read,omitempty"`
	WebSearchPer1k *float64   `json:"web_search_per_1k,omitempty"`
	LongContext    *PriceTier `json:"long_context,omitempty"`
}

// PriceTier is a long-context price that replaces the base rates for the whole
// call once its input passes a threshold.
type PriceTier struct {
	AboveInputTokens int64    `json:"above_input_tokens"`
	InputPerM        *float64 `json:"input_per_m"`
	OutputPerM       *float64 `json:"output_per_m"`
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

// MaxPolicyBytes bounds the policy node Decode accepts. The node is policy
// text — rules, patterns, prices and names — read by every run, not a data
// store, so an oversized node is refused rather than decoded (fail closed: the
// runner must not read a node it cannot trust itself to have read whole).
const MaxPolicyBytes = 64 << 10

// Decode reads the shared node. It returns (p, found, err):
//
//   - found=false, err=nil: the node is absent or empty, which is today's
//     behaviour (the runner keeps the policy from the job env);
//   - p != nil, err=nil: a policy this code understands;
//   - found=true, err!=nil: the node exists and cannot be used, which the
//     runner must treat as a halt, never as an empty ceiling (§5(3)).
//
// Unknown JSON keys are ignored; version is not (see VersionError), and the
// budget block is strict (see the package comment).
func Decode(raw []byte) (*Policy, bool, error) {
	if isNull(raw) {
		return nil, false, nil
	}
	if len(raw) > MaxPolicyBytes {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is %d bytes; at most %d are allowed", len(raw), MaxPolicyBytes)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is not a JSON object: %w", err)
	}

	var p Policy
	for k := range doc {
		if !knownTopLevelKeys[k] {
			p.UnknownKeys = append(p.UnknownKeys, k)
		}
	}
	sort.Strings(p.UnknownKeys)

	vraw, ok := doc["version"]
	if !ok {
		return nil, true, errors.New("projectpolicy: the policy has no version")
	}
	if err := json.Unmarshal(vraw, &p.Version); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy version is not an integer: %w", err)
	}
	if p.Version != Version {
		return nil, true, &VersionError{Found: p.Version, Understood: Version}
	}

	if braw, ok := doc["budget"]; ok && !isNull(braw) {
		dec := json.NewDecoder(bytes.NewReader(braw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p.Budget); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget: %w", err)
		}
		if err := validBudget(p.Budget); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget: %w", err)
		}
	}

	if araw, ok := doc["allow_repos"]; ok && !isNull(araw) {
		if err := json.Unmarshal(araw, &p.AllowRepos); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: allow_repos: %w", err)
		}
		for i, pattern := range p.AllowRepos {
			if err := ValidPattern(pattern); err != nil {
				return nil, true, fmt.Errorf("projectpolicy: allow_repos[%d]: %w", i, err)
			}
		}
	}

	if rraw, ok := doc["model_prices"]; ok && !isNull(rraw) {
		dec := json.NewDecoder(bytes.NewReader(rraw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p.Prices); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: model_prices: %w", err)
		}
		for _, id := range sortedKeys(p.Prices) {
			price := p.Prices[id]
			if price.InputPerM == nil || price.OutputPerM == nil {
				return nil, true, fmt.Errorf("projectpolicy: model_prices: %q: input_per_m and output_per_m are required", id)
			}
			if t := price.LongContext; t != nil && (t.InputPerM == nil || t.OutputPerM == nil) {
				return nil, true, fmt.Errorf("projectpolicy: model_prices: %q: long_context needs input_per_m and output_per_m", id)
			}
		}
		// The catalogue's own parser is the authority on this document's
		// spelling, its required fields and its ranges: the strict decode
		// above and this call must agree.
		if _, err := pricing.ParseOverrides(string(rraw)); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: model_prices: %w", err)
		}
	}

	if rraw, ok := doc["routing"]; ok && !isNull(rraw) {
		if err := json.Unmarshal(rraw, &p.Routing); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: routing: %w", err)
		}
		if err := validRouting(p.Routing); err != nil {
			return nil, true, err
		}
	}
	if praw, ok := doc["providers"]; ok && !isNull(praw) {
		if err := json.Unmarshal(praw, &p.Providers); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: providers: %w", err)
		}
		if err := validProviders(praw, p.Providers); err != nil {
			return nil, true, err
		}
	}
	return &p, true, nil
}

// validProviders holds the providers block to the rules internal/config holds
// the same document to in fugaro.yaml (ValidateModelProviders), plus the two
// rules that only a reader of the written document can apply: an unknown key
// inside an entry, and the null spelling of allow_data_to.
//
// An unknown key inside an entry is refused, and it used to be ignored, on the
// argument that a misspelling is harmless because "the value it would have set
// stays zero and every one of these fields is checked below". That argument was
// false for the three fields whose zero is not a refusal: a misspelt
// allow_data_to leaves the nil slice that ModelProvider.AllowsData reads as
// every repository, a misspelt route_fee_pct charges no fee, and a misspelt
// price_policy silently prices at the catalogue's cheapest end. The same
// document in fugaro.yaml is refused outright (config's own decode is strict,
// and runner.ProvidersFromEnv's is DisallowUnknownFields), so tolerating it
// here made the readable node the one carrier where a typo flips a
// security-relevant default — the two carriers disagreeing in the unsafe
// direction. Forward compatibility is not lost with it: P8's mechanism is the
// version integer (the runner refuses a version it does not know), not a
// silently ignored field, and a newer CLI that adds a provider field publishes
// a newer version with it.
//
// The keys an entry may carry are read off config.ModelProvider's own JSON
// tags (providerEntryKeys), never respelled, for the same reason the type is
// embedded rather than copied.
//
// The second rule is the null spelling of allow_data_to:
// ValidateModelProviders reads "the key was written with nothing under it" off
// the YAML node, and JSON has no such marker, so a written null arrives as the
// nil slice that means every repository (ModelProvider.AllowsData). That is the
// same ambiguity the YAML rule refuses — "shared with nobody" and "not decided
// yet" would both be published as "every repository" — so it is refused here,
// from the raw document.
func validProviders(raw json.RawMessage, providers map[string]Provider) error {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("projectpolicy: providers: %w", err)
	}
	carrier := make(map[string]config.ModelProvider, len(providers))
	for _, name := range providerNames(providers) {
		p := providers[name]
		if entry := entries[name]; entry != nil {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(entry, &fields); err != nil {
				return fmt.Errorf("projectpolicy: providers.%s: %w", name, err)
			}
			if unknown := unknownProviderKeys(fields); len(unknown) > 0 {
				return fmt.Errorf("projectpolicy: providers.%s: unknown %s: an entry carries %s. A misspelt key leaves the field it names unset, and for %s that is not the safe value, so it is refused here rather than published; the same entry in fugaro.yaml is refused the same way",
					name, quoteKeys(unknown), strings.Join(providerEntryKeyNames(), ", "), strings.Join(typoUnsafeKeys, ", "))
			}
			if data, ok := fields["allow_data_to"]; ok && isNull(data) {
				return fmt.Errorf("projectpolicy: providers.%s.allow_data_to: an explicit empty list is ambiguous: write [\"*\"] for every repository, or remove the key entirely", name)
			}
		}
		carrier[name] = p.ModelProvider
	}
	// config's own validation is the authority on this document, so a value it
	// refuses in fugaro.yaml is refused on the readable node too: the node is
	// what a run is routed and metered by.
	if ps := config.ValidateModelProviders(carrier); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, problem := range ps {
			msgs[i] = problem.String()
		}
		return fmt.Errorf("projectpolicy: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// The values routing.claude.via accepts (D1a).
const (
	// RouteDirect calls Anthropic directly: what a policy without a routing
	// block means (D1b).
	RouteDirect = "direct"
	// RouteOpenRouter routes Claude calls through an openrouter-compatible
	// provider entry.
	RouteOpenRouter = "openrouter"
	// RouteAuto routes by the model list the route carries.
	RouteAuto = "auto"
)

// validRouting checks the routing block's values. Phase 1b is what reads it,
// but a via this Fugaro does not know must be refused now rather than
// decoded: routing is enforced, and an unrecognised value would later mean
// either "no route" (a silent bypass of what the owner asked for) or a
// reader that guesses. The block carries no other value worth checking
// today; models are Claude IDs, which phase 1b validates against its own
// list.
func validRouting(r *Routing) error {
	if r == nil || r.Claude == nil {
		return nil
	}
	via := r.Claude.Via
	switch via {
	case RouteDirect, RouteOpenRouter, RouteAuto:
		return nil
	}
	return fmt.Errorf("projectpolicy: routing.claude.via %q must be %s, %s or %s", via, RouteDirect, RouteOpenRouter, RouteAuto)
}

// typoUnsafeKeys are the provider keys whose zero value, left in place by a
// misspelt key, is not a refusal: allow_data_to's nil slice is "every
// repository" (ModelProvider.AllowsData), route_fee_pct's 0 is "no fee on this
// route", and price_policy's "" is the cheapest end of the catalogue's range.
// They are named in the refusal unknownProviderKeys words, because they are the
// reason an unknown key is an error here rather than a tolerated field.
var typoUnsafeKeys = []string{"allow_data_to", "route_fee_pct", "price_policy"}

// providerEntryKeys is the set of keys a providers entry may carry: exactly the
// JSON names of config.ModelProvider's own fields. It is read off the type,
// never respelled, so a field added to the carrier is known here the moment it
// exists and the two cannot drift apart.
var providerEntryKeys = providerKeysOf(reflect.TypeOf(config.ModelProvider{}))

// providerKeysOf collects the JSON names of a struct's fields.
func providerKeysOf(t reflect.Type) map[string]bool {
	keys := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

// providerEntryKeyNames is the known keys, sorted, for the refusal to quote.
func providerEntryKeyNames() []string {
	names := make([]string, 0, len(providerEntryKeys))
	for name := range providerEntryKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unknownProviderKeys is the keys of one entries[name] object that
// config.ModelProvider does not declare, sorted: the misspellings and the
// fields of a Fugaro this one is not.
func unknownProviderKeys(fields map[string]json.RawMessage) []string {
	var unknown []string
	for key := range fields {
		if !providerEntryKeys[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// quoteKeys renders keys as a comma-separated list of quoted names.
func quoteKeys(keys []string) string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = strconv.Quote(key)
	}
	return strings.Join(out, ", ")
}

// validBudget mirrors internal/config's checks of fugaro.yaml's budget: block
// (validateBudget) for the four keys the policy carries — every value and, for
// allowed_models, every entry (CheckModelID, DuplicateModel) — because these
// values are enforced, not advisory. An unrecognised mode is the dangerous one:
// policy.Merge treats it as unset, and the run falls back to off.
func validBudget(b Budget) error {
	if b.Mode == "" {
		// The block is there and names no mode: a node published for its caps
		// alone would take the mode from the job environment, which nobody
		// reading the policy can see, so enforcement would depend on a deploy
		// (H1). Leaving the block out is how a policy says it sets no budget.
		return errors.New("mode must be off, observe or enforce: a policy that sets no budget key leaves the budget block out")
	}
	if !policy.ValidMode(b.Mode) {
		return fmt.Errorf("mode %q must be off, observe or enforce", b.Mode)
	}
	if m, err := pricing.FromUSD(b.PerRunUSD); err != nil {
		return fmt.Errorf("per_run_usd %v: it must be a number from 0 to %d US dollars", b.PerRunUSD, pricing.MaxUSD)
	} else if b.PerRunUSD > 0 && m < 1 {
		return fmt.Errorf("per_run_usd %v: it rounds to nothing; the smallest cap is $0.000001", b.PerRunUSD)
	}
	if b.MaxRunTokens < 0 {
		return fmt.Errorf("max_run_tokens %d must not be negative", b.MaxRunTokens)
	}
	if b.AllowedModels != nil && len(b.AllowedModels) == 0 {
		// An omitted key keeps the allow-list the job's environment was
		// deployed with: the node replaces the ceiling key by key, so leaving
		// the key out is how a policy says "the deployed list stands", not
		// "no model is restricted" (H1, L-a).
		return errors.New("allowed_models must list at least one model; leave it out to keep the allow-list the job's environment carries")
	}
	for i, m := range b.AllowedModels {
		if msg := config.CheckModelID(m); msg != "" {
			return fmt.Errorf("allowed_models[%d]: %s", i, msg)
		}
	}
	if i, dup := config.DuplicateModel(b.AllowedModels); dup {
		return fmt.Errorf("allowed_models[%d]: %s is listed twice; list each model once", i, b.AllowedModels[i])
	}
	return nil
}

// isNull reports whether a value is absent: an empty body, or the JSON null
// RTDB reads a missing node as.
func isNull(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// providerNames is a stable order for error messages (a map's range is random).
func providerNames(m map[string]Provider) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedKeys is a stable order for error messages (a map's range is random).
func sortedKeys(m map[string]Price) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
