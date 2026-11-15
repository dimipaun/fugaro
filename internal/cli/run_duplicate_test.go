package cli

// The duplicate guard's tests (run_duplicate.go): a launch of a plan task
// refuses while a run of it is going or a pull request of it is open, warns and
// launches when it cannot find out, and is escaped with --allow-duplicate. The
// reads it makes are this machine's fakes -- the fixture bucket and the fake
// code host -- so nothing here touches a network or an account.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// duplicatePlan is the plan task these tests launch, and duplicateOther a
// different one, so a test can say that the guard is about one task rather than
// about runs in general.
const (
	duplicatePlan  = "setup-wizard/W1.3"
	duplicateOther = "setup-wizard/W1.4"
)

var (
	// dupLive is in flight on duplicatePlan, dupLeft finished and left pull
	// request 7 open on it, dupOther is in flight on duplicateOther, and
	// dupNext is the run every launch below names.
	dupLive  = wallRunIDAt(0, "000100", "d0d0")
	dupLeft  = wallRunIDAt(1, "000200", "d1d1")
	dupOther = wallRunIDAt(0, "000300", "d2d2")
	dupNext  = wallRunIDAt(0, "000400", "d3d3")
)

// duplicateRig is a fixture that can launch a plan task: the job image whose
// runner reads one (planTaskSince), and room for the runs a test starts.
func duplicateRig(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0112)
	setMaxParallel(t, 8)
	return f
}

// dupRefused runs the launch args and wants the duplicate refusal: exit 1, the
// message in want, and nothing started for the run it named. It returns that
// message, so a test can read the whole of what the caller is told.
func dupRefused(t *testing.T, f *cloudFixture, want string, args ...string) string {
	t.Helper()
	_, _, err := execute(t, args...)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
		t.Fatalf("%v: err = %v, want exit 1 with %q", args, err, want)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%v: %d executions, want none", args, n)
	}
	return err.Error()
}

// dupLiveRun stores a run of planTask that is in flight: its task, and a
// launch.json carrying the id with a fresh launched_at and no execution yet,
// which is a run the backend has not been asked about (the row reads
// `pending`, and a pending run is one that is going). The row's plan task is
// the launch record's value and never the task's, so both are written and the
// launch record is the one the guard reads.
func dupLiveRun(t *testing.T, f *cloudFixture, id, planTask string) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	spec := firstRunSpec(id)
	spec.PlanTask = planTask
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, PlanTask: planTask, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// dupFinishedRun stores a run of planTask that ended, leaving pull request pr
// behind it.
func dupFinishedRun(t *testing.T, f *cloudFixture, id, planTask string, pr int) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	spec := firstRunSpec(id)
	spec.PlanTask = planTask
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, PlanTask: planTask, LaunchedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, f, id, prRecord(id, "", pr, 1))
}

// dupStub installs the guard's opener over one fake code host holding prs: the
// provider kind is resolved from this machine's config for real, and the read
// itself is the fake's, which is the network no test has.
func dupStub(t *testing.T, prs ...fake.PRState) *fake.Provider {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: fake.State{PRs: prs}}
	was := openDuplicateProvider
	openDuplicateProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", func(_ context.Context, _, repo string) (gitprov.Provider, []string, error) {
			if repo != "acme/app" {
				return nil, nil, errors.New("no credential on this machine for " + repo)
			}
			return p, nil, nil
		}, nil
	}
	t.Cleanup(func() { openDuplicateProvider = was })
	return p
}

// dupStubFails installs an opener that cannot open anything, which is the
// machine with no credential for its code host.
func dupStubFails(t *testing.T) {
	t.Helper()
	was := openDuplicateProvider
	openDuplicateProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", nil, errors.New("no credential on this machine")
	}
	t.Cleanup(func() { openDuplicateProvider = was })
}

// dupPR is one pull request of the fake host, on the branch a run of id is on.
func dupPR(number int, id string, state gitprov.PRState) fake.PRState {
	return fake.PRState{
		PR:    gitprov.PR{Number: number, URL: "https://forge.example/acme/app/pull/7"},
		Spec:  gitprov.PRSpec{Branch: "fugaro/" + id, Base: "main", Title: "x"},
		State: state,
	}
}

