package report

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// A run's log is the whole of what it printed, kept beside the one line per
// step the person sees (D1352). Three numbers decide it, and they are the
// owner's: how long a log lives, how many one command keeps, and the modes.
const (
	// logRetention is how long a log is kept: 14 days, the spec's first half.
	logRetention = 14 * 24 * time.Hour
	// logKeepPerCommand is how many logs one command name keeps: 50, the
	// spec's second half. It is per command name rather than one 50 for the
	// directory because commands differ in how much they log: a busy one
	// would spend a global budget and evict another command's log, and the
	// log a person wants after a failure is the one the command that failed
	// wrote — `fugaro update`, or the `run` whose failure block prints the
	// path. Counting per name bounds one command's appetite without letting
	// it reach another's (F6). The read commands are not the pressure here:
	// they write no log at all (D1352, T6).
	logKeepPerCommand = 50
	// logDirMode is the mode of the directory the logs live in: 0700, since
	// a log holds a run's full output and its neighbours have no business in
	// it.
	logDirMode fs.FileMode = 0o700
	// logFileMode is the mode of a log itself (F6a).
	logFileMode fs.FileMode = 0o600
	// logStampLayout is the timestamp in a log's name: fourteen digits, UTC.
	// Retention reads the name back, so the layout is one rule for the writer
	// and the pruner.
	logStampLayout = "20060102150405"
	// logSuffix ends every log's name.
	logSuffix = ".log"
)

// logCommandRE is what a command name may be: the first half of the file
// name and nothing that could be a path. A name outside it is refused before
// any directory is touched (`..` and `/` cannot reach the filesystem), and it
// is the same rule the retention pattern is built from.
var logCommandRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// euid is os.Geteuid as a variable, so the ownership check is testable
// without a second account — the same shape as internal/image's geteuid.
var euid = os.Geteuid

