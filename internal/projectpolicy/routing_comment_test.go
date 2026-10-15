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
// validRouting's own comment said "routing is enforced" — which nothing does.
// The runner decodes the block with the rest of the policy and acts on none of
// it; internal/cli preserves it byte-for-byte (patchPolicyJSON) and prints it.
// Phase 1b is what applies it. A comment that says otherwise is what a reader
// trusts when they decide whether a key they added to `routing` takes effect.
//
// The test reads the source because that is the deliverable: the claims are the
// change. The behaviour each corrected claim describes is pinned beside them —
// an unknown key inside `routing` is ignored, an unknown `routing.claude.via`
// is refused — so a change to either without the comment fails here.
func TestRoutingCommentsMatchTheCode(t *testing.T) {
	src, err := os.ReadFile("policy.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, bad := range []string{
		"routing is enforced",
		"which refuses a block this Fugaro cannot act on",
	} {
		if strings.Contains(text, bad) {
			t.Errorf("policy.go still claims %q: unknown keys inside routing are ignored (the test below decodes one), only an unknown routing.claude.via value is refused, and nothing applies the block yet", bad)
		}
	}
	for _, want := range []string{
		// What the code does with an unknown key inside the block.
		"unknown keys inside `routing` are ignored",
		// Why tolerating them is not a live risk in this release.
		"not applied",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("policy.go no longer says %q about routing: the code ignores an unknown key there and refuses only an unknown routing.claude.via value, and phase 1b is what applies the block", want)
		}
	}

	// The behaviour those sentences describe.
	p, found, err := Decode([]byte(`{"version": 1, "routing": {"claude": {"via": "direct", "models": ["claude-sonnet-5"]}, "future": {"a": 1}}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy: an unknown key inside routing is ignored, as the comment now says", p, found, err)
	}
	if p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.Via != RouteDirect {
		t.Errorf("Routing = %+v, want claude.via direct", p.Routing)
	}
	if _, _, err := Decode([]byte(`{"version": 1, "routing": {"claude": {"via": "nope"}}}`)); err == nil || !strings.Contains(err.Error(), "routing.claude.via") {
		t.Errorf("err = %v, want the via refusal (the one routing value this Fugaro checks)", err)
	}
}