// TestRunRefusesADuplicatePlanTask is the guard's headline: a launch that would
// be a second run of a plan task stops before anything is started and names the
// run or the pull request that holds the task. The launches beside it -- a task
// nothing holds, another task, and no task at all -- are the launches that must
// still work.
func TestRunRefusesADuplicatePlanTask(t *testing.T) {
	t.Run("a run in flight", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		msg := dupRefused(t, f, task.ShortRun(dupLive), "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if !strings.Contains(msg, "is still pending") || !strings.Contains(msg, duplicatePlan) {
			t.Fatalf("refusal = %q, want the run's status and the plan task", msg)
		}
	})

	t.Run("a pull request left open", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7)
		dupStub(t, dupPR(7, dupLeft, gitprov.PROpen))
		msg := dupRefused(t, f, "pull request #7", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if !strings.Contains(msg, task.ShortRun(dupLeft)) {
			t.Fatalf("refusal = %q, want the run that left it", msg)
		}
	})

	t.Run("a task nothing holds", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7)
		// The pull request is merged: the task is done, and a launch that
		// redoes it is a real second attempt rather than a duplicate.
		dupStub(t, dupPR(7, dupLeft, gitprov.PRMerged))
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"); err != nil {
			t.Fatal(err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the launch's one", n)
		}
	})

	t.Run("another plan task's run", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupOther, duplicateOther)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a launch that names no plan task", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		// The guard is about --plan-task: a launch without it is untouched,
		// which is every launch written before the flag existed.
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "Add a feature"); err != nil {
			t.Fatal(err)
		}
	})
}

// TestRunDuplicateRefusalNamesTheWaysOut pins the refusal's three ways out: the
// pull request a --pr continues, the chain a --retry or a --continues joins,
// and --allow-duplicate. A guard that only said "refused" would leave the
// caller to guess, and the guess is what launched the duplicate in the first
// place.
func TestRunDuplicateRefusalNamesTheWaysOut(t *testing.T) {
	f := duplicateRig(t)
	dupFinishedRun(t, f, dupLeft, duplicatePlan, 7)
	dupStub(t, dupPR(7, dupLeft, gitprov.PROpen))
	msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
	for _, want := range []string{
		"plan task " + duplicatePlan,
		"nothing was launched",
		"fugaro run --pr 7",
		"fugaro run --retry " + task.ShortRun(dupLeft),
		"--continues " + task.ShortRun(dupLeft),
		"--allow-duplicate",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal = %q, want %q in it", msg, want)
		}
	}
}

// TestRunAllowsADeliberateDuplicate: --allow-duplicate launches the second run
// and prints one line saying what it duplicates. It records nothing -- the task
// it stores is the task the same launch without the flag would store, which is
// what keeps the flag out of every wire format -- and it works for a batch,
// which is the redundant-attempts shape the parallelism skill plans.
func TestRunAllowsADeliberateDuplicate(t *testing.T) {
	f := duplicateRig(t)
	dupFinishedRun(t, f, dupLeft, duplicatePlan, 7)
	dupStub(t, dupPR(7, dupLeft, gitprov.PROpen))
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "--allow-duplicate", "Add a feature")
	if err != nil {
		t.Fatalf("--allow-duplicate: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "note: --allow-duplicate:") || !strings.Contains(errOut, "pull request #7") {
		t.Fatalf("stderr = %q, want the note naming the pull request it duplicates", errOut)
	}
	if spec := readSpec(t, f, dupNext); spec.PlanTask != duplicatePlan {
		t.Fatalf("task plan_task = %q", spec.PlanTask)
	}
	if wire := taskJSON(t, f, dupNext); strings.Contains(wire, "duplicate") {
		t.Fatalf("the flag reached the task:\n%s", wire)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--plan-task", duplicatePlan, "--allow-duplicate", "Add a feature"); err != nil {
		t.Fatalf("--attempts --allow-duplicate: %v", err)
	}
}

