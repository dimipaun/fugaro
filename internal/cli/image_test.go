package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// checkoutWith creates a remote holding files, clones it, and makes the
// clone the working directory.
func checkoutWith(t *testing.T, files map[string]string) string {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, files)
	parent := t.TempDir()
	dir := filepath.Join(parent, "app")
	testutil.Git(t, parent, "clone", "--quiet", remote, dir)
	t.Chdir(dir)
	return dir
}

// npmFiles is a Node checkout with an npm lockfile and no dependencies.
func npmFiles() map[string]string {
	return map[string]string{
		"fugaro.yaml":       cliMinimalYAML,
		"package.json":      `{"name":"app","private":true}`,
		"package-lock.json": `{"name":"app","lockfileVersion":3,"requires":true,"packages":{"":{"name":"app"}}}`,
	}
}

func TestImageRenderGenerated(t *testing.T) {
	checkoutWith(t, npmFiles())
	out, _, err := execute(t, "image", "render")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM ${FUGARO_BASE}", "# Dependency warm-up for package-lock.json.\nRUN npm ci\n", "finalize-checkout /work/repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output lacks %q:\n%s", want, out)
		}
	}
}

func TestImageRenderRepositoryDockerfile(t *testing.T) {
	const df = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN git clone \"$REPO_URL\" /work/repo\n"
	files := npmFiles()
	files["fugaro.yaml"] = strings.Replace(cliMinimalYAML, "{ commands:", "{ dockerfile: .fugaro/app.Dockerfile, commands:", 1)
	files[".fugaro/app.Dockerfile"] = df
	checkoutWith(t, files)
	out, stderr, err := execute(t, "image", "render")
	if err != nil {
		t.Fatal(err)
	}
	if out != df || !strings.Contains(stderr, "uses the repository Dockerfile .fugaro/app.Dockerfile") {
		t.Fatalf("stdout %q, stderr %q", out, stderr)
	}
}

func TestImageRenderNeedsCheckout(t *testing.T) {
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "image", "render")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not inside a git checkout") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestImageRenderReportsConfigProblems(t *testing.T) {
	files := npmFiles()
	files["fugaro.yaml"] = strings.Replace(cliMinimalYAML, "github", "gitlab", 1)
	checkoutWith(t, files)
	_, _, err := execute(t, "image", "render")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "git.provider: must be one of github, bitbucket") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// `fugaro image render` is exempt from --json, because
// its output is the Dockerfile itself. Its help text says so.
func TestImageRenderHelpNotesJSONExemption(t *testing.T) {
	cmd := newImageRenderCmd()
	if !strings.Contains(cmd.Long, "--json") {
		t.Fatalf("image render help does not mention the --json exemption: %q", cmd.Long)
	}
}

// executeStdin is execute with stdin.
func executeStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestImageSelftestCommand checks the stdin and stdout contract; the checks
// themselves are covered in internal/image.
func TestImageSelftestCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // keep the developer's own ~/.docker and ~/.npmrc out of it
	if _, _, err := executeStdin(t, "{not json", "image", "selftest"); err == nil || !strings.Contains(err.Error(), "reading the selftest spec") {
		t.Fatalf("err = %v", err)
	}
	spec := `{"base":"base","repo_dir":"` + filepath.Join(t.TempDir(), "missing") + `","commit":"abc"}`
	out, _, err := executeStdin(t, spec, "image", "selftest")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	var rep image.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not a report: %v\n%s", err, out)
	}
	for _, c := range rep.Checks {
		if c.Name == "checkout" && !c.OK && !rep.Passed {
			return
		}
	}
	t.Fatalf("want a failed checkout check, got %+v", rep)
}

