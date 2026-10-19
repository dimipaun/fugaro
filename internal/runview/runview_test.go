package runview

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

var (
	now    = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	flat   = backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0}
	prices = func(*backend.Execution) backend.Prices { return flat }
	spec   = &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Batch: "b1", RequestedBy: "me@example.com"}
	launch = &runstore.Launch{Execution: "exec-1", LogURL: "https://log", LaunchedAt: now.Add(-time.Minute)}
)

func rec(status runstore.Status, cost *runstore.Cost) *runstore.Record {
	return &runstore.Record{Status: status, Stage: "implement", CostUSD: 2, Cost: cost}
}

func exec(state backend.State) *backend.Execution {
	e := &backend.Execution{Name: "exec-1", State: state, Started: now.Add(-time.Hour), CPU: 4, MemoryGiB: 8}
	if state.Terminal() {
		e.Completed = now.Add(-30 * time.Minute)
	}
	return e
}

func TestJoinStatuses(t *testing.T) {
	sub := runstore.NewCost(2, 0.1, runstore.BasisSubscription)
	cases := []struct {
		name   string
		in     Input
		status string
	}{
		{"unlaunched", Input{Task: spec}, StatusUnlaunched},
		{"cancelled before launch", Input{Task: spec, CancelMarker: true}, "cancelled"},
		{"launched, not started", Input{Task: spec, Launch: launch, Exec: exec(backend.StatePending)}, StatusPending},
		{"launched, unknown to backend", Input{Task: spec, Launch: launch}, StatusPending},
		{"running", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, &sub), Exec: exec(backend.StateRunning)}, "running"},
		{"finished", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &sub), Exec: exec(backend.StateSucceeded)}, "succeeded"},
		{"draft", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusFailed, &sub), Exec: exec(backend.StateSucceeded)}, "failed"},
	}
	for _, tc := range cases {
		if got := Join(tc.in, prices, now); got.Status != tc.status {
			t.Errorf("%s: status %s, want %s", tc.name, got.Status, tc.status)
		}
	}
}

func TestJoinLaunchingClaim(t *testing.T) {
	fresh := &runstore.Claim{Holder: "laptop/1/1", At: now.Add(-time.Minute)}
	row := Join(Input{Task: spec, Claim: fresh}, prices, now)
	if row.Status != StatusLaunching || row.Settled {
		t.Fatalf("fresh claim: %+v", row)
	}
	stale := &runstore.Claim{Holder: "laptop/1/1", At: now.Add(-runstore.ClaimTTL - time.Second)}
	row = Join(Input{Task: spec, Claim: stale}, prices, now)
	if row.Status != StatusUnlaunched || !row.Settled {
		t.Fatalf("stale claim: %+v (--watch must be able to stop)", row)
	}
	if row := Join(Input{Task: spec}, prices, now); row.Status != StatusUnlaunched || !row.Settled {
		t.Fatalf("no claim: %+v", row)
	}
}

func TestJoinRunningPastDeadlineWithoutExecution(t *testing.T) {
	past := now.Add(-time.Minute)
	r := rec(runstore.StatusRunning, nil)
	r.Deadline = &past
	row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now)
	if row.Status != "infra_error" || !row.Terminal {
		t.Fatalf("row = %+v", row)
	}
	future := now.Add(time.Hour)
	r.Deadline = &future
	if row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now); row.Status != "running" {
		t.Fatalf("before the deadline: %+v", row)
	}
}

func TestJoinExecutionEndedWithoutFinalizing(t *testing.T) {
	for _, state := range []backend.State{backend.StateFailed, backend.StateSucceeded} {
		row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil), Exec: exec(state)}, prices, now)
		if row.Status != "infra_error" || !strings.Contains(row.Reason, ReasonNoFinalRecord) || !row.Terminal {
			t.Errorf("%s: row = %+v", state, row)
		}
	}
	row := Join(Input{Task: spec, Launch: launch, Exec: exec(backend.StateCancelled)}, prices, now)
	if row.Status != "cancelled" {
		t.Errorf("cancelled execution without a record: %+v", row)
	}
}

func TestJoinCostUsesExecutionAndBasis(t *testing.T) {
	sub := runstore.NewCost(2, 0, runstore.BasisSubscription)
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &sub), Exec: exec(backend.StateSucceeded)}, prices, now)
	// 30 minutes × 4 vCPU × $0.00001 = $0.072 compute; model $2 notional.
	if row.Cost.ModelBasis != runstore.BasisSubscription || row.Cost.ComputeUSD < 0.0719 || row.Cost.ComputeUSD > 0.0721 || row.Cost.TotalUSD != 0.07 {
		t.Fatalf("cost = %+v", row.Cost)
	}
	api := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: exec(backend.StateSucceeded)}, prices, now)
	tot := Sum([]Row{row, api})
	if tot.Runs != 2 || tot.ModelNotionalUSD != 2 || tot.ModelUSD != 2 || math.Abs(tot.TotalUSD-(row.Cost.TotalUSD+api.Cost.TotalUSD)) > 0.005 {
		t.Fatalf("totals = %+v", tot)
	}
}

