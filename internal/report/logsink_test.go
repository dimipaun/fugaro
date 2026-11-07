package report

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// logNow is the clock every log test runs on: a fixed instant, so a name, a
// stamp and the age of a seeded file are all decided by the test rather than
// by the wall.
var logNow = time.Date(2026, 11, 7, 3, 41, 32, 0, time.UTC)

// logFixedNow is that instant as the injected clock.
func logFixedNow() time.Time { return logNow }

// logStateDir makes a state directory and the environment that names it, and
// returns both.
func logStateDir(t *testing.T) (string, func(string) string) {
	t.Helper()
	state := t.TempDir()
	return state, envOf(map[string]string{"XDG_STATE_HOME": state})
}

// logDirOf is where a state directory's logs go.
func logDirOf(state string) string { return filepath.Join(state, "fugaro", "logs") }

// seedLog writes a file that looks like an older log of cmd and returns its
// path. It is the only way a retention test puts a file in front of pruning.
func seedLog(t *testing.T, dir, cmd string, at time.Time, pid int) string {
	t.Helper()
	p := filepath.Join(dir, logName(cmd, at, pid))
	if err := os.WriteFile(p, []byte("kept from an earlier run\n"), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", p, err)
	}
	return p
}

// logNames lists the names in dir, for a test that counts what survived.
func logNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// logBody reads a sink's file, closing the sink first so the last partial
// line is in it.
func logBody(t *testing.T, sink Sink) string {
	t.Helper()
	if err := sink.Close(); err != nil {
		t.Fatalf("closing the log: %v", err)
	}
	b, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return string(b)
}

func TestLogPathFollowsUserDir(t *testing.T) {
	// D1351, F5: one directory rule, `<UserDir state>/fugaro/logs`. The
	// Windows row passes goos rather than reading runtime.GOOS, the same way
	// localcfg.UserDir's own test does.
	cases := []struct {
		name       string
		goos       string
		env        func(home string) map[string]string
		wantSuffix []string
	}{
		{
			name:       "XDG_STATE_HOME decides on every platform",
			goos:       "linux",
			env:        func(string) map[string]string { return map[string]string{"XDG_STATE_HOME": "$BASE"} },
			wantSuffix: []string{"fugaro", "logs"},
		},
		{
			name:       "the home directory decides where XDG_STATE_HOME is unset",
			goos:       "darwin",
			env:        func(string) map[string]string { return map[string]string{"HOME": "$BASE"} },
			wantSuffix: []string{".local", "state", "fugaro", "logs"},
		},
		{
			name:       "Windows uses %LOCALAPPDATA%",
			goos:       "windows",
			env:        func(string) map[string]string { return map[string]string{"LOCALAPPDATA": "$BASE"} },
			wantSuffix: []string{"fugaro", "logs"},
		},
		{
			// The state is Local, never Roaming: a log is not configuration,
			// and with no %LOCALAPPDATA% the profile's POSIX state directory
			// is what is left.
			name: "Windows never puts the state under %APPDATA%",
			goos: "windows",
			env: func(home string) map[string]string {
				return map[string]string{"APPDATA": filepath.Join(home, "roaming"), "USERPROFILE": "$BASE"}
			},
			wantSuffix: []string{".local", "state", "fugaro", "logs"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			m := map[string]string{}
			for k, v := range c.env(base) {
				m[k] = strings.ReplaceAll(v, "$BASE", base)
			}
			sink, err := OpenLog("update", envOf(m), c.goos, logFixedNow, nil)
			if err != nil {
				t.Fatalf("OpenLog: %v", err)
			}
			defer sink.Close()
			want := base
			for _, s := range c.wantSuffix {
				want = filepath.Join(want, s)
			}
			if got := filepath.Dir(sink.Path()); got != want {
				t.Errorf("the log is in %s, want %s", got, want)
			}
			// The name carries the UTC stamp and the pid, and it is the
			// command's own name first, because retention reads it back.
			wantName := "update-" + logNow.Format(logStampLayout) + "-" + strconv.Itoa(os.Getpid()) + ".log"
			if got := filepath.Base(sink.Path()); got != wantName {
				t.Errorf("the log is named %q, want %q", got, wantName)
			}
			if _, err := os.Lstat(sink.Path()); err != nil {
				t.Errorf("the log's path names no file: %v", err)
			}
		})
	}
}

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

func TestRetention14Days(t *testing.T) {
	state, env := logStateDir(t)
	dir := logDirOf(state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := seedLog(t, dir, "update", logNow.Add(-15*24*time.Hour), 7)
	fresh := seedLog(t, dir, "update", logNow.Add(-13*24*time.Hour), 8)
	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Errorf("a log 15 days old survived: %v", err)
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Errorf("a log 13 days old was pruned: %v", err)
	}
}

