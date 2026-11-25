package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/lift"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// appCopyCheckout makes the working directory a checkout of acme/app whose
// default branch (refs/remotes/origin/main, no network) holds folder as
// .fugaro/recipes/<name>.yaml when folder is not "". It returns the checkout's
// root.
func appCopyCheckout(t *testing.T, folder map[string]string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	files := map[string]string{
		"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
			"agent:\n  auth: api-key\nworkflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n",
		"README.md": "code\n",
	}
	for k, v := range folder {
		files[k] = v
	}
	testutil.WriteFiles(t, dir, files)
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "init")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	t.Chdir(dir)
	return dir
}

// appCopy is the project's copy of name for acme/app, and copyYAML a recipe
// text that names itself that copy.
func appCopy(name string) (copy, text string) {
	copy = recipe.CopyName(name, "github", "acme/app")
	return copy, "version: 1\nname: " + copy + "\nsteps:\n  - review: { max_rounds: 1 }\n"
}

// TestResolveRecipeOwnCopyFirst: a repository's own recipe is the project's
// copy of it (D3303), so the copy is what a run of that repository gets --
// before the flat name in the same store, and before the catalog.
func TestResolveRecipeOwnCopyFirst(t *testing.T) {
	f := newCloudFixture(t)
	copy, copyText := appCopy("team")
	projectRecipesFixture(t, f, map[string]string{copy: copyText, "team": projectTeam})
	root := appCopyCheckout(t, nil)
	env := fileEnv(t, f)
	ctx, now := context.Background(), time.Now()

	rr, err := resolveRecipe(ctx, env, root, "team", now, false)
	if err != nil || rr.Name != copy || rr.Source != recipe.SourceProject || string(rr.Text) != copyText {
		t.Fatalf("the copy did not win: %+v, %v", rr, err)
	}
	// The copy by its own name, which is how fugaro run --recipe, recipes show
	// and the run record name it.
	rr, err = resolveRecipe(ctx, env, root, copy, now, false)
	if err != nil || rr.Name != copy || string(rr.Text) != copyText {
		t.Fatalf("the copy by name: %+v, %v", rr, err)
	}
	// The copy name resolves to the flat name when the store has no copy.
	rr, err = resolveRecipe(ctx, env, root, "solo", now, false)
	if err != nil || rr.Name != "solo" || rr.Source != recipe.SourceCatalog {
		t.Fatalf("solo: %+v, %v", rr, err)
	}
}

// TestResolveRecipeIgnoresTheFolder: the checkout's .fugaro/recipes/ is not a
// layer. Its file loses to the project's recipe of the same name, and a
// catalog name it holds is still the catalog's. The repository is migrated --
// it has a record, so D3308's refusal is not what is being asked here -- and
// the file is on the default branch as well as in the working tree.
func TestResolveRecipeIgnoresTheFolder(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	root := appCopyCheckout(t, map[string]string{
		".fugaro/recipes/team.yaml": repoTeam,
		".fugaro/recipes/solo.yaml": "version: 1\nname: solo\nsteps:\n  - review: { max_rounds: 3 }\n",
	})
	writeBucketFile(t, f, lift.RecordPath(appSlug), `{"version":1}`)
	env := fileEnv(t, f)
	ctx, now := context.Background(), time.Now()

	rr, err := resolveRecipe(ctx, env, root, "team", now, false)
	if err != nil || rr.Source != recipe.SourceProject || string(rr.Text) != projectTeam {
		t.Fatalf("the folder's team won: %+v, %v", rr, err)
	}
	catalog, _ := recipe.CatalogText("solo")
	rr, err = resolveRecipe(ctx, env, root, "solo", now, false)
	if err != nil || rr.Source != recipe.SourceCatalog || string(rr.Text) != string(catalog) {
		t.Fatalf("the folder's solo won: %+v, %v", rr, err)
	}
	// A name only the folder has is unknown, not resolved.
	if _, err := resolveRecipe(ctx, env, root, "mine", now, false); err == nil || !strings.Contains(err.Error(), "recipe mine is not in project aurora") {
		t.Fatalf("a folder-only name resolved: %v", err)
	}
}

