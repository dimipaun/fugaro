//go:build unix

package procgroup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitForFile waits for path to appear, and reports the wait as a failure when
// it does not: every use below is a child saying it is ready, and a test that
// never sees it has nothing to assert about.
func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// TestContextSIGTERMsTheWholeGroup: Run's first answer to an ended context is
// SIGTERM to the negative pid, not to the tracked process — so a child the
// command started is asked to stop too, and a daemon that traps the signal
// gets to shut itself down before the SIGKILL that follows. The Unix half of
// that pair is procgroup_unix.go's terminateGroup, and nothing else in this
// package's tests reaches a process other than the direct child.
//
// The child is a background subshell in the same group. It traps SIGTERM and
// writes a file, which is the only evidence that the signal — rather than the
// unconditional SIGKILL that reaps the group once the tracked process exits —
// is what stopped it. The parent's own trap waits for that file before it
// exits, so the reaping SIGKILL cannot win a race with the child's handler: if
// the child never sees SIGTERM, the parent's trap gives up after about four
// seconds and the assertion below fails on the missing file rather than the
// test hanging.
//
// Mutation: terminateGroup's body to syscall.Kill(cmd.Process.Pid, SIGTERM)
// (the direct child only — what procgroup_other.go does on Windows). The
// child's trap never runs, no file is written, and this test fails.
func TestContextSIGTERMsTheWholeGroup(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	parentTerm := filepath.Join(dir, "parent-term")
	childTerm := filepath.Join(dir, "child-term")
	script := `
trap 'i=0; while [ ! -f "$1/child-term" ] && [ $i -lt 40 ]; do i=$((i+1)); sleep 0.1; done; echo parent > "$1/parent-term"; exit 0' TERM
( trap 'echo child > "$1/child-term"; exit 0' TERM
  echo ready > "$1/ready"
  while :; do sleep 0.2; done ) &
wait
`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Cmd{Name: "sh", Args: []string{"-c", script, "sh", dir}})
		done <- err
	}()

	// The child exists and has its trap installed before the context ends, so
	// what the signal does is never a question of timing.
	waitForFile(t, ready, 10*time.Second)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want the context's own error", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}
	for _, c := range []struct{ path, who string }{
		{parentTerm, "the command itself"},
		{childTerm, "the child it started, which is only in the group"},
	} {
		if _, err := os.Stat(c.path); err != nil {
			t.Errorf("%s was not stopped by SIGTERM: %v (terminateGroup must signal -pid, not pid)", c.who, err)
		}
	}
}
