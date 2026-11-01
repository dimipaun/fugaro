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
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// Policy is the shared, readable node, decoded as JSON.
//
// The `budget` block is the M9a policy (mode, per-run ceiling, allowed models,
// token ceiling); its keys are all-or-nothing in the sense Decode documents: a
// block that names some of them leaves the rest at zero, exactly as
// fugaro.yaml's own budget: block does, and a key that is not documented is an
// error rather than a silently-dropped zero.
type Policy struct {
	Version int    `json:"version"`
	Budget  Budget `json:"budget"`
	// BaseBranch is the project's default base branch (internal/config's
	// EffectiveBaseBranch, layer 3): what a repository of this project runs
	// against when neither its committed fugaro.yaml sets git.base_branch nor
	// its local repos.<r>.base_branch entry names one. It is here and not in
	// the published shared config for the reason every member of this node is:
	// the owner writes it (`fugaro project policy publish`) and the runner
	// fails closed on the node, while the shared file is writable by any
	// launcher — a base branch there would let one move every repository of the
	// project at once. "" means the project sets none, and the value is then
	// main.
	//
	// It is the node's own member and not a per-repository one: the
	// repository entries this node has (config/policy/repos/$slug) carry
	// admissions, and a base branch is not an admission.
	BaseBranch string              `json:"base_branch,omitempty"`
	AllowRepos []string            `json:"allow_repos"`
	Providers  map[string]Provider `json:"providers,omitempty"`
	Prices     Prices              `json:"model_prices,omitempty"` // the owner's price overrides, keyed by model ID (Prices documents the node's own spelling)
	Routing    *Routing            `json:"routing,omitempty"`      // the run applies it (internal/runner's applyRouting); nil = direct (D1b)

	// Credentials are the credentials cascade's settings (D152, D153): the
	// paid-fallback policy, the wait-or-switch threshold, the backoff a
	// credential with no reset time is held for, and the two owner-headroom
	// controls (D142). They live here and nowhere else — D153's ruling is that
	// a repository cannot move a project onto paid tiers, so there is no
	// fugaro.yaml key for any of them (internal/config refuses one) — and the
	// cascade they configure is switched on by routing.claude's `via: cascade`
	// (O3, D150), not by this block: the block says *how* the cascade behaves,
	// the switch says whether it is on.
	//
	// nil is every default (an absent block and a written empty one are the
	// same policy), and the accessors below are how a reader asks: a consumer
	// never reads a member directly, because each member's absent meaning is a
	// value and not a zero (FallbackValue, WaitThresholdDuration,
	// BackoffInitialDuration, BackoffCapDuration, StopsAfterExhaustion,
	// ConcurrencyCap).
	Credentials *Credentials `json:"credentials,omitempty"`

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
	// project's local config (DeriveAccounts), and `fugaro update` validates the
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

	// BitbucketToken is the project's Bitbucket credential block (D473, D474,
	// docs/secrets-brokering D493): the repositories that token may reach, and
	// whether the credentials service forwards it for them. It is derived from
	// the project's local config (DeriveBitbucketToken) and is derived whether
	// or not a profile mounts the token, because a proxied run's whole
	// admission is this block — the job it runs in does not hold the
	// credential. A profile that mounts the token is held to it:
	// validProfiles and validBitbucketProfileRepos are the two halves of that
	// rule.
	BitbucketToken *BitbucketToken `json:"bitbucket_token,omitempty"`

	// BitbucketAPIRoots are the Bitbucket API roots the owner allow-lists
	// (D474, task T2.8): the runner accepts FUGARO_BITBUCKET_API_URL only when
	// it is one of these, matched exactly after trimming one trailing "/". The
	// block is data rather than an environment value for the reason CloneURLs
	// is: an environment value on a shared job is launcher-writable, and every
	// run of the project is held to one list.
	BitbucketAPIRoots []string `json:"bitbucket_api_roots,omitempty"`

	// ReviewFloor is the owner's review-level floor (D722, G1): per rigor
	// mode, the levels a run of that mode must run, whatever a lower layer
	// asks for. It belongs to this node for the reason every member does —
	// the owner writes it and the runner fails closed on it — and for a
	// sharper one: the floor exists to stop a launcher turning review off,
	// and the shared file is writable by any launcher, so a floor there could
	// be dropped by the very launcher it is meant to bind
	// (docs/design/bucket-iam.md §10 L1, L2). `fugaro project policy publish`
	// writes it from the owner's project config, and the runner reads the
	// node at bootstrap and merges the floor as a union (internal/policy's
	// Merge): a lower layer can add a level and never remove one.
	//
	// An absent member and a written empty one are both "no floor": the
	// publisher writes nothing at all for a project that sets none, so the
	// node of such a project is byte for byte what it was before this member
	// existed.
	ReviewFloor map[rigor.Mode][]policy.Level `json:"review_floor,omitempty"`

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
	"credentials": true, "base_branch": true,
	"bitbucket_token": true, "bitbucket_api_roots": true,
	"review_floor": true,
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

// RouteCascade is routing.claude's cascade value (O3, D150): the switch that
// turns the credentials cascade on for a `cascade` workflow. It is config's own
// value, aliased, for the reason Routing is aliased — one switch, one
// declaration — and it is named here because the policy is where an operator
// reads it.
//
// It needs no provider claim while tier C for Claude is off (O3, C3.6), so a
// project turns the cascade on by writing the via alone: nothing about where a
// Claude call goes changes, only which credential carries it.
const RouteCascade = config.RouteCascade

// The values credentials.fallback accepts (D152), spelled as the node spells
// them. There is no third value: the cascade is A→B→C or A alone, and a name
// outside these two is refused rather than read as one of them.
const (
	// FallbackOAuthOnly is what an absent fallback means, and it is what today's
	// OAuth runs do: never spend the project's money, wait for the shared
	// token's reset instead (and let the run wait, whatever the threshold —
	// the threshold decides between waiting and failing over, and there is
	// nothing to fail over to).
	FallbackOAuthOnly = "oauth-only"
	// FallbackAuto is the cascade: tier A, then the project's Anthropic API key
	// (tier B), then tier C where it is on. It must be written, and D152 refuses
	// it without an enforcing per-run ceiling, because it is the value that
	// starts spending.
	FallbackAuto = "auto"
)

// The cascade's defaults (D153): a 15-minute wait-or-switch threshold, and a
// backoff of 15 minutes doubling to a 5-hour cap for a credential whose
// exhaustion came with no reset time. They are what an absent member means, so
// a project that writes nothing gets the spec's resolved behaviour rather than
// a zero — a zero threshold would fail over on every exhaustion, and a zero cap
// is a wait that never doubles.
const (
	DefaultWaitThreshold       = 15 * time.Minute
	DefaultBackoffInitial      = 15 * time.Minute
	DefaultBackoffCap          = 5 * time.Hour
	DefaultConcurrencyPerToken = 1
)

// Credentials is the cascade's settings block (D152, D153). Every member is
// optional and every accessor below answers what an absent one means, so the
// two spellings of "the operator wrote nothing" — an absent block and an empty
// one — are one policy.
//
// The two controls are D142's owner-headroom pair, and their defaults are the
// protective ones. The plan names defaults for the threshold and the backoff
// only, so the two are this task's decision, made on D142's own ground: the
// tier-A credentials are subscription tokens somebody donated, and a project
// that has not decided may not spend one of them more than once in a window and
// may not run more than one run at a time against one. Both are written to be
// turned off — false, and 0 — and neither is written to be turned on quietly.
type Credentials struct {
	// Fallback is FallbackOAuthOnly or FallbackAuto; absent is
	// FallbackOAuthOnly (D152). A written FallbackAuto needs the budget block
	// to be enforcing with a positive per_run_usd, which Decode refuses
	// otherwise: `auto` is the one value that starts spending the project's
	// money, and it may not be reached by writing one word.
	Fallback string `json:"fallback,omitempty"`
	// WaitThreshold is how long a run waits for an exhausted credential's reset
	// before failing over to the next tier (the spec's wait-or-switch rule),
	// as a duration string read by config.ParseDuration. Under
	// FallbackOAuthOnly it is ignored: there is nothing to fail over to, so the
	// run waits for the earliest reset whatever the threshold says (D152).
	WaitThreshold string `json:"wait_threshold,omitempty"`
	// Backoff is the wait a credential is held for when its exhaustion reported
	// no reset time. nil is DefaultBackoffInitial / DefaultBackoffCap.
	Backoff *Backoff `json:"backoff,omitempty"`
	// StopAfterExhaustionPerWindow is D142's first control: a pointer so that
	// "the operator wrote false" and "the operator wrote nothing" are different
	// states, since the absent one means true (StopsAfterExhaustion). It is a
	// *bool rather than a bool for the same reason require_zdr is:
	// encoding/json fills an absent key with the zero value, and false is the
	// zero value, so a plain bool could not tell the protective default from
	// the opt-out.
	StopAfterExhaustionPerWindow *bool `json:"stop_after_exhaustion_per_window,omitempty"`
	// ConcurrencyPerToken is D142's second control: at most this many runs may
	// hold one shared credential at a time. A pointer, so an absent key (one
	// run at a time, DefaultConcurrencyPerToken) is not the written 0, which
	// means no cap at all.
	ConcurrencyPerToken *int `json:"concurrency_per_token,omitempty"`
}

// Backoff is the wait a backed-off credential is held for: `initial` the first
// time, doubling on each failed probe up to `cap` (the spec's resolved default,
// D153). Either member may be left out, and one left out is its default.
type Backoff struct {
	Initial string `json:"initial,omitempty"`
	Cap     string `json:"cap,omitempty"`
}

// credentialsMembers, backoffMembers are the member names the two structs are
// spelled with, read from their own json tags (carrierMembers) so a field this
// type gains is held to its spelling without a second list — the same
// arrangement accountMembers and profileMembers have.
var (
	credentialsMembers = carrierMembers(reflect.TypeOf(Credentials{}))
	backoffMembers     = carrierMembers(reflect.TypeOf(Backoff{}))
)

// FallbackValue is the paid-fallback policy in force: FallbackOAuthOnly when
// the member is absent (D152) and the written value otherwise. It is nil-safe,
// so a policy with no credentials block reads today's behaviour rather than a
// zero string no comparison matches.
func (c *Credentials) FallbackValue() string {
	if c == nil || c.Fallback == "" {
		return FallbackOAuthOnly
	}
	return c.Fallback
}

// WaitThresholdDuration is the wait-or-switch threshold in force: the written
// duration, or DefaultWaitThreshold.
func (c *Credentials) WaitThresholdDuration() time.Duration {
	if c == nil {
		return DefaultWaitThreshold
	}
	return credentialsDuration(c.WaitThreshold, DefaultWaitThreshold)
}

// BackoffInitialDuration is the first wait of a credential backed off with no
// reset time: the written duration, or DefaultBackoffInitial.
func (c *Credentials) BackoffInitialDuration() time.Duration {
	if c == nil || c.Backoff == nil {
		return DefaultBackoffInitial
	}
	return credentialsDuration(c.Backoff.Initial, DefaultBackoffInitial)
}

// BackoffCapDuration is the longest wait the doubling reaches: the written
// duration, or DefaultBackoffCap.
func (c *Credentials) BackoffCapDuration() time.Duration {
	if c == nil || c.Backoff == nil {
		return DefaultBackoffCap
	}
	return credentialsDuration(c.Backoff.Cap, DefaultBackoffCap)
}

// StopsAfterExhaustion reports D142's first control: true unless the operator
// wrote false. The pointer is what makes that sentence possible — an absent key
// is the protective default, and only a written false is an opt-out.
func (c *Credentials) StopsAfterExhaustion() bool {
	return c == nil || c.StopAfterExhaustionPerWindow == nil || *c.StopAfterExhaustionPerWindow
}

// ConcurrencyCap is D142's second control: the number of runs that may hold one
// shared credential at a time, DefaultConcurrencyPerToken when the key is
// absent, and 0 when the operator wrote 0 — 0 is the written "no cap", and it
// is deliberately not the absent key's meaning.
func (c *Credentials) ConcurrencyCap() int {
	if c == nil || c.ConcurrencyPerToken == nil {
		return DefaultConcurrencyPerToken
	}
	return *c.ConcurrencyPerToken
}

// credentialsDuration reads a written duration, or answers def when the member
// was not written. A value that is not a positive duration answers def too, and
// that case is unreachable through the node: Decode refuses it
// (validCredentialsDuration). It is spelled this way rather than as an error
// because the alternative for a hand-built policy is a zero — and a zero
// threshold fails over on every exhaustion, while a zero backoff cap is a wait
// that never doubles. A reader that cannot be given a bad value must not turn
// one into the most permissive setting there is.
func credentialsDuration(value string, def time.Duration) time.Duration {
	if value == "" {
		return def
	}
	d, err := config.ParseDuration(value)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// checkCredentialsMembers holds the block's member names to the documented
// spelling — the top level and the backoff block inside it — for the reason
// checkMemberNames gives: encoding/json folds a member name, so a strict decode
// alone reads {"fallback": ..., "Fallback": ...} as whichever the document
// wrote last. What a folded name costs here is a *setting*: a misspelt
// concurrency_per_token leaves the cap at its default while the node appears to
// state one, and a misspelt fallback leaves the cascade at oauth-only under a
// node that reads as auto.
//
// The member-name lists are derived from the structs (carrierMembers), and what
// the check adds over the strict decode is the fold: a name no member answers
// to is refused by DisallowUnknownFields first.
func checkCredentialsMembers(raw json.RawMessage) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return fmt.Errorf("projectpolicy: credentials: %w", err)
	}
	if err := checkMemberNames("credentials", names, credentialsMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("projectpolicy: credentials: %w", err)
	}
	b, ok := members["backoff"]
	if !ok || IsNull(b) {
		return nil
	}
	bnames, err := objectMemberNames(b)
	if err != nil {
		return fmt.Errorf("projectpolicy: credentials.backoff: %w", err)
	}
	if err := checkMemberNames("credentials.backoff", bnames, backoffMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	return nil
}

// validCredentials holds the block's values to their rules: D152's fallback
// rule, which is a property of this block and the budget block together, and
// the shapes of the durations and the controls.
//
// The fallback rule is the one that matters. `auto` is the cascade's paid
// fallback — tier A, then B, then C where C is on — and D152's ruling is that
// no project reaches it by writing one word: the budget must be enforcing, with
// a per-run ceiling above zero, or the value is refused. Observe mode is not
// enough (the ceiling would be recorded and not enforced), an absent budget
// block is not enough (the run's ceiling is then whatever its job's environment
// carries, which this node cannot see), and enforce with per_run_usd 0 is not
// enough either (a cap of zero is nothing charged against it). The refusal
// names the two things to set, because the operator can set both and a sentence
// that named only `fallback` would send them to the one key that is already
// right.
func validCredentials(c *Credentials, b Budget) error {
	switch c.Fallback {
	case "", FallbackOAuthOnly:
	case FallbackAuto:
		if b.Mode != policy.ModeEnforce || b.PerRunUSD <= 0 {
			return fmt.Errorf("projectpolicy: credentials.fallback: %q is the cascade's paid fallback, and this policy's budget does not bound it: write budget: {mode: %q, per_run_usd: <a ceiling above zero>} in the project policy (currently mode %q, per_run_usd %v). A run under auto may spend the project's Anthropic key (tier B) and, where tier C is on, a third party's; with the ceiling unset or unenforced that spend is one nobody can see or stop — which is why the value must be written and the ceiling must be there (D152)",
				FallbackAuto, policy.ModeEnforce, b.Mode, b.PerRunUSD)
		}
	default:
		return fmt.Errorf("projectpolicy: credentials.fallback: %q is not a value this Fugaro knows: write %q (never spend the project's money — wait for the shared token's reset) or %q (fall over to the paid tiers), or remove the key, which means %q (D152)",
			c.Fallback, FallbackOAuthOnly, FallbackAuto, FallbackOAuthOnly)
	}
	if _, err := validCredentialsDuration("projectpolicy: credentials.wait_threshold", c.WaitThreshold); err != nil {
		return err
	}
	if c.Backoff != nil {
		// Both members are held to their spelling and their shape first, so the
		// comparison below reads values this function has already accepted.
		if _, err := validCredentialsDuration("projectpolicy: credentials.backoff.initial", c.Backoff.Initial); err != nil {
			return err
		}
		if _, err := validCredentialsDuration("projectpolicy: credentials.backoff.cap", c.Backoff.Cap); err != nil {
			return err
		}
	}
	// A cap below the wait it caps is a wait that never doubles to it, and the
	// pair the rule is about is the *effective* pair: an absent member is its
	// default (D153 — 15m, capped at 5h), which is what the cascade would use.
	// Comparing only what was written left the state the rule exists to refuse
	// reachable by writing one member — {"backoff": {"cap": "1m"}} is a 15m
	// initial with a 1m cap, and {"backoff": {"initial": "10h"}} a 10h initial
	// with a 5h cap — so a node that writes one of the two is held to the
	// other's default. The members are compared through the same accessors a
	// consumer reads, so this refusal and a reader cannot disagree about which
	// pair is meant.
	if initial, cap := c.BackoffInitialDuration(), c.BackoffCapDuration(); cap < initial {
		return fmt.Errorf("projectpolicy: credentials.backoff.cap: %s is below credentials.backoff.initial (%s): a cap below the first wait is a probe schedule that never doubles, so a credential with no reset time would be retried at the same rate for ever. The two are compared as they would be used, and an absent member is its default (%s and %s), so a node that writes only one of them is held to the other's default: write a cap at or above the initial wait, or remove the member you wrote",
			cap, initial, DefaultBackoffInitial, DefaultBackoffCap)
	}
	if c.ConcurrencyPerToken != nil && *c.ConcurrencyPerToken < 0 {
		return fmt.Errorf("projectpolicy: credentials.concurrency_per_token: %d is not a number of runs: write the number of runs that may hold one shared credential at a time, at least 1, or 0 for no cap at all (D142)",
			*c.ConcurrencyPerToken)
	}
	return nil
}

// validCredentialsDuration reads one duration member of the block: "" (not
// written) is no value, and anything else must be a duration config.ParseDuration
// reads and a positive one.
//
// The spelling is config.ParseDuration's — the one profiles.<p>.timeout_class is
// read with, day counts and all — so "90m" is a threshold here because it is a
// timeout there, rather than a form this package refused and that one accepted.
// A non-positive duration is refused rather than read as "no wait": a threshold
// of zero fails over on every exhaustion (the tier switch becomes the rule),
// and a backoff of zero is a credential a run retries immediately.
func validCredentialsDuration(at, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := config.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration, for example \"90s\", \"15m\" or \"5h\"", at, value)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive duration", at, value)
	}
	return d, nil
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

	if braw, ok := doc["base_branch"]; ok && !IsNull(braw) {
		if err := json.Unmarshal(braw, &p.BaseBranch); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: base_branch: %w", err)
		}
		// The same rule fugaro.yaml's git.base_branch and the installation
		// config's own base_branch are held to (config.ValidBranchName): the
		// value reaches git, Docker build arguments, Cloud Build and a run's
		// ref, and the node is a hand-writable object.
		if !config.ValidBranchName(p.BaseBranch) {
			return nil, true, fmt.Errorf("projectpolicy: base_branch %q is not a plain git branch name (the project's default base branch)", p.BaseBranch)
		}
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

	// The credentials block is judged against the budget block decoded just
	// above, and after it: D152's rule for `fallback: auto` is a property of
	// the two together (the spend must be bounded), so reading them the other
	// way round would decide it against a budget this reader has not seen —
	// a policy whose auto was accepted because the block was still zero.
	if craw, ok := doc["credentials"]; ok && !IsNull(craw) {
		if err := checkCredentialsMembers(craw); err != nil {
			return nil, true, err
		}
		dec := json.NewDecoder(bytes.NewReader(craw))
		dec.DisallowUnknownFields()
		var c Credentials
		if err := dec.Decode(&c); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: credentials: %w", err)
		}
		if err := validCredentials(&c, p.Budget); err != nil {
			return nil, true, err
		}
		p.Credentials = &c
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
	// The Bitbucket block is judged *before* the profiles block, and the
	// profiles against it: a profile that mounts the token admits a subset of
	// this block's repositories, which is a property of the two together, and
	// the profiles are the side that may not widen it. Reading them the other
	// way round would hold a profile's list to a block this reader has not
	// seen.
	if braw, ok := doc["bitbucket_token"]; ok && !IsNull(braw) {
		bt, err := decodeBitbucketTokenBlock(braw)
		if err != nil {
			return nil, true, err
		}
		if err := validBitbucketTokenBlock(bt); err != nil {
			return nil, true, err
		}
		p.BitbucketToken = bt
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
	// The profile-side half of the Bitbucket rule, after both blocks have been
	// decoded and judged: a profile mounting the token admits only what the
	// block admits. A profile with no block at all is refused here rather than
	// read as covered by the project's allow_repos — the token's reach is the
	// block's, and the project's enrollment is not a list that token is
	// bounded by.
	if err := validBitbucketProfileRepos(p.Profiles, p.BitbucketToken); err != nil {
		return nil, true, err
	}
	if rraw, ok := doc["bitbucket_api_roots"]; ok && !IsNull(rraw) {
		if err := json.Unmarshal(rraw, &p.BitbucketAPIRoots); err != nil {
			return nil, true, fmt.Errorf("projectpolicy: bitbucket_api_roots: %w", err)
		}
		if err := ValidBitbucketAPIRoots(p.BitbucketAPIRoots); err != nil {
			return nil, true, err
		}
	}
	if fraw, ok := doc["review_floor"]; ok && !IsNull(fraw) {
		floor, err := decodeReviewFloor(fraw)
		if err != nil {
			return nil, true, err
		}
		p.ReviewFloor = floor
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

// decodeReviewFloor reads the owner's review-level floor (Policy.ReviewFloor)
// from the node.
//
// What the members *mean* is policy.ParseFloor's rule, the same one the
// operator's config file is held to: a mode that is not one of rigor's, a
// level outside l1/l2/l3, a level listed twice and a mode with no level in it
// are refused rather than dropped — a dropped level is a run that reviews less
// than the owner required, which is the one direction this key must never
// move, and the node is hand-writable. What is left here is the document's own
// shape, which the config loader has no equivalent of: the member must be an
// object whose keys are named once (encoding/json keeps the last value it
// reads, so a mode named twice would leave which floor a run got up to the
// order the keys were written in).
//
// A block that names no mode at all is no floor: the publisher writes nothing
// for a project that sets none, and a written empty object — which is how an
// operator takes a published floor away on purpose — reads as the same answer.
func decodeReviewFloor(raw json.RawMessage) (map[rigor.Mode][]policy.Level, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: review_floor: %w", err)
	}
	if err := checkNoDuplicateNames("projectpolicy: review_floor", names); err != nil {
		return nil, err
	}
	var written map[string][]string
	if err := json.Unmarshal(raw, &written); err != nil {
		return nil, fmt.Errorf("projectpolicy: review_floor: %w", err)
	}
	floor, err := policy.ParseFloor(written)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	return floor, nil
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

// ------------------------------------------------- the per-repository node

// RepoPolicy is one repository's policy child, config/policy/repos/$slug (O4).
// It is a *different node* from the shared one: the rules let exactly one run
// token read it — the token whose slug is $slug — so what is written here is
// what that repository's own run may know, and this release writes one thing
// into it: the names of the Claude accounts the repository is admitted to.
//
// The names are a copy of a fact the shared node already holds inverted
// (accounts.<name>.allow_repos) and they are published because the reader is
// not a run: the credentials service, which holds the tier-A tokens and has to
// decide, per call, whether *this* repository may be given one (C1.11, O4).
// Publishing them is how the service learns the admissions without reading the
// operator's local config, which lives on one machine.
//
// Nothing here is a secret and nothing here is a value: an account's name is
// what config.ClaudeAccount is keyed by, and its token is a secret version the
// service reads as its own service account (D147). A run of the repository
// gains nothing from this node it could not already infer from its own job
// (O4) — and the node is still readable by that run, so a value here would be a
// value a prompt-injected agent can take through /proc.
//
// A repository with no admissions has no child at all: publishing writes one
// per repository that some account covers, so the node's presence is the
// admission, and "no node" and "an empty list" are not two states a reader has
// to tell apart. (An empty list is refused anyway — see DecodeRepo.)
//
// Credentials is a pointer for the same reason: `omitempty` is a no-op on a
// struct, so a value-typed field would marshal a repository with nothing
// admitted as {"credentials":{"accounts":null}} — a document DecodeRepo refuses
// (a written block must name at least one account), which would make the type
// this package writes back the one document this package will not read. A nil
// pointer writes no member at all, which is the state "this repository is
// admitted to nothing".
type RepoPolicy struct {
	Credentials *RepoCredentials `json:"credentials,omitempty"`
}

// RepoCredentials is the one block this release writes into a repository's
// node: the accounts it is admitted to, by name.
type RepoCredentials struct {
	// Accounts are the account names this repository's runs may use a tier-A
	// credential from, sorted. Never empty: a written block that names none is
	// refused, because the operator who wrote one believes they admitted
	// somebody.
	Accounts []string `json:"accounts"`
}

// repoMembers, repoCredentialsMembers are the member names those two structs
// are spelled with, derived from their json tags as credentialsMembers is.
var (
	repoMembers            = carrierMembers(reflect.TypeOf(RepoPolicy{}))
	repoCredentialsMembers = carrierMembers(reflect.TypeOf(RepoCredentials{}))
)

// DecodeRepo reads one repository's policy node, config/policy/repos/$slug. It
// returns (p, found, err) exactly as Decode documents: absent is (nil, false,
// nil), and a node that exists and cannot be used is (nil, true, err) — an
// error, never "no admissions", because a reader that turns an unreadable
// admission list into an empty one hands tier A to nobody (fail closed, which
// is what "the service cannot read this repository's admissions" must mean) or,
// worse, reads a list it did not understand as a list it did.
//
// It is strict where Decode is tolerant, and the difference is deliberate. The
// shared node tolerates an unknown top-level key because a newer Fugaro's
// section must not brick an older reader (P8); this node was introduced by this
// release, nothing else writes it, and its whole content is an admission — so a
// key this reader does not know is refused rather than ignored, and a future
// member there is a version bump, which is a decision for whoever adds it.
//
// The rules and the publishing are the same either way: what a repository may
// use a tier-A credential from is a security decision, and a member that does
// not fold into this shape (a misspelt `accounts`, a block written as a list)
// is refused instead of being read as the silent "no accounts".
func DecodeRepo(raw []byte) (*RepoPolicy, bool, error) {
	if IsNull(raw) {
		return nil, false, nil
	}
	if len(raw) > MaxPolicyBytes {
		return nil, true, fmt.Errorf("projectpolicy: the repository's policy node is %d bytes; at most %d are allowed", len(raw), MaxPolicyBytes)
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the repository's policy node: %w", err)
	}
	if err := checkMemberNames("the repository's policy node", names, repoMembers); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rp RepoPolicy
	if err := dec.Decode(&rp); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the repository's policy node: %w", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the repository's policy node: %w", err)
	}
	block, ok := members["credentials"]
	if !ok || IsNull(block) {
		// No block, or one written as null: a repository this policy admits to
		// no account. It decodes rather than being refused — the node is
		// written per repository, and a repository with no account is a legal
		// state (its runs are simply never given tier A) — and it admits
		// nothing. The decoded policy carries no block for the same reason: a
		// null and an absent member are one policy, and the shape a caller
		// re-marshals must be the shape this function reads.
		rp.Credentials = nil
		return &rp, true, nil
	}
	bnames, err := objectMemberNames(block)
	if err != nil {
		return nil, true, fmt.Errorf("projectpolicy: the repository's policy node's credentials: %w", err)
	}
	if err := checkMemberNames("the repository's policy node's credentials", bnames, repoCredentialsMembers); err != nil {
		return nil, true, fmt.Errorf("projectpolicy: %w", err)
	}
	if rp.Credentials == nil || len(rp.Credentials.Accounts) == 0 {
		return nil, true, errors.New("projectpolicy: the repository's policy node's credentials.accounts: a written credentials block must name at least one account: an absent list, a null one and an empty one would all read as \"this repository may use no account's token\", which is the state a hand-written node uses to take tier A away from a repository by accident — remove the whole credentials block to say that nobody is admitted, or name the accounts")
	}
	seen := make(map[string]bool, len(rp.Credentials.Accounts))
	for i, name := range rp.Credentials.Accounts {
		if msg := config.ValidClaudeAccountRef(name); msg != "" {
			return nil, true, fmt.Errorf("projectpolicy: the repository's policy node's credentials.accounts[%d]: %s — an entry here is the NAME of an account of the project policy's accounts block, which is what the credentials service resolves to a token", i, msg)
		}
		if seen[name] {
			return nil, true, fmt.Errorf("projectpolicy: the repository's policy node's credentials.accounts[%d]: an account is listed twice; list each account once", i)
		}
		seen[name] = true
	}
	return &rp, true, nil
}

// AdmittedAccounts is the accounts this repository's runs may use a tier-A
// credential from, sorted, and nil when the node admits none. It is a copy:
// the decoded node is not a value a caller may edit into a policy that says
// something else.
func (r *RepoPolicy) AdmittedAccounts() []string {
	if r == nil || r.Credentials == nil || len(r.Credentials.Accounts) == 0 {
		return nil
	}
	out := make([]string, len(r.Credentials.Accounts))
	copy(out, r.Credentials.Accounts)
	sort.Strings(out)
	return out
}

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
