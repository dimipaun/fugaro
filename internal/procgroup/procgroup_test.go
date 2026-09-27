package procgroup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sh(script string) Cmd { return Cmd{Name: "sh", Args: []string{"-c", script}} }

func TestExitCode(t *testing.T) {
	code, err := Run(context.Background(), sh("exit 3"))
	if err != nil || code != 3 {
		t.Fatalf("code=%d err=%v, want 3 <nil>", code, err)
	}
}

func TestCapturesOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	c := sh("echo out; echo err >&2")
	c.Stdout, c.Stderr = &out, &errOut
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if out.String() != "out\n" || errOut.String() != "err\n" {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestContextKillsGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c := sh("sleep 30")
	c.Grace = 100 * time.Millisecond
	start := time.Now()
	_, err := Run(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run did not return promptly after the deadline")
	}
}

// A background process that inherits stdout (like a Gradle daemon) must
// neither keep Run waiting nor survive it.
func TestReapsLeakedDaemon(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	var out bytes.Buffer
	c := sh("sleep 30 & echo $! > " + pidFile + "; echo started")
	c.Stdout = &out
	start := time.Now()
	code, err := Run(context.Background(), c)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run waited for the leaked daemon")
	}
	if !strings.Contains(out.String(), "started") {
		t.Fatalf("stdout = %q", out.String())
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("leaked daemon %d is still alive", pid)
}

// errWriter always fails; a Stdout that never drains must not make Run wait
// for the child's output to be consumed.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("errWriter: boom") }

func TestWriterErrorReturnsPromptly(t *testing.T) {
	c := sh("head -c 1000000 /dev/zero")
	c.Stdout = errWriter{}
	start := time.Now()
	_, err := Run(context.Background(), c)
	if err == nil {
		t.Fatal("err = nil, want a wrapped writer error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run did not return promptly after a writer error")
	}
}

// TestSharedWriterForStdoutAndStderr must pass under -race: Stdout and
// Stderr are the same writer, so the two output-copy goroutines must not
// write to it concurrently.
func TestSharedWriterForStdoutAndStderr(t *testing.T) {
	var buf bytes.Buffer
	c := sh("echo out; echo err >&2")
	c.Stdout, c.Stderr = &buf, &buf
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "out") || !strings.Contains(buf.String(), "err") {
		t.Fatalf("combined output = %q", buf.String())
	}
}

// slowErrWriter's Write blocks for delay before returning an error. Used to
// keep the output-copy goroutine busy inside w.Write past Run's drain
// timeout, so Run reliably takes the outputDrainTimeout branch instead of
// the <-copied one, while the goroutine's write to its copyErrs slot still
// happens later — with no synchronization at all back to Run's own,
// earlier read of copyErrs. If that read weren't guarded to only happen on
// the <-copied path, it would race with this later write.
type slowErrWriter struct {
	delay time.Duration
}

func (w slowErrWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return 0, errors.New("slowErrWriter: boom")
}

// TestDrainTimeoutSkipsCopyErrors deterministically reaches the
// outputDrainTimeout branch, with no dependency on process-group escape,
// external tools, or timing luck: the writer's own delay (longer than
// outputDrainTimeout) guarantees the output-copy goroutine cannot finish
// before Run's drain-timeout fires, so `drained` is always false here.
//
// Run must therefore not read copyErrs on this path: proven by asserting
// err is nil (the copy goroutine's eventual error must be dropped, not
// surfaced) and by running under -race, which would otherwise catch the
// goroutine's later, unsynchronized write to copyErrs racing with an
// earlier unguarded read of it.
func TestDrainTimeoutSkipsCopyErrors(t *testing.T) {
	delay := outputDrainTimeout + time.Second
	c := sh("echo x")
	c.Stdout = slowErrWriter{delay: delay}

	start := time.Now()
	code, err := Run(context.Background(), c)
	elapsed := time.Since(start)

	if elapsed < outputDrainTimeout {
		t.Fatalf("elapsed = %v, want >= %v: the drain-timeout branch was not reached", elapsed, outputDrainTimeout)
	}
	if elapsed > outputDrainTimeout+2*time.Second {
		t.Fatalf("Run took too long to return: %v", elapsed)
	}
	if err != nil {
		t.Fatalf("err = %v, want nil: the copy goroutine had not written its "+
			"error yet when Run returned, and once the drain times out that "+
			"result can never be read safely, so it must be dropped", err)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0 (the tracked shell's own exit code)", code)
	}

	// Let the still-running copy goroutine actually finish — its write to
	// copyErrs is what would race with the read above if Run's guard were
	// missing — before this test returns, so a leaked goroutine or a race
	// report is attributed to this test rather than a later, unrelated one.
	time.Sleep(delay)
}
