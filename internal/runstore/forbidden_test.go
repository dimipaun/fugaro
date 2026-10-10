package runstore

import (
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

// PutFile and create write through gocloud's WriteAll directly, so without
// routing through blobx a GCS 403 would come back unclassified (gocloud
// maps it to NotFound), not blobx.ErrForbidden (docs/design/bucket-iam.md
// §2.3).
func TestWritesClassifyForbidden(t *testing.T) {
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "runs/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	s := Open(b.Bucket, "acme-app", runID)
	if err := s.CreateTask(ctx, &task.Spec{RunID: runID, Repo: "acme/app"}); !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("CreateTask (create) = %v, want ErrForbidden", err)
	}
	if err := s.PutFile(ctx, "task.json", []byte("{}"), "application/json"); !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("PutFile = %v, want ErrForbidden", err)
	}
}
