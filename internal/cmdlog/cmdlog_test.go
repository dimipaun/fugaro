package cmdlog

// cmdlog_test.go holds the run ID and the read side of the command log to the
// writer that is already in the tree: internal/report's OpenLog, the output
// plan's own task (D1351, D1352). T-DG1 was written to build a minimal writer
// here when that task was not built; it was, so nothing here writes a log of
// its own — every test leaves its logs through the writer the CLI really uses
// and reads them back through this package. What the plan asks of a writer's
// tests is therefore here as the reader's side of the same rules: the ID round
// trip, the per-command-name split retention keys on, and the store's modes.
//
// The red step the plan names — "a `--json` read leaves no log" — is D1352's
// rule and belongs to the CLI, which opens no log at all for a read command;
// what this package owes the same rule is TestReadingLeavesNoLog: asking the
// store for a log creates no log and no directory either.

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
)

// logAt is the clock every log here is written with: a fixed UTC instant, so a
// name's stamp is the test's own rather than the machine's.
var logAt = time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC)

// env is a fixed environment naming a state directory under the test's own temp
// directory, so nothing here reads or writes the machine's real one (the
// hermetic rule).
func env(t *testing.T) func(string) string {
	t.Helper()
	base := t.TempDir()
	m := map[string]string{"XDG_STATE_HOME": base, "HOME": base}
	return func(name string) string { return m[name] }
}

// store is the log directory the environment names, read the way the writer
// places it rather than out of this package.
func store(t *testing.T, getenv func(string) string) string {
	t.Helper()
	base, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirState)
	if err != nil {
		t.Fatalf("the test environment names no state directory: %v", err)
	}
	return filepath.Join(base, "fugaro", "logs")
}

// stamp is a time as it stands in a log's name.
func stamp(at time.Time) string { return at.UTC().Format(stampLayout) }

// writeLog leaves one log through the writer the CLI uses, holding body.
func writeLog(t *testing.T, getenv func(string) string, command string, at time.Time, body string) {
	t.Helper()
	sink, err := report.OpenLog(command, getenv, runtime.GOOS, func() time.Time { return at }, nil)
	if err != nil {
		t.Fatalf("the writer refused %s: %v", command, err)
	}
	if _, err := io.WriteString(sink, body); err != nil {
		t.Fatalf("writing %s's log: %v", command, err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("closing %s's log: %v", command, err)
	}
}

// runSteps leaves one log through a real reporter, so the file holds what a run
// really leaves: its step lines and the closing line last.
func runSteps(t *testing.T, getenv func(string) string, command string, at time.Time, end func(*report.Reporter)) {
	t.Helper()
	sink, err := report.OpenLog(command, getenv, runtime.GOOS, func() time.Time { return at }, nil)
	if err != nil {
		t.Fatalf("the writer refused %s: %v", command, err)
	}
	rep := report.New(report.Options{
		Out:  io.Discard,
		Err:  io.Discard,
		Now:  func() time.Time { return at },
		Sink: sink,
	})
	end(rep)
	rep.Summary()
	if err := sink.Close(); err != nil {
		t.Fatalf("closing %s's log: %v", command, err)
	}
}

// hand leaves a file in the store directly, under a name the test picks: two
// runs of one process inside one second collide on the writer's own name, so
// the store's order between two such names and its treatment of what is not a
// log are read from files placed by hand.
func hand(t *testing.T, getenv func(string) string, name, body string) {
	t.Helper()
	dir := store(t, getenv)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// onlyStem is the stem of the one log the store holds for command, read out of
// the directory the writer put it in.
func onlyStem(t *testing.T, getenv func(string) string, command string) string {
	t.Helper()
	entries, err := os.ReadDir(store(t, getenv))
	if err != nil {
		t.Fatalf("the run left no log directory: %v", err)
	}
	re := regexp.MustCompile("^" + regexp.QuoteMeta(command) + `-\d{14}-\d+\.log$`)
	var names []string
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("the run left %d logs of %s, want one: %v", len(names), command, names)
	}
	return strings.TrimSuffix(names[0], logSuffix)
}

// TestIDRoundTrip: a finished run's ID is the stem of its log file, the parser
// reads it back to the same three parts, and Open returns what the run wrote.
// The names include one with a dash and one that is a prefix of it, because a
// name is split at its stamp and not at its first dash: `image` must never
// cover `image-refresh`'s logs, which is what the retention rule keys on
// (D1352).
func TestIDRoundTrip(t *testing.T) {
	for _, name := range []string{"update", "run", "image", "image-refresh", "budget-set", "a"} {
		t.Run(name, func(t *testing.T) {
			getenv := env(t)
			body := "terraform: refused\n"
			writeLog(t, getenv, name, logAt, body)

			id, err := Latest(getenv, false)
			if err != nil {
				t.Fatalf("Latest = %v, want the log %s just left", err, name)
			}
			if id.Command != name {
				t.Errorf("the run ID names %q, want %q", id.Command, name)
			}
			if !id.At.Equal(logAt) {
				t.Errorf("the run ID is stamped %s, want %s", id.At, logAt)
			}
			if id.PID != os.Getpid() {
				t.Errorf("the run ID names pid %d, want %d", id.PID, os.Getpid())
			}
			// One name serves the file, the printed line and the argument (D1609).
			if want := onlyStem(t, getenv, name); id.String() != want {
				t.Errorf("the run ID is %q, and the log it names is %q", id.String(), want)
			}

			back, err := ParseID(id.String())
			if err != nil {
				t.Fatalf("ParseID(%q) = %v", id.String(), err)
			}
			if back.Command != id.Command || back.PID != id.PID || !back.At.Equal(id.At) {
				t.Errorf("ParseID(%q) = %+v, want %+v", id.String(), back, id)
			}

			rc, err := Open(getenv, back)
			if err != nil {
				t.Fatalf("Open(%s) = %v", back, err)
			}
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("reading %s's log: %v", back, err)
			}
			if err := rc.Close(); err != nil {
				t.Fatalf("closing %s's log: %v", back, err)
			}
			if string(got) != body {
				t.Errorf("the log holds %q, want %q", got, body)
			}
		})
	}
}

