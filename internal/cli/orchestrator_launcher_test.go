package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
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
	publishPolicy(t, f.launchBudgetFixture, `{"version":9,"allow_repos":["github/acme/other"]}`)
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
// workflow's own, and the daemon's launch is the one that carries no checkout
// (A2's withoutCheckout, so the read A3 adds is never a read of the directory
// either: both subtests reach the same refusal, and it is the installation with
// no credentials service, which is what this fixture is). The refusal is the
// gates' own, because the workflow's kind is what those gates are judged by:
// the read A3 adds is the gates' read as much as the job decision's, so a
// launch whose read cannot be made refuses where `fugaro run` refuses the same
// case, before anything of the launch is written. What a refusal must not leave
// behind is a task, a claim, a budget token, a Cloud Run call or an execution —
// and it leaves none of them.
//
// Mutation (run, restored): drop the withoutCheckout switch from Launch, so
// both the gates and the start read the daemon's directory — the first subtest
// never reaches the sentence above: its gates read the directory's own
// `checkout: clone` workflow and refuse it for a base image this installation
// does not have, which is the kind of judgement the switch exists to keep off
// the directory.
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
			for _, want := range []string{"acme/app", "checkout", "credentials service"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			s := f.runStore(t, runID)
			if _, err := s.ReadTask(ctx); !errors.Is(err, runstore.ErrNotFound) {
				t.Errorf("the refused launch stored a task: %v", err)
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
// attaches nothing), the named-token field gate (no work item carries a
// token), the mode: artifact and mode: review gates (the daemon starts
// neither), the proxy gate (nothing here brokers Bitbucket), the checkout:
// clone and checkout: shared gates, and every gate that judges a document only
// a checkout of the repository holds — the repository's own fugaro.yaml above
// all, which is where the cascade's gate and the per-field config gates read
// from (the daemon resolves without a checkout,
// TestRunLauncherResolvesThePlanWithoutACheckout, and a run's own bootstrap
// refuses what a launch could not read).
//
// The plan-task gate beside the named-token one is the one a daemon's launch
// does reach: the item carries the plan task its run stands for, so the field
// goes through the command's own resolution and its own refusal (B7,
// TestLauncherPassesPlanTask). The item's external id is the other field no
// flag carries, and it is dropped rather than refused against an older runner
// (TestExternalDroppedForOlderRunner), which is why no row below covers it.
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
				publishPolicy(t, f.launchBudgetFixture, `{"version":9,"allow_repos":["github/acme/*"],"review_floor":{"pro":["l2"]}}`)
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

	// The crash: the task is stored and the start never happened. The brief is
	// the item's own, which is what this spec carries and what the launcher
	// builds for an item with no issue behind it (briefFor).
	if _, _, err := l.prepare(ctx, spec, spec.Brief, runID); err != nil {
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

// TestLauncherPassesPlanTask: the item's plan task is what joins a daemon's run
// to its node. `fugaro run --plan-task` records it in two places — the stored
// task, which the runner reads and `--retry` replays, and launch.json, which
// `plan status` and the console's graph read — and the daemon's launch resolves
// through that same command path (resolveAttemptPlan is where the flag and its
// checks live), so an item carrying one has to reach both. F38 is the gap this
// closes: a run a daemon started used to carry none, so `plan status` showed
// the node with no run on it while the run went, and the --plan-task duplicate
// refusal could not see such a run either, so a second run for a node a daemon
// was already driving passed it.
//
// Mutation (run, restored): drop planTask from the runOptions literal in
// prepare — both records lose the join and this fails.
func TestLauncherPassesPlanTask(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	l := f.launcher(t)
	const planTask = "orchestrator-graph-estimates/B7"
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", PlanTask: planTask, Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("launching an item that stands for a plan task: %v", err)
	}
	s := f.runStore(t, runID)
	ts, err := s.ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if ts.PlanTask != planTask {
		t.Errorf("the stored task's plan task is %q, want the item's %q: the runner reads the stored task, and `fugaro run --retry` replays it", ts.PlanTask, planTask)
	}
	launch, err := s.ReadLaunch(ctx)
	if err != nil {
		t.Fatalf("reading the launch the daemon recorded: %v", err)
	}
	if launch.PlanTask != planTask {
		t.Errorf("launch.json carries plan task %q, want the item's %q: `plan status` joins a run to its node by it, and the --plan-task duplicate refusal reads it off the runs a repository already has (F38)", launch.PlanTask, planTask)
	}
}

// TestLauncherPassesExternal: the item's external id — the ticket it came from
// outside Fugaro with — reaches the task's own field. No flag carries it, so
// the daemon's launch is its only writer, and the field is what the pull
// request of a run for the item builds its `Fixes` line from (B5, D2727). The
// id is not re-checked here: the orchestrator admitted it when the item was
// declared (orchestrator.externalRE), so this is a copy of an id already
// checked, not a second rule about what a ticket looks like.
//
// Mutation (run, restored): drop the ts.External assignment from prepare — the
// stored task carries none, so the run has no ticket to name (B5).
func TestLauncherPassesExternal(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	l := f.launcher(t)
	const ticket = "github:acme/app#7"
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", External: ticket, Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("launching an item that came from a ticket: %v", err)
	}
	ts, err := f.runStore(t, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if ts.External != ticket {
		t.Errorf("the task's external id is %q, want the item's %q: the runner reads the stored task to name the issue its pull request closes", ts.External, ticket)
	}
}

// TestExternalDroppedForOlderRunner: the field arrived in externalSince's
// runner, and a task.json carrying a field an older runner does not know is a
// run that dies at bootstrap — the runner decodes the task strictly
// (task.Parse rejects an unknown field) with the approved attempt already
// spent. So the launch drops the field rather than refusing, out loud, judged
// on the image the job would really run: what the drop costs is one Fixes line,
// and what a refusal would cost is the attempt. The rest of the launch is
// untouched, which is what makes the drop a safe default rather than a
// silent half-launch.
//
// Mutation (run, restored): make imagePredates return false in the drop, or
// remove the externalSkew call from prepare — the stored task carries an id the
// image below externalSince cannot read, which is the bootstrap death this gate
// exists to prevent.
func TestExternalDroppedForOlderRunner(t *testing.T) {
	f := newLauncherFixture(t)
	// The installation's derived job image, which is the judge a launch with no
	// checkout has (releaseOf asks the installation's base image and
	// recordBaseRelease the job's own build record).
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, externalSince, -1))
	ctx := context.Background()
	l := f.launcher(t)
	var warn strings.Builder
	l.stderr = &warn
	const ticket = "github:acme/app#7"
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", External: ticket, Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("a launch whose job image predates the field was refused rather than started without it (the approved attempt is what a refusal spends): %v", err)
	}
	ts, err := f.runStore(t, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if ts.External != "" {
		t.Errorf("the stored task carries external %q for a job image older than %s: the runner refuses that task at bootstrap and the attempt is spent", ts.External, externalSince)
	}
	// One line about it, and it is the drop's: a launch that silently leaves out
	// a field the item carried is one nobody can explain later. The launch's
	// other notes (the fixture's own bucket note) are not this gate's.
	var dropped []string
	for _, line := range strings.Split(strings.TrimSuffix(warn.String(), "\n"), "\n") {
		if strings.Contains(line, "external") {
			dropped = append(dropped, line)
		}
	}
	if len(dropped) != 1 {
		t.Fatalf("the drop printed %d line(s) about the field, want the one: %q", len(dropped), warn.String())
	}
	for _, want := range []string{ticket, externalSince} {
		if !strings.Contains(dropped[0], want) {
			t.Errorf("the line the drop printed does not name %q: %q", want, dropped[0])
		}
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Errorf("the launch started %d execution(s), want the one the approved attempt is: %v", len(execs), execs)
	}
}

