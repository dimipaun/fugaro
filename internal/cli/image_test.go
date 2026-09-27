package cli

import (
	"path/filepath"
	"strings"
	"testing"

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
