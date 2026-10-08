package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// writeRunObject writes a run object directly into the fixture's runs
// bucket (a plain file: bucket_url is file://), the same object fugaro run
// or the runner itself would write at that path.
func (f *budgetFixture) writeRunObject(t *testing.T, slug, run, name, data string) {
	t.Helper()
	p := filepath.Join(f.runsDir(), "runs", slug, run, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeTask writes a minimal, valid task.json for slug/run.
func (f *budgetFixture) writeTask(t *testing.T, slug, run string, extra string) {
	t.Helper()
	f.writeRunObject(t, slug, run, "task.json", `{"version":1,"run_id":"`+run+`","repo":"acme/app","ref":"main","task":"do it","overrides":{}`+extra+`}`)
}

// writeClaim writes the "launching" claim object, taken at.
func (f *budgetFixture) writeClaim(t *testing.T, slug, run string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(struct {
		Holder string    `json:"holder"`
		At     time.Time `json:"at"`
	}{Holder: "someone@example.com", At: at.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRunObject(t, slug, run, "launching", string(data))
}

// writeLaunch writes launch.json, launched at at.
func (f *budgetFixture) writeLaunch(t *testing.T, slug, run string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(struct {
		Version    int       `json:"version"`
		RunID      string    `json:"run_id"`
		Backend    string    `json:"backend"`
		Execution  string    `json:"execution"`
		Job        string    `json:"job"`
		LaunchedAt time.Time `json:"launched_at"`
	}{Version: 1, RunID: run, Backend: "cloud-run", Execution: "projects/p/locations/r/jobs/j/executions/e-" + run, Job: "j", LaunchedAt: at.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRunObject(t, slug, run, "launch.json", string(data))
}

// writeResult writes a terminal result.json.
func (f *budgetFixture) writeResult(t *testing.T, slug, run, status string) {
	t.Helper()
	f.writeRunObject(t, slug, run, "result.json",
		`{"version":1,"run_id":"`+run+`","repo":"acme/app","status":"`+status+`","outcome":"none","started_at":"2026-10-02T09:00:00Z"}`)
}

// runID is a valid-looking run id minted roughly d ago from now, unique via
// suffix.
func runID(d time.Duration, suffix string) string {
	return time.Now().Add(-d).UTC().Format("20060102-150405") + "-" + suffix
}

func TestWatchShowsClaimedRunAsQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(2*time.Minute, "a001")
	f.writeTask(t, appSlug, run, `,"workflow":"web","requested_by":"a@b.c"`)
	f.writeClaim(t, appSlug, run, time.Now().Add(-2*time.Minute))

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []struct {
			Run         string `json:"run"`
			Stage       string `json:"stage"`
			Queued      bool   `json:"queued"`
			Stuck       bool   `json:"stuck"`
			Workflow    string `json:"workflow"`
			RequestedBy string `json:"requested_by"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var row *struct {
		Run         string `json:"run"`
		Stage       string `json:"stage"`
		Queued      bool   `json:"queued"`
		Stuck       bool   `json:"stuck"`
		Workflow    string `json:"workflow"`
		RequestedBy string `json:"requested_by"`
	}
	for i := range doc.Runs {
		if doc.Runs[i].Run == run {
			row = &doc.Runs[i]
		}
	}
	if row == nil {
		t.Fatalf("queued run missing from %s", out)
	}
	if !row.Queued || row.Stuck || row.Stage != "queued" || row.Workflow != "web" || row.RequestedBy != "a@b.c" {
		t.Fatalf("row = %+v", row)
	}

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, run) || !strings.Contains(plain, "queued") || !strings.Contains(plain, "QUEUED") {
		t.Fatalf("plain output lacks the queued run:\n%s", plain)
	}
}

func TestWatchShowsLaunchedRunAsQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(time.Minute, "a002")
	f.writeTask(t, appSlug, run, "")
	f.writeLaunch(t, appSlug, run, time.Now().Add(-time.Minute))

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, run) || !strings.Contains(out, `"queued":true`) {
		t.Fatalf("out = %s", out)
	}
}

// A run whose registry entry already shows it running must never also be
// listed queued: no duplicate row.
func TestWatchStartedRunNotDuplicatedAsQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := "20261002-090000-aaaa" // seedWatch's own live run
	f.writeTask(t, appSlug, run, "")
	f.writeClaim(t, appSlug, run, time.Now().Add(-time.Minute))

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `"run":"`+run+`"`) != 1 {
		t.Fatalf("the run must appear exactly once (live, not queued):\n%s", out)
	}
	if !strings.Contains(out, `"title":"mine"`) || strings.Contains(out, `"queued":true`) {
		t.Fatalf("the live row must win over the queued stand-in:\n%s", out)
	}
}

// A finished or cancelled run must never show as queued.
func TestWatchFinishedRunNeverQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	cancelled := runID(time.Minute, "c001")
	f.writeTask(t, appSlug, cancelled, "")
	f.writeResult(t, appSlug, cancelled, "cancelled")
	succeeded := runID(2*time.Minute, "c002")
	f.writeTask(t, appSlug, succeeded, "")
	f.writeResult(t, appSlug, succeeded, "succeeded")

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, cancelled) || strings.Contains(out, succeeded) {
		t.Fatalf("a finished/cancelled run must not appear as queued:\n%s", out)
	}
}

// The runs bucket being unreachable must not hide the live rows: it degrades
// with a one-line note instead.
func TestWatchQueuedBucketUnreachableDegradesWithNote(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	// Remove the project marker: the queued-run bucket check then refuses,
	// simulating an unreadable (or misconfigured) bucket.
	if err := os.Remove(filepath.Join(f.runsDir(), "fugaro", "project.json")); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		QueuedNote string                   `json:"queued_note"`
		Runs       []struct{ Title string } `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.QueuedNote == "" {
		t.Fatalf("want a queued_note when the bucket can't be read: %s", out)
	}
	if len(doc.Runs) != 2 {
		t.Fatalf("the live rows must still show: %+v", doc.Runs)
	}

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "queued runs unavailable") || !strings.Contains(plain, "mine") {
		t.Fatalf("plain = %s", plain)
	}
}

// A queued run that starts moves to its live row, with no duplicate: two
// successive snapshots, before and after its registry entry appears.
func TestWatchQueuedBecomesLiveAcrossSnapshots(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(time.Minute, "a004")
	f.writeTask(t, appSlug, run, `,"workflow":"web"`)
	f.writeClaim(t, appSlug, run, time.Now().Add(-time.Minute))

	before, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, run) || !strings.Contains(before, `"queued":true`) {
		t.Fatalf("before: want the run queued: %s", before)
	}

	// The run starts: the runner writes the registry entry.
	f.db.Set(budget.PathAgent(appSlug, run), map[string]any{
		"repo": "acme/app", "title": "started", "stage": "code", "auth": "api_key",
		"startedAt": time.Now().UnixMilli(), "updatedAt": time.Now().UnixMilli(), "requestedBy": "a@b.c",
	})

	after, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(after, `"run":"`+run+`"`) != 1 {
		t.Fatalf("after: want exactly one row for the started run: %s", after)
	}
	var doc struct {
		Runs []struct {
			Run    string `json:"run"`
			Queued bool   `json:"queued"`
			Title  string `json:"title"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(after), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range doc.Runs {
		if r.Run == run {
			found = true
			if r.Queued || r.Title != "started" {
				t.Fatalf("after: run %+v, want the live row, not queued", r)
			}
		}
	}
	if !found {
		t.Fatalf("after: run missing: %s", after)
	}
}

// The streaming (non-once) path also shows queued runs: it polls the
// bucket on its own clock, sped up here so the test doesn't wait 15s.
func TestWatchStreamShowsQueuedRun(t *testing.T) {
	old := queuedPollInterval
	queuedPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { queuedPollInterval = old })

	f := newBudgetFixture(t, "")
	seedWatch(f, "streamed")
	run := runID(time.Minute, "a005")
	f.writeTask(t, appSlug, run, "")
	f.writeClaim(t, appSlug, run, time.Now().Add(-time.Minute))

	cmd := NewRootCmd()
	out := &lockedBuf{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"watch", "--json"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), run) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), run) {
		t.Fatalf("the streamed output never showed the queued run:\n%s", out.String())
	}
}
