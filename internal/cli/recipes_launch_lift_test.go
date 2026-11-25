package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// repoRecipe is a repository's own recipe under a name no catalog holds.
const repoRecipe = "version: 1\nname: mine\nsteps:\n  - review: { max_rounds: 1 }\n"

// releaseCopy is the first job image whose runner parses a project recipe's
// copy name (recipeCopySince, the release-A gate's first trigger). Every task
// this file's launches write carries a copy — that is what a repository's own
// recipe reaches a run as (D3303, R7) — so a fixture whose launch must reach
// its own subject, rather than be refused on the image, writes this base
// rather than the releaseSeal the package's other fixtures use.
const releaseCopy = "ghcr.io/dimipaun/fugaro-base:" + recipeCopySince

// seedChosenRootOnACopyImage is seedChosenRoot for a follow-up whose task will
// carry a copy: the record seedChosenRoot writes names releaseSeal, and the
// release-A gate would refuse the copy before the follow-up's own subject.
func seedChosenRootOnACopyImage(t *testing.T, f *cloudFixture, chosen *runstore.RecipeRecord) {
	t.Helper()
	seedChosenRoot(t, f, chosen)
	writeBuildRecord(t, f, appSlug, "web", releaseCopy)
}

// publishCopy writes a repository's own recipe as the project's copy of it and
// answers the copy name (D3302): the name recipe.CopyName derives for the
// repository, and the text with its name: line rewritten, which is what the
// migration publishes and so what the store holds.
func publishCopy(t *testing.T, f *cloudFixture, repo, name, text string) string {
	t.Helper()
	copied := recipe.CopyName(name, "github", repo)
	if copied == "" {
		t.Fatalf("no copy name for recipe %s of %s", name, repo)
	}
	renamed := strings.Replace(text, "name: "+name, "name: "+copied, 1)
	if renamed == text {
		t.Fatalf("the text of %s carries no name: %s line to rewrite", name, name)
	}
	writeBucketFile(t, f, recipe.ObjectKey(copied), renamed)
	return copied
}

// TestLaunchEmbedsTheCopy is R7 on the launch paths that used to leave the
// recipe to the run: a repository's recipe reaches a run as the project's copy
// of it, embedded in the task, because the runner reads neither the
// repository's folder (D3237) nor anything but the task, the project store is
// the launcher's to read, and a task naming a repository recipe is refused at
// bootstrap (D3304) after the run was paid for.
//
// The three paths are the ones a repository recipe can enter by: a first run
// whose document names it (agent.recipe, read here from the ref's own file
// because the launch stands outside every checkout), a follow-up inheriting
// the name from the previous run's task, and a follow-up that inherits no
// recipe at all and lands on the repository's own default.
func TestLaunchEmbedsTheCopy(t *testing.T) {
	t.Run("a first run from outside a checkout", func(t *testing.T) {
		f := newProjectJobFixture(t)
		// The copy this launch embeds needs the release A base: the fixture's
		// own is the node's release (nodeRelease), below recipeCopySince.
		rewriteConfig(t, "fugaro-base:"+nodeRelease, "fugaro-base:"+recipeCopySince)
		projectRecipesFixture(t, f.cloudFixture, nil)
		copied := publishCopy(t, f.cloudFixture, "acme/app", "mine", repoRecipe)
		s := newServiceReadFixture(t)
		s.document = strings.Replace(projectJobRepoYAML, "  auth: api-key", "  auth: api-key\n  recipe: mine", 1)
		s.use(t, f.cloudFixture)
		isolateCache(t)
		testutil.IsolateGit(t)
		t.Chdir(t.TempDir())

		id := wallRunIDAt(0, "120000", "a001")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "Add a feature"); err != nil {
			t.Fatalf("a launch of a repository whose ref names a repository recipe: %v", err)
		}
		got := readSpec(t, f.cloudFixture, id).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("task recipe = %+v, want the project's copy of mine (%s) embedded: agent.recipe was read from the ref's file, so what it names has to reach the run as its copy (R7)", got, copied)
		}
	})
	t.Run("a follow-up inheriting the name", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		copied := publishCopy(t, f, "acme/app", "mine", repoRecipe)
		writeBuildRecord(t, f, appSlug, "web", releaseCopy)
		root := firstRunSpec(rootID)
		root.Recipe = &task.Recipe{Name: "mine", Source: string(recipe.SourceRepo)}
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)

		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
			t.Fatal(err)
		}
		got := readSpec(t, f, fuID).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("follow-up recipe = %+v, want the project's copy of mine (%s) embedded: the name the previous task inherited is resolved and embedded, not carried as a name", got, copied)
		}
	})
	t.Run("the default fallback", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		def := "version: 1\nname: default\nsteps:\n  - review: { max_rounds: 1 }\n"
		copied := publishCopy(t, f, "acme/app", "default", def)
		seedChosenRootOnACopyImage(t, f, nil)

		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
			t.Fatal(err)
		}
		got := readSpec(t, f, fuID).Recipe
		want := strings.Replace(def, "name: default", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("follow-up recipe = %+v, want the repository's own copy of default (%s) embedded: the fallback embeds what the store holds for this repository", got, copied)
		}
	})
}

