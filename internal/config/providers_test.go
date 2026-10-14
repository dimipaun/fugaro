package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"gopkg.in/yaml.v3"
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

// TestProviderAllowDataToSpellings is the round-6 ruling: every spelling of
// "written, but with no repositories listed" is the same ambiguity as an
// explicit [], and is refused the same way. The dangerous ones are the null
// spellings -- `allow_data_to:` with every entry commented out, or `~` --
// which YAML gives the same Go value as an absent key, and which therefore
// used to mean "every repository" by accident (design §3a: absent, or
// ["*"], is every repository).
func TestProviderAllowDataToSpellings(t *testing.T) {
	const body = "kind: anthropic-compat\nbase_url: https://openrouter.ai/api\nauth: bearer\n" +
		"secret: openrouter-api-key\nmodels: [\"deepseek/*\"]\n"
	for name, tc := range map[string]struct {
		tail    string
		anyRepo bool
		refused bool
	}{
		"absent":              {"", true, false},
		"wildcard":            {"allow_data_to: [\"*\"]\n", true, false},
		"listed":              {"allow_data_to: [acme/app]\n", false, false},
		"explicit empty list": {"allow_data_to: []\n", false, true},
		"null":                {"allow_data_to:\n", false, true},
		"tilde":               {"allow_data_to: ~\n", false, true},
		"only comments":       {"allow_data_to:\n  # - acme/app\n", false, true},
	} {
		t.Run(name, func(t *testing.T) {
			var p ModelProvider
			if err := yaml.Unmarshal([]byte(body+tc.tail), &p); err != nil {
				t.Fatal(err)
			}
			ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
			if tc.refused {
				if len(ps) != 1 || ps[0].Path != "providers.openrouter.allow_data_to" {
					t.Fatalf("%s: problems = %q, want one refusal of allow_data_to", name, problemsText(ps))
				}
				if !strings.Contains(ps[0].Message, "ambiguous") {
					t.Fatalf("%s: the refusal does not name the ambiguity: %q", name, ps[0].Message)
				}
				// ... and it stays fail-closed even if validation is
				// bypassed, exactly as the explicit [] does.
				if p.AllowsData("acme/anything") {
					t.Fatalf("%s: allows every repository despite being refused", name)
				}
				return
			}
			if len(ps) != 0 {
				t.Fatalf("%s: refused though it must be accepted: %q", name, problemsText(ps))
			}
			if got := p.AllowsData("acme/anything"); got != tc.anyRepo {
				t.Errorf("%s: AllowsData(acme/anything) = %v, want %v", name, got, tc.anyRepo)
			}
		})
	}
	// Reading the mapping node to tell a null from an absent key must not
	// cost the decoder's own strictness: an unknown field is still refused,
	// and still names the type, as yaml.v3's KnownFields does.
	var p ModelProvider
	err := yaml.Unmarshal([]byte(body+"api_key: sk-secret\n"), &p)
	if err == nil || !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), "not found in type") {
		t.Fatalf("an unknown provider field: %v", err)
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
