package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestTestTimeReportScript runs scripts/test-time-report_test.sh, which drives
// scripts/test-time-report.sh against fixture `go test -json` streams and a
// four-file tree in a temporary directory: no case runs a Go test, reads the
// repository's own test files or touches the cgroup, so nothing here reaches
// the network or the tree the suite was started in. The suite is what pins the
// tables the script prints -- the slowest tests with their file, the time per
// package, and the tests whose bodies hold a wall-clock wait, which is the
// figure the verify step's own length turns on.
func TestTestTimeReportScript(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "awk", "sort", "cut", "wc", "find", "xargs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	cmd := exec.Command("bash", filepath.Join(testutil.ModuleRoot(), "scripts", "test-time-report_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-time-report_test.sh: %v\n%s", err, out)
	}
}
