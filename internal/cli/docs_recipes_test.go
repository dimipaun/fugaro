package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// TestRecipesGuideMatchesTheCLI: docs/recipes.md must describe the commands
// and the catalog that exist, and nothing else.
func TestRecipesGuideMatchesTheCLI(t *testing.T) {
	data, err := os.ReadFile("../../docs/recipes.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	real := map[string]bool{}
	for _, c := range newRecipesCmd().Commands() {
		real[c.Name()] = true
		if !strings.Contains(doc, "fugaro recipes "+c.Name()) {
			t.Errorf("docs/recipes.md never names fugaro recipes %s", c.Name())
		}
	}
	for _, m := range regexp.MustCompile(`fugaro recipes ([a-z]+)`).FindAllStringSubmatch(doc, -1) {
		if !real[m[1]] {
			t.Errorf("docs/recipes.md names fugaro recipes %s, which does not exist", m[1])
		}
	}
	for _, name := range recipe.CatalogNames() {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/recipes.md never names the catalog's %s", name)
		}
	}
	for _, want := range []string{"fugaro run --recipe", "agent.recipe", ".fugaro/recipes/", "fugaro/recipes/", "16 KiB", "0.5.0", "fugaro update",
		"use_when", "0.7.0", "commands.lint"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/recipes.md never says %q", want)
		}
	}
	// Every documented command and flag is in the real command tree.
	root := NewRootCmd()
	allFlags := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { allFlags[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, m := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(doc, -1) {
		if !allFlags[m[1]] {
			t.Errorf("docs/recipes.md names --%s, which no command has", m[1])
		}
	}
	for _, want := range []struct {
		path []string
		flag string
	}{{[]string{"run"}, "recipe"}, {[]string{"run"}, "pr"}, {[]string{"recipes", "ls"}, "json"}, {[]string{"recipes", "show"}, "json"}, {[]string{"recipes", "validate"}, "json"}, {[]string{"setup"}, "base"}, {[]string{"setup"}, "repo"}} {
		c, _, err := root.Find(want.path)
		if err != nil || c == nil || c.Name() != want.path[len(want.path)-1] {
			t.Errorf("no command %v", want.path)
			continue
		}
		if c.Flags().Lookup(want.flag) == nil {
			t.Errorf("fugaro %s has no --%s", strings.Join(want.path, " "), want.flag)
		}
	}
	for _, c := range []string{"fugaro validate", "fugaro watch", "fugaro runs ls", "fugaro image build", "fugaro update"} {
		if !strings.Contains(doc, c) {
			continue
		}
		if sub, _, err := root.Find(strings.Fields(c)[1:]); err != nil || sub == root {
			t.Errorf("docs/recipes.md names %q, which is not a command", c)
		}
	}
	// Each reserved key the guide lists is really refused with its own message.
	for _, key := range []string{"goto", "on_reject", "extends", "on_pass", "on_fail", "model", "models"} {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("docs/recipes.md never lists the reserved key %s", key)
		}
		_, ps := recipe.Parse([]byte("version: 1\nname: x\nsteps:\n  - review: {}\n" + key + ": 1\n"))
		if len(ps) == 0 || strings.Contains(recipe.ProblemsText(ps), "is not a recipe key") {
			t.Errorf("reserved key %s is not refused with a message of its own: %v", key, ps)
		}
	}
	// checks is refused only as a step name: the step type is check.
	if !strings.Contains(doc, "`checks`") {
		t.Error("docs/recipes.md never lists the reserved step name checks")
	}
	if _, ps := recipe.Parse([]byte("version: 1\nname: x\nsteps:\n  - checks: {}\n  - review: {}\n")); len(ps) == 0 || strings.Contains(recipe.ProblemsText(ps), "is not a recipe key") {
		t.Errorf("a checks step is not refused with a message of its own: %v", ps)
	}
	for _, sub := range []string{"ls", "show", "validate"} {
		if !strings.Contains(doc, "`"+sub+"`") || !strings.Contains(doc, "take `--json`") {
			t.Errorf("docs/recipes.md does not say that recipes %s takes --json", sub)
		}
	}
	readme, _ := os.ReadFile("../../README.md")
	if !strings.Contains(string(readme), "docs/recipes.md") {
		t.Error("README.md does not link docs/recipes.md")
	}
}

