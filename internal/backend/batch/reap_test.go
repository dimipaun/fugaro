package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// reapTest is one reaper on the Batch and Compute fakes, with a clock the
// test drives and the two listings it needs.
type reapTest struct {
	r   *Reaper
	bf  *gcpfake.Batch
	cf  *gcpfake.Compute
	now time.Time
}

// newReaperTest builds the reaper every test starts from: one installation,
// one region, a dedicated service account's credentials (NoAuth here), a
// 30-minute longest workflow (so QueueDeadline is 32m and MaxAge 79m), and
// the clock pinned an hour after the epoch the test back-dates its jobs and
// instances from.
func newReaperTest(t *testing.T, tweak func(*ReaperOptions)) *reapTest {
	t.Helper()
	bf, cf := gcpfake.NewBatch(t), gcpfake.NewCompute(t)
	bf.Project, bf.Region = testProject, testRegion
	cf.Project = testProject
	// The fakes' render knobs and the reaper's endpoints must agree: a job
	// the fake answers under another project is one the reaper's own
	// canonical names would not match.
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	o := ReaperOptions{
		GCPProject: testProject,
		Region:     testRegion,
		Timeouts:   testTimeouts{task: 30 * time.Minute, longest: 30 * time.Minute},
		Endpoints:  Endpoints{Batch: bf.URL + "/", Compute: cf.URL + "/compute/v1/", NoAuth: true},
		Now:        func() time.Time { return now },
	}
	if tweak != nil {
		tweak(&o)
	}
	r, err := NewReaper(context.Background(), o)
	if err != nil {
		t.Fatalf("NewReaper: %v", err)
	}
	return &reapTest{r: r, bf: bf, cf: cf, now: now}
}

// The ages the tests back-date their jobs and instances by, read off the
// reaper's own deadlines rather than written as literals. The harness's
// 30-minute workflow is shorter than a run's own override ceiling
// (backend.MaxRunTime), so the deadlines are the ceiling's -- a queue deadline
// of a day, a run budget of a day plus ReapMargin, an age floor of the two
// added -- and a literal "ten hours" is no longer past any of them.
// TestReaperDefaultsComeFromTheRunCeiling pins the numbers themselves.

// stuck is a time in one state past the run budget: the shape the reaper
// deletes, and the shape a cancellation that has not finished is reported for.
func (rt *reapTest) stuck() time.Duration { return rt.r.RunBudget() + time.Hour }

// old is an instance age past MaxAge: the age floor no longer saves it, which
// is what a test wants when the thing that must save a VM is the job behind it.
func (rt *reapTest) old() time.Duration { return rt.r.MaxAge() + time.Hour }

// startedAfter is how long ago to create a job whose state began stateAge ago,
// for the states whose event must postdate the creation time plus the queue
// deadline: the check the SCHEDULED, DELETION_IN_PROGRESS and
// CANCELLATION_IN_PROGRESS branches make before they read a status event as the
// state's. A job created only stateAge ago (the harness's default) carries an
// event at its creation time, which may date the queue wait, and is refused
// (TestReaperRefusesAStaleStateEvent is that shape).
func (rt *reapTest) startedAfter(stateAge time.Duration) time.Duration {
	return rt.r.QueueDeadline() + stateAge + time.Hour
}

// addJob creates a Fugaro job as the launcher would: the design's label set
// in both label fields, which is what makes it reapable at all (design
// §2.4), in the raw Batch state and at the age given.
func (rt *reapTest) addJob(t *testing.T, runID, state string, age time.Duration) string {
	t.Helper()
	return rt.addJobLabels(t, runID, state, age, nil)
}

// addJobLabels is addJob with extra labels merged into both label fields (a
// test that wants a job this installation did not launch, say).
func (rt *reapTest) addJobLabels(t *testing.T, runID, state string, age time.Duration, edit map[string]string) string {
	t.Helper()
	return rt.addJobLabelFields(t, runID, state, age, edit, edit)
}

// addJobLabelFields is addJobLabels with the two label fields edited
// separately. A job whose Job.labels and AllocationPolicy.labels disagree is
// what the reaper's own client-side marker check exists for (the API's filter
// reads Job.labels only), and one map cannot build it: a key removed here must
// stay there.
func (rt *reapTest) addJobLabelFields(t *testing.T, runID, state string, age time.Duration, jobEdit, allocEdit map[string]string) string {
	t.Helper()
	id := JobID(backendtest.Slug, backendtest.Workflow, runID)
	labels, err := Labels(backendtest.Slug, backendtest.Workflow, runID)
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	jobLabels, allocLabels := map[string]string{}, map[string]string{}
	for k, v := range labels {
		jobLabels[k], allocLabels[k] = v, v
	}
	editField := func(field map[string]string, edit map[string]string) {
		for k, v := range edit {
			if v == "" {
				delete(field, k)
				continue
			}
			field[k] = v
		}
	}
	editField(jobLabels, jobEdit)
	editField(allocLabels, allocEdit)
	body := map[string]any{
		"labels":           jobLabels,
		"allocationPolicy": map[string]any{"labels": allocLabels},
		"status":           map[string]any{"state": "QUEUED"},
	}
	rt.create(t, id, body)
	// The state AND its status event: the reaper judges a job's stuckness by
	// the time it has spent in the state it reports, so a job at "age" is one
	// that entered that state "age" ago. A job older than its state -- one that
	// queued for a long time and then started -- is what SetStateAt and
	// addJobStateAt are for.
	rt.bf.SetStateAt(id, state, rt.now.Add(-age))
	rt.bf.SetCreated(id, rt.now.Add(-age))
	if state == "RUNNING" {
		// A RUNNING job reports the runDuration Batch defines for exactly this
		// quantity ("the duration of time that the Job spent in status
		// RUNNING"), and the reaper requires it for a RUNNING job: the event
		// may date an earlier state, so it is not a measurement on its own. A
		// test that wants the shape the reaper refuses clears it again
		// (SetRunDuration(id, "")).
		rt.bf.SetRunDuration(id, age.String())
	}
	return id
}

// addJobStateAt is addJob with the two times the reaper reads separated: the
// job was created createdAt ago and entered state stateAge ago. It is how a
// test builds the job whose creation age and state age disagree -- the case
// the reaper must not confuse.
func (rt *reapTest) addJobStateAt(t *testing.T, runID, state string, createdAge, stateAge time.Duration) string {
	t.Helper()
	id := rt.addJobLabels(t, runID, state, stateAge, nil)
	rt.bf.SetCreated(id, rt.now.Add(-createdAge))
	return id
}

// addJobDisagreeingRunLabels is addJob with the two label fields naming
// different runs: not a job this build's launcher could create (Labels writes
// one value into both fields, names.go), and the ambiguity reapJob refuses
// rather than pick a run from.
func (rt *reapTest) addJobDisagreeingRunLabels(t *testing.T, runID, state string, age time.Duration) string {
	t.Helper()
	id := JobID(backendtest.Slug, backendtest.Workflow, runID)
	labels, err := Labels(backendtest.Slug, backendtest.Workflow, runID)
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	jobLabels, allocLabels := map[string]string{}, map[string]string{}
	for k, v := range labels {
		jobLabels[k], allocLabels[k] = v, v
	}
	allocLabels[RunLabel] = "acme-app-20260927-100009-abcd"
	body := map[string]any{
		"labels":           jobLabels,
		"allocationPolicy": map[string]any{"labels": allocLabels},
		"status":           map[string]any{"state": "QUEUED"},
	}
	rt.create(t, id, body)
	rt.bf.SetState(id, state)
	rt.bf.SetCreated(id, rt.now.Add(-age))
	return id
}

// create posts a job to the fake's own jobs.create, rather than through
// Launch: the reaper's decisions are about jobs that already exist in a given
// state, and Launch mints a name and a state of its own. The POST is the real
// call, so the fake's create-time validation (the job id, the body) still
// applies.
func (rt *reapTest) create(t *testing.T, id string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the job: %v", err)
	}
	u := fmt.Sprintf("%s/v1/projects/%s/locations/%s/jobs?jobId=%s", rt.bf.URL, testProject, testRegion, id)
	resp, err := http.Post(u, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: %s: %s", u, resp.Status, b)
	}
}

// vm adds a Compute instance, in the fake's single zone.
func (rt *reapTest) vm(name string, labels map[string]string, age time.Duration) {
	rt.cf.AddInstanceCreated("fake-region-a", name, labels, rt.now.Add(-age))
}

// runLabels is the label set a VM created by a Fugaro job carries:
// AllocationPolicy.labels reach the VMs, Job.labels do not (design §2.4).
func runLabels(t *testing.T, runID string) map[string]string {
	t.Helper()
	l, err := Labels(backendtest.Slug, backendtest.Workflow, runID)
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	return l
}

func (rt *reapTest) reap(t *testing.T) Report {
	t.Helper()
	rep, err := rt.r.Reap(context.Background())
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	return rep
}

// name is a job's full resource name, which is what the report names (a
// person pastes it into gcloud).
func (rt *reapTest) name(id string) string {
	return "projects/" + testProject + "/locations/" + testRegion + "/jobs/" + id
}

func hasAction(rep Report, kind ActionKind, target string) bool {
	for _, a := range rep.Actions {
		if a.Kind == kind && a.Target == target {
			return true
		}
	}
	return false
}

func refused(rep Report, target string) bool {
	for _, f := range rep.Refused {
		if f.Target == target {
			return true
		}
	}
	return false
}

// refusedBecause is refused plus the reason: a refusal that names the wrong
// cause is a report an operator cannot act on, so the tests read it.
func refusedBecause(rep Report, target, reason string) bool {
	for _, f := range rep.Refused {
		if f.Target == target && strings.Contains(f.Reason, reason) {
			return true
		}
	}
	return false
}

// TestReaperDefaultsComeFromTheRunCeiling pins the two derived deadlines: they
// come from the longest a run may legitimately be active, which is the longest
// configured task timeout OR a run's own override ceiling, whichever is more
// (design §1.4's "the launcher's own task timeout", read as the launcher's
// longest), and the age floor adds the queue wait, the run and ReapMargin
// (§3.4).
//
// The ceiling half is the destructive one: --total-timeout lets a run ask for
// up to backend.MaxRunTime whatever the workflows are configured with, so a
// base that stopped at the configured longest would call a two-hour run stuck
// forty-seven minutes in and delete it (jobs.delete), one pass after it
// crossed -- the live run this whole file exists not to delete.
func TestReaperDefaultsComeFromTheRunCeiling(t *testing.T) {
	rt := newReaperTest(t, nil)
	// The harness's 30-minute workflow is SHORTER than the ceiling, so the
	// ceiling is what both deadlines are built from.
	queueDeadline := backend.MaxRunTime + backend.TaskTimeoutSlack
	if got := rt.r.QueueDeadline(); got != queueDeadline {
		t.Fatalf("QueueDeadline = %v, want %v", got, queueDeadline)
	}
	// The facts pass that lists runs by their mint time has to reach back over
	// this same wait, and it has no platform read to ask for it: its Batch
	// margin is this deadline's floor (backend.MintSkewMargin), and a run still
	// in the queue when its day is written is the one a shorter margin drops.
	if got := backend.MintSkewMargin(backend.CloudBatch); got != queueDeadline {
		t.Fatalf("backend.MintSkewMargin(CloudBatch) = %v, want the queue deadline %v", got, queueDeadline)
	}
	runBudget := queueDeadline + ReapMargin
	if got := rt.r.RunBudget(); got != runBudget {
		t.Fatalf("RunBudget = %v, want %v", got, runBudget)
	}
	if got, want := rt.r.MaxAge(), queueDeadline+runBudget; got != want {
		t.Fatalf("MaxAge = %v, want %v", got, want)
	}
	// A workflow longer than the ceiling is the base instead, so an
	// installation whose workflows are configured for longer than any override
	// is still waited out.
	long := backend.MaxRunTime + time.Hour
	rt = newReaperTest(t, func(o *ReaperOptions) { o.Timeouts = testTimeouts{task: long, longest: long} })
	if got, want := rt.r.QueueDeadline(), long+backend.TaskTimeoutSlack; got != want {
		t.Fatalf("QueueDeadline with a %v workflow = %v, want %v", long, got, want)
	}
	if got, want := rt.r.RunBudget(), long+backend.TaskTimeoutSlack+ReapMargin; got != want {
		t.Fatalf("RunBudget with a %v workflow = %v, want %v", long, got, want)
	}
	// An installation with no workflows at all answers zero, and must not
	// collapse to a two-minute deadline: the ceiling is what a run may still
	// ask for, whatever its workflows are configured with.
	rt = newReaperTest(t, func(o *ReaperOptions) { o.Timeouts = testTimeouts{} })
	if got := rt.r.QueueDeadline(); got != queueDeadline {
		t.Fatalf("QueueDeadline with no workflows = %v, want %v", got, queueDeadline)
	}
}

