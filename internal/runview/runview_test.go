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
		{"old status, execution still running", withPR(runstore.StatusRunning, at(2*time.Hour)), backend.StateRunning, true},
		{"old status of an old record without the time", withPR(runstore.StatusRunning, nil), backend.StateRunning, false},
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
	r := withPR(runstore.StatusRunning, at(2*time.Hour))
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
