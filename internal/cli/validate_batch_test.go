package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// The Batch half of `fugaro validate` (plan 2026-10-19-compute-backend.md
// Task 8 step 4, design compute-backend.md §4.4).
//
// Every check here fires only for an installation whose project config selects
// backend: cloud-batch, and internal/localcfg refuses that name until the
// enable task (Task 10) -- so no installation reaches any of these lines
// today, which is the plan's Global Constraint 12 and the reason these tests
// call the check directly rather than through the command. Task 10's own
// commit is what makes them reachable, and its test asserts the artifact
// refusal survives that enable.

// batchValidateRig is a project config that selects the Batch backend without
// going through localcfg's refusal (which is the build's, not the config's):
// the name is written onto the parsed struct, exactly as a config file would
// have set it. The YAML is a cloud-run config, so localcfg.Parse accepts it
// and every other field keeps the validation the tree already gives it.
func batchValidateRig(t *testing.T, machineType, backendName string) *localcfg.Config {
	t.Helper()
	yaml := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		// A complete batch block, less the two fields this rig varies: a block
		// missing its image, network or account is refused by localcfg's own
		// validation, which is not what these tests are about.
		"batch:\n" +
		"  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n" +
		"  boot_image: projects/proj-1234/global/images/family/fugaro-base\n" +
		"  network: projects/proj-1234/global/networks/fugaro\n" +
		"  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n" +
		"  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com\n"
	if machineType != "" {
		yaml += "  machine_type: " + machineType + "\n"
	}
	lc, err := localcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("the rig's own project config does not parse: %v", err)
	}
	lc.BackendName = backendName
	return lc
}

func batchValidateConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("the rig's fugaro.yaml has problems: %v", problems)
	}
	return cfg
}

const batchValidateYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 2, memory: 4Gi }
    timeouts: { total: 30m }
`

// TestValidateRefusesAnArtifactRunOnCloudBatch is Global Constraint 13's
// boundary, and the reason step 4 exists: an artifact-producing run stays on
// the per-repository Cloud Run backend until a per-run prefix condition or a
// per-run account exists, because a Batch run's account is not per-repository
// and "the same run's inputs, artifacts and record would be readable and
// overwritable by every run on the profile" (docs/design/artifact-runs.md
// §8.5, D11). The refusal is what Task 10 must leave standing; nothing else
// in this plan enforces that boundary.
//
// Mutation (drop the refusal; refuse every mode; or stop quoting §8.5 and D11
// in the message): the named assertion fails.
func TestValidateRefusesAnArtifactRunOnCloudBatch(t *testing.T) {
	lc := batchValidateRig(t, "e2-standard-4", backend.CloudBatch)
	cfg := batchValidateConfig(t, batchValidateYAML)
	rcp := &recipe.Recipe{Name: "deliver", Mode: recipe.ModeArtifact}

	ps := batchValidateProblems(lc, cfg, rcp)
	if len(ps) != 1 {
		t.Fatalf("batchValidateProblems = %v, want the one artifact refusal", ps)
	}
	for _, want := range []string{"docs/design/artifact-runs.md", "§8.5", "D11", "cloud-run"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal does not name %q: %s", want, ps[0].Message)
		}
	}
	// What it waits for, in the design's own terms: the refusal is a boundary
	// this plan leaves standing, not a bug to fix.
	if !strings.Contains(ps[0].Message, "per-run") {
		t.Errorf("the refusal does not say what it waits for: %s", ps[0].Message)
	}
}

// TestValidateAcceptsANonArtifactRecipeOnCloudBatch is the other half: the
// refusal is about artifact-producing runs, not about every run on the
// backend, and an implement recipe on the same installation passes.
//
// Mutation (refuse every recipe): this fails.
func TestValidateAcceptsANonArtifactRecipeOnCloudBatch(t *testing.T) {
	lc := batchValidateRig(t, "e2-standard-4", backend.CloudBatch)
	cfg := batchValidateConfig(t, batchValidateYAML)
	for _, mode := range []string{recipe.ModeImplement, recipe.ModeReview} {
		if ps := batchValidateProblems(lc, cfg, &recipe.Recipe{Name: "impl", Mode: mode}); len(ps) != 0 {
			t.Errorf("a mode: %s recipe is refused on cloud-batch: %v", mode, ps)
		}
	}
	// And a recipe the checkout does not carry (nil) is not an artifact one.
	if ps := batchValidateProblems(lc, cfg, nil); len(ps) != 0 {
		t.Errorf("an unresolved recipe is refused: %v", ps)
	}
}

// TestValidateArtifactRefusalIsCloudBatchOnly: the same artifact recipe on
// Cloud Run is not refused here. Cloud Run is where artifact runs live today
// (design artifact-runs.md D11), so a refusal that fired there would refuse
// the one backend the design keeps them on.
//
// Mutation (drop the backend gate): this fails.
func TestValidateArtifactRefusalIsCloudBatchOnly(t *testing.T) {
	cfg := batchValidateConfig(t, batchValidateYAML)
	rcp := &recipe.Recipe{Name: "deliver", Mode: recipe.ModeArtifact}
	for _, name := range []string{backend.CloudRun, ""} {
		lc := batchValidateRig(t, "", name)
		if ps := batchValidateProblems(lc, cfg, rcp); len(ps) != 0 {
			t.Errorf("backend %q: the artifact run is refused on a backend that is not cloud-batch: %v", lc.Backend(), ps)
		}
	}
}

// TestValidateRefusesAWorkflowLongerThanTheRunningCeiling: run.go sends a
// workflow's clock plus backend.TaskTimeoutSlack as the job's maxRunDuration,
// and Batch fails a task after 14 days RUNNING (design §1.3, §9). A workflow
// past that is a job Batch refuses at create, which is what validate is here
// to say first.
//
// Mutation (drop the check, drop the slack, or use a day count other than
// batch.MaxRunDuration): the refusal case fails.
func TestValidateRefusesAWorkflowLongerThanTheRunningCeiling(t *testing.T) {
	lc := batchValidateRig(t, "e2-standard-4", backend.CloudBatch)
	long := batchValidateConfig(t, `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 2, memory: 4Gi }
    timeouts: { total: 360h }
