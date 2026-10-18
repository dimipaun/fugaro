package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"gopkg.in/yaml.v3"
)

// Model providers are the owner's list of non-Anthropic backends a run's
// models may be routed to (design m10-multi-model.md §3). They live in the
// local config, never in fugaro.yaml: a repository can only name a model,
// and the owner decides whether it may reach one. They are not the git
// providers of Providers.

// ModelProviderKind is the only kind of provider in M10: an endpoint that
// accepts Anthropic Messages requests.
const ModelProviderKind = "anthropic-compat"

// MaxRouteFeePct bounds route_fee_pct; a larger fee is a typo.
const MaxRouteFeePct = 50

// The two price policies a provider route may set: which end of the selected
// tier's price range the built-in price catalog charges. They are the config's
// spelling of pricing.CatalogRoute.Policy, and ValidateModelProviders refuses
// anything else.
const (
	PricePolicyCheapest = "cheapest"
	PricePolicyHighest  = "highest"
)

// ModelProvider is one entry of the local config's providers block.
type ModelProvider struct {
	Kind string `yaml:"kind" json:"kind"`
	// BaseURL is https, or http on a loopback host (the fake in tests).
	BaseURL string `yaml:"base_url" json:"base_url"`
	// Auth is how the credential is sent: "bearer" (Authorization) or
	// "x-api-key".
	Auth string `yaml:"auth" json:"auth"`
	// Secret is the logical name of the secret holding the key, never the
	// key itself.
	Secret string `yaml:"secret" json:"secret"`
	// RouteFeePct is added to every charge on this provider; 0 is none.
	RouteFeePct float64 `yaml:"route_fee_pct,omitempty" json:"route_fee_pct,omitempty"`
	// RequireZDR prices the provider's models at the endpoints that hold no
	// data (the built-in price catalog's zdr tier) rather than at every
	// endpoint the model is served by. Unset means true: a route's safe end
	// is the one the owner would have picked. It prices a tier; it does not
	// enforce routing (see PricePolicy).
	RequireZDR *bool `yaml:"require_zdr,omitempty" json:"require_zdr,omitempty"`
	// PricePolicy is which end of the selected tier's price range is charged:
	// "cheapest" or "highest"; unset means cheapest (PricePolicySet). It
	// prices a tier; it does not choose the endpoint OpenRouter serves from,
	// so a call may still be billed at the other end of the range (see
	// docs/multi-model.md §2).
	PricePolicy string `yaml:"price_policy,omitempty" json:"price_policy,omitempty"`
	// Models are the model IDs the provider serves: exact IDs, or a prefix
	// ending in "*" such as "deepseek/*". No two providers may overlap.
	Models []string `yaml:"models" json:"models"`
	// AllowDataTo are the repositories (owner/name) whose code may be sent
	// here. Absent (the zero value) or ["*"] mean every repository (owner
	// ruling, 2026-10-13 and its follow-ups, design
	// docs/design/project-secrets.md §3a); a list written with no
	// repositories in it -- [] , or the key with nothing under it at all --
	// is refused by ValidateModelProviders as ambiguous.
	AllowDataTo []string `yaml:"allow_data_to,omitempty" json:"allow_data_to,omitempty"`
	// allowDataToWritten records that the YAML gave allow_data_to at all.
	// It exists because a key written with nothing under it (`allow_data_to:`
	// with every entry commented out, or `~`) decodes to the same nil slice
	// as an absent key -- which means every repository -- and only the
	// mapping node can tell the two apart (UnmarshalYAML).
	allowDataToWritten bool
}

// modelProviderType is ModelProvider's own type name, for the unknown-field
// refusal below: UnmarshalYAML has to build the message yaml.v3's strict
// mode would have built, and that mode names the type it was decoding into.
var modelProviderType = reflect.TypeOf(ModelProvider{})

