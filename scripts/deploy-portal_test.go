package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestDeployPortalScript runs scripts/deploy-portal_test.sh, which drives
// scripts/deploy-portal.sh with node, npm, gcloud, firebase, git and go stubbed
// on PATH: nothing is built, deployed or reached over the network.
//
// The script itself is OWNER-RUN and refuses a coding agent's session, so
// nothing else in the tree runs it at all; this is what holds its refusals --
// the agent's session, a region the configuration does not name, a build with
// no Firebase configuration -- to a check rather than to a reading.
func TestDeployPortalScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	cmd := exec.Command(bash, filepath.Join(testutil.ModuleRoot(), "scripts", "deploy-portal_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deploy-portal_test.sh: %v\n%s", err, out)
	}
}
