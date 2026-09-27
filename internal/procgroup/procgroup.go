// Package procgroup runs a command in its own process group so that the whole
// tree, including background daemons it leaves behind, can be killed.
package procgroup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Cmd describes a command to run.
type Cmd struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string // nil means the current process environment
	Stdin  io.Reader
	Stdout io.Writer // nil discards
	Stderr io.Writer // nil discards
	// Grace is how long to wait, once ctx ends and the group has been sent
	// SIGTERM, before escalating to SIGKILL; zero means 10s.
	Grace time.Duration
}

// cmdWaitDelay bounds how long cmd.Wait spends draining exec's own internal
// stdin-copying goroutine once the tracked process has exited. Without it, a
// leaked descendant that keeps the read end of the stdin pipe open could
// block Wait indefinitely.
const cmdWaitDelay = 2 * time.Second

// outputDrainTimeout bounds how long Run waits, after the tracked process
// and everything left in its group have been killed, for the output-copying
// goroutines to notice their pipes closed and finish flushing into the
// caller's writers.
const outputDrainTimeout = 2 * time.Second

// Run starts c in a new process group and waits for it. As soon as the
// tracked process exits, everything it left behind in its group is
// SIGKILLed, whether or not ctx ended the run. When ctx ends first, the
// group is sent SIGTERM, then — if it hasn't exited within Grace — SIGKILL,
// and Run returns ctx.Err(). A non-zero exit status is reported through the
// exit code, not err; a negative exit code means the process was killed by
// a signal rather than exiting on its own.
func Run(ctx context.Context, c Cmd) (int, error) {
	grace := c.Grace
	if grace == 0 {
		grace = 10 * time.Second
	}
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = cmdWaitDelay

	// Our own pipes, rather than exec's, so Wait returns when the command
	// exits even if a leaked child still holds the write ends.
	outR, outW, err := os.Pipe()
	if err != nil {
		return -1, fmt.Errorf("procgroup: create stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return -1, fmt.Errorf("procgroup: create stderr pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	startErr := cmd.Start()
	outW.Close()
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return -1, fmt.Errorf("procgroup: start: %w", startErr)
	}

	// If the caller passes the same writer for both Stdout and Stderr (a
	// common way to get combined output), the two copy goroutines below
	// would otherwise write to it concurrently. Serialize them with a
	// shared mutex. A nil writer, or two distinct writers, need no such
	// protection: each is touched by only one goroutine, or is io.Discard,
	// which is safe for concurrent use.
	stdoutW, stderrW := c.Stdout, c.Stderr
	if sameWriter(c.Stdout, c.Stderr) {
		var mu sync.Mutex
		stdoutW = syncWriter{mu: &mu, w: c.Stdout}
		stderrW = syncWriter{mu: &mu, w: c.Stderr}
	}

	var copies sync.WaitGroup
	copyErrs := make([]error, 2)
	for i, p := range []struct {
		r *os.File
		w io.Writer
	}{{outR, stdoutW}, {errR, stderrW}} {
		copies.Add(1)
		go func(i int, r *os.File, w io.Writer) {
			defer copies.Done()
			if w == nil {
				w = io.Discard
			}
			if _, err := io.Copy(w, r); err != nil {
				copyErrs[i] = err
				// The caller's writer failed, but the child may still be
				// producing output; keep draining so it never blocks on a
				// full pipe waiting for a reader that has stopped reading.
				_, _ = io.Copy(io.Discard, r)
			}
		}(i, p.r, p.w)
	}

	pgid := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitErr, ctxErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		ctxErr = ctx.Err()
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case waitErr = <-waited:
		case <-time.After(grace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			waitErr = <-waited
		}
	}
	// Reap whatever the command left behind in its group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	copied := make(chan struct{})
	go func() { copies.Wait(); close(copied) }()
	select {
	case <-copied:
	case <-time.After(outputDrainTimeout):
		// A child escaped the group and still holds a pipe open. Run
		// returns anyway; the abandoned copy goroutines keep running and
		// may still write to the caller's Stdout/Stderr after this call
		// returns.
	}
	outR.Close()
	errR.Close()

	// code stays -1 if the process never started, was killed by a signal,
	// or hasn't produced a ProcessState for some other reason.
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctxErr != nil {
		return code, ctxErr
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return code, fmt.Errorf("procgroup: wait: %w", waitErr)
	}
	if copyErr := firstErr(copyErrs); copyErr != nil {
		return code, fmt.Errorf("procgroup: copy output: %w", copyErr)
	}
	return code, nil
}

// sameWriter reports whether a and b are the same non-nil writer. It never
// panics: comparing two interface values with == panics if their dynamic
// type is non-comparable (for example a struct holding a slice), so that
// case is treated as "not the same" instead of crashing.
func sameWriter(a, b io.Writer) (same bool) {
	if a == nil || b == nil {
		return false
	}
	defer func() { recover() }()
	return a == b
}

// syncWriter serializes writes to w with mu, so two goroutines can safely
// share it as the destination for both a command's stdout and stderr.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// firstErr returns the first non-nil error in errs, or nil.
func firstErr(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
