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

// TestWorkingSkillCarriesTheDueSignalAndTheReportsPlace (R3, D962, E1, E8):
// the cadence is kept only if a reader can tell it lapsed, and the skill has
// to say where a report lands in the CLI that ships. That is the checkout's
// own `docs/fugaro-reviews/` today (`--dir` names the folder; the writer, the
// reader and `--due` all work on it), with the commit as a separate opt-in
// step. The bucket the 2026-11-05 rulings (E1, E7, E8) move reports into is
// R10's writer, which the plan ships in the same release as R1 and R3: the
// wording here is the shipped one, and R10's PR is where both this file and
// the skill change together.
func TestWorkingSkillCarriesTheDueSignalAndTheReportsPlace(t *testing.T) {
	files := workingSkillFiles(t)
	ref, ok := files["skills/working/reference/periodic-reviews.md"]
	if !ok {
		t.Fatal("the working skill has no reference/periodic-reviews.md")
	}
	// The cadence's own reading, the report's place and the fifth rule are
	// the reference's, where the cadence and the rules are described.
	for _, want := range []struct{ what, text string }{
		{"the due signal with its command", "fugaro runs digest --due"},
		{"the report's place, the checkout's report folder", "docs/fugaro-reviews/"},
		{"`--dir` as the folder's name", "`--dir` names the folder"},
		{"the commit as a separate opt-in run", "opt-in"},
		{"the fifth rule's name, where the rules are listed", "cost_per_run_rising"},
	} {
		if !strings.Contains(ref, want.text) {
			t.Errorf("reference/periodic-reviews.md never names %s (%q): a reader who never hears of `--due` cannot tell the cadence lapsed, a reader not told where a report lands looks in the wrong folder, and a report prints five rules", want.what, want.text)
		}
	}
	// SKILL.md is what an agent always reads, so the two sentences a reader
	// acts on are there as well as in the reference.
	skill, ok := files["skills/working/SKILL.md"]
	if !ok {
		t.Fatal("the working skill has no SKILL.md")
	}
	for _, want := range []string{"fugaro runs digest --due", "docs/fugaro-reviews/", "opt-in"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skills/working/SKILL.md never says %q: the due signal and where a report lands are the two things the review section is read for", want)
		}
	}
}

// TestSetupSkillNamesTheReviewCadence (R3, D965, F18): the reviews are the
// user's cadence, and setup is the one moment it is cheapest to decide. The
// skill prints the three ways to schedule a report -- a cron job or a CI
// workflow of the repository, an agent routine, and a scheduled report run --
// each with the command it runs, and writes no schedule itself: a workflow or
// a crontab Fugaro wrote would commit into a repository it does not own,
// under the user's identity.
func TestSetupSkillNamesTheReviewCadence(t *testing.T) {
	skill := setupFiles(t)["skills/setup/SKILL.md"]
	for _, want := range []struct{ what, text string }{
		{"a cron job of the repository", "cron job"},
		{"a CI workflow of the repository", "CI workflow"},
		{"an agent routine", "agent routine"},
		{"a scheduled report run", "report run"},
		{"the cadence's own command", "fugaro runs digest --period"},
		{"the report run's recipe and its command", `fugaro run --recipe run-digest "periodic run review"`},
		{"that the schedule is the user's to write, never the agent's", "without the user's word"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("skills/setup/SKILL.md never names %s (%q): the user installs the cadence, and the setup skill is where the three ways and the command for each are printed once, writing nothing without the user's word", want.what, want.text)
		}
	}
}
