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
	"fmt"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
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

// dupLiveFollowUpRun stores a run of planTask that is in flight and carries a
// pull request of its own: a follow-up of pr, which is the one shape where a
// run going and a pull request are both there, and so the one where a refusal
// has to choose between the two.
func dupLiveFollowUpRun(t *testing.T, f *cloudFixture, id, planTask, root string, pr int) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	spec := followUpSpec(id, root, root, pr)
	spec.PlanTask = planTask
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, PlanTask: planTask, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// dupLivePRRun stores a run that is in flight on pull request pr and names no
// plan task: a --pr follow-up launched on its own, which is the command the
// refusal itself prints (followup.go records o.planTask, empty). Its row
// carries the pull request and no task, so the guard never matches the run --
// and the pull request it is going on reads live for every row the guard does
// see, the finished run that left that pull request among them.
func dupLivePRRun(t *testing.T, f *cloudFixture, id, root string, pr int) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if err := s.CreateTask(ctx, followUpSpec(id, root, root, pr)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// pausedPRRun stores a run that suspended itself on pull request pr and names
// no plan task: a --pr follow-up of root launched on its own, which is the
// command the refusal itself prints, that then paused waiting for a credential.
// Its row carries the pull request and no task, so the guard never matches it --
// and the pull request it is suspended on reads paused for every row the guard
// does see, the finished run that left that pull request among them (the fold
// is the pull request's, and the node that says the pause stands is written by
// the test, pausedNode).
func pausedPRRun(t *testing.T, f *cloudFixture, id, root string, pr int, at time.Time) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if err := s.CreateTask(ctx, followUpSpec(id, root, root, pr)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, LaunchedAt: at}); err != nil {
		t.Fatal(err)
	}
	rec := prRecord(id, "", pr, 1)
	rec.Branch, rec.Status, rec.Stage, rec.Outcome, rec.StartedAt = "fugaro/"+root, runstore.StatusPaused, "implement", runstore.OutcomeNone, at
	writeRecord(t, f, id, rec)
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

// TestRunDuplicateRefusalNamesTheWaysOut pins the refusal's ways out, per shape,
// because the shapes have different ones: a run of the task that ended and left
// its pull request is continued by naming that pull request, and a run still in
// flight is waited for, with the deliberate second attempt as the other thing a
// caller may do. A guard that only said "refused" would leave the caller to
// guess, and the guess is what launched the duplicate in the first place;
// TestRunDuplicateWaysOutRun runs what is printed.
//
// No shape names --retry or --continues: every run this guard can name has
// launched already, so a --retry of it reports already-launched and starts
// nothing (run.go's existingLaunch), and a --continues of it -- a launch that
// takes the guard's own way out rather than meeting it (run.go's call site skips
// the check) -- would start a second run of the very task the refusal just
// stopped, on a chain whose remaining budget the live run is still spending
// against. While a run is in flight --pr is missing too, and so is it where the
// pull request a run of the task left is itself live, a run of it going that the
// guard did not match, or itself paused, a run of it suspended in a pause that
// still stands: resolvePR refuses a follow-up while any of the pull request's
// runs is live *or suspended*, so --pr would be one more dead end in each, and
// waiting -- over the repository, or over that one pull request -- is the whole
// of those shapes. A way out that does nothing,
// that is refused itself, or that starts the duplicate by another door, is worse
// than none.
func TestRunDuplicateRefusalNamesTheWaysOut(t *testing.T) {
	t.Run("the run left a pull request", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		for _, want := range []string{
			"plan task " + duplicatePlan,
			"nothing was launched",
			"fugaro run --pr 7 --repo acme/app",
			"run " + task.ShortRun(dupLeft) + " left pull request #7 of acme/app",
			"--allow-duplicate",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want %q in it", msg, want)
			}
		}
		for _, bad := range []string{"--retry", "--continues"} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal names %s, a command that starts nothing here: %q", bad, msg)
			}
		}
	})

	t.Run("the run is still in flight", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		for _, want := range []string{
			"plan task " + duplicatePlan,
			"nothing was launched",
			"run " + task.ShortRun(dupLive) + " is still pending",
			"fugaro runs ls --repo acme/app",
			"--allow-duplicate",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want %q in it", msg, want)
			}
		}
		for _, bad := range []string{"--retry", "--continues"} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal names %s while the run is still going, and that launch would start the second run this refusal just stopped: %q", bad, msg)
			}
		}
	})

	t.Run("the run in flight is on a pull request", func(t *testing.T) {
		// A follow-up run that is still going carries the pull request it
		// continues, and a --pr of that pull request is refused for as long as
		// one of its runs is live (resolvePR): waiting is the whole of the way
		// out here, and the live run is named with it.
		f := duplicateRig(t)
		dupLiveFollowUpRun(t, f, dupLive, duplicatePlan, dupLeft, 7)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		for _, want := range []string{
			"run " + task.ShortRun(dupLive) + " is still pending, on pull request #7 of acme/app",
			"fugaro runs ls --repo acme/app",
			"--allow-duplicate",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want %q in it", msg, want)
			}
		}
		for _, bad := range []string{"--retry", "--continues", "--pr "} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal names %s while a run of that pull request is still going, and that launch is refused too: %q", bad, msg)
			}
		}
	})

	t.Run("a run going on the pull request a run of the task left", func(t *testing.T) {
		// A run of the task ended and left pull request 7, and a run of that
		// pull request is going that this guard never matches: a --pr follow-up
		// launched without --plan-task, which is the command the refusal itself
		// prints. resolvePR reads one pull request's runs whatever task they
		// name, so the --pr the refusal would print here is refused exactly as
		// it is for a run of the task still going, and the pull request's own
		// listing is the way out instead.
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		dupLivePRRun(t, f, dupLive, dupLeft, 7)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		for _, want := range []string{
			"run " + task.ShortRun(dupLeft) + " left pull request #7 of acme/app",
			"still being worked on",
			"fugaro runs ls --pr 7 --repo acme/app",
			"--allow-duplicate",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want %q in it", msg, want)
			}
		}
		for _, bad := range []string{"fugaro run --pr", "--retry", "--continues"} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal names %s while a run of that pull request is still going, and that launch is refused too: %q", bad, msg)
			}
		}
	})

	t.Run("a run suspended on the pull request a run of the task left", func(t *testing.T) {
		// The same shape, one step further on: the run of pull request 7 that
		// the guard never matches did not stop going -- it suspended itself into
		// a pause that still stands (PR12). Between the pause and its resume that
		// chain is in flight, so resolvePR refuses a follow-up of that pull
		// request exactly as it refuses one with a run going, and `fugaro run --pr
		// 7` -- the command the default branch of the refusal prints -- would be a
		// way out that is itself refused. Printing it anyway is the bug this
		// sub-test exists for.
		f := duplicateRig(t)
		at := time.Now().UTC()
		resume := at.Add(time.Hour)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		suspended := wallRunIDAt(0, "000700", "d7d7")
		pausedPRRun(t, f, suspended, dupLeft, 7, at)
		pausedNode(t, f, suspended, dupLeft, resume, false)

		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		for _, want := range []string{
			"run " + task.ShortRun(dupLeft) + " left pull request #7 of acme/app",
			"suspended in a pause that will resume it",
			"wait for the pause on that pull request to be resumed (fugaro runs ls --pr 7 --repo acme/app)",
			"--allow-duplicate",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want %q in it", msg, want)
			}
		}
		for _, bad := range []string{"fugaro run --pr", "--retry", "--continues"} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal names %s while a run of that pull request is suspended, and that launch is refused too: %q", bad, msg)
			}
		}
	})
}

