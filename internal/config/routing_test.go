package config

import (
	"go/ast"
	"go/token"
	"slices"
	"strconv"
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
	// The count is the number of entries that claim it, not the word "two":
	// three entries claiming one model is the same defect, and it names all
	// three (the review finding on the hard-coded "two provider entries").
	for _, want := range []string{"routing.claude.models", "claimed by 2 provider entries", "aaa-openrouter", "zzz-openrouter", "anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems = %q, want %q", got, want)
		}
	}
	claims["mmm-openrouter"] = ModelProvider{Kind: ModelProviderKind, BaseURL: "https://m.example/api", Auth: "bearer", Secret: "m-key", Models: []string{"anthropic/claude-sonnet-5.5"}, AllowDataTo: []string{"*"}}
	got = problemsText(CheckRouting(switchOn, claims, "routing"))
	if !strings.Contains(got, "claimed by 3 provider entries") || !strings.Contains(got, "mmm-openrouter") {
		t.Errorf("three claims: problems = %q, want all three entries named with their count", got)
	}
	if route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, claims); ok || route != "" || sent != "" {
		t.Errorf("RoutingClaudeFor = (%q, %q, %v), want no resolution: an ambiguous claim must not resolve", route, sent, ok)
	}
	// One entry claiming it twice (the two spellings on one route) is one
	// entry, and resolves: the ambiguity is between *routes*, and there is one.
	//
	// The two orders are the same entry and must read the same way. Taking the
	// first match inside the entry made them different — the order that listed
	// the route's own ID first resolved, and the same two written the other way
	// round refused the switch, because the claim the run is judged and sent by
	// was the first spelling rather than the map's pair (review finding on this
	// change). Hand mutation this fails on: restoring the `break` after the
	// first same-model match, which makes the second case below refused.
	for _, order := range [][]string{
		{"anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5"},
		{"anthropic/claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"},
	} {
		both := map[string]ModelProvider{"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key", Models: order, AllowDataTo: []string{"*"}}}
		if ps := CheckRouting(switchOn, both, "routing"); len(ps) != 0 {
			t.Errorf("one entry claiming the model twice as %v: problems = %s, want none", order, problemsText(ps))
		}
		if route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, both); !ok || route != "openrouter" || sent != "anthropic/claude-sonnet-5.5" {
			t.Errorf("one entry claiming the model twice as %v: RoutingClaudeFor = (%q, %q, %v), want the map's pair for it (anthropic/claude-sonnet-5.5), whatever order the entry lists the two in", order, route, sent, ok)
		}
	}
}

// TestRoutingRefusesAProviderNamedDirect: the resolver answers with a provider
// entry's own name as the route, and RouteDirect ("direct") is the resolver's
// value for the direct route — so an entry named direct makes "the route this
// call takes" and "calls go direct" one string, and the consumers that tell the
// two apart by comparing the route with RouteDirect (internal/runner's routing
// check, internal/config's pin admission, allowed) read a routed model as
// unrouted: the run halts at bootstrap with "this run cannot resolve its
// route", naming a cause that is not the real one. The name passed every check
// there was — providerNameRE accepts it, the loader has no reserved-name rule,
// CheckRouting judged the switch by the claim alone — so nothing refused it
// before this (the review finding on this change).
func TestRoutingRefusesAProviderNamedDirect(t *testing.T) {
	switchOn := &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{canonicalClaude}}}
	named := map[string]ModelProvider{RouteDirect: claudeRoute([]string{"*"})}
	got := problemsText(CheckRouting(switchOn, named, "routing"))
	for _, want := range []string{"routing.claude.models", canonicalClaude, RouteDirect, "Rename the provider entry"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems = %q, want %q: the refusal has to name the entry and the remedy", got, want)
		}
	}
	if route, sent, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, named); ok || route != "" || sent != "" {
		t.Errorf("RoutingClaudeFor = (%q, %q, %v), want no resolution: a route that is the direct route's own name must not be answered with", route, sent, ok)
	}
	// The other via words are not reserved: the resolver never returns one as
	// the route, so a provider entry may be called openrouter (a fixture's own
	// provider is) or auto, and a route by either name is a route.
	for _, name := range []string{RouteOpenRouter, RouteAuto} {
		one := map[string]ModelProvider{name: claudeRoute([]string{"*"})}
		if ps := CheckRouting(switchOn, one, "routing"); len(ps) != 0 {
			t.Errorf("a provider named %s: problems = %s, want none", name, problemsText(ps))
		}
		if route, _, ok := RoutingClaudeFor(switchOn.Claude, canonicalClaude, one); !ok || route != name {
			t.Errorf("a provider named %s: RoutingClaudeFor route = %q, ok = %v, want %q", name, route, ok, name)
		}
	}
	// The name is refused where it would be a call's route, not everywhere: a
	// switch that routes nothing (via: direct) is untouched by an entry by that
	// name, and so is a switch whose list does not name the model the entry
	// claims.
	direct := &Routing{Claude: &RoutingClaude{Via: RouteDirect, Models: []string{canonicalClaude}}}
	if ps := CheckRouting(direct, named, "routing"); len(ps) != 0 {
		t.Errorf("via: direct beside a provider named %s: problems = %s, want none — nothing is routed through it", RouteDirect, problemsText(ps))
	}
	if route, sent, ok := RoutingClaudeFor(direct.Claude, canonicalClaude, named); !ok || route != RouteDirect || sent != canonicalClaude {
		t.Errorf("via: direct beside a provider named %s: RoutingClaudeFor = (%q, %q, %v), want the direct route", RouteDirect, route, sent, ok)
	}
}