// TestParseIDRefusesWhatNoLogIsNamed: every way a typed argument or a file in
// the store can look like a run ID without being one. A refusal is what keeps
// a name a log cannot carry from reaching the filesystem at all.
func TestParseIDRefusesWhatNoLogIsNamed(t *testing.T) {
	for _, s := range []string{
		"",
		"update",
		"update-20261107183012",
		"-20261107183012-48213",
		"update-2026110718301-48213",
		"update-202611071830123-48213",
		"update-2026110718301x-48213",
		"update-20261107183012-48213.log",
		"update-20261107183012-48a13",
		"update-20261107183012-",
		"update-20261107183012-48213-1",
		"Update-20261107183012-48213",
		"update_x-20261107183012-48213",
		"../update-20261107183012-48213",
		"up/down-20261107183012-48213",
		"update-20261307183012-48213",
		// a cloud run is `<repo-slug>/<run-id>`, and no log this machine holds
		"dimipaun/fugaro-20261107183012-48213",
		// the name rule takes 41 characters of command and no more
		strings.Repeat("a", 42) + "-20261107183012-48213",
	} {
		t.Run(s, func(t *testing.T) {
			if id, err := ParseID(s); err == nil {
				t.Errorf("ParseID(%q) = %+v, want a refusal", s, id)
			}
		})
	}
}

// TestTheParserTakesWhatTheWriterNames: the name rule is one rule. The writer
// refuses a command name the file name rule will not take (OpenLog's refusal is
// silent by design, G5), and the parser has to refuse the run ID of the same
// name, or a typed argument would name a log that can never exist.
func TestTheParserTakesWhatTheWriterNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		logs bool
	}{
		{"update", true},
		{"image-refresh", true},
		{"a", true},
		{"a0", true},
		{strings.Repeat("a", 41), true},
		{strings.Repeat("a", 42), false},
		{"Update", false},
		{"", false},
		{"../x", false},
		{"x/y", false},
		{"x_y", false},
		{"x y", false},
		{"0x", false},
		{"-x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := env(t)
			sink, err := report.OpenLog(tc.name, getenv, runtime.GOOS, func() time.Time { return logAt }, nil)
			if err == nil {
				_ = sink.Close()
			}
			if logs := err == nil; logs != tc.logs {
				t.Errorf("the writer files %q under a log: %v (want %v)", tc.name, logs, tc.logs)
			}
			_, perr := ParseID(tc.name + "-" + stamp(logAt) + "-48213")
			if takes := perr == nil; takes != tc.logs {
				t.Errorf("the parser takes %q: %v, and the writer files it: %v", tc.name, takes, tc.logs)
			}
		})
	}
}

