package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func writeRecipeFile(t *testing.T, dir, rel, text string) string {
	t.Helper()
	testutil.WriteFiles(t, dir, map[string]string{rel: text})
	return filepath.Join(dir, filepath.FromSlash(rel))
}

func TestRecipesValidate(t *testing.T) {
	dir := t.TempDir()
	good := writeRecipeFile(t, dir, "team.yaml", projectTeam)
	out, _, err := execute(t, "recipes", "validate", good)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good: %s, %v", out, err)
	}
	bad := writeRecipeFile(t, dir, "bad.yaml", "version: 1\nname: bad\nmodel: claude-opus-5\nsteps:\n  - review: {}\n")
	out, _, err = execute(t, "recipes", "validate", "--json", bad)
	var doc struct {
		Valid    bool             `json:"valid"`
		Problems []recipe.Problem `json:"problems"`
	}
	if ExitCode(err) != ExitUserError || json.Unmarshal([]byte(out), &doc) != nil || doc.Valid || len(doc.Problems) != 1 ||
		!strings.Contains(doc.Problems[0].Message, "never names a model") {
		t.Fatalf("bad: %s, %v", out, err)
	}
}

// Review Focus 4.
func TestRecipesValidateNameMismatch(t *testing.T) {
	path := writeRecipeFile(t, t.TempDir(), ".fugaro/recipes/mine.yaml", "version: 1\nname: claude-solo\nsteps:\n  - review: {}\n")
	_, _, err := execute(t, "recipes", "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	out, _, _ := execute(t, "recipes", "validate", path)
	if !strings.Contains(out, "the file name and the name must agree") {
		t.Fatalf("out = %s", out)
	}
}

func TestRecipesPublishRefusedInAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team")))); err == nil {
		t.Fatal("published from an agent session")
	}
}

func TestRecipesPublishWritesObject(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "claude-solo.yaml", projectSolo)
	out, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(out, "published claude-solo to gs://fugaro-runs-proj-1234/fugaro/recipes/claude-solo.yaml") ||
		!strings.Contains(errOut, "replaces the catalog's claude-solo") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
	got, err := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("claude-solo"))))
	if err != nil || string(got) != projectSolo {
		t.Fatalf("object = %q, %v", got, err)
	}
}

func TestRecipesPublishShowsWhatItReplaces(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(errOut, "replacing the project's team") ||
		!strings.Contains(errOut, "- ") || !strings.Contains(errOut, "+ ") || !strings.Contains(errOut, "max_rounds: 3") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

func TestRecipesPublishNeedsFileNamedAfterRecipe(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "mine.yaml", projectSolo)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("claude-solo")))); err == nil {
		t.Fatal("published under a mismatched file name")
	}
}

func TestRecipesPublishRefusesInvalidAndCustomBucket(t *testing.T) {
	f := newCloudFixture(t) // custom bucket name
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	if _, _, err := execute(t, "recipes", "publish", path); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("custom bucket: %v", err)
	}
	projectRecipesFixture(t, f, nil)
	bad := writeRecipeFile(t, t.TempDir(), "bad.yaml", "version: 1\nname: bad\nsteps: []\n")
	if _, _, err := execute(t, "recipes", "publish", bad); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("invalid: %v", err)
	}
}

