package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestValidateResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, errOut, err := execute(t, "validate")
	if err != nil || !strings.Contains(out, "fugaro.yaml is valid") || !strings.Contains(errOut, "uses the project layer") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestValidateWithAProjectLayerFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "") // none published
	layerCheckout(t, f, minimalAnchored)
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"layer.yaml": testProjectLayer, "bad.yaml": layerWithDefaults("  budget: { per_run_usd: 1 }\n")})
	if out, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "layer.yaml")); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good layer: %q %v", out, err)
	}
	_, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "bad.yaml"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bad.yaml is invalid") {
		t.Fatalf("bad layer: %v", err)
	}
}

// --project-layer FILE must refuse a symlink, end to end through the real
// command, not just at readLayerFile: a symlink could point anywhere the
// user running validate can read.
//
// Mutation (run, restore): revert readLayerFile to open the path directly
// (see TestReadLayerFile), and this test fails: validate reads the
// secret through the symlink and reports it valid.
func TestValidateProjectLayerFileRefusesASymlink(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "") // none published
	layerCheckout(t, f, minimalAnchored)
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"layer.yaml": testProjectLayer})
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(filepath.Join(dir, "layer.yaml"), link); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "validate", "--project-layer", link)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
}
func TestValidateOfflineNeedsTheLayerForAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "validate", "--offline")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "--project-layer") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

func TestValidateOfflineWarnsForAFullFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	out, errOut, err := execute(t, "validate", "--offline")
	if err != nil || !strings.Contains(out, "is valid") || !strings.Contains(errOut, "project layer") || !strings.Contains(errOut, "was not checked") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestDoctorFugaroYAMLResolves(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if c, fy := fugaroYAMLCheck(context.Background(), dir, fileEnv(t, f).lc); c == nil || !c.OK || fy == nil || !fy.Valid {
		t.Fatalf("check %+v, %+v", c, fy)
	}
}

func TestImageRenderResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "image", "render")
	if err != nil || !strings.Contains(out, "FROM") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// validate must never go strict when the bucket merely fails to read (as
// opposed to --offline with nothing cached, which TestValidateOfflineNeedsTheLayerForAMinimalFile
// covers): decision L16 keeps validate lenient always, so an unreadable
// bucket still ends in the ordinary "N problem(s)" report, never a raw
// remote error that skips it.
//
// Mutation (run, restore): change validate.go's resolveFugaroYAML call to
// pass `Lenient: offline` instead of `Lenient: true`, and this test fails:
// ExitCode becomes ExitRemoteError (the bucket error returned raw) and out
// is empty, since RunE returns before printing anything.
func TestValidateStaysLenientWhenTheBucketIsUnreadable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, errors.New("boom")
	}
	out, _, err := execute(t, "validate")
	if ExitCode(err) != ExitUserError || out == "" || !strings.Contains(err.Error(), "problem(s)") {
		t.Fatalf("out %q, err %v (exit %d)", out, err, ExitCode(err))
	}
}

// A workflow that took its commands from a profile needs every consumer on
// layeredSince: validate must say so, not only name the layer.
//
// Mutation (run, restore): make usesProfiles always return false, and this
// test fails (the warning line disappears though minimalAnchored's only
// workflow comes entirely from profile svc).
func TestValidateWarnsWhenAWorkflowUsesAProfile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil || !strings.Contains(errOut, "profiles need fugaro "+layeredSince) {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

// validate --json must carry the layer's sha256, not just mention it in a
// warning line: fugaro config show and CI tooling read the JSON.
//
// Mutation (run, restore): drop `ProjectLayer: layer` from the
// validateOutput literal in validate.go, and this test fails (project_layer
// is absent from the JSON).
func TestValidateJSONCarriesTheLayerSHA256(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "validate", "--json")
	if err != nil {
		t.Fatalf("err %v\n%s", err, out)
	}
	var vo validateOutput
	if jerr := json.Unmarshal([]byte(out), &vo); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	want := config.LayerSum([]byte(testProjectLayer))
	if vo.ProjectLayer == nil || vo.ProjectLayer.SHA256 != want {
		t.Fatalf("project_layer = %+v, want sha256 %s", vo.ProjectLayer, want)
	}
}

// validate's project-layer line shows the real sha256's 12-character
// prefix, never "(invalid)": shortSHA (internal/cli/ls.go, task 17)
// refuses anything that isn't exactly 64 lowercase hex characters, but the
// layer's own sha256 (config.LayerSum's output, hex.EncodeToString of a
// sha256.Sum256) always is one, so it must always pass.
//
// Mutation (run, restore): change validateLayerLines' shortSHA(l.SHA256)
// call to shortSHA(l.Project) (a short, non-hex string, "aurora"), and
// this test fails: the warning line then shows "(invalid)" instead of a
// sha256 prefix.
func TestValidateShowsTheRealLayerSHA256Prefix(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("err %v\nstderr %s", err, errOut)
	}
	want := config.LayerSum([]byte(testProjectLayer))[:12]
	if !strings.Contains(errOut, "sha256 "+want) {
		t.Fatalf("stderr %q, want it to contain the real sha256 prefix %q", errOut, want)
	}
	if strings.Contains(errOut, "(invalid)") {
		t.Fatalf("stderr %q shows (invalid) for a real layer's sha256", errOut)
	}
}

