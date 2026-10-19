package config

import (
	"strings"
	"testing"
)

// TestRepoCannotWriteCredentials: D153 — the credentials block lives in the
// project's published policy (config/policy/shared) and nowhere else. A
// repository's own fugaro.yaml cannot carry it, exactly as it cannot carry
// routing (TestRepoCannotWriteRouting) or providers
// (TestRepoCannotAddProvider), and neither can the project layer.
//
// The reason is the one that decides where the block is authoritative at all:
// the cascade is a property of the *project* — one set of accounts, one
// threshold, one backoff, one budget the fallback's spend is charged against —
// so a per-repository copy would be a second answer to a question the service
// asks once, and a repository that could write its own would be a repository
// that could turn its project's cascade on. The block is read from the node by
// the credentials service (C1.11) and by nothing in a job's own file.
//
// Every document here is minimalYAML — the fixture the rest of this package
// parses as valid — with the credentials key appended, so the *key* is the only
// reason Parse refuses it. Hand mutation to see it red: give config.Config a
// `Credentials map[string]any` field tagged `yaml:"credentials"`, and every
// case below parses cleanly and this test fails (the same mutation the routing
// test documents).
func TestRepoCannotWriteCredentials(t *testing.T) {
	if _, ps := Parse([]byte(minimalYAML)); len(ps) != 0 {
		t.Fatalf("the fixture is not valid, so a refusal below would prove nothing: %v", ps)
	}
	for _, doc := range []string{
		minimalYAML + "credentials: {fallback: oauth-only}\n",
		minimalYAML + "credentials: {}\n",
		minimalYAML + "agent:\n  credentials: {fallback: auto}\n",
	} {
		_, ps := Parse([]byte(doc))
		if len(ps) == 0 {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
			continue
		}
		// And refused *for the credentials key*, not for something else: the
		// strict decoder names the field it does not know.
		if got := problemsText(ps); !strings.Contains(got, "credentials") {
			t.Errorf("refused without naming the key:\n%s\nproblems = %q", doc, got)
		}
	}
	// The project layer is the other site D153 names, and it is not open
	// either: the layer's own decode is strict, so a layer that writes
	// credentials fails closed instead of half-applying.
	if _, ps := ParseProjectLayer([]byte("version: 1\nproject: aurora\ncredentials: {fallback: auto}\n"), LayerAnchor{}); len(ps) == 0 {
		t.Error("a project layer carrying credentials: was accepted")
	}
}
