package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// liveCheckScripts are the owner-run live checks' scripts and their tests.
// Each test drives its script against fake tools on PATH -- a fake `claude`
// and a fake upstream for Check 90, fake `gcloud`, `fugaro` and `curl` for
// Check 91 -- so no network, no cloud project and no credential is touched
// here, and no live step of either check is run by an agent.
var liveCheckScripts = []struct {
	name string
	test string
}{
	{"check90.sh", "check90_test.sh"},
	{"check91.sh", "check91_test.sh"},
}

// TestLiveCheckScripts runs the bash test of each live check.
func TestLiveCheckScripts(t *testing.T) {
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	for _, c := range liveCheckScripts {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", c.test)
			cmd := exec.Command("bash", path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", c.test, err, out)
			}
		})
	}
}
