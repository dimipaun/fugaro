package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	checkBase    = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc"
	checkRepoURL = "https://bitbucket.org/acme/app.git"
	checkBucket  = "fugaro-runs-proj-1234"
	checkToken   = "bb-token-value"
)

var (
	checkBaseDigest   = "sha256:" + strings.Repeat("1", 64)
	checkLatestDigest = "sha256:" + strings.Repeat("2", 64)
)

// checkFixture is acme/app on Bitbucket, whose clone URL git rewrites to a
// local bare repository serving blobless clones, with the check's Cloud
// Build, registry and bucket pointed at fakes.
type checkFixture struct {
	fb     *gcpfake.Build
	reg    *gcpfake.Registry
	bucket string // file:// URL of the runs bucket's stand-in
	slug   string
	rs     infra.RepoSpec
	work   string // a clone of the remote, to push from
	image  string // untagged
	gitCfg string
}

// checkRemote serves files as acme/app's base branch: https clones of
// checkRepoURL reach it through git's url.insteadOf.
func checkRemote(t *testing.T, files map[string]string) (f *checkFixture) {
	t.Helper()
	testutil.IsolateGit(t)
	bare := testutil.NewRemote(t, files)
	testutil.Git(t, bare, "config", "uploadpack.allowFilter", "true")
	testutil.Git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	f = &checkFixture{slug: mustSlug("bitbucket", "acme/app"), gitCfg: filepath.Join(t.TempDir(), "gitconfig")}
	f.pointGitAt(t, "file://"+bare)
	f.work = filepath.Join(t.TempDir(), "work")
	testutil.Git(t, filepath.Dir(f.work), "clone", "--quiet", bare, f.work)
	return f
}

