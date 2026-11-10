package diagnoseagent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/cmdlog"
)

// evidenceFrag is the harmless constant every fixture value here is built
// from: no value in this file is a secret-shaped literal (the repository's
// Rule 10), and what these tests assert about a credential is that it does not
// reach a file, never that it looks like one.
const evidenceFrag = "ZQ7X4"

// evidenceValue is one test's fixture secret.
func evidenceValue(t *testing.T, tag string) string {
	t.Helper()
	return evidenceFrag + "-" + t.Name() + "-" + tag
}

// evidenceCommit is a fixture commit, forty hex digits as internal/cli.Commit
// carries them.
const evidenceCommit = "0123456789abcdef0123456789abcdef01234567"

// evidenceShape is a credential no list holds: the shape a failing cloud call
// prints, which only the pattern pass can know. It is built from pieces
// (Rule 10) and it is the exact shape internal/redact's corpus pins.
func evidenceShape() string {
	return strings.Join([]string{"ghp_", strings.Repeat("A1b2C3d4", 5)}, "")
}

// evidenceID is the run ID the fixtures diagnose: the shape D1609 rules,
// `update-<14-digit UTC stamp>-<pid>`.
func evidenceID() cmdlog.ID {
	return cmdlog.ID{Command: "update", At: time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC), PID: 48213}
}

// evidenceEnv points the state directory at a temporary directory: the
// evidence folder is placed by localcfg.UserDir, and a test may not touch the
// machine's own.
func evidenceEnv(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	return func(name string) string {
		if name == "XDG_STATE_HOME" {
			return dir
		}
		return ""
	}
}

// evidenceRead returns every file in the folder, by base name, so an assertion
// can run over all of them rather than over one it remembered to name.
func evidenceRead(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[d.Name()] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the evidence folder: %v", err)
	}
	return out
}

// TestEvidenceCollectWritesTheFolderAndBothPasses pins the collector's whole
// contract in one place: the folder it writes, the files in it, and the rule
// that no file carries a secret — neither one the caller named (the exact
// pass) nor a shape nobody named (the pattern pass, F4). The log, the config
// and the doctor output each hold both, so a collector that redacted one of
// them, or only the exact values, fails here.
func TestEvidenceCollectWritesTheFolderAndBothPasses(t *testing.T) {
	getenv := evidenceEnv(t)
	secret := evidenceValue(t, "planted")
	shape := evidenceShape()
	id := evidenceID()

	logText := "terraform apply: reading the installation\n" +
		"ERROR: the api refused " + shape + " (403)\n" +
		"env: ANTHROPIC_API_KEY=" + secret + "\n"
	configText := "model: sonnet\nclaude_accounts:\n  team-b:\n    token: " + secret + "\n"
	doctorText := "budget check failed: Authorization: Bearer " + shape + "\n"

	ev, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2",
		Commit:  evidenceCommit,
		Getenv:  getenv,
		GOOS:    "linux",
		Open:    func(cmdlog.ID) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(logText)), nil },
		Doctor:  func(context.Context) ([]byte, error) { return []byte(doctorText), nil },
		Config:  func(context.Context) ([]byte, error) { return []byte(configText), nil },
		Secrets: []string{secret},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if ev.RunID != id.String() {
		t.Errorf("Evidence.RunID = %q, want %q", ev.RunID, id.String())
	}
	if ev.Command != "update" {
		t.Errorf("Evidence.Command = %q, want update: it is the command the log's name spells", ev.Command)
	}
	if ev.Version != "v0.5.2" {
		t.Errorf("Evidence.Version = %q, want v0.5.2", ev.Version)
	}
	if ev.EvidenceDir == "" {
		t.Fatal("Evidence.EvidenceDir is empty: the prompt points the agent at it")
	}
	fi, err := os.Lstat(ev.EvidenceDir)
	if err != nil {
		t.Fatalf("the evidence folder was not left behind: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", ev.EvidenceDir)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("the evidence folder is %04o, want 0700 (D1603)", perm)
	}

	files := evidenceRead(t, ev.EvidenceDir)
	for _, name := range []string{id.String() + ".log", "doctor.txt", "version.txt", "config.yaml"} {
		if _, ok := files[name]; !ok {
			t.Errorf("the evidence folder holds no %s: it holds %v", name, evidenceNames(files))
		}
	}
	for name, text := range files {
		if strings.Contains(text, secret) {
			t.Errorf("%s carries the secret the caller named: every file is redacted with the exact pass", name)
		}
		if strings.Contains(text, shape) {
			t.Errorf("%s carries a credential-shaped value: every file is redacted with the pattern pass too", name)
		}
	}
	// The three files that were given something to redact carry the marker
	// where it stood: the pass substitutes, it never drops the text around it.
	for _, name := range []string{id.String() + ".log", "doctor.txt", "config.yaml"} {
		if !strings.Contains(files[name], "[REDACTED]") {
			t.Errorf("%s holds no [REDACTED] where a value stood: %q", name, files[name])
		}
	}
	if got := files["version.txt"]; !strings.Contains(got, "v0.5.2") || !strings.Contains(got, evidenceCommit) {
		t.Errorf("version.txt = %q, want the installed version and the commit the binary was built from", got)
	}
	for name := range files {
		mode := evidenceMode(t, filepath.Join(ev.EvidenceDir, name))
		if mode != 0o600 {
			t.Errorf("%s is %04o, want 0600", name, mode)
		}
	}
}

