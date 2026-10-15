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
	// The state AND its status event: the reaper judges a job's stuckness by
	// the time it has spent in the state it reports, so a job at "age" is one
	// that entered that state "age" ago. A job older than its state -- one that
	// queued for a long time and then started -- is what SetStateAt and
	// addJobStateAt are for.
	rt.bf.SetStateAt(id, state, rt.now.Add(-age))
	rt.bf.SetCreated(id, rt.now.Add(-age))
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
	// Both jobs' VMs, old enough that only the job behind each stands between
	// it and the reaper: one job is still waiting for capacity, the other is
	// being cancelled, and neither frees its VM in this pass.
	rt.vm("vm-young", runLabels(t, "20260927-100001-abcd"), 10*time.Hour)
	rt.vm("vm-cancelled", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	// Neither job's VM was the reaper's in that pass: the young one is still
	// Batch's, and the cancelled one is being torn down from
	// CANCELLATION_IN_PROGRESS.
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("a queued job's VM was deleted: %v", got)
	}
	if !refusedBecause(rep, "fake-region-a/vm-young", "still within the queue deadline") {
		t.Fatalf("report = %v, want the queued job's VM refused", rep.Lines())
	}
	if !refusedBecause(rep, "fake-region-a/vm-cancelled", "cancelled") {
		t.Fatalf("report = %v, want the cancelled job's VM refused", rep.Lines())
	}
}