// TestRecipesCommandsAcceptACopy: a copy name is an ordinary project recipe to
// the commands (D3303, R19) -- publish replaces it, show reads it, ls lists it
// under its own name, and rm removes it, which is how a migrated copy is
// replaced or dropped.
func TestRecipesCommandsAcceptACopy(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	copy, copyText := appCopy("team")
	path := writeRecipeFile(t, t.TempDir(), copy+".yaml", copyText)

	if _, _, err := execute(t, "recipes", "publish", path); err != nil {
		t.Fatalf("publish: %v", err)
	}
	obj := filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey(copy)))
	if data, err := os.ReadFile(obj); err != nil || string(data) != copyText {
		t.Fatalf("the copy was not published: %q, %v", data, err)
	}
	if out, _, err := execute(t, "recipes", "show", copy); err != nil || !strings.Contains(out, "recipe "+copy+" from project") {
		t.Fatalf("show: out %q, err %v", out, err)
	}
	out, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil || !strings.Contains(out, `"name": "`+copy+`"`) || !strings.Contains(out, `"source": "project"`) {
		t.Fatalf("ls does not list the copy under its own name: out %s, err %v", out, err)
	}
	if _, _, err := execute(t, "recipes", "rm", copy); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(obj); err == nil {
		t.Fatal("the copy survived fugaro recipes rm")
	}
	if out, _, err := execute(t, "recipes", "ls"); err != nil || strings.Contains(out, copy) {
		t.Fatalf("ls still lists the removed copy: out %s, err %v", out, err)
	}
	// A second rm says the project has no such recipe, and a name that is not
	// a recipe name at all is refused before any cloud work.
	if _, _, err := execute(t, "recipes", "rm", copy); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "has no recipe") {
		t.Fatalf("rm twice: %v", err)
	}
	if _, _, err := execute(t, "recipes", "rm", "Bad_Name"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not a recipe name") {
		t.Fatalf("rm a bad name: %v", err)
	}
}

// TestRecipesLsSaysTheFolderIsNotRead: ls reads no checkout folder, and says
// so where a person would look for one, so a repository whose recipes were
// never published is not mistaken for one with none.
func TestRecipesLsSaysTheFolderIsNotRead(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	appCopyCheckout(t, map[string]string{
		".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: {}\n",
	})
	out, errOut, err := execute(t, "recipes", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, ".fugaro/recipes/ is not read") || !strings.Contains(errOut, "fugaro update") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(out, "mine") {
		t.Fatalf("the folder's recipe is a row:\n%s", out)
	}
}

// TestLaunchRefusesAnUnmigratedFolderRecipe: a repository whose default branch
// still holds .fugaro/recipes/<name>.yaml and has no migration record is
// refused before anything is written, naming the repository, the recipe and
// the fix (D3308); with the record, or with no such file on the default
// branch, the launch runs the project's recipe of that name as before.
func TestLaunchRefusesAnUnmigratedFolderRecipe(t *testing.T) {
	name, text := "team", projectTeam
	// Refused: the default branch holds the file and there is no record.
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{name: text})
	writeBuildRecord(t, f, appSlug, "web", releaseSeal)
	appCopyCheckout(t, map[string]string{".fugaro/recipes/" + name + ".yaml": repoTeam})
	_, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", name, "A task")
	for _, want := range []string{"recipe " + name + " is still acme/app's own file", recipe.RepoPath(name), "has no migration record", "fugaro update"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("the unmigrated folder recipe was not refused with %q: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", "runs", appSlug, "20261007-100000-abcd", "task.json")); err == nil {
		t.Fatal("a task.json was written for a refused launch")
	}

	// The same launch with the migration record: the project's recipe.
	f = newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{name: text})
	writeBuildRecord(t, f, appSlug, "web", releaseSeal)
	appCopyCheckout(t, map[string]string{".fugaro/recipes/" + name + ".yaml": repoTeam})
	writeBucketFile(t, f, lift.RecordPath(appSlug), `{"version":1}`)
	if _, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", name, "A task"); err != nil {
		t.Fatalf("a migrated repository was refused: %v", err)
	}
	if got := readSpec(t, f, "20261007-100000-abcd").Recipe; got == nil || got.Name != name || got.Source != "project" {
		t.Fatalf("task recipe = %+v", got)
	}

	// A repository whose default branch holds no such file, no record either.
	f = newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{name: text})
	writeBuildRecord(t, f, appSlug, "web", releaseSeal)
	appCopyCheckout(t, nil)
	if _, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", name, "A task"); err != nil {
		t.Fatalf("a repository with no folder recipe was refused: %v", err)
	}
	if got := readSpec(t, f, "20261007-100000-abcd").Recipe; got == nil || got.Name != name || got.Source != "project" {
		t.Fatalf("task recipe = %+v", got)
	}
}
