package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
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
	// And the state directory, where a step-printing command files its run's
	// log: one of these per test, so a test can read back the one log its own
	// run left (onlyLog) instead of TestMain's shared backstop directory.
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
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

// ---------------------------------------------------------------- resolveRun

// endingNow is the instant the resolveRun tests pin lsNow to. An ending's
// collision rule is computed from the run IDs themselves (SR1), so a test that
// asserts which ending tells two runs apart needs the IDs minted against a
// clock it chose and not against the second the test happened to run: the
// extension reaches as far into the launch stamp as the two runs agree.
var endingNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// endingFixture is the cloud fixture with the budget database an ending is
// looked up in — the /agents feed of its active runs — and lsNow pinned.
type endingFixture struct {
	*cloudFixture
	db *gcpfake.RTDB
}

// newEndingFixture is one installation resolveRun works against: aurora's
// config with a budget database, the agent markers cleared (a test that wants
// a person's session says so itself), and the clock the seven-day window is
// measured back from pinned to endingNow.
func newEndingFixture(t *testing.T) *endingFixture {
	t.Helper()
	clearAgentMarkers(t)
	f := &endingFixture{cloudFixture: newCloudFixture(t), db: gcpfake.NewRTDB(t)}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+f.db.URL+" }\n")
	old := lsNow
	lsNow = func() time.Time { return endingNow }
	t.Cleanup(func() { lsNow = old })
	return f
}

// env is the fixture's environment over the fixture's own runs bucket, the one
// seedTitledRun writes to: resolveRun then reads the objects a seed wrote.
func (f *endingFixture) env(t *testing.T) *cloudEnv {
	t.Helper()
	b, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return envOn(t, f.cloudFixture, b)
}

// bothTerminals makes a person's terminal of the streams a lookup is given:
// stdinIsTerminal and writerIsTerminal both answer true, which with no --json
// and no agent marker is the one case a pick list is shown in (D1700).
func bothTerminals(t *testing.T) {
	t.Helper()
	oldIn, oldOut := stdinIsTerminal, writerIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stdinIsTerminal, writerIsTerminal = oldIn, oldOut })
}

// resolveOpts are the options of one lookup, with the streams a test reads
// back: in is what the person would type.
func resolveOpts(in string) (resolveRunOptions, *strings.Builder, *strings.Builder) {
	out, errOut := &strings.Builder{}, &strings.Builder{}
	return resolveRunOptions{in: strings.NewReader(in), out: out, errOut: errOut}, out, errOut
}

// seedTitledRun seeds one run of the fixture's repository with its own title:
// task.json is what locates a run at all, and the title is the fact the lists
// below show.
func seedTitledRun(t *testing.T, f *cloudFixture, id, title string) {
	t.Helper()
	ctx := context.Background()
	b, err := blobx.Open(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Title: title}
	if err := runstore.Open(b.Bucket, appSlug, id).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
}

// collide seeds the two runs of one ending: minted a minute apart from
// endingNow, so with endingNow at 12:00:00 they are …-115800-3bb2 and
// …-115900-3bb2, and the shortest endings that tell them apart are 800-3bb2
// and 900-3bb2 (SR1: the extension crosses the dash). newer is the one minted
// later.
func collide(t *testing.T, f *endingFixture, ending string) (newer, older string) {
	t.Helper()
	newer, older = recentRunID(t, time.Minute, ending), recentRunID(t, 2*time.Minute, ending)
	seedTitledRun(t, f.cloudFixture, older, "the older run")
	seedTitledRun(t, f.cloudFixture, newer, "the newer run")
	return newer, older
}

// TestResolveRunEnding: a full reference behaves as locateRun does, a unique
// ending resolves to its run, and the two ways a lookup finds nothing — an
// ending shorter than four characters (D1701) and one neither an active run nor
// a run the bucket holds carries (D1709) — are user errors with their own
// sentences.
func TestResolveRunEnding(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	ctx := context.Background()
	id := recentRunID(t, time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, id, "fix the parser")

	for _, ref := range []string{"3bb2", id, appSlug + "/" + id} {
		o, _, errOut := resolveOpts("")
		slug, got, err := resolveRun(ctx, env, ref, o)
		if err != nil || slug != appSlug || got != id {
			t.Fatalf("resolveRun(%q) = %q, %q, %v (stderr %q)", ref, slug, got, err, errOut.String())
		}
	}

	o, _, _ := resolveOpts("")
	if _, _, err := resolveRun(ctx, env, "3bb", o); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "at least 4") {
		t.Errorf("a three-character ending = %v, want the minimum-length refusal", err)
	}
	o, _, _ = resolveOpts("")
	_, _, err := resolveRun(ctx, env, "ffff", o)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "among the active runs or the runs the bucket still holds") || !strings.Contains(err.Error(), "pass the full ID") {
		t.Errorf("an ending no run carries = %v, want D1709's sentence", err)
	}
}

