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
	out, _, err := execute(t, "config", "example")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(config.Example) {
		t.Fatal("config example does not print the embedded example")
	}
}
