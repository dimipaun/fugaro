package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// l8_skew_test.go is S5's half of lift L8: the release-A launch gate
// (checkL8Image, checkCloneL8Image), one case per trigger R16, D3297 and D3302
// name, and the notes a launch prints for the lifted keys its default branch
// still writes.

// l8Head and l8Workflow are the repository every launch here starts from, each
// case adding what its trigger writes: a file committed at main, which is both
// the ref a launch of it resolves (git.base_branch) and the default branch this
// gate's own read answers with, so one commit is what the run reads and what the
// gate judges.
const (
	l8Head     = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n"
	l8Workflow = "workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"
)

// l8AgentKeys is a repository agent block writing two lifted keys — the model a
// run's coder takes and the background one — which is what the notes are read
// from.
const l8AgentKeys = "agent:\n" + pinnedAgent + "\n"

// l8Base is the base image a build record names for a job image built from
// release rel.
func l8Base(rel string) string { return "ghcr.io/dimipaun/fugaro-base:" + rel }

// l8Checkout makes the working directory a checkout of acme/other holding files,
// committed at main, with the Cloud Run job a launch of workflow svc resolves.
func l8Checkout(t *testing.T, f *cloudFixture, files map[string]string) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	all := map[string]string{"build.sh": "true\n", "test.sh": "true\n"}
	for path, text := range files {
		all[path] = text
	}
	testutil.WriteFiles(t, dir, all)
	commitRef(t, dir, "main")
	t.Chdir(dir)
}

// The four launches below, one per trigger: each builds its fixture, writes the
// build record of a job image built from base image rel and returns the fixture
// — for the executions a refusal must not have started — with what the launch
// answered.
type l8Launch func(t *testing.T, rel string) (*cloudFixture, error)

// l8LiftedKeyLaunch is T1's repository: its own file writes agent.models.background,
// one of the keys C1 lists, so an image before release A runs a runner that
// honours it.
func l8LiftedKeyLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	f := newCloudFixture(t)
	l8Checkout(t, f, map[string]string{"fugaro.yaml": l8Head + "agent: { auth: api-key, models: { background: claude-haiku-4-5 } }\n" + l8Workflow})
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", l8Base(rel))
	_, _, err := execute(t, "run", "--run-id", "20261124-100000-abcd", "A task")
	return f, err
}

// l8RecipesFolderLaunch is R16's repository: its default branch holds
// .fugaro/recipes/, which release A's launcher no longer reads a recipe from
// (PR1, PR2). The file is named team and not default, so nothing in this
// launcher resolves it: the folder alone is what the gate is judged on.
func l8RecipesFolderLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	f := newCloudFixture(t)
	l8Checkout(t, f, map[string]string{
		"fugaro.yaml":               l8Head + "agent: { auth: api-key }\n" + l8Workflow,
		".fugaro/recipes/team.yaml": "version: 1\nname: team\nsteps:\n  - review: {}\n",
	})
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", l8Base(rel))
	_, _, err := execute(t, "run", "--run-id", "20261124-100000-abcd", "A task")
	return f, err
}

// l8ABReposLaunch is AB1's project: the layer it resolves over writes ab.repos,
// the per-repository map an older runner's strict decoder refuses whole. The
// repository's own file writes nothing lifted, and the models the launch needs
// are the layer's own defaults.agent.models, where the project states them —
// naming them in the file would trip the lifted-key trigger first, which is what
// that case is for.
func l8ABReposLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, l8ABReposLayer())
	dir := layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, l8Base(rel))
	_, _, err := execute(t, "run", "--run-id", "20261124-100000-abcd", "A task")
	return f, err
}

// l8ABReposNoCheckoutLaunch is the same project launched from outside a
// checkout of the repository (a --repo launch): the configuration the checkout
// resolves over is not there to read, so the layer the task itself carries is
// the only place the launcher can see ab.repos — and it is the layer the run
// reads, which is why the trigger cannot be read from the checkout alone.
func l8ABReposNoCheckoutLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, l8ABReposLayer())
	writeBuildRecord(t, f, appSlug, "web", l8Base(rel))
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261124-100000-abcd", "A task")
	return f, err
}

// l8ABReposLayer is the project layer both ab.repos cases publish: AB1's key,
// and the models the layer states itself, because a repository that named them
// would trip the lifted-key trigger first.
func l8ABReposLayer() string {
	layer := strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n",
		"  agent: { auth: api-key, models: { coder: claude-sonnet-5-5 } }\n", 1)
	return layer + "ab:\n  repos: { acme/other: 0 }\n"
}

