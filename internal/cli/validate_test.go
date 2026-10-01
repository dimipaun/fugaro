package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

const cliMinimalYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fugaro.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateValid(t *testing.T) {
	out, _, err := execute(t, "validate", writeConfig(t, cliMinimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("output = %q", out)
	}
}

func TestValidateJSONProblems(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "github", "gitlab", 1))
	out, _, err := execute(t, "validate", "--json", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit code = %d, want %d (err %v)", ExitCode(err), ExitUserError, err)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.Valid || len(got.Problems) != 1 || got.Problems[0].Path != "git.provider" {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateChecksFiles(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "sh build.sh", "./build.sh", 1))
	out, _, err := execute(t, "validate", path)
	if err == nil || !strings.Contains(out, "workflows.app.commands.build: ./build.sh does not exist") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestConfigExample(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "config", "example")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(config.Example) {
		t.Fatal("config example does not print the embedded example")
	}
}

func TestValidateReportsCloudRunLimits(t *testing.T) {
	cfg := strings.Replace(cliMinimalYAML, "base: web-node,", "base: web-node, resources: { cpu: 3, memory: 4Gi },", 1)
	out, _, err := execute(t, "validate", "--json", writeConfig(t, cfg))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d (%v), output %s", ExitCode(err), err, out)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Problems) != 1 || got.Problems[0].Path != "workflows.app.resources.cpu" || !strings.Contains(got.Problems[0].Message, "Cloud Run") {
		t.Fatalf("problems = %+v", got.Problems)
	}
}

// With no project to select, the example carries the placeholder and says
// where the real name comes from.
func TestConfigExampleWithoutProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "config", "example")
	if err != nil || !strings.Contains(out, "# the Fugaro project; fugaro init --config-only writes your project config\nproject: example\n") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestConfigExampleUsesSelectedProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	writeProject(t, "aurora", "proj-1234")
	t.Chdir(t.TempDir())
	t.Setenv("FUGARO_PROJECT", "aurora")
	out, _, err := execute(t, "config", "example")
	if err != nil || !strings.Contains(out, "\nproject: aurora\n") || strings.Contains(out, "project: example") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, ps := config.Parse([]byte(out)); len(ps) > 0 {
		t.Fatalf("the example is invalid: %v", ps)
	}
	// Inside a checkout, the checkout's project selects.
	isolateProjects(t, t.TempDir())
	writeProject(t, "borealis", "proj-5678")
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: borealis\n")
	t.Chdir(root)
	if out, _, err = execute(t, "config", "example"); err != nil || !strings.Contains(out, "\nproject: borealis\n") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestValidateHintNamesProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	noProject := writeConfig(t, strings.Replace(cliMinimalYAML, "project: aurora\n", "", 1))
	out, _, err := execute(t, "validate", noProject)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "project: is required") || !strings.Contains(out, "fugaro config example") {
		t.Fatalf("generic hint: out = %q, err = %v", out, err)
	}
	writeProject(t, "aurora", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "aurora")
	out, _, err = execute(t, "validate", noProject)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "project: is required") || !strings.Contains(out, "add `project: aurora`") {
		t.Fatalf("named hint: out = %q, err = %v", out, err)
	}
	if out, _, err = execute(t, "validate", "--json", noProject); !strings.Contains(out, "add `project: aurora`") {
		t.Fatalf("json: out = %q, err = %v", out, err)
	}
}