func TestImageBuildLocalRejectsRepo(t *testing.T) {
	_, _, err := execute(t, "image", "build", "--local", "--repo", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--repo applies to Cloud Build") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestImageBuildDevBuildNeedsBase(t *testing.T) {
	checkoutWith(t, npmFiles())
	_, _, err := execute(t, "image", "build", "--local")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "development build") || !strings.Contains(err.Error(), "--base") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// cloneWorkflowYAML is cliMinimalYAML with app switched to checkout: clone.
var cloneWorkflowYAML = strings.Replace(cliMinimalYAML, "app: { ", "app: { checkout: clone, ", 1)

// TestImageBuildRefusesClone: a checkout: clone workflow has no
// per-repository image to build, --local or on Cloud Build.
//
// Mutations (run, restore): (1) in runImageBuild, remove the `if
// cfg.Workflows[name].Checkout == config.CheckoutClone` guard — the --local
// case then fails differently ("fugaro dev is a development build with no
// published base image", from image.BaseRef("", Version), not the
// clone-specific message). (2) the same removal in runImageBuildCloud — the
// cloud case fails the same way instead of refusing with the clone message.
func TestImageBuildRefusesClone(t *testing.T) {
	const want = "this workflow runs the base image (checkout: clone): there is nothing to build"
	if !strings.Contains(cloneWorkflowYAML, "checkout: clone") {
		t.Fatalf("cloneWorkflowYAML is not a clone workflow (cliMinimalYAML changed shape):\n%s", cloneWorkflowYAML)
	}
	checkoutWith(t, map[string]string{"fugaro.yaml": cloneWorkflowYAML})
	if _, _, err := execute(t, "image", "build", "--local"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
		t.Fatalf("--local: exit %d, err %v", ExitCode(err), err)
	}

	cloneBitbucket := strings.Replace(bitbucketYAML, "app: { ", "app: { checkout: clone, ", 1)
	if !strings.Contains(cloneBitbucket, "checkout: clone") {
		t.Fatalf("the Bitbucket fixture is not a clone workflow (bitbucketYAML changed shape):\n%s", cloneBitbucket)
	}
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cloneBitbucket})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	newCloudFixture(t)
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "provider: github", "provider: bitbucket", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeBuild(t, "image", "build"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
		t.Fatalf("cloud: exit %d, err %v", ExitCode(err), err)
	}
}

func TestImageBuildReportsConfigProblems(t *testing.T) {
	files := npmFiles()
	files["fugaro.yaml"] = strings.Replace(cliMinimalYAML, "github", "gitlab", 1)
	checkoutWith(t, files)
	_, _, err := execute(t, "image", "build", "--local", "--base", "fugaro-base:dev")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro.yaml has 1 problem(s)") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// bitbucketYAML is cliMinimalYAML on Bitbucket, with one workflow secret.
var bitbucketYAML = strings.Replace(strings.Replace(cliMinimalYAML, "provider: github", "provider: bitbucket", 1),
	"{ commands:", "{ secrets: [{ name: npm-token, env: NPM_TOKEN }], commands:", 1)

// cloudBuildCheckout is a Bitbucket checkout of acme/app whose origin is
// https, with a local config pointing at a Cloud Build fake. The local
// config's repos entry is switched to Bitbucket to match. The repository's
// image registry exists unless bare.
func cloudBuildCheckout(t *testing.T, bare bool) (*gcpfake.Build, *cloudFixture) {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = bitbucketYAML
	dir := checkoutWith(t, files)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	fb := gcpfake.NewBuild(t)
	sm := gcpfake.NewSecrets(t)
	f := newCloudFixture(t, "cloud_build: "+fb.URL+"/", "secret_manager: "+sm.URL+"/")
	f.sm = sm
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "provider: github", "provider: bitbucket", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !bare {
		fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(mustSlug("bitbucket", "acme/app")))
	}
	return fb, f
}

// cloudBuildSpec's GitSecretID is the parameter it is given, never
// rs.Secrets[ws.GitSecret] looked up inside again (N1): the repository's own
// copy is not what Readiness may have resolved the logical name to, so a
// regression to the old internal read would submit every build naming a
// secret ID the build account may not be granted.
func TestCloudBuildSpecUsesPassedGitSecretID(t *testing.T) {
	cfg, problems := config.Parse([]byte(bitbucketYAML))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	rs := infra.RepoSpec{Name: "acme/app", Slug: mustSlug("bitbucket", "acme/app"), Provider: "bitbucket",
		RepoURL:   "https://bitbucket.org/acme/app.git",
		Workflows: map[string]infra.WorkflowSpec{"app": {}},
		Secrets:   map[string]string{"bitbucket-token": "repo-scoped-id"}}
	spec, err := cloudBuildSpec(rs, cfg, "app", "b:1", "E2_HIGHCPU_8", "gs://runs", "project-wide-id")
	if err != nil {
		t.Fatal(err)
	}
	if spec.GitSecretID != "project-wide-id" {
		t.Fatalf("GitSecretID = %q, want the parameter project-wide-id (not the repository's own %q)", spec.GitSecretID, rs.Secrets["bitbucket-token"])
	}
}

