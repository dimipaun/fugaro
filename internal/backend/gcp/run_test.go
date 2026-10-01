package gcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The jobs of acme-app's web and api workflows.
var webJob, apiJob = JobName("acme-app", "web"), JobName("acme-app", "api")

func newTestBackend(t *testing.T) (*Backend, *gcpfake.Run, *gcpfake.Logging) {
	t.Helper()
	fr, fl := gcpfake.NewRun(t), gcpfake.NewLogging(t)
	fr.Project, fr.Region = "proj-1234", "us-east5"
	b, err := New(context.Background(), Options{GCPProject: "proj-1234", Region: "us-east5",
		Endpoints: Endpoints{Run: fr.URL + "/", Logging: fl.URL + "/", NoAuth: true}, LogSettle: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return b, fr, fl
}

func TestLaunchAndInspect(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	var gotEnv map[string]string
	fr.OnRun = func(c gcpfake.RunCall) { gotEnv = c.Env }
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if gotEnv["FUGARO_RUN"] != "acme-app/20260927-100000-abcd" {
		t.Fatalf("env = %v", gotEnv)
	}
	if !strings.HasPrefix(ref.Name, "projects/proj-1234/locations/us-east5/jobs/"+webJob+"/executions/") || ref.LogURL == "" {
		t.Fatalf("ref = %+v", ref)
	}
	e, err := b.Execution(ctx, ref.Name)
	if err != nil || e.State != backend.StatePending || e.CPU != 4 || e.MemoryGiB != 8 {
		t.Fatalf("execution = %+v, %v", e, err)
	}
	fr.SetState(ref.Name, backend.StateRunning)
	active, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil || len(active) != 1 || active[0].State != backend.StateRunning {
		t.Fatalf("List active = %+v, %v", active, err)
	}
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatal(err)
	}
	if e, _ := b.Execution(ctx, ref.Name); e.State != backend.StateCancelled || e.Completed.IsZero() {
		t.Fatalf("after Cancel = %+v", e)
	}
	if active, _ := b.List(ctx, backend.ListFilter{ActiveOnly: true}); len(active) != 0 {
		t.Fatalf("cancelled execution still active: %+v", active)
	}
}

func TestNamesAreCanonicalWhateverTheAPISends(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.ProjectNumber = "123456789"
	fr.AddJob(webJob, "4", "8Gi")
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil || !strings.HasPrefix(ref.Name, "projects/proj-1234/") {
		t.Fatalf("ref = %+v, %v (want the project ID form)", ref, err)
	}
	list, _ := b.List(ctx, backend.ListFilter{})
	if len(list) != 1 || list[0].Name != ref.Name {
		t.Fatalf("List names = %+v", list)
	}
	// A name spelled with the number is accepted too.
	id, _ := backend.ParseExecution(ref.Name)
	id.GCPProject = "123456789"
	if e, err := b.Execution(ctx, id.String()); err != nil || e.Name != ref.Name {
		t.Fatalf("Execution(number form) = %+v, %v", e, err)
	}
	if _, err := b.Execution(ctx, webJob+"-1"); err == nil {
		t.Fatal("a short name was sent to the API")
	}
}

func TestLaunchMissingJob(t *testing.T) {
	b, _, _ := newTestBackend(t)
	_, err := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if !errors.Is(err, backend.ErrNotFound) || !errors.Is(err, backend.ErrRejected) || !strings.Contains(err.Error(), webJob) ||
		!strings.Contains(err.Error(), "fugaro init --repo") || strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("err = %v", err)
	}
}

func TestLaunchAmbiguousErrorsAreNotRejected(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	for _, code := range []int{429, 500, 503} {
		fr.FailRunWith = code
		_, err := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
		if err == nil || errors.Is(err, backend.ErrRejected) {
			t.Errorf("HTTP %d: err = %v (must be ambiguous, not rejected)", code, err)
		}
	}
	fr.FailRunWith = 403
	if _, err := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"}); !errors.Is(err, backend.ErrRejected) {
		t.Errorf("HTTP 403: err = %v", err)
	}
}

