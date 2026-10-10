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
	cmd.Env = append(cmd.Env, "FAKE_DEBIAN_DIGEST="+argIn(t, df, "DEBIAN_DIGEST"))
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
	// codex is a harness-tier tool, yq a kit-tier tool (design base-image.md
	// section 6); selftest-passed-false is a selftest that exits 0 but
	// reports "passed":false, proving smoke.sh reads the report body and
	// doesn't trust the exit code alone.
	for _, missing := range []string{"mise", "gcloud", "selftest", "codex", "yq", "selftest-passed-false"} {
		if out, err := runSmokeBase(t, missing); err == nil {
			t.Errorf("passed with %s missing:\n%s", missing, out)
		}
	}
}

// TestSmokeBasePinsRequireExactVersionMatch: a pin must match the reported
// version exactly, not as a substring (1.2.3 must not accept 11.2.30 or
// 1.2.34, both of which contain "1.2.3").
func TestSmokeBasePinsRequireExactVersionMatch(t *testing.T) {
	for _, reported := range []string{"11.2.30", "1.2.34"} {
		out, err := runSmokeBase(t, "", "MISE_VERSION=1.2.3", "FAKE_MISE_VERSION="+reported)
		if err == nil || !strings.Contains(out, "not pinned") {
			t.Errorf("reported %s against pin 1.2.3: err=%v\n%s", reported, err, out)
		}
	}
	if out, err := runSmokeBase(t, "", "MISE_VERSION=1.2.3", "FAKE_MISE_VERSION=1.2.3"); err != nil {
		t.Errorf("an exact match failed: %v\n%s", err, out)
	}
}

// TestSmokeBaseChecksBaseJSONAgainstEveryPin: every one of base.json's 12
// fields (images/base/Dockerfile) is checked against its own Dockerfile
// pin, not just mise. FAKE_BASE_JSON_MISMATCH corrupts one field in the
// image's reported /etc/fugaro/base.json without touching that tool's own
// --version output, so a mismatch can only be caught by a check that
// actually reads base.json.
func TestSmokeBaseChecksBaseJSONAgainstEveryPin(t *testing.T) {
	for _, field := range []string{"debian", "mise", "claude_code", "gh", "yq", "gcloud",
		"docker_cli", "harness_node", "codex", "opencode", "goose", "crush"} {
		out, err := runSmokeBase(t, "", "FAKE_BASE_JSON_MISMATCH="+field)
		if err == nil {
			t.Errorf("a wrong %s in base.json passed:\n%s", field, out)
		}
		if !strings.Contains(out, "base.json") {
			t.Errorf("a wrong %s in base.json: the failure doesn't name base.json:\n%s", field, out)
		}
	}
}
