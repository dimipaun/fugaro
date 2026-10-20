package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// The Batch backend's offline checks (plan 2026-10-19-compute-backend.md Task
// 8 step 4, design compute-backend.md §4.4).
//
// Every one of them fires only for an installation whose project config selects
// backend: cloud-batch, and internal/localcfg refuses that name until the
// enable task (Task 10). So nothing here changes any path an installation takes
// today -- the plan's Global Constraint 12 -- and Task 10 is what makes these
// lines reachable. The one that outlives that enable is the artifact refusal
// (Global Constraint 13): Task 10 lifts the backend-name refusal and must leave
// that one standing.
//
// NOT written here, and why, so a reader of Task 8's step 4 does not have to
// reconstruct it: design §4.4's item 2 (docker: true requires a Docker-capable
// backend) and item 6 (no socket for a workflow without docker: true) read a
// workflow key that does not exist in this tree. `docker: true` is C16's key
// (design §4.2), internal/backend's LaunchSpec.Docker and internal/agent's
// EnvSpec.Docker are already the seam's two halves of it, and internal/config
// carries no `docker:` field for either to be set from: nothing in this tree
// writes one, so a check here would read a value no config can hold. Item 5
// (spot combined with a workflow that must finalize is a warning) reads a
// profile's spot switch, which no task in this plan adds either. Both are
// recorded in this task's pull request as the part of step 4 that is not
// writable yet, rather than written against a surface that does not exist.

// batchValidateProblems are validate's offline checks for an installation on
// the Batch backend. It returns nothing for every other project -- the checks
// are about a backend no other installation selects -- and it is deliberately
// a pure function of the project config, the fugaro.yaml and the recipe the
// checkout resolves, so it is testable without a project (the backend name
// cannot be selected in this build).
func batchValidateProblems(lc *localcfg.Config, cfg *config.Config, rcp *recipe.Recipe) []config.Problem {
	if lc == nil {
		return nil
	}

	// The name is one this build can open, before anything is asked about the
	// backend it names. internal/localcfg refuses an unknown name before a
	// config that carries one ever reaches validate, so this is the second line
	// of the same defence -- and the one that stays true if that validation
	// loosens, which is what design §4.4 item 1 asks for. The wording is
	// backend.Open's, deliberately: an operator who reads one of the two
	// messages has read both. It is asked first because it is the one check
	// that has an answer for a name that is neither backend: everything below
	// reads the batch block, and a name that is not cloud-batch has no batch
	// block to read.
	if !isSelectableBackend(lc.Backend()) {
		return []config.Problem{{Path: "backend", Message: fmt.Sprintf(
			"unknown backend %q; known backends: %s",
			// Sorted, as backend.Open sorts its own: the two messages are one
			// sentence, so an operator cannot tell which of the two refused.
			lc.Backend(), strings.Join(slices.Sorted(slices.Values(selectableBackends)), ", "))}}
	}
	if lc.Backend() != backend.CloudBatch {
		return nil
	}
	var ps []config.Problem

	// The machine type a run's VM is (design §5.2, §4.4 item 3): it is priced
	// and sized from the price table, so a type the table does not carry can be
	// neither -- and a run on it would be a run whose cost nothing can report.
	// One fact about the installation, so one problem, at the key that names
	// it; CheckResources answers it with no Field for exactly that reason.
	for _, is := range batch.CheckResources(config.Resources{}, batchMachineTypeOf(lc)) {
		if is.Field == "" {
			ps = append(ps, config.Problem{Path: "batch.machine_type", Message: is.Message})
		}
	}

	// The artifact boundary (Global Constraint 13, design artifact-runs.md
	// §8.5 and D11). The repository's recipe is what decides whether a run of
	// it produces artifacts, so the check reads the recipe this checkout
	// resolves the way a launch would (agent.recipe, else the default) rather
	// than guessing from anything in fugaro.yaml.
	if rcp != nil && rcp.Mode == recipe.ModeArtifact {
		ps = append(ps, config.Problem{Path: "backend", Message: artifactOnBatchMessage})
	}

	// The timeouts (design §4.4 item 4). A workflow's clock plus the slack
	// every backend adds is what run.go sends as the job's maxRunDuration, and
	// Batch fails a task after batch.MaxRunDuration RUNNING: a workflow past
	// that is a job Batch refuses at create, so validate refuses it where the
	// operator can see it.
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		total := cfg.Workflows[name].Timeouts.Total.Duration
		if total <= 0 {
			continue
		}
		if total+backend.TaskTimeoutSlack > batch.MaxRunDuration {
			ps = append(ps, config.Problem{Path: "workflows." + name + ".timeouts.total", Message: fmt.Sprintf(
				"a workflow on cloud-batch runs for at most %v: this is %v plus the %v every backend adds as the job's maxRunDuration, and Batch fails a task after 14 days RUNNING (docs/design/compute-backend.md §1.3, §9)",
				batch.MaxRunDuration-backend.TaskTimeoutSlack, total, backend.TaskTimeoutSlack)})
		}
	}
	return ps
}

// artifactOnBatchMessage is the artifact boundary's refusal, named here so
// that the two readers of it -- the check above, and the test Task 10's commit
// writes to assert the boundary survived the enable -- hold one string rather
// than two spellings of it. It names the design and its decision, and what the
// boundary is waiting for, because the next reader of it is the plan that
// removes it.
const artifactOnBatchMessage = "an artifact-producing run is never launched on cloud-batch: this repository's recipe is mode: artifact, so its runs deliver into the runs bucket rather than opening a pull request, and a Batch run's account is not per-repository -- the same run's inputs, artifacts and record would be readable and overwritable by every run on the profile (docs/design/artifact-runs.md §8.5 and D11). What this waits for is a per-run prefix condition or a per-run account; until one of them exists, an artifact-producing run stays on backend: cloud-run, and nothing in this build removes the refusal"

// isSelectableBackend reports whether name is one of the backends
// selectableBackends lists -- the names this build can open (backend.Open's
// contract, held against the opener map by
// TestBackendsLsListsTheOpenerMapsNames).
func isSelectableBackend(name string) bool {
	for _, n := range selectableBackends {
		if n == name {
			return true
		}
	}
	return false
}

// batchMachineTypeOf is the machine type the installation's batch block names,
// "" when it names none (the backend's own default then applies).
func batchMachineTypeOf(lc *localcfg.Config) string {
	if lc.Batch == nil {
		return ""
	}
	return lc.Batch.MachineType
}
