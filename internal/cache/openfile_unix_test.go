//go:build unix

package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openRootNoFollow reads inside its root and nowhere else: archive.go's walk
// and key.go's hashKeyFile both Lstat the name inside the root first, and this
// is the guard for what the walk can race with. The link here points outside
// the root, which is the case os.Root refuses.
//
// Mutation (openRootNoFollow's body to
// os.OpenFile(filepath.Join(rt.Name(), name), os.O_RDONLY, 0)): the join and
// the plain open have no root at all, the link out of the root is followed,
// the target's bytes come back, and this test fails on "read outside the
// root".
func TestOpenRootNoFollowRefusesALinkOutOfTheRoot(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.json"), []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	rt, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	f, err := openRootNoFollow(rt, "link.json")
	if err == nil {
		defer f.Close()
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		t.Fatalf("read outside the root: %q", buf[:n])
	}

	// The regular file beside it is read by the same call, so the refusal is
	// about the link and not about the root refusing everything.
	f, err = openRootNoFollow(rt, "key.json")
	if err != nil {
		t.Fatalf("opening a regular file in the root: %v", err)
	}
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	f.Close()
	if err != nil || !strings.HasPrefix(string(buf[:n]), `{"a":1}`) {
		t.Fatalf("regular file: %q, %v", buf[:n], err)
	}
}
