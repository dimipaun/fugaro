package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// projectRecipesFixture gives the fixture's installation the default runs
// bucket name (fugaro-runs-proj-1234), which project recipes need, and
// writes recipes (name -> text) into its file:// runs bucket.
func projectRecipesFixture(t *testing.T, f *cloudFixture, recipes map[string]string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(data), "runs_bucket: unused-bucket", "runs_bucket: fugaro-runs-proj-1234", 1)
	if out == string(data) {
		t.Fatal("the fixture config has no runs_bucket: unused-bucket line")
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, text := range recipes {
		writeBucketFile(t, f, recipe.ObjectKey(name), text)
	}
}

// repoRecipeCopy publishes text as the project's copy of repo's recipe name
// — the copy a launch of that repository resolves (D3303) — and returns the
// copy name. A copy names itself, and the store refuses one whose file name
// and its own name disagree, so the copy's name replaces the name the text
// carries. It is the fixture's stand-in for the migration fugaro update
// performs, and it makes the project store readable the way
// projectRecipesFixture does.
func repoRecipeCopy(t *testing.T, f *cloudFixture, repo, name, text string) string {
	t.Helper()
	copy := recipe.CopyName(name, "github", repo)
	named := strings.Replace(text, "\nname: "+name+"\n", "\nname: "+copy+"\n", 1)
	if named == text {
		t.Fatalf("the recipe text does not name %s", name)
	}
	projectRecipesFixture(t, f, map[string]string{copy: named})
	return copy
}

// writeBucketFile writes key into the fixture's file:// runs bucket.
func writeBucketFile(t *testing.T, f *cloudFixture, key, text string) {
	t.Helper()
	p := filepath.Join(f.dir, "runs", filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeBuildRecord stores the build record of slug's workflow with baseRef,
// and no edition: a record that cannot say which edition built it is the
// "unknown" state (imagecheck.Record.Edition), which gets no edition warning.
func writeBuildRecord(t *testing.T, f *cloudFixture, slug, workflow, baseRef string) {
	t.Helper()
	writeEditionBuildRecord(t, f, slug, workflow, baseRef, "")
}

// writeEditionBuildRecord is writeBuildRecord with the record's edition: the
// build the record describes was made by a fugaro of edition (EditionCore or
// EditionEE), which is what the launch-side edition warnings judge.
func writeEditionBuildRecord(t *testing.T, f *cloudFixture, slug, workflow, baseRef, edition string) {
	t.Helper()
	data, err := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: workflow, BaseRef: baseRef, FugaroVersion: "0.5.0", Edition: edition})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.RecordKey(slug, workflow), string(data))
}

// fileEnv is a cloudEnv on the fixture's own file:// runs bucket.
func fileEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	b, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return envOn(t, f, b)
}

// noAgentSession clears every coding-agent marker for the test (the suite
// may itself run under one).
func noAgentSession(t *testing.T) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
}

// localcfgRecipeCached reads aurora's cache entry for name.
func localcfgRecipeCached(t *testing.T, name string) (localcfg.SharedCacheEntry, bool) {
	t.Helper()
	return localcfg.LoadRecipeCache(os.Getenv, "aurora", name)
}