// cloudBuildCheckoutWithProjectSecret is cloudBuildCheckout with
// bitbucket-token allow-listed project-wide for acme/app, the repository's
// own container present with no enabled version, the project copy holding
// one, and the build account granted member of it when granted is true.
// It returns the fixture, the fake's Cloud Build and the two secret IDs.
func cloudBuildCheckoutWithProjectSecret(t *testing.T, granted bool) (*cloudFixture, *gcpfake.Build, string, string) {
	t.Helper()
	fb, f := cloudBuildCheckout(t, false)
	f.appendConfig(t, "project_secrets:\n  bitbucket-token:\n    allow_data_to: [acme/app]\n")
	slug := mustSlug("bitbucket", "acme/app")
	repoID, projectID := gcp.SecretID(slug, "bitbucket-token"), gcp.ProjectSecretID("aurora", "bitbucket-token")
	f.sm.Seed(repoID, nil, nil) // created, no enabled version: a build cannot read it
	f.sm.Seed(projectID, nil, []byte("project-token"))
	if granted {
		sa := "serviceAccount:" + gcp.BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com"
		f.sm.SetPolicy(projectID, []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{sa}}})
	}
	return f, fb, repoID, projectID
}

// gitSecretVersionName is the availableSecrets versionName a build naming
// secret ID id sends.
func gitSecretVersionName(id string) string {
	return "projects/proj-1234/secrets/" + id + "/versions/latest"
}

// availableGitSecret is the credential versionName of the last request the
// fake Cloud Build saw.
func availableGitSecret(t *testing.T, fb *gcpfake.Build) string {
	t.Helper()
	secrets, _ := fb.Last()["availableSecrets"].(map[string]any)
	sm, _ := secrets["secretManager"].([]any)
	first, _ := sm[0].(map[string]any)
	s, _ := first["versionName"].(string)
	return s
}

// B1: a standalone fugaro image build resolves the provider credential's ID
// from live state (the repository's own copy has no version, so the project
// copy wins) and, when the build account's own grant already names that
// same ID, submits the build naming it -- no refusal.
func TestRunImageBuildCloudResolvesLiveAndMatchesGrant(t *testing.T) {
	_, fb, repoID, projectID := cloudBuildCheckoutWithProjectSecret(t, true)
	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatal(err)
	}
	if got := availableGitSecret(t, fb); got != gitSecretVersionName(projectID) {
		t.Fatalf("availableSecrets names %q, want the project-wide copy %q (the repository's own %q has no version)", got, projectID, repoID)
	}
}

// B1: a repository override stored after the last apply makes the live
// resolution prefer the repository's own ID while the build account's grant
// still names the project copy. The build is refused, naming both IDs,
// before anything is submitted (a build submitted on the wrong ID fails its
// clone at the end, long after it was billed).
func TestRunImageBuildCloudRefusesOnGrantMismatch(t *testing.T) {
	f, fb, repoID, projectID := cloudBuildCheckoutWithProjectSecret(t, true)
	// The override: a version stored for the repository's own copy after
	// the last apply, so the live resolution now prefers it -- while the
	// grant the apply fixed still names the project copy.
	f.sm.Seed(repoID, nil, []byte("repo-token"))
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), repoID) || !strings.Contains(err.Error(), projectID) || !strings.Contains(err.Error(), "update --repo") {
		t.Fatalf("exit %d, err %v: want a refusal naming both %s and %s and fugaro update --repo", ExitCode(err), err, repoID, projectID)
	}
	if len(fb.Requests()) != 0 {
		t.Errorf("a build was submitted anyway: %v", fb.Requests())
	}
}

// B1's third outcome, the one a naive "" == mismatch implementation would
// get wrong: nothing has ever granted the build account either candidate
// (the pre-first-apply state), but the repository's own copy does have an
// enabled version, so the build proceeds on it rather than being refused.
// Refusing here would block every repository's genuinely first build.
func TestRunImageBuildCloudProceedsWhenNothingGrantedYet(t *testing.T) {
	f, fb, repoID, _ := cloudBuildCheckoutWithProjectSecret(t, false)
	f.sm.Seed(repoID, nil, []byte("repo-token")) // the repository's own copy, versioned
	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatalf("a first build was refused: %v", err)
	}
	if got := availableGitSecret(t, fb); got != gitSecretVersionName(repoID) {
		t.Fatalf("availableSecrets names %q, want the live-resolved %q", got, repoID)
	}
}