// TestReaperDecidesEveryBatchState is the decision table the review is about:
// what the reaper does with a job in each of the nine documented
// JobStatus.State values (design §2.3), old enough that no age filter saves
// it.
func TestReaperDecidesEveryBatchState(t *testing.T) {
	// The job's own terminal states: the record is history and Batch's own
	// teardown owns its VM, so nothing is done to any of them.
	for _, state := range []string{"SUCCEEDED", "FAILED", "CANCELLED"} {
		t.Run("keeps a "+state+" job", func(t *testing.T) {
			rt := newReaperTest(t, nil)
			id := rt.addJob(t, "20260927-100000-abcd", state, 10*time.Hour)
			rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
			rep := rt.reap(t)
			if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
				t.Fatalf("a %s job was touched: deletes=%v cancels=%v", state, rt.bf.Deletes(), rt.bf.Cancels())
			}
			if len(rep.Actions) != 1 || !hasAction(rep, ActionDeleteVM, "fake-region-a/vm-1") {
				t.Fatalf("Reap actions = %v, want only the leaked VM of %s", rep.Lines(), id)
			}
			// Its VM is another matter: the job is finished, Batch had its
			// chance, and the VM outlived every deadline.
			if got := rt.cf.Deletes(); len(got) != 1 || got[0] != "fake-region-a/vm-1" {
				t.Fatalf("the leaked VM of a %s job was not reaped: deletes=%v", state, got)
			}
		})
	}
	t.Run("keeps the VMs of a job being cancelled", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// A cancel goes CANCELLATION_IN_PROGRESS -> CANCELLED (§1.4), and
		// Batch is tearing the run's VMs down for as long as this state lasts.
		// Reading it as finished deletes the VM of a job Batch is mid-teardown
		// on -- the race §3.4 forbids -- so the run stays owned and the VM
		// survives to the pass after, which is when a finished job's VM is
		// reapable at all.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "CANCELLATION_IN_PROGRESS", rt.startedAfter(2*time.Minute), 2*time.Minute)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
			t.Fatalf("a cancellation in progress was touched: deletes=%v cancels=%v", rt.bf.Deletes(), rt.bf.Cancels())
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("instances.delete = %v, want none: Batch is still tearing the run down", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is CANCELLATION_IN_PROGRESS") {
			t.Fatalf("report = %v, want the VM refused as a cancelling job's", rep.Lines())
		}
		if len(rt.cf.Instances()) != 1 {
			t.Fatalf("instances = %v, want the VM still there for the next pass", rt.cf.Instances())
		}
		if got := rt.bf.State(id); got != "CANCELLATION_IN_PROGRESS" {
			t.Fatalf("job state = %q, want the job left exactly as Batch reported it", got)
		}
	})
	t.Run("reports a cancellation that has not finished, and does not delete it", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Past the run budget. The reaper still will not delete it: deleting a
		// cancelled job erases the record the run's ls, diagnose and watch
		// read (§1.4, C8). It says so instead, and leaves the VMs alone.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "CANCELLATION_IN_PROGRESS", rt.startedAfter(rt.stuck()), rt.stuck())
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 {
			t.Fatalf("jobs.delete = %v, want none: a cancelled run's record is history", rt.bf.Deletes())
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("instances.delete = %v, want none", got)
		}
		stuckReport := fmt.Sprintf("a cancellation that has not finished in %.0f minutes", rt.stuck().Minutes())
		if !refusedBecause(rep, rt.name(id), stuckReport) {
			t.Fatalf("report = %v, want a refusal naming the stuck cancellation", rep.Lines())
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is CANCELLATION_IN_PROGRESS") {
			t.Fatalf("report = %v, want the VM refused with it", rep.Lines())
		}
	})
	t.Run("deletes a stuck SCHEDULED job", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Created 34 hours ago and SCHEDULED for 10: it left the queue past the
		// 32-minute deadline, so its SCHEDULED event postdates the moment the
		// queue wait ended and is the transition this state began with -- the
		// measurement the branch may act on.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "SCHEDULED", rt.startedAfter(rt.stuck()), rt.stuck())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
			t.Fatalf("jobs.delete = %v, want [%s]", got, id)
		}
		if !hasAction(rep, ActionDeleteJob, rt.name(id)) {
			t.Fatalf("report = %v, want a delete of %s", rep.Lines(), id)
		}
	})
	t.Run("deletes a stuck RUNNING job", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
		if len(rt.reap(t).Actions) != 1 || len(rt.bf.Deletes()) != 1 {
			t.Fatalf("a hung RUNNING job past every deadline was not deleted: deletes=%v", rt.bf.Deletes())
		}
		if got := rt.bf.Deletes()[0]; got != id {
			t.Fatalf("jobs.delete = %s, want %s", got, id)
		}
	})
	t.Run("deletes a stuck DELETION_IN_PROGRESS job", func(t *testing.T) {
		// The residual §3.4 names: a deletion that never finished, on a job
		// whose queue wait was over long before the deletion's own event.
		rt := newReaperTest(t, nil)
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", rt.startedAfter(rt.stuck()), rt.stuck())
		rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
			t.Fatalf("jobs.delete = %v, want [%s]", got, id)
		}
	})
	t.Run("refuses an unknown state", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "SUCCEEDING", 10*time.Hour)
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
			t.Fatalf("a job whose state this build does not know was acted on: %v", rep.Lines())
		}
		if !refused(rep, rt.name(id)) {
			t.Fatalf("report = %v, want a refusal of %s", rep.Lines(), id)
		}
	})
	t.Run("refuses STATE_UNSPECIFIED", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// STATE_UNSPECIFIED is a state this build knows as pending, and it is
		// not QUEUED: nothing says the job has been accepted, so nothing is
		// done with it.
		id := rt.addJob(t, "20260927-100000-abcd", "STATE_UNSPECIFIED", 10*time.Hour)
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
			t.Fatalf("a STATE_UNSPECIFIED job was acted on: %v", rep.Lines())
		}
		if !refused(rep, rt.name(id)) {
			t.Fatalf("report = %v, want a refusal of %s", rep.Lines(), id)
		}
	})
}

// TestReaperQueueDeadline: a job that has sat in QUEUED past the deadline is
// DELETED -- the queue deadline's verb since jobs.cancel turned out not to be
// a permission Google has, and the one case where the delete is affordable:
// a job that never left QUEUED never ran a task, so it has no result.json and
// no VM, and its run's launch.json is the launcher's own write to the runs
// bucket (cli/run.go), which the delete leaves in place -- which is what ls,
// diagnose and watch read for it (design §1.4) -- and one within the deadline
// is left alone.
func TestReaperQueueDeadline(t *testing.T) {
	rt := newReaperTest(t, nil)
	// 40 minutes: past the 32-minute queue deadline, on a 30-minute workflow.
	old := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	// 10 minutes: still within it.
	young := rt.addJob(t, "20260927-100001-abcd", "QUEUED", 10*time.Minute)
	// Both jobs' VMs, old enough that only the job behind each stands between
	// it and the reaper: one job is still waiting for capacity, the other is
	// being deleted, and neither frees its VM in this pass.
	rt.vm("vm-young", runLabels(t, "20260927-100001-abcd"), rt.old())
	rt.vm("vm-cancelled", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != old {
		t.Fatalf("jobs.delete = %v, want [%s]", got, old)
	}
	if got := rt.bf.Cancels(); len(got) != 0 {
		t.Fatalf("the queue deadline cancelled a job (%v): jobs.cancel is not a permission the reaper's role can carry", got)
	}
	if !hasAction(rep, ActionDeleteJob, rt.name(old)) {
		t.Fatalf("report = %v, want a delete of %s", rep.Lines(), old)
	}
	// The job's record is gone, not assumed gone: a job that never left QUEUED
	// has no result.json and no VM to lose (it never ran a task), so the job's
	// own record is all the delete erases -- the run's launch.json is the
	// launcher's object in the runs bucket, which is why ls, diagnose and
	// watch keep showing the run (design §1.4). This is the assertion that
	// says so rather than the comment above it.
	if got := rt.bf.IDs(); len(got) != 1 || got[0] != JobID(backendtest.Slug, backendtest.Workflow, "20260927-100001-abcd") {
		t.Fatalf("jobs = %v, want the deleted job's record gone and the young one's left", got)
	}
	if strings.Contains(strings.Join(rep.Lines(), "\n"), young) {
		t.Fatalf("a job still within the queue deadline was reported: %v", rep.Lines())
	}
	// Neither job's VM was the reaper's in that pass: the young one is still
	// Batch's, and the deleted one's VM is Batch's teardown, which the delete
	// must not race.
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("a queued job's VM was deleted: %v", got)
	}
	if !refusedBecause(rep, "fake-region-a/vm-young", "still within the queue deadline") {
		t.Fatalf("report = %v, want the queued job's VM refused", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-cancelled", "deleted by this pass for the queue deadline") {
		t.Fatalf("report = %v, want the deleted job's VM refused", rep.Lines())
	}
}

// TestReaperKeepsTheVMsOfAJobBeingDeleted: a job already in
// DELETION_IN_PROGRESS and within every deadline is being deleted by someone,
// and Batch is taking its VMs down with it. The reaper must not race that --
// only once the deletion itself is stuck past the age floor does it act, and
// then the job goes first.
func TestReaperKeepsTheVMsOfAJobBeingDeleted(t *testing.T) {
	rt := newReaperTest(t, nil)
	// Created 34 hours ago, in DELETION_IN_PROGRESS for 10 minutes: the
	// deletion's own event postdates the queue deadline, so the 10 minutes are
	// the deletion's and the job is within the run budget -- which is what this
	// test is about. (A job whose state event does NOT postdate the queue
	// deadline is refused rather than read as a fresh deletion; that is
	// TestReaperRefusesAStaleStateEvent.)
	rt.addJobStateAt(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 34*time.Hour, 10*time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep := rt.reap(t)
	if len(rt.bf.Deletes()) != 0 {
		t.Fatalf("a job whose deletion is under way was deleted again: %v", rt.bf.Deletes())
	}
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("the reaper deleted a VM out from under a deletion in progress: %v", got)
	}
	if !refusedBecause(rep, "fake-region-a/vm-1", "DELETION_IN_PROGRESS") {
		t.Fatalf("report = %v, want the VM refused and named as the deleting job's", rep.Lines())
	}
}

// TestReaperRefusesAStaleStateEvent: SCHEDULED and DELETION_IN_PROGRESS have
// no second measurement of their time in state -- the latest job-level status
// event is all there is, and nothing in the API reference says such an event is
// emitted on every transition (UNVERIFIED, design §3.4). So the event may date
// the job's QUEUE WAIT, which sits at its creation time: a job that has just
// been scheduled, or just had its deletion started, after a long queue wait
// would then read as hours into its state and be deleted -- a live run deleted,
// by exactly the uncertainty RUNNING refuses to act on. The one thing that can
// be checked about an event whose state it does not name is whether it
// postdates the creation time plus the queue deadline; when it does not, the
// job is refused, and only then.
func TestReaperRefusesAStaleStateEvent(t *testing.T) {
	for _, state := range []string{"SCHEDULED", "DELETION_IN_PROGRESS"} {
		t.Run("a "+state+" job whose only event dates its queue wait", func(t *testing.T) {
			rt := newReaperTest(t, nil)
			// Created 34 hours ago and in this state for a second, with the
			// event the harness wrote at the state change... and then moved
			// back to the creation time, which is the shape the event list
			// cannot rule out: the latest job-level event a job carries may be
			// its QUEUED transition.
			id := rt.addJobStateAt(t, "20260927-100000-abcd", state, 34*time.Hour, time.Second)
			rt.bf.SetStatusEvents(id, map[string]any{
				"type": "STATUS_CHANGED", "description": "Job state is QUEUED",
				"eventTime": rt.now.Add(-34 * time.Hour).Format(time.RFC3339Nano),
			})
			rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
			rep := rt.reap(t)
			if got := rt.bf.Deletes(); len(got) != 0 {
				t.Fatalf("a %s job was deleted on an event that may date its queue wait: %v", state, got)
			}
			if got := rt.bf.Cancels(); len(got) != 0 {
				t.Fatalf("a %s job was cancelled on that event: %v", state, got)
			}
			if got := rt.cf.Deletes(); len(got) != 0 {
				t.Fatalf("the VM of a %s job was deleted: %v", state, got)
			}
			if !refusedBecause(rep, rt.name(id), "may date the queue wait rather than the "+state+" state") {
				t.Fatalf("report = %v, want the job refused for the stale event", rep.Lines())
			}
			if !refusedBecause(rep, "fake-region-a/vm-1", "may date the queue wait rather than the "+state+" state") {
				t.Fatalf("report = %v, want its VM refused with it", rep.Lines())
			}
			if got := rt.bf.State(id); got != state {
				t.Fatalf("job state = %q, want it left as it is", got)
			}
		})
	}
	// CANCELLATION_IN_PROGRESS is the third branch that reads a state event, and
	// the one where nothing destructive rests on the measurement -- it is never
	// deleted, only reported. It needs the guard all the same: the report is the
	// audit trail, and the latest event of a job cancelled moments after a long
	// queue wait may date that wait, which would name "a cancellation that has
	// not finished in 26 hours" for a cancellation just asked for. A refusal is
	// the line an operator can act on; a wrong measurement is not.
	t.Run("a just-cancelled job is not reported as a stuck cancellation", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "CANCELLATION_IN_PROGRESS", 34*time.Hour, time.Second)
		rt.bf.SetStatusEvents(id, map[string]any{
			"type": "STATUS_CHANGED", "description": "Job state is QUEUED",
			"eventTime": rt.now.Add(-34 * time.Hour).Format(time.RFC3339Nano),
		})
		rep := rt.reap(t)
		if !refusedBecause(rep, rt.name(id), "may date the queue wait rather than the CANCELLATION_IN_PROGRESS state") {
			t.Fatalf("report = %v, want the job refused for the stale event", rep.Lines())
		}
		for _, l := range rep.Lines() {
			if strings.Contains(l, "a cancellation that has not finished") {
				t.Fatalf("report = %v, want no stuck-cancellation line: the event dates the queue wait, not the state", rep.Lines())
			}
		}
	})
	// And the other side of the bound: an event that DOES postdate the queue
	// deadline is acted on, so the guard is a narrowing of what is measured, not
	// a branch that never deletes. (SCHEDULED is the state whose event is the
	// only measurement; RUNNING's is corrected by runDuration, which its own
	// tests cover.)
	t.Run("an event that postdates the queue deadline is acted on", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "SCHEDULED", rt.startedAfter(rt.stuck()), rt.stuck())
		rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
			t.Fatalf("jobs.delete = %v, want [%s]: its SCHEDULED event is the transition it began with", got, id)
		}
	})
}

