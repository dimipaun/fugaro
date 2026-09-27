// Package procgroup runs a command in its own process group so that the whole
// tree, including background daemons it leaves behind, can be killed.
package procgroup

import (
	"context"
	"errors"
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
	// Grace is how long to wait after SIGTERM before SIGKILL; zero means 10s.
	Grace time.Duration
}

// Run starts c in a new process group and waits for it. When ctx ends, the
// group gets SIGTERM, then SIGKILL after the grace period, and Run returns
// ctx.Err(). When the command exits, any processes it left in the group are
// killed. A non-zero exit status is reported through the exit code, not err.
func Run(ctx context.Context, c Cmd) (int, error) {
	grace := c.Grace
	if grace == 0 {
		grace = 10 * time.Second
	}
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Our own pipes, rather than exec's, so Wait returns when the command
	// exits even if a leaked child still holds the write ends.
	outR, outW, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return -1, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	startErr := cmd.Start()
	outW.Close()
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return -1, startErr
	}

	var copies sync.WaitGroup
	for _, p := range []struct {
		r *os.File
		w io.Writer
	}{{outR, c.Stdout}, {errR, c.Stderr}} {
		copies.Add(1)
		go func() {
			defer copies.Done()
			w := p.w
			if w == nil {
				w = io.Discard
			}
			_, _ = io.Copy(w, p.r)
		}()
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
	case <-time.After(2 * time.Second): // a child escaped the group and still holds a pipe
	}
	outR.Close()
	errR.Close()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctxErr != nil {
		return code, ctxErr
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return code, waitErr
	}
	return code, nil
}
