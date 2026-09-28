package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/task"
)

func TestCreateTaskOnce(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	s := Open(b, "acme-app", "20260926-221530-a1b2")
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x"}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTask(ctx, spec); !errors.Is(err, ErrExists) {
		t.Fatalf("second CreateTask = %v", err)
	}
}

func TestLaunchAndClaim(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	if _, err := s.ReadLaunch(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadLaunch before = %v", err)
	}
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	ok, _, err := s.Claim(ctx, "laptop", at)
	if err != nil || !ok {
		t.Fatalf("first Claim = %v, %v", ok, err)
	}
	ok, existing, err := s.Claim(ctx, "other", at.Add(time.Second))
	if err != nil || ok || existing == nil || existing.Holder != "laptop" || !existing.At.Equal(at) {
		t.Fatalf("second Claim = %v, %+v, %v", ok, existing, err)
	}
	l := &Launch{Version: 1, RunID: "20260926-221530-a1b2", Backend: "cloud-run", Execution: "projects/p/locations/r/jobs/j/executions/e1", Job: "j", LaunchedAt: at}
	if err := s.WriteLaunch(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, l); !errors.Is(err, ErrExists) {
		t.Fatalf("second WriteLaunch = %v", err)
	}
	got, err := s.ReadLaunch(ctx)
	if err != nil || got.Execution != l.Execution {
		t.Fatalf("ReadLaunch = %+v, %v", got, err)
	}
	if s.ClaimKey() != "runs/acme-app/20260926-221530-a1b2/launching" {
		t.Fatalf("ClaimKey = %s", s.ClaimKey())
	}
}

func TestCreateRecordOnce(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	r := &Record{Version: 1, RunID: "20260926-221530-a1b2", Execution: "projects/p/locations/r/jobs/j/executions/e1", Status: StatusRunning, Stage: "bootstrap", Outcome: OutcomeNone}
	if err := s.CreateRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r2 := *r
	r2.Execution = "projects/p/locations/r/jobs/j/executions/e2"
	if err := s.CreateRecord(ctx, &r2); !errors.Is(err, ErrExists) {
		t.Fatalf("second CreateRecord = %v", err)
	}
	if got, _ := s.ReadRecord(ctx); got.Execution != r.Execution {
		t.Fatalf("the second CreateRecord overwrote: %+v", got)
	}
}

var (
	errFakePrecondition = errors.New("fake: precondition failed")
	errFakeNotFound     = errors.New("fake: not found")
	errFakeRemote       = errors.New("fake: backend unavailable")
)

// fakeBucket is a scripted driver for the races memblob can't stage. Only
// the methods runstore uses are implemented; the rest panic.
type fakeBucket struct {
	driver.Bucket
	conflicts int    // how many writes fail with a precondition error first
	attrErr   error  // what Attributes (Exists) returns
	data      []byte // the one object, once written
	writes    int
	reads     int
}

