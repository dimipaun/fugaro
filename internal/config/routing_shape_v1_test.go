package config

import (
	"fmt"
	"strings"
	"testing"
)

// This file is task C1.2's frozen copy of the routing shape rule, and it exists
// for one question: what does a Fugaro that predates `via: cascade` do with a
// published policy that carries it?
//
// The answer is in checkRoutingShapeV1 below, which is CheckRoutingShape as it
// stood before this task, copied here verbatim except that the three via words
// it knew are spelled as literals rather than through the constants — a frozen
// reader that read today's constants would move with them, and the whole point
// of the copy is that it does not. Its answer: an older reader refuses any via
// other than direct, openrouter or auto, so a shared node carrying `via:
// cascade` is refused whole, by the older CLI at launch and at publish (both go
// through Decode, which reaches this rule through CheckRouting) and by an older
// runner as a policy it cannot decode, which halts the run. That is fail
// closed, and it is why the plan's C1.2 ships no policy version bump for the
// switch: what an older reader does is refuse, not misread.
//
// The freeze is a copy and not a call of the real function on purpose: the real
// CheckRoutingShape now accepts cascade, so a test asserting the old refusal
// against it could only ever fail. A future task that changes the via list again
// leaves this copy alone, and this test keeps answering the same question about
// the same release.

// checkRoutingShapeV1 is CheckRoutingShape as of the release before C1.2:
// direct, openrouter and auto, and nothing else.
func checkRoutingShapeV1(r *Routing, path string) []Problem {
	if r == nil || r.Claude == nil {
		return nil
	}
	c := r.Claude
	via := c.ViaValue()
	switch via {
	case "direct", "openrouter", "auto":
	default:
		return []Problem{{Path: path + ".claude.via", Message: fmt.Sprintf("%q must be direct, openrouter or auto", c.Via)}}
	}
	var ps []Problem
	if c.Via == "" {
		ps = append(ps, Problem{Path: path + ".claude.via", Message: "is written with no value: name direct, openrouter or auto, or remove the block — an absent switch is direct (D1b)"})
	}
	if (via == "openrouter" || via == "auto") && len(c.Models) == 0 {
		ps = append(ps, Problem{Path: path + ".claude.models", Message: fmt.Sprintf("is empty, and an empty list with via: %s would opt every Claude model in: name the IDs this switch carries, one explicit Claude ID per entry, never a pattern (D1a, docs/design/mixed-credentials.md §4)", via)})
	}
	// The model-entry rule is deliberately not copied: this file freezes the
	// shape rule's answer about the *via*, and CheckRoutingModel is the same
	// rule in both releases (C1.2 does not touch it).
	for i, m := range c.Models {
		if msg := CheckRoutingModel(m); msg != "" {
			ps = append(ps, Problem{Path: fmt.Sprintf("%s.claude.models[%d]", path, i), Message: msg})
		}
	}
	return ps
}

// TestRoutingCascadeRefusedByOlderShapeCheck pins the behaviour the plan's
// C1.2 "Version skew — no bump" section claims: an older reader refuses
// `via: cascade` outright, with the error CheckRoutingShape's via rule prints
// and not with a version skew's sentence. It asserts both halves in one place,
// because either alone would be misleading: that today's rule accepts the
// switch, and that the frozen older one does not.
func TestRoutingCascadeRefusedByOlderShapeCheck(t *testing.T) {
	sw := &Routing{Claude: &RoutingClaude{Via: RouteCascade}}

	if ps := CheckRoutingShape(sw, "routing"); len(ps) > 0 {
		t.Fatalf("this release must accept via: %s with no models list, got %v", RouteCascade, ps)
	}
	ps := checkRoutingShapeV1(sw, "routing")
	if len(ps) != 1 {
		t.Fatalf("an older reader must refuse via: %s with exactly one problem, got %d: %v", RouteCascade, len(ps), ps)
	}
	if ps[0].Path != "routing.claude.via" {
		t.Errorf("the refusal names %q, want routing.claude.via", ps[0].Path)
	}
	if !strings.Contains(ps[0].Message, "direct, openrouter or auto") {
		t.Errorf("the refusal is %q, and it must be the via rule's own sentence (an older reader names the values it knows, never a version skew)", ps[0].Message)
	}
	// The same frozen reader accepts what it always accepted: the copy is a
	// reader of the old shape, not a reader that refuses everything.
	for _, via := range []string{RouteDirect, RouteOpenRouter} {
		back := &Routing{Claude: &RoutingClaude{Via: via, Models: []string{"claude-sonnet-5-5"}}}
		if ps := checkRoutingShapeV1(back, "routing"); len(ps) != 0 {
			t.Errorf("the frozen reader refused via: %s (%v); it is a copy of the old rule, not a deny-all", via, ps)
		}
	}
}
