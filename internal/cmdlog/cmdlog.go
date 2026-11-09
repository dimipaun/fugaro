// Package cmdlog reads the run IDs of the commands this machine has logged.
//
// D1609: a local command's run ID is the stem of its log file —
// `<command>-<14-digit UTC stamp>-<pid>`, for example
// `update-20261107183012-48213` — so one name is the file, the line a failure
// prints and the argument `fugaro troubleshoot` takes, and the command routes
// by shape: a cloud run is `<repo-slug>/<run-id>`, which is not a log this
// machine holds (ParseID refuses it).
//
// The writer is internal/report's OpenLog — the output plan's own task, D1351
// and D1352, which was in the tree before this package — so there is one store,
// one name rule and one retention rule, and this package reads what that
// writes rather than keeping a second store beside it. Nothing here writes: a
// read leaves no directory and no file behind, and a machine that has never run
// a logged command is not given one by asking.
package cmdlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

const (
	// stampLayout is the timestamp in a log's name: fourteen digits, UTC. It is
	// the writer's layout as well, and TestIDRoundTrip pins the two together by
	// reading back the stem of the file a run left.
	stampLayout = "20060102150405"
	// logSuffix ends every log's name.
	logSuffix = ".log"
	// tailBytes is how much of a log's end a run's outcome is read from. A log
	// is the whole of what a run printed — a Terraform stream is megabytes —
	// and the closing line that names the outcome is the last line of it, so
	// the read is bounded to the tail rather than the file.
	tailBytes = 512
)

// storePath is where the logs live inside the state directory (D1351):
// `<localcfg.UserDir state>/fugaro/logs`.
var storePath = filepath.Join("fugaro", "logs")

// commandRE is what a command name may be — the first half of a log's name,
// and nothing that could be a path. It is report.OpenLog's own rule, spelled
// here rather than read out of it: TestTheParserTakesWhatTheWriterNames holds
// the two together by writing through the writer and parsing here, so a rename
// that broke either fails there.
var commandRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// closingBad are the openings of the closing line a run that did not end done
// leaves: report's summaryLine writes `Done in …`, `Failed in …` or
// `Left for you in …` as the run's last line, and a run left for the user
// exits 1 like a failure, so both are a run with something to explain.
// TestLatestReadsTheWritersClosingLine drives a real reporter through all three
// outcomes, so the wording cannot drift out from under this list.
var closingBad = []string{"Failed in ", "Left for you in "}

// ErrNoLog is the answer when there is no log: no state directory, no log
// directory, no file at the name, and — for Latest — nothing the filter takes.
// It is an ordinary answer rather than a failure: a machine that has never run
// a logged command has no logs, and a caller says so without an error to
// report.
var ErrNoLog = errors.New("cmdlog: no log")

// ID is one logged command: the stem of its log file. The three parts are the
// name rule's, so an ID printed, typed back and parsed is the same ID.
type ID struct {
	// Command is the name the run filed its log under (D1352): `update`,
	// `image-refresh`. It is the first part of the name, up to the stamp, so a
	// name that carries dashes of its own is still one name.
	Command string
	// At is when the run started, to the second, in UTC: the stamp in the name.
	At time.Time
	// PID is the process that ran it, which is what keeps two runs of one
	// command inside one second apart.
	PID int
}

// String is the ID's own spelling: the log file's stem.
func (id ID) String() string {
	return id.Command + "-" + id.At.UTC().Format(stampLayout) + "-" + strconv.Itoa(id.PID)
}

// ParseID reads a run ID back from its spelling — what a person typed, or the
// stem of a name in the store. The stamp is the last two dash-separated parts
// and the command is everything in front of them, so `image-refresh-…` is one
// command and not `image` with a suffix: retention is per command name (D1352),
// and a reader that split at the first dash would read another command's logs.
//
// Anything else is refused with the reason: a cloud run's `<repo-slug>/<run-id>`
// has the shape of neither part, a file name carrying the `.log` suffix is not
// the stem, and a command name the writer would refuse (a path, a capital, a
// name past the length rule) names no log at all.
func ParseID(s string) (ID, error) {
	head, pidPart, ok := cutLast(s, "-")
	if !ok || !allDigits(pidPart) {
		return ID{}, fmt.Errorf("cmdlog: %q is not a run ID: a run ID is <command>-<14-digit UTC stamp>-<pid>", s)
	}
	command, stamp, ok := cutLast(head, "-")
	if !ok || len(stamp) != len(stampLayout) || !allDigits(stamp) {
		return ID{}, fmt.Errorf("cmdlog: %q is not a run ID: its stamp is not fourteen digits of UTC", s)
	}
	if !commandRE.MatchString(command) {
		return ID{}, fmt.Errorf("cmdlog: %q is not a run ID: %q is not a command name a log is filed under", s, command)
	}
	at, err := time.ParseInLocation(stampLayout, stamp, time.UTC)
	if err != nil {
		return ID{}, fmt.Errorf("cmdlog: %q is not a run ID: its stamp is not a time", s)
	}
	pid, err := strconv.Atoi(pidPart)
	if err != nil {
		return ID{}, fmt.Errorf("cmdlog: %q is not a run ID: its pid is not a number", s)
	}
	return ID{Command: command, At: at, PID: pid}, nil
}

