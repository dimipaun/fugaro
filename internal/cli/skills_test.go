package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var inlineCommandRE = regexp.MustCompile("`(fugaro [^`]+)`")

// skillCommands returns the fugaro command lines a skill quotes, in code
// spans or in code blocks.
func skillCommands(md string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			if strings.HasPrefix(trimmed, "fugaro ") {
				out = append(out, trimmed)
			}
			continue
		}
		for _, m := range inlineCommandRE.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// notYet are commands design §9.1 defines that later milestones add.
var notYet = []string{}

// TestSkillCommandsExist fails when a skill tells the agent to run a fugaro
// command or flag that the CLI doesn't have.
func TestSkillCommandsExist(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "plugin", "skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills found: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := skillCommands(string(data))
		if len(lines) == 0 {
			t.Errorf("%s quotes no fugaro commands", f)
		}
		for _, line := range lines {
			var words []string
			for _, w := range strings.Fields(line)[1:] {
				if slices.Contains([]string{">", ">>", "|", "&&", "||", ";", "2>&1", "<"}, w) {
					break
				}
				words = append(words, w)
			}
			if len(words) > 0 && slices.Contains(notYet, words[0]) {
				continue
			}
			root := NewRootCmd()
			cmd, rest, err := root.Find(words)
			if err != nil || cmd == root {
				t.Errorf("%s: `%s` names no fugaro command", f, line)
				continue
			}
			for _, w := range rest {
				if !strings.HasPrefix(w, "--") {
					continue
				}
				name, _, _ := strings.Cut(strings.TrimPrefix(w, "--"), "=")
				if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
					t.Errorf("%s: `%s`: %s has no --%s flag", f, line, cmd.CommandPath(), name)
				}
			}
		}
	}
}
