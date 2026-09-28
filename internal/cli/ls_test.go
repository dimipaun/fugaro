package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

type lsOut struct {
	Runs   []runview.Row  `json:"runs"`
	Totals runview.Totals `json:"totals"`
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
// launch.json's duplicate that exited (N-2).
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
// not a failure of the listing (N-9, S-I2).
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

func TestLsFlagErrors(t *testing.T) {
	newCloudFixture(t)
	for _, args := range [][]string{{"ls", "--since", "7w"}, {"ls", "--all", "--repo", "acme/app"}} {
		if _, _, err := execute(t, args...); ExitCode(err) != ExitUserError {
			t.Errorf("%v: %v", args, err)
		}
	}
}
