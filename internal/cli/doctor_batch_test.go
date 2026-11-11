package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The Batch half of `fugaro doctor` (plan 2026-10-19-compute-backend.md Task 8
// step 5, design compute-backend.md §4.4; Task 10 step 2 turns the same checks
// into the gate). Every check fires only for an installation that carries a
// batch block, and its severity is what the installation's own selection
// decides (batchSeverity): a hard failure with no severity for a config that
// selects backend: cloud-batch -- which is what refuses the enable until every
// one of them passes -- and a warning for one that merely carries the block
// while it runs on Cloud Run.

// batchRigFakes are the fakes a Batch installation's checks read, plus the
// rig's own CRM and Cloud Run fakes (already pointed at by resource_manager
// and run).
type batchRigFakes struct {
	compute   *gcpfake.Compute
	logging   *gcpfake.Logging
	usage     *gcpfake.ServiceUsage
	iam       *gcpfake.IAM
	scheduler *gcpfake.Scheduler
	ar        *gcpfake.ArtifactRegistry
}

// newBatchFakes starts one of each.
func newBatchFakes(t *testing.T) batchRigFakes {
	t.Helper()
	return batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t),
		usage: gcpfake.NewServiceUsage(t), iam: gcpfake.NewIAM(t),
		scheduler: gcpfake.NewScheduler(t), ar: gcpfake.NewArtifactRegistry(t)}
}

// The installation's own names, as the block records them and as the fakes
// above are given them.
const (
	batchRunAccount    = "fugaro-runs@proj-1234.iam.gserviceaccount.com"
	batchReaperAccount = "fugaro-reaper@proj-1234.iam.gserviceaccount.com"
	batchPreloadFamily = "fugaro-preload-abc12345"
	batchSocketGID     = 989
	batchJobRepo       = "fugaro"
	// batchRegion is the installation's own region and batchSchedulerRegion
	// the one the reaper's schedule lives in: Cloud Scheduler does not serve
	// every Cloud Run region, so an installation recording no scheduler_region
	// has the check resolve it (gcp.SchedulerRegion), and for us-east5 that is
	// us-east4 -- the region the fake below places the job in.
	batchRegion          = "us-east5"
	batchSchedulerRegion = "us-east4"
)

// batchBlock is a batch block whose every value is this installation's own:
// the anchors the shared-config check applies would refuse anything else, and
// the names are what the fakes below are given. socket_gid is set, so the gid
// comparison is a comparison and not the recorded-zero case.
const batchBlock = `batch:
  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1
  machine_type: e2-standard-4
  boot_image: projects/proj-1234/global/images/family/fugaro-preload-abc12345
  network: projects/proj-1234/global/networks/fugaro
  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro
  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com
  socket_gid: 989
`

// batchStep is one thing the installation's apply, its compute root's IAM and
// its preload build put in place, named so a case can be built without exactly
// one of them (the plan's "a fake that answers 'absent' and then 'present'").
type batchStep struct {
	name string
	add  func(t *testing.T, r *doctorRig, f batchRigFakes)
}

// The names of the steps the gate test builds its worlds without, and the
// steps of the two invocations that are not part of the list above (the
// schedule is a Scheduler job and the reaper job's own get is what reads it).
const (
	batchStepFamily   = "the preload image family's generation"
	batchStepNetwork  = "the network"
	batchStepSubnet   = "the subnetwork"
	batchStepSink     = "the log sink's arms"
	batchStepExcl     = "the log exclusion's arms"
	batchStepRepo     = "the job image's repository"
	batchStepRunAcct  = "the run account and its display name"
	batchStepPolicy   = "the launcher grant and the run account's roles"
	batchStepActAs    = "the launchers' actAs on the run account"
	batchStepReaperAC = "the reaper's account"
	batchStepReaperRR = "the reaper's role and its five permissions"
	batchStepReaperJo = "the reaper's Cloud Run job"
	batchStepReaperSc = "the reaper's schedule"
)

// batchSteps is the whole list, in the order doctor checks them.
func batchSteps() []batchStep {
	return []batchStep{
		{batchStepFamily, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.compute.AddImageFamilyLabels(batchPreloadFamily, map[string]string{infra.SocketGIDLabel: strconv.Itoa(batchSocketGID)})
		}},
		{batchStepNetwork, func(_ *testing.T, _ *doctorRig, f batchRigFakes) { f.compute.AddNetwork("fugaro") }},
		{batchStepSubnet, func(_ *testing.T, _ *doctorRig, f batchRigFakes) { f.compute.AddSubnetwork(batchRegion, "fugaro") }},
		{batchStepSink, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.logging.AddSink("proj-1234", infra.LogSink,
				"logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro", batchBothArms)
		}},
		{batchStepExcl, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.logging.AddExclusion("proj-1234", infra.LogExclusion, batchBothArms)
		}},
		{batchStepRepo, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.ar.AddRepository("proj-1234", batchRegion, batchJobRepo, nil)
		}},
		{batchStepRunAcct, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.AddServiceAccount("proj-1234", batchRunAccount, "Fugaro job app")
		}},
		{batchStepPolicy, func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.crm.SetPolicy("proj-1234", batchProvisionedPolicy()...)
		}},
		{batchStepActAs, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.SetServiceAccountPolicy("proj-1234", batchRunAccount,
				gcpfake.Binding{Role: batchActAsRole, Members: []string{batchLauncher}})
		}},
		{batchStepReaperAC, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.AddServiceAccount("proj-1234", batchReaperAccount, "Fugaro reaper")
		}},
		{batchStepReaperRR, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.AddRole("proj-1234", infra.ReaperRoleID, "Fugaro reaper", false)
			f.iam.SetRolePermissions("projects/proj-1234/roles/"+infra.ReaperRoleID, infra.ReaperPermissions...)
		}},
		{batchStepReaperJo, func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.run.SetJobJSON(infra.ReaperJob, `{"template":{"template":{"serviceAccount":"`+batchReaperAccount+`"}}}`)
		}},
		{batchStepReaperSc, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.scheduler.SetJob("proj-1234", batchSchedulerRegion, infra.ReaperSchedulerJob,
				"https://run.googleapis.com/v2/projects/proj-1234/locations/"+batchRegion+"/jobs/"+infra.ReaperJob+":run", batchReaperAccount)
		}},
	}
}