// OpenLog opens this run's log and prunes it back to the retention rule. The
// directory is `<localcfg.UserDir state>/fugaro/logs` (D1351): `$XDG_STATE_HOME`
// where it is set, else `~/.local/state` on macOS and Linux, else
// `%LOCALAPPDATA%` on Windows — the one directory rule the CLI has, which the
// spec's `~/.fugaro` would have been a fourth beside (F5). The whole path is
// resolved inside a root opened at the state directory, so no link along it,
// and nothing swapped in behind one, can put this run's log somewhere else.
//
// Every failure — no state directory, a directory the process does not own, a
// name already taken, a read-only home — is returned as an error and, with
// it, as a sink whose Path is empty, because a run with no log is still a
// run: the caller's command must not fail over its log (G5). The values
// secrets returns are the ones the run must never keep; they are read at each
// line, so a value the converge fills in after OpenLog still applies (F6),
// and a nil secrets keeps the stage's own lines only.
func OpenLog(cmdName string, getenv func(string) string, goos string, now func() time.Time, secrets func() []string) (Sink, error) {
	if !logCommandRE.MatchString(cmdName) {
		return noLog{}, fmt.Errorf("report: %q is not a command name, and a log is named after one", cmdName)
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if now == nil {
		now = time.Now
	}
	base, err := localcfg.UserDir(getenv, goos, localcfg.DirState)
	if err != nil {
		return noLog{}, fmt.Errorf("report: no state directory to keep %s's log in: %w", cmdName, err)
	}
	at := now().UTC()
	root, err := logRoot(base)
	if err != nil {
		return noLog{}, fmt.Errorf("report: no log for %s: %w", cmdName, err)
	}
	defer root.Close()
	dir, err := logDir(root)
	if err != nil {
		return noLog{}, fmt.Errorf("report: no log for %s: %w", cmdName, err)
	}
	name := logName(cmdName, at, os.Getpid())
	f, err := createLogFile(root, dir, name)
	if err != nil {
		return noLog{}, fmt.Errorf("report: no log for %s: %w", cmdName, err)
	}
	// After the open, so a failure to prune never costs the run its log, and
	// before anything is written, so a pruned file is never one this run is
	// still writing to.
	pruneLogs(root, dir, cmdName, name, at)
	return &logSink{f: f, path: filepath.Join(base, dir, name), red: NewRedactor(f, secrets)}, nil
}

// logName is a log's file name: `<command>-<14 digits UTC>-<pid>.log`. The
// pid is what keeps two runs of one command in the same second apart.
func logName(cmdName string, at time.Time, pid int) string {
	return cmdName + "-" + at.Format(logStampLayout) + "-" + strconv.Itoa(pid) + logSuffix
}

// logRoot is the state directory, created if it is not there, as a root:
// every name this package resolves is resolved inside it, so no link —
// wherever it sits along `<state>/fugaro/logs`, and whatever it is swapped
// for between two of these calls — can take the log outside the state
// directory (os.Root refuses a name with a component that leaves its root,
// and does not follow the last one at all). The state directory itself is not
// checked for a link or tightened: it is the one directory rule (D1351), read
// from the caller's own environment, it is shared with whatever else keeps
// state there, and where it points is the user's choice.
func logRoot(base string) (*os.Root, error) {
	if err := os.MkdirAll(base, logDirMode); err != nil {
		return nil, err
	}
	return os.OpenRoot(base)
}

// logDir returns the log directory's name inside root — `fugaro/logs` —
// creating it 0700 or tightening an existing one, and refusing both where the
// path to it is not one this process may write a log into.
//
// Every component is checked, not only the last: Lstat'ing `fugaro/logs`
// resolves a symlink at `fugaro` on the way, so a link there would decide
// where this run's log of its own secrets lands — the very thing the check
// exists to refuse (F6a).
func logDir(root *os.Root) (string, error) {
	dir := ""
	for _, part := range []string{"fugaro", "logs"} {
		dir = filepath.Join(dir, part)
		if err := checkLogDir(root, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// checkLogDir is one component of that name: created if it is not there,
// refused if what is there is not a directory this process may use.
//
// The path is attacker-influenced (an agent's session often points
// XDG_STATE_HOME at a temporary directory) and the file's name is
// predictable, so the directory is Lstat'ed first: a symlink is refused
// rather than followed, and so is a directory owned by another user, who
// would otherwise decide where this run's log of its own secrets lands (F6a).
func checkLogDir(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(name, logDirMode); err != nil {
			return err
		}
		// Mkdir applies the umask, so the mode is set once more rather than
		// assumed.
		if err := root.Chmod(name, logDirMode); err != nil {
			return err
		}
		return nil
	case err != nil:
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", name)
	}
	if !dirOwnedByUser(fi) {
		return fmt.Errorf("%s is not this user's directory", name)
	}
	// An existing directory is tightened only here, past both checks: the
	// Lstat above is what refuses a link at this name, and the chmod below is
	// not a second guard against one — root.Chmod follows a link that stays
	// inside the root (os/root_unix.go), so a name swapped for an in-state
	// link between the two calls has the link's target tightened instead.
	// What that can cost is bounded: the target is a directory inside the
	// state directory, and the log is created with O_EXCL, so it never follows
	// the swap itself.
	if fi.Mode().Perm() != logDirMode {
		_ = root.Chmod(name, logDirMode)
	}
	return nil
}

// createLogFile creates the log and never an existing one. The name is
// predictable, so what is already at it decides nothing: O_EXCL refuses a file
// or a symlink at the name outright, and the mode is 0600 from the first byte
// (F6a). O_EXCL is the guard that matters, not the root's O_NOFOLLOW: the root
// follows a link that stays inside it, and it is O_EXCL that turns that follow
// into a refusal (os/root_unix.go). On Windows the root is stricter and refuses
// any link at all (the note in internal/cache/openfile_other.go). One portable
// call, where a bare O_NOFOLLOW would need a second platform pair; and because
// the name is resolved inside the root, the create cannot land outside the
// state directory either.
func createLogFile(root *os.Root, dir, name string) (*os.File, error) {
	return root.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, logFileMode)
}

// pruneLogs deletes this command's logs that the retention rule has outlived:
// older than 14 days, or past the newest 50. It is best effort — a log
// directory it cannot read or a file it cannot remove leaves the run's own
// log in place and changes no exit code (G5, G6).
//
// keep is the log just created for this run, which is never a candidate: it
// is in the directory being read, and the clock may have moved backwards
// since the logs it is compared against were written, which would make it the
// oldest name here — the first thing the 50-file rule deletes, leaving Path()
// naming a file that is gone. It counts as the newest instead, so the rule
// still leaves 50 logs of this command behind.
//
// What it may remove is exactly the files named `<this command>-<14 digits
// UTC>-<pid>.log` that are regular files: not another command's logs (a
// prefix match would delete `image-refresh-*` for `image`), not a symlink,
// not a directory, and nothing outside the directory — every name it removes
// is one ReadDir handed back from it, resolved inside the same root the log
// was created in.
func pruneLogs(root *os.Root, dir, cmdName, keep string, now time.Time) {
	// The directory is opened through the root rather than read as an fs.FS:
	// fs.FS names are slash-separated and a backslash in one is invalid, so
	// root.FS() would refuse the name filepath.Join builds on Windows and
	// retention would never run there.
	d, err := root.Open(dir)
	if err != nil {
		return
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return
	}
	type agedLog struct {
		name string
		at   time.Time
	}
	var mine []agedLog
	for _, e := range entries {
		at, ok := logStamp(e.Name(), cmdName)
		if !ok {
			continue
		}
		fi, err := root.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		mine = append(mine, agedLog{name: e.Name(), at: at})
	}
	sort.Slice(mine, func(i, j int) bool {
		if mine[i].name == keep || mine[j].name == keep {
			return mine[i].name == keep
		}
		if !mine[i].at.Equal(mine[j].at) {
			return mine[i].at.After(mine[j].at)
		}
		return mine[i].name > mine[j].name
	})
	for i, l := range mine {
		if i < logKeepPerCommand && now.Sub(l.at) <= logRetention {
			continue
		}
		_ = root.Remove(filepath.Join(dir, l.name))
	}
}

// logStamp reads the time out of a log's name, and reports whether the name
// is this command's log at all. The name has to be exactly
// `<command>-<14 digits UTC>-<pid>.log`: the command's own name, then a stamp
// that parses and a pid that is a number, and nothing else.
func logStamp(name, cmdName string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, cmdName+"-")
	if !ok {
		return time.Time{}, false
	}
	stamp, rest, ok := strings.Cut(rest, "-")
	if !ok || len(stamp) != len(logStampLayout) || !allDigits(stamp) {
		return time.Time{}, false
	}
	pid, ok := strings.CutSuffix(rest, logSuffix)
	if !ok || pid == "" || !allDigits(pid) {
		return time.Time{}, false
	}
	at, err := time.ParseInLocation(logStampLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// allDigits reports whether s is one or more decimal digits and nothing else.
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// logSink is the open log. Writes are serialised because a run streams its
// children's output from more than one goroutine, and redacted line by line
// on the way in.
type logSink struct {
	mu   sync.Mutex
	f    *os.File
	path string
	red  *Redactor
}

// Write never fails: a log that cannot be written is a log the person does
// not have, and never a failed command (G5). The bytes are counted as taken
// so a child writing to this sink never sees a short write.
func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return len(p), nil
	}
	_, _ = s.red.Write(p)
	return len(p), nil
}

// Path is the file the run's output is in: what the failure block prints.
func (s *logSink) Path() string { return s.path }

// Close writes the last partial line and closes the file. The run's own
// closing is the caller's: a command owns the sink it opened and closes it on
// its way out.
func (s *logSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	_ = s.red.Flush()
	err := s.f.Close()
	s.f = nil
	return err
}

// noLog is the sink for a run that has no log: no state directory, a
// directory it may not use, a name already taken. It discards everything and
// reports no path, so every caller can treat "there is no log" as one case
// (G5, T6).
type noLog struct{}

func (noLog) Write(p []byte) (int, error) { return len(p), nil }

func (noLog) Path() string { return "" }

func (noLog) Close() error { return nil }

var (
	_ Sink = (*logSink)(nil)
	_ Sink = noLog{}
)
