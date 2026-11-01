package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type lsOut struct {
	Runs     []runview.Row  `json:"runs"`
	Totals   runview.Totals `json:"totals"`
	Warnings []string       `json:"warnings"`
}

func seedRun(t *testing.T, f *cloudFixture, id, batch, who string, launched bool) string {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	_ = s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Batch: batch, RequestedBy: who})
	if !launched {
		return ""
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
	_ = s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()})
	return exec
}

func TestLsFiltersAndTotals(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5" // where the fixture's backend lists
	today := time.Now().UTC().Format("20060102")
	e1 := seedRun(t, f, today+"-090000-aaaa", "b1", "someone@example.com", true)
	seedRun(t, f, today+"-091000-bbbb", "b1", "other@example.com", true)
	seedRun(t, f, today+"-092000-cccc", "b2", "someone@example.com", false)
	seedRun(t, f, "20200101-000000-dddd", "b1", "someone@example.com", false) // outside --since // runid-guard:allow
	f.run.SetState(e1, backend.StateRunning)

	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got lsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 3 || got.Runs[0].Status != runview.StatusUnlaunched || got.Totals.Runs != 3 {
		t.Fatalf("ls = %+v", got)
	}
	out, _, _ = execute(t, "runs", "ls", "--json", "--mine", "--batch", "b1")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 1 || got.Runs[0].Status != "running" || got.Runs[0].Cost.ComputeUSD <= 0 {
		t.Fatalf("ls --mine --batch b1 = %+v", got.Runs)
	}
	out, _, _ = execute(t, "runs", "ls", "--json", "--since", "0")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("ls --since 0 = %d runs", len(got.Runs))
	}
	human, _, err := execute(t, "runs", "ls")
	if err != nil || !strings.Contains(human, "unlaunched") || !strings.Contains(human, "3 runs") {
		t.Fatalf("human ls = %s, %v", human, err)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute, "0": 0} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	// maxSinceDays is the last day count whose duration fits int64 nanoseconds;
	// one more used to wrap to a negative window instead of being refused.
	if got, err := parseSince("106751d"); err != nil || got != 106751*24*time.Hour || got <= 0 {
		t.Errorf("parseSince(106751d) = %v, %v", got, err)
	}
	for _, bad := range []string{"", "d", "-1d", "7w", "106752d", "9223372036854775807d"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func writeRecord(t *testing.T, f *cloudFixture, id string, r *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	if err := runstore.Open(b, appSlug, id).WriteRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
}

func lsJSON(t *testing.T, args ...string) (lsOut, string) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "ls", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("ls: %v (%s)", err, errOut)
	}
	var got lsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got, errOut
}

// After a double launch, the record's execution (the owner) decides, not
// launch.json's duplicate that exited.
func TestLsFollowsTheRecordsExecution(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	id := time.Now().UTC().Format("20060102") + "-090000-aaaa"
	dup := seedRun(t, f, id, "", "someone@example.com", true)
	owner := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
	f.run.SetState(dup, backend.StateSucceeded)
	f.run.SetState(owner, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Status: runstore.StatusRunning, Stage: "implement", Execution: owner})
	got, _ := lsJSON(t)
	if len(got.Runs) != 1 || got.Runs[0].Status != "running" || !backend.SameExecution(got.Runs[0].Execution, owner) {
		t.Fatalf("ls = %+v", got.Runs)
	}
	// The owner ending without a final record is infra_error, never success.
	f.run.SetState(owner, backend.StateSucceeded)
	got, _ = lsJSON(t)
	if got.Runs[0].Status != "infra_error" || !strings.Contains(got.Runs[0].Reason, runview.ReasonNoFinalRecord) {
		t.Fatalf("ls = %+v", got.Runs)
	}
}

// An execution name that can't be parsed is a warning and a per-row error,
// not a failure of the listing: one bad run must not hide the others.
func TestLsUnparseableExecutionWarns(t *testing.T) {
	f := newCloudFixture(t)
	id := time.Now().UTC().Format("20060102") + "-090000-aaaa"
	seedRun(t, f, id, "", "someone@example.com", false)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	_ = runstore.Open(b, appSlug, id).WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: "not-a-name"})
	b.Close()
	got, errOut := lsJSON(t)
	if len(got.Runs) != 1 || got.Runs[0].Status != runview.StatusError || strings.Count(errOut, "not a Cloud Run execution name") != 1 {
		t.Fatalf("ls = %+v, stderr %q", got.Runs, errOut)
	}
}

// loadRows for one run (diagnose) asks the backend for its execution alone.
func TestLoadRowsOneRun(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	id := "20200101-000000-dddd" // far outside any --since
	e := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(e, backend.StateRunning)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	rows, err := loadRows(context.Background(), env, lsFilter{runRef: appSlug + "/" + id}, time.Now())
	if err != nil || len(rows) != 1 || rows[0].Status != "running" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}

// --watch redraws until every row has settled.
func TestLsWatchStopsWhenSettled(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	today := time.Now().UTC().Format("20060102")
	e := seedRun(t, f, today+"-090000-aaaa", "", "someone@example.com", true)
	seedRun(t, f, today+"-091000-bbbb", "", "someone@example.com", false) // unlaunched: settled
	f.run.SetState(e, backend.StateRunning)
	out, err := executeWatch(t, 1, func() { f.run.SetState(e, backend.StateFailed) })
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var docs []lsOut
	for dec.More() {
		var d lsOut
		if err := dec.Decode(&d); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	last := docs[len(docs)-1]
	if len(docs) < 2 || last.Runs[1].Status != "infra_error" || !last.Runs[1].Settled {
		t.Fatalf("%d docs, last %+v", len(docs), last.Runs)
	}
}

// afterWrites calls fn once, synchronously, after the nth write to w. A
// watch test uses it to change the world between two redraws: a wall-clock
// sleep races the first listing, whose duration varies with machine load.
type afterWrites struct {
	buf bytes.Buffer
	n   int
	fn  func()
}

func (w *afterWrites) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.n--; w.n == 0 {
		w.fn()
	}
	return n, err
}

// executeWatch runs "ls --watch --json" and calls fn right after its nth
// redraw is written, so exactly n redraws see the state before fn.
func executeWatch(t *testing.T, n int, fn func()) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &afterWrites{n: n, fn: fn}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"runs", "ls", "--watch", "--json", "--interval", "10ms"})
	err := cmd.Execute()
	return out.buf.String(), err
}

// --watch reads the image status once per invocation, not on every redraw.
func TestLsWatchReadsImageStatusOnce(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	e := seedRun(t, f, today+"-090000-aaaa", "", "someone@example.com", true)
	f.run.SetState(e, backend.StateRunning)
	var reads atomic.Int32
	prev := readImageStatus
	readImageStatus = func(ctx context.Context, b *blobx.Bucket, slug, workflow string, now time.Time) (imagecheck.Status, error) {
		reads.Add(1)
		return prev(ctx, b, slug, workflow, now)
	}
	t.Cleanup(func() { readImageStatus = prev })
	out, err := executeWatch(t, 3, func() { f.run.SetState(e, backend.StateFailed) })
	if err != nil {
		t.Fatal(err)
	}
	docs := 0
	for dec := json.NewDecoder(strings.NewReader(out)); dec.More(); docs++ {
		var d lsOut
		if err := dec.Decode(&d); err != nil {
			t.Fatal(err)
		}
	}
	if docs < 3 || reads.Load() != 1 {
		t.Fatalf("%d redraws read the image status %d times, want once", docs, reads.Load())
	}
}

