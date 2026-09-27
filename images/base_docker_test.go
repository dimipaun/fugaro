//go:build docker

package images_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestBaseImageSmoke runs images/smoke.sh, the check CI runs, against the
// base image built from this checkout.
func TestBaseImageSmoke(t *testing.T) {
	image := testutil.BaseImage(t)
	out, err := exec.Command("sh", "smoke.sh", image, "web-node").CombinedOutput()
	if err != nil {
		t.Fatalf("smoke.sh: %v\n%s", err, out)
	}
	for _, want := range []string{"fugaro dev", "claude 2.1.283 (Claude Code)", "gh version 2.101.0", "node v24.", "smoke: " + image + " ok"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q:\n%s", want, out)
		}
	}
}