func TestImageBuildCloud(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	const base = "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro-base:dev-abc"
	out, _, err := executeBuild(t, "image", "build", "--base", base)
	if err != nil {
		t.Fatal(err)
	}
	slug := mustSlug("bitbucket", "acme/app")
	image := gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(slug), slug, "app")
	if !strings.HasPrefix(out, "built "+image+"@sha256:") {
		t.Fatalf("output = %q", out)
	}
	subs, _ := fb.Last()["substitutions"].(map[string]any)
	if subs["_REPO_URL"] != "https://bitbucket.org/acme/app.git" || subs["_FUGARO_BASE"] != base || subs["_WORKFLOW"] != "app" ||
		subs["_GIT_USER"] != "x-token-auth" || subs["_SECRET_ENVS"] != "NPM_TOKEN" || subs["_BASE_BRANCH"] != "main" {
		t.Fatalf("substitutions = %v", subs)
	}
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(slug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

func TestImageBuildCloudNoWait(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	out, _, err := executeBuild(t, "image", "build", "--no-wait", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range fb.Requests() {
		if r.Method == "GET" && strings.Contains(r.Path, "/builds/") {
			t.Errorf("--no-wait polled the build: %s", r.Path)
		}
	}
	if !strings.Contains(out, "submitted") {
		t.Errorf("output = %q", out)
	}
}

func TestImageBuildCloudRepoMustBeTheCheckout(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1", "--repo", "acme/other")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a build for another repository reached Cloud Build")
	}
}

func TestImageBuildCloudFailureIsRemote(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	fb.Outcome = "FAILURE"
	_, errOut, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(errOut, "log: ") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, errOut)
	}
}

// TestImageBuildCloudDigestUnknown: a SUCCESS without pushed-image results
// still built and pushed the tag, so it is a success, but the output says
// the digest is unknown rather than printing an empty one.
func TestImageBuildCloudDigestUnknown(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	fb.NoResults = true
	out, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "@ ") || strings.HasSuffix(strings.TrimSpace(out), "@") || !strings.Contains(out, "digest unknown") {
		t.Errorf("output = %q", out)
	}
}

// The build always records to the runs bucket, the only bucket its account
// may write; ls and image status read it there too. A bucket_url off GCS
// (a test's local stand-in for the runs bucket) is read in its place, and
// any bucket_url other than the runs bucket gets a warning from the build.
func TestBuildRecordBucketAgreesWithReaders(t *testing.T) {
	for _, c := range []struct {
		bucketURL, read string
		note            bool
	}{
		{"", "gs://fugaro-runs-x", false},
		{"gs://fugaro-runs-x", "gs://fugaro-runs-x", false},
		{"gs://other-bucket", "gs://fugaro-runs-x", true},
		{"gs://other-bucket/prefix", "gs://fugaro-runs-x", true},
		{"file:///tmp/runs", "file:///tmp/runs", true},
	} {
		lc := &localcfg.Config{RunsBucket: "fugaro-runs-x", Bucket: c.bucketURL}
		if got := lc.RecordBucketURL(); got != "gs://fugaro-runs-x" {
			t.Errorf("bucket_url %q: the build records to %s, want the runs bucket", c.bucketURL, got)
		}
		if got := recordReadURL(lc); got != c.read {
			t.Errorf("bucket_url %q: ls and image status read %s, want %s", c.bucketURL, got, c.read)
		}
		if note := buildRecordNote(lc); (note != "") != c.note {
			t.Errorf("bucket_url %q: note %q, want one: %v", c.bucketURL, note, c.note)
		}
	}
}

// With a bucket_url other than the runs bucket, fugaro image build still
// sends the runs bucket as the record's bucket: the build account can't
// write anywhere else, so the gate would fail after a whole billable build.
func TestImageBuildRecordsToTheRunsBucket(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	setBucketURL(t, f, "gs://other-bucket")
	_, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	subs, _ := fb.Last()["substitutions"].(map[string]any)
	if subs["_BUCKET"] != "gs://unused-bucket" {
		t.Fatalf("_BUCKET = %v, want the runs bucket gs://unused-bucket", subs["_BUCKET"])
	}
	if !strings.Contains(stderr, "gs://unused-bucket") || !strings.Contains(stderr, "bucket_url") {
		t.Errorf("no warning about bucket_url: %q", stderr)
	}
}

// setBucketURL points the fixture's local config at bucket_url u, with fake
// application default credentials, which gocloud reads (but never uses) to
// open a gs:// bucket.
func setBucketURL(t *testing.T, f *cloudFixture, u string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "bucket_url: "+f.bucket, "bucket_url: "+u, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	adc := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	// A gs:// bucket can't be read here, so the project-name check (which
	// reads fugaro/project.json from it) is answered from its cache, as if
	// it had passed a moment ago.
	if err := localcfg.SaveNameCheck(os.Getenv, "aurora", localcfg.NameCheck{GCPProject: "proj-1234", RunsBucket: u, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}
