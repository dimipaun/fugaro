package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	scheduler "google.golang.org/api/cloudscheduler/v1"
	compute "google.golang.org/api/compute/v1"
	iam "google.golang.org/api/iam/v1"
	logging "google.golang.org/api/logging/v2"
	run "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"
	serviceusage "google.golang.org/api/serviceusage/v1"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The Batch half of `fugaro doctor` (plan 2026-10-19-compute-backend.md Task 8
// step 5, design compute-backend.md §4.4): the same questions `fugaro
// validate` asks offline, asked against the live project.
//
// What it reads, and why each one is here rather than in validate:
//
//   - The Compute and Batch APIs, in the project: a Batch run's VM cannot be
//     created, and its job cannot be submitted, while either is disabled. A
//     disabled API is invisible offline -- only the project knows.
//   - The preload image FAMILY the batch block names: the runs' bootDisk.image
//     is the family, not one of its generations (§1.2), and the family exists
//     only once the preload build has published a generation. An installation
//     that ran --provision-batch but whose preload build has not run yet has a
//     family name and no image behind it, which is exactly the state a launch
//     would fail in. The family's get is also where the socket gid is read
//     back: the generation the family serves labels itself with the gid the
//     preload build measured, and the gid this installation recorded is a copy
//     the next rebuild can leave behind, so the two are COMPARED (Task 10 step
//     2, design §2.2). A recorded zero is not compared: it means no socket was
//     recorded, and the backend's own refusal of a Docker run is Task 7's
//     shape, not this gate's.
//   - The price table (Task 10 step 2): a run's VM is priced from the compiled
//     table (internal/backend/batch/prices.go), so a region or a machine type
//     the table does not carry is a run launched with no cost -- and on this
//     backend that is the machine time the budget counts. Offline, like the
//     job definition, and here rather than in validate for the same reason:
//     the machine type is an installation fact.
//   - The network and the subnetwork the batch block names, and the job
//     definition those facts make: Batch refuses a no-external-IP job unless
//     both are named explicitly (Check 57a, §1.7), and a name that does not
//     exist in the project is a run that fails at create.
//   - The installation's log sink, `fugaro-jobs`: without it a Batch run's
//     task lines sit in _Default, where every project viewer reads them
//     (§1.5). The sink is installation Terraform, so it reaches a project only
//     when that apply runs there, and an installation from before the Batch arm
//     routes only Cloud Run's lines (docs/gcp-setup.md).
//   - The launchers' roles/batch.jobsEditor grant, which the compute module
//     narrows with a Condition to Fugaro-shaped job names
//     (gcp.LauncherJobsCondition): a launcher without it cannot start a run.
//   - The RUN accounts' own three grants (Task 10 step 2): each holds
//     roles/batch.agentReporter and roles/logging.logWriter on the project --
//     without the first a job's VM never reports task state and the job never
//     reaches a terminal state -- and the launchers hold
//     roles/iam.serviceAccountUser on the account the job runs as, which is
//     what creating a job that names a custom account takes. The account the
//     batch block names is checked to BE one of those accounts (step 4): a
//     published block is written to the runs bucket, and a writer with
//     objectAdmin there could otherwise name the reaper's account or the
//     deployer's, which on an installation whose launcher holds actAs is a
//     confused deputy.
//   - The reaper's invocation (Task 10 step 2): its account, the five
//     permissions of its custom role, the Cloud Run job that runs `fugaro
//     reaper` as that account and the Scheduler job that starts it. A Batch
//     run whose VM outlives its job leaks, and the reaper is the one thing
//     that sweeps one -- an installation enabled without it is the leak §3.4
//     is about.
//   - The job image's repository (Task 10 step 5, fifth entry): a
//     pull-through repository of the project serves whatever its upstream's
//     owner published, so a job image out of one is a stranger's code running
//     as the installation's own run account.
//   - The seal's pair (design §1.6, Task 14, the owner's answer P1): an
//     installation that mints budget tokens cannot launch a run on Batch at
//     all -- a launch carries the budget token's seal key in an environment
//     entry and a Batch job's variables are the job's own definition -- so
//     this is the last place an operator is told before paying for a launch
//     that is refused at create. It is here rather than in validate alone
//     because minting has a published-policy half no config answers; validate
//     refuses the half a config alone decides, offline.
//
// A read that cannot be made is a warning that says so and never a pass (the
// same rule doctor's other cloud checks follow), and a test configuration
// (no_auth with no endpoint for the API in question) is skipped, so no real
// project is read from one.
//
// THE GATE (Task 10 step 2, design §4.4 and §8.1 task 9). These checks are why
// `backend: cloud-batch` may be a name config validation accepts: an
// installation whose config SELECTS the backend has a doctor that FAILS until
// every one of them passes, which is what `batchSeverity` below is -- no
// severity, so doctor fails.
//
// What the gate does NOT do is hold a launch back. `fugaro run` reads none of
// these checks (grep: no doctorBatch call outside this file, doctor.go and the
// tests), so an installation that selects the name before it is provisioned
// still launches: the run's output goes to the project's `_Default` log bucket
// rather than to the installation's own log view, and a VM no reaper sweeps is
// left to its job's own timeout. Doctor is the operator's read before the
// first run, and SECURITY.md's "The VM backend, and the Docker daemon it gives
// a run", `fugaro backends ls`'s help and Check 124a of the live register all
// say so in as many words -- none of them may read as a fence that is not
// there. They are read for an installation that carries
// a batch block and does NOT select the name too, and there they stay warnings:
// such an installation is on the way to selecting it (or merely carries the
// block `fugaro setup --provision-batch` wrote), and a red doctor on a
// cloud-run installation would refuse work that is fine today.

// doctorBatchChecks are the checks above, in the order doctor prints them.
//
// An installation that carries no batch block gets none of them, with one
// exception: the one whose config SELECTS the backend. There the missing block
// is itself the finding -- every check below reads the block for the machine
// type, the network, the image and the account a run's VMs are built from, and
// an installation that selected the name without them is exactly the state the
// gate exists to refuse -- so the block's absence is answered by name rather
// than read as "not provisioned for Batch, nothing to say".
func doctorBatchChecks(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	if lc.Batch == nil {
		if lc.Backend() != backend.CloudBatch {
			return nil
		}
		return []doctorCheck{batchProblem(lc, "batch-block",
			"this installation selects backend: cloud-batch and its local config carries no batch block, so nothing on this path knows the machine type, the boot image, the network or the account a run's VMs would be built from (design compute-backend.md §4.4, §8.1 task 9). The block is what the installation's own apply writes, so a config that selects the backend without one is an installation that was never provisioned for it -- and a launch on it has no job definition to submit",
			"provision the backend (fugaro setup --provision-batch in "+lc.GCPProject+"), which writes the block and applies the compute root, then rerun fugaro doctor")}
	}
	var out []doctorCheck
	// The seal's pair is read first: it is the one finding on this path that no
	// apply clears, so an operator who has it reads it before the provisioning
	// below rather than after it.
	out = append(out, doctorBatchBudgetSeal(ctx, lc)...)
	out = append(out, doctorBatchAPIs(ctx, lc)...)
	out = append(out, doctorBatchJobDefinition(lc))
	out = append(out, doctorBatchCost(lc))
	out = append(out, doctorBatchImageFamily(ctx, lc))
	out = append(out, doctorBatchNetwork(ctx, lc)...)
	out = append(out, doctorBatchLogSink(ctx, lc)...)
	out = append(out, doctorBatchJobImage(ctx, lc))
	out = append(out, doctorBatchSecrets(ctx, lc))
	out = append(out, doctorBatchRunAccounts(ctx, lc, c)...)
	out = append(out, doctorBatchLauncherIAM(ctx, lc, c)...)
	out = append(out, doctorBatchReaper(ctx, lc)...)
	out = append(out, doctorBatchArtifactBoundary())
	return out
}

// batchSeverity is the severity a FAILING Batch check carries: no severity at
// all -- doctor's own hard failure -- for an installation whose config selects
// the backend, and "warning" for one that merely carries a batch block while it
// runs on cloud-run.
//
// The distinction is the whole of what the enable task hands over (design §4.4,
// §8.1 task 9): lifting the refusal in internal/localcfg is safe only because
// `fugaro doctor` is what refuses an installation that selected the name
// without the provisioning behind it. For an installation that did not select
// it, the same checks are the report Task 8 wrote them as: a batch block is
// work in progress, and a red doctor would refuse an installation that is
// running fine on Cloud Run.
func batchSeverity(lc *localcfg.Config) string {
	if lc.Backend() == backend.CloudBatch {
		return ""
	}
	return "warning"
}

// batchProblem is a failing Batch check, at the severity this installation's
// own selection decides (batchSeverity): the four fields every failing check
// here answers with.
func batchProblem(lc *localcfg.Config, id, problem, fix string) doctorCheck {
	return doctorCheck{ID: id, Severity: batchSeverity(lc), Problem: problem, Fix: fix}
}

// doctorBatchArtifactBoundary names the boundary Global Constraint 13 leaves
// standing, for an installation that carries a batch block: an
// artifact-producing run is not launched on this backend, and `fugaro
// validate` (`artifacts:` with `backend: cloud-batch`) with the launch's own
// gate (run.go's checkArtifactLaunch) are what refuse it. The constraint asks
// doctor to name it, and this is where an operator reads what the backend does
// not do.
//
// Informational, never a warning: the state it describes is the plan's own
// decision (docs/design/artifact-runs.md §8.5 and D11 — a Batch run's account
// is not per-repository), not a fault this installation can fix, and a warning
// no run of `fugaro update` could clear is the shape this file refuses for the
// launcher grant's operator case. Task 10's doctor gate is what makes that
// matter: every check has to pass before the enable, and an info check never
// fails doctor, so naming the boundary costs that gate nothing.
func doctorBatchArtifactBoundary() doctorCheck {
	return doctorCheck{ID: "batch-artifact-boundary", Severity: "info",
		Problem: "an artifact-producing run is never launched on cloud-batch (docs/design/artifact-runs.md §8.5 and D11): a Batch run's account is not per-repository, so the same run's inputs, artifacts and record would be readable and overwritable by every run on the profile. fugaro validate refuses `artifacts:` with `backend: cloud-batch`, and a launch naming an artifact recipe is refused by the launch's own gate",
		Fix:     "keep artifact-producing recipes on backend: cloud-run. This is the boundary a later plan removes, with the per-run prefix condition or the per-run account it is waiting for (plan 2026-10-19-compute-backend.md, Global Constraint 13)"}
}

// doctorBatchBudgetSeal is the pair no provisioning clears: an installation
// that mints budget tokens and selects `backend: cloud-batch`.
//
// Every launch of an installation that mints carries the budget token's seal
// key, and internal/backend/batch's jobDefinition refuses a launch carrying
// that environment entry before the job is created: a Batch job's `variables`
// are the job's definition -- a standing copy readable by every launcher the
// IAM Condition admits and by the reaper -- so the key has no carrier on this
// backend and the refusal is the owner's answer rather than a gap
// (docs/design/budget-token-channel.md, "The Batch backend refuses a sealed
// launch"; plan 2026-10-19-compute-backend.md Task 14 and P1). Minting is the
// installation's and an installation has one backend, so there is no per-run
// fallback: the whole installation stays on cloud-run.
//
// The design asks for this check by name -- the pair read at the gate rather
// than met at jobs.create's doorstep, after a token was minted and dropped --
// and Task 10 is the enable that made the pair reachable at all, since a name
// nothing selects cannot be minting on this backend
// (docs/design/compute-backend.md §1.6, §8.1 task 9).
//
// The minting question is budgetMintOn's, the same function a launch decides
// with: a gate that mints where a launch does not, or the other way round,
// would be a check disagreeing with the runs it is about. It is asked only of a
// config that selects the backend -- an installation that mints and runs on
// cloud-run has no such pair, and this check reads nothing for it.
func doctorBatchBudgetSeal(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "batch-budget-seal"
	if lc.Backend() != backend.CloudBatch {
		return nil
	}
	mints, err := (&cloudEnv{lc: lc}).budgetMintOn(ctx)
	if mints {
		return []doctorCheck{batchProblem(lc, id,
			"this installation mints budget tokens -- a budget database with a mode that is not off and a signer, or a published policy -- and its project config selects backend: cloud-batch (docs/design/compute-backend.md §1.6), so every launch of it is refused before the job is created. A launch carries the budget token's seal key in an environment entry, and a Batch job has no carrier for one: its variables are the job's definition, a standing copy readable by every launcher the IAM Condition admits and by the reaper (docs/design/budget-token-channel.md, \"The Batch backend refuses a sealed launch\"). Minting is the installation's, not a run's, so there is no per-run fallback and no budget-off run here",
			"keep this installation on backend: cloud-run, or stop minting on it (drop the published policy and the budget mode): the seal's carrier is a documented limit rather than something this installation can provision around (plan 2026-10-19-compute-backend.md Task 14, P1)")}
	}
	if err != nil {
		// The read a config alone cannot answer was attempted and failed: never
		// a pass, which is the rule every cloud check here follows. The mode
		// arm above has already answered what a config alone decides, so what
		// reaches here is the policy read.
		return []doctorCheck{batchUnchecked(id, "whether this installation mints budget tokens", batchReadFix, err, lc)}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// batchUnchecked is a read that could not be made, in the shape every cloud
// check here answers with: a warning naming what could not be read, never a
// silent pass.
func batchUnchecked(id, what, fix string, err error, lc *localcfg.Config) doctorCheck {
	return batchProblem(lc, id, "could not check "+what+": "+oneLineCLI(err.Error()), fix)
}

// batchRefused is a read a caller does not have the permission for, which for
// two of these checks is NOT the same finding as an absent resource: Check 57a
// measured that a binding or a role written moments before is not readable
// everywhere at once, so an installation whose compute apply has just finished
// answers this way while being correctly provisioned. The sentence says
// "propagating" and sends the operator to a rerun rather than to a
// re-provision -- the propagation delay is the enable task's one live hazard
// (plan 2026-10-19-compute-backend.md Task 10 step 3, design §8.1 task 9).
//
// internal/infra's propagating predicate is the tree's one definition of this
// shape for an apply; this is its read-side half: a 403/PERMISSION_DENIED
// answer from a policy or a role read, which is what a binding that has not
// propagated produces and also what a credential that may not read the project
// produces. The two are indistinguishable from here, so the message names both
// readings rather than picking one.
func batchRefused(id, what, fix string, lc *localcfg.Config) doctorCheck {
	return batchProblem(lc, id, "could not read "+what+": this credential was refused, which is either a grant that is still propagating (Check 57a measured that a freshly written binding is not readable for a while, design §8.1 task 9) or a credential that may not read it there", fix)
}

// batchSkipped is a check this configuration does not make: a test
// configuration (endpoints.no_auth) with no endpoint for the API in question,
// so no real project is read from it. It says so as an informational line and
// is never OK -- a pass for a read nobody made is the shape batchUnchecked
// exists to refuse, and info keeps a test configuration's doctor --strict
// green without claiming anything was checked.
func batchSkipped(id, what, endpoint string) doctorCheck {
	return doctorCheck{ID: id, Severity: "info",
		Problem: "not checked: " + what + " is not read in a test configuration (endpoints.no_auth with no endpoints." + endpoint + ")"}
}

// batchReadFix is what to do about a read that failed: the operator's own
// credentials and the project are the two things that decide it.
const batchReadFix = "rerun fugaro doctor with access to the project, or as an operator"

// doctorBatchAPIs asks whether the Compute and Batch APIs are enabled in the
// project. Both are the installation module's `google_project_service`
// resources, so a project that ran the installation's apply has them.
func doctorBatchAPIs(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.ServiceUsage == "" {
		return nil
	}
	svc, err := serviceusage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ServiceUsage)...)
	if err != nil {
		return []doctorCheck{batchUnchecked("batch-apis", "whether the Compute and Batch APIs are enabled in "+lc.GCPProject, batchReadFix, err, lc)}
	}
	var disabled []string
	for _, s := range []string{batchServiceCompute, batchServiceBatch} {
		st, err := svc.Services.Get("projects/" + lc.GCPProject + "/services/" + s).Context(ctx).Do()
		if err != nil {
			return []doctorCheck{batchUnchecked("batch-apis", "whether "+s+" is enabled in "+lc.GCPProject, batchReadFix, err, lc)}
		}
		// The state is the API's own string, printed inside this check's
		// sentence: one line of it, like every other value a project answers
		// (batchUnchecked's read errors, the sink's destination).
		if st.State != "ENABLED" {
			disabled = append(disabled, fmt.Sprintf("%s is %s", s, oneLineCLI(st.State)))
		}
	}
	if len(disabled) > 0 {
		return []doctorCheck{batchProblem(lc, "batch-apis",
			"a Batch run needs both APIs enabled in "+lc.GCPProject+", and "+strings.Join(disabled, ", ")+": the installation module enables them, so this project's apply has not run since they were added (design compute-backend.md §4.4)",
			"run fugaro update in "+lc.GCPProject+", which applies the installation root")}
	}
	return []doctorCheck{{ID: "batch-apis", OK: true}}
}

// The two APIs a Batch run needs, as Service Usage names them.
const (
	batchServiceCompute = "compute.googleapis.com"
	batchServiceBatch   = "batch.googleapis.com"
)

// doctorBatchJobDefinition is the job definition the installation's own facts
// make, checked by the backend that would send it: the network and the
// subnetwork are named, they are full resource names, the subnetwork is in the
// backend's region, the run account is not the default Compute Engine account,
// and the image and the recorded socket gid are answers a job can be built
// from (batch.CheckOptions, which is batch.New's own validation). Every one of
// them is a refusal at launch, which is what a check here is for: the operator
// reads it now rather than out of a failed create.
//
// It is offline -- it reads no cloud API -- and it is here rather than in
// validate because the batch block is an installation fact that only doctor
// and the opener read: what validate answers about is a checkout's fugaro.yaml.
func doctorBatchJobDefinition(lc *localcfg.Config) doctorCheck {
	o := batchOptions(lc)
	if err := batch.CheckOptions(&o); err != nil {
		return batchProblem(lc, "batch-job-definition",
			"a Batch run's job definition is one this build refuses, so no run on this installation can start: "+oneLineCLI(err.Error()),
			"fix the batch block of this installation's local config (fugaro update writes it; the fields are documented in docs/gcp-setup.md)")
	}
	return doctorCheck{ID: "batch-job-definition", OK: true}
}

// doctorBatchCost is the price table's presence for this installation's run
// (Task 10 step 2, design §5.2): the machine type and the region a run's VM is
// are priced from the table compiled into this build, so a pair the table does
// not carry is a run whose cost the budget cannot count -- and on this backend
// the VM's machine time is what the budget IS. It reads no cloud API: an
// installation that recorded a type and a region no release prices is refused
// here rather than after a launch the budget cannot charge for.
func doctorBatchCost(lc *localcfg.Config) doctorCheck {
	const id = "batch-cost"
	machineType := lc.Batch.MachineType
	if machineType == "" {
		// Empty means the backend's own default, which checkVM fills in
		// (batch.DefaultMachineType): the same value the job definition is
		// built with.
		machineType = batch.DefaultMachineType()
	}
	if _, err := batch.PricesFor(lc.Region, machineType); err != nil {
		return batchProblem(lc, id,
			fmt.Sprintf("a Batch run on this installation cannot be priced: %s. The price table is compiled into this build (internal/backend/batch/prices.go), and the cost a run is charged for is its VM's machine time (design compute-backend.md §5.2)", oneLineCLI(err.Error())),
			"set batch.machine_type to one this build prices, or run in a region it carries (the table's regions are listed in internal/backend/batch/prices.go; a region added there needs a Fugaro release that carries it)")
	}
	return doctorCheck{ID: id, OK: true}
}

// doctorBatchImageFamily reads the preload image family the runs' VMs boot.
// The batch block records it as the family resource name the preload stage
// publishes, so the read is the family's own get -- the request the API answers
// with the family's newest generation, which is what a VM's bootDisk.image
// resolves. NOT_FOUND is the state an installation is in between
// --provision-batch and the preload build's first run: the name is right and
// there is nothing behind it yet.
func doctorBatchImageFamily(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-image-family"
	if lc.Batch.BootImage == "" {
		// localcfg refuses a block without a boot image only when it is
		// written, and Batch.BootImage is optional there: a block that leaves
		// it empty is already refused by the job-definition check, which is
		// where it belongs, so this check has nothing to read.
		return doctorCheck{ID: id, OK: true}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.Compute == "" {
		return batchSkipped(id, "the preload image family", "compute")
	}
	family := pathTail(lc.Batch.BootImage)
	project := resourceProject(lc.Batch.BootImage, lc.GCPProject)
	svc, err := compute.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Compute)...)
	if err != nil {
		return batchUnchecked(id, "the preload image family "+family+" in "+project, batchReadFix, err, lc)
	}
	img, err := svc.Images.GetFromFamily(project, family).Context(ctx).Do()
	switch {
	case err == nil:
		return batchImageFamilyGID(lc, img, project, family)
	case isNotFound(err):
		return batchProblem(lc, id, fmt.Sprintf("the preload image family projects/%s/global/images/family/%s does not exist in %s: a run's bootDisk.image names the family (not one of its generations), so this installation's VMs have nothing to boot until the preload build has published a generation (design compute-backend.md §1.2, §4.4)", project, family, project),
			"run the preload rebuild (fugaro setup --provision-batch in "+project+", or the fugaro-preload-rebuild Scheduler job), then rerun fugaro doctor")
	default:
		return batchUnchecked(id, "the preload image family "+family+" in "+project, batchReadFix, err, lc)
	}
}

