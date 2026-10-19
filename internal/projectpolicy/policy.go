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
//     the exact failure this package promises to prevent. Its member names are
//     held to the documented spelling too (checkMemberNames, validBudget):
//     encoding/json folds case, so a strict decode alone would read
//     {"mode": "enforce", "MODE": "off"} as whichever of the two the document
//     wrote last;
//   - model_prices is read with the catalogue's own parser and keeps the
//     catalogue's pointer spelling, so an omitted optional rate keeps its list
//     default instead of being priced at zero; its member names are escaped on
//     the node (an RTDB key cannot hold the "/" or "." of a model ID) and
//     unescaped when the node is read, see Prices — and the names *inside* an
//     entry are held to the documented spelling as the budget block's are, for
//     the same reason;
//   - a providers entry is the document internal/runner's ProvidersFromEnv
//     decodes out of FUGARO_MODEL_PROVIDERS (Provider embeds
//     config.ModelProvider, so the spelling cannot drift) and is checked by
//     internal/config's own ValidateModelProviders, so a base URL that is not
//     https, a kind that is not anthropic-compat, an auth style that is not
//     bearer or x-api-key, an ambiguous empty allow_data_to, a route_fee_pct
//     over the cap or a models list two providers both claim is refused here
//     rather than reaching a run; price_policy is checked against the
//     catalogue's two values beside them, and again by validPricePolicy below,
//     because the node is read by a runner that never parses a config file.
//     An unknown key *inside* an entry is refused, exactly as
//     internal/config's own decoder refuses one in fugaro.yaml, and so is a
//     member name that differs from the documented one only in case
//     (validProviders): a misspelt allow_data_to would otherwise leave the
//     widest setting there is (a nil list means every repository) and a
//     misspelt route_fee_pct would drop the owner's surcharge, silently, on a
//     node an administrator can write by hand — and encoding/json's own
//     matching, which folds case, is one way to misspell a name that the
//     strictness above does not catch by itself. The tolerance that stays is at
//     the top level, where every unknown key is listed in Policy.UnknownKeys:
//     each one is a section of its own, so tolerating it leaves the rest of the
//     policy in force. Inside `routing` the tolerance is the same only while
//     the block still names a switch: an unknown member beside a written
//     `claude` is ignored as an unknown top-level key is, and Decode lists no
//     key of the block — which is why the node is edited as a document rather
//     than as a value of this Fugaro's type (internal/cli's patchPolicyJSON) —
//     but a block that names no `claude` switch at all is refused
//     (validRoutingMembers), because the block has exactly one member that does
//     anything: a misspelt member, or a vendor this release does not route,
//     would otherwise decode to a nil switch and leave every Claude call on the
//     direct route while the node says otherwise. The switch's own values are
//     held to the rules the operator's config loader applies
//     (config.CheckRouting, validRouting), and the member names encoding/json
//     reads them by are held to their spelling (CheckRoutingMemberNames),
//     because a name those checks do not read is not a value anything refuses —
//     it is a value nothing looked at. routing is decoded and validated here
//     and applied by the run that reads it: internal/runner's applyRouting
//     rewrites the run's Claude pins from the switch the block names (Task
//     1b.2), so what the tolerance above costs is a member nothing acts on
//     rather than a switch that does nothing — and these checks are what
//     protect that reader, because nothing acts on the block before they have
//     judged it.
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
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/repourl"
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
	Prices     Prices              `json:"model_prices,omitempty"` // the owner's price overrides, keyed by model ID (Prices documents the node's own spelling)
	Routing    *Routing            `json:"routing,omitempty"`      // the run applies it (internal/runner's applyRouting); nil = direct (D1b)

	// Profiles are the project's job profiles (design project-centric §4.1,
	// task 3.1), keyed by profile name. A repository's workflow selects one by
	// name (config.Workflow.JobProfile, default DefaultProfileName) and the
	// runner refuses the run when the policy does not define it (§5(3), fail
	// closed), so this block is a closed set a workflow may only name, never
	// extend. In this release the selection is checked by name only; the
	// profile's cpu, memory, timeout, secrets and allow_repos are validated
	// here and applied by the job layout and admission check of later phase 3
	// tasks (3.2, 3.3), which build the job a run is launched as from it.
	//
	// An absent block is a phase-1 node: it declares no profile, and a workflow
	// that selects one is then refused at run start rather than run on
	// something else. validProfiles holds the block to the rule that makes that
	// safe — a block that is present must define the default.
	Profiles map[string]Profile `json:"profiles,omitempty"`

	// Accounts are the project's Claude accounts, keyed by account name (D3,
	// design project-named-oauth-tokens §4.1): which repositories each
	// account's token may be used by. A profile lists the accounts it serves
	// (config.JobProfile.ClaudeAccounts) and one job is deployed per (profile,
	// provider, account), so this block is what bounds the token a run of an
	// account's job holds — the profile is shared by every repository admitted
	// to it, and the account's list is the per-repository decision.
	//
	// It is derived, never hand-written: publish computes it from the
	// project's local config (DeriveAccounts), and `fugaro init` validates the
	// profiles it deploys against the same block.
	Accounts map[string]Account `json:"accounts,omitempty"`

	// CloneURLs are the project's clone URLs, keyed by canonical repository
	// (Canonical: "github/owner/name", the spelling allow_repos patterns and
	// the runner's slug already use). A repository that is not listed here
	// clones from its provider's usual host, which is what every repository
	// does today; an entry exists for a repository reached another way (a
	// mirror, a proxy, an owner's own host).
	//
	// It is data, not an environment value, because on a project job the clone
	// URL cannot be one: FUGARO_REMOTE is launcher-writable and a shared job
	// sets none, so the runner computes the URL from task.json's repository,
	// the provider the job's name pins and this block (plan task 3.3 step 3,
	// design §4.1(1)). The URL is validated on the node against the same rules
	// the deployer applies (repourl.CloneURL), so an entry can never point a
	// run at another repository or another provider's host. The member names
	// are escaped for RTDB — a canonical name carries slashes — which is
	// CloneURLs' own business and not a reader's.
	CloneURLs CloneURLs `json:"clone_urls,omitempty"`

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
	"profiles": true, "clone_urls": true, "accounts": true,
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
// auth, secret, models, allow_data_to, route_fee_pct. A respelling here would
// be a second carrier for one document, and the two would drift — a kind of
// drift no test can see, because each side would be self-consistent.
//
// RequireZDR and PricePolicy are the two price-catalogue switches
// (docs/design/provider-price-catalog.md D3). They live on the carrier
// (config.ModelProvider, beside RouteFeePct) and are NOT respelled here: a
// second declaration of the same key would be shadowed rather than merged,
// and — because Provider embeds a carrier that has its own UnmarshalYAML —
// a decode that went through the carrier would fill one declaration and
// leave the other at zero, which is a switch silently dropped from a
// published policy. One carrier, one declaration: a field copied out of the
// operator's config by name (internal/cli's policyProviders) cannot lose
// either switch, and neither can a decode.
type Provider struct {
	config.ModelProvider
}