// TestEvidenceCollectKeepsTheLogsOwnWords: the log is the diagnosis's reason to
// exist, so the file named after the run ID is the log's own text (redacted),
// not a note about it. The line the failure is readable from has to survive.
func TestEvidenceCollectKeepsTheLogsOwnWords(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	logText := "step: reading the installation\nStep failed: permission denied on the bucket\n"
	ev, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(logText)), nil },
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := evidenceRead(t, ev.EvidenceDir)[id.String()+".log"]
	if !strings.Contains(got, "permission denied on the bucket") {
		t.Errorf("the log file = %q, want the log's own line: the collector redacts, it does not summarise", got)
	}
}

// TestEvidenceCollectDoesNotAbortOnAMissingDoctor: doctor makes cloud calls and
// the cloud is often exactly what is broken (F23), so a doctor that fails, or
// one this diagnosis does not have at all, leaves a note in the folder rather
// than failing the collection. The rest of the evidence is what the diagnosis
// still has.
func TestEvidenceCollectDoesNotAbortOnAMissingDoctor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doctor func(context.Context) ([]byte, error)
		want   string
	}{
		{
			name:   "no doctor to run",
			doctor: nil,
			want:   "not run",
		},
		{
			name: "the doctor failed",
			doctor: func(context.Context) ([]byte, error) {
				return nil, errors.New("listing the jobs: the api is unreachable")
			},
			want: "the api is unreachable",
		},
		{
			name:   "the doctor printed nothing",
			doctor: func(context.Context) ([]byte, error) { return []byte("\n\t\n"), nil },
			want:   "printed nothing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := evidenceEnv(t)
			id := evidenceID()
			ev, err := Collect(context.Background(), id, Options{
				Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
				Open: func(cmdlog.ID) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("Failed in 1.2s\n")), nil
				},
				Doctor: tc.doctor,
			})
			if err != nil {
				t.Fatalf("Collect with %s: %v, want the collection to carry on: a diagnosis is not lost over the doctor", tc.name, err)
			}
			got := evidenceRead(t, ev.EvidenceDir)["doctor.txt"]
			if !strings.Contains(got, tc.want) {
				t.Errorf("doctor.txt = %q, want it to carry %q", got, tc.want)
			}
			if _, ok := evidenceRead(t, ev.EvidenceDir)[id.String()+".log"]; !ok {
				t.Error("the log is missing: a missing doctor is not a missing diagnosis")
			}
		})
	}
}

// TestEvidenceCollectNotesADoctorThatTimesOut: the timeout is the collector's
// own (F23), so a doctor that never answers is not the diagnosis's hang. The
// note says so, because the agent has to be able to tell "doctor said nothing
// is wrong" from "doctor never answered".
func TestEvidenceCollectNotesADoctorThatTimesOut(t *testing.T) {
	was := doctorTimeout
	doctorTimeout = 20 * time.Millisecond
	t.Cleanup(func() { doctorTimeout = was })

	getenv := evidenceEnv(t)
	id := evidenceID()
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	ev, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("Failed in 1.2s\n")), nil
		},
		Doctor: func(ctx context.Context) ([]byte, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-blocked:
				return []byte("the doctor answered too late\n"), nil
			}
		},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := evidenceRead(t, ev.EvidenceDir)["doctor.txt"]
	if !strings.Contains(got, "did not finish within") {
		t.Errorf("doctor.txt = %q, want a note that doctor did not finish in time", got)
	}
	if strings.Contains(got, "too late") {
		t.Error("doctor.txt carries a doctor's answer to a call that was abandoned")
	}
}

