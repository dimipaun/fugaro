package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	crm "google.golang.org/api/cloudresourcemanager/v1"
)

// The Batch half of `fugaro doctor` (plan 2026-10-19-compute-backend.md Task 8
// step 5, design compute-backend.md §4.4; Task 10 step 2 turns the same checks
// into the gate). Every check fires only for an installation that carries a
// batch block, and its severity is what the installation's own selection
// decides (batchSeverity): a hard failure with no severity for a config that
// selects backend: cloud-batch -- which is the gate the enable hands over:
// every one of them has to pass -- and a warning for one that merely carries
// the block while it runs on Cloud Run.

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
// comparison is a comparison and not the recorded-zero case. opt_in is `in`,
// which is what an installation that opted in has (BR7, D3110): every fixture
// built on this block is an installation whose claim can be made, so a check
// that fails here is a check failing for the reason the case names.
const batchBlock = `batch:
  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1
  machine_type: e2-standard-4
  boot_image: projects/proj-1234/global/images/family/fugaro-preload-abc12345
  network: projects/proj-1234/global/networks/fugaro
  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro
  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com
  socket_gid: 989
  opt_in: in
`

// batchBlockWithoutOptIn is the same block as an installation that never opted
// in carries it: the field is absent, which is a state of its own and not a
// zero value (D3110, D3111).
var batchBlockWithoutOptIn = strings.Replace(batchBlock, "  opt_in: in\n", "", 1)

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
	batchStepLink     = "the credentials service account's read on the Batch jobs"
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
				"logging.googleapis.com/projects/proj-1234/locations/us/buckets/fugaro", batchBothArms)
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
		{batchStepLink, func(_ *testing.T, _ *doctorRig, f batchRigFakes) {
			f.iam.AddRole("proj-1234", batchLinkRole, "Fugaro Batch link", false)
			f.iam.SetRolePermissions("projects/proj-1234/roles/"+batchLinkRole, batchJobsGetPermission, "batch.jobs.list")
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
// the launchers' conditioned grant, the two roles every run account holds, and
// the credentials service account's binding to the link's role (BR7's second
// half, BR5's to plan: the role's own id is the root's and this check reads its
// permissions and not its name).
func batchProvisionedPolicy() []gcpfake.Binding {
	return append(batchProvisionedPolicyWithoutLink(), batchLinkBinding())
}

// batchProvisionedPolicyWithoutLink is the same policy for an installation the
// root has not yet been fed the credentials service account's read for: what
// every Batch check but batch-link-grant passes on.
func batchProvisionedPolicyWithoutLink() []gcpfake.Binding {
	return []gcpfake.Binding{
		batchListerPolicy(batchLauncher),
		{Role: batchAgentReporterRole, Members: []string{"serviceAccount:" + batchRunAccount}},
		{Role: batchLogWriterRole, Members: []string{"serviceAccount:" + batchRunAccount}},
	}
}

// batchLinkBinding is the credentials service account's grant of the link's
// role: the one binding the claim's read is made through.
func batchLinkBinding() gcpfake.Binding {
	return gcpfake.Binding{Role: "projects/proj-1234/roles/" + batchLinkRole, Members: []string{"serviceAccount:" + batchCredsAccount}}
}

// batchCredsAccount is the credentials service's own account
// (infra.CredentialsAccountID in this installation's project): the member the
// link's read is granted to, and never a run account.
const batchCredsAccount = infra.CredentialsAccountID + "@proj-1234.iam.gserviceaccount.com"

// batchLinkRole is the custom role the compute root plans for the link's read.
// The id is BR5's to settle and this fixture's to name: doctor's check reads
// the role's PERMISSIONS through the API and never its name.
const batchLinkRole = "fugaroBatchLink"

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
		"batch-opt-in", "batch-apis", "batch-job-definition", "batch-cost", "batch-image-family",
		"batch-network", "batch-subnetwork", "batch-log-sink", "batch-log-exclusion",
		"batch-job-image", "batch-run-accounts", "batch-run-account-roles",
		"batch-run-account-actas", "batch-link-grant", "batch-launcher-iam", "batch-reaper-role",
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
		"logging.googleapis.com/projects/proj-1234/locations/us/buckets/fugaro", batchSinkArm)
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

// TestDoctorBatchComparesTheRecordedSocketGID is step 2's comparison as its
// own case (plan 2026-10-19-compute-backend.md Task 10): the generation the
// preload image family serves carries the socket's gid as its own
// socket-gid label, so the number the installation recorded in
// batch.socket_gid is checked against the image a run boots and not trusted.
// Every rebuild re-reads that gid off the builder VM and labels a new
// generation with it, so a recorded value the image has moved past is a Docker
// run whose --group-add names a gid the image no longer has.
//
// The three cases the plan names, and the three readings they have to keep
// apart: a recorded gid the image has moved past (refused), a recorded gid the
// image carries (passes), and a recorded ZERO (not refused) -- zero means this
// installation recorded no socket at all, which is Task 7's launch refusal's
// shape and not this gate's, and refusing the whole installation over it would
// be this gate deciding a question the config already answers.
//
// Mutation (drop the comparison and trust batch.socket_gid; refuse a recorded
// zero): the mismatch case stops failing, or the zero case starts to.
func TestDoctorBatchComparesTheRecordedSocketGID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recorded string
		label    string
		want     bool
	}{
		{"a recorded gid the image has moved past", "989", "1000", false},
		{"a recorded gid the image carries", "989", "989", true},
		{"a recorded zero", "0", "1000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			block := strings.Replace(batchBlock, "socket_gid: 989", "socket_gid: "+tc.recorded, 1)
			// The family's label is the generation's, so it is placed rather
			// than provisioned: the step above writes the one this fixture's
			// block records.
			f.compute.AddImageFamilyLabels(batchPreloadFamily, map[string]string{infra.SocketGIDLabel: tc.label})
			batchProvisionedWithout(t, r, f, batchStepFamily)
			r.withBatchSelected(t, f, block, batchLists)

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-image-family")
			if !ok {
				t.Fatal("doctor reports no batch-image-family check")
			}
			if c.OK != tc.want {
				t.Fatalf("with batch.socket_gid %s against %s=%s, the check = %+v, want ok=%v", tc.recorded, infra.SocketGIDLabel, tc.label, c, tc.want)
			}
			if tc.want {
				return
			}
			if c.Severity != "" {
				t.Errorf("the mismatch has severity %q: an installation that selects the backend is refused for it, so it takes doctor's own hard failure", c.Severity)
			}
			for _, want := range []string{tc.recorded, tc.label, infra.SocketGIDLabel, "unable to open the socket"} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the mismatch does not name %q: %s", want, c.Problem)
				}
			}
		})
	}
}

