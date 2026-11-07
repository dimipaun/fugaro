//go:build unix

package report

import (
	"os"
	"testing"
)

// What is pinned here is the Unix half of F6a, and it cannot be asserted
// anywhere else: 0600 and 0700 are POSIX modes, and Windows' os.FileMode is
// not the ACL that decides access there; and the ownership rule reads a uid
// out of syscall.Stat_t, which Windows has no field for — logsink_other.go
// says what that half does instead of refusing. The rest of the sink's tests
// are platform-neutral and live in logsink_test.go.

func TestLogFileModeAndDirMode(t *testing.T) {
	// F6a: 0600 in a 0700 directory, and an existing one is tightened.
	t.Run("a directory this sink creates", func(t *testing.T) {
		state, env := logStateDir(t)
		sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
		if err != nil {
			t.Fatalf("OpenLog: %v", err)
		}
		defer sink.Close()
		fi, err := os.Lstat(sink.Path())
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("the log is mode %04o, want 0600: it is secrets-adjacent and kept for 14 days", got)
		}
		di, err := os.Lstat(logDirOf(state))
		if err != nil {
			t.Fatal(err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("the logs directory is mode %04o, want 0700", got)
		}
	})

	t.Run("an existing directory is tightened", func(t *testing.T) {
		state, env := logStateDir(t)
		dir := logDirOf(state)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
		if err != nil {
			t.Fatalf("OpenLog: %v", err)
		}
		defer sink.Close()
		di, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("an existing 0755 directory stayed %04o, want 0700", got)
		}
	})
}

// TestLogDirOwnedByAnotherUserMeansNoLog is the ownership half of F6a, split
// from the symlink cases in logsink_test.go because only Unix can run it: the
// check reads the uid out of the directory's stat, and dirOwnedByUser answers
// true on every other platform (logsink_other.go), so the refusal this asserts
// is one those platforms never make.
func TestLogDirOwnedByAnotherUserMeansNoLog(t *testing.T) {
	state, env := logStateDir(t)
	if err := os.MkdirAll(logDirOf(state), 0o700); err != nil {
		t.Fatal(err)
	}
	was := euid
	euid = func() int { return was() + 1 }
	t.Cleanup(func() { euid = was })
	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err == nil {
		t.Errorf("a directory owned by another user was accepted")
	}
	if sink == nil || sink.Path() != "" {
		t.Errorf("the sink reports a path with no log: %v", sink)
	}
}
