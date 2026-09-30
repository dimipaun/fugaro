package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// at is a log timestamp sec seconds after the test starts. Entries must
// be dated after their execution's creation, from which a read without
// Since starts.
func at(sec int) string {
	return testStart.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339Nano)
}

var testStart = time.Now()

func TestLogsOnceAndFollow(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"`+at(1)+`","severity":"INFO","message":"stage started","stage":"implement","run_id":"20260927-100000-abcd"}
{"time":"`+at(2)+`","severity":"WARNING","message":"cache miss","stage":"bootstrap"}
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
		fl.AddJSONLines(ref.Name, []byte(`{"time":"`+at(3)+`","severity":"INFO","message":"run finished"}`+"\n"))
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
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"`+at(5)+`","message":"newer"}`+"\n"))
	var got []string
	go func() {
		time.Sleep(30 * time.Millisecond)
		fl.AddJSONLines(ref.Name, []byte(`{"time":"`+at(3)+`","message":"late"}`+"\n"))
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
	fr.AddJob(webJob, "4", "8Gi")
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
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte("panic: boom\n"))
	var got []backend.LogEntry
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name, Since: time.Now().Add(-time.Minute)}, func(e backend.LogEntry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "panic: boom" || got[0].Fields != nil {
		t.Fatalf("entries = %+v", got)
	}
	if err := b.Logs(ctx, backend.LogQuery{Execution: webJob + "-1"}, func(backend.LogEntry) error { return nil }); err == nil {
		t.Fatal("a short name was sent to the API")
	}
}

func TestLogsWithoutSinceStartAtTheExecutionsCreation(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	e, _ := b.Execution(ctx, ref.Name)
	fl.AddJSONLines(ref.Name, []byte(`{"time":"`+e.Created.Add(-time.Hour).UTC().Format(time.RFC3339Nano)+`","message":"before"}`+"\n"+
		`{"time":"`+e.Created.Add(time.Second).UTC().Format(time.RFC3339Nano)+`","message":"after"}`+"\n"))
	var got []string
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(e backend.LogEntry) error { got = append(got, e.Message); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "after" {
		t.Fatalf("entries = %q", got)
	}
	reqs := fl.Requests()
	want := `timestamp>="` + e.Created.Add(-createdMargin).UTC().Format(time.RFC3339Nano) + `"`
	var req struct{ Filter string }
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &req); err != nil || !strings.HasSuffix(req.Filter, " AND "+want) {
		t.Fatalf("filter lacks %s: %q, %v", want, req.Filter, err)
	}
}

// A single entry dated far in the future must not make follow skip the
// entries that follow it.
func TestLogsFollowSurvivesAFutureDatedEntry(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"`+time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano)+`","message":"future"}`+"\n"))
	var got []string
	go func() {
		time.Sleep(30 * time.Millisecond)
		fl.AddJSONLines(ref.Name, []byte(`{"time":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","message":"now"}`+"\n"))
		fr.SetState(ref.Name, backend.StateSucceeded)
	}()
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond},
		func(e backend.LogEntry) error { got = append(got, e.Message); return nil })
	if err != nil || len(got) != 2 || got[0] != "future" || got[1] != "now" {
		t.Fatalf("followed = %q, %v", got, err)
	}
}

// An execution the backend has forgotten has finished: follow settles on
// its logs instead of failing.
func TestLogsFollowSettlesOnAForgottenExecution(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	gone := backend.ExecID{Project: "proj-1234", Region: "us-east5", Job: webJob, Name: webJob + "-gone1"}.String()
	fl.AddJSONLines(gone, []byte(`{"time":"`+at(1)+`","severity":"INFO","message":"last words"}`+"\n"))
	var got []backend.LogEntry
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := b.Logs(fctx, backend.LogQuery{Execution: gone, Since: testStart, Follow: true, Poll: 10 * time.Millisecond},
		func(e backend.LogEntry) error { got = append(got, e); return nil })
	if err != nil || len(got) != 1 {
		t.Fatalf("follow = %+v, %v", got, err)
	}
}

// The same job and execution names in another region are another
// execution: its logs never mix in.
func TestLogsStayInTheBackendsRegion(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	id, _ := backend.ParseExecution(ref.Name)
	other := id
	other.Region = "europe-west9"
	fl.AddJSONLines(ref.Name, []byte(`{"message":"here"}`+"\n"))
	fl.AddJSONLines(other.String(), []byte(`{"message":"elsewhere"}`+"\n"))
	var got []string
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name, Since: time.Now().Add(-time.Hour)}, func(e backend.LogEntry) error { got = append(got, e.Message); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "here" {
		t.Fatalf("entries = %q", got)
	}
}

const testLogView = "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"

// With a log view set, entries are read through the view, not the project.
func TestReadLogsThroughView(t *testing.T) {
	ctx := context.Background()
	fr, fl := gcpfake.NewRun(t), gcpfake.NewLogging(t)
	fr.Project, fr.Region = "proj-1234", "us-east5"
	fl.Resource = testLogView // the fake fails the test on any other resourceNames
	b, err := New(ctx, Options{GCPProject: "proj-1234", Region: "us-east5", LogView: testLogView,
		Endpoints: Endpoints{Run: fr.URL + "/", Logging: fl.URL + "/", NoAuth: true}, LogSettle: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	b.now = func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }
	fr.AddJob(webJob, "4", "8Gi")
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	// The Cloud Run page reads _Default, which holds nothing under
	// isolation, so the URL opens the view in Logs Explorer instead.
	name := ref.Name[strings.LastIndex(ref.Name, "/")+1:]
	wantURL := "https://console.cloud.google.com/logs/query;query=" +
		url.PathEscape(`resource.type="cloud_run_job" AND resource.labels.location="us-east5" AND resource.labels.job_name="`+webJob+`" AND labels."run.googleapis.com/execution_name"="`+name+`"`) +
		";storageScope=storage," + url.PathEscape(testLogView) +
		";timeRange=2026-09-27T09:59:00Z/2026-09-28T09:59:00Z?project=proj-1234"
	if ref.LogURL != wantURL {
		t.Fatalf("LogURL = %q\nwant     %q", ref.LogURL, wantURL)
	}
	if x, err := b.Execution(ctx, ref.Name); err != nil || x.LogURL == "" || x.LogURL == wantURL {
		// Listed executions carry their own createTime, not the launch's clock.
		t.Fatalf("Execution LogURL = %q, err %v", x.LogURL, err)
	}
	fl.AddJSONLines(ref.Name, []byte(`{"time":"`+at(1)+`","severity":"INFO","message":"hello"}`+"\n"))
	var got []backend.LogEntry
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(e backend.LogEntry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "hello" {
		t.Fatalf("entries = %+v", got)
	}
}
