package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/watch"
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
// bucket on its own clock, sped up here so the test doesn't wait 60s.
func TestWatchStreamShowsQueuedRun(t *testing.T) {
	old := queuedNextPoll
	queuedNextPoll = func() time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { queuedNextPoll = old })

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

// queuedJSONRun is one run of watch --json, as the queued-run tests read it.
type queuedJSONRun struct {
	Run         string `json:"run"`
	Title       string `json:"title"`
	Queued      bool   `json:"queued"`
	Stuck       bool   `json:"stuck"`
	Workflow    string `json:"workflow"`
	RequestedBy string `json:"requested_by"`
	Recipe      string `json:"recipe"`
}

// watchOnceJSON runs watch --once --json and returns its runs by id and its
// queued note.
func watchOnceJSON(t *testing.T) (map[string]queuedJSONRun, string, string) {
	t.Helper()
	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs       []queuedJSONRun `json:"runs"`
		QueuedNote string          `json:"queued_note"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	runs := map[string]queuedJSONRun{}
	for _, r := range doc.Runs {
		runs[r.Run] = r
	}
	return runs, doc.QueuedNote, out
}

// Past runstore.ClaimTTL runview gives a launch up (lost, or unlaunched for
// a claim alone); watch keeps showing it, stuck, so the warning is seen
// exactly when it applies. A run with a record has started: never queued.
func TestWatchStaleLaunchShowsStuck(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	lost := runID(11*time.Minute, "b001")
	f.writeTask(t, appSlug, lost, "")
	f.writeLaunch(t, appSlug, lost, time.Now().Add(-11*time.Minute))
	claimed := runID(12*time.Minute, "b002")
	f.writeTask(t, appSlug, claimed, "")
	f.writeClaim(t, appSlug, claimed, time.Now().Add(-12*time.Minute))
	fresh := runID(time.Minute, "b003")
	f.writeTask(t, appSlug, fresh, "")
	f.writeLaunch(t, appSlug, fresh, time.Now().Add(-time.Minute))
	started := runID(11*time.Minute, "b004") // lost after starting: it has a record
	f.writeTask(t, appSlug, started, "")
	f.writeLaunch(t, appSlug, started, time.Now().Add(-11*time.Minute))
	f.writeResult(t, appSlug, started, "running")
	old := runID(40*time.Minute, "b005") // past the lookback
	f.writeTask(t, appSlug, old, "")
	f.writeLaunch(t, appSlug, old, time.Now().Add(-40*time.Minute))

	runs, note, out := watchOnceJSON(t)
	if note != "" {
		t.Fatalf("note = %q", note)
	}
	for _, id := range []string{lost, claimed} {
		if r, ok := runs[id]; !ok || !r.Queued || !r.Stuck {
			t.Fatalf("%s: want a stuck queued row: %s", id, out)
		}
	}
	if r := runs[fresh]; !r.Queued || r.Stuck {
		t.Fatalf("fresh: want queued, not stuck: %s", out)
	}
	for _, id := range []string{started, old} {
		if _, ok := runs[id]; ok {
			t.Fatalf("%s must not show as queued: %s", id, out)
		}
	}
	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "not started after 11 min") {
		t.Fatalf("plain lacks the stuck warning:\n%s", plain)
	}
}

// A run cancelled while pending (launch.json and the cancel marker) is not
// queued any more.
func TestWatchCancelledPendingRunNotQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(2*time.Minute, "b011")
	f.writeTask(t, appSlug, run, "")
	f.writeLaunch(t, appSlug, run, time.Now().Add(-2*time.Minute))
	f.writeRunObject(t, appSlug, run, "cancel", time.Now().UTC().Format(time.RFC3339))
	runs, _, out := watchOnceJSON(t)
	if _, ok := runs[run]; ok {
		t.Fatalf("a cancelled run must not show as queued: %s", out)
	}
}

// The documented limitation: the scan lists run IDs minted within the
// lookback (plus queuedMintMargin), never the repository's history, so a
// fugaro run --retry of an old stored task (old ID, fresh launch.json) is
// not shown queued; it shows as a live row once its runner starts, and
// fugaro ls shows it pending meanwhile.
func TestWatchRetriedOldRunNotQueued(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(2*time.Hour, "b021")
	f.writeTask(t, appSlug, run, "")
	f.writeLaunch(t, appSlug, run, time.Now().Add(-time.Minute))
	runs, note, out := watchOnceJSON(t)
	if _, ok := runs[run]; ok || note != "" {
		t.Fatalf("an old run ID is outside the listing, so not queued (documented): %s", out)
	}
}

// One run that can't be read is left out with a note; the others still show.
func TestWatchUnreadableRunSkippedWithNote(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	bad := runID(time.Minute, "b031")
	f.writeTask(t, appSlug, bad, "")
	f.writeLaunch(t, appSlug, bad, time.Now().Add(-time.Minute))
	// task.json can't be read (and not because it is absent).
	if err := os.Chmod(filepath.Join(f.runsDir(), "runs", appSlug, bad, "task.json"), 0); err != nil {
		t.Fatal(err)
	}
	good := runID(2*time.Minute, "b032")
	f.writeTask(t, appSlug, good, "")
	f.writeLaunch(t, appSlug, good, time.Now().Add(-2*time.Minute))
	runs, note, out := watchOnceJSON(t)
	if r, ok := runs[good]; !ok || !r.Queued {
		t.Fatalf("the readable run must still show: %s", out)
	}
	if _, ok := runs[bad]; ok || !strings.Contains(note, "1 could not be read") {
		t.Fatalf("want the unreadable run left out with a note: %s", out)
	}
}

// A runs bucket that hangs never holds --once past queuedOnceWait: it
// prints the live rows and a note.
func TestWatchOnceDoesNotWaitForHungBucket(t *testing.T) {
	oldWait, oldOpen := queuedOnceWait, openQueueBucket
	queuedOnceWait = 100 * time.Millisecond
	openQueueBucket = func(ctx context.Context, _ *localcfg.Config) (*blobx.Bucket, error) {
		select { // hangs until abandoned (or, should --once wait anyway, for 6s)
		case <-ctx.Done():
		case <-time.After(6 * time.Second):
		}
		return nil, errors.New("hung")
	}
	t.Cleanup(func() { queuedOnceWait, openQueueBucket = oldWait, oldOpen })
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	start := time.Now()
	runs, note, out := watchOnceJSON(t)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("watch --once waited %s for a hung bucket", time.Since(start))
	}
	if !strings.Contains(note, "did not answer") || len(runs) != 2 {
		t.Fatalf("want the live rows and a note: %s", out)
	}
}

// The live source keeps its last good rows through a failed scan or two,
// saying so, and drops them after queuedDropAfter failures in a row.
func TestQueuedSourceDropsRowsAfterRepeatedFailures(t *testing.T) {
	rows := []watch.QueuedRun{{Run: "20261002-090000-abcd", Slug: appSlug}}
	fail := errors.New("boom")
	var next error
	qs := &queuedSource{scan: func(context.Context) ([]watch.QueuedRun, string, error) {
		if next != nil {
			return nil, "", next
		}
		return rows, "", nil
	}}
	ctx := context.Background()
	qs.refresh(ctx)
	next = fail
	for i := 1; i < queuedDropAfter; i++ {
		qs.refresh(ctx)
		if got, note := qs.Get(); len(got) != 1 || !strings.Contains(note, "earlier read") {
			t.Fatalf("failure %d: rows %v note %q", i, got, note)
		}
	}
	qs.refresh(ctx)
	if got, note := qs.Get(); got != nil || !strings.Contains(note, "boom") {
		t.Fatalf("after %d failures: rows %v note %q", queuedDropAfter, got, note)
	}
	next = nil
	qs.refresh(ctx)
	if got, note := qs.Get(); len(got) != 1 || note != "" {
		t.Fatalf("after recovery: rows %v note %q", got, note)
	}
}

// task.json is written by whoever launched the run: escape sequences in its
// workflow and requested_by never reach the plain or JSON output (a recipe
// name with one fails task.json's own validation; internal/watch's
// TestMergeQueuedSanitisesTaskFields covers the recipe and the TUI).
func TestWatchQueuedTaskFieldsSanitised(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(time.Minute, "b041")
	f.writeTask(t, appSlug, run, `,"workflow":"w\u001b]0;PWNED\u0007X","requested_by":"a\u001b]0;PWNED\u0007b\u009b2J"`)
	f.writeLaunch(t, appSlug, run, time.Now().Add(-time.Minute))
	runs, _, out := watchOnceJSON(t)
	r, ok := runs[run]
	if !ok {
		t.Fatalf("run missing: %s", out)
	}
	for _, s := range []string{r.Title, r.Workflow, r.RequestedBy} {
		if strings.Contains(s, "PWNED") || strings.ContainsAny(s, "\x1b\x07\u009b") {
			t.Fatalf("an escape sequence leaked into the JSON: %+v", r)
		}
	}
	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, run) || strings.Contains(plain, "PWNED") || strings.ContainsAny(plain, "\x1b\x07\u009b") {
		t.Fatalf("plain: %q", plain)
	}
}