// TestFollowUpCarriesACopyName is the name the launcher used to drop: a copy
// name (recipe.IsCopyName) is not a recipe name (NameRE), so the follow-up's
// inherited recipe was refused as unnameable and fell through to the default
// without a word — the copy the previous run ran is exactly what it must keep.
func TestFollowUpCarriesACopyName(t *testing.T) {
	t.Run("the previous run's record", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		copied := publishCopy(t, f, "acme/app", "mine", repoRecipe)
		seedChosenRootOnACopyImage(t, f, &runstore.RecipeRecord{Name: copied, Source: string(recipe.SourceProject), SHA256: strings.Repeat("a", 64)})

		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
		if err != nil {
			t.Fatal(err)
		}
		got := readSpec(t, f, fuID).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("follow-up recipe = %+v, want the copy %s the runner chose for the previous run, embedded", got, copied)
		}
		if !strings.Contains(errOut, "keeping recipe "+copied) {
			t.Fatalf("stderr = %q, want the line naming the copy it kept", errOut)
		}
	})
	t.Run("--recipe names the copy", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		copied := publishCopy(t, f, "acme/app", "mine", repoRecipe)
		writeBuildRecord(t, f, appSlug, "web", releaseCopy)
		root := firstRunSpec(rootID)
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)

		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--recipe", copied, "x"); err != nil {
			t.Fatalf("a follow-up naming the copy %s: %v", copied, err)
		}
		got := readSpec(t, f, fuID).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.YAML != want {
			t.Fatalf("follow-up recipe = %+v, want %s embedded", got, copied)
		}
	})
}

