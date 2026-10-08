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
	if cmd.Name() != "refresh" {
		t.Fatalf("command is named %q, the docs say refresh", cmd.Name())
	}
	var found bool
	for _, c := range newImageCmd().Commands() {
		if c.Name() == "refresh" {
			found = true
		}
	}
	if !found {
		t.Error("refresh is not registered under image")
	}
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
			if !strings.Contains(line, "fugaro image refresh") {
				continue
			}
			// Every --flag token of the paragraph (gcp-setup.md) or sentence about the command: a flag of
			// another command the paragraph names, or one the command
			// deliberately lacks, is listed; any other must exist on it.
			scan := line
			if !strings.HasPrefix(line, "**Moving a repository") {
				// Outside the full description, only the sentence that
				// names the command: the rest of the line may be about
				// other commands and their flags.
				_, rest, _ := strings.Cut(line, "fugaro image refresh")
				scan, _, _ = strings.Cut(rest, ". ")
			}
			for _, f := range strings.Fields(scan) {
				i := strings.Index(f, "--")
				if i < 0 {
					continue
				}
				name := strings.TrimRight(strings.Trim(f[i+2:], "`"), "`,.;:)*")
				name, _, _ = strings.Cut(name, "=")
				switch name {
				case "":
				case "yes", "json", "plan-only":
					if cmd.Flags().Lookup(name) != nil {
						t.Errorf("%s: the docs say fugaro image refresh has no --%s, but it does", path, name)
					}
				case "anchor", "base", "base-image":
				default:
					if cmd.Flags().Lookup(name) == nil {
						t.Errorf("%s: fugaro image refresh has no flag --%s", path, name)
					}
				}
			}
		}
	}
}