// TestLatestTakesTheNewestLog: the newest run wins, across command names —
// the command that failed is whichever it was — and the order is the name's
// own, the stamp first, which is the order the writer's retention keeps the
// store in.
func TestLatestTakesTheNewestLog(t *testing.T) {
	getenv := env(t)
	writeLog(t, getenv, "update", logAt, "✓ applied\nDone in 0.4s, 1 step\n")
	writeLog(t, getenv, "run", logAt.Add(time.Minute), "run started\n")
	writeLog(t, getenv, "image-refresh", logAt.Add(2*time.Minute), "✓ built\nDone in 3.1s, 1 step\n")

	id, err := Latest(getenv, false)
	if err != nil {
		t.Fatalf("Latest = %v", err)
	}
	if id.Command != "image-refresh" || !id.At.Equal(logAt.Add(2*time.Minute)) {
		t.Errorf("Latest = %+v, want the newest run, image-refresh at %s", id, logAt.Add(2*time.Minute))
	}

	// Two runs of one command inside one second: the stamp is the same and the
	// pid is what is left to order them by.
	hand(t, getenv, "update-"+stamp(logAt.Add(3*time.Minute))+"-1.log", "✓ applied\nDone in 0.4s, 1 step\n")
	hand(t, getenv, "update-"+stamp(logAt.Add(3*time.Minute))+"-2.log", "✓ applied\nDone in 0.4s, 1 step\n")
	id, err = Latest(getenv, false)
	if err != nil {
		t.Fatalf("Latest = %v", err)
	}
	if id.Command != "update" || !id.At.Equal(logAt.Add(3*time.Minute)) || id.PID != 2 {
		t.Errorf("Latest = %+v, want the later of the two runs of update inside one second", id)
	}
}

// TestLatestFailedOnlyKeepsARunThatEndedBadly: what `fugaro troubleshoot` takes
// when it is given no ID is a run that exited 1, which is a log whose closing
// line is the reporter's failure or its step left for the user. A newer run
// that ended done is passed over, a run left for the user counts, and a log
// with no closing line is not guessed at.
func TestLatestFailedOnlySkipsARunThatEndedDone(t *testing.T) {
	const (
		failed = "✗ apply: the plan was refused\nFailed in 1.2s, 2 steps\n"
		done   = "✓ apply\nDone in 0.2s, 1 step\n"
		left   = "◌ project: type the project name\nLeft for you in 0.1s, 1 step\n"
		raw    = "terraform: still working\n"
	)

	t.Run("a newer run that ended done is skipped", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "update", logAt, failed)
		writeLog(t, getenv, "run", logAt.Add(time.Minute), done)
		if id, err := Latest(getenv, false); err != nil || id.Command != "run" {
			t.Errorf("Latest = %+v, %v, want the newer run", id, err)
		}
		id, err := Latest(getenv, true)
		if err != nil || id.Command != "update" {
			t.Errorf("Latest(failedOnly) = %+v, %v, want the run that failed", id, err)
		}
	})

	t.Run("a run left for the user counts", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "update", logAt, failed)
		writeLog(t, getenv, "run", logAt.Add(time.Minute), left)
		id, err := Latest(getenv, true)
		if err != nil || id.Command != "run" {
			t.Errorf("Latest(failedOnly) = %+v, %v, want the run left for the user, which exits 1 too", id, err)
		}
	})

	t.Run("a log with no closing line is not known to have failed", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "run", logAt, raw)
		if id, err := Latest(getenv, false); err != nil || id.Command != "run" {
			t.Errorf("Latest = %+v, %v, want the run's log", id, err)
		}
		if id, err := Latest(getenv, true); !errors.Is(err, ErrNoLog) {
			t.Errorf("Latest(failedOnly) = %+v, %v, want ErrNoLog: no log is better than the wrong one", id, err)
		}
	})

	t.Run("a closing line written with a carriage return", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "update", logAt, "x apply: the plan was refused\r\nFailed in 1.2s, 2 steps\r\n")
		if id, err := Latest(getenv, true); err != nil || id.Command != "update" {
			t.Errorf("Latest(failedOnly) = %+v, %v, want the run that failed", id, err)
		}
	})

	t.Run("nothing failed", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "update", logAt, done)
		writeLog(t, getenv, "run", logAt.Add(time.Minute), done)
		if id, err := Latest(getenv, true); !errors.Is(err, ErrNoLog) {
			t.Errorf("Latest(failedOnly) = %+v, %v, want ErrNoLog", id, err)
		}
	})
}

