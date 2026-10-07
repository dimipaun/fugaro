package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	projectSolo = "version: 1\nname: claude-solo\nsteps:\n  - review: { max_rounds: 2 }\n"
	projectTeam = "version: 1\nname: team\nsteps:\n  - review: {}\n"
	repoTeam    = "version: 1\nname: team\nsteps:\n  - review: { max_rounds: 3 }\n"
)

func TestResolveRecipeLayers(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo, "team": projectTeam})
	env := fileEnv(t, f)
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{".fugaro/recipes/team.yaml": repoTeam})
	ctx, now := context.Background(), time.Now()
	for _, tc := range []struct {
		root, name string
		source     recipe.Source
		text       string
	}{
		{root, "team", recipe.SourceRepo, repoTeam},
		{"", "team", recipe.SourceProject, projectTeam},
		{root, "claude-solo", recipe.SourceProject, projectSolo},
		{root, "cheap-loop-senior", recipe.SourceCatalog, ""},
		{root, "default", recipe.SourceCatalog, ""},
	} {
		rr, err := resolveRecipe(ctx, env, tc.root, tc.name, now, false)
		if err != nil {
			t.Fatalf("%s at %q: %v", tc.name, tc.root, err)
		}
		if rr.Source != tc.source || (tc.text != "" && string(rr.Text) != tc.text) {
			t.Errorf("%s at %q: %+v", tc.name, tc.root, rr)
		}
	}
	if _, err := resolveRecipe(ctx, env, root, "nope", now, false); err == nil || !strings.Contains(err.Error(), "recipe nope is not in .fugaro/recipes") {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := resolveRecipe(ctx, env, root, "Bad_Name", now, false); err == nil || !strings.Contains(err.Error(), "is not a recipe name") {
		t.Fatalf("bad name: %v", err)
	}
}

func TestResolveRecipeTaskRecipe(t *testing.T) {
	text, _ := recipe.CatalogText("claude-solo")
	if got := (&resolvedRecipe{Name: "default", Source: recipe.SourceCatalog}).taskRecipe(); got != nil {
		t.Fatalf("catalog default carries %+v", got)
	}
	if got := (&resolvedRecipe{Name: "team", Source: recipe.SourceRepo, Text: []byte(repoTeam)}).taskRecipe(); got.YAML != "" || got.SHA256 != "" || got.Source != "repo" {
		t.Fatalf("repo = %+v", got)
	}
	got := (&resolvedRecipe{Name: "claude-solo", Source: recipe.SourceCatalog, Text: text}).taskRecipe()
	if got.YAML != string(text) || got.SHA256 != recipe.Sum(text) || got.Source != "catalog" {
		t.Fatalf("catalog = %+v", got)
	}
	if got := (&resolvedRecipe{Name: "default", Source: recipe.SourceProject, Text: []byte(projectTeam)}).taskRecipe(); got == nil || got.Source != "project" {
		t.Fatalf("project default = %+v", got)
	}
}

func TestResolveRecipeCustomBucketSkipsProject(t *testing.T) {
	f := newCloudFixture(t) // runs_bucket: unused-bucket, not the default name
	writeBucketFile(t, f, recipe.ObjectKey("claude-solo"), projectSolo)
	rr, err := resolveRecipe(context.Background(), fileEnv(t, f), "", "claude-solo", time.Now(), false)
	if err != nil || rr.Source != recipe.SourceCatalog {
		t.Fatalf("rr = %+v, %v", rr, err)
	}
}

func TestResolveRecipeCache(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	env := fileEnv(t, f)
	ctx, now := context.Background(), time.Now()
	if rr, err := resolveRecipe(ctx, env, "", "team", now, false); err != nil || rr.Source != recipe.SourceProject {
		t.Fatalf("first: %+v, %v", rr, err)
	}
	if err := os.Remove(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team")))); err != nil {
		t.Fatal(err)
	}
	// Fresh: the cache answers without reading the bucket.
	if rr, err := resolveRecipe(ctx, env, "", "team", now.Add(time.Hour), false); err != nil || rr.Source != recipe.SourceProject {
		t.Fatalf("cached: %+v, %v", rr, err)
	}
	// Stale, or refresh: the bucket says it is gone, and the cache goes too.
	if _, err := resolveRecipe(ctx, env, "", "team", now.Add(25*time.Hour), false); err == nil {
		t.Fatal("a deleted project recipe still resolves after the cache went stale")
	}
	if _, ok := localcfgRecipeCached(t, "team"); ok {
		t.Fatal("the cache entry survived the bucket's not-found")
	}
}

func TestResolveRecipeProjectNameMismatch(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectSolo}) // names itself claude-solo
	_, err := resolveRecipe(context.Background(), fileEnv(t, f), "", "team", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "the file name and the name must agree") || !strings.Contains(err.Error(), "gs://fugaro-runs-proj-1234/fugaro/recipes/team.yaml") {
		t.Fatalf("err = %v", err)
	}
}