// A running record with no deadline and no execution the backend knows
// can never finish on its own: infra_error.
func TestJoinRunningWithoutDeadlineOrExecution(t *testing.T) {
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil)}, prices, now)
	if row.Status != "infra_error" || !row.Settled {
		t.Fatalf("row = %+v", row)
	}
}

// Without the execution, compute comes from the record, estimated or not.
func TestJoinCostWithoutExecution(t *testing.T) {
	stored := runstore.NewCost(2, 0.5, runstore.BasisAPIList)
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &stored)}, prices, now)
	if row.Cost.ComputeUSD != 0.5 || !row.Cost.ComputeEstimated || row.Cost.TotalUSD != 2.5 {
		t.Fatalf("stored cost: %+v", row.Cost)
	}
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil)}, prices, now)
	if row.Cost.ComputeEstimated || row.Cost.ModelBasis != runstore.BasisAPIList || row.Cost.TotalUSD != 2 {
		t.Fatalf("no stored cost: %+v", row.Cost)
	}
	e := exec(backend.StateSucceeded)
	e.CPU, e.MemoryGiB = 0, 0 // resources unknown: the stored cost stands
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &stored), Exec: e}, prices, now)
	if row.Cost.ComputeUSD != 0.5 || !row.Cost.ComputeEstimated {
		t.Fatalf("unknown resources: %+v", row.Cost)
	}
}

// The record's execution wins over launch.json's, which may name a
// duplicate after a double launch.
func TestJoinPrefersRecordExecution(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.Execution = "exec-owner"
	if row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now); row.Execution != "exec-owner" {
		t.Fatalf("execution = %q", row.Execution)
	}
}

// A run whose objects can't be followed is an error row: settled, not
// terminal, and it names no execution.
func TestJoinProblemIsAnErrorRow(t *testing.T) {
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil), Exec: exec(backend.StateRunning), Problem: "forged"}, prices, now)
	if row.Status != StatusError || row.Reason != "forged" || row.Terminal || !row.Settled || row.Execution != "" {
		t.Fatalf("row = %+v", row)
	}
}

// A launched run that never wrote a record, and whose execution the
// backend doesn't know, is lost once the claim TTL has passed: it failed
// before the runner started (an image pull, a crash at start) and was
// garbage-collected. It must settle, so runs ls --watch can stop.
func TestJoinLaunchedRunLostWithoutRecord(t *testing.T) {
	old := *launch
	old.LaunchedAt = now.Add(-runstore.ClaimTTL - time.Second)
	row := Join(Input{Task: spec, Launch: &old}, prices, now)
	if row.Status != "infra_error" || !row.Terminal || !row.Settled || !strings.Contains(row.Reason, "never recorded") {
		t.Fatalf("lost run: %+v", row)
	}
	// Without a launch time, the run ID's time bounds it.
	old.LaunchedAt = time.Time{}
	if row := Join(Input{RunID: spec.RunID, Task: spec, Launch: &old}, prices, now); row.Status != "infra_error" {
		t.Fatalf("lost run without launched_at: %+v", row)
	}
	// Within the TTL the backend may just not list it yet.
	if row := Join(Input{Task: spec, Launch: launch}, prices, now); row.Status != StatusPending || row.Settled {
		t.Fatalf("young run: %+v", row)
	}
}

// Compute is priced with the execution's own region and machine type, which
// are the backend's answers: runview hands the execution to the price book
// rather than parsing the name, so a platform whose name has no region in it
// (a Batch job) is priced as well as Cloud Run's (design compute-backend.md
// §5.2).
func TestJoinPricesWithTheExecutionsOwnRegion(t *testing.T) {
	e := exec(backend.StateSucceeded)
	e.Name = "projects/p/locations/europe-west2/jobs/j/executions/j-1"
	e.Region = "europe-west2"
	var asked []*backend.Execution
	book := func(x *backend.Execution) backend.Prices {
		asked = append(asked, x)
		if x != nil && x.Region == "europe-west2" {
			return backend.Prices{VCPUSecondUSD: 1}
		}
		return flat
	}
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: e}, book, now)
	if want := 4 * 1800.0; row.Cost.ComputeUSD != want {
		t.Fatalf("compute = %v (asked %v), want %v", row.Cost.ComputeUSD, asked, want)
	}
}