// TestLatestReadsTheWritersClosingLine: the words are report's — summaryLine
// writes `Done in …`, `Failed in …` or `Left for you in …` as a run's last line
// — and this drives a real reporter through all three outcomes so the two
// packages cannot drift apart silently. The megabyte is there because the
// closing line is read from the end of the file rather than by reading the
// whole of it: a Terraform stream is what really sits in front of it.
func TestLatestReadsTheWritersClosingLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		end    func(*report.Reporter)
		failed bool
	}{
		{"done", func(r *report.Reporter) { r.Step("terraform apply").Done("") }, false},
		{"failed", func(r *report.Reporter) { r.Step("terraform apply").Failed(errors.New("the plan was refused"), nil) }, true},
		{"left for you", func(r *report.Reporter) { r.Step("project").Left("type the project name") }, true},
		{"no step at all", func(r *report.Reporter) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := env(t)
			runSteps(t, getenv, "update", logAt, tc.end)
			id, err := Latest(getenv, true)
			if tc.failed && (err != nil || id.Command != "update") {
				t.Errorf("Latest(failedOnly) = %+v, %v, want the run whose outcome is %s", id, err, tc.name)
			}
			if !tc.failed && !errors.Is(err, ErrNoLog) {
				t.Errorf("Latest(failedOnly) = %+v, %v, want ErrNoLog", id, err)
			}
			if id, err := Latest(getenv, false); err != nil || id.Command != "update" {
				t.Errorf("Latest = %+v, %v, want the log the run left either way", id, err)
			}
		})
	}

	t.Run("a log of a megabyte", func(t *testing.T) {
		getenv := env(t)
		writeLog(t, getenv, "update", logAt,
			strings.Repeat("terraform: still working\n", 40000)+"Failed in 9.9s, 3 steps\n")
		id, err := Latest(getenv, true)
		if err != nil || id.Command != "update" {
			t.Errorf("Latest(failedOnly) = %+v, %v, want the run whose log ends in a failure", id, err)
		}
	})
}