func TestListIgnoresOtherJobsAndOldExecutions(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "1", "512Mi")
	fr.AddJob("unrelated-job", "1", "512Mi")
	fr.Start(webJob)
	fr.Start("unrelated-job")
	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil || len(got) != 1 || got[0].Job != webJob {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got, _ := b.List(ctx, backend.ListFilter{Since: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Fatalf("Since in the future listed %+v", got)
	}
}

func TestExecutionStateMapping(t *testing.T) {
	now := "2026-09-27T10:00:00Z"
	cases := []struct {
		e    execJSON
		want backend.State
	}{
		{execJSON{}, backend.StatePending},
		{execJSON{StartTime: now}, backend.StateRunning},
		{execJSON{StartTime: now, CompletionTime: now, SucceededCount: 1}, backend.StateSucceeded},
		{execJSON{StartTime: now, CompletionTime: now, FailedCount: 1}, backend.StateFailed},
		{execJSON{StartTime: now, CompletionTime: now, CancelledCount: 1}, backend.StateCancelled},
		{execJSON{CompletionTime: now}, backend.StateFailed}, // ended without any task outcome: never "succeeded"
	}
	for _, tc := range cases {
		if got := stateOf(tc.e.api()); got != tc.want {
			t.Errorf("%+v → %s, want %s", tc.e, got, tc.want)
		}
	}
}

// execJSON is the subset of an Execution that stateOf reads.
type execJSON struct {
	StartTime, CompletionTime                                 string
	RunningCount, SucceededCount, FailedCount, CancelledCount int64
}

func (e execJSON) api() *run.GoogleCloudRunV2Execution {
	return &run.GoogleCloudRunV2Execution{StartTime: e.StartTime, CompletionTime: e.CompletionTime,
		RunningCount: e.RunningCount, SucceededCount: e.SucceededCount, FailedCount: e.FailedCount, CancelledCount: e.CancelledCount}
}

func TestListFollowsPagesAndStopsAtSince(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	b.listPageSize = 2
	fr.AddJob(webJob, "1", "512Mi")
	for range 5 {
		fr.Start(webJob)
	}
	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil || len(got) != 5 {
		t.Fatalf("List = %d executions, %v", len(got), err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Created.After(got[i-1].Created) {
			t.Fatalf("not newest first: %+v", got)
		}
	}
	lists := func() (n int) {
		for _, r := range fr.Requests() {
			if strings.HasSuffix(r.Path, "/executions") {
				n++
			}
		}
		return n
	}
	before := lists()
	if got, _ := b.List(ctx, backend.ListFilter{Since: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Fatalf("Since in the future listed %+v", got)
	}
	if n := lists() - before; n != 1 {
		t.Fatalf("List with a future Since fetched %d pages, want 1", n)
	}
}

// The jobs/- listing is sorted across jobs, so it ends at the first
// execution older than Since, whichever job it belongs to.
func TestListStopsAtTheFirstExecutionOlderThanSince(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	b.listPageSize = 1
	fr.AddJob(webJob, "1", "512Mi")
	fr.AddJob(apiJob, "1", "512Mi")
	old := fr.Start(apiJob)
	fr.SetCreated(old, time.Now().Add(-365*24*time.Hour))
	fr.Start(webJob)
	since := time.Now().Add(-time.Hour)
	if got, err := b.List(ctx, backend.ListFilter{Since: since}); err != nil || len(got) != 1 || got[0].Job != webJob {
		t.Fatalf("List = %+v, %v; want the newer execution only", got, err)
	}
}

func TestListPerJob(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.Project, fr.Region = "proj-1234", "us-east5"
	fr.AddJob(webJob, "1", "512Mi")
	fr.AddJob(apiJob, "2", "1Gi")
	fr.Start(webJob)
	api := fr.Start(apiJob)
	got, err := b.List(ctx, backend.ListFilter{Jobs: []string{apiJob}})
	if err != nil || len(got) != 1 || !backend.SameExecution(got[0].Name, api) || got[0].CPU != 2 || got[0].MemoryGiB != 1 {
		t.Fatalf("List(api) = %+v, %v", got, err)
	}
}

func TestExecutionAndCancelNotFound(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "1", "512Mi")
	missing := "projects/proj-1234/locations/us-east5/jobs/" + webJob + "/executions/" + webJob + "-99"
	if _, err := b.Execution(ctx, missing); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Execution(missing) = %v", err)
	}
	if err := b.Cancel(ctx, missing); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Cancel(missing) = %v", err)
	}
	if err := b.Cancel(ctx, webJob+"-1"); err == nil {
		t.Fatal("Cancel accepted a short name")
	}
}

