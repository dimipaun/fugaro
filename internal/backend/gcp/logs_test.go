package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

func TestLogsOnceAndFollow(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:01Z","severity":"INFO","message":"stage started","stage":"implement","run_id":"20260927-100000-abcd"}
{"time":"2026-09-27T10:00:02Z","severity":"WARNING","message":"cache miss","stage":"bootstrap"}
`))
	var got []backend.LogEntry
	collect := func(e backend.LogEntry) error { got = append(got, e); return nil }
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Message != "stage started" || got[0].Fields["stage"] != "implement" || got[1].Severity != "WARNING" {
		t.Fatalf("entries = %+v", got)
	}

	// Follow: a late entry arrives, then the execution ends; follow returns
	// after LogSettle and never repeats an entry.
	got = nil
	go func() {
		time.Sleep(30 * time.Millisecond)
		fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:03Z","severity":"INFO","message":"run finished"}`+"\n"))
		fr.SetState(ref.Name, backend.StateSucceeded)
	}()
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond}, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Message != "run finished" {
		t.Fatalf("followed entries = %+v", got)
	}
}

// Cloud Logging can ingest an entry after a newer one. Follow reads back
// over a lookback window, so the late entry is emitted, once.
func TestLogsFollowCatchesLateIngestedEntries(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:05Z","message":"newer"}`+"\n"))
	var got []string
	go func() {
		time.Sleep(30 * time.Millisecond)
		fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:03Z","message":"late"}`+"\n"))
		fr.SetState(ref.Name, backend.StateFailed)
	}()
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond},
		func(e backend.LogEntry) error { got = append(got, e.Message); return nil })
	if err != nil || len(got) != 2 || got[0] != "newer" || got[1] != "late" {
		t.Fatalf("followed = %q, %v", got, err)
	}
}

func TestLogsFollowStopsOnContext(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, _ := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond}, func(backend.LogEntry) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's", err)
	}
}

func TestLogsTextPayloadAndShortNameRefused(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte("panic: boom\n"))
	var got []backend.LogEntry
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name, Since: time.Now().Add(-time.Minute)}, func(e backend.LogEntry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "panic: boom" || got[0].Fields != nil {
		t.Fatalf("entries = %+v", got)
	}
	if err := b.Logs(ctx, backend.LogQuery{Execution: "fugaro-acme-app-web-1"}, func(backend.LogEntry) error { return nil }); err == nil {
		t.Fatal("a short name was sent to the API")
	}
}