func TestLsFlagErrors(t *testing.T) {
	newCloudFixture(t)
	for _, args := range [][]string{{"runs", "ls", "--since", "7w"}, {"runs", "ls", "--all", "--repo", "acme/app"}} {
		if _, _, err := execute(t, args...); ExitCode(err) != ExitUserError {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// priceOverride prices us-east5, the fixture's region, far above list.
const priceOverride = "compute_prices:\n  us-east5: { vcpu_second_usd: 0.001, gib_second_usd: 0.0005 }\n"

// seedFinishedRun seeds a launched run whose execution ran for a fixed,
// nonzero time, so two estimates of it differ only by price.
func seedFinishedRun(t *testing.T, f *cloudFixture, id string) {
	t.Helper()
	e := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(e, backend.StateRunning)
	time.Sleep(2 * time.Millisecond)
	f.run.SetState(e, backend.StateSucceeded)
}

// wantOverrideRatio checks that an estimate at the override price is the
// list-price estimate scaled by the ratio of the two per-second rates of
// the fixture job (4 vCPU, 8 GiB).
func wantOverrideRatio(t *testing.T, list, override float64) {
	t.Helper()
	lp := gcp.ListPrices("us-east5")
	want := (4*0.001 + 8*0.0005) / (4*lp.VCPUSecondUSD + 8*lp.GiBSecondUSD)
	if list <= 0 || math.Abs(override/list-want) > 1e-6*want {
		t.Fatalf("compute: list %g, override %g (ratio %g), want ratio %g", list, override, override/list, want)
	}
}

func TestLsUsesPriceOverride(t *testing.T) {
	f := newCloudFixture(t)
	seedFinishedRun(t, f, time.Now().UTC().Format("20060102")+"-090000-aaaa")
	compute := func() float64 {
		out, _, err := execute(t, "runs", "ls", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var got lsOut
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Runs) != 1 {
			t.Fatalf("ls = %s, %v", out, err)
		}
		return got.Runs[0].Cost.ComputeUSD
	}
	list := compute()
	f.appendConfig(t, priceOverride)
	wantOverrideRatio(t, list, compute())
}

// putBuildObject writes an object of the runs bucket, such as a workflow's
// image.json or check.json.
func putBuildObject(t *testing.T, f *cloudFixture, key string, data []byte) {
	t.Helper()
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func putImageRecord(t *testing.T, f *cloudFixture, builtAt time.Time) {
	t.Helper()
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "web", BuiltAt: builtAt, SourceCommit: "abc123"})
	putBuildObject(t, f, imagecheck.RecordKey(appSlug, "web"), data)
}

func putCheckState(t *testing.T, f *cloudFixture, cs imagecheck.CheckState) {
	t.Helper()
	cs.Version = 1
	data, err := cs.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	putBuildObject(t, f, imagecheck.CheckKey(appSlug, "web"), data)
}

func TestLsImageAgeJSON(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	started := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	builtAt := started.Add(-30 * time.Hour)
	withImage, plain, skewed := today+"-090000-aaaa", today+"-091000-bbbb", today+"-092000-cccc"
	for _, id := range []string{withImage, plain, skewed} {
		seedRun(t, f, id, "", "someone@example.com", false)
	}
	writeRecord(t, f, withImage, &runstore.Record{Version: 1, RunID: withImage, Status: runstore.StatusSucceeded, Stage: "writeback",
		StartedAt: started, Image: &runstore.ImageInfo{BuiltAt: &builtAt, BakedCommit: "abc123"}})
	writeRecord(t, f, plain, &runstore.Record{Version: 1, RunID: plain, Status: runstore.StatusSucceeded, Stage: "writeback",
		StartedAt: started, Image: &runstore.ImageInfo{BakedCommit: "abc123"}})
	future := started.Add(time.Hour)
	writeRecord(t, f, skewed, &runstore.Record{Version: 1, RunID: skewed, Status: runstore.StatusSucceeded, Stage: "writeback",
		StartedAt: started, Image: &runstore.ImageInfo{BuiltAt: &future, BakedCommit: "abc123"}})

	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Runs []map[string]json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	ages := map[string]string{}
	for _, r := range raw.Runs {
		var id string
		_ = json.Unmarshal(r["run_id"], &id)
		ages[id] = string(r["image_age_s"])
	}
	if ages[withImage] != "108000" || ages[plain] != "" || ages[skewed] != "" {
		t.Fatalf("image_age_s by run = %v", ages)
	}
	// The human table is unchanged.
	human, _, err := execute(t, "runs", "ls")
	if err != nil || strings.Contains(human, "image") {
		t.Fatalf("human ls = %s, %v", human, err)
	}
}

func TestLsWarnsRebuildFailed(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	built := now.Add(-96 * time.Hour)
	failedAt := now.Add(-20 * time.Hour)
	failed := imagecheck.CheckState{CheckedAt: now.Add(-time.Hour), Decision: imagecheck.RebuildFailedLast, Reasons: []string{imagecheck.ReasonBase},
		BuildID: "b0008", LastBuildStatus: "FAILURE", LastBuildAt: &failedAt}
	putImageRecord(t, f, built)
	putCheckState(t, f, failed)

	want := "warning: acme/app web: the image rebuild of " + isoDay(failedAt) + " failed (FAILURE); runs still use the image built " + isoDay(built) + ". See fugaro image status.\n"
	out, _, err := execute(t, "runs", "ls")
	if err != nil || !strings.HasPrefix(out, want) {
		t.Fatalf("ls = %q, %v\nwant a first line %q", out, err, want)
	}
	if !strings.Contains(out, "RUN") || strings.Index(out, "RUN") < len(want) {
		t.Fatalf("the warning does not come before the table: %q", out)
	}
	got, _ := lsJSON(t)
	if len(got.Warnings) != 1 || got.Warnings[0]+"\n" != want {
		t.Fatalf("warnings = %q", got.Warnings)
	}
	if out, _, _ := execute(t, "runs", "ls", "--json"); strings.Contains(out, "\nwarning:") || !strings.Contains(out, `"warnings": [`) {
		t.Fatalf("json output has a human warning line, or no array: %s", out)
	}

	// The check itself failing is a warning, whatever the last build did.
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-time.Hour), Decision: imagecheck.CheckFailed, Error: "reading the base branch: boom"})
	got, _ = lsJSON(t)
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "acme/app web: the daily image check failed") || !strings.Contains(got.Warnings[0], "boom") {
		t.Fatalf("check-failed warnings = %q", got.Warnings)
	}

	// After a manual fix the record is newer than the failed build, and the
	// stale check.json keeps saying FAILURE until the next check: no warning.
	putImageRecord(t, f, failedAt.Add(time.Hour))
	putCheckState(t, f, failed)
	got, _ = lsJSON(t)
	if len(got.Warnings) != 0 {
		t.Fatalf("a superseded failure warned: %q", got.Warnings)
	}
	// A healthy check warns about nothing, and the array is still there.
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-time.Hour), Decision: imagecheck.Skip})
	out, _, _ = execute(t, "runs", "ls", "--json")
	if !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("json = %s", out)
	}
	if human, _, _ := execute(t, "runs", "ls"); strings.Contains(human, "warning:") {
		t.Fatalf("human ls warned: %s", human)
	}
}

// addCheckJob installs the repository's check job, checking workflows, as
// fugaro update --repo does: ls reads which workflows it checks when there is
// no checkout to read fugaro.yaml from.
func (f *cloudFixture) addCheckJob(t *testing.T, workflows ...string) {
	t.Helper()
	spec, err := json.Marshal(infra.CheckJobSpec{Repo: "acme/app", Provider: "github", Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	job := gcp.CheckJobName(appSlug)
	f.run.SetJob(job, map[string]string{"fugaro": "managed"}, "base:1")
	f.run.SetJobEnv(job, map[string]string{infra.CheckSpecEnv: string(spec)})
}

// TestLsCheckOffWithoutCheckout: without a checkout, ls takes the
// workflows on the daily check from the installed check job, so a
// workflow whose check is off, or a repository with no check job, gets no
// "hasn't run" warning from an old check.json.
func TestLsCheckOffWithoutCheckout(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	putImageRecord(t, f, now.Add(-100*time.Hour))
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-72 * time.Hour), Decision: imagecheck.Skip})
	if got, _ := lsJSON(t); len(got.Warnings) != 0 {
		t.Fatalf("no check job, yet warnings = %q", got.Warnings)
	}
	f.addCheckJob(t, "other")
	if got, _ := lsJSON(t); len(got.Warnings) != 0 {
		t.Fatalf("web is not checked, yet warnings = %q", got.Warnings)
	}
	f.addCheckJob(t, "other", "web")
	if got, _ := lsJSON(t); len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "hasn't run since") {
		t.Fatalf("web is checked: warnings = %q", got.Warnings)
	}
}

func TestLsWarnsCheckStale(t *testing.T) {
	f := newCloudFixture(t)
	f.addCheckJob(t, "web")
	now := time.Now().UTC().Truncate(time.Second)
	checkedAt := now.Add(-72 * time.Hour)
	putImageRecord(t, f, now.Add(-100*time.Hour))
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: checkedAt, Decision: imagecheck.Skip})
	got, _ := lsJSON(t)
	want := "warning: acme/app web: the daily image check hasn't run since " + checkedAt.Format("2006-01-02") + "."
	if len(got.Warnings) != 1 || got.Warnings[0] != want {
		t.Fatalf("warnings = %q, want %q", got.Warnings, want)
	}

	// Inside 48 hours it is on time.
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-47 * time.Hour), Decision: imagecheck.Skip})
	if got, _ = lsJSON(t); len(got.Warnings) != 0 {
		t.Fatalf("a check 47h old warned: %q", got.Warnings)
	}

	// No check.json: silent while the image is younger than 48 hours (the
	// schedule may be paused), a warning once it is older.
	if err := os.Remove(filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.CheckKey(appSlug, "web"))); err != nil {
		t.Fatal(err)
	}
	putImageRecord(t, f, now.Add(-47*time.Hour))
	if got, _ = lsJSON(t); len(got.Warnings) != 0 {
		t.Fatalf("a young image without a check warned: %q", got.Warnings)
	}
	built := now.Add(-60 * time.Hour)
	putImageRecord(t, f, built)
	got, _ = lsJSON(t)
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "acme/app web: the daily image check has never run") || !strings.Contains(got.Warnings[0], built.Format("2006-01-02")) {
		t.Fatalf("warnings = %q", got.Warnings)
	}

	// Neither object: nothing to say (no image yet, or nothing built).
	if err := os.Remove(filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.RecordKey(appSlug, "web"))); err != nil {
		t.Fatal(err)
	}
	if got, _ = lsJSON(t); len(got.Warnings) != 0 {
		t.Fatalf("no objects warned: %q", got.Warnings)
	}
}

