//go:build docker

package images_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestBaseImageSmoke runs images/smoke.sh, the check CI runs, against the
// base image built from this checkout. It also sets the pinned versions
// read from the Dockerfile's own ARGs, so smoke.sh checks the image's
// Claude Code, gh and Node against them, not just against the smoke
// output's shape.
func TestBaseImageSmoke(t *testing.T) {
	image := testutil.BaseImage(t)
	claudeCodeVersion := dockerfileARG(t, "CLAUDE_CODE_VERSION")
	ghVersion := dockerfileARG(t, "GH_VERSION")
	nodeVersion := dockerfileARG(t, "NODE_VERSION")
	cmd := exec.Command("sh", "smoke.sh", image, "web-node")
	cmd.Env = append(os.Environ(),
		"CLAUDE_CODE_VERSION="+claudeCodeVersion,
		"GH_VERSION="+ghVersion,
		"NODE_VERSION="+nodeVersion,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke.sh: %v\n%s", err, out)
	}
	for _, want := range []string{
		"fugaro dev",
		fmt.Sprintf("claude %s (Claude Code)", claudeCodeVersion),
		"gh version " + ghVersion,
		"node v" + nodeVersion + ".",
		"smoke: " + image + " ok",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q:\n%s", want, out)
		}
	}
}

// retiredKeyNode is a Node.js release signed by a key from the "Other keys
// used to sign some previous releases" list, not a current releaser's:
// v18.17.0's SHASUMS256.txt.sig was made by Danielle Adams's
// 74F12602B6F1C4E913FAA37AD3A89613643B6201 (checked with gpgv on
// 2026-09-27).
const retiredKeyNode = "18.17.0"

// TestInstallNodeUsesTheBakedKeyring runs install-node the way a derived
// build does for image.node, with every key source unreachable: the
// keyring baked into the base must be enough, including for a release
// signed by a retired key. Without that keyring, install-node must refuse
// rather than skip verification.
func TestInstallNodeUsesTheBakedKeyring(t *testing.T) {
	image := testutil.BaseImage(t)
	blocked := []string{"run", "--rm", "--user", "root"}
	for _, host := range []string{"keys.openpgp.org", "keyserver.ubuntu.com", "raw.githubusercontent.com", "github.com"} {
		blocked = append(blocked, "--add-host", host+":127.0.0.1")
	}
	blocked = append(blocked, image, "sh", "-c")
	out := testutil.Docker(t, append(blocked,
		"test -s /usr/local/lib/fugaro/nodejs.gpg && /usr/local/lib/fugaro/install-node "+retiredKeyNode+" >&2 && node -v")...)
	if !strings.HasSuffix(out, "v"+retiredKeyNode) {
		t.Fatalf("node -v after install-node %s = %q", retiredKeyNode, out)
	}
	cmd := exec.Command("docker", append(blocked,
		"rm /usr/local/lib/fugaro/nodejs.gpg && /usr/local/lib/fugaro/install-node "+retiredKeyNode)...)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "no Node.js release keyring") {
		t.Fatalf("install-node without the keyring: err=%v\n%s", err, out)
	}
}

// TestBaseImageHardening keeps D1's hardening on Debian 13: not root, no
// passwordless sudo, the expected setuid and setgid sets (sudo and su are
// still setuid here; every derived build strips them), ssh-keysign and
// ssh-agent stripped, no capability, tini as PID 1.
func TestBaseImageHardening(t *testing.T) {
	img := testutil.BaseImageOf(t, "base")
	if got := testutil.Docker(t, "run", "--rm", img, "id", "-u"); got != "1000" {
		t.Errorf("uid %s", got)
	}
	if out, err := exec.Command("docker", "run", "--rm", img, "sudo", "-n", "true").CombinedOutput(); err == nil {
		t.Errorf("passwordless sudo works: %s", out)
	}
	found := testutil.Docker(t, "run", "--rm", "--user", "0", img, "sh", "-c", "find / -xdev -perm /6000 -type f | sort")
	want := "/usr/bin/chage\n/usr/bin/chfn\n/usr/bin/chsh\n/usr/bin/expiry\n/usr/bin/gpasswd\n/usr/bin/mount\n/usr/bin/newgrp\n/usr/bin/passwd\n/usr/bin/su\n/usr/bin/sudo\n/usr/bin/umount\n/usr/sbin/unix_chkpwd"
	if found != want {
		t.Errorf("setuid/setgid files:\n%s\nwant:\n%s", found, want)
	}
	if caps := testutil.Docker(t, "run", "--rm", "--user", "0", img, "sh", "-c", "find / -xdev -type f -exec getcap {} + 2>/dev/null || true"); caps != "" {
		t.Errorf("file capabilities: %s", caps)
	}
	if pid1 := testutil.Docker(t, "run", "--rm", img, "sh", "-c", `tr "\000" " " </proc/1/cmdline`); !strings.HasPrefix(pid1, "/usr/bin/tini ") {
		t.Errorf("PID 1 is %q", pid1)
	}
}

// TestBaseImageTools checks that every tools.tsv row answers with no network
// and an empty HOME, as the CI smoke runs it, and that mise and the harness
// Node stay apart (no node on the bare base's PATH).
func TestBaseImageTools(t *testing.T) {
	img := testutil.BaseImageOf(t, "base")
	cmd := exec.Command("docker", "run", "--rm", "-i", "--network", "none", img, "fugaro", "image", "selftest")
	cmd.Stdin = strings.NewReader(`{"tools":"all","tools_only":true}`)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"passed":true`) {
		t.Fatalf("presence checks: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "--rm", img, "sh", "-c", "command -v node").CombinedOutput(); err == nil {
		t.Errorf("a node is on PATH in the bare base: %s", out)
	}
	if got := testutil.Docker(t, "run", "--rm", img, "mise", "settings", "get", "trusted_config_paths"); !strings.Contains(got, "/work/repo") {
		t.Errorf("mise does not trust /work/repo: %s", got)
	}
}

// TestBaseImageManagedSettingsDir checks that /etc/claude-code, where the
// runner writes Claude Code's managed settings, is an empty directory owned
// by the agent's user (uid 1000), and that the runner's user can write there.
func TestBaseImageManagedSettingsDir(t *testing.T) {
	image := testutil.BaseImage(t)
	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", image, "-c",
		`stat -c '%u:%g %a %F' /etc/claude-code && ls -A /etc/claude-code | wc -l && id -u &&
		 touch /etc/claude-code/probe && rm /etc/claude-code/probe && echo writable`).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	want := "1000:1000 755 directory\n0\n1000\nwritable\n"
	if string(out) != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}
