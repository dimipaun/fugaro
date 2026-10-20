//go:build unix

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

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