// dupRef is the ref the refusal's commands name a run by, and the one the CLI's
// own lookups take: the runs bucket's <slug>/<run-id>. A bare ending is not
// one (runstore.Locate refuses it), which is the mistake this pins.
func dupRef(id string) string { return appSlug + "/" + id }

// TestRunDuplicateWaysOutRun is the other half of the refusal: the commands it
// prints are ones the CLI accepts and that really do the thing they are printed
// for. A refusal whose ways out are themselves refused sends the caller into a
// second error instead of out of the first, which the bare ending they were
// once printed as did, and a way out the CLI accepts but that launches nothing
// -- a --retry of a run that already launched -- is the same dead end one step
// further on. So each arm runs what its shape prints: the launches are held to
// the execution they start, the listing to itself, and the shapes it must not
// name while a run is in flight to the duplicate they would start.
func TestRunDuplicateWaysOutRun(t *testing.T) {
	t.Run("the run in flight: waiting, and the deliberate second attempt", func(t *testing.T) {
		f := duplicateRig(t)
		dupLiveRun(t, f, dupLive, duplicatePlan)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		// What the refusal must not do while the run is going: name a launch
		// that starts a second run of the same task. `--continues` is exactly
		// that -- the guard skips it -- so the launch it names would be the
		// duplicate this refusal just stopped.
		if strings.Contains(msg, "--continues") {
			t.Fatalf("refusal names --continues of the run in flight: %q", msg)
		}
		// The command it names instead is the listing, which starts nothing.
		if _, _, err := execute(t, "runs", "ls", "--repo", "acme/app"); err != nil {
			t.Fatalf("the printed `fugaro runs ls --repo acme/app`: %v", err)
		}
		// And the launch it names beside that is a way out rather than another
		// refusal: the deliberate second attempt it prints happens, because that
		// is the one the caller asked for.
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "--allow-duplicate", "Add a feature"); err != nil {
			t.Fatalf("the printed --allow-duplicate way out was refused: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the deliberate second attempt's one", n)
		}
	})

	t.Run("a pull request a run left: --pr, as printed", func(t *testing.T) {
		f := duplicateRig(t)
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		// Both arguments are read out of the printed sentence, the repository
		// included: the refusal names the caller's own <owner/name>, and a
		// launch copied without it is refused outside a checkout or aimed at
		// whatever repository the caller happens to stand in.
		pr := dupPrinted(t, msg, "--pr ")
		repo := dupPrinted(t, msg, "--repo ")
		ref := dupRef(dupLeft)
		// The two commands the refusal does not print, each held to what it
		// really does with the run it would have named -- not to the error it
		// reports, which the fixture's thin launch record can turn into a
		// different one: what matters is that the caller gets no run.
		if _, _, _ = execute(t, "run", "--retry", ref); len(f.run.Executions()) != 0 {
			t.Fatalf("--retry of a run that already launched started %d execution(s), want none", len(f.run.Executions()))
		}
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--continues", ref, "--run-id", dupOther, "Add a feature"); err == nil {
			t.Fatalf("--continues %s was taken, and the refusal must print it then", ref)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions after the two commands the refusal does not print, want none", n)
		}
		// And what it does print is a way out rather than another refusal: the
		// launch it names happens, with the repository it printed.
		if _, _, err := execute(t, "run", "--pr", pr, "--repo", repo, "--run-id", dupOther, "Add a feature"); err != nil {
			t.Fatalf("the printed --pr way out was refused: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the follow-up launch's one", n)
		}
	})

	t.Run("a pull request a run suspended on: the listing, not --pr", func(t *testing.T) {
		// The pull request a run of the task left has a run of its own suspended
		// in a pause that stands. The refusal must not print `fugaro run --pr 7`
		// here, and this arm is the proof rather than the assertion: it runs that
		// launch and the pause refuses it (resolvePR), which is exactly the dead
		// end the printed way out would send the caller into. What it prints
		// instead -- the pull request's listing -- runs, and the deliberate
		// second attempt it names beside it really launches.
		f := duplicateRig(t)
		at := time.Now().UTC()
		dupFinishedRun(t, f, dupLeft, duplicatePlan, 7, runstore.OutcomeReady)
		suspended := wallRunIDAt(0, "000800", "d8d8")
		pausedPRRun(t, f, suspended, dupLeft, 7, at)
		pausedNode(t, f, suspended, dupLeft, at.Add(time.Hour), false)

		msg := dupRefused(t, f, "already being worked on", "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		if strings.Contains(msg, "fugaro run --pr") {
			t.Fatalf("refusal prints `fugaro run --pr` while a run of that pull request is suspended in a standing pause: %q", msg)
		}
		// The launch it must not print, run as printed: refused, because the
		// suspended run holds that pull request exactly as a running one does.
		if _, _, err := execute(t, "run", "--pr", "7", "--repo", "acme/app", "--run-id", dupOther, "Add a feature"); ExitCode(err) != ExitUserError {
			t.Fatalf("`fugaro run --pr 7` with a run of it suspended: err = %v, want the follow-up refusal the printed way out would have been", err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions after the launch the refusal must not print, want none", n)
		}
		// The listing it prints instead is a way out rather than another
		// refusal: it starts nothing and reports the pull request.
		if _, _, err := execute(t, "runs", "ls", "--pr", "7", "--repo", "acme/app"); err != nil {
			t.Fatalf("the printed `fugaro runs ls --pr 7 --repo acme/app`: %v", err)
		}
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "--allow-duplicate", "Add a feature"); err != nil {
			t.Fatalf("the printed --allow-duplicate way out was refused: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the deliberate second attempt's one", n)
		}
	})
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

