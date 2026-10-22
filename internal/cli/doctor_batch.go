package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	compute "google.golang.org/api/compute/v1"
	logging "google.golang.org/api/logging/v2"
	serviceusage "google.golang.org/api/serviceusage/v1"

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
//     would fail in.
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
//
// All of them are WARNINGS, never failures: nothing in this build can select
// backend: cloud-batch (internal/localcfg refuses the name, the plan's Global
// Constraint 12), so an installation's batch block is a fact about work in
// progress rather than a broken installation, and a red doctor on it would
// complain about a plan that is not finished. Task 10 is the task that makes
// these the gate it needs: it turns the backend name on, and its own commit
// decides which of them a project must satisfy before it may.
//
// They are read only for an installation that carries a batch block
// (localcfg.Batch), which is the one signal this build has that an operator
// intends this backend: the backend NAME cannot be selected, so a check gated
// on it would be dead code. A read that cannot be made is a warning that says
// so and never a pass (the same rule doctor's other cloud checks follow), and
// a test configuration (no_auth with no endpoint for the API in question) is
// skipped, so no real project is read from one.

// doctorBatchChecks are the checks above, in the order doctor prints them.
func doctorBatchChecks(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	if lc.Batch == nil {
		return nil
	}
	var out []doctorCheck
	out = append(out, doctorBatchAPIs(ctx, lc)...)
	out = append(out, doctorBatchJobDefinition(lc))
	out = append(out, doctorBatchImageFamily(ctx, lc))
	out = append(out, doctorBatchNetwork(ctx, lc)...)
	out = append(out, doctorBatchLogSink(ctx, lc))
	out = append(out, doctorBatchLauncherIAM(ctx, lc, c)...)
	out = append(out, doctorBatchArtifactBoundary())
	return out
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

// batchUnchecked is a read that could not be made, in the shape every cloud
// check here answers with: a warning naming what could not be read, never a
// silent pass.
func batchUnchecked(id, what, fix string, err error) doctorCheck {
	return doctorCheck{ID: id, Severity: "warning",
		Problem: "could not check " + what + ": " + oneLineCLI(err.Error()),
		Fix:     fix}
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
		return []doctorCheck{batchUnchecked("batch-apis", "whether the Compute and Batch APIs are enabled in "+lc.GCPProject, batchReadFix, err)}
	}
	var disabled []string
	for _, s := range []string{batchServiceCompute, batchServiceBatch} {
		st, err := svc.Services.Get("projects/" + lc.GCPProject + "/services/" + s).Context(ctx).Do()
		if err != nil {
			return []doctorCheck{batchUnchecked("batch-apis", "whether "+s+" is enabled in "+lc.GCPProject, batchReadFix, err)}
		}
		// The state is the API's own string, printed inside this check's
		// sentence: one line of it, like every other value a project answers
		// (batchUnchecked's read errors, the sink's destination).
		if st.State != "ENABLED" {
			disabled = append(disabled, fmt.Sprintf("%s is %s", s, oneLineCLI(st.State)))
		}
	}
	if len(disabled) > 0 {
		return []doctorCheck{{ID: "batch-apis", Severity: "warning",
			Problem: "a Batch run needs both APIs enabled in " + lc.GCPProject + ", and " + strings.Join(disabled, ", ") + ": the installation module enables them, so this project's apply has not run since they were added (design compute-backend.md §4.4)",
			Fix:     "run fugaro update in " + lc.GCPProject + ", which applies the installation root"}}
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
		return doctorCheck{ID: "batch-job-definition", Severity: "warning",
			Problem: "a Batch run's job definition is one this build refuses, so no run on this installation can start: " + oneLineCLI(err.Error()),
			Fix:     "fix the batch block of this installation's local config (fugaro update writes it; the fields are documented in docs/gcp-setup.md)"}
	}
	return doctorCheck{ID: "batch-job-definition", OK: true}
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
		return batchUnchecked(id, "the preload image family "+family+" in "+project, batchReadFix, err)
	}
	_, err = svc.Images.GetFromFamily(project, family).Context(ctx).Do()
	switch {
	case err == nil:
		return doctorCheck{ID: id, OK: true}
	case isNotFound(err):
		return doctorCheck{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the preload image family projects/%s/global/images/family/%s does not exist in %s: a run's bootDisk.image names the family (not one of its generations), so this installation's VMs have nothing to boot until the preload build has published a generation (design compute-backend.md §1.2, §4.4)", project, family, project),
			Fix:     "run the preload rebuild (fugaro setup --provision-batch in " + project + ", or the fugaro-preload-rebuild Scheduler job), then rerun fugaro doctor"}
	default:
		return batchUnchecked(id, "the preload image family "+family+" in "+project, batchReadFix, err)
	}
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
		return []doctorCheck{batchUnchecked("batch-network", "the network and subnetwork a run's VMs are placed in", batchReadFix, err)}
	}
	var out []doctorCheck
	netProject := resourceProject(network, lc.GCPProject)
	netName := pathTail(network)
	if _, err := svc.Networks.Get(netProject, netName).Context(ctx).Do(); err != nil {
		if !isNotFound(err) {
			// The pair is read independently, so one read that cannot be made
			// does not hide the other resource's finding.
			out = append(out, batchUnchecked("batch-network", "the network "+network, batchReadFix, err))
		} else {
			out = append(out, doctorCheck{ID: "batch-network", Severity: "warning",
				Problem: "the network " + network + " that the batch block names is not in " + netProject + ": a run's VMs are placed in it explicitly, because Batch refuses a no-external-IP job unless the network and the subnetwork are named (design compute-backend.md §1.7)",
				Fix:     "check batch.network in the local config, or run fugaro setup --provision-batch in " + netProject + " which creates the installation's own network"})
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
			return []doctorCheck{batchUnchecked("batch-subnetwork", "the subnetwork "+subnetwork, batchReadFix, err)}
		}
		return []doctorCheck{{ID: "batch-subnetwork", Severity: "warning",
			Problem: "the subnetwork " + subnetwork + " that the batch block names is not in " + subProject + ": the VMs are placed in it explicitly (Batch refuses a no-external-IP job unless both are named, design compute-backend.md §1.7), and the subnetwork is where a run's VMs are, so one in a second project or region is a run this installation cannot see",
			Fix:     "check batch.subnetwork in the local config, or run fugaro setup --provision-batch in " + subProject + " which creates the installation's own subnetwork"}}
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

// doctorBatchLogSink reads the installation's log sink, fugaro-jobs. What it
// checks is the two facts that decide whether a Batch run's lines are isolated:
// the destination is the installation's log bucket, and the filter carries the
// Batch arm. Without either, a Batch run's task lines land in _Default, where
// every project viewer reads them (§1.5, docs/gcp-setup.md). The sink's filter
// is installation Terraform's, pinned there byte for byte
// (deploy/terraform/terraform_test.go); this reads the installed one, which an
// apply only rewrites when the owner runs it.
func doctorBatchLogSink(ctx context.Context, lc *localcfg.Config) doctorCheck {
	const id = "batch-log-sink"
	if lc.Endpoints.NoAuth && lc.Endpoints.Logging == "" {
		return batchSkipped(id, "the log sink "+infra.LogSink, "logging")
	}
	svc, err := logging.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Logging)...)
	if err != nil {
		return batchUnchecked(id, "the log sink "+infra.LogSink, batchReadFix, err)
	}
	sink, err := svc.Projects.Sinks.Get("projects/" + lc.GCPProject + "/sinks/" + infra.LogSink).Context(ctx).Do()
	switch {
	case isNotFound(err):
		return doctorCheck{ID: id, Severity: "warning",
			Problem: "the log sink " + infra.LogSink + " is not in " + lc.GCPProject + ", so a Batch run's task lines are not routed into the installation's log bucket: they sit in _Default, where every project viewer reads them (design compute-backend.md §1.5). The log isolation is installation Terraform, so an installation whose apply predates the Batch arm still has the sink and routes only Cloud Run's lines",
			Fix:     "run fugaro update in " + lc.GCPProject + ", which applies the installation root's logging"}
	case err != nil:
		return batchUnchecked(id, "the log sink "+infra.LogSink+" and its filter", batchReadFix, err)
	}
	if want := "logging.googleapis.com/" + logBucketPath(lc); sink.Destination != want {
		// The destination is the sink's own string, printed inside this
		// check's sentence: one line of it, as the read errors above are
		// (the installed destination is a value a project answers, not one
		// this config wrote).
		return doctorCheck{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the log sink %s routes to %s, not to this installation's log bucket %s (design compute-backend.md §1.5)", infra.LogSink, oneLineCLI(sink.Destination), want),
			Fix:     "run fugaro update in " + lc.GCPProject + ", which applies the installation root's logging"}
	}
	if !strings.Contains(sink.Filter, batchSinkArm) {
		return doctorCheck{ID: id, Severity: "warning",
			Problem: "the log sink " + infra.LogSink + "'s filter has no Batch arm (" + batchSinkArm + "), so a Batch job's lines are not routed into the installation's log bucket and `fugaro logs` would find nothing in the view (design compute-backend.md §1.5): the installation has not been applied since that arm was added",
			Fix:     "run fugaro update in " + lc.GCPProject + " (or fugaro doctor after the owner applies the installation root)"}
	}
	return doctorCheck{ID: id, OK: true}
}