// batchProvisionedPolicy is the project policy the compute root's apply leaves:
// the launchers' conditioned grant and the two roles every run account holds.
func batchProvisionedPolicy() []gcpfake.Binding {
	return []gcpfake.Binding{
		batchListerPolicy(batchLauncher),
		{Role: batchAgentReporterRole, Members: []string{"serviceAccount:" + batchRunAccount}},
		{Role: batchLogWriterRole, Members: []string{"serviceAccount:" + batchRunAccount}},
	}
}

// batchBothArms is the arm pair the installation module's sink and exclusion
// both carry: Cloud Run's managed lines and the Batch arm beside it.
var batchBothArms = `(resource.type="cloud_run_job" AND labels."fugaro"="managed") OR (` + batchSinkArm + `)`

// batchProvisioned is what the fakes hold once the installation's apply, the
// compute root's IAM and the preload build have run, and batchProvisionedWithout
// is the same with the named steps left out. Both are what the gate test builds
// its worlds from, and every step in batchSteps is that test's case for the
// check it belongs to.
func batchProvisioned(t *testing.T, r *doctorRig, f batchRigFakes) {
	t.Helper()
	batchProvisionedWithout(t, r, f)
}

func batchProvisionedWithout(t *testing.T, r *doctorRig, f batchRigFakes, without ...string) {
	t.Helper()
	for _, s := range batchSteps() {
		if slices.Contains(without, s.name) {
			continue
		}
		s.add(t, r, f)
	}
}

// batchLauncher is the one launcher every fixture here grants, and the member
// the caller's own policy is built from when a test needs a different one.
const batchLauncher = "user:a@example.com"

// batchLists is the launcher list every fixture writes: a Batch launch is a
// launcher's action, so the grant is read from a config that names one.
const batchLists = "terraform: { launchers: [\"" + batchLauncher + "\"] }\n"

// withBatch writes the rig's local config with batchBlock and the endpoints
// its checks read, plus whatever extra YAML the test wants (the launcher
// lists, a log view). withBatchBlock is the same with a block of the test's
// own, for a case that is about the block itself.
func (r *doctorRig) withBatch(t *testing.T, f batchRigFakes, extra string) {
	t.Helper()
	r.withBatchBlock(t, f, batchBlock, extra)
}

func (r *doctorRig) withBatchBlock(t *testing.T, f batchRigFakes, block, extra string) {
	t.Helper()
	// The Compute fake scopes a read to its own project, as the API does, so
	// it stands in for the installation's project.
	f.compute.Project = "proj-1234"
	cfg := strings.Replace(r.cfg, "endpoints: { ",
		"endpoints: { compute: "+f.compute.URL+"/compute/v1/, logging: "+f.logging.URL+"/, service_usage: "+f.usage.URL+"/, "+
			"iam: "+f.iam.URL+"/, cloud_scheduler: "+f.scheduler.URL+"/, artifact_registry: "+f.ar.URL+"/, ", 1)
	cfg += block + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withBatchSelected is withBatch for an installation that SELECTS the backend:
// the same config plus backend: cloud-batch, which is what makes every failing
// Batch check a hard failure instead of a warning (batchSeverity, Task 10's
// step 1 and the gate step 2 hands over).
func (r *doctorRig) withBatchSelected(t *testing.T, f batchRigFakes, block, extra string) {
	t.Helper()
	r.withBatchBlock(t, f, block, "backend: cloud-batch\n"+extra)
}

// batchListerPolicy is the launchers' conditioned grant, as the compute module
// writes it: roles/batch.jobsEditor, on the launcher members, with the project
// and region condition.
func batchListerPolicy(launchers ...string) gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/batch.jobsEditor", Members: launchers,
		Condition: &gcpfake.IAMCondition{
			Title:       gcp.LauncherJobsConditionTitle,
			Description: gcp.LauncherJobsConditionDescription,
			Expression:  gcp.LauncherJobsCondition("proj-1234", "us-east5"),
		}}
}

