package plugin_test

import (
	"os"
	"strings"
	"testing"
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
