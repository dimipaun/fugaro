package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// appSlug is the storage slug of the fixture's repository, GitHub's acme/app.
var appSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// cloudFixture is a local config pointing at fakes and a file:// bucket.
type cloudFixture struct {
	dir     string
	bucket  string // file:// URL
	run     *gcpfake.Run
	logging *gcpfake.Logging
	// sm is the Secret Manager fake a rig that needs one wires in (the
	// image build rig: a standalone build resolves the provider
	// credential's live ID before submitting, B1). Nil for a rig with no
	// secret_manager endpoint, which is every other cloudFixture -- and
	// deliberately so for the test that proves a command needs none.
	sm *gcpfake.Secrets
}

// newCloudFixture writes a local config whose endpoints point at the Run and
// Logging fakes, plus any extra "key: url" endpoint entries (secret_manager,
// cloud_build) a later test adds with its own fake.
func newCloudFixture(t *testing.T, extraEndpoints ...string) *cloudFixture {
	t.Helper()
	f := &cloudFixture{dir: t.TempDir(), run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	// Executions the fake starts live where the config's backend looks.
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + f.logging.URL + "/, " + extra(extraEndpoints) + "no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	// The project config is aurora's, in a config directory of the test's
	// own; FUGARO_CONFIG names it too, as tests append to it.
	path := isolateProjects(t, f.dir)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.run.AddJob(gcp.JobName(appSlug, "web"), "4", "8Gi")
	// The installation names its project in the runs bucket, as its
	// Terraform does; every cloud command checks it.
	f.writeMarker(t, "aurora", "proj-1234")
	return f
}

// markerPath is where the fixture's runs bucket keeps the project marker.
func (f *cloudFixture) markerPath() string {
	return filepath.Join(f.dir, "runs", filepath.FromSlash(infra.ProjectMarkerObject))
}

// writeMarker makes the fixture's installation name project name in GCP
// project gcpProject.
func (f *cloudFixture) writeMarker(t *testing.T, name, gcpProject string) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcpProject})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.markerPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.markerPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolateProjects points the project configs at a directory under dir,
// clears FUGARO_PROJECT and FUGARO_CONFIG, and returns where aurora's
// project config goes.
func isolateProjects(t *testing.T, dir string) string {
	t.Helper()
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// The project-name check caches its answer here, never in the
	// developer's own cache.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(projects, "aurora.yaml")
}

// appendConfig adds top-level YAML (such as registry: …) to the local config.
func (f *cloudFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setMaxParallel rewrites the fixture config's max_parallel from the default
// of 2 the fixture writers give it (newCloudFixture here, newBudgetFixture in
// budget_test.go): n >= 1 raises it, or lowers it for a test that means to
// trigger the cap, and 0 drops the line, the shape a machine whose config
// names no cap has — the config layer then resolves its own default (20). A
// dropped line is asked for with 0 rather than a value of its own because
// localcfg refuses max_parallel below 1, so no test can mean a cap of none.
func setMaxParallel(t *testing.T, n int) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := ""
	if n > 0 {
		replacement = fmt.Sprintf("max_parallel: %d\n", n)
	}
	updated := strings.Replace(string(data), "max_parallel: 2\n", replacement, 1)
	if updated == string(data) {
		t.Fatal("max_parallel: 2 not found in the fixture's config")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func extra(entries []string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e + ", ")
	}
	return b.String()
}

// memEnv builds a cloudEnv on a shared memblob bucket, for tests that call
// launchRun directly and concurrently (fileblob's IfNotExist is not atomic).
func memEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	return envOn(t, f, blobx.Wrap(memblob.OpenBucket(nil)))
}

// gcsEnv builds a cloudEnv on the GCS fake, so conditional writes take
// blobx's generation-matching path, as in production.
func gcsEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	return envOn(t, f, gcpfake.NewGCS(t).Bucket(t, "runs"))
}

func envOn(t *testing.T, f *cloudFixture, bucket *blobx.Bucket) *cloudEnv {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	be, err := gcp.New(context.Background(), gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region,
		Endpoints: gcp.Endpoints{Run: f.run.URL + "/", Logging: f.logging.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	return &cloudEnv{lc: lc, bucket: bucket, be: be}
}

func TestRepoFromOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		"git@github.com:acme/app.git":              "acme/app",
		"https://user:tok@bitbucket.org/acme/app/": "acme/app",
		"ssh://git@bitbucket.org:22/acme/app.git":  "acme/app",
		"https://host/deep/group/app":              "",
		"https://host/app":                         "",
		"not a url":                                "",
	} {
		if got, ok := repoFromOrigin(origin); got != want || ok != (want != "") {
			t.Errorf("repoFromOrigin(%q) = %q, %v; want %q", origin, got, ok, want)
		}
	}
}