// TestDoctorBatchChecksPassOnAProvisionedInstallation: every check is OK for
// an installation whose apply has run and whose preload build has published a
// generation -- the precondition of the gate, and the state the property test's
// whole world assumes.
func TestDoctorBatchChecksPassOnAProvisionedInstallation(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatch(t, f, batchLists)

	o := r.runDoctorJSON(t)
	for _, id := range []string{
		"batch-apis", "batch-job-definition", "batch-cost", "batch-image-family",
		"batch-network", "batch-subnetwork", "batch-log-sink", "batch-log-exclusion",
		"batch-job-image", "batch-run-accounts", "batch-run-account-roles",
		"batch-run-account-actas", "batch-launcher-iam", "batch-reaper-role",
		"batch-reaper-job", "batch-reaper-schedule",
		// The boundary is the one line that is NOT ok and must not be: it
		// describes the plan's own decision (Global Constraint 13) and takes
		// info, so it is asserted here for its severity and not for a pass.
		// TestDoctorBatchNamesTheArtifactBoundary is where its text is pinned.
		"batch-artifact-boundary",
	} {
		c, ok := doctorCheckByID(o.Checks, id)
		if !ok {
			t.Errorf("doctor reports no %s check", id)
			continue
		}
		if id == "batch-artifact-boundary" {
			if c.OK || c.Severity != "info" {
				t.Errorf("check %s = %+v, want the boundary's info line", id, c)
			}
			continue
		}
		if !c.OK {
			t.Errorf("check %s = %+v, want ok", id, c)
		}
	}
	if !o.OK {
		t.Errorf("a provisioned installation is refused: %+v", failingChecks(o))
	}
}

// TestDoctorBatchChecksAreSilentWithoutABatchBlock: an installation that
// carries no batch block gets none of these checks. Nothing has published the
// block, so no config could name the account a run's VMs run as, and the block
// is what an installation's apply writes when the backend is provisioned -- a
// check on a project nobody provisioned for Batch would be noise.
//
// Mutation (drop the lc.Batch == nil gate): this fails.
func TestDoctorBatchChecksAreSilentWithoutABatchBlock(t *testing.T) {
	r := newDoctorRig(t)
	o := r.runDoctorJSON(t)
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "batch-") {
			t.Errorf("an installation with no batch block reported %s: %+v", c.ID, c)
		}
	}
}

// TestDoctorBatchReportsADisabledAPI: a Batch run needs compute.googleapis.com
// and batch.googleapis.com, and a disabled one is a fact only the project has.
//
// Mutation (drop the state check, or read only one of the two APIs): the
// assertion on the named service fails.
func TestDoctorBatchReportsADisabledAPI(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	f.usage.Disable("batch.googleapis.com")
	r.withBatch(t, f, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-apis")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (%v), want a warning", c, ok)
	}
	if !strings.Contains(c.Problem, "batch.googleapis.com") || !strings.Contains(c.Problem, "DISABLED") {
		t.Errorf("the warning does not name the disabled API: %s", c.Problem)
	}
	if strings.Contains(c.Problem, "compute.googleapis.com") {
		t.Errorf("the warning names an API that is enabled: %s", c.Problem)
	}
}

// TestDoctorBatchReportsAMissingImageFamily: the runs' bootDisk.image names
// the preload image FAMILY, so a family with nothing behind it is a run whose
// VMs have nothing to boot -- the state an installation is in between
// --provision-batch and the preload build's first run.
//
// Mutation (read the family's generation instead, or treat 404 as ok): this
// fails.
func TestDoctorBatchReportsAMissingImageFamily(t *testing.T) {
	// The family is the one fact missing: the network and the subnetwork are
	// there, so this is the one thing the checks report.
	f := newBatchFakes(t)
	f.compute.AddNetwork("fugaro")
	f.compute.AddSubnetwork("us-east5", "fugaro")
	f.logging.AddSink("proj-1234", infra.LogSink,
		"logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro", batchSinkArm)
	r := newDoctorRig(t)
	r.withBatch(t, f, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-image-family")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (%v), want a warning", c, ok)
	}
	// What only the NOT_FOUND branch says: a read that failed for any other
	// reason is a different finding, so the assertions below have to tell the
	// two apart (the SDK's own 404 text carries the resource name too).
	for _, want := range []string{"does not exist in proj-1234", "nothing to boot", "the family (not one of its generations)"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the warning does not mention %q: %s", want, c.Problem)
		}
	}
}

// TestDoctorBatchReportsANetworkOrSubnetworkThatIsNotThere: both names are
// sent explicitly because Batch refuses a no-external-IP job without them
// (Check 57a, §1.7), and a name that is recorded and absent is a run that
// fails at create -- or worse, is placed by whatever project the name points
// at. Each is its own check, at its own config key.
//
// Mutation (drop either branch, or fold the two into one check): a named
// assertion fails.
func TestDoctorBatchReportsANetworkOrSubnetworkThatIsNotThere(t *testing.T) {
	for _, tc := range []struct {
		name      string
		network   bool
		subnet    bool
		wantID    string
		wantInMsg string
	}{
		{"network", false, true, "batch-network", "projects/proj-1234/global/networks/fugaro"},
		{"subnetwork", true, false, "batch-subnetwork", "projects/proj-1234/regions/us-east5/subnetworks/fugaro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			if !tc.network || !tc.subnet {
				// Rebuild the Compute fake without the resource the case is
				// about, keeping the other one.
				f.compute = gcpfake.NewCompute(t)
				if tc.subnet {
					f.compute.AddSubnetwork("us-east5", "fugaro")
				}
				if tc.network {
					f.compute.AddNetwork("fugaro")
				}
			}
			r.withBatch(t, f, "")

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, tc.wantID)
			if !ok || c.OK || c.Severity != "warning" {
				t.Fatalf("check = %+v (%v), want a warning", c, ok)
			}
			if !strings.Contains(c.Problem, tc.wantInMsg) {
				t.Errorf("the warning does not name the resource: %s", c.Problem)
			}
			// The other one is present and is not reported.
			other := "batch-subnetwork"
			if tc.wantID == other {
				other = "batch-network"
			}
			if oc, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, other); !ok || !oc.OK {
				t.Errorf("the present resource's check = %+v (%v), want ok", oc, ok)
			}
		})
	}
}

