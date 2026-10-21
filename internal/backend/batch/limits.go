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
// compute-backend.md §1.3, §9). run.go sends the run's total plus
// backend.TaskTimeoutSlack as the job's maxRunDuration, so a total longer than
// this is a job Batch refuses at create -- which is why fugaro validate bounds
// the value that can reach the field first, where the operator can see it
// rather than reading it out of a failed create.
//
// The total a run reaches this field with is not a workflow's timeouts.total
// in this build: batchTimeouts.TaskTimeout answers every workflow with
// localcfg.BatchTaskTimeout (batch.task_timeout, else backend.MaxRunTime), and
// a launch that carries its own --total-timeout wins over both. So what
// validate bounds is batch.task_timeout, which is the one input that can put a
// total past this ceiling; when the enable task reads the workflow's own
// timeouts.total per workflow, that value is what validate bounds instead.
//
// It is a ceiling, not a plan value: Fugaro's own bound is
// backend.MaxRunTime (the cap a per-run override may name), and both bind
// first for every installation that has one.
//
// 14 days is the ceiling for the provisioning model this build's jobs are
// created under, and it is a fact about the job JSON run.go builds rather than
// about a constant it states: that JSON has no provisioningModel field at all,
// and Batch reads an absent one as STANDARD. Batch documents a 7-day RUNNING
// ceiling under FLEX_START (design §1.8), which would be the one that binds if
// a job ever named it -- so a later task that asks for FLEX_START, or for the
// SPOT model design §1.3 has the profile carry (C5), has to revisit this
// constant rather than only the job definition. Nothing in this tree sets a
// provisioning model anywhere.
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
// bounds it is that share.
//
// The failure mode is a mismatch, not a job that never starts: the task's
// computeResource is taskResources(machine type) and the workflow's resources:
// is not sent to Batch at all (run.go's job definition, which builds the
// container with no limits either), so an ask above the share is a run sized
// for capacity its own task never claimed -- and a run that believed it had
// more room than the task it runs in. The design's answer for a run that needs
// more is a larger machine type, not an overcommitted task (design §9 step 9);
// the shares themselves are a floor known to work rather than a measured
// maximum, which is what Check 57b-9 measures.
//
// There is deliberately no coupling rule between the two fields. On a VM the
// machine type fixes both numbers and the run's pair only sizes its
// container, so 4 CPUs with 1Gi and 1 CPU with 8Gi are both asks the default
// machine type's share holds (4 CPUs and 8Gi, see defaultMachineType);
// refusing either would be importing Cloud Run's rule into a backend that does
// not have it.
//
// An unknown machine type is an Issue with no Field: it is a fact about the
// installation's batch block rather than about one workflow's resources, and
// the caller attaches it where it belongs. It is refused here rather than
// left to batch.New because this is the offline check, and a type the price
// table does not carry can neither be priced nor sized honestly.
func CheckResources(r config.Resources, machineType string) []Issue {
	if machineType == "" {
		// The block names no type: checkVM fills defaultMachineType in, so the
		// ask is judged against what a run on this installation actually gets
		// rather than against nothing.
		machineType = defaultMachineType
	}
	mt, err := MachineTypeOf(machineType)
	if err != nil {
		return []Issue{{"", fmt.Sprintf("the Batch backend's machine type: %v", err)}}
	}
	// The share, as run.go's taskResources computes it. Computed from the same
	// two constants rather than restated as numbers, and
	// TestCheckResourcesMatchesTheTaskShares holds this computation against
	// taskResources' own, which is what the job sends.
	shareCPU := mt.VCPU / taskVCPUShare
	shareMem := mt.MemoryGiB / taskMemShare

	var out []Issue
	if r.CPU <= 0 {
		out = append(out, Issue{"cpu", "a run needs at least one CPU"})
	} else if float64(r.CPU) > shareCPU {
		out = append(out, Issue{"cpu", fmt.Sprintf(
			"a run on %s asks for at most %g CPUs: %d is more than the share of the machine type the run's task claims (%g of the type's %g), and the workflow's resources are not sent to Batch -- the task's computeResource is that share -- so this is a run sized for capacity its own task never claimed. The answer is a larger batch.machine_type, not an overcommitted task (design §1.3, §9 step 9)",
			mt.Name, shareCPU, r.CPU, shareCPU, mt.VCPU)})
	}
	gib, err := backend.MemoryGiB(r.Memory)
	switch {
	case err != nil:
		out = append(out, Issue{"memory", err.Error()})
	case gib > shareMem:
		out = append(out, Issue{"memory", fmt.Sprintf(
			"a run on %s asks for at most %s of memory: %s is more than the share of the machine type the run's task claims (%s of the type's %s), and the workflow's resources are not sent to Batch -- the task's computeResource is that share -- so this is a run sized for capacity its own task never claimed. The answer is a larger batch.machine_type, not an overcommitted task (design §1.3, §9 step 9)",
			mt.Name, giB(shareMem), r.Memory, giB(shareMem), giB(mt.MemoryGiB))})
	}
	return out
}

// giB spells a whole number of GiB the way a workflow's resources.memory
// field is written, so a refusal quotes a value the operator can compare with
// the one in their file rather than a float.
func giB(n float64) string { return fmt.Sprintf("%gGi", n) }
