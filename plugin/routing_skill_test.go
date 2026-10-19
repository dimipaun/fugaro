package plugin_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/runview"
)

// TestRoutingSkillChoosesRecipes (design generic-tool §4): the skill reads
// the catalog from the CLI, matches use_when, states its choice for one
// task, summarises a batch, never blocks a batch, and honours a pinned
// recipe and agent.recipe.
func TestRoutingSkillChoosesRecipes(t *testing.T) {
	data, err := os.ReadFile("skills/routing/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"## Choosing a recipe",
		"fugaro recipes ls --json",
		"use_when",
		"one line",                   // the single-task statement
		"never wait for approval",    // batches
		"12 standard, 2 solo",        // the summary's example
		"agent.recipe",               // policy default respected
		"the user named",             // a pinned recipe wins
		"launchable: true",           // only a launchable recipe may be picked
		"never invent a recipe name", // never fabricate one
		"job-image gate",             // a launch can be refused
		"never edit `fugaro.yaml`",   // never work around a refusal
		"never silently fall back to a different recipe", // offer, don't substitute
	} {
		if !strings.Contains(s, want) {
			t.Errorf("routing skill never says %q", want)
		}
	}
	for _, name := range []string{"standard", "solo", "premium", "review-only", "test-and-fix", "lint-fix"} {
		if strings.Contains(s, "`"+name+"`") {
			t.Errorf("routing skill hard-codes the recipe %s: it must read the catalog, not a decision tree", name)
		}
	}
}

// TestParallelismSkillNamesPick: the "Redundant attempts" paragraph gives
// the actual command to compare attempts, not just prose about the idea.
func TestParallelismSkillNamesPick(t *testing.T) {
	data, err := os.ReadFile("skills/parallelism/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "fugaro run --recipe pick --pr A --pr B"; !strings.Contains(string(data), want) {
		t.Errorf("parallelism skill never says %q", want)
	}
}

// TestLaunchPointsAtRoutingSkill: launch.md's recipe paragraph sends the
// reader to the routing skill instead of repeating its judgement.
func TestLaunchPointsAtRoutingSkill(t *testing.T) {
	data, err := os.ReadFile("skills/working/reference/launch.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "The `routing` skill has the full procedure for choosing one."; !strings.Contains(string(data), want) {
		t.Errorf("launch.md never says %q", want)
	}
}

// TestWorkingSkillStatusNamesWhoStartedEachRun: CONTRIBUTING's rule — a CLI
// change that touches a documented command changes the skill that names it, in
// the same PR — in the direction skills_lint_test.go does not check (that lint
// holds a skill's commands and flags to the CLI, never a documented command's
// new fields to the skill). `runs ls --json` gained `launched_by`, its text
// table gained the `BY` column, and `fugaro runs stats` arrived; the working
// skill's status reference enumerates that list's per-run keys verbatim, so a
// key missing there is a fact a user asking "who started this?" is never told.
//
// Each of the three is tied to the code that makes it true rather than typed
// twice: the key to runview.Row's own json tag, the column to the header
// internal/cli/runs.go writes for it, and the command to the cobra tree.
func TestWorkingSkillStatusNamesWhoStartedEachRun(t *testing.T) {
	data, err := os.ReadFile("skills/working/reference/status.md")
	if err != nil {
		t.Fatal(err)
	}
	status := string(data)

	// The key: runview.Row's own, so a rename fails here rather than leaving
	// the skill naming a field the row no longer carries.
	f, ok := reflect.TypeOf(runview.Row{}).FieldByName("LaunchedBy")
	if !ok {
		t.Fatal("runview.Row has no LaunchedBy field, and the working skill documents runs ls --json's keys")
	}
	key := strings.Split(f.Tag.Get("json"), ",")[0]
	if !strings.Contains(status, "`"+key+"`") {
		t.Errorf("skills/working/reference/status.md does not list %q among a run's keys: runs ls --json emits it and the skill enumerates that document", key)
	}

	// The column: the header internal/cli/runs.go writes, so the skill names a
	// column the listing has.
	runs, err := os.ReadFile(filepath.Join("..", "internal", "cli", "runs.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runs), `"RUN\tSTATUS\tSTAGE\tAGE\tCOST\tPR\tBY"`) {
		t.Fatal("internal/cli/runs.go no longer writes the listing header this test reads the BY column from; update it with the column")
	}
	if !strings.Contains(status, "`BY`") {
		t.Error("skills/working/reference/status.md does not name the listing's BY column: it is how a person reading the text listing learns who started a run")
	}

	// The command: resolved in the CLI's own tree, so the skill cannot name a
	// command that does not exist.
	if !strings.Contains(status, "fugaro runs stats") {
		t.Error("skills/working/reference/status.md does not name `fugaro runs stats`: a user asking who started the runs of a window has no command to be pointed at")
	}
	root := cli.NewRootCmd()
	cmd, _, err := root.Find([]string{"runs", "stats"})
	if err != nil || cmd == nil || cmd.Name() != "stats" {
		t.Fatalf("fugaro runs stats does not resolve in the CLI tree: %v", err)
	}
}
