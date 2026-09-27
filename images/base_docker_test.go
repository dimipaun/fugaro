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