`)
	ps := batchValidateProblems(lc, long, nil)
	if len(ps) != 1 || ps[0].Path != "workflows.web.timeouts.total" {
		t.Fatalf("a 360h workflow on cloud-batch = %v, want one problem at workflows.web.timeouts.total", ps)
	}
	if !strings.Contains(ps[0].Message, "14 days") {
		t.Errorf("the timeout refusal does not name the ceiling: %s", ps[0].Message)
	}
	// The same workflow on Cloud Run is not this check's business.
	cr := batchValidateRig(t, "", backend.CloudRun)
	if ps := batchValidateProblems(cr, long, nil); len(ps) != 0 {
		t.Errorf("a 360h workflow on cloud-run is refused by the Batch check: %v", ps)
	}
	// And a workflow inside the ceiling passes.
	short := batchValidateConfig(t, batchValidateYAML)
	if ps := batchValidateProblems(lc, short, nil); len(ps) != 0 {
		t.Errorf("a 30m workflow on cloud-batch = %v, want none", ps)
	}
}

// TestValidateRefusesAMachineTypeThisBuildCannotPrice: the type a run's VM is
// is priced and sized from (design §5.2, §4.4 item 3), so a type the table
// does not carry can be neither. The refusal is reported once, at the config
// key that names it, rather than once per workflow.
//
// Mutation (drop the machine-type branch, or attach it to a workflow path):
// this fails.
func TestValidateRefusesAMachineTypeThisBuildCannotPrice(t *testing.T) {
	cfg := batchValidateConfig(t, batchValidateYAML)
	lc := batchValidateRig(t, "e2-micro", backend.CloudBatch)
	ps := batchValidateProblems(lc, cfg, nil)
	if len(ps) != 1 || ps[0].Path != "batch.machine_type" {
		t.Fatalf("an unpriceable machine type = %v, want one problem at batch.machine_type", ps)
	}
	if !strings.Contains(ps[0].Message, "e2-standard-4") {
		t.Errorf("the refusal does not list the types this build prices: %s", ps[0].Message)
	}
	// A config that names no type at all is the backend's own default, which
	// the table carries.
	if ps := batchValidateProblems(batchValidateRig(t, "", backend.CloudBatch), cfg, nil); len(ps) != 0 {
		t.Errorf("a batch block with no machine type = %v, want none (the backend defaults it)", ps)
	}
}

// TestValidateRefusesABackendNameWithNoOpener: design §4.4 item 1 -- the name
// is looked up among the names this build can open, with backend.Open's own
// "known backends: …" message. internal/localcfg refuses an unknown name
// first, so this is the second line of the same defence, and the one that
// stays true if that validation ever loosens.
//
// Mutation (drop the lookup): this fails; it is also how a name the opener map
// does not carry stops being silently treated as Cloud Run.
func TestValidateRefusesABackendNameWithNoOpener(t *testing.T) {
	lc := batchValidateRig(t, "", "cloud-foo")
	cfg := batchValidateConfig(t, batchValidateYAML)
	ps := batchValidateProblems(lc, cfg, nil)
	if len(ps) != 1 || ps[0].Path != "backend" {
		t.Fatalf("an unknown backend name = %v, want one problem at backend", ps)
	}
	if !strings.Contains(ps[0].Message, "known backends: "+backend.CloudBatch+", "+backend.CloudRun) {
		t.Errorf("the refusal does not list the known names as backend.Open does: %s", ps[0].Message)
	}
}

// TestComputeProblemsUsesTheBackendsOwnLimits: the resources of a workflow are
// checked against the backend the installation selects, and the two backends
// have different answers (design §4.4 item 3). 8 CPUs are a Cloud Run job
// task's; on e2-standard-4 they are more than the share of the machine type a
// Batch task gets, so the same fugaro.yaml is refused on one and accepted on
// the other.
//
// Mutation (call gcp.CheckResources for both, or batch.CheckResources for
// both): one of the two assertions fails.
func TestComputeProblemsUsesTheBackendsOwnLimits(t *testing.T) {
	cfg := batchValidateConfig(t, `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 8, memory: 32Gi }
`)
	cr := computeProblems(cfg, batchValidateRig(t, "", backend.CloudRun))
	if len(cr) != 0 {
		t.Errorf("8 CPUs/32Gi is a Cloud Run job task and is refused there: %v", cr)
	}
	bc := computeProblems(cfg, batchValidateRig(t, "e2-standard-4", backend.CloudBatch))
	if len(bc) == 0 || bc[0].Path != "workflows.web.resources.cpu" {
		t.Fatalf("8 CPUs on an e2-standard-4 = %v, want one problem at workflows.web.resources.cpu", bc)
	}
	// No project config at all is Cloud Run's answer, not a Batch one: the
	// backend is a fact about the installation (design §4.1).
	if ps := computeProblems(cfg, nil); len(ps) != 0 {
		t.Errorf("with no project config the resources are checked against the VM's limits: %v", ps)
	}
}