func TestLsWarnsInOtherCases(t *testing.T) {
	f := newCloudFixture(t)
	f.addCheckJob(t, "web")
	now := time.Now().UTC().Truncate(time.Second)
	built := now.Add(-96 * time.Hour)
	failedAt := now.Add(-20 * time.Hour)
	warnings := func() []string {
		t.Helper()
		got, _ := lsJSON(t)
		return got.Warnings
	}
	one := func(ws []string, wants ...string) {
		t.Helper()
		if len(ws) != 1 {
			t.Fatalf("warnings = %q", ws)
		}
		for _, w := range wants {
			if !strings.Contains(ws[0], w) {
				t.Fatalf("warning %q lacks %q", ws[0], w)
			}
		}
	}

	// A failure newer than the record warns even when the decision is skip
	// (a rebuild is not due, but runs still use the older image).
	putImageRecord(t, f, built)
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-time.Hour), Decision: imagecheck.Skip,
		BuildID: "b1", LastBuildStatus: "FAILURE", LastBuildAt: &failedAt})
	one(warnings(), "the image rebuild of "+isoDay(failedAt)+" failed (FAILURE)", "built "+isoDay(built))

	// No record at all.
	if err := os.Remove(filepath.Join(strings.TrimPrefix(f.bucket, "file://"), imagecheck.RecordKey(appSlug, "web"))); err != nil {
		t.Fatal(err)
	}
	one(warnings(), "failed (FAILURE); the image has no record yet")
	putImageRecord(t, f, built)

	// The last build of these inputs ended without fixing the image.
	putCheckState(t, f, imagecheck.CheckState{CheckedAt: now.Add(-time.Hour), Decision: imagecheck.RebuildFailedLast,
		Reasons: []string{imagecheck.ReasonLastBuildIneffective}, BuildID: "b2", LastBuildStatus: "SUCCESS", LastBuildAt: &failedAt})
	one(warnings(), "the image rebuild of "+isoDay(failedAt)+" ended (SUCCESS) without fixing the image")

	// A stale check on a workflow that is not on the daily check is fine.
	stale := imagecheck.CheckState{CheckedAt: now.Add(-72 * time.Hour), Decision: imagecheck.Skip}
	putCheckState(t, f, stale)
	one(warnings(), "hasn't run since")
	dir := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", dir)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web:\n" +
		"    commands: { build: make, test: make test }\n    rebuild: { check: \"off\" }\n"})
	t.Chdir(dir)
	if ws := warnings(); len(ws) != 0 {
		t.Fatalf("check: off warned: %q", ws)
	}
}

// An object of the runs bucket that can't be read is a warning of its own,
// and a missing check.json is then no proof that the check never ran.
func TestLsWarnsUnreadableStatus(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	putImageRecord(t, f, now.Add(-100*time.Hour))
	for name, data := range map[string][]byte{
		"unparsable": []byte("{not json"),
		"oversized":  make([]byte, blobx.MaxReadBytes+1),
	} {
		putBuildObject(t, f, imagecheck.CheckKey(appSlug, "web"), data)
		got, _ := lsJSON(t)
		if len(got.Warnings) != 1 || !strings.HasPrefix(got.Warnings[0], "warning: acme/app web: ") || strings.Contains(got.Warnings[0], "never run") {
			t.Fatalf("%s check.json: warnings = %q", name, got.Warnings)
		}
		if name == "unparsable" && !strings.Contains(got.Warnings[0], imagecheck.CheckKey(appSlug, "web")) {
			t.Fatalf("the warning doesn't name the object: %q", got.Warnings)
		}
		human, _, _ := execute(t, "runs", "ls")
		if !strings.HasPrefix(human, got.Warnings[0]+"\n") {
			t.Fatalf("%s: human ls = %q", name, human)
		}
	}
}

// runIDAt is a run ID of the day daysAgo days ago, at hhmmss, with suffix
// hex: recentRunID (runid_test.go) in whole days. Like it, it reads lsNow,
// the clock the listings are windowed by, so a test that pins that clock mints
// and lists against the same time. The follow-up commands (run --pr, run
// --retry) read the wall clock instead: mint their runs with wallRunIDAt
// (runid_test.go).
func runIDAt(daysAgo int, hhmmss, hex string) string {
	return mintRunIDAt(lsNow, daysAgo, hhmmss, hex)
}

// seedSpec stores spec as its run's task.json and, when launched, starts
// an execution for it and records the launch; it returns the execution.
func seedSpec(t *testing.T, f *cloudFixture, spec *task.Spec, launched bool) string {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, spec.RunID)
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if !launched {
		return ""
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, spec.RunID)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: exec, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return exec
}

// firstRunSpec is a first run's task; followUpSpec continues root's PR pr.
func firstRunSpec(id string) *task.Spec {
	return &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", RequestedBy: "someone@example.com"}
}

func followUpSpec(id, root, previous string, pr int) *task.Spec {
	return &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web",
		Branch: "fugaro/" + root, PR: pr, PreviousRun: previous, RequestedBy: "someone@example.com"}
}

// prRecord is a finished record of run id on PR pr, which pushed.
func prRecord(id, exec string, pr int, cost float64) *runstore.Record {
	c := runstore.NewCost(cost, 0.5, runstore.BasisAPIList)
	return &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Branch: "fugaro/" + id, PushedHead: "1111111111111111111111111111111111111111", CostUSD: cost, Cost: &c,
		PR: &runstore.PRRef{Number: pr, URL: "https://github.com/acme/app/pull/" + strconv.Itoa(pr)}}
}

// prFixture seeds runs on PR 7 and PR 8 of acme/app:
//   - root, 30 days ago: the first run, which opened PR 7;
//   - fu1, today: a follow-up of PR 7, finished;
//   - fu2, today: a follow-up of PR 7 whose result.json is corrupt;
//   - other, today: a first run that opened PR 8;
//   - plain, today: a first run that never launched.
type prRuns struct{ root, fu1, fu2, other, plain string }

func seedPRRuns(t *testing.T, f *cloudFixture) prRuns {
	t.Helper()
	r := prRuns{root: runIDAt(30, "090000", "aaaa"), fu1: runIDAt(0, "000100", "bbbb"), fu2: runIDAt(0, "000200", "cccc"),
		other: runIDAt(0, "000300", "dddd"), plain: runIDAt(0, "000400", "eeee")}
	// Finished runs with no execution to join: their cost is the record's.
	seedSpec(t, f, firstRunSpec(r.root), false)
	writeRecord(t, f, r.root, prRecord(r.root, "", 7, 2))
	seedSpec(t, f, followUpSpec(r.fu1, r.root, r.root, 7), false)
	fu := prRecord(r.fu1, "", 7, 1)
	fu.Branch, fu.FollowUp = "fugaro/"+r.root, &runstore.FollowUp{PR: 7, PreviousRun: r.root}
	writeRecord(t, f, r.fu1, fu)
	seedSpec(t, f, followUpSpec(r.fu2, r.root, r.fu1, 7), false)
	putBuildObject(t, f, "runs/"+appSlug+"/"+r.fu2+"/result.json", []byte("{not json"))
	seedSpec(t, f, firstRunSpec(r.other), false)
	writeRecord(t, f, r.other, prRecord(r.other, "", 8, 5))
	seedSpec(t, f, firstRunSpec(r.plain), false)
	return r
}

func runIDs(rows []runview.Row) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.RunID)
	}
	return ids
}

func TestLsPRFilter(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRRuns(t, f)
	got, errOut := lsJSON(t, "--pr", "7")
	// Newest first; the root is 30 days old, inside --pr's 90-day default;
	// the corrupt run is kept, since its task says it is on PR 7.
	if want := []string{r.fu2, r.fu1, r.root}; strings.Join(runIDs(got.Runs), ",") != strings.Join(want, ",") {
		t.Fatalf("ls --pr 7 = %v, want %v (stderr %q)", runIDs(got.Runs), want, errOut)
	}
	if got.Runs[0].Status != runview.StatusError || got.Runs[0].TaskPR != 7 || !got.Runs[1].FollowUp || got.Runs[1].PreviousRun != r.root ||
		got.Runs[2].RecordPR != 7 || got.Runs[2].FollowUp || !got.Runs[2].Pushed {
		t.Fatalf("rows = %+v", got.Runs)
	}
	if got, _ := lsJSON(t, "--pr", "8", "--repo", "acme/app"); strings.Join(runIDs(got.Runs), ",") != r.other {
		t.Fatalf("ls --pr 8 = %v", runIDs(got.Runs))
	}
	// An explicit --since still bounds it.
	if got, _ := lsJSON(t, "--pr", "7", "--since", "7d"); len(got.Runs) != 2 {
		t.Fatalf("ls --pr 7 --since 7d = %v", runIDs(got.Runs))
	}
	if got, _ := lsJSON(t, "--pr", "9"); len(got.Runs) != 0 || got.Totals.Runs != 0 {
		t.Fatalf("ls --pr 9 = %+v", got)
	}
	// Without --pr the default window is still 7 days.
	if got, _ := lsJSON(t); len(got.Runs) != 4 {
		t.Fatalf("ls = %v", runIDs(got.Runs))
	}
}

