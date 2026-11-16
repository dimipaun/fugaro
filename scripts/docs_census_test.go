package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestDocsCensusScript runs scripts/docs-census_test.sh, which drives
// scripts/docs-census.sh against a fake `git` and a fake `go` on PATH: no case
// fetches a remote, adds a worktree or builds anything, so nothing here reaches
// the network or the tree the suite was started in. The suite is what pins that
// the census tests the merged tree rather than the caller's checkout, that a
// failure names the command and the tests go printed, and that a missing go or
// git skips instead of failing. scripts/land-prs_test.sh drives the same census
// through land-prs.sh.
func TestDocsCensusScript(t *testing.T) {
	for _, tool := range []string{"bash", "mktemp", "cp", "ln", "sed", "sort"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	cmd := exec.Command("bash", filepath.Join(testutil.ModuleRoot(), "scripts", "docs-census_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs-census_test.sh: %v\n%s", err, out)
	}
}
