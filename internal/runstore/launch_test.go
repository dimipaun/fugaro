package runstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

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
