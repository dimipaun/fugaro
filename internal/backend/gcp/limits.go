package gcp

import (
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// Issue is one way a workflow's resources don't fit Cloud Run.
type Issue struct{ Field, Message string }

// Cloud Run job task limits, from cloud.google.com/run/docs/configuring/jobs/cpu
// and .../jobs/memory-limits (checked 2026-09-27). Memory above each
// threshold needs at least that many CPUs, and 4 or more CPUs need a floor.
var (
	cpuChoices = []int{1, 2, 4, 6, 8}
	memNeedCPU = []struct {
		overGiB float64
		cpu     int
	}{{24, 8}, {16, 6}, {8, 4}, {4, 2}}
	cpuNeedMem = map[int]float64{4: 2, 6: 4, 8: 4}
)

// CheckResources reports why r cannot be a Cloud Run job task (design §14:
// the core schema only requires a positive CPU count and a memory quantity).
func CheckResources(r config.Resources) []Issue {
	var out []Issue
	if !slices.Contains(cpuChoices, r.CPU) {
		out = append(out, Issue{"cpu", "Cloud Run jobs need one of 1, 2, 4, 6, 8 CPUs"})
	}
	gib, err := backend.MemoryGiB(r.Memory)
	switch {
	case err != nil:
		out = append(out, Issue{"memory", err.Error()})
	case gib > 32:
		out = append(out, Issue{"memory", "Cloud Run jobs allow at most 32Gi"})
	case gib < 0.5:
		out = append(out, Issue{"memory", "Cloud Run jobs need at least 512Mi"})
	default:
		for _, t := range memNeedCPU {
			if gib > t.overGiB && r.CPU < t.cpu {
				out = append(out, Issue{"memory", fmt.Sprintf("%s on Cloud Run needs at least %d CPUs", r.Memory, t.cpu)})
				break
			}
		}
		if floor, ok := cpuNeedMem[r.CPU]; ok && gib < floor {
			out = append(out, Issue{"memory", fmt.Sprintf("on Cloud Run, %d CPUs need at least %gGi", r.CPU, floor)})
		}
	}
	return out
}