// batchImageFamilyGID compares the socket gid this installation recorded with
// the one the generation the family serves carries (plan
// 2026-10-19-compute-backend.md Task 10 step 2, design §2.2).
//
// Why it is a comparison and not a trust: every rebuild of the preloaded image
// re-reads the socket's gid off the builder VM and labels the new generation
// with it (Task 9), so the number batch.socket_gid was written with is a copy
// that a later generation can leave behind. A Docker run whose --group-add
// names a gid the image no longer has comes up unable to open the socket, and
// this is the read that refuses the mismatch by name, before the run that
// would pay for it.
//
// A recorded ZERO is not a mismatch: it means this installation has no socket
// recorded at all, and what that costs is a Docker run refused at launch by
// the backend's own checkDocker -- Task 7's refusal, not this gate's (the
// plan's own tests say so: "a recorded zero is asserted not to be refused").
// An installation whose config records none may be one that never asked for
// Docker, and refusing the whole installation over it would be this gate
// deciding a question the config already answers.
func batchImageFamilyGID(lc *localcfg.Config, img *compute.Image, project, family string) doctorCheck {
	const id = "batch-image-family"
	if lc.Batch.SocketGID == 0 {
		return doctorCheck{ID: id, OK: true}
	}
	var raw string
	if img != nil && img.Labels != nil {
		raw = strings.TrimSpace(img.Labels[infra.SocketGIDLabel])
	}
	gid, err := strconv.Atoi(raw)
	if raw == "" || err != nil {
		// The generation carries no readable mark: the family's get answered a
		// generation whose label is missing or is not a number. That is not a
		// mismatch -- it is nothing to compare with, and reporting it as equal
		// would be the silent pass this file refuses.
		return batchProblem(lc, id,
			fmt.Sprintf("the generation the preload image family projects/%s/global/images/family/%s serves carries no readable %s label (it is %s), so the batch.socket_gid of %d this installation recorded cannot be checked against the image a run boots: the preload build writes the gid it read off the builder VM as that label, and a generation that carries none is one this build cannot verify (design compute-backend.md §2.2)", project, family, infra.SocketGIDLabel, oneLineCLI(quoteOrEmpty(raw)), lc.Batch.SocketGID),
			"rebuild the preloaded image (the fugaro-preload-rebuild Scheduler job, or fugaro setup --provision-batch), which re-reads the socket's gid and labels the new generation with it, then rerun fugaro doctor")
	}
	if gid != lc.Batch.SocketGID {
		return batchProblem(lc, id,
			fmt.Sprintf("this installation recorded batch.socket_gid %d, and the generation the preload image family %s serves in %s carries %s=%d: every rebuild re-reads the socket's gid off the builder VM and labels the new generation with it (design compute-backend.md §2.2), so a recorded value the image has moved past is a Docker run whose --group-add names a gid the image no longer has -- the container comes up unable to open the socket", lc.Batch.SocketGID, family, project, infra.SocketGIDLabel, gid),
			fmt.Sprintf("set batch.socket_gid to %d in this installation's local config (the value the family's current generation carries), then rerun fugaro doctor", gid))
	}
	return doctorCheck{ID: id, OK: true}
}

