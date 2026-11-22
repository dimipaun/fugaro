package runner

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// summaryRun is a run whose only business is the summary it writes beside its
// record: the store it writes to, the bucket it is opened over and the clock
// the summary's own moment comes from.
func summaryRun(t *testing.T) (*run, *runstore.Store, *runstore.Record) {
	t.Helper()
	const (
		slug = "github-acme-app"
		id   = "20261119-130000-abcd"
	)
	b := blobx.Wrap(memblob.OpenBucket(nil))
	t.Cleanup(func() { b.Close() })
	s := runstore.Open(b.Bucket, slug, id)
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	r := &run{
		d:    Deps{Store: s, Bucket: b, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return at }},
		spec: &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "the run's prompt", Title: "the run's title"},
	}
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusRunning, Stage: "implement", Execution: "projects/p/executions/e-1",
		StartedAt: at.Add(-time.Hour), CostUSD: 0.5,
	}
	return r, s, rec
}

// TestSaveSummaryWritesTheRunSummary: the runner writes the object the table
// reads, beside the record it was handed, at the moment of its own clock and
// under the run's own name -- the identity stamped by the store and never by
// the caller -- with the task's title already resolved the way a row prints
// it. This is the writer half of RL4: nothing else in the tree writes a
// summary, so a run that stops writing one costs every listing of it its
// speedup and the row still comes from the three objects.
func TestSaveSummaryWritesTheRunSummary(t *testing.T) {
	ctx := context.Background()
	r, s, rec := summaryRun(t)
	r.saveSummary(ctx, rec)

	sum, err := s.ReadSummary(ctx)
	if err != nil {
		t.Fatalf("the run wrote no summary: %v", err)
	}
	if sum.Version != runstore.SummaryVersion || sum.RunID != s.RunID() || sum.Slug != s.Slug() {
		t.Errorf("the summary names %d %s/%s, want the run the store was opened with", sum.Version, sum.Slug, sum.RunID)
	}
	if !sum.At.Equal(r.d.Now()) {
		t.Errorf("the summary is stamped %s, want the run's own clock %s", sum.At, r.d.Now())
	}
	back := sum.AsRecord()
	if back.Status != rec.Status || back.Stage != rec.Stage || back.Execution != rec.Execution ||
		back.StartedAt != rec.StartedAt || back.CostUSD != rec.CostUSD {
		t.Errorf("the summary's record = %+v, want the record it was written from: %+v", back, rec)
	}
	if got := sum.Spec().TitleLine(); got != r.spec.TitleLine() {
		t.Errorf("the summary's title = %q, want the title a row prints, %q", got, r.spec.TitleLine())
	}
	key := s.Prefix() + runstore.SummaryName
	if ok, err := r.d.Bucket.Exists(ctx, key); err != nil || !ok {
		t.Errorf("the summary is not at %s: %v, %v", key, ok, err)
	}
}

// TestSaveSummaryWithoutARecordOrABucketWritesNothing: a run too early to have
// a record -- and a run whose Deps carry no bucket at all, as most of this
// package's tests are -- writes no summary, and that is not a failure: a
// summary stands in for a record, so one written before the record exists
// would be a listing's row of a run that never happened. The fallback is what
// answers those runs.
func TestSaveSummaryWithoutARecordOrABucketWritesNothing(t *testing.T) {
	ctx := context.Background()
	r, s, _ := summaryRun(t)
	r.saveSummary(ctx, nil)
	if _, err := s.ReadSummary(ctx); !errors.Is(err, runstore.ErrNotFound) {
		t.Errorf("a summary was written with no record to stand in for: %v", err)
	}

	noBucket := &run{d: Deps{Store: s, Log: slog.New(slog.DiscardHandler), Now: time.Now}, spec: r.spec}
	noBucket.saveSummary(ctx, &runstore.Record{Version: 1, RunID: s.RunID(), Status: runstore.StatusRunning})
	if _, err := s.ReadSummary(ctx); !errors.Is(err, runstore.ErrNotFound) {
		t.Errorf("a run with no bucket wrote a summary: %v", err)
	}
}