// A VM-backed execution is billed for its machine type from its boot window,
// with Compute Engine's one-minute minimum, and its estimate records the
// table it came from (design compute-backend.md §5.2, C17).
func TestJoinPricesAVMRunFromItsBootWindow(t *testing.T) {
	vm := backend.Prices{VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6, VCPU: 4, MemoryGiB: 16,
		Source: "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"}
	book := func(x *backend.Execution) backend.Prices {
		if x == nil || x.MachineType != "e2-standard-4" || x.Region != "us-central1" {
			t.Errorf("price book asked for %+v, want the VM's own type and region", x)
		}
		return vm
	}
	// The execution's own limits are the workflow's resources, which are not
	// what Compute Engine bills: the machine type's quantities are, and they
	// travel with its prices.
	e := &backend.Execution{Name: "projects/p/locations/us-central1/jobs/fugaro-j-1", State: backend.StateSucceeded,
		Region: "us-central1", MachineType: "e2-standard-4", CPU: 2, MemoryGiB: 8,
		Created: now.Add(-40 * time.Second), Started: now.Add(-10 * time.Second), Completed: now.Add(-5 * time.Second)}
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: e}, book, now)
	// 35 s from the boot window, floored to the 60 s Compute Engine bills,
	// over the e2-standard-4's 4 vCPU / 16 GiB and not the workflow's 2/8.
	if want := 60 * (4*1e-5 + 16*2e-6); row.Cost.ComputeUSD != want {
		t.Errorf("compute = %v, want %v (a one-minute floor over the boot window, at the machine type's quantities)", row.Cost.ComputeUSD, want)
	}
	if !row.Cost.ComputeEstimated {
		t.Error("compute is not estimated")
	}
	// The line carries the table's own words and, because this execution
	// reported no scheduled time, says which window it priced.
	if want := vm.Source + "; " + e.WindowNote(); row.Cost.ComputeSource != want {
		t.Errorf("compute source = %q, want %q", row.Cost.ComputeSource, want)
	}
	if !strings.Contains(row.Cost.ComputeSource, "window starts at job creation") {
		t.Errorf("compute source = %q, want it to say the window started at creation", row.Cost.ComputeSource)
	}

	// A backend that reports the scheduled time gets the window from there:
	// 295 s from scheduling to completion, not creation's 35 s floored to 60,
	// and the line says so instead of naming queue time.
	scheduled := *e
	scheduled.Scheduled = now.Add(-5 * time.Minute)
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: &scheduled}, book, now)
	if want := 295 * (4*1e-5 + 16*2e-6); math.Abs(row.Cost.ComputeUSD-want) > 1e-12 {
		t.Errorf("compute = %v, want %v (the scheduled window, not creation's 40 s)", row.Cost.ComputeUSD, want)
	}
	if !strings.Contains(row.Cost.ComputeSource, "window starts at the job's scheduled time") {
		t.Errorf("compute source = %q, want it to name the scheduled window", row.Cost.ComputeSource)
	}
	if strings.Contains(row.Cost.ComputeSource, "queue time") {
		t.Errorf("compute source = %q names queue time although the window is the scheduled one", row.Cost.ComputeSource)
	}
	// Cloud Run's window and its record stay what they were: no source line,
	// and the window from Started.
	cloud := &backend.Execution{Name: "projects/p/locations/us-central1/jobs/j/executions/j-1", State: backend.StateSucceeded,
		Region: "us-central1", CPU: 4, MemoryGiB: 16,
		Created: now.Add(-40 * time.Second), Started: now.Add(-10 * time.Second), Completed: now.Add(-5 * time.Second)}
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: cloud}, prices, now)
	if want := 5 * flat.VCPUSecondUSD * 4; row.Cost.ComputeUSD != want {
		t.Errorf("Cloud Run compute = %v, want %v (start to completion, no floor)", row.Cost.ComputeUSD, want)
	}
	if row.Cost.ComputeSource != "" {
		t.Errorf("Cloud Run compute source = %q, want none", row.Cost.ComputeSource)
	}
}

// A VM whose boot window the backend has not reported, and an execution no
// price table carries, are both "not estimated": never $0.00, which a reader
// could not tell from free (design compute-backend.md §5.2).
func TestJoinLeavesWhatItCannotPriceUnestimated(t *testing.T) {
	e := &backend.Execution{Name: "projects/p/locations/us-central1/jobs/fugaro-j-1", State: backend.StateRunning,
		Region: "us-central1", MachineType: "e2-standard-4", CPU: 4, MemoryGiB: 16, Started: now.Add(-10 * time.Minute)}
	if row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil), Exec: e}, prices, now); row.Cost.ComputeEstimated || row.Cost.ComputeUSD != 0 {
		t.Errorf("a VM with no boot window: compute = %v estimated=%v, want not estimated", row.Cost.ComputeUSD, row.Cost.ComputeEstimated)
	}
	books := map[string]backend.Prices{
		"no table": {},
		// A book that names itself but bills nothing: "compute $0.00,
		// estimate" is as unreadable as a free run, and only a test that
		// hands one over reaches the check (the config refuses a zero
		// override, localcfg.ValidPrice).
		"a table whose rates are zero": {Source: "a table that prices nothing"},
	}
	for name, p := range books {
		known := func(*backend.Execution) backend.Prices { return p }
		row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil), Exec: e}, known, now)
		if row.Cost.ComputeEstimated || row.Cost.ComputeSource != "" {
			t.Errorf("%s: compute = %v estimated=%v source=%q, want not estimated and no source line",
				name, row.Cost.ComputeUSD, row.Cost.ComputeEstimated, row.Cost.ComputeSource)
		}
	}
}

