//go:build unix

package runner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestOpenRootNoFollowDoesNotBlockOnAFIFO: the flags at this call site are
// O_RDONLY|O_NOFOLLOW|O_NONBLOCK, and the root is what refuses the link — so
// what O_NONBLOCK buys here is the other half of the open, the one a swapped-in
// FIFO would otherwise win: a FIFO opened read-only with no writer blocks until
// something opens the write end, which is forever for an agent that has already
// gone. With the flag the open returns at once and the caller's fstat is what
// refuses the file; without it this call never returns and the run hangs.
//
// Mutation: dropping O_NONBLOCK from openRootNoFollow makes this test fail on
// the timeout below, because the open never returns. The goroutine it leaves
// blocked is abandoned with the test process, which is the point of the case.
func TestOpenRootNoFollowDoesNotBlockOnAFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "agent.log"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	done := make(chan error, 1)
	go func() {
		f, err := openRootNoFollow(rt, "agent.log")
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		// A FIFO with no writer opens and hands back a handle; that it is not
		// a regular file is the caller's fstat's business, not this open's.
		if err != nil {
			t.Fatalf("opening a FIFO in the root: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openRootNoFollow blocked on a FIFO with no writer: O_NONBLOCK is what stops that")
	}
}

// The runner opens files that are not its own: the agent's followup.md and a
// saved session file under HOME, both of which an agent-side process can
// replace with a link between the check and the open. openNoFollow and
// openRootNoFollow are the guards for that window, and they are two different
// guards — the test's two halves say which is which.
//
// openNoFollow is a plain open with O_NOFOLLOW, so the flag itself is what
// refuses a link at the path. Mutation: its body to os.Open, and the link is
// followed and this half fails on "a symlink was followed".
//
// openRootNoFollow passes the same flags, but the symlink half of them is a
// pass-through there: os.Root resolves the path itself and, whatever flags the
// caller passes, refuses a link that leaves the root while following one that
// stays inside it (openfile_unix.go says so; measured on go1.27.1, an in-root
// relative link opens with and without O_NOFOLLOW). So what refuses a
// swapped-in link at that call site is **the root, not the flag** — the link
// below is relative and points out of the root for exactly that reason, and
// mutating only the flags would not fail this half. Its mutation is the body
// to os.OpenFile(filepath.Join(rt.Name(), name), os.O_RDONLY, 0): the join and
// the plain open have no root left to refuse the link, the target's bytes come
// back, and this half fails on "read outside the root".
func TestOpenNoFollowRefusesASymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("the agent's notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside the root"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "followup.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	f, err := openNoFollow(link)
	if err == nil {
		defer f.Close()
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		t.Fatalf("a symlink was followed: %q", buf[:n])
	}
	if f, err := openNoFollow(target); err != nil {
		t.Fatalf("opening a regular file: %v", err)
	} else {
		f.Close()
	}

	rt, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// Relative and out of the root: the case os.Root refuses, and the case the
	// root — not the flag — is what refuses.
	if err := os.Symlink(filepath.Join("..", filepath.Base(outside), "secret.md"), filepath.Join(dir, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if f, err := openRootNoFollow(rt, "escape.md"); err == nil {
		defer f.Close()
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		t.Fatalf("read outside the root: %q", buf[:n])
	}
	if f, err := openRootNoFollow(rt, "target.md"); err != nil {
		t.Fatalf("opening a regular file in the root: %v", err)
	} else {
		f.Close()
	}
}
