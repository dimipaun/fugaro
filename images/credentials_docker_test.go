//go:build docker

package images_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCredentialsImageSmoke builds the credentials image the way CI does and
// runs images/smoke-credentials.sh against it: the binary runs as a non-root
// user in an image with no shell and nothing credential-shaped baked in, and
// the entrypoint Cloud Run runs refuses by name without the installation
// module's environment.
func TestCredentialsImageSmoke(t *testing.T) {
	const image = "fugaro-credentials:test"
	if out, err := exec.Command("sh", "build-base.sh", "credentials", image).CombinedOutput(); err != nil {
		t.Fatalf("build-base.sh credentials: %v\n%s", err, out)
	}
	out, err := exec.Command("sh", "smoke-credentials.sh", image).CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-credentials.sh: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "smoke-credentials: "+image+" ok") {
		t.Fatalf("smoke output:\n%s", out)
	}
	// The service image holds every model credential the installation has, so
	// its size is bounded and it is scanned for secrets: both are the
	// scripts the images workflow runs on the other legs.
	if out, err := exec.Command("sh", "size-budget.sh", image).CombinedOutput(); err != nil {
		t.Fatalf("size-budget.sh: %v\n%s", err, out)
	}
	if out, err := exec.Command("sh", "scan.sh", "--secrets", image).CombinedOutput(); err != nil {
		t.Fatalf("scan.sh --secrets: %v\n%s", err, out)
	}
}