func TestParseCPU(t *testing.T) {
	for in, want := range map[string]float64{"4": 4, "4000m": 4, "500m": 0.5, "0.5": 0.5} {
		if got, err := parseCPU(in); err != nil || got != want {
			t.Errorf("parseCPU(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "four", "-1", "4 m"} {
		if _, err := parseCPU(in); err == nil {
			t.Errorf("parseCPU(%q) succeeded", in)
		}
	}
}

func launchSpec() backend.LaunchSpec {
	return backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"}
}

func TestLaunchCancelledRequestIsAmbiguous(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	fr.FailRunWith = 499
	if _, err := b.Launch(context.Background(), launchSpec()); err == nil || errors.Is(err, backend.ErrRejected) {
		t.Fatalf("HTTP 499: err = %v (must be ambiguous, not rejected)", err)
	}
}

// :run succeeded, so the execution exists: an unreadable operation must
// not let the caller release its claim.
func TestLaunchUnreadableMetadataIsAmbiguous(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	fr.BadRunMetadata = true
	_, err := b.Launch(context.Background(), launchSpec())
	if err == nil || errors.Is(err, backend.ErrRejected) || errors.Is(err, backend.ErrNotFound) || len(fr.Executions()) != 1 {
		t.Fatalf("err = %v, executions %v", err, fr.Executions())
	}
}

func TestListKeepsExecutionsWithUnparseableLimits(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	var warns []string
	b.o.Warn = func(m string) { warns = append(warns, m) }
	fr.AddJob(webJob, "four", "8G")
	fr.Start(webJob)
	got, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil || len(got) != 1 || got[0].CPU != 0 || got[0].MemoryGiB != 0 || got[0].State != backend.StatePending {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "cost unknown") {
		t.Fatalf("warnings = %q", warns)
	}
}

func TestListSkipsAMissingJob(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	var warns []string
	b.o.Warn = func(m string) { warns = append(warns, m) }
	fr.AddJob(webJob, "1", "512Mi")
	fr.Start(webJob)
	got, err := b.List(ctx, backend.ListFilter{Jobs: []string{"fugaro-acme-app-gone", webJob}})
	if err != nil || len(got) != 1 || got[0].Job != webJob {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "fugaro-acme-app-gone") {
		t.Fatalf("warnings = %q", warns)
	}
}

func TestCancelFinishedExecutionFails(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "1", "512Mi")
	ref, _ := b.Launch(ctx, launchSpec())
	fr.SetState(ref.Name, backend.StateSucceeded)
	err := b.Cancel(ctx, ref.Name)
	if err == nil || errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Cancel(finished) = %v", err)
	}
	if fr.State(ref.Name) != backend.StateSucceeded {
		t.Fatal("cancel changed a finished execution")
	}
}

// Execution names come from bucket objects a run can write: the backend
// follows only its own region's names, spelled [a-z0-9-], and never sends
// another to the API.
func TestExecutionNamesArePinnedToTheRegion(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	calls := 0
	fr.OnRun = func(gcpfake.RunCall) { calls++ }
	for _, name := range []string{
		"projects/proj-1234/locations/europe-west1/jobs/" + webJob + "/executions/" + webJob + "-abcde",
		"projects/proj-1234/locations/us-east5/jobs/" + webJob + "/executions/x?alt=media",
		"projects/proj-1234/locations/us-east5/jobs/" + webJob + "%2F..%2Fother/executions/e",
		"projects/proj-1234/locations/us-east5/jobs/Fugaro-Web/executions/e",
		"projects/proj_1234/locations/us-east5/jobs/" + webJob + "/executions/e",
	} {
		if _, err := b.Execution(ctx, name); err == nil || errors.Is(err, backend.ErrNotFound) {
			t.Errorf("Execution(%q) = %v, want a refusal", name, err)
		}
		if err := b.Cancel(ctx, name); err == nil || errors.Is(err, backend.ErrNotFound) {
			t.Errorf("Cancel(%q) = %v, want a refusal", name, err)
		}
		if err := b.Logs(ctx, backend.LogQuery{Execution: name}, func(backend.LogEntry) error { return nil }); err == nil {
			t.Errorf("Logs(%q) succeeded, want a refusal", name)
		}
	}
	if calls != 0 {
		t.Fatalf("%d launches", calls)
	}
}