// Latest is the run ID of the newest logged command on this machine, which is
// what `fugaro troubleshoot` takes when it is given no ID (T-DG8). getenv is
// the environment reader, os.Getenv in the CLI.
//
// failedOnly keeps the answer to a run that ended badly: one whose log closes
// with report's `Failed in …` or `Left for you in …`, so the run exited 1 and
// left something to explain. A log with no closing line — a command that
// reports no step at all, a run that died mid-line — is not known to have
// failed and is skipped rather than guessed at, and a machine whose logs all
// ended well answers ErrNoLog: no log is better than the wrong one.
//
// The order is the name's own — the stamp, then the name — which is the order
// the writer's retention already keeps the store in, so no second rule about
// which of two runs is newer exists.
func Latest(getenv func(string) string, failedOnly bool) (ID, error) {
	base, err := storeBase(getenv)
	if err != nil {
		return ID{}, ErrNoLog
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return ID{}, ErrNoLog
	}
	defer root.Close()
	ids, err := logsIn(root)
	if err != nil {
		return ID{}, ErrNoLog
	}
	for _, id := range ids {
		if !failedOnly || endedBadly(root, id) {
			return id, nil
		}
	}
	return ID{}, ErrNoLog
}

// Open returns the log a run ID names, for reading: the file the run's own
// output is in, which the evidence a diagnosis reads is built from (T-DG6).
// getenv is the environment reader, os.Getenv in the CLI.
//
// The ID is checked rather than trusted — one built by hand with a command name
// no log could carry is refused, so a typed argument cannot reach outside the
// store — and the name is resolved inside a root opened at the state
// directory, as the writer creates it, so the link the store refuses to write
// through is one this refuses to read through either. Nothing is created: a
// log that is not there is ErrNoLog.
func Open(getenv func(string) string, id ID) (io.ReadCloser, error) {
	if !commandRE.MatchString(id.Command) {
		return nil, fmt.Errorf("cmdlog: %q is not a command name, so it names no log", id.Command)
	}
	base, err := storeBase(getenv)
	if err != nil {
		return nil, fmt.Errorf("cmdlog: no log %s: %w", id, ErrNoLog)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("cmdlog: no log %s: %w", id, ErrNoLog)
	}
	f, err := openLog(root, id)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("cmdlog: no log %s: %w", id, ErrNoLog)
	}
	return &logFile{f: f, root: root}, nil
}

// storeBase is the state directory the logs live in, placed by the one
// directory rule the CLI has (D1351, localcfg.UserDir): $XDG_STATE_HOME where
// it is set, else ~/.local/state on macOS and Linux, else %LOCALAPPDATA% on
// Windows.
func storeBase(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirState)
}

// logsIn lists the IDs of the logs in the store, newest first. What it reads
// is exactly report.OpenLog's names: `<command>-<14 digits>-<pid>.log`, a
// regular file. Everything else in the directory — another tool's notes, a
// directory, a link at a log's name, a name whose stamp is not fourteen digits
// — is not a log and is skipped rather than reported as one.
func logsIn(root *os.Root) ([]ID, error) {
	// The directory is opened through the root rather than read as an fs.FS:
	// fs.FS names are slash-separated and a backslash in one is invalid, so
	// root.FS() would refuse the name filepath.Join builds on Windows and the
	// store would never be read there (the same reason the writer prunes
	// through the root).
	d, err := root.Open(storePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var ids []ID
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), logSuffix)
		if !ok {
			continue
		}
		id, err := ParseID(stem)
		if err != nil {
			continue
		}
		fi, err := root.Lstat(filepath.Join(storePath, e.Name()))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if !ids[i].At.Equal(ids[j].At) {
			return ids[i].At.After(ids[j].At)
		}
		return ids[i].String() > ids[j].String()
	})
	return ids, nil
}

// endedBadly reports whether the run id names ended other than done: its log's
// closing line is one the reporter writes for a failure or for a step left for
// the user. A log with no closing line — no step was ever reported, or the run
// died inside one — is not known to have failed.
func endedBadly(root *os.Root, id ID) bool {
	f, err := openLog(root, id)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	off := int64(0)
	if fi.Size() > tailBytes {
		off = fi.Size() - tailBytes
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	line := lastLine(string(buf))
	for _, opening := range closingBad {
		if strings.HasPrefix(line, opening) {
			return true
		}
	}
	return false
}

// lastLine is the last line of text that carries anything, with the newline
// and the carriage return a terminal writes stripped off.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimRight(lines[i], "\r"); strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// openLog opens id's log inside root. The name is built from the ID's parts
// and resolved through the root, so it cannot leave the store and a link at the
// name is refused rather than followed (os.Root's rule, which is how the writer
// creates the file with O_EXCL).
func openLog(root *os.Root, id ID) (*os.File, error) {
	return root.Open(filepath.Join(storePath, id.String()+logSuffix))
}

// logFile is an open log and the root it was resolved through. The root stays
// open for as long as the file does, so the name cannot be swapped under a
// caller partway through a read.
type logFile struct {
	f    *os.File
	root *os.Root
}

func (l *logFile) Read(p []byte) (int, error) { return l.f.Read(p) }

func (l *logFile) Close() error {
	err := l.f.Close()
	if cerr := l.root.Close(); err == nil {
		err = cerr
	}
	return err
}

// cutLast splits s at its last sep, the way a log's name is read from its end:
// the command in front of the stamp is everything up to it, dashes and all.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
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

var _ io.ReadCloser = (*logFile)(nil)