// providerKeys are the yaml keys a providers entry accepts, read from the
// struct's own tags so a new field is picked up without a second list.
var providerKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(ModelProvider{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// UnmarshalYAML decodes one providers entry, and refuses a key it does not
// know. That second job is normally yaml.v3's own (Decoder.KnownFields), but
// known fields are a property of a Decoder while decoding through a Node --
// the only way to see a key that was written with nothing under it -- is
// always lenient, so the check is repeated here, with the same message.
func (p *ModelProvider) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: a provider must be a mapping of field to value", node.Line)}}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; !providerKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, modelProviderType)}}
		}
	}
	type plain ModelProvider // no methods: decodes the fields without recursing into this one
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*p = ModelProvider(v)
	// A null value is short-circuited by yaml.v3 before any unmarshaler
	// sees it, so the key's presence is read off the node itself: written
	// with nothing under it is not the same state as absent.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "allow_data_to" {
			p.allowDataToWritten = true
			break
		}
	}
	return nil
}

var (
	providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	modelPatternRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*\*?$`)
	repoSlugRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// RequireZDROn reports whether the route prices the ZDR-eligible endpoints
// only: true unless require_zdr is false (the PRSettings.EarlyDraftOn shape).
func (p ModelProvider) RequireZDROn() bool { return p.RequireZDR == nil || *p.RequireZDR }

// PricePolicySet is the policy to apply, spelled out: cheapest when the field
// is unset. It is the value that reaches the price table, so the compiler
// never branches on the empty string.
func (p ModelProvider) PricePolicySet() string {
	if p.PricePolicy == "" {
		return PricePolicyCheapest
	}
	return p.PricePolicy
}

// CatalogEligible reports whether the built-in price catalog may price a
// route at baseURL. It is an inference, not a knob: the catalog is generated
// from OpenRouter's public API, so it prices OpenRouter itself (openrouter.ai
// and any subdomain of it) and a loopback address, which is the fake a test
// routes to. A vendor's own endpoint — api.deepseek.com, a Bedrock host — is
// priced by model_prices, because its prices are the vendor's and nothing
// here has read them (design D5, P6).
func CatalogEligible(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai") {
		return true
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Matches reports whether the provider serves model.
func (p ModelProvider) Matches(model string) bool {
	return slices.ContainsFunc(p.Models, func(pat string) bool { return PatternMatches(pat, model) })
}

// AllowsData reports whether repo (owner/name) may send code to p: true for
// every repository when AllowDataTo is nil (the key is absent -- never
// written at all), or when it contains the literal entry "*" (owner ruling,
// 2026-10-13, and its follow-ups, design docs/design/project-secrets.md
// §3a). A list written with no repositories in it -- [] , or a null key,
// which the unmarshaller marks with allowDataToWritten -- is refused by
// ValidateModelProviders before this method is ever called on valid config.
// Checking the zero value here, rather than len(...) == 0, is deliberate: it
// keeps this method correctly fail-closed (an empty-but-not-absent list
// matches nothing in the ContainsFunc below, and a written-but-null one
// answers false above) even if that validation were ever bypassed.
// Otherwise, an exact, case-insensitive match against a listed repository,
// as before.
func (p ModelProvider) AllowsData(repo string) bool {
	if p.AllowDataTo == nil {
		return !p.allowDataToWritten
	}
	return slices.ContainsFunc(p.AllowDataTo, func(r string) bool { return r == "*" || strings.EqualFold(r, repo) })
}

// OpenToEveryRepo reports whether every repository of the project may use
// p's key and send it code: an absent allow_data_to (the key never written
// at all), or the explicit ["*"] marker -- both mean the same thing (design
// §3a, the owner's ruling). A list written with no repositories in it is
// refused by ValidateModelProviders and is not open.
//
// It exists so the three surfaces that must make this visible -- validate,
// init and doctor -- ask one question, rather than each reading the field:
// before this design an absent list meant nobody, so a config written under
// the old rule silently becomes the widest setting there is.
func (p ModelProvider) OpenToEveryRepo() bool {
	if p.AllowDataTo == nil {
		return !p.allowDataToWritten
	}
	return slices.Contains(p.AllowDataTo, "*")
}

// OpenToEveryRepoReason is why p is open to every repository, as a clause
// for a message whose subject is the provider (validate's Problem.Path,
// init's note, doctor's check), so the three surfaces cannot drift apart in
// wording. The two spellings read differently on purpose: a key never
// written is usually an oversight, while ["*"] is the marker for meaning it.
func (p ModelProvider) OpenToEveryRepoReason() string {
	if p.AllowDataTo == nil {
		return "has no allow_data_to, so every repository of the project may mount its key and send it code (write allow_data_to: [\"*\"] to mean that on purpose, or remove the ambiguity by listing the repositories)"
	}
	return "has allow_data_to: [\"*\"]: every repository of the project may mount its key and send it code, deliberately"
}

// OpenProviders are the providers every repository of the project may use,
// in name order.
func OpenProviders(providers map[string]ModelProvider) []string {
	var out []string
	for _, name := range sortedKeys(providers) {
		if providers[name].OpenToEveryRepo() {
			out = append(out, name)
		}
	}
	return out
}

// PatternMatches reports whether the model pattern (an exact ID, or a prefix
// ending in *) claims model. The gateway's routes use the same function.
func PatternMatches(pat, model string) bool {
	if pre, ok := strings.CutSuffix(pat, "*"); ok {
		return strings.HasPrefix(model, pre)
	}
	return pat == model
}

// PatternsOverlap reports whether some model ID matches both patterns.
func PatternsOverlap(a, b string) bool {
	pa, ga := strings.CutSuffix(a, "*")
	pb, gb := strings.CutSuffix(b, "*")
	switch {
	case ga && gb:
		return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
	case ga:
		return strings.HasPrefix(b, pa)
	case gb:
		return strings.HasPrefix(a, pb)
	}
	return a == b
}

// ProviderFor is the provider whose models claim model, if any. The
// providers do not overlap (ValidateModelProviders), so at most one does.
func ProviderFor(providers map[string]ModelProvider, model string) (name string, p ModelProvider, ok bool) {
	for _, n := range sortedKeys(providers) {
		if providers[n].Matches(model) {
			return n, providers[n], true
		}
	}
	return "", ModelProvider{}, false
}

// keyPrefix is how every model provider's API key begins ("sk-ant-…",
// "sk-proj-…", "sk-…"): a .secret value starting with it is a key pasted where
// its name belongs, and is refused rather than stored as a name.
const keyPrefix = "sk-"

// CheckSecretName reports why name is not a secret name, or "" when it is.
//
// It is the rule a document that names a secret holds every name to, and its
// caller is the project policy's profiles (projectpolicy.validProfiles): a
// name is written into a document every run of a shared job can read, where a
// *value* must never be. Neither message echoes the value: this check exists
// because the value may BE a key, and quoting it would print one into a
// terminal, a log or a halt message, which is the leak the check is for.
//
// ValidateModelProviders does not call this function — the providers block
// words its own two refusals ("the key" the provider holds, and the offending
// prefix quoted back) and adds two rules a profile's secrets have no use for
// (a name Fugaro already uses, and two providers claiming one secret). The
// *rules* are shared all the same — SecretNameRE and keyPrefix below are one
// definition for both — and TestTheSecretNameRuleIsOneRule holds the two
// readers to the same verdict on the same names, so the spellings cannot drift
// apart without a test failing.
func CheckSecretName(name string) string {
	switch {
	case name == "":
		return "must be a secret name, and it is empty: a value is never written where its name belongs"
	case !SecretNameRE.MatchString(name):
		return "must be a secret name (lower-case letters, digits and '-'), the name of the secret that holds the value, never the value"
	case strings.HasPrefix(name, keyPrefix):
		// Every model provider's key begins with "sk-", and the rest of a key
		// is lower-case, digits and dashes often enough to pass the name rule
		// above, so a pasted key would otherwise be accepted and published as
		// the name of a secret (ValidateModelProviders' own comment).
		return "looks like an API key, not the name of a secret: a value must never be written where its name belongs"
	}
	return ""
}

// ValidateModelProviders reports every rule the providers block breaks. Paths
// are relative to the local config ("providers.<name>.<field>").
func ValidateModelProviders(providers map[string]ModelProvider) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	names := sortedKeys(providers)
	secrets := map[string]string{}
	for _, name := range names {
		p := providers[name]
		at := "providers." + name
		if !providerNameRE.MatchString(name) {
			add(at, "provider name must match %s", providerNameRE)
		}
		if name == RouteDirect {
			// The one name the pattern allows and routing cannot carry. A
			// resolved route is the provider entry's *name* (RoutingClaudeFor),
			// and the direct route is spelled with the same string, so a call
			// routed to an entry named "direct" reads as a call that went
			// direct: the resolver fails closed for the name
			// (reservedRouteName), CheckRouting refuses the switch that names a
			// model the entry claims, and a pin carrying the entry's own route
			// ID is refused as a direct pin (internal/config/pins.go). This is
			// that same refusal where the *name* is judged, and it is the one
			// of the three an entry nothing routes to yet reaches: the entry is
			// refused beside its name rather than at the first switch that
			// reads it.
			add(at, "a provider entry may not be named %q: that is the switch's own value for the direct route (routing.claude.via: %s, config.RouteDirect), so a call routed to this entry would read as a call that went direct and the run would halt. Name the entry after the service that serves it (for openrouter-compatible routes, 'openrouter')", name, RouteDirect)
		}
		if p.Kind != ModelProviderKind {
			add(at+".kind", "must be %s", ModelProviderKind)
		}
		if err := checkProviderURL(p.BaseURL); err != nil {
			add(at+".base_url", "%v", err)
		}
		if !slices.Contains([]string{"bearer", "x-api-key"}, p.Auth) {
			add(at+".auth", "must be one of bearer, x-api-key")
		}
		switch {
		case !SecretNameRE.MatchString(p.Secret):
			add(at+".secret", "must be a secret name (lower-case letters, digits and '-'), the name of the secret that holds the key, never the key")
		case strings.HasPrefix(p.Secret, keyPrefix):
			// A secret name may be spelled like one; a key that *is* one is
			// another matter. Every provider's keys start with "sk-" (and
			// the rest of a key is lower-case and dashes often enough to pass
			// the name rule), so a pasted key would otherwise be accepted
			// here, written into the config and stored as the name of a
			// secret — the one thing .secret must never carry.
			//
			// The value is never echoed: this check exists because the value
			// IS a key, and a %q of it would print one into a terminal, a
			// log, a halt message or result.json — the leak the check is
			// for, and the field path already says which value it refuses
			// (M1, the security review of PR #379).
			add(at+".secret", "starts with %q: that is the beginning of an API key, not a secret's name; .secret is the NAME of the secret in Secret Manager that holds the key", keyPrefix)
		case reservedSecretName(p.Secret):
			add(at+".secret", "%q is a secret Fugaro already uses: give the provider key its own name", p.Secret)
		default:
			if other, dup := secrets[p.Secret]; dup {
				add(at+".secret", "%q is already the secret of provider %s: each provider has its own key", p.Secret, other)
			}
			secrets[p.Secret] = name
		}
		if math.IsNaN(p.RouteFeePct) || p.RouteFeePct < 0 || p.RouteFeePct > MaxRouteFeePct {
			add(at+".route_fee_pct", "must be between 0 and %d (a percentage added to every charge)", MaxRouteFeePct)
		}
		if p.PricePolicy != "" && p.PricePolicy != PricePolicyCheapest && p.PricePolicy != PricePolicyHighest {
			add(at+".price_policy", "%q must be one of %s, %s (which end of the built-in price catalog's range to charge; unset means %s)", p.PricePolicy, PricePolicyCheapest, PricePolicyHighest, PricePolicyCheapest)
		}
		if len(p.Models) == 0 {
			add(at+".models", "must list the model IDs the provider serves, such as deepseek/*")
		}
		for _, m := range p.Models {
			if msg := CheckModelPattern(m); msg != "" {
				add(at+".models", "%s", msg)
			}
		}
		if len(p.AllowDataTo) == 0 && (p.allowDataToWritten || p.AllowDataTo != nil) {
			// An absent key and ["*"] both mean "every repository", but a
			// list written with nothing in it cannot be told apart from
			// "not decided yet" (design §3a, round 5). That covers [] and
			// the null spellings alike: `allow_data_to:` with every entry
			// commented out, or `~`, decodes to a nil slice -- the same
			// value as an absent key, and the reason unmarshalling reads
			// the key's presence off the node (round 6). Refuse rather
			// than guess.
			add(at+".allow_data_to", "an explicit empty list is ambiguous: write [\"*\"] for any repository, or remove the key entirely")
		}
		for _, r := range p.AllowDataTo {
			if r != "*" && !repoSlugRE.MatchString(r) {
				add(at+".allow_data_to", "%q must be a repository as owner/name (or \"*\" for every repository)", r)
			}
		}
	}
	ps = append(ps, providerOverlapProblems(providers)...)
	return ps
}

// providerOverlapProblems reports every pair of model patterns that a model
// could match in two providers: a model must belong to one provider
// (ProviderFor returns the first match by name), so the overlap is refused.
// It is the rule ValidateModelProviders applies to the local config and
// ApplyClaudeClaims applies to the config a project layer's claims merged
// into, so a claim that collides is refused at config time rather than by a
// gateway that cannot start.
func providerOverlapProblems(providers map[string]ModelProvider) []Problem {
	var ps []Problem
	names := sortedKeys(providers)
	for i, a := range names {
		for _, b := range names[i+1:] {
			for _, pa := range providers[a].Models {
				for _, pb := range providers[b].Models {
					if PatternsOverlap(pa, pb) {
						ps = append(ps, Problem{Path: "providers." + b + ".models", Message: fmt.Sprintf("%q overlaps %q of provider %s: a model must belong to one provider", pb, pa, a)})
					}
				}
			}
		}
	}
	return ps
}

// ProviderKeyEnvPrefix starts the variable a provider's key is mounted as
// in the runner. It is under FUGARO_, which a workflow secret's variable may
// not use (ReservedEnvPrefixes), and the agent's environment is built from
// an allow-list that never includes it.
const ProviderKeyEnvPrefix = "FUGARO_PROVIDER_KEY_"

// SecretEnv is the variable p's key is mounted as in the runner (only the
// runner and its gateway read it; the agent's environment never has it).
func (p ModelProvider) SecretEnv() string { return ProviderKeyEnv(p.Secret) }

// ProviderKeyEnv is the variable the provider secret named secret is
// mounted as: FUGARO_PROVIDER_KEY_ and the name in upper case with '_' for '-'.
func ProviderKeyEnv(secret string) string {
	return ProviderKeyEnvPrefix + strings.ToUpper(strings.ReplaceAll(secret, "-", "_"))
}

// IsProviderKeyEnv reports whether name is a provider key's variable.
func IsProviderKeyEnv(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), ProviderKeyEnvPrefix)
}

// ProviderBySecret is the provider whose key is the secret named secret.
// Provider secrets are the owner's, in the local config, so a workflow's own
// secrets cannot be checked against them by Validate: the places that hold
// both (the job spec, secrets set) call this.
func ProviderBySecret(providers map[string]ModelProvider, secret string) (name string, ok bool) {
	for _, n := range sortedKeys(providers) {
		if strings.EqualFold(providers[n].Secret, secret) {
			return n, true
		}
	}
	return "", false
}

func reservedSecretName(s string) bool { _, ok := ReservedSecrets[s]; return ok }

// ClaudePatterns are the model patterns no provider may claim: Claude goes
// direct to Anthropic, also under the "anthropic/" vendor prefix. One
// explicit ID is the exception, and only one: a route may name it
// (CheckClaudeClaim).
var ClaudePatterns = []string{"claude-*", "anthropic/*"}

// CheckClaudeClaim is the rule for a route entry that overlaps a Claude
// pattern: the problem, or "" when the entry is the one shape a route may
// claim -- an explicit "anthropic/<id>", and nothing else under that prefix
// (design docs/design/mixed-credentials.md §4, owner ruling 2026-10-14). The
// claim is the entry itself, so this is the whole permission: no separate
// list, no switch. A wildcard ("anthropic/*"), a bare "claude-*" spelling, a
// ":" variant, a "[1m]" spelling or a name the provider-ID grammar rejects is
// still refused, with the message this rule has always had.
//
// It is exported because it is the one place the shape of a claim is written:
// the rule the gateway's routes inherit through CheckModelPattern, and the
// rule a project layer's claim key is judged by (Task 7) are this function,
// not a copy of it.
func CheckClaudeClaim(m string) string {
	// One explicit provider model ID under the prefix, and nothing else. The
	// "*" test is kept deliberately as a second wall: no wildcard is a
	// provider model ID by the pricing grammar, so it is redundant today --
	// but this function *is* the permission, and the grammar it asks is
	// internal/pricing's to widen. A wildcard that ever passed that grammar
	// must still not claim a class of Claude models. The rule also stays
	// readable as the two things the owner ruled out: a class, and a spelling
	// a pin could not name.
	if strings.HasPrefix(m, "anthropic/") && !strings.Contains(m, "*") && pricing.IsProviderModelID(m) {
		return ""
	}
	return fmt.Sprintf("%q would claim Claude models, which go direct to Anthropic", m)
}

// CheckModelPattern is the rule for one entry of a provider's models: a
// model ID, or a prefix ending in *, that claims no Claude model -- no Claude
// pattern, and one explicit ID only when it is a claim (CheckClaudeClaim) --
// and is not too broad. It returns the problem, or "" when the pattern is
// fine. The gateway's routes apply the same rule, so a config that validates
// here always starts there: the rule lives here and nowhere else.
func CheckModelPattern(m string) string {
	if !modelPatternRE.MatchString(m) || len(m) > 100 {
		return fmt.Sprintf("%q must be a model ID, or a prefix ending in * such as deepseek/*", m)
	}
	for _, c := range ClaudePatterns {
		if PatternsOverlap(m, c) {
			return CheckClaudeClaim(m)
		}
	}
	if strings.Count(m, "*") == 1 && len(m) < 3 {
		return fmt.Sprintf("%q is too broad: name a prefix such as deepseek/*", m)
	}
	return ""
}

// CheckProviderBaseURL is the rule for a provider's base URL, shared with
// the gateway: https, or http on a loopback host (tests); a path prefix is
// allowed ("https://openrouter.ai/api"); no credentials, query (not even an
// empty "?"), fragment, escaped path or dot segment.
func CheckProviderBaseURL(s string) error { return checkProviderURL(s) }

func checkProviderURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("%q must be a URL with a scheme and a host, no credentials, query or fragment", s)
	}
	if u.RawPath != "" || strings.ContainsAny(u.Path, "\\%") {
		return fmt.Errorf("%q has an escaped path", s)
	}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%q has a dot segment", s)
		}
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("%q must be https (http only on a loopback address, for tests)", s)
}

// CheckProviderAuth is the auth rule for models routed to a provider: the
// coder, the reviewer and the background model that a provider claims need
// agent.auth api-key. oauth is a subscription credential that cannot leave
// Anthropic, and vertex is Claude on Vertex; neither can carry a provider
// key, and a run does not mix credentials.
func CheckProviderAuth(a Agent, providers map[string]ModelProvider) []Problem {
	var ps []Problem
	if a.Auth == "api-key" {
		return nil
	}
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", a.Models.Background},
	} {
		name, _, ok := ProviderFor(providers, r.model)
		if !ok {
			continue
		}
		why := "vertex serves Claude only and a run does not mix credentials"
		if a.Auth == "oauth" {
			why = "an oauth token is a Claude subscription credential and is never sent to another provider"
		}
		ps = append(ps, Problem{Path: r.path, Message: fmt.Sprintf("%s is served by provider %s, which needs agent.auth: api-key (agent.auth is %s: %s)", CodeSpan(r.model), name, a.Auth, why)})
	}
	return ps
}

// CheckProviderGateway refuses a provider model unless the budget gateway is
// on and enforcing. The gateway holds the key and routes the call, so without
// it the call would go to Anthropic; and observe never refuses a call, while
// a provider's calls are real dollars (and an injected agent could send what
// the pins would stop), so the owner's default is enforce. mode is the run's
// effective budget mode ("" and "off" are the gateway off); bg is the run's
// effective background model. One problem per role that names a provider
// model.
func CheckProviderGateway(a Agent, bg string, providers map[string]ModelProvider, mode string) []Problem {
	if mode == "enforce" {
		return nil
	}
	why := "which needs the budget gateway: set budget.mode to enforce (with agent.auth api-key) and a per-run cap; without it the call would go to Anthropic"
	if mode == "observe" {
		why = "which needs budget.mode enforce: observe never refuses a call and a provider's calls are real dollars. Set budget.mode: enforce with budget.per_run_usd in the project config and run `fugaro init --repo` (or budget.mode: enforce in fugaro.yaml); `fugaro budget set --global --mode enforce` makes the shared database enforce too (it needs the global --daily and --per-run caps)"
	}
	var ps []Problem
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", bg},
	} {
		if _, _, ok := ProviderFor(providers, r.model); ok {
			ps = append(ps, Problem{Path: r.path, Message: fmt.Sprintf("%s is served by a provider, %s", CodeSpan(r.model), why)})
		}
	}
	return ps
}

// CheckProviderPolicy is the repository's side of a provider model (design
// m10-multi-model.md §3): a model ID that is not Claude's (a "vendor/model"
// ID) is accepted in the file's pins (agent.models.*, agent.model) and in its
// budget.allowed_models only when a provider claims it, that provider's
// allow_data_to names repo (owner/name), and it is a plain priced ID (no
// alias, no ":" variant). Otherwise the run is refused before any call. The
// providers are the owner's (local config): a repository cannot add one,
// and an allow-list that merges away a model keeps it out (CheckAllowed).
// allowed is the file's own allow-list, nil when it sets none; prices may be
// nil, which skips the price rule.
func CheckProviderPolicy(a Agent, allowed []string, repo string, providers map[string]ModelProvider, prices *pricing.Table) []Problem {
	var ps []Problem
	check := func(path, model string) {
		if !strings.Contains(model, "/") && !claimed(providers, model) {
			return // a Claude ID or alias: the pin rules are CheckPins'
		}
		name, p, ok := ProviderFor(providers, model)
		switch {
		case !ok:
			ps = append(ps, Problem{Path: path, Message: unclaimedModelRefusal(model)})
		case !p.AllowsData(repo):
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("%s is served by provider %s, whose allow_data_to does not name %s: the owner must list the repository in the project config before its code may be sent there", CodeSpan(model), name, CodeSpan(repo))})
		case strings.Contains(model, ":"):
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("%s is a variant (a \":\" suffix is another price and routing): name the plain vendor/model ID", CodeSpan(model))})
		case pricing.IsAlias(model):
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("%s is not an explicit model ID: name a plain vendor/model ID", CodeSpan(model))})
		case prices != nil:
			if _, ok := prices.Lookup(model); !ok {
				// A model the catalog refused says why — the switch to
				// flip and both ways out (P8) — rather than only naming
				// model_prices.
				msg := fmt.Sprintf("%s has no price: add it under model_prices in the project config", CodeSpan(model))
				if why := prices.Refusal(model); why != "" {
					msg = why
				}
				ps = append(ps, Problem{Path: path, Message: msg})
			}
		}
	}
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", a.Models.Background},
		{"agent.model", a.Model},
	} {
		// agent.model is the role models' default: said once, under the role.
		if r.path == "agent.model" && (r.model == a.ModelFor(RoleCoder) || r.model == a.ModelFor(RoleReviewer)) {
			continue
		}
		if r.model != "" {
			check(r.path, r.model)
		}
	}
	for i, m := range allowed {
		check(fmt.Sprintf("budget.allowed_models[%d]", i), m)
	}
	return ps
}

// unclaimedModelRefusal is the refusal for a model no provider route serves.
// A model that would be a claim if a route named it (CheckClaudeClaim's one
// shape) is told the truth about itself instead of being called "not a Claude
// model": it is a Claude model, Claude models go direct to Anthropic, and
// only the owner can change that, by naming that exact ID in a route's models
// in the project config, or in the project layer's claude_models for a route
// the project config already defines. Every other model keeps the original
// wording, whose substring "no provider serves it" three tests read.
func unclaimedModelRefusal(model string) string {
	if CheckClaudeClaim(model) == "" {
		return fmt.Sprintf("%s is a Claude model: Claude models go direct to Anthropic and no provider serves it. Only the owner can change that, by naming that exact ID in a route's models in the project config (or in the project layer's claude_models, for a route the project config already defines), never in fugaro.yaml", CodeSpan(model))
	}
	return fmt.Sprintf("%s is not a Claude model and no provider serves it: providers are set by the owner in the project config, never in fugaro.yaml", CodeSpan(model))
}

func claimed(providers map[string]ModelProvider, model string) bool {
	_, _, ok := ProviderFor(providers, model)
	return ok
}

// NeedsAnthropicKey reports whether a run on this configuration sends a call
// to Anthropic, and so needs the Anthropic API key. The model ID alone decides
// the route, exactly as the gateway decides it (internal/gateway/route.go): a
// model no allowed provider claims goes direct. So a Claude pin, or a model no
// route serves, needs the key, and a repository whose pins are all served by
// providers it may send code to does not (design docs/design/project-centric.md
// §6.1). repo "" is an unknown repository, which no provider may serve.
//
// Callers ask this only where the gateway is on and enforcing: without it a
// provider model is refused (CheckProviderGateway) and every call would go to
// Anthropic, which AnthropicKeyNeeded decides.
func NeedsAnthropicKey(a Agent, providers map[string]ModelProvider, repo string) bool {
	for _, model := range []string{a.ModelFor(RoleCoder), a.ModelFor(RoleReviewer), EffectiveBackground(a, providers)} {
		if _, p, ok := ProviderFor(providers, model); !ok || !p.AllowsData(repo) {
			return true
		}
	}
	return false
}

// AnthropicKeyNeeded is the whole rule, and the one both callers ask: does a
// run on this configuration need the Anthropic API key stored? Only an
// api-key run ever does (oauth has its own token, Vertex the job's account),
// and only when a call actually goes to Anthropic: only enforce routes a call
// to a provider (CheckProviderGateway refuses a provider model in any other
// mode), so under any other mode every call goes to Anthropic whatever the
// pins are -- and under enforce it is the pins, exactly as NeedsAnthropicKey
// reads them. mode is the effective one (the project's ceiling merged with the
// file), the same value the runner and validate compute.
func AnthropicKeyNeeded(a Agent, providers map[string]ModelProvider, repo, mode string) bool {
	if a.Auth != "api-key" {
		return false
	}
	return mode != policy.ModeEnforce || NeedsAnthropicKey(a, providers, repo)
}

// EffectiveBackground is the model of Claude Code's background (haiku-role)
// requests. The file's agent.models.background when set. Otherwise, with a
// coder a provider serves, the coder's own model: the alternative is Claude
// Code's built-in Claude haiku, a hidden call to Anthropic that no pin allows
// and that a run on a provider is not expected to make. With a Claude coder
// it stays unset, as it always was.
func EffectiveBackground(a Agent, providers map[string]ModelProvider) string {
	if a.Models.Background != "" {
		return a.Models.Background
	}
	if coder := a.ModelFor(RoleCoder); claimed(providers, coder) {
		return coder
	}
	return ""
}
