// Package verify runs a repository's build and test commands on behalf of the
// agent and records each result in the run's state directory (design §4.3).
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/procgroup"
)

const (
	settingsFile = "workflow.json"
	recordsDir   = "verify"

	// commandGrace is how long a timed-out or cancelled verify command's
	// group gets between SIGTERM and SIGKILL. It is shorter than the agent
	// group's 10s, so when the agent's stage is killed, `fugaro verify`
	// (which lives in the agent's group) still has time to SIGKILL its own
	// test group before it is itself SIGKILLed and orphans that group.
	commandGrace = 5 * time.Second
)

// Settings is what the runner tells `fugaro verify` about the workflow.
type Settings struct {
	RepoDir     string              `json:"repo_dir"`
	Build       string              `json:"build"`
	Test        string              `json:"test"`
	RerunFailed *config.RerunFailed `json:"rerun_failed,omitempty"`
	Reports     []string            `json:"reports"`
	TimeoutS    int                 `json:"timeout_s"`
}

// WriteSettings stores settings in stateDir and prepares the records directory.
func WriteSettings(stateDir string, s Settings) error {
	if err := os.MkdirAll(filepath.Join(stateDir, recordsDir), 0o755); err != nil {
		return fmt.Errorf("creating verify state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding verify settings: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, settingsFile), data, 0o644); err != nil {
		return fmt.Errorf("writing verify settings: %w", err)
	}
	return nil
}

// ClearState removes the settings and records an earlier run left in
// stateDir, and nothing else there.
func ClearState(stateDir string) error {
	for _, name := range []string{settingsFile, recordsDir} {
		if err := os.RemoveAll(filepath.Join(stateDir, name)); err != nil {
			return fmt.Errorf("removing stale %s: %w", name, err)
		}
	}
	return nil
}

// LoadSettings reads the settings the runner wrote.
func LoadSettings(stateDir string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(filepath.Join(stateDir, settingsFile))
	if err != nil {
		return s, fmt.Errorf("reading verify settings: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("decoding verify settings: %w", err)
	}
	return s, nil
}

// Kind is what a verify run executes.
type Kind string

const (
	KindBuild Kind = "build"
	KindTest  Kind = "test"
)

// Record is the result of one `fugaro verify` invocation.
type Record struct {
	N         int       `json:"n"`
	Kind      Kind      `json:"kind"`
	Rerun     bool      `json:"rerun,omitempty"`
	HeadSHA   string    `json:"head_sha"`
	CleanTree bool      `json:"clean_tree"`
	ExitCode  int       `json:"exit_code"`
	TimedOut  bool      `json:"timed_out,omitempty"`
	Passed    bool      `json:"passed"`
	Tests     int       `json:"tests"`
	Failures  int       `json:"failures"`
	Skipped   int       `json:"skipped"`
	Failed    []string  `json:"failed,omitempty"`
	Flaky     []string  `json:"flaky,omitempty"`
	Warning   string    `json:"warning,omitempty"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
}

// Summary is the one-line result printed for the agent.
func (r Record) Summary() string {
	kind := string(r.Kind)
	if r.Rerun {
		kind += " --rerun-failed"
	}
	status := "passed"
	if !r.Passed {
		status = "FAILED"
	}
	s := fmt.Sprintf("fugaro verify %s #%d: %s (exit %d", kind, r.N, status, r.ExitCode)
	if r.Kind == KindTest {
		s += fmt.Sprintf(", %d tests, %d failures", r.Tests, r.Failures)
	}
	if len(r.Failed) > 0 {
		s += ": " + strings.Join(r.Failed, ", ")
	}
	if len(r.Flaky) > 0 {
		s += "; flaky: " + strings.Join(r.Flaky, ", ")
	}
	if r.TimedOut {
		s += "; timed out"
	}
	if r.Warning != "" {
		s += "; warning: " + r.Warning
	}
	if !r.CleanTree {
		s += "; working tree not clean, so this run cannot mark the PR ready: commit first"
	}
	return s + ")"
}

// Options configure one verify run.
type Options struct {
	StateDir string
	Kind     Kind
	Rerun    bool
	Env      []string // nil means the current process environment
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

// Run executes the configured command, parses fresh reports and records the result.
func Run(ctx context.Context, o Options) (Record, error) {
	s, err := LoadSettings(o.StateDir)
	if err != nil {
		return Record{}, err
	}
	repo, err := gitops.Open(s.RepoDir, nil)
	if err != nil {
		return Record{}, err
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	sha, err := repo.HeadSHA(ctx)
	if err != nil {
		return Record{}, err
	}
	clean, err := repo.IsClean(ctx)
	if err != nil {
		return Record{}, err
	}

	var command string
	var prev *Record
	switch o.Kind {
	case KindBuild:
		if o.Rerun {
			return Record{}, errors.New("--rerun-failed only applies to test")
		}
		command = s.Build
	case KindTest:
		command = s.Test
		if o.Rerun {
			if prev, command, err = rerunCommand(o.StateDir, s, sha, clean); err != nil {
				return Record{}, err
			}
		}
	default:
		return Record{}, fmt.Errorf("unknown verify kind %q (want build or test)", o.Kind)
	}

	start := now()
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if s.TimeoutS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(s.TimeoutS)*time.Second)
	}
	defer cancel()
	code, runErr := procgroup.Run(runCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: s.RepoDir, Env: env,
		Stdout: o.Stdout, Stderr: o.Stderr, Grace: commandGrace,
	})
	timedOut := errors.Is(runErr, context.DeadlineExceeded)
	if runErr != nil && !timedOut {
		return Record{}, runErr
	}

	rec := Record{
		Kind: o.Kind, Rerun: o.Rerun, HeadSHA: sha, CleanTree: clean, ExitCode: code,
		TimedOut: timedOut, StartedAt: start.UTC(), DurationS: now().Sub(start).Seconds(),
	}
	if o.Kind == KindTest {
		cases, err := ReadCases(s.RepoDir, s.Reports, start)
		if err != nil {
			return Record{}, err
		}
		if len(cases) == 0 {
			rec.Warning = "no JUnit reports found under " + strings.Join(s.Reports, ", ")
		}
		if prev == nil {
			rec.Tests, rec.Skipped, rec.Failed = summarize(cases)
		} else {
			rec.Tests, rec.Skipped = prev.Tests, prev.Skipped
			rec.Flaky, rec.Failed = compareRerun(prev.Failed, cases)
		}
		rec.Failures = len(rec.Failed)
	}
	rec.Passed = code == 0 && !timedOut && rec.Failures == 0
	if err := writeRecord(o.StateDir, &rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func rerunCommand(stateDir string, s Settings, sha string, clean bool) (*Record, string, error) {
	if s.RerunFailed == nil {
		return nil, "", errors.New("this repository does not configure commands.rerun_failed")
	}
	prev, err := lastTest(stateDir)
	if err != nil {
		return nil, "", err
	}
	if prev == nil {
		return nil, "", errors.New("no previous `fugaro verify test` run to rerun")
	}
	if len(prev.Failed) == 0 {
		return nil, "", errors.New("the previous test run has no failed tests to rerun")
	}
	if prev.HeadSHA != sha || !prev.CleanTree || !clean {
		return nil, "", errors.New("--rerun-failed needs the previous test run to be on the current commit with a clean working tree; run `fugaro verify test` first")
	}
	return prev, RerunCommand(*s.RerunFailed, prev.Failed), nil
}

// RerunCommand builds rf.Command followed by rf.Each for every test ID.
func RerunCommand(rf config.RerunFailed, ids []string) string {
	parts := []string{rf.Command}
	for _, id := range ids {
		parts = append(parts, strings.ReplaceAll(rf.Each, "{id}", shellQuote(id)))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func summarize(cases []TestCase) (tests, skipped int, failed []string) {
	for _, c := range cases {
		tests++
		if c.Skipped {
			skipped++
		}
		if c.Failed {
			failed = append(failed, c.ID)
		}
	}
	return tests, skipped, failed
}

// compareRerun splits the previously failed tests into those that now pass
// (flaky) and those that still fail or did not run. Only a fresh, non-skipped
// result proves a test re-ran, so a test that was skipped or is missing from
// the rerun's reports (including a rerun that left no reports at all,
// whatever its exit code) stays failed.
func compareRerun(prevFailed []string, cases []TestCase) (flaky, failed []string) {
	ran, failedNow := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if !c.Skipped {
			ran[c.ID] = true
		}
		if c.Failed {
			failedNow[c.ID] = true
		}
	}
	for _, id := range prevFailed {
		if ran[id] && !failedNow[id] {
			flaky = append(flaky, id)
		} else {
			failed = append(failed, id)
		}
	}
	for _, c := range cases {
		if c.Failed && !slices.Contains(prevFailed, c.ID) {
			failed = append(failed, c.ID)
		}
	}
	return flaky, failed
}

func writeRecord(stateDir string, rec *Record) error {
	dir := filepath.Join(stateDir, recordsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("listing verify records: %w", err)
	}
	for n := len(entries) + 1; ; n++ {
		rec.N = n
		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding verify record %d: %w", n, err)
		}
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%04d.json", n)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // a concurrent verify took this number
		}
		if err != nil {
			return fmt.Errorf("writing verify record %d: %w", n, err)
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return fmt.Errorf("writing verify record %d: %w", n, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("writing verify record %d: %w", n, err)
		}
		return nil
	}
}

// Records returns every verify record in stateDir, oldest first.
func Records(stateDir string) ([]Record, error) {
	dir := filepath.Join(stateDir, recordsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading verify records: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	recs := make([]Record, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading verify record %s: %w", name, err)
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		recs = append(recs, r)
	}
	return recs, nil
}

func lastTest(stateDir string) (*Record, error) {
	recs, err := Records(stateDir)
	if err != nil {
		return nil, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == KindTest {
			return &recs[i], nil
		}
	}
	return nil, nil
}