func TestRetention50FilesPerCommand(t *testing.T) {
	state, env := logStateDir(t)
	dir := logDirOf(state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Sixty of this command's logs, all inside the 14 days, and three of
	// another command's, two of them older than any of them: the other
	// command's files are not this command's to count or to delete.
	for i := 1; i <= 60; i++ {
		seedLog(t, dir, "update", logNow.Add(-time.Duration(i)*time.Second), 100+i)
	}
	other := make([]string, 0, 3)
	for i, at := range []time.Duration{-30 * 24 * time.Hour, -20 * 24 * time.Hour, -time.Minute} {
		other = append(other, seedLog(t, dir, "image-refresh", logNow.Add(at), 200+i))
	}
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("not a log\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()

	update, imageRefresh := 0, 0
	for _, name := range logNames(t, dir) {
		switch {
		case strings.HasPrefix(name, "update-"):
			update++
		case strings.HasPrefix(name, "image-refresh-"):
			imageRefresh++
		}
	}
	if update != 50 {
		t.Errorf("%d `update` logs survive, want the newest 50", update)
	}
	if imageRefresh != 3 {
		t.Errorf("%d `image-refresh` logs survive, want all 3: retention is per command name", imageRefresh)
	}
	for _, p := range other {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("another command's log was pruned: %v", err)
		}
	}
	if _, err := os.Lstat(foreign); err != nil {
		t.Errorf("a file this sink did not create was pruned: %v", err)
	}
}

func TestRetentionNeverPrunesTheRunsOwnLog(t *testing.T) {
	// G5: the sink's Path is what the failure block prints, so the log the run
	// is writing is the one file retention may never remove. A clock that
	// moved backwards makes this run's stamp the oldest in the directory, and
	// without the run's own name being held back the 50-file rule deletes it
	// the moment it is created.
	state, env := logStateDir(t)
	dir := logDirOf(state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 60; i++ {
		seedLog(t, dir, "update", logNow.Add(time.Duration(i)*time.Minute), 100+i)
	}

	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("this run's own output\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sink.Path()); err != nil {
		t.Fatalf("the run's own log is not where Path says it is: %v", err)
	}
	body := logBody(t, sink)
	if !strings.Contains(body, "this run's own output") {
		t.Errorf("the run's own log does not hold the run's output:\n%q", body)
	}
	// The own log counts as the newest, so the rule still leaves 50 behind.
	update := 0
	for _, name := range logNames(t, dir) {
		if strings.HasPrefix(name, "update-") {
			update++
		}
	}
	if update != logKeepPerCommand {
		t.Errorf("%d `update` logs survive, want %d", update, logKeepPerCommand)
	}
}

func TestRetentionIgnoresSymlinksAndForeignFiles(t *testing.T) {
	// G6: a pruning error is swallowed and it never removes a file of another
	// command, a symlink, or a file the sink did not create.
	state, env := logStateDir(t)
	dir := logDirOf(state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("not mine to delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := logNow.Add(-30 * 24 * time.Hour)
	link := filepath.Join(dir, logName("update", old, 999))
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}
	subdir := filepath.Join(dir, logName("update", old, 998))
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	// One real old log, so the test can see that pruning did run.
	realold := seedLog(t, dir, "update", old, 997)

	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()

	if _, err := os.Lstat(realold); !os.IsNotExist(err) {
		t.Errorf("an old log of this command survived: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink named like an old log was removed (or replaced): %v %v", fi, err)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "not mine to delete\n" {
		t.Errorf("pruning reached through the symlink: %q %v", b, err)
	}
	if fi, err := os.Lstat(subdir); err != nil || !fi.IsDir() {
		t.Errorf("a directory named like an old log was removed: %v %v", fi, err)
	}
}

func TestRetentionMatchesTheExactNamePattern(t *testing.T) {
	// F6a: the pattern is `<this command>-<14 digits UTC>-<pid>.log`, matched
	// exactly. A prefix glob for `image` would delete `image-refresh-*`.
	state, env := logStateDir(t)
	dir := logDirOf(state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := logNow.Add(-30 * 24 * time.Hour)
	mine := seedLog(t, dir, "image", old, 11)
	theirs := []string{
		seedLog(t, dir, "image-refresh", old, 12),
		seedLog(t, dir, "image-build", old, 13),
		seedLog(t, dir, "image-refresh-catalog", old, 14),
	}
	// The same command's name, but not the shape: no stamp, fourteen digits
	// that are not a date, a non-numeric pid.
	for _, name := range []string{
		"image-old.log",
		"image-20261399000000-15.log",
		"image-20261010000000-latest.log",
		"image-2026101000000-16.log",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sink, err := OpenLog("image", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()

	if _, err := os.Lstat(mine); !os.IsNotExist(err) {
		t.Errorf("an old log of this command survived: %v", err)
	}
	for _, p := range theirs {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("another command's log was pruned by `image`: %v", err)
		}
	}
	for _, name := range []string{
		"image-old.log",
		"image-20261399000000-15.log",
		"image-20261010000000-latest.log",
		"image-2026101000000-16.log",
	} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was pruned, and it is not this command's log name: %v", name, err)
		}
	}
}

func TestRedactsBeforeWrite(t *testing.T) {
	_, env := logStateDir(t)
	secrets := []string{"s3cret-token-value"}
	sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return secrets })
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("terraform: creating the token s3cret-token-value now\n")); err != nil {
		t.Fatalf("writing the log: %v", err)
	}
	body := logBody(t, sink)
	if strings.Contains(body, "s3cret-token-value") {
		t.Errorf("a value the run holds reached the log:\n%s", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("the line was not redacted, it was dropped:\n%s", body)
	}
	if !strings.Contains(body, "terraform: creating the token") || !strings.Contains(body, "now") {
		t.Errorf("the rest of the line was lost:\n%s", body)
	}
}

func TestRedactsASecretSplitAcrossWrites(t *testing.T) {
	// F6: redaction is by line, so a value arriving in two halves is still one
	// value when it is replaced.
	_, env := logStateDir(t)
	sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return []string{"s3cret-token-value"} })
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("key: s3cret-tok")); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte("en-value\n")); err != nil {
		t.Fatal(err)
	}
	body := logBody(t, sink)
	if strings.Contains(body, "s3cret-token-value") || strings.Contains(body, "s3cret-tok") {
		t.Errorf("a value split across two writes reached the log:\n%s", body)
	}
	if !strings.HasPrefix(body, "key: [REDACTED]\n") {
		t.Errorf("the line is not the redacted one:\n%q", body)
	}
}

