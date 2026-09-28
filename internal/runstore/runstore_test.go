package runstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

var ctx = context.Background()

const runID = "20260926-221530-abcd"

func newStore(t *testing.T) *Store {
	t.Helper()
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { b.Close() })
	return Open(b, "acme-app", runID)
}

func TestTaskRoundTrip(t *testing.T) {
	s := newStore(t)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "do it"}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadTask(ctx)
	if err != nil || got.Task != "do it" || got.Repo != "acme/app" {
		t.Fatalf("ReadTask = %+v, %v", got, err)
	}
}

func TestRecordRoundTripAndNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.ReadRecord(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadRecord on an empty store err = %v, want ErrNotFound", err)
	}
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, Passed: true}}, StartedAt: time.Now().UTC()}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.Status != StatusSucceeded || len(got.Verify) != 1 {
		t.Fatalf("ReadRecord = %+v, %v", got, err)
	}
}

func TestCancelMarker(t *testing.T) {
	s := newStore(t)
	if ok, err := s.CancelRequested(ctx); err != nil || ok {
		t.Fatalf("CancelRequested before = %v, %v", ok, err)
	}
	if err := s.RequestCancel(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CancelRequested(ctx); err != nil || !ok {
		t.Fatalf("CancelRequested after = %v, %v", ok, err)
	}
}

func TestPutFileUnderPrefix(t *testing.T) {
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := Open(b, "acme-app", runID)
	if err := s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte("{}\n"), "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	key := "runs/acme-app/" + runID + "/transcripts/implement-1.jsonl"
	if ok, _ := b.Exists(ctx, key); !ok || s.Prefix() != "runs/acme-app/"+runID+"/" {
		t.Fatalf("object %s missing or prefix %q wrong", key, s.Prefix())
	}
}

func TestParseRef(t *testing.T) {
	if slug, id, err := ParseRef("acme-app/" + runID); err != nil || slug != "acme-app" || id != runID {
		t.Fatalf("ParseRef = %q %q %v", slug, id, err)
	}
	// Every slug task.Slug produces for a valid repo must parse back.
	for _, repo := range []string{"acme/my_service", "Acme/Server", "acme/app.v2", "org/sub/repo-x"} {
		spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: "main", Task: "t"}
		if err := spec.Validate(); err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		want, err := task.Slug("github", repo)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		if slug, id, err := ParseRef(want + "/" + runID); err != nil || slug != want || id != runID {
			t.Errorf("ParseRef(Slug(%q)) = %q %q %v", repo, slug, id, err)
		}
	}
	for _, bad := range []string{"", "acme-app", "a/b/c", "acme-app/not-a-run", "./" + runID, "../" + runID, "acme.app/" + runID} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) succeeded", bad)
		}
	}
}

// TestFinalizeReserve: the record carries the run's finalize reserve, so
// cancel can floor its grace without a checkout; an older record has none.
func TestFinalizeReserve(t *testing.T) {
	if d, ok := (&Record{FinalizeReserveS: 90}).FinalizeReserve(); !ok || d != 90*time.Second {
		t.Fatalf("FinalizeReserve = %v, %v", d, ok)
	}
	if d, ok := (&Record{}).FinalizeReserve(); ok || d != 0 {
		t.Fatalf("FinalizeReserve of a record without one = %v, %v", d, ok)
	}
}
