package projectpolicy

import (
	"os"
	"strings"
	"testing"
)

// The routing block is decoded loosely and validated in exactly one place, and
// both halves of that were misstated in this package's own prose: the package
// doc said routing's tolerance "cannot fail open" and that validRouting
// "refuses a block this Fugaro cannot act on" (it refuses one *value*), and
// validRouting's own comment said "routing is enforced" — which nothing did
// while the block was inert. The other direction is the mistake the first
// correction left behind: the prose that said the runner "acts on none of it"
// and that "phase 1b is what applies it" describes this release as the one
// before it. The run *does* apply the switch — internal/runner's applyRouting
// rewrites its Claude pins from it before any consumer reads them (Task 1b.2,
// design §6.2) — and internal/cli writes the block from the operator's config
// (patchPolicyJSON, `project policy publish`). A comment that says otherwise is
// what a reader trusts when they decide whether a key they added to `routing`
// takes effect: they publish a switch believing their Claude calls still go
// direct, and nothing tells them they do not.
//
// #414 sharpened what the tolerance is: an unknown member of the block is
// ignored only while the block still names a `claude` switch — a block that
// names none is refused (validRoutingMembers), because a misspelt member or a
// vendor this release does not route decodes to a nil switch and leaves every
// Claude call silently on the direct route — and the switch's own values are
// held to the rules the operator's config loader applies (config.CheckRouting,
// through validRouting) while the member names encoding/json reads them by are
// held to their spelling (CheckRoutingMemberNames). The claims pinned here are
// the merged ones: the unconditional "unknown keys inside `routing` are
// ignored" is no longer what this code does.
//
// The test reads the source because that is the deliverable: the claims are the
// change. The behaviour each corrected claim describes is pinned beside them —
// an unknown member beside a written `claude` is ignored, a block naming no
// `claude` switch is refused, an unknown `routing.claude.via` is refused — so a
// change to either without the comment fails here. The claim that the run
// applies the block is pinned where the application is, one package over:
// internal/runner's routing tests (routing_internal_test.go,
// routing_e2e_test.go) and internal/cli's
// TestDocsProjectLayerSaysRoutingIsApplied, whose subject is the same sentence
// in the operator's docs.
func TestRoutingCommentsMatchTheCode(t *testing.T) {
	src, err := os.ReadFile("policy.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, bad := range []string{
		"routing is enforced",
		"which refuses a block this Fugaro cannot act on",
		// The stale half: the run applies the switch (internal/runner's
		// applyRouting), so the document may not say that nothing does.
		"not applied: phase 1b is what reads it",
		"phase 1b is what applies it",
		"acts on none of it",
		// The unqualified form: the tolerance is conditional on the block
		// naming a switch, and validRoutingMembers refuses one that names none
		// (the test below decodes both).
		"unknown keys inside `routing` are ignored",
	} {
		if strings.Contains(text, bad) {
			t.Errorf("policy.go still claims %q: an unknown member of routing is ignored only beside a written `claude`, a block naming no `claude` switch is refused (validRoutingMembers), and the run applies the block (internal/runner's applyRouting)", bad)
		}
	}
	for _, want := range []string{
		// The refusal #414 added, and the name check it runs first.
		"names no `claude` switch",
		"validRoutingMembers",
		"CheckRoutingMemberNames",
		// Where the switch's values are judged.
		"config.CheckRouting",
		// Where the block is applied, which this release does.
		"internal/runner's applyRouting",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("policy.go no longer says %q about routing: the merged code refuses a block that names no `claude` switch (validRoutingMembers), checks the switch's names (CheckRoutingMemberNames) and values (config.CheckRouting), and the run applies the block (internal/runner's applyRouting, Task 1b.2)", want)
		}
	}

	// The behaviour those sentences describe.

	// An unknown member beside a written `claude` is ignored: that is the
	// tolerance the block keeps.
	p, found, err := Decode([]byte(`{"version": 2, "routing": {"claude": {"via": "direct", "models": ["claude-sonnet-5"]}, "future": {"a": 1}}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy: an unknown member beside a written `claude` is ignored, as the comment says", p, found, err)
	}
	if p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.Via != RouteDirect {
		t.Errorf("Routing = %+v, want claude.via direct", p.Routing)
	}
	// A block naming no `claude` switch is refused rather than decoded to a nil
	// switch: a misspelt member, or a vendor this release does not route.
	if _, _, err := Decode([]byte(`{"version": 2, "routing": {"future": {"a": 1}}}`)); err == nil || !strings.Contains(err.Error(), "no claude switch") {
		t.Errorf("err = %v, want the refusal for a block that names no `claude` switch", err)
	}
	// And an unknown value of the one member that does anything is refused.
	if _, _, err := Decode([]byte(`{"version": 2, "routing": {"claude": {"via": "nope"}}}`)); err == nil || !strings.Contains(err.Error(), "routing.claude.via") {
		t.Errorf("err = %v, want the via refusal (the one routing value this Fugaro checks)", err)
	}
}
