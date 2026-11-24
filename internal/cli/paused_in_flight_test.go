package cli

// PR12 of docs/model-credentials-routing: between a pause and its resume the
// chain is in flight to every guard that asks (F64, D3195).
//
// The evidence a pause leaves has two halves, and every test here seeds both
// (R6, C3): the run's own record says it ended paused, and the credentials
// service's node under paused/ says whether the pause still stands and when the
// resume is due. The record is what a listing's rows carry; the node is the one
// thing the rows cannot hold, and it is the only place the launcher's
// cancellation is written (D3197). So a guard that read the rows alone reads
// every suspended chain as a finished run -- which is exactly the second launch
// F64 found admitted between a pause and its resume.
//
// The pause's own word is written by hand here, as the runner's suspend (PR5)
// will write it: WriteRecord validates nothing, and the record these tests
// store is the object the runner leaves (runstore.StatusPaused, Stage and
// Outcome as a suspend leaves them).

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// pausedNode stores the credentials service's mark for run id: the pause
// standing, or cancelled by the run's launcher (D3197). resumeAt is the instant
// the refusal hands the caller, and chain is the id of the chain the pause
// belongs to (the run id the chain is rooted at, D1045).
func pausedNode(t *testing.T, f *cloudFixture, id, chain string, resumeAt time.Time, cancelled bool) {
	t.Helper()
	n := runstore.PauseNode{
		Launcher: "112233445566778899", ResumeAt: resumeAt.UnixMilli(),
		Chain: chain, PausedSeconds: 600, Cancelled: cancelled,
	}
	raw, err := n.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	f.budget.DB.Set(runstore.PauseNodePath(appSlug, id), json.RawMessage(raw))
}

// pausedRun stores a first run that suspended itself: its task, the launch
// record the duplicate guard reads its plan task from, and a record that ended
// paused. pr is the pull request the run carries, 0 for a suspended run that
// opened none -- a run between a pause and its resume is in flight either way,
// which is the case a guard that keyed on a pull request would miss.
func pausedRun(t *testing.T, f *cloudFixture, id, planTask string, pr int, at time.Time) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	spec := firstRunSpec(id)
	spec.PlanTask = planTask
	if pr > 0 {
		spec.Branch, spec.PR = "fugaro/"+id, pr
	}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, PlanTask: planTask, LaunchedAt: at}); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusPaused, Stage: "implement", Outcome: runstore.OutcomeNone, StartedAt: at}
	if pr > 0 {
		rec.Branch = "fugaro/" + id
		rec.PR = &runstore.PRRef{Number: pr, URL: "https://github.com/acme/app/pull/" + strconv.Itoa(pr)}
	}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// pausedView is the daemon's own run view over the fixture that is current when
// it is called, built the way `fugaro orchestrator run` builds it: the same
// environment (the fixture's own config, which FUGARO_CONFIG names) and the
// project's own database client, which is the one the pause node is read
// through (orchestrator_cmd's openBudget).
func pausedView(t *testing.T) *orchestratorRunView {
	t.Helper()
	ctx := context.Background()
	lc, db, err := openBudget(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.bucket.Close() })
	return &orchestratorRunView{bucket: env.bucket.Bucket, env: env, db: db, warn: io.Discard}
}

