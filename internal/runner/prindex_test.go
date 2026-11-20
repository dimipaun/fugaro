package runner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The pull-request index the runner writes (docs/runs-ls-speed, RL2W,
// D2962): one object per run under runs/<slug>/pr-index/<N>/<run-id>.json,
// written by the run's own runner and by no other run.

// indexEntries lists the entries of PR n of the fixture repository, failing
// on an object the reader had to skip: a test that wants a count wants the
// count of what a listing would find.
func indexEntries(t *testing.T, b *blob.Bucket, n int) []runstore.PRIndexEntry {
	t.Helper()
	out, err := runstore.ListPRIndex(context.Background(), b, repoSlug, n, func(f string, a ...any) {
		t.Errorf("skipped an index object: "+f, a...)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// indexObjects is every object name under the repository's pr-index prefix,
// whatever number it is filed under: what a test asserting "no entry at all"
// has to look at.
func indexObjects(t *testing.T, b *blob.Bucket) []string {
	t.Helper()
	it := b.List(&blob.ListOptions{Prefix: "runs/" + repoSlug + "/pr-index/"})
	var out []string
	for {
		obj, err := it.Next(context.Background())
		if err != nil {
			break
		}
		if obj.IsDir {
			continue
		}
		out = append(out, obj.Key)
	}
	return out
}

// TestRunnerEntryOnOpenAndFollow: the run that opens the pull request writes
// its own opened entry, and the follow-up that starts on it writes a
// followed one beside it, neither replacing the other.
func TestRunnerEntryOnOpenAndFollow(t *testing.T) {
	h := followUpHarness(t, "", func(h *harness) { withBucket(h) })
	if got := indexEntries(t, h.bucket, 1); len(got) != 1 || got[0].RunID != runID ||
		got[0].Kind != runstore.PRIndexOpened || got[0].Slug != repoSlug || got[0].At.IsZero() {
		t.Fatalf("after the run that opened PR 1: entries = %+v, want one opened entry for %s", got, runID)
	}
	h.followUp(t, followID, runID, "")
	if rec, err := h.run(t, withAnswer(implement("rename"), "Renamed the helper."), review("ship", 0)); err != nil {
		t.Fatal(err)
	} else if rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("the follow-up is not on PR 1: %+v", rec)
	}
	// Newest first (runstore.ListPRIndex): the follow-up's id sorts over the
	// first run's, and both entries are there — the opener's is not replaced
	// by the follow-up's write.
	got := indexEntries(t, h.bucket, 1)
	if len(got) != 2 || got[0].RunID != followID || got[0].Kind != runstore.PRIndexFollowed ||
		got[1].RunID != runID || got[1].Kind != runstore.PRIndexOpened {
		t.Fatalf("after the follow-up: entries = %+v, want %s followed and %s opened", got, followID, runID)
	}
}

// TestRunnerWritesOnlyItsOwnEntry: the entry is the run's own object, so a
// run of the same repository that is already in the index is left exactly as
// it was written.
func TestRunnerWritesOnlyItsOwnEntry(t *testing.T) {
	const other = "20260925-221530-0bad" // another run of the same repository, on PR 1
	h := prHarness(t, prCfg(t, 2, ""))
	b := withBucket(h)
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	theirs := runstore.PRIndexEntry{RunID: other, Slug: repoSlug, PR: 1, Kind: runstore.PRIndexOpened, At: at}
	if err := runstore.PutPRIndex(context.Background(), b.Bucket, theirs); err != nil {
		t.Fatal(err)
	}
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	} else if rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("the run opened no PR 1: %+v", rec)
	}
	got := indexEntries(t, h.bucket, 1)
	if len(got) != 2 || got[0].RunID != runID || got[1].RunID != other {
		t.Fatalf("entries = %+v, want exactly %s and %s", got, runID, other)
	}
	if got[1].RunID != theirs.RunID || got[1].Kind != theirs.Kind || got[1].Slug != theirs.Slug ||
		got[1].PR != theirs.PR || !got[1].At.Equal(theirs.At) {
		t.Errorf("the other run's entry was rewritten: %+v, want %+v", got[1], theirs)
	}
	// The entry is under the writing run's own name.
	if want, err := runstore.PRIndexPath(repoSlug, 1, runID); err != nil || !slices.Contains(indexObjects(t, h.bucket), want) {
		t.Errorf("the bucket does not hold %s (err %v): %q", want, err, indexObjects(t, h.bucket))
	}
}

// lockedBuffer is a log sink the run's own goroutines may write to while the
// test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestEntryWriteFailureIsWarning: an index write that fails is a warning in
// the run's log, and the run finishes exactly as it would have. The bucket
// refuses the write because a plain file sits where the entry's directory
// would go, so nothing can be created under it.
func TestEntryWriteFailureIsWarning(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	dir := t.TempDir()
	b, err := blob.OpenBucket(context.Background(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	h.deps.Bucket = blobx.Wrap(b)
	logs := &lockedBuffer{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	key, err := runstore.PRIndexPath(repoSlug, 1, runID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, filepath.Dir(key))
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("the failed index write ended the run: %+v", rec)
	}
	if !strings.Contains(logs.String(), "writing the pull-request index entry failed") {
		t.Errorf("the run logged no warning about the index:\n%s", logs.String())
	}
	if got := indexEntries(t, b, 1); len(got) != 0 {
		t.Errorf("entries = %+v, want none: the write could not land", got)
	}
}

// TestNoPRNoEntry: a run that opens no pull request writes nothing at all.
// The run is the one the workflow guard refuses: it committed a workflow
// file, so finalize refuses before the push and no pull request is ever
// opened.
func TestNoPRNoEntry(t *testing.T) {
	h := consoleHarness(t)
	withBucket(h)
	refuseAll(t, h.remote, "this hook must never run")
	rec := runRefused(t, h, consoleStep("echo 'on: push' > .github/workflows/new.yml && git add -A && git commit -qm 'Add CI'"), review("ship", 0))
	if rec.PR != nil || len(h.provider.State.PRs) != 0 {
		t.Fatalf("the run opened a pull request: rec = %+v, PRs = %+v", rec, h.provider.State.PRs)
	}
	if got := indexObjects(t, h.bucket); len(got) != 0 {
		t.Errorf("the run wrote %q with no pull request of its own", got)
	}
}
