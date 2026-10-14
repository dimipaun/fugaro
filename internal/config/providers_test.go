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

// TestRequireZDRDefaultsTrue pins the default of the route's first switch: a
// provider that says nothing prices the ZDR-eligible endpoints, because the
// safe end of a marketplace route is the one the owner would have picked, and
// only an explicit false prices every endpoint (design D3, D9).
func TestRequireZDRDefaultsTrue(t *testing.T) {
	var p ModelProvider
	if !p.RequireZDROn() {
		t.Error("an unset require_zdr is not on")
	}
	no := false
	p.RequireZDR = &no
	if p.RequireZDROn() {
		t.Error("require_zdr: false is on")
	}
	yes := true
	p.RequireZDR = &yes
	if !p.RequireZDROn() {
		t.Error("require_zdr: true is off")
	}
}

// TestPricePolicyDefaultIsCheapest pins the second switch's default and its
// effective value: unset means cheapest, spelled out so the compiler of a
// catalog row never sees the empty string (design D3).
func TestPricePolicyDefaultIsCheapest(t *testing.T) {
	var p ModelProvider
	if got := p.PricePolicySet(); got != PricePolicyCheapest {
		t.Errorf("an unset price_policy is %q, want %q", got, PricePolicyCheapest)
	}
	p.PricePolicy = PricePolicyHighest
	if got := p.PricePolicySet(); got != PricePolicyHighest {
		t.Errorf("price_policy: highest is %q", got)
	}
	if PricePolicyCheapest != "cheapest" || PricePolicyHighest != "highest" {
		t.Errorf("the policy names are %q/%q, want cheapest/highest: they are the config's own spelling", PricePolicyCheapest, PricePolicyHighest)
	}
}

// TestValidateModelProvidersRefusesUnknownPolicy pins that a typo in
// price_policy is a validate problem naming the provider and the field, not a
// route that silently prices the wrong end of the range.
func TestValidateModelProvidersRefusesUnknownPolicy(t *testing.T) {
	for _, policy := range []string{"", PricePolicyCheapest, PricePolicyHighest} {
		p := okProvider()
		p.PricePolicy = policy
		if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
			t.Errorf("price_policy %q: %s", policy, problemsText(ps))
		}
	}
	p := okProvider()
	p.PricePolicy = "cheapest-ish"
	ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
	if len(ps) == 0 {
		t.Fatal("an unknown price_policy was accepted")
	}
	got := problemsText(ps)
	for _, want := range []string{"providers.openrouter.price_policy", "cheapest", "highest", "cheapest-ish"} {
		if !strings.Contains(got, want) {
			t.Errorf("the problem does not name %q: %s", want, got)
		}
	}
}

// TestCatalogEligible pins which routes the built-in price catalog may price:
// OpenRouter itself (any subdomain) and a loopback fake in a test. Everything
// else is a vendor's own endpoint, priced by model_prices (design D5, P6).
func TestCatalogEligible(t *testing.T) {
	for url, want := range map[string]bool{
		"https://openrouter.ai/api/v1": true,
		"https://openrouter.ai":        true,
		"https://api.openrouter.ai/v1": true,
		"https://openrouter.ai.evil.t": false,
		"https://notopenrouter.ai/api": false,
		"http://127.0.0.1:8123":        true,
		"http://[::1]:8123":            true,
		"http://localhost:8123":        true,
		"https://api.deepseek.com/api": false,
		"https://api.deepseek.com":     false,
		"https://bedrock-runtime.aws":  false,
		"":                             false,
		"openrouter.ai":                false,
	} {
		if got := CatalogEligible(url); got != want {
			t.Errorf("CatalogEligible(%q) = %v, want %v", url, got, want)
		}
	}
}
