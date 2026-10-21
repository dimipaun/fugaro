package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The Batch half of `fugaro doctor` (plan 2026-10-19-compute-backend.md Task 8
// step 5, design compute-backend.md §4.4). Every check fires only for an
// installation that carries a batch block, and every one of them is a warning:
// nothing in this build can select backend: cloud-batch (internal/localcfg
// refuses the name, Global Constraint 12), so these report work in progress
// rather than a broken installation. Task 10 is the task that turns the block
// on and decides which of them the enable must satisfy.

// batchRigFakes are the three fakes a Batch installation's checks read, plus
// the rig's own CRM fake (already pointed at by resource_manager).
type batchRigFakes struct {
	compute *gcpfake.Compute
	logging *gcpfake.Logging
	usage   *gcpfake.ServiceUsage
}

// batchBlock is a batch block whose every value is this installation's own:
// the anchors the shared-config check applies would refuse anything else, and
// the names are what the Compute fake below is given.
const batchBlock = `batch:
  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1
  machine_type: e2-standard-4
  boot_image: projects/proj-1234/global/images/family/fugaro-preload-abc12345
  network: projects/proj-1234/global/networks/fugaro
  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro
  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com
`

// batchProvisioned is what the three fakes hold once the installation's apply
// and its preload build have run: the family, the network and the subnetwork,
// the log sink routing to the installation's bucket with both arms, and every
// API enabled (the fake answers ENABLED unless a test disables one).
func batchProvisioned(t *testing.T, f batchRigFakes) {
	t.Helper()
	f.compute.AddImageFamily("fugaro-preload-abc12345")
	f.compute.AddNetwork("fugaro")
	f.compute.AddSubnetwork("us-east5", "fugaro")
	f.logging.AddSink("proj-1234", infra.LogSink,
		"logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro",
		`(resource.type="cloud_run_job" AND labels."fugaro"="managed") OR (`+batchSinkArm+`)`)
}

// withBatch writes the rig's local config with batchBlock and the three
// endpoints its checks read, plus whatever extra YAML the test wants (the
// launcher lists, a log view). withBatchBlock is the same with a block of the
// test's own, for a case that is about the block itself.
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
		"endpoints: { compute: "+f.compute.URL+"/compute/v1/, logging: "+f.logging.URL+"/, service_usage: "+f.usage.URL+"/, ", 1)
	cfg += block + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
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
// generation. The launchers' grant is read from the rig's own CRM fake, which
// is why the policy is set here and not by batchProvisioned.
func TestDoctorBatchChecksPassOnAProvisionedInstallation(t *testing.T) {
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
	r.withBatch(t, f, "terraform: { launchers: [\"user:a@example.com\"] }\n")
	r.crm.SetPolicy("proj-1234", batchListerPolicy("user:a@example.com"))

	o := r.runDoctorJSON(t)
	for _, id := range []string{"batch-apis", "batch-job-definition", "batch-image-family", "batch-network", "batch-subnetwork", "batch-log-sink", "batch-launcher-iam"} {
		c, ok := doctorCheckByID(o.Checks, id)
		if !ok || !c.OK {
			t.Errorf("check %s = %+v (%v), want ok", id, c, ok)
		}
	}
}

// TestDoctorBatchChecksAreSilentWithoutABatchBlock: an installation that
// carries no batch block gets none of these checks. The backend's own name
// cannot be selected in this build, so the block is the only signal there is
// that an operator intends this backend, and a check on a project nobody
// provisioned for Batch would be noise.
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
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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
	f := batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
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
			r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
			batchProvisioned(t, f)
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
			r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
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
			r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
			batchProvisioned(t, f)
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
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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
// finding: warning about it would name a Fix -- run fugaro init -- that leaves
// the member in exactly the state the warning complains about.
//
// Mutation (read lc.Terraform.Launchers and lc.Terraform.Operators together, as
// this check did): the operator is reported as not holding the grant and the
// assertion that the check is ok fails.
func TestDoctorBatchDoesNotRequireTheGrantOfOperators(t *testing.T) {
	const launcher, operator = "user:a@example.com", "user:o@example.com"
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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

// TestDoctorBatchNamesTheArtifactBoundary: Global Constraint 13 asks doctor to
// name the boundary an artifact-producing run meets on this backend, and this
// is the check that does it. Informational, never a warning: it describes the
// plan's own decision (docs/design/artifact-runs.md §8.5 and D11), not a fault
// an installation can fix, and a warning no run of fugaro init could clear
// would make Task 10's gate unsatisfiable -- which is why the severity matters
// here and not only the text.
//
// Mutation (drop the check; make it a warning; or drop §8.5 and D11): the
// named assertion fails.
func TestDoctorBatchNamesTheArtifactBoundary(t *testing.T) {
	r, f := newDoctorRig(t), batchRigFakes{compute: gcpfake.NewCompute(t), logging: gcpfake.NewLogging(t), usage: gcpfake.NewServiceUsage(t)}
	batchProvisioned(t, f)
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