// ls --pr joins executions only for the runs on the PR: a busy repository
// costs no per-run backend call for runs off it.
func TestLsPRJoinsExecutionsOnlyForPR(t *testing.T) {
	f := newCloudFixture(t)
	old := time.Now().Add(-200 * 24 * time.Hour) // outside the listing's window: each needs a call of its own
	on := runIDAt(0, "000100", "aaaa")
	e := seedSpec(t, f, firstRunSpec(on), true)
	writeRecord(t, f, on, prRecord(on, e, 7, 1))
	f.run.SetCreated(e, old)
	for i, hex := range []string{"bbbb", "cccc", "dddd"} {
		id := runIDAt(0, "00020"+strconv.Itoa(i), hex)
		e := seedSpec(t, f, firstRunSpec(id), true)
		writeRecord(t, f, id, prRecord(id, e, 8, 1))
		f.run.SetCreated(e, old)
	}
	gets := func() int {
		n := 0
		for _, r := range f.run.Requests() {
			if r.Method == "GET" && strings.Contains(r.Path, "/executions/") {
				n++
			}
		}
		return n
	}
	got, _ := lsJSON(t, "--pr", "7")
	if len(got.Runs) != 1 || got.Runs[0].RunID != on {
		t.Fatalf("ls --pr 7 = %v", runIDs(got.Runs))
	}
	if n := gets(); n != 1 {
		t.Fatalf("ls --pr 7 asked for %d executions one by one, want 1 (the run on the PR)", n)
	}
	// Without --pr every run's execution is fetched: the count above is the filter's doing.
	before := gets()
	if got, _ := lsJSON(t); len(got.Runs) != 4 || gets()-before != 4 {
		t.Fatalf("ls = %d runs, %d execution calls", len(got.Runs), gets()-before)
	}
}

func TestLsPRNeedsOneRepo(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "runs", "ls", "--pr", "7", "--all"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
		t.Fatalf("--pr --all: %v", err)
	}
	for _, bad := range []string{"0", "-3"} {
		if _, _, err := execute(t, "runs", "ls", "--pr", bad); ExitCode(err) != ExitUserError {
			t.Fatalf("--pr %s: %v", bad, err)
		}
	}
	// A config with two repositories needs --repo.
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	two := strings.Replace(string(data), "repos:\n", "repos:\n  acme/webapp: { provider: github, base_branch: main, workflows: [web] }\n", 1)
	if err := os.WriteFile(path, []byte(two), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "runs", "ls", "--pr", "7"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
		t.Fatalf("two repositories: %v", err)
	}
	seedPRRuns(t, f)
	if got, _ := lsJSON(t, "--pr", "7", "--repo", "acme/app"); len(got.Runs) != 3 {
		t.Fatalf("--repo: %v", runIDs(got.Runs))
	}
	// A config with no repositories (ls would list the whole bucket) needs --repo too.
	none := strings.Replace(string(data), "repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n", "", 1)
	if none == string(data) {
		t.Fatal("the fixture's repos entry moved")
	}
	if err := os.WriteFile(path, []byte(none), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "runs", "ls", "--pr", "7"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
		t.Fatalf("no repositories: %v", err)
	}
}

// ls --pr can't tell whether a run whose record is unreadable, and whose
// task names no PR, is on the PR: it leaves the run out but still warns.
func TestLsPRWarnsUndecidableRun(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRRuns(t, f)
	lost := runIDAt(0, "000500", "ffff")
	seedSpec(t, f, firstRunSpec(lost), false)
	putBuildObject(t, f, "runs/"+appSlug+"/"+lost+"/result.json", []byte("{not json"))
	got, errOut := lsJSON(t, "--pr", "7")
	if len(got.Runs) != 3 || !strings.Contains(errOut, lost+": result.json is unreadable") || !strings.Contains(errOut, r.fu2+": result.json is unreadable") {
		t.Fatalf("ls --pr 7 = %v, stderr %q", runIDs(got.Runs), errOut)
	}
	// A run whose record says it is on another PR is off this one: no warning for it.
	if got, errOut := lsJSON(t, "--pr", "8"); len(got.Runs) != 1 || strings.Contains(errOut, r.fu2) {
		t.Fatalf("ls --pr 8 = %v, stderr %q", runIDs(got.Runs), errOut)
	}
}

// ls --pr's totals line is the PR's total cost: every run on it, and only those.
func TestLsPRTotals(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRRuns(t, f)
	got, _ := lsJSON(t, "--pr", "7")
	// root $2 + $0.5, fu1 $1 + $0.5, fu2 nothing known; PR 8's $5 is not counted.
	if got.Totals.Runs != 3 || math.Abs(got.Totals.ModelUSD-3) > 1e-9 || math.Abs(got.Totals.ComputeUSD-1) > 1e-9 {
		t.Fatalf("totals = %+v (runs %v; other %s)", got.Totals, runIDs(got.Runs), r.other)
	}
	human, _, err := execute(t, "runs", "ls", "--pr", "7")
	if err != nil || !strings.Contains(human, "3 runs · ≈ $4.00 billed") {
		t.Fatalf("human ls --pr 7 = %s, %v", human, err)
	}
}

// The PR column shows the number, and the URL once it is known.
func TestLsPRColumn(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRRuns(t, f)
	waiting := runIDAt(0, "000500", "ffff") // a follow-up not launched yet: its task knows the PR, no record yet
	seedSpec(t, f, followUpSpec(waiting, r.root, r.fu1, 7), false)
	human, _, err := execute(t, "runs", "ls", "--pr", "7")
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, l := range strings.Split(human, "\n") {
		if fields := strings.Fields(l); len(fields) > 0 {
			lines[fields[0]] = l
		}
	}
	// The BY cell follows the PR column, and none of these runs recorded a
	// launcher (seedSpec's launch.json carries none): a dash.
	if l := lines[appSlug+"/"+r.fu1]; !strings.Contains(l, "#7 https://github.com/acme/app/pull/7 (draft)") || !strings.HasSuffix(strings.TrimSpace(l), "-") {
		t.Fatalf("finished follow-up line = %q\n%s", l, human)
	}
	if l := lines[appSlug+"/"+waiting]; !strings.Contains(l, "#7") || !strings.HasSuffix(strings.TrimSpace(l), "-") {
		t.Fatalf("unlaunched follow-up line = %q\n%s", l, human)
	}
	if l := lines[appSlug+"/"+r.plain]; l != "" {
		t.Fatalf("a run off the PR is listed: %q", l)
	}
}

func TestLsShowsHalted(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	id := runIDAt(0, "090000", "aaaa")
	e := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(e, backend.StateSucceeded)
	rec := prRecord(id, e, 7, 1)
	rec.Status, rec.Outcome = runstore.StatusHalted, runstore.OutcomeDraft
	rec.Halt = &runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run", At: time.Now().UTC(), Detail: "run used 110 tokens of 100"}
	rec.Reason = "halted: token_cap: run used 110 tokens of 100"
	writeRecord(t, f, id, rec)
	got, _ := lsJSON(t)
	if len(got.Runs) != 1 || got.Runs[0].Status != "halted" || got.Runs[0].Halt == nil || got.Runs[0].Halt.Reason != runstore.HaltTokenCap || !got.Runs[0].Terminal {
		t.Fatalf("rows = %+v", got.Runs)
	}
	out, _, err := execute(t, "runs", "ls")
	if err != nil || !strings.Contains(out, "halted (token_cap)") {
		t.Fatalf("ls = %s, %v", out, err)
	}
}

func TestLsShowsDraftInProgress(t *testing.T) {
	f := newCloudFixture(t)
	const run, done, stale = "20260927-100000-abcd", "20260927-100100-bbbb", "20260927-100200-cccc"
	e1 := seedRun(t, f, run, "", "", true)
	f.run.SetState(e1, backend.StateRunning)
	live := staleRecord(run, e1, time.Now().Add(-time.Minute))
	writeRecord(t, f, run, live)
	e2 := seedRun(t, f, done, "", "", true)
	f.run.SetState(e2, backend.StateSucceeded)
	ready := prRecord(done, e2, 8, 1)
	ready.Status, ready.Outcome = runstore.StatusSucceeded, runstore.OutcomeReady
	writeRecord(t, f, done, ready)
	e3 := seedRun(t, f, stale, "", "", true)
	f.run.SetState(e3, backend.StateFailed)
	writeRecord(t, f, stale, staleRecord(stale, e3, time.Now().Add(-5*time.Hour)))

	human, _, err := execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(human, "\n") {
		switch {
		case strings.Contains(line, run):
			// The BY cell (a dash: seedRun's launch.json records no launcher)
			// follows the PR column.
			if !strings.Contains(line, "https://github.com/acme/app/pull/7 (draft)") || !strings.HasSuffix(strings.TrimSpace(line), "-") {
				t.Errorf("running line: %q", line)
			}
		case strings.Contains(line, done):
			if strings.Contains(line, "(draft") {
				t.Errorf("ready PR marked draft: %q", line)
			}
		case strings.Contains(line, stale):
			if !strings.Contains(line, "(draft, stale)") {
				t.Errorf("stale line: %q", line)
			}
		}
	}
	got, _ := lsJSON(t, "--since", "0")
	for _, r := range got.Runs {
		if r.RunID == stale && !r.StaleDraft {
			t.Errorf("json lacks stale_draft: %+v", r)
		}
		if r.RunID == run && (r.StaleDraft || r.PRStatusAt == nil) {
			t.Errorf("json of the live run: %+v", r)
		}
	}
}