// pointGitAt makes git fetch checkRepoURL from url.
func (f *checkFixture) pointGitAt(t *testing.T, url string) {
	t.Helper()
	if err := os.WriteFile(f.gitCfg, []byte("[url \""+url+"\"]\n\tinsteadOf = "+checkRepoURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", f.gitCfg)
}

// hookCheck points the check's Cloud Build, registry and bucket at fakes.
func (f *checkFixture) hookCheck(t *testing.T) {
	t.Helper()
	f.fb = gcpfake.NewBuild(t)
	f.reg = gcpfake.NewRegistry(t)
	f.bucket = "file://" + t.TempDir()
	oldBucket, oldEndpoints, oldRegistry := checkOpenBucket, checkEndpoints, checkRegistry
	t.Cleanup(func() { checkOpenBucket, checkEndpoints, checkRegistry = oldBucket, oldEndpoints, oldRegistry })
	checkOpenBucket = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if url != "gs://"+checkBucket {
			t.Errorf("the check opened %s", url)
		}
		return blobx.Open(ctx, f.bucket)
	}
	checkEndpoints = gcp.Endpoints{CloudBuild: f.fb.URL + "/", NoAuth: true}
	checkRegistry = func() *imagecheck.Registry {
		return &imagecheck.Registry{HTTP: &http.Client{}, Google: &http.Client{}, Endpoint: func(string) string { return f.reg.URL }}
	}
	f.reg.SetManifest("proj-1234/fugaro-base/fugaro-web-node", "dev-abc", checkBaseDigest)
}

// checkLC is the local config the check job's spec is made from.
func checkLC() *localcfg.Config {
	return &localcfg.Config{
		Version: 1, Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", RunsBucket: checkBucket, BaseImages: map[string]string{"web-node": checkBase},
		Build: localcfg.Build{MachineType: "E2_HIGHCPU_8"},
		Repos: map[string]localcfg.Repo{"acme/app": {Provider: "bitbucket", BaseBranch: "main", Workflows: []string{"app"}}},
	}
}

// newCheckJob is a check job installed for installedYAML, running
// against a remote whose base branch holds files: its env is set as
// Terraform sets the job's, and the provider token is mounted.
func newCheckJob(t *testing.T, files map[string]string, installedYAML string) *checkFixture {
	t.Helper()
	f := checkRemote(t, files)
	f.hookCheck(t)
	cfg, problems := config.Parse([]byte(installedYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	rs, err := infra.Repo(infra.Inputs{LC: checkLC(), Repo: "acme/app", Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check == nil {
		t.Fatal("no check job")
	}
	f.rs = rs
	f.image = gcp.ImageName(rs.RegistryPath, f.slug, "app")
	for k, v := range rs.Check.Env {
		t.Setenv(k, v)
	}
	for env := range rs.Check.SecretEnv {
		t.Setenv(env, checkToken)
	}
	return f
}

func checkFiles() map[string]string {
	files := npmFiles()
	files["fugaro.yaml"] = bitbucketYAML
	return files
}

// seedRecord writes the record a build of the remote's head would have
// written, with latest at its digest.
func (f *checkFixture) seedRecord(t *testing.T) *imagecheck.Record {
	t.Helper()
	testutil.Git(t, f.work, "pull", "--quiet", "--ff-only")
	cfg, _ := config.Parse([]byte(bitbucketYAML))
	tree, err := imagecheck.Open(context.Background(), f.work, "HEAD", nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := imagecheck.KeyFiles(cfg, "app", tree)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := imagecheck.ImageConfigHash(cfg, "app", tree)
	if err != nil {
		t.Fatal(err)
	}
	rec := imagecheck.Record{
		Version: imagecheck.RecordVersion, Repo: "acme/app", Workflow: "app", BuiltAt: time.Now().Add(-time.Hour).UTC(), BuildID: "b-old",
		SourceCommit: testutil.Git(t, f.work, "rev-parse", "HEAD"), BaseBranch: "main", KeyFiles: keys, ImageConfigHash: hash,
		BaseRef: checkBase, BaseDigest: checkBaseDigest, Image: f.image, ImageDigest: checkLatestDigest,
		FugaroVersion: Version, TemplateSalt: gcp.TemplateSalt(Version),
	}
	writeImageRecord(t, f.bucket, rec)
	f.reg.SetManifest(strings.SplitN(f.image, "/", 2)[1], "latest", checkLatestDigest)
	return &rec
}

func (f *checkFixture) checkState(t *testing.T, workflow string) *imagecheck.CheckState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.CheckKey(f.slug, workflow)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := imagecheck.ParseCheckState(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// checkLine is one JSON log line of the check job.
type checkLine struct {
	Severity        string   `json:"severity"`
	Event           string   `json:"event"`
	Repo            string   `json:"repo"`
	Workflow        string   `json:"workflow"`
	Decision        string   `json:"decision"`
	Reasons         []string `json:"reasons"`
	BuildID         *string  `json:"build_id"`
	LastBuildStatus string   `json:"last_build_status"`
	Error           string   `json:"error"`
}

func checkLines(t *testing.T, out string) []checkLine {
	t.Helper()
	var lines []checkLine
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var l checkLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("not a JSON line: %q", sc.Text())
		}
		if l.Event != "image-check" || l.Repo != "acme/app" || l.Reasons == nil || l.BuildID == nil {
			t.Fatalf("line = %s", sc.Text())
		}
		lines = append(lines, l)
	}
	return lines
}

func runCheckJob(t *testing.T, args ...string) ([]checkLine, error) {
	t.Helper()
	out, stderr, err := executeJob(t, append([]string{"image", "check", "--job"}, args...)...)
	if stderr != "" {
		t.Logf("stderr: %s", stderr)
	}
	return checkLines(t, out), err
}

func TestCheckJobSubmitsBuild(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	lines, err := runCheckJob(t)
	if err != nil {
		t.Fatal(err)
	}
	if buildPosts(f.fb) != 1 {
		t.Fatalf("builds submitted = %d", buildPosts(f.fb))
	}
	if len(lines) != 1 || lines[0].Decision != imagecheck.Rebuild || !slices.Equal(lines[0].Reasons, []string{imagecheck.ReasonNoRecord}) ||
		*lines[0].BuildID != "b0001" || lines[0].Severity != "INFO" {
		t.Fatalf("lines = %+v", lines)
	}

	// The request is the one fugaro image build sends for the workflow.
	cfg, _ := config.Parse([]byte(bitbucketYAML))
	want, err := gcp.BuildRequest("proj-1234", gcp.BuildSpec{
		Slug: f.slug, GitProvider: "bitbucket", RepoURL: checkRepoURL, BaseBranch: "main", Workflow: "app", Base: checkBase,
		Image: f.image, GitSecretID: gcp.SecretID(f.slug, "bitbucket-token"), GitUser: "x-token-auth",
		ServiceAccount: gcp.BuildServiceAccountID(f.slug) + "@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
		WorkflowSecrets: cfg.Workflows["app"].Secrets, Bucket: "gs://" + checkBucket,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(want)
	var wantMap map[string]any
	if err := json.Unmarshal(data, &wantMap); err != nil {
		t.Fatal(err)
	}
	got := f.fb.Last()
	if !reflect.DeepEqual(got, wantMap) {
		t.Fatalf("request =\n%v\nwant\n%v", got, wantMap)
	}
	// The check never skips the smoke test.
	steps, _ := got["steps"].([]any)
	if !slices.ContainsFunc(steps, func(s any) bool { m, _ := s.(map[string]any); return m["id"] == "smoke" }) {
		t.Fatal("the check's build has no smoke step")
	}

	st := f.checkState(t, "app")
	head := testutil.Git(t, f.work, "rev-parse", "HEAD")
	if st == nil || st.Decision != imagecheck.Rebuild || st.BuildID != "b0001" || st.BuildInputs == nil || st.BuildInputs.SourceCommit != head ||
		st.BuildInputs.BaseDigest != checkBaseDigest || st.LastBuildAt == nil || st.LastBuildStatus != "QUEUED" || st.CheckedAt.IsZero() {
		t.Fatalf("check.json = %+v", st)
	}

	// --dry-run decides the same, and submits nothing.
	if lines, err := runCheckJob(t, "--dry-run"); err != nil || len(lines) != 1 || buildPosts(f.fb) != 1 {
		t.Fatalf("dry run: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
}

func TestCheckJobSkipsAndLogs(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	f.seedRecord(t)
	lines, err := runCheckJob(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Decision != imagecheck.Skip || len(lines[0].Reasons) != 0 || lines[0].Workflow != "app" ||
		*lines[0].BuildID != "" || lines[0].Severity != "INFO" {
		t.Fatalf("lines = %+v", lines)
	}
	if buildPosts(f.fb) != 0 {
		t.Fatal("a build was submitted although nothing changed")
	}
	st := f.checkState(t, "app")
	if st == nil || st.Decision != imagecheck.Skip || st.BuildID != "" || time.Since(st.CheckedAt) > time.Minute {
		t.Fatalf("check.json = %+v", st)
	}
}

func TestCheckReportsFailedRebuild(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	f.fb.FailStep = "smoke"
	if _, err := runCheckJob(t); err != nil {
		t.Fatal(err)
	}
	lines, err := runCheckJob(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Severity != "ERROR" || lines[0].Decision != imagecheck.RebuildFailedLast ||
		lines[0].LastBuildStatus != "FAILURE" || *lines[0].BuildID != "b0001" {
		t.Fatalf("lines = %+v", lines)
	}
	st := f.checkState(t, "app")
	if st.Decision != imagecheck.RebuildFailedLast || st.LastBuildStatus != "FAILURE" || st.BuildID != "b0001" || st.BuildInputs == nil {
		t.Fatalf("check.json = %+v", st)
	}
}

// TestCheckJobBacksOffAfterFailure: a failed rebuild isn't submitted again
// with the same inputs; a changed input or --force submits one.
func TestCheckJobBacksOffAfterFailure(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	f.fb.FailStep = "smoke"
	for i, want := range []int{1, 1, 1} {
		if _, err := runCheckJob(t); err != nil {
			t.Fatal(err)
		}
		if buildPosts(f.fb) != want {
			t.Fatalf("run %d: builds submitted = %d", i+1, buildPosts(f.fb))
		}
	}
	testutil.WriteFiles(t, f.work, map[string]string{"package-lock.json": `{"name":"app","lockfileVersion":3,"requires":true,"packages":{"":{"name":"app","version":"1.0.0"}}}`})
	testutil.Git(t, f.work, "commit", "--quiet", "-am", "lockfile")
	testutil.Git(t, f.work, "push", "--quiet", "origin", "HEAD:main")
	lines, err := runCheckJob(t)
	if err != nil || buildPosts(f.fb) != 2 || lines[0].Decision != imagecheck.Rebuild {
		t.Fatalf("changed inputs: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
	if _, err := runCheckJob(t); err != nil || buildPosts(f.fb) != 2 {
		t.Fatalf("second failure: %v, %d builds", err, buildPosts(f.fb))
	}
	lines, err = runCheckJob(t, "--force")
	if err != nil || buildPosts(f.fb) != 3 || !slices.Contains(lines[0].Reasons, imagecheck.ReasonForce) {
		t.Fatalf("forced: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
}

func TestCheckFailureIsLogged(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	f.pointGitAt(t, "file://"+filepath.Join(t.TempDir(), "missing.git"))
	out, stderr, err := executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, %v (%s)", ExitCode(err), err, stderr)
	}
	lines := checkLines(t, out)
	if len(lines) != 1 || lines[0].Decision != imagecheck.CheckFailed || lines[0].Severity != "ERROR" || !strings.Contains(lines[0].Error, "clon") {
		t.Fatalf("lines = %+v", lines)
	}
	if strings.Contains(out+stderr, checkToken) {
		t.Fatal("the provider token reached the output")
	}
	if st := f.checkState(t, "app"); st == nil || st.Decision != imagecheck.CheckFailed || st.Error == "" {
		t.Fatalf("check.json = %+v", st)
	}
	if buildPosts(f.fb) != 0 {
		t.Fatal("a failed check submitted a build")
	}
}

// TestCheckJobNeedsItsSpec: a job without FUGARO_CHECK_SPEC can't tell what
// to check: it fails, and says why.
func TestCheckJobNeedsItsSpec(t *testing.T) {
	newCheckJob(t, checkFiles(), bitbucketYAML)
	t.Setenv(infra.CheckSpecEnv, "")
	_, _, err := executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), infra.CheckSpecEnv) {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
}

func TestCheckNotInstalledWorkflow(t *testing.T) {
	// Head has a second daily workflow the installed job doesn't know.
	files := checkFiles()
	files["fugaro.yaml"] = bitbucketYAML + "  api: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	f := newCheckJob(t, files, bitbucketYAML)
	f.seedRecord(t)
	lines, err := runCheckJob(t)
	if err != nil {
		t.Fatal(err)
	}
	byWorkflow := map[string]checkLine{}
	for _, l := range lines {
		byWorkflow[l.Workflow] = l
	}
	if l := byWorkflow["api"]; l.Decision != imagecheck.NotInstalled || !slices.Equal(l.Reasons, []string{"run fugaro init --repo"}) {
		t.Fatalf("api = %+v", l)
	}
	if byWorkflow["app"].Decision != imagecheck.Skip {
		t.Fatalf("app = %+v", byWorkflow["app"])
	}
	if f.checkState(t, "api") != nil {
		t.Error("a workflow the job doesn't check got a check.json")
	}

	// Head turns app's check off: the installed schedule no longer
	// matches, so nothing is built until init --repo runs.
	yaml := strings.Replace(files["fugaro.yaml"], "base: web-node, secrets:", "base: web-node, rebuild: { check: off }, secrets:", 1)
	testutil.WriteFiles(t, f.work, map[string]string{"fugaro.yaml": yaml, "package-lock.json": `{"changed":true}`})
	testutil.Git(t, f.work, "commit", "--quiet", "-am", "check off")
	testutil.Git(t, f.work, "push", "--quiet", "origin", "HEAD:main")
	lines, err = runCheckJob(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Workflow == "app" && (l.Decision != imagecheck.NotInstalled || !slices.Equal(l.Reasons, []string{"run fugaro init --repo"})) {
			t.Fatalf("app = %+v", l)
		}
	}
	if buildPosts(f.fb) != 0 {
		t.Fatal("a build was submitted for a workflow whose check is off")
	}
}

// localCheck is a checkout of acme/app whose origin is the https URL, with
// a local config whose Cloud Build endpoint is the fake.
func localCheck(t *testing.T) *checkFixture {
	t.Helper()
	f := checkRemote(t, checkFiles())
	f.hookCheck(t)
	dir := filepath.Join(t.TempDir(), "app")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", checkRepoURL, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", checkRepoURL)
	t.Chdir(dir)
	// Only the check's clone reaches the remote through the rewrite: the
	// checkout's origin must read as the https URL.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	old := checkLocalEnv
	t.Cleanup(func() { checkLocalEnv = old })
	checkLocalEnv = func() []string { return append(os.Environ(), "GIT_CONFIG_GLOBAL="+f.gitCfg) }
	cf := newCloudFixture(t, "cloud_build: "+f.fb.URL+"/")
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "provider: github", "provider: bitbucket", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	cf.appendConfig(t, "base_images: {web-node: "+checkBase+"}\n")
	f.bucket = cf.bucket
	cfg, _ := config.Parse([]byte(bitbucketYAML))
	lc := checkLC()
	rs, err := infra.Repo(infra.Inputs{LC: lc, Repo: "acme/app", Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	f.rs, f.image = rs, gcp.ImageName(rs.RegistryPath, f.slug, "app")
	return f
}

// TestLocalCheckNeverSubmits: locally the check only prints its decision,
// even when a trigger fires or it is forced.
func TestLocalCheckNeverSubmits(t *testing.T) {
	f := localCheck(t)
	out, stderr, err := execute(t, "image", "check")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if !strings.Contains(out, "app: rebuild") || !strings.Contains(out, imagecheck.ReasonNoRecord) || !strings.Contains(out, "fugaro image build") {
		t.Fatalf("out = %q", out)
	}
	out, _, err = execute(t, "image", "check", "--force", "--dry-run", "--json", "--workflow", "app")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Decisions []imagecheck.Decision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Decisions) != 1 || got.Decisions[0].Decision != imagecheck.Rebuild ||
		!slices.Equal(got.Decisions[0].Reasons, []string{imagecheck.ReasonForce, imagecheck.ReasonNoRecord}) {
		t.Fatalf("json = %s, %v", out, err)
	}
	if buildPosts(f.fb) != 0 {
		t.Fatalf("a local check submitted %d builds", buildPosts(f.fb))
	}
	if st := f.checkState(t, "app"); st != nil {
		t.Fatalf("a local check wrote check.json: %+v", st)
	}

	// With the record in place, nothing changed.
	f.seedRecord(t)
	out, _, err = execute(t, "image", "check")
	if err != nil || !strings.Contains(out, "app: skip") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// TestLocalCheckUsesTheSelectedProjectsLayer: the local check's own
// checkout config must resolve against the project --config (or
// --project/--gcp-project) actually selected, never a flag-blind
// re-selection (loadCheckoutConfig's own fallback, selectedProjectConfig,
// which looks at $FUGARO_PROJECT/$FUGARO_CONFIG and the "exactly one
// project config" default, none of which this test leaves anything for).
// A flag-blind lc=nil forces findLayer into its cache-only mode (decision
// L16's Lenient path) even though the real installation --config names is
// reachable, so a file with no workflows: that needs the project layer
// would wrongly refuse with "needs the project layer" instead of
// resolving it from the (reachable, never actually offline) bucket.
//
// Mutation (run, restore): change runImageCheckLocal's
// loadCheckoutResolved(ctx, "", lc, layerOptions{Lenient: true}) call to
// pass nil instead of lc, and this test fails: the command then refuses
// with "needs the project layer" before ever reaching originRepo.
func TestLocalCheckUsesTheSelectedProjectsLayer(t *testing.T) {
	f := newCloudFixture(t)
	publishedLayer(t, f, testProjectLayer)
	cfgPath := os.Getenv("FUGARO_CONFIG")
	f.appendConfig(t, "base_images: {web-node: "+checkBase+"}\n")
	layerCheckout(t, f, minimalAnchored)
	// A clean slate: isolateProjects clears $FUGARO_PROJECT/$FUGARO_CONFIG
	// and points XDG at an empty directory, and isolateCache gives the
	// layer cache a directory of its own, so loadCheckoutConfig's
	// flag-blind fallback has nothing to find by name, $FUGARO_*, or
	// "exactly one project config", and nothing cached either — only
	// --config below names the installation at all.
	isolateProjects(t, t.TempDir())
	isolateCache(t)
	_, _, err := execute(t, "image", "check", "--config", cfgPath)
	// The layer resolved fine (no "needs the project layer" refusal): the
	// run reaches past it to the next thing this checkout (acme/other, on
	// GitHub, layerCheckout's own origin) lacks, a GitHub App ID.
	if !strings.Contains(err.Error(), "no GitHub App ID") {
		t.Fatalf("err = %v, want it to fail past the project layer, not at it", err)
	}
}

func TestImageStatusJSON(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	rec := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "web", BuiltAt: now.Add(-30 * time.Hour), BuildID: "b0007",
		SourceCommit: "abc123", BaseRef: checkBase, BaseDigest: checkBaseDigest, Image: "img", ImageDigest: checkLatestDigest}
	data, _ := json.Marshal(rec)
	put := func(key string, data []byte) {
		path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(imagecheck.RecordKey(appSlug, "web"), data)
	at := now.Add(-2 * time.Hour)
	cs := imagecheck.CheckState{Version: 1, CheckedAt: now.Add(-time.Hour), Decision: imagecheck.RebuildFailedLast,
		Reasons: []string{imagecheck.ReasonBase}, BuildID: "b0008", LastBuildStatus: "FAILURE", LastBuildAt: &at}
	data, _ = cs.Marshal()
	put(imagecheck.CheckKey(appSlug, "web"), data)

	out, stderr, err := execute(t, "image", "status", "--json")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	var got imageStatusOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Workflows) != 1 {
		t.Fatalf("workflows = %+v", got.Workflows)
	}
	w := got.Workflows[0]
	if w.Repo != "acme/app" || w.Workflow != "web" || w.Image == nil || w.Image.SourceCommit != "abc123" || w.Image.BaseDigest != checkBaseDigest ||
		w.Image.AgeS < 30*3600 || !w.Image.BuiltAt.Equal(rec.BuiltAt) || w.Check == nil || w.Check.Decision != imagecheck.RebuildFailedLast ||
		!slices.Equal(w.Check.Reasons, []string{imagecheck.ReasonBase}) || w.Check.LastBuildStatus != "FAILURE" {
		t.Fatalf("status = %+v, image %+v, check %+v", w, w.Image, w.Check)
	}

	out, _, err = execute(t, "image", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acme/app web", "abc123", "30h", "rebuild-failed-last", "base", "FAILURE", "b0008"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}

	// --repo narrows to one repository; one that isn't set up is an error.
	if _, _, err := execute(t, "image", "status", "--repo", "acme/other"); ExitCode(err) != ExitUserError {
		t.Fatalf("unknown repo: %v", err)
	}
}

// TestCheckJobBacksOffAfterForcePushToOlderCommit: the branch is reset to
// an older commit. The rebuild succeeds but the gate keeps the newer
// record, so built-commit-gone still fires: the next night doesn't pay for
// the same build again, and says so at ERROR, until the inputs change or
// the check is forced.
func TestCheckJobBacksOffAfterForcePushToOlderCommit(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	testutil.WriteFiles(t, f.work, map[string]string{"src/a.ts": "export {}\n"})
	testutil.Git(t, f.work, "add", "-A")
	testutil.Git(t, f.work, "commit", "--quiet", "-m", "newer")
	testutil.Git(t, f.work, "push", "--quiet", "origin", "HEAD:main")
	f.seedRecord(t)
	testutil.Git(t, f.work, "reset", "--quiet", "--hard", "HEAD~1")
	testutil.Git(t, f.work, "push", "--quiet", "--force", "origin", "HEAD:main")

	// The fake build succeeds without touching the record, as a build the
	// gate superseded does.
	lines, err := runCheckJob(t)
	if err != nil || buildPosts(f.fb) != 1 || lines[0].Decision != imagecheck.Rebuild || !slices.Contains(lines[0].Reasons, imagecheck.ReasonBuiltCommitGone) {
		t.Fatalf("first night: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
	for night := 2; night <= 3; night++ {
		lines, err = runCheckJob(t)
		if err != nil || buildPosts(f.fb) != 1 || lines[0].Decision != imagecheck.RebuildFailedLast || lines[0].Severity != "ERROR" ||
			!slices.Contains(lines[0].Reasons, imagecheck.ReasonLastBuildIneffective) || lines[0].LastBuildStatus != "SUCCESS" {
			t.Fatalf("night %d: %+v, %v, %d builds", night, lines, err, buildPosts(f.fb))
		}
	}
	if lines, err = runCheckJob(t, "--force"); err != nil || buildPosts(f.fb) != 2 || lines[0].Decision != imagecheck.Rebuild {
		t.Fatalf("forced: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
	testutil.WriteFiles(t, f.work, map[string]string{"src/b.ts": "export {}\n"})
	testutil.Git(t, f.work, "add", "-A")
	testutil.Git(t, f.work, "commit", "--quiet", "-m", "new commit")
	testutil.Git(t, f.work, "push", "--quiet", "origin", "HEAD:main")
	if lines, err = runCheckJob(t); err != nil || buildPosts(f.fb) != 3 || lines[0].Decision != imagecheck.Rebuild {
		t.Fatalf("new commit: %+v, %v, %d builds", lines, err, buildPosts(f.fb))
	}
}

// TestCheckFailedRebuildClearsAfterManualFix: once a manual build records
// an image that clears the triggers, the old failure no longer alerts.
func TestCheckFailedRebuildClearsAfterManualFix(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	f.fb.FailStep = "smoke"
	if _, err := runCheckJob(t); err != nil {
		t.Fatal(err)
	}
	f.seedRecord(t) // fugaro image build, run by hand, fixed it
	for night := 2; night <= 3; night++ {
		lines, err := runCheckJob(t)
		if err != nil || len(lines) != 1 || lines[0].Decision != imagecheck.Skip || lines[0].Severity != "INFO" {
			t.Fatalf("night %d: %+v, %v", night, lines, err)
		}
	}
}

// TestCheckJobRecordWriteFailureAfterSubmit: a build was submitted but
// check.json couldn't record it: the check fails, at ERROR, naming the
// build, so it isn't lost.
func TestCheckJobRecordWriteFailureAfterSubmit(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	key := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.CheckKey(f.slug, "app"))
	// Another writer creates check.json while the build is submitted.
	f.fb.Steps = map[string]func(string) error{"prep": func(string) error {
		if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
			return err
		}
		return os.WriteFile(key, []byte(`{"version":1,"decision":"skip"}`), 0o644)
	}}
	out, stderr, err := executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, %v (%s)", ExitCode(err), err, stderr)
	}
	lines := checkLines(t, out)
	if len(lines) != 1 || lines[0].Severity != "ERROR" || lines[0].Decision != imagecheck.CheckFailed || *lines[0].BuildID != "b0001" ||
		!strings.Contains(lines[0].Error, "submitted Cloud Build build b0001") || buildPosts(f.fb) != 1 {
		t.Fatalf("lines = %+v, %d builds", lines, buildPosts(f.fb))
	}
}

// TestCheckJobForbiddenWriteStaysExitRemoteError: the daily check job runs
// as the build account, the only writer of check.json, so a check.json
// write it is forbidden to make means that account's builds/ grant itself
// is broken (an infrastructure fault for an operator to fix with Terraform,
// not something "ask an operator to publish it" or fugaro init --operator
// addresses). The job's documented exit 2 for a failed check
// (imagecheck.go's "It exits 2 when the check itself failed") holds; the
// refusal text in the JSON log line names the build account's Terraform
// grant, not the operator role, so an on-call engineer is pointed at the
// right fix.
func TestCheckJobForbiddenWriteStaysExitRemoteError(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	gcs := gcpfake.NewGCS(t)
	gcs.DenyWrites(checkBucket, "builds/")
	checkOpenBucket = func(context.Context, string) (*blobx.Bucket, error) { return gcs.Bucket(t, checkBucket), nil }
	out, stderr, err := executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, %v (%s)", ExitCode(err), err, stderr)
	}
	lines := checkLines(t, out)
	if len(lines) != 1 || lines[0].Decision != imagecheck.CheckFailed ||
		!strings.Contains(lines[0].Error, "Terraform IAM condition") || !strings.Contains(lines[0].Error, "build service account") {
		t.Fatalf("lines = %+v", lines)
	}
	// The real fix is the Terraform grant, not adding a person as an
	// operator: the job never runs as a launcher asking to publish
	// something.
	if strings.Contains(lines[0].Error, "operator role") || strings.Contains(lines[0].Error, "fugaro init --operator") {
		t.Errorf("error points at the operator role, not the build account's Terraform grant: %s", lines[0].Error)
	}
	// "nothing was published" would be false here: the build did run.
	if strings.Contains(lines[0].Error, "nothing was published") {
		t.Errorf("error says nothing was published, but a build ran: %s", lines[0].Error)
	}
	if buildPosts(f.fb) != 1 {
		t.Fatalf("builds submitted = %d, want 1 (the build runs; only recording it is refused)", buildPosts(f.fb))
	}
}

// TestCheckJobUnreadableStateFailsSafe: check.json holds the back-off
// state, so an unreadable one stops the check (no build, ERROR, exit 2)
// and is left for a human, instead of being replaced.
func TestCheckJobUnreadableStateFailsSafe(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	key := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.CheckKey(f.slug, "app"))
	if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
	lines := checkLines(t, out)
	if len(lines) != 1 || lines[0].Severity != "ERROR" || lines[0].Decision != imagecheck.CheckFailed || !strings.Contains(lines[0].Error, "unreadable") {
		t.Fatalf("lines = %+v", lines)
	}
	if buildPosts(f.fb) != 0 {
		t.Fatal("a build was submitted without the back-off state")
	}
	if data, _ := os.ReadFile(key); string(data) != "{" {
		t.Fatalf("check.json was replaced: %q", data)
	}
}

// fakeRecordBucket makes the record bucket an in-memory one holding a
// record and a failed rebuild for acme/app web, and returns the URLs asked
// to open.
func fakeRecordBucket(t *testing.T) *[]string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	rec, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "web", BuiltAt: now.Add(-96 * time.Hour), BuildID: "b0007",
		SourceCommit: "abc123", BaseRef: checkBase, BaseDigest: checkBaseDigest, Image: "img", ImageDigest: checkLatestDigest})
	failedAt := now.Add(-20 * time.Hour)
	state := imagecheck.CheckState{Version: 1, CheckedAt: now.Add(-time.Hour), Decision: imagecheck.RebuildFailedLast, Reasons: []string{imagecheck.ReasonBase},
		BuildID: "b0008", LastBuildStatus: "FAILURE", LastBuildAt: &failedAt}
	cs, _ := state.Marshal()
	var opened []string
	prev := openRecordBucket
	openRecordBucket = func(ctx context.Context, u string) (*blobx.Bucket, error) {
		opened = append(opened, u)
		mem := blobx.Wrap(memblob.OpenBucket(nil))
		for key, data := range map[string][]byte{imagecheck.RecordKey(appSlug, "web"): rec, imagecheck.CheckKey(appSlug, "web"): cs} {
			if err := mem.WriteAll(ctx, key, data, nil); err != nil {
				return nil, err
			}
		}
		return mem, nil
	}
	t.Cleanup(func() { openRecordBucket = prev })
	return &opened
}

// With a bucket_url other than the runs bucket, image status and ls read
// the build record where builds write it: the runs bucket.
func TestImageStatusReadsTheRunsBucket(t *testing.T) {
	f := newCloudFixture(t)
	setBucketURL(t, f, "gs://other-bucket")
	opened := fakeRecordBucket(t)
	out, stderr, err := execute(t, "image", "status", "--json")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	var got imageStatusOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Workflows) != 1 || got.Workflows[0].Image == nil || got.Workflows[0].Image.SourceCommit != "abc123" {
		t.Fatalf("status = %s", out)
	}
	if !slices.Equal(*opened, []string{"gs://unused-bucket"}) {
		t.Fatalf("opened %q, want the runs bucket", *opened)
	}

	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	env := &cloudEnv{lc: lc, bucket: blobx.Wrap(memblob.OpenBucket(nil))}
	defer env.Close()
	w := imageWarnings(context.Background(), env, []string{appSlug}, time.Now().UTC())
	if len(w) != 1 || !strings.Contains(w[0], "the image rebuild of") {
		t.Fatalf("ls warnings = %q", w)
	}
}

// The check job reads the GCP project from FUGARO_GCP_PROJECT and the
// project's name from FUGARO_PROJECT; the old shape (a GCP ID under
// FUGARO_PROJECT alone) is refused, asking for init --repo.
func TestCheckJobReadsGCPProject(t *testing.T) {
	newCheckJob(t, checkFiles(), bitbucketYAML)
	e, err := readJobEnv()
	if err != nil || e.gcpProject != "proj-1234" || e.name != "aurora" {
		t.Fatalf("env = %+v, %v", e, err)
	}
	t.Setenv("FUGARO_GCP_PROJECT", "")
	t.Setenv("FUGARO_PROJECT", "proj-1234") // the pre-M9a job
	_, _, err = executeJob(t, "image", "check", "--job")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "FUGARO_GCP_PROJECT") || !strings.Contains(err.Error(), "fugaro init --repo") {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
	t.Setenv("FUGARO_GCP_PROJECT", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "")
	if _, err := readJobEnv(); err == nil || !strings.Contains(err.Error(), "FUGARO_PROJECT") {
		t.Fatalf("no project name: %v", err)
	}
}

// The job's spec carries the name from FUGARO_PROJECT into every job's env,
// as the installed jobs have it.
func TestCheckJobResolvesSpec(t *testing.T) {
	f := newCheckJob(t, checkFiles(), bitbucketYAML)
	e, err := readJobEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Parse([]byte(bitbucketYAML))
	rs, _, err := e.repoSpec(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check.Env["FUGARO_PROJECT"] != "aurora" || rs.Check.Env["FUGARO_GCP_PROJECT"] != "proj-1234" ||
		rs.Workflows["app"].Env["FUGARO_PROJECT"] != "aurora" || rs.Installation.ProjectName != "aurora" {
		t.Errorf("the spec's env = %v", rs.Check.Env)
	}
	if rs.RegistryPath != f.rs.RegistryPath || rs.BuildServiceAccountEmail != f.rs.BuildServiceAccountEmail {
		t.Errorf("registry %s, build account %s; the installed job names %s and %s", rs.RegistryPath, rs.BuildServiceAccountEmail, f.rs.RegistryPath, f.rs.BuildServiceAccountEmail)
	}
}

// The local check resolves the spec from the selected project config alone
// (no installation outputs), and reports the project.
func TestLocalCheckResolvesSpec(t *testing.T) {
	localCheck(t)
	out, stderr, err := execute(t, "image", "check", "--json", "--dry-run")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	var got struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Project != "aurora" {
		t.Fatalf("json = %s, %v", out, err)
	}
}

// A workflow of a base kind head's fugaro.yaml gained after the job was
// installed has no base image in the job's spec; the spec still resolves, so
// the job reports that workflow as not installed instead of failing them all.
func TestCheckJobResolvesSpecWithAKindAddedSinceInstall(t *testing.T) {
	newCheckJob(t, checkFiles(), bitbucketYAML)
	e, err := readJobEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse([]byte(bitbucketYAML + "  server: { base: java-services, commands: { build: make, test: make test } }\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	rs, _, err := e.repoSpec(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check == nil || rs.Check.Image == "" {
		t.Fatalf("check = %+v", rs.Check)
	}
	// The installed kind is still what the job was installed with.
	if want := e.spec.BaseImages["web-node"]; want != checkBase || e.spec.BaseImages["java-services"] != "" {
		t.Errorf("spec base images = %v", e.spec.BaseImages)
	}
}

// executeJob runs a command as the Cloud Run check job does: its environment
// names the job.
func executeJob(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("CLOUD_RUN_JOB", "fugaro-check")
	return execute(t, args...)
}
