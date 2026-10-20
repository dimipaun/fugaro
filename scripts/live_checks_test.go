package scripts_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

// liveCheckTimeout is each script's own deadline. The two are independent
// programs that share nothing but this test: one hanging fake must not decide
// the other's result, so they run CONCURRENTLY, each under its own deadline --
// and the deadline is here rather than left to the package's -timeout, whose
// message would name the package and not the script that hung.
const liveCheckTimeout = 20 * time.Minute

// TestLiveCheckScripts runs the bash test of each live check.
func TestLiveCheckScripts(t *testing.T) {
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	for _, c := range liveCheckScripts {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), liveCheckTimeout)
			defer cancel()
			path := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", c.test)
			out, err := exec.CommandContext(ctx, "bash", path).CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s did not finish within %s and was killed:\n%s", c.test, liveCheckTimeout, out)
			}
			if err != nil {
				t.Fatalf("%s: %v\n%s", c.test, err, out)
			}
		})
	}
}
