package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

func TestRegistryEntryRecipe(t *testing.T) {
	r := &run{spec: &task.Spec{Repo: "acme/app", Task: "x"}, cfg: &config.Config{},
		rec: &runstore.Record{Recipe: &runstore.RecipeRecord{Name: "claude-solo", Source: "catalog"}}}
	if e := r.registryEntry(); e.Recipe != "claude-solo" {
		t.Fatalf("entry = %+v", e)
	}
	r.rec.Recipe.Name = "default"
	if e := r.registryEntry(); e.Recipe != "" {
		t.Fatalf("a default run names its recipe: %+v", e)
	}
	r.rec.Recipe = nil
	if e := r.registryEntry(); e.Recipe != "" {
		t.Fatalf("entry = %+v", e)
	}
}

// The registry entry — what a run says about itself while it runs, and what
// the console lists it by — carries the run's own job title, from the ONE
// place every surface reads (task.Spec.TitleLine): the launcher's --title,
// else the task prompt's first line. A brief opening with a shared preamble
// would otherwise make every run of it read identically.
//
// Mutation (build the title from the task's first line instead of
// Spec.TitleLine): the entry shows "Read the plan first." and this test
// fails.
func TestRegistryEntryCarriesTheJobTitle(t *testing.T) {
	r := &run{
		spec: &task.Spec{Repo: "acme/app", Task: "Read the plan first.\n\nThen fix it.", Title: "Ship the flaky-test fix"},
		cfg:  &config.Config{}, rec: &runstore.Record{},
	}
	if got := r.registryEntry().Title; got != "Ship the flaky-test fix" {
		t.Fatalf("title = %q, want the run's own title", got)
	}
	// With no title the entry is exactly what it always was: the task's
	// first line.
	r.spec.Title = ""
	if got := r.registryEntry().Title; got != "Read the plan first." {
		t.Fatalf("title = %q, want the task's first line", got)
	}
}

// A long title is clipped to 80 runes and redacted, like every other string
// the run publishes about itself: the entry is shown in a terminal and sent
// to the registry.
func TestRegistryEntryTitleIsClippedAndRedacted(t *testing.T) {
	r := &run{
		spec: &task.Spec{Repo: "acme/app", Task: "x", Title: strings.Repeat("日", 100)},
		cfg:  &config.Config{}, rec: &runstore.Record{},
	}
	if got := []rune(r.registryEntry().Title); len(got) != 80 {
		t.Fatalf("title = %q (%d runes), want the first 80", string(got), len(got))
	}
	r.spec.Title = "deploy with super-secret-token"
	r.addSecret("super-secret-token")
	if got := r.registryEntry().Title; got != "deploy with [REDACTED]" {
		t.Fatalf("title = %q, want the secret redacted", got)
	}
}
