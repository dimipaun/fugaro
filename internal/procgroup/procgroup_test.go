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