// TestResolveRunEndingFindsAFinishedRunBeyondTheWindow: the window an ending is
// looked up in is `runs ls`'s seven days while the bucket keeps a run for
// runs_days (D1702), and every message that hands an ending over — `fugaro logs
// <ending>`, `fugaro artifacts ls <ending>`, `fugaro diagnose <ending>` — prints
// the short form, the one a report or a PR from last month still carries. A
// miss in the window is therefore not the end of the lookup: the runs the
// bucket still holds are asked once more, and the ending a month-old report
// carries names its run again. Mutation caught: refusing an ending the window
// does not carry while the bucket holds the run it names.
func TestResolveRunEndingFindsAFinishedRunBeyondTheWindow(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	ctx := context.Background()
	id := recentRunID(t, 30*24*time.Hour, "3bb2")
	seedTitledRun(t, f.cloudFixture, id, "a run of last month")

	o, _, _ := resolveOpts("")
	slug, got, err := resolveRun(ctx, env, "3bb2", o)
	if err != nil || slug != appSlug || got != id {
		t.Fatalf("resolveRun(3bb2) of a run 30 days old = %q, %q, %v, want the run the bucket still holds", slug, got, err)
	}
}

// TestResolveRunEndingBeyondTheWindowRefusesAnAmbiguousEnding: the widened
// listing is a listing like any other, so an ending two of its runs share is
// never resolved by picking one (D1700): it comes back as the candidates — the
// repository and the endings that tell them apart, with each one's title — and
// the person pastes the run they mean. Mutation caught: resolving an ending two
// of the bucket's runs carry to one of them.
func TestResolveRunEndingBeyondTheWindowRefusesAnAmbiguousEnding(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	ctx := context.Background()
	newer := recentRunID(t, 30*24*time.Hour+time.Minute, "3bb2")
	older := recentRunID(t, 30*24*time.Hour+2*time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, older, "the older run")
	seedTitledRun(t, f.cloudFixture, newer, "the newer run")

	o, out, _ := resolveOpts("")
	slug, got, err := resolveRun(ctx, env, "3bb2", o)
	if err == nil || slug != "" || got != "" {
		t.Fatalf("resolveRun(3bb2) = %q, %q, %v, want a refusal and no run", slug, got, err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal = %v, want a user error", err)
	}
	for _, want := range []string{appSlug, "900-3bb2", "800-3bb2", "the newer run", "the older run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%v", want, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a lookup nobody answers wrote a pick list to stdout: %q", out.String())
	}
}

// TestResolveRunAmbiguousNonTerminalRefuses is the red step: two runs whose
// IDs share their last four characters are never resolved by picking one
// (D1700) — a lookup nobody answers lists both, under the endings that tell
// them apart, and returns no run at all.
func TestResolveRunAmbiguousNonTerminalRefuses(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	collide(t, f, "3bb2")

	o, out, _ := resolveOpts("")
	slug, got, err := resolveRun(context.Background(), env, "3bb2", o)
	if err == nil || slug != "" || got != "" {
		t.Fatalf("resolveRun(3bb2) = %q, %q, %v, want a refusal and no run", slug, got, err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal = %v, want a user error", err)
	}
	for _, want := range []string{appSlug, "800-3bb2", "900-3bb2", "the older run", "the newer run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a lookup nobody answers wrote a pick list to stdout: %q", out.String())
	}
}

// TestResolveRunAmbiguousTerminalPicks: the same collision at a person's
// terminal is a numbered list, and the number names the run (D1700). The
// matches come back newest first, as the resolver sorts them.
func TestResolveRunAmbiguousTerminalPicks(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	bothTerminals(t)
	_, older := collide(t, f, "3bb2")
	ctx := context.Background()

	o, out, errOut := resolveOpts("2\n")
	slug, got, err := resolveRun(ctx, env, "3bb2", o)
	if err != nil || slug != appSlug || got != older {
		t.Fatalf("picking 2 = %q, %q, %v (stderr %q)", slug, got, err, errOut.String())
	}
	for _, want := range []string{"1)", "2)", "800-3bb2", "900-3bb2", "the older run", "the newer run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the pick list does not show %q:\n%s", want, out.String())
		}
	}
	o, _, _ = resolveOpts("7\n")
	if _, _, err := resolveRun(ctx, env, "3bb2", o); ExitCode(err) != ExitUserError {
		t.Errorf("an answer outside the list = %v, want a user error", err)
	}
}

