package config

import (
	"strings"
	"testing"
)

// claudeRoute is a provider entry that claims one explicit Claude ID, the shape
// docs/design/mixed-credentials.md §4 requires of an opt-in route entry. It is
// built in Go rather than parsed: the claim is what #359's phase 2 lets a
// provider's models carry, and this task resolves and validates the switch
// against it, not against the config's own spelling of a claim.
func claudeRoute(allow []string) ModelProvider {
	return ModelProvider{
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
		AllowDataTo: allow,
	}
}

const canonicalClaude = "claude-sonnet-5-5"

// TestRoutingAbsentIsDirect: an absent switch, an empty one and via: direct all
// mean today's behaviour (D1b) — and so does a Claude ID the list does not
// name, because the list is the opt-in (D1a).
func TestRoutingAbsentIsDirect(t *testing.T) {
	routed := map[string]ModelProvider{"openrouter": claudeRoute([]string{"*"})}
	for _, tc := range []struct {
		name string
		c    *RoutingClaude
	}{
		{"no switch at all", nil},
		{"an empty switch", &RoutingClaude{}},
		{"via: direct", &RoutingClaude{Via: RouteDirect}},
		{"via: direct with a list", &RoutingClaude{Via: RouteDirect, Models: []string{canonicalClaude}}},
		{"via: openrouter naming another model", &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-opus-5-5"}}},
	} {
		route, sent, ok := RoutingClaudeFor(tc.c, canonicalClaude, routed)
		if route != RouteDirect || sent != canonicalClaude || !ok {
			t.Errorf("%s: RoutingClaudeFor = (%q, %q, %v), want (%q, %q, true)", tc.name, route, sent, ok, RouteDirect, canonicalClaude)
		}
	}
	// And the switch's own absent meaning: via unset is direct even with a list.
	c := &RoutingClaude{Models: []string{canonicalClaude}}
	if route, _, ok := RoutingClaudeFor(c, canonicalClaude, routed); route != RouteDirect || !ok {
		t.Errorf("via unset with a list: route = %q, ok = %v, want direct", route, ok)
	}
}

// TestRoutingRefusesAGlob: D1a's opt-in is an explicit list of Claude IDs, never
// a pattern — a glob, a bare provider ID or a variant suffix is refused with a
// message that says which ruling the rule comes from. (The plan's §9 spells this
// test TestRoutingClaudeRefusesAGlob; it is the same rule, asserted here at the
// config level where the switch is validated.)
func TestRoutingRefusesAGlob(t *testing.T) {
	routed := map[string]ModelProvider{"openrouter": claudeRoute([]string{"*"})}
	for _, tc := range []struct{ name, model, want string }{
		{"a glob", "claude-*", "explicit list"},
		{"a bare prefix", "claude-", "not a Claude model ID"},
		{"a vendor glob", "anthropic/*", "explicit list"},
		{"the route's own ID", "anthropic/claude-sonnet-5.5", "route ID, not the canonical one"},
		{"a variant", "claude-sonnet-5-5:free", "variant"},
		{"an alias", "sonnet", "not a Claude model ID"},
		{"not Claude at all", "deepseek/deepseek-v4-flash", "not the canonical one"},
	} {
		ps := CheckRouting(&Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{tc.model}}}, routed, "routing")
		got := problemsText(ps)
		if len(ps) == 0 {
			t.Errorf("%s (%q): accepted, want refused", tc.name, tc.model)
			continue
		}
		if !strings.Contains(got, "routing.claude.models") || !strings.Contains(got, tc.want) {
			t.Errorf("%s (%q): problems = %q, want the path and %q", tc.name, tc.model, got, tc.want)
		}
	}
	// An empty list with via: openrouter is the pattern-like opt-in D1a refuses:
	// it would mean "every Claude model".
	got := problemsText(CheckRouting(&Routing{Claude: &RoutingClaude{Via: RouteOpenRouter}}, routed, "routing"))
	if !strings.Contains(got, "routing.claude.models") || !strings.Contains(got, "empty") {
		t.Errorf("via: openrouter with no list: problems = %q, want a refusal naming the empty list", got)
	}
	// A via this binary does not know is refused rather than guessed at.
	got = problemsText(CheckRouting(&Routing{Claude: &RoutingClaude{Via: "elsewhere", Models: []string{canonicalClaude}}}, routed, "routing"))
	if !strings.Contains(got, "routing.claude.via") {
		t.Errorf("via: elsewhere: problems = %q, want a refusal naming routing.claude.via", got)
	}
}

