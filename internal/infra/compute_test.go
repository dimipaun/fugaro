package infra

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// The compute root's contract (design compute-backend §8.1 task 6). The root
// is applied by a stage of its own; these tests hold the Go side of that
// interface to the HCL side, so the two cannot drift apart silently.

// computeSpec is a complete spec: every input the root requires.
func computeSpec() ComputeSpec {
	return ComputeSpec{
		Project:            "proj-1234",
		ProjectNumber:      "123456789",
		Region:             "us-east5",
		Network:            DefaultNetwork("proj-1234"),
		Subnetwork:         DefaultSubnetwork("proj-1234", "us-east5"),
		Names:              ComputeNamesFor(),
		ManageAPIs:         true,
		Launchers:          []string{"user:owner@example.com"},
		Operators:          []string{},
		RunAccounts:        []string{"serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"},
		RegistryRepository: GhcrRegistry,
		BaseImage:          "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3",
		BaseProject:        "batch-custom-image",
		BaseFamily:         "batch-cos-stable-official",
		PreloadZone:        "us-east5-a",
		PreloadSchedule:    "0 3 * * 1",
		PreloadRefreshDays: PreloadRefreshDaysDefault,
		BuilderMachineType: "e2-medium",
		BuildSubmitterRole: "projects/proj-1234/roles/fugaroBuildSubmitter",
	}
}

