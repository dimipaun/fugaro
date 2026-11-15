package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestCIHygieneScript runs scripts/ci-hygiene_test.sh, which drives
// scripts/ci-hygiene.sh in fixture repositories built in a temp directory: no
// case reads this tree, and nothing here touches the network.
func TestCIHygieneScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	cmd := exec.Command(bash, filepath.Join(testutil.ModuleRoot(), "scripts", "ci-hygiene_test.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-hygiene_test.sh: %v\n%s", err, out)
	}
}

// TestCIHygieneRunsCleanOnThisTree is the check itself, run against the tree it
// guards rather than against a fixture: the shard job runs this package, so a
// committed leftover of a local edit -- the `sed -i` backup files that turned
// main's CI red over 2026-11-10..13 -- fails here, in the pull request, and not
// only in the `hygiene` step of ci.yml, which the owner has to copy in first.
// Rules 2 and 3 report and do not fail, so only rule 1 can fail this test, and
// what the script prints (its report-only warnings included) is passed on.
func TestCIHygieneRunsCleanOnThisTree(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root := testutil.ModuleRoot()
	cmd := exec.Command(bash, filepath.Join(root, "scripts", "ci-hygiene.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scripts/ci-hygiene.sh refuses this tree: %v\n%s", err, out)
	}
}