// TestNoTaskCarriesARepoRecipe is R7 over every launch path a repository
// recipe can enter by, including the ones that still hold one as a leftover
// from an older release: the task carries the project's copy, embedded, or —
// for a launch that asked for nothing at all — no recipe, which is the
// runner's to resolve from the catalog. Source: repo is the shape the
// release-A runner refuses (D3304), and the launcher writes none of it.
func TestNoTaskCarriesARepoRecipe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		launch func(t *testing.T) (*cloudFixture, *task.Spec)
	}{
		{
			name: "a first run with --recipe, the folder still there",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, map[string]string{"mine": repoRecipe})
				recipeCheckout(t, f, "", map[string]string{recipe.RepoPath("mine"): repoRecipe})
				writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseSeal)
				id := wallRunIDAt(0, "120000", "b001")
				if _, _, err := execute(t, "run", "--run-id", id, "--recipe", "mine", "A task"); err != nil {
					t.Fatal(err)
				}
				return f, readSpecOf(t, f, mustSlug("github", "acme/other"), id)
			},
		},
		{
			name: "a first run whose document names it",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, nil)
				publishCopy(t, f, "acme/other", "mine", repoRecipe)
				recipeCheckout(t, f, "  recipe: mine\n", map[string]string{recipe.RepoPath("mine"): repoRecipe})
				writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseCopy)
				id := wallRunIDAt(0, "120000", "b002")
				if _, _, err := execute(t, "run", "--run-id", id, "A task"); err != nil {
					t.Fatal(err)
				}
				return f, readSpecOf(t, f, mustSlug("github", "acme/other"), id)
			},
		},
		{
			name: "a follow-up inheriting the name",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, nil)
				publishCopy(t, f, "acme/app", "mine", repoRecipe)
				writeBuildRecord(t, f, appSlug, "web", releaseCopy)
				root := firstRunSpec(rootID)
				root.Recipe = &task.Recipe{Name: "mine", Source: string(recipe.SourceRepo)}
				seedSpec(t, f, root, false)
				rec := prRecord(rootID, "", 7, 1)
				rec.StartedAt = time.Now().Add(-72 * time.Hour)
				writeRecord(t, f, rootID, rec)
				if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
					t.Fatal(err)
				}
				return f, readSpec(t, f, fuID)
			},
		},
		{
			name: "a follow-up inheriting the name the runner chose",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, nil)
				publishCopy(t, f, "acme/app", "mine", repoRecipe)
				seedChosenRootOnACopyImage(t, f, &runstore.RecipeRecord{Name: "mine", Source: string(recipe.SourceRepo)})
				if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
					t.Fatal(err)
				}
				return f, readSpec(t, f, fuID)
			},
		},
		{
			name: "the default fallback on a repository's own default",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, nil)
				publishCopy(t, f, "acme/app", "default", "version: 1\nname: default\nsteps:\n  - review: { max_rounds: 1 }\n")
				seedChosenRootOnACopyImage(t, f, nil)
				if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
					t.Fatal(err)
				}
				return f, readSpec(t, f, fuID)
			},
		},
		{
			name: "the default fallback on nothing at all",
			launch: func(t *testing.T) (*cloudFixture, *task.Spec) {
				f := newCloudFixture(t)
				projectRecipesFixture(t, f, nil)
				seedChosenRoot(t, f, nil)
				if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
					t.Fatal(err)
				}
				return f, readSpec(t, f, fuID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, spec := tc.launch(t)
			if spec.Recipe != nil && spec.Recipe.Source == string(recipe.SourceRepo) {
				t.Fatalf("task recipe = %+v: a run no longer reads the repository's folder (D3237), so no task the launcher writes may name it", spec.Recipe)
			}
		})
	}
}

// TestLaunchPrintsTheCleanupLines is the launch's half of the cleanup diff
// (D3296): a checkout that still holds .fugaro/recipes/ while the project
// store holds what it says is offered the removal, in the same notes channel
// the other launch lines use, and nothing is deleted.
func TestLaunchPrintsTheCleanupLines(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"mine": repoRecipe})
	recipeCheckout(t, f, "", map[string]string{recipe.RepoPath("mine"): repoRecipe})
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseSeal)

	_, errOut, err := execute(t, "run", "--run-id", "20261124-120000-c001", "--recipe", "mine", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, recipe.RepoPath("mine")+" is in the project store as mine") ||
		!strings.Contains(errOut, "remove the folder with `git rm -r "+recipe.RepoDir+"`") {
		t.Fatalf("stderr = %q, want the cleanup diff the store's copy earns", errOut)
	}
	if _, err := os.Stat(filepath.FromSlash(recipe.RepoPath("mine"))); err != nil {
		t.Fatalf("the launch deleted the leftover file, and it offers the diff rather than applying it: %v", err)
	}
}