// TestRoutingHasOneResolver: the spelling that answers "which route does this
// Claude model take?" — the "anthropic/" vendor prefix a route entry's claim
// carries — is written in code in exactly four files, each of which uses it as
// something other than a derivation, and *nowhere* is a route's ID built from
// it. That derivation is the second answer to where a call goes: a spelling
// assembled here would be sent and charged on a route the map never paired the
// model with, and nothing would say so.
//
// The files that may write it, and why each is not a derivation: the resolver
// (which strips it off a claim to ask the map), the claim-shape judge in
// providers.go (which only tests for it), the map's own data (its route IDs are
// these literals), and the generator's detection of a Claude catalog row.
//
// This resolves each file's imports and reads string literals, where the text
// search it replaces matched a regexp against the whole file: the search stayed
// green for a route ID built from the literal elsewhere, and for the same code
// reached through an aliased import (the review finding on this change).
//
// The literal it looks for is the prefix at the *start* of a string, not the
// exact four bytes, and the calls it reads are the whole formatting family
// (Sprintf, Errorf, Sprint, Sprintln, Appendf; strings' Join, Repeat, Replace,
// ReplaceAll) rather than Sprintf and Join alone. The fourth review finding on
// this change is why: `fmt.Errorf("anthropic/%s", id)` — a format string that
// *begins* with the prefix, assembled in an allowed file — was reported by
// neither the writer case nor the builder case, and the test stayed green while
// the route ID was built a second way.
//
// Hand mutations this fails on: writing `"anthropic/" + id` anywhere (a
// concatenation of the prefix is a derivation, in an allowed file included);
// writing `fmt.Errorf("anthropic/%s", id)` in an allowed file (the format
// string is a literal that begins with the prefix, and Errorf assembles it);
// and adding a fifth file that writes the prefix for a new reason.
func TestRoutingHasOneResolver(t *testing.T) {
	const prefix = "anthropic/"
	may := map[string]string{
		"internal/config/routing.go":                  "the resolver: claudeRouteID strips the prefix off a route entry's claim, and the map's pair is the authority on what follows (routing.go, RoutingClaudeFor)",
		"internal/config/providers.go":                "CheckClaudeClaim's shape judge: whether a claim is one explicit Claude ID",
		"internal/pricing/canonical.go":               "the map's own data: the route IDs it holds are these literals, and they are read through RouteIDFor alone",
		"internal/pricing/catalog/gencatalog/main.go": "the generator, detecting which catalog rows are Claude models",
	}
	var writers, builders []string
	for _, f := range moduleGoFiles(t) {
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if strings.HasPrefix(literal(v), prefix) {
					if _, ok := may[f.path]; !ok {
						writers = append(writers, f.path)
					}
				}
			case *ast.BinaryExpr:
				if v.Op == token.ADD && startsWithPrefix(v, prefix) {
					builders = append(builders, f.path+" concatenates it")
				}
			case *ast.AssignStmt:
				if v.Tok == token.ADD_ASSIGN {
					for _, r := range v.Rhs {
						if startsWithPrefix(r, prefix) {
							builders = append(builders, f.path+" appends it")
						}
					}
				}
			case *ast.CallExpr:
				if buildsAString(f, v) && startsWithPrefix(v, prefix) {
					builders = append(builders, f.path+" formats it")
				}
			}
			return true
		})
	}
	slices.Sort(writers)
	slices.Sort(builders)
	if len(writers) != 0 {
		t.Errorf("%v write the %q vendor prefix a route's own ID is built from: the route's spelling is the map's data (internal/pricing/canonical.go), and a second place that writes it is a second answer to where a Claude call goes (Task 1b.2, TestRoutingResolutionHasOneSite)", slices.Compact(writers), prefix)
	}
	if len(builders) != 0 {
		t.Errorf("%v the %q vendor prefix into a string: a route's own ID is read from the checked-in map (pricing.RouteIDFor) and never assembled — a derived spelling is a route nobody paired the model with", slices.Compact(builders), prefix)
	}
}