// TestDoctorBatchReportsASinkWithoutTheBatchArm: the installation's sink is
// what routes a Fugaro job's lines into the installation's log bucket. A sink
// that is absent, routes elsewhere, or filters only Cloud Run's resource type
// leaves a Batch run's task lines in _Default, where every project viewer
// reads them (§1.5).
//
// Mutation (drop the arm check, drop the destination check, or treat a missing
// sink as ok): one of the three cases stops warning.
func TestDoctorBatchReportsASinkWithoutTheBatchArm(t *testing.T) {
	const wantDest = "logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro"
	cloudRunArm := `resource.type="cloud_run_job" AND labels."fugaro"="managed"`
	for _, tc := range []struct {
		name     string
		add      bool
		dest     string
		filter   string
		wantInMs string
	}{
		{"absent", false, "", "", "is not in proj-1234"},
		{"wrong destination", true, "logging.googleapis.com/projects/other/locations/global/buckets/fugaro", cloudRunArm + " OR " + batchSinkArm, "routes to"},
		{"no batch arm", true, wantDest, cloudRunArm, batchSinkArm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			f.compute.AddImageFamily("fugaro-preload-abc12345")
			f.compute.AddNetwork("fugaro")
			f.compute.AddSubnetwork("us-east5", "fugaro")
			if tc.add {
				f.logging.AddSink("proj-1234", infra.LogSink, tc.dest, tc.filter)
			}
			r.withBatch(t, f, "")

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-log-sink")
			if !ok || c.OK || c.Severity != "warning" {
				t.Fatalf("check = %+v (%v), want a warning", c, ok)
			}
			if !strings.Contains(c.Problem, tc.wantInMs) {
				t.Errorf("the warning does not say %q: %s", tc.wantInMs, c.Problem)
			}
		})
	}
}

// TestDoctorBatchReportsTheLauncherGrant: the launchers' roles/batch.jobsEditor
// grant is conditioned on Fugaro-shaped job names (gcp.LauncherJobsCondition),
// and both directions are findings: a launcher without a grant cannot start a
// run, and a launcher with an unconditioned one may start any job in the
// project.
//
// Mutation (drop the condition comparison, or report a refused read as a
// missing grant): the unconditioned case stops warning, or the denied case
// names the member.
func TestDoctorBatchReportsTheLauncherGrant(t *testing.T) {
	const launcher = "user:a@example.com"
	for _, tc := range []struct {
		name     string
		policy   []gcpfake.Binding
		deny     bool
		wantInMs string
	}{
		{"unconditioned", []gcpfake.Binding{{Role: "roles/batch.jobsEditor", Members: []string{launcher}}}, false, "without the compute module's condition"},
		{"missing", []gcpfake.Binding{{Role: "roles/owner", Members: []string{launcher}}}, false, "do not hold a roles/batch.jobsEditor grant"},
		{"denied read", nil, true, "could not read the IAM policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			r.withBatch(t, f, "terraform: { launchers: [\""+launcher+"\"] }\n")
			if tc.policy != nil {
				r.crm.SetPolicy("proj-1234", tc.policy...)
			}
			if tc.deny {
				r.crm.DenyPolicy("proj-1234")
			}

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-launcher-iam")
			if !ok || c.OK || c.Severity != "warning" {
				t.Fatalf("check = %+v (%v), want a warning", c, ok)
			}
			if !strings.Contains(c.Problem, tc.wantInMs) {
				t.Errorf("the warning does not say %q: %s", tc.wantInMs, c.Problem)
			}
			if tc.deny {
				// A refused read is not a missing grant: 57a found a fresh
				// grant is not visible to a binding read for a while, so the
				// two states cannot be told apart from here.
				if strings.Contains(c.Problem, launcher) {
					t.Errorf("a refused policy read names a launcher as missing the grant: %s", c.Problem)
				}
				if !strings.Contains(c.Problem, "propagat") {
					t.Errorf("a refused read does not say the policy may still be propagating: %s", c.Problem)
				}
			}
		})
	}
}

// TestDoctorBatchReportsBothLauncherConditions: the two lists this check
// builds are independent, so a project can hold one of each -- a launcher with
// an unconditioned grant (a widening) and a launcher with no grant at all
// (one that cannot start a run). Reporting only the first because it is
// non-empty leaves the second a fact the operator never reads, and the missing
// half is not visible in the policy the excess one is found in.
//
// Mutation (report misconditioned only when it is non-empty): the missing
// launcher is not named and the assertion fails.
func TestDoctorBatchReportsBothLauncherConditions(t *testing.T) {
	const excess, absent = "user:a@example.com", "user:b@example.com"
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatch(t, f, "terraform: { launchers: [\""+excess+"\", \""+absent+"\"] }\n")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/batch.jobsEditor", Members: []string{excess}},
		gcpfake.Binding{Role: "roles/owner", Members: []string{absent}},
	)

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-launcher-iam")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (%v), want a warning", c, ok)
	}
	if !strings.Contains(c.Problem, "without the compute module's condition") || !strings.Contains(c.Problem, excess) {
		t.Errorf("the warning does not report the unconditioned grant: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "do not hold a roles/batch.jobsEditor grant") || !strings.Contains(c.Problem, absent) {
		t.Errorf("the warning drops the launcher that holds no grant at all: %s", c.Problem)
	}
}

