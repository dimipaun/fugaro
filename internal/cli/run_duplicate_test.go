package cli

// The duplicate guard's tests (run_duplicate.go): a launch of a plan task
// refuses while a run of it is going or a run of it left a pull request
// behind, warns and launches when it cannot find out, and is escaped with
// --allow-duplicate. The guard's one read is the fixture bucket, and the run
// records in it: no code host is opened and no credential is read, so nothing
// here touches a network or an account.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
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
	// request 7 behind it on duplicatePlan, dupOther is in flight on
	// duplicateOther, and dupNext is the run every launch below names.
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
// behind it, with the word the record has for what it left: runstore.OutcomeReady
// for a pull request it left ready, OutcomeDraft for one it left a draft --
// the two outcomes the listing folds a pull request's state out of
// (markPRStates).
func dupFinishedRun(t *testing.T, f *cloudFixture, id, planTask string, pr int, outcome runstore.Outcome) {
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
	rec := prRecord(id, "", pr, 1)
	rec.Outcome = outcome
	writeRecord(t, f, id, rec)
}

// dupFinishedNoPRRun stores a run of planTask that ended without ever getting
// as far as a pull request, which is a run that leaves nothing holding the
// task.
func dupFinishedNoPRRun(t *testing.T, f *cloudFixture, id, planTask string) {
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
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusFailed, Stage: "bootstrap", Outcome: runstore.OutcomeNone})
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

	t.Run("a pull request a run left ready", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		msg := dupRefused(t, f, "pull request #7", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if !strings.Contains(msg, task.ShortRun(dupLeft)) || !strings.Contains(msg, prStateReady) {
			t.Fatalf("refusal = %q, want the run that left it and the state the records fold", msg)
		}
	})

	t.Run("a pull request a run left a draft", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeDraft)
		msg := dupRefused(t, f, "pull request #7", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if !strings.Contains(msg, prStateStalled) {
			t.Fatalf("refusal = %q, want the state the records fold", msg)
		}
	})

	t.Run("a task nothing holds", func(t *testing.T) {
		f := duplicateRig(t)
		// A run of the task that failed before it ever opened a pull request:
		// it left nothing behind, so the task is free and the launch is real
		// work rather than a duplicate.
		dupFinishedNoPRRun(t, f, dupLeft, duplicatePlan)
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
		dupFinishedRun(t, f, dupLeft, duplicateOther, 8, runstore.OutcomeReady)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a launch that names no plan task", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
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
// place. The chain is named as the <slug>/<run-id> ref both commands take, and
// TestRunDuplicateWaysOutRun executes what is printed; the prose beside it
// still shows the run's ending, which is what every listing shows.
func TestRunDuplicateRefusalNamesTheWaysOut(t *testing.T) {
	f := duplicateRig(t)
	dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
	msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
	for _, want := range []string{
		"plan task " + duplicatePlan,
		"nothing was launched",
		"fugaro run --pr 7",
		"run " + task.ShortRun(dupLeft) + " left pull request #7 of acme/app",
		"fugaro run --retry " + dupRef(dupLeft),
		"--continues " + dupRef(dupLeft),
		"--allow-duplicate",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal = %q, want %q in it", msg, want)
		}
	}
}

// dupRef is the ref the refusal's commands name a run by, and the one the CLI's
// own lookups take: the runs bucket's <slug>/<run-id>. A bare ending is not
// one (runstore.Locate refuses it), which is the mistake this pins.
func dupRef(id string) string { return appSlug + "/" + id }

// TestRunDuplicateWaysOutRun is the other half of the refusal: the commands it
// prints are ones the CLI accepts. A refusal whose ways out are themselves
// refused sends the caller into a second error instead of out of the first, and
// both of these were printed as the run's bare ending, which neither --retry
// nor --continues resolves.
func TestRunDuplicateWaysOutRun(t *testing.T) {
	t.Run("the run in flight: --continues, as printed", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		ref := dupPrinted(t, msg, "--continues ")
		// The lookup both commands make first: a ref the CLI cannot resolve is
		// the refusal naming a fix that is not one.
		if _, _, err := locateRun(t.Context(), dupEnv(t, f), ref); err != nil {
			t.Fatalf("--continues %s, as the refusal printed it: %v", ref, err)
		}
		// And it is a way out rather than another refusal: the launch it names
		// happens.
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--continues", ref, "--run-id", dupOther, "Add a feature"); err != nil {
			t.Fatalf("the printed --continues way out was refused: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the continued launch's one", n)
		}
	})

	t.Run("a pull request a run left: --retry, as printed", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		ref := dupPrinted(t, msg, "--retry ")
		if _, _, err := locateRun(t.Context(), dupEnv(t, f), ref); err != nil {
			t.Fatalf("--retry %s, as the refusal printed it: %v", ref, err)
		}
	})
}