// The values price_policy accepts (docs/design/provider-price-catalog.md D3),
// spelled here as the policy spells them. They are config's own values, kept
// as constants of this package too so a reader of a policy need not import
// internal/config to name one.
const (
	// PricePolicyCheapest charges the cheapest eligible endpoint's price, and
	// is what an unset price_policy means (the catalogue's own default).
	PricePolicyCheapest = config.PricePolicyCheapest
	// PricePolicyHighest charges the dearest eligible endpoint's price.
	PricePolicyHighest = config.PricePolicyHighest
)

// RequireZDROn reports whether this route is priced as if only ZDR-eligible
// endpoints may serve it: true unless require_zdr is explicitly false. It is
// the carrier's own accessor (config.ModelProvider.RequireZDROn), named here
// as a policy reader reads it.
func (p Provider) RequireZDROn() bool { return p.ModelProvider.RequireZDROn() }

// EffectivePricePolicy is the policy a route is priced under:
// PricePolicyCheapest when the policy does not set one (D3). It is the
// carrier's own accessor (config.ModelProvider.PricePolicySet), under the
// name this package's readers use.
func (p Provider) EffectivePricePolicy() string { return p.ModelProvider.PricePolicySet() }

// Price is one model's owner-set rates, spelled exactly as the price catalogue
// spells model_prices (internal/pricing/overrides.go's wireRates, and
// internal/localcfg.ModelPrice): input_per_m and output_per_m are required,
// and the optional rates are pointers so that a field left out keeps the list
// default (cache_write_5m 1.25, cache_write_1h 2, cache_read 0.1,
// web_search_per_1k 10), never 0. Decode checks the block with the catalogue's
// own pricing.ParseOverrides as well, so the two spellings cannot drift
// (TestDecodeModelPricesAgreeWithTheCatalog). The model ID it belongs to is
// the map key of Prices, which escapes it on the node and not in Go.
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

// Routing is phase 1b's routing layer (D1a: an object, not a scalar), and this
// release applies it: internal/runner's applyRouting rewrites the run's Claude
// pins from the switch before any consumer reads them, so the block decides
// where a routed call goes and what it costs. nil is today's behaviour, direct
// (D1b).
//
// It is config.Routing, aliased rather than respelled, for the reason Provider
// embeds config.ModelProvider: one document, one declaration. The switch is
// written in the operator's config, validated there against the providers it
// resolves through (config.CheckRouting), and only *delivered* here — so a
// second declaration of the same object would be a second spelling of it, and
// the reader would then hold it to a rule the writer never applied.
type Routing = config.Routing

