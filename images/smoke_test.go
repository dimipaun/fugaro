package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeDocker is a stand-in for `docker` that answers smoke.sh's `docker run
// --rm IMAGE cmd...` calls with canned, healthy output, so smoke.sh can be
// exercised without a real Docker daemon or image. Setting
// FAKE_DOCKER_MISSING to a command name (for example "claude") makes that
// one command behave like a container whose entrypoint can't find the
// binary, exactly what a broken image would do, so the test can check that
// smoke.sh actually notices and fails instead of silently continuing.
const fakeDockerScript = `#!/bin/sh
set -eu
shift # run
while [ "$1" != fake-image ]; do shift; done # --rm and any other flags
shift # IMAGE
cmd="$*"
missing=${FAKE_DOCKER_MISSING:-}
fail_missing() {
  echo "OCI runtime exec failed: exec: \"$1\": executable file not found in \$PATH" >&2
  exit 127
}
case "$cmd" in
  "fugaro version")
    [ "$missing" = fugaro ] && fail_missing fugaro
    echo "fugaro dev" ;;
  "claude --version")
    [ "$missing" = claude ] && fail_missing claude
    echo "$FAKE_CLAUDE_VERSION (Claude Code)" ;;
  "gh --version")
    [ "$missing" = gh ] && fail_missing gh
    printf 'gh version %s (2026-09-15)\nhttps://github.com/cli/cli/releases/tag/v%s\n' "$FAKE_GH_VERSION" "$FAKE_GH_VERSION" ;;
  "node -v")
    [ "$missing" = node ] && fail_missing node
    case "$FAKE_NODE_VERSION" in
      *.*.*) echo "v$FAKE_NODE_VERSION" ;;
      *) echo "v$FAKE_NODE_VERSION.21.0" ;;
    esac ;;
  "corepack --version")
    [ "$missing" = corepack ] && fail_missing corepack
    echo "0.36.0" ;;
  "go version")
    [ "$missing" = go ] && fail_missing go
    echo "go version go${FAKE_GO_VERSION:-0} linux/amd64" ;;
  "terraform version")
    [ "$missing" = terraform ] && fail_missing terraform
    printf 'Terraform v%s\non linux_amd64\n' "${FAKE_TERRAFORM_VERSION:-0}" ;;
  "make --version")
    [ "$missing" = make ] && fail_missing make
    echo "GNU Make 4.3" ;;
  "gcc --version")
    [ "$missing" = gcc ] && fail_missing gcc
    echo "gcc (Ubuntu) 13.3.0" ;;
  "go env GOTOOLCHAIN")
    echo "${FAKE_GOTOOLCHAIN:-local}" ;;
  "java -version")
    [ "$missing" = java ] && fail_missing java
    printf 'openjdk version "%s" 2026-07-21 LTS\nOpenJDK Runtime Environment Temurin-%s (build %s)\n' "${FAKE_JDK_VERSION:-0}" "${FAKE_JDK_VERSION:-0}" "${FAKE_JDK_VERSION:-0}" >&2 ;;
  "psql --version")
    [ "$missing" = psql ] && fail_missing psql
    echo "psql (PostgreSQL) ${FAKE_POSTGRES_VERSION:-0}.11 (Ubuntu)" ;;
  "redis-server --version")
    [ "$missing" = redis-server ] && fail_missing redis-server
    echo "Redis server v=7.0.15 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=x" ;;
  "sh -c "*"firebase --version"*)
    [ "$missing" = firebase ] && fail_missing firebase
    echo "${FAKE_FIREBASE_VERSION:-0}" ;;
  "sh -c "*"FIREBASE_EMULATORS_PATH"*)
    printf '%s\n' ${FAKE_EMULATOR_JARS:-firebase-database-emulator-v4.jar cloud-firestore-emulator-v1.jar cloud-storage-rules-runtime-v1.jar pubsub-emulator-0.8 dataconnect-emulator-3 ui-v1.15.0} ;;
  "bash -c "*"fugaro-services"*)
    [ "${FAKE_SERVICES_FAIL:-}" = 1 ] && { echo "fugaro-services: timed out" >&2; exit 1; }
    echo "services ok" ;;
  "id -un")
    echo "fugaro" ;;
  "pwd")
    echo "/work/repo" ;;
  "sh -c "*"proc/1/cmdline"*)
    echo "/usr/bin/tini fugaro exec " ;;
  "sudo -n true")
    exit 1 ;;
  "stat -c %a /work/creds")
    echo "700" ;;
  "sh -c "*"! -user fugaro"*)
    printf '' ;;
  "sh -c "*".git-credentials"*)
    exit 1 ;;
  "sh -c "*".claude.json"*)
    exit 1 ;;
  *)
    echo "fake-docker: unhandled command: $cmd" >&2
    exit 99 ;;
esac
`

