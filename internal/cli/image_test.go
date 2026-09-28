package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/image"
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

// npmFiles is a web-node checkout with an npm lockfile and no dependencies.
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
	files["fugaro.yaml"] = strings.Replace(cliMinimalYAML, "base: web-node,", "base: web-node, dockerfile: .fugaro/app.Dockerfile,", 1)
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

// Controller ruling: `fugaro image render` is exempt from --json, because
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
	cmd.SetArgs(args)
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
	spec := `{"base":"web-node","repo_dir":"` + filepath.Join(t.TempDir(), "missing") + `","commit":"abc"}`
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

func TestImageBuildReportsConfigProblems(t *testing.T) {
	files := npmFiles()
	files["fugaro.yaml"] = strings.Replace(cliMinimalYAML, "github", "gitlab", 1)
	checkoutWith(t, files)
	_, _, err := execute(t, "image", "build", "--local", "--base", "fugaro-web-node:dev")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro.yaml has 1 problem(s)") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// bitbucketYAML is cliMinimalYAML on Bitbucket, with one workflow secret.
var bitbucketYAML = strings.Replace(strings.Replace(cliMinimalYAML, "provider: github", "provider: bitbucket", 1),
	"base: web-node,", "base: web-node, secrets: [{ name: npm-token, env: NPM_TOKEN }],", 1)

// cloudBuildCheckout is a Bitbucket checkout of acme/app whose origin is
// https, with a local config pointing at a Cloud Build fake. The local
// config's repos entry is switched to Bitbucket to match; registry and
// build.service_account are added unless bare.
func cloudBuildCheckout(t *testing.T, bare bool) (*gcpfake.Build, *cloudFixture) {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = bitbucketYAML
	dir := checkoutWith(t, files)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	fb := gcpfake.NewBuild(t)
	f := newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "provider: github", "provider: bitbucket", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !bare {
		f.appendConfig(t, "registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n")
	}
	return fb, f
}

func TestImageBuildCloud(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	const base = "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro-web-node:dev-abc"
	out, _, err := execute(t, "image", "build", "--json", "--base", base)
	if err != nil {
		t.Fatal(err)
	}
	var res gcp.BuildResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	slug := mustSlug("bitbucket", "acme/app")
	if res.Status != "SUCCESS" || res.Digest == "" || res.Image != gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/fugaro", slug, "app")+":latest" {
		t.Fatalf("result = %+v", res)
	}
	subs, _ := fb.Last()["substitutions"].(map[string]any)
	if subs["_REPO_URL"] != "https://bitbucket.org/acme/app.git" || subs["_FUGARO_BASE"] != base || subs["_WORKFLOW"] != "app" ||
		subs["_GIT_SECRET"] != "projects/proj-1234/secrets/"+gcp.SecretID(slug, "bitbucket-token")+"/versions/latest" ||
		subs["_GIT_USER"] != "x-token-auth" || subs["_SECRET_ENVS"] != "NPM_TOKEN" || subs["_BASE_BRANCH"] != "main" {
		t.Fatalf("substitutions = %v", subs)
	}
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/fugaro-build@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

func TestImageBuildCloudNoWait(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	out, _, err := execute(t, "image", "build", "--no-wait", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range fb.Requests() {
		if r.Method == "GET" {
			t.Errorf("--no-wait polled the build: %s", r.Path)
		}
	}
	if !strings.Contains(out, "submitted") {
		t.Errorf("output = %q", out)
	}
}

func TestImageBuildCloudGitHubNeedsM5(t *testing.T) {
	checkoutWith(t, npmFiles())
	fb := gcpfake.NewBuild(t)
	f := newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	f.appendConfig(t, "registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n")
	_, _, err := execute(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "M5") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a GitHub build reached Cloud Build")
	}
}

func TestImageBuildCloudNeedsRegistry(t *testing.T) {
	fb, f := cloudBuildCheckout(t, true)
	_, _, err := execute(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "registry") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	f.appendConfig(t, "registry: us-east5-docker.pkg.dev/proj-1234/fugaro\n")
	_, _, err = execute(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "build.service_account") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("an incomplete config reached Cloud Build")
	}
}

func TestImageBuildCloudRepoMustBeTheCheckout(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	_, _, err := execute(t, "image", "build", "--base", "b:1", "--repo", "acme/other")
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
	out, _, err := execute(t, "image", "build", "--json", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	var res gcp.BuildResult
	if json.Unmarshal([]byte(out), &res) != nil || res.Status != "FAILURE" || res.LogURL == "" {
		t.Fatalf("output = %s", out)
	}
}

// TestImageBuildCloudDigestUnknown: a SUCCESS without pushed-image results
// still built and pushed the tag, so it is a success, but the output says
// the digest is unknown rather than printing an empty one.
func TestImageBuildCloudDigestUnknown(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	fb.NoResults = true
	out, _, err := execute(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "@ ") || strings.HasSuffix(strings.TrimSpace(out), "@") || !strings.Contains(out, "digest unknown") {
		t.Errorf("output = %q", out)
	}
	out, _, err = execute(t, "image", "build", "--json", "--base", "b:1")
	var res gcp.BuildResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Status != "SUCCESS" || res.Digest != "" || strings.Contains(out, `"digest"`) {
		t.Errorf("json output = %s, %v", out, err)
	}
}