// TestReaperQueueDeadlineNeedsACreateTime: a job past the queue
// deadline with no readable create time is refused, not cancelled -- the
// deadline cannot be judged.
func TestReaperQueueDeadlineNeedsACreateTime(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	rt.bf.SetCreated(id, time.Time{})
	rep := rt.reap(t)
	if len(rt.bf.Cancels()) != 0 || len(rt.bf.Deletes()) != 0 {
		t.Fatalf("a job with no create time was acted on: %v", rep.Lines())
	}
	if !refused(rep, rt.name(id)) {
		t.Fatalf("report = %v, want a refusal of %s", rep.Lines(), id)
	}
}

// TestReaperKeepsALiveJobAndItsVM: a run that is running now is not the
// reaper's, and neither is its VM -- Batch owns that one (design §3.4).
func TestReaperKeepsALiveJobAndItsVM(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Minute)
	// The VM is old enough that the age floor does not save it: the only
	// thing standing between it and the reaper is the live job behind it.
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep := rt.reap(t)
	if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
		t.Fatalf("a live job was acted on: %v", rep.Lines())
	}
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("the VM of a live job was deleted: %v", got)
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-1", "its job is RUNNING, 10 minutes into the state: Batch owns its teardown") {
		t.Fatalf("report = %v, want the live job's VM refused and named as the live job's", rep.Lines())
	}
}

// TestReaperRefusesTheVMsOfAJobItRefused is the other half of every refusal
// in reapJob: a job the pass declines to act on is still in the region and may
// still be RUNNING, so its VMs are not the reaper's either. Each subtest puts
// an old VM (past the age floor, so nothing else saves it) behind a job the
// pass refuses, and the VM must survive the pass.
func TestReaperRefusesTheVMsOfAJobItRefused(t *testing.T) {
	const run = "20260927-100000-abcd"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, rt *reapTest)
		why   string
	}{
		{
			name: "unknown Batch state",
			setup: func(t *testing.T, rt *reapTest) {
				rt.addJob(t, run, "SUCCEEDING", 10*time.Hour)
			},
			why: "does not know",
		},
		{
			name: "STATE_UNSPECIFIED",
			setup: func(t *testing.T, rt *reapTest) {
				rt.addJob(t, run, "STATE_UNSPECIFIED", 10*time.Hour)
			},
			why: "does not know",
		},
		{
			name: "unreadable create time",
			setup: func(t *testing.T, rt *reapTest) {
				id := rt.addJob(t, run, "RUNNING", 10*time.Hour)
				rt.bf.SetCreated(id, time.Time{})
			},
			why: "create time is unreadable",
		},
		{
			name: "label fields that disagree",
			setup: func(t *testing.T, rt *reapTest) {
				// The same run label in both fields would be attributable; a
				// job whose two fields name different runs is the ambiguity
				// reapJob refuses, and the VM carries one of the two.
				rt.addJobDisagreeingRunLabels(t, run, "RUNNING", 10*time.Hour)
			},
			// The whole clause, not one word of it: the refusal covers a
			// missing label and a malformed one as well as two fields that
			// disagree, so the test reads the part this case is.
			why: "not the same in both label fields",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newReaperTest(t, nil)
			tc.setup(t, rt)
			rt.vm("vm-1", runLabels(t, run), rt.old())
			rep := rt.reap(t)
			if got := rt.cf.Deletes(); len(got) != 0 {
				t.Fatalf("the VM of a job this pass refused was deleted: %v", got)
			}
			if len(rt.cf.Instances()) != 1 {
				t.Fatalf("instances = %v, want the VM left alone", rt.cf.Instances())
			}
			if !refusedBecause(rep, "fake-region-a/vm-1", tc.why) {
				t.Fatalf("report = %v, want the VM refused because of the job behind it (%q)", rep.Lines(), tc.why)
			}
		})
	}
}

// TestReaperDeletesALeakedVMWhoseJobIsGone is the case the reaper exists for:
// a VM carrying the installation's marker and a run label, past every
// deadline, with no job behind it.
func TestReaperDeletesALeakedVMWhoseJobIsGone(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
	rt.vm("vm-2", runLabels(t, "20260927-100001-abcd"), rt.old())
	rep := rt.reap(t)
	got := rt.cf.Deletes()
	if len(got) != 2 || got[0] != "fake-region-a/vm-1" || got[1] != "fake-region-a/vm-2" {
		t.Fatalf("instances.delete = %v, want both leaked VMs", got)
	}
	if len(rep.Refused) != 0 {
		t.Fatalf("refusals = %v, want none", rep.Lines())
	}
}

// TestReaperRefusesAVMWithNoRunLabel: the per-run label is the only thing
// that attributes a VM to a run. Without it the reaper will not guess (design
// §2.4, §3.4).
func TestReaperRefusesAVMWithNoRunLabel(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.vm("vm-1", map[string]string{labelManaged: managedValue}, rt.old())
	rep := rt.reap(t)
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("a VM with no %s label was deleted: %v", RunLabel, got)
	}
	if !refused(rep, "fake-region-a/vm-1") {
		t.Fatalf("report = %v, want a refusal of the unlabelled VM", rep.Lines())
	}
	if len(rt.cf.Instances()) != 1 {
		t.Fatalf("the VM is gone: %v", rt.cf.Instances())
	}
}

// TestReaperRefusesAnotherInstallationsVM is the client-side label check: the
// server-side filter should never have returned this VM, and the reaper
// refuses it all the same, because whether the API honours a filter or
// ignores it the answer must be the same (the shape List already has).
func TestReaperRefusesAnotherInstallationsVM(t *testing.T) {
	rt := newReaperTest(t, func(o *ReaperOptions) {})
	rt.cf.IgnoreFilter = true
	labels := runLabels(t, "20260927-100000-abcd")
	labels[labelManaged] = "someone-else"
	rt.vm("vm-foreign", labels, rt.old())
	rep := rt.reap(t)
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("another installation's VM was deleted: %v", got)
	}
	if !refused(rep, "fake-region-a/vm-foreign") {
		t.Fatalf("report = %v, want a refusal naming the foreign VM", rep.Lines())
	}
}

// TestReaperRefusesAVMOutsideItsRegion: jobs.list is regional and the
// aggregated instance listing is not -- it is project-wide -- so the pass
// holds job facts for one region and instance facts for the whole project. A
// VM of another region belongs to jobs this pass never read, and deciding it
// from the instance listing alone is exactly what §3.4 forbids (the reaper is
// job-first). It is refused, not deleted.
func TestReaperRefusesAVMOutsideItsRegion(t *testing.T) {
	rt := newReaperTest(t, nil)
	// Another region's zone, same installation, the run label of a run this
	// pass found no job for: every check the reaper makes except the region.
	rt.cf.AddInstanceCreated("other-region-a", "vm-elsewhere", runLabels(t, "20260927-100000-abcd"), rt.now.Add(-rt.old()))
	// And one of its own, which must still be reaped: the region check must
	// refuse the other region, not stop the pass.
	rt.vm("vm-here", runLabels(t, "20260927-100001-abcd"), rt.old())
	rep := rt.reap(t)
	if got := rt.cf.Deletes(); len(got) != 1 || got[0] != "fake-region-a/vm-here" {
		t.Fatalf("instances.delete = %v, want only this region's VM", got)
	}
	if !refused(rep, "other-region-a/vm-elsewhere") {
		t.Fatalf("report = %v, want a refusal naming the other region's VM", rep.Lines())
	}
	if got := rt.cf.Instances(); len(got) != 1 || got[0] != "other-region-a/vm-elsewhere" {
		t.Fatalf("instances = %v, want the other region's VM to survive", got)
	}
}

