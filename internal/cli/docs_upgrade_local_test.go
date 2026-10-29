package cli

import (
	"strings"
	"testing"
)

// TestUpgradeDocsNameTheLocalHalf: the setup guide used to name the plugin's
// `upgrade` skill and tell the reader to run `fugaro upgrade --local`. That page
// is the short setup path now (this review's finding: it carries the
// fresh-setup path only), and the reader meets the command in README.md's
// getting-started note, which says what to do when the `/fugaro:` skills are not
// listed. The plugin's skill set itself is pinned by plugin/plugin_test.go's
// TestSkillSetIsFive.
func TestUpgradeDocsNameTheLocalHalf(t *testing.T) {
	const path = "README.md"
	doc := readDoc(t, path)
	if !strings.Contains(doc, "fugaro upgrade --local") {
		t.Errorf("%s never tells the reader to run fugaro upgrade --local, the local half of an upgrade (the plugin pin and the installed plugin)", path)
	}
	for _, old := range []string{"/plugin marketplace add dimipaun/fugaro", "/plugin install fugaro@fugaro", "/plugin marketplace update fugaro"} {
		if strings.Contains(doc, old) {
			t.Errorf("%s still says %q, which fugaro upgrade replaces", path, old)
		}
	}
	if newUpgradeCmd().Flags().Lookup("local") == nil {
		t.Errorf("fugaro upgrade has no --local, which %s tells the reader to run", path)
	}
}