// literal is a string literal's value, or "" for anything else.
func literal(lit *ast.BasicLit) string {
	if lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// containsLiteral reports whether any basic string literal inside n is s.
func containsLiteral(n ast.Node, s string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && literal(lit) == s {
			found = true
		}
		return true
	})
	return found
}

// startsWithPrefix reports whether any basic string literal inside n begins
// with s. It is containsLiteral's wider sibling, and the width is the point: a
// route ID is assembled from a format string that begins with the prefix
// ("anthropic/%s") as surely as from the bare four bytes, so a scan that only
// recognised the exact literal read the second spelling as no spelling at all
// (the fourth review finding on this change).
func startsWithPrefix(n ast.Node, s string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && strings.HasPrefix(literal(lit), s) {
			found = true
		}
		return true
	})
	return found
}

// buildsAString reports whether call is a call that assembles a string from its
// arguments — the fmt formatting family and strings' joining and replacing — as
// opposed to a call that judges one (HasPrefix, CutPrefix, TrimPrefix), which is
// not a derivation. Which family member is asked is not the question the test is
// asking: a route ID built with Errorf is built, and the review finding on this
// change is a format string it was assembled with that went unseen because this
// list held Sprintf and Join alone.
func buildsAString(f goSource, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch f.imports[id.Name] {
	case "fmt":
		switch sel.Sel.Name {
		case "Sprintf", "Errorf", "Sprint", "Sprintln", "Appendf", "Fprintf":
			return true
		}
	case "strings":
		switch sel.Sel.Name {
		case "Join", "Repeat", "Replace", "ReplaceAll":
			return true
		}
	}
	return false
}

// TestRoutingCascadeNeedsNoProviderClaim: the credentials cascade is one switch
// with one key (D150), and it is written as `routing.claude: {via: cascade}`
// alone (O3) — no provider entry claims anything for it, yet the switch is
// accepted, with an empty providers table and with the providers table a
// cascade project actually has.
//
// The reason it needs no claim is the reason it routes nothing here: tier C for
// Claude is off (C3.6), so a cascade call still goes to Anthropic under Claude's
// own IDs and no provider entry has a destination to offer. What the switch
// changes is which credential carries the call, and that decision belongs to the
// credentials service, not to this package.
func TestRoutingCascadeNeedsNoProviderClaim(t *testing.T) {
	sw := &Routing{Claude: &RoutingClaude{Via: RouteCascade}}
	for _, tc := range []struct {
		name      string
		providers map[string]ModelProvider
	}{
		{"no providers at all", nil},
		{"an empty table", map[string]ModelProvider{}},
		{"a table that claims nothing for Claude", map[string]ModelProvider{"openrouter": okProvider()}},
	} {
		if ps := CheckRouting(sw, tc.providers, "routing"); len(ps) != 0 {
			t.Errorf("%s: problems = %s, want none — via: %s claims no route", tc.name, problemsText(ps), RouteCascade)
		}
		// And the resolution agrees, on every model: the cascade forwards the
		// body it was given, so the pin and the ID it is sent with are the same
		// model as the direct route's.
		for _, model := range []string{canonicalClaude, "claude-opus-5-5"} {
			route, sent, ok := RoutingClaudeFor(sw.Claude, model, tc.providers)
			if !ok || route != RouteDirect || sent != model {
				t.Errorf("%s (%s): RoutingClaudeFor = (%q, %q, %v), want (%q, %q, true)", tc.name, model, route, sent, ok, RouteDirect, model)
			}
		}
	}
	// And the resolver is correct by itself, not only by the shape rule that
	// keeps a list away from it: a cascade switch that names a model anyway —
	// CheckRoutingShape refuses such a node, so no policy carries one, and the
	// resolver is handed it here to pin its own contract — still resolves to the
	// direct route with the model unchanged. Without the resolver's own cascade
	// case this switch would resolve through the provider lookup, fail closed
	// with ok=false, and halt every run that carried it: the resolver must never
	// be the reader that invents a route for the cascade, nor the one that
	// refuses a call the cascade does not move.
	named := &RoutingClaude{Via: RouteCascade, Models: []string{canonicalClaude}}
	if route, sent, ok := RoutingClaudeFor(named, canonicalClaude, map[string]ModelProvider{"openrouter": claudeRoute([]string{"*"})}); !ok || route != RouteDirect || sent != canonicalClaude {
		t.Errorf("via: %s naming a model: RoutingClaudeFor = (%q, %q, %v), want (%q, %q, true)", RouteCascade, route, sent, ok, RouteDirect, canonicalClaude)
	}
}

