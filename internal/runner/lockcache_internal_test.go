package runner

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const (
	ownerExec = "projects/p/locations/r/jobs/j/executions/j-aaaaa"
	dupExec   = "projects/p/locations/r/jobs/j/executions/j-bbbbb"
)

// TestDisownRecordNeverOverwritesTheOwner: a duplicate rewrites only the
// record it created; one naming the owner (it wrote it in the meantime) is
// left byte for byte.
func TestDisownRecordNeverOverwritesTheOwner(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		recordExec string
		wantExec   string
	}{
		"owner's record": {ownerExec, ownerExec},
		"own record":     {dupExec, ownerExec},
	} {
		t.Run(name, func(t *testing.T) {
			b := memblob.OpenBucket(nil)
			defer b.Close()
			store := runstore.Open(b, "acme-app", "20260926-221530-abcd")
			stage := map[string]string{ownerExec: "implement", dupExec: "bootstrap"}[tc.recordExec]
			if err := store.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: "20260926-221530-abcd", Execution: tc.recordExec, Status: runstore.StatusRunning, Stage: stage}); err != nil {
				t.Fatal(err)
			}
			before, _ := b.ReadAll(ctx, store.Prefix()+"result.json")
			r := &run{d: Deps{Store: store, Bucket: blobx.Wrap(b), Execution: dupExec, Log: slog.New(slog.DiscardHandler)}}
			expires := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			r.disownRecord(ctx, lock.Holder{RunID: "20260926-221530-abcd", Execution: ownerExec, ExpiresAt: expires})
			after, _ := b.ReadAll(ctx, store.Prefix()+"result.json")
			got, err := store.ReadRecord(ctx)
			if err != nil || got.Execution != tc.wantExec || got.Stage != stage {
				t.Fatalf("record = %+v, %v", got, err)
			}
			if tc.recordExec == ownerExec && !bytes.Equal(before, after) {
				t.Fatalf("the owner's record was rewritten:\n%s\n->\n%s", before, after)
			}
			if tc.recordExec == dupExec && (got.Deadline == nil || !got.Deadline.Equal(expires)) {
				t.Fatalf("deadline = %v, want the lock's expiry %s", got.Deadline, expires)
			}
		})
	}
}
