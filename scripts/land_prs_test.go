package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestLandPRsScript runs scripts/land-prs_test.sh, which drives
// scripts/land-prs.sh against a fake `gh` on PATH: no case reaches GitHub, no
// pull request is merged and no branch is deleted, and the fake counts the
// calls it is given so a case can say what the script did and did not do. The
// test needs python3 beside bash, because the script reads gh's JSON with it.
func TestLandPRsScript(t *testing.T) {
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	cmd := exec.Command("bash", filepath.Join(testutil.ModuleRoot(), "scripts", "land-prs_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("land-prs_test.sh: %v\n%s", err, out)
	}
}