// ClaudeRoute is the Claude routing switch: an explicit list of Claude IDs,
// never a pattern (D1a). config.RoutingClaude, by the alias above.
type ClaudeRoute = config.RoutingClaude

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
	if IsNull(raw) {
		return nil, false, nil
	}
	if len(raw) > MaxPolicyBytes {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is %d bytes; at most %d are allowed", len(raw), MaxPolicyBytes)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is not a JSON object: %w", err)
	}
	// The names the document writes, duplicates included, before the map above
	// has folded them away: a key written twice is read as the last of the two
	// and this package looks every top-level key up by its exact spelling, so
	// {"version":1,"budget":{...},"budget":null} is a node that carries no
	// budget at all — the run would keep the ceiling its job environment was
	// deployed with while the operator's node appears to set one.
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the policy node is not a JSON object: %w", err)
	}
	if err := checkTopLevelNames(names); err != nil {
		return nil, true, err
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

	if braw, ok := doc["budget"]; ok && !IsNull(braw) {
		dec := json.NewDecoder(bytes.NewReader(braw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p.Budget); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget: %w", err)
		}
		if err := validBudget(braw, p.Budget); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: budget: %w", err)
		}
	}

	if araw, ok := doc["allow_repos"]; ok && !IsNull(araw) {
		if err := json.Unmarshal(araw, &p.AllowRepos); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: allow_repos: %w", err)
		}
		for i, pattern := range p.AllowRepos {
			if err := ValidPattern(pattern); err != nil {
				return nil, true, fmt.Errorf("projectpolicy: allow_repos[%d]: %w", i, err)
			}
		}
	}

	if rraw, ok := doc["model_prices"]; ok && !IsNull(rraw) {
		// Prices' own UnmarshalJSON does the strict part: each entry is
		// decoded with DisallowUnknownFields, and each member name is
		// unescaped back into the model ID the block is keyed by.
		if err := json.Unmarshal(rraw, &p.Prices); err != nil {
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
		// above and this call must agree. It reads the block in the
		// catalogue's own spelling, which is the decoded map's — never the
		// node's escaped member names, which are not model IDs.
		catalog, err := p.Prices.CatalogJSON()
		if err != nil {
			return nil, true, fmt.Errorf("projectpolicy: model_prices: %w", err)
		}
		if _, err := pricing.ParseOverrides(string(catalog)); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: model_prices: %w", err)
		}
	}

	if praw, ok := doc["providers"]; ok && !IsNull(praw) {
		ps, err := decodeProviders(praw)
		if err != nil {
			return nil, true, fmt.Errorf("projectpolicy: providers: %w", err)
		}
		p.Providers = ps
		if err := validProviders(praw, p.Providers); err != nil {
			return nil, true, err
		}
	}
	// The accounts block is judged *before* the profiles block, and the
	// profiles block against it: the subset rule is a property of the two
	// together, and the profiles are the side that may not widen what an
	// account covers. Decoding them in the other order would let a profile
	// name an account whose entry has not been read yet.
	if craw, ok := doc["accounts"]; ok && !IsNull(craw) {
		// decodeAccountBlock's errors name their place themselves
		// ("projectpolicy: accounts.<name>..."), so they are returned as they are.
		as, err := decodeAccountBlock(craw)
		if err != nil {
			return nil, true, err
		}
		p.Accounts = as
		if err := validAccounts(p.Accounts); err != nil {
			return nil, true, err
		}
	}
	if fraw, ok := doc["profiles"]; ok && !IsNull(fraw) {
		// decodeProfileBlock's errors name their place themselves
		// ("projectpolicy: profiles.<name>..."), so they are returned as they are.
		ps, err := decodeProfileBlock(fraw)
		if err != nil {
			return nil, true, err
		}
		p.Profiles = ps
		if err := validProfiles(p.Profiles, p.Accounts); err != nil {
			return nil, true, err
		}
	}
	if craw, ok := doc["clone_urls"]; ok && !IsNull(craw) {
		if err := json.Unmarshal(craw, &p.CloneURLs); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: clone_urls: %w", err)
		}
		if err := validCloneURLs(p.CloneURLs); err != nil {
			return nil, true, err
		}
	}
	// Routing is judged *after* the providers block, and against it: D1b's
	// safeguard (a route resolves to a provider only when that provider's
	// allow_data_to is written) is a property of the two blocks together, and
	// the node is the document a run is routed by. Decoding them in the other
	// order would hold the switch to its shape alone and accept a node that
	// names a route nothing in it claims.
	if rraw, ok := doc["routing"]; ok && !IsNull(rraw) {
		if err := json.Unmarshal(rraw, &p.Routing); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: routing: %w", err)
		}
		if err := validRoutingMembers(rraw, p.Routing); err != nil {
			return nil, true, err
		}
		if err := validRouting(p.Routing, p.Providers); err != nil {
			return nil, true, err
		}
	}
	return &p, true, nil
}

// validCloneURLs checks the clone_urls block, entry by entry: each key a
// canonical repository name, and each value an https URL without credentials
// that names exactly that repository on that repository's own provider's host
// — the rules the deployer applies to a repository URL (repourl.CloneURL, the
// one function internal/infra's repoURL and the runner share).
//
// Both halves are the security property. A key that is not canonical names no
// repository the runner can look up, so the entry would silently never be
// used; a value naming another repository, or another provider's host, would
// point a run at a repository other than the one its slug, its locks, its
// cache and its budget session name — the very thing the run's own remote
// checks refuse when a launcher sets FUGARO_REMOTE, and the reason the node
// is checked here rather than trusted.
func validCloneURLs(urls CloneURLs) error {
	for _, key := range sortedStringKeys(urls) {
		at := fmt.Sprintf("projectpolicy: clone_urls[%s]", key)
		provider, repo, ok := strings.Cut(key, "/")
		if !ok || provider == "" || repo == "" {
			return fmt.Errorf("%s: a clone_urls key is a canonical repository name, like github/owner/name", at)
		}
		if c, err := Canonical(provider, repo); err != nil || c != key {
			return fmt.Errorf("%s: a clone_urls key is a canonical repository name, like github/owner/name", at)
		}
		if _, err := repourl.CloneURL(provider, repo, urls[key]); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
	}
	return nil
}

// CloneURL is the clone URL the project gives a canonical repository, and ""
// when the policy names none — the provider's usual host, which is what the
// runner's own helper then builds. It is a plain lookup and not a decision:
// whether a repository may run at all is Admits'.
func (p *Policy) CloneURL(canonical string) string { return p.CloneURLs[canonical] }