// TestReaperRefusesAPartialJobListing: jobs.list reports the locations it
// could not reach (ListJobsResponse.Unreachable). A pass that reads the jobs
// it did get as all the jobs there are reads absence as fact -- and absence is
// what lets the instance pass delete a leaked VM -- so a partial listing ends
// the pass before anything is deleted, with an error rather than a report.
func TestReaperRefusesAPartialJobListing(t *testing.T) {
	rt := newReaperTest(t, nil)
	stuck := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), rt.old())
	rt.bf.Unreachable = []string{"fake-region-b"}
	_, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap on a listing the API could not complete: want an error")
	}
	if !strings.Contains(err.Error(), "fake-region-b") {
		t.Fatalf("error = %v, want it to name the unreachable location", err)
	}
	if got := rt.bf.Deletes(); len(got) != 0 {
		t.Fatalf("jobs.delete = %v on a partial listing, want none", got)
	}
	if st := rt.bf.State(stuck); st != "RUNNING" {
		t.Fatalf("the stuck job's state is %q, want RUNNING: nothing may be deleted from a partial listing", st)
	}
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("instances.delete = %v on a partial listing, want none: the missing jobs are the ones that would have owned these VMs", got)
	}
}

// TestReaperRefusesAJobListedOutsideItsLocation: the reaper asks one location
// for its jobs, and the names those jobs are acted on by are the ones it
// derived itself (jobNameFor) -- jobs.get (deleteQueued's read-back) and
// jobs.delete take j.Name as the pass set it, and jobs.delete is the
// destructive verb of the five in this file's header. A listing that answers a
// job whose name cannot be placed in this installation's location is therefore
// not the listing this pass asked for: the runs the job claims would be the
// one thing the job pass could not hand to the instance pass -- a job it never
// attributed is a run whose VMs it never owned, which is exactly what the
// instance pass deletes. So the pass ends before anything is deleted, with an
// error rather than a report, as it does on a partial listing.
//
// The fake's ProjectNumber knob is what makes the API answer with a name that
// is not this installation's: it is documented as "replaces the project ID in
// every name the fake returns, as the real API may", and here it is set to
// another project's ID rather than to a number -- a rendering the reaper
// cannot read as its own project, and the one shape it must refuse. That a
// NUMBER is accepted (and rewritten) is the other half,
// TestReaperAcceptsTheProjectNumberSpelling; the region arm of the same check
// is pinned by TestJobNameForKeepsEveryNameInThisInstallationsLocation, which
// no fake can reach (a name in another region is a name the fake never
// renders).
//
// The error is also read for what it must NOT carry: the name is the one value
// this pass must not act on, and a message that printed it would read as a job
// to go and fix rather than as a listing that is not the one that was asked
// for.
//
// Mutation (drop the check): the pass deletes the stuck job and the leaked VM
// and returns no error, so the first and last assertions fail together.
func TestReaperRefusesAJobListedOutsideItsLocation(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.bf.ProjectNumber = "another-project"
	stuck := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), rt.old())
	_, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap on a listing that answers a job of another location: want an error")
	}
	if want := rt.r.location(); !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to name the location this pass asked about (%s)", err, want)
	}
	if strings.Contains(err.Error(), "another-project") {
		t.Fatalf("error = %v, want no value from the listing: the name is the thing this pass must not act on", err)
	}
	if got := rt.bf.Deletes(); len(got) != 0 {
		t.Fatalf("jobs.delete = %v on a listing from outside the location, want none", got)
	}
	if st := rt.bf.State(stuck); st != "RUNNING" {
		t.Fatalf("the stuck job's state is %q, want RUNNING: nothing may be deleted from a listing that is not the one this pass asked for", st)
	}
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("instances.delete = %v on a listing from outside the location, want none: a job this pass never placed is a run it never owned", got)
	}
}

// TestReaperAcceptsTheProjectNumberSpelling is the boundary of the check
// above, and the reason it is not "the name must start with the project ID":
// Batch answers a job's name with the project NUMBER (design §2.3 -- the
// backend's own canonical rewrites it back, and TestExecutionRewritesTheProjectNumber
// pins the create-time half of it), so a check that refused the number would
// refuse every listing from an API that spells the project that way, and the
// reaper -- a safety net that must run unattended -- would reap nothing at all
// and say so on every pass.
//
// The number is not merely tolerated, it is replaced: the name the pass acts
// on is the one jobNameFor derived from the configured project, which is what
// the action's target has to show. A pass that accepted the number and then
// acted on the listing's own spelling would be sending jobs.delete to a name
// whose project segment it never checked against anything -- the hole this
// test's sibling refuses on the other side of the boundary.
//
// Mutation (compare the project segment to the configured ID exactly): this
// fails, with the pass refusing its own listing. Mutation (accept the number
// and keep the listing's spelling): the target assertion fails, naming
// projects/123456789/... .
func TestReaperAcceptsTheProjectNumberSpelling(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.bf.ProjectNumber = "123456789"
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rep := rt.reap(t)
	if !hasAction(rep, ActionDeleteJob, rt.name(id)) {
		t.Fatalf("actions = %v, want the stuck job deleted as %s: the pass acts on the name it derived from its own project, not on the listing's spelling", rep.Actions, rt.name(id))
	}
	for _, a := range rep.Actions {
		if strings.Contains(a.Target, "123456789") {
			t.Fatalf("action target = %q, want no name carrying the project number: every name this pass acts on is in the project it was configured with", a.Target)
		}
	}
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
		t.Fatalf("jobs.delete = %v, want exactly [%s]", got, id)
	}
}

// TestJobNameForKeepsEveryNameInThisInstallationsLocation pins the check
// itself, arm by arm: every name this pass acts on comes from jobNameFor, and
// what it returns is either a name in this installation's own location or
// nothing at all.
//
// It is a direct test because the other arm cannot be reached through the
// fakes: gcpfake.Batch renders the project (ProjectNumber, which
// TestReaperRefusesAJobListedOutsideItsLocation uses) but never the region --
// it stores a job under the region its create named and answers a get, cancel,
// delete or list naming another region with NOT_FOUND, because that is what
// the API does. A listing that answers a name in another region is exactly the
// server the reaper cannot trust, and no knob can produce it; the arm is still
// the difference between a name the reaper acts on and a delete sent to
// another region, so it is pinned here.
//
// The region is compared and never rewritten, the project ID is kept, the
// number is replaced by the configured ID, and everything else -- another
// project's ID, a malformed name, a part that is not [a-z0-9-] -- is refused.
func TestJobNameForKeepsEveryNameInThisInstallationsLocation(t *testing.T) {
	rt := newReaperTest(t, nil)
	const job = "20260927-100000-abcd"
	own := rt.name(job)
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"this installation's own project ID", own, own},
		{"the project number, which Batch answers a created job with", "projects/123456789/locations/" + testRegion + "/jobs/" + job, own},
		{"another project's ID", "projects/another-project/locations/" + testRegion + "/jobs/" + job, ""},
		{"another region", "projects/" + testProject + "/locations/fake-region-b/jobs/" + job, ""},
		{"another project's number in another region", "projects/987654321/locations/fake-region-b/jobs/" + job, ""},
		{"a name that is not a Batch job name", "projects/" + testProject + "/locations/" + testRegion + "/jobs", ""},
		{"another service's resource", "projects/" + testProject + "/locations/" + testRegion + "/executions/" + job, ""},
		{"a part outside [a-z0-9-]", "projects/" + testProject + "/locations/" + testRegion + "/jobs/2026_0927", ""},
		{"an empty job id", "projects/" + testProject + "/locations/" + testRegion + "/jobs/", ""},
		{"an empty project", "projects//locations/" + testRegion + "/jobs/" + job, ""},
		{"an empty region", "projects/" + testProject + "/locations//jobs/" + job, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rt.r.jobNameFor(tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("jobNameFor(%q) = %q, want no name: this pass may only act inside %s", tc.in, got, rt.r.location())
				}
				return
			}
			if !ok {
				t.Fatalf("jobNameFor(%q) = no name, want %q", tc.in, tc.want)
			}
			if got != tc.want {
				t.Fatalf("jobNameFor(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !strings.HasPrefix(got, rt.r.location()+"/jobs/") {
				t.Fatalf("jobNameFor(%q) = %q, want a name under %s/jobs/", tc.in, got, rt.r.location())
			}
		})
	}
}

// TestReaperJudgesStucknessByTimeInState is the destructive case the age of a
// job cannot answer: a run may sit in the queue far longer than the queue
// deadline -- the deadline is the previous pass's, and a reaper that was not
// running enforced nothing -- and then start and run legitimately. Judged by
// its age since creation, that run is "stuck" the second it starts; judged by
// the time it has spent in the state it reports, it is a live run.
func TestReaperJudgesStucknessByTimeInState(t *testing.T) {
	t.Run("a job that queued for hours and just started", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Created 34 hours ago (past the queue deadline AND past MaxAge), in
		// the RUNNING state for 20 seconds.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "RUNNING", 34*time.Hour, 20*time.Second)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a job that started 20 seconds ago was deleted for its queue wait: %v", got)
		}
		if got := rt.bf.Cancels(); len(got) != 0 {
			t.Fatalf("a job that started 20 seconds ago was cancelled: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of a just-started run was deleted: %v", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is RUNNING, 0 minutes into the state") {
			t.Fatalf("report = %v, want the young run's VM refused as a live job's", rep.Lines())
		}
		_ = id
	})
	t.Run("a stale job-level event beside a fresh runDuration", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The event list is not a per-state log: nothing in the API reference
		// says a job-level event is emitted on every transition, so the latest
		// one a RUNNING job carries can be its QUEUED transition ten hours
		// ago. Read as the time in state, that is a live run deleted;
		// runDuration is the second measurement of the same quantity, and the
		// shorter of the two is what a deadline may act on.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour, 10*time.Hour)
		rt.bf.SetStatusEvents(id, map[string]any{
			"type": "STATUS_CHANGED", "description": "Job state is QUEUED",
			"state": "QUEUED", "eventTime": rt.now.Add(-10 * time.Hour).Format(time.RFC3339Nano),
		})
		rt.bf.SetRunDuration(id, "30s")
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a job that started 30 seconds ago was deleted for a stale event: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of a just-started run was deleted: %v", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is RUNNING, 0 minutes into the state") {
			t.Fatalf("report = %v, want the young run's VM refused as a live job's", rep.Lines())
		}
	})
	t.Run("a deletion just started on an old job", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Batch's own 60-day job delete (design §9) puts an old job into
		// DELETION_IN_PROGRESS moments before the pass reads it.
		rt.addJobStateAt(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 60*24*time.Hour, 2*time.Minute)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a deletion that started two minutes ago was deleted again: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of a job being deleted was deleted out from under it: %v", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "DELETION_IN_PROGRESS") {
			t.Fatalf("report = %v, want the VM refused as a deleting job's", rep.Lines())
		}
	})
	t.Run("a SCHEDULED job that waited out its queue deadline", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Created 34 hours ago -- past the 32-minute queue deadline AND past
		// MaxAge, so an age rule would delete it -- and SCHEDULED for a second.
		// The run must read as live, and (this subtest's own point) the pass
		// has to actually run to say so: without rt.reap there is nothing to
		// observe and a SCHEDULED branch that measured from the creation time
		// would go unnoticed.
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "SCHEDULED", rt.old(), time.Second)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a just-scheduled job was deleted: %v", got)
		}
		if got := rt.bf.Cancels(); len(got) != 0 {
			t.Fatalf("a just-scheduled job was cancelled: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of a just-scheduled run was deleted: %v", got)
		}
		if got := rt.bf.State(id); got != "SCHEDULED" {
			t.Fatalf("job state = %q, want the just-scheduled job left as it is", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is SCHEDULED, 0 minutes into the state") {
			t.Fatalf("report = %v, want the VM refused as a live job's", rep.Lines())
		}
	})
}