// TestTaskWithoutExternalDecodes: the field is additive, and the strictness of
// the task's own decoder is what makes that a claim to test rather than to
// assume — task.Parse rejects an unknown field, so a task carrying this one
// handed to the runner of an older image is that run's death at bootstrap. A task.json written
// for an item that came from no ticket carries no external key at all (an empty
// field writes none, so the spec's bytes are the ones the release before the
// field wrote) and decodes into an empty External; one carrying an id decodes
// into it.
func TestTaskWithoutExternalDecodes(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "Start the run this attempt recorded."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err != nil {
		t.Fatalf("launching an item that came from no ticket: %v", err)
	}
	data, err := f.runStore(t, runID).ReadFile(ctx, "task.json")
	if err != nil {
		t.Fatalf("reading the task the launch stored: %v", err)
	}
	if bytes.Contains(data, []byte(`"external"`)) {
		t.Errorf("the task of an item with no ticket carries an external key, so its bytes differ from the ones the release before the field writes:\n%s", data)
	}
	ts, err := task.Parse(data)
	if err != nil {
		t.Fatalf("the task the launch stored does not decode: %v", err)
	}
	if ts.External != "" {
		t.Errorf("a task with no external key decoded into %q, want the empty field", ts.External)
	}
	// The same task with the id the daemon's launch writes, through the same
	// decoder: what the runner of this release reads back is the id.
	const ticket = "github:acme/app#7"
	ts.External = ticket
	encoded, err := ts.Marshal()
	if err != nil {
		t.Fatalf("writing the task back with the external id: %v", err)
	}
	back, err := task.Parse(encoded)
	if err != nil {
		t.Fatalf("a task carrying the external id does not decode: %v", err)
	}
	if back.External != ticket {
		t.Errorf("the task decoded into external %q, want %q", back.External, ticket)
	}
}

// The resident launcher (mobile plan task P5.1d): the same launch, started from
// a container that has no checkout and no local config, over the task a
// launcher before it stored.