// TestDoctorBatchSaysPropagatingForAPolicyItCannotRead is step 3 (plan
// 2026-10-19-compute-backend.md Task 10): a doctor run against an installation
// whose apply has just finished can see a binding that is not readable yet
// (Check 57a measured it), so a refused policy read is reported as a grant that
// may still be propagating and NOT as a missing one. The two are
// indistinguishable from here -- a 403 is what both produce -- and reporting
// the second sends an operator to re-provision an installation that is
// correctly provisioned.
//
// This is the plan's red step: a check that reports a missing grant for a
// binding that exists but is not readable yet fails here, on the assertion that
// the message says propagating and on the three phrases a missing-grant finding
// carries.
//
// Mutation (report a denied read as a missing grant): the assertions below
// fail.
func TestDoctorBatchSaysPropagatingForAPolicyItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
	}{
		{"the launchers' conditioned grant", "batch-launcher-iam"},
		{"the run accounts' two project roles", "batch-run-account-roles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			// The policy is written -- batchProvisioned wrote it -- and the
			// read of it is refused: the shape a binding written moments ago
			// produces.
			r.crm.DenyPolicy("proj-1234")
			r.withBatchSelected(t, f, batchBlock, batchLists)

			o := r.runDoctorJSON(t)
			c, ok := doctorCheckByID(o.Checks, tc.id)
			if !ok {
				t.Fatalf("doctor reports no %s check", tc.id)
			}
			if c.OK {
				t.Fatalf("%s reads ok for a policy it could not read: %+v", tc.id, c)
			}
			if c.Severity != "" {
				t.Errorf("%s has severity %q: an installation that selects the backend is refused for it", tc.id, c.Severity)
			}
			for _, want := range []string{"propagating", "57a", "§8.1 task 9"} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("%s does not say %q: %s", tc.id, want, c.Problem)
				}
			}
			for _, missing := range []string{"do not hold", "without the compute module's condition", "not one of this installation's run accounts"} {
				if strings.Contains(c.Problem, missing) {
					t.Errorf("%s reports a missing grant for a policy it could not read (%q): %s", tc.id, missing, c.Problem)
				}
			}
			// Doctor IS refused, and that is the gate working: an installation
			// that selects the name is refused for a check that could not be
			// made (batchSeverity's hard failure for the selection), so the
			// operator fixes the credential and reruns -- which is what the
			// propagating sentence above sends them to do. What must not
			// happen, and the assertion above is about, is this being reported
			// as a grant the installation is missing.
			if o.OK {
				t.Errorf("%s reads ok and doctor passes for a policy it could not read", tc.id)
			}
		})
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
	const wantDest = "logging.googleapis.com/projects/proj-1234/locations/us/buckets/fugaro"
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