// TestReaperRefusesAJobWhoseTimeInStateIsUnknown: the time in state is what a
// stuckness decision rests on, so a job that reports no job-level event time
// (and, for RUNNING, no runDuration) is not stuck -- it is refused, and its VMs
// are refused with it. The RunDuration Batch reports for a RUNNING job is the
// fallback that reads it.
func TestReaperRefusesAJobWhoseTimeInStateIsUnknown(t *testing.T) {
	t.Run("no event time, no runDuration", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
		rt.bf.SetStatusEvents(id, map[string]any{"type": "STATUS_CHANGED", "description": "no time on this one"})
		// The harness models the runDuration a RUNNING job reports; this shape
		// is the one where it reports none.
		rt.bf.SetRunDuration(id, "")
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a job whose time in state is unknown was deleted: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of that job was deleted: %v", got)
		}
		if !refused(rep, rt.name(id)) {
			t.Fatalf("report = %v, want the job refused", rep.Lines())
		}
		if !refusedBecause(rep, rt.name(id), "no readable time for the RUNNING state") {
			t.Fatalf("report = %v, want the refusal to name the missing time", rep.Lines())
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "no readable time for the state") {
			t.Fatalf("report = %v, want its VM refused with the same reason", rep.Lines())
		}
	})
	t.Run("a stale event and no runDuration", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The shape the "shorter of the two measurements" guard exists for: a
		// RUNNING job whose latest job-level event is the QUEUED transition it
		// passed through hours ago (nothing in the API says a job-level event
		// is emitted per state change). Delete on that reading is delete on a
		// live run -- and without a runDuration there is no second measurement
		// to correct it, so the time in state is unknown and the job is
		// refused. Guards that apply only when the runDuration happens to be
		// there leave exactly this shape acting on the stale event.
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
		rt.bf.SetStatusEvents(id, map[string]any{
			"type": "STATUS_CHANGED", "description": "job state changed to QUEUED",
			"eventTime": rt.now.Add(-10 * time.Hour).Format(time.RFC3339Nano),
		})
		rt.bf.SetRunDuration(id, "")
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a RUNNING job was deleted on a job-level event that may date its queue wait: %v", got)
		}
		if got := rt.bf.Cancels(); len(got) != 0 {
			t.Fatalf("a RUNNING job was cancelled on that event: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of a RUNNING job was deleted: %v", got)
		}
		if !refusedBecause(rep, rt.name(id), "no readable time for the RUNNING state") {
			t.Fatalf("report = %v, want the refusal to name the missing time", rep.Lines())
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "no readable time for the state") {
			t.Fatalf("report = %v, want its VM refused with the same reason", rep.Lines())
		}
	})
	t.Run("a deletion whose time in state is unknown", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The same posture in the other state a delete decision rests on: a
		// deletion already under way, with nothing that dates it. The job is
		// old, so an age rule would call it stuck; the reaper refuses it
		// instead, and leaves its VM alone.
		id := rt.addJob(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 60*24*time.Hour)
		rt.bf.SetStatusEvents(id)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a deletion with no readable time in state was deleted again: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of that job was deleted: %v", got)
		}
		if !refusedBecause(rep, rt.name(id), "no readable time for the DELETION_IN_PROGRESS state") {
			t.Fatalf("report = %v, want the refusal to name the missing time", rep.Lines())
		}
	})
	t.Run("a cancellation whose time in state is unknown", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The third state whose branch reads the time in state. The job is old
		// enough that an age rule would call it stuck, and there is nothing to
		// date the state it reports: refused, not reported as a stuck
		// cancellation, and its VM left alone as always.
		id := rt.addJob(t, "20260927-100000-abcd", "CANCELLATION_IN_PROGRESS", 60*24*time.Hour)
		rt.bf.SetStatusEvents(id)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a job whose time in state is unknown was deleted: %v", got)
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of that job was deleted: %v", got)
		}
		if !refusedBecause(rep, rt.name(id), "no readable time for the CANCELLATION_IN_PROGRESS state") {
			t.Fatalf("report = %v, want the refusal to name the missing time", rep.Lines())
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "no readable time for the state") {
			t.Fatalf("report = %v, want its VM refused with the same reason", rep.Lines())
		}
	})
	t.Run("runDuration is the fallback", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
		rt.bf.SetStatusEvents(id)
		rt.bf.SetRunDuration(id, rt.stuck().String())
		rep := rt.reap(t)
		if got := rep.Actions; len(got) != 1 || got[0].Kind != ActionDeleteJob {
			t.Fatalf("actions = %v, want the job deleted: runDuration says it has been running past the run budget", rep.Lines())
		}
	})
	t.Run("a task-level event is not when the job changed state", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
		// Only a task-level event, and it is recent: the job has been RUNNING
		// past the run budget whatever a task inside it did a minute ago.
		rt.bf.SetStatusEvents(id, map[string]any{
			"type": "TASK_STATE_CHANGED", "taskState": "RUNNING",
			"eventTime": rt.now.Add(-time.Minute).Format(time.RFC3339Nano),
		})
		rt.bf.SetRunDuration(id, rt.stuck().String())
		rep := rt.reap(t)
		if got := rep.Actions; len(got) != 1 || got[0].Kind != ActionDeleteJob {
			t.Fatalf("actions = %v, want the job deleted by its runDuration", rep.Lines())
		}
	})
}

// TestReaperKeepsAYoungVM: Batch's own teardown takes seconds (Check 57a: 3
// to 26 of them), so a VM with no live job that appeared moments ago is a
// teardown in progress far more often than it is a leak -- deleting it would
// race Batch (§3.4).
func TestReaperKeepsAYoungVM(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 30*time.Second)
	rep := rt.reap(t)
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("a VM 30 seconds old was deleted: %v", got)
	}
	if !refused(rep, "fake-region-a/vm-1") {
		t.Fatalf("report = %v, want the young VM refused and named", rep.Lines())
	}
}

// TestReaperRefusesAVMWhoseAgeItCannotRead: the age floor is the last thing
// between a live run's VM and a delete, so a VM whose creationTimestamp the
// pass cannot read -- an empty one (which the API answers as no timestamp, and
// the parser reads as the zero time) or one that is not a time at all -- is
// refused rather than deleted: an unknown age is not an age past the floor. It
// is the same fail-closed posture as an unreadable job createTime, and the
// refusal says which, because the report is the audit trail.
func TestReaperRefusesAVMWhoseAgeItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant func(t *testing.T, rt *reapTest)
	}{
		{"an empty creationTimestamp", func(t *testing.T, rt *reapTest) {
			rt.cf.AddInstanceCreated("fake-region-a", "vm-1", runLabels(t, "20260927-100000-abcd"), time.Time{})
		}},
		{"a creationTimestamp that is not a time", func(t *testing.T, rt *reapTest) {
			rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
			rt.cf.SetInstanceCreatedRaw("fake-region-a", "vm-1", "yesterday")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := newReaperTest(t, nil)
			c.plant(t, rt)
			rep := rt.reap(t)
			if got := rt.cf.Deletes(); len(got) != 0 {
				t.Fatalf("a VM whose age is unknown was deleted: %v", got)
			}
			if !refusedBecause(rep, "fake-region-a/vm-1", "its creation time is unreadable") {
				t.Fatalf("report = %v, want the refusal to name the unreadable creation time", rep.Lines())
			}
		})
	}
}

// TestReaperDeletesTheJobBeforeTheInstance is the ordering rule of §3.4: a
// stuck job is deleted in the job pass, and no instance is ever deleted
// underneath a job that still exists -- and a VM behind a job this pass deleted
// gets one pass of grace, because Batch tears a deleted job's VMs down itself
// and deleting one in the same pass races that teardown. The next pass, which
// is what a scheduled reaper runs, reaps what Batch left behind.
func TestReaperDeletesTheJobBeforeTheInstance(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())

	first := rt.reap(t)
	if len(first.Actions) != 1 || first.Actions[0].Kind != ActionDeleteJob || first.Actions[0].Target != rt.name(id) {
		t.Fatalf("first pass = %v, want the job deleted and nothing else", first.Lines())
	}
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
		t.Fatalf("jobs.delete = %v, want [%s]", got, id)
	}
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("instances.delete = %v, want none: the job was deleted in this very pass", got)
	}
	if !refusedBecause(first, "fake-region-a/vm-1", "its job is being deleted by this pass") {
		t.Fatalf("first pass = %v, want the VM refused with the reason", first.Lines())
	}
	if len(rt.cf.Instances()) != 1 {
		t.Fatalf("instances = %v, want the VM still there for the next pass", rt.cf.Instances())
	}

	// The pass after: the job is gone from the listing for good (the fake's
	// delete removes the record), its VM is a leak, and the instance pass finds
	// it because no job owns the run any more.
	second := rt.reap(t)
	if len(second.Actions) != 1 || second.Actions[0].Kind != ActionDeleteVM || second.Actions[0].Target != "fake-region-a/vm-1" {
		t.Fatalf("second pass = %v, want the leaked VM reaped", second.Lines())
	}
	if got := rt.cf.Deletes(); len(got) != 1 || got[0] != "fake-region-a/vm-1" {
		t.Fatalf("instances.delete = %v, want the VM", got)
	}
}

// TestReaperDoesNotRaceItsOwnJobDeleteInADryRun: the grace is a property of
// the decision, not of the call having been made, so a dry run reports the same
// shape -- the job listed as a delete, its VM refused -- and touches nothing.
func TestReaperDoesNotRaceItsOwnJobDeleteInADryRun(t *testing.T) {
	rt := newReaperTest(t, func(o *ReaperOptions) { o.DryRun = true })
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep := rt.reap(t)
	if len(rep.Actions) != 1 || rep.Actions[0].Kind != ActionDeleteJob {
		t.Fatalf("dry run = %v, want the job listed as the one action", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-1", "its job would be deleted by this pass") {
		t.Fatalf("dry run = %v, want the VM refused the way a live pass refuses it", rep.Lines())
	}
	if len(rt.cf.Deletes()) != 0 || len(rt.bf.Deletes()) != 0 {
		t.Fatalf("a dry run deleted something: %v %v", rt.cf.Deletes(), rt.bf.Deletes())
	}
}

// TestReaperRefusesAJobItCannotAttribute: a job without the per-run label, or
// with the two label fields disagreeing about it, is not one this build
// created -- so it is not one the reaper deletes or cancels.
func TestReaperRefusesAJobItCannotAttribute(t *testing.T) {
	t.Run("no run label", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJobLabels(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour, map[string]string{RunLabel: ""})
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 {
			t.Fatalf("an unattributable job was acted on: %v", rep.Lines())
		}
		if !refused(rep, rt.name(id)) {
			t.Fatalf("report = %v, want a refusal of %s", rep.Lines(), id)
		}
	})
	t.Run("no marker in Job.labels", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The marker only in AllocationPolicy.labels, and a server that
		// ignores the filter (a fake told not to honour it, the shape a caller
		// must survive either way). The API's own filter reads Job.labels, so
		// a job like this should never have been returned -- and the reaper
		// checks the marker itself on every job it decided from, which is what
		// this reaches: listJobs drops it, so it is not listed, not judged and
		// not acted on.
		rt.bf.IgnoreFilter = true
		unmarked := rt.addJobLabelFields(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour, map[string]string{labelManaged: ""}, nil)
		// A marked job in the same listing, which the pass must still act on:
		// without it the test would pass on a pass that did nothing at all.
		marked := rt.addJob(t, "20260927-100001-abcd", "RUNNING", rt.stuck())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 1 || got[0] != marked {
			t.Fatalf("jobs.delete = %v, want only the marked job %s (the unmarked one is not this installation's)", got, marked)
		}
		if len(rep.Actions) != 1 || rep.Actions[0].Target != rt.name(marked) {
			t.Fatalf("actions = %v, want only the marked job", rep.Lines())
		}
		if strings.Contains(strings.Join(rep.Lines(), "\n"), unmarked) {
			t.Fatalf("report = %v, want no line about a job the marker check dropped", rep.Lines())
		}
		if st := rt.bf.State(unmarked); st != "RUNNING" {
			t.Fatalf("the unmarked job's state is %q: it must not be touched", st)
		}
	})
}