// resident is the real resident launcher over the fixture's own environment.
// The opener is the service's own shape (orchestrator_serve.go's `open`: a
// closure the launcher calls per launch), and it OPENS ONE PER CALL, as the
// service does — the launcher closes the environment it opened on every way
// out of a launch, so an opener that handed every launch one shared env would
// have the second launch of a test read a closed bucket: the fixture has to be
// the production shape for the close to be exercised at all.
//
// The launcher's own reason for one env per launch is the identity it writes
// onto it, so the two halves of the fixture's shape are the service's two.
func (f *launcherFixture) resident(t *testing.T) *residentLauncher {
	t.Helper()
	return &residentLauncher{open: func(ctx context.Context) (*cloudEnv, error) {
		lc, _, err := openBudget(ctx, cloudOptions{})
		if err != nil {
			return nil, err
		}
		return openCloudFor(ctx, lc)
	}}
}

// seedResidentRun stores the task of a run no launcher of this fixture ever
// started: the state a person's patch leaves on an orchestrator node, and the
// whole of what a resident pass is handed — the node records the run id, and
// the runs bucket holds the task that id names. The task is built from the item
// the caller will hand the launcher, because that is what an honest task is:
// one a launch stored for that item, which the launcher holds the two to
// (residentRunIsTheItems).
func seedResidentRun(t *testing.T, f *launcherFixture, runID string, spec orchestrator.Spec, requestedBy string) {
	t.Helper()
	seedTaskRun(t, f.cloudFixture, runID, residentTaskOf(spec, requestedBy))
}

// residentTaskOf is the task a launch stores for one item, in the pairs
// buildAttemptSpec makes: the item's brief, title, plan task and rigor tier go
// into the task straight through, the external ticket is set on the way out,
// and the recipe is carried by name — the item's declaration has no recipe text
// to write, and the name is what the launcher's own comparison reads. A test
// that wants a task the item does NOT approve moves one of these afterwards.
func residentTaskOf(spec orchestrator.Spec, requestedBy string) *task.Spec {
	ts := &task.Spec{
		RequestedBy: requestedBy,
		Title:       spec.Title,
		PlanTask:    spec.PlanTask,
		Task:        spec.Brief,
		Tier:        tier.Tier(spec.RigorTier),
		External:    spec.External,
	}
	// Nothing for an item that named no recipe, which is one of the shapes a
	// launch of the catalog default writes (taskRecipeEmbedding: no newer
	// runner than a default run needs); the other is the default by name, which
	// the accept test pins. A name the catalog knows is EMBEDDED, text and sum,
	// which is what a real launch of one writes; a name it does not is a
	// repository recipe, carrying the name alone, and its text is read at the
	// run's own ref rather than stored.
	if name := spec.Recipe; name != "" && name != recipe.DefaultName {
		if text, ok := recipe.CatalogText(name); ok {
			ts.Recipe = &task.Recipe{Name: name, Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)}
		} else {
			ts.Recipe = &task.Recipe{Name: name, Source: string(recipe.SourceRepo)}
		}
	}
	return ts
}