// TestDoctorReadsTheLogBucketWhereItIs (LP6, R5): the sink check compares the
// live destination against the installation's OWN log bucket. That bucket's
// location is the one the config's log view names -- the view path the module
// wrote into the outputs an earlier update recorded -- and place.For's answer
// for the region only when the config records no view at all. A bucket's
// location is permanent, so a check that assumed one would warn about a sink
// routing exactly where the installation's bucket is, which is the warning this
// test must not see.
func TestDoctorReadsTheLogBucketWhereItIs(t *testing.T) {
	for _, tc := range []struct {
		name string
		view string
		// bucket is where the sink routes, as the trailing locations/<bucket>
		// pair of the project's own bucket path.
		bucket string
		ok     bool
	}{
		{"the location the recorded view names", "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs", "global", true},
		{"the mapping's answer, with no view recorded", "", "us", true},
		{"the mapping's answer, against a view that names another", "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs", "us", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBatchFakes(t)
			f.logging.AddSink("proj-1234", infra.LogSink,
				"logging.googleapis.com/projects/proj-1234/locations/"+tc.bucket+"/buckets/fugaro", batchSinkArm)
			r := newDoctorRig(t)
			extra := ""
			if tc.view != "" {
				extra = "log_view: " + tc.view + "\n"
			}
			r.withBatch(t, f, extra)
			c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-log-sink")
			if !found {
				t.Fatal("doctor reports no batch-log-sink check")
			}
			if c.OK != tc.ok {
				t.Fatalf("check = %+v, want ok=%v: the sink routes to the bucket's own location and nothing here derives it", c, tc.ok)
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

// TestDoctorBatchReportsAnEmptyLauncherList: the compute module's launcher
// grant is for_each over the run's own launcher list, which is
// terraform.operators whole -- the admins among them, because an admin operates
// too (init_compute.go) -- so an installation whose list holds nobody has no
// member the grant can be about and nobody who can start a run. That is a fact
// about the config, and it is the one this check is about: a list an apply
// wrote empty must not be invisible to doctor.
//
// Mutation (return nil instead of this warning when the list is empty, as the
// check did while it subtracted the admins from the list before testing it --
// roles.go's removed launcherOnly): doctor reports no batch-launcher-iam check
// at all and this fails.
func TestDoctorBatchReportsAnEmptyLauncherList(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// The module's own grant, on nobody: the policy is what it is, and the
	// list the check reads is the config's.
	r.withBatch(t, f, "")

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-launcher-iam")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (%v), want a warning: the config names no launcher at all", c, ok)
	}
	if !strings.Contains(c.Problem, "terraform.operators") || !strings.Contains(c.Problem, "no run can start") {
		t.Errorf("the warning does not say the config holds no launcher: %s", c.Problem)
	}
	if !strings.Contains(c.Fix, "--operator") {
		t.Errorf("the Fix does not name the door that writes the list: %s", c.Fix)
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
// Mutation (read lc.Terraform.Operators and lc.Terraform.Admins together, as
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

// TestDoctorPassesAnInstallationThatOptedIn is BR7's first half as its own
// case: an installation whose block carries the opt-in fact and whose project
// grants the credentials service account the link's read passes BOTH of the
// claim's checks -- and, because that is the whole of what a Batch installation
// needs beyond what Task 10's gate already covered, doctor does not refuse it.
//
// Mutation (make batch-opt-in fire on a block that carries the fact, or
// batch-link-grant compare the role's NAME against a literal): the provisioned
// installation is refused, and this fails.
func TestDoctorPassesAnInstallationThatOptedIn(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatchSelected(t, f, batchBlock, batchLists)

	o := r.runDoctorJSON(t)
	for _, id := range []string{"batch-opt-in", "batch-link-grant"} {
		c, ok := doctorCheckByID(o.Checks, id)
		if !ok {
			t.Fatalf("doctor reports no %s check for an installation that opted in", id)
		}
		if !c.OK {
			t.Errorf("%s = %+v, want ok: the opt-in fact is present and the link's read is granted", id, c)
		}
	}
}

// TestDoctorNamesTheUpdateWhereTheLinkGrantIsMissing is BR7's first refusal:
// the installation opted in and everything Task 10 provisions is there, and the
// one thing missing is the read the claim's link makes -- which only the
// compute root `fugaro update` applies creates. The check says so, names the
// update as its fix, and carries the mark `fugaro backends use` reads before it
// refuses a selection (R14): selecting is not what is wrong here.
//
// Mutation (drop the mark from this arm): the last assertion fails, and BR18's
// selection starts waiting for a grant only the update makes.
func TestDoctorNamesTheUpdateWhereTheLinkGrantIsMissing(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// The root has not been fed the credentials service account: the role
	// exists and nothing is bound to it.
	r.crm.SetPolicy("proj-1234", batchProvisionedPolicyWithoutLink()...)
	r.withBatchSelected(t, f, batchBlock, batchLists)

	// The mark is read in process: it is what the gate reads and --json does
	// not carry it.
	c, ok := doctorCheckByID(batchChecksInProcess(t, r), "batch-link-grant")
	if !ok {
		t.Fatal("doctor reports no batch-link-grant check")
	}
	if c.OK {
		t.Fatalf("an installation whose link's read is not granted reads ok: %+v", c)
	}
	if c.Severity != "" {
		t.Errorf("batch-link-grant has severity %q, want doctor's own hard failure for an installation that selects the backend", c.Severity)
	}
	for _, want := range []string{batchCredsAccount, "batch.jobs.get", "docs/batch-run-identity/implementation-plan.md"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the finding does not name %q: %s", want, c.Problem)
		}
	}
	if !strings.Contains(c.Fix, "fugaro update") {
		t.Errorf("the fix does not send the operator to the update: %s", c.Fix)
	}
	if !c.updateFix {
		t.Error("the missing-grant failure does not carry the update mark: the selection would wait for the root's own work (R14)")
	}
}

// TestDoctorNamesOptingInWhereTheFactIsMissing is BR7's second refusal, and the
// one no cloud fact can clear: the block carries no opt-in fact, so the
// installation never answered whether Batch runs are wanted. The fix is opting
// in -- an operator's write of the published block -- and not the update, which
// applies the compute root only where the fact is already there (BR6, R2).
//
// The check is NOT marked: its fix is the operator's own answer, so a selection
// cannot be let through for it (R14's list is the root's work alone).
//
// Mutation (say `fugaro update` alone is the fix, or mark the check): the
// assertions below fail, and a reader is sent to a command that cannot do this.
func TestDoctorNamesOptingInWhereTheFactIsMissing(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatchSelected(t, f, batchBlockWithoutOptIn, batchLists)

	c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-opt-in")
	if !ok {
		t.Fatal("doctor reports no batch-opt-in check")
	}
	if c.OK {
		t.Fatalf("a block with no opt-in fact reads ok: %+v", c)
	}
	for _, want := range []string{"opt-in fact is absent", "batch.opt_in", "R3"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the finding does not name %q: %s", want, c.Problem)
		}
	}
	for _, want := range []string{"opt this installation in", "fugaro backends use cloud-batch"} {
		if !strings.Contains(c.Fix, want) {
			t.Errorf("the fix does not name %q: %s", want, c.Fix)
		}
	}
	if c.updateFix {
		t.Error("the opt-in failure carries the update mark: an operator's own answer is not work the compute root does")
	}
}

// TestValidateAgreesWithDoctorOnTheClaim is BR7's offline half: the one Batch
// fact a config decides is answered with ONE sentence by both readers, so an
// operator told by doctor and by validate is told the same thing about the same
// block. Withdrawn is a state of its own and takes the same refusal.
//
// Mutation (write a second sentence in either reader, or read a different field
// in one of them): the two texts stop being equal, and this fails.
func TestValidateAgreesWithDoctorOnTheClaim(t *testing.T) {
	for _, tc := range []struct {
		name    string
		block   string
		refused bool
	}{
		{name: "an installation that opted in", block: batchBlock},
		{name: "a block with no opt-in fact", block: batchBlockWithoutOptIn, refused: true},
		{name: "a block whose opt-in was withdrawn",
			block: strings.Replace(batchBlock, "opt_in: in", "opt_in: withdrawn", 1), refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			r.withBatchSelected(t, f, tc.block, batchLists)

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-opt-in")
			if !ok {
				t.Fatal("doctor reports no batch-opt-in check")
			}
			// validate is asked the same installation's config, offline: the
			// reader below is the one `fugaro validate` runs.
			lc, err := localcfg.Parse([]byte(readFileString(t, r.cfgPath)))
			if err != nil {
				t.Fatalf("the rig's own config does not parse: %v", err)
			}
			ps := batchValidateProblems(lc, &config.Config{Project: lc.Name}, nil)
			var optIn string
			for _, p := range ps {
				if p.Path == "batch.opt_in" {
					optIn = p.Message
				}
			}
			if !tc.refused {
				if !c.OK || optIn != "" {
					t.Fatalf("an installation that opted in is refused: doctor=%+v, validate=%q", c, optIn)
				}
				return
			}
			if c.OK || optIn == "" {
				t.Fatalf("a block that carries no opt-in is passed: doctor=%+v, validate=%q", c, optIn)
			}
			if optIn != c.Problem {
				t.Errorf("the two readers of one fact disagree:\n doctor:   %s\n validate: %s", c.Problem, optIn)
			}
			if got, want := strings.Contains(optIn, "withdrawn"), tc.name == "a block whose opt-in was withdrawn"; got != want {
				t.Errorf("the sentence does not name the state the fact is in (withdrawn=%v, want %v): %s", got, want, optIn)
			}
		})
	}
}

// The check asks budgetMintOn, the function a launch decides with, so these
// configs are the states of its own answer: a budget database, with the signer
// account a run's identity is minted through -- which mints whatever the mode
// says and whether or not a policy is published (AB4, F7) -- and no database at
// all, where nothing mints because no launch of the installation happens (the
// launcher refuses it with the fix), so the pair this check reads is not what
// stops it and the check has nothing to report.
//
// A database with no signer yet is refused here as well, which is the one shape
// where an arm of this check could drift from `fugaro validate`'s offline arm:
// the pair is the database's own (AB4, F7), and the two readers of it answer
// for the same configs (validate_batch.go's case is a block with the same two
// fields and no signer).
//
// Mutation (drop the check from doctorBatchChecks): the minting cases read as
// absent, and every "doctor reports no batch-budget-seal check" assertion
// fails. Mutation (read the mode again): the two off cases stop being refused,
// which is the direction AB4 closed. Mutation (read the signer): the
// database-with-no-signer case passes and fails here.
func TestDoctorBatchRefusesAMintingInstallation(t *testing.T) {
	// budgetYAML is one installation's budget block, with a database of its own
	// per case: a policy published for one case must not be found by another.
	budgetYAML := func(t *testing.T, mode string, publish bool, signer string) string {
		t.Helper()
		db := gcpfake.NewRTDB(t)
		if publish {
			db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
			db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version})
		}
		block := "budget: { mode: " + mode + ", per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + db.URL
		if signer != "" {
			block += ", token_signer: " + signer
		}
		return block + " }\n"
	}
	for _, tc := range []struct {
		name       string
		mode       string
		publish    bool
		noDatabase bool
		noSigner   bool
		mints      bool
	}{
		{name: "a budget mode that mints", mode: "enforce", mints: true},
		{name: "a mode that says off with nothing published", mode: "off", mints: true},
		{name: "a mode that says off with a published policy", mode: "off", publish: true, mints: true},
		{name: "a database with no signer yet", mode: "enforce", noSigner: true, mints: true},
		{name: "an installation with no budget database", noDatabase: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			signer := testSigner
			if tc.noSigner {
				signer = ""
			}
			block := batchLists + budgetYAML(t, tc.mode, tc.publish, signer)
			if tc.noDatabase {
				block = batchLists + "budget: { per_run_usd: 5 }\n"
			}
			r.withBatchSelected(t, f, batchBlock, block)
			o := r.runDoctorJSON(t)
			c, ok := doctorCheckByID(o.Checks, "batch-budget-seal")
			if !ok {
				t.Fatalf("doctor reports no batch-budget-seal check for an installation that selects the backend")
			}
			if !tc.mints {
				if !c.OK {
					t.Fatalf("an installation that does not mint is refused by the pair check: %+v", c)
				}
				return
			}
			if c.OK {
				t.Fatalf("an installation that mints and selects the backend reads ok: %+v", c)
			}
			if c.Severity != "" {
				t.Errorf("the pair has severity %q, want doctor's own hard failure: an installation that selects the backend is refused for it", c.Severity)
			}
			for _, want := range []string{"mints budget tokens", "docs/budget-token-channel/spec.md", "§1.6"} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the finding does not name %q: %s", want, c.Problem)
				}
			}
			// The repair is the installation's own answer, and it is the one
			// thing provisioning cannot change: this backend stays unselected.
			for _, want := range []string{"cloud-run", "Task 14"} {
				if !strings.Contains(c.Fix, want) {
					t.Errorf("the fix does not name %q: %s", want, c.Fix)
				}
			}
			if !o.OK {
				// The gate itself: doctor refuses the installation, not merely
				// one of its lines.
				if len(failingChecks(o)) == 0 {
					t.Errorf("doctor is refused with no hard failure to name: %+v", o.Checks)
				}
			}
		})
	}
}

