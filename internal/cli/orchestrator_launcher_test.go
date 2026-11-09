package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The daemon's launcher over `fugaro run`'s pre-launch path (O8, F11, T2).

// launcherFixture is one installation with an orchestrator id: the budget
// database the state and its lease live in, the runs bucket, and the launch
// path's own gates satisfied (the budget fixture is the one every launch test
// builds on).
type launcherFixture struct {
	*launchBudgetFixture
}

func newLauncherFixture(t *testing.T) *launcherFixture {
	t.Helper()
	f := &launcherFixture{launchBudgetFixture: newLaunchBudget(t, "enforce")}
	// The installation the daemon runs in is this release's, so its job image
	// is built from a base that knows every field one of its launches carries:
	// the daemon's launch passes the same gates `fugaro run` passes
	// (freshLaunchGates), and the budget fixture's own record is a release old
	// enough that the tier and the title would be refused — which is a fact
	// about those tests and not about this one. The base is a release past the
	// newest skew gate rather than a literal, so the next gate does not turn
	// every daemon test into a refusal.
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, securitySince, 1))
	return f
}

// launcher is the real launcher over the fixture's own environment, the way
// the daemon builds it once at start-up.
func (f *launcherFixture) launcher(t *testing.T) *orchestratorLauncher {
	t.Helper()
	ctx := context.Background()
	lc, _, err := openBudget(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.bucket.Close() })
	return &orchestratorLauncher{env: env, stderr: io.Discard, started: &launchInFlight{}}
}

// runStore is one run's store in the fixture's bucket.
func (f *launcherFixture) runStore(t *testing.T, runID string) *runstore.Store {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return runstore.Open(b, appSlug, runID)
}

// TestRunLauncherUsesTheLaunchClaim: the launcher starts a run at most once,
// because it starts it through the same claim `fugaro run` takes. A second
// call for a run id whose launch is already recorded starts nothing and reports
// the run that exists, which is what a state node re-issuing an intent after a
// crash depends on.
func TestRunLauncherUsesTheLaunchClaim(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	got, err := l.Launch(ctx, spec, runID)
	if err != nil {
		t.Fatalf("launching: %v", err)
	}
	if got != runID {
		t.Fatalf("the launcher started %q for the run recorded as %q", got, runID)
	}
	again, err := l.Launch(ctx, spec, runID)
	if err != nil {
		t.Fatalf("launching a run id whose launch is already recorded: %v", err)
	}
	if again != runID {
		t.Fatalf("the second call reported %q, want the run that exists (%q)", again, runID)
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the run was started %d times, want once: %v", len(execs), execs)
	}
	if _, err := f.runStore(t, runID).ReadLaunch(ctx); err != nil {
		t.Fatalf("the launch was not recorded at the run's own node: %v", err)
	}
}

// TestRunLauncherCarriesTheProjectLayer: the task the launcher stores carries
// the project layer, the way `fugaro run` puts it there. The runner resolves a
// run's configuration from the task's own copy and nowhere else, so a launch
// that skipped this step would start a run without the project's shared
// defaults — the daemon's own project selection is not something a run can read
// back.
func TestRunLauncherCarriesTheProjectLayer(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	publishedLayer(t, f.cloudFixture, testProjectLayer)
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("launching: %v", err)
	}
	ts, err := f.runStore(t, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if ts.ProjectLayer == nil {
		t.Fatal("the task carries no project layer, so the run would resolve its configuration without the project's shared defaults")
	}
	if !strings.Contains(ts.ProjectLayer.YAML, "project: aurora") {
		t.Errorf("the task's project layer is not the one published for this installation:\n%s", ts.ProjectLayer.YAML)
	}
}

// TestRunLauncherRunsThePolicyGate: the project's policy is read before
// anything of the launch is written, as `fugaro run` reads it. A repository the
// policy does not admit is a refusal with no task and no claim behind it.
func TestRunLauncherRunsThePolicyGate(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	// A policy that admits a different repository: what refuses below is the
	// allow-list and not the budget, which the fixture leaves enforcing.
	publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/other"]}`)
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "This is the launch the policy refuses."}
	const runID = "20261109-101500-abcd"

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a launch the project's policy does not admit went through")
	}
	if !strings.Contains(err.Error(), "does not admit") {
		t.Fatalf("the refusal is not the allow-list's own: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Fatalf("a refused launch started %d execution(s): %v", len(execs), execs)
	}
	if _, err := f.runStore(t, runID).ReadTask(ctx); err == nil {
		t.Fatal("a refused launch stored a task, and the gate refuses before anything is written")
	}
}