func TestRecipesLsLayers(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo, "team": projectTeam})
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/team.yaml", repoTeam)
	t.Chdir(dir)
	out, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []recipeRow `json:"recipes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	want := []recipeRow{
		{Name: "team", Source: "repo"},
		{Name: "claude-solo", Source: "project"},
		{Name: "team", Source: "project", ShadowedBy: "repo"},
		{Name: "cheap-loop-senior", Source: "catalog"},
		{Name: "claude-solo", Source: "catalog", ShadowedBy: "project"},
		{Name: "default", Source: "catalog"},
	}
	if len(doc.Recipes) != len(want) {
		t.Fatalf("rows = %+v", doc.Recipes)
	}
	for i, w := range want {
		g := doc.Recipes[i]
		if g.Name != w.Name || g.Source != w.Source || g.ShadowedBy != w.ShadowedBy {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestRecipesShowSource(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo})
	out, _, err := execute(t, "recipes", "show", "claude-solo")
	if err != nil || !strings.HasPrefix(out, "recipe claude-solo from project (gs://fugaro-runs-proj-1234/fugaro/recipes/claude-solo.yaml)\nsha256 "+recipe.Sum([]byte(projectSolo))+"\n\n") ||
		!strings.HasSuffix(out, projectSolo) {
		t.Fatalf("out = %q, %v", out, err)
	}
}

func TestRecipesShowPrintsNote(t *testing.T) {
	newCloudFixture(t) // custom runs bucket: project recipes are not read
	out, errOut, err := execute(t, "recipes", "show", "default")
	if err != nil || !strings.HasPrefix(out, "recipe default from catalog") || !strings.Contains(errOut, "note: project recipes need the default runs bucket name") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

// The agent check comes before any cloud access: with no project at all the
// refusal is still the agent's, not "no project".
func TestRecipesPublishAgentCheckComesFirst(t *testing.T) {
	isolateProjects(t, t.TempDir())
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
}

// What a replacement shows is exactly the lines that change.
func TestRecipesPublishDiffIsExact(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil {
		t.Fatal(err)
	}
	head := "replacing the project's team (sha256 " + recipe.Sum([]byte(projectTeam)) + " -> " + recipe.Sum([]byte(repoTeam)) + "):\n"
	_, body, ok := strings.Cut(errOut, head)
	if !ok || body != lineDiff(projectTeam, repoTeam) || body != "  +   - review: { max_rounds: 3 }\n  -   - review: {}\n" {
		t.Fatalf("stderr %q", errOut)
	}
}

// A publisher that wins the race between the read and the write is not
// overwritten: the second one is refused and the first one's content stays.
func TestRecipesPublishRefusesAConcurrentPublisher(t *testing.T) {
	const theirs = "version: 1\nname: team\nsteps:\n  - review: { max_rounds: 9 }\n"
	for _, existing := range []bool{true, false} {
		f := newCloudFixture(t)
		recipes := map[string]string{}
		if existing {
			recipes["team"] = projectTeam
		}
		projectRecipesFixture(t, f, recipes)
		noAgentSession(t)
		old := recipePublishRace
		recipePublishRace = func(ctx context.Context, b *blobx.Bucket, key string) {
			if err := b.Bucket.WriteAll(ctx, key, []byte(theirs), nil); err != nil {
				t.Error(err)
			}
		}
		path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
		_, _, err := execute(t, "recipes", "publish", path)
		recipePublishRace = old
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") || !strings.Contains(err.Error(), "another publisher") {
			t.Fatalf("existing=%v: err = %v", existing, err)
		}
		got, rerr := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team"))))
		if rerr != nil || string(got) != theirs {
			t.Fatalf("existing=%v: object = %q, %v", existing, got, rerr)
		}
	}
}

func TestRecipesPublishRefusesAnOversizedObject(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": strings.Repeat("# x\n", 6000)})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "delete it by hand") {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team"))))
	if len(got) != 24000 {
		t.Fatalf("the oversized object was replaced: %d bytes", len(got))
	}
}

func TestRecipesPublishAndValidateRefuseAFifo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "team.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	noAgentSession(t)
	if _, _, err := execute(t, "recipes", "validate", fifo); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("validate: %v", err)
	}
	newCloudFixture(t)
	if _, _, err := execute(t, "recipes", "publish", fifo); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("publish: %v", err)
	}
}

// fugaro recipes validate|publish FILE must refuse a symlink, not follow
// it: a symlink could name any file the user running the command can read.
//
// Mutation (run, restore): revert readRecipeFile to open the path
// directly (os.OpenFile without O_NOFOLLOW, as it did before), and this
// test fails: validate reads the file through the symlink and reports it
// valid.
func TestRecipesValidateRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	good := writeRecipeFile(t, dir, "team.yaml", projectTeam)
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "recipes", "validate", link); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
}

// validate and publish agree on the file name rule, wherever it is run.
func TestRecipesValidateStemRule(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "mine.yaml", projectTeam) // named team inside
	t.Chdir(dir)
	out, _, err := execute(t, "recipes", "validate", "mine.yaml")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "the file name and the name must agree") {
		t.Fatalf("out %q, err %v", out, err)
	}
	// Under .fugaro/recipes with a relative path, from inside it.
	writeRecipeFile(t, dir, ".fugaro/recipes/mine.yaml", projectTeam)
	t.Chdir(filepath.Join(dir, ".fugaro", "recipes"))
	if out, _, err = execute(t, "recipes", "validate", "mine.yaml"); ExitCode(err) != ExitUserError || !strings.Contains(out, "must agree") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// A bidi override in a description (valid YAML) and an escape sequence in
// an old object (invalid, so only the replacement diff shows it).
const (
	bidiRecipe = "version: 1\nname: team\ndescription: \"evil \\u202e txt\"\nsteps:\n  - review: {}\n"
	escRecipe  = "version: 1\nname: team\n# \x1b[2Jclear\nsteps:\n  - review: {}\n"
)

func TestRecipesOutputIsPrintable(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": bidiRecipe})
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"recipes", "ls"}, {"recipes", "show", "team"}} {
		out, errOut, err := execute(t, args...)
		if err != nil || strings.ContainsRune(out+errOut, '\u202e') || !strings.Contains(out, "\\u202e") {
			t.Fatalf("%v: out %q, stderr %q, err %v", args, out, errOut, err)
		}
	}
	writeBucketFile(t, f, recipe.ObjectKey("team"), escRecipe)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || strings.ContainsRune(errOut, 0x1b) || !strings.Contains(errOut, "\\u001b[2Jclear") {
		t.Fatalf("publish: stderr %q, err %v", errOut, err)
	}
}

// Decision D9: ls and show read the bucket, whatever a fresh cache holds.
func TestRecipesLsAndShowRefreshTheCache(t *testing.T) {
	for _, args := range [][]string{{"recipes", "show", "team"}, {"recipes", "ls"}} {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
		testutil.IsolateGit(t)
		t.Chdir(t.TempDir())
		stale := localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: repoTeam}
		if err := localcfg.SaveRecipeCache(os.Getenv, "aurora", "team", stale); err != nil {
			t.Fatal(err)
		}
		out, _, err := execute(t, args...)
		if err != nil || (args[1] == "show" && !strings.HasSuffix(out, projectTeam)) {
			t.Fatalf("%v: out %q, err %v", args, out, err)
		}
		if e, ok := localcfgRecipeCached(t, "team"); !ok || e.YAML != projectTeam {
			t.Fatalf("%v: cache = %+v, %v", args, e, ok)
		}
	}
}

func TestRecipesLsReportsSkippedFiles(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	writeBucketFile(t, f, recipe.ObjectPrefix+"Bad_Name.yaml", projectTeam)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/notes.txt", "x")
	t.Chdir(dir)
	_, errOut, err := execute(t, "recipes", "ls")
	if err != nil || !strings.Contains(errOut, "note: skipped .fugaro/recipes/notes.txt") || !strings.Contains(errOut, "note: skipped gs://fugaro-runs-proj-1234/"+recipe.ObjectPrefix+"Bad_Name.yaml") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}