// TestNoCheckMentionsAKey is the amendment's rule as a test: the release's key
// is gone, and no Batch check -- doctor's, or validate's offline problems --
// may name a key fact, a seal key or a KMS key any more. R13 is dropped with
// the key it was about, so a differing or missing key fact is not a failure
// either reader can produce.
//
// Mutation (keep the seal pair's check, or reword its fix into the removal):
// the scan finds the spelling below.
func TestNoCheckMentionsAKey(t *testing.T) {
	banned := []string{"seal_key", "seal key", "key fact", "cryptoKey", "KMS"}

	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// The world that reports the most: every cloud fact present and the opt-in
	// fact absent, so the claim's fact check prints beside Task 10's whole list.
	r.withBatchSelected(t, f, batchBlockWithoutOptIn, batchLists)
	for _, c := range r.runDoctorJSON(t).Checks {
		if !strings.HasPrefix(c.ID, "batch-") {
			continue
		}
		for _, bad := range banned {
			if strings.Contains(c.ID+c.Problem+c.Fix, bad) {
				t.Errorf("check %s names %q: %s %s", c.ID, bad, c.Problem, c.Fix)
			}
		}
	}

	lc, err := localcfg.Parse([]byte(readFileString(t, r.cfgPath)))
	if err != nil {
		t.Fatalf("the rig's own config does not parse: %v", err)
	}
	for _, p := range batchValidateProblems(lc, &config.Config{Project: lc.Name}, nil) {
		for _, bad := range banned {
			if strings.Contains(p.Message, bad) {
				t.Errorf("validate's problem at %s names %q: %s", p.Path, bad, p.Message)
			}
		}
	}
}

