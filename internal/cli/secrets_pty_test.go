//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// ttyFixture is a pseudo-terminal: tty is what fugaro reads as stdin, and
// everything the terminal would display (its echo) collects from master.
type ttyFixture struct {
	master, tty *os.File
	mu          sync.Mutex
	shown       bytes.Buffer
	copied      chan struct{}
	abandoned   bool // a cancelled readSecret left a read pending
}

func newTTY(t *testing.T) *ttyFixture {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	f := &ttyFixture{master: master, tty: tty, copied: make(chan struct{})}
	go func() {
		defer close(f.copied)
		buf := make([]byte, 256)
		for {
			n, err := master.Read(buf)
			f.mu.Lock()
			f.shown.Write(buf[:n])
			f.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		// Closing a blocking fd waits for a read in flight, so first end
		// the one a cancel abandoned (in a real run it dies with the
		// process): feed it a line and wait until it has taken it.
		if f.abandoned {
			_, _ = master.WriteString("\n")
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
				n, err := unix.IoctlGetInt(int(tty.Fd()), ioctlInputQueue)
				if err != nil || n == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Error("the abandoned read never took its line")
					break
				}
			}
		}
		// Closing the tty then ends the master's read.
		tty.Close()
		select {
		case <-f.copied:
		case <-time.After(5 * time.Second):
			t.Error("the pty's reader did not stop")
		}
		master.Close()
	})
	return f
}

func (f *ttyFixture) echo(t *testing.T) bool {
	t.Helper()
	tio, err := unix.IoctlGetTermios(int(f.tty.Fd()), ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	return tio.Lflag&unix.ECHO != 0
}

// waitEcho waits until the terminal's ECHO flag is want.
func (f *ttyFixture) waitEcho(t *testing.T, want bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); f.echo(t) != want; {
		if time.Now().After(deadline) {
			t.Fatalf("ECHO never became %v", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *ttyFixture) displayed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown.String()
}

type readResult struct {
	value []byte
	err   error
}

func startRead(ctx context.Context, f *ttyFixture, prompt *bytes.Buffer, name string, multiline bool) chan readResult {
	done := make(chan readResult, 1)
	go func() {
		v, err := readSecret(ctx, f.tty, prompt, name, multiline)
		done <- readResult{v, err}
	}()
	return done
}

func waitRead(t *testing.T, done chan readResult) readResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("readSecret did not return")
		return readResult{}
	}
}

func TestReadSecretHiddenPrompt(t *testing.T) {
	f := newTTY(t)
	if !f.echo(t) {
		t.Fatal("a fresh pty has ECHO off")
	}
	var prompt bytes.Buffer
	done := startRead(context.Background(), f, &prompt, "claude-oauth-token", false)
	f.waitEcho(t, false) // off during the read
	if _, err := f.master.WriteString(tokenValue + "\n"); err != nil {
		t.Fatal(err)
	}
	r := waitRead(t, done)
	if r.err != nil || string(r.value) != tokenValue {
		t.Fatalf("readSecret = %q, %v", r.value, r.err)
	}
	f.waitEcho(t, true) // and back on after
	if !strings.Contains(prompt.String(), "Paste the value for claude-oauth-token (input hidden)") || strings.Contains(prompt.String(), "trailing newline") {
		t.Fatalf("prompt = %q", prompt.String())
	}
	time.Sleep(100 * time.Millisecond) // let any echo arrive
	if strings.Contains(f.displayed(), tokenValue) || strings.Contains(f.displayed(), "EXAMPLE") {
		t.Fatalf("the terminal echoed the value: %q", f.displayed())
	}
}

// TestReadSecretHiddenPromptCancelled: Ctrl-C cancels the command's context
// (main's signalContext); the read must return at once and leave the
// terminal echoing again.
func TestReadSecretHiddenPromptCancelled(t *testing.T) {
	f := newTTY(t)
	ctx, cancel := context.WithCancel(context.Background())
	var prompt bytes.Buffer
	done := startRead(ctx, f, &prompt, "claude-oauth-token", false)
	f.waitEcho(t, false)
	_, _ = f.master.WriteString("sk-partial") // typed, no Enter yet
	// Linux hands what the master writes to the line discipline from a
	// workqueue, after write returns. Cancelling at once can restore ECHO
	// before that worker has seen the bytes, and the kernel then echoes them
	// (CI: "the terminal echoed the value"), which a person's keystrokes,
	// arriving long before their Ctrl-C, never see. Let the bytes arrive
	// while ECHO is still off.
	time.Sleep(200 * time.Millisecond)
	cancel()
	f.abandoned = true
	r := waitRead(t, done)
	if !errors.Is(r.err, errCancelled) || ExitCode(r.err) != ExitUserError || r.value != nil || strings.Contains(r.err.Error(), "partial") {
		t.Fatalf("readSecret = %q, %v", r.value, r.err)
	}
	if !f.echo(t) {
		t.Fatal("cancelling left the terminal with ECHO off")
	}
	if strings.Contains(f.displayed(), "partial") {
		t.Fatalf("the terminal echoed the value: %q", f.displayed())
	}
}

func TestReadSecretHiddenPromptRefusesMultiline(t *testing.T) {
	f := newTTY(t)
	var prompt bytes.Buffer
	r := waitRead(t, startRead(context.Background(), f, &prompt, "github-app-key", true))
	if ExitCode(r.err) != ExitUserError || !strings.Contains(r.err.Error(), "redirect it from a file") || prompt.Len() != 0 {
		t.Fatalf("readSecret = %v, prompt %q", r.err, prompt.String())
	}
	if !f.echo(t) {
		t.Fatal("ECHO turned off")
	}
}

// The hidden prompt itself is never entered for a name the project copy
// already covers: at a real terminal, with a line already waiting in stdin,
// the run finishes done without taking it, storing anything, or printing a
// prompt. (TestInitSecretsDoesNotPromptForProjectCoveredName asserts the same
// run's outcome at a faked terminal; this one is what rules out the read
// happening at all.) If the fix regressed, the line is read: the name is
// prompted for, validated and stored, so the test fails rather than blocking.
func TestInitSecretsNeverReadsStdinForProjectCoveredName(t *testing.T) {
	tty := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, tty.tty)
	r.e.lc.ProjectSecrets = map[string]config.ProjectSecret{
		"bitbucket-token":    {AllowDataTo: []string{"acme/app"}},
		"claude-oauth-token": {AllowDataTo: []string{"acme/app"}},
	}
	for _, name := range []string{"bitbucket-token", "claude-oauth-token"} {
		r.store.seedProject(r.e.lc.Name, name, tokenValue)
	}
	// Lines are waiting: a prompt that were reached reads them (and, if the
	// fix regressed, stores them) instead of blocking, so a regression fails
	// this test rather than hanging it. More lines than names, since a
	// regression is free to prompt for more of them than this test knows.
	if _, err := tty.master.WriteString(strings.Repeat(tokenValue+"\n", 8)); err != nil {
		t.Fatal(err)
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatalf("%v (prompts %q)", err, r.errOut.String())
	}
	if got := stateOf(res, initflow.Secrets); got != initflow.Done {
		t.Fatalf("state = %q, want done", got)
	}
	if n := r.store.setCalls(); n != 0 {
		t.Fatalf("%d value(s) stored: the prompt was reached", n)
	}
	if s := r.errOut.String(); strings.Contains(s, "Paste the value") {
		t.Fatalf("a prompt was printed: %q", s)
	}
}