// A VM run whose execution reports no CPU/memory limits at all is priced
// from its machine type: the type's quantities are what Compute Engine bills,
// and the Batch backend sets no limits on the execution (#381). Requiring
// e.CPU or e.MemoryGiB would leave every VM run on the model-only fallback,
// reading "compute not estimated" for a run whose compute is known (§5.2).
func TestJoinPricesAVMRunWithNoResourceLimits(t *testing.T) {
	vm := backend.Prices{VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6, VCPU: 4, MemoryGiB: 16,
		Source: "Compute Engine VM list price, e2-standard family, us-east5 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"}
	book := func(*backend.Execution) backend.Prices { return vm }
	e := &backend.Execution{Name: "projects/p/locations/us-east5/jobs/fugaro-j-1", State: backend.StateSucceeded,
		Region: "us-east5", MachineType: "e2-standard-4",
		Created: now.Add(-40 * time.Second), Completed: now.Add(-5 * time.Second)}
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: e}, book, now)
	if want := 60 * (4*1e-5 + 16*2e-6); row.Cost.ComputeUSD != want {
		t.Errorf("compute = %v, want %v (the machine type's quantities over the floored window)", row.Cost.ComputeUSD, want)
	}
	if !row.Cost.ComputeEstimated || row.Cost.ComputeSource == "" {
		t.Errorf("compute estimated=%v source=%q, want an estimate with its source line", row.Cost.ComputeEstimated, row.Cost.ComputeSource)
	}
	// An execution with neither a machine type nor limits is still not
	// priced: Cloud Run's quantities are the workflow's, and nothing
	// reported them.
	cloud := &backend.Execution{Name: "exec-1", State: backend.StateSucceeded, Region: "us-east5",
		Started: now.Add(-10 * time.Second), Completed: now.Add(-5 * time.Second)}
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: cloud}, book, now)
	if row.Cost.ComputeEstimated {
		t.Errorf("an execution with no quantities and no machine type is priced as estimated: %v", row.Cost.ComputeUSD)
	}
}

// followSpec is a follow-up task on PR 7 of the branch run 20260927-090000-aaaa opened.
var followSpec = &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
	Branch: "fugaro/20260927-090000-aaaa", PR: 7, PreviousRun: "20260927-090000-aaaa"}

func TestJoinFollowUpFields(t *testing.T) {
	started := now.Add(-time.Hour)
	// A follow-up with a record: the record's PR and branch, pushed.
	r := rec(runstore.StatusSucceeded, nil)
	r.Outcome, r.Branch, r.StartedAt = runstore.OutcomeReady, followSpec.Branch, started
	r.PR = &runstore.PRRef{Number: 7, URL: "https://example.invalid/pr/7"}
	r.PushedHead, r.HeadSHA = "1111111111111111111111111111111111111111", "1111111111111111111111111111111111111111"
	r.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: followSpec.PreviousRun}
	row := Join(Input{Task: followSpec, Launch: launch, Record: r}, prices, now)
	if row.Branch != followSpec.Branch || row.Outcome != "ready" || row.TaskPR != 7 || row.RecordPR != 7 || row.PR != 7 ||
		!row.Pushed || row.PushedHead != r.PushedHead || !row.FollowUp || row.PreviousRun != followSpec.PreviousRun ||
		row.StartedAt == nil || !row.StartedAt.Equal(started) {
		t.Fatalf("follow-up with a record: %+v", row)
	}

	// Only the task so far: the task's branch and PR, nothing pushed.
	row = Join(Input{Task: followSpec}, prices, now)
	if row.Branch != followSpec.Branch || row.Outcome != "" || row.TaskPR != 7 || row.RecordPR != 0 || row.PR != 7 ||
		row.Pushed || row.PushedHead != "" || !row.FollowUp || row.PreviousRun != followSpec.PreviousRun || row.StartedAt != nil {
		t.Fatalf("task-only follow-up: %+v", row)
	}

	// A follow-up whose record got its PR at bootstrap but never pushed has
	// not updated the PR.
	early := rec(runstore.StatusInfraError, nil)
	early.PR, early.HeadSHA = &runstore.PRRef{Number: 7}, "2222222222222222222222222222222222222222"
	early.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: followSpec.PreviousRun}
	if row := Join(Input{Task: followSpec, Launch: launch, Record: early}, prices, now); row.Pushed || row.PushedHead != "" {
		t.Fatalf("an unpushed follow-up counts as pushed: %+v", row)
	}
	early.FollowUp = nil // even when the refusal came before the block was set
	if row := Join(Input{Task: followSpec, Launch: launch, Record: early}, prices, now); row.Pushed {
		t.Fatalf("an unpushed follow-up task counts as pushed: %+v", row)
	}

	// A first run: its own branch and PR, no task PR, not a follow-up.
	first := rec(runstore.StatusFailed, nil)
	first.Outcome, first.Branch = runstore.OutcomeDraft, "fugaro/"+spec.RunID
	first.PR, first.PushedHead = &runstore.PRRef{Number: 7}, "3333333333333333333333333333333333333333"
	row = Join(Input{Task: spec, Launch: launch, Record: first}, prices, now)
	if row.Branch != first.Branch || row.Outcome != "draft" || row.TaskPR != 0 || row.RecordPR != 7 || row.PR != 7 ||
		!row.Pushed || row.PushedHead != first.PushedHead || row.FollowUp || row.PreviousRun != "" {
		t.Fatalf("first run: %+v", row)
	}

	// An error row keeps what its task says, so a PR's listing still shows it.
	row = Join(Input{Task: followSpec, Launch: launch, Problem: "result.json is unreadable"}, prices, now)
	if row.Status != StatusError || row.TaskPR != 7 || row.PR != 7 || !row.FollowUp || row.Pushed {
		t.Fatalf("error row: %+v", row)
	}
}