// quoteOrEmpty renders a label value for one of the sentences above: the value
// in quotes when there is one, and "<empty>" when there is not, so a missing
// label and an empty one read differently from a value in a message.
func quoteOrEmpty(s string) string {
	if s == "" {
		return "<empty>"
	}
	return strconv.Quote(s)
}

// doctorBatchNetwork reads the network and the subnetwork the batch block
// names. Both are required for a no-external-IP job (Batch refuses one without
// them, Check 57a, §1.7), and both are the installation module's own
// resources: a name that is recorded and absent is either a typo or an apply
// that has not run.
func doctorBatchNetwork(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Compute == "" {
		return nil
	}
	network, subnetwork := lc.Batch.Network, lc.Batch.Subnetwork
	if network == "" || subnetwork == "" {
		// A block that names neither is refused by the job-definition check.
		return nil
	}
	svc, err := compute.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Compute)...)
	if err != nil {
		return []doctorCheck{batchUnchecked("batch-network", "the network and subnetwork a run's VMs are placed in", batchReadFix, err, lc)}
	}
	var out []doctorCheck
	netProject := resourceProject(network, lc.GCPProject)
	netName := pathTail(network)
	if _, err := svc.Networks.Get(netProject, netName).Context(ctx).Do(); err != nil {
		if !isNotFound(err) {
			// The pair is read independently, so one read that cannot be made
			// does not hide the other resource's finding.
			out = append(out, batchUnchecked("batch-network", "the network "+network, batchReadFix, err, lc))
		} else {
			out = append(out, batchProblem(lc, "batch-network",
				"the network "+network+" that the batch block names is not in "+netProject+": a run's VMs are placed in it explicitly, because Batch refuses a no-external-IP job unless the network and the subnetwork are named (design compute-backend.md §1.7)",
				"check batch.network in the local config, or run fugaro setup --provision-batch in "+netProject+" which creates the installation's own network"))
		}
	} else {
		out = append(out, doctorCheck{ID: "batch-network", OK: true})
	}
	return append(out, doctorBatchSubnetwork(ctx, svc, lc, subnetwork)...)
}

