package runner

import (
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
