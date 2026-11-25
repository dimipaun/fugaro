package scripts_test

// The hermetic halves of Checks 870 and 871, the two owner-run live checks of
// docs/console-fixes/implementation-plan.md's T-CHK. They live at scripts/ root
// rather than under scripts/live-checks/ because that plan's Files line puts
// them there, so they are not in liveCheckScripts and nothing else in this
// repository runs them: without the driver below, the two `_test.sh` suites the
// plan names as their tests are never run, and a script that can never pass
// reads green everywhere.
//
// Each drives its check against fake tools on PATH: no cloud, no project, no
// run and no model is touched, and no step of either check's live half happens
// here. 870's fake answers the three reads with figures the case chooses, so the
// median, the bound and the refusals are all read off it. 871's adds a loopback
// HTTP server standing in for the GCS and Firestore REST APIs, so the seed, the
// wait for the walk and the cleanup run with no account and no bucket.

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func runCheckSuite(t *testing.T, suite string) {
	t.Helper()
	for _, tool := range []string{"bash", "mktemp", "python3", "perl", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	cmd := exec.Command("bash", filepath.Join(testutil.ModuleRoot(), "scripts", suite))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", suite, err, out)
	}
}

// TestCheck870Script runs scripts/check-870-run-index-timings_test.sh: a fast
// project that passes, a slow read that fails, a read that exits non-zero, an
// empty index and an unfinished one, a core build's graph stub, a checkout that
// joins no run, and the refusals (the owner's own installation, a coding agent's
// session, a traced shell, a bad argument).
func TestCheck870Script(t *testing.T) {
	runCheckSuite(t, "check-870-run-index-timings_test.sh")
}

// TestCheck871Script runs scripts/check-871-run-index-100x_test.sh: the seed, the
// walk it waits for and the cleanup, all against the loopback server, plus the
// same readings and refusals, and that the access token never reaches an argv.
func TestCheck871Script(t *testing.T) {
	runCheckSuite(t, "check-871-run-index-100x_test.sh")
}