// TestRoutingRefusesOpenrouterWithoutAllowDataTo: D1b's safeguard is real, not
// nominal — a route resolves to a provider only when that provider's
// allow_data_to is *written* (a list, or the explicit ["*"]). Under the owner's
// ruling an absent key already means every repository, so an absent key here is
// exactly the state the safeguard exists to refuse.
func TestRoutingRefusesOpenrouterWithoutAllowDataTo(t *testing.T) {
	switchOn := &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{canonicalClaude}}}
	for _, tc := range []struct {
		name  string
		route ModelProvider
	}{
		{"allow_data_to absent", ModelProvider{Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"}}},
		{"allow_data_to written with nothing under it", func() ModelProvider {
			p := claudeRoute(nil)
			p.allowDataToWritten = true
			return p
		}()},
	} {
		got := problemsText(CheckRouting(switchOn, map[string]ModelProvider{"openrouter": tc.route}, "routing"))
		if !strings.Contains(got, "allow_data_to") || !strings.Contains(got, "openrouter") {
			t.Errorf("%s: problems = %q, want a refusal naming allow_data_to and the provider", tc.name, got)
		}
		// And the resolver agrees: no route to take, rather than a silent
		// fall back to the direct path.
		if route, _, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, map[string]ModelProvider{"openrouter": tc.route}); ok || route != "" {
			t.Errorf("%s: RoutingClaudeFor = (%q, ok %v), want no resolution", tc.name, route, ok)
		}
	}
	// Written as a list, and as the explicit wildcard: both resolve.
	for _, allow := range [][]string{{"*"}, {"edgeappinc/fugarosandbox"}} {
		providers := map[string]ModelProvider{"openrouter": claudeRoute(allow)}
		if ps := CheckRouting(switchOn, providers, "routing"); len(ps) != 0 {
			t.Errorf("allow_data_to %v: problems = %s, want none", allow, problemsText(ps))
		}
		route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, providers)
		if !ok || route != "openrouter" || sent != "anthropic/claude-sonnet-5.5" {
			t.Errorf("allow_data_to %v: RoutingClaudeFor = (%q, %q, %v), want (openrouter, anthropic/claude-sonnet-5.5, true)", allow, route, sent, ok)
		}
	}
	// A switch that names an ID no provider claims is refused too: the route it
	// promises does not exist, and the fix is to name the ID in a route entry.
	got := problemsText(CheckRouting(switchOn, map[string]ModelProvider{"openrouter": okProvider()}, "routing"))
	if !strings.Contains(got, canonicalClaude) || !strings.Contains(got, "claims") {
		t.Errorf("unclaimed ID: problems = %q, want a refusal naming the model and the missing claim", got)
	}
	if _, _, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, map[string]ModelProvider{"openrouter": okProvider()}); ok {
		t.Error("an unclaimed routed ID resolved")
	}
}

// TestRoutingResolvesOnlyTheClaimedModel: the claim is per model — a route that
// claims one Claude ID does not carry another — and via: auto resolves the same
// way openrouter does (the caller that knows whether an Anthropic key exists is
// what prefers the direct path, Task 1b.3).
func TestRoutingResolvesOnlyTheClaimedModel(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": claudeRoute([]string{"*"})}
	auto := &RoutingClaude{Via: RouteAuto, Models: []string{canonicalClaude}}
	if route, sent, ok := RoutingClaudeFor(auto, canonicalClaude, providers); !ok || route != "openrouter" || sent != "anthropic/claude-sonnet-5.5" {
		t.Errorf("auto: RoutingClaudeFor = (%q, %q, %v), want the route's ID", route, sent, ok)
	}
	// The same switch, an ID it names, but the route claims the other model.
	other := map[string]ModelProvider{"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"anthropic/claude-opus-5.5"}, AllowDataTo: []string{"*"}}}
	if route, _, ok := RoutingClaudeFor(auto, canonicalClaude, other); ok {
		t.Errorf("a route claiming another model resolved to %q", route)
	}
}

// TestRepoCannotWriteRouting: the switch lives in the operator's config and the
// project layer, and nowhere else — a repository's fugaro.yaml cannot carry it,
// exactly as it cannot carry providers (TestRepoCannotAddProvider).
func TestRepoCannotWriteRouting(t *testing.T) {
	for _, doc := range []string{
		"routing:\n  claude: {via: openrouter, models: [claude-sonnet-5-5]}\n",
		"routing:\n  claude: {via: direct}\n",
		"agent:\n  routing: {claude: {via: direct}}\n",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
		}
	}
}