// dockerfileARG reads name's default value from an `ARG name=value` line in
// images/web-node/Dockerfile, so tests check the image against the versions
// actually pinned there instead of a second, driftable literal.
func dockerfileARG(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("web-node/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(\S+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("web-node/Dockerfile has no ARG %s=... line", name)
	}
	return string(m[1])
}

// runSmoke runs smoke.sh IMAGE web-node with a fake docker on PATH, with
// missing naming a command that fake docker should report as absent (or ""
// for a fully healthy image). The fake image reports the versions pinned in
// the Dockerfile unless fakeEnv overrides FAKE_CLAUDE_VERSION,
// FAKE_GH_VERSION or FAKE_NODE_VERSION. The pins' own variables
// (CLAUDE_CODE_VERSION, GH_VERSION, NODE_VERSION) are never inherited, so
// smoke.sh reads them from the Dockerfile as it does in CI.
func runSmoke(t *testing.T, missing string, fakeEnv ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "docker")
	if err := os.WriteFile(fake, []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "web-node")
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "CLAUDE_CODE_VERSION", "GH_VERSION", "NODE_VERSION":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"),
		"FAKE_CLAUDE_VERSION="+dockerfileARG(t, "CLAUDE_CODE_VERSION"),
		"FAKE_GH_VERSION="+dockerfileARG(t, "GH_VERSION"),
		"FAKE_NODE_VERSION="+dockerfileARG(t, "NODE_VERSION"))
	cmd.Env = append(cmd.Env, fakeEnv...)
	if missing != "" {
		cmd.Env = append(cmd.Env, "FAKE_DOCKER_MISSING="+missing)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSmokeSucceedsAgainstAHealthyImage(t *testing.T) {
	out, err := runSmoke(t, "")
	if err != nil {
		t.Fatalf("smoke.sh: %v\n%s", err, out)
	}
	if !strings.Contains(out, "smoke: fake-image ok") {
		t.Errorf("output lacks the ok line:\n%s", out)
	}
}

func TestSmokeFailsWhenAToolIsMissing(t *testing.T) {
	for _, missing := range []string{"fugaro", "claude", "gh", "node"} {
		t.Run(missing, func(t *testing.T) {
			out, err := runSmoke(t, missing)
			if err == nil {
				t.Fatalf("smoke.sh exited 0 despite %s being missing:\n%s", missing, out)
			}
			if strings.Contains(out, "smoke: fake-image ok") {
				t.Errorf("smoke.sh printed ok despite %s being missing:\n%s", missing, out)
			}
			if !strings.Contains(out, "smoke: "+missing) {
				t.Errorf("output doesn't name the failing check (%s):\n%s", missing, out)
			}
		})
	}
}

// TestSmokeChecksTheDockerfilePinsByDefault: with none of the pin variables
// set, as in CI, smoke.sh still checks the image against the Dockerfile's
// ARG defaults, so a release build always has its pins asserted.
func TestSmokeChecksTheDockerfilePinsByDefault(t *testing.T) {
	for _, tc := range []struct{ fake, pin string }{
		{"FAKE_CLAUDE_VERSION=0.0.1", "CLAUDE_CODE_VERSION=" + dockerfileARG(t, "CLAUDE_CODE_VERSION")},
		{"FAKE_GH_VERSION=0.0.1", "GH_VERSION=" + dockerfileARG(t, "GH_VERSION")},
		{"FAKE_NODE_VERSION=7", "NODE_VERSION major=" + dockerfileARG(t, "NODE_VERSION")},
	} {
		t.Run(tc.pin, func(t *testing.T) {
			out, err := runSmoke(t, "", tc.fake)
			if err == nil || !strings.Contains(out, "not pinned "+tc.pin) {
				t.Fatalf("smoke.sh with %s: err=%v\n%s", tc.fake, err, out)
			}
		})
	}
}

// TestSmokeNodePinMajorOrExact: NODE_VERSION may pin a major (24), which
// matches any 24.x.y, or an exact release (24.19.0), which must match
// exactly.
func TestSmokeNodePinMajorOrExact(t *testing.T) {
	for _, tc := range []struct {
		pin, image string
		ok         bool
	}{
		{"24", "24.19.0", true},
		{"24", "240.1.0", false},
		{"24", "25.0.0", false},
		{"24.19.0", "24.19.0", true},
		{"24.19.0", "24.19.1", false},
		{"24.1", "24.19.0", false},
	} {
		t.Run(tc.pin+" vs "+tc.image, func(t *testing.T) {
			out, err := runSmoke(t, "", "NODE_VERSION="+tc.pin, "FAKE_NODE_VERSION="+tc.image)
			if ok := err == nil && strings.Contains(out, "smoke: fake-image ok"); ok != tc.ok {
				t.Fatalf("smoke.sh NODE_VERSION=%s against node v%s: err=%v, want ok=%t\n%s", tc.pin, tc.image, err, tc.ok, out)
			}
		})
	}
}
