//go:build unix

package pluginwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFile is given paths that other tools also write, and what it must never
// do is read the bytes behind a link it was not given: replaceFile reads the
// file's current contents to compare them with what the caller read, so a link
// followed here reports contents the caller never saw, and the comparison that
// keeps a concurrent edit from being overwritten is made against another
// file's.
//
// This is the Unix half of the pair and it is the half that refuses: O_NOFOLLOW
// makes the open of the link itself fail. The Windows half cannot (see
// openfile_other.go) and its own weaker promise is not asserted here.
//
// Mutation (swap openNoFollow's body for os.Open): the link is followed, the
// target's bytes come back, and this test fails on "a symlink was followed"
// before the error it expects.
func TestReadFileDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"keep":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	data, _, exists, err := readFile(link, 1<<10)
	if err == nil {
		t.Fatalf("a symlink was followed: exists=%v data=%q", exists, data)
	}
	if data != nil {
		t.Fatalf("a refused read returned data: %q", data)
	}

	// The regular file itself, read through the same call, is unaffected.
	data, perm, exists, err := readFile(target, 1<<10)
	if err != nil || !exists || string(data) != `{"keep":true}` {
		t.Fatalf("regular file: %q, %v, %v, %v", data, perm, exists, err)
	}
	if perm != 0o600 {
		t.Errorf("perm = %04o, want 0600", perm)
	}

	// A missing file is not an error, which is what replaceFile's
	// "the file was not there before" path reads.
	switch _, _, exists, err := readFile(filepath.Join(dir, "absent.json"), 1<<10); {
	case err != nil || exists:
		t.Fatalf("missing file: exists=%v err=%v", exists, err)
	}
}

// The refusal names the path the caller named, like every other source
// failure: readFile's error is what a caller prints.
func TestReadFileRefusalNamesThePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := readFile(link, 1<<10)
	if err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("the refusal does not name the link: %v", err)
	}
}