// doctorBatchSubnetwork is the subnetwork half of doctorBatchNetwork, read in
// the project its name names for the same reason (resourceProject): a shared
// VPC's subnetwork is in the host project.
func doctorBatchSubnetwork(ctx context.Context, svc *compute.Service, lc *localcfg.Config, subnetwork string) []doctorCheck {
	subProject := resourceProject(subnetwork, lc.GCPProject)
	region, subName := subnetParts(subnetwork)
	if _, err := svc.Subnetworks.Get(subProject, region, subName).Context(ctx).Do(); err != nil {
		if !isNotFound(err) {
			return []doctorCheck{batchUnchecked("batch-subnetwork", "the subnetwork "+subnetwork, batchReadFix, err, lc)}
		}
		return []doctorCheck{batchProblem(lc, "batch-subnetwork",
			"the subnetwork "+subnetwork+" that the batch block names is not in "+subProject+": the VMs are placed in it explicitly (Batch refuses a no-external-IP job unless both are named, design compute-backend.md §1.7), and the subnetwork is where a run's VMs are, so one in a second project or region is a run this installation cannot see",
			"check batch.subnetwork in the local config, or run fugaro setup --provision-batch in "+subProject+" which creates the installation's own subnetwork")}
	}
	return []doctorCheck{{ID: "batch-subnetwork", OK: true}}
}

// resourceProject is the project a full resource name names -- parts[1] of
// projects/<p>/... -- and fallback for a name that has no such shape (which
// the job-definition check refuses first). The checks above read the resource
// THE NAME NAMES, not this installation's own of the same tail: a subnetwork
// of a shared VPC is placed in the host project while the run's VMs are in
// this one (design compute-backend.md §1.7 names the pair explicitly for
// exactly that reason), so reading lc.GCPProject for a name that points
// elsewhere answers about a network the run does not use -- a false OK when
// this project happens to hold a same-named one, and a false "not there" when
// only the named project has it.
func resourceProject(name, fallback string) string {
	if parts := strings.Split(name, "/"); len(parts) >= 2 && parts[0] == "projects" && parts[1] != "" {
		return parts[1]
	}
	return fallback
}

// doctorBatchLogSink reads the installation's log sink, fugaro-jobs, and the
// exclusion that goes with it. What it checks is the three facts that decide
// whether a Batch run's lines are isolated: the sink's destination is the
// installation's log bucket, its filter carries the Batch arm, and the
// `fugaro-jobs-from-default` exclusion carries that arm too. Without any of
// them a Batch run's task lines land in _Default, where every project viewer
// reads them (§1.5, docs/gcp-setup.md). Both filters are installation
// Terraform's, pinned there byte for byte
// (deploy/terraform/terraform_test.go); this reads the installed ones, which an
// apply only rewrites when the owner runs it.
//
// The exclusion is the sink's counterpart and it is read for the same reason:
// the sink routes a run's lines into the installation's bucket, and the
// exclusion is what keeps them out of _Default once they are routed. An
// installation that applied the sink without the exclusion -- or whose
// exclusion predates the Batch arm -- leaves every Batch run's task output
// readable by every project viewer while `fugaro logs` reads the view.
//
// The arm itself is built from the value the READER builds its own filter from
// (batchSinkArm, below): a filter that spelled the resource type differently
// would route nothing while the reader, which selects one job's lines by
// labels.job_uid inside that type, found nothing either -- and a check with its
// own copy of the string could not see the disagreement.
func doctorBatchLogSink(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "batch-log-sink"
	if lc.Endpoints.NoAuth && lc.Endpoints.Logging == "" {
		return []doctorCheck{batchSkipped(id, "the log sink "+infra.LogSink+" and the exclusion "+infra.LogExclusion, "logging")}
	}
	svc, err := logging.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Logging)...)
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the log sink "+infra.LogSink, batchReadFix, err, lc)}
	}
	out := []doctorCheck{doctorBatchSink(ctx, svc, lc, id)}
	return append(out, doctorBatchExclusion(ctx, svc, lc)...)
}

// doctorBatchSink is the sink half: it exists, it routes to the installation's
// bucket, and its filter carries the Batch arm.
func doctorBatchSink(ctx context.Context, svc *logging.Service, lc *localcfg.Config, id string) doctorCheck {
	sink, err := svc.Projects.Sinks.Get("projects/" + lc.GCPProject + "/sinks/" + infra.LogSink).Context(ctx).Do()
	switch {
	case isNotFound(err):
		return batchProblem(lc, id, "the log sink "+infra.LogSink+" is not in "+lc.GCPProject+", so a Batch run's task lines are not routed into the installation's log bucket: they sit in _Default, where every project viewer reads them (design compute-backend.md §1.5). The log isolation is installation Terraform, so an installation whose apply predates the Batch arm still has the sink and routes only Cloud Run's lines",
			"run fugaro update in "+lc.GCPProject+", which applies the installation root's logging")
	case err != nil:
		return batchUnchecked(id, "the log sink "+infra.LogSink+" and its filter", batchReadFix, err, lc)
	}
	if want := "logging.googleapis.com/" + logBucketPath(lc); sink.Destination != want {
		// The destination is the sink's own string, printed inside this
		// check's sentence: one line of it, as the read errors above are
		// (the installed destination is a value a project answers, not one
		// this config wrote).
		return batchProblem(lc, id, fmt.Sprintf("the log sink %s routes to %s, not to this installation's log bucket %s (design compute-backend.md §1.5)", infra.LogSink, oneLineCLI(sink.Destination), want),
			"run fugaro update in "+lc.GCPProject+", which applies the installation root's logging")
	}
	if !strings.Contains(sink.Filter, batchSinkArm) {
		return batchProblem(lc, id, "the log sink "+infra.LogSink+"'s filter has no Batch arm ("+batchSinkArm+"), so a Batch job's lines are not routed into the installation's log bucket and `fugaro logs` would find nothing in the view (design compute-backend.md §1.5): the installation has not been applied since that arm was added",
			"run fugaro update in "+lc.GCPProject+" (or fugaro doctor after the owner applies the installation root)")
	}
	return doctorCheck{ID: id, OK: true}
}

