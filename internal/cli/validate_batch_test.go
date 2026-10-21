package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
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
	return batchValidateRigWith(t, machineType, backendName, "")
}

// batchValidateRigWith is the same rig with extra lines inside the batch block
// (the task timeout), for a case that is about a field of the block.
func batchValidateRigWith(t *testing.T, machineType, backendName, extra string) *localcfg.Config {
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
	yaml += extra
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
}

// TestValidateRefusesAnUnreadableRecipeOnCloudBatch is the same boundary
// failing closed offline: the check reads the checkout's own recipe
// (localRecipe), and a recipe it cannot resolve at all is not one it may pass.
// nil reads as "not an artifact run" to the mode check, while the runs of this
// installation may be resolving a mode: artifact recipe at their base, so the
// installation is refused instead -- the launch's gate refuses the same shape
// (artifactUnreadOnBatchRefusal, artifactShadowedOnBatchRefusal).
//
// Mutation (drop the nil arm): this fails, and the boundary passes whatever it
// could not read.
func TestValidateRefusesAnUnreadableRecipeOnCloudBatch(t *testing.T) {
	cfg := batchValidateConfig(t, batchValidateYAML)
	ps := batchValidateProblems(batchValidateRig(t, "e2-standard-4", backend.CloudBatch), cfg, nil)
	if len(ps) != 1 || ps[0].Message != artifactUnreadOfflineRefusal {
		t.Fatalf("an unresolved recipe on cloud-batch = %v, want the one fail-closed refusal", ps)
	}
	if ps[0].Path != "backend" {
		t.Errorf("the refusal is at %q, want backend, where the boundary's other refusal is", ps[0].Path)
	}
	for _, want := range []string{"docs/design/artifact-runs.md", "§8.5", "D11"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal does not name %q: %s", want, ps[0].Message)
		}
	}
	// Cloud Run: the boundary is not this backend's, and an unresolved recipe
	// is not refused there (the run resolves it itself).
	if ps := batchValidateProblems(batchValidateRig(t, "", backend.CloudRun), cfg, nil); len(ps) != 0 {
		t.Errorf("an unresolved recipe on cloud-run is refused by the Batch check: %v", ps)
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

// TestValidateRefusesATaskTimeoutPastTheRunningCeiling: the job's
// maxRunDuration is the run's total plus backend.TaskTimeoutSlack, and Batch
// fails a task after 14 days RUNNING (design §1.3, §9). The total a run
// reaches that field with on this backend is localcfg.BatchTaskTimeout --
// batchTimeouts.TaskTimeout answers every workflow with it, and a
// --total-timeout launch is capped at backend.MaxRunTime -- so
// batch.task_timeout is what validate bounds. The field's own validation is
// only "greater than zero" (internal/localcfg), which is why a value past the
// ceiling would otherwise reach the create.
//
// Mutation (drop the check, drop the slack, or bound the workflows'
// timeouts.total instead -- which no launch sends in this build): the refusal
// case fails, or the case that a long workflow is accepted fires.
func TestValidateRefusesATaskTimeoutPastTheRunningCeiling(t *testing.T) {
	lc := batchValidateRigWith(t, "e2-standard-4", backend.CloudBatch, "  task_timeout: 360h\n")
	// An implementing recipe, so these cases are about the timeout field and
	// not about the boundary's fail-closed arm (an unresolved recipe is its own
	// case, TestValidateRefusesAnUnreadableRecipeOnCloudBatch).
	impl := &recipe.Recipe{Name: "impl", Mode: recipe.ModeImplement}
	ps := batchValidateProblems(lc, batchValidateConfig(t, batchValidateYAML), impl)
	if len(ps) != 1 || ps[0].Path != "batch.task_timeout" {
		t.Fatalf("batch.task_timeout: 360h on cloud-batch = %v, want one problem at batch.task_timeout", ps)
	}
	for _, want := range []string{"14 days", "batch.task_timeout", "Batch refuses"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the timeout refusal does not say %q: %s", want, ps[0].Message)
		}
	}
	// The same installation on Cloud Run is not this check's business.
	cr := batchValidateRigWith(t, "", backend.CloudRun, "  task_timeout: 360h\n")
	if ps := batchValidateProblems(cr, batchValidateConfig(t, batchValidateYAML), impl); len(ps) != 0 {
		t.Errorf("a 360h task timeout on cloud-run is refused by the Batch check: %v", ps)
	}
	// A block that names no task timeout is backend.MaxRunTime, which is inside
	// the ceiling, and so is the longest value the field can carry under it.
	inside := batchValidateRigWith(t, "e2-standard-4", backend.CloudBatch, "  task_timeout: 335h\n")
	if ps := batchValidateProblems(inside, batchValidateConfig(t, batchValidateYAML), impl); len(ps) != 0 {
		t.Errorf("a 335h task timeout on cloud-batch = %v, want none", ps)
	}
	// The slack is real and the bound is the field's, not the ceiling's: the
	// job's maxRunDuration is task_timeout + TaskTimeoutSlack, so 335h59m is
	// 14 days and a minute of maxRunDuration — refused — while 335h58m is
	// exactly 14 days, which is the last value that fits. A check that compared
	// the field against batch.MaxRunDuration itself would let 335h59m through
	// (mutation: drop the subtraction).
	over := batchValidateRigWith(t, "e2-standard-4", backend.CloudBatch, "  task_timeout: 335h59m\n")
	if ps := batchValidateProblems(over, batchValidateConfig(t, batchValidateYAML), impl); len(ps) != 1 || ps[0].Path != "batch.task_timeout" {
		t.Errorf("a 335h59m task timeout on cloud-batch = %v, want one problem at batch.task_timeout (the slack puts its maxRunDuration past 14 days)", ps)
	}
	edge := batchValidateRigWith(t, "e2-standard-4", backend.CloudBatch, "  task_timeout: 335h58m\n")
	if ps := batchValidateProblems(edge, batchValidateConfig(t, batchValidateYAML), impl); len(ps) != 0 {
		t.Errorf("a 335h58m task timeout on cloud-batch = %v, want none (its maxRunDuration is exactly 14 days)", ps)
	}
	if ps := batchValidateProblems(batchValidateRig(t, "e2-standard-4", backend.CloudBatch), batchValidateConfig(t, batchValidateYAML), impl); len(ps) != 0 {
		t.Errorf("the block's own default (backend.MaxRunTime) = %v, want none", ps)
	}
	// And a workflow's own clock is not what reaches the field in this build:
	// a 360h workflow is a config the launch answers with batch.task_timeout,
	// so the check that used to refuse it here does not (that bound belongs to
	// the enable task, which is what reads the workflow's clock).
	long := batchValidateConfig(t, `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 2, memory: 4Gi }
    timeouts: { total: 360h }
`)
	if ps := batchValidateProblems(batchValidateRig(t, "e2-standard-4", backend.CloudBatch), long, impl); len(ps) != 0 {
		t.Errorf("a 360h workflow clock is refused by the Batch timeout check, which bounds batch.task_timeout instead: %v", ps)
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
	ps := batchValidateProblems(lc, cfg, &recipe.Recipe{Name: "impl", Mode: recipe.ModeImplement})
	if len(ps) != 1 || ps[0].Path != "batch.machine_type" {
		t.Fatalf("an unpriceable machine type = %v, want one problem at batch.machine_type", ps)
	}
	if !strings.Contains(ps[0].Message, "e2-standard-4") {
		t.Errorf("the refusal does not list the types this build prices: %s", ps[0].Message)
	}
	// A config that names no type at all is the backend's own default, which
	// the table carries.
	if ps := batchValidateProblems(batchValidateRig(t, "", backend.CloudBatch), cfg, &recipe.Recipe{Name: "impl", Mode: recipe.ModeImplement}); len(ps) != 0 {
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

// TestComputeProblemsJudgeOnlyTheFilesOwnProject: the backend, the machine
// type a VM is and the size a workflow may ask for are facts about the
// installation the FILE's project names, so a project config selected for
// another project judges nothing here. It is the guard budgetProblems,
// providerProblems and cascadeProblems all carry, and the shape `fugaro
// validate` in project A's checkout hands these checks for a file that names
// project B: without it the file is refused on limits that are not its
// installation's (the one call site, computeCheckoutProblems).
//
// Mutation (drop the `lc != nil && lc.Name != cfg.Project` early return): the
// foreign-project cases below fail.
func TestComputeProblemsJudgeOnlyTheFilesOwnProject(t *testing.T) {
	// One config per backend, each refused on that backend and accepted on the
	// other, so the "own project" arm is a case the check really does fire on:
	// 8 CPUs/32Gi is a Cloud Run job task and more than an e2-standard-4's task
	// share (TestComputeProblemsUsesTheBackendsOwnLimits), and 3 CPUs is not
	// one of Cloud Run's sizes while being inside an e2-standard-8's share.
	const (
		batchRefused = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 8, memory: 32Gi }
`
		cloudRunRefused = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    commands: { build: "true", test: "true" }
    resources: { cpu: 3, memory: 4Gi }
`
	)
	for _, tc := range []struct{ backendName, machineType, yaml string }{
		{backend.CloudBatch, "e2-standard-4", batchRefused},
		{backend.CloudRun, "", cloudRunRefused},
	} {
		cfg := batchValidateConfig(t, tc.yaml)
		if ps := computeProblems(cfg, batchValidateRig(t, tc.machineType, tc.backendName)); len(ps) == 0 {
			t.Fatalf("backend %q: the rig's own project accepts these resources, so this case proves nothing", tc.backendName)
		}
		foreign := batchValidateRig(t, tc.machineType, tc.backendName)
		foreign.Name = "somewhere-else"
		if ps := computeProblems(cfg, foreign); len(ps) != 0 {
			t.Errorf("backend %q: a config selected for another project judged this file: %v", tc.backendName, ps)
		}
	}
}

// TestBatchValidateProblemsJudgeOnlyTheFilesOwnProject is the same guard on the
// Batch checks: the machine type, the task timeout and the artifact boundary
// are all read from the installation the file's project names, so another
// project's config -- which is to say, no installation of this file's at all,
// the same state as a nil lc -- refuses nothing here.
//
// Mutation (drop the `lc.Name != cfg.Project` half of the early return): the
// foreign-project cases below fail.
func TestBatchValidateProblemsJudgeOnlyTheFilesOwnProject(t *testing.T) {
	cfg := batchValidateConfig(t, batchValidateYAML)
	impl := &recipe.Recipe{Name: "impl", Mode: recipe.ModeImplement}
	art := &recipe.Recipe{Name: "deliver", Mode: recipe.ModeArtifact}
	// A machine type this build cannot price, so the check has something to say
	// about the rig's own project.
	own := batchValidateRig(t, "e2-micro", backend.CloudBatch)
	if ps := batchValidateProblems(own, cfg, impl); len(ps) == 0 {
		t.Fatal("the rig's own project is not refused for an unpriceable machine type, so this case proves nothing")
	}
	foreign := batchValidateRig(t, "e2-micro", backend.CloudBatch)
	foreign.Name = "somewhere-else"
	for name, rcp := range map[string]*recipe.Recipe{"an implement recipe": impl, "the artifact boundary": art} {
		if ps := batchValidateProblems(foreign, cfg, rcp); len(ps) != 0 {
			t.Errorf("%s: a config selected for another project judged this file: %v", name, ps)
		}
	}
	// And the unreadable-recipe arm is the nil-config arm, which is the same
	// state: no installation of this file's to fail closed against.
	if ps := batchValidateProblems(foreign, cfg, nil); len(ps) != 0 {
		t.Errorf("a config selected for another project refused an unresolved recipe: %v", ps)
	}
}

// TestTheCheckoutReadRunsTheBatchChecksToo: a resolved checkout is read
// through loadCheckoutResolved by `fugaro image refresh`, image build and the
// layer commands, and that read called computeProblems alone. computeProblems
// drops the one Issue batch.CheckResources answers with no Field -- the
// machine type the price table does not carry, a fact about the installation's
// batch block rather than about a workflow -- on the premise that
// batchValidateProblems reports it once, at batch.machine_type. The premise
// holds only where BOTH are called, which is what `fugaro validate` does and
// the checkout read did not: so an installation whose machine type cannot be
// priced was told nothing at all, and no per-workflow resource check ran
// either (CheckResources cannot size a VM it cannot price, so it returns that
// one issue and nothing else). Unreachable today only because
// internal/localcfg refuses backend: cloud-batch; the enable task makes it
// reachable, which is why it is closed here.
//
// Mutation (drop the batchValidateProblems half from the shared helper): this
// fails, and the checkout read reports nothing again.
func TestTheCheckoutReadRunsTheBatchChecksToo(t *testing.T) {
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte(batchValidateYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	// e2-standard-64 is a machine type localcfg accepts and the price table
	// does not carry, which is the shape CheckResources answers with no Field.
	lc := batchValidateRig(t, "e2-standard-64", backend.CloudBatch)
	_, _, err := loadCheckoutResolved(t.Context(), root, lc, layerOptions{NoBucket: true, Lenient: true})
	if err == nil {
		t.Fatal("the checkout read accepted a fugaro.yaml on an installation whose machine type the price table does not carry")
	}
	if !strings.Contains(err.Error(), "batch.machine_type") {
		t.Errorf("the checkout read refused with %q, which does not name batch.machine_type: the problem computeProblems drops for the caller that also runs batchValidateProblems has no second home in this one", err)
	}
}

// TestOnlyTheSharedHelperCallsTheBackendsChecks holds the premise the test
// above is about structurally: every offline check of a checkout's fugaro.yaml
// is taken through computeCheckoutProblems, so no caller can take
// computeProblems without batchValidateProblems again. A new call site that
// reaches for one of the two directly fails here rather than in production,
// where the symptom is an installation told nothing about a machine type the
// price table does not carry.
//
// Mutation (add a direct computeProblems call in another command file): this
// fails naming the file.
func TestOnlyTheSharedHelperCallsTheBackendsChecks(t *testing.T) {
	files, err := filepath.Glob("../../internal/cli/*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The declarations are not calls, and the files that hold them: stripping
	// them first is what makes the count below a count of call sites.
	decls := []string{"func computeProblems(", "func batchValidateProblems("}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "compute.go") {
			// compute.go is the one home of both calls (computeCheckoutProblems
			// is the pair's single call site), and its two declarations are
			// there besides.
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Join(strings.Fields(string(data)), " ")
		for _, d := range decls {
			code = strings.ReplaceAll(code, d, "func <decl>(")
		}
		for _, name := range []string{"computeProblems(", "batchValidateProblems("} {
			if strings.Contains(code, name) {
				t.Errorf("%s calls %s directly: every offline check of a checkout is taken through computeCheckoutProblems, because computeProblems drops the machine type's own problem on the premise that batchValidateProblems reports it at that caller -- a caller that takes one half reports nothing for an installation whose machine type the price table does not carry", path, strings.TrimSuffix(name, "("))
			}
		}
	}
}
