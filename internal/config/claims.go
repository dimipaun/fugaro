package config

import (
	"maps"
	"slices"
)

// ApplyClaudeClaims merges a project layer's claude_models into the owner's
// providers (design docs/design/mixed-credentials.md §4, the owner's ruling
// of 2026-10-14): the claims are the layer's way of saying which Claude
// model each route the owner already defined also serves. The result is a
// copy of providers whose named routes have the claimed IDs appended, in
// pattern order and deduplicated, so a route that already names the ID is
// unchanged.
//
// The layer may only add to a route the local config defines (P8): a name
// the local config does not have is a problem naming it, never a silent
// no-op and never an added route, so base_url, auth, secret and allow_data_to
// stay where the owner put them.
//
// The merged result is then checked against the providers' own overlap rule
// (ValidateModelProviders applies the same one to the local config): a claim
// that collides with another provider's pattern is a config-time problem
// here, rather than a gateway that refuses to start the run.
//
// An empty claims map returns providers unchanged (the same map, not a
// copy), so every path that has no layer is byte-identical to before.
func ApplyClaudeClaims(providers map[string]ModelProvider, claims map[string][]string) (map[string]ModelProvider, []Problem) {
	if len(claims) == 0 {
		return providers, nil
	}
	out := make(map[string]ModelProvider, len(providers))
	for name, p := range providers {
		p.Models = slices.Clone(p.Models)
		out[name] = p
	}
	var ps []Problem
	for _, name := range sortedKeys(claims) {
		p, ok := out[name]
		if !ok {
			ps = append(ps, Problem{Path: "claude_models." + showKey(name), Message: "names provider " + showKey(name) + ", which the project config does not define: a project layer may add a Claude model to a route the owner already defined, never create one (base_url, auth, secret and allow_data_to are the owner's)"})
			continue
		}
		if len(claims[name]) == 0 {
			ps = append(ps, Problem{Path: "claude_models." + showKey(name), Message: "must list at least one Claude model ID (an explicit anthropic/<id>)"})
			continue
		}
		for _, m := range claims[name] {
			if !slices.Contains(p.Models, m) {
				p.Models = append(p.Models, m)
			}
		}
		out[name] = p
	}
	ps = append(ps, providerOverlapProblems(out)...)
	if len(ps) > 0 {
		return out, ps
	}
	return out, nil
}

// ClaudeClaimsOf is l's claude_models, nil when l is nil, so a caller that
// has no layer asks for claims without asking about the layer twice.
func ClaudeClaimsOf(l *ProjectLayer) map[string][]string {
	if l == nil {
		return nil
	}
	return maps.Clone(l.ClaudeModels)
}