// sortedStringKeys is a stable order for error messages, like sortedKeys.
func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// decodeProfileBlock reads the profiles block off the node, refusing a key inside
// an entry this Fugaro does not know. A profile is what a job is deployed as —
// its cpu, memory, task timeout, provider and the secrets it mounts — so a
// misspelt key is not a dropped field: a misspelt timeout_class would deploy a
// job with no timeout, a misspelt allow_repos would deploy it with the
// project's wider list, and a misspelt secrets entry would mount nothing where
// the owner listed a secret. The node tolerates unknown *top-level* keys so a
// newer Fugaro's field does not brick an older runner (P8); inside this block
// the same tolerance fails open, and a newer profile field bumps Version,
// which is what refuses a node this binary cannot read.
//
// A name written twice is refused (checkNoDuplicateNames, the same rule
// validProviders applies to the providers block): profiles are looked up by
// their exact spelling — Policy.ProfileFor, and every reader of a selection
// through it — so encoding/json keeping the last of the two silently drops the
// first profile whole, and which profile a run lands on would depend on the
// order the keys were written in.
func decodeProfileBlock(raw json.RawMessage) (map[string]Profile, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: profiles: %w", err)
	}
	if err := checkNoDuplicateNames("profiles", names); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("projectpolicy: profiles: %w", err)
	}
	out := make(map[string]Profile, len(entries))
	for _, name := range sortedProfileNames(entries) {
		dec := json.NewDecoder(bytes.NewReader(entries[name]))
		dec.DisallowUnknownFields()
		var pr Profile
		if err := dec.Decode(&pr); err != nil {
			return nil, fmt.Errorf("projectpolicy: profiles.%s: %w", name, err)
		}
		if err := checkWrittenProfileFields(name, entries[name]); err != nil {
			return nil, err
		}
		out[name] = pr
	}
	return out, nil
}

// decodeProviders reads the providers block off the node, refusing a key inside
// an entry that this Fugaro does not know. internal/config's decoder refuses one
// in fugaro.yaml (ModelProvider.UnmarshalYAML, a known-fields check yaml.v3
// cannot apply while decoding through a Node), and the node is the more
// security-relevant of the two documents: it is what a run is routed and metered
// by, and every run token can read it.
//
// The tolerance an unknown key used to get here was justified by P8 (a field a
// newer release adds must not brick an older runner), but it does not hold for
// this block: two of its fields fail OPEN when a key that names them is
// misspelt. A misspelt allow_data_to leaves the nil slice that means every
// repository — the widest data-sharing setting there is, and the value
// ValidateModelProviders' ambiguity check is skipped for, because the check that
// marks a written-but-empty list is set by the YAML unmarshaller and never by
// JSON — and a misspelt route_fee_pct drops the owner's surcharge silently. A
// newer release that adds a provider field bumps Version, which is what refuses
// a node this binary cannot read, so nothing forward-compatible is lost.
func decodeProviders(raw json.RawMessage) (map[string]Provider, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := make(map[string]Provider, len(entries))
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dec := json.NewDecoder(bytes.NewReader(entries[name]))
		dec.DisallowUnknownFields()
		var pv Provider
		if err := dec.Decode(&pv); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = pv
	}
	return out, nil
}

// checkWrittenProfileFields applies the profile rules that read the written
// document rather than the decoded value: a member written twice, the spelling
// of a profile's members, and an explicitly empty allow_repos.
//
// A profile that names no list falls back to the project's allow_repos
// (§4.1), and the carrier's `omitempty` means the node cannot carry an empty
// list at all — so `"allow_repos": []` and `"allow_repos": null` would both be
// read as "the project's list applies". That is the ambiguity
// providers.allow_data_to is refused for, in the wider direction: an operator
// who wrote "[]" believing it admits nobody would get every repository the
// project enrolls, with nothing in the node to show the difference. Leaving
// the key out is how a profile says the project's list applies.
//
// The member spelling is checked first, and it is what makes the second check
// hold: encoding/json matches a member to its field case-insensitively, so
// `"Allow_Repos": []` decodes into AllowRepos while the `fields["allow_repos"]`
// lookup below — which reads the name as written — never sees it, and the
// empty list becomes the nil slice that means the project's list. It is the
// same guard, and the same reason, as validProviders' member check: a name
// that differs only in case is read by the decoder and by nothing else here.
func checkWrittenProfileFields(name string, raw json.RawMessage) error {
	// A member written twice is read off the document as written, before the map
	// below folds the repeat away: encoding/json keeps the last one it reads, so
	// `"cpu": 2, "cpu": 4` is 4 and `"allow_repos": ["github/acme/*"]` followed by
	// `["github/*/*"]` admits every repository, either way with nothing left in
	// the decoded value to refuse. Every other carrier of this document refuses a
	// repeat the same way (checkNoDuplicateNames).
	names, err := objectMemberNames(raw)
	if err != nil {
		return fmt.Errorf("projectpolicy: profiles.%s: %w", name, err)
	}
	if err := checkNoDuplicateNames("profiles."+name, names); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("projectpolicy: profiles.%s: %w", name, err)
	}
	for _, member := range memberNames(fields) {
		known := spelledMember(profileMembers, member)
		if known == member {
			continue
		}
		if known == "" {
			// Unreachable through Decode: decodeProfileBlock decodes the entry
			// with DisallowUnknownFields first, so a name no member answers to
			// is refused there. Kept so this function holds on its own too.
			return fmt.Errorf("projectpolicy: profiles.%s: unknown field %q", name, member)
		}
		// The consequence is named only where it is that member's: an
		// "Allow_Repos" the check never saw is the silent fall-back to the
		// project's list, and any other member is the same silence without
		// that particular effect. A refusal that promised the fall-back for
		// "Provider" would be describing something that cannot happen.
		why := "so the decoder would read the entry this check never saw"
		if known == "allow_repos" {
			why = "so an \"" + member + "\": [] would fall back to the project's allow_repos instead of being refused"
		}
		return fmt.Errorf("projectpolicy: profiles.%s.%s: a profile's members are spelled as fugaro.yaml spells them — write %s. A name that differs only in case is read by the decoder and not by this document's own checks, %s", name, member, known, why)
	}
	v, ok := fields["allow_repos"]
	if !ok {
		return nil
	}
	var patterns []string
	empty := IsNull(v)
	if err := json.Unmarshal(v, &patterns); err == nil && len(patterns) == 0 {
		empty = true
	}
	if empty {
		return fmt.Errorf("projectpolicy: profiles.%s.allow_repos: an explicit empty list is ambiguous: remove the key to fall back to the project's allow_repos, or write the repositories this profile admits (the project's allow_repos is still required, so a profile's list can only narrow it)", name)
	}
	return nil
}