// TestDoctorBatchDoesNotRequireTheGrantOfOperators: the grant this check reads
// is for_each over the compute root's own launchers (modules/compute/iam.tf,
// roots/compute/main.tf passes var.launchers and var.operators separately), and
// the installation's operators never receive it -- they hold actAs on the
// preload account there and nothing else (design §8.1 task 6). So an
// installation whose operator is not also one of its launchers is not a
// finding: warning about it would name a Fix -- run fugaro update -- that leaves
// the member in exactly the state the warning complains about.
//
// Mutation (read lc.Terraform.Launchers and lc.Terraform.Operators together, as
// this check did): the operator is reported as not holding the grant and the
// assertion that the check is ok fails.
func TestDoctorBatchDoesNotRequireTheGrantOfOperators(t *testing.T) {
	const launcher, operator = "user:a@example.com", "user:o@example.com"
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatch(t, f, "terraform: { launchers: [\""+launcher+"\"], operators: [\""+operator+"\"] }\n")
	// The module's own grant, on the module's own member: the launcher, with
	// the Fugaro job-name condition, and nothing at all for the operator.
	r.crm.SetPolicy("proj-1234", batchListerPolicy(launcher))

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-launcher-iam")
	if !ok {
		t.Fatal("doctor reports no batch-launcher-iam check")
	}
	if !c.OK {
		t.Fatalf("check = %+v, want it ok: an operator holds no roles/batch.jobsEditor and the compute module never grants one (modules/compute/iam.tf)", c)
	}
	if strings.Contains(c.Problem, operator) {
		t.Errorf("the check names the operator: %s", c.Problem)
	}
}

// TestDoctorBatchRefusesAJobDefinitionTheBackendWouldRefuse: the network and
// the subnetwork are what Batch refuses a no-external-IP job without (§1.7),
// and doctor reports the backend's own refusal -- the same one a launch would
// fail with -- offline, from the block alone.
//
// Mutation (drop the check, or report it as ok): this fails.
func TestDoctorBatchRefusesAJobDefinitionTheBackendWouldRefuse(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// A block with no network or subnetwork: localcfg accepts it (the fields
	// are optional there), and the backend is what refuses a launch.
	block := strings.Replace(batchBlock, "  network: projects/proj-1234/global/networks/fugaro\n", "", 1)
	block = strings.Replace(block, "  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n", "", 1)
	r.withBatchBlock(t, f, block, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-job-definition")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (%v), want a warning", c, ok)
	}
	if !strings.Contains(c.Problem, "network and a subnetwork") {
		t.Errorf("the warning is not the backend's own refusal: %s", c.Problem)
	}
}

// TestDoctorBatchReadsTheProjectTheResourceNameNames: the batch block's
// network and subnetwork are full resource names, and a subnetwork of a shared
// VPC lives in the HOST project while the run's VMs are in this one — which is
// what §1.7 has the pair named explicitly for. Reading lc.GCPProject for a name
// that points elsewhere answers about a resource the run does not use: a false
// OK where this project happens to hold a same-named one, and a false "not
// there" where only the named project has it. The project the read uses is the
// project the NAME names.
//
// Mutation (read lc.GCPProject, as this check did): the case fails, because the
// fake holds this project's own network of that name and answers OK.
func TestDoctorBatchReadsTheProjectTheResourceNameNames(t *testing.T) {
	const id = "batch-network"
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// The installation's own project has this network; the name points at the
	// host project, which does not.
	block := strings.Replace(batchBlock, "  network: projects/proj-1234/global/networks/fugaro\n",
		"  network: projects/host-4321/global/networks/fugaro\n", 1)
	r.withBatchBlock(t, f, block, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, id)
	if !ok || c.OK {
		t.Fatalf("a network named in another project = %+v (%v), want a finding: the read must follow the name's project", c, ok)
	}
	if !strings.Contains(c.Problem, "host-4321") {
		t.Errorf("the finding does not name the project the read used: %s", c.Problem)
	}
}