// doctorBatchExclusion is the exclusion half: fugaro-jobs-from-default exists
// and carries the Batch arm. It is why the routed lines leave _Default at all
// (modules/installation/logging.tf applies it to the _Default sink, which
// Terraform never manages), and an installation that skipped it -- or whose
// exclusion predates the arm -- keeps every Batch run's task output readable by
// every project viewer there.
func doctorBatchExclusion(ctx context.Context, svc *logging.Service, lc *localcfg.Config) []doctorCheck {
	const id = "batch-log-exclusion"
	ex, err := svc.Projects.Exclusions.Get("projects/" + lc.GCPProject + "/exclusions/" + infra.LogExclusion).Context(ctx).Do()
	switch {
	case isNotFound(err):
		return []doctorCheck{batchProblem(lc, id, "the log exclusion "+infra.LogExclusion+" is not in "+lc.GCPProject+", so the lines the installation's sink routes stay in _Default as well: every project viewer reads a Batch run's task output there, which is the isolation §1.5 is about (design compute-backend.md §1.5, docs/gcp-setup.md)",
			"run fugaro update in "+lc.GCPProject+", which applies the installation root's logging")}
	case err != nil:
		return []doctorCheck{batchUnchecked(id, "the log exclusion "+infra.LogExclusion+" and its filter", batchReadFix, err, lc)}
	}
	if !strings.Contains(ex.Filter, batchSinkArm) {
		return []doctorCheck{batchProblem(lc, id, "the log exclusion "+infra.LogExclusion+"'s filter has no Batch arm ("+batchSinkArm+"), so a Batch job's lines are excluded from nothing and sit in _Default where every project viewer reads them (design compute-backend.md §1.5): the installation has not been applied since that arm was added",
			"run fugaro update in "+lc.GCPProject+", which applies the installation root's logging")}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// batchSinkArm is the Batch arm of the installation's log filters, as
// deploy/terraform/gcp/modules/installation/logging.tf writes it: the resource
// type a Batch job's lines carry, pinned by the same fugaro=managed marker as
// the Cloud Run arm. The filters themselves are Terraform's and its own test
// holds them byte for byte; doctor reads the installed ones and checks that
// this arm is in them.
//
// It is built from the values the READER uses rather than written out: the
// resource type is internal/backend/batch's own (batch.JobResourceType), which
// is what `fugaro logs` selects one job's lines inside by labels.job_uid, and
// the marker is gcp's (gcp.LabelManaged/gcp.ManagedValue), which is what the
// job definition labels every job with. A sink whose arm spelled either
// differently routes lines nobody reads, and a check holding a literal copy
// could not see it.
var batchSinkArm = `resource.type=` + strconv.Quote(batch.JobResourceType) +
	` AND labels.` + strconv.Quote(gcp.LabelManaged) + `=` + strconv.Quote(gcp.ManagedValue)

// logBucketPath is the installation's log bucket as the sink's destination
// names it: the bucket of the log view this installation reads its runs
// through, else the installation module's own bucket. Both are the same
// installation fact (localcfg.LogView is written from the module's output), and
// the view's path carries the bucket.
func logBucketPath(lc *localcfg.Config) string {
	bucket := infra.LogBucket
	if parts := strings.Split(lc.LogView, "/"); len(parts) == 8 && parts[6] == "views" {
		bucket = parts[5]
	}
	return "projects/" + lc.GCPProject + "/locations/global/buckets/" + bucket
}

// doctorBatchLauncherIAM reads the launchers' roles/batch.jobsEditor grant,
// which the compute module writes as a conditioned binding: Fugaro-shaped job
// names in this project and region, and nothing else
// (gcp.LauncherJobsCondition, the module's own expression). A launcher without
// it cannot start a run; a launcher with an unconditioned one can start any job
// in the project, which is the widening the condition exists to prevent.
//
// A policy read that is refused does not report a missing grant: 57a found a
// freshly created grant is not visible to a binding read for a while, so the
// two states are indistinguishable from here and neither is reported as fact.
// The same wording covers a grant that is absent for any other reason -- what
// doctor can say is what it read.
func doctorBatchLauncherIAM(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	// The launcher list is terraform.operators, and it is the WHOLE of it: the
	// list the compute root hands var.launchers (init_compute.go) and the one
	// the module's launcher_jobs_editor is for_each over, admins included,
	// because an admin operates too. The two lists reach the root as two
	// separate variables (infra.ComputeSpec.Launchers/Operators,
	// roots/compute/main.tf), and its var.operators hold actAs on the preload
	// account and nothing else (modules/compute/iam.tf, design §8.1 task 6).
	//
	// Subtracting the admins here (roles.go's launcherOnly, removed) read a
	// different list from the one the module grants: a member on both lists
	// was reported as missing a grant the module does write, and an
	// installation whose two lists held the same person was read as having no
	// launchers at all, so the check returned nothing about the one member it
	// was about.
	launchers := sortedMembers(lc.Terraform.Operators)
	if c == nil {
		return nil
	}
	const id = "batch-launcher-iam"
	if len(launchers) == 0 {
		// Nobody is recorded as a launcher, so no member holds the Batch grant
		// this check is about and no run can start. The config is where that
		// is fixed, and the Fix names the door that writes it: silence here is
		// what left the empty list invisible.
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: "terraform.operators in this machine's config holds no member, so nobody holds the launchers' roles/batch.jobsEditor grant in " + lc.GCPProject + " and no run can start there",
			Fix:     "name who may start and watch runs: run fugaro update --operator user:<address> (a whole list, which writes the roles' node and the config)"}}
	}
	p, err := c.Projects.GetIamPolicy(lc.GCPProject, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if isAccessDenied(err) {
		return []doctorCheck{batchRefused(id, "the IAM policy of "+lc.GCPProject+" for the launchers' roles/batch.jobsEditor grant", "an operator runs fugaro doctor, which can read the project's policy", lc)}
	}
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the launchers' roles/batch.jobsEditor grant in "+lc.GCPProject, batchReadFix, err, lc)}
	}
	want := gcp.LauncherJobsCondition(lc.GCPProject, lc.Region)
	var missing, misconditioned []string
	for _, m := range launchers {
		ok, exact := batchEditorGrantFor(p, m, want)
		switch {
		case !ok:
			missing = append(missing, m)
		case !exact:
			misconditioned = append(misconditioned, m)
		}
	}
	switch {
	case len(misconditioned) > 0 && len(missing) > 0:
		// Both conditions, in one finding, because the two lists are
		// independent: a launcher holding an unconditioned grant says nothing
		// about whether another holds no grant at all, and the one that cannot
		// start a run is the one an operator has to act on. Reporting the
		// widening alone drops the second — a project where one launcher holds
		// an unconditioned grant and another holds none is exactly that shape,
		// and the missing half is not visible in the policy the excess one is
		// read from.
		return []doctorCheck{batchProblem(lc, id,
			fmt.Sprintf("%s hold roles/batch.jobsEditor in %s without the compute module's condition (%s): the grant is conditioned on Fugaro-shaped job names in this project and region, and an unconditioned one may start any job in the project. And %s do not hold a roles/batch.jobsEditor grant there with that condition at all, so they cannot start a Batch run (design compute-backend.md §3.7, §4.4)", strings.Join(misconditioned, ", "), lc.GCPProject, want, strings.Join(missing, ", ")),
			"run fugaro update in "+lc.GCPProject+", which writes the conditioned grant for every launcher (or, if a grant was just written, rerun fugaro doctor once it propagates)")}
	case len(misconditioned) > 0:
		return []doctorCheck{batchProblem(lc, id,
			fmt.Sprintf("%s hold roles/batch.jobsEditor in %s without the compute module's condition (%s): the grant is conditioned on Fugaro-shaped job names in this project and region, and an unconditioned one may start any job in the project (design compute-backend.md §3.7)", strings.Join(misconditioned, ", "), lc.GCPProject, want),
			"run fugaro update in "+lc.GCPProject+", which writes the conditioned grant")}
	case len(missing) > 0:
		return []doctorCheck{batchProblem(lc, id,
			fmt.Sprintf("%s do not hold a roles/batch.jobsEditor grant in %s with the compute module's condition (%s), so they cannot start a Batch run. A grant that was just made may not be visible to this read yet -- on a freshly created project Check 57a found the same, which is why the message says so and this is not reported as a missing grant (design compute-backend.md §4.4)", strings.Join(missing, ", "), lc.GCPProject, want),
			"run fugaro update in "+lc.GCPProject+" (or, if the grant was just written, rerun fugaro doctor once it propagates)")}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// batchEditorGrantFor reports whether member holds roles/batch.jobsEditor in p,
// and whether every one of its bindings carries exactly the wanted condition
// expression. ok is false when the member holds no such binding at all; exact
// is false when one of the bindings it holds is unconditioned or carries
// another condition (both are grants a launcher does not need, and the
// unconditioned one is the widening the condition exists to prevent).
func batchEditorGrantFor(p *crm.Policy, member, want string) (ok, exact bool) {
	if p == nil {
		return false, false
	}
	exact = true
	for _, b := range p.Bindings {
		if b.Role != batchLauncherRole || !slices.Contains(b.Members, member) {
			continue
		}
		ok = true
		if b.Condition == nil || b.Condition.Expression != want {
			exact = false
		}
	}
	return ok, exact
}

// batchLauncherRole is the predefined role the compute module narrows for the
// launchers. It is written here as the string the module writes, not as a
// constant of this package: no Go side creates the binding, and the module's
// own grant check (tf.Cover) is what holds the two together.
const batchLauncherRole = "roles/batch.jobsEditor"

// doctorBatchRunAccounts reads the identity a Batch run's VM has (Task 10 step
// 2, design §3.2): the installation's run-class accounts, the two project roles
// each of them needs, whether the account the batch block names is one of them
// (step 4), and whether the launchers may act as them.
//
// The accounts are LISTED rather than derived from the config, for the reason
// infra.JobRunAccounts gives: the local config records workflow names and not
// the accounts the repository roots created for them. An account that is
// missing here is a run whose VM never reports task state, and one that is
// named by the batch block and is not in the list is the configured account a
// foreign write put there.
func doctorBatchRunAccounts(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	const id = "batch-run-accounts"
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" {
		return []doctorCheck{batchSkipped(id, "the installation's run accounts and the grants on them", "iam")}
	}
	svc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the installation's run accounts", batchReadFix, err, lc)}
	}
	accounts, err := infra.JobRunAccounts(ctx, &infra.Clients{IAM: svc}, lc.GCPProject)
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the installation's run accounts", batchReadFix, err, lc)}
	}
	out := []doctorCheck{doctorBatchAccountMembership(lc, id, accounts)}
	out = append(out, doctorBatchRunAccountRoles(ctx, lc, c, accounts)...)
	return append(out, doctorBatchActAs(ctx, lc, svc, accounts)...)
}

