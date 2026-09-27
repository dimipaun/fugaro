//go:build docker

package images_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestBaseImageSmoke runs images/smoke.sh, the check CI runs, against the
// base image built from this checkout. It also sets the pinned versions
// from the Dockerfile's ARGs, so smoke.sh checks the image's Claude Code,
// gh and Node against them, not just against the smoke output's shape.
func TestBaseImageSmoke(t *testing.T) {
	image := testutil.BaseImage(t)
	cmd := exec.Command("sh", "smoke.sh", image, "web-node")
	cmd.Env = append(os.Environ(),
		"CLAUDE_CODE_VERSION=2.1.283",
		"GH_VERSION=2.101.0",
		"NODE_VERSION=24",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke.sh: %v\n%s", err, out)
	}
	for _, want := range []string{"fugaro dev", "claude 2.1.283 (Claude Code)", "gh version 2.101.0", "node v24.", "smoke: " + image + " ok"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q:\n%s", want, out)
		}
	}
}
