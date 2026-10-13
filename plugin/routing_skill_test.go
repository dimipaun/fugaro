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
		"one line",                // the single-task statement
		"never wait for approval", // batches
		"12 standard, 2 solo",     // the summary's example
		"agent.recipe",            // policy default respected
		"the user named",          // a pinned recipe wins
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
