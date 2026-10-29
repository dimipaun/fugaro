package scripts_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
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

// liveCheckTimeout is each shard's own deadline. The scripts are independent
// programs that share nothing but this test: one hanging fake must not decide
// another's result, so every shard runs CONCURRENTLY under its own deadline --
// and the deadline is here rather than left to the package's -timeout, whose
// message would name the package and not the script that hung. Measured on a
// Mac with 3 shards per script (6 side by side): the package took 479s, the
// longest shard 478s (check91 shard2); unsharded it took 854s (check90 503s
// alone). Twelve minutes is 1.5x that, so a loaded machine has room, and it
// fires well inside CI's -timeout 20m, so a hang is reported by name rather
// than as a package-wide panic. Run `go test ./scripts` with -timeout 30m or
// more locally: Go's default 10-minute package timeout is too short.
//
// The deadline is only worth having if it actually ends the run, and that is
// what procgroup.Run below is for: see the comment there.
const liveCheckTimeout = 12 * time.Minute

// liveCheckShards is how many parallel subtests each script's tests are split
// into (CHECK_TEST_SHARD=i/n, read by the scripts' own t() helper): every n-th
// test goes to shard i, each in its own temp dir, so the wall time of the
// package is about the longest shard instead of the sum.
const liveCheckShards = 3

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
		for shard := 0; shard < liveCheckShards; shard++ {
			shardEnv := fmt.Sprintf("CHECK_TEST_SHARD=%d/%d", shard, liveCheckShards)
			t.Run(fmt.Sprintf("%s/shard%d", c.name, shard), func(t *testing.T) {
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
					Env:    append(os.Environ(), shardEnv),
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
}