// plant leaves a file at exactly the name an ID resolves to, inside the state
// directory: the store for a name that stays in it, and the directory the ID
// climbs to for one that does not. A test that plants is asserting a refusal
// that is not "no such log" — the file is there, and only the check under test
// stands between it and a caller.
func plant(t *testing.T, getenv func(string) string, id ID) {
	t.Helper()
	path := filepath.Join(store(t, getenv), id.String()+logSuffix)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("a file this ID resolves to\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOpenRefusesACommandNameNoLogIsFiledUnder: Open checks the ID rather than
// trusting it, and this is the half of that check which does not depend on
// finding a log — a hand-built ID, an argument assembled by another program, a
// name nothing here printed. A file is planted at every one of these names, so
// what is asserted is the guard and not the absence of the file: without it
// each of these IDs opens a file, and the evidence a diagnosis is built from
// would be a file no run of this machine wrote.
func TestOpenRefusesACommandNameNoLogIsFiledUnder(t *testing.T) {
	for _, command := range []string{
		"",
		"./update",
		"../secret",
		"x/y",
		"Update",
		"x_y",
		"x y",
		strings.Repeat("a", 42),
	} {
		t.Run(command, func(t *testing.T) {
			getenv := env(t)
			id := ID{Command: command, At: logAt, PID: 48213}
			plant(t, getenv, id)
			rc, err := Open(getenv, id)
			if err == nil {
				got, _ := io.ReadAll(rc)
				_ = rc.Close()
				t.Errorf("Open(%q) read %q: a command name no log is filed under reached a file", id, got)
			}
		})
	}
}

// TestTheStoreIsTheWriters: the store this reads is the one the writer made
// (D1351, D1352) — the directory 0700, the log 0600 — and reading leaves it
// exactly as it was: no second store, no second file, no loosened mode.
func TestTheStoreIsTheWriters(t *testing.T) {
	getenv := env(t)
	body := "the run's own output\n"
	writeLog(t, getenv, "update", logAt, body)
	dir := store(t, getenv)

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the run left no log directory: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("the log directory is %v, want 0700", fi.Mode().Perm())
	}
	stem := onlyStem(t, getenv, "update")
	path := filepath.Join(dir, stem+logSuffix)
	fi, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("the log %s is %v, want 0600", path, fi.Mode().Perm())
	}

	rc, err := Open(getenv, ID{Command: "update", At: logAt, PID: os.Getpid()})
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if got, err := io.ReadAll(rc); err != nil || string(got) != body {
		t.Errorf("the log holds %q, %v, want %q", got, err, body)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	fi, err = os.Stat(path)
	if err != nil {
		t.Fatalf("reading the log left %s behind: %v", path, err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("reading the log made it %v, want 0600", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("reading the store left %d files in it, want the one log: %v", len(entries), entries)
	}
}

// TestTheStoreIgnoresWhatIsNotALog: another tool's notes, a name whose stamp is
// not fourteen digits, a directory and a link at a log's name are none of them
// a log, and none of them makes Latest answer one.
func TestTheStoreIgnoresWhatIsNotALog(t *testing.T) {
	getenv := env(t)
	hand(t, getenv, "notes.txt", "not a log\n")
	hand(t, getenv, "update-2026110718301-48213.log", "a stamp of thirteen digits\n")
	hand(t, getenv, "update-"+stamp(logAt)+"-48213", "no suffix\n")
	if err := os.Mkdir(filepath.Join(store(t, getenv), "run-"+stamp(logAt)+"-1.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		elsewhere := filepath.Join(t.TempDir(), "elsewhere.log")
		if err := os.WriteFile(elsewhere, []byte("elsewhere\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(store(t, getenv), "image-"+stamp(logAt)+"-1.log")); err != nil {
			t.Fatal(err)
		}
	}

	if id, err := Latest(getenv, false); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest = %+v, %v, want ErrNoLog: none of these is a log", id, err)
	}
	if _, err := Open(getenv, ID{Command: "update", At: logAt, PID: 48213}); !errors.Is(err, ErrNoLog) {
		t.Errorf("Open = %v, want ErrNoLog: a log's name without its suffix is not a log", err)
	}
}

// TestReadingLeavesNoLog: asking the store for a log writes nothing — no state
// directory, no log directory, no file — so a command that only reads can never
// leave one behind by reading. It is this package's half of the plan's red
// step, whose other half is D1352's own rule in the CLI: a read command opens
// no log at all.
func TestReadingLeavesNoLog(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "never-created")
	m := map[string]string{"XDG_STATE_HOME": missing}
	getenv := func(name string) string { return m[name] }
	id := ID{Command: "update", At: logAt, PID: 48213}

	if got, err := Latest(getenv, false); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest = %+v, %v, want ErrNoLog", got, err)
	}
	if got, err := Latest(getenv, true); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest(failedOnly) = %+v, %v, want ErrNoLog", got, err)
	}
	if _, err := Open(getenv, id); !errors.Is(err, ErrNoLog) {
		t.Errorf("Open = %v, want ErrNoLog", err)
	}

	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading the store created %s: %v", missing, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("reading the store left %d entries under %s: %v", len(entries), base, entries)
	}
}