// batchSinkArm is the Batch arm of the installation's log filter, as
// deploy/terraform/gcp/modules/installation/logging.tf writes it: the resource
// type a Batch job's lines carry, pinned by the same fugaro=managed marker as
// the Cloud Run arm. The filter as a whole is Terraform's and its own test
// holds it byte for byte; doctor reads the installed sink and checks that this
// arm is in it.
const batchSinkArm = `resource.type="batch.googleapis.com/Job" AND labels."fugaro"="managed"`

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
	// Only terraform.launchers, and not the operators beside them: the two
	// lists reach the compute root as two separate variables
	// (infra.ComputeSpec.Launchers/Operators, roots/compute/main.tf), and the
	// module's launcher_jobs_editor is for_each over var.launchers alone --
	// the installation's operators hold actAs on the preload account there and
	// nothing else (modules/compute/iam.tf, design §8.1 task 6). That is
	// narrower than the installation and repository roots, which write
	// launchersWithOperators into their own tfvars because an operator
	// launches too (infra.tfvars.go); the Batch grant is not widened the same
	// way. Reading the union here would therefore warn about a member the
	// module deliberately does not grant, and no run of `fugaro update` could
	// clear that warning: the Fix it prints is the state the installation is
	// already in.
	launchers := slices.Clone(lc.Terraform.Launchers)
	slices.Sort(launchers)
	launchers = slices.Compact(launchers)
	if len(launchers) == 0 || c == nil {
		// No launchers are recorded, so there is no member the grant is about;
		// an installation with no launchers cannot start a run at all, which
		// is a fact about the config and not about IAM.
		return nil
	}
	const id = "batch-launcher-iam"
	p, err := c.Projects.GetIamPolicy(lc.GCPProject, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if isAccessDenied(err) {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: "could not read the IAM policy of " + lc.GCPProject + " to check the launchers' roles/batch.jobsEditor grant: this credential does not hold resourcemanager.projects.getIamPolicy there. On a freshly created project the grant and the policy may still be propagating -- Check 57a found a new grant is not visible to a binding read for a while -- so this is not read as a missing grant",
			Fix:     "an operator runs fugaro doctor, which can read the project's policy"}}
	}
	if err != nil {
		return []doctorCheck{batchUnchecked(id, "the launchers' roles/batch.jobsEditor grant in "+lc.GCPProject, batchReadFix, err)}
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
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s hold roles/batch.jobsEditor in %s without the compute module's condition (%s): the grant is conditioned on Fugaro-shaped job names in this project and region, and an unconditioned one may start any job in the project. And %s do not hold a roles/batch.jobsEditor grant there with that condition at all, so they cannot start a Batch run (design compute-backend.md §3.7, §4.4)", strings.Join(misconditioned, ", "), lc.GCPProject, want, strings.Join(missing, ", ")),
			Fix:     "run fugaro update in " + lc.GCPProject + ", which writes the conditioned grant for every launcher (or, if a grant was just written, rerun fugaro doctor once it propagates)"}}
	case len(misconditioned) > 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s hold roles/batch.jobsEditor in %s without the compute module's condition (%s): the grant is conditioned on Fugaro-shaped job names in this project and region, and an unconditioned one may start any job in the project (design compute-backend.md §3.7)", strings.Join(misconditioned, ", "), lc.GCPProject, want),
			Fix:     "run fugaro update in " + lc.GCPProject + ", which writes the conditioned grant"}}
	case len(missing) > 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s do not hold a roles/batch.jobsEditor grant in %s with the compute module's condition (%s), so they cannot start a Batch run. A grant that was just made may not be visible to this read yet -- on a freshly created project Check 57a found the same, which is why this is a warning and not a missing grant (design compute-backend.md §4.4)", strings.Join(missing, ", "), lc.GCPProject, want),
			Fix:     "run fugaro update in " + lc.GCPProject + " (or, if the grant was just written, rerun fugaro doctor once it propagates)"}}
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
