package batch

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestCheckResourcesAcceptsWhatAVMCanHold is the check's positive half: the
// two quantities a run may ask for on the default machine type are accepted,
// including combinations Cloud Run's coupling rules refuse (4 CPUs with 1Gi,
// 1 CPU with 8Gi) -- on a VM the machine type fixes both and the run's own
// numbers only size the container.
//
// Mutation (apply Cloud Run's checks here, or compare against the machine
// type's whole quantities instead of the task's share): the 4 CPU/1Gi case
// fails, which is the coupling rule this check exists to NOT have.
func TestCheckResourcesAcceptsWhatAVMCanHold(t *testing.T) {
	// The last two are the pairs Cloud Run's coupling refuses and this
	// backend must not: e2-standard-16's task holds 8 CPUs and 16Gi.
	for _, r := range []config.Resources{
		{CPU: 2, Memory: "4Gi"},
		{CPU: 1, Memory: "512Mi"},
		{CPU: 8, Memory: "1Gi"},
		{CPU: 1, Memory: "16Gi"},
	} {
		if is := CheckResources(r, "e2-standard-16"); len(is) != 0 {
			t.Errorf("CheckResources(%+v, e2-standard-16) = %+v, want none: that type's task holds 8 CPUs and 16Gi, and a VM has no coupling rule between the two", r, is)
		}
	}
	if is := CheckResources(config.Resources{CPU: 2, Memory: "4Gi"}, defaultMachineType); len(is) != 0 {
		t.Errorf("CheckResources(2 CPU/4Gi, %s) = %+v, want none: that type's task holds exactly that", defaultMachineType, is)
	}
}

// TestCheckResourcesRefusesMoreThanTheTaskShare is the check's refusal half:
// a task asking for more than the share of the machine type Batch places it on
// sits QUEUED, so validate refuses it rather than letting the run wait out the
// queue deadline.
//
// Mutation (drop the cpu bound, drop the memory bound, or compare against the
// machine type's whole quantities -- 4 CPUs and 16Gi on e2-standard-4): the
// named case passes and the test fails.
func TestCheckResourcesRefusesMoreThanTheTaskShare(t *testing.T) {
	for _, c := range []struct {
		name        string
		r           config.Resources
		machineType string
		field       string
	}{
		{"cpu over the share", config.Resources{CPU: 4, Memory: "1Gi"}, defaultMachineType, "cpu"},
		{"memory over the share", config.Resources{CPU: 1, Memory: "8Gi"}, defaultMachineType, "memory"},
		{"neither of them", config.Resources{CPU: 8, Memory: "32Gi"}, defaultMachineType, "cpu"},
		{"the smallest type", config.Resources{CPU: 1, Memory: "4Gi"}, "e2-standard-2", "memory"},
		{"a machine type the table does not price", config.Resources{CPU: 1, Memory: "1Gi"}, "e2-micro", ""},
	} {
		is := CheckResources(c.r, c.machineType)
		if len(is) == 0 {
			t.Errorf("%s: CheckResources(%+v, %s) accepted it", c.name, c.r, c.machineType)
			continue
		}
		if is[0].Field != c.field {
			t.Errorf("%s: issue field = %q, want %q (%+v)", c.name, is[0].Field, c.field, is)
		}
		if is[0].Message == "" {
			t.Errorf("%s: the issue has no message", c.name)
		}
	}
	// The machine-type refusal names what the table does carry, so the operator
	// can fix it without reading the source.
	is := CheckResources(config.Resources{CPU: 1, Memory: "1Gi"}, "e2-micro")
	if !strings.Contains(is[0].Message, defaultMachineType) {
		t.Errorf("the unknown-machine-type refusal does not list the types this build prices: %q", is[0].Message)
	}
}

// TestCheckResourcesRefusesANonPositiveCPU pins the one field the core schema
// leaves open (design §14: the schema requires a positive CPU count, and this
// is the backend's own answer for a value that reached it anyway).
//
// Mutation (drop the r.CPU <= 0 branch): the case is accepted.
func TestCheckResourcesRefusesANonPositiveCPU(t *testing.T) {
	is := CheckResources(config.Resources{CPU: 0, Memory: "1Gi"}, defaultMachineType)
	if len(is) == 0 || is[0].Field != "cpu" {
		t.Fatalf("CheckResources accepted a run with no CPU: %+v", is)
	}
}

// TestMaxRunDurationIsBatchsCeiling pins the constant validate refuses a
// workflow's clock against: 14 days RUNNING before Batch fails a task
// automatically (design §1.3, §9, cloud.google.com/batch/quotas, checked
// 2026-10-15). A workflow's timeout plus backend.TaskTimeoutSlack is what
// run.go sends as maxRunDuration, so this is the number that makes the
// refusal true.
//
// Mutation (raise or lower it, or send it without the slack): the value is a
// platform limit, and validate's message quotes it.
func TestMaxRunDurationIsBatchsCeiling(t *testing.T) {
	if MaxRunDuration != 336*time.Hour {
		t.Fatalf("MaxRunDuration = %v, want 336h (14 days, design §1.3)", MaxRunDuration)
	}
}
