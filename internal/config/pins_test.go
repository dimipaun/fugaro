package config

import (
	"strings"
	"testing"

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
