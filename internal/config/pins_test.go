package config

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
)

func pinnedAgent() Agent {
	return Agent{Models: ModelRoles{Coder: "claude-opus-5-5", Reviewer: "claude-sonnet-5-5", Background: "claude-haiku-4-5"}}
}

func paths(ps []Problem) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Path)
	}
	return strings.Join(s, ",")
}

func TestCheckPinsPasses(t *testing.T) {
	if ps := CheckPins(pinnedAgent(), pricing.Embedded()); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	// agent.model covers the roles models leaves out.
	a := Agent{Model: "claude-sonnet-5-5", Models: ModelRoles{Background: "claude-haiku-4-5"}}
	if ps := CheckPins(a, pricing.Embedded()); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsAliasRefused(t *testing.T) {
	a := pinnedAgent()
	a.Models.Reviewer = "sonnet"
	a.Models.Coder = "claude-opus-5-5[1m]"
	ps := CheckPins(a, pricing.Embedded())
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	if !strings.Contains(ps[1].Message, "alias") {
		t.Errorf("message %q does not say alias", ps[1].Message)
	}
}

func TestCheckPinsUnknownIDRefused(t *testing.T) {
	a := pinnedAgent()
	a.Models.Background = "claude-nonesuch-9"
	ps := CheckPins(a, pricing.Embedded())
	if paths(ps) != "agent.models.background" || !strings.Contains(ps[0].Message, "price") {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsBackgroundRequired(t *testing.T) {
	a := pinnedAgent()
	a.Models.Background = ""
	if ps := CheckPins(a, pricing.Embedded()); paths(ps) != "agent.models.background" {
		t.Fatalf("problems: %v", ps)
	}
	// With no model at all, the coder and reviewer have no pin either.
	if ps := CheckPins(Agent{}, pricing.Embedded()); paths(ps) != "agent.models.coder,agent.models.reviewer,agent.models.background" {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsOverrideTable(t *testing.T) {
	a := pinnedAgent()
	a.Models.Coder = "claude-acme-1"
	if ps := CheckPins(a, pricing.Embedded()); len(ps) != 1 {
		t.Fatalf("without the override: %v", ps)
	}
	one := 1.0
	tbl, err := pricing.Embedded().With(pricing.Overrides{"claude-acme-1": {InputPerM: one, OutputPerM: one}})
	if err != nil {
		t.Fatal(err)
	}
	if ps := CheckPins(a, tbl); len(ps) != 0 {
		t.Fatalf("with the override: %v", ps)
	}
}

func TestCheckAllowed(t *testing.T) {
	a := pinnedAgent()
	all := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{}), nil); ps != nil {
		t.Fatalf("no list must allow everything: %v", ps)
	}
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	e := policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	ps := CheckAllowed(a, e, all)
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "claude-opus-5-5") || !strings.Contains(m, "default-branch") || !strings.Contains(m, "claude-haiku-4-5") {
		t.Errorf("message %q", m)
	}
	// An empty non-nil list denies every model, however it came about.
	e = policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"other"}})
	if ps := CheckAllowed(a, e, all); len(ps) != 3 || !strings.Contains(ps[0].Message, "none") || !strings.Contains(ps[0].Message, "the project's allow-list: "+strings.Join(all, ", ")) {
		t.Fatalf("deny-all: %v", ps)
	}
	// A role with no model can't be shown to be on the list.
	a.Models.Reviewer = ""
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all); paths(ps) != "agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckAllowedQuotesModelNames(t *testing.T) {
	a := pinnedAgent()
	a.Models.Coder = "[x](http://evil)`@me\n"
	ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}}), nil)
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "`[x](http://evil)?@me?`") {
		t.Fatalf("problems: %v", ps)
	}
	if got := CodeSpan("a`b\x00c"); got != "`a?b?c`" {
		t.Fatalf("CodeSpan = %q", got)
	}
}

func providerAgent(coder string) Agent {
	return Agent{Models: ModelRoles{Coder: coder, Reviewer: "claude-sonnet-5-5", Background: coder}}
}

func TestUnpricedModelRefused(t *testing.T) {
	// A provider model with no price anywhere is refused at the pin check,
	// never silently charged.
	ps := CheckPins(providerAgent("qwen/qwen3-coder"), pricing.Embedded())
	if paths(ps) != "agent.models.coder,agent.models.background" || !strings.Contains(ps[0].Message, "no price") {
		t.Fatalf("problems: %v", ps)
	}
	// The embedded starter row for the first model is a price, so it passes.
	if ps := CheckPins(providerAgent("deepseek/deepseek-v4-flash"), pricing.Embedded()); len(ps) != 0 {
		t.Fatalf("starter row refused: %v", ps)
	}
	// A variant suffix is a different model: no price.
	if ps := CheckPins(providerAgent("deepseek/deepseek-v4-flash:free"), pricing.Embedded()); len(ps) == 0 {
		t.Fatal("a variant suffix was priced")
	}
}

func TestUnverifiedPriceWarns(t *testing.T) {
	ws := PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), pricing.Embedded())
	if paths(ws) != "agent.models.coder,agent.models.background" || ws[0].Code != CodeUnverifiedPrice ||
		!strings.Contains(ws[0].Message, "unverified") || !strings.Contains(ws[0].Message, "model_prices") {
		t.Fatalf("warnings: %v", ws)
	}
	if ws := PinWarnings(pinnedAgent(), pricing.Embedded()); len(ws) != 0 {
		t.Fatalf("Claude pins warned: %v", ws)
	}
	// The owner's price wins and silences the warning.
	tbl, err := pricing.Embedded().With(pricing.Overrides{"deepseek/deepseek-v4-flash": {InputPerM: 0.1, OutputPerM: 0.2}})
	if err != nil {
		t.Fatal(err)
	}
	// ...but leaving the cache multipliers at 0 is its own warning.
	ws = PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), tbl)
	if paths(ws) != "agent.models.coder,agent.models.background" || ws[0].Code != pricing.CodeCacheRateDefaulted {
		t.Fatalf("an override without cache rates: %v", ws)
	}
	full, err := pricing.Embedded().With(pricing.Overrides{"deepseek/deepseek-v4-flash": {InputPerM: 0.1, OutputPerM: 0.2, CacheWrite5m: 1, CacheWrite1h: 1, CacheRead: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	if ws := PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), full); len(ws) != 0 {
		t.Fatalf("an owner's full price still warns: %v", ws)
	}
}