// doctorBatchAccountMembership is step 4's requirement: the account the batch
// block names -- which `fugaro update` publishes to the runs bucket, and which
// a machine reaching that bucket reads as its own -- has to BE one of the
// installation's run accounts.
//
// Why it is a membership and not a shape: the batch block is written to a
// shared document, and the accounts it could otherwise name are other
// identities of the same project -- the reaper's, the deployer's, the history
// account's. On an installation whose launcher holds actAs on the account a
// published block names, a block naming one of those is a confused deputy: a
// run whose VM runs as the account that deletes jobs, or as the account that
// mints credentials. Requiring the account to be a run-class account of this
// installation is the narrowest form of the rule that closes it, and it is the
// one the project itself can answer (the account's display name is the marker
// the repository roots write).
func doctorBatchAccountMembership(lc *localcfg.Config, id string, accounts []string) doctorCheck {
	// No run account at all is its own finding and is tested first: the
	// membership below would otherwise report a project that never created one,
	// naming an account that does not exist, as a block that named the wrong
	// identity -- a different fault with a different fix.
	if len(accounts) == 0 {
		return batchProblem(lc, id,
			"this installation's project has no Fugaro run account at all: the accounts a Batch job's VM runs as are the run-class accounts the repository roots created, and with none of them every job this installation launched would be one whose VM cannot report its state (design compute-backend.md §3.2). The accounts are found by the display name the repository roots write (infra.JobRunAccounts)",
			fmt.Sprintf("apply the repository roots for this installation's workflows (fugaro update in %s), then rerun fugaro doctor", lc.GCPProject))
	}
	// An empty service_account is the job-definition check's finding, not this
	// one's (batch.CheckOptions refuses it where the block is read).
	if lc.Batch.ServiceAccount == "" || slices.Contains(accounts, "serviceAccount:"+lc.Batch.ServiceAccount) {
		return doctorCheck{ID: id, OK: true}
	}
	return batchProblem(lc, id,
		fmt.Sprintf("the batch block names %s as the account a run's VMs run as, and it is not one of this installation's run accounts (%s): a published block is written to the runs bucket, so a machine that reads it could otherwise name the reaper's account, the deployer's or the installation's history account as the identity of its own run -- a confused deputy on an installation whose launcher holds actAs on that account (design compute-backend.md §3.2, §3.7)", lc.Batch.ServiceAccount, nameListOrNone(accounts)),
		fmt.Sprintf("set batch.service_account to one of this installation's run accounts (fugaro update writes the block from the project's own accounts), or create the account and run fugaro update in %s", lc.GCPProject))
}

// nameListOrNone renders a list of accounts for a sentence, and says "none"
// for an empty one, so a message never ends in a colon with nothing after it.
func nameListOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// doctorBatchRunAccountRoles reads the two project roles every run account
// needs (modules/compute/iam.tf, Task 10 step 2): roles/batch.agentReporter,
// without which a job's VM never reports task state and the job never reaches
// a terminal state -- the run then sits until the queue deadline fires it --
// and roles/logging.logWriter, without which the VM's own lines never arrive
// and `fugaro logs` reads an empty view.
//
// A refused policy read is not a missing grant, for the reason
// doctorBatchLauncherIAM gives: a grant written moments ago is not readable
// everywhere at once (Check 57a), so the two states are indistinguishable from
// here and neither is reported as fact.
func doctorBatchRunAccountRoles(ctx context.Context, lc *localcfg.Config, c *crm.Service, accounts []string) []doctorCheck {
	const id = "batch-run-account-roles"
	if len(accounts) == 0 || c == nil {
		// No account means nothing to read a grant for, and the membership
		// check above is the finding.
		return nil
	}
	p, err := c.Projects.GetIamPolicy(lc.GCPProject, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if isAccessDenied(err) {
		return []doctorCheck{batchRefused(id, "the run accounts' roles/batch.agentReporter and roles/logging.logWriter in "+lc.GCPProject, "an operator runs fugaro doctor, which can read the project's policy", lc)}
	}
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the run accounts' roles/batch.agentReporter and roles/logging.logWriter in "+lc.GCPProject, batchReadFix, err, lc)}
	}
	var missing []string
	for _, a := range accounts {
		for _, role := range []string{batchAgentReporterRole, batchLogWriterRole} {
			if !policyHasRole(p, role, a) {
				missing = append(missing, a+" ("+role+")")
			}
		}
	}
	if len(missing) > 0 {
		return []doctorCheck{batchProblem(lc, id,
			"these run accounts do not hold what a Batch job's VM runs on: "+strings.Join(missing, ", ")+". roles/batch.agentReporter is what reports a task's state -- without it the job never reaches a terminal state and the run sits until the queue deadline fires it -- and roles/logging.logWriter is what its own lines need, without which fugaro logs reads an empty view (design compute-backend.md §3.2, modules/compute/iam.tf)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's grants (or, if a grant was just written, rerun fugaro doctor once it propagates)")}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// policyHasRole reports whether p grants role to member, through any binding.
func policyHasRole(p *crm.Policy, role, member string) bool {
	if p == nil {
		return false
	}
	for _, b := range p.Bindings {
		if b.Role == role && slices.Contains(b.Members, member) {
			return true
		}
	}
	return false
}

// The two project roles a Batch job's service account needs, as
// modules/compute/iam.tf writes them (Batch's own documentation, checked
// 2026-10-15).
const (
	batchAgentReporterRole = "roles/batch.agentReporter"
	batchLogWriterRole     = "roles/logging.logWriter"
)

// doctorBatchActAs reads the launchers' roles/iam.serviceAccountUser on each
// run account: creating a job that names a custom account takes actAs on that
// account, and the module writes it as the cross product of the two lists --
// a launcher holds it on the accounts this installation named and on no others
// (modules/compute/iam.tf, design §3.7).
//
// The read is per account and the account is what it is on: a launcher holding
// the role project-wide is the widening the module avoids, and doctor cannot
// see that from here (a project-wide grant would not be visible in the
// account's own policy). What this answers is the direction that stops a
// launch: a launcher without actAs on the account a job names cannot create
// that job at all.
func doctorBatchActAs(ctx context.Context, lc *localcfg.Config, svc *iam.Service, accounts []string) []doctorCheck {
	const id = "batch-run-account-actas"
	launchers := sortedMembers(lc.Terraform.Operators)
	if len(launchers) == 0 || len(accounts) == 0 {
		return nil
	}
	var missing []string
	for _, a := range accounts {
		email := strings.TrimPrefix(a, "serviceAccount:")
		p, err := svc.Projects.ServiceAccounts.GetIamPolicy("projects/" + lc.GCPProject + "/serviceAccounts/" + email).
			OptionsRequestedPolicyVersion(3).Context(ctx).Do()
		if err != nil {
			return []doctorCheck{batchUnchecked(id, "the launchers' actAs on the run account "+email, batchReadFix, err, lc)}
		}
		for _, m := range launchers {
			if !iamPolicyHasRole(p, batchActAsRole, m) {
				missing = append(missing, m+" on "+email)
			}
		}
	}
	if len(missing) > 0 {
		return []doctorCheck{batchProblem(lc, id,
			"these launchers cannot act as the run account their own jobs name, so creating a job that names it is refused: "+strings.Join(missing, ", ")+". Creating a Batch job that names a custom service account takes roles/iam.serviceAccountUser on that account (design compute-backend.md §3.7, cloud.google.com/batch/docs/create-run-job-custom-service-account)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's launcher grants for every run account (or, if a grant was just written, rerun fugaro doctor once it propagates)")}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// iamPolicyHasRole is policyHasRole for a service account's own IAM policy,
// which is an iam.Policy and not a crm.Policy: the two API's types are
// separate and neither converts.
func iamPolicyHasRole(p *iam.Policy, role, member string) bool {
	if p == nil {
		return false
	}
	for _, b := range p.Bindings {
		if b.Role == role && slices.Contains(b.Members, member) {
			return true
		}
	}
	return false
}

// batchActAsRole is the role the module grants the launchers on each run
// account. It is the predefined one, on the account and never on the project,
// and it is written here as the string the module writes for the reason
// batchLauncherRole is.
const batchActAsRole = "roles/iam.serviceAccountUser"

// doctorBatchJobImage reads the repository the batch block's image is served
// from (Task 10 step 5, fifth entry). A pull-through repository of the project
// serves whatever its upstream's owner published under the name it caches, so
// a job image resolved through one is a stranger's code running as this
// installation's own run account -- with the run account's grants on the
// project and its reader grant on the pinned repository.
//
// The rule is the repository's MODE and not a name: `fugaro-ghcr` was the
// pull-through repository the installation's pin used to name, and a rule
// written against that one name would pass every other pull-through repository
// an installation could add -- and would also pass a standard repository that
// later became one. A REMOTE_REPOSITORY serves its upstream's content and a
// VIRTUAL_REPOSITORY resolves against a priority list that may include one;
// only a STANDARD_REPOSITORY holds bytes this project's own builds pushed.
//
// What this does NOT read is the image's provenance: whether the tag's digest
// is the one the installation's own build published is Check 57b's and the
// pin's, not a fact this API answers.
func doctorBatchJobImage(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-job-image"
	project, location, repo, ok := dockerPkgRepo(lc.Batch.Image)
	if !ok {
		// An image that is not an Artifact Registry path at all -- a Docker
		// Hub reference, say -- is refused by the shared-config anchor check
		// when the block is written and by the job-definition check here; it
		// is not a repository this check can read, and saying so would be a
		// warning about a config that never reaches a launch.
		return doctorCheck{ID: id, OK: true}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.ArtifactRegistry == "" {
		return batchSkipped(id, "the repository the job image is served from", "artifact_registry")
	}
	svc, err := artifactregistry.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ArtifactRegistry)...)
	if err != nil {
		return batchUnchecked(id, "the repository "+repo+" in "+project, batchReadFix, err, lc)
	}
	r, err := svc.Projects.Locations.Repositories.Get("projects/" + project + "/locations/" + location + "/repositories/" + repo).Context(ctx).Do()
	switch {
	case isNotFound(err):
		return batchProblem(lc, id,
			fmt.Sprintf("the repository projects/%s/locations/%s/repositories/%s that the batch block's image is served from is not in %s: the runs' job image is what the VMs run, so an image whose repository is not there is a launch that fails at pull", project, location, repo, project),
			"check batch.image in the local config, or build and push the installation's image (fugaro update builds it in this project)")
	case err != nil:
		return batchUnchecked(id, "the repository "+repo+" in "+project, batchReadFix, err, lc)
	}
	// The mode's own strings, as the API answers them: an empty one is a
	// repository that never set the field, which the API documents as the
	// standard mode. Only STANDARD_REPOSITORY is what this project's own
	// builds pushed into.
	switch r.Mode {
	case "", "STANDARD_REPOSITORY":
		return doctorCheck{ID: id, OK: true}
	}
	return batchProblem(lc, id,
		fmt.Sprintf("the batch block's image %s is served from %s, whose mode is %s: a repository that is not a STANDARD_REPOSITORY serves content this project's own builds did not push -- a REMOTE_REPOSITORY is a pull-through cache of another registry, and a VIRTUAL_REPOSITORY resolves through a priority list that may name one -- so the job's VMs would run whatever its upstream's owner published, as this installation's own run account, with that account's grants on the project (design compute-backend.md §3.2, §4.4)", lc.Batch.Image, repo, oneLineCLI(r.Mode)),
		"push the installation's image into a STANDARD_REPOSITORY of this project and point batch.image at it (the repository the installation's pin names is the one fugaro update builds into); a pull-through repository is for pulling other people's images, never for the image a run executes")
}

// doctorBatchSecrets reads the Secret Manager secrets the batch block names
// (Task 10 step 5, fourth entry): the block's `secrets:` entries become the
// job's secretVariables, so a run's VM mounts them and the run reads their
// values. The block is published to the runs bucket, and `internal/cli`'s
// ParseShared holds each of them to the anchor's project and to nothing more --
// it has a name, a GCP project and a bucket, and no way to ask which of that
// project's secrets this installation created. A writer with objectAdmin on the
// bucket could therefore name another secret of the same project, and the
// installation's own launcher would read it and hand its value to a run's
// agents: step 4's neighbour, with a secret instead of an account.
//
// The rule is the ownership LABEL, which is the project's own answer to "did
// this installation create it" and the same kind of answer infra.JobRunAccounts
// takes from the display name: every Fugaro-created secret carries
// fugaro=managed (gcp.LabelManaged), and internal/infra's discovery reads its
// own the same way (liveProjectSecrets). A secret of the project that does not
// carry it is not one the installation created, whatever its name.
//
// What this does NOT read is whether the run account can read the secret: that
// is the accessor grant `fugaro update` writes for the installation's own
// secrets, and a block naming a secret it may not read is a run that fails at
// pull, not an escalation.
func doctorBatchSecrets(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-secrets"
	if len(lc.Batch.Secrets) == 0 {
		return doctorCheck{ID: id, OK: true}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.SecretManager == "" {
		return batchSkipped(id, "the secrets the batch block names", "secret_manager")
	}
	svc, err := secretmanager.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.SecretManager)...)
	if err != nil {
		return batchUnchecked(id, "the installation's secrets in "+lc.GCPProject, batchReadFix, err, lc)
	}
	ours := map[string]bool{}
	it := svc.Projects.Secrets.List("projects/"+lc.GCPProject).
		Filter("labels."+gcp.LabelManaged+"="+gcp.ManagedValue).Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
		for _, s := range p.Secrets {
			ours[pathTail(s.Name)] = true
		}
		return nil
	})
	switch {
	case isAccessDenied(it):
		return batchRefused(id, "the secrets of "+lc.GCPProject, "an operator runs fugaro doctor, which can list the project's secrets", lc)
	case it != nil:
		return batchUnchecked(id, "the installation's secrets in "+lc.GCPProject, batchReadFix, it, lc)
	}
	var foreign []string
	for _, env := range slices.Sorted(maps.Keys(lc.Batch.Secrets)) {
		if name := pathTail(lc.Batch.Secrets[env]); !ours[name] {
			foreign = append(foreign, env+" ("+lc.Batch.Secrets[env]+")")
		}
	}
	if len(foreign) > 0 {
		return batchProblem(lc, id,
			fmt.Sprintf("the batch block names secrets that Fugaro did not create in %s: %s. The block is published to the runs bucket, so a writer with objectAdmin there could otherwise point a run at any secret of the project -- one a teammate owns, one the installation's own deployer holds -- and the launcher would read it and hand its value to the run's agents. A secret this installation created carries the %s=%s label, which is what this check reads (design compute-backend.md §3.2, §4.4)", lc.GCPProject, strings.Join(foreign, ", "), gcp.LabelManaged, gcp.ManagedValue),
			"remove those entries from batch.secrets, or have the installation create the secret (fugaro update in "+lc.GCPProject+" writes the project's own), then rerun fugaro doctor")
	}
	return doctorCheck{ID: id, OK: true}
}