func TestRedactsTheLongerValueFirst(t *testing.T) {
	// F6: one value is often inside another — an account id inside the
	// connection string that holds it — and replacing the shorter one first
	// would redact its own part and leave the tail of the longer one in the
	// log, where the value it still is would never be looked for again.
	_, env := logStateDir(t)
	secrets := []string{"abc", "XXabcYYsecretlong"}
	sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return secrets })
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("v=XXabcYYsecretlong\n")); err != nil {
		t.Fatal(err)
	}
	body := logBody(t, sink)
	if strings.Contains(body, "secretlong") || strings.Contains(body, "abc") {
		t.Errorf("the shorter value's redaction left the rest of the longer one in the log:\n%q", body)
	}
	if !strings.HasPrefix(body, "v=[REDACTED]\n") {
		t.Errorf("the line is not the redacted one:\n%q", body)
	}
	// The order is the caller's: the slice is the converge's live slots, and
	// sorting it would be a change to state this package does not own.
	if secrets[0] != "abc" || secrets[1] != "XXabcYYsecretlong" {
		t.Errorf("the redactor reordered the caller's values: %q", secrets)
	}
}

func TestRedactsTheLineTheRunEndedIn(t *testing.T) {
	// F6: the last line of a run has no newline of its own, and it is the line
	// a failure most often ends in. Close is what writes it, so it is also the
	// one place a regression would put the raw buffer — and therefore a secret
	// — into a log that lives 14 days.
	_, env := logStateDir(t)
	sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return []string{"s3cret-token-value"} })
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("token: s3cret-token-value")); err != nil {
		t.Fatal(err)
	}
	body := logBody(t, sink)
	if strings.Contains(body, "s3cret-token-value") {
		t.Errorf("the value the run ended on reached the log:\n%q", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("the last line was not redacted, it was dropped:\n%q", body)
	}
	if !strings.Contains(body, "token: ") {
		t.Errorf("the rest of the last line was lost:\n%q", body)
	}
}

