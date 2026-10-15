package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// addJob creates a Fugaro job as the launcher would: the design's label set
// in both label fields, which is what makes it reapable at all (design
// §2.4), in the raw Batch state and at the age given.
func (rt *reapTest) addJob(t *testing.T, runID, state string, age time.Duration) string {
	t.Helper()
	return rt.addJobLabels(t, runID, state, age, nil)
}

// addJobLabels is addJob with extra labels merged in (a test that wants a
// job this installation did not launch, say).
func (rt *reapTest) addJobLabels(t *testing.T, runID, state string, age time.Duration, edit map[string]string) string {
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
	for k, v := range edit {
		if v == "" {
			delete(jobLabels, k)
			delete(allocLabels, k)
			continue
		}
		jobLabels[k], allocLabels[k] = v, v
	}
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

// TestReaperDefaultsComeFromTheLongestWorkflow pins the two derived
// deadlines: the queue deadline is the longest configured task timeout plus
// its slack (design §1.4's "the launcher's own task timeout"), and the age
// floor adds the queue wait, the run and ReapMargin (§3.4).
func TestReaperDefaultsComeFromTheLongestWorkflow(t *testing.T) {
	rt := newReaperTest(t, nil)
	if got, want := rt.r.QueueDeadline(), 30*time.Minute+backend.TaskTimeoutSlack; got != want {
		t.Fatalf("QueueDeadline = %v, want %v", got, want)
	}
	want := 30*time.Minute + backend.TaskTimeoutSlack + 30*time.Minute + backend.TaskTimeoutSlack + ReapMargin
	if got := rt.r.MaxAge(); got != want {
		t.Fatalf("MaxAge = %v, want %v", got, want)
	}
	// An installation with no workflows at all must not collapse to a
	// two-minute deadline: the default task timeout is the floor.
	rt = newReaperTest(t, func(o *ReaperOptions) { o.Timeouts = testTimeouts{} })
	if got, want := rt.r.QueueDeadline(), backend.DefaultTaskTimeout+backend.TaskTimeoutSlack; got != want {
		t.Fatalf("QueueDeadline with no workflows = %v, want %v", got, want)
	}
}

// TestReaperDecidesEveryBatchState is the decision table the review is about:
// what the reaper does with a job in each of the nine documented
// JobStatus.State values (design §2.3), old enough that no age filter saves
// it.
func TestReaperDecidesEveryBatchState(t *testing.T) {
	// The job's own terminal states: the record is history and Batch's own
	// teardown owns its VM, so nothing is done to any of them.
	for _, state := range []string{"SUCCEEDED", "FAILED", "CANCELLED", "CANCELLATION_IN_PROGRESS"} {
		t.Run("keeps a "+state+" job", func(t *testing.T) {
			rt := newReaperTest(t, nil)
			id := rt.addJob(t, "20260927-100000-abcd", state, 10*time.Hour)
			rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	t.Run("deletes a stuck SCHEDULED job", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "SCHEDULED", 10*time.Hour)
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
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
		if len(rt.reap(t).Actions) != 1 || len(rt.bf.Deletes()) != 1 {
			t.Fatalf("a hung RUNNING job past every deadline was not deleted: deletes=%v", rt.bf.Deletes())
		}
		if got := rt.bf.Deletes()[0]; got != id {
			t.Fatalf("jobs.delete = %s, want %s", got, id)
		}
	})
	t.Run("deletes a stuck DELETION_IN_PROGRESS job", func(t *testing.T) {
		// The residual §3.4 names: a deletion that never finished.
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 10*time.Hour)
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
// cancelled -- jobs.cancel, never jobs.delete, because its record is what ls,
// diagnose and watch read (design §1.4, C8) -- and one within the deadline is
// left alone.
func TestReaperQueueDeadline(t *testing.T) {
	rt := newReaperTest(t, nil)
	// 40 minutes: past the 32-minute queue deadline, on a 30-minute workflow.
	old := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
	// 10 minutes: still within it.
	young := rt.addJob(t, "20260927-100001-abcd", "QUEUED", 10*time.Minute)
	rep := rt.reap(t)
	if got := rt.bf.Cancels(); len(got) != 1 || got[0] != old {
		t.Fatalf("jobs.cancel = %v, want [%s]", got, old)
	}
	if len(rt.bf.Deletes()) != 0 {
		t.Fatalf("the queue deadline deleted a job (%v): it must leave the record behind", rt.bf.Deletes())
	}
	if !hasAction(rep, ActionCancelJob, rt.name(old)) {
		t.Fatalf("report = %v, want a cancel of %s", rep.Lines(), old)
	}
	if strings.Contains(strings.Join(rep.Lines(), "\n"), young) {
		t.Fatalf("a job still within the queue deadline was reported: %v", rep.Lines())
	}
}

// TestReaperQueueDeadlineLeavesTheDeadlineToJobState: a job past the queue
// deadline with no readable create time is refused, not cancelled -- the
// deadline cannot be judged.
func TestReaperQueueDeadlineNeedsACreateTime(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
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
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	if !refusedBecause(rep, "fake-region-a/vm-1", "the job behind it is RUNNING") {
		t.Fatalf("report = %v, want the live job's VM refused and named as the live job's", rep.Lines())
	}
}

// TestReaperDeletesALeakedVMWhoseJobIsGone is the case the reaper exists for:
// a VM carrying the installation's marker and a run label, past every
// deadline, with no job behind it.
func TestReaperDeletesALeakedVMWhoseJobIsGone(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 3*time.Hour)
	rt.vm("vm-2", runLabels(t, "20260927-100001-abcd"), 3*time.Hour)
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
	rt.vm("vm-1", map[string]string{labelManaged: managedValue}, 3*time.Hour)
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
	rt.vm("vm-foreign", labels, 3*time.Hour)
	rep := rt.reap(t)
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("another installation's VM was deleted: %v", got)
	}
	if !refused(rep, "fake-region-a/vm-foreign") {
		t.Fatalf("report = %v, want a refusal naming the foreign VM", rep.Lines())
	}
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

// TestReaperDeletesTheJobBeforeTheInstance is the ordering rule of §3.4: a
// stuck job is deleted in the job pass, and only then does the instance pass
// look -- so the VM of a leaked job is reapable in the same pass, and no
// instance is ever deleted underneath a job that still exists.
func TestReaperDeletesTheJobBeforeTheInstance(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
	rep := rt.reap(t)
	if len(rep.Actions) != 2 {
		t.Fatalf("actions = %v, want the job and then its VM", rep.Lines())
	}
	if rep.Actions[0].Kind != ActionDeleteJob || rep.Actions[0].Target != rt.name(id) {
		t.Fatalf("first action = %+v, want the job %s deleted first", rep.Actions[0], id)
	}
	if rep.Actions[1].Kind != ActionDeleteVM || rep.Actions[1].Target != "fake-region-a/vm-1" {
		t.Fatalf("second action = %+v, want the VM deleted second", rep.Actions[1])
	}
	// And the calls were made in that order too, not just reported so.
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
		t.Fatalf("jobs.delete = %v, want [%s]", got, id)
	}
	if got := rt.cf.Deletes(); len(got) != 1 || got[0] != "fake-region-a/vm-1" {
		t.Fatalf("instances.delete = %v, want the VM", got)
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
		// The marker only in AllocationPolicy.labels: the job would not be
		// listed by the installation's own filter either, and it is refused.
		rt.addJobLabels(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour, map[string]string{labelManaged: ""})
		rep := rt.reap(t)
		if len(rt.bf.Deletes()) != 0 {
			t.Fatalf("a job without the marker in Job.labels was deleted: %v", rep.Lines())
		}
		if len(rep.Actions) != 0 {
			t.Fatalf("actions = %v, want none", rep.Lines())
		}
	})
}

// TestReaperIsIdempotent: a second pass over the same project finds nothing
// left to do -- the deletes it made are gone, and the refusals it made are
// made again, which is not an action.
func TestReaperIsIdempotent(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.addJob(t, "20260927-100001-abcd", "QUEUED", 40*time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), 10*time.Hour)
	rt.vm("vm-2", map[string]string{labelManaged: managedValue}, 10*time.Hour)
	first := rt.reap(t)
	if len(first.Actions) != 3 {
		t.Fatalf("first pass actions = %v, want a job, a cancel and a VM", first.Lines())
	}
	second := rt.reap(t)
	if len(second.Actions) != 0 {
		t.Fatalf("second pass actions = %v, want none", second.Lines())
	}
	if len(rt.cf.Deletes()) != 1 || len(rt.bf.Deletes()) != 1 || len(rt.bf.Cancels()) != 1 {
		t.Fatalf("a second pass made a second call: jobs=%v cancels=%v instances=%v", rt.bf.Deletes(), rt.bf.Cancels(), rt.cf.Deletes())
	}
	if !refused(second, "fake-region-a/vm-2") {
		t.Fatalf("the second pass stopped refusing: %v", second.Lines())
	}
}