// TestDoctorBatchNeverPassesAReadItCouldNotMake: every cloud check here is a
// read of a project, and a read that fails is a warning naming what could not
// be read, never a pass -- doctor's rule for its other cloud checks, and the
// whole point of a Batch check while the backend name is refused (an
// installation's batch block is work in progress, so a silent pass would hide
// exactly the state the operator is in). The endpoints here answer nothing:
// nothing is listening on them, which is a read that cannot be made.
//
// The launchers' grant is read from the resource manager, which here answers
// every request with a 500: a failed read that is not a refusal, so it takes
// the unchecked arm and not the propagation one a 403 takes.
//
// Mutation (answer OK on a read error, i.e. `default: return
// doctorCheck{ID: id, OK: true}`, or the IAM check's non-denied error arm
// returning OK): each named check stops warning.
func TestDoctorBatchNeverPassesAReadItCouldNotMake(t *testing.T) {
	const dead = "http://127.0.0.1:1/"
	r := newDoctorRig(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"code":500,"message":"backend error","status":"INTERNAL"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	// One endpoint, three services, nothing answering on it; no_auth carries
	// over from the rig's own config, so the client does not go looking for
	// credentials before the connection fails.
	cfg := strings.Replace(r.cfg, "endpoints: { ",
		"endpoints: { compute: "+dead+"compute/v1/, logging: "+dead+", service_usage: "+dead+", ", 1)
	cfg = strings.Replace(cfg, "resource_manager: "+r.crm.URL+"/", "resource_manager: "+broken.URL+"/", 1) +
		batchBlock + "terraform: { launchers: [\"user:a@example.com\"] }\n"
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	o := r.runDoctorJSON(t)
	for _, id := range []string{"batch-apis", "batch-image-family", "batch-network", "batch-subnetwork", "batch-log-sink", "batch-launcher-iam"} {
		c, ok := doctorCheckByID(o.Checks, id)
		if !ok {
			t.Errorf("doctor reports no %s check", id)
			continue
		}
		if c.OK {
			t.Errorf("%s = %+v, want a finding: its read could not be made", id, c)
			continue
		}
		if !strings.Contains(c.Problem, "could not check") {
			t.Errorf("%s does not say the read could not be made: %s", id, c.Problem)
		}
	}
}

// TestDoctorBatchSkipsAreNeverPasses: a test configuration (endpoints.no_auth
// with no endpoint for an API) skips that API's read, so no real project is
// read from it -- and a skipped read is reported as not checked, never as ok.
// The image family and the log sink are single checks that always print, so
// their skip is the line that has to say it; the API and network checks print
// nothing when skipped.
//
// Mutation (return doctorCheck{ID: id, OK: true} from either skip, as these
// did): the check reads ok and the assertion fails.
func TestDoctorBatchSkipsAreNeverPasses(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.WriteFile(r.cfgPath, []byte(r.cfg+batchBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	o := r.runDoctorJSON(t)
	for _, id := range []string{"batch-image-family", "batch-log-sink"} {
		c, ok := doctorCheckByID(o.Checks, id)
		if !ok {
			t.Errorf("doctor reports no %s check", id)
			continue
		}
		if c.OK || c.Severity != "info" || !strings.Contains(c.Problem, "not checked") {
			t.Errorf("%s = %+v, want an info line that says it was not checked: nothing was read", id, c)
		}
	}
}

// batchGateCondition is one of the conditions `fugaro doctor` refuses an
// installation that selects backend: cloud-batch for (plan
// 2026-10-19-compute-backend.md Task 10 step 2, design §4.4 and §8.1 task 9),
// with the two halves of the plan's own test shape: how to build a world
// without it ("a fake that answers absent") and a phrase only ITS OWN finding
// carries, so a case cannot pass on another condition's finding.
type batchGateCondition struct {
	name string
	id   string
	want string
	// without are the provisioning steps the absent world is built without.
	without []string
	// alter makes the condition absent in a world that has everything else,
	// for a condition whose prerequisites must still be there (the family's
	// gid label is only readable against a family that exists).
	alter func(t *testing.T, r *doctorRig, f batchRigFakes)
	// block, when set, is the batch block the absent world is written with:
	// the four conditions that are a VALUE of the block rather than a
	// resource in the project.
	block string
}

// batchGateConditions is the whole product of the gate's conditions. A check
// added to doctorBatchChecks without a case here is a condition nothing proves
// doctor refuses for -- which is what the plan's property test is for.
var batchGateConditions = []batchGateCondition{
	{name: "the Compute and Batch APIs enabled", id: "batch-apis", want: batchServiceBatch,
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) { f.usage.Disable(batchServiceBatch) }},
	{name: "a job definition the backend accepts", id: "batch-job-definition", want: "network and a subnetwork",
		block: strings.NewReplacer(
			"  network: projects/proj-1234/global/networks/fugaro\n", "",
			"  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n", "").Replace(batchBlock)},
	{name: "a priced machine type", id: "batch-cost", want: "cannot be priced",
		block: strings.Replace(batchBlock, "machine_type: e2-standard-4", "machine_type: e2-mega-9", 1)},
	{name: "the preload image family", id: "batch-image-family", want: "does not exist in proj-1234",
		without: []string{batchStepFamily}},
	{name: "the gid the family's generation carries", id: "batch-image-family", want: "no readable socket-gid label",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.compute.AddImageFamilyLabels(batchPreloadFamily, nil)
		}},
	{name: "the network", id: "batch-network", want: "that the batch block names is not in proj-1234",
		without: []string{batchStepNetwork}},
	{name: "the subnetwork", id: "batch-subnetwork", want: "that the batch block names is not in proj-1234",
		without: []string{batchStepSubnet}},
	{name: "the log sink", id: "batch-log-sink", want: "is not in proj-1234",
		without: []string{batchStepSink}},
	{name: "the sink's Batch arm", id: "batch-log-sink", want: "has no Batch arm",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.logging.AddSink("proj-1234", infra.LogSink,
				"logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro", batchCloudRunArm)
		}},
	{name: "the log exclusion", id: "batch-log-exclusion", want: "is not in proj-1234",
		without: []string{batchStepExcl}},
	{name: "the exclusion's Batch arm", id: "batch-log-exclusion", want: "has no Batch arm",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.logging.AddExclusion("proj-1234", infra.LogExclusion, batchCloudRunArm)
		}},
	{name: "the job image's repository", id: "batch-job-image", want: "is not in proj-1234",
		without: []string{batchStepRepo}},
	{name: "the image's repository being a standard one", id: "batch-job-image", want: "whose mode is REMOTE_REPOSITORY",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.ar.SetRepositoryMode("proj-1234", batchRegion, batchJobRepo, "REMOTE_REPOSITORY")
		}},
	{name: "the block naming one of the installation's run accounts", id: "batch-run-accounts", want: "confused deputy",
		block: strings.Replace(batchBlock, "service_account: "+batchRunAccount, "service_account: "+batchReaperAccount, 1)},
	{name: "the installation's run accounts", id: "batch-run-accounts", want: "no Fugaro run account at all",
		without: []string{batchStepRunAcct}},
	{name: "the run accounts' two project roles", id: "batch-run-account-roles", want: "roles/batch.agentReporter",
		alter: func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.crm.SetPolicy("proj-1234", batchListerPolicy(batchLauncher))
		}},
	{name: "the launchers' conditioned jobsEditor grant", id: "batch-launcher-iam", want: "without the compute module's condition",
		alter: func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.crm.SetPolicy("proj-1234", append([]gcpfake.Binding{
				{Role: batchLauncherRole, Members: []string{batchLauncher}}}, batchProvisionedPolicy()[1:]...)...)
		}},
	{name: "the launchers' actAs on the run account", id: "batch-run-account-actas", want: "cannot act as the run account",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.SetServiceAccountPolicy("proj-1234", batchRunAccount)
		}},
	{name: "the reaper's account", id: "batch-reaper-role", want: "is not in proj-1234",
		without: []string{batchStepReaperAC}},
	{name: "the reaper's custom role", id: "batch-reaper-role", want: "does not exist",
		without: []string{batchStepReaperRR}},
	{name: "the reaper's five permissions", id: "batch-reaper-role", want: "is missing the permissions the sweep needs",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.SetRolePermissions("projects/proj-1234/roles/"+infra.ReaperRoleID, infra.ReaperPermissions[:4]...)
		}},
	{name: "a reaper role that is exactly those five", id: "batch-reaper-role", want: "carries permissions the sweep does not use",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.SetRolePermissions("projects/proj-1234/roles/"+infra.ReaperRoleID,
				append(slices.Clone(infra.ReaperPermissions), "batch.jobs.create")...)
		}},
	{name: "the reaper's Cloud Run job", id: "batch-reaper-job", want: "is not in proj-1234",
		without: []string{batchStepReaperJo}},
	{name: "the reaper job running as the reaper account", id: "batch-reaper-job", want: "not as the reaper account",
		alter: func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.run.SetJobJSON(infra.ReaperJob, `{"template":{"template":{"serviceAccount":"`+batchRunAccount+`"}}}`)
		}},
	{name: "the reaper's schedule", id: "batch-reaper-schedule", want: "is not in proj-1234",
		without: []string{batchStepReaperSc}},
	{name: "the schedule minting from the reaper account", id: "batch-reaper-schedule", want: "mints its OAuth token from",
		alter: func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.scheduler.SetJob("proj-1234", batchSchedulerRegion, infra.ReaperSchedulerJob, "https://example.invalid/", batchRunAccount)
		}},
}