// TestRunLauncherRefusesAJobImageTheTaskOutgrew: the daemon's launch is
// `fugaro run`'s launch, so it passes that command's gates before anything of it
// is written, the per-field image gates included. The item's tier is the one
// field the daemon cannot drop — a person approved the item carrying it — so an
// installation whose job image predates the release that ships the field is a
// refusal, and what must not happen is a start: the run would reach a runner
// older than the field, which refuses the whole task at bootstrap (task.Parse
// decodes with DisallowUnknownFields), and the approved attempt would be spent
// on a run that never did any work. Nothing of the launch is left behind: no
// task, no claim, no execution.
func TestRunLauncherRefusesAJobImageTheTaskOutgrew(t *testing.T) {
	f := newLauncherFixture(t)
	// The fixture's own image is this release's; this one is the release before
	// the tier was shipped.
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, governanceSince, -1))
	ctx := context.Background()
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", RigorTier: "medium", Brief: "This is the launch the image gate refuses."}
	const runID = "20261109-101500-abcd"

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a launch onto a job image that cannot read the tier the item carries went through")
	}
	if !strings.Contains(err.Error(), "tier") {
		t.Fatalf("the refusal is not the tier's own gate: %v", err)
	}
	s := f.runStore(t, runID)
	if _, err := s.ReadTask(ctx); err == nil {
		t.Error("a refused launch stored a task, so a run the job would refuse at bootstrap is on record")
	}
	if _, err := s.ReadClaim(ctx); !errors.Is(err, runstore.ErrNotFound) {
		t.Errorf("a refused launch took the launch claim: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a refused launch started %d execution(s): %v", len(execs), execs)
	}
}

// TestRunLauncherRunsPrecheck: the budget's own pre-check runs before anything
// is started, and a refusal is a refusal — no task is stored, no claim is taken
// and no execution is started. It is the order `fugaro run` refuses in too: a
// launch the budget turns down costs no claim to release.
func TestRunLauncherRunsPrecheck(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	// The kill switch is a refusal nobody has to guess at: budget.Precheck
	// reads it, and it is on.
	f.db.Set("config/kill/global", map[string]any{"on": true, "by": "owner", "reason": "stop"})
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "This is the launch the budget refuses."}
	const runID = "20261109-101500-abcd"

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a launch the budget refuses went through")
	}
	if !strings.Contains(err.Error(), "the budget refuses the launch") {
		t.Fatalf("the refusal is not the budget's own: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Fatalf("a refused launch started %d execution(s): %v", len(execs), execs)
	}
	if _, err := f.runStore(t, runID).ReadClaim(ctx); err == nil {
		t.Fatal("a refused launch took the claim, and a refusal must cost no claim")
	}
}

// launchLauncher runs one daemon launch of the fixture's own work item through
// the real launcher, the way a daemon tick does, and answers with the refusal
// when there is one.
func launchLauncher(t *testing.T, f *launcherFixture, runID string) error {
	t.Helper()
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	_, err := f.launcher(t).Launch(context.Background(), spec, runID)
	return err
}

// launcherCascadeCheckout lays down the checkout of acme/app a daemon stands
// in when its repository asks for the cascade: one commit on the default
// branch and nothing else, since a daemon's launch names no ref. cascadeCLI is
// the file such a repository writes.
func launcherCascadeCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": cascadeCLI})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository asks for the cascade")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	t.Chdir(dir)
}