// l8CopyNameLaunch is D3302's task: one that embeds a project recipe's copy
// name, which an older runner's recipe parser refuses at bootstrap. A copy is
// resolved from the project store by release A's own lookup (D3303), so the
// name is built the way the copy is (recipe.CopyName) rather than typed here,
// and the gate is called the way the launch calls it — the task's field is what
// is judged, so no checkout is needed for it.
func l8CopyNameLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	t.Helper()
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	writeBuildRecord(t, f, appSlug, "web", l8Base(rel))
	spec := &task.Spec{Version: 1, RunID: "20261124-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: l8CopyName(t), Source: string(recipe.SourceProject)}}
	return f, checkL8Image(context.Background(), env, appSlug, spec, "base", &bytes.Buffer{})
}

// l8CopyNameCloneLaunch is the same task on the gate's other branch: a
// checkout: clone workflow's job is built per launch and so has no build record
// to judge, which is what checkCloneL8Image is for. Its judge is the
// installation's own base image (base_images.base), and its fix is the base
// image's (fugaro update --base base). The trigger is again the copy name, the
// one that needs no checkout.
func l8CopyNameCloneLaunch(t *testing.T, rel string) (*cloudFixture, error) {
	t.Helper()
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	lc := *env.lc
	lc.BaseImages = map[string]string{config.BaseKind: l8Base(rel)}
	env.lc = &lc
	spec := &task.Spec{Version: 1, RunID: "20261124-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: l8CopyName(t), Source: string(recipe.SourceProject)}}
	return f, checkCloneL8Image(context.Background(), env, spec, &bytes.Buffer{})
}

// l8CopyName is acme/app's copy of the recipe review, the name D3302 spells.
func l8CopyName(t *testing.T) string {
	t.Helper()
	name := recipe.CopyName("review", "github", "acme/app")
	if !recipe.IsCopyName(name) {
		t.Fatalf("recipe.CopyName gave %q, which is no copy name: this test is about the name a task embeds", name)
	}
	return name
}

// TestL8GateRefusesAnOlderImage is the release-A launch gate: a job image built
// from a base before l8Since is refused, whatever writes the trigger — the
// default branch's lifted key, its .fugaro/recipes/, the layer's ab.repos or the
// copy name a task embeds — and the boundary is the constant's own: the release
// before it is refused (0.29.0, the release L1 ships in, D3300), l8Since and the
// release above it are launched, and every refusal is the shared image gate's,
// naming the release, the image to move and `fugaro update` as the fix.
func TestL8GateRefusesAnOlderImage(t *testing.T) {
	below, above := shiftRelease(t, l8Since, -1), shiftRelease(t, l8Since, 1)
	for _, c := range []struct {
		name string
		// launch is the trigger's own repository, launched on base image rel.
		launch l8Launch
		// want is what this trigger's refusal has to name beyond the fix and
		// the release every image gate names: the subject, what the image has
		// to know, and the release where it is not l8Since's own.
		want []string
	}{
		{
			name:   "lifted-key",
			launch: l8LiftedKeyLaunch,
			want: []string{
				"fugaro.yaml's agent.models.background on the default branch of acme/other",
				"whose runner knows a repository's fugaro.yaml with the keys the project owns dropped and named",
			},
		},
		{
			name:   "recipes-folder",
			launch: l8RecipesFolderLaunch,
			want: []string{
				".fugaro/recipes/ on the default branch of acme/other",
				"whose runner knows a recipe the project store holds rather than .fugaro/recipes/",
			},
		},
		{
			name:   "ab-repos",
			launch: l8ABReposLaunch,
			want: []string{
				"the project layer's ab.repos (read for acme/other)",
				"whose runner knows the project layer's ab.repos, its per-repository sampling rate",
				"(fugaro " + abReposSince + " or later)",
			},
		},
		{
			// The same trigger with no checkout of the repository: the layer
			// the task carries is the one the run reads, and the one this
			// launch can be judged on.
			name:   "ab-repos-outside-a-checkout",
			launch: l8ABReposNoCheckoutLaunch,
			want: []string{
				"the project layer's ab.repos (read for acme/app)",
				"whose runner knows the project layer's ab.repos, its per-repository sampling rate",
				"(fugaro " + abReposSince + " or later)",
			},
		},
		{
			name:   "copy-name",
			launch: l8CopyNameLaunch,
			want: []string{
				"the recipe " + l8CopyName(t) + " (a project recipe's copy)",
				"whose runner knows a project recipe's copy name",
				"(fugaro " + recipeCopySince + " or later)",
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, tc := range []struct {
				rel     string
				refused bool
			}{{below, true}, {l8Since, false}, {above, false}} {
				t.Run(tc.rel, func(t *testing.T) {
					f, err := c.launch(t, tc.rel)
					if !tc.refused {
						if err != nil {
							t.Fatalf("release %s: err = %v, want launched", tc.rel, err)
						}
						return
					}
					if ExitCode(err) != ExitUserError {
						t.Fatalf("err = %v, want the launch refused", err)
					}
					for _, want := range append(c.want,
						"(fugaro "+l8Since+" or later)",
						imageGateRemedy,
						causeMarkerJobImage,
					) {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("%v lacks %q", err, want)
						}
					}
					if n := len(f.run.Executions()); n != 0 {
						t.Errorf("%d executions, want none", n)
					}
				})
			}
		})
	}
}

