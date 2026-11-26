package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// because the launch stands outside every checkout, whatever the project has
// published), a follow-up inheriting the name from the previous run's task, and
// a follow-up that inherits no recipe at all and lands on the repository's own
// default.
func TestLaunchEmbedsTheCopy(t *testing.T) {
	t.Run("a first run from outside a checkout", func(t *testing.T) {
		f := newProjectJobFixture(t)
		// The copy this launch embeds needs the release A base, which the
		// fixture's own is: its image is the node's release (nodeRelease) and
		// that release is release A's (recipeCopySince, LM21).
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
	t.Run("a first run from outside a checkout, on a project with no profile", func(t *testing.T) {
		f := newProjectJobFixture(t)
		// The recipe is not the job: this project published a policy that
		// defines no profile, so the launch decides no job from the ref's file
		// and runs on the repository's own job — and the file still names
		// agent.recipe, which the run has to receive as its copy (R7).
		f.db.Set(projectpolicy.PathShared(), map[string]any{
			"version": projectpolicy.Version, "allow_repos": []string{"github/acme/*"}})
		// Without a profile the launch is judged on the repository's own image,
		// which the fixture built from the node's release — below recipeCopySince,
		// the base the copy this launch embeds needs.
		writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseCopy)
		projectRecipesFixture(t, f.cloudFixture, nil)
		copied := publishCopy(t, f.cloudFixture, "acme/app", "mine", repoRecipe)
		s := newServiceReadFixture(t)
		s.document = strings.Replace(projectJobRepoYAML, "  auth: api-key", "  auth: api-key\n  recipe: mine", 1)
		s.use(t, f.cloudFixture)
		isolateCache(t)
		testutil.IsolateGit(t)
		t.Chdir(t.TempDir())

		id := wallRunIDAt(0, "120001", "a002")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "Add a feature"); err != nil {
			t.Fatalf("a launch from outside a checkout on a project that published no profile: %v", err)
		}
		got := readSpec(t, f.cloudFixture, id).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("task recipe = %+v, want the project's copy of mine (%s) embedded: the recipe is read from the ref's file whether or not the document decides a job", got, copied)
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
	t.Run("the previous run's record, of the repository's own file", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		copied := publishCopy(t, f, "acme/app", "mine", repoRecipe)
		// The record of a run that read the repository's file before the copies
		// existed sums that file, and the copy is that file with its name: line
		// rewritten (R14): nothing about the recipe changed, so the launch says
		// nothing about a change.
		seedChosenRootOnACopyImage(t, f, &runstore.RecipeRecord{
			Name: "mine", Source: string(recipe.SourceRepo), SHA256: recipe.Sum([]byte(repoRecipe))})

		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
		if err != nil {
			t.Fatal(err)
		}
		got := readSpec(t, f, fuID).Recipe
		want := strings.Replace(repoRecipe, "name: mine", "name: "+copied, 1)
		if got == nil || got.Name != copied || got.Source != string(recipe.SourceProject) || got.YAML != want {
			t.Fatalf("follow-up recipe = %+v, want the copy %s of the repository's file the previous run ran, embedded", got, copied)
		}
		if strings.Contains(errOut, "has changed since run") {
			t.Fatalf("stderr = %q, want no change reported: the only difference between the record's file and the copy is the migration's rename (R14)", errOut)
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

// repoReviewRecipe is a repository's own .fugaro/recipes/review-only.yaml: the
// catalog recipe under the catalog's own name with a line of the repository's
// own, which is the file the two review-mode launches read out of the checkout
// before this — a task naming a repository recipe, which the release-A runner
// refuses at bootstrap (D3304) after the run was paid for.
const repoReviewRecipe = "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n# kept by the repository\n"

// TestReviewLaunchCarriesNoRepoRecipe is R7 on the two review-mode paths, the
// ones TestNoTaskCarriesARepoRecipe's launches cannot reach: `fugaro review --pr
// N` (review.go) and `fugaro run --recipe <mode: review> --pr N` (run.go's
// review branch) both resolved the name over the checkout's .fugaro/recipes/
// first, so a repository keeping a review recipe of its own got a task naming
// it. No launch reads that folder any more (D3237): the name resolves over the
// project store and the catalog, and the task carries what it answered.
func TestReviewLaunchCarriesNoRepoRecipe(t *testing.T) {
	catalog, ok := recipe.CatalogText(reviewOnlyRecipe)
	if !ok {
		t.Fatalf("the catalog holds no recipe %s", reviewOnlyRecipe)
	}
	t.Run("fugaro review", func(t *testing.T) {
		f := newReviewFixture(t)
		dir := projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
		testutil.WriteFiles(t, dir, map[string]string{recipe.RepoPath(reviewOnlyRecipe): repoReviewRecipe})

		if _, _, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal"); err != nil {
			t.Fatalf("a review from a checkout keeping its own %s.yaml: %v", reviewOnlyRecipe, err)
		}
		slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
		if err != nil {
			t.Fatal(err)
		}
		wantEmbeddedRecipe(t, reviewTaskSpec(t, f.cloudFixture, slug, id).Recipe, catalog)
	})
	t.Run("fugaro run --recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		recipeCheckout(t, f, "", map[string]string{recipe.RepoPath(reviewOnlyRecipe): repoReviewRecipe})
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)

		id := wallRunIDAt(0, "120002", "a003")
		if _, _, err := execute(t, "run", "--recipe", reviewOnlyRecipe, "--pr", "7", "--run-id", id); err != nil {
			t.Fatalf("a review by name from a checkout keeping its own %s.yaml: %v", reviewOnlyRecipe, err)
		}
		wantEmbeddedRecipe(t, readSpecOf(t, f, mustSlug("github", "acme/other"), id).Recipe, catalog)
	})
}

// wantEmbeddedRecipe wants rcp to be text, the recipe a launch resolved by
// name, and never the repository's own file: a task naming a repository recipe
// is the shape the release-A runner refuses at bootstrap (D3304), and the
// launcher writes none of it (R7).
func wantEmbeddedRecipe(t *testing.T, rcp *task.Recipe, text []byte) {
	t.Helper()
	switch {
	case rcp == nil:
		t.Fatalf("the launch's task carries no recipe, though it resolved one by name")
	case rcp.Source == string(recipe.SourceRepo):
		t.Fatalf("task recipe = %+v: no launch reads the checkout's %s (D3237), so none may name it", rcp, recipe.RepoDir)
	case rcp.YAML != string(text):
		t.Fatalf("task recipe YAML = %q, want the %s the name resolves to:\n%s", rcp.YAML, rcp.Name, text)
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
