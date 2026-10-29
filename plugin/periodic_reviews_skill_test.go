package plugin_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// workingSkillFiles returns the working skill's own files, SKILL.md and its
// reference/*.md, keyed by their repository-relative path.
func workingSkillFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(filepath.Join("skills", "working"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(path)] = string(data)
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no working skill files: %v", err)
	}
	return files
}

// TestWorkingSkillNamesRunsDigest: reviewing the runs is a command now, not a
// hand-run listing, and the skill sends the reader to it over the four periods
// it reports. The cadence is the point of the command, so a skill that names
// only the daily report leaves the weekly, monthly and yearly ones unsaid.
func TestWorkingSkillNamesRunsDigest(t *testing.T) {
	files := workingSkillFiles(t)
	var all strings.Builder
	for _, text := range files {
		all.WriteString(text)
		all.WriteString("\n")
	}
	tree := all.String()

	if !strings.Contains(tree, "fugaro runs digest") {
		t.Error("the working skill never names `fugaro runs digest`: the daily, weekly, monthly and yearly reviews have no command")
	}
	for _, period := range []string{"day", "week", "month", "year"} {
		if !regexp.MustCompile(`--period[^\n]*\b` + period + `\b`).MatchString(tree) {
			t.Errorf("the working skill never names `--period %s`: the command reports four periods and the skill must say so", period)
		}
	}

	skill, ok := files["skills/working/SKILL.md"]
	if !ok {
		t.Fatal("the working skill has no SKILL.md")
	}
	if !strings.Contains(skill, "reference/periodic-reviews.md") {
		t.Error("SKILL.md does not point at `reference/periodic-reviews.md`: the cadence, the reading and the submission step live there, and a reader who is not sent to the file never finds them")
	}
}

// TestWorkingSkillNeverSubmitsOnItsOwn: sharing a learning with Fugaro's
// maintainers is the person's step. The skill may name one form of the
// command, the one that opens the browser and leaves the submit button to the
// person; a mention without it is an agent filing an issue by itself, which is
// what this test exists to prevent.
func TestWorkingSkillNeverSubmitsOnItsOwn(t *testing.T) {
	files := workingSkillFiles(t)
	seen := 0
	for name, text := range files {
		for _, loc := range regexp.MustCompile(`gh issue create`).FindAllStringIndex(text, -1) {
			seen++
			rest := text[loc[1]:]
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				rest = rest[:i]
			}
			if !strings.Contains(rest, "--web") {
				t.Errorf("%s: `gh issue create` without `--web`: a learning is submitted by the person in the browser, never filed by the agent", name)
			}
		}
	}
	if seen == 0 {
		t.Fatal("the working skill never names `gh issue create --web`: the one step of a review that is the person's has no command, and an agent left without it invents one")
	}

	var all strings.Builder
	for _, text := range files {
		all.WriteString(text)
		all.WriteString("\n")
	}
	tree := all.String()
	for _, want := range []string{"after the person's word", "never submits a learning on its own"} {
		if !strings.Contains(tree, want) {
			t.Errorf("the working skill never says %q: a learning is submitted only once the person asks for it, and the agent never files one itself", want)
		}
	}
}