// TestL8GateRefusesACloneLaunchAnOlderImage is the same gate on the other branch
// of freshLaunchGates: a checkout: clone workflow's job is built per launch and
// so carries no build record, so checkCloneL8Image judges the installation's
// base image and refuses it with the fix that moves it — `fugaro update --base
// base`, not the checkout's own `fugaro update`. The triggers are one judge
// (l8LaunchSkew), so this pins the branch and not the four triggers again.
func TestL8GateRefusesACloneLaunchAnOlderImage(t *testing.T) {
	for _, tc := range []struct {
		rel     string
		refused bool
	}{
		{shiftRelease(t, l8Since, -1), true},
		{l8Since, false},
		{shiftRelease(t, l8Since, 1), false},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			_, err := l8CopyNameCloneLaunch(t, tc.rel)
			if !tc.refused {
				if err != nil {
					t.Fatalf("release %s: err = %v, want launched", tc.rel, err)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want the launch refused", err)
			}
			for _, want := range []string{
				"a project recipe's copy name",
				"(fugaro " + l8Since + " or later)",
				"the checkout: clone job image of acme/app workflow web",
				"fugaro update --base base",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
		})
	}
}

// TestL8SinceHasAReleaseBullet: l8Since names 0.30.0, which is not tagged, so
// docs/release.md's Before-you-tag list is what holds the constant to the release
// actually cut — the job every other *Since constant's bullet does, and the one
// this constant's own comment claims.
func TestL8SinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `l8Since`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no l8Since bullet: the checklist is where a release verifies the constant")
	}
	for _, want := range []string{
		"currently `\"" + l8Since + "\"`",
		"abReposSince", "recipeCopySince", "checkL8Image", "checkCloneL8Image",
		"default branch", ".fugaro/recipes/", "not tagged yet",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's l8Since bullet does not say %q", want)
		}
	}
	// The two companion constants carry release A's number, which is what makes
	// one bullet and one census-row move the whole gate: a value that moved on
	// its own would refuse a release the other two admit.
	if abReposSince != l8Since || recipeCopySince != l8Since {
		t.Errorf("abReposSince = %q and recipeCopySince = %q, want both %q: release A is one release", abReposSince, recipeCopySince, l8Since)
	}
}

// TestLaunchNamesLiftedKeys is the other half of S5: a launch prints each lifted
// key its default branch writes, once, and a repository that writes none is
// printed nothing. The launch itself goes through — C1's note decides nothing,
// and release A's keys still decode and still validate — which is why the notes
// are read off stderr of a launch that succeeded.
func TestLaunchNamesLiftedKeys(t *testing.T) {
	const note = "note: the default branch of acme/other writes a key the project now owns: "
	for _, c := range []struct {
		name  string
		agent string
		want  []string
	}{
		{"two keys", l8AgentKeys, []string{"agent.model is the project's choice (D3237)", "agent.models.background is the project's choice (D3237)"}},
		{"none", "agent: { auth: api-key }\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			l8Checkout(t, f, map[string]string{"fugaro.yaml": l8Head + c.agent + l8Workflow})
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", l8Base(shiftRelease(t, l8Since, 1)))
			_, errOut, err := execute(t, "run", "--run-id", "20261124-100000-abcd", "A task")
			if err != nil {
				t.Fatalf("err = %v, want the launch: the note is not a refusal", err)
			}
			if got := strings.Count(errOut, note); got != len(c.want) {
				t.Fatalf("the launch printed %d notes, want %d:\n%s", got, len(c.want), errOut)
			}
			for _, why := range c.want {
				if got := strings.Count(errOut, note+why); got != 1 {
					t.Errorf("the notes name %q %d times, want once:\n%s", why, got, errOut)
				}
			}
		})
	}
}