func TestOriginRepo(t *testing.T) {
	testutil.IsolateGit(t)
	for origin, want := range map[string]string{
		"git@github.com:acme/app.git":          "acme/app",
		"https://user:tok@github.com/acme/app": "acme/app",
		"ssh://git@bitbucket.org:22/acme/app":  "acme/app",
	} {
		dir := t.TempDir()
		testutil.Git(t, dir, "init", "-q")
		testutil.Git(t, dir, "remote", "add", "origin", origin)
		t.Chdir(dir)
		if got, err := originRepo(context.Background()); err != nil || got != want {
			t.Errorf("originRepo(%s) = %q, %v", origin, got, err)
		}
	}
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://user:tok@host/deep/group/app")
	t.Chdir(dir)
	if _, err := originRepo(context.Background()); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "tok") {
		t.Errorf("nested origin: %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := originRepo(context.Background()); ExitCode(err) != ExitUserError {
		t.Errorf("no checkout: %v", err)
	}
}

func TestRemoteKeepsAnExistingCode(t *testing.T) {
	if ExitCode(remote(userErr("bad flag"))) != ExitUserError {
		t.Error("remote() overrode a user error")
	}
	if ExitCode(remote(os.ErrPermission)) != ExitRemoteError || remote(nil) != nil {
		t.Error("remote() did not mark a plain error as exit 2")
	}
}

// The cost estimate's prices are the region the jobs run in, after --region:
// a run with no execution at all is priced in the installation's own region.
func TestCloudPricesFollowTheRegion(t *testing.T) {
	newCloudFixture(t) // region us-east5
	if gcp.ListPrices("us-east5") == gcp.ListPrices("europe-west2") {
		t.Fatal("the two regions price the same, so the test cannot tell them apart")
	}
	for flag, region := range map[string]string{"": "us-east5", "europe-west2": "europe-west2"} {
		env, err := openCloud(context.Background(), cloudOptions{region: flag, stderr: discard})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := env.prices(nil)(nil), gcp.ListPrices(region); got != want {
			t.Errorf("--region %q: prices = %+v, want %s's %+v", flag, got, region, want)
		}
		env.Close()
	}
}

// A VM-backed execution is priced with Compute Engine's table for its own
// machine type and region, not with Cloud Run's jobs list price -- and one
// the table does not carry is left unestimated rather than priced with
// either (design compute-backend.md §5.2).
func TestCloudPricesUseTheVMsTableForAVMRun(t *testing.T) {
	newCloudFixture(t)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	var warned []string
	prices := env.prices(func(s string) { warned = append(warned, s) })
	vm := &backend.Execution{Name: "projects/p/locations/us-central1/jobs/fugaro-j-1", Region: "us-central1", MachineType: "e2-standard-4", CPU: 4, MemoryGiB: 16}
	got := prices(vm)
	want, err := batch.PricesFor("us-central1", "e2-standard-4")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("a VM run prices = %+v, want the VM table's %+v", got, want)
	}
	if got == gcp.ListPrices("us-central1") {
		t.Error("a VM run is priced with Cloud Run's table")
	}
	// The execution's own region decides, not the installation's.
	other := *vm
	other.Region = "europe-west2"
	if got := prices(&other); got != (backend.Prices{}) {
		t.Errorf("a VM in an unpriced region = %+v, want no prices (compute not estimated)", got)
	}
	unknown := *vm
	unknown.MachineType = "n2-standard-4"
	if got := prices(&unknown); got != (backend.Prices{}) {
		t.Errorf("a VM of an unknown machine type = %+v, want no prices (compute not estimated)", got)
	}
	// Neither refusal is silent, and neither is said twice: a listing of
	// rows that all read "compute not estimated" has to say why (design
	// compute-backend.md §5.2). The second refusal of the same reason is
	// quiet, so a hundred unpriced runs are not a hundred lines.
	if len(warned) != 2 {
		t.Fatalf("warnings = %q, want one per refusal (two)", warned)
	}
	for i, want := range []string{"europe-west2", "n2-standard-4"} {
		if !strings.Contains(warned[i], want) {
			t.Errorf("warning %d = %q, want it to name %q", i, warned[i], want)
		}
	}
	if got := prices(&other); got != (backend.Prices{}) || len(warned) != 2 {
		t.Errorf("a repeated refusal warned again: %q", warned)
	}
}

