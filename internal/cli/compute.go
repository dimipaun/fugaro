package cli

import (
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// computeProblems checks each workflow against its own compute backend's
// limits: the backend an installation selects is what sizes a run's job, and
// the two backends' limits do not agree (design compute-backend.md §4.4 item
// 3). Every workflow runs on Cloud Run until the project config names another
// backend (design §14), which is gcp.CheckResources for all of them; on the
// Batch backend it is the VM's own check (internal/backend/batch), never Cloud
// Run's -- Cloud Run's cpuChoices and its memory/CPU coupling are a Cloud Run
// job task's properties, and a VM has neither.
//
// lc is the selected project config, nil when there is none: with nothing
// selected the answer is Cloud Run's, because the backend is an installation
// fact and an unknown installation is on the default (localcfg.Config.Backend).
func computeProblems(cfg *config.Config, lc *localcfg.Config) []config.Problem {
	var ps []config.Problem
	onBatch := lc != nil && lc.Backend() == backend.CloudBatch
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		res := cfg.Workflows[name].Resources
		path := "workflows." + name + ".resources."
		if onBatch {
			for _, is := range batch.CheckResources(res, batchMachineTypeOf(lc)) {
				if is.Field == "" {
					// The machine type's own refusal, which is about the
					// installation's batch block and not about this workflow:
					// batchValidateProblems reports it once, at
					// batch.machine_type.
					continue
				}
				ps = append(ps, config.Problem{Path: path + is.Field, Message: is.Message})
			}
			continue
		}
		for _, is := range gcp.CheckResources(res) {
			ps = append(ps, config.Problem{Path: path + is.Field, Message: is.Message})
		}
	}
	return ps
}