// dockerPkgRepo parses a Docker image reference served by Artifact Registry
// into the repository's project, location and id. ok is false for a reference
// that is not one (no -docker.pkg.dev host, or a path too short to name a
// repository), which the caller answers rather than guessing at.
//
// Every Artifact Registry Docker host is <location>-docker.pkg.dev, and the
// path is <project>/<repository>/<image>, so the three fields are the host's
// prefix and the first two path elements. A reference that also carries a host
// of some other registry is not this function's to parse.
func dockerPkgRepo(image string) (project, location, repo string, ok bool) {
	const suffix = "-docker.pkg.dev"
	parts := strings.Split(image, "/")
	if len(parts) < 3 || !strings.HasSuffix(parts[0], suffix) {
		return "", "", "", false
	}
	location = strings.TrimSuffix(parts[0], suffix)
	if location == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[1], location, parts[2], true
}

// doctorBatchReaper reads the reaper's invocation (Task 10 step 2, design §3.4
// and §8.1 Task 11): the account, the five permissions of its custom role, the
// Cloud Run job that runs `fugaro reaper` as that account, and the Scheduler
// job that starts it every ReapMargin.
//
// Why the gate needs it: a Batch run whose VM outlives its job leaks, and on
// this backend the VM is billed machine time the budget counts -- the reaper
// is the one thing that sweeps one. An installation enabled without it is the
// leak §3.4 is about, and it is the failure an operator meets days later, in a
// bill rather than in a launch.
//
// The role's permissions are compared as a SET and in both directions: a role
// missing one of the five cannot sweep what that permission reaches, and a role
// carrying one more is a janitor that can do something this design never
// decided it may -- the two custom roles' whole point (§3.7).
func doctorBatchReaper(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "batch-reaper"
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" && lc.Endpoints.Run == "" && lc.Endpoints.CloudScheduler == "" {
		return []doctorCheck{batchSkipped(id, "the reaper's account, its role and its invocation", "iam")}
	}
	out := []doctorCheck{doctorBatchReaperRole(ctx, lc)}
	return append(out, doctorBatchReaperInvocation(ctx, lc)...)
}

