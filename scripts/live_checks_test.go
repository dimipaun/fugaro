package scripts_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/procgroup"
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
// message would name the package and not the script that hung. Each script's
// test takes about five minutes (check91) and about eight and a half (check90,
// measured 503s on a Mac, 86s of it setup) alone, and the two run side by side;
// side by side the package took 854s on that Mac; eighteen minutes leaves room
// for that on a loaded machine and still fires inside CI's -timeout 20m, so a hang is reported by name rather than as
// a package-wide panic. Run `go test ./scripts` with -timeout 30m or more
// locally: Go's default 10-minute package timeout is shorter than this.
//
// The deadline is only worth having if it actually ends the run, and that is
// what procgroup.Run below is for: see the comment there.
const liveCheckTimeout = 18 * time.Minute

// syncBuffer collects a command's output and can be read while a copy
// goroutine may still be writing to it. procgroup.Run gives up on its drain
// after its own bound and returns anyway when something outside the killed
// group still holds the pipe, so the writer it was handed may be written to
// after Run has returned; a plain bytes.Buffer would then be read and written
// at the same time.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), liveCheckTimeout)
			defer cancel()
			path := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", c.test)
			// procgroup, not exec.CommandContext: both suites deliberately leave
			// background processes holding the output pipe (Check 90's proxy and
			// upstream, Check 91's sleeping fake gcloud and its interrupt tests),
			// and a context kill reaches the direct child only -- bash -- so
			// CombinedOutput would keep reading a pipe its grandchild still holds
			// and block past the deadline: the hang would then surface as Go's own
			// package timeout, a panic naming the package, which is the one thing
			// the deadline above exists to avoid. procgroup.Run puts the run in
			// its own process group, SIGTERMs the whole group when the deadline
			// fires (the scripts trap it and clean up after themselves), SIGKILLs
			// whatever is left once the grace period is over, bounds its own drain
			// -- so this returns, by script name, however the child misbehaves.
			var out syncBuffer
			code, err := procgroup.Run(ctx, procgroup.Cmd{
				Name:   "bash",
				Args:   []string{path},
				Stdout: &out,
				Stderr: &out,
			})
			if ctx.Err() != nil {
				t.Fatalf("%s did not finish within %s and was killed:\n%s", c.test, liveCheckTimeout, out.String())
			}
			if err != nil {
				t.Fatalf("%s: %v\n%s", c.test, err, out.String())
			}
			if code != 0 {
				t.Fatalf("%s exited %d:\n%s", c.test, code, out.String())
			}
		})
	}
}