// TestEvidenceCollectKeepsTheEndOfAnOversizeLog: a log is unbounded, and a
// command that streamed gigabytes would otherwise be read into memory whole.
// What is kept is the end, because that is where a failed command writes why
// it failed, and the file says what was left out — a truncated log that
// claimed to be the whole one would be a lie the diagnosis rests on.
func TestEvidenceCollectKeepsTheEndOfAnOversizeLog(t *testing.T) {
	was := maxLogBytes
	maxLogBytes = 256
	t.Cleanup(func() { maxLogBytes = was })

	getenv := evidenceEnv(t)
	id := evidenceID()
	head := "step: the beginning of a very long stream\n"
	tail := "Step failed: quota exceeded\n"
	logText := head + strings.Repeat("a long line of installation output\n", 20) + tail
	ev, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(logText)), nil },
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := evidenceRead(t, ev.EvidenceDir)[id.String()+".log"]
	if !strings.Contains(got, tail) {
		t.Errorf("the log file does not hold the end of the log: %q", got)
	}
	if strings.Contains(got, head) {
		t.Errorf("the log file holds the beginning of a log past the cap: %q", got)
	}
	if !strings.Contains(got, "only its last") {
		t.Errorf("the log file does not say what was left out: %q", got)
	}
}

// TestEvidenceCollectReplacesAnEarlierCollection: the folder belongs to the run
// ID (one diagnosis per failed command), so a second collection writes over the
// first's files. A file the first left and the second does not write would be
// evidence for a diagnosis nobody ran here.
func TestEvidenceCollectReplacesAnEarlierCollection(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	first := evidenceValue(t, "first")
	collect := func(logText string) Evidence {
		t.Helper()
		ev, err := Collect(context.Background(), id, Options{
			Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
			Open:    func(cmdlog.ID) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(logText)), nil },
			Secrets: []string{first},
		})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		return ev
	}
	ev := collect("Failed in 1.2s: " + first + "\n")
	collect("Failed in 1.2s: a second run\n")
	files := evidenceRead(t, ev.EvidenceDir)
	for name, text := range files {
		if strings.Contains(text, first) {
			t.Errorf("%s still carries the first collection's text: a second collection replaces the folder", name)
		}
	}
}

// TestEvidenceCollectRefusesWhatIsNotARunID: the folder's name comes from the
// run ID, so an ID nothing wrote is refused before anything is created. The
// state directory is left as it was: a refusal writes nothing.
func TestEvidenceCollectRefusesWhatIsNotARunID(t *testing.T) {
	for _, id := range []cmdlog.ID{
		{Command: "../logs", At: time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC), PID: 1},
		{Command: "Update", At: time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC), PID: 1},
		{Command: "", At: time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC), PID: 1},
	} {
		getenv := evidenceEnv(t)
		_, err := Collect(context.Background(), id, Options{
			Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
			Open: func(cmdlog.ID) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("Failed in 1.2s\n")), nil
			},
		})
		switch {
		case err == nil:
			t.Errorf("Collect(%q) = nil error, want a refusal: that is not a run ID a log is filed under", id)
		case !strings.Contains(err.Error(), "run ID"):
			t.Errorf("Collect(%q) = %v, want the refusal to name what a run ID is", id, err)
		}
	}
}

// TestEvidenceCollectFailsWhenTheLogCannotBeRead: the log is the evidence the
// diagnosis is about, so a run whose log cannot be read is not a diagnosis with
// less evidence — it is no diagnosis. Nothing is written: the folder is created
// after the log is in hand.
func TestEvidenceCollectFailsWhenTheLogCannotBeRead(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	_, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open:   func(cmdlog.ID) (io.ReadCloser, error) { return nil, cmdlog.ErrNoLog },
		Doctor: func(context.Context) ([]byte, error) { return []byte("all checks are ok\n"), nil },
	})
	if !errors.Is(err, cmdlog.ErrNoLog) {
		t.Fatalf("Collect = %v, want the log read's own error", err)
	}
	entries, err := os.ReadDir(getenv("XDG_STATE_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a collection with no log left %d entries in the state directory: nothing is created before the log is in hand", len(entries))
	}
}

// TestEvidenceCollectGivesUpOnALogThatNeverProgresses: Open is a seam, so the
// reader behind it is not always the log store's own file. A reader that
// answers every call with nothing and no error must end the collection rather
// than spin it: a diagnosis that never returns is a diagnosis nobody gets.
func TestEvidenceCollectGivesUpOnALogThatNeverProgresses(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	_, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) {
			return io.NopCloser(evidenceStall{}), nil
		},
	})
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("Collect = %v, want the read to give up: an empty read is not the end of a stream", err)
	}
}

// evidenceStall reads nothing forever without ever saying so.
type evidenceStall struct{}

func (evidenceStall) Read([]byte) (int, error) { return 0, nil }

// evidenceNames is a folder's file names, sorted, for a failure message.
func evidenceNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// evidenceMode is one file's permission bits.
func evidenceMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}
