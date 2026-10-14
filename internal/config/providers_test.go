package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestProvidersAreTheGitprovKinds keeps config.Providers in step with the
// provider kinds gitprov implements.
func TestProvidersAreTheGitprovKinds(t *testing.T) {
	want := []string{gitprov.KindGitHub, gitprov.KindBitbucket}
	if got := slices.Sorted(slices.Values(Providers)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("config.Providers = %v, gitprov kinds = %v", Providers, want)
	}
}

func TestAllowsDataEmptyMeansAnyRepo(t *testing.T) {
	for _, p := range []ModelProvider{{}, {AllowDataTo: nil}} {
		if !p.AllowsData("acme/anything") {
			t.Fatalf("AllowsData(%+v) = false, want true (no list set, owner's final ruling)", p)
		}
	}
	// An explicit, non-nil, zero-length list is NOT the same state (P7,
	// design §3a round 5): ValidateModelProviders refuses it outright, and
	// AllowsData stays fail-closed on it rather than treating it as
	// "every repository", so a bypass of that validation can never widen
	// access by accident. The plan's own test text listed
	// {AllowDataTo: []string{}} among the any-repo cases, which contradicts
	// its own P7 implementation; the decision wins here.
	if (ModelProvider{AllowDataTo: []string{}}).AllowsData("acme/anything") {
		t.Fatal("an explicit, non-nil empty allow_data_to was treated as \"every repository\"; it must stay fail-closed")
	}
}

func TestAllowsDataWildcardMeansAnyRepo(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"*"}}
	for _, repo := range []string{"acme/app", "ANYONE/anything"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (explicit [\"*\"] marker)", repo)
		}
	}
}

func TestAllowsDataNonEmptyListUnaffectedByWildcardRule(t *testing.T) {
	// The mutation-proof case: the wildcard/empty-list rule must be purely
	// additive, never weakening a real, non-empty, non-wildcard list.
	p := ModelProvider{AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}
	for _, repo := range []string{"acme/app", "acme/sandbox"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (listed)", repo)
		}
	}
	if p.AllowsData("acme/other") {
		t.Fatal("an unlisted repo was allowed against a real, non-empty, non-wildcard list")
	}
}

func TestAllowsDataWildcardIsExactNotGlob(t *testing.T) {
	// "acme/*" is NOT the wildcard: only the literal entry "*" is.
	p := ModelProvider{AllowDataTo: []string{"acme/*"}}
	if p.AllowsData("acme/app") {
		t.Fatal("\"acme/*\" was treated as a glob; only the literal \"*\" entry is the wildcard")
	}
}

func TestValidateModelProvidersRefusesExplicitEmptyList(t *testing.T) {
	// MUST-FIX 2, orchestrator's ruling (round 5): an absent key and ["*"]
	// both mean "any repository," but an EXPLICIT allow_data_to: [] is
	// ambiguous (did the operator mean "any repo" or "I haven't decided
	// yet") and must be refused, not silently accepted as either meaning.
	p := okProvider()
	p.AllowDataTo = []string{} // non-nil, explicit, zero-length -- NOT the same input as omitting the key
	ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
	if len(ps) == 0 {
		t.Fatal("an explicit allow_data_to: [] was accepted; it must be refused (write [\"*\"] or remove the key)")
	}
	if !strings.Contains(problemsText(ps), "ambiguous") {
		t.Fatalf("problems = %q, want a message naming the ambiguity", problemsText(ps))
	}
	// The absent-key case (the zero value, never touched) must still be
	// accepted -- this test would also catch a mutation that refused BOTH
	// states instead of only the explicit one.
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": okProvider()}); len(ps) != 0 {
		t.Fatalf("an absent allow_data_to (the zero value) was wrongly refused too: %+v", ps)
	}
}

func TestValidateModelProvidersAcceptsWildcardEntry(t *testing.T) {
	p := okProvider()
	p.AllowDataTo = []string{"*"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Fatalf("the explicit [\"*\"] marker was refused: %+v", ps)
	}
	p.AllowDataTo = []string{"*", "acme/app"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Fatalf("a mixed list with \"*\" was refused: %+v", ps)
	}
}