// TestReaperOwnsEveryRunLabelTheJobClaims is the ownership half of the refusal
// above, and it is the destructive half. A job the reaper refuses is still in
// the project and may still be RUNNING; its VMs must survive the pass whatever
// they are labelled with. The value a VM carries is AllocationPolicy.labels'
// (design §2.4), and the API does not check a label's SHAPE -- the launcher's
// IAM condition narrows job names, not labels -- so the value that reaches a VM
// can be anything a job creator wrote. A jobRunLabels that returned only the
// well-formed values would therefore fail to own exactly the value the
// instance pass then reads off the VM and finds absent from owned: the VM would
// pass the marker, region, non-empty-run and age checks and be deleted under a
// job that is still RUNNING. Owning a garbage value costs a map entry; the
// alternative costs someone's live run.
//
// Every case also has its VM past MaxAge with the job in a state the pass
// refuses, so nothing but the run-label ownership stands between the VM and
// compute.instances.delete.
func TestReaperOwnsEveryRunLabelTheJobClaims(t *testing.T) {
	const wellFormed = "acme-app-20260927-100000-abcd"
	for _, tc := range []struct {
		name                 string
		jobLabel, allocLabel string
	}{
		{
			// Reachable for anyone who can create a Batch job in the project:
			// nothing about the label's shape is checked before it reaches a VM.
			name:       "the job claims a malformed run in both label fields",
			jobLabel:   "NOT-A-LABEL",
			allocLabel: "NOT-A-LABEL",
		},
		{
			// Job.labels well-formed and AllocationPolicy.labels malformed:
			// jobRunLabel refuses (the fields disagree), and the value the VM
			// actually carries is the malformed one. This is the case a
			// well-formed-only jobRunLabels gets wrong on its own.
			name:       "the job is well-formed where the VMs do not read it",
			jobLabel:   wellFormed,
			allocLabel: "NOT-A-LABEL",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newReaperTest(t, nil)
			id := rt.addJobLabelFields(t, wellFormed, "RUNNING", 10*time.Hour,
				map[string]string{RunLabel: tc.jobLabel},
				map[string]string{RunLabel: tc.allocLabel})
			// The VM carries what AllocationPolicy gave the job, which is what
			// a real VM would carry.
			vmLabels := runLabels(t, wellFormed)
			vmLabels[RunLabel] = tc.allocLabel
			rt.vm("vm-1", vmLabels, rt.old())
			rep := rt.reap(t)
			if got := rt.cf.Deletes(); len(got) != 0 {
				t.Fatalf("the VM of a job the pass refused was deleted because its %s label is malformed: %v", RunLabel, got)
			}
			if got := rt.bf.Deletes(); len(got) != 0 {
				t.Fatalf("the unattributable job was deleted: %v", got)
			}
			if !refusedBecause(rep, "fake-region-a/vm-1", "could not attribute the job to a run") {
				t.Fatalf("report = %v, want the VM refused as the refused job's", rep.Lines())
			}
			if !refused(rep, rt.name(id)) {
				t.Fatalf("report = %v, want the job refused too", rep.Lines())
			}
		})
	}
}

// TestReaperIsIdempotent: a second pass over the same project finds nothing
// left to do -- the deletes it made are gone, and the refusals it made are
// made again, which is not an action.
func TestReaperIsIdempotent(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.addJob(t, "20260927-100001-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), rt.old())
	rt.vm("vm-2", map[string]string{labelManaged: managedValue}, rt.old())
	first := rt.reap(t)
	if len(first.Actions) != 3 {
		t.Fatalf("first pass actions = %v, want two jobs and a VM", first.Lines())
	}
	second := rt.reap(t)
	if len(second.Actions) != 0 {
		t.Fatalf("second pass actions = %v, want none", second.Lines())
	}
	if len(rt.cf.Deletes()) != 1 || len(rt.bf.Deletes()) != 2 {
		t.Fatalf("a second pass made a second call: jobs=%v instances=%v", rt.bf.Deletes(), rt.cf.Deletes())
	}
	if !refused(second, "fake-region-a/vm-2") {
		t.Fatalf("the second pass stopped refusing: %v", second.Lines())
	}
}

// TestReaperToleratesADeleteOfSomethingAlreadyGone: both deletes are 404 when
// the resource is gone -- someone else's reaper, a hand-delete, Batch's own
// teardown finishing between the listing and the call. A pass must read that as
// success (the promise idempotence rests on), not as a failure that ends the
// pass with an error, and not as an action it did not take.
func TestReaperToleratesADeleteOfSomethingAlreadyGone(t *testing.T) {
	t.Run("jobs.delete", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
		// The job is in the listing; by the time the delete arrives it is gone
		// (another reaper, or an operator). Only the delete path is refused.
		rt.bf.RefusePath(rt.name(id), http.StatusNotFound, "NOT_FOUND", "NOT_FOUND", "job "+id+" not found")
		rep, err := rt.r.Reap(context.Background())
		if err != nil {
			t.Fatalf("Reap with a job already gone: %v, want no error", err)
		}
		if len(rep.Actions) != 0 {
			t.Fatalf("actions = %v, want none: nothing was deleted by this pass", rep.Lines())
		}
		if st := rt.bf.State(id); st != "RUNNING" {
			t.Fatalf("the fake's job changed state (%q): the delete was refused, not applied", st)
		}
	})
	t.Run("instances.delete", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		// The VM is in the listing; by the time the delete arrives it is gone.
		rt.cf.FailDeleteWith = http.StatusNotFound
		rep, err := rt.r.Reap(context.Background())
		if err != nil {
			t.Fatalf("Reap with an instance already gone: %v, want no error", err)
		}
		if len(rep.Actions) != 0 {
			t.Fatalf("actions = %v, want none: the VM was already gone", rep.Lines())
		}
		if len(rt.cf.Deletes()) != 0 {
			t.Fatalf("the fake recorded a delete it refused: %v", rt.cf.Deletes())
		}
	})
	t.Run("a delete refused for another reason still fails the pass", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rt.cf.FailDeleteWith = http.StatusForbidden
		if _, err := rt.r.Reap(context.Background()); err == nil {
			t.Fatal("a forbidden instances.delete was tolerated: want an error")
		}
	})
}

// TestReaperDeadlineBoundaries pins the `<=`: a job exactly at a deadline is
// inside it, not past it. Off by one here is off by one run: both deadlines
// delete, so a boundary read the wrong way acts a second (or a pass) early on
// something that was still legitimate.
func TestReaperDeadlineBoundaries(t *testing.T) {
	rt := newReaperTest(t, nil)
	// Exactly at the queue deadline (the run ceiling plus the slack): still
	// waiting, not past it.
	at := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline())
	// One second past it: deleted by the queue deadline.
	past := rt.addJob(t, "20260927-100001-abcd", "QUEUED", rt.r.QueueDeadline()+time.Second)
	// Exactly at the run budget (the ceiling, the slack and ReapMargin) in
	// state: still live. One second past it: stuck.
	live := rt.addJob(t, "20260927-100002-abcd", "RUNNING", rt.r.RunBudget())
	stuck := rt.addJob(t, "20260927-100003-abcd", "RUNNING", rt.r.RunBudget()+time.Second)
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 2 || !slices.Contains(got, past) || !slices.Contains(got, stuck) {
		t.Fatalf("jobs.delete = %v, want [%s %s] and nothing else (the job exactly at the deadline is inside it)", got, past, stuck)
	}
	if strings.Contains(strings.Join(rep.Lines(), "\n"), at) || strings.Contains(strings.Join(rep.Lines(), "\n"), live) {
		t.Fatalf("report = %v, want neither job that was exactly at its deadline", rep.Lines())
	}
}

// TestReaperWaitsOutAnOverriddenRun is the ceiling half of the run budget, and
// the destructive one. --total-timeout (LaunchSpec.Timeout) lets a run ask for
// far longer than every workflow the installation configures, and the CLI caps
// it only at backend.MaxRunTime. A run budget built from the configured longest
// alone would therefore call an overridden run stuck the moment it crossed that
// number and delete it -- jobs.delete on a live run, one pass after it crossed,
// which is the failure this whole file exists to prevent and which no deadline
// validation can catch (the override is legal). That is why NewReaper's base is
// max(longest, backend.MaxRunTime).
func TestReaperWaitsOutAnOverriddenRun(t *testing.T) {
	// The harness's longest workflow is 30 minutes, so a budget built from it
	// would be 47 minutes: two hours of RUNNING is far past that and well
	// inside the day a run may ask for.
	t.Run("a run two hours into an overridden timeout is not stuck", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "RUNNING", rt.old(), 2*time.Hour)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
		rep := rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a run two hours into an overridden timeout was deleted: %v (run budget %v)", got, rt.r.RunBudget())
		}
		if got := rt.cf.Deletes(); len(got) != 0 {
			t.Fatalf("the VM of that run was deleted: %v", got)
		}
		if !refusedBecause(rep, "fake-region-a/vm-1", "its job is RUNNING, 120 minutes into the state") {
			t.Fatalf("report = %v, want the VM refused as a live run's", rep.Lines())
		}
		if st := rt.bf.State(id); st != "RUNNING" {
			t.Fatalf("job state = %q, want it left running", st)
		}
	})
	// And the run budget is still where the patience ends, so the subtest above
	// is not green because this pass deletes nothing at all: past it the same
	// job goes, and the VM with the pass after.
	t.Run("and past the run budget the same run is stuck", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJobStateAt(t, "20260927-100000-abcd", "RUNNING", rt.old(), rt.r.RunBudget()+time.Second)
		rt.reap(t)
		if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
			t.Fatalf("jobs.delete = %v, want [%s]: past the run budget a run is stuck again", got, id)
		}
	})
}

