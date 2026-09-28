package runview

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

var (
	now    = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	flat   = backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0}
	prices = func(string) backend.Prices { return flat }
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
// garbage-collected. It must settle, so ls --watch can stop.
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

// Compute is priced in the execution's own region, not the local config's.
func TestJoinPricesTheExecutionsRegion(t *testing.T) {
	e := exec(backend.StateSucceeded)
	e.Name = "projects/p/locations/europe-west2/jobs/j/executions/j-1"
	var asked []string
	book := func(region string) backend.Prices {
		asked = append(asked, region)
		if region == "europe-west2" {
			return backend.Prices{VCPUSecondUSD: 1}
		}
		return flat
	}
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: e}, book, now)
	if want := 4 * 1800.0; row.Cost.ComputeUSD != want {
		t.Fatalf("compute = %v (asked %v), want %v", row.Cost.ComputeUSD, asked, want)
	}
}
