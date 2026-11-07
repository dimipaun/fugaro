//go:build windows

package report

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLogLivesUnderLocalAppData is the Windows half of the log's location
// (D1351), checked on the platform itself: `%LOCALAPPDATA%\fugaro\logs`, never
// `%APPDATA%`, which is where the config goes.
//
// What is pinned is the path shape only. Windows has no POSIX mode to assert
// (`TestLogFileModeAndDirMode` and the ownership case are in
// logsink_unix_test.go: a mode there is what os.FileMode reports, and a
// directory's stat carries a uid), and logsink_other.go says what that half
// does instead, so neither is checked here.
func TestLogLivesUnderLocalAppData(t *testing.T) {
	local := t.TempDir()
	roaming := t.TempDir()
	env := func(k string) string {
		switch k {
		case "LOCALAPPDATA":
			return local
		case "APPDATA":
			return roaming
		}
		return ""
	}
	now := time.Date(2026, 11, 7, 3, 41, 32, 0, time.UTC)
	sink, err := OpenLog("update", env, "windows", func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()

	dir := filepath.Join(local, "fugaro", "logs")
	if got := filepath.Dir(sink.Path()); got != dir {
		t.Errorf("the log is in %q, want %q", got, dir)
	}
	if strings.HasPrefix(sink.Path(), roaming) {
		t.Errorf("the log is under %%APPDATA%%: %s", sink.Path())
	}
	name := filepath.Base(sink.Path())
	if !regexp.MustCompile(`^update-\d{14}-\d+\.log$`).MatchString(name) {
		t.Errorf("the log is named %q, want update-<14 digits>-<pid>.log", name)
	}
	if _, err := os.Stat(sink.Path()); err != nil {
		t.Errorf("the log's path names no file: %v", err)
	}
}

// TestLogRefusesANameWithAPathInIt is the one check the two platforms share
// and the one a Windows user is likeliest to hit: a value the CLI was handed
// is passed to OpenLog as a command name, and it never becomes a path.
func TestLogRefusesANameWithAPathInIt(t *testing.T) {
	env := func(k string) string {
		if k == "LOCALAPPDATA" {
			return t.TempDir()
		}
		return ""
	}
	for _, name := range []string{"..\\x", "C:\\Windows\\update", "update\\..\\..\\x"} {
		sink, err := OpenLog(name, env, "windows", time.Now, nil)
		if err == nil {
			t.Errorf("OpenLog accepted the command name %q", name)
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("%q: the sink reports a path with no log: %v", name, sink)
		}
	}
}