// TestRunLauncherPassesTheCommandsLaunchGates: the daemon's launch is `fugaro
// run`'s launch, and what must not exist is a second launch path that skips a
// gate the command passes — a run started here onto a runner that cannot read
// what the task carries is an approved attempt spent on a job that dies at
// bootstrap. The launcher runs the command's own block (freshLaunchGates), so
// this pins it a gate at a time: each gate's own refusal, and nothing of the
// launch written behind it — no task, no claim, no execution.
//
// The gates listed are the ones a daemon's own launch shape reaches. The rest
// of that block fires only on a shape the daemon does not have and stays with
// the command's own tests: the --attach caps and recipe gate (the daemon
// attaches nothing), the named-token and plan-task field gates (no work item
// carries either), the mode: artifact and mode: review gates (the daemon
// starts neither), the proxy gate (nothing here brokers Bitbucket), the
// checkout: clone and checkout: shared gates, and the per-field gates of the
// configuration the command reads from a checkout.
//
// Mutation (run, restored): drop the freshLaunchGates call from the launcher's
// prepare — every subtest below launches and fails.
func TestRunLauncherPassesTheCommandsLaunchGates(t *testing.T) {
	const first, second = "20261109-101500-aaaa", "20261109-101500-bbbb"
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, f *launcherFixture)
		// holding is a successful launch that must happen first, for the one
		// gate that judges what the installation already holds.
		holding bool
		want    []string
	}{
		{
			name:    "the cascade's own gate, first of all",
			prepare: func(t *testing.T, f *launcherFixture) { launcherCascadeCheckout(t) },
			want:    []string{"agent.auth: cascade cannot run here", "published no policy"},
		},
		{
			name: "the job title's own field gate",
			prepare: func(t *testing.T, f *launcherFixture) {
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, titlesSince, -1))
			},
			want: []string{"a job title (--title, kept by --pr follow-ups and --retry) needs a job image", "fugaro " + titlesSince},
		},
		{
			name: "the seal's gate",
			// The daemon's installation mints its budget tokens (the fixture's
			// mode is enforce), so a launch here seals whatever it starts and
			// is judged against an image whose runner can read the seal. The
			// image is the oldest that still carries the title above, so this
			// is the seal's refusal and not the title's.
			prepare: func(t *testing.T, f *launcherFixture) {
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+titlesSince)
			},
			want: []string{"sealed budget token", "fugaro " + sealSince},
		},
		{
			name: "the owner's review-level floor",
			// The floor lives in the owner-only policy node and the daemon
			// passes no --mode and no --review-levels, so it is exactly the
			// ask its launch has to carry on its own (D722, G1): a run whose
			// default mode is pro (Fugaro's own default) is refused on an
			// image older than the release that reads levels.
			prepare: func(t *testing.T, f *launcherFixture) {
				publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/*"],"review_floor":{"pro":["l2"]}}`)
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, reviewLevelsSince, -1))
			},
			want: []string{"the owner's review-level floor", "fugaro " + reviewLevelsSince},
		},
		{
			name:    "the parallel cap",
			prepare: func(t *testing.T, f *launcherFixture) { setMaxParallel(t, 1) },
			holding: true,
			want:    []string{"runs active", "max_parallel is 1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLauncherFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}
			ran := 0
			if tc.holding {
				if err := launchLauncher(t, f, first); err != nil {
					t.Fatalf("the launch that fills the cap: %v", err)
				}
				ran = 1
			}
			err := launchLauncher(t, f, second)
			if err == nil {
				t.Fatalf("a launch %s went through", tc.name)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			ctx := context.Background()
			s := f.runStore(t, second)
			if _, err := s.ReadTask(ctx); err == nil {
				t.Error("a refused launch stored a task, so a run the gate refuses is on record")
			}
			if _, err := s.ReadClaim(ctx); !errors.Is(err, runstore.ErrNotFound) {
				t.Errorf("a refused launch took the launch claim: %v", err)
			}
			if execs := f.run.Executions(); len(execs) != ran {
				t.Errorf("executions = %v, want the %d the gate left running", execs, ran)
			}
		})
	}
}

// TestRunLauncherGatesAReIssueOnTheStoredProjectLayer: a daemon that dies
// between storing the task and starting the run leaves a task the next tick
// finishes, and the runner reads that stored task — its project layer included,
// which createTask keeps however the layer was re-resolved since (repeatTaskBytes
// excludes the layer from "is this the same task"). So the gates of the
// re-issue must judge the layer the stored task carries and not a project layer
// republished in between, the way `fugaro run` does for the same case. Judging
// the republished one is a document the runner never reads deciding the launch:
// here it is a spurious refusal — the published layer switches a review level
// off with `model: none`, which the stored task's layer does not say — and the
// attempt would sit there failing a cycle every tick, with the image the job
// actually runs never in question.
//
// Mutation (run, restored): drop the adoptStoredProjectLayer call from the
// launcher's prepare — the re-issue below is refused by the none-model gate and
// fails.
func TestRunLauncherGatesAReIssueOnTheStoredProjectLayer(t *testing.T) {
	f := newLauncherFixture(t)
	// Below noneModelSince, so the gate on a level whose model is none has
	// something to refuse; the layer's own level is the whole of the ask here.
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, noneModelSince, -1))
	publishedLayer(t, f.cloudFixture, testProjectLayer)
	ctx := context.Background()
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	// The crash: the task is stored and the start never happened.
	if _, _, err := l.prepare(ctx, spec, runID); err != nil {
		t.Fatalf("storing the task of the attempt that crashed: %v", err)
	}
	stored, err := f.runStore(t, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("the task the crash left: %v", err)
	}
	if stored.ProjectLayer == nil {
		t.Fatal("the stored task carries no project layer, so this test proves nothing about the layer the runner reads")
	}
	if strings.Contains(stored.ProjectLayer.YAML, "model: none") {
		t.Fatalf("the stored layer is not the one this test is about:\n%s", stored.ProjectLayer.YAML)
	}

	// The project layer is republished before the next tick.
	withNone := strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n",
		"  agent: { auth: api-key, levels: { l2: { model: none } } }\n", 1)
	if withNone == testProjectLayer {
		t.Fatal("the project layer fixture no longer carries the agent line this test rewrites")
	}
	// writeBucketFile rather than publishedLayer: the bucket name is already
	// the default one, and publishing again is a second write of the layer key.
	writeBucketFile(t, f.cloudFixture, config.LayerKey, withNone)

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("the re-issue of an attempt whose stored task carries the layer the image reads: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the re-issue started %d execution(s), want the one it finishes: %v", len(execs), execs)
	}
	after, err := f.runStore(t, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("the task after the re-issue: %v", err)
	}
	if !reflect.DeepEqual(after.ProjectLayer, stored.ProjectLayer) {
		t.Errorf("the task on record now carries a different layer than the one the re-issue was gated on:\nafter  %+v\nstored %+v", after.ProjectLayer, stored.ProjectLayer)
	}
}