// A record written before follow-ups existed has no pushed_head; its PR
// exists only because its push succeeded, so it has pushed its head_sha.
func TestJoinPushedPreM6(t *testing.T) {
	old := rec(runstore.StatusSucceeded, nil)
	old.PR, old.HeadSHA = &runstore.PRRef{Number: 3, URL: "https://example.invalid/pr/3"}, "4444444444444444444444444444444444444444"
	row := Join(Input{Task: spec, Launch: launch, Record: old}, prices, now)
	if !row.Pushed || row.PushedHead != old.HeadSHA || row.PR != 3 {
		t.Fatalf("pre-M6 record with a PR: %+v", row)
	}
	neither := rec(runstore.StatusInfraError, nil)
	neither.HeadSHA = "5555555555555555555555555555555555555555"
	if row := Join(Input{Task: spec, Launch: launch, Record: neither}, prices, now); row.Pushed || row.PushedHead != "" {
		t.Fatalf("a record with neither a PR nor pushed_head: %+v", row)
	}
	if row := Join(Input{Task: spec}, prices, now); row.Pushed {
		t.Fatalf("no record: %+v", row)
	}
	// A PR without a head_sha names no commit: not pushed.
	headless := rec(runstore.StatusSucceeded, nil)
	headless.PR = &runstore.PRRef{Number: 3}
	if head, ok := Pushed(spec, headless); ok || head != "" {
		t.Fatalf("a PR without a head_sha: %q, %v", head, ok)
	}
}

// Without its task (task.json unreadable, or a caller holding only the
// record), the record's branch tells a follow-up from a first run: a first
// run's branch always names its own run ID.
func TestPushedWithoutTask(t *testing.T) {
	const head = "6666666666666666666666666666666666666666"
	fu := &runstore.Record{RunID: followSpec.RunID, Branch: followSpec.Branch, HeadSHA: head, PR: &runstore.PRRef{Number: 7}}
	if h, ok := Pushed(nil, fu); ok || h != "" {
		t.Fatalf("a follow-up record with a PR and no block yet: %q, %v", h, ok)
	}
	first := &runstore.Record{RunID: spec.RunID, Branch: "fugaro/" + spec.RunID, HeadSHA: head, PR: &runstore.PRRef{Number: 7}}
	if h, ok := Pushed(nil, first); !ok || h != head {
		t.Fatalf("a first run's record: %q, %v", h, ok)
	}
	fu.PushedHead = head
	if h, ok := Pushed(nil, fu); !ok || h != head {
		t.Fatalf("a follow-up that pushed: %q, %v", h, ok)
	}
}

func TestJoinHalted(t *testing.T) {
	h := &runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run", At: now, Detail: "run used 110 tokens of 100"}
	r := rec(runstore.StatusHalted, nil)
	r.Halt, r.Reason = h, "halted: token_cap: run used 110 tokens of 100"
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.Status != "halted" || !row.Terminal || !row.Settled || row.Halt == nil || *row.Halt != *h || row.Reason != r.Reason {
		t.Fatalf("row = %+v", row)
	}
	// A run that was not halted has no halt.
	if row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusFailed, nil), Exec: exec(backend.StateSucceeded)}, prices, now); row.Halt != nil {
		t.Fatalf("halt = %+v", row.Halt)
	}
}

// A halted run whose finalize then failed is an infra_error that still says
// what halted it: the halt is shown whatever the status.
func TestJoinInfraErrorKeepsHalt(t *testing.T) {
	h := &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: now, Detail: "run cap $1.00 reached"}
	r := rec(runstore.StatusInfraError, nil)
	r.Halt = h
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.Status != "infra_error" || row.Halt == nil || *row.Halt != *h {
		t.Fatalf("row = %+v", row)
	}
}

func withDeadline(r *runstore.Record, d time.Time) *runstore.Record {
	r.Deadline = &d
	return r
}