// TestResolveRunAgentMarkerRefuses: an agent's session with a pty must not
// answer its own prompt (D1700): the pick list is not shown and the collision
// is the refusal.
func TestResolveRunAgentMarkerRefuses(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	bothTerminals(t)
	t.Setenv(agentenv.AgentMarkers[0], "1")
	collide(t, f, "3bb2")

	o, out, _ := resolveOpts("1\n")
	slug, got, err := resolveRun(context.Background(), env, "3bb2", o)
	if err == nil || slug != "" || got != "" {
		t.Fatalf("an agent's terminal = %q, %q, %v, want a refusal", slug, got, err)
	}
	if out.Len() != 0 {
		t.Errorf("an agent's session was shown a pick list: %q", out.String())
	}
	if !strings.Contains(err.Error(), "800-3bb2") || !strings.Contains(err.Error(), "900-3bb2") {
		t.Errorf("the refusal does not list the candidates:\n%s", err)
	}
}

// TestResolveRunJSONRefuses: --json is a machine reading, so nobody is asked
// even on a terminal (D1700).
func TestResolveRunJSONRefuses(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	bothTerminals(t)
	collide(t, f, "3bb2")

	o, out, _ := resolveOpts("1\n")
	o.asJSON = true
	slug, got, err := resolveRun(context.Background(), env, "3bb2", o)
	if err == nil || slug != "" || got != "" {
		t.Fatalf("a --json lookup = %q, %q, %v, want a refusal", slug, got, err)
	}
	if out.Len() != 0 {
		t.Errorf("a --json lookup was shown a pick list: %q", out.String())
	}
}

// TestResolveRunPlantedActiveMatchIgnored: the active feed is a node the job
// account can write, so an entry in it is a claim and not a run — a valid run
// ID with a task.json in the bucket is what confirms one (F6, D1710). An
// ending resolves to the runs the bucket holds, never to a name planted in the
// registry.
func TestResolveRunPlantedActiveMatchIgnored(t *testing.T) {
	ctx := context.Background()

	t.Run("a planted entry alone is not a run", func(t *testing.T) {
		f := newEndingFixture(t)
		env := f.env(t)
		planted := recentRunID(t, time.Minute, "3bb2")
		f.db.Set(budget.PathAgent(appSlug, planted), map[string]any{"run_id": planted, "title": "planted"})

		o, _, _ := resolveOpts("")
		slug, got, err := resolveRun(ctx, env, "3bb2", o)
		if err == nil || slug != "" || got != "" {
			t.Fatalf("resolveRun(3bb2) = %q, %q, %v, want nothing: a registry entry the bucket does not confirm is not a run", slug, got, err)
		}
		if !strings.Contains(err.Error(), "no run ending") {
			t.Errorf("the refusal is %v, want the not-found sentence", err)
		}
	})

	t.Run("a planted entry does not shadow the run that exists", func(t *testing.T) {
		f := newEndingFixture(t)
		env := f.env(t)
		real := recentRunID(t, 3*time.Minute, "3bb2")
		seedTitledRun(t, f.cloudFixture, real, "the real run")
		planted := recentRunID(t, time.Minute, "3bb2")
		f.db.Set(budget.PathAgent(appSlug, planted), map[string]any{"run_id": planted, "title": "planted"})

		o, _, _ := resolveOpts("")
		slug, got, err := resolveRun(ctx, env, "3bb2", o)
		if err != nil || slug != appSlug || got != real {
			t.Fatalf("resolveRun(3bb2) = %q, %q, %v, want the run the bucket confirms (%s)", slug, got, err, real)
		}
	})
}

