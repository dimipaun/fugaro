package imagecheck

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

func TestReadStatus(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	st, err := ReadStatus(ctx, b, "slug", "web", now)
	if err != nil || st.Image != nil || st.Check != nil {
		t.Fatalf("nothing recorded: %+v, %v", st, err)
	}

	rec := Record{Version: RecordVersion, Workflow: "web", BuiltAt: now.Add(-50 * time.Hour), SourceCommit: "c1", BaseDigest: digestA, ImageDigest: digestB, BuildID: "b1"}
	data, _ := json.Marshal(rec)
	if _, err := b.Create(ctx, RecordKey("slug", "web"), data, "application/json"); err != nil {
		t.Fatal(err)
	}
	cs := CheckState{Version: CheckVersion, CheckedAt: now.Add(-time.Hour), Decision: RebuildFailedLast, Reasons: []string{ReasonBase}, BuildID: "b2", LastBuildStatus: "FAILURE"}
	data, _ = cs.Marshal()
	if _, err := b.Create(ctx, CheckKey("slug", "web"), data, "application/json"); err != nil {
		t.Fatal(err)
	}
	st, err = ReadStatus(ctx, b, "slug", "web", now)
	if err != nil || st.Image == nil || st.Check == nil {
		t.Fatalf("ReadStatus = %+v, %v", st, err)
	}
	if st.Image.AgeS != 50*3600 || st.Image.SourceCommit != "c1" || st.Check.Decision != RebuildFailedLast || st.Check.LastBuildStatus != "FAILURE" {
		t.Fatalf("status = %+v %+v", st.Image, st.Check)
	}

	// An unreadable object is reported, not fatal.
	if err := b.WriteAll(ctx, CheckKey("slug", "web"), []byte("{"), nil); err != nil {
		t.Fatal(err)
	}
	st, err = ReadStatus(ctx, b, "slug", "web", now)
	if err != nil || st.Check != nil || len(st.Errors) != 1 {
		t.Fatalf("unreadable check.json: %+v, %v", st, err)
	}
}
