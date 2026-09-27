package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
shift # --rm
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
    echo "2.1.283 (Claude Code)" ;;
  "gh --version")
    [ "$missing" = gh ] && fail_missing gh
    printf 'gh version 2.101.0 (2026-09-15)\nhttps://github.com/cli/cli/releases/tag/v2.101.0\n' ;;
  "node -v")
    [ "$missing" = node ] && fail_missing node
    echo "v24.21.0" ;;
  "corepack --version")
    [ "$missing" = corepack ] && fail_missing corepack
    echo "0.36.0" ;;
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

// runSmoke runs smoke.sh IMAGE web-node with a fake docker on PATH, with
// missing naming a command that fake docker should report as absent (or ""
// for a fully healthy image).
func runSmoke(t *testing.T, missing string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "docker")
	if err := os.WriteFile(fake, []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "web-node")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
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

func TestSmokeFailsWhenATooIsMissing(t *testing.T) {
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
