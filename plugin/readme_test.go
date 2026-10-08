package plugin_test

// Docs tests (plan T7): the README's Getting started is the two steps of the
// brief, and nothing in the repository still names a retired per-command
// skill (the plugin has five: setup, working, routing, parallelism, upgrade).

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestReadmeGettingStartedIsTwoSteps: the section has exactly two numbered
// steps, `fugaro init` and `/fugaro:setup`; the third step the skill carries
// is one sentence, and the plugin note says where init wires it.
func TestReadmeGettingStartedIsTwoSteps(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	_, rest, ok := strings.Cut(readme, "\n## Getting started\n")
	if !ok {
		t.Fatal("README.md has no `## Getting started` section")
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	steps := regexp.MustCompile(`(?m)^\d+\. .*$`).FindAllString(section, -1)
	if len(steps) != 2 {
		t.Fatalf("Getting started has %d numbered steps, want 2: %q", len(steps), steps)
	}
	if !strings.Contains(steps[0], "`fugaro init`") {
		t.Errorf("step 1 is %q, want `fugaro init`", steps[0])
	}
	if !strings.Contains(steps[1], "`/fugaro:setup`") {
		t.Errorf("step 2 is %q, want `/fugaro:setup`", steps[1])
	}
	for _, want := range []string{"merge", "`fugaro init` again", "`.claude/settings.json`", "install"} {
		if !strings.Contains(section, want) {
			t.Errorf("Getting started never says %q", want)
		}
	}
}

// TestReadmeNamesNoRetiredSkill: no document, help text, manifest or test
// string names a per-command skill that no longer exists.
func TestReadmeNamesNoRetiredSkill(t *testing.T) {
	retired := regexp.MustCompile("(?i)(fugaro:(onboard|launch|logs|diagnose|followup)\\b|fugaro:status\\b|\\b(onboard|launch|status|logs|diagnose|followup) skill\\b|skills/(onboard|launch|status|logs|diagnose|followup)\\b|six skills|six per-command)")
	// fugaro:status is also the pull request body's status marker.
	marker := regexp.MustCompile(`\[//\]|<!--|fugaro:status (begin|end)|fugaro:status["')]|rendered description|StripMarkers|fugaro:status\\n`)
	// docs/plans and the M11 design and brief describe the change itself.
	skip := regexp.MustCompile(`^(docs/plans/|docs/design/m11-|docs/design/setup-and-skills-spec-source\.md|plugin/.*_test\.go)`)
	var roots = []string{"README.md", "CONTRIBUTING.md", "docs", "schemas", "scripts", "internal", ".claude-plugin", "plugin/.claude-plugin", "plugin/skills"}
	n := 0
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join("..", root), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel("..", p)
			rel = filepath.ToSlash(rel)
			if skip.MatchString(rel) || strings.HasSuffix(rel, ".DS_Store") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			n++
			for i, line := range strings.Split(string(data), "\n") {
				if m := retired.FindString(line); m != "" && !(strings.HasPrefix(strings.ToLower(m), "fugaro:status") && marker.MatchString(line)) {
					t.Errorf("%s:%d names a retired skill (%q)", rel, i+1, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 20 {
		t.Fatalf("scanned only %d files", n)
	}
}
