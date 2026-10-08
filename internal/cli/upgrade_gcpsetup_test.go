package cli

import (
	"os"
	"strings"
	"testing"
)

// TestGcpSetupDocNamesUpgrade: docs/gcp-setup.md (code review follow-up to
// upgrade task 9) names the five-skill plugin, including upgrade, and never
// sends a reader to the old four manual steps (the two /plugin slash
// commands) that fugaro upgrade --local now does for them.
func TestGcpSetupDocNamesUpgrade(t *testing.T) {
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "five skills") {
		t.Error("docs/gcp-setup.md never says \"five skills\"")
	}
	if !strings.Contains(doc, "`upgrade` (bring a checkout up to the installed `fugaro`)") {
		t.Error("docs/gcp-setup.md never names the upgrade skill among the five")
	}
	if !strings.Contains(doc, "fugaro upgrade --local") {
		t.Error("docs/gcp-setup.md never tells the reader to run fugaro upgrade --local")
	}
	for _, old := range []string{"four skills", "/plugin marketplace add dimipaun/fugaro", "/plugin install fugaro@fugaro", "/plugin marketplace update fugaro"} {
		if strings.Contains(doc, old) {
			t.Errorf("docs/gcp-setup.md still says %q, which fugaro upgrade replaces", old)
		}
	}
	if newUpgradeCmd().Flags().Lookup("local") == nil {
		t.Error("fugaro upgrade has no --local, which docs/gcp-setup.md now tells the reader to run")
	}
}
