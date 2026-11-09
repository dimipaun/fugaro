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
