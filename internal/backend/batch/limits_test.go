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
		t.Errorf("CheckResources(2 CPU/4Gi, %s) = %+v, want none: that type's task holds 4 CPUs and 8Gi, and this pair is inside both", defaultMachineType, is)
	}
}

// TestCheckResourcesAcceptsTheConfigDefaultsOnTheDefaultMachineType is the
// consequence the default machine type is sized for: a workflow that names no
// `resources:` gets the config's defaults (internal/config/defaults.go), so a
// freshly provisioned installation whose workflows are all left at the defaults
// must not refuse them. It is the shape the default had before review --
// e2-standard-4's task share is 2000m/4096MiB, Check 57a's measured request and
// below the default ask of 4 CPUs and 8Gi, so every workflow that named no
// resources was refused until the operator raised `batch.machine_type`: a
// default installation that cannot run its default workflow.
//
// Mutation (set defaultMachineType back to e2-standard-4): this fails, naming
// workflows.web.resources.cpu.
func TestCheckResourcesAcceptsTheConfigDefaultsOnTheDefaultMachineType(t *testing.T) {
	cfg, ps := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows:\n  web:\n    commands: { build: \"true\", test: \"true\" }\n"))
	if len(ps) > 0 {
		t.Fatalf("the fixture config has problems: %v", ps)
	}
	r := cfg.Workflows["web"].Resources
	if r.CPU != 4 || r.Memory != "8Gi" {
		t.Fatalf("the config's defaults are %+v, want the 4 CPU/8Gi this test is about: the default machine type is sized to it", r)
	}
	if is := CheckResources(r, defaultMachineType); len(is) != 0 {
		t.Errorf("a workflow left at the config's defaults (%+v) is refused on the default machine type %s: %+v", r, defaultMachineType, is)
	}
}

// TestCheckResourcesRefusesMoreThanTheTaskShare is the check's refusal half:
// the task's computeResource is the machine type's share, and a workflow's
// resources: is not sent to Batch at all, so an ask above it is a run sized for
// capacity its own task never claimed. The refusal names the larger machine
// type as the answer (design §9 step 9), which the message assertions below
// hold.
//
// Mutation (drop the cpu bound, drop the memory bound, or compare against the
// machine type's whole quantities -- 4 CPUs and 16Gi on e2-standard-4, 8 CPUs
// and 32Gi on the default): the named case passes and the test fails.
func TestCheckResourcesRefusesMoreThanTheTaskShare(t *testing.T) {
	for _, c := range []struct {
		name        string
		r           config.Resources
		machineType string
		field       string
	}{
		// e2-standard-4, named rather than taken from defaultMachineType: the
		// default is e2-standard-8 (see its own test), whose share is 4 CPU/8Gi,
		// and an ask over THAT is 8 CPUs or 16Gi.
		{"cpu over the share", config.Resources{CPU: 4, Memory: "1Gi"}, "e2-standard-4", "cpu"},
		{"memory over the share", config.Resources{CPU: 1, Memory: "8Gi"}, "e2-standard-4", "memory"},
		{"neither of them", config.Resources{CPU: 8, Memory: "32Gi"}, "e2-standard-4", "cpu"},
		{"cpu over the default's share", config.Resources{CPU: 8, Memory: "1Gi"}, defaultMachineType, "cpu"},
		{"memory over the default's share", config.Resources{CPU: 1, Memory: "16Gi"}, defaultMachineType, "memory"},
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
	// And it names the answer the design gives a run that asks for more: a
	// larger machine type, not an overcommitted task (design §9 step 9). The
	// failure it must NOT claim is the one this backend cannot produce -- the
	// workflow's resources: never reaches the job's computeResource, so no ask
	// in this field can leave a task QUEUED.
	over := CheckResources(config.Resources{CPU: 4, Memory: "1Gi"}, "e2-standard-4")
	if len(over) == 0 {
		t.Fatal("the over-share ask was accepted")
	}
	for _, want := range []string{"batch.machine_type", "computeResource"} {
		if !strings.Contains(over[0].Message, want) {
			t.Errorf("the over-share refusal does not say %q: %q", want, over[0].Message)
		}
	}
	if strings.Contains(over[0].Message, "QUEUED") {
		t.Errorf("the over-share refusal claims a failure this backend cannot produce (the task's computeResource is the share, so the workflow's ask cannot make the job unplaceable): %q", over[0].Message)
	}
}

// TestCheckResourcesMatchesTheTaskShares holds the bound CheckResources applies
// to the number the job actually sends. taskResources is what the task's
// computeResource carries (run.go), and this check computes the same share from
// the same two constants -- two computations of one fact, so this is what keeps
// them the same fact: the largest ask the check accepts is exactly what the task
// claims, and one more than that is refused, on every type the table prices.
//
// Mutation (change either computation, or compare against the machine type's
// whole quantities): the type named in the failure no longer matches the ask
// the task carries.
func TestCheckResourcesMatchesTheTaskShares(t *testing.T) {
	for _, name := range []string{defaultMachineType, "e2-standard-2", "e2-standard-16"} {
		mt, err := MachineTypeOf(name)
		if err != nil {
			t.Fatalf("MachineTypeOf(%s): %v", name, err)
		}
		// The task asks Batch in milli-CPU and MiB; a workflow asks in whole
		// CPUs and a memory string.
		tr := taskResources(mt)
		shareCPU := float64(tr.CpuMilli) / 1000
		shareMem := float64(tr.MemoryMib) / 1024
		ask := config.Resources{CPU: int(shareCPU), Memory: giB(shareMem)}
		if is := CheckResources(ask, name); len(is) != 0 {
			t.Errorf("CheckResources(%+v, %s) = %+v, want none: taskResources asks Batch for exactly %g CPUs, and the check refuses the task's own claim", ask, name, is, shareCPU)
		}
		over := config.Resources{CPU: int(shareCPU) + 1, Memory: giB(shareMem)}
		if is := CheckResources(over, name); len(is) == 0 || is[0].Field != "cpu" {
			t.Errorf("CheckResources(%+v, %s) = %+v, want a cpu issue: that is one CPU more than the task asks Batch for", over, name, is)
		}
		fat := config.Resources{CPU: int(shareCPU), Memory: giB(shareMem + 1)}
		if is := CheckResources(fat, name); len(is) == 0 || is[0].Field != "memory" {
			t.Errorf("CheckResources(%+v, %s) = %+v, want a memory issue: that is one GiB more than the task asks Batch for", fat, name, is)
		}
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
