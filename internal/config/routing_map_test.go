package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRoutingRefusesAClaudeIDTheMapDoesNotMap: the switch names a Claude ID the
// checked-in canonical-to-route map carries as explicitly unmapped (no measured
// route ID), and a route entry claims a spelling of it. The switch is refused
// loudly, with the map's own reason, and does not resolve: guessing the route's
// ID (or sending the canonical one on a route) is the failure this refuses.
//
// Hand mutation this fails on: dropping the map's unmapped check, so the switch
// resolves to a spelling nothing measured.
func TestRoutingRefusesAClaudeIDTheMapDoesNotMap(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": {
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5"}, AllowDataTo: []string{"*"},
	}}
	sw := &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5"}}
	ps := CheckRouting(&Routing{Claude: sw}, providers, "routing")
	if len(ps) == 0 {
		t.Fatal("a Claude ID the map does not map was accepted by the switch")
	}
	got := problemsText(ps)
	if !strings.Contains(got, "no route's own ID has been measured") {
		t.Errorf("the refusal does not carry the map's reason: %s", got)
	}
	if route, sent, ok := RoutingClaudeFor(sw, "claude-sonnet-5", providers); ok {
		t.Errorf("RoutingClaudeFor resolved %s to (%q, %q): an unmapped ID must not resolve", "claude-sonnet-5", route, sent)
	}
}

// TestRoutingRefusesAClaimThatIsNotTheMapsPair: the route entry claims
// anthropic/claude-sonnet-5-5 — a spelling the old comparison folded into the
// canonical ID, which is exactly the guess Task 1b.2 removes. The switch names
// claude-sonnet-5-5 and the route claims the near miss, so there is no pair in
// the map and the switch is refused rather than resolved to a route entry that
// spells the model its own way.
//
// Hand mutation this fails on: resolving by the case/separator fold instead of
// the map (the pre-1b.2 sameClaudeID), which routes the call and prices it at
// whatever the wrong key is priced at.
func TestRoutingRefusesAClaimThatIsNotTheMapsPair(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": {
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5-5"}, AllowDataTo: []string{"*"},
	}}
	sw := &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}
	if ps := CheckRouting(&Routing{Claude: sw}, providers, "routing"); len(ps) == 0 {
		t.Error("a claim that is not the map's pair was accepted: the route's ID must be the one the map holds")
	}
	if route, sent, ok := RoutingClaudeFor(sw, "claude-sonnet-5-5", providers); ok {
		t.Errorf("RoutingClaudeFor resolved the near miss %q to (%q, %q), want no resolution", "anthropic/claude-sonnet-5-5", route, sent)
	}
}

// TestRoutingResolutionHasOneSite is the real half of 1b.1's
// TestRoutingHasOneResolver: the map (pricing.RouteIDFor, CanonicalFor,
// UnmappedReason) may be read only from internal/pricing's own file and from
// internal/config's resolver. Every consumer — the pins renderer, the price
// lookup, the run's own config — asks RoutingClaudeFor, so a second site is a
// second answer to where a Claude call goes and what it costs.
//
// Hand mutation this fails on: reading the map from a second package (the
// runner's own stagePins, say) because it already knows the pins.
func TestRoutingResolutionHasOneSite(t *testing.T) {
	callers := regexp.MustCompile(`pricing\.(RouteIDFor|CanonicalFor|UnmappedReason)\(`)
	// Relative to this package's directory (internal/config): the map's own
	// file, and the resolver's.
	allowed := map[string]bool{
		filepath.Join("..", "pricing", "canonical.go"): true,
		"routing.go": true,
	}
	var found []string
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !callers.Match(src) {
			return nil
		}
		if allowed[filepath.Clean(path)] {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("the canonical-to-route map is read in %v, want only internal/pricing/canonical.go (the data) and internal/config/routing.go (the resolver): a second site is a second answer to where a Claude call goes", found)
	}
}
