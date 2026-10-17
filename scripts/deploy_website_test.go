package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestDeployWebsiteScript runs scripts/deploy-website_test.sh, which drives
// scripts/deploy-website.sh with node, npm and firebase stubbed on PATH: no
// build, no network and no deploy happen here.
func TestDeployWebsiteScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	cmd := exec.Command(bash, filepath.Join(testutil.ModuleRoot(), "scripts", "deploy-website_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deploy-website_test.sh: %v\n%s", err, out)
	}
}
