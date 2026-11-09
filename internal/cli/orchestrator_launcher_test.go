package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
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
	return &launcherFixture{launchBudgetFixture: newLaunchBudget(t, "enforce")}
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
	publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["acme/other"]}`)
	l := f.launcher(t)
	spec := orchestrator.Spec{ItemID: "wi-1", Title: "land the daemon", Repo: "acme/app", Recipe: "standard", Brief: "This is the launch the policy refuses."}
	const runID = "20261109-101500-abcd"

	if _, err := l.Launch(ctx, spec, runID); err == nil {
		t.Fatal("a launch the project's policy does not admit went through")
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Fatalf("a refused launch started %d execution(s): %v", len(execs), execs)
	}
	if _, err := f.runStore(t, runID).ReadTask(ctx); err == nil {
		t.Fatal("a refused launch stored a task, and the gate refuses before anything is written")
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