// batchCloudRunArm is the installation's sink and exclusion filter with the
// Cloud Run arm alone: what an installation applied before the Batch arm was
// added holds.
const batchCloudRunArm = `resource.type="cloud_run_job" AND labels."fugaro"="managed"`

// TestDoctorBatchGateRefusesAnInstallationThatSelectsTheName is Task 10's
// property test (plan 2026-10-19-compute-backend.md Task 10, "Tests", in the
// shape of the managed-projects plan's Task 0.3): over the whole product of the
// gate's conditions, an installation that fails any one of them is refused.
//
// Each case builds three worlds and asserts the three things the enable turns
// on:
//
//   - the whole installation, SELECTING backend: cloud-batch: doctor is not
//     refused (o.OK), and the condition's own check is ok -- so the list below
//     is a list of things that are actually checked, not of things this test
//     happens to name;
//   - the same world WITHOUT that one condition: doctor is refused (o.OK is
//     false), the condition's own check carries no severity at all -- doctor's
//     own hard failure, which is the whole of what Task 10 hands over -- and
//     its message is its own, matched by a phrase only that finding carries;
//   - the same absent world on an installation that does NOT select the name
//     (the same config with backend: cloud-run, the default): the same check
//     now carries "warning" and doctor is not refused. That is the deliberate
//     non-change: a batch block is work in progress for an installation
//     running fine on Cloud Run, and a hard failure there would refuse an
//     installation this task must not touch.
//
// Mutation (drop batchSeverity's selection arm -- return "warning" always):
// every absent case stops failing hard. Mutation (add a check to
// doctorBatchChecks without a case here): nothing in this table covers it, and
// the first case's assertion that the whole installation is not refused is what
// a check that fails unconditionally would break.
func TestDoctorBatchGateRefusesAnInstallationThatSelectsTheName(t *testing.T) {
	// world builds one installation. absent applies the case's own removal
	// (its block, its skipped steps and its alter) -- and nothing of it is
	// applied to the whole world, or every case would report its own finding
	// three times.
	world := func(t *testing.T, tc batchGateCondition, absent, selected bool) (*doctorRig, doctorOutput) {
		t.Helper()
		r, f := newDoctorRig(t), newBatchFakes(t)
		block := batchBlock
		if absent {
			if tc.block != "" {
				block = tc.block
			}
			batchProvisionedWithout(t, r, f, tc.without...)
			if tc.alter != nil {
				tc.alter(t, r, f)
			}
		} else {
			batchProvisioned(t, r, f)
		}
		if selected {
			r.withBatchSelected(t, f, block, batchLists)
		} else {
			r.withBatchBlock(t, f, block, batchLists)
		}
		return r, r.runDoctorJSON(t)
	}

	for _, tc := range batchGateConditions {
		t.Run(tc.name, func(t *testing.T) {
			// The whole installation, selected: not refused, and this
			// condition's own check passes.
			_, whole := world(t, tc, false, true)
			if !whole.OK {
				t.Errorf("a fully provisioned installation that selects the backend is refused: %+v", failingChecks(whole))
			}
			if c, ok := doctorCheckByID(whole.Checks, tc.id); !ok || !c.OK {
				t.Fatalf("the provisioned check %s = %+v (%v), want ok: this case's condition is not one doctor checks", tc.id, c, ok)
			}

			// Without it: refused, hard, with a message of its own.
			_, absent := world(t, tc, true, true)
			c, ok := doctorCheckByID(absent.Checks, tc.id)
			if !ok {
				t.Fatalf("doctor reports no %s check for %s", tc.id, tc.name)
			}
			if c.OK {
				t.Fatalf("with %s absent, %s reads ok: %+v", tc.name, tc.id, c)
			}
			if c.Severity != "" {
				t.Errorf("with %s absent, %s has severity %q: an installation that selects the backend is refused for it, so it takes doctor's own hard failure and not a warning", tc.name, tc.id, c.Severity)
			}
			if !strings.Contains(c.Problem, tc.want) {
				t.Errorf("the finding for %s is not its own (%q missing): %s", tc.name, tc.want, c.Problem)
			}
			if absent.OK {
				t.Errorf("doctor is not refused for %s: %+v", tc.name, failingChecks(absent))
			}

			// The same absent world on Cloud Run: a warning, and no refusal.
			_, cloudRun := world(t, tc, true, false)
			cr, ok := doctorCheckByID(cloudRun.Checks, tc.id)
			if !ok {
				t.Fatalf("doctor reports no %s check for an installation that runs on cloud-run", tc.id)
			}
			if cr.OK || cr.Severity != "warning" {
				t.Errorf("without the backend selected, %s = %+v, want a warning: a batch block is work in progress for an installation that runs on Cloud Run (batchSeverity)", tc.id, cr)
			}
			if !cloudRun.OK {
				t.Errorf("a Batch check refuses an installation that does not select the backend: %+v", failingChecks(cloudRun))
			}
		})
	}
}