func TestCheckRunExecution(t *testing.T) {
	own := backend.ExecID{GCPProject: "proj-1234", Region: "us-east5", Job: webJob, Name: webJob + "-abcde"}
	if err := CheckRunExecution(own.String(), "us-east5", "acme-app", "web"); err != nil {
		t.Fatalf("own execution refused: %v", err)
	}
	num := own
	num.GCPProject = "123456789"
	if err := CheckRunExecution(num.String(), "us-east5", "acme-app", "web"); err != nil {
		t.Fatalf("project number refused: %v", err)
	}
	other := own
	other.Job = apiJob
	for name, n := range map[string]string{"another job": other.String(), "another region": strings.Replace(own.String(), "us-east5", "us-central1", 1), "junk": "x"} {
		if err := CheckRunExecution(n, "us-east5", "acme-app", "web"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRedactURL(t *testing.T) {
	for _, u := range []string{"https://user:tok@host/o/r", "ssh://user:tok@host/o/r"} {
		if got := RedactURL(u); strings.Contains(got, "tok") || !strings.Contains(got, "host/o/r") {
			t.Errorf("RedactURL(%q) = %q", u, got)
		}
	}
	if got := RedactURL("https://host/%zz"); got != "<unparseable URL>" {
		t.Errorf("unparseable: %q", got)
	}
}

func TestLaunchSendsTimeoutOverride(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	spec := launchSpec()
	spec.Timeout = 45 * time.Minute
	if _, err := b.Launch(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	// The workflow's total plus the slack the job's task timeout adds.
	if got := fr.RunRequests(); len(got) != 1 || got[0].Timeout != "2820s" {
		t.Fatalf("requests = %+v, want timeout 2820s", got)
	}
}

func TestLaunchNoTimeoutByDefault(t *testing.T) {
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	if _, err := b.Launch(context.Background(), launchSpec()); err != nil {
		t.Fatal(err)
	}
	if got := fr.RunRequests(); len(got) != 1 || got[0].Timeout != "" {
		t.Fatalf("requests = %+v, want no timeout override", got)
	}
}

func TestLongestTaskTimeout(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	b.listPageSize = 1
	if got, err := b.LongestTaskTimeout(ctx); err != nil || got != 0 {
		t.Fatalf("no jobs: %v, %v", got, err)
	}
	fr.AddJob(webJob, "1", "512Mi")
	fr.AddJob(apiJob, "1", "512Mi")
	fr.SetJobTimeout(webJob, 45*time.Minute)
	fr.SetJobTimeout(apiJob, 30*time.Hour)
	if got, err := b.LongestTaskTimeout(ctx); err != nil || got != 30*time.Hour {
		t.Fatalf("LongestTaskTimeout = %v, %v; want 30h across pages", got, err)
	}
}

// The repository's check job is not a workflow job: its timeout does not
// widen the window in which a run can still be active.
func TestLongestTaskTimeoutIgnoresCheckJobs(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "1", "512Mi")
	fr.AddJob("fugarochk-acme-app", "1", "512Mi")
	fr.SetJobTimeout(webJob, time.Hour)
	fr.SetJobTimeout("fugarochk-acme-app", 100*time.Hour)
	if got, err := b.LongestTaskTimeout(ctx); err != nil || got != time.Hour {
		t.Fatalf("LongestTaskTimeout = %v, %v; want 1h", got, err)
	}
}