// TestTheChecksOnlyTheUpdateFixesAreMarked is R14's list, held exactly: the
// Batch checks whose fix is the compute root `fugaro update` applies carry the
// mark `fugaro backends use` reads before it refuses a selection, and no other
// check does. The list is by id and it is short on purpose -- a check added to
// it is a state an operator may select the backend in, and one left out of it
// is a selection that waits for work only the update does.
//
// Two worlds, because one check's failure precludes another's: a project with
// no run account at all reports batch-run-accounts and cannot report the two
// checks that read a grant ON an account (doctorBatchRunAccountRoles and
// doctorBatchActAs return nothing when the list is empty). The worlds' marks
// are unioned, and every mark has to be on the list -- batch-launcher-iam
// fails in both and is deliberately not marked: the widening it reports is an
// operator's grant and not the root's.
//
// Mutation (mark batch-launcher-iam, or stop marking batch-run-account-actas):
// the set stops matching, and either BR18 lets through a widening or waits for
// a grant that is not the root's.
func TestTheChecksOnlyTheUpdateFixesAreMarked(t *testing.T) {
	want := []string{"batch-link-grant", "batch-run-account-actas", "batch-run-account-roles", "batch-run-accounts"}
	worlds := []struct {
		name    string
		without []string
	}{
		// No run account at all: the claim's link is unbound too, so the two
		// account-grant checks have nothing to read.
		{name: "a project with no run account", without: []string{batchStepRunAcct, batchStepActAs, batchStepLink}},
		// The accounts exist and nothing is bound to them: the root has not
		// been applied over the project yet.
		{name: "run accounts with no grants on them", without: []string{batchStepPolicy, batchStepActAs, batchStepLink}},
	}

	var union []string
	for _, w := range worlds {
		t.Run(w.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisionedWithout(t, r, f, w.without...)
			r.withBatchSelected(t, f, batchBlock, batchLists)

			checks := batchChecksInProcess(t, r)
			var marked []string
			for _, c := range checks {
				if !strings.HasPrefix(c.ID, "batch-") || c.OK {
					continue
				}
				if !c.updateFix {
					continue
				}
				marked = append(marked, c.ID)
				if !slices.Contains(want, c.ID) {
					t.Errorf("%s carries the update mark and is not on R14's list %v: %s", c.ID, want, c.Problem)
				}
				if !strings.Contains(c.Fix, "fugaro update") {
					t.Errorf("%s carries the update mark and its fix does not name the update: %s", c.ID, c.Fix)
				}
			}
			slices.Sort(marked)
			union = append(union, marked...)
			if len(marked) == 0 {
				t.Errorf("no check of this world is marked, and the world is missing exactly the root's work: %+v", checks)
			}
		})
	}
	slices.Sort(union)
	union = slices.Compact(union)
	if !slices.Equal(union, want) {
		t.Errorf("the checks the update alone fixes are %v, want %v (R14)", union, want)
	}
}

