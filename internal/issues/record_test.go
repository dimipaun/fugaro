package issues

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// recordedAt is the clock the recording tests run on, and recordStem the run
// they are about: D1609's `<command>-<14-digit UTC>-<pid>`.
var recordedAt = time.Date(2026, 11, 8, 18, 5, 9, 0, time.UTC)

const recordStem = "update-20261108180509-48213"

// spyStore is the local issues folder as the recording seam sees it: it counts
// what was read and keeps what was written, so "a local finding writes
// nothing" and "a run's finding is saved once" are asserted rather than read
// off the folder afterwards.
type spyStore struct {
	issues []Issue
	lists  int
	saves  []Issue
}

func (s *spyStore) List() ([]Issue, error) {
	s.lists++
	return s.issues, nil
}

func (s *spyStore) Save(i Issue) error {
	s.saves = append(s.saves, i)
	return nil
}

// errSharedTouched is what the spy bucket answers with, so a seam that did
// reach the shared store would fail loudly rather than quietly pass.
var errSharedTouched = errors.New("record_test: the recording seam touched the shared store")

// spyBucket is the project's shared store as the seam could see it: a driver
// counting every read, write and listing, so "recording a verdict touches no
// shared state" (F26) is a test and not a reading of record.go.
type spyBucket struct {
	driver.Bucket // nil: every method but the three below is one nobody calls
	reads         int
	writes        int
	lists         int
}

func (b *spyBucket) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (b *spyBucket) As(any) bool                        { return false }
func (b *spyBucket) ErrorAs(error, any) bool            { return false }
func (b *spyBucket) Close() error                       { return nil }

func (b *spyBucket) NewRangeReader(context.Context, string, int64, int64, *driver.ReaderOptions) (driver.Reader, error) {
	b.reads++
	return nil, errSharedTouched
}

func (b *spyBucket) NewTypedWriter(context.Context, string, string, *driver.WriterOptions) (driver.Writer, error) {
	b.writes++
	return nil, errSharedTouched
}

func (b *spyBucket) ListPaged(context.Context, *driver.ListOptions) (*driver.ListPage, error) {
	b.lists++
	return nil, errSharedTouched
}

// touched is everything the shared store saw, as one number a test compares
// with zero.
func (b *spyBucket) touched() int { return b.reads + b.writes + b.lists }

// recordShared is the spy bucket as the seam takes it.
func recordShared(b *spyBucket) *blobx.Bucket { return blobx.Wrap(blob.NewBucket(b)) }

// recordRun is the run a verdict is recorded from: one stem, one reporter, one
// clock, the failed command's argv and one excerpt of its log.
func recordRun(store IssueStore, shared *blobx.Bucket) RunMeta {
	return RunMeta{
		Stem:          recordStem,
		Reporter:      "user:ana@example.test",
		Created:       recordedAt,
		Command:       []string{"fugaro", "update", "--repo", "acme/widgets"},
		Logs:          []string{"the second apply hangs"},
		FugaroVersion: "0.22.0",
		OS:            "linux",
		Store:         store,
		Shared:        shared,
	}
}

// recordVerdict is a verdict of one outcome, with the signature the two saved
// ones are matched by and the fix steps the two local ones print.
func recordVerdict(outcome Outcome) Verdict {
	return Verdict{
		Outcome:   outcome,
		Summary:   "the second apply hangs after the provider cache is refreshed",
		Diagnosis: "the provider cache is stale",
		Steps:     "1. run fugaro update\n2. wait for the first apply",
		FixSteps:  "run fugaro update again",
		Signature: "fugaro update: exit status 1 (apply failed)",
	}
}

// corpusRow is one row of the redaction corpus by name: the text a test plants
// and the value that must not survive it. The corpus itself is the exhaustive
// one (redact_test.go), and a row is reused here rather than written again, so
// the two tests cannot drift apart about what a secret looks like.
func corpusRow(t *testing.T, name string) (in, leak string) {
	t.Helper()
	for _, row := range corpus {
		if row.name == name {
			return row.in, row.leak
		}
	}
	t.Fatalf("the corpus has no row named %q", name)
	return "", ""
}

