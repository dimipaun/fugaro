package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lift"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// recipes_e2e_lift_test.go is I3 (docs/repo-override-rulings/implementation-plan.md):
// L8's recipe half end to end, from `fugaro update` to the run the launch
// buys. One world holds both halves — the migration's runs bucket is the
// launcher's, so what the update publishes is what a launch reads — and the
// three paths a repository's own recipe can enter a run by are walked over it:
// a first run out of a checkout whose default branch still holds the file, a
// follow-up of a run whose task named it, and a launch from outside every
// checkout, which learns the name from the repository's own document at its
// default branch. Each is a repository whose folder held .fugaro/recipes/
// review.yaml: twice, once where that name clashed with another repository's
// file and once where nothing else answered it.

// liftE2EReview is a repository's own recipe, the file the migration reads and
// the text a run has to reach. The rounds are the fixture's handle on which
// text a run resolved: the store's copy says two, another repository's file
// says three.
func liftE2EReview(rounds int) string {
	return fmt.Sprintf("version: 1\nname: review\nsteps:\n  - review: { max_rounds: %d }\n", rounds)
}

// liftE2EWorld is one project after I2's update: the launcher's fixture, whose
// runs bucket the migration wrote into, and what that migration published for
// acme/app.
type liftE2EWorld struct {
	f *cloudFixture
	// name and stored are the recipe a run of acme/app resolves off the
	// project store and the bytes it resolves to: the copy the migration
	// published where the file clashed, the file itself where it did not.
	name   string
	stored string
}

// newLiftE2EWorld migrates a project whose repositories keep review.yaml and
// answers the world a run of acme/app sees. second is what acme/two's file
// held: the same bytes as acme/app's, which leaves the name one nothing else
// answers (published flat), or another repository's, which makes it a clash
// (a copy each, D3301).
func newLiftE2EWorld(t *testing.T, second string) *liftE2EWorld {
	t.Helper()
	f := newProjectJobFixture(t).cloudFixture
	projectRecipesFixture(t, f, nil)
	// The layer the project published before this update, without its agent
	// line: that block is a flow map, and a lifted value cannot be added
	// inside one (TestUpdateLiftsEveryKey's rig does the same).
	writeBucketFile(t, f, config.LayerKey, strings.Replace(testProjectLayer, layerAgentLine, "", 1))
	// What the launch half needs stays; only the update rig's own world is
	// built on top of it, and the launches below read the launcher's again.
	launcher := liftE2EEnvSnapshot()

	r := newLayerRig(t, "")
	liftE2EShareBucket(t, r, f)
	app := liftE2EReview(2)
	other := second
	lifted := []byte("agent:\n  models:\n    background: claude-haiku-4-5\n")
	old := liftRepositories
	liftRepositories = func(_ context.Context, _ *initEngine, _ *localcfg.Config, _ []string) ([]lift.Repo, []string, error) {
		return []lift.Repo{
			{Repo: "acme/app", Provider: "github", Config: lifted, Recipes: []lift.RecipeFile{{Path: "review.yaml", Data: []byte(app)}}},
			{Repo: "acme/two", Provider: "github", Config: lifted, Recipes: []lift.RecipeFile{{Path: "review.yaml", Data: []byte(other)}}},
		}, nil, nil
	}
	t.Cleanup(func() { liftRepositories = old })
	fakeTerminal(t)

	if out, errOut, err := executeStdin(t, "", "update"); err != nil {
		t.Fatalf("fugaro update did not migrate the project's recipes: %v\n%s%s", err, out, errOut)
	}
	liftE2EEnvRestore(t, launcher)

	w := &liftE2EWorld{f: f}
	if second == app {
		// Nothing else answers the name: the store holds the repository's own
		// file under it.
		w.name, w.stored = "review", app
	} else {
		w.name = recipe.CopyName("review", "github", "acme/app")
		if w.name == "" {
			t.Fatal("no copy name for recipe review of acme/app")
		}
		renamed, err := recipe.Rename([]byte(app), w.name)
		if err != nil {
			t.Fatal(err)
		}
		w.stored = string(renamed)
	}
	return w
}

