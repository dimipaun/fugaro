package runstore

import (
	"errors"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func TestReplaceRecordIf(t *testing.T) {
	for name, b := range map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	} {
		t.Run(name, func(t *testing.T) {
			s := Open(b.Bucket, "acme-app", runID)
			if _, _, err := ReadRecordVersion(ctx, b, "acme-app", runID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("ReadRecordVersion of a missing record = %v", err)
			}
			if err := s.WriteRecord(ctx, &Record{Version: 1, RunID: runID, Stage: "bootstrap"}); err != nil {
				t.Fatal(err)
			}
			rec, v, err := ReadRecordVersion(ctx, b, "acme-app", runID)
			if err != nil || rec.Stage != "bootstrap" {
				t.Fatalf("ReadRecordVersion = %+v, %v", rec, err)
			}
			rec.Stage = "implement"
			if err := ReplaceRecordIf(ctx, b, "acme-app", runID, rec, v); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.ReadRecord(ctx); got.Stage != "implement" {
				t.Fatalf("stored = %+v", got)
			}
			// v is stale now: a replace against it must not land.
			rec.Stage = "fix"
			if err := ReplaceRecordIf(ctx, b, "acme-app", runID, rec, v); !errors.Is(err, ErrChanged) {
				t.Fatalf("stale ReplaceRecordIf = %v", err)
			}
			if got, _ := s.ReadRecord(ctx); got.Stage != "implement" {
				t.Fatalf("a stale replace landed: %+v", got)
			}
		})
	}
}
