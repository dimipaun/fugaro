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
// Check 91, fake `gcloud` and `fugaro` for Checks 111, 146 and 401, fake `gcloud`,
// `git`, `curl` and `fugaro` for Check 190, fake `gcloud`, `fugaro` and a fake
// Realtime Database behind a fake `curl` for Check 191, fake `gcloud`, `fugaro`
// and `curl` for Check 195 (a small project whose service and account appear and
// disappear, whose IAM policy names what was granted to it, and whose build ends
// the way its own step's line says), a fake `gcloud` that
// evaluates the conditions of Check 340's own IAM bindings for Check 340, and
// fake `gcloud`, `fugaro` and a fake credentials service, GitHub and Identity
// Toolkit behind a fake `curl` for Check 96 -- or, for Checks 145 and 180,
// against a fake Bitbucket Cloud on a loopback port, so no
// network, no cloud project and no credential is touched here, and no live
// step of any of them is run by an agent. Check 130's fake is a fake `curl`
// alone, answering the three anonymous GETs the check makes about a
// repository; its fake `gcloud` and `fugaro` log any call and fail, so the
// test can say the check never reaches a cloud tool. Check 300's fakes are a
// fake `gcloud`, a fake `fugaro` and a fake RTDB behind a fake `curl`, and
// Checks 303 and 304's fake is a fake `fugaro` alone -- 303's answers `doctor`,
// `version`, the daemon's `run`, the node's `state` and `events` and the
// installation's config, and 304's never performs a handoff, so a case can say
// the check stopped at the build's own verb help -- and Check 305's are a fake
// `fugaro`, a fake `gcloud` and a fake front door on a loopback port that mints
// the check's tokens and serves the state group's own documents, with a fake
// browser that opens the authorization URL the check prints and follows it to
// the check's own loopback, so both sign-ins run with no network. Check 361's
// are a fake `gcloud` that mints a token sentinel and a fake
// `curl` that answers the check's one read from files the test writes and
// records the request -- the URL, the headers, and the Authorization header
// of the `--config` file the check handed over -- so a case can say the token
// travelled through a 0600 file and never onto a command line or into the
// block. Check 401's fake `curl`
// serves the grants node from the case's own directory and its fake `fugaro`
// update PRETENDS to be the migration, so a case says what the converge did by
// writing files. Checks 410 to 412 drive the diagnosis of a real failed run
// against a fake `gcloud` (a project that is NOT_FOUND) and a fake `fugaro`
// whose update fails and leaves a log; 411's fake `fugaro` also writes the
// source record a machine without git would have fetched, and its PATH is built
// to hold no git at all, so the case says the check really ran with none; 412's
// fake `claude` is the client the check wraps, so the wrapper is the real one
// and the tool log is the child's own. Checks 500 and 501 drive a fake `gcloud`
// and a fake `fugaro` that logs every call with the directory it was made in, so
// a case says both what the check measured and that no image was built, no
// `fugaro update` was answered for the owner and no run was launched. Checks 520
// and 521 drive the same two fakes over the vision-model work: a fake `fugaro`
// whose `doctor --json` carries the `vision-coder` sentence the premise is read
// from, whose `run --json` launches the run the check follows, whose `runs ls
// --json` says whether it has ended, and whose `cancel` answers the command 521
// stops a run with -- against a fake `gcloud` that serves the run's record from
// the case's own directory, with a mode for each state a case bends: a build
// older than the release, a coder that reads images itself, a slot that is set,
// a run that never ends, a record that is another run's, and a refusal whose
// sentence has lost one of the parts an owner needs.
var liveCheckScripts = []struct {
	name string
	test string
}{
	{"check90.sh", "check90_test.sh"},
	{"check91.sh", "check91_test.sh"},
	{"check111.sh", "check111_test.sh"},
	{"check130.sh", "check130_test.sh"},
	{"check145.sh", "check145_test.sh"},
	{"check146.sh", "check146_test.sh"},
	{"check148.sh", "check148_test.sh"},
	{"check180.sh", "check180_test.sh"},
	{"check190.sh", "check190_test.sh"},
	{"check191.sh", "check191_test.sh"},
	{"check195.sh", "check195_test.sh"},
	{"check300.sh", "check300_test.sh"},
	{"check303.sh", "check303_test.sh"},
	{"check304.sh", "check304_test.sh"},
	{"check305.sh", "check305_test.sh"},
	{"check340.sh", "check340_test.sh"},
	{"check96.sh", "check96_test.sh"},
	{"check361.sh", "check361_test.sh"},
	{"check401.sh", "check401_test.sh"},
	{"check-410.sh", "check-410_test.sh"},
	{"check-500.sh", "check-500_test.sh"},
	{"check-501.sh", "check-501_test.sh"},
	{"check-411.sh", "check-411_test.sh"},
	{"check-412.sh", "check-412_test.sh"},
	{"check510.sh", "check510_test.sh"},
	{"check511.sh", "check511_test.sh"},
	{"check520.sh", "check520_test.sh"},
	{"check521.sh", "check521_test.sh"},
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
