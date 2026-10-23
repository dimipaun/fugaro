//go:build unix

package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReadRepoFile's Lstat walk refuses a symlink that is there when it looks, and
// openNoFollow is what refuses one planted in the window after that look — the
// agent is not the only process in its container, and a recipe path is a path
// any of them can touch. The Unix half is the one that refuses at the open:
// O_NOFOLLOW fails the link itself rather than reading what it points at.
//
// Mutation (openNoFollow's body to os.Open): the link is followed, the bytes
// behind it come back, and this test fails on "a symlink was followed".
func TestOpenNoFollowRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(target, []byte("name: team\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
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
	if !strings.Contains(err.Error(), link) {
		t.Errorf("the refusal does not name the link: %v", err)
	}
	// The regular file is opened by the same call, so this is the flag's
	// doing and not a blanket refusal.
	f, err = openNoFollow(target)
	if err != nil {
		t.Fatalf("opening a regular file: %v", err)
	}
	f.Close()
	// A missing file stays fs.ErrNotExist, which ReadRepoFile answers as
	// "found is false" rather than as an error.
	if _, err := openNoFollow(filepath.Join(dir, "absent.yaml")); !os.IsNotExist(err) {
		t.Errorf("missing file: %v, want IsNotExist", err)
	}
}