// carrierMembers are the member names an entry of that carrier is spelled
// with, read from the struct's own json tags so a field internal/config adds is
// picked up without a second list. It is the json twin of internal/config's
// providerKeys, which holds a fugaro.yaml entry to the same names; a field with
// a tag is known by its tag alone, exactly as encoding/json matches it. An
// embedded struct contributes its own members, which is how a policy's Profile
// is spelled with config.JobProfile's fields.
func carrierMembers(t reflect.Type) []string {
	var out []string
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := range t.NumField() {
			f := t.Field(i)
			if f.Anonymous {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			switch {
			case tag == "-":
			case tag != "":
				out = append(out, tag)
			default:
				out = append(out, f.Name)
			}
		}
	}
	walk(t)
	sort.Strings(out)
	return out
}

// providerMembers, profileMembers are the two carriers whose member spelling
// this package reads off the raw document. One guard serves both: a second
// spelling rule for the profiles block would be a second thing to keep in step
// with the first.
var (
	providerMembers = carrierMembers(reflect.TypeOf(Provider{}))
	profileMembers  = carrierMembers(reflect.TypeOf(Profile{}))
)

// memberNames is a stable order for error messages (a map's range is random).
func memberNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// spelledMember is the member of that carrier name is a spelling of: name
// itself when it is spelled as this Fugaro spells it, the documented spelling
// when it differs only in case, and "" when no member is known by that name at
// all.
func spelledMember(members []string, name string) string {
	for _, known := range members {
		if known == name {
			return name
		}
	}
	for _, known := range members {
		if strings.EqualFold(known, name) {
			return known
		}
	}
	return ""
}

