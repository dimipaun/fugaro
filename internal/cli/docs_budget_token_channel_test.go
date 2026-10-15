package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsBudgetTokenChannelDesignIsWritten (plan task 3b.1,
// SECURITY-SENSITIVE):
// the design that moves the run's budget token out of reach of the other runs
// of a shared job exists, prices the four candidates the plan names, states a
// recommendation, says plainly that nothing follows it until the owner
// approves, and keeps the interim ruling D13's own words say the profile
// remains the isolation boundary. Its red state is this test: the document
// did not exist.
func TestDocsBudgetTokenChannelDesignIsWritten(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The subject and where it sits.
		"Phase 3b",
		"Task 3b.1",
		// The four candidates the plan lists, each priced.
		"signed URL",
		"Secret Manager",
		"tokens/",
		"execution identity",
		// The constraints it must not break.
		"launcher-overridable",
		"fail closed",
		"profile is the isolation boundary",
		// The verdict and its gate.
		"capability, not a boundary",
		"no code follows this document until the owner approves it",
		// The probe that gates one candidate, and the check that has not run.
		"Check 52",
		// The owner's decisions and the live check the implementation needs.
		"## The owner's open decisions",
		"## The check outline",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsSecurityMdPointsAtTheTokenChannelDesign: the trust model's reader can
// find the design that replaces the token's delivery channel, and the pointer
// says the design is not built.
func TestDocsSecurityMdPointsAtTheTokenChannelDesign(t *testing.T) {
	sec := readDoc(t, "SECURITY.md")
	if !strings.Contains(sec, "docs/design/budget-token-channel.md") {
		t.Fatal("SECURITY.md does not point at docs/design/budget-token-channel.md")
	}
	if _, err := os.Stat("../../docs/design/budget-token-channel.md"); err != nil {
		t.Fatalf("SECURITY.md points at %s, which is not there: %v", "docs/design/budget-token-channel.md", err)
	}
}