// TestRecordLocalOutcomesWriteNothing: a finding that is this machine's alone
// — a wrong setting, a bug a newer release already fixes — is answered with
// its fix steps, and the store is not touched at all: no file is written, and
// nothing is even read (acceptance 1, F6).
func TestRecordLocalOutcomesWriteNothing(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeConfiguration, OutcomeFixedInNewerRelease} {
		t.Run(string(outcome), func(t *testing.T) {
			store, bucket := &spyStore{}, &spyBucket{}
			v := recordVerdict(outcome)
			got, err := Record(v, recordRun(store, recordShared(bucket)), nil)
			if err != nil {
				t.Fatalf("Record(%s): %v", outcome, err)
			}
			if got.FixSteps != v.FixSteps {
				t.Errorf("FixSteps = %q, want the agent's %q", got.FixSteps, v.FixSteps)
			}
			if got.Line != "" {
				t.Errorf("Line = %q, want nothing printed for a local finding", got.Line)
			}
			if got.Issue.LocalID != "" || got.Issue.Summary != "" {
				t.Errorf("Issue = %+v, want nothing saved", got.Issue)
			}
			if store.lists != 0 || len(store.saves) != 0 {
				t.Errorf("the local store saw %d list(s) and %d save(s), want none", store.lists, len(store.saves))
			}
			if n := bucket.touched(); n != 0 {
				t.Errorf("the shared store saw %d call(s), want none", n)
			}
		})
	}
}

// TestRecordBugAndUnclearSaveRedacted: a bug and an unclear finding are saved,
// under their own class, and what is written is the redacted text — the stored
// bytes carry no secret of any kind the gate knows, and none of the values
// this machine holds (acceptance 2, F5).
func TestRecordBugAndUnclearSaveRedacted(t *testing.T) {
	token, tokenLeak := corpusRow(t, "github token")
	password, passwordLeak := corpusRow(t, "password")
	email, emailLeak := corpusRow(t, "email")
	home, homeLeak := corpusRow(t, "home path")
	known := "known-" + strings.Repeat("Qwzw", 4)

	for _, outcome := range []Outcome{OutcomeBug, OutcomeUnclear} {
		t.Run(string(outcome), func(t *testing.T) {
			state, env := issueState(t)
			store := issueStore(t, env)
			v := recordVerdict(outcome)
			v.Summary = "the update stops half way: " + token
			v.Diagnosis = "the connection was refused: " + password
			v.Steps = "1. run fugaro update from " + home
			run := recordRun(store, nil)
			run.Command = []string{"fugaro", "config", "set", "--token", known}
			run.Logs = []string{email, "the value " + known + " was rejected"}
			if _, err := Record(v, run, []string{known}); err != nil {
				t.Fatalf("Record(%s): %v", outcome, err)
			}
			path := filepath.Join(state, "fugaro", "issues", "local-"+recordStem+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the record is not at %s: %v", path, err)
			}
			for _, leak := range []string{tokenLeak, passwordLeak, emailLeak, homeLeak, known} {
				if strings.Contains(string(data), leak) {
					t.Errorf("%q survived into the saved record:\n%s", leak, data)
				}
			}
			if !strings.Contains(string(data), redactedValue) {
				t.Errorf("nothing in the record was redacted:\n%s", data)
			}
			got, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("the saved record does not read back: %v", err)
			}
			class, ok := v.Saved()
			if !ok || got.Class != class {
				t.Errorf("class = %q, want %q", got.Class, class)
			}
			if got.Fingerprint != Fingerprint(v.Signature) {
				t.Errorf("fingerprint = %q, want %q", got.Fingerprint, Fingerprint(v.Signature))
			}
			if got.Status != StatusPending || got.Number != 0 || got.Uploaded {
				t.Errorf("a saved issue is pending and unnumbered, got %q, %d, uploaded=%v", got.Status, got.Number, got.Uploaded)
			}
			if !strings.Contains(got.FailedCommand, "fugaro config set") || strings.Contains(got.FailedCommand, known) {
				t.Errorf("failed command = %q, want the command with its values redacted", got.FailedCommand)
			}
		})
	}
}