// validProviders holds the providers block to the rules internal/config holds
// the same document to in fugaro.yaml (ValidateModelProviders), plus the one
// rule that only a reader of the written document can apply.
//
// The one rule ValidateModelProviders cannot apply here is the null spelling of
// allow_data_to: it reads "the key was written with nothing under it" off the
// YAML node, and JSON has no such marker, so a written null arrives as the nil
// slice that means every repository (ModelProvider.AllowsData). That is the
// same ambiguity the YAML rule refuses — "shared with nobody" and "not decided
// yet" would both be published as "every repository" — so it is refused here,
// from the raw document.
func validProviders(raw json.RawMessage, providers map[string]Provider) error {
	// A provider named twice is one provider to this reader (the map keeps the
	// last entry), and the entry it keeps is the whole of what that provider
	// allows: a second `openrouter` written beside a first replaces its
	// allow_data_to, its secret and its fee, so a node can widen an allow-list
	// by repeating the block.
	blockNames, err := objectMemberNames(raw)
	if err != nil {
		return fmt.Errorf("projectpolicy: providers: %w", err)
	}
	if err := checkNoDuplicateNames("providers", blockNames); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("projectpolicy: providers: %w", err)
	}
	carrier := providerCarrier(providers)
	for _, name := range providerNames(providers) {
		p := providers[name]
		if err := validPricePolicy(name, p.PricePolicy); err != nil {
			return err
		}
		if entry := entries[name]; entry != nil {
			// The entry's own names, duplicates included, before the map below
			// folds them away: encoding/json keeps the last of a repeated
			// member, and the two members that fail open are exactly the ones a
			// repeat can widen — a second allow_data_to is the allow-list this
			// reader ends up with, and a second route_fee_pct is the fee.
			members, err := objectMemberNames(entry)
			if err != nil {
				return fmt.Errorf("projectpolicy: providers.%s: %w", name, err)
			}
			if err := checkNoDuplicateNames("providers."+name, members); err != nil {
				return fmt.Errorf("projectpolicy: %w", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(entry, &fields); err == nil {
				for _, member := range memberNames(fields) {
					known := spelledMember(providerMembers, member)
					if known == member {
						continue
					}
					if known == "" {
						// Unreachable through Decode: decodeProviders decodes
						// the entry with DisallowUnknownFields first, so a name
						// no member answers to is refused there. Kept so this
						// function holds on its own too.
						return fmt.Errorf("projectpolicy: providers.%s: unknown field %q", name, member)
					}
					// A member name has to be the documented one, because the
					// checks here read it off the raw document: encoding/json
					// matches a member to its field case-insensitively, so
					// "Allow_Data_To": null decoded into the field the tag
					// names while the null check just below — which looks the
					// name up as written — never saw it, and the nil slice a
					// null leaves behind is "every repository". fugaro.yaml
					// refuses the same spelling (ModelProvider.UnmarshalYAML
					// compares names exactly), and the node is held to the
					// config's rules.
					return fmt.Errorf("projectpolicy: providers.%s.%s: a provider entry's members are spelled as fugaro.yaml spells them — write %s. A name that differs only in case is read by the decoder and not by this document's own checks, and the null spelling of allow_data_to would publish as every repository", name, member, known)
				}
				if data, ok := fields["allow_data_to"]; ok && IsNull(data) {
					return fmt.Errorf("projectpolicy: providers.%s.allow_data_to: an explicit empty list is ambiguous: write [\"*\"] for every repository, or remove the key entirely", name)
				}
				// route_fee_pct is the other member JSON spells a written key
				// with no value under: it is a float64, and a null decodes into
				// it as 0, which is no surcharge at all. A node that means to
				// set a fee — or that a tool serialized an absent value into —
				// would price every request on this route without the owner's
				// surcharge, and nothing would say the value was not read.
				// (No other member is silent this way: a null kind, base_url,
				// auth, secret or models is refused by ValidateModelProviders
				// just below, and a null require_zdr or price_policy is the
				// default the key being absent gives.)
				if fee, ok := fields["route_fee_pct"]; ok && IsNull(fee) {
					return fmt.Errorf("projectpolicy: providers.%s.route_fee_pct: a written null is read as 0, which is no surcharge at all — write the percentage this route adds to every charge, or remove the key entirely", name)
				}
			}
		}
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

// The values routing.claude.via accepts (D1a). config's own values, aliased:
// the switch is one object, written in the operator's config and read here.
const (
	// RouteDirect calls Anthropic directly: what a policy without a routing
	// block means (D1b).
	RouteDirect = config.RouteDirect
	// RouteOpenRouter routes Claude calls through an openrouter-compatible
	// provider entry.
	RouteOpenRouter = config.RouteOpenRouter
	// RouteAuto routes by the model list the route carries.
	RouteAuto = config.RouteAuto
)

// routingMembers and routingClaudeMembers are the member names the routing
// block and its switch are documented with, the names CheckRoutingMemberNames
// holds a written block to. They are named here rather than written inline in
// that function so the lists are pinnable: checkMemberNames refuses a *case
// variant* of a name the list holds and cannot see one it does not, so a field
// added to the switch without the list is a spelling the decoder reads and
// this check does not (TestPolicyMemberListsCoverTheirStructs).
var (
	routingMembers       = []string{"claude"}
	routingClaudeMembers = []string{"via", "models"}
)

// CheckRoutingMemberNames refuses a routing block whose member names encoding/json
// does not read the way the document spells them: a name written twice, two names
// that fold to one under Unicode simple case folding, or a modelled name
// (`claude`, `via`, `models`) spelled in a folding of itself. It is exported
// because the CLI's publish writes into the same
// block and must not add an exact `claude` beside a `Claude` a hand-written node
// already carries: the two fold to one field, and which one wins is the order the
// keys were written in.
//
// The node is the one document a run is routed by, and it is hand-writable (an
// administrator edits it through the RTDB console or the CLI, not through a config
// loader that would have refused this). The values are checked by validRouting and
// config.CheckRouting; this is the check that the *names* are the ones those checks
// read, because a name they do not read is not a value they refuse — it is a value
// nothing looked at. `routing.claude` is where it matters most: `{"via": "direct",
// "VIA": "openrouter"}` and `{"via": "openrouter", "via": "direct"}` both decode to
// a switch whose route is decided by document order, and the diff, the runner and
// the operator's own reading would each see a different one.
//
// `routing: {}`, and a block whose members are all names this Fugaro does not
// model (a sibling a newer release added), pass: there is no name here to read two
// ways.
func CheckRoutingMemberNames(raw json.RawMessage) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return fmt.Errorf("projectpolicy: routing: %w", err)
	}
	if err := checkMemberNames("routing", names, routingMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("projectpolicy: routing: %w", err)
	}
	claude, ok := members["claude"]
	if !ok || IsNull(claude) {
		return nil
	}
	cnames, err := objectMemberNames(claude)
	if err != nil {
		return fmt.Errorf("projectpolicy: routing.claude: %w", err)
	}
	if err := checkMemberNames("routing.claude", cnames, routingClaudeMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	return nil
}

// checkMemberNames refuses, at one level of a block, a member name encoding/json
// would read as another: the exact same name twice (the decoder keeps the last it
// reads, so which value survives depends on the order the keys were written in),
// two names that fold to one (the decoder folds a member name by Unicode simple
// case folding before matching it, so they are one field), or a modelled name
// spelled in a folding of itself (which is the same sentence one name at a
// time). A name this Fugaro does not model passes: an unknown key here is a key
// this reader ignores, and the refusal for a block that then names no switch is
// validRoutingMembers'.
//
// It is the one check DisallowUnknownFields cannot make. That decoder refuses a
// name no field answers to, and passes a name that answers to a field in
// another case — which is how {"mode": "enforce", "MODE": "off"} comes to be a
// budget whose mode is decided by the order the keys were written in, and how a
// rate written INPUT_PER_M lands on input_per_m. Three written blocks are held
// to it: the budget block (validBudget), a model_prices entry
// (Prices.UnmarshalJSON) and the routing switch (CheckRoutingMemberNames,
// which adds the prefix its exported errors carry). The prefix is the block's
// path without that prefix — "budget", "model_prices" — so the caller's own
// wrap names the block once.
//
// The folding is *the decoder's own*, which is not strings.ToLower: encoding/json
// folds a member name with unicode.SimpleFold (foldName's contract is
// bytes.EqualFold, so strings.EqualFold answers it), and SimpleFold has orbits
// ToLower does not. U+017F LATIN SMALL LETTER LONG S (ſ) is ToLower's own
// spelling of itself — ToLower("ſ") is "ſ" — and is in 's”s orbit, so a block
// naming allowed_models and allowed_modelſ was read as one member by the
// decoder while this check saw two names, and the allow-list the document
// visibly states was not the one the run was bounded by (code review of the
// first shape of this check). U+212A KELVIN SIGN is the mirror case, fold-equal
// to 'k' and *not* lower-cased to anything else — both are refused now, and
// TestDecodeRefusesFoldEqualMemberNames holds the cases.
func checkMemberNames(prefix string, names, known []string) error {
	seen := map[string]bool{}
	var written []string
	for _, n := range names {
		if seen[n] {
			return fmt.Errorf("%s names %q twice: encoding/json keeps the last one it reads, so which value this Fugaro reads would depend on the order the keys were written in — write the member once", prefix, n)
		}
		seen[n] = true
		for _, prev := range written {
			if strings.EqualFold(prev, n) {
				return fmt.Errorf("%s names both %q and %q, which encoding/json reads as one member (it folds a member name by Unicode simple case folding, as strings.EqualFold does), so which value this Fugaro reads would depend on the order the keys were written in — write the member once, spelled as the node spells it", prefix, prev, n)
			}
		}
		written = append(written, n)
		for _, k := range known {
			if n != k && strings.EqualFold(n, k) {
				return fmt.Errorf("%s spells %s as %q: encoding/json folds a member name by Unicode simple case folding (as strings.EqualFold does) before it matches it, so this Fugaro reads it as %s — write the member as %s documents it, or remove it", prefix, k, n, k, k)
			}
		}
	}
	return nil
}

// checkNoDuplicateNames refuses a block that names one of its keys twice, for
// the blocks this package reads by looking a key up rather than by decoding
// into a struct: the top level of the node, the providers block, the profiles
// block and the model_prices block, whose keys are looked up (or unescaped and
// stored) by their exact spelling. encoding/json keeps the last of a repeated key when it
// builds the map, so the first one is a value the document wrote and this
// reader drops — and where the key holds a whole block, what is dropped is the
// block.
//
// It is checkMemberNames' duplicate half only, with no case rule: a map key is
// matched exactly, so two keys that differ only in case are two keys here —
// while inside a struct-typed entry they are one field, which is what that
// function is for.
func checkNoDuplicateNames(prefix string, names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			return fmt.Errorf("%s names %q twice: encoding/json keeps the last one it reads, so which value this Fugaro reads would depend on the order the keys were written in — write the key once", prefix, n)
		}
		seen[n] = true
	}
	return nil
}

