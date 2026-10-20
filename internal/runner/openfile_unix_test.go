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
// openRootNoFollow are the guards for that window, and the Unix half refuses
// at the open (O_NOFOLLOW) rather than after a read.
//
// Mutation, one per half: openNoFollow's body to os.Open, and the link is
// followed and this test fails on "a symlink was followed"; openRootNoFollow's
// body to os.OpenFile(filepath.Join(rt.Name(), name), os.O_RDONLY, 0), and the
// open has no root left to refuse the link, so the second half fails the same
// way. (Mutating only openRootNoFollow's flags would not fail it: os.Root
// resolves the path itself, so what refuses the link here is the root, not the
// flag — openfile_unix.go's comment says so.)
func TestOpenNoFollowRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("the agent's notes"), 0o600); err != nil {
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
	// The link is inside the root, so the root alone allows it: only the
	// flag refuses it.
	if f, err := openRootNoFollow(rt, "followup.md"); err == nil {
		defer f.Close()
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		t.Fatalf("a symlink inside the root was followed: %q", buf[:n])
	}
	if f, err := openRootNoFollow(rt, "target.md"); err != nil {
		t.Fatalf("opening a regular file in the root: %v", err)
	} else {
		f.Close()
	}
}