// dupEnv is the fixture's own cloud environment, the one locateRun reads a
// printed ref through.
func dupEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	b, err := blob.OpenBucket(t.Context(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return envOn(t, f, blobx.Wrap(b))
}

// dupPrinted is the argument the refusal's message gives to flag, as a caller
// reading it would copy it: everything between the flag and the next space or
// closing bracket. The command is read out of the printed sentence rather than
// rebuilt here, so the test fails on what the caller is actually told.
func dupPrinted(t *testing.T, msg, flag string) string {
	t.Helper()
	i := strings.Index(msg, flag)
	if i < 0 {
		t.Fatalf("refusal = %q, want %s in it", msg, flag)
	}
	fields := strings.FieldsFunc(msg[i+len(flag):], func(r rune) bool { return r == ' ' || r == ')' || r == ',' })
	if len(fields) == 0 {
		t.Fatalf("refusal = %q, want an argument after %s", msg, flag)
	}
	return fields[0]
}

// TestRunDuplicateNeedsNoCodeHostCredential is the guard's shape pinned against
// the mistake that would have made it worthless: the check must not need the
// operator's code-host credential. The launcher holds no App key and no
// Bitbucket token (docs/git-providers.md), so a guard that read the forge would
// warn and launch on an ordinary machine -- the exact launch the incident's
// duplicates were -- and one that did hold a key would mint an installation
// token from a coding agent's session, which every other path refuses there.
// The environment here is stripped of every one of those variables, and the
// refusal still lands from the run records alone.
func TestRunDuplicateNeedsNoCodeHostCredential(t *testing.T) {
	for _, name := range []string{
		"FUGARO_GITHUB_APP_ID", "FUGARO_GITHUB_APP_PRIVATE_KEY", "FUGARO_GITHUB_APP_PRIVATE_KEY_FILE",
		"FUGARO_BITBUCKET_TOKEN", "FUGARO_BITBUCKET_EMAIL", "GH_TOKEN", "GITHUB_TOKEN",
	} {
		t.Setenv(name, "")
	}
	f := duplicateRig(t)
	dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
	msg := dupRefused(t, f, "pull request #7", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
	if strings.Contains(msg, "warning") {
		t.Fatalf("refusal = %q, want the decision taken from the records with no warning about a read that could not be made", msg)
	}
}

// TestRunAllowsADeliberateDuplicate: --allow-duplicate launches the second run
// and prints one line saying what it duplicates. It records nothing -- the task
// it stores is the task the same launch without the flag would store, which is
// what keeps the flag out of every wire format -- and it works for a batch,
// which is the redundant-attempts shape the parallelism skill plans.
func TestRunAllowsADeliberateDuplicate(t *testing.T) {
	f := duplicateRig(t)
	dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
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

// TestRunDuplicateCheckFailsOpen: a read that cannot be made warns once and
// launches. The guard is a fence around a task that is already being worked on,
// and a bucket this machine cannot read is not evidence that the task is free --
// but refusing on that would stop every launch from that machine, which is
// worse than the duplicate it prevents.
func TestRunDuplicateCheckFailsOpen(t *testing.T) {
	f := duplicateRig(t)
	// The guard's own read, not a launch's: a bucket that cannot be read is the
	// read's failure without a fixture that breaks the launch path with it.
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
	if err := planTaskDuplicate(ctx, envOn(t, f, blobx.Wrap(b)), o, appSlug, "acme/app", dupNext, &warn); err != nil {
		t.Fatalf("a read the guard cannot make must not refuse: %v", err)
	}
	if !strings.Contains(warn.String(), "could not check whether plan task "+duplicatePlan) {
		t.Fatalf("warning = %q, want the line about the check that did not run", warn.String())
	}
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