func TestJoinStaleDraft(t *testing.T) {
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	withPR := func(st runstore.Status, statusAt *time.Time) *runstore.Record {
		r := rec(st, nil)
		r.PR = &runstore.PRRef{Number: 7, URL: "https://h/pr/7", StatusAt: statusAt}
		return r
	}
	cases := []struct {
		name  string
		r     *runstore.Record
		state backend.State
		want  bool
	}{
		{"fresh status", withPR(runstore.StatusRunning, at(5*time.Minute)), backend.StateRunning, false},
		{"2h old status, no deadline, execution alive", withPR(runstore.StatusRunning, at(2*time.Hour)), backend.StateRunning, false},
		{"old status of an old record without the time", withPR(runstore.StatusRunning, nil), backend.StateRunning, false},
		{"healthy long stage: status 2h old, execution alive, deadline ahead", withDeadline(withPR(runstore.StatusRunning, at(2*time.Hour)), now.Add(time.Hour)), backend.StateRunning, false},
		{"status 5h old, no deadline, execution alive", withPR(runstore.StatusRunning, at(5*time.Hour)), backend.StateRunning, true},
		{"past the deadline, execution alive", withDeadline(withPR(runstore.StatusRunning, at(time.Minute)), now.Add(-2*time.Minute)), backend.StateRunning, true},
		{"execution gone, record not final", withPR(runstore.StatusRunning, at(time.Minute)), backend.StateFailed, true},
		{"finished run, old status", withPR(runstore.StatusFailed, at(5*time.Hour)), backend.StateSucceeded, false},
		{"no PR", rec(runstore.StatusRunning, nil), backend.StateFailed, false},
	}
	for _, c := range cases {
		row := Join(Input{Task: spec, Launch: launch, Record: c.r, Exec: exec(c.state)}, prices, now)
		if row.StaleDraft != c.want {
			t.Errorf("%s: stale_draft = %v, want %v (%+v)", c.name, row.StaleDraft, c.want, row)
		}
	}
	r := withPR(runstore.StatusRunning, at(5*time.Hour))
	r.DraftFallback = true
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateRunning)}, prices, now)
	if !row.DraftFallback || row.PRStatusAt == nil || !row.PRStatusAt.Equal(*r.PR.StatusAt) {
		t.Errorf("fallback/status_at not carried: %+v", row)
	}
	// A follow-up's PR may be ready: never called a draft.
	fr := withPR(runstore.StatusRunning, at(2*time.Hour))
	fr.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: "20260927-100000-abcd"}
	if row := Join(Input{Task: spec, Launch: launch, Record: fr, Exec: exec(backend.StateRunning)}, prices, now); row.StaleDraft {
		t.Errorf("follow-up flagged: %+v", row)
	}
}

func TestJoinRecipe(t *testing.T) {
	sub := runstore.NewCost(2, 0.1, runstore.BasisSubscription)
	r := rec(runstore.StatusSucceeded, &sub)
	r.Recipe = &runstore.RecipeRecord{Name: "claude-solo", Source: "catalog", SHA256: strings.Repeat("a", 64)}
	if got := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now); got.Recipe != "claude-solo" {
		t.Fatalf("from the record: %q", got.Recipe)
	}
	t2 := *spec
	t2.Recipe = &task.Recipe{Name: "mine", Source: "repo"}
	if got := Join(Input{Task: &t2}, prices, now); got.Recipe != "mine" {
		t.Fatalf("from the task: %q", got.Recipe)
	}
	if got := Join(Input{Task: spec}, prices, now); got.Recipe != "" {
		t.Fatalf("none: %q", got.Recipe)
	}
}

func TestJoinCarriesOAuthAccount(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.OAuthAccount = "acme-personal"
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.OAuthAccount != "acme-personal" {
		t.Fatalf("row.OAuthAccount = %q, want acme-personal", row.OAuthAccount)
	}
	if got := Join(Input{Task: spec}, prices, now); got.OAuthAccount != "" {
		t.Fatalf("no record: %q", got.OAuthAccount)
	}
}

