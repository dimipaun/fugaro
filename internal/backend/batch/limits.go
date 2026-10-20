package batch

import (
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// MaxRunDuration is the longest maxRunDuration a Batch task may be given: 14
// days in RUNNING before Batch fails the task automatically
// (cloud.google.com/batch/quotas, checked 2026-10-15; design
// compute-backend.md §1.3, §9). run.go sends the workflow's own clock plus
// backend.TaskTimeoutSlack as the job's maxRunDuration, so a workflow whose
// clock is longer than this is a job Batch refuses at create -- which is why
// fugaro validate refuses it first, where the operator can see it rather than
// reading it out of a failed create.
//
// It is a ceiling, not a plan value: Fugaro's own bound is
// backend.MaxRunTime (the cap a per-run override may name) and each
// workflow's timeouts.total, and both bind first for every installation that
// has one. Under FLEX_START the ceiling is 7 days, which this constant does
// not model: no Fugaro job asks for FLEX_START (run.go's job definition names
// STANDARD provisioning), so the 14-day figure is the one this build can be
// held to.
const MaxRunDuration = 14 * 24 * time.Hour

// Issue is one way a workflow's resources do not fit the VM a run gets on
// this backend. It is internal/backend/gcp's Issue in shape, because the two
// are read by the same caller and land under the same path
// (workflows.<name>.resources.<field>).
type Issue struct{ Field, Message string }

// CheckResources reports why r cannot be a run's ask on this backend -- the
// VM's own answer, not Cloud Run's (design §4.4 item 3; generic-tool §2,
// v1 §3.4: "a Batch backend would bring its own of each").
//
// Cloud Run's rules deliberately do not apply and are not restated here:
// its cpuChoices (1, 2, 4, 6, 8) are the job task's, and its memory/CPU
// coupling (internal/backend/gcp/limits.go) is what a Cloud Run task's two
// limits must satisfy. On Batch the VM is a Compute Engine machine type and
// the task's computeResource is derived from it (run.go's taskResources:
// half the type's vCPUs, a quarter of its memory -- the shares Check 57a
// scheduled with). A workflow's resources: is the run's own ask, and what
// bounds it is that share: a task asking for more than the VM Batch placed it
// on holds sits QUEUED until the queue deadline deletes it, which is a run
// that never starts rather than a run that fails.
//
// There is deliberately no coupling rule between the two fields. On a VM the
// machine type fixes both numbers and the run's pair only sizes its
// container, so 4 CPUs with 1Gi and 1 CPU with 8Gi are both asks an
// e2-standard-4 can hold; refusing either would be importing Cloud Run's rule
// into a backend that does not have it.
//
// An unknown machine type is an Issue with no Field: it is a fact about the
// installation's batch block rather than about one workflow's resources, and
// the caller attaches it where it belongs. It is refused here rather than
// left to batch.New because this is the offline check, and a type the price
// table does not carry can neither be priced nor sized honestly.
func CheckResources(r config.Resources, machineType string) []Issue {
	mt, err := MachineTypeOf(machineType)
	if err != nil {
		return []Issue{{"", fmt.Sprintf("the Batch backend's machine type: %v", err)}}
	}
	// The share, as run.go's taskResources computes it. Computed the same way
	// rather than named as constants twice: taskVCPUShare and taskMemShare are
	// the authority, and TestCheckResourcesMatchesTheTaskShares holds the two
	// together.
	shareCPU := mt.VCPU / taskVCPUShare
	shareMem := mt.MemoryGiB / taskMemShare

	var out []Issue
	if r.CPU <= 0 {
		out = append(out, Issue{"cpu", "a run needs at least one CPU"})
	} else if float64(r.CPU) > shareCPU {
		out = append(out, Issue{"cpu", fmt.Sprintf(
			"a run on %s asks for at most %g CPUs: %d is more than the share of the machine type a Batch task gets (%g of the type's %g), and a task asking for more than the VM it is placed on holds sits QUEUED until the queue deadline deletes it (design §1.3, §2.3)",
			mt.Name, shareCPU, r.CPU, shareCPU, mt.VCPU)})
	}
	gib, err := backend.MemoryGiB(r.Memory)
	switch {
	case err != nil:
		out = append(out, Issue{"memory", err.Error()})
	case gib > shareMem:
		out = append(out, Issue{"memory", fmt.Sprintf(
			"a run on %s asks for at most %s of memory: %s is more than the share of the machine type a Batch task gets (%s of the type's %s), and a task asking for more than the VM it is placed on holds sits QUEUED until the queue deadline deletes it (design §1.3, §2.3)",
			mt.Name, giB(shareMem), r.Memory, giB(shareMem), giB(mt.MemoryGiB))})
	}
	return out
}

// giB spells a whole number of GiB the way a workflow's resources.memory
// field is written, so a refusal quotes a value the operator can compare with
// the one in their file rather than a float.
func giB(n float64) string { return fmt.Sprintf("%gGi", n) }
