package cli

import (
	"fmt"
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
// The artifact boundary has two arms and this file is only one of them. This
// arm is offline and per-checkout, so it judges the recipe this checkout
// resolves the way a launch would (agent.recipe, else the default); a launch
// that names its own --recipe -- including one that would produce artifacts --
// is judged by the launch's gate, run.go's checkArtifactLaunch, which asks the
// same artifactOnBatchRefusal with the recipe the launch resolves: the text a
// project or catalog recipe carries in the task -- and, for that one, the
// repository layer the ref holds of the same name, which is the recipe that
// actually runs (TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch)
// -- or, for a repository recipe (named, or the one agent.recipe names), the
// file in this launcher's working tree. The runner reads a repository recipe at
// the task's ref -- the repository's base branch unless the launch names
// another -- so neither arm sees the recipe the runner will read when the two
// differ: that residual is recorded in the plan's Task 10 step 5 inventory,
// which must close it before the name is selectable, and
// TestTheArtifactLaunchGateJudgesTheWorkingTreeNotTheRef pins it. A boundary
// enforced only here would be a hole the moment the name is selectable, which
// is exactly what Global Constraint 13 exists to prevent.
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
// are about a backend no other installation selects, and about an installation
// whose project is the one the file is being judged for -- and it is
// deliberately a pure function of the project config, the fugaro.yaml and the
// recipe the checkout resolves, so it is testable without a project (the
// backend name cannot be selected in this build).
//
// The project guard is the one budgetProblems, providerProblems, cascadeProblems
// and computeProblems carry: a config selected for another project is not this
// file's installation, so the backend it names, the machine type a run's VM is
// and the task timeout it holds are not facts this file can be refused on. It
// reads as the same `lc == nil ||` prefix because a nil config is the same
// case -- there is no installation here to judge against, and the checks below
// read lc rather than test it.
func batchValidateProblems(lc *localcfg.Config, cfg *config.Config, rcp *recipe.Recipe) []config.Problem {
	if lc == nil || lc.Name != cfg.Project {
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
	// than guessing from anything in fugaro.yaml. That resolution is the
	// offline approximation: a launch that names its own --recipe is judged by
	// the launch's gate (run.go's checkArtifactLaunch, which asks the same
	// function below), and Task 10 must leave both standing.
	if msg, ok := artifactOnBatchRefusal(lc.Backend(), rcp); ok {
		ps = append(ps, config.Problem{Path: "backend", Message: msg})
	}
	// And the same boundary fails closed on a recipe this checkout cannot
	// resolve, exactly as the launch's gate does on cloud-batch: a nil recipe
	// reads as "not an artifact run" to the check above, while the runs of this
	// installation may be resolving a mode: artifact one at their base (an
	// agent.recipe this checkout cannot read, a file it cannot parse, a name
	// that is in neither the repository nor the catalog). Nothing here can tell
	// the two apart, so the installation is refused rather than passed.
	if rcp == nil {
		ps = append(ps, config.Problem{Path: "backend", Message: artifactUnreadOfflineRefusal})
	}

	// The timeouts (design §4.4 item 4). The job's maxRunDuration is the run's
	// total plus the slack every backend adds, and Batch fails a task after
	// batch.MaxRunDuration RUNNING: a total past that is a job Batch refuses at
	// create, so validate refuses the input that reaches the field where the
	// operator can see it.
	//
	// On this backend that input is batch.task_timeout, not a workflow's
	// timeouts.total: batchTimeouts.TaskTimeout answers every workflow with
	// lc.BatchTaskTimeout() (the field, else backend.MaxRunTime), and a launch
	// carrying its own --total-timeout is capped at backend.MaxRunTime by
	// run.go's own bound -- so neither can pass this ceiling. When the enable
	// task reads the workflow's own timeouts.total per workflow (cloud.go's
	// batchTimeouts), that value is what this bounds instead, and the field
	// stops being the answer for every workflow.
	limit := batch.MaxRunDuration - backend.TaskTimeoutSlack
	if d := lc.BatchTaskTimeout(); d > limit {
		ps = append(ps, config.Problem{Path: "batch.task_timeout", Message: fmt.Sprintf(
			"batch.task_timeout is %v, and the job's maxRunDuration is that plus the %v every backend adds as its slack: Batch fails a task after 14 days RUNNING (%v), so a task timeout above %v is a job Batch refuses at create (docs/design/compute-backend.md §1.3, §9)",
			d, backend.TaskTimeoutSlack, batch.MaxRunDuration, limit)})
	}
	return ps
}

// artifactOnBatchRefusal is the artifact boundary as both of its callers ask
// it: the offline check above, and the launch's own gate (run.go's
// checkArtifactLaunch), which is the arm that holds once Task 10 has made the
// name selectable. One function rather than two conditions, because a boundary
// that only one of the two paths enforces is the hole Global Constraint 13
// exists to keep shut: `fugaro run --recipe <an artifact recipe>` on a Batch
// installation would be exactly that, and the launch's gate is where the
// recipe the launch names is resolved. Resolved as this launcher can: a
// repository recipe is read from its working tree, not at the task's ref the
// runner reads -- the residual the plan's Task 10 must close, see the file
// comment above -- and a recipe whose text the task carries is judged against
// the ref's own file of that name, which is what runs.
func artifactOnBatchRefusal(backendName string, rcp *recipe.Recipe) (string, bool) {
	if backendName != backend.CloudBatch || rcp == nil || rcp.Mode != recipe.ModeArtifact {
		return "", false
	}
	return artifactOnBatchMessage, true
}

// artifactOnBatchMessage is the artifact boundary's refusal, named here so
// that the two readers of it -- the check above, and the test Task 10's commit
// writes to assert the boundary survived the enable -- hold one string rather
// than two spellings of it. It names the design and its decision, and what the
// boundary is waiting for, because the next reader of it is the plan that
// removes it.
const artifactOnBatchMessage = "an artifact-producing run is never launched on cloud-batch: this repository's recipe is mode: artifact, so its runs deliver into the runs bucket rather than opening a pull request, and a Batch run's account is not per-repository -- the same run's inputs, artifacts and record would be readable and overwritable by every run on the profile (docs/design/artifact-runs.md §8.5 and D11). What this waits for is a per-run prefix condition or a per-run account; until one of them exists, an artifact-producing run stays on backend: cloud-run, and nothing in this build removes the refusal"

// artifactUnreadOfflineRefusal is the offline arm's fail-closed shape of the
// same boundary: an installation on cloud-batch whose recipe this checkout
// cannot resolve. The offline check reads the checkout's own layer
// (localRecipe), so a recipe it cannot read or parse, or one that is in neither
// the repository nor the catalog, leaves it with nothing to judge -- and a
// boundary that passes what it cannot read is not a boundary. The launch's gate
// refuses the same shape for the same reason (artifactUnreadOnBatchRefusal,
// artifactShadowedOnBatchRefusal).
const artifactUnreadOfflineRefusal = "this installation is on cloud-batch and the recipe its runs use could not be resolved from this checkout (agent.recipe in fugaro.yaml, else .fugaro/recipes/default.yaml, else the catalog), so nothing here can tell an artifact-producing recipe from any other -- and an artifact-producing run is never launched on cloud-batch, because a Batch run's account is not per-repository (docs/design/artifact-runs.md §8.5 and D11). Name a recipe this checkout can read and parse, and it is judged on its own mode"

// artifactUnreadOnBatchRefusal is the launch's other shape of the same
// boundary (Global Constraint 13): on cloud-batch a launch whose recipe the
// launcher cannot read here is refused rather than passed to the runner.
//
// The hole it closes is the boundary's fail-open one. A repo-sourced recipe is
// read from this checkout (recipeForLaunch), so a launcher running from a
// directory that is not a checkout of spec.Repo -- or a first run whose recipe
// is whatever this checkout's agent.recipe names -- resolves it to nil, and
// artifactOnBatchRefusal reads nil as "not an artifact run". The runner then
// resolves that recipe at the task's ref and runs it, which is an
// artifact-producing run that reached a Batch VM without the boundary ever
// seeing the mode. So the launch side fails closed instead, at a cost stated
// plainly: a Batch launch from outside the repository's own checkout is refused
// until the recipe can be read there, and the Fix says so.
func artifactUnreadOnBatchRefusal(repo string) string {
	return fmt.Sprintf("a run on cloud-batch whose recipe this launcher cannot read is refused: the recipe of %s is not readable from this directory (a repository recipe is read from a checkout whose origin is %s, and a first run's recipe is the one that checkout's agent.recipe names), so nothing on this path can tell an artifact-producing recipe from any other -- and an artifact-producing run is never launched on cloud-batch, because a Batch run's account is not per-repository (docs/design/artifact-runs.md §8.5 and D11). Run this launch from a checkout of %s that holds the ref it launches, and it is judged on the recipe the run reads there", repo, repo, repo)
}

// artifactShadowedOnBatchRefusal is the launch's third shape of the same
// boundary (Global Constraint 13): on cloud-batch, a launch whose task carries
// its own recipe text and whose repository layer at the ref this launcher
// cannot read.
//
// The hole it closes is the boundary reading the wrong document. A recipe's
// text travels in the task for a project or catalog recipe, and the task's
// record of where it came from does not decide anything: the runner reads the
// repository layer at the task's ref FIRST and only falls back to the task's
// copy (internal/runner/recipe.go's resolveRecipe, TestRecipeRepoLayerWins), so
// a .fugaro/recipes/<name>.yaml at the ref is the recipe that runs, mode
// included. `fugaro run --recipe <a project or catalog name>` from a directory
// that is not a checkout of the run's repository is that launch: the task
// carries text this launcher can read perfectly well, and a base branch whose
// same-named file is mode: artifact would run it on a Batch VM. The launcher
// therefore asks the ref for the name, exactly as the runner does, and refuses
// when it cannot -- the fail-closed direction, at the cost stated plainly: a
// launch carrying a project or catalog recipe is refused unless this
// directory's checkout holds the ref it launches.
func artifactShadowedOnBatchRefusal(name, repo, ref string) string {
	return fmt.Sprintf("a run on cloud-batch whose recipe this launcher cannot resolve is refused: the recipe %s travels in this task as text, but a run does not necessarily read it -- the repository layer at %s is read first, so a .fugaro/recipes/%s.yaml at %s is the recipe that runs, its mode included, whatever text the task carries. Nothing here can read %s's repository layer (%s is read from a checkout of its own; this directory has none that holds the ref), so nothing on this path can tell an artifact-producing recipe from any other -- and an artifact-producing run is never launched on cloud-batch, because a Batch run's account is not per-repository (docs/design/artifact-runs.md §8.5 and D11). Run this launch from a checkout of %s that holds %s, and the recipe the run reads there is the one judged", name, ref, name, ref, repo, ref, repo, ref)
}

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