// TestAPausedChainIsInFlight pins the first half of PR12's acceptance: while a
// pause stands the chain is in flight to every guard that asks, exactly as a
// running run is. Each guard reads it from a different place and each has its
// own sub-test -- the duplicate guard reads a listing's rows, the follow-up
// guard reads one pull request's, and the orchestrator's own view reads the
// records the state node names -- so a fix that reached only one of them is a
// fix that leaves the others admitting the second run F64 found.
func TestAPausedChainIsInFlight(t *testing.T) {
	at := time.Now().UTC()
	resume := at.Add(90 * time.Minute)

	t.Run("a duplicate launch", func(t *testing.T) {
		f := duplicateRig(t)
		suspended := wallRunIDAt(0, "000500", "d4d4")
		pausedRun(t, f, suspended, duplicatePlan, 0, at)
		pausedNode(t, f, suspended, suspended, resume, false)

		msg := dupRefused(t, f, "is still paused until",
			"run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature")
		// The refusal names the run by its ending, the instant the chain comes
		// back (the whole of what tells the caller whether the wait is minutes
		// or hours, R15), and the one way out a suspended chain has.
		for _, want := range []string{task.ShortRun(suspended), resume.Format(time.RFC3339), "wait for the chain to resume it"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal = %q, want it to carry %q", msg, want)
			}
		}
	})

	t.Run("a follow-up", func(t *testing.T) {
		f := newCloudFixture(t)
		seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
		suspended := wallRunIDAt(0, "130000", "beef")
		seedFollowUp(t, f, suspended, rootID, rootID, at, runstore.StatusPaused, true)
		pausedNode(t, f, suspended, rootID, resume, false)

		wantRefused(t, f, "is paused until "+resume.Format(time.RFC3339),
			"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID)
	})

	t.Run("the orchestrator's own view", func(t *testing.T) {
		f := newCloudFixture(t)
		suspended := wallRunIDAt(0, "140000", "c0de")
		pausedRun(t, f, suspended, "", 0, at)
		pausedNode(t, f, suspended, suspended, resume, false)

		reps, err := pausedView(t).Runs(context.Background(), []string{suspended})
		if err != nil {
			t.Fatal(err)
		}
		// The platform's word on the execution is deliberately not asked about
		// it: ending the execution is what a pause is (R12), so a view that
		// folded on it would read every suspended chain as a run that died
		// without reporting.
		if len(reps) != 1 || !reps[0].Started || reps[0].Finished {
			t.Fatalf("the daemon's view of a suspended run = %+v, want it started, not finished, and folded on nothing else", reps)
		}
	})
}

// TestACancelledPauseIsNotInFlight pins the exception the same acceptance
// names: a pause its launcher cancelled is not in flight (D3197). Nothing will
// resume that chain, so every guard that refused above admits the work again --
// and the daemon's view folds it as the cancellation it is, the outcome no
// standing approval retries.
func TestACancelledPauseIsNotInFlight(t *testing.T) {
	at := time.Now().UTC()
	resume := at.Add(90 * time.Minute)

	t.Run("a duplicate launch", func(t *testing.T) {
		f := duplicateRig(t)
		over := wallRunIDAt(0, "000600", "d5d5")
		pausedRun(t, f, over, duplicatePlan, 0, at)
		pausedNode(t, f, over, over, resume, true)

		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", dupNext, "--plan-task", duplicatePlan, "Add a feature"); err != nil {
			t.Fatal(err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the launch's one: a cancelled pause holds nothing", n)
		}
	})

	t.Run("a follow-up", func(t *testing.T) {
		f := newCloudFixture(t)
		seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
		over := wallRunIDAt(0, "130500", "feed")
		seedFollowUp(t, f, over, rootID, rootID, at, runstore.StatusPaused, true)
		pausedNode(t, f, over, rootID, resume, true)

		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
			t.Fatal(err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the follow-up's one: a cancelled pause is a chain that is over", n)
		}
	})

	t.Run("the orchestrator's own view", func(t *testing.T) {
		f := newCloudFixture(t)
		over := wallRunIDAt(0, "140500", "dead")
		pausedRun(t, f, over, "", 0, at)
		pausedNode(t, f, over, over, resume, true)

		reps, err := pausedView(t).Runs(context.Background(), []string{over})
		if err != nil {
			t.Fatal(err)
		}
		if len(reps) != 1 || !reps[0].Finished || reps[0].Outcome != orchestrator.OutcomeCancelled {
			t.Fatalf("the daemon's view of a cancelled pause = %+v, want it finished with the cancellation", reps)
		}
	})
}