// TestRoutingCascadeRefusesAModelsList: via: cascade takes no models list, and
// the refusal is what keeps today's runner inert under the switch rather than
// halting under it.
//
// The switch's list is D1a's opt-in — the thing that decides which pins the
// route is applied to — and the runner applies a routed pin by asking
// RoutingClaudeFor for a route that is *not* direct (internal/runner/routing.go,
// applyRouting). Cascade resolves to direct, so a cascade switch that named a
// model would halt every run of the project with "names X, and this run cannot
// resolve its route". Refusing the list here, at the shape rule both the CLI and
// the policy reader reach, is the smaller, safer answer: the switch is exactly
// the one O3 spells, `{via: cascade}`, and no run of this release changes what
// it sends because of it.
func TestRoutingCascadeRefusesAModelsList(t *testing.T) {
	sw := &Routing{Claude: &RoutingClaude{Via: RouteCascade, Models: []string{canonicalClaude}}}
	got := problemsText(CheckRouting(sw, nil, "routing"))
	if !strings.Contains(got, "routing.claude.models") || !strings.Contains(got, RouteCascade) {
		t.Errorf("via: %s with a list: problems = %q, want a refusal naming the list and the via", RouteCascade, got)
	}
	if !strings.Contains(got, RouteOpenRouter) {
		t.Errorf("via: %s with a list: problems = %q, want the refusal to name the value that does take a list (%s)", RouteCascade, got, RouteOpenRouter)
	}
	// The shape rule is where the refusal lives, so the *shape* of a published
	// policy is judged by it before its routes are: a policy reader that saw
	// only CheckRouting's answer would let the node through.
	if ps := CheckRoutingShape(sw, "routing"); len(ps) != 1 || !strings.Contains(problemsText(ps), "routing.claude.models") {
		t.Errorf("CheckRoutingShape(via: %s with a list) = %s, want exactly one refusal on routing.claude.models", RouteCascade, problemsText(ps))
	}
}

// TestRoutingAutoMeaningUnchangedForAPIKey: O3's second half — for every auth
// that is not cascade, today's meaning of `routing.claude` is untouched, and
// `via: auto` above all. Auto is key-presence driven: it resolves through the
// provider route exactly as openrouter does, and the caller that knows whether
// an Anthropic key exists (the runner's RoutingInertReason) is what prefers the
// direct path. Adding cascade changes neither half of that.
func TestRoutingAutoMeaningUnchangedForAPIKey(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": claudeRoute([]string{"*"})}
	auto := &RoutingClaude{Via: RouteAuto, Models: []string{canonicalClaude}}
	// The claim resolves exactly as before: auto is the openrouter route, and
	// the ID sent is the route's own.
	if ps := CheckRouting(&Routing{Claude: auto}, providers, "routing"); len(ps) != 0 {
		t.Errorf("via: auto: problems = %s, want none", problemsText(ps))
	}
	route, sent, ok := RoutingClaudeFor(auto, canonicalClaude, providers)
	if !ok || route != "openrouter" || sent != "anthropic/claude-sonnet-5.5" {
		t.Errorf("via: auto: RoutingClaudeFor = (%q, %q, %v), want (openrouter, anthropic/claude-sonnet-5.5, true)", route, sent, ok)
	}
	// Without a route claiming the ID, auto is refused and does not resolve: the
	// key-presence caller falls back to the direct path itself, which is D1b's
	// way of preferring the credential the run already has — not a silent
	// fall-through inside the resolver.
	if ps := CheckRouting(&Routing{Claude: auto}, map[string]ModelProvider{"openrouter": okProvider()}, "routing"); len(ps) == 0 {
		t.Error("via: auto with an unclaimed ID: accepted, want refused")
	}
	if _, _, ok := RoutingClaudeFor(auto, canonicalClaude, map[string]ModelProvider{"openrouter": okProvider()}); ok {
		t.Error("via: auto with an unclaimed ID resolved")
	}
}
