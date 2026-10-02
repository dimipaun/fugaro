//go:build docker

package images_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHistoryImageSmoke builds the history image the way init and CI do and
// runs images/smoke-history.sh against it.
func TestHistoryImageSmoke(t *testing.T) {
	const image = "fugaro-history:test"
	if out, err := exec.Command("sh", "build-base.sh", "history", image).CombinedOutput(); err != nil {
		t.Fatalf("build-base.sh history: %v\n%s", err, out)
	}
	out, err := exec.Command("sh", "smoke-history.sh", image).CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-history.sh: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "smoke-history: "+image+" ok") {
		t.Fatalf("smoke output:\n%s", out)
	}
}