// TestRecordLineNamesTheSubmitCommand: the line a saved issue is named by takes
// the argument the command takes — the local ID until the issue is uploaded and
// its number from then on (F23) — and a run whose finding is already saved is
// named as it stands, with no second file written for it.
func TestRecordLineNamesTheSubmitCommand(t *testing.T) {
	t.Run("before an upload", func(t *testing.T) {
		store, bucket := &spyStore{}, &spyBucket{}
		got, err := Record(recordVerdict(OutcomeBug), recordRun(store, recordShared(bucket)), nil)
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		want := "Saved issue local-" + recordStem + ". Run 'fugaro issue submit local-" + recordStem + "' to file it on GitHub."
		if got.Line != want {
			t.Errorf("Line = %q, want %q", got.Line, want)
		}
		if len(store.saves) != 1 {
			t.Errorf("the store saved %d issue(s), want 1", len(store.saves))
		}
	})

	t.Run("after an upload", func(t *testing.T) {
		// The record the store already holds is the sample one, uploaded as
		// #0007; the verdict is about the same run, so the issue is already
		// there and is named by its number.
		state, env := issueState(t)
		store := issueStore(t, env)
		if err := store.Save(issueSample()); err != nil {
			t.Fatalf("seeding the store: %v", err)
		}
		run := recordRun(store, nil)
		run.Stem = issueSample().Run
		got, err := Record(recordVerdict(OutcomeBug), run, nil)
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		want := "Saved issue #0007. Run 'fugaro issue submit 7' to file it on GitHub."
		if got.Line != want {
			t.Errorf("Line = %q, want %q", got.Line, want)
		}
		if got.Issue.Number != issueSample().Number {
			t.Errorf("Issue = %+v, want the record that is already there", got.Issue)
		}
		entries, err := os.ReadDir(filepath.Join(state, "fugaro", "issues"))
		if err != nil {
			t.Fatalf("reading the folder: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("the folder holds %d file(s), want the one record: %v", len(entries), entries)
		}
	})
}

// TestRecordNeverSubmits: recording a verdict files nothing. The record is the
// local one a person can still change their mind about: no number, no URL, not
// uploaded — and the shared store saw no write.
func TestRecordNeverSubmits(t *testing.T) {
	store, bucket := &spyStore{}, &spyBucket{}
	got, err := Record(recordVerdict(OutcomeBug), recordRun(store, recordShared(bucket)), nil)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.Issue.Number != 0 || got.Issue.URL != "" || got.Issue.Uploaded {
		t.Errorf("Issue = %+v, want an unnumbered, unfiled record", got.Issue)
	}
	// The record is written all the same: this test is about what is filed,
	// not about a seam that did nothing at all.
	if len(store.saves) != 1 {
		t.Errorf("the local store saved %d issue(s), want 1", len(store.saves))
	}
	if bucket.writes != 0 {
		t.Errorf("the shared store saw %d write(s), want none", bucket.writes)
	}
}

// TestRecordChecksNoDuplicates: troubleshoot never looks for duplicates — all
// matching happens at upload (spec §2, F26). A second finding with the same
// signature is saved beside the first rather than merged into it, and the
// shared store is not read or listed to find that out.
func TestRecordChecksNoDuplicates(t *testing.T) {
	first := issueSample()
	first.Number, first.Uploaded, first.URL = 0, false, ""
	first.LocalID, first.Run = "local-prior-20261108180000-1111", "prior-20261108180000-1111"
	first.Fingerprint = Fingerprint(recordVerdict(OutcomeBug).Signature)
	store, bucket := &spyStore{issues: []Issue{first}}, &spyBucket{}
	got, err := Record(recordVerdict(OutcomeBug), recordRun(store, recordShared(bucket)), nil)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(store.saves) != 1 || got.Issue.LocalID != "local-"+recordStem {
		t.Errorf("saved %d issue(s) as %q, want the run's own record beside the one already there", len(store.saves), got.Issue.LocalID)
	}
	if got.Issue.Fingerprint != first.Fingerprint {
		t.Errorf("fingerprint = %q, want the same one the saved issue carries", got.Issue.Fingerprint)
	}
	if bucket.reads != 0 || bucket.lists != 0 {
		t.Errorf("the shared store saw %d read(s) and %d list(s), want none", bucket.reads, bucket.lists)
	}
}