// TestReaperDryRunListsWhatItWouldDelete: dry-run is the way an operator sees
// what a pass would do before letting it -- and it touches nothing, both of the
// reaper's verbs included.
func TestReaperDryRunListsWhatItWouldDelete(t *testing.T) {
	rt := newReaperTest(t, func(o *ReaperOptions) { o.DryRun = true })
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	queued := rt.addJob(t, "20260927-100001-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), rt.old())
	// A VM whose run is the queued job, so the refusal's wording for that job
	// is in the report too: a dry run deletes nothing, and a line saying the
	// job "was deleted" would be a line an operator cannot act on.
	rt.vm("vm-2", runLabels(t, "20260927-100001-abcd"), rt.old())
	rep := rt.reap(t)
	if !rep.DryRun {
		t.Fatal("Report.DryRun is false")
	}
	if !hasAction(rep, ActionDeleteJob, rt.name(id)) || !hasAction(rep, ActionDeleteJob, rt.name(queued)) ||
		!hasAction(rep, ActionDeleteVM, "fake-region-a/vm-1") {
		t.Fatalf("dry-run report = %v, want both jobs and the VM", rep.Lines())
	}
	if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 || len(rt.cf.Deletes()) != 0 {
		t.Fatalf("dry-run touched something: jobs=%v cancels=%v instances=%v", rt.bf.Deletes(), rt.bf.Cancels(), rt.cf.Deletes())
	}
	lines := strings.Join(rep.Lines(), "\n")
	if !strings.Contains(lines, "would delete job") {
		t.Fatalf("dry-run lines do not say what they would do: %v", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-2", "would be deleted by this pass for the queue deadline") {
		t.Fatalf("dry-run lines do not say what they would do about the queued job: %v", rep.Lines())
	}
	if strings.Contains(lines, "was deleted by this pass") {
		t.Fatalf("a dry run says a job was deleted: %v", rep.Lines())
	}
	// The same project, really reaped afterwards: the same three actions,
	// and this time they happen.
	rt.r.o.DryRun = false
	rep = rt.reap(t)
	if len(rep.Actions) != 3 || len(rt.bf.Deletes()) != 2 || len(rt.cf.Deletes()) != 1 {
		t.Fatalf("the real pass after a dry run did not do what the dry run listed: %v", rep.Lines())
	}
	lines = strings.Join(rep.Lines(), "\n")
	if strings.Contains(lines, "would ") {
		t.Fatalf("a real pass's lines say they would: %v", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-2", "is being deleted by this pass for the queue deadline") {
		t.Fatalf("the real pass's line about the queued job does not say it was deleted: %v", rep.Lines())
	}
}

// TestReaperRefusalsCarryAReason checks that a refusal carries why, since the
// report is the audit trail: a bare "refused" line is useless to the person
// reading it. The two refusals a pass with nothing to act on produces are the
// ones asserted here, each with the reason that belongs to it -- a refusal
// that names the wrong cause is as unusable as one that names none.
func TestReaperRefusalsCarryAReason(t *testing.T) {
	rt := newReaperTest(t, nil)
	// A VM carrying this installation's managed label and nothing else: no
	// run can be attributed to it, so it is refused rather than deleted.
	rt.vm("vm-1", map[string]string{labelManaged: managedValue}, rt.old())
	// And a job in a state this build does not know (SUCCEEDING is not one of
	// Batch's states), which the job pass refuses rather than guesses at.
	id := rt.addJob(t, "20260927-100000-abcd", "SUCCEEDING", 3*time.Hour)
	rep := rt.reap(t)
	if len(rep.Refused) != 2 {
		t.Fatalf("report = %v, want exactly two refusals: the VM and the job", rep.Lines())
	}
	for _, c := range []struct{ target, reason string }{
		{"fake-region-a/vm-1", "it carries no " + RunLabel + " label, so it cannot be attributed to a run"},
		{rt.name(id), `its Batch state "SUCCEEDING" is not one this build knows`},
	} {
		if !refusedBecause(rep, c.target, c.reason) {
			t.Errorf("report = %v, want %s refused with %q", rep.Lines(), c.target, c.reason)
		}
	}
	// Both refusals are fail-closed: neither touched anything.
	if len(rt.bf.Deletes()) != 0 || len(rt.cf.Deletes()) != 0 {
		t.Fatalf("a refusal acted: jobs=%v instances=%v", rt.bf.Deletes(), rt.cf.Deletes())
	}
}

// TestReaperFailsClosedWhenTheListingsFail: a listing the reaper cannot read
// is an error, not an empty project -- and nothing is deleted on the strength
// of a listing it could not finish.
func TestReaperFailsClosedWhenTheListingsFail(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100001-abcd"), rt.old())
	rt.bf.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "no batch.jobs.list")
	if _, err := rt.r.Reap(context.Background()); err == nil {
		t.Fatal("Reap with an unreadable job listing: want an error")
	}
	if len(rt.bf.Deletes()) != 0 || len(rt.cf.Deletes()) != 0 {
		t.Fatalf("something was deleted from a listing that failed: %v", rt.bf.Deletes())
	}
	_ = id

	// The same for the instance listing, which is read after the job pass:
	// the job pass's work is reported, and the pass still fails.
	rt = newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.cf.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "no compute.instances.list")
	rep, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap with an unreadable instance listing: want an error")
	}
	if len(rep.Actions) != 1 {
		t.Fatalf("report = %v, want the job it deleted before the failure", rep.Lines())
	}
}

// TestReaperQueueDeadlineErasesTheRecordAndAStartedJobKeepsIt: the queue
// deadline's delete erases the job's own record -- its state and the status
// message Batch attaches, and nothing the run's readers depend on, because the
// run's launch.json is the launcher's object in the runs bucket -- for a job
// that never ran a task -- and it
// must not touch a job that DID leave QUEUED, whatever the listing said when
// the pass decided. The second half is the promise §1.4 makes about ls,
// diagnose and watch: a run that ran keeps its record.
func TestReaperQueueDeadlineErasesTheRecordAndAStartedJobKeepsIt(t *testing.T) {
	rt := newReaperTest(t, nil)
	gone := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	rt.reap(t)
	if got := rt.bf.IDs(); len(got) != 0 {
		t.Fatalf("the queue deadline left the record of %s behind: %v", gone, got)
	}

	// The other half: the listing says QUEUED past the deadline and the job
	// has left QUEUED by the time the reaper reads it back. Deleting it would
	// erase a run that ran -- and a run that left QUEUED is the one shape the
	// queue deadline may never delete.
	rt = newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100001-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	// The race, and the only way a test can reach it: a listing and a
	// read-back read the same stored state otherwise.
	rt.bf.GetRace = map[string]string{id: "RUNNING"}
	// Its VM, old enough that the age floor cannot be what saves it: the VM
	// must be left alone because the job behind it is a run Batch owns, not
	// because it is young.
	rt.vm("vm-1", runLabels(t, "20260927-100001-abcd"), rt.old())
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 0 {
		t.Fatalf("the queue deadline deleted a job that had left QUEUED: %v", got)
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none: the job is left to the next pass, which judges it on its time in its new state", rep.Lines())
	}
	// It is left where it is, in the state it reached on its own: no state
	// was rewritten by the pass that decided against the delete, and no API
	// call was made that would have rewritten one.
	if st := rt.bf.State(id); st != "RUNNING" {
		t.Fatalf("the job's state = %q, want RUNNING", st)
	}
	// Its VM too: a job that left QUEUED is a run in flight, and Batch owns
	// its teardown. Deleting the VM here would race it.
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("the VM of a job that had left QUEUED was deleted in the same pass: %v", got)
	}
	if !refusedBecause(rep, "fake-region-a/vm-1", "left QUEUED") {
		t.Fatalf("report = %v, want the run's VM refused", rep.Lines())
	}
}

// TestReaperQueueDeadlineOfAJobThatIsAlreadyGone: the job was in the listing
// the pass decided from and had been deleted by the time the read-back arrived
// -- someone else's delete, or Batch's own 60-day rule (§9) -- so the read
// answers 404. That is not a failure of the pass: one vanished job must not
// stop the reaper from doing everything else it had decided to do, which is the
// same idempotence the deletes have (a 404 from jobs.delete is success).
// Nothing is recorded, because nothing was deleted.
func TestReaperQueueDeadlineOfAJobThatIsAlreadyGone(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	// The read-back is refused as a 404, which is the shape of a job that
	// vanished: a real 404 needs a second reaper to race this one.
	rt.bf.RefusePath(rt.name(id), http.StatusNotFound, "NOT_FOUND", "NOT_FOUND", "job "+id+" not found")
	// Something else in the same pass that the reaper must still do: a stuck job
	// after the vanished one. Without it the test would pass on a pass that gave
	// up at the first difference.
	stuck := rt.addJobStateAt(t, "20260927-100001-abcd", "SCHEDULED", rt.startedAfter(rt.stuck()), rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep, err := rt.r.Reap(context.Background())
	if err != nil {
		t.Fatalf("Reap: %v, want no error: a job that is already gone needed no delete", err)
	}
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != stuck {
		t.Fatalf("jobs.delete = %v, want [%s]: the pass went on after the vanished job", got, stuck)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Target != rt.name(stuck) {
		t.Fatalf("actions = %v, want only the stuck job's delete", rep.Lines())
	}
	if len(rt.cf.Deletes()) != 0 {
		t.Fatalf("the vanished job's VM was deleted in the same pass: %v", rt.cf.Deletes())
	}
	// The run is owned for this pass as if the delete had happened: the job is
	// gone, so nothing would have raced the VM's delete -- but the pass decided
	// to delete, and one pass of grace costs an interval on a leak that the next
	// pass reaps (§3.4).
	if !refusedBecause(rep, "fake-region-a/vm-1", "is being deleted by this pass for the queue deadline") {
		t.Fatalf("report = %v, want the run's VM refused for this pass", rep.Lines())
	}
}

// TestReaperQueueDeadlineDeleteThatFails: a delete the API refuses for a reason
// that is not the job's own absence -- a missing permission, say -- is a real
// failure. The pass must say so rather than read a refused delete as a delete,
// and the job must be left where it was. The read-back the queue deadline makes
// before the delete still answers, so the failure is the delete's.
func TestReaperQueueDeadlineDeleteThatFails(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", rt.r.QueueDeadline()+time.Minute)
	rt.bf.FailDeleteWith = http.StatusForbidden
	rep, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap with a delete the API refused: want an error")
	}
	if !strings.Contains(err.Error(), "deleting queued job") {
		t.Fatalf("error = %v, want it to name what the pass was doing", err)
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none: nothing was deleted", rep.Lines())
	}
	if st := rt.bf.State(id); st != "QUEUED" {
		t.Fatalf("the job's state = %q, want it untouched", st)
	}
}

// TestReaperNeedsItsConfiguration refuses what cannot be reaped with: a
// reaper without a project, a region or a timeout source is a reaper whose
// deadlines would be invented.
func TestReaperNeedsItsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tweak func(*ReaperOptions)
	}{
		{"no project", func(o *ReaperOptions) { o.GCPProject = "" }},
		{"no region", func(o *ReaperOptions) { o.Region = "" }},
		{"no timeouts", func(o *ReaperOptions) { o.Timeouts = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bf := gcpfake.NewBatch(t)
			o := ReaperOptions{
				GCPProject: "p", Region: "r", Timeouts: testTimeouts{longest: time.Minute},
				Endpoints: Endpoints{Batch: bf.URL + "/", NoAuth: true},
			}
			tc.tweak(&o)
			if _, err := NewReaper(context.Background(), o); err == nil {
				t.Fatal("NewReaper: want an error")
			}
		})
	}
}