func TestLsRecipeColumn(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded", Recipe: "default"}}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "RECIPE") {
		t.Fatalf("default only:\n%s", b.String())
	}
	b.Reset()
	rows = append(rows, runview.Row{Run: "acme-app/20261007-110000-abcd", Status: "running", Recipe: "claude-solo"})
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || !strings.Contains(b.String(), "RECIPE") || !strings.Contains(b.String(), "claude-solo") {
		t.Fatalf("with a recipe:\n%s", b.String())
	}
}

func TestLsRecipeColumnDefaultsEmpty(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"},
		{Run: "acme-app/20261007-110000-abcd", Status: "running", Recipe: "claude-solo"},
	}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.Contains(l, "100000") {
			line = l
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "default") {
		t.Fatalf("line %q in:\n%s", line, b.String())
	}
}

func TestLsAccountColumn(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"}}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "ACCOUNT") {
		t.Fatalf("no named account:\n%s", b.String())
	}
	b.Reset()
	rows = append(rows, runview.Row{Run: "acme-app/20261007-110000-abcd", Status: "running", OAuthAccount: "acme-personal"})
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || !strings.Contains(b.String(), "ACCOUNT") || !strings.Contains(b.String(), "acme-personal") {
		t.Fatalf("with a named account:\n%s", b.String())
	}
	var line string
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.Contains(l, "100000") {
			line = l
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "default") {
		t.Fatalf("default row's ACCOUNT cell %q in:\n%s", line, b.String())
	}
}

// TestLsAccountColumnSanitizesHostileNames: an OAuth account name is
// untrusted text read back from a run's own result.json (bucket data, not
// validated by this binary), not something only "default" can hold. A tab
// or newline in it must not add a tabwriter column or a row, an ESC/bidi/
// line-separator rune must not reach the terminal raw, and a 10 KB name
// must not widen the table to match.
func TestLsAccountColumnSanitizesHostileNames(t *testing.T) {
	fields := func(line string) []string {
		return regexp.MustCompile(`  +`).Split(strings.TrimRight(line, " "), -1)
	}
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"a\tb", "a\nb", "a\rb", "esc\x1b[2Jc",
		"bidi" + string(rune(0x202e)) + "evil",
		"line" + string(rune(0x2028)) + "sep",
		"para" + string(rune(0x2029)) + "graph",
		huge,
	}
	for _, name := range hostile {
		var b strings.Builder
		rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded", Stage: "implement",
			PR: 7, PRURL: "https://example.com/pr/7", OAuthAccount: name}}
		if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("name %q: %d lines, want 3 (header, row, totals):\n%q", name, len(lines), out)
		}
		hdr, row := fields(lines[0]), fields(lines[1])
		if len(row) != len(hdr) {
			t.Fatalf("name %q: row has %d fields, header has %d (a tab or newline widened/split the table):\n%q", name, len(row), len(hdr), out)
		}
		if n := utf8.RuneCountInString(out); n > 600 {
			t.Fatalf("name %q: output is %d runes, not bounded:\n%.80q...", name, n, out)
		}
		for _, r := range out {
			if r == '\x1b' || (r >= 0x202a && r <= 0x202e) || r == 0x2028 || r == 0x2029 {
				t.Fatalf("name %q: hostile rune %U survived in:\n%q", name, r, out)
			}
		}
	}
}

// TestLsRecipeColumnSanitizesHostileNames: a recipe name is the same kind
// of untrusted, result.json/task.json-sourced text as an OAuth account
// name (TestLsAccountColumnSanitizesHostileNames), and shares its column
// in the same table, so it must be held to the same bar.
func TestLsRecipeColumnSanitizesHostileNames(t *testing.T) {
	fields := func(line string) []string {
		return regexp.MustCompile(`  +`).Split(strings.TrimRight(line, " "), -1)
	}
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"a\tb", "a\nb", "a\rb", "esc\x1b[2Jc",
		"bidi" + string(rune(0x202e)) + "evil",
		"line" + string(rune(0x2028)) + "sep",
		"para" + string(rune(0x2029)) + "graph",
		huge,
	}
	for _, name := range hostile {
		var b strings.Builder
		rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded", Stage: "implement",
			PR: 7, PRURL: "https://example.com/pr/7", Recipe: name}}
		if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("name %q: %d lines, want 3 (header, row, totals):\n%q", name, len(lines), out)
		}
		hdr, row := fields(lines[0]), fields(lines[1])
		if len(row) != len(hdr) {
			t.Fatalf("name %q: row has %d fields, header has %d (a tab or newline widened/split the table):\n%q", name, len(row), len(hdr), out)
		}
		if n := utf8.RuneCountInString(out); n > 600 {
			t.Fatalf("name %q: output is %d runes, not bounded:\n%.80q...", name, n, out)
		}
		for _, r := range out {
			if r == '\x1b' || (r >= 0x202a && r <= 0x202e) || r == 0x2028 || r == 0x2029 {
				t.Fatalf("name %q: hostile rune %U survived in:\n%q", name, r, out)
			}
		}
	}
}

func TestLsJSONOAuthAccountOmitempty(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"},
		{Run: "acme-app/20261007-110000-abcd", Status: "running", OAuthAccount: "acme-personal"},
	}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil || len(doc.Runs) != 2 {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if _, ok := doc.Runs[0]["oauth_account"]; ok {
		t.Fatalf("unset oauth_account present: %v", doc.Runs[0])
	}
	if doc.Runs[1]["oauth_account"] != "acme-personal" {
		t.Fatalf("oauth_account = %v", doc.Runs[1])
	}
}

func TestLsJSONRecipeOmitempty(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"},
		{Run: "acme-app/20261007-110000-abcd", Status: "running", Recipe: "claude-solo"},
	}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil || len(doc.Runs) != 2 {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if _, ok := doc.Runs[0]["recipe"]; ok {
		t.Fatalf("unset recipe present: %v", doc.Runs[0])
	}
	if doc.Runs[1]["recipe"] != "claude-solo" {
		t.Fatalf("recipe = %v", doc.Runs[1])
	}
}

func TestRunsLsIsTheOldLs(t *testing.T) {
	f := newCloudFixture(t)
	// ls hides runs older than its window, so the ID's date must follow the clock.
	id := time.Now().UTC().Format("20060102") + "-100000-abcd"
	seedRun(t, f, id, "", "someone@example.com", true)
	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("runs ls = %s, %v", out, err)
	}
}

// TestLsIsGone: the 0.7.0 tombstone was deleted by #350 in 0.7.5 (design
// generic-tool §5, G17, P3; the owner's 2026-10-14 ruling moved the deletion
// from 0.8.0 to the next release, which #370 renamed 0.7.5), so there is no
// top-level `ls` command at all.
// `fugaro runs ls` is the only listing (TestRunsLsIsTheOldLs).
func TestLsIsGone(t *testing.T) {
	newCloudFixture(t)
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "ls" {
			t.Fatal("a top-level ls command is still registered: the tombstone was deleted in 0.7.5")
		}
	}
	_, _, err := execute(t, "ls")
	if err == nil || !strings.Contains(err.Error(), `unknown command "ls"`) {
		t.Fatalf("ls = %v, want an unknown-command error", err)
	}
	// The exit code is part of the contract, not just the message. With the
	// tombstone gone, `fugaro ls` is nothing but a typo to cobra, and a typo
	// is a user error: exit 1 (design v1 §9.1, "1 for a user error, 2 for a
	// remote failure"). Nothing here touched the cloud, so it must never be
	// ExitRemoteError. (The deleted tombstone's line in design generic-tool
	// §5 said "exits 2", which the tombstone's own code never did: it
	// returned userErr, exit 1.)
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("fugaro ls exit code = %d, want %d (an unknown command is a user error)", got, ExitUserError)
	}
}

