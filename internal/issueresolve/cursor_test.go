package issueresolve

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cursorEnv is an environment whose cache lives in dir, so nothing here
// touches the machine's real cache directory.
func cursorEnv(dir string) func(string) string {
	return func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
}

func cursorPathFor(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, "fugaro", "issue-cursor.json")
}

// TestCursorOrdersLeastRecentlyCheckedFirst pins the order that keeps a new
// issue from starving behind old, never-closing ones: a number never asked
// comes first, then the one asked longest ago, and the rest by number so a
// single pass is reproducible. Reading an issue does not reorder the file.
func TestCursorOrdersLeastRecentlyCheckedFirst(t *testing.T) {
	dir := t.TempDir()
	env := cursorEnv(dir)
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)

	c := LoadCursor(env)
	if err := c.Mark([]int{7, 9}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := c.Mark([]int{5}, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadCursor(env)
	got := reloaded.Order([]int{9, 5, 12, 7, 3})
	want := []int{3, 12, 5, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order = %v, want %v", got, want)
		}
	}

	// Order reads the cursor and changes nothing: a second call sees the
	// same file.
	if again := LoadCursor(env).Order([]int{9, 5, 12, 7, 3}); again[0] != 3 {
		t.Fatalf("Order changed the cursor: %v", again)
	}
}

// TestCursorCorruptFileIsEmpty pins that a cursor file that cannot be read
// - garbage, a directory, a symlink, a file with a key that is not an issue
// number - is no cursor at all, never an error and never a crash, and that
// the next Mark replaces it. The file it writes is private (0600).
func TestCursorCorruptFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	env := cursorEnv(dir)
	path := cursorPathFor(t, dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, content := range []string{"", "{", `{"checked":{"seven":"2026-11-08T18:00:00Z"}}`, "null"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got := LoadCursor(env).Order([]int{4, 2})
		if got[0] != 2 || got[1] != 4 {
			t.Errorf("Order with cursor %q = %v, want [2 4]: a cursor that is not readable is no cursor", content, got)
		}
	}

	if err := LoadCursor(env).Mark([]int{4}, time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Mark wrote no cursor over the corrupt one: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("cursor file mode = %o, want 600", perm)
	}
	if got := LoadCursor(env).Order([]int{4, 2}); got[0] != 2 {
		t.Errorf("Order after Mark = %v, want the unasked 2 first", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(env).Order([]int{4, 2}); got[0] != 2 {
		t.Errorf("Order with a symlinked cursor = %v: a symlink must not be followed", got)
	}
}
