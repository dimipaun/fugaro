package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
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

// ModelProvider is one entry of the local config's providers block.
type ModelProvider struct {
	Kind string `yaml:"kind"`
	// BaseURL is https, or http on a loopback host (the fake in tests).
	BaseURL string `yaml:"base_url"`
	// Auth is how the credential is sent: "bearer" (Authorization) or
	// "x-api-key".
	Auth string `yaml:"auth"`
	// Secret is the logical name of the secret holding the key, never the
	// key itself.
	Secret string `yaml:"secret"`
	// RouteFeePct is added to every charge on this provider; 0 is none.
	RouteFeePct float64 `yaml:"route_fee_pct,omitempty"`
	// Models are the model IDs the provider serves: exact IDs, or a prefix
	// ending in "*" such as "deepseek/*". No two providers may overlap.
	Models []string `yaml:"models"`
	// AllowDataTo are the repositories (owner/name) whose code may be sent
	// here; empty means none.
	AllowDataTo []string `yaml:"allow_data_to,omitempty"`
}

var (
	providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	modelPatternRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*\*?$`)
	repoSlugRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// Matches reports whether the provider serves model.
func (p ModelProvider) Matches(model string) bool {
	return slices.ContainsFunc(p.Models, func(pat string) bool { return patternMatches(pat, model) })
}

// AllowsData reports whether repo (owner/name) may send code to p.
func (p ModelProvider) AllowsData(repo string) bool { return slices.Contains(p.AllowDataTo, repo) }

func patternMatches(pat, model string) bool {
	if pre, ok := strings.CutSuffix(pat, "*"); ok {
		return strings.HasPrefix(model, pre)
	}
	return pat == model
}

// patternsOverlap reports whether some model ID matches both patterns.
func patternsOverlap(a, b string) bool {
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
		if len(p.Models) == 0 {
			add(at+".models", "must list the model IDs the provider serves, such as deepseek/*")
		}
		for _, m := range p.Models {
			switch {
			case !modelPatternRE.MatchString(m) || len(m) > 100:
				add(at+".models", "%q must be a model ID, or a prefix ending in * such as deepseek/*", m)
			case patternsOverlap(m, "claude-*"):
				add(at+".models", "%q would claim Claude models, which go direct to Anthropic", m)
			case strings.Count(m, "*") == 1 && len(m) < 3:
				add(at+".models", "%q is too broad: name a prefix such as deepseek/*", m)
			}
		}
		for _, r := range p.AllowDataTo {
			if !repoSlugRE.MatchString(r) {
				add(at+".allow_data_to", "%q must be a repository as owner/name", r)
			}
		}
	}
	for i, a := range names {
		for _, b := range names[i+1:] {
			for _, pa := range providers[a].Models {
				for _, pb := range providers[b].Models {
					if patternsOverlap(pa, pb) {
						add("providers."+b+".models", "%q overlaps %q of provider %s: a model must belong to one provider", pb, pa, a)
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
		if providers[n].Secret == secret {
			return n, true
		}
	}
	return "", false
}

func reservedSecretName(s string) bool { _, ok := ReservedSecrets[s]; return ok }

func checkProviderURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q must be a URL with a scheme and a host, no credentials, query or fragment", s)
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
