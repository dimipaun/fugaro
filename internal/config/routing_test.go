package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// An empty list under either open via is the pattern-like opt-in D1a
	// refuses: it would mean "every Claude model". auto is held to it too — a
	// switch that names no model routes nothing, so a node carrying one reads
	// as routed while every call goes direct (the same silent-inert state a
	// misspelt routing member produces, projectpolicy's validRoutingMembers).
	for _, via := range []string{RouteOpenRouter, RouteAuto} {
		got := problemsText(CheckRouting(&Routing{Claude: &RoutingClaude{Via: via}}, routed, "routing"))
		if !strings.Contains(got, "routing.claude.models") || !strings.Contains(got, "empty") || !strings.Contains(got, via) {
			t.Errorf("via: %s with no list: problems = %q, want a refusal naming the empty list and the via", via, got)
		}
	}
	// A list under via: direct stays inert rather than refused: it names
	// nothing that is routed, and D1b's default is where an unwritten switch
	// leaves every call.
	if got := problemsText(CheckRouting(&Routing{Claude: &RoutingClaude{Via: RouteDirect, Models: []string{canonicalClaude}}}, routed, "routing")); got != "" {
		t.Errorf("via: direct with a list: problems = %q, want none", got)
	}
	// A via this binary does not know is refused rather than guessed at.
	got := problemsText(CheckRouting(&Routing{Claude: &RoutingClaude{Via: "elsewhere", Models: []string{canonicalClaude}}}, routed, "routing"))
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

// TestRepoCannotWriteRouting: the switch is written in the operator's config
// and nowhere else — a repository's fugaro.yaml cannot carry it, exactly as it
// cannot carry providers (TestRepoCannotAddProvider).
//
// Every document here is minimalYAML — the fixture the rest of this package
// parses as valid — with the routing key appended, so the *routing key* is the
// only reason Parse refuses it. (The first version of this test used bare
// documents that failed Parse for missing version/project/workflows anyway,
// which is no test at all: the review of PR #414. Hand mutation to see it red:
// give config.Config a `Routing *Routing` field with `yaml:"routing"`, and every
// case below parses cleanly and this test fails.)
func TestRepoCannotWriteRouting(t *testing.T) {
	if _, ps := Parse([]byte(minimalYAML)); len(ps) != 0 {
		t.Fatalf("the fixture is not valid, so a refusal below would prove nothing: %v", ps)
	}
	for _, doc := range []string{
		minimalYAML + "routing:\n  claude: {via: openrouter, models: [claude-sonnet-5-5]}\n",
		minimalYAML + "routing:\n  claude: {via: direct}\n",
		minimalYAML + "routing: {}\n",
		minimalYAML + "agent:\n  routing: {claude: {via: direct}}\n",
	} {
		_, ps := Parse([]byte(doc))
		if len(ps) == 0 {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
			continue
		}
		// And refused *for the routing key*, not for something else: the
		// strict decoder names the field it does not know.
		if got := problemsText(ps); !strings.Contains(got, "routing") {
			t.Errorf("refused without naming the key:\n%s\nproblems = %q", doc, got)
		}
	}
	// The project layer is the other site D1a names, and it is not open in this
	// release either: the layer's own decode is strict, so a layer that writes
	// routing fails closed instead of half-applying.
	if _, ps := ParseProjectLayer([]byte("version: 1\nproject: aurora\nrouting: {claude: {via: openrouter, models: [claude-sonnet-5-5]}}\n"), LayerAnchor{}); len(ps) == 0 {
		t.Error("a project layer carrying routing: was accepted")
	}
}

// TestRoutingRefusesAModelTwoRoutesClaim: two provider entries can claim one
// Claude model under different spellings (anthropic/claude-sonnet-5.5 and
// anthropic/claude-sonnet-5-5 are the same model), and a resolver that took the
// first would route by provider name — whichever sorts first — so validation
// passed or failed by an alphabetical accident. Both spellings are refused,
// naming both entries, and the resolver fails closed rather than picking one.
func TestRoutingRefusesAModelTwoRoutesClaim(t *testing.T) {
	claims := map[string]ModelProvider{
		"aaa-openrouter": {Kind: ModelProviderKind, BaseURL: "https://a.example/api", Auth: "bearer", Secret: "a-key", Models: []string{"anthropic/claude-sonnet-5.5"}, AllowDataTo: []string{"*"}},
		"zzz-openrouter": {Kind: ModelProviderKind, BaseURL: "https://z.example/api", Auth: "bearer", Secret: "z-key", Models: []string{"anthropic/claude-sonnet-5-5"}, AllowDataTo: []string{"*"}},
	}
	switchOn := &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{canonicalClaude}}}
	got := problemsText(CheckRouting(switchOn, claims, "routing"))
	for _, want := range []string{"routing.claude.models", "two provider entries", "aaa-openrouter", "zzz-openrouter", "anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems = %q, want %q", got, want)
		}
	}
	if route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, claims); ok || route != "" || sent != "" {
		t.Errorf("RoutingClaudeFor = (%q, %q, %v), want no resolution: an ambiguous claim must not resolve", route, sent, ok)
	}
	// One entry claiming it twice (the two spellings on one route) is one
	// entry, and resolves: the ambiguity is between *routes*, and there is one.
	both := map[string]ModelProvider{"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5"}, AllowDataTo: []string{"*"}}}
	if ps := CheckRouting(switchOn, both, "routing"); len(ps) != 0 {
		t.Errorf("one entry claiming the model twice: problems = %s, want none", problemsText(ps))
	}
	if route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, both); !ok || route != "openrouter" || sent != "anthropic/claude-sonnet-5.5" {
		t.Errorf("one entry claiming the model twice: RoutingClaudeFor = (%q, %q, %v), want the entry's first spelling", route, sent, ok)
	}
}

// TestRoutingHasOneResolver: the canonical ID and a route's own spelling of it
// are compared in exactly one file. That comparison *is* the answer to where a
// Claude call goes, so a second one is a second answer — the pins renderer, the
// price lookup and validate are all supposed to ask RoutingClaudeFor, not to
// re-derive the route (Task 1b.2/1b.3 say the same in the plan). The marker is
// the pair the comparison needs: the vendor prefix stripped off a claim to get
// the route's spelling (a CutPrefix with the "anthropic/" literal — not
// pricing.IsAlias's HasPrefix predicate, which only judges the spelling).
func TestRoutingHasOneResolver(t *testing.T) {
	const resolver = "routing.go"
	marker := regexp.MustCompile(`CutPrefix\([^)]*"anthropic/"`)
	var found []string
	err := filepath.WalkDir("../../internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if marker.Match(src) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	if len(found) != 1 || filepath.Base(found[0]) != resolver {
		t.Errorf("the canonical-to-route comparison lives in %v, want exactly internal/config/%s: the route is resolved in one place (RoutingClaudeFor), and a second site is a second answer to where a Claude call goes", found, resolver)
	}
}