func TestSingularNounAliases(t *testing.T) {
	for _, args := range [][]string{{"recipe", "validate", "--help"}, {"secret", "ls", "--help"}, {"account", "ls", "--help"}, {"run", "--help"}} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

// TestLsLauncherColumn: every listing shows who started each run — the
// column is always there, with the launcher's short form (the part before
// the @ of an address) and a dash for a run whose launch.json recorded
// none. Mutation caught: showing the whole address, or leaving the column
// out when no run has a launcher.
func TestLsLauncherColumn(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-abcd", Status: "succeeded", LaunchedBy: "dana@example.com"},
		{Run: "acme-app/20261007-110000-abcd", Status: "running", LaunchedBy: "ci-runner"},
		{Run: "acme-app/20261007-120000-abcd", Status: "running"},
	}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.Contains(lines[0], "BY") {
		t.Fatalf("no BY column even though a run has a launcher:\n%s", out)
	}
	for i, want := range map[int]string{1: "dana", 2: "ci-runner", 3: "-"} {
		cell := strings.TrimSpace(lines[i][strings.LastIndex(lines[i], "  "):])
		if cell != want {
			t.Errorf("row %d's BY cell = %q, want %q:\n%s", i, cell, want, out)
		}
	}
	if strings.Contains(out, "dana@example.com") {
		t.Errorf("the whole address reached the text column:\n%s", out)
	}
}

// TestLsLauncherColumnSanitizesHostileValues: the launcher is written by
// whoever launched the run, so it is untrusted text like an OAuth account
// or a recipe name and must clear the same bar: no extra tabwriter column
// or row, no escape, bidi or line-separator rune, no unbounded width.
// Mutation caught: interpolating r.LaunchedBy into the cell as it is.
func TestLsLauncherColumnSanitizesHostileValues(t *testing.T) {
	fields := func(line string) []string {
		return regexp.MustCompile(`  +`).Split(strings.TrimRight(line, " "), -1)
	}
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"a\tb@example.com", "a\nb@example.com", "a\rb@example.com", "esc\x1b[2Jc@example.com",
		"bidi" + string(rune(0x202e)) + "evil@example.com",
		"line" + string(rune(0x2028)) + "sep@example.com",
		"para" + string(rune(0x2029)) + "graph@example.com",
		"@" + huge,
	}
	for _, by := range hostile {
		var b strings.Builder
		rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded", Stage: "implement",
			PR: 7, PRURL: "https://example.com/pr/7", LaunchedBy: by}}
		if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("launcher %q: %d lines, want 3 (header, row, totals):\n%q", by, len(lines), out)
		}
		hdr, row := fields(lines[0]), fields(lines[1])
		if len(row) != len(hdr) {
			t.Fatalf("launcher %q: row has %d fields, header has %d (a tab or newline widened/split the table):\n%q", by, len(row), len(hdr), out)
		}
		if n := utf8.RuneCountInString(out); n > 600 {
			t.Fatalf("launcher %q: output is %d runes, not bounded:\n%.80q...", by, n, out)
		}
		for _, r := range out {
			if r == '\x1b' || (r >= 0x202a && r <= 0x202e) || r == 0x2028 || r == 0x2029 {
				t.Fatalf("launcher %q: hostile rune %U survived in:\n%q", by, r, out)
			}
		}
	}
}

// seedSpecBy seeds spec and, when launched, writes a launch.json that
// records by as its launcher: the shape fugaro run writes (seedSpec's own
// launch records none, the pre-0.4.0 shape).
func seedSpecBy(t *testing.T, f *cloudFixture, spec *task.Spec, by string) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, spec.RunID)
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, spec.RunID)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: exec, LaunchedBy: by, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// TestLsLauncherComesFromLaunchJSON: the launcher is launch.json's own
// record — not the task's RequestedBy, which is a different field a
// different writer fills — the JSON carries it in full, the text column
// carries its short form, and a run whose launch.json recorded none has no
// key at all and a dash.
func TestLsLauncherComesFromLaunchJSON(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	seedSpecBy(t, f, firstRunSpec(today+"-090000-aaaa"), "dana@example.com")
	seedSpecBy(t, f, firstRunSpec(today+"-091000-bbbb"), "") // launched, no launched_by

	got, _ := lsJSON(t)
	if len(got.Runs) != 2 {
		t.Fatalf("runs = %v", runIDs(got.Runs))
	}
	byRun := map[string]string{}
	for _, r := range got.Runs {
		byRun[r.RunID] = r.LaunchedBy
	}
	if byRun[today+"-090000-aaaa"] != "dana@example.com" {
		t.Fatalf("launched_by = %q, want the launch.json's value", byRun[today+"-090000-aaaa"])
	}
	if byRun[today+"-091000-bbbb"] != "" {
		t.Fatalf("a launch.json with no launched_by produced %q", byRun[today+"-091000-bbbb"])
	}
	// The key is absent from the wire, not "" or null — and the task's own
	// RequestedBy is still there beside it, so the absent key is not the
	// task's identity having been dropped.
	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range doc.Runs {
		if r["run_id"] != today+"-091000-bbbb" {
			continue
		}
		seen = true
		if v, ok := r["launched_by"]; ok {
			t.Fatalf("absent launcher still emitted launched_by = %v", v)
		}
		if r["requested_by"] != "someone@example.com" {
			t.Fatalf("requested_by = %v, want the task's own (proving launched_by is not read from it)", r["requested_by"])
		}
	}
	if !seen {
		t.Fatalf("the no-launcher run is missing:\n%s", out)
	}
	// The text listing agrees: the short form, and a dash for the run whose
	// launch.json recorded no launcher.
	human, _, err := execute(t, "runs", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "dana") || strings.Contains(human, "dana@example.com") {
		t.Fatalf("text listing does not show the short form:\n%s", human)
	}
	var dashLine string
	for _, l := range strings.Split(human, "\n") {
		if strings.Contains(l, "091000") {
			dashLine = l
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(dashLine), "-") {
		t.Fatalf("row without a launcher's BY cell = %q, want a dash:\n%s", dashLine, human)
	}
}

// TestLsFollowUpShowsItsOwnLauncher: a follow-up records its own launcher at
// its own launch, so the PR's listing shows each run's own — the follow-up's
// identity, not the first run's. Mutation caught: carrying the previous
// run's launcher onto a follow-up's row.
func TestLsFollowUpShowsItsOwnLauncher(t *testing.T) {
	f := newCloudFixture(t)
	root, fu := runIDAt(0, "090000", "aaaa"), runIDAt(0, "100000", "bbbb")
	seedSpecBy(t, f, firstRunSpec(root), "dana@example.com")
	writeRecord(t, f, root, prRecord(root, "", 7, 1))
	seedSpecBy(t, f, followUpSpec(fu, root, root, 7), "ci-runner@proj.iam.gserviceaccount.com")

	got, _ := lsJSON(t, "--pr", "7", "--repo", "acme/app")
	if len(got.Runs) != 2 {
		t.Fatalf("runs = %v", runIDs(got.Runs))
	}
	byRun := map[string]string{}
	for _, r := range got.Runs {
		byRun[r.RunID] = r.LaunchedBy
	}
	if byRun[root] != "dana@example.com" || byRun[fu] != "ci-runner@proj.iam.gserviceaccount.com" {
		t.Fatalf("launchers = %v, want each run's own", byRun)
	}
	human, _, err := execute(t, "runs", "ls", "--pr", "7", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "dana") || !strings.Contains(human, "ci-runner") || strings.Contains(human, "ci-runner@") {
		t.Fatalf("text listing does not show both short forms:\n%s", human)
	}
}

// TestLsLauncherShortFormAndClip: runs ls has a BY column showing the short form of the
// launcher (the part before an @, the whole value otherwise), a dash when none
// was recorded, and a value past the column's cap is clipped so one launch.json
// cannot widen the table. Mutation caught: no clip, full address, no dash.
func TestLsLauncherShortFormAndClip(t *testing.T) {
	long := strings.Repeat("c", maxNameCellRunes+30)
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-aaaa", Status: "succeeded", LaunchedBy: "dana@example.com"},
		{Run: "acme-app/20261007-100000-bbbb", Status: "succeeded", LaunchedBy: "ci-runner@proj.iam.gserviceaccount.com"},
		{Run: "acme-app/20261007-100000-cccc", Status: "succeeded", LaunchedBy: "plain-id"},
		{Run: "acme-app/20261007-100000-dddd", Status: "succeeded"},
		{Run: "acme-app/20261007-100000-eeee", Status: "succeeded", LaunchedBy: "@example.com"},
		{Run: "acme-app/20261007-100000-ffff", Status: "succeeded", LaunchedBy: long},
	}
	var b strings.Builder
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for id, want := range map[string]string{"aaaa": "dana", "bbbb": "ci-runner", "cccc": "plain-id", "dddd": "-", "eeee": "-",
		"ffff": strings.Repeat("c", maxNameCellRunes) + "…"} {
		found := false
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "-"+id) {
				found = true
				f := strings.Fields(l)
				if got := f[len(f)-1]; got != want {
					t.Errorf("run %s: BY = %q, want %q\n%s", id, got, want, out)
				}
			}
		}
		if !found {
			t.Errorf("no row for %s:\n%s", id, out)
		}
	}
	if strings.Contains(out, "@") {
		t.Errorf("an address reached the text table:\n%s", out)
	}
}