func TestSinkUsesTheHeldSecretsFilledAfterOpen(t *testing.T) {
	// F6: the values are read at each line, so the converge's live slots — a
	// slot the secrets stage fills later in the run — are read as they are
	// then, and an empty slot is not a value at all.
	_, env := logStateDir(t)
	held := make([]string, 3)
	sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return held })
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("before the stage\n")); err != nil {
		t.Fatal(err)
	}
	held[1] = "filled-later-value"
	if _, err := sink.Write([]byte("the key filled-later-value is set\n")); err != nil {
		t.Fatal(err)
	}
	body := logBody(t, sink)
	if !strings.HasPrefix(body, "before the stage\n") {
		t.Errorf("an empty slot was replaced as if it were a value:\n%q", body)
	}
	if strings.Contains(body, "filled-later-value") {
		t.Errorf("a value filled after the sink opened reached the log:\n%s", body)
	}
	if !strings.Contains(body, "the key [REDACTED] is set") {
		t.Errorf("the later line was not redacted:\n%s", body)
	}
}

func TestSinkWithoutASecretsSourceKeepsStageLines(t *testing.T) {
	_, env := logStateDir(t)
	sink, err := OpenLog("setup", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := sink.Write([]byte("[changed] the policy was published\n")); err != nil {
		t.Fatal(err)
	}
	body := logBody(t, sink)
	if body != "[changed] the policy was published\n" {
		t.Errorf("a sink with no secrets to redact changed its line:\n%q", body)
	}
}

func TestLogDirSymlinkOrForeignOwnerMeansNoLog(t *testing.T) {
	t.Run("the logs directory is a symlink", func(t *testing.T) {
		state, env := logStateDir(t)
		elsewhere := t.TempDir()
		if err := os.MkdirAll(filepath.Join(state, "fugaro"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, logDirOf(state)); err != nil {
			t.Skipf("this filesystem has no symlinks: %v", err)
		}
		sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
		if err == nil {
			t.Errorf("a symlinked logs directory was accepted")
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("the sink reports a path with no log: %v", sink)
		}
		if names := logNames(t, elsewhere); len(names) != 0 {
			t.Errorf("the log was written through the symlink: %v", names)
		}
	})

	t.Run("the logs directory belongs to another user", func(t *testing.T) {
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
	})

	t.Run("a file where the directory goes", func(t *testing.T) {
		state, env := logStateDir(t)
		if err := os.MkdirAll(filepath.Join(state, "fugaro"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logDirOf(state), []byte("in the way\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
		if err == nil {
			t.Errorf("a file in the logs directory's place was accepted")
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("the sink reports a path with no log: %v", sink)
		}
	})
}

func TestLogFileIsCreatedExclusively(t *testing.T) {
	// F6a: the name is predictable, so a file or a symlink already at it is
	// refused rather than followed or truncated.
	t.Run("a file is already there", func(t *testing.T) {
		state, env := logStateDir(t)
		dir := logDirOf(state)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		name := logName("update", logNow, os.Getpid())
		pre := filepath.Join(dir, name)
		if err := os.WriteFile(pre, []byte("an earlier run's log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
		if err == nil {
			t.Errorf("OpenLog reused a file that was already there")
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("the sink reports a path with no log: %v", sink)
		}
		if b, err := os.ReadFile(pre); err != nil || string(b) != "an earlier run's log\n" {
			t.Errorf("the file that was already there was written to: %q %v", b, err)
		}
	})

	t.Run("a symlink is already there", func(t *testing.T) {
		state, env := logStateDir(t)
		dir := logDirOf(state)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(t.TempDir(), "victim.txt")
		if err := os.WriteFile(victim, []byte("not this run's output\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		name := logName("update", logNow, os.Getpid())
		if err := os.Symlink(victim, filepath.Join(dir, name)); err != nil {
			t.Skipf("this filesystem has no symlinks: %v", err)
		}
		sink, err := OpenLog("update", env, "linux", logFixedNow, func() []string { return []string{"s3cret-token-value"} })
		if err == nil {
			t.Errorf("OpenLog followed a symlink at the log's name")
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("the sink reports a path with no log: %v", sink)
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "not this run's output\n" {
			t.Errorf("the log was written through the symlink: %q %v", b, err)
		}
	})
}

func TestUnwritableStateDirNeverFailsTheCaller(t *testing.T) {
	// G5: an agent's sandbox may have a read-only home. The command still
	// runs; it has no log.
	state := t.TempDir()
	ro := filepath.Join(state, "readonly")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if f, err := os.Create(filepath.Join(ro, "probe")); err == nil {
		f.Close()
		_ = os.Remove(filepath.Join(ro, "probe"))
		t.Skip("the directory is writable despite its mode (running as root)")
	}

	sink, err := OpenLog("update", envOf(map[string]string{"XDG_STATE_HOME": ro}), "linux", logFixedNow, nil)
	if err == nil {
		t.Errorf("OpenLog accepted a state directory it cannot write in")
	}
	if sink == nil {
		t.Fatalf("OpenLog returned no sink: the caller must be able to use one either way")
	}
	if sink.Path() != "" {
		t.Errorf("a sink with no log reports the path %q", sink.Path())
	}
	if n, err := sink.Write([]byte("[changed] the policy was published\n")); n != len("[changed] the policy was published\n") || err != nil {
		t.Errorf("writing to a sink with no log = %d, %v; want the length and no error", n, err)
	}
	if err := sink.Close(); err != nil {
		t.Errorf("closing a sink with no log: %v", err)
	}
}

func TestCommandNameIsValidated(t *testing.T) {
	// A command name is a path component and the first half of a file name, so
	// one rule decides both.
	long := strings.Repeat("a", 42) // one past the pattern's forty
	for _, name := range []string{"", "../x", "..", "update/../x", "Update", "update ", "9update", "update_log", "-update", ".", long} {
		_, env := logStateDir(t)
		sink, err := OpenLog(name, env, "linux", logFixedNow, nil)
		if err == nil {
			t.Errorf("OpenLog accepted the command name %q", name)
		}
		if sink == nil || sink.Path() != "" {
			t.Errorf("%q: the sink reports a path with no log: %v", name, sink)
		}
	}
	for _, name := range []string{"u", "update", "image-refresh", strings.Repeat("a", 41)} {
		_, env := logStateDir(t)
		sink, err := OpenLog(name, env, "linux", logFixedNow, nil)
		if err != nil {
			t.Errorf("OpenLog refused the command name %q: %v", name, err)
			continue
		}
		defer sink.Close()
		if got := filepath.Base(sink.Path()); got != name+"-"+logNow.Format(logStampLayout)+"-"+strconv.Itoa(os.Getpid())+".log" {
			t.Errorf("the log of %q is named %q", name, got)
		}
	}
}

func TestLogNameIsReadBackByItsOwnPattern(t *testing.T) {
	// The writer and the pruner are one rule: every name this sink writes,
	// the retention test must read back as this command's log.
	if _, ok := logStamp(logName("image", logNow, 42), "image"); !ok {
		t.Errorf("the sink's own name %q does not match the retention pattern", logName("image", logNow, 42))
	}
	pat := regexp.MustCompile(`^image-\d{14}-\d+\.log$`)
	if got := logName("image", logNow, 42); !pat.MatchString(got) {
		t.Errorf("the log's name is %q, want it to match %s", got, pat)
	}
}

func TestPruningNeverCostsTheRunItsLog(t *testing.T) {
	// G5, G6: every failure on the pruning side is swallowed — the run gets
	// its log whatever the directory holds.
	state, env := logStateDir(t)
	dir := logDirOf(state)
	old := logNow.Add(-30 * 24 * time.Hour)
	// A non-empty directory in an old log's place: not a regular file, so not
	// this run's to delete, and nothing about it may stop the run's own log
	// from being created.
	inTheWay := filepath.Join(dir, logName("update", old, 3))
	if err := os.MkdirAll(filepath.Join(inTheWay, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()
	if sink.Path() == "" {
		t.Fatalf("a run with an entry it cannot prune got no log")
	}
	if fi, err := os.Lstat(inTheWay); err != nil || !fi.IsDir() {
		t.Errorf("the entry that was not the sink's to delete was removed anyway: %v %v", fi, err)
	}
	if _, err := sink.Write([]byte("[changed] the policy was published\n")); err != nil {
		t.Errorf("writing the log: %v", err)
	}
	// The other half of best effort: a directory that cannot be walked at all
	// is not a failure of the run that asked for it.
	pruneLogs(filepath.Join(state, "gone"), "update", "update-20261107034132-1.log", logNow)
}

func TestLogSinkReportsItsPathAndEndsOnce(t *testing.T) {
	// Path is what a failure block prints, so it names the file that was
	// opened; and Close is what a caller's defer runs after its own explicit
	// one, so a second Close and a write after it are harmless.
	_, env := logStateDir(t)
	sink, err := OpenLog("update", env, "linux", logFixedNow, nil)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer sink.Close()
	p := sink.Path()
	if p == "" {
		t.Fatalf("a log that opened reports no path")
	}
	if _, err := os.Lstat(p); err != nil {
		t.Errorf("the reported path is not the file that was opened: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Errorf("closing the log: %v", err)
	}
	if _, err := sink.Write([]byte("after the close\n")); err != nil {
		t.Errorf("writing to a closed log: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Errorf("closing a closed log: %v", err)
	}
	if got := sink.Path(); got != p {
		t.Errorf("Path changed to %q after Close", got)
	}
}