// checkTopLevelNames holds the node's own keys to what Decode reads: no key
// twice (checkNoDuplicateNames), and no key that is a modelled top-level key
// spelled in another case.
//
// The second rule is not checkMemberNames': Decode looks a top-level key up by
// its exact spelling and never decodes the node into a struct, so "Budget"
// beside "budget" is not folded onto one field — it is a second key that
// nothing reads. The value under it is then absent as far as every reader here
// is concerned, which is the same silence a duplicated key has, reached by a
// spelling the operator would not expect to matter. Every other unknown key
// stays tolerated and listed in Policy.UnknownKeys (P8): a misspelt `per_run_usdc`
// is not a spelling of a key this Fugaro models, and a newer release's key is
// not either.
func checkTopLevelNames(names []string) error {
	if err := checkNoDuplicateNames("the policy node", names); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	for _, n := range names {
		for known := range knownTopLevelKeys {
			if n != known && strings.EqualFold(n, known) {
				return fmt.Errorf("projectpolicy: the policy node spells %s as %q: this Fugaro reads the key %s, and the value under %q is not read at all — write it as %s", known, n, known, n, known)
			}
		}
	}
	return nil
}

// objectMemberNames returns the member names of a JSON object in the order the
// document writes them, duplicates included: decoding into a map keeps only the
// last one of a repeated key, and seeing that there was more than one is the
// whole point of this walk.
func objectMemberNames(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("the block is not an object")
	}
	var names []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := kt.(string)
		if !ok {
			return nil, errors.New("the block has a member without a name")
		}
		names = append(names, name)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// validRoutingMembers refuses a written routing block that names no claude