// declaredVariables are the variables a root's variables.tf declares.
func declaredVariables(t *testing.T, path string) []string {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^variable\s+"([a-z0-9_]+)"\s*\{`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s declares no variables", path)
	}
	slices.Sort(out)
	return out
}

// REGRESSION (the tfvars interface): the keys ComputeVars writes are exactly
// the variables the compute root declares, no more and no fewer. A variable
// added to the root and not fed from Go fails here rather than in a plan that
// would silently take its default, and a Go field the root no longer declares
// fails too.
func TestComputeVarsMatchRootVariables(t *testing.T) {
	raw, err := ComputeVars(computeSpec())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range doc {
		got = append(got, k)
	}
	slices.Sort(got)
	want := declaredVariables(t, "gcp/roots/compute/variables.tf")
	if !slices.Equal(got, want) {
		t.Errorf("tfvars keys = %v, the root declares %v", got, want)
	}
}

// A spec that could only produce a failing apply fails here instead: no run
// account, a network or subnetwork left out (Batch refuses noExternalIpAddress
// without both), no repository to read the pinned image from, no
// build-submitter role.
func TestComputeVarsRefuseWhatTheModuleRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(s *ComputeSpec)
		want string
	}{
		{"no project", func(s *ComputeSpec) { s.Project = "" }, "project"},
		{"no region", func(s *ComputeSpec) { s.Region = "" }, "region"},
		{"no network", func(s *ComputeSpec) { s.Network = "" }, "network"},
		{"no subnetwork", func(s *ComputeSpec) { s.Subnetwork = "" }, "subnetwork"},
		{"no run accounts", func(s *ComputeSpec) { s.RunAccounts = nil }, "run account"},
		{"no repository", func(s *ComputeSpec) { s.RegistryRepository = "" }, "repository"},
		{"no base image", func(s *ComputeSpec) { s.BaseImage = "" }, "base image"},
		{"no build-submitter role", func(s *ComputeSpec) { s.BuildSubmitterRole = "" }, "build-submitter"},
		// The never-touch list is the reaper's, and it is a list of PROJECT
		// IDS: an entry the reaper could not read is one it could not refuse,
		// so the project it names would be swept as if the list did not exist.
		{"a never-touch entry that is not a project id", func(s *ComputeSpec) { s.ReaperRefuseProjects = []string{"acme-prod", "not a project"} }, "never-touch"},
		{"no refresh window", func(s *ComputeSpec) { s.PreloadRefreshDays = 0 }, "refresh window"},
		// A window at or past the lifetime is refused: Batch documents a
		// custom image's support as ending 60 days after its starting date,
		// so an image replaced only after that has been unsupported first.
		{"a refresh window at the lifetime", func(s *ComputeSpec) { s.PreloadRefreshDays = 60 }, "refresh window"},
		{"a refresh window past the lifetime", func(s *ComputeSpec) { s.PreloadRefreshDays = 90 }, "refresh window"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := computeSpec()
			c.mut(&s)
			if _, err := ComputeVars(s); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ComputeVars = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// The reaper's never-touch list reaches the root as a JSON list whatever the
// caller has to say: a spec that names none writes `[]` and not `null` (the
// variable is a list(string) whose default is the empty list, and a tfvars
// written to be read by hand should not depend on terraform's null-means-
// default rule), and a spec that names some writes them through in order --
// the order the module joins them in for FUGARO_REAPER_REFUSE_PROJECTS.
func TestComputeVarsWriteTheNeverTouchList(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"none named", nil, []string{}},
		{"the caller's own", []string{"acme-prod", "acme-staging"}, []string{"acme-prod", "acme-staging"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := computeSpec()
			s.ReaperRefuseProjects = c.in
			raw, err := ComputeVars(s)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			field := doc["reaper_refuse_projects"]
			if strings.TrimSpace(string(field)) == "null" {
				t.Fatalf("reaper_refuse_projects is null: the variable is a list(string), and the tfvars is written to be read by hand as well as by terraform")
			}
			var got []string
			if err := json.Unmarshal(field, &got); err != nil {
				t.Fatalf("reaper_refuse_projects = %s: %v", field, err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("reaper_refuse_projects = %q, want %q", got, c.want)
			}
		})
	}
}

// The run accounts the module is given must not be the default Compute Engine
// account: the design forbids it (§3.2), and an empty set is refused above, so
// what is left to check here is that the spec's own default is no account at
// all — a caller has to say which identities run.
func TestComputeSpecRunsAsNamedAccountsOnly(t *testing.T) {
	if s := computeSpec(); len(s.RunAccounts) == 0 {
		t.Fatal("the fixture names no run account")
	}
	var zero ComputeSpec
	if len(zero.RunAccounts) != 0 || zero.Names.ReaperAccountID != "" {
		t.Error("an unset spec names identities")
	}
}

// The compute root has a workdir of its own, beside the installation's, and
// it is a root PrepareWorkdir knows: the installation's tfvars, imports and
// saved plan must never be rewritten by this root's run.
func TestComputeWorkdirIsItsOwn(t *testing.T) {
	env := func(k string) string { return map[string]string{"HOME": "/home/x"}[k] }
	inst, err := InstallationWorkdir(env, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ComputeWorkdir(env, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	if dir == inst || filepath.Dir(dir) != filepath.Dir(inst) {
		t.Errorf("compute workdir %s is not a sibling of %s", dir, inst)
	}
	if StatePrefixCompute == StatePrefixInstallation || !strings.HasPrefix(StatePrefixCompute, "fugaro/") {
		t.Errorf("state prefix %q is not the root's own", StatePrefixCompute)
	}
	if _, err := ComputeWorkdir(env, "not a project"); err == nil {
		t.Error("a bad project id was accepted")
	}
	wd, err := PrepareWorkdir(filepath.Join(t.TempDir(), "compute"), "compute")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(wd.Root, filepath.Join("gcp", "roots", "compute")) {
		t.Errorf("PrepareWorkdir(compute) root = %s", wd.Root)
	}
}

// The refresh window is the one thing that keeps the preloaded boot image
// inside the lifetime Batch documents for a custom image -- 60 days after its
// starting date, whichever base it was built from (§1.2, C19) -- because the
// image's name carries only the pin's hash and does not move when Google's
// family rolls. So the default the stage sends is inside the lifetime, the
// root and the module default to the same number, and the window reaches the
// build's own script (the module's env, preload.tf).
func TestPreloadRefreshWindowIsInsideTheSupportedLifetime(t *testing.T) {
	if PreloadRefreshDaysDefault <= 0 || PreloadRefreshDaysDefault >= 60 {
		t.Errorf("PreloadRefreshDaysDefault = %d, want 1-59 days: Batch ends a custom image's support at 60 days", PreloadRefreshDaysDefault)
	}
	for _, path := range []string{"gcp/roots/compute/variables.tf", "gcp/modules/compute/variables.tf"} {
		b, err := fs.ReadFile(terraform.FS, path)
		if err != nil {
			t.Fatal(err)
		}
		blk := regexp.MustCompile(`(?s)variable "preload_refresh_days" \{(.*?)\n\}`).FindStringSubmatch(string(b))
		if blk == nil {
			t.Errorf("%s does not declare preload_refresh_days: the value the Go side sends would be a variable the root does not take", path)
			continue
		}
		if !regexp.MustCompile(`(?m)^\s*default\s*=\s*` + strconv.Itoa(PreloadRefreshDaysDefault) + `\s*$`).MatchString(blk[1]) {
			t.Errorf("%s's default is not the %d the stage sends: an apply by hand would build a different schedule of replacements", path, PreloadRefreshDaysDefault)
		}
	}
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "REFRESH_DAYS=${var.preload_refresh_days}") {
		t.Error("the build's environment does not carry the refresh window to preload.sh")
	}
}

// The preloaded image's name is the module's: one image per pinned base, and
// the Go side derives the same name the module does, so a caller can name the
// image a run boots without reading a state. The module's expression is
// asserted here too, because the two must stay the same function.
func TestPreloadedImageMatchesTheModule(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	tf := string(b)
	if !strings.Contains(tf, "substr(sha256(var.base_image), 0, 8)") {
		t.Error("the module no longer derives the image's name from the pinned base's hash: PreloadedImage would be wrong")
	}
	one := PreloadedImage("ghcr.io/acme/base:1")
	if one == PreloadedImage("ghcr.io/acme/base:2") {
		t.Error("two pins share one image name: a stale image would serve a new pin")
	}
	if !strings.HasPrefix(one, PreloadImage+"-") || len(one) != len(PreloadImage)+9 {
		t.Errorf("PreloadedImage = %q, want %s-<8 hex>", one, PreloadImage)
	}
	// The exact value, so a change to the derivation is a visible one.
	if got, want := one, "fugaro-preload-"+hash8("ghcr.io/acme/base:1"); got != want {
		t.Errorf("PreloadedImage = %q, want %q", got, want)
	}
}

// hash8 is the fixture's own sha256 prefix, computed the long way so the test
// does not restate the implementation it checks.
func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])
	return h[:8]
}

// The launcher's Condition and the reaper's sweep are both defined over the
// job prefix, and the module takes it as an input rather than writing it out:
// a prefix that drifted between the two would leave the launcher unable to
// create a job the reaper is looking for.
func TestJobPrefixIsTheModulesInput(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "jobs/${var.names.job_prefix}") {
		t.Error("the launcher's Condition no longer takes the job prefix from names.job_prefix")
	}
	if JobPrefix != "fugaro-" {
		t.Errorf("JobPrefix = %q: the job names are fugaro-<slug>-<workflow>-<hash> (internal/backend/batch/names.go)", JobPrefix)
	}
}

// The preload rebuild's start is retried while the scheduler agent's binding
// on the preload account propagates (57a's measurement: a binding created
// moments before is not readable everywhere at once), and a refusal that
// outlasts the retries is returned as it is, so the caller can name the one
// command that starts the job by hand. A job that is not there is NOT
// retried: it is a 404, not a propagated 403. What IS retried is any 403
// whatever its wording — see the permission-worded case at the end, which is
// the shape a permanent denial comes in.
func TestRunSchedulerJobRetriesPropagation(t *testing.T) {
	restore := runJobRetries
	runJobRetries = []time.Duration{0, 0}
	t.Cleanup(func() { runJobRetries = restore })
	ctx := context.Background()
	f := newCloud(t)
	f.sched.SetJob("proj-1234", "us-east5", PreloadSchedulerJob, "https://cloudbuild.googleapis.com/v1/projects/proj-1234/builds", "fugaro-preload@proj-1234.iam.gserviceaccount.com")

	f.sched.RefuseRuns = 2
	if err := RunSchedulerJob(ctx, f.c, "proj-1234", "us-east5", PreloadSchedulerJob); err != nil {
		t.Fatalf("two refusals then a run: %v", err)
	}
	if runs := f.sched.Runs(); len(runs) != 1 {
		t.Errorf("the job ran %d times, want once: %v", len(runs), runs)
	}

	f.sched.RefuseRuns = len(runJobRetries) + 1
	err := RunSchedulerJob(ctx, f.c, "proj-1234", "us-east5", PreloadSchedulerJob)
	if err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatalf("a run that stays refused: %v", err)
	}

	// The retry is keyed on the STATUS, not on the refusal's wording: a
	// permanent denial -- the caller does not hold cloudscheduler.jobs.run,
	// worded nothing like a propagated one -- is retried through the same
	// ladder and then reported as the API worded it. That is deliberate
	// (infra.propagating): nothing in a 403 tells the two apart reliably, and
	// not retrying a real propagation gap would fail a fresh installation's
	// first rebuild. The cost is bounded and paid once per installation.
	f.sched.RefuseRunMessage = "Permission 'cloudscheduler.jobs.run' denied on resource 'projects/proj-1234/locations/us-east5/jobs/fugaro-preload' (or it may not exist)."
	f.sched.RefuseRuns = len(runJobRetries)
	if err := RunSchedulerJob(ctx, f.c, "proj-1234", "us-east5", PreloadSchedulerJob); err != nil {
		t.Fatalf("a permission-worded 403 was not retried like a propagated one: %v", err)
	}
	// And one that outlasts the ladder surfaces with its OWN wording, not with
	// a retry-shaped excuse.
	f.sched.RefuseRuns = len(runJobRetries) + 1
	err = RunSchedulerJob(ctx, f.c, "proj-1234", "us-east5", PreloadSchedulerJob)
	if err == nil || !strings.Contains(err.Error(), "cloudscheduler.jobs.run") {
		t.Fatalf("the permanent refusal's own wording was not what surfaced: %v", err)
	}
	f.sched.RefuseRunMessage = ""
	// A job that does not exist fails at once: retrying a 404 would only
	// delay the real answer.
	if err := RunSchedulerJob(ctx, f.c, "proj-1234", "us-east5", "no-such-job"); err == nil {
		t.Error("a run of a job that is not there succeeded")
	}
}

// JobRunAccounts finds the installation's run-class accounts by their
// display-name mark and nothing else: they are what a job's VM runs as, and
// an account left out is a run that can never report its state. Another
// account in the project, however it is named, is never one.
func TestJobRunAccountsReadsTheJobMark(t *testing.T) {
	f := newCloud(t)
	f.crm.AddProject("proj-1234", testProjectNumber)
	f.iam.AddServiceAccount("proj-1234", "fugaro-acme-web@proj-1234.iam.gserviceaccount.com", "Fugaro job acme/sandbox web")
	f.iam.AddServiceAccount("proj-1234", "fugaro-acme-web-m4@proj-1234.iam.gserviceaccount.com", "Fugaro M4 job acme/sandbox web")
	f.iam.AddServiceAccount("proj-1234", "fugaro-reaper@proj-1234.iam.gserviceaccount.com", "Fugaro reaper")
	f.iam.AddServiceAccount("proj-1234", "123456789012-compute@developer.gserviceaccount.com", "Compute Engine default service account")

	got, err := JobRunAccounts(context.Background(), f.c, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"serviceAccount:fugaro-acme-web-m4@proj-1234.iam.gserviceaccount.com",
		"serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com",
	}
	if !slices.Equal(got, want) {
		t.Errorf("JobRunAccounts = %q, want %q", got, want)
	}
	// The reaper is not a run account: the design's §3.7 gives it exactly five
	// permissions over jobs and instances (Task 9), and a VM running as it
	// could do neither of the things a run must.
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, ReaperAccountID) }) {
		t.Errorf("the reaper's own account was returned as a run account: %q", got)
	}
}

// REGRESSION (the outputs interface, the mirror of the tfvars one): every
// output the compute root declares is one DecodeComputeOutputs reads, and
// vice versa. An output added to the root and not to the struct is a name the
// stage and a later doctor check would have to re-derive from the state, and
// a field with no output behind it would read as empty forever.
//
// It reads the ROOT's file, the way TestComputeVarsMatchRootVariables reads
// the root's variables.tf: `terraform output -json` in the compute workdir is
// the root's answer, so an output that stops at the module is a name the
// stage never sees. That the root passes the module's set through unchanged is
// the second half of the test.
func TestComputeOutputsCoverTheRoot(t *testing.T) {
	declared := declaredOutputs(t, "gcp/roots/compute/outputs.tf")
	if len(declared) == 0 {
		t.Fatal("the compute root declares no outputs")
	}
	var read []string
	typ := reflect.TypeFor[ComputeOutputs]()
	for i := range typ.NumField() {
		read = append(read, typ.Field(i).Tag.Get("json"))
	}
	slices.Sort(read)
	if !slices.Equal(declared, read) {
		t.Errorf("the compute root's outputs are %q, ComputeOutputs reads %q", declared, read)
	}
	if module := declaredOutputs(t, "gcp/modules/compute/outputs.tf"); !slices.Equal(module, declared) {
		t.Errorf("the module's outputs are %q and the root passes through %q: a root output is the module's, one for one", module, declared)
	}
}

// declaredOutputs are the output names one of the compute tree's files
// declares, sorted.
func declaredOutputs(t *testing.T, path string) []string {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^output "([a-z0-9_]+)"\s*\{`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// DecodeComputeOutputs reads the root's own names and is not confused by one
// it does not know: terraform's `output -json` wraps each value, and a value
// the struct has no field for is skipped rather than fatal.
func TestDecodeComputeOutputs(t *testing.T) {
	raw := map[string]json.RawMessage{
		"preload_image": json.RawMessage(`"fugaro-preload-e3b0c442"`),
		"nat":           json.RawMessage(`"fugaro-nat"`),
		"some_future":   json.RawMessage(`{"nested":true}`),
		"reaper_role":   json.RawMessage(`null`),
	}
	got, err := DecodeComputeOutputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreloadImage != "fugaro-preload-e3b0c442" || got.NAT != "fugaro-nat" || got.ReaperRole != "" {
		t.Errorf("DecodeComputeOutputs = %+v", got)
	}
	if _, err := DecodeComputeOutputs(nil); !errors.Is(err, ErrNoOutputs) {
		t.Errorf("no outputs: err = %v, want ErrNoOutputs", err)
	}
	if _, err := DecodeComputeOutputs(map[string]json.RawMessage{"nat": json.RawMessage(`7`)}); err == nil {
		t.Error("a number where a name belongs was accepted")
	}
}