// A local compute_prices override wins on a VM run too, but it does not turn
// the run into a Cloud Run job: the machine type's quantities still decide
// the figure and the line still names the disk and NAT the figure leaves out
// (design §5.2, C17). Without this, an overridden price on a VM run would
// bill the workflow's resources -- which Batch reports as nothing at all --
// and its source line would stop naming what the figure excludes.
func TestCloudPricesOverrideOnAVMRunKeepsTheMachineTypesQuantities(t *testing.T) {
	newCloudFixture(t)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	env.lc.ComputePrices = map[string]localcfg.Price{"us-central1": {VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6}}
	vm := &backend.Execution{Name: "projects/p/locations/us-central1/jobs/fugaro-j-1", Region: "us-central1", MachineType: "e2-standard-4"}
	got := env.prices(nil)(vm)
	// The operator's rates win over the VM table's.
	if got.VCPUSecondUSD != 1e-5 || got.GiBSecondUSD != 2e-6 {
		t.Errorf("an overridden VM run prices = %+v, want the local override's rates", got)
	}
	// The machine type's quantities do not, and the note survives.
	if got.VCPU != 4 || got.MemoryGiB != 16 {
		t.Errorf("an overridden VM run carries %v vCPU / %v GiB, want the e2-standard-4's 4/16", got.VCPU, got.MemoryGiB)
	}
	for _, want := range []string{localcfg.PriceSource, "boot disk", "Cloud NAT"} {
		if !strings.Contains(got.Source, want) {
			t.Errorf("an overridden VM run's source %q does not name %q", got.Source, want)
		}
	}
	// A VM of a machine type the table does not carry is refused, override or
	// not: the quantities are unknown, so there is nothing to bill.
	unknown := &backend.Execution{Name: vm.Name, Region: "us-central1", MachineType: "n2-standard-4"}
	if got := env.prices(nil)(unknown); got != (backend.Prices{}) {
		t.Errorf("an overridden VM of an unknown machine type = %+v, want no prices", got)
	}
	// Cloud Run's override is the local override, unchanged: no machine
	// type's quantities, its own source line.
	cloud := &backend.Execution{Name: "exec-1", Region: "us-central1", CPU: 2, MemoryGiB: 4}
	if got := env.prices(nil)(cloud); got.VCPU != 0 || got.MemoryGiB != 0 || got.Source != localcfg.PriceSource {
		t.Errorf("Cloud Run's overridden prices = %+v, want the override as it was", got)
	}
}

// TestJobWorkflowRefusesInsteadOfPanicking is review finding #1's "confirm
// it cannot panic" follow-up: unlike OAuthToken (config.ClaudeAccountNameRE,
// enforced by task.Spec.Validate on every task.Parse), task.Spec's Workflow
// field is never shape-checked, so a task this process didn't just build —
// another CLI's unvalidated --workflow flag, or bucket data a launcher wrote
// directly — can carry a value gcp.WorkflowVariant itself refuses outright
// (a panic, by design, so two distinct inputs can never collide on its "_"
// join unnoticed). jobWorkflow is the panic-safe wrapper checkExecution and
// launchRun both use; it must turn that panic into ok == false, never crash
// the process reading an old or hostile task.json.
func TestJobWorkflowRefusesInsteadOfPanicking(t *testing.T) {
	for _, bad := range []string{"Not-Lowercase", "has a space", "", "way-too-long-a-workflow-name-for-the-charset"} {
		if _, ok := jobWorkflow(bad, "acme-personal"); ok {
			t.Errorf("jobWorkflow(%q, acme-personal) = ok, want refused", bad)
		}
	}
	if v, ok := jobWorkflow("web", ""); !ok || v != "web" {
		t.Fatalf("jobWorkflow(web, \"\") = %q, %v, want web, true (the default account is the identity)", v, ok)
	}
	if v, ok := jobWorkflow("web", "acme-personal"); !ok || v != "web_acme-personal" {
		t.Fatalf("jobWorkflow(web, acme-personal) = %q, %v", v, ok)
	}
}

// "." and ".." are no slug: path.Join would take them out of runs/.
func TestRunRefRefusesDotSlugs(t *testing.T) {
	newCloudFixture(t)
	for _, ref := range []string{"./20260927-100000-abcd", "../20260927-100000-abcd"} {
		for _, cmd := range []string{"logs", "diagnose", "cancel"} {
			_, _, err := execute(t, cmd, ref)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "<repo-slug>/<run-id>") {
				t.Errorf("%s %s: %v", cmd, ref, err)
			}
		}
	}
}
