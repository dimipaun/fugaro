package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
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
	// every daemon test into a refusal — and the policy's own floor is one of
	// those gates (policyImageGate, whose floor is the release that introduced
	// the node's version): a base below it is refused before this fixture's
	// launches reach the gate they are about.
	base := shiftRelease(t, securitySince, 1)
	if newer(nodeRelease, base) {
		base = nodeRelease
	}
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+base)
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
	publishPolicy(t, f.launchBudgetFixture, `{"version":5,"allow_repos":["github/acme/other"]}`)
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

// TestRunLauncherResolvesThePlanWithoutACheckout: a daemon's working directory
// is whatever its unit was started from, so the plan it resolves must not read
// a checkout at all. The case that matters is a person's own checkout of the
// item's repository: the branch they left checked out there is a branch origin
// has, and newRunRef takes such a branch as the run's ref — a ref the approval's
// digest does not cover, and one that moves under the daemon when the person
// switches branches (between a crash and the re-issue it would rebuild a spec
// whose task.json differs from the one already stored, and createTask refuses
// "already holds a different task" every tick). The file in that checkout is
// read for the base branch too. Launched from this directory, the run is cut
// from the base branch the layers resolve and from nothing else, exactly as
// `fugaro run` from outside a checkout is.
//
// Mutation (run, restored): drop the withoutCheckout(ctx) line at the top of
// prepare — the task below is then cut from ref feature-x, the branch the
// directory has checked out, and the launch warns that it named it.
func TestRunLauncherResolvesThePlanWithoutACheckout(t *testing.T) {
	f := newLauncherFixture(t)
	// The person's checkout of the item's repository: a different base branch
	// in its file, and feature-x checked out with a remote-tracking ref, which
	// is what makes it a ref newRunRef would take.
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\n"})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the checkout's own file")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/develop", "HEAD")
	testutil.Git(t, dir, "checkout", "-q", "-b", "feature-x")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/feature-x", "HEAD")
	t.Chdir(dir)

	l := f.launcher(t)
	var warn strings.Builder
	l.stderr = &warn
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"
	if _, err := l.Launch(context.Background(), spec, runID); err != nil {
		t.Fatalf("launching from a checkout of the item's repository: %v", err)
	}
	ts, err := f.runStore(t, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if ts.Ref != "main" {
		t.Errorf("the run is cut from %q, and a launch with no checkout is cut from the base branch the daemon's own layers resolve (main)", ts.Ref)
	}
	if ts.Ref == "feature-x" {
		t.Errorf("the run is cut from the branch the working directory has checked out (%q), which the item's approval does not cover", ts.Ref)
	}
	// The spec writes the default base branch as the empty field, so the
	// checkout file's develop — the base a launch that read the directory
	// would resolve — cannot appear here either.
	if ts.BaseBranch != "" {
		t.Errorf("the run's base branch is %q, and the daemon's own layers leave it at the default (the empty field); the checkout's file, which names develop, is not the daemon's to read", ts.BaseBranch)
	}
	if strings.Contains(warn.String(), "--ref") {
		t.Errorf("the launch named the working directory's branch: %q", warn.String())
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the launch started %d execution(s): %v", len(execs), execs)
	}
}

// launcherProfilesFixture is the daemon's launcher on an installation that has
// published profiles: the standard profile on github, in the local config and
// in the published policy, and the shared project job the installation would
// have deployed for it — which is the job a `checkout: clone` workflow runs on
// (project_job.go). It is launcherFixture otherwise, so the gates every launch
// passes before the job is resolved are the ones that fixture satisfies.
type launcherProfilesFixture struct {
	*launcherFixture
}

func newLauncherProfilesFixture(t *testing.T) *launcherProfilesFixture {
	t.Helper()
	f := newLauncherFixture(t)
	f.appendConfig(t, "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h } }\n")
	publishPolicy(t, f.launchBudgetFixture, projectJobPolicy)
	job := gcp.ProjectJobName("standard", "github", "")
	f.run.AddProjectJob(job, "standard", "github", "", "2", "4Gi")
	f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	return &launcherProfilesFixture{launcherFixture: f}
}

