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
//
// A config selected for ANOTHER project judges nothing here, which is the
// guard budgetProblems, providerProblems and cascadeProblems all carry: which
// backend a workflow's resources are sized against is a fact about the
// installation the file's own project names, and judging a file naming project
// B against checkout A's would refuse a valid file — or admit an invalid one —
// on limits that are not its own. A nil lc is not that case and keeps Cloud
// Run's answer, which is why this tests lc rather than reading through it.
func computeProblems(cfg *config.Config, lc *localcfg.Config) []config.Problem {
	if lc != nil && lc.Name != cfg.Project {
		return nil
	}
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
					// batch.machine_type -- which is a premise about the
					// CALLER, and the reason both of them are reached through
					// computeCheckoutProblems rather than one at a time (an
					// installation whose machine type the price table does not
					// carry gets this Issue alone from CheckResources, so a
					// caller that dropped it would report nothing at all and
					// run no per-workflow resource check either).
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

// computeCheckoutProblems is every offline problem a fugaro.yaml in this
// checkout has on the backend its installation selects: the file's own checks
// (config.Check, which reads the repository at root), the per-workflow compute
// limits of the backend's own answers (computeProblems), and the backend's
// offline checks (batchValidateProblems -- nothing for a project that is not on
// cloud-batch, and on it the artifact boundary Global Constraint 13 keeps).
//
// One function rather than one call per half at each site, because the halves
// are not independent: computeProblems drops the machine type's own Issue -- the
// one batch.CheckResources answers with no Field -- on the premise that
// batchValidateProblems reports it at batch.machine_type. That premise is about
// the caller, and the checkout read behind `fugaro image refresh`, image build
// and the layer commands called computeProblems alone: an installation whose
// machine type the price table does not carry was told nothing at all, and no
// per-workflow resource check ran either (a VM the table cannot price cannot be
// sized, so CheckResources returns that one Issue and nothing else). Every
// caller goes through here, which is what makes the premise true by
// construction rather than by each caller remembering it.
//
// root is the checkout the file was read from: config.Check reads the
// repository there, and localRecipe resolves this checkout's recipe from it
// (agent.recipe, else the default), which is the recipe the artifact boundary
// judges. The offline approximation that resolution is -- a launch that names
// its own --recipe is judged by its own gate -- is batchValidateProblems' own
// contract.
func computeCheckoutProblems(cfg *config.Config, root string, lc *localcfg.Config) []config.Problem {
	ps := append(config.Check(cfg, root), computeProblems(cfg, lc)...)
	return append(ps, batchValidateProblems(lc, cfg, localRecipe(root, cfg))...)
}
