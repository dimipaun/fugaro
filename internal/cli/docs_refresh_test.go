package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsNameImageRefresh: the operator docs give the one command, not the
// four-step sequence, and every flag they give it is real.
func TestDocsNameImageRefresh(t *testing.T) {
	cmd := newImageRefreshCmd()
	for _, path := range []string{"../../docs/gcp-setup.md", "../../docs/recipes.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md",
		"../../plugin/skills/working/reference/followup.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro image refresh") {
			t.Errorf("%s never names fugaro image refresh", path)
		}
		if strings.Contains(doc, "from outside the checkout") {
			t.Errorf("%s still gives the init --base from outside the checkout sequence", path)
		}
		for _, line := range strings.Split(doc, "\n") {
			_, rest, ok := strings.Cut(line, "fugaro image refresh")
			for ok {
				for _, f := range strings.Fields(strings.SplitN(rest, "`", 2)[0]) {
					if name, isFlag := strings.CutPrefix(f, "--"); isFlag && cmd.Flags().Lookup(strings.TrimRight(name, ",.;)")) == nil {
						t.Errorf("%s: fugaro image refresh has no flag %s", path, f)
					}
				}
				_, rest, ok = strings.Cut(rest, "fugaro image refresh")
			}
		}
	}
}