// switch this Fugaro can act on.
//
// An unknown key *inside* this block cannot get the tolerance an unknown
// top-level key gets. Every other top-level key is a section of its own, so
// tolerating one leaves the rest of the policy in force; `routing` has exactly
// one member that does anything, and a block whose member is misspelt
// (`routing: {claud: ...}`) or belongs to something this release does not route
// (`routing: {gemini: ...}`) decodes to a nil switch — every Claude call stays
// on the direct route, silently, on the one document a run is routed by. That
// is the failure this package exists to prevent, read at P8's boundary: what an
// older reader would drop here is not a newer release's extra tightening, it is
// the switch itself.
//
// Nothing forward-compatible is lost: a routing block is only ever *delivered*
// to a node by this Fugaro (internal/cli copies it out of the operator's config,
// whose own decode refuses these blocks), so a node carrying one this cannot
// name was hand-written or written by a release whose node also bumps Version,
// which is what refuses a node this binary cannot read. The remedy is in the
// message, because the node's writer is an administrator.
//
// `routing: {}` — a block with no members at all — is tolerated, exactly as an
// absent one is: it names nothing, and a nil switch is what it says.
//
// The member *names* are checked first, by CheckRoutingMemberNames: encoding/json
// matches a member case-insensitively and keeps the last of two that fold to one
// name, so `{"claude": {"via": "direct", "VIA": "openrouter"}}` would otherwise
// be a switch this Fugaro reads as one of two routes depending on the order the
// keys were written in — and a `CLAUDE` alone would be read as the switch while
// the name check below saw no `claude` at all.
func validRoutingMembers(raw json.RawMessage, r *Routing) error {
	if err := CheckRoutingMemberNames(raw); err != nil {
		return err
	}
	if r != nil && r.Claude != nil {
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("projectpolicy: routing: %w", err)
	}
	if len(members) == 0 {
		return nil
	}
	named := make([]string, 0, len(members))
	for name := range members {
		named = append(named, fmt.Sprintf("%q", name))
	}
	sort.Strings(named)
	if _, isClaude := members["claude"]; isClaude {
		// claude is named, so the member name is not the problem: the value is
		// (a null, or the switch decoded to nothing).
		return fmt.Errorf("projectpolicy: routing.claude is written with no switch (the block names %s): write routing.claude as an object with the Claude IDs it routes, or remove the routing block — an absent one is direct (D1b)", strings.Join(named, ", "))
	}
	return fmt.Errorf("projectpolicy: routing names %s, and this Fugaro routes claude alone: a block that names no claude switch leaves every Claude call on the direct route while the node says otherwise. Write routing.claude, or remove the routing block — an absent one is direct (D1b)", strings.Join(named, ", "))
}

// validRouting checks the routing block against the providers block of the same
// node, with the rules the operator's own config loader applies
// (config.CheckRouting): the via, an explicit list of Claude IDs and never a
// pattern (D1a), and — for every ID the switch names — a provider entry in
// this node that claims it and has allow_data_to written (D1b).
//
// Both halves are applied here because the node is the artifact every run
// reads: an operator's config is judged at load, but a hand-written node is
// never loaded from a config at all. A node that names a route nothing claims,
// or claims it without allow_data_to written, is refused rather than accepted
// as "the switch is there but inert" — D1b's safeguard would otherwise be
// nominal on the one document that decides where a Claude call goes, and
// "inert" is a state nothing downstream would report.
func validRouting(r *Routing, providers map[string]Provider) error {
	if r == nil || r.Claude == nil {
		return nil
	}
	for _, p := range config.CheckRouting(r, providerCarrier(providers), "routing") {
		return fmt.Errorf("projectpolicy: %s: %s", p.Path, p.Message)
	}
	return nil
}

// providerCarrier is the providers block in the carrier internal/config's rules
// are written against (config.ModelProvider, which the policy's Provider
// embeds). One map, built the same way for the providers' own validation and for
// the routing check that reads them, so the two cannot judge different entries.
func providerCarrier(providers map[string]Provider) map[string]config.ModelProvider {
	out := make(map[string]config.ModelProvider, len(providers))
	for _, name := range providerNames(providers) {
		out[name] = providers[name].ModelProvider
	}
	return out
}

// validPricePolicy checks the one provider value the carrier's own validation
// does not: an unrecognised price_policy would price a route at the wrong end
// of the catalogue's range rather than fail. internal/config refuses it too
// (ValidateModelProviders, the same two values); this is the check the *node*
// is held to, because a policy is read by a runner that never parses the
// operator's config.
func validPricePolicy(name, policy string) error {
	switch policy {
	case "", PricePolicyCheapest, PricePolicyHighest:
		return nil
	}
	return fmt.Errorf("projectpolicy: providers.%s.price_policy %q must be %s or %s", name, policy, PricePolicyCheapest, PricePolicyHighest)
}

// budgetMembers are the member names the budget block is documented with, in
// the spelling its json tags write: the names checkMemberNames holds a written
// block to, and the same four internal/config reads fugaro.yaml's budget block
// by.
var budgetMembers = []string{"allowed_models", "max_run_tokens", "mode", "per_run_usd"}

// validBudget mirrors internal/config's checks of fugaro.yaml's budget: block
// (validateBudget) for the four keys the policy carries — every value and, for
// allowed_models, every entry (CheckModelID, DuplicateModel) — because these
// values are enforced, not advisory. An unrecognised mode is the dangerous one:
// policy.Merge treats it as unset, and the run falls back to off.
//
// raw is the block as written, and it is read first for the member *names*
// (checkMemberNames, against budgetMembers): the strict decode above refuses a
// name no field answers to, but not a name that answers to one in another case,
// and {"mode": "enforce", "MODE": "off"} would otherwise decode to whichever
// the document wrote last — a budget this reader enforces and its author cannot
// read. internal/config's own decoder refuses the same spelling in fugaro.yaml,
// and the node is the document a run is metered by.
func validBudget(raw json.RawMessage, b Budget) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkMemberNames("budget", names, budgetMembers); err != nil {
		return err
	}
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

// IsNull reports whether a value is absent: an empty body, or the JSON null
// RTDB reads a missing node as. Exported because the write path asks the same
// question of a member it is about to replace (internal/cli's
// policyNodeBytes): a `routing` member written as null is an absent one there
// exactly as it is here, and a member the two packages read differently is a
// member one of them gets wrong.
func IsNull(raw []byte) bool {
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