// TestRecipesGuideNamesCommitReports: the commit run's row in
// docs/recipes.md, which is the one place a person reads what designating a
// repository commits -- the closed list of what a committed report holds and
// what it never holds (D2753, D969), and the two keys the designation is
// written with (R14's member). It is a test of its own rather than a clause
// of TestRecipesGuideMatchesTheCLI because that test's name does not match the
// Docs filter the rest of the page's pins are run under.
func TestRecipesGuideNamesCommitReports(t *testing.T) {
	data, err := os.ReadFile("../../docs/recipes.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	text, ok := recipe.CatalogText("commit-reports")
	if !ok {
		t.Fatal("the catalog has no commit-reports recipe")
	}
	if !strings.Contains(string(text), string(recipe.StepCommitReports)) {
		t.Errorf("the catalog's commit-reports recipe no longer carries a commit_reports step:\n%s", text)
	}
	for _, want := range []string{
		"`commit-reports`",
		"commit_reports: {}",
		"`reports_commit.repo`",
		"`reports_commit.dir`",
		"`docs/fugaro-reviews-mirror`",
		"the run ids a flagged row is actionable by",
		"no person, no address, no task text, no log line and no credential",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/recipes.md never says %q: the commit run's row is where a person reads what designating a repository commits, and what a committed report holds and never holds (D2753, D969)", want)
		}
	}
}

// TestDocsRecipesGuideNamesByLevel: the level branch (LD9) has one home in
// docs/recipes.md, section 1's key list, and the things a person has to learn
// there are pinned here rather than left to the prose: which levels a
// by_level: entry may name, that a level may vary nothing but roles, which
// release's reader parses the key — and, the fact a person acts on, that no
// run applies the block yet, so a recipe that writes it runs on its top-level
// roles. The level list is held against recipe.LevelNames (the vocabulary the
// parser accepts) and the release against levelRolesSince (the gate that
// refuses a job image whose runner would die on the key at bootstrap), so the
// page cannot drift from either the parser or the launch check.
// The name carries Docs because that is the filter the repository's doc runs
// use (CONTRIBUTING.md's census); the sibling pins in this file are older and
// run only with the whole package.
func TestDocsRecipesGuideNamesByLevel(t *testing.T) {
	data, err := os.ReadFile("../../docs/recipes.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"`by_level`",
		"its level's own share of the deployed cap, whatever the recipe says",
		"No run applies the block yet",
		"runs on its top-level `roles` at every level",
		"is not a recipe key",
		"remove by_level",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/recipes.md never says %q: section 1 is where a person learns what a by_level: entry may name and vary, and section 8 what it refuses (LD9)", want)
		}
	}
	levels := "`" + strings.Join(recipe.LevelNames[:len(recipe.LevelNames)-1], "`, `") +
		"` or `" + recipe.LevelNames[len(recipe.LevelNames)-1] + "`"
	if !strings.Contains(doc, levels) {
		t.Errorf("docs/recipes.md never lists the levels a by_level: entry may name as the parser knows them (%s)", levels)
	}
	if want := "The first release whose reader knows the key is " + levelRolesSince; !strings.Contains(doc, want) {
		t.Errorf("docs/recipes.md never says %q: the page and the launch gate (levelRolesSince) have to move together", want)
	}
	// The refusal a level-conditioned budget gets is the parser's own wording,
	// quoted in section 8 rather than paraphrased.
	_, ps := recipe.Parse([]byte("version: 1\nname: x\nroles: { reviewer: coder }\nby_level: { critical: { cap: 1 } }\nsteps:\n  - review: {}\n"))
	if len(ps) == 0 {
		t.Fatal("a by_level entry that tries to set a budget is not refused")
	}
	msg := recipe.ProblemsText(ps)
	for _, want := range []string{"a by_level entry can set roles only", "the cap a run gets is its level's own, and the loop's shape is the recipe's"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the budget refusal inside by_level no longer says %q: %v", want, msg)
		}
		if !strings.Contains(doc, want) {
			t.Errorf("docs/recipes.md never quotes the refusal's own words %q (section 8)", want)
		}
	}
}

// TestLaunchSkillAllowsReviewModeTaskText: the working skill's launch.md
// guides the agent's own `fugaro run` invocations, so a wrong claim there
// about review-only/pick taking "never a task description" (true once,
// before task 12 wired TEXT/--task-file through to the review prompt as
// optional extra instructions — see TestReviewModeIncludesLauncherTaskText,
// internal/runner/reviewmode_test.go) could make the agent wrongly omit a
// user's review instructions when launching one of these recipes. Must
// agree with docs/recipes.md's own, correct claim ("TEXT, or --task-file,
// is optional then too").
func TestLaunchSkillAllowsReviewModeTaskText(t *testing.T) {
	data, err := os.ReadFile("../../plugin/skills/working/reference/launch.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if strings.Contains(doc, "never a task description") {
		t.Error("launch.md still claims review-only/pick take no task description; they take one, optionally")
	}
	if !strings.Contains(doc, "a task description is optional for either") {
		t.Error("launch.md no longer says a task description is optional for review-only/pick")
	}
}