// doctor's fugaroYAMLCheck call site (doctor.go) must hand it the selected
// project config, not nil: with the shared bucket's layer published and a
// minimal (layer-only) checkout, resolving needs that lc to find the layer
// at all.
//
// Mutation (run, restore): change doctor.go's `fugaroYAMLCheck(ctx,
// co.Root, lc)` call to pass nil, and this test fails (fy.Valid becomes
// false: with no project config selected the lenient path never checks the
// bucket, so the minimal file's "must define at least one workflow" problem
// is never resolved).
func TestDoctorJSONResolvesTheLayerForTheSelectedProject(t *testing.T) {
	r := newDoctorRig(t)
	isolateCache(t)
	runs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	old := layerBucketOpener
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+runs) }
	t.Cleanup(func() { layerBucketOpener = old })
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), minimalAnchored))

	o := doctorJSON(t)
	if o.FugaroYAML == nil || !o.FugaroYAML.Valid {
		t.Fatalf("fugaro.yaml check = %+v", o.FugaroYAML)
	}
}

// checkoutParse (cloud.go) must resolve the layer for whatever project is
// selected, not a nil one.
//
// Mutation (run, restore): change checkoutParse's
// `parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))` to pass
// nil, and this test fails (cfg becomes nil: the minimal file's "needs a
// layer" problem is never resolved without a selected project).
func TestCheckoutParseResolvesTheLayerForTheSelectedProject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": minimalAnchored})
	t.Chdir(dir)
	cfg, problems := checkoutParse(context.Background(), "acme/other")
	if cfg == nil || len(problems) != 0 {
		t.Fatalf("cfg %+v, problems %v", cfg, problems)
	}
}

// resolveRepoTarget (init_repo_target.go), the repository stage's first
// read of the default branch's fugaro.yaml, must resolve the layer for the
// selected project too.
//
// Mutation (run, restore): change its
// `parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))` call to
// pass nil, and this test fails (tgt.cfg becomes nil, so the stage would
// report the minimal file "not valid" though it is, under the layer).
func TestResolveRepoTargetResolvesTheLayerForTheSelectedProject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := repoCheckout(t, "https://github.com/acme/app.git", minimalAnchored)
	t.Chdir(dir)
	tgt, status := resolveRepoTarget(context.Background(), "")
	if tgt == nil || tgt.cfg == nil {
		t.Fatalf("target %+v, status %+v", tgt, status)
	}
}

// defaultBranchConfig (initsecrets.go), which `secrets set NAME` uses to
// check a workflow secret's name against the repository's declared
// secrets, must resolve the layer for the selected project.
//
// Mutation (run, restore): change its
// `parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))` call to
// pass nil, and this test fails (it returns nil though the default branch's
// minimal file resolves cleanly under the published layer).
func TestDefaultBranchConfigResolvesTheLayerForTheSelectedProject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := repoCheckout(t, "https://github.com/acme/app.git", minimalAnchored)
	t.Chdir(dir)
	if cfg := defaultBranchConfig(context.Background(), dir); cfg == nil {
		t.Fatal("defaultBranchConfig returned nil")
	}
}

// checkoutParse must read the bucket at most once per command invocation:
// run.go calls it (through checkoutConfig) up to three times for the same
// repository, launch_budget.go once more, and secrets.go a fourth time when
// its own first read comes back nil. withCheckoutParseCache, installed once
// by root.go's PersistentPreRun, is what makes repeats free.
//
// Mutation (run, restore): make checkoutParse skip the cache lookup and
// always parse fresh, and this test fails (reads becomes 3, not 1).
func TestCheckoutParseCachesTheLayerReadPerCommand(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": minimalAnchored})
	t.Chdir(dir)

	reads := 0
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		reads++
		return read(ctx, b)
	}
	ctx := withCheckoutParseCache(context.Background())
	for i := 0; i < 3; i++ {
		if cfg, _ := checkoutParse(ctx, "acme/other"); cfg == nil {
			t.Fatalf("call %d: checkoutParse found no config", i)
		}
	}
	if reads != 1 {
		t.Fatalf("layerRead called %d times, want 1", reads)
	}
}

// TestRunMemoizesTheLayerReadThroughTheRealCommand is
// TestCheckoutParseCachesTheLayerReadPerCommand end to end, through a real
// `fugaro run` (NewRootCmd, not a call to withCheckoutParseCache): run.go
// calls checkoutConfig twice in the first-run path (once directly, once
// inside runnerChooses), so without root.go's PersistentPreRun installing
// the cache, this is two bucket reads for one command.
//
// Mutation (run, restore): remove the
// `cmd.SetContext(withCheckoutParseCache(cmd.Context()))` line from
// root.go's PersistentPreRun, and this test fails (reads becomes 2).
func TestRunMemoizesTheLayerReadThroughTheRealCommand(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "") // the default runs bucket name; no layer object needed
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngcp_project: proj-1234\n" +
		"git: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n" +
		"workflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)

	reads := 0
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		reads++
		return read(ctx, b)
	}
	if _, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("layerRead called %d times through the real command, want 1", reads)
	}
}

// defaultBranchParse must read the bucket at most once per command
// invocation too: fugaro init calls it (through resolveRepoTarget, once
// while gathering inputs and once more in the repository stage, and
// through defaultBranchConfig in the secrets stage) up to three times for
// the same checkout.
//
// Mutation (run, restore): make defaultBranchParse skip the cache lookup
// and always read fresh, and this test fails (reads becomes 3, not 1).
func TestDefaultBranchParseCachesTheLayerReadPerCommand(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := repoCheckout(t, "https://github.com/acme/app.git", minimalAnchored)
	t.Chdir(dir)

	reads := 0
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		reads++
		return read(ctx, b)
	}
	ctx := withCheckoutParseCache(context.Background())
	for i := 0; i < 3; i++ {
		if db := defaultBranchParse(ctx, dir); db.cfg == nil {
			t.Fatalf("call %d: defaultBranchParse found no config: %+v", i, db)
		}
	}
	if reads != 1 {
		t.Fatalf("layerRead called %d times, want 1", reads)
	}
}
