package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDocsNameUpgrade: the docs give the one upgrade command, never the old
// four steps, and every flag they give it is real.
func TestDocsNameUpgrade(t *testing.T) {
	cmd := newUpgradeCmd()
	flagRE := regexp.MustCompile("`fugaro upgrade((?: --?[a-z-]+)*)")
	for _, path := range []string{"../../README.md", "../../docs/gcp-setup.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/design/v1.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro upgrade") {
			t.Errorf("%s never names fugaro upgrade", path)
		}
		for _, old := range []string{"/plugin install fugaro@fugaro", "/plugin marketplace add dimipaun/fugaro"} {
			if strings.Contains(doc, old) {
				t.Errorf("%s still lists %q", path, old)
			}
		}
		for _, m := range flagRE.FindAllStringSubmatch(doc, -1) {
			for _, f := range strings.Fields(m[1]) {
				if name := strings.TrimLeft(f, "-"); cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: fugaro upgrade has no %s", path, f)
				}
			}
		}
	}
	for _, path := range []string{"../../README.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/release.md"} {
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "brew upgrade dimipaun/tap/fugaro && fugaro upgrade") {
			t.Errorf("%s does not give the upgrade sequence", path)
		}
	}
}
