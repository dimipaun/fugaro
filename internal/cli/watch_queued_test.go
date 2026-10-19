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

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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

// runID is a valid-looking run id minted d before the clock watch's recency
// windows read (watchNow: the queued scan's lookbacks and the degraded view's
// window), unique via suffix. It is the mint the watch tests use, and the
// clock is the one watch is windowed by: minting against another clock (lsNow,
// time.Now) would put the id outside the window a test pinning watchNow pins,
// and fail for a reason the test never meant. recentRunID (runid_test.go) and
// runIDAt (runs_test.go) are the same mint against lsNow, the clock the
// listings are windowed by; all three share mintRunID.
func runID(d time.Duration, suffix string) string {
	return mintRunID(watchNow, d, suffix)
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
	// The run is minted inside the queued scan's listing window
	// (finishedLookback, 24h) so the scanner really does read it as a queued
	// candidate and the live row has to win: seedWatch's own 2026-10-02 id is
	// older than that window, so a fixed id made this pass without ever
	// reaching the dedup it names (#651). runID, not recentRunID: this window
	// is watch's, measured from watchNow, and minting against lsNow would put
	// the id somewhere the pinned window never looks.
	run := runID(30*time.Second, "a11e")
	f.db.Set(budget.PathAgent(appSlug, run), map[string]any{
		"repo": "acme/app", "title": "mine", "stage": "code", "round": 2, "auth": "api_key", "spent": 4 * usd1,
		"startedAt": time.Now().UnixMilli() - 600_000, "updatedAt": time.Now().UnixMilli(), "requestedBy": "someone@example.com"})
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
	LaunchedBy  string `json:"launched_by"`
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
// fugaro runs ls shows it pending meanwhile.
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
	qs := &queuedSource{scan: func(context.Context) ([]watch.QueuedRun, []watch.FinishedRun, string, error) {
		if next != nil {
			return nil, nil, "", next
		}
		return rows, nil, "", nil
	}}
	ctx := context.Background()
	qs.refresh(ctx)
	next = fail
	for i := 1; i < queuedDropAfter; i++ {
		qs.refresh(ctx)
		if got, _, note := qs.Get(); len(got) != 1 || !strings.Contains(note, "earlier read") {
			t.Fatalf("failure %d: rows %v note %q", i, got, note)
		}
	}
	qs.refresh(ctx)
	if got, _, note := qs.Get(); got != nil || !strings.Contains(note, "boom") {
		t.Fatalf("after %d failures: rows %v note %q", queuedDropAfter, got, note)
	}
	next = nil
	qs.refresh(ctx)
	if got, _, note := qs.Get(); len(got) != 1 || note != "" {
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

// writeLaunchBy writes launch.json with the launcher fugaro run records at
// launch (writeLaunch's own records none: the shape every run launched before
// the launcher was recorded has).
func (f *budgetFixture) writeLaunchBy(t *testing.T, slug, run, by string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(struct {
		Version    int       `json:"version"`
		RunID      string    `json:"run_id"`
		Backend    string    `json:"backend"`
		Execution  string    `json:"execution"`
		LaunchedBy string    `json:"launched_by,omitempty"`
		LaunchedAt time.Time `json:"launched_at"`
	}{Version: 1, RunID: run, Backend: "cloud-run", Execution: "projects/p/locations/r/jobs/j/executions/e-" + run,
		LaunchedBy: by, LaunchedAt: at.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRunObject(t, slug, run, "launch.json", string(data))
}

// writeLaunchJob writes launch.json naming the Cloud Run job the run was
// launched on, which is what every project-job assertion here reads.
func (f *budgetFixture) writeLaunchJob(t *testing.T, slug, run, job string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(struct {
		Version    int       `json:"version"`
		RunID      string    `json:"run_id"`
		Backend    string    `json:"backend"`
		Execution  string    `json:"execution"`
		Job        string    `json:"job"`
		LaunchedAt time.Time `json:"launched_at"`
	}{Version: 1, RunID: run, Backend: "cloud-run", Execution: "projects/p/locations/r/jobs/" + job + "/executions/e-" + run, Job: job, LaunchedAt: at.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRunObject(t, slug, run, "launch.json", string(data))
}

// writeResultAt writes a terminal result.json finished at finishedAt: the
// fixture's writeResult carries a fixed, long-past timestamp, and a finished
// row older than the scanner's 24 h lookback is dropped on purpose.
func (f *budgetFixture) writeResultAt(t *testing.T, slug, run, status string, finishedAt time.Time) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"version": 1, "run_id": run, "repo": "acme/app", "status": status, "outcome": "none",
		"started_at": finishedAt.Add(-time.Minute).UTC(), "finished_at": finishedAt.UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.writeRunObject(t, slug, run, "result.json", string(data))
}

// The JSON a shared project job's run carries (plan task 3.5 step 2): the job,
// and the (profile, provider, account) its name was computed from, beside it —
// for a live row, a queued one and a finished one alike, because all three are
// read from the same launch.json and the job's name spells none of the three
// out (R2-5: the triple is computed from the published policy's profiles and
// matched, never parsed back).
//
// A run on a per-repository job keeps the job alone: no profile of the policy
// computes its name, and inventing a triple for it would send a reader after an
// identity the run does not have. The grouping itself stays by repository —
// every row here keeps the repository its own slug names, whatever job it ran
// on.
func TestWatchJSONCarriesProfileAndAccount(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	// The published policy is where the triple comes from: the same document
	// the launcher resolves the job from and the runner matches CLOUD_RUN_JOB
	// against, read under the same rules — projectpolicy.Read honours a
	// policy only once the rules deployed to the project are this Fugaro's
	// generation (a policy on older rules is one nothing enforces), so the
	// marker is part of publishing it.
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version,
		"profiles": map[string]any{
			"standard": map[string]any{"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h"},
		},
	})
	job := gcp.ProjectJobName("standard", "github", "")
	if job == "" {
		t.Fatal("no project job name")
	}

	now := time.Now()
	live, queued, finished := runID(3*time.Minute, "e001"), runID(2*time.Minute, "e002"), runID(time.Minute, "e003")
	// The live run: the registry entry watch draws, plus the launch.json the
	// job is read from.
	ms := now.UnixMilli()
	f.db.Set(budget.PathAgent(appSlug, live), map[string]any{
		"repo": "acme/app", "title": "on the shared job", "stage": "code", "auth": "api_key", "spent": usd1,
		"startedAt": ms - 120_000, "updatedAt": ms})
	f.writeTask(t, appSlug, live, "")
	f.writeLaunchJob(t, appSlug, live, job, now.Add(-3*time.Minute))
	// The queued run: launch.json and nothing else.
	f.writeTask(t, appSlug, queued, "")
	f.writeLaunchJob(t, appSlug, queued, job, now.Add(-2*time.Minute))
	// The finished run: a per-repository job, the shape a baked workflow keeps
	// until phase 4 ends.
	legacy := "fugaro-acme-app-web"
	f.writeTask(t, appSlug, finished, "")
	f.writeLaunchJob(t, appSlug, finished, legacy, now.Add(-time.Minute))
	f.writeResultAt(t, appSlug, finished, "succeeded", now.Add(-30*time.Second))

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Run      string `json:"run"`
		Slug     string `json:"slug"`
		Queued   bool   `json:"queued"`
		Job      string `json:"job"`
		Profile  string `json:"profile"`
		Provider string `json:"provider"`
		Account  string `json:"account"`
	}
	var doc struct {
		Runs     []row `json:"runs"`
		Finished []row `json:"finished"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("watch --once --json is not JSON: %v\n%s", err, out)
	}
	find := func(rows []row, run string) *row {
		for i := range rows {
			if rows[i].Run == run {
				return &rows[i]
			}
		}
		return nil
	}
	for _, c := range []struct {
		name   string
		row    *row
		queued bool
		want   row
	}{
		{"a live run", find(doc.Runs, live), false, row{Run: live, Slug: appSlug, Job: job, Profile: "standard", Provider: "github"}},
		{"a queued run", find(doc.Runs, queued), true, row{Run: queued, Slug: appSlug, Queued: true, Job: job, Profile: "standard", Provider: "github"}},
		{"a finished run on a per-repository job", find(doc.Finished, finished), false, row{Run: finished, Slug: appSlug, Job: legacy}},
	} {
		if c.row == nil {
			t.Fatalf("%s (%s) is missing from %s", c.name, c.want.Run, out)
		}
		if c.row.Queued != c.queued {
			t.Errorf("%s: queued = %v, want %v", c.name, c.row.Queued, c.queued)
		}
		if *c.row != c.want {
			t.Errorf("%s: row = %+v, want %+v", c.name, *c.row, c.want)
		}
	}
}

// A job name is launch.json's — an object every launcher, and on a shared job
// every run, can write (D4) — so it is display text: stripped of escapes like
// every other string the bucket gives watch, and never given a triple the
// published policy does not compute for it. And the account of a project job
// is absent from the JSON while its profile and provider are present: phase 3
// refuses a profile that mounts a Claude OAuth token (projectpolicy, design
// D2), so every project job's account is the API-key one, "".
func TestWatchJSONHostileJobNameIsTextOnly(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version,
		"profiles": map[string]any{
			"standard": map[string]any{"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h"},
		},
	})
	job := gcp.ProjectJobName("standard", "github", "")
	now := time.Now()
	named, hostile := runID(2*time.Minute, "f001"), runID(time.Minute, "f002")
	f.writeTask(t, appSlug, named, "")
	f.writeLaunchJob(t, appSlug, named, job, now.Add(-2*time.Minute))
	f.writeTask(t, appSlug, hostile, "")
	f.writeLaunchJob(t, appSlug, hostile, job+"\x1b]0;PWNED\x07\u009b2J", now.Add(-time.Minute))

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("watch --once --json is not JSON: %v\n%s", err, out)
	}
	seen := 0
	for _, r := range doc.Runs {
		switch r["run"] {
		case named:
			seen++
			if r["job"] != job || r["profile"] != "standard" || r["provider"] != "github" {
				t.Errorf("the run on the project job = %v, want job %s and (standard, github)", r, job)
			}
			if _, ok := r["account"]; ok {
				t.Errorf("an API-key project job's run carries an account: %v", r)
			}
		case hostile:
			seen++
			j, _ := r["job"].(string)
			if strings.ContainsAny(j, "\x1b\x07\u009b") {
				t.Errorf("a control character leaked into the job: %q", j)
			}
			for _, k := range []string{"profile", "provider", "account"} {
				if _, ok := r[k]; ok {
					t.Errorf("a job name no profile computes was given a %s: %v", k, r)
				}
			}
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d of the two runs in %s", seen, out)
	}
}