// liftE2EShareBucket points the update rig at the launcher's own runs bucket: the
// migration's store, records and layer are the ones the launches read.
func liftE2EShareBucket(t *testing.T, r *layerRig, f *cloudFixture) {
	t.Helper()
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := "bucket_url: file://" + r.runs
	if !strings.Contains(string(data), old) {
		t.Fatalf("the rig's config no longer holds %q:\n%s", old, data)
	}
	out := strings.Replace(string(data), old, "bucket_url: file://"+filepath.Join(f.dir, "runs"), 1)
	if err := os.WriteFile(r.cfg, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// liftE2EEnvSnapshot is the environment as it stands: the two worlds this file
// joins are built out of environment variables (the config's path, the XDG
// directories, HOME), and each half reads its own.
func liftE2EEnvSnapshot() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// liftE2EEnvRestore puts the environment back where a snapshot found it, so
// the half that built the update rig stops standing in the launcher's way.
func liftE2EEnvRestore(t *testing.T, want map[string]string) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := want[k]; !ok {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	for k, v := range want {
		t.Setenv(k, v)
	}
}

// claim is what a launch left in the run's task, checked against what the
// migration published: the store's name and bytes, and no other source.
func (w *liftE2EWorld) claim(t *testing.T, spec *task.Spec) {
	t.Helper()
	got := spec.Recipe
	if got == nil || got.Name != w.name || got.Source != string(recipe.SourceProject) ||
		got.YAML != w.stored || got.SHA256 != recipe.Sum([]byte(w.stored)) {
		t.Fatalf("task recipe = %+v, want the project store's %s (%q)", got, w.name, w.stored)
	}
}

// firstRun is a first run out of a checkout of acme/app whose default branch
// still holds the file: the launch resolves the name off the store, and the
// migration's record is what lets it (D3308).
func (w *liftE2EWorld) firstRun(t *testing.T) {
	t.Helper()
	appCopyCheckout(t, map[string]string{".fugaro/recipes/review.yaml": liftE2EReview(3)})
	id := wallRunIDAt(0, "120000", "e2e1")
	if _, _, err := execute(t, "run", "--run-id", id, "--recipe", "review", "Add a feature"); err != nil {
		t.Fatalf("a first run of a migrated repository: %v", err)
	}
	w.claim(t, readSpec(t, w.f, id))
}

// followUp continues a run whose task named the repository's recipe, the
// shape a launch before the release wrote (Source: repo): the follow-up
// resolves that name where it resolves now and embeds what it finds (D3303).
func (w *liftE2EWorld) followUp(t *testing.T) {
	t.Helper()
	appCopyCheckout(t, map[string]string{".fugaro/recipes/review.yaml": liftE2EReview(3)})
	root := firstRunSpec(rootID)
	root.Recipe = &task.Recipe{Name: "review", Source: string(recipe.SourceRepo)}
	seedSpec(t, w.f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, w.f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
		t.Fatalf("a follow-up of a run that named a repository recipe: %v", err)
	}
	w.claim(t, readSpec(t, w.f, fuID))
}

// fromOutside launches acme/app from no checkout at all, where the name comes
// from the repository's own document at its default branch — read through the
// credentials service, the way a launch outside a checkout reads one.
func (w *liftE2EWorld) fromOutside(t *testing.T) {
	t.Helper()
	s := newServiceReadFixture(t)
	s.document = strings.Replace(projectJobRepoYAML, "  auth: api-key", "  auth: api-key\n  recipe: review", 1)
	if s.document == projectJobRepoYAML {
		t.Fatal("the fixture's repository document no longer carries the agent auth line")
	}
	s.use(t, w.f)
	testutil.IsolateGit(t)
	isolateCache(t)
	t.Chdir(t.TempDir())
	id := wallRunIDAt(0, "120001", "e2e2")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "Add a feature"); err != nil {
		t.Fatalf("a launch from outside a checkout: %v", err)
	}
	w.claim(t, readSpec(t, w.f, id))
}

// TestMigratedRecipeReachesARunFromTheStore is I3's acceptance: after the
// update migrates a repository whose folder held review.yaml, every way a
// repository's recipe can enter a run reaches the project store's copy of it
// and nothing else — the file the checkout still holds, another repository's
// file of the same name and the catalog are all outranked or unread — and the
// run's task records it as the project's (source: project).
func TestMigratedRecipeReachesARunFromTheStore(t *testing.T) {
	// Every path gets a world of its own: a launch leaves an execution running
	// (the fake reports it active, as a real one is until it ends), and the
	// fixture admits two at a time.
	for _, c := range []struct {
		name string
		// second is what acme/two's folder held: the same bytes as acme/app's
		// (one text, published flat) or another repository's (a clash).
		second string
	}{
		{"a file another repository also holds", liftE2EReview(3)},
		{"a file nothing else answers", liftE2EReview(2)},
	} {
		for _, p := range []struct {
			name string
			run  func(*liftE2EWorld, *testing.T)
		}{
			{"a first run", (*liftE2EWorld).firstRun},
			{"a follow-up", (*liftE2EWorld).followUp},
			{"from outside a checkout", (*liftE2EWorld).fromOutside},
		} {
			t.Run(c.name+", "+p.name, func(t *testing.T) {
				p.run(newLiftE2EWorld(t, c.second), t)
			})
		}
	}
}
