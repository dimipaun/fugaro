package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var basePins = []string{"CLAUDE_CODE_VERSION", "GH_VERSION", "MISE_VERSION", "GCLOUD_VERSION", "DOCKER_CLI_VERSION",
	"YQ_VERSION", "CODEX_VERSION", "OPENCODE_VERSION", "GOOSE_VERSION", "CRUSH_VERSION", "HARNESS_NODE_VERSION"}

// runSmokeBase runs smoke.sh against the base with a fake docker whose image
// reports the Dockerfile's pins unless fakeEnv overrides one.
func runSmokeBase(t *testing.T, missing string, fakeEnv ...string) (string, error) {
	t.Helper()
	df := baseDockerfile(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "base")
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasSuffix(name, "_VERSION") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"))
	for _, pin := range basePins {
		fake := "FAKE_" + strings.TrimSuffix(pin, "_VERSION") + "_VERSION"
		if pin == "CLAUDE_CODE_VERSION" {
			fake = "FAKE_CLAUDE_VERSION"
		}
		cmd.Env = append(cmd.Env, fake+"="+argIn(t, df, pin))
	}
	cmd.Env = append(cmd.Env, fakeEnv...)
	if missing != "" {
		cmd.Env = append(cmd.Env, "FAKE_DOCKER_MISSING="+missing)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSmokeBaseSucceedsAgainstAHealthyImage(t *testing.T) {
	if out, err := runSmokeBase(t, ""); err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, out)
	}
}

func TestSmokeBaseChecksEveryPin(t *testing.T) {
	for _, fake := range []string{"FAKE_MISE_VERSION", "FAKE_GCLOUD_VERSION", "FAKE_DOCKER_CLI_VERSION", "FAKE_YQ_VERSION",
		"FAKE_CODEX_VERSION", "FAKE_OPENCODE_VERSION", "FAKE_GOOSE_VERSION", "FAKE_CRUSH_VERSION", "FAKE_HARNESS_NODE_VERSION"} {
		out, err := runSmokeBase(t, "", fake+"=0.0.1")
		if err == nil || !strings.Contains(out, "not pinned") {
			t.Errorf("%s=0.0.1 passed: %v\n%s", fake, err, out)
		}
	}
}

func TestSmokeBaseFailsWhenAToolIsMissing(t *testing.T) {
	for _, missing := range []string{"mise", "gcloud", "selftest"} {
		if out, err := runSmokeBase(t, missing); err == nil {
			t.Errorf("passed with %s missing:\n%s", missing, out)
		}
	}
}