// TestReaperDryRunListsWhatItWouldDelete: dry-run is the way an operator sees
// what a pass would do before letting it -- and it touches nothing, cancel
// included.
func TestReaperDryRunListsWhatItWouldDelete(t *testing.T) {
	rt := newReaperTest(t, func(o *ReaperOptions) { o.DryRun = true })
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	queued := rt.addJob(t, "20260927-100001-abcd", "QUEUED", 40*time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), 10*time.Hour)
	rep := rt.reap(t)
	if !rep.DryRun {
		t.Fatal("Report.DryRun is false")
	}
	if !hasAction(rep, ActionDeleteJob, rt.name(id)) || !hasAction(rep, ActionCancelJob, rt.name(queued)) ||
		!hasAction(rep, ActionDeleteVM, "fake-region-a/vm-1") {
		t.Fatalf("dry-run report = %v, want the job, the cancel and the VM", rep.Lines())
	}
	if len(rt.bf.Deletes()) != 0 || len(rt.bf.Cancels()) != 0 || len(rt.cf.Deletes()) != 0 {
		t.Fatalf("dry-run touched something: jobs=%v cancels=%v instances=%v", rt.bf.Deletes(), rt.bf.Cancels(), rt.cf.Deletes())
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "would delete job") {
		t.Fatalf("dry-run lines do not say what they would do: %v", rep.Lines())
	}
	// The same project, really reaped afterwards: the same three actions,
	// and this time they happen.
	rt.r.o.DryRun = false
	rep = rt.reap(t)
	if len(rep.Actions) != 3 || len(rt.bf.Deletes()) != 1 || len(rt.bf.Cancels()) != 1 || len(rt.cf.Deletes()) != 1 {
		t.Fatalf("the real pass after a dry run did not do what the dry run listed: %v", rep.Lines())
	}
	if strings.Contains(strings.Join(rep.Lines(), "\n"), "would ") {
		t.Fatalf("a real pass's lines say they would: %v", rep.Lines())
	}
}

