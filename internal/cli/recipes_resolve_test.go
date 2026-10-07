package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
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

func bucketObject(f *cloudFixture, name string) string {
	return filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey(name)))
}

func TestResolveRecipeCustomBucketNote(t *testing.T) {
	f := newCloudFixture(t)
	rr, err := resolveRecipe(context.Background(), fileEnv(t, f), "", "default", time.Now(), false)
	if err != nil || rr.Source != recipe.SourceCatalog {
		t.Fatalf("rr = %+v, %v", rr, err)
	}
	if !strings.Contains(rr.Note, "project recipes need the default runs bucket name fugaro-runs-proj-1234") {
		t.Fatalf("note = %q", rr.Note)
	}
}

func TestResolveRecipePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo})
	env := fileEnv(t, f)
	ctx, now := context.Background(), time.Now()
	if _, err := resolveRecipe(ctx, env, "", "claude-solo", now, false); err != nil { // fills the cache
		t.Fatal(err)
	}
	if err := os.Chmod(bucketObject(f, "claude-solo"), 0); err != nil {
		t.Fatal(err)
	}
	rr, err := resolveRecipe(ctx, env, "", "claude-solo", now.Add(48*time.Hour), false)
	if err == nil || !strings.Contains(err.Error(), "the project recipe claude-solo") || strings.Contains(err.Error(), "shared config") {
		t.Fatalf("rr = %+v, err = %v", rr, err)
	}
}

// unreachableEnv is env with a runs bucket nothing listens on.
func unreachableEnv(t *testing.T, env *cloudEnv) *cloudEnv {
	t.Helper()
	b, err := unreachableGCS(t)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return &cloudEnv{lc: env.lc, bucket: b, be: env.be}
}

func TestResolveRecipeUnreachable(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	env := fileEnv(t, f)
	now := time.Now()
	if _, err := resolveRecipe(context.Background(), env, "", "team", now, false); err != nil {
		t.Fatal(err)
	}
	down := unreachableEnv(t, env)
	rr, err := resolveRecipe(context.Background(), down, "", "team", now.Add(3*24*time.Hour), false)
	if err != nil || rr.Source != recipe.SourceProject || !strings.Contains(rr.Note, "cached project recipe team") {
		t.Fatalf("under 7 days: %+v, %v", rr, err)
	}
	if _, err := resolveRecipe(context.Background(), down, "", "team", now.Add(8*24*time.Hour), false); err == nil {
		t.Fatal("an entry over 7 days stood in for an unreachable bucket")
	}
	// An entry for another bucket never stands in.
	other := localcfg.SharedCacheEntry{GCPProject: "other-proj", Bucket: "fugaro-runs-other-proj", CheckedAt: now, YAML: projectTeam}
	if err := localcfg.SaveRecipeCache(os.Getenv, "aurora", "team", other); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRecipe(context.Background(), down, "", "team", now.Add(time.Hour), false); err == nil {
		t.Fatal("another project's entry stood in")
	}
}

func TestResolveRecipeOversized(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam + "# " + strings.Repeat("x", 17<<10) + "\n"})
	_, err := resolveRecipe(context.Background(), fileEnv(t, f), "", "team", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "over the 16 KiB limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveRecipeRefreshBypassesFresh(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	env := fileEnv(t, f)
	now := time.Now()
	if _, err := resolveRecipe(context.Background(), env, "", "team", now, false); err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, recipe.ObjectKey("team"), "version: 1\nname: team\nsteps:\n  - review: { max_rounds: 4 }\n")
	rr, err := resolveRecipe(context.Background(), env, "", "team", now.Add(time.Hour), false)
	if err != nil || string(rr.Text) != projectTeam {
		t.Fatalf("fresh entry not used: %+v, %v", rr, err)
	}
	rr, err = resolveRecipe(context.Background(), env, "", "team", now.Add(time.Hour), true)
	if err != nil || !strings.Contains(string(rr.Text), "max_rounds: 4") {
		t.Fatalf("refresh did not read the bucket: %+v, %v", rr, err)
	}
}