// batchChecksInProcess runs doctor's Batch checks the way the gate runs them:
// in process, against the rig's own config and fakes. doctorCheck.updateFix is
// not in --json -- it is not a class, it is the one question `fugaro backends
// use` asks before refusing a selection -- so a test about the mark has to read
// the checks the gate reads rather than the report.
func batchChecksInProcess(t *testing.T, r *doctorRig) []doctorCheck {
	t.Helper()
	lc, err := localcfg.Load(r.cfgPath)
	if err != nil {
		t.Fatalf("the rig's own config does not read back: %v", err)
	}
	ctx := context.Background()
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		t.Fatalf("the rig's own resource_manager endpoint does not open: %v", err)
	}
	return doctorBatchChecks(ctx, lc, c)
}

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
	// absentOnly marks the one condition that is the block's own absence:
	// `backend: cloud-batch` selected by a config that was never provisioned
	// for it. Its check exists in the absent world alone -- the whole world
	// runs the checks below the block instead, and an installation that runs
	// on cloud-run and carries no block says nothing about Batch at all --
	// so the two assertions that assume a check in every world are inverted
	// for it. Leaving it out of the table instead would read as "not a gate
	// condition", which is the opposite of what it is.
	absentOnly bool
}

// batchGateConditions is the whole product of the gate's conditions. A check
// added to doctorBatchChecks without a case here is a condition nothing proves
// doctor refuses for -- which is what the plan's property test is for.
var batchGateConditions = []batchGateCondition{
	{name: "the batch block itself", id: "batch-block", want: "carries no batch block", absentOnly: true},
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
				"logging.googleapis.com/projects/proj-1234/locations/us/buckets/fugaro", batchCloudRunArm)
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
	{name: "a secret the installation did not create", id: "batch-secrets", want: "Fugaro did not create",
		block: batchSecretBlock},
	{name: "the block naming one of the installation's run accounts", id: "batch-run-accounts", want: "confused deputy",
		block: strings.Replace(batchBlock, "service_account: "+batchRunAccount, "service_account: "+batchReaperAccount, 1)},
	{name: "the installation's run accounts", id: "batch-run-accounts", want: "no Fugaro run account at all",
		without: []string{batchStepRunAcct}},
	{name: "the run accounts' two project roles", id: "batch-run-account-roles", want: "roles/batch.agentReporter",
		alter: func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.crm.SetPolicy("proj-1234", batchListerPolicy(batchLauncher))
		}},
	{name: "the block's opt-in fact", id: "batch-opt-in", want: "opt-in fact is absent",
		block: batchBlockWithoutOptIn},
	{name: "the link's read for the credentials service account", id: "batch-link-grant", want: "carry batch.jobs.get",
		alter: func(_ *testing.T, r *doctorRig, _ batchRigFakes) {
			r.crm.SetPolicy("proj-1234", batchProvisionedPolicyWithoutLink()...)
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
			if tc.absentOnly {
				block = ""
			} else if tc.block != "" {
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
			if c, ok := doctorCheckByID(whole.Checks, tc.id); tc.absentOnly {
				if ok {
					t.Errorf("a provisioned installation reports %s: it is the absent world's finding alone (%+v)", tc.id, c)
				}
			} else if !ok || !c.OK {
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
			if tc.absentOnly {
				// Nothing selects the backend and nothing published a block:
				// there is no Batch fact to read, so the finding does not
				// exist and doctor names Batch nowhere. The block is what an
				// installation's apply writes, so this is the state of every
				// installation that never asked for the backend.
				if ok {
					t.Errorf("an installation that runs on cloud-run and carries no batch block reports %s: %+v", tc.id, cr)
				}
				if !cloudRun.OK {
					t.Errorf("an installation with no batch block and no batch backend is refused: %+v", failingChecks(cloudRun))
				}
				return
			}
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

// TestDoctorBatchRefusesAnImageFromAPullThroughRepository is step 5's fifth
// entry as its own case (plan 2026-10-19-compute-backend.md Task 10): a
// published `batch.image` in a pull-through repository of the project OTHER
// than `fugaro-ghcr` -- the one `internal/cli`'s ParseShared refuses by name
// and by that name alone, because the reader has no repository list to tell the
// project's remote repositories from the ones this installation's builds push
// to (docs/shared-config/spec.md §5, TestParseSharedLeavesAnotherPullThroughRepository).
//
// The shape this task took is the second of the two the plan's vocabulary
// allows: the project's remote repositories are refused AS A CLASS, and the
// gate is where it is read. The gate reads the repository's MODE through this
// project's own Artifact Registry -- a name-based check (`fugaro-ghcr`, or any
// other ID) passes every repository an operator adds under another ID, and also
// passes a standard repository that later becomes a pull-through one -- and a
// repository that is not a STANDARD_REPOSITORY is a finding, which refuses an
// installation that selects the backend. The image is the code a run's VMs
// execute as the run account, so this is the confusion the entry names: the
// upstream's owner publishes under the name the cache serves, and the VMs run
// it with that account's grants on the project.
//
// Mutation (replace the mode rule with a `fugaro-ghcr` name check): the
// pull-through case below stops failing, and the standard-repository case in
// TestParseSharedAcceptsThePositivesTheBatchRefusalsLeaveOpen's own list still
// passes -- which is exactly the pair that distinguishes the shape.
func TestDoctorBatchRefusesAnImageFromAPullThroughRepository(t *testing.T) {
	const (
		pullThrough = "docker-hub"
		image       = "us-east5-docker.pkg.dev/proj-1234/" + pullThrough + "/attacker/evil:1"
	)
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"REMOTE_REPOSITORY", false},
		{"VIRTUAL_REPOSITORY", false},
		{"STANDARD_REPOSITORY", true},
		// A repository that never set the field: the API documents that as the
		// standard mode, which is what the installation's own repository does.
		{"", true},
	} {
		t.Run("mode "+oneLineCLI(tc.mode), func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			f.ar.AddRepository("proj-1234", batchRegion, pullThrough, nil)
			f.ar.SetRepositoryMode("proj-1234", batchRegion, pullThrough, tc.mode)
			batchProvisioned(t, r, f)
			r.withBatchSelected(t, f, strings.Replace(batchBlock, "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1", image, 1), batchLists)

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-job-image")
			if !ok {
				t.Fatal("doctor reports no batch-job-image check")
			}
			if c.OK != tc.want {
				t.Fatalf("the job image's repository with mode %q: %+v, want ok=%v", tc.mode, c, tc.want)
			}
			if tc.want {
				return
			}
			if c.Severity != "" {
				t.Errorf("the finding has severity %q: an installation that selects the backend is refused for it", c.Severity)
			}
			for _, want := range []string{image, pullThrough, "STANDARD_REPOSITORY", "design compute-backend.md §3.2, §4.4"} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the finding does not name %q: %s", want, c.Problem)
				}
			}
		})
	}
}