// TestReaperRefusalsAreReasonsAnOperatorCanActOn checks that every refusal
// carries why, since the report is the audit trail: a bare "refused" line is
// useless to the person reading it.
func TestReaperRefusalsCarryAReason(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.vm("vm-1", map[string]string{labelManaged: managedValue}, 3*time.Hour)
	rt.addJob(t, "20260927-100000-abcd", "SUCCEEDING", 3*time.Hour)
	rep := rt.reap(t)
	if len(rep.Refused) == 0 {
		t.Fatal("no refusals, want two")
	}
	for _, f := range rep.Refused {
		if f.Target == "" || f.Reason == "" {
			t.Fatalf("refusal %+v has no target or no reason", f)
		}
	}
}

// TestReaperFailsClosedWhenTheListingsFail: a listing the reaper cannot read
// is an error, not an empty project -- and nothing is deleted on the strength
// of a listing it could not finish.
func TestReaperFailsClosedWhenTheListingsFail(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.vm("vm-1", runLabels(t, "20260927-100001-abcd"), 10*time.Hour)
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
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.cf.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "no compute.instances.list")
	rep, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap with an unreadable instance listing: want an error")
	}
	if len(rep.Actions) != 1 {
		t.Fatalf("report = %v, want the job it deleted before the failure", rep.Lines())
	}
}

// TestReaperJobsDeleteIsTheOnlyThingThatErasesAJob: the queue deadline's
// cancel and the failure to cancel both leave the record; only the stuck-job
// delete erases one. This is the promise §1.4 makes about ls, diagnose and
// watch.
func TestReaperCancelKeepsTheRecordAndDeleteDoesNot(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
	rt.reap(t)
	if got := rt.bf.IDs(); len(got) != 1 {
		t.Fatalf("the queue deadline's cancel removed the job: %v", got)
	}
	if st := rt.bf.State(JobID(backendtest.Slug, backendtest.Workflow, "20260927-100000-abcd")); st != "CANCELLED" {
		t.Fatalf("the cancelled job's state = %q, want CANCELLED", st)
	}
}

// TestReaperToleratesACancelThatRacesTheJobsOwnEnd: the job started (or
// finished) between the listing and the cancel, which the API refuses. The
// pass reads it back and is content, exactly as Cancel is (design §2.3).
func TestReaperToleratesACancelThatRacesTheJobsOwnEnd(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
	rt.bf.SetState(id, "RUNNING")
	rep := rt.reap(t)
	if len(rt.bf.Cancels()) != 0 {
		t.Fatalf("a cancel that raced the job's start was recorded: %v", rt.bf.Cancels())
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none (the job had started)", rep.Lines())
	}
	if len(rt.bf.Deletes()) != 0 {
		t.Fatalf("the reaper deleted a freshly running job: %v", rt.bf.Deletes())
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
	rt.cf.AddInstanceCreated("fake-region-b", "vm-b", runLabels(t, "20260927-100003-abcd"), rt.now.Add(-3*time.Hour))
	rt.cf.AddInstanceCreated("fake-region-a", "vm-a", runLabels(t, "20260927-100004-abcd"), rt.now.Add(-3*time.Hour))
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