// TestReaperKeepsTheVMsOfAJobBeingDeleted: a job already in
// DELETION_IN_PROGRESS and within every deadline is being deleted by someone,
// and Batch is taking its VMs down with it. The reaper must not race that --
// only once the deletion itself is stuck past the age floor does it act, and
// then the job goes first.
func TestReaperKeepsTheVMsOfAJobBeingDeleted(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 10*time.Minute)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
			why: "disagree",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newReaperTest(t, nil)
			tc.setup(t, rt)
			rt.vm("vm-1", runLabels(t, run), 10*time.Hour)
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
	rt.cf.AddInstanceCreated("other-region-a", "vm-elsewhere", runLabels(t, "20260927-100000-abcd"), rt.now.Add(-3*time.Hour))
	// And one of its own, which must still be reaped: the region check must
	// refuse the other region, not stop the pass.
	rt.vm("vm-here", runLabels(t, "20260927-100001-abcd"), 3*time.Hour)
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
	stuck := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.vm("vm-1", runLabels(t, "20260927-100002-abcd"), 3*time.Hour)
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
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	t.Run("a deletion just started on an old job", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// Batch's own 60-day job delete (design §9) puts an old job into
		// DELETION_IN_PROGRESS moments before the pass reads it.
		rt.addJobStateAt(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 60*24*time.Hour, 2*time.Minute)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
		rt.addJobStateAt(t, "20260927-100000-abcd", "SCHEDULED", 34*time.Hour, time.Second)
		if got := rt.bf.Deletes(); len(got) != 0 {
			t.Fatalf("a just-scheduled job was deleted: %v", got)
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
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	t.Run("a deletion whose time in state is unknown", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		// The same posture in the other state a delete decision rests on: a
		// deletion already under way, with nothing that dates it. The job is
		// old, so an age rule would call it stuck; the reaper refuses it
		// instead, and leaves its VM alone.
		id := rt.addJob(t, "20260927-100000-abcd", "DELETION_IN_PROGRESS", 60*24*time.Hour)
		rt.bf.SetStatusEvents(id)
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
	t.Run("runDuration is the fallback", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
		rt.bf.SetStatusEvents(id)
		rt.bf.SetRunDuration(id, "600m")
		if got := rt.reap(t).Actions; len(got) != 1 || got[0].Kind != ActionDeleteJob {
			t.Fatalf("actions = %v, want the job deleted: runDuration says it has been running for 10 hours", rt.reap(t).Lines())
		}
	})
	t.Run("a task-level event is not when the job changed state", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
		// Only a task-level event, and it is recent: the job has been RUNNING
		// for ten hours whatever a task inside it did a minute ago.
		rt.bf.SetStatusEvents(id, map[string]any{
			"type": "TASK_STATE_CHANGED", "taskState": "RUNNING",
			"eventTime": rt.now.Add(-time.Minute).Format(time.RFC3339Nano),
		})
		rt.bf.SetRunDuration(id, "600m")
		if got := rt.reap(t).Actions; len(got) != 1 || got[0].Kind != ActionDeleteJob {
			t.Fatalf("actions = %v, want the job deleted by its runDuration", rt.reap(t).Lines())
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

// TestReaperDeletesTheJobBeforeTheInstance is the ordering rule of §3.4: a
// stuck job is deleted in the job pass, and no instance is ever deleted
// underneath a job that still exists -- and a VM behind a job this pass deleted
// gets one pass of grace, because Batch tears a deleted job's VMs down itself
// and deleting one in the same pass races that teardown. The next pass, which
// is what a scheduled reaper runs, reaps what Batch left behind.
func TestReaperDeletesTheJobBeforeTheInstance(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)

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
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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

// TestReaperToleratesADeleteOfSomethingAlreadyGone: both deletes are 404 when
// the resource is gone -- someone else's reaper, a hand-delete, Batch's own
// teardown finishing between the listing and the call. A pass must read that as
// success (the promise idempotence rests on), not as a failure that ends the
// pass with an error, and not as an action it did not take.
func TestReaperToleratesADeleteOfSomethingAlreadyGone(t *testing.T) {
	t.Run("jobs.delete", func(t *testing.T) {
		rt := newReaperTest(t, nil)
		id := rt.addJob(t, "20260927-100000-abcd", "RUNNING", 10*time.Hour)
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
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
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
		rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
		rt.cf.FailDeleteWith = http.StatusForbidden
		if _, err := rt.r.Reap(context.Background()); err == nil {
			t.Fatal("a forbidden instances.delete was tolerated: want an error")
		}
	})
}

// TestReaperDeadlineBoundaries pins the `<=`: a job exactly at a deadline is
// inside it, not past it. Off by one here is off by one run: the queue
// deadline cancels, and the run deadline deletes, so a boundary read the wrong
// way acts a second (or a pass) early on something that was still legitimate.
func TestReaperDeadlineBoundaries(t *testing.T) {
	rt := newReaperTest(t, nil)
	// Exactly at the 32-minute queue deadline: still waiting, not cancelled.
	at := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 30*time.Minute+backend.TaskTimeoutSlack)
	// One second past it: cancelled.
	past := rt.addJob(t, "20260927-100001-abcd", "QUEUED", 30*time.Minute+backend.TaskTimeoutSlack+time.Second)
	// Exactly at the run budget (30m task + 2m slack + 15m margin) in state:
	// still live. One second past it: stuck.
	live := rt.addJob(t, "20260927-100002-abcd", "RUNNING", rt.r.RunBudget())
	stuck := rt.addJob(t, "20260927-100003-abcd", "RUNNING", rt.r.RunBudget()+time.Second)
	rep := rt.reap(t)
	if got := rt.bf.Cancels(); len(got) != 1 || got[0] != past {
		t.Fatalf("jobs.cancel = %v, want only [%s] (the job exactly at the deadline is inside it)", got, past)
	}
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != stuck {
		t.Fatalf("jobs.delete = %v, want only [%s]", got, stuck)
	}
	if strings.Contains(strings.Join(rep.Lines(), "\n"), at) || strings.Contains(strings.Join(rep.Lines(), "\n"), live) {
		t.Fatalf("report = %v, want neither job that was exactly at its deadline", rep.Lines())
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

// TestReaperToleratesACancelThatRacesTheJobsOwnEnd: the job was QUEUED in the
// listing the pass decided from and had finished by the time the cancel
// arrived -- the API refuses a cancel of a terminal job, FAILED_PRECONDITION
// in the fake, UNDOCUMENTED in the real one (design §9, Check 57a step 7). The
// pass must read the job back rather than fail on it, exactly as Cancel does
// (§2.3, live check 5), and must leave its record alone.
func TestReaperToleratesACancelThatRacesTheJobsOwnEnd(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
	// The race: the job reaches a terminal state as the cancel arrives, which
	// is the only way this branch is reachable at all (a listing and a cancel
	// read the same stored state otherwise).
	rt.bf.CancelRace = map[string]string{id: "SUCCEEDED"}
	// An old VM of the run, so the age floor cannot be what saves it.
	rt.vm("vm-1", runLabels(t, "20260927-100000-abcd"), 10*time.Hour)
	rep := rt.reap(t)
	if len(rt.bf.Cancels()) != 0 {
		t.Fatalf("a cancel that raced the job's own end was recorded: %v", rt.bf.Cancels())
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none (the job had finished by itself)", rep.Lines())
	}
	if len(rt.bf.Deletes()) != 0 {
		t.Fatalf("the reaper deleted a job that finished on its own: %v", rt.bf.Deletes())
	}
	if got := rt.bf.IDs(); len(got) != 1 || got[0] != id {
		t.Fatalf("jobs = %v, want the record of %s left behind", got, id)
	}
	if st := rt.bf.State(id); st != "SUCCEEDED" {
		t.Fatalf("the raced job's state = %q, want the state it reached on its own", st)
	}
	// Its VM is left alone by this pass: the run was cancelled, and a cancel
	// does not clear a run (the next pass reads the job as finished and reaps
	// the VM then). Deleting it here would race Batch's own teardown.
	if got := rt.cf.Deletes(); len(got) != 0 {
		t.Fatalf("a cancelled run's VM was deleted in the same pass: %v", got)
	}
	if !refusedBecause(rep, "fake-region-a/vm-1", "cancelled") {
		t.Fatalf("report = %v, want the cancelled run's VM refused", rep.Lines())
	}
}

// TestReaperCancelOfAQueuedJobThatFails: a cancel the API refuses for a reason
// that is not the race -- a missing permission, say -- is a real failure. The
// pass must say so rather than read a refused cancel as a cancel, and the job
// must be left where it was.
func TestReaperCancelOfAQueuedJobThatFails(t *testing.T) {
	rt := newReaperTest(t, nil)
	id := rt.addJob(t, "20260927-100000-abcd", "QUEUED", 40*time.Minute)
	// Only the cancel is refused: the listing and the read-back it falls back
	// on still answer, so the failure is the cancel's and not the listing's.
	rt.bf.RefusePath(id+":cancel", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "no batch.jobs.cancel")
	rep, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap with a cancel the API refused: want an error")
	}
	if !strings.Contains(err.Error(), "batch.jobs.cancel") {
		t.Fatalf("error = %v, want it to name the permission the pass needed", err)
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("actions = %v, want none: nothing was cancelled", rep.Lines())
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
// and one shorter than a queue wait plus a run is a deadline that would delete
// live runs -- the exact failure the queue deadline and the run budget exist
// to avoid.
func TestReaperRefusesDeadlinesItCannotHonour(t *testing.T) {
	base := 30 * time.Minute
	slack := backend.TaskTimeoutSlack
	queueDefault := base + slack
	floor := queueDefault + base + slack // queue deadline plus a run
	for _, tc := range []struct {
		name  string
		tweak func(*ReaperOptions)
	}{
		{"a negative queue deadline", func(o *ReaperOptions) { o.QueueDeadline = -time.Minute }},
		{"a negative maximum age", func(o *ReaperOptions) { o.MaxAge = -time.Minute }},
		{"a queue deadline longer than the reaper acts on", func(o *ReaperOptions) { o.QueueDeadline = MaxDeadline + time.Second }},
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