// failingChecks are the checks that fail doctor (no severity and not ok), for a
// failure message that names what refused the installation rather than the
// whole list.
func failingChecks(o doctorOutput) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if !c.OK && c.Severity == "" {
			out = append(out, c)
		}
	}
	return out
}

// TestDoctorBatchNamesTheArtifactBoundary: Global Constraint 13 asks doctor to
// name the boundary an artifact-producing run meets on this backend, and this
// is the check that does it. Informational, never a warning: it describes the
// plan's own decision (docs/design/artifact-runs.md §8.5 and D11), not a fault
// an installation can fix, and a warning no run of fugaro update could clear
// would make Task 10's gate unsatisfiable -- which is why the severity matters
// here and not only the text.
//
// Mutation (drop the check; make it a warning; or drop §8.5 and D11): the
// named assertion fails.
func TestDoctorBatchNamesTheArtifactBoundary(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatch(t, f, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-artifact-boundary")
	if !ok {
		t.Fatal("doctor does not name the artifact boundary for a Batch installation")
	}
	if c.Severity != "info" {
		t.Errorf("the boundary's check has severity %q, want info: it is a decision and not a fault, and a warning would fail doctor --strict for every Batch installation", c.Severity)
	}
	for _, want := range []string{"artifact-runs.md", "§8.5", "D11", "cloud-run"} {
		if !strings.Contains(c.Problem+c.Fix, want) {
			t.Errorf("the boundary's text does not name %q: %s / %s", want, c.Problem, c.Fix)
		}
	}
	// An installation with no batch block gets none of these checks, this one
	// included: the same silence the other batch checks keep.
	if _, ok := doctorCheckByID(newDoctorRig(t).runDoctorJSON(t).Checks, "batch-artifact-boundary"); ok {
		t.Error("an installation with no batch block is told about the boundary")
	}
}

// TestBatchSinkArmMatchesTheModulesFilter: batchSinkArm is a copy of the
// installation module's own arm (deploy/terraform/gcp/modules/installation/
// logging.tf's local.fugaro_batch_logs), and the sink check reads it with
// strings.Contains. Nothing else holds the two together: the module's side is
// pinned by deploy/terraform/terraform_test.go, but a whitespace or label edit
// there would leave doctor warning on every correctly applied installation
// with no Go test failing -- the asymmetry with batchLauncherRole, which
// internal/infra/tf.Cover holds to the compute module, is why this reads the
// module's file rather than restating the arm.
//
// Mutation (change the constant, or the module's local): this fails.
func TestBatchSinkArmMatchesTheModulesFilter(t *testing.T) {
	data, err := os.ReadFile("../../deploy/terraform/gcp/modules/installation/logging.tf")
	if err != nil {
		t.Fatal(err)
	}
	// The arm is a Terraform string literal, so its inner quotes are escaped
	// there and not in the Go constant.
	tf := strings.ReplaceAll(string(data), `\"`, `"`)
	if !strings.Contains(tf, batchSinkArm) {
		t.Errorf("the installation module's filter no longer carries the arm doctor checks for (%s): doctor would warn on every correctly applied installation, and the module and the Go constant change together", batchSinkArm)
	}
}
