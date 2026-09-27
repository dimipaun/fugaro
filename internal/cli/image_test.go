package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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

func TestImageBuildNeedsLocal(t *testing.T) {
	_, _, err := execute(t, "image", "build")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "M4") || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
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