// TestReaperRefusesDeadlinesItCannotHonour: the two deadlines a caller sets
// are what the reaper deletes on, so they are validated rather than trusted. A
// negative one is a unit or sign error, one longer than the reaper will ever
// act on protects nothing (a job that old is deleted by the same rule anyway),
// one shorter than a queue wait plus a run -- plus the boot and teardown the
// age floor exists for -- is a deadline that would delete live runs, and a
// queue deadline longer than a whole run budget is one under which no job's
// state deadline could be read against a trustworthy status event. All are the
// failures the queue deadline and the run budget exist to avoid.
func TestReaperRefusesDeadlinesItCannotHonour(t *testing.T) {
	base := 30 * time.Minute
	slack := backend.TaskTimeoutSlack
	// The base the defaults derive from is max(longest, the run ceiling): a run
	// may override its total time past the installation's workflows, so the
	// deadlines are the ceiling's, not this 30-minute workflow's.
	longest := max(base, backend.MaxRunTime)
	queueDefault := longest + slack
	// The floor is the queue deadline plus a whole run budget, and the run
	// budget is the longest a run may be active, its slack AND ReapMargin: an
	// instance must also be old enough for a boot and a teardown, or the floor
	// would admit an age at which a VM of a just-finished run looks like a
	// leak.
	floor := queueDefault + longest + slack + ReapMargin
	runBudget := longest + slack + ReapMargin
	for _, tc := range []struct {
		name  string
		tweak func(*ReaperOptions)
	}{
		{"a negative queue deadline", func(o *ReaperOptions) { o.QueueDeadline = -time.Minute }},
		{"a negative maximum age", func(o *ReaperOptions) { o.MaxAge = -time.Minute }},
		{"a queue deadline longer than the reaper acts on", func(o *ReaperOptions) { o.QueueDeadline = MaxDeadline + time.Second }},
		{"a queue deadline longer than a whole run budget", func(o *ReaperOptions) { o.QueueDeadline = runBudget + time.Second }},
		{"a maximum age longer than the reaper acts on", func(o *ReaperOptions) { o.MaxAge = MaxDeadline + time.Second }},
		{"a maximum age too short to hold a queue wait and a run", func(o *ReaperOptions) { o.MaxAge = floor - time.Second }},
		{"a maximum age below an explicit queue deadline", func(o *ReaperOptions) {
			o.QueueDeadline = queueDefault
			o.MaxAge = queueDefault + time.Minute
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bf := gcpfake.NewBatch(t)
			o := ReaperOptions{
				GCPProject: testProject, Region: testRegion,
				Timeouts:  testTimeouts{task: base, longest: base},
				Endpoints: Endpoints{Batch: bf.URL + "/", NoAuth: true},
			}
			tc.tweak(&o)
			if _, err := NewReaper(context.Background(), o); err == nil {
				t.Fatal("NewReaper: want an error")
			}
		})
	}
	// The values that are inside the bounds are accepted, and the deadlines
	// derived from them are the ones the decisions use: a deadline at the bound
	// itself is inside it, and a maximum age of exactly the floor (a queue wait
	// plus a run) leaves a run no room to be wrongly judged.
	for _, tc := range []struct {
		name  string
		want  time.Duration
		tweak func(*ReaperOptions)
	}{{
		"a maximum age at the bound", MaxDeadline, func(o *ReaperOptions) { o.MaxAge = MaxDeadline },
	}, {
		"a maximum age exactly the queue deadline plus a run", floor, func(o *ReaperOptions) { o.MaxAge = floor },
	}, {
		// The queue deadline's own bound is inclusive: one exactly at a run
		// budget is the longest queue wait the reaper will read a state
		// deadline against, and MaxAge's default still holds a queue wait plus
		// a run.
		"a queue deadline exactly at a run budget", 2 * runBudget, func(o *ReaperOptions) { o.QueueDeadline = runBudget },
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newReaperTest(t, tc.tweak)
			if got, want := rt.r.MaxAge(), tc.want; got != want {
				t.Fatalf("MaxAge() = %v, want %v", got, want)
			}
		})
	}
}

// TestReaperListingsPageAndSortWhatTheyReturn: the job listing follows page
// tokens, and the instance listing is ordered by zone and name whatever order
// the scopes came back in -- a report read twice must not reorder itself.
func TestReaperListingsPageAndSort(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.r.listPageSize = 1
	ids := []string{
		rt.addJob(t, "20260927-100000-abcd", "SUCCEEDED", 3*time.Hour),
		rt.addJob(t, "20260927-100001-abcd", "SUCCEEDED", 3*time.Hour),
		rt.addJob(t, "20260927-100002-abcd", "SUCCEEDED", 3*time.Hour),
	}
	jobs, err := rt.r.listJobs(context.Background())
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	if len(jobs) != len(ids) {
		t.Fatalf("listJobs returned %d jobs, want %d (page size 1)", len(jobs), len(ids))
	}
	// Instances come back in scope order, not map order: two instances in two
	// zones, and the report names them sorted.
	rt.cf.Zones = []string{"fake-region-b", "fake-region-a"}
	rt.cf.AddInstanceCreated("fake-region-b", "vm-b", runLabels(t, "20260927-100003-abcd"), rt.now.Add(-rt.old()))
	rt.cf.AddInstanceCreated("fake-region-a", "vm-a", runLabels(t, "20260927-100004-abcd"), rt.now.Add(-rt.old()))
	rep := rt.reap(t)
	var targets []string
	for _, a := range rep.Actions {
		if a.Kind == ActionDeleteVM {
			targets = append(targets, a.Target)
		}
	}
	if len(targets) != 2 || targets[0] != "fake-region-a/vm-a" || targets[1] != "fake-region-b/vm-b" {
		t.Fatalf("VM actions = %v, want them sorted by zone/name", targets)
	}
}

// TestReportLinesPerActionKind: one line of the report, for every ActionKind
// and for both a pass and a dry run, with and without a reason. This is the
// rendering the reaper's own Cloud Run job logs and `fugaro reaper` prints, so
// each line has to name the action, the target and the reason on its own: a
// reader of a log has no other row to join it to.
func TestReportLinesPerActionKind(t *testing.T) {
	const (
		jobName = "projects/proj-1234/locations/us-east5/jobs/acme-app-web-20260927-100000-abcd"
		vmName  = "us-east5-a/acme-app-web-20260927-100001-abcd"
		why     = "still RUNNING 900 minutes into the state, past every run deadline this installation configures"
	)
	for _, tc := range []struct {
		name   string
		kind   ActionKind
		dryRun bool
		target string
		reason string
		want   string
	}{
		{"a deleted job", ActionDeleteJob, false, jobName, why,
			"deleted job " + jobName + " (" + why + ")"},
		{"a job a dry run would delete", ActionDeleteJob, true, jobName, "stuck: " + why,
			"would delete job " + jobName + " (stuck: " + why + ")"},
		{"a deleted VM", ActionDeleteVM, false, vmName, "no Batch job of its run answers for it",
			"deleted VM " + vmName + " (no Batch job of its run answers for it)"},
		{"a VM a dry run would delete", ActionDeleteVM, true, vmName, "no Batch job of its run answers for it",
			"would delete VM " + vmName + " (no Batch job of its run answers for it)"},
		// An action with no reason is a line without the parentheses rather
		// than one ending in "()".
		{"an action with no reason", ActionDeleteJob, false, jobName, "", "deleted job " + jobName},
		// A kind this build does not know renders as itself: a new ActionKind
		// whose verb nobody wrote shows up in the log rather than as an empty
		// line or a line that says it deleted something it did not.
		{"a kind this build does not know", ActionKind("reap-something"), false, jobName, "why",
			"reap-something " + jobName + " (why)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := Report{DryRun: tc.dryRun, Actions: []Action{{Kind: tc.kind, Target: tc.target, Reason: tc.reason}}}
			if got := rep.Lines(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("Lines() = %q, want exactly [%q]", got, tc.want)
			}
		})
	}
}

// TestReportLinesOrderAndHeadline: the report reads as a header, then what the
// pass did, then what it refused and why. The header is the line that makes an
// old log checkable -- the project, the region and the three deadlines every
// deletion in it was judged by -- and it is written from the Report's own
// fields, so a Report that names no project (one built by hand) has none.
func TestReportLinesOrderAndHeadline(t *testing.T) {
	rep := Report{
		Project: "proj-1234", Region: "us-east5",
		QueueDeadline: 25*time.Hour + backend.TaskTimeoutSlack,
		RunBudget:     25*time.Hour + backend.TaskTimeoutSlack + ReapMargin,
		MaxAge:        50*time.Hour + 2*backend.TaskTimeoutSlack + ReapMargin,
		Actions: []Action{
			{Kind: ActionDeleteJob, Target: "projects/proj-1234/locations/us-east5/jobs/j", Reason: "stuck"},
			{Kind: ActionDeleteVM, Target: "us-east5-a/vm", Reason: "leaked"},
		},
		Refused: []Refusal{{Target: "us-east5-a/other", Reason: "it is not ours"}},
	}
	want := []string{
		"reaped projects/proj-1234/locations/us-east5: queue deadline 25h2m0s, run budget 25h17m0s, max age 50h19m0s",
		"deleted job projects/proj-1234/locations/us-east5/jobs/j (stuck)",
		"deleted VM us-east5-a/vm (leaked)",
		"refused us-east5-a/other: it is not ours",
	}
	if got := rep.Lines(); !slices.Equal(got, want) {
		t.Fatalf("Lines() = %q, want %q", got, want)
	}
	// A dry run says so on the header as well: a log that holds only "would
	// delete ..." lines must not read later as a pass that found nothing to
	// delete.
	rep.DryRun = true
	if got := rep.Lines()[0]; got != "would reap projects/proj-1234/locations/us-east5: queue deadline 25h2m0s, run budget 25h17m0s, max age 50h19m0s" {
		t.Errorf("a dry run's headline = %q, want it to say would reap", got)
	}
	// No project, no headline: the lines of a report built by hand (or of a
	// pass that never got as far as naming where it looked) start with the
	// first action.
	bare := Report{Actions: []Action{{Kind: ActionDeleteJob, Target: "j", Reason: "stuck"}}}
	if got := bare.Lines(); len(got) != 1 || got[0] != "deleted job j (stuck)" {
		t.Errorf("Lines() of a report naming no project = %q, want the action alone", got)
	}
}

// TestReapReportCarriesWhereItLookedAndByWhat: the context Reap fills in is
// the one the Reaper decides by, not a second derivation -- a report whose
// header disagreed with the deadlines the pass actually used would be worse
// than no header at all. Including on a pass that errored before it decided
// anything: the report a failed pass returns still says where it was looking.
func TestReapReportCarriesWhereItLookedAndByWhat(t *testing.T) {
	rt := newReaperTest(t, nil)
	rep := rt.reap(t)
	if rep.Project != testProject || rep.Region != testRegion {
		t.Errorf("report names %s/%s, want %s/%s", rep.Project, rep.Region, testProject, testRegion)
	}
	if rep.QueueDeadline != rt.r.QueueDeadline() || rep.RunBudget != rt.r.RunBudget() || rep.MaxAge != rt.r.MaxAge() {
		t.Errorf("report deadlines = %v/%v/%v, want the reaper's own %v/%v/%v",
			rep.QueueDeadline, rep.RunBudget, rep.MaxAge, rt.r.QueueDeadline(), rt.r.RunBudget(), rt.r.MaxAge())
	}
	if got := rep.Lines()[0]; !strings.HasPrefix(got, "reaped projects/"+testProject+"/locations/"+testRegion+": ") {
		t.Errorf("a clean pass's first line = %q, want the headline naming the pass", got)
	}
	if rep.DryRun {
		t.Errorf("a live pass's report says DryRun")
	}
	// The failed pass: the instance delete is refused by the API, so Reap
	// returns an error with the report it built up to that point.
	rt = newReaperTest(t, nil)
	rt.cf.FailDeleteWith = http.StatusForbidden
	rt.vm("vm", runLabels(t, "20260927-100000-abcd"), rt.old())
	rep, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap succeeded with an instance delete the API refused")
	}
	if rep.Project != testProject || rep.Region != testRegion || rep.QueueDeadline != rt.r.QueueDeadline() {
		t.Errorf("a failed pass's report = %+v, want it to carry the project, region and deadlines it was judging by", rep)
	}
	if got := rep.Lines()[0]; !strings.HasPrefix(got, "reaped projects/"+testProject+"/locations/"+testRegion+": ") {
		t.Errorf("a failed pass's first line = %q, want the headline naming the pass", got)
	}
}