// TestThePullRequestStateIsPaused pins the second half of PR12's acceptance:
// the pull request is a fifth state, `paused` (D3195), a new value of the
// pr_state member the other four are -- none of them a new member, and none of
// them changed (CLAUDE.md, "A field never changes meaning"). It is read in the
// fold every listing makes (markPRStates), and end to end from the bucket, with
// the node deciding between a pause that stands and one the launcher cancelled.
func TestThePullRequestStateIsPaused(t *testing.T) {
	const slug = "acme-app-3f9c1d2e4b5a6078" // a storage slug, one token, as a row's run reference carries it
	at := lsNow()
	standing := func(s, r string) (pauseFact, bool) { return pauseFact{resumeAt: at.Add(time.Hour)}, true }
	cancelled := func(s, r string) (pauseFact, bool) { return pauseFact{cancelled: true}, true }

	// PR 41: a run of it suspended itself over a run that had left it ready.
	// The pause is the newer, larger fact about the pull request -- the work is
	// not finished and the chain comes back on its own -- so paused is read
	// before ready, and every row of the pull request carries it.
	rows := []runview.Row{
		prStateRow(slug, "20261112-090000-aa41", at.Add(-time.Hour), runstore.StatusSucceeded, runstore.OutcomeReady, 41),
		prStateRow(slug, "20261112-090000-bb41", at, runstore.StatusPaused, runstore.OutcomeNone, 41),
	}
	markPRStates(rows, nil, standing)
	for _, r := range rows {
		if r.PRState != prStatePaused {
			t.Errorf("row %s of a suspended pull request carries pr_state %q, want %q", r.RunID, r.PRState, prStatePaused)
		}
	}

	// PR 42: the same row with the launcher's cancellation on the node. The
	// chain is over, so the pull request is whatever its runs left, and a run
	// that ended with no word for it leaves it stalled.
	rows = []runview.Row{
		prStateRow(slug, "20261112-090000-aa42", at, runstore.StatusPaused, runstore.OutcomeNone, 42),
	}
	markPRStates(rows, nil, cancelled)
	if rows[0].PRState != prStateStalled {
		t.Errorf("row %s of a cancelled pause carries pr_state %q, want %q", rows[0].RunID, rows[0].PRState, prStateStalled)
	}

	// And the three words that were there before still mean what they did: a
	// run going, a run that left the pull request ready, and a run that left
	// it a draft or failed. The fifth word takes nothing from them.
	rows = []runview.Row{
		prStateRow(slug, "20261112-090000-aa43", at, runstore.StatusRunning, runstore.OutcomeNone, 43),
		prStateRow(slug, "20261112-090000-aa44", at, runstore.StatusSucceeded, runstore.OutcomeReady, 44),
		prStateRow(slug, "20261112-090000-aa45", at, runstore.StatusFailed, runstore.OutcomeDraft, 45),
	}
	markPRStates(rows, nil, standing)
	for i, want := range []string{prStateLive, prStateReady, prStateStalled} {
		if rows[i].PRState != want {
			t.Errorf("row %s carries pr_state %q, want %q", rows[i].RunID, rows[i].PRState, want)
		}
	}

	t.Run("from a listing", func(t *testing.T) {
		f := newCloudFixture(t)
		suspended := wallRunIDAt(0, "150000", "5a5a")
		pausedRun(t, f, suspended, "", 7, at)
		pausedNode(t, f, suspended, suspended, at.Add(time.Hour), false)

		got, _ := lsJSON(t, "--since", "0")
		// The state is the pull request's, beside the run's own status: the two
		// are different facts and the row carries both (a suspended run's
		// record says paused, and the pull request it is on is paused too).
		if len(got.Runs) != 1 || got.Runs[0].Status != string(runstore.StatusPaused) || got.Runs[0].PRState != prStatePaused {
			t.Fatalf("runs ls = %+v, want the one row with status %q and pr_state %q", got.Runs, runstore.StatusPaused, prStatePaused)
		}
		// --state paused is untouched: it is the run's own word and stays in
		// the vocabulary the flag has always matched (lsPRStates is the pull
		// request's three, deliberately not this one).
		got, _ = lsJSON(t, "--since", "0", "--state", "paused")
		if ids := runIDs(got.Runs); !slices.Equal(ids, []string{suspended}) {
			t.Errorf("runs ls --state paused listed %v, want the run that suspended itself alone (%v)", ids, []string{suspended})
		}
		// The node decides the word, and nothing else does: the same record
		// with the launcher's cancellation on it reads as what the run left.
		pausedNode(t, f, suspended, suspended, at.Add(time.Hour), true)
		got, _ = lsJSON(t, "--since", "0")
		if len(got.Runs) != 1 || got.Runs[0].PRState != prStateStalled {
			t.Fatalf("runs ls = %+v, want pr_state %q once the pause is cancelled", got.Runs, prStateStalled)
		}
	})
}