// TestRunDuplicateCheckFailsOpen: a lookup that cannot be made warns once and
// launches. The guard is a fence around a task that is already being worked on,
// and a machine with no credential for its code host (or no network) is not
// evidence that the task is free -- but refusing on that would stop every
// launch from that machine, which is worse than the duplicate it prevents.
func TestRunDuplicateCheckFailsOpen(t *testing.T) {
	t.Run("the code host cannot be read", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7)
		dupStubFails(t)
		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if err != nil {
			t.Fatalf("the launch must go ahead: %v (%s)", err, errOut)
		}
		if !strings.Contains(errOut, "could not check whether plan task "+duplicatePlan+" is already being worked on") {
			t.Fatalf("stderr = %q, want the warning about the check that did not run", errOut)
		}
		if n := strings.Count(errOut, "warning:"); n != 1 {
			t.Fatalf("stderr = %q, want one warning line", errOut)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the launch's one", n)
		}
	})

	t.Run("the runs cannot be read", func(t *testing.T) {
		f := duplicateRig(t)
		// The guard's own read, not a launch's: a bucket that cannot be read
		// is the same class of failure as a code host that cannot be, and the
		// launch must take the same answer. A closed bucket is that read's
		// failure without a fixture that breaks the launch path with it.
		ctx := context.Background()
		b, err := blob.OpenBucket(ctx, f.bucket)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		var warn strings.Builder
		o := &runOptions{planTask: duplicatePlan}
		if err := planTaskDuplicate(ctx, envOn(t, f, blobx.Wrap(b)), o, "acme/app", appSlug, dupNext, &warn); err != nil {
			t.Fatalf("a read the guard cannot make must not refuse: %v", err)
		}
		if !strings.Contains(warn.String(), "could not check whether plan task "+duplicatePlan) {
			t.Fatalf("warning = %q, want the line about the check that did not run", warn.String())
		}
	})
}

// TestRunDuplicateLeavesTheContinuingShapesAlone: the guard is about starting a
// second run of a task, so the three shapes that continue something that exists
// are untouched -- they are its own ways out -- and --allow-duplicate, which
// answers it, is refused with the two that carry a plan task through a flag.
func TestRunDuplicateLeavesTheContinuingShapesAlone(t *testing.T) {
	t.Run("--pr", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", release0112)
		seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
		dupLiveRun(t, f, dupLive, duplicatePlan)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--plan-task", duplicatePlan, "also rename x"); err != nil {
			t.Fatalf("a follow-up continues a run that exists: %v", err)
		}
	})

	t.Run("--retry", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		// The stored task of a run that never started, whose launch is the way
		// out of the refusal rather than a launch that could meet it.
		spec := firstRunSpec(dupLeft)
		spec.PlanTask = duplicatePlan
		seedSpec(t, f, spec, false)
		if _, _, err := execute(t, "run", "--retry", dupLeft); err != nil {
			t.Fatalf("--retry is a way out of the refusal: %v", err)
		}
	})

	t.Run("--continues", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		dupLiveRun(t, f, dupLive, duplicatePlan)
		if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "--plan-task", duplicatePlan, "A task"); err != nil {
			t.Fatalf("--continues is a way out of the refusal: %v", err)
		}
	})

	t.Run("--allow-duplicate with a continuing launch", func(t *testing.T) {
		seedRoot(t, newCloudFixture(t), rootID, time.Now().Add(-72*time.Hour))
		for _, args := range [][]string{
			{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--allow-duplicate", "x"},
			{"run", "--repo", "acme/app", "--continues", rootID, "--run-id", fuID, "--allow-duplicate", "x"},
		} {
			_, _, err := execute(t, args...)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "it takes no --allow-duplicate") {
				t.Fatalf("%v: err = %v, want the refusal", args, err)
			}
		}
	})
}

// TestRunAttemptsRefusesADuplicatePlanTask: a best-of-N batch is N runs of one
// task, so the second batch of a task that is already being worked on is the
// same duplicate as a second single launch. The redundant attempts of a hard
// task are deliberate and say so (TestRunAllowsADeliberateDuplicate).
func TestRunAttemptsRefusesADuplicatePlanTask(t *testing.T) {
	f := duplicateRig(t)
	dupLiveRun(t, f, dupLive, duplicatePlan)
	dupRefused(t, f, task.ShortRun(dupLive), "run", "--repo", "acme/app", "--attempts", "2", "--plan-task", duplicatePlan, "Add a feature")
}

// TestRunDuplicateIgnoresItsOwnRunID: a repeated --run-id reports the launch it
// repeats instead of starting another, and the row that launch left is the
// launch's own -- so the guard has to leave it out of what it reads, or the
// idempotence the CLI documents everywhere would be broken by the run itself.
func TestRunDuplicateIgnoresItsOwnRunID(t *testing.T) {
	f := duplicateRig(t)
	args := []string{"run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, append(args, "--json")...)
	if err != nil {
		t.Fatalf("the repeat must report the launch it repeats: %v", err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Status != "already-launched" {
		t.Fatalf("repeat = %s (%v), want already-launched", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the first launch's one", n)
	}
}
