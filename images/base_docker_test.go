//go:build docker

package images_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

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