func (f *fakeBucket) ErrorCode(err error) gcerrors.ErrorCode {
	switch {
	case errors.Is(err, errFakePrecondition):
		return gcerrors.FailedPrecondition
	case errors.Is(err, errFakeNotFound):
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}
func (f *fakeBucket) As(any) bool             { return false }
func (f *fakeBucket) ErrorAs(error, any) bool { return false }
func (f *fakeBucket) Close() error            { return nil }

func (f *fakeBucket) Attributes(context.Context, string) (*driver.Attributes, error) {
	return nil, f.attrErr
}

func (f *fakeBucket) NewTypedWriter(context.Context, string, string, *driver.WriterOptions) (driver.Writer, error) {
	f.writes++
	if f.writes <= f.conflicts {
		return nil, errFakePrecondition
	}
	return &fakeWriter{f: f}, nil
}

func (f *fakeBucket) NewRangeReader(context.Context, string, int64, int64, *driver.ReaderOptions) (driver.Reader, error) {
	f.reads++
	if f.data == nil {
		return nil, errFakeNotFound
	}
	return &fakeReader{Reader: bytes.NewReader(f.data)}, nil
}

type fakeWriter struct {
	f   *fakeBucket
	buf bytes.Buffer
}

func (w *fakeWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *fakeWriter) Close() error                { w.f.data = w.buf.Bytes(); return nil }

type fakeReader struct{ *bytes.Reader }

func (r *fakeReader) Close() error                         { return nil }
func (r *fakeReader) Attributes() *driver.ReaderAttributes { return &driver.ReaderAttributes{} }
func (r *fakeReader) As(any) bool                          { return false }

// The claim vanished between our failed create and our read: the
// second create wins.
func TestClaimRetriesWhenTheClaimVanishes(t *testing.T) {
	f := &fakeBucket{conflicts: 1}
	s := Open(blob.NewBucket(f), "acme-app", "20260926-221530-a1b2")
	ok, existing, err := s.Claim(context.Background(), "laptop", time.Now())
	if err != nil || !ok || existing != nil {
		t.Fatalf("Claim = %v, %+v, %v", ok, existing, err)
	}
	if f.writes != 2 || f.reads != 1 {
		t.Fatalf("writes=%d reads=%d, want 2 and 1", f.writes, f.reads)
	}
	var c Claim
	if err := json.Unmarshal(f.data, &c); err != nil || c.Holder != "laptop" {
		t.Fatalf("stored claim %q: %v", f.data, err)
	}
}

// The retry is bounded, and a claim that keeps vanishing is someone else's
// claim in flight, held as of now: the caller waits on it, as it would on
// any fresh claim, instead of failing with "not found".
func TestClaimRetriesOnlyOnce(t *testing.T) {
	f := &fakeBucket{conflicts: 100}
	s := Open(blob.NewBucket(f), "acme-app", "20260926-221530-a1b2")
	at := time.Now()
	ok, existing, err := s.Claim(context.Background(), "laptop", at)
	if ok || err != nil || existing == nil || !existing.At.Equal(at.UTC()) {
		t.Fatalf("Claim = %v, %+v, %v; want held by someone as of now", ok, existing, err)
	}
	if f.writes != 2 || f.reads != 2 {
		t.Fatalf("writes=%d reads=%d, want 2 and 2", f.writes, f.reads)
	}
}

func TestUnreadableClaimIsHeldSinceZero(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	s := Open(b, "acme-app", "20260926-221530-a1b2")
	if _, err := s.ReadClaim(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadClaim before = %v", err)
	}
	if err := b.WriteAll(ctx, s.ClaimKey(), []byte("{garbage"), nil); err != nil {
		t.Fatal(err)
	}
	ok, existing, err := s.Claim(ctx, "laptop", time.Now())
	if err != nil || ok || existing == nil || existing.Holder != "" || !existing.At.IsZero() {
		t.Fatalf("Claim over garbage = %v, %+v, %v", ok, existing, err)
	}
	c, err := s.ReadClaim(ctx)
	if err != nil || c.Holder != "" || !c.At.IsZero() {
		t.Fatalf("ReadClaim garbage = %+v, %v", c, err)
	}
}

func TestReadClaim(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.FixedZone("x", 3600))
	if ok, _, err := s.Claim(ctx, "laptop", at); err != nil || !ok {
		t.Fatalf("Claim = %v, %v", ok, err)
	}
	c, err := s.ReadClaim(ctx)
	if err != nil || c.Holder != "laptop" || !c.At.Equal(at) || c.At.Location() != time.UTC {
		t.Fatalf("ReadClaim = %+v, %v", c, err)
	}
}

func TestRecordDeadlineRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	r := &Record{Version: 1, RunID: "20260926-221530-a1b2", Status: StatusRunning, Outcome: OutcomeNone}
	if err := s.WriteRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	raw, err := s.ReadFile(ctx, "result.json")
	if err != nil || strings.Contains(string(raw), "deadline") {
		t.Fatalf("a nil deadline must be omitted: %s, %v", raw, err)
	}
	d := time.Date(2026, 9, 27, 11, 3, 0, 0, time.UTC)
	r.Deadline = &d
	if err := s.WriteRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.Deadline == nil || !got.Deadline.Equal(d) {
		t.Fatalf("ReadRecord = %+v, %v", got, err)
	}
}