// TestJoinOAuthAccountFallsBackToTask is named oauth tokens task 9b's third
// review round, comment 3: a run that died before finalize has no
// result.json (Record nil) at all, and an older record predates the
// OAuthAccount field entirely — both read "" exactly like a run that used
// the default account, so the skill's own tried-list seeding
// (reference/diagnose.md step 2) could start from the wrong account. The
// task the run actually launched under (task.Spec.OAuthToken, set once at
// launch and never changed afterward) is the best evidence left in either
// case.
func TestJoinOAuthAccountFallsBackToTask(t *testing.T) {
	t2 := *spec
	t2.OAuthToken = "acme-personal"

	// No record at all (died before finalize): falls back to the task.
	if got := Join(Input{Task: &t2}, prices, now); got.OAuthAccount != "acme-personal" {
		t.Fatalf("no record: OAuthAccount = %q, want acme-personal (from the task)", got.OAuthAccount)
	}
	// A record that exists but predates the field (OAuthAccount ""): falls
	// back to the task too, not left at "".
	r := rec(runstore.StatusSucceeded, nil)
	if got := Join(Input{Task: &t2, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now); got.OAuthAccount != "acme-personal" {
		t.Fatalf("pre-field record: OAuthAccount = %q, want acme-personal (from the task)", got.OAuthAccount)
	}
	// The record's own OAuthAccount, when it has one, still wins over the
	// task's (the runner's own confirmation at finalize is authoritative).
	r2 := rec(runstore.StatusSucceeded, nil)
	r2.OAuthAccount = "acme-overflow"
	if got := Join(Input{Task: &t2, Launch: launch, Record: r2, Exec: exec(backend.StateSucceeded)}, prices, now); got.OAuthAccount != "acme-overflow" {
		t.Fatalf("record wins: OAuthAccount = %q, want acme-overflow", got.OAuthAccount)
	}
	// No task and no record: still "" (nothing to fall back to).
	if got := Join(Input{}, prices, now); got.OAuthAccount != "" {
		t.Fatalf("neither: OAuthAccount = %q, want empty", got.OAuthAccount)
	}
}

func TestJoinCarriesTheProjectLayer(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.ProjectLayer = &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}
	r.ConfigSHA256 = strings.Repeat("b", 64)
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.ProjectLayer == nil || row.ProjectLayer.Generation != 3 || row.ConfigSHA256 != r.ConfigSHA256 {
		t.Fatalf("row %+v", row)
	}
}

// TestRowProjectLayerFieldsKeepTheirJSONNames pins the wire names ls --json
// and diagnose --json promise in docs/recipes.md: a rename here is a
// breaking change to that document, caught only by checking the actual
// marshaled keys rather than the Go field names.
func TestRowProjectLayerFieldsKeepTheirJSONNames(t *testing.T) {
	row := Row{
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true},
		ConfigSHA256: strings.Repeat("b", 64),
	}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["project_layer"]; !ok {
		t.Errorf("no project_layer key: %s", data)
	}
	if _, ok := m["config_sha256"]; !ok {
		t.Errorf("no config_sha256 key: %s", data)
	}
}

// TestJoinArtifactBytesCountsNoNegativeSize: the record is untrusted text, and
// a negative byte count is not a size — the row's total counts it as none
// rather than subtracting it from the artifacts that do have one. Mutation
// caught: summing a.Bytes directly.
func TestJoinArtifactBytesCountsNoNegativeSize(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.Artifacts = []runstore.ArtifactFile{
		{Name: "a.mp4", Bytes: -1 << 20},
		{Name: "b.mp4", Bytes: 10},
		{Name: "c.mp4", Bytes: 0},
	}
	row := Join(Input{Task: spec, Launch: launch, Exec: exec(backend.StateSucceeded), Record: r}, prices, now)
	if row.ArtifactCount != 3 {
		t.Errorf("artifact count = %d, want 3", row.ArtifactCount)
	}
	if row.ArtifactBytes != 10 {
		t.Errorf("artifact bytes = %d, want 10 (a negative count is not a size)", row.ArtifactBytes)
	}
}

// TestJoinLaunchedByIsTheLaunchRecordsOwn: the launcher is what the run's own
// launch.json says, sanitised, and nothing at all for a run whose record has
// none — never a value invented from the task, the record or the account.
// Mutation caught: leaving LaunchedBy out of Join, or filling it from
// spec.RequestedBy (which is the task's, not the launch's).
func TestJoinLaunchedByIsTheLaunchRecordsOwn(t *testing.T) {
	l := *launch
	l.LaunchedBy = "dana@example.com"
	row := Join(Input{Task: spec, Launch: &l, Record: rec(runstore.StatusSucceeded, nil), Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.LaunchedBy != "dana@example.com" {
		t.Errorf("LaunchedBy = %q, want the launch.json's own value", row.LaunchedBy)
	}
	// A launch.json with no launched_by (one written by hand, or by a
	// launcher older than the field): absent, not the task's RequestedBy.
	row = Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil)}, prices, now)
	if row.LaunchedBy != "" {
		t.Errorf("LaunchedBy = %q for a launch.json with none, want \"\"", row.LaunchedBy)
	}
	// No launch.json at all: an unlaunched run has no launcher.
	if row := Join(Input{Task: spec}, prices, now); row.LaunchedBy != "" {
		t.Errorf("unlaunched row carries LaunchedBy %q", row.LaunchedBy)
	}
}