// TestResolveRunFeedUnreadable: a feed that cannot be read is said on stderr
// and the lookup goes on among the window's runs (F6). The answer is narrower
// than an ending normally gets, and a person is told so rather than shown a
// list that silently misses the runs that are live now.
func TestResolveRunFeedUnreadable(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	id := recentRunID(t, time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, id, "the run")
	f.db.DenyRead("agents")
	ctx := context.Background()

	o, _, errOut := resolveOpts("")
	slug, got, err := resolveRun(ctx, env, "3bb2", o)
	if err != nil || slug != appSlug || got != id {
		t.Fatalf("resolveRun(3bb2) with the feed unreadable = %q, %q, %v", slug, got, err)
	}
	if !strings.Contains(errOut.String(), "could not read the active runs") || !strings.Contains(errOut.String(), "in the bucket") {
		t.Errorf("the fallback to the window was not said: %q", errOut.String())
	}

	// The active-only lookup has no window to fall back to, so a feed it
	// cannot read is a failure and not a narrower answer: `cancel` must never
	// be told an ending names nothing when nobody could read the live runs.
	o, _, _ = resolveOpts("")
	o.activeOnly = true
	if _, _, err := resolveRun(ctx, env, "3bb2", o); err == nil {
		t.Errorf("the active-only lookup answered with an unreadable feed")
	}
}

// TestResolveRunActiveOnlyRefusesAndLists: the active-only lookup is `cancel`'s
// (D1710). It never resolves — an ending names the runs it lists, and the
// full ID is what a person pastes — and a run that merely finished inside the
// window is not one of them.
func TestResolveRunActiveOnlyRefusesAndLists(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	ctx := context.Background()
	live := recentRunID(t, time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, live, "the running run")
	f.db.Set(budget.PathAgent(appSlug, live), map[string]any{"run_id": live, "title": "the running run"})
	finished := recentRunID(t, 2*time.Minute, "cafe")
	seedTitledRun(t, f.cloudFixture, finished, "the finished run")

	// opts is the active-only lookup, with the two streams a test reads back.
	opts := func() (resolveRunOptions, *strings.Builder) {
		o, out, _ := resolveOpts("")
		o.activeOnly = true
		return o, out
	}

	// Exactly one active match is still a refusal: this is what makes the
	// full ID the only way to cancel a run (D1710).
	o, out := opts()
	slug, got, err := resolveRun(ctx, env, "3bb2", o)
	if err == nil || slug != "" || got != "" {
		t.Fatalf("the active-only lookup = %q, %q, %v, want a refusal and no run", slug, got, err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), appSlug+"/"+live) || !strings.Contains(err.Error(), "the running run") {
		t.Errorf("the refusal does not name the full ID to paste and its title:\n%v", err)
	}
	if strings.Contains(err.Error(), finished) {
		t.Errorf("the refusal lists a run that is not active:\n%v", err)
	}
	// The list is part of the refusal, so nothing reaches stdout: `cancel
	// --json` prints its document there, and an ending is not one.
	if out.Len() != 0 {
		t.Errorf("the refusal wrote %q on stdout, want nothing there", out.String())
	}

	// A second active run sharing the ending is listed too, in full.
	second := recentRunID(t, 3*time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, second, "the other running run")
	f.db.Set(budget.PathAgent(appSlug, second), map[string]any{"run_id": second, "title": "the other running run"})
	o, _ = opts()
	if _, _, err := resolveRun(ctx, env, "3bb2", o); ExitCode(err) != ExitUserError {
		t.Fatalf("two active matches = %v, want a user error", err)
	} else {
		for _, want := range []string{appSlug + "/" + live, appSlug + "/" + second, "the other running run"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not list %q:\n%v", want, err)
			}
		}
	}

	// An ending only a finished run carries names no active run.
	o, _ = opts()
	if _, _, err := resolveRun(ctx, env, "cafe", o); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no active run ending") {
		t.Errorf("an ending of a finished run = %v, want the active-only sentence", err)
	}
}

// TestLocateRunStillRefusesAnEnding: locateRun is what `run --retry` keeps
// (a full ID or <slug>/<run-id>), and SR3 adds resolveRun beside it rather
// than widening it: an ending is refused there however unique it is.
func TestLocateRunStillRefusesAnEnding(t *testing.T) {
	f := newEndingFixture(t)
	env := f.env(t)
	ctx := context.Background()
	id := recentRunID(t, time.Minute, "3bb2")
	seedTitledRun(t, f.cloudFixture, id, "fix the parser")

	if _, _, err := locateRun(ctx, env, "3bb2"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "neither <repo-slug>/<run-id> nor a run ID") {
		t.Errorf("locateRun(3bb2) = %v, want the malformed-reference refusal", err)
	}
	slug, got, err := locateRun(ctx, env, id)
	if err != nil || slug != appSlug || got != id {
		t.Errorf("locateRun(%s) = %q, %q, %v, want the run", id, slug, got, err)
	}
}