// TestRunLauncherTakesNoJobFromItsWorkingDirectory is the launch's own half of
// the rule TestRunLauncherResolvesThePlanWithoutACheckout pins for the plan: no
// read of the daemon's directory decides anything about a launch, which
// includes the two reads that pick what the run runs on and how.
//
// A daemon started in a person's checkout of the item's repository finds a
// `checkout: clone` workflow there, and an installation that has published
// profiles deploys a shared project job for exactly those workflows — so a
// launch that took that directory's word would run the item on the shared job
// of the workflow's profile while its own gates had judged a repository with no
// checkout: the clone-side gates (the base image a clone workflow runs on, and
// the seal and proxy pair judged with the workflow's own kind) are decided
// against a kind the launch then ignored, and the run starts on a job and a
// credential shape its gates never approved. The item is refused instead, with
// the sentence `fugaro run` from outside a checkout gives, in either directory —
// with a checkout of the item's repository and with none — because without a
// checkout the launch cannot tell a clone workflow's shared job from a baked
// workflow's own. What a refusal must not leave behind is a claim, a budget
// token, a Cloud Run call or an execution. The task is stored: the refusal is
// the start's own, past the writes `fugaro run` also makes first.
//
// Mutation (run, restored): mark only prepare with withoutCheckout, as the
// launcher did before, and hand launchRun the caller's context — the first
// subtest launches, and one jobs.run call names the shared project job of the
// profile with one execution behind it: the launch these gates exist to refuse.
func TestRunLauncherTakesNoJobFromItsWorkingDirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where func(t *testing.T)
	}{
		{"a checkout of the item's repository", func(t *testing.T) { projectJobCheckout(t) }},
		{"no checkout at all", func(t *testing.T) { t.Chdir(t.TempDir()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLauncherProfilesFixture(t)
			tc.where(t)
			l := f.launcher(t)
			ctx := context.Background()
			spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
			const runID = "20261109-101500-abcd"

			_, err := l.Launch(ctx, spec, runID)
			if err == nil {
				t.Fatal("a daemon launched from outside the item's repository took its job from the directory it runs in")
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			for _, want := range []string{"cannot tell which Cloud Run job it targets", "acme/app", "clone workflow"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			s := f.runStore(t, runID)
			if _, err := s.ReadTask(ctx); err != nil {
				t.Errorf("the refusal came before the task was stored: %v", err)
			}
			if _, err := s.ReadClaim(ctx); !errors.Is(err, runstore.ErrNotFound) {
				t.Errorf("the refused launch took the launch claim: %v", err)
			}
			if tok := readToken(t, f.cloudFixture, runID); tok != "" {
				t.Errorf("the refused launch minted a budget token: %s", tok)
			}
			if calls := f.run.RunRequests(); len(calls) != 0 {
				t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
			}
			if execs := f.run.Executions(); len(execs) != 0 {
				t.Errorf("the refused launch started %d execution(s): %v", len(execs), execs)
			}
		})
	}
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
// checkout: clone and checkout: shared gates, and every gate that judges a
// document only a checkout of the repository holds — the repository's own
// fugaro.yaml above all, which is where the cascade's gate and the per-field
// config gates read from (the daemon resolves without a checkout,
// TestRunLauncherResolvesThePlanWithoutACheckout, and a run's own bootstrap
// refuses what a launch could not read).
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
				publishPolicy(t, f.launchBudgetFixture, `{"version":5,"allow_repos":["github/acme/*"],"review_floor":{"pro":["l2"]}}`)
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

// TestRunLauncherReIssuesARunItsOwnExecutionWouldRefuse: a crash between
// jobs.run and launch.json — the execution started, the process died before the
// record of it was written — leaves a run in flight whose launch the next tick
// re-issues under the intent that recorded the run id. The gates must not judge
// that re-issue as a first launch, and the parallel cap is the one that does:
// the run's own execution is the run the cap counts, so under max_parallel 1 the
// run is refused by itself ("1 runs active; max_parallel is 1") every tick for
// the rest of its life, and the breaker trips on a run that is going fine. What
// is left of a launch that already went is the report that it went.
//
// Mutation (run, restored): call freshLaunchGates outside the prior == nil check
// in the launcher's prepare — the re-issue below is refused by the run's own
// execution and the attempt never finishes its launch.
func TestRunLauncherReIssuesARunItsOwnExecutionWouldRefuse(t *testing.T) {
	f := newLauncherFixture(t)
	setMaxParallel(t, 1)
	ctx := context.Background()
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("the launch that fills the cap: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the first launch started %d execution(s), want one: %v", len(execs), execs)
	}
	s := f.runStore(t, runID)
	launched, err := s.ReadLaunch(ctx)
	if err != nil {
		t.Fatalf("the launch the first call recorded: %v", err)
	}
	// The crash after jobs.run and before launch.json: the execution is going
	// and the only record of the run is the one the runner writes as it begins.
	// launch.json is removed under the run's own object key, the way the state
	// the crash left has it — absent — and the record is what puts it back.
	if err := s.DeleteFile(ctx, "launch.json"); err != nil {
		t.Fatalf("clearing the record of the launch that crashed: %v", err)
	}
	if err := s.WriteRecord(ctx, &runstore.Record{
		Version: 1, RunID: runID, Repo: "acme/app", Workflow: "web", Execution: launched.Execution,
		Status: runstore.StatusRunning, Stage: "bootstrap", Outcome: runstore.OutcomeNone,
	}); err != nil {
		t.Fatalf("writing the record the runner left as it began: %v", err)
	}

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("the re-issue of a run whose own execution is in flight: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the re-issue started %d execution(s), want the one it reports: %v", len(execs), execs)
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