// TestJoinLaunchedByIsSanitised: the launcher is untrusted display text, so
// what reaches the JSON (and so the console) carries no escape sequence, no
// control, no bidi override and no newline, and a 10 KB value is capped.
// Mutation caught: assigning in.Launch.LaunchedBy raw.
func TestJoinLaunchedByIsSanitised(t *testing.T) {
	cases := map[string]string{
		"ansi":    "\x1b[2Jroot",
		"osc":     "\x1b]52;c;AAAA\x07root",
		"newline": "root@x.io\nroot@y.io",
		"cr":      "root@x.io\rroot@y.io",
		"bidi":    "root" + string(rune(0x202e)) + "@x.io",
		"sep":     "root@x.io" + string(rune(0x2028)) + "root@y.io",
		"nul":     "root\x00@x.io",
		"tab":     "root\t@x.io",
	}
	for name, hostile := range cases {
		l := *launch
		l.LaunchedBy = hostile
		row := Join(Input{Task: spec, Launch: &l, Record: rec(runstore.StatusSucceeded, nil)}, prices, now)
		if strings.ContainsAny(row.LaunchedBy, "\x1b\n\r\t\x00\x07") {
			t.Errorf("%s: hostile control survived: %q", name, row.LaunchedBy)
		}
		for _, r := range row.LaunchedBy {
			if r == 0x202e || r == 0x2028 || r == 0x2029 {
				t.Errorf("%s: hostile rune %U survived: %q", name, r, row.LaunchedBy)
			}
		}
		// Sanitised, not erased: the harmless part of the value is still
		// there, so a hostile byte cannot hide an identity entirely.
		if !strings.Contains(row.LaunchedBy, "root") {
			t.Errorf("%s: the value was destroyed rather than sanitised: %q", name, row.LaunchedBy)
		}
	}
	l := *launch
	l.LaunchedBy = strings.Repeat("x", 10_000)
	row := Join(Input{Task: spec, Launch: &l, Record: rec(runstore.StatusSucceeded, nil)}, prices, now)
	if n := len([]rune(row.LaunchedBy)); n > safetext.FieldMax {
		t.Errorf("a 10 KB launcher was not capped: %d runes", n)
	}
	// And the JSON document carries exactly the sanitised value.
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["launched_by"] != row.LaunchedBy {
		t.Errorf("launched_by = %v, want %q", m["launched_by"], row.LaunchedBy)
	}
}

// TestLaunchedByIsOmittedFromTheJSONWhenAbsent: the key is absent, never
// "" or null, for a run whose launch.json recorded no launcher.
func TestLaunchedByIsOmittedFromTheJSONWhenAbsent(t *testing.T) {
	row := Join(Input{Task: spec, Launch: launch}, prices, now)
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "launched_by") {
		t.Errorf("an absent launcher still emitted the key: %s", data)
	}
}

// TestLauncherShort: a text column shows the part before the @ of an
// address and the whole value of anything else — a service account name, a
// CI identity, a value with no @ at all.
func TestLauncherShort(t *testing.T) {
	for in, want := range map[string]string{
		"dana@example.com":                           "dana",
		"dana.person+fugaro@example.com":             "dana.person+fugaro",
		"fugaro-runner@proj.iam.gserviceaccount.com": "fugaro-runner",
		"ci-runner":    "ci-runner",
		"":             "",
		"@example.com": "",
		"a@b@c":        "a",
	} {
		if got := LauncherShort(in); got != want {
			t.Errorf("LauncherShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestJoinCarriesTheRunsChangedFiles: the row carries the run's own record of
// the paths it changed (console v2 design D27), and a run that recorded none
// carries none — an absent field, never an empty set.
func TestJoinCarriesTheRunsChangedFiles(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.ChangedFiles = runstore.NewChangedFiles([]string{"a.go", "b/c.go"})
	row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now)
	if row.ChangedFiles == nil || len(row.ChangedFiles.Paths) != 2 || row.ChangedFiles.Paths[1] != "b/c.go" {
		t.Fatalf("row.ChangedFiles = %+v", row.ChangedFiles)
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"changed_files"`) {
		t.Fatalf("the row does not carry the set:\n%s", b)
	}
	plain := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil)}, prices, now)
	if plain.ChangedFiles != nil {
		t.Fatalf("a run that recorded none: %+v", plain.ChangedFiles)
	}
	b, err = json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "changed_files") {
		t.Fatalf("a run that recorded none names the field:\n%s", b)
	}
}

// TestJoinReboundsTheRecordedChangedFiles: the row does not pass a record's
// set through as written — a record carrying more than the bound is cut on
// read and flagged truncated, so `runs ls --json` never hands the console an
// unbounded list or a cut one dressed as whole.
func TestJoinReboundsTheRecordedChangedFiles(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	big := make([]string, 0, 3*runstore.MaxChangedFiles)
	for i := range 3 * runstore.MaxChangedFiles {
		big = append(big, fmt.Sprintf("f%04d", i))
	}
	r.ChangedFiles = &runstore.ChangedFiles{Paths: big, Total: len(big)}
	row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now)
	if row.ChangedFiles == nil || len(row.ChangedFiles.Paths) != runstore.MaxChangedFiles || !row.ChangedFiles.Truncated {
		t.Fatalf("an oversized record reached the row as %d paths, truncated %v",
			len(row.ChangedFiles.Paths), row.ChangedFiles.Truncated)
	}
}