// tokenClaim reads one developer claim out of the custom token a launch sealed
// and left in the fixture's bucket: the payload's "claims" object, which is
// what internal/budget/token wrote (token.Claims.claimSet) and what Firebase's
// own exchange reads back. The token is not a secret in a test — it is the
// fixture's own fake signer's signature — and what is read here is one claim,
// never the token itself.
func tokenClaim(t *testing.T, tok, claim string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("the run's token has %d segments, want a JWT's three", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("the token's payload does not decode: %v", err)
	}
	var body struct {
		Claims map[string]any `json:"claims"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("the token's payload is not JSON: %v", err)
	}
	v, _ := body.Claims[claim].(string)
	return v
}

// TestResidentLauncherStartsTheRecordedAttempt is the plan task's own sentence,
// run the way the resident service runs it: read the launch request, take the
// run's claim, mint the budget token and start the JOB execution — for the run
// id the node recorded, over the task a launcher before it stored, from a
// process that has no checkout.
//
// What the identity is minted under is the whole of what this launcher decides
// that the command's path does not: `Me` would reach for git config in a
// container that has neither a person nor a checkout, so the name is the task's
// own requested_by — the same name the launch record's launched_by carries
// beside it, and the token's rb claim. The last two assertions are the fence
// around that: a task that names nobody is refused below, and a run whose
// stored task is another repository's is refused as well.
func TestResidentLauncherStartsTheRecordedAttempt(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	// The item carries every field a task stores, so the honest re-issue below
	// is the honest case for the whole declaration and not for an item that
	// happens to be mostly empty.
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app",
		Recipe: "standard", RigorTier: "medium", PlanTask: "p5/P5.1d", External: "github:acme/app#7",
		Brief: "Start the run the node recorded."}
	seedResidentRun(t, f, runID, spec, "dana@example.com")
	l := f.resident(t)

	got, err := l.Launch(ctx, spec, runID)
	if err != nil {
		t.Fatalf("the resident launcher refused the run the node records: %v", err)
	}
	if got != runID {
		t.Fatalf("the resident launcher started %q for the run the node recorded as %q", got, runID)
	}
	execs := f.run.Executions()
	if len(execs) != 1 {
		t.Fatalf("the resident launch started %d executions, want one: %v", len(execs), execs)
	}
	// The claim: this launch went through `fugaro run`'s own, so the run has
	// exactly one execution and the record of it is at the run's own node.
	rec, err := f.runStore(t, runID).ReadLaunch(ctx)
	if err != nil {
		t.Fatalf("the launch was not recorded at the run's own node: %v", err)
	}
	if rec.LaunchedBy != "dana@example.com" {
		t.Errorf("the launch record says launched_by %q, and a resident launch is the person the run's own task names (dana@example.com)", rec.LaunchedBy)
	}
	// The mint: sealed in the bucket, handed to the execution by the marker,
	// and attributed to the task's requested_by rather than to a service
	// account or to whatever git config a container might have.
	if tok := tokenClaim(t, launchedToken(t, f.cloudFixture, runID), "rb"); tok != "dana@example.com" {
		t.Errorf("the run's identity is attributed to %q, and the task the node recorded names dana@example.com", tok)
	}
	// A second call for the same run id — the next tick re-issuing the same
	// attempt, which is what a resident pass does with a run it cannot see
	// started — is answered with the run that exists and starts nothing: the
	// fence around every re-issue.
	again, err := l.Launch(ctx, spec, runID)
	if err != nil {
		t.Fatalf("re-issuing a run whose launch is already recorded: %v", err)
	}
	if again != runID {
		t.Errorf("the second call reported %q, want the run that exists (%q)", again, runID)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("the run was started %d times, want once: the resident pass re-issues on every tick", n)
	}
}

// TestResidentLauncherRefusesARunThatNamesNobody: a stored task whose
// requested_by is empty is one this launcher will not start, because the name
// it mints the run's identity under would have to be invented. The refusal is a
// user error and it leaves NOTHING of the launch behind — no claim, no token,
// no execution, no record — which is what the assertions below hold it to: the
// identity is decided before launchRun is called.
func TestResidentLauncherRefusesARunThatNamesNobody(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	// The task is the item's own and only requested_by is empty: what is
	// refused below is the identity, not the declaration.
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app", Recipe: "standard", Brief: "Start the run the node recorded."}
	seedResidentRun(t, f, runID, spec, "")
	l := f.resident(t)

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a resident launch minted the identity of a run whose task names nobody")
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: nothing about this launch is a service's fault, and the fix is the operator's (patch the item, or launch from a machine of your own)", err)
	}
	for _, want := range []string{"requested_by", runID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("the run was started %d time(s) for a task that names nobody", n)
	}
	if raw := readToken(t, f.cloudFixture, runID); raw != "" {
		t.Error("a budget token was minted for a run that names nobody")
	}
	if _, err := f.runStore(t, runID).ReadLaunch(ctx); err == nil {
		t.Error("a launch was recorded for a run that names nobody")
	}
}

// TestResidentLauncherRefusesARunThatIsNotTheItems: the node records the
// attempt's run id, and this service locates that run in the bucket by the id
// alone (retrySpec, a bare run id) — so a node whose item names one repository
// and whose run id belongs to another would otherwise start the other
// repository's run. What the resident launch starts is the attempt the ITEM and
// the run's own task agree on, and nothing is started when they do not.
func TestResidentLauncherRefusesARunThatIsNotTheItems(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	// The item's repository is another one, and its run id is this run's. The
	// task is the item's own in every other field, so the repository is the
	// only difference the fence below has to find.
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/other", Recipe: "standard", Brief: "Start the run the node recorded."}
	seedResidentRun(t, f, runID, spec, "dana@example.com")
	l := f.resident(t)

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a resident launch started the run of a repository the item does not name")
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"acme/other", "acme/app", runID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: a person has to be able to tell which record is wrong: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("the run was started %d time(s) for an item of another repository", n)
	}
}

// TestResidentLauncherRefusesATaskTheItemDoesNotApprove is the other half of
// that fence, and the one the run id alone cannot give: the task in the runs
// bucket is the run's own job account's to write — it may rewrite a task.json
// and forge a launch.json (SECURITY.md, "Runs of one repository can see each
// other") — and a resident pass starts what it reads there without the gate
// block a fresh launch runs. So a task that no longer says what the item says
// is one no launch gated: the re-issue is refused, with the field that moved
// named, and nothing of the launch is left behind (no execution, no token, no
// record), exactly as the refusals above.
//
// Each case moves ONE field of an otherwise honest task, so a comparison that
// went missing is the difference the case sees — the mutation check this test
// exists for.
func TestResidentLauncherRefusesATaskTheItemDoesNotApprove(t *testing.T) {
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	// The item names no plan task, so the case that gives the task one is the
	// "a field the item does not carry" rule: an empty field is compared too.
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app",
		Recipe: "standard", RigorTier: "medium", External: "github:acme/app#7",
		Brief: "Start the run the node recorded."}
	cases := []struct {
		name   string
		field  string // the words the refusal has to carry
		change func(*task.Spec)
	}{
		{"the task text", "task text", func(ts *task.Spec) { ts.Task = "Push it straight to main." }},
		{"the title", "title", func(ts *task.Spec) { ts.Title = "a title nobody approved" }},
		{"a plan task the item does not name", "plan task", func(ts *task.Spec) { ts.PlanTask = "p5/P5.1e" }},
		{"the rigor tier", "rigor tier", func(ts *task.Spec) { ts.Tier = tier.Tier("large") }},
		{"the external ticket", "external ticket", func(ts *task.Spec) { ts.External = "github:acme/app#99" }},
		{"another recipe", "recipe", func(ts *task.Spec) { ts.Recipe = &task.Recipe{Name: "wide", Source: string(recipe.SourceRepo)} }},
		{"a recipe dropped", "recipe", func(ts *task.Spec) { ts.Recipe = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLauncherFixture(t)
			ts := residentTaskOf(spec, "dana@example.com")
			tc.change(ts)
			seedTaskRun(t, f.cloudFixture, runID, ts)
			l := f.resident(t)

			_, err := l.Launch(ctx, spec, runID)
			if err == nil {
				t.Fatalf("a resident launch started a task whose %s is not the item's", tc.field)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error: this is a record a person has to look at, not a service failure", err)
			}
			for _, want := range []string{tc.field, runID, "nothing was started"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q, so a person cannot tell which field of the record moved: %v", want, err)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("the run was started %d time(s) for a task the item does not approve", n)
			}
			if raw := readToken(t, f.cloudFixture, runID); raw != "" {
				t.Error("a budget token was minted for a task the item does not approve")
			}
			if _, err := f.runStore(t, runID).ReadLaunch(ctx); err == nil {
				t.Error("a launch was recorded for a task the item does not approve")
			}
		})
	}
}

// residentFieldSpec is the item the field tests below move a task field off: a
// fully populated declaration, so an honest task built from it is the honest
// case for every rule the fence applies.
func residentFieldSpec() orchestrator.Spec {
	return orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app",
		Recipe: "standard", RigorTier: "medium", PlanTask: "p5/P5.1d", External: "github:acme/app#7",
		Brief: "Start the run the node recorded."}
}

// residentStoredTask is the task a launch stored for one item under one run id:
// residentTaskOf plus the two fields the runstore keys a stored task by, which
// the fence reads before it compares anything of the declaration. A test that
// calls the fence directly needs them set, or every rule below would refuse for
// the repository's reason instead of its own.
func residentStoredTask(spec orchestrator.Spec, requestedBy, runID string) *task.Spec {
	ts := residentTaskOf(spec, requestedBy)
	ts.RunID = runID
	ts.Repo = spec.Repo
	return ts
}

// TestResidentRunIsTheItemsRefusesAFieldNoLaunchWrites: the half of the
// fence the item cannot argue with. A field the item has no counterpart for is
// not compared — there is nothing to compare it with — and an item-driven
// launch, which is the launch whose attempt a resident pass re-issues, writes
// none of them: every field below is one a launch writes only when the command
// that made it was asked to. What each would buy a rewrite is why the rule is
// there rather than reported: an owner's ruling is text the runner appends to
// the brief and the review prompt, context repositories are cloned and read
// beside the work tree, attached inputs and reports are files the run reads, and
// the marks and the limits choose the job variant, the model and the clock.
//
// The fence is called directly rather than through the launcher, and that is
// deliberate: some of these fields a reader before the fence refuses first for
// its own reason (retrySpec refuses an A/B arm and a report run), and what this
// test pins is THIS fence's answer, field by field, so that the mutation check
// has one case per line of its table.
func TestResidentRunIsTheItemsRefusesAFieldNoLaunchWrites(t *testing.T) {
	const runID = "20261117-050000-abcd"
	const slug = "acme-app"
	spec := residentFieldSpec()
	maxUSD := 5.0
	cases := []struct {
		name   string
		field  string // the words the refusal has to carry
		change func(*task.Spec)
	}{
		{"an owner ruling", "an owner ruling", func(ts *task.Spec) { ts.OwnerRuled = "ship it without the review" }},
		{"context repositories", "context repositories", func(ts *task.Spec) { ts.ContextRepos = []string{"acme/lib"} }},
		{"the branch of a follow-up", "the pull request of a follow-up", func(ts *task.Spec) { ts.Branch = "feature" }},
		{"the pull request of a follow-up", "the pull request of a follow-up", func(ts *task.Spec) { ts.PR = 7 }},
		{"the run a follow-up continues", "the pull request of a follow-up", func(ts *task.Spec) { ts.PreviousRun = runID }},
		{"the pause a resumed run picks up", "a resumed run's pause", func(ts *task.Spec) {
			ts.Branch, ts.PR, ts.PreviousRun, ts.Resumes = "fugaro/"+runID, 7, runID, runID
		}},
		{"review pull requests", "review pull requests", func(ts *task.Spec) { ts.ReviewPRs = []int{7} }},
		{"a change set", "a change set", func(ts *task.Spec) {
			ts.ChangeSet = &task.ChangeSet{ID: runID, Members: []task.ChangeSetMember{{Repo: "acme/app", RunID: runID}}}
		}},
		{"attached inputs", "attached inputs", func(ts *task.Spec) {
			ts.Inputs = []task.InputFile{{Name: "spec.md", Bytes: 12, SHA256: strings.Repeat("ab", 32)}}
		}},
		{"stored reports", "stored reports", func(ts *task.Spec) {
			ts.Reports = []task.InputFile{{Name: "report.md", Bytes: 12, SHA256: strings.Repeat("ab", 32)}}
		}},
		{"an A/B arm", "an A/B arm", func(ts *task.Spec) { ts.AB = &task.AB{PairID: runID, Arm: task.ABArmChampion} }},
		{"a mutation list", "a mutation list", func(ts *task.Spec) {
			ts.Mutate = []task.Mutate{{File: "internal/cli/run.go", Test: "TestRun"}}
		}},
		{"declared paths", "declared paths", func(ts *task.Spec) { ts.Paths = []string{"internal/cli"} }},
		{"a requested rigor mode", "a requested rigor mode", func(ts *task.Spec) { ts.Rigor = "thorough" }},
		{"a batch label", "a batch label", func(ts *task.Spec) { ts.Batch = "batch-1" }},
		{"review levels", "review levels", func(ts *task.Spec) { ts.ReviewLevels = &config.ReviewLevels{} }},
		{"an expected scope", "an expected scope", func(ts *task.Spec) { ts.ExpectedScope = []string{"internal/cli"} }},
		{"a follow-up link", "a follow-up link", func(ts *task.Spec) {
			ts.FollowUpOf = &task.FollowUpOf{Run: runID, Keys: []string{"k1"}}
		}},
		{"a model grade", "a model grade", func(ts *task.Spec) { ts.ModelGrade = "high" }},
		{"the review class", "the review class", func(ts *task.Spec) { ts.ReviewClass = true }},
		{"a compression override", "a compression override", func(ts *task.Spec) { ts.CompressionOff = true }},
		{"the debt mark", "the debt mark", func(ts *task.Spec) { ts.Debt = true }},
		{"the security mark", "the security mark", func(ts *task.Spec) { ts.Security = true }},
		{"a review spec", "a review spec", func(ts *task.Spec) { ts.ReviewSpec = &task.ReviewSpec{} }},
		{"a findings follow-up", "a findings follow-up", func(ts *task.Spec) { ts.FromFindings = true }},
		{"a run's spend limit", "a run limit", func(ts *task.Spec) { ts.Overrides.MaxUSD = &maxUSD }},
		{"a run's wall-clock limit", "a run limit", func(ts *task.Spec) { ts.Overrides.MaxTime = "45m" }},
	}
	// The item's own task first: the table below moves one field off a task the
	// fence has to accept, or every case would pass for the wrong reason.
	honest := residentStoredTask(spec, "dana@example.com", runID)
	if err := residentRunIsTheItems(spec, slug, honest); err != nil {
		t.Fatalf("the fence refused the task the item's own launch writes: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := residentStoredTask(spec, "dana@example.com", runID)
			tc.change(ts)
			err := residentRunIsTheItems(spec, slug, ts)
			if err == nil {
				t.Fatalf("the fence started a task carrying %s, which no launch of the item writes", tc.field)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error: this is a record a person has to look at, not a service failure", err)
			}
			// The wording of the presence refusal, field and all: a case that
			// passed on some OTHER rule of the fence (the comparison half, the
			// repository) would be a line of the table nothing tests.
			for _, want := range []string{"holds a task carrying " + tc.field + ",", runID, "nothing was started"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q, so a person cannot tell which field of the record is the foreign one: %v", want, err)
				}
			}
		})
	}
}

// TestResidentRunIsTheItemsAcceptsTheFieldsTheLaunchItselfResolves is the other
// side of the table above, and the reason it is a refusal list and not a
// whitelist of fields: these are fields an item cannot declare either, and the
// launch RESOLVES them — the ref and the base branch the repository's
// configuration names, the workflow that decides the job, the project layer it
// embeds, the mode the resolved recipe carries, the account a named token
// mounts, the author a commit is recorded under, and the wall clock the
// workflow's timeouts name. An honest stored task carries them and no item does,
// so refusing them would refuse every honest re-issue; what a rewritten one
// could do with them is the pull request's open design question, not a rule
// here.
func TestResidentRunIsTheItemsAcceptsTheFieldsTheLaunchItselfResolves(t *testing.T) {
	const runID = "20261117-050000-abcd"
	spec := residentFieldSpec()
	ts := residentStoredTask(spec, "dana@example.com", runID)
	ts.Ref, ts.BaseBranch, ts.Workflow = "main", "main", "web"
	ts.Mode = recipe.ModeArtifact
	ts.OAuthToken = "acme-review"
	ts.Author = &task.Person{Name: "Dana", Email: "dana@example.com"}
	ts.ProjectLayer = &task.ProjectLayer{SHA256: strings.Repeat("ab", 32), YAML: "agent:\n  recipe: standard\n"}
	ts.Overrides.TotalTimeout = "45m"
	if err := residentRunIsTheItems(spec, "acme-app", ts); err != nil {
		t.Fatalf("the fence refused a task carrying the fields the launch itself resolves, which is what every honest re-issue carries: %v", err)
	}
}

// TestResidentTaskFieldsAreAllClassified keeps the fence's claim true as the
// task format grows. Every field of task.Spec is one of three things to a
// resident re-issue: COMPARED with the item's declaration (the six pairs above
// plus the repository), resolved by the launch ITSELF (so the item cannot hold
// the task to it and the fence leaves it alone), or a field no launch of an item
// writes, which the fence refuses by its presence. A field added to the format
// and classified as nothing would otherwise slip through the fence silently —
// the failure mode the review of this change found — and this test makes that
// impossible: it fails until the new field is named in one of the three lists,
// which is where the fence, its comment and this test are kept in step.
func TestResidentTaskFieldsAreAllClassified(t *testing.T) {
	compared := []string{"repo", "task", "title", "plan_task", "tier", "external", "recipe"}
	resolved := []string{"version", "run_id", "ref", "base_branch", "workflow", "requested_by",
		"mode", "oauth_token", "overrides", "project_layer", "author", "digest_runs"}
	refused := []string{"branch", "pr", "previous_run", "resumes", "owner_ruled", "context_repos", "review_prs",
		"change_set", "inputs", "reports", "ab", "mutate", "paths", "rigor", "batch", "review_levels",
		"expected_scope", "follow_up_of", "model_grade", "review_class", "compression_off", "debt", "security",
		"review_spec", "from_findings", "own_text"}
	// overrides is classified as the launch's own because the launch writes one
	// of its fields (total_timeout, from the workflow's timeouts) and the fence
	// refuses the rest by value rather than by presence; the case above pins the
	// refusal of a limit and the accept test pins the total timeout. digest_runs
	// is the launch's own for the same reason (buildAttemptSpec writes it from
	// the plan), and a task that set it would be read as a report run and
	// refused by retrySpec before the fence ever runs.
	classified := map[string]string{}
	for _, f := range compared {
		classified[f] = "compared with the item"
	}
	for _, f := range resolved {
		classified[f] = "resolved by the launch"
	}
	for _, f := range refused {
		classified[f] = "refused by its presence"
	}
	typ := reflect.TypeOf(task.Spec{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Errorf("task.Spec field %s has no json name, and the fence reads the format by its names", f.Name)
			continue
		}
		if _, ok := classified[name]; !ok {
			t.Errorf("task.Spec field %s (json %q) is not classified by the resident fence: name it in residentRunIsTheItems as compared, as the launch's own, or as a field no launch of an item writes — the fence's comment says which, and a field nobody classified is one a rewritten task could carry past it", f.Name, name)
		}
	}
	for name, how := range classified {
		found := false
		for i := 0; i < typ.NumField(); i++ {
			if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the fence classifies %q as %s, and task.Spec has no such field", name, how)
		}
	}
}

// TestResidentLauncherStartsNothingForAFieldTheItemCannotDeclare is the same
// rule end to end, through the launcher and the command's own retry reader,
// which is where the review's probe stood: a stored task with an owner's ruling
// or a context repository in it — text the runner appends to the brief and the
// review prompt, repositories cloned beside the work tree, neither of which the
// item's declaration can express — was started as-is under the approved run id,
// without the gate block that admits an allow_repos entry. It is refused now,
// and refusals leave nothing of a launch behind.
func TestResidentLauncherStartsNothingForAFieldTheItemCannotDeclare(t *testing.T) {
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	spec := residentFieldSpec()
	cases := []struct {
		name   string
		field  string
		change func(*task.Spec)
	}{
		{"an owner ruling", "an owner ruling", func(ts *task.Spec) {
			// task.Validate — which the launcher's read of the stored task
			// applies — holds a ruling to the follow-up shape, because a ruling
			// answers findings a run on a pull request left. So the task a
			// rewrite could store carries branch, pr and previous_run with it,
			// and the fence refuses those under their own line of the table too.
			ts.OwnerRuled = "skip the review this time"
			ts.Branch, ts.PR, ts.PreviousRun = "fugaro/"+runID, 7, runID
		}},
		{"context repositories", "context repositories", func(ts *task.Spec) { ts.ContextRepos = []string{"acme/lib"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLauncherFixture(t)
			ts := residentStoredTask(spec, "dana@example.com", runID)
			tc.change(ts)
			seedTaskRun(t, f.cloudFixture, runID, ts)
			l := f.resident(t)

			_, err := l.Launch(ctx, spec, runID)
			if err == nil {
				t.Fatalf("a resident launch started a task carrying %s, which no launch of the item writes", tc.field)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error: this is a record a person has to look at, not a service failure", err)
			}
			for _, want := range []string{"holds a task carrying " + tc.field + ",", runID, "nothing was started"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q, so a person cannot tell which field of the record is the foreign one: %v", want, err)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("the run was started %d time(s) for a task carrying %s", n, tc.field)
			}
			if raw := readToken(t, f.cloudFixture, runID); raw != "" {
				t.Error("a budget token was minted for a task carrying a field the item cannot declare")
			}
			if _, err := f.runStore(t, runID).ReadLaunch(ctx); err == nil {
				t.Error("a launch was recorded for a task carrying a field the item cannot declare")
			}
		})
	}
}

// TestResidentLauncherAcceptsATicketTheLaunchCouldNotRecord: the external id is
// the one field of the declaration a launch may legitimately leave OUT.
// externalSkew drops it rather than refusing the attempt when the job image
// predates the release that reads it (a stored task an older runner cannot
// decode is a paid-for attempt that never started, that gate's own reason), so
// a stored task carrying no ticket where the item names one is a task a launch
// really wrote, and the re-issue starts. A task naming a DIFFERENT ticket is a
// disagreement like any other field, which the per-field test above covers.
func TestResidentLauncherAcceptsATicketTheLaunchCouldNotRecord(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app",
		Recipe: "standard", External: "github:acme/app#7", Brief: "Start the run the node recorded."}
	ts := residentTaskOf(spec, "dana@example.com")
	ts.External = ""
	seedTaskRun(t, f.cloudFixture, runID, ts)
	l := f.resident(t)

	got, err := l.Launch(ctx, spec, runID)
	if err != nil {
		t.Fatalf("the resident launcher refused a task whose ticket the launch could not record: %v", err)
	}
	if got != runID {
		t.Errorf("the resident launcher started %q, want the run the node recorded (%q)", got, runID)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Errorf("the resident launch started %d executions, want one", n)
	}
}

// TestResidentLauncherAcceptsTheDefaultRecipeTheItemNamed is the honest side of
// the recipe comparison, which is the only field of the six whose two sides are
// not the same shape: an item NAMES a recipe and a task carries what the launch
// resolved it to, and a launch of the catalog default stores NO recipe at all
// (taskRecipeEmbedding, so a default run needs no newer runner) or the default
// by name, depending on whether the item asked for it. Both are the item's, and
// both re-issue.
func TestResidentLauncherAcceptsTheDefaultRecipeTheItemNamed(t *testing.T) {
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	cases := []struct {
		name   string
		recipe *task.Recipe
	}{
		{"no recipe stored", nil},
		{"the default by name", &task.Recipe{Name: recipe.DefaultName, Source: string(recipe.SourceRepo)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLauncherFixture(t)
			spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app",
				Brief: "Start the run the node recorded."}
			ts := residentTaskOf(spec, "dana@example.com")
			ts.Recipe = tc.recipe
			seedTaskRun(t, f.cloudFixture, runID, ts)
			l := f.resident(t)

			got, err := l.Launch(ctx, spec, runID)
			if err != nil {
				t.Fatalf("the resident launcher refused an item that named no recipe and a task that resolved to the default: %v", err)
			}
			if got != runID {
				t.Errorf("the resident launcher started %q, want the run the node recorded (%q)", got, runID)
			}
			if n := len(f.run.Executions()); n != 1 {
				t.Errorf("the resident launch started %d executions, want one", n)
			}
		})
	}
}

// TestResidentLauncherRefusesANamedAccountThisDeploymentCannotSee pins a
// LIMITATION of this deployment rather than a property to be proud of, so that
// it is a known refusal with a test behind it instead of a surprise in a log:
// the account a stored task names is judged against the operator's own
// `claude_accounts` (retrySpec asks claudeAccountFor with a recorded name), and
// a resident container is told six names and holds no project file. So a
// re-issue of a task that names a NAMED Claude account is refused, every tick,
// with the command's own sentence — and the run is NOT started on some other
// account to make the tick quiet: the credential a run mounts is the one its
// stored task names, so a launcher that substituted an account would start a
// run whose review stages are refused inside the run, for a token nothing
// mounted.
//
// The person's fix is the command's own, from a machine whose config knows the
// account (`fugaro run --retry acme/app/<run>`), which is the same fix the
// empty requested_by refusal names. Until then the node's attempt stays open and
// the tick writes one warning line per pass, which is a thing an operator can
// see. The plain account and a task naming no account are unaffected: they are
// answered without the key at all.
func TestResidentLauncherRefusesANamedAccountThisDeploymentCannotSee(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	const runID = "20261117-050000-abcd"
	spec := orchestrator.Spec{ItemID: "p5-1d", Title: "the launcher half", Repo: "acme/app", Recipe: "standard", Brief: "Start the run the node recorded."}
	ts := residentTaskOf(spec, "dana@example.com")
	ts.OAuthToken = "acme-overflow"
	seedTaskRun(t, f.cloudFixture, runID, ts)
	l := f.resident(t)

	_, err := l.Launch(ctx, spec, runID)
	if err == nil {
		t.Fatal("a resident launch started a task whose named account this deployment cannot judge: the account walk has to answer for the name the task carries")
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: the fix is the operator's (retry it from their own machine)", err)
	}
	for _, want := range []string{"acme-overflow", "claude_accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q, so a person cannot tell which account or which key to fix: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("the run was started %d time(s) on an account its stored task does not name", n)
	}
	if raw := readToken(t, f.cloudFixture, runID); raw != "" {
		t.Error("a budget token was minted for a run that was not started")
	}
}