// TestLsDerivesAPreemptedRunFromTheBatchJob: the whole read path of a run whose
// Cloud Batch job was preempted -- loadRows (the loader runs ls, runs ls
// --watch, diagnose and the degraded watch all share) and printRows (the table
// and the --json document) -- end to end against the Batch fake.
//
// It is necessarily a rig and not the ordinary cloud fixture: `backend:
// cloud-batch` is refused by config validation until the enable task (design
// compute-backend.md §8.1, plan Task 10), so the only way a test reaches a
// Batch-backed read is by handing the loader the backend the opener would build
// (cloud_openers_test.go's rig). What it holds is the rule, not the wiring: one
// run, its record never finalized, its job FAILED with the platform's own
// preemption in its status events.
//
// The evidence here is the event's wording and not the reserved exit code:
// that code has one home (`internal/backend/batch`'s preemptExitCode, whose own
// test covers it) and a second literal in this package would be the copy that
// drifts. Either half must derive the same row; this is that half.
func TestLsDerivesAPreemptedRunFromTheBatchJob(t *testing.T) {
	ctx := context.Background()
	rig := newBatchRig(t, 412)
	be := rig.batchBackend(t)
	const runID = "20260919-120000-abcd"
	ref, err := be.Launch(ctx, backend.LaunchSpec{
		Repo:     backend.RepoRef{Repo: backendtest.Repo, Slug: backendtest.Slug},
		Workflow: backendtest.Workflow,
		RunID:    runID,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bucket, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	s := runstore.Open(bucket.Bucket, backendtest.Slug, runID)
	// A launch.json and no result.json: the runner was killed before it could
	// finalize, which is what a preemption looks like in the bucket.
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: runID, Execution: ref.Name}); err != nil {
		t.Fatal(err)
	}
	env := &cloudEnv{lc: rig.lc, bucket: bucket, be: be}
	f := lsFilter{runRef: backendtest.Slug + "/" + runID, warn: io.Discard}

	// Before the preemption the job is running: the row must say so.
	rig.bf.SetState(ref.Job, "RUNNING")
	rows, err := loadRows(ctx, env, f, time.Now())
	if err != nil {
		t.Fatalf("loadRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != string(runstore.StatusRunning) || rows[0].Derived != "" {
		t.Fatalf("a running job read as %+v, want running with nothing derived", rows)
	}

	// The platform reclaims the VM: the job ends FAILED and its own report says
	// why. The run's own objects are untouched -- nothing is written back.
	rig.bf.SetState(ref.Job, "FAILED")
	rig.bf.SetStatusEvents(ref.Job, map[string]any{
		"type": "STATUS_CHANGED", "description": "Task state is FAILED",
		"taskState": "FAILED", "taskExecution": map[string]any{"exitCode": 50000},
	}, map[string]any{
		"type": "STATUS_CHANGED", "description": "The task's VM was preempted",
	})
	rows, err = loadRows(ctx, env, f, time.Now())
	if err != nil {
		t.Fatalf("loadRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("loadRows returned %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Status != string(runstore.StatusFailed) || row.Derived != runview.DerivedPreempted || row.Reason != runview.ReasonPreempted {
		t.Fatalf("the derived row is %+v, want failed/%s with its reason", row, runview.DerivedPreempted)
	}
	if !row.Terminal || !row.Settled {
		t.Errorf("the derived row is terminal=%v settled=%v: runs ls --watch could not stop on it", row.Terminal, row.Settled)
	}
	// The derived status is never written back: the bucket still holds the
	// launch and nothing else.
	if rec, err := absent(s.ReadRecord(ctx)); err != nil || rec != nil {
		t.Fatalf("the read side wrote a record (%+v, %v): the derivation is never written anywhere", rec, err)
	}

	// And it reaches both of printRows' shapes: the table decorates the status,
	// the document carries the field.
	var b strings.Builder
	if err := printRows(&b, rig.lc.Name, rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "failed (preempted)") {
		t.Errorf("the table shows no derived status:\n%s", out)
	}
	b.Reset()
	if err := printRows(&b, rig.lc.Name, rows, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []runview.Row `json:"runs"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("%v: %s", err, b.String())
	}
	if len(doc.Runs) != 1 || doc.Runs[0].Status != "failed" || doc.Runs[0].Derived != runview.DerivedPreempted {
		t.Fatalf("the document is %+v: the derived field must be additive and the status must stay the vocabulary", doc.Runs)
	}
	// The optional field is absent for every row that is not derived, which is
	// what keeps the shape additive for a reader that has never heard of it.
	b.Reset()
	if err := printRows(&b, rig.lc.Name, []runview.Row{{Run: "acme/app/20260919-120000-abcd", Status: "succeeded"}}, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "derived") {
		t.Errorf("a settled row carries the derived key:\n%s", b.String())
	}
}

// TestDiagnoseShowsADerivedStatus: diagnose prints a derived run's status the
// way runs ls's table does, so the one-run read says what happened to it rather
// than leaving the reader to notice a reason line (the owner's 2026-10-21
// ruling). The halted decoration beside it is unchanged, which is the half a
// careless refactor would move.
func TestDiagnoseShowsADerivedStatus(t *testing.T) {
	var b strings.Builder
	d := &Diagnosis{Row: runview.Row{Run: "acme/app/20260919-120000-abcd", Status: "failed",
		Derived: runview.DerivedPreempted, Reason: runview.ReasonPreempted}}
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Status:   failed (preempted)") {
		t.Errorf("diagnose does not show the derived status:\n%s", out)
	}
	if !strings.Contains(out, "Reason:   "+runview.ReasonPreempted) {
		t.Errorf("diagnose does not show the derived reason:\n%s", out)
	}
	b.Reset()
	halt := &runstore.Halt{Reason: runstore.HaltRunCap, At: time.Now()}
	if err := printDiagnosis(&b, &Diagnosis{Row: runview.Row{Run: "acme/app/x", Status: "halted", Halt: halt}}, false); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "Status:   halted\n") {
		t.Errorf("the halted status line changed shape:\n%s", out)
	}
}

// TestRunsLsParallelOrderStable: ls reads every run's objects lsWorkers at a
// time, and what comes out is what reading them one at a time produced. The
// rows are the listing's own order over the whole set, and each unreadable
// object is still its own error row warned about as its run is reached:
// repository by repository in slug order, and the newest run first within
// each, which is the order the reads are handed out in.
func TestRunsLsParallelOrderStable(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	slugs := []string{appSlug, mustSlug("github", "acme/other"), mustSlug("github", "acme/third")}
	type seeded struct{ slug, id string }
	var all []seeded
	var corrupt []seeded
	for si, slug := range slugs {
		repo := "acme/" + strings.TrimPrefix(slug, "acme-")
		for i := range 6 {
			id := fmt.Sprintf("20261018-%02d0000-%04x", 10+i, si*8+i)
			s := runstore.Open(b, slug, id)
			if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: repo, Ref: "main", Workflow: "web", Task: "x"}); err != nil {
				t.Fatal(err)
			}
			all = append(all, seeded{slug, id})
			if (si == 1 && i == 5) || (si == 2 && i == 3) {
				if err := s.PutFile(ctx, "result.json", []byte("{nope"), "application/json"); err != nil {
					t.Fatal(err)
				}
				corrupt = append(corrupt, seeded{slug, id})
				continue
			}
			rec := &runstore.Record{Version: 1, RunID: id, Repo: repo, Workflow: "web", Status: runstore.StatusSucceeded}
			if err := s.WriteRecord(ctx, rec); err != nil {
				t.Fatal(err)
			}
		}
	}

	out, errOut, err := execute(t, "runs", "ls", "--all", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got lsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := make([]string, 0, len(all))
	for _, s := range all {
		want = append(want, s.slug+"/"+s.id)
	}
	// The listing's order: start time newest first, the full run reference
	// breaking a tie.
	slices.SortStableFunc(want, func(a, b string) int {
		at, _ := runstore.RunTime(a[strings.Index(a, "/")+1:])
		bt, _ := runstore.RunTime(b[strings.Index(b, "/")+1:])
		if c := bt.Compare(at); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	have := make([]string, 0, len(got.Runs))
	for _, r := range got.Runs {
		have = append(have, r.Run)
	}
	if !slices.Equal(have, want) {
		t.Fatalf("rows = %v, want %v", have, want)
	}

	var warned []string
	for _, line := range strings.Split(errOut, "\n") {
		if after, ok := strings.CutPrefix(line, "warning: run "); ok {
			warned = append(warned, strings.SplitN(after, ": ", 2)[0])
		}
	}
	var wantWarned []string
	for _, s := range corrupt {
		wantWarned = append(wantWarned, s.slug+"/"+s.id)
	}
	if !slices.Equal(warned, wantWarned) {
		t.Fatalf("warnings = %v, want %v: the runs are read in slug order, newest first within each", warned, wantWarned)
	}
}

// lsWidthDriver is the runs bucket a listing's width test reads through: the
// objects the test seeds, and a count of how many object reads are in flight
// at once. A read holds until the pass has want of them open — one past the
// width under test — and gives up at hold, so a pass that widens past its
// bound is measured widening and a pass at the bound is measured sitting at
// it rather than at whatever the scheduler happened to overlap.
type lsWidthDriver struct {
	stubBucket
	mu        sync.Mutex
	objects   map[string][]byte
	want      int
	hold      time.Duration
	open      int
	peak      int
	reached   bool
	width     chan struct{}
	widthOnce sync.Once
}

// errNoObject is the driver's missing object, reported as gcerrors.NotFound:
// a run whose launch.json nobody wrote has no launcher, which is a fact about
// the run rather than a read that failed.
var errNoObject = errors.New("no such object")

func (d *lsWidthDriver) ErrorCode(err error) gcerrors.ErrorCode {
	if errors.Is(err, errNoObject) {
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}

// peakOpen is the most object reads that were ever in flight at once.
func (d *lsWidthDriver) peakOpen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.peak
}

// ListPaged answers the listing a run listing walks: the directories under the
// prefix, one per run, so ls sees the runs the test seeded.
func (d *lsWidthDriver) ListPaged(_ context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	d.mu.Lock()
	keys := make([]string, 0, len(d.objects))
	for k := range d.objects {
		keys = append(keys, k)
	}
	d.mu.Unlock()
	slices.Sort(keys)
	var page driver.ListPage
	seen := map[string]struct{}{}
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, opts.Prefix)
		if !ok {
			continue
		}
		i := strings.Index(rest, opts.Delimiter)
		if opts.Delimiter == "" || i < 0 {
			continue
		}
		dir := opts.Prefix + rest[:i+len(opts.Delimiter)]
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		page.Objects = append(page.Objects, &driver.ListObject{Key: dir, IsDir: true})
	}
	return &page, nil
}

func (d *lsWidthDriver) NewRangeReader(_ context.Context, key string, offset, length int64, _ *driver.ReaderOptions) (driver.Reader, error) {
	d.mu.Lock()
	body, ok := d.objects[key]
	d.mu.Unlock()
	if !ok {
		return nil, errNoObject
	}
	d.mu.Lock()
	d.open++
	if d.open > d.peak {
		d.peak = d.open
	}
	widened := d.open >= d.want && !d.reached
	d.reached = d.reached || widened
	waiting := !d.reached && d.hold > 0
	d.mu.Unlock()
	switch {
	case widened:
		d.widthOnce.Do(func() { close(d.width) })
	case waiting:
		select {
		case <-d.width:
		case <-time.After(d.hold):
		}
	}
	d.mu.Lock()
	d.open--
	d.mu.Unlock()
	if offset > 0 && offset < int64(len(body)) {
		body = body[offset:]
	}
	if length >= 0 && length < int64(len(body)) {
		body = body[:length]
	}
	return &lsWidthReader{Reader: bytes.NewReader(body), size: int64(len(body))}, nil
}

// lsWidthReader serves one object's bytes.
type lsWidthReader struct {
	*bytes.Reader
	size int64
}

func (r *lsWidthReader) Close() error { return nil }
func (r *lsWidthReader) As(any) bool  { return false }
func (r *lsWidthReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{ContentType: "application/json", Size: r.size}
}

// lsWidthRuns writes n runs into a plain bucket and hands back a driver holding
// the same objects byte for byte: the listing reads the driver, so what the
// width test measures is runstore's own objects and never a second fixture's.
func lsWidthRuns(t *testing.T, n int) *lsWidthDriver {
	t.Helper()
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	for i := range n {
		id := at.Add(time.Duration(i)*time.Minute).Format("20060102-150405") + fmt.Sprintf("-%04x", i)
		s := runstore.Open(b.Bucket, appSlug, id)
		if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Status: runstore.StatusSucceeded}); err != nil {
			t.Fatal(err)
		}
	}
	d := &lsWidthDriver{objects: map[string][]byte{}, width: make(chan struct{})}
	for it := b.List(nil); ; {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := b.ReadAll(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		d.objects[obj.Key] = body
	}
	return d
}

// TestRunsLsReadsStayWithinLsWorkers: ls reads many runs' objects at once, and
// no more than lsWorkers of them at any moment. The bound is the half of that
// a peak of one cannot show and the half that is easy to lose: a pass with no
// bound returns the same rows and opens one connection per run of a bucket's
// whole window. Mutations caught: reading one run at a time (a peak of 1), and
// dropping the bound (a peak of every read the fixture holds).
func TestRunsLsReadsStayWithinLsWorkers(t *testing.T) {
	f := newCloudFixture(t)
	const runs = lsWorkers * 2
	d := lsWidthRuns(t, runs)
	d.want = lsWorkers + 1
	d.hold = 250 * time.Millisecond
	env := envOn(t, f, blobx.Wrap(blob.NewBucket(d)))

	rows, err := loadRows(context.Background(), env, lsFilter{slugs: []string{appSlug}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != runs {
		t.Fatalf("rows = %d, want the %d runs the bucket holds: the width test measured a listing that read something else", len(rows), runs)
	}
	if peak := d.peakOpen(); peak <= 1 || peak > lsWorkers {
		t.Errorf("runs ls read %d objects at once, want more than one and at most lsWorkers (%d): the listing reads in parallel and within its bound", peak, lsWorkers)
	}
}

// peakExecutionBackend is the compute backend a listing's follow width is
// measured through: it counts the execution reads the pass has in flight at
// once, and holds each one open until want of them are open at the same time —
// one past the width under test — or hold passes, so a pass that widens past
// its bound is measured widening and a pass at the bound is measured sitting at
// it rather than at whatever the scheduler happened to overlap.
type peakExecutionBackend struct {
	backend.Backend
	want int
	hold time.Duration

	mu      sync.Mutex
	open    int
	peak    int
	calls   int
	reached bool
	gate    chan struct{}
}

// peakCalls is how many execution reads the pass made in all, and the most of
// them that were ever in flight at once.
func (b *peakExecutionBackend) peakCalls() (calls, peak int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, b.peak
}

func (b *peakExecutionBackend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	b.mu.Lock()
	b.open++
	b.calls++
	if b.open > b.peak {
		b.peak = b.open
	}
	// One goroutine widened it, so one closes it: the flag is set inside the
	// same critical section the reading of it is.
	widened := b.open >= b.want && !b.reached
	b.reached = b.reached || widened
	waiting := !b.reached && b.hold > 0
	b.mu.Unlock()
	switch {
	case widened:
		close(b.gate)
	case waiting:
		select {
		case <-b.gate:
		case <-time.After(b.hold):
		}
	}
	b.mu.Lock()
	b.open--
	b.mu.Unlock()
	return b.Backend.Execution(ctx, name)
}

// TestRunsLsFollowsExecutionsWithinFollowWorkers: the follow pass reads back the
// execution of every run the listing did not answer for, and it reads no more
// than followWorkers of them at once. That read leaves the bucket for the
// compute backend, which is why it has a width of its own rather than following
// lsWorkers — and a width is the half a peak of one cannot show: a pass with no
// bound reads every run's execution at once, and one with no fan-out is a queue
// of round trips.
// Mutations caught: following one run at a time (a peak of 1), and dropping the
// bound (a peak that reaches followWorkers+1, which opens the gate and lets the
// rest of the bucket through).
func TestRunsLsFollowsExecutionsWithinFollowWorkers(t *testing.T) {
	f := newCloudFixture(t)
	const runs = followWorkers * 2
	ctx := context.Background()
	bucket, err := blobx.Open(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	for i := range runs {
		id := at.Add(time.Duration(i)*time.Minute).Format("20060102-150405") + fmt.Sprintf("-%04x", i)
		s := runstore.Open(bucket.Bucket, appSlug, id)
		if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}); err != nil {
			t.Fatal(err)
		}
		// A name of this backend's own shape that nothing ever started: the
		// listing cannot answer for it (which is what makes the run reach the
		// backend one by one), and the read that follows it is the one this
		// pass's width is about.
		exec := backend.ExecID{GCPProject: "proj-1234", Region: "us-east5", Job: gcp.JobName(appSlug, "web"), Name: fmt.Sprintf("gone-%04x", i)}.String()
		if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	env := envOn(t, f, bucket)
	peek := &peakExecutionBackend{Backend: env.be, want: followWorkers + 1, hold: 200 * time.Millisecond, gate: make(chan struct{})}
	env.be = peek

	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{appSlug}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	calls, peak := peek.peakCalls()
	if len(rows) != runs || calls != runs {
		t.Fatalf("rows = %d and %d execution reads, want %d of each: the width test measured a pass that read something else", len(rows), calls, runs)
	}
	if peak != followWorkers {
		t.Errorf("runs ls followed %d executions at once, want exactly followWorkers (%d): the pass fans out over the runs the listing could not answer for, within its bound", peak, followWorkers)
	}
}