// doctorBatchReaperRole reads the reaper's account and its custom role's
// permissions. The account and the role are one finding: a role with no
// account to hold it sweeps nothing, and an account with no role cannot sweep
// what it reaches.
func doctorBatchReaperRole(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-reaper-role"
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" {
		return batchSkipped(id, "the reaper's account and its custom role", "iam")
	}
	svc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
	if err != nil {
		return batchUnchecked(id, "the reaper's account and its custom role", batchReadFix, err, lc)
	}
	account := infra.ReaperAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	if _, err := svc.Projects.ServiceAccounts.Get("projects/" + lc.GCPProject + "/serviceAccounts/" + account).Context(ctx).Do(); err != nil {
		if !isNotFound(err) {
			return batchUnchecked(id, "the reaper's account "+account, batchReadFix, err, lc)
		}
		return batchProblem(lc, id,
			"the reaper's service account "+account+" is not in "+lc.GCPProject+": the scheduled sweep is the one thing that deletes a Batch job and a VM a run has leaked, so an installation without it leaves every leaked VM billed until an operator finds it (design compute-backend.md §3.4, §8.1 Task 11)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper account and role")
	}
	role, err := svc.Projects.Roles.Get("projects/" + lc.GCPProject + "/roles/" + infra.ReaperRoleID).Context(ctx).Do()
	if err != nil {
		if !isNotFound(err) {
			return batchUnchecked(id, "the reaper's custom role "+infra.ReaperRoleID, batchReadFix, err, lc)
		}
		return batchProblem(lc, id,
			"the reaper's custom role projects/"+lc.GCPProject+"/roles/"+infra.ReaperRoleID+" does not exist: the account holds nothing that can list or delete a leaked job and VM without it, so the sweep would be refused by both APIs (design compute-backend.md §3.7)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper role")
	}
	got := slices.Clone(role.IncludedPermissions)
	slices.Sort(got)
	got = slices.Compact(got)
	want := slices.Clone(infra.ReaperPermissions)
	slices.Sort(want)
	missing := difference(want, got)
	excess := difference(got, want)
	switch {
	case len(missing) == 0 && len(excess) == 0:
		return doctorCheck{ID: id, OK: true}
	case len(missing) > 0 && len(excess) > 0:
		return batchProblem(lc, id,
			"the reaper's custom role "+infra.ReaperRoleID+" does not carry the five permissions the sweep is defined over, and carries others: missing "+strings.Join(missing, ", ")+", and holding "+strings.Join(excess, ", ")+". The five are its whole surface -- list and get every decision the sweep makes, delete a leaked job and its VM, and nothing that creates either (design compute-backend.md §3.7) -- and a role wider than them is a second launcher rather than a janitor",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper role")
	case len(missing) > 0:
		return batchProblem(lc, id,
			"the reaper's custom role "+infra.ReaperRoleID+" is missing the permissions the sweep needs: "+strings.Join(missing, ", ")+". The sweep's deletes are refused without them, so the jobs and VMs a run leaked stay until an operator finds them (design compute-backend.md §3.7)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper role")
	default:
		return batchProblem(lc, id,
			"the reaper's custom role "+infra.ReaperRoleID+" carries permissions the sweep does not use: "+strings.Join(excess, ", ")+". The five are its whole surface (design compute-backend.md §3.7): a reaper account that can do more than end a leaked job and its VM is a second launcher, and every permission beyond that surface is one this design never decided to grant",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper role as this release defines it")
	}
}

// difference is the elements of a that are not in b (both sorted, so the
// answer is sorted too).
func difference(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// doctorBatchReaperInvocation reads the two resources that run the sweep: the
// Cloud Run job that carries `fugaro reaper` and the account it runs as, and
// the Scheduler job that starts it. Either one missing is a sweep that never
// runs -- the job without the schedule never starts, and the schedule without
// the job has nothing to start -- so both are read and each is its own check.
func doctorBatchReaperInvocation(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	var out []doctorCheck
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		out = append(out, batchSkipped("batch-reaper-job", "the Cloud Run job that runs the sweep", "run"))
	} else {
		out = append(out, doctorBatchReaperJob(ctx, lc))
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.CloudScheduler == "" {
		out = append(out, batchSkipped("batch-reaper-schedule", "the Scheduler job that starts the sweep", "cloud_scheduler"))
	} else {
		out = append(out, doctorBatchReaperSchedule(ctx, lc))
	}
	return out
}

// doctorBatchReaperJob reads the Cloud Run job that runs `fugaro reaper`: it
// exists in the installation's region and its tasks run as the reaper account.
// The account is the half that matters and the half a hand edit breaks: a job
// whose tasks run as another account is a sweep that is refused, or one that
// sweeps as an identity with more than five permissions.
func doctorBatchReaperJob(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-reaper-job"
	svc, err := run.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return batchUnchecked(id, "the Cloud Run job "+infra.ReaperJob, batchReadFix, err, lc)
	}
	j, err := svc.Projects.Locations.Jobs.Get("projects/" + lc.GCPProject + "/locations/" + lc.Region + "/jobs/" + infra.ReaperJob).Context(ctx).Do()
	if err != nil {
		if !isNotFound(err) {
			return batchUnchecked(id, "the Cloud Run job "+infra.ReaperJob, batchReadFix, err, lc)
		}
		return batchProblem(lc, id,
			"the Cloud Run job "+infra.ReaperJob+" that runs `fugaro reaper` is not in "+lc.GCPProject+" ("+lc.Region+"): it is the process the sweep is, and without it a leaked Batch job and its VM are deleted by nobody (design compute-backend.md §3.4, §8.1 Task 11)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper job")
	}
	account := infra.ReaperAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	if got := reaperJobAccount(j); got != account {
		return batchProblem(lc, id,
			fmt.Sprintf("the Cloud Run job %s runs as %s, not as the reaper account %s: the job's identity is what its sweep is authorized by, so a job running as another account is one whose deletes are refused -- or one that deletes as an identity with more than the reaper's five permissions (design compute-backend.md §3.7, §8.1 Task 11)", infra.ReaperJob, oneLineCLI(got), account),
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper job as this release defines it")
	}
	return doctorCheck{ID: id, OK: true}
}

// reaperJobAccount is the service account a Cloud Run job's tasks run as:
// template.template.serviceAccount, which is the nested shape Cloud Run v2's
// jobs get serves. "" for a job that names none, which is the project's
// default Compute Engine account -- the one the reaper's design excludes.
func reaperJobAccount(j *run.GoogleCloudRunV2Job) string {
	if j == nil || j.Template == nil || j.Template.Template == nil {
		return ""
	}
	return j.Template.Template.ServiceAccount
}

// doctorBatchReaperSchedule reads the Scheduler job that starts the sweep every
// ReapMargin: it exists in the SCHEDULER region, and it mints its OAuth token
// from the reaper account.
//
// The region is the installation's scheduler region and not its own: Cloud
// Scheduler does not serve every Cloud Run region, so a job created in one it
// does not serve fails the apply and does not exist at all (reaper.tf, design
// §8.1 Task 11). Reading the installation's own region here would report a
// schedule that is there as missing.
func doctorBatchReaperSchedule(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-reaper-schedule"
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			return batchProblem(lc, id,
				"the region the reaper's Scheduler job lives in cannot be resolved: "+oneLineCLI(err.Error())+". Cloud Scheduler is not offered in every Cloud Run region, so the schedule lives where it is served (design compute-backend.md §8.1 Task 11)",
				"set scheduler_region in this installation's local config (fugaro update writes it from the installation's own answer)")
		}
		region = r
	}
	svc, err := scheduler.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.CloudScheduler)...)
	if err != nil {
		return batchUnchecked(id, "the Scheduler job "+infra.ReaperSchedulerJob, batchReadFix, err, lc)
	}
	name := infra.SchedulerJobName(lc.GCPProject, region, infra.ReaperSchedulerJob)
	j, err := svc.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	if err != nil {
		if !isNotFound(err) {
			return batchUnchecked(id, "the Scheduler job "+infra.ReaperSchedulerJob, batchReadFix, err, lc)
		}
		return batchProblem(lc, id,
			"the Scheduler job "+infra.ReaperSchedulerJob+" is not in "+lc.GCPProject+" ("+region+"): nothing starts the sweep, so a Batch job and a VM a run leaked are deleted only by hand -- and the VM is billed machine time the whole time (design compute-backend.md §3.4, §8.1 Task 11)",
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper schedule")
	}
	account := infra.ReaperAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	if got := schedulerTokenAccount(j); got != account {
		return batchProblem(lc, id,
			fmt.Sprintf("the Scheduler job %s mints its OAuth token from %s, not from the reaper account %s: the token's identity is the identity the started job's sweep has, so a schedule mints one the sweep cannot use (design compute-backend.md §3.7, §8.1 Task 11)", infra.ReaperSchedulerJob, oneLineCLI(got), account),
			"run fugaro update in "+lc.GCPProject+", which applies the compute root's reaper schedule as this release defines it")
	}
	return doctorCheck{ID: id, OK: true}
}

// schedulerTokenAccount is the account a Scheduler job mints its OAuth token
// from: httpTarget.oauthToken.serviceAccountEmail. "" for a job that carries
// no HTTP target or no token, which is a schedule that cannot start the sweep.
func schedulerTokenAccount(j *scheduler.Job) string {
	if j == nil || j.HttpTarget == nil || j.HttpTarget.OauthToken == nil {
		return ""
	}
	return j.HttpTarget.OauthToken.ServiceAccountEmail
}

// pathTail is the last element of a full resource name: the family's name from
// projects/<p>/global/images/family/<family>, a network's from
// projects/<p>/global/networks/<n>.
func pathTail(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// subnetParts are a subnetwork resource name's region and name
// (projects/<p>/regions/<r>/subnetworks/<s>). Both are "" for a name that does
// not have that shape, which the job-definition check refuses first.
func subnetParts(name string) (region, subnet string) {
	parts := strings.Split(name, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "regions" || parts[4] != "subnetworks" {
		return "", ""
	}
	return parts[3], parts[5]
}
