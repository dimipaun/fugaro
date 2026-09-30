package cli

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
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
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
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
	seedRun(t, f, "20200101-000000-dddd", "b1", "someone@example.com", false) // outside --since
	f.run.SetState(e1, backend.StateRunning)

	out, _, err := execute(t, "ls", "--json")
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
	out, _, _ = execute(t, "ls", "--json", "--mine", "--batch", "b1")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 1 || got.Runs[0].Status != "running" || got.Runs[0].Cost.ComputeUSD <= 0 {
		t.Fatalf("ls --mine --batch b1 = %+v", got.Runs)
	}
	out, _, _ = execute(t, "ls", "--json", "--since", "0")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("ls --since 0 = %d runs", len(got.Runs))
	}
	human, _, err := execute(t, "ls")
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
	for _, bad := range []string{"", "d", "-1d", "7w"} {
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
	out, errOut, err := execute(t, append([]string{"ls", "--json"}, args...)...)
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
	owner := f.run.Start(gcp.JobName(appSlug, "web"))
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
	env, err := openCloud(context.Background(), cloudOptions{})
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
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.run.SetState(e, backend.StateFailed)
	}()
	out, _, err := execute(t, "ls", "--watch", "--json", "--interval", "10ms")
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

// --watch reads the image status once per invocation, not on every redraw.
func TestLsWatchReadsImageStatusOnce(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	e := seedRun(t, f, today+"-090000-aaaa", "", "someone@example.com", true)
	f.run.SetState(e, backend.StateRunning)
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.run.SetState(e, backend.StateFailed)
	}()
	var reads atomic.Int32
	prev := readImageStatus
	readImageStatus = func(ctx context.Context, b *blobx.Bucket, slug, workflow string, now time.Time) (imagecheck.Status, error) {
		reads.Add(1)
		return prev(ctx, b, slug, workflow, now)
	}
	t.Cleanup(func() { readImageStatus = prev })
	out, _, err := execute(t, "ls", "--watch", "--json", "--interval", "10ms")
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
	for _, args := range [][]string{{"ls", "--since", "7w"}, {"ls", "--all", "--repo", "acme/app"}} {
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
		out, _, err := execute(t, "ls", "--json")
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

	out, _, err := execute(t, "ls", "--json")
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
	human, _, err := execute(t, "ls")
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
	out, _, err := execute(t, "ls")
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
	if out, _, _ := execute(t, "ls", "--json"); strings.Contains(out, "\nwarning:") || !strings.Contains(out, `"warnings": [`) {
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
	out, _, _ = execute(t, "ls", "--json")
	if !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("json = %s", out)
	}
	if human, _, _ := execute(t, "ls"); strings.Contains(human, "warning:") {
		t.Fatalf("human ls warned: %s", human)
	}
}

// addCheckJob installs the repository's check job, checking workflows, as
// fugaro init --repo does: ls reads which workflows it checks when there is
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
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\ngit: { provider: github }\nworkflows:\n  web:\n    base: web-node\n" +
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
		human, _, _ := execute(t, "ls")
		if !strings.HasPrefix(human, got.Warnings[0]+"\n") {
			t.Fatalf("%s: human ls = %q", name, human)
		}
	}
}

// runIDAt is a run ID of the day d days ago, at hhmmss, with suffix hex.
func runIDAt(daysAgo int, hhmmss, hex string) string {
	return time.Now().UTC().AddDate(0, 0, -daysAgo).Format("20060102") + "-" + hhmmss + "-" + hex
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
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
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
	if _, _, err := execute(t, "ls", "--pr", "7", "--all"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
		t.Fatalf("--pr --all: %v", err)
	}
	for _, bad := range []string{"0", "-3"} {
		if _, _, err := execute(t, "ls", "--pr", bad); ExitCode(err) != ExitUserError {
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
	if _, _, err := execute(t, "ls", "--pr", "7"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
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
	if _, _, err := execute(t, "ls", "--pr", "7"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ls --pr needs --repo") {
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
	human, _, err := execute(t, "ls", "--pr", "7")
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
	human, _, err := execute(t, "ls", "--pr", "7")
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, l := range strings.Split(human, "\n") {
		if fields := strings.Fields(l); len(fields) > 0 {
			lines[fields[0]] = l
		}
	}
	if l := lines[appSlug+"/"+r.fu1]; !strings.HasSuffix(l, "#7 https://github.com/acme/app/pull/7") {
		t.Fatalf("finished follow-up line = %q\n%s", l, human)
	}
	if l := lines[appSlug+"/"+waiting]; !strings.HasSuffix(l, "#7") {
		t.Fatalf("unlaunched follow-up line = %q\n%s", l, human)
	}
	if l := lines[appSlug+"/"+r.plain]; l != "" {
		t.Fatalf("a run off the PR is listed: %q", l)
	}
}
