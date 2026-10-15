package backend

import (
	"testing"
	"time"
)

var vmNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// A VM is billed from its boot window, with Compute Engine's one-minute
// minimum, and a window the backend has not reported is unknown rather than
// free (design compute-backend.md §5.2).
func TestBilledWindowOnAVM(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exec      Execution
		want      time.Duration
		wantKnown bool
	}{
		{
			// The boot window is what the VM is billed for, and it is longer
			// than the run: the runner's start is later than the job's
			// creation by the whole cold start.
			name:      "the boot window, not the run",
			exec:      Execution{MachineType: "e2-standard-4", Created: vmNow.Add(-5 * time.Minute), Started: vmNow.Add(-4 * time.Minute), Completed: vmNow.Add(-time.Minute)},
			want:      4 * time.Minute,
			wantKnown: true,
		},
		{
			// Compute Engine bills a minimum of one minute: a 40 s VM costs
			// a full minute.
			name:      "the one-minute floor",
			exec:      Execution{MachineType: "e2-standard-4", Created: vmNow.Add(-40 * time.Second), Started: vmNow.Add(-10 * time.Second)},
			want:      VMMinimumBilled,
			wantKnown: true,
		},
		{
			// A VM that has not started its container is still billing, and
			// its window is known: Started being zero is not "free".
			name:      "booting, not started",
			exec:      Execution{MachineType: "e2-standard-4", Created: vmNow.Add(-3 * time.Minute)},
			want:      3 * time.Minute,
			wantKnown: true,
		},
		{
			// The scheduled time is where Compute Engine starts
			// provisioning, so it and not creation starts the window
			// (design §5.2): 6 minutes from scheduling against creation's
			// 8, which would count Batch's queue time as VM time.
			name:      "the scheduled time, not creation",
			exec:      Execution{MachineType: "e2-standard-4", Scheduled: vmNow.Add(-6 * time.Minute), Created: vmNow.Add(-8 * time.Minute), Started: vmNow.Add(-4 * time.Minute), Completed: vmNow.Add(-time.Minute)},
			want:      5 * time.Minute,
			wantKnown: true,
		},
		{
			name:      "no boot window reported",
			exec:      Execution{MachineType: "e2-standard-4", Started: vmNow.Add(-time.Minute)},
			wantKnown: false,
		},
	} {
		got, ok := tc.exec.BilledWindow(vmNow)
		if ok != tc.wantKnown || got != tc.want {
			t.Errorf("%s: BilledWindow = %v, %v; want %v, %v", tc.name, got, ok, tc.want, tc.wantKnown)
		}
	}
}

// WindowNote is what tells a reader which time the window started from, and
// it must say the fallback is the queue-time one: a window from creation
// includes the stretch before any VM exists and errs high (design §5.2).
func TestWindowNoteNamesWhichTimeTheWindowStartedFrom(t *testing.T) {
	vm := Execution{MachineType: "e2-standard-4", Created: vmNow.Add(-time.Minute)}
	if got, want := vm.WindowNote(), "window starts at job creation: includes queue time, errs high"; got != want {
		t.Errorf("a VM with no scheduled time: WindowNote = %q, want %q", got, want)
	}
	vm.Scheduled = vmNow.Add(-40 * time.Second)
	if got, want := vm.WindowNote(), "window starts at the job's scheduled time"; got != want {
		t.Errorf("a VM with a scheduled time: WindowNote = %q, want %q", got, want)
	}
	// Cloud Run's rows carry no window line at all: their estimate is what
	// it always was.
	cloud := Execution{Created: vmNow.Add(-time.Minute), Started: vmNow.Add(-40 * time.Second)}
	if got := cloud.WindowNote(); got != "" {
		t.Errorf("Cloud Run's WindowNote = %q, want none", got)
	}
}

// Cloud Run's window is its own: start to completion, no floor, and always
// known -- so every Cloud Run estimate already recorded is unchanged.
func TestBilledWindowIsBilledForCloudRun(t *testing.T) {
	e := Execution{Name: "projects/p/locations/us-central1/jobs/j/executions/j-1", Started: vmNow.Add(-40 * time.Second), Completed: vmNow.Add(-10 * time.Second)}
	if got, ok := e.BilledWindow(vmNow); got != 30*time.Second || !ok {
		t.Errorf("BilledWindow = %v, %v; want Billed's 30s, true", got, ok)
	}
	// A pending execution: no start, so no duration, and still "known" --
	// which is what runview priced as $0.00 estimated before this existed.
	if got, ok := (Execution{}).BilledWindow(vmNow); got != 0 || !ok {
		t.Errorf("BilledWindow of an unstarted execution = %v, %v; want 0, true", got, ok)
	}
}

// Quantities: a price table that bills a machine type carries that type's
// quantities and they win; one that does not (Cloud Run's jobs list price,
// the local config's override, the runner's FUGARO_COMPUTE_PRICES) leaves the
// execution's own CPU/MemoryGiB deciding, so no Cloud Run estimate moves
// (design compute-backend.md §5.2).
func TestQuantitiesAreTheMachineTypesWhenTheTableCarriesThem(t *testing.T) {
	vm := Prices{VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6, VCPU: 4, MemoryGiB: 16}
	if cpu, mem := vm.Quantities(2, 8); cpu != 4 || mem != 16 {
		t.Errorf("Quantities = %v vCPU / %v GiB, want the table's 4/16", cpu, mem)
	}
	cloud := Prices{VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6}
	if cpu, mem := cloud.Quantities(2, 8); cpu != 2 || mem != 8 {
		t.Errorf("Quantities = %v vCPU / %v GiB, want the execution's own 2/8", cpu, mem)
	}
	// A half-filled table is not a machine type's: both quantities or neither.
	half := Prices{VCPU: 4}
	if cpu, mem := half.Quantities(2, 8); cpu != 2 || mem != 8 {
		t.Errorf("Quantities of a half-filled table = %v vCPU / %v GiB, want the execution's own 2/8", cpu, mem)
	}
}