// batchSecretBlock is batchBlock with the one `secrets:` entry an installation
// publishes: an environment variable name and a Secret Manager secret's own
// resource name, never a value (localcfg's Batch.Secrets).
var batchSecretBlock = batchBlock + "  secrets: { OPENROUTER_API_KEY: projects/proj-1234/secrets/" + batchSecretID + " }\n"

// batchSecretID is the secret the block above names, and the one the fake is
// seeded with: a project secret this installation created (the labels
// internal/infra's own discovery lists them by).
const batchSecretID = "fugaro-openrouter-key"

// TestDoctorBatchRefusesASecretTheInstallationDidNotCreate is step 5's fourth
// entry as its own case (plan 2026-10-19-compute-backend.md Task 10): the
// batch block's `secrets:` entries become the job's secretVariables, the block
// is published to the runs bucket, and the only thing the reader
// (internal/cli's ParseShared) can hold them to is that they are secrets of the
// anchor's project -- it has a name, a GCP project and a bucket, and no way to
// ask which of that project's secrets this installation created. A writer with
// objectAdmin on the bucket can therefore name any secret the project holds,
// and this installation's own launcher would read it and hand its value to a
// run's agents: step 4's shape, with a secret instead of an account.
//
// The shape this task took is the ownership label, which is the same kind of
// answer step 4 takes from the account's display name: every Fugaro-created
// secret carries fugaro=managed (gcp.LabelManaged), which is what
// internal/infra's own discovery lists its secrets by, and a secret of the
// project without it is not one this installation created whatever its name.
// The gate refuses an installation that selects the backend for such an entry.
//
// Mutation (drop the check; or compare names instead of reading the label): the
// foreign case stops failing, and the case below of the installation's own
// secret -- which no name rule could tell from it -- starts failing.
func TestDoctorBatchRefusesASecretTheInstallationDidNotCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		labels  map[string]string
		want    bool
		comment string
	}{
		{"a secret this installation created", map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: "aurora"}, true,
			"the labels internal/infra's discovery lists an installation's project secrets by"},
		{"a secret of the project Fugaro did not create", map[string]string{"owner": "teammate"}, false,
			"a secret another team's deployment wrote: the block is published, so naming it is what the gate refuses"},
		{"a secret of the project with no labels at all", nil, false,
			"a stranger's secret never carries the ownership label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := newDoctorRig(t), newBatchFakes(t)
			batchProvisioned(t, r, f)
			r.sm.Seed(batchSecretID, tc.labels, []byte("not-a-real-value"))
			r.withBatchSelected(t, f, batchSecretBlock, batchLists)

			c, ok := doctorCheckByID(r.runDoctorJSON(t).Checks, "batch-secrets")
			if !ok {
				t.Fatal("doctor reports no batch-secrets check")
			}
			if c.OK != tc.want {
				t.Fatalf("the block's secret with labels %v: %+v, want ok=%v (%s)", tc.labels, c, tc.want, tc.comment)
			}
			if tc.want {
				return
			}
			if c.Severity != "" {
				t.Errorf("the finding has severity %q: an installation that selects the backend is refused for it", c.Severity)
			}
			for _, want := range []string{batchSecretID, "OPENROUTER_API_KEY", gcp.LabelManaged + "=" + gcp.ManagedValue, "did not create"} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the finding does not name %q: %s", want, c.Problem)
				}
			}
		})
	}
}

// TestDoctorBatchNamesTheArtifactBoundary: Global Constraint 13 asks doctor to
// name the boundary an artifact-producing run meets on this backend, and this
// is the check that does it. Informational, never a warning: it describes the
// plan's own decision (docs/artifact-runs/spec.md §8.5 and D11), not a fault
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
	for _, want := range []string{"artifact-runs/spec.md", "§8.5", "D11", "cloud-run"} {
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