// TestRunPlanTaskLaunchReadsNoExecutionPerRun: a launch with --plan-task reads
// the history to find a duplicate, and what it asks the compute platform must
// not grow with that history. Every run whose execution the platform has since
// deleted used to cost a request of its own, which on a 90-day window was
// thousands per launch, against a read quota the launches themselves share.
//
// Mutation caught: following each run the executions listing lacks with a read
// of its own.
func TestRunPlanTaskLaunchReadsNoExecutionPerRun(t *testing.T) {
	f := duplicateRig(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	const history = 60
	at := time.Now().Add(-48 * time.Hour)
	for i := range history {
		id := at.Add(time.Duration(i)*time.Minute).Format("20060102-150405") + fmt.Sprintf("-%04x", i)
		s := runstore.Open(b, appSlug, id)
		if err := s.CreateTask(ctx, firstRunSpec(id)); err != nil {
			t.Fatal(err)
		}
		gone := backend.ExecID{GCPProject: "proj-1234", Region: "us-east5", Job: gcp.JobName(appSlug, "web"), Name: fmt.Sprintf("gone-%04x", i)}.String()
		if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: gone, LaunchedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	if _, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"); err != nil {
		t.Fatalf("launch: %v (%s)", err, errOut)
	}
	gets := 0
	for _, r := range f.run.Requests() {
		if r.Method == "GET" && strings.Contains(r.Path, "/executions/") {
			gets++
		}
	}
	if gets != 0 {
		t.Fatalf("a launch over %d past runs read %d executions one by one, want none: the cost of a launch must not grow with the history", history, gets)
	}
}
