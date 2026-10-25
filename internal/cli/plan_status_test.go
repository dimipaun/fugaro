package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// statusGraph is the graph artifact the rig's checkout carries: three tasks in
// a chain, two of them in 0.11 (the CLI stream's shipped release, so the
// catalogue names it Camel) and the third in 0.12 (Dolphin, still open).
const statusGraph = `{
  "schema_version": 2,
  "plan": "a plan document",
  "stream": "cli",
  "nodes": [
    {"id": "A", "title": "first task", "run": null, "version": "0.11", "footprint": {"prediction": true, "paths": []}},
    {"id": "B", "title": "second task", "run": null, "version": "0.11", "footprint": {"prediction": true, "paths": []}},
    {"id": "C", "title": "third task", "run": null, "version": "0.12", "footprint": {"prediction": true, "paths": []}}
  ],
  "edges": [
    {"task": "B", "needs": "A", "reason": "B reads what A writes"},
    {"task": "C", "needs": "B", "reason": "C reads what B writes"}
  ]
}`

// statusGraphOne is a one-task graph, for the tests that are about one run's
// own columns rather than about the graph.
const statusGraphOne = `{
  "schema_version": 2,
  "plan": "a plan document",
  "stream": "cli",
  "nodes": [
    {"id": "R", "title": "the only task", "run": null, "version": "0.12", "footprint": {"prediction": true, "paths": []}}
  ],
  "edges": []
}`

// statusStreams is the catalogue the rig's checkout carries.
const statusStreams = `streams:
  - name: cli
    theme: animals
    names: [Badger, Camel, Dolphin]
    current: "0.11.1"
    next: Dolphin
    tag_prefix: v
  - name: console
    theme: fruits
    names: [Cherry, Mango]
    current: "0.111.0"
    next: Mango
    tag_prefix: console-v
  - name: mobile
    theme: birds
    names: [Falcon, Pelican]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// planStatusRig is a checkout of acme/app — the fixture's own repository, so a
// run of the fixture's bucket joins its plans — carrying one graph artifact and
// the streams catalogue.
func planStatusRig(t *testing.T, graph string) *cloudFixture {
	t.Helper()
	// Before any git runs: CI has no identity of its own.
	testutil.IsolateGit(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	writeCheckoutFile(t, dir, "docs/plan.graph.json", graph)
	writeCheckoutFile(t, dir, "docs/release-streams.yaml", statusStreams)
	t.Chdir(dir)
	return f
}

func writeCheckoutFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedPlanRun is a run of the fixture's repository whose launch recorded
// planTask, with rec as its result.json when one is given.
func seedPlanRun(t *testing.T, f *cloudFixture, id, planTask string, rec *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", RequestedBy: "someone@example.com"}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now(), PlanTask: planTask}); err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		return
	}
	rec.Version, rec.RunID, rec.Repo = 1, id, "acme/app"
	writeRecord(t, f, id, rec)
}

// mergedGH is a fake gh that says MERGED for every pull request number.
func mergedGH(t *testing.T) {
	t.Helper()
	testutil.FakeCommand(t, "gh", "echo MERGED\n")
}

func planStatusJSON(t *testing.T, args ...string) (planStatusDoc, string) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"plan", "status", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("plan status: %v (%s)", err, errOut)
	}
	var got planStatusDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got, errOut
}

// TestPlanIDFromPath: the join's plan id is where the artifact sits, and the
// two places a plan is written — beside docs/ itself and in docs/plans/ — are
// the two the launch's --plan-task spells.
func TestPlanIDFromPath(t *testing.T) {
	for path, want := range map[string]string{
		"docs/plan.graph.json":                    "plan",
		"docs/plans/2026-10-25-a-plan.graph.json": "plans/2026-10-25-a-plan",
		"docs/setup-wizard/graph.graph.json":      "setup-wizard",
		"docs/a/b/c.graph.json":                   "a/b",
	} {
		if got := planIDFromPath(path); got != want {
			t.Errorf("planIDFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestArtifactCountCap: the walk reads a checkout's plans, and a directory that
// carries more of them than the cap is refused rather than read without end.
// Mutation caught: counting only what a read later succeeded on, or reading
// every artifact a checkout holds.
func TestArtifactCountCap(t *testing.T) {
	dir := t.TempDir()
	for i := range planMaxArtifacts {
		writeCheckoutFile(t, dir, filepath.Join("docs", "p"+string(rune('a'+i%26))+string(rune('a'+i/26))+".graph.json"), "{}")
	}
	got, err := planArtifacts(filepath.Join(dir, "docs"))
	if err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if len(got) != planMaxArtifacts {
		t.Fatalf("at the cap: %d artifacts, want %d", len(got), planMaxArtifacts)
	}
	writeCheckoutFile(t, dir, "docs/one-too-many.graph.json", "{}")
	if _, err := planArtifacts(filepath.Join(dir, "docs")); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("past the cap: err = %v, want a refusal naming the cap", err)
	}
}

// TestCheckoutConfined (GV2.1's probe, run here because this is the walk it
// probes): a symlink under docs/ ends the read instead of being followed, and a
// docs/ that is itself a link out of the checkout is refused. Mutation caught:
// a WalkDir that follows links, and a read that resolves a path before deciding
// whether to open it — either one reads and prints a directory the checkout
// does not hold.
func TestCheckoutConfined(t *testing.T) {
	outside := t.TempDir()
	writeCheckoutFile(t, outside, "secret.graph.json", statusGraph)

	dir := t.TempDir()
	writeCheckoutFile(t, dir, "docs/plan.graph.json", statusGraph)
	if err := os.Symlink(filepath.Join(outside, "secret.graph.json"), filepath.Join(dir, "docs", "linked.graph.json")); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}
	if _, err := planArtifacts(filepath.Join(dir, "docs")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("a symlink under docs/: err = %v, want a refusal", err)
	}

	// The same link one level down: the walk must not enter the directory it
	// points at either.
	dir2 := t.TempDir()
	writeCheckoutFile(t, dir2, "docs/plan.graph.json", statusGraph)
	if err := os.Symlink(outside, filepath.Join(dir2, "docs", "elsewhere")); err != nil {
		t.Fatal(err)
	}
	if _, err := planArtifacts(filepath.Join(dir2, "docs")); err == nil {
		t.Error("a linked directory under docs/ was walked into")
	}

	// docs/ itself a link.
	dir3 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir3, "docs")); err != nil {
		t.Fatal(err)
	}
	if _, err := planArtifacts(filepath.Join(dir3, "docs")); err == nil {
		t.Error("a linked docs/ was read")
	}

	// A checkout with no docs/ at all is no plans, not a refusal.
	if got, err := planArtifacts(filepath.Join(t.TempDir(), "docs")); err != nil || len(got) != 0 {
		t.Errorf("no docs/: %q, %v", got, err)
	}
}

// TestPlanStatusOpenWorkByDefault: through the command, merged and cancelled
// work collapses into its release's counters — the run of a merged pull request
// is the task's done fact, a cancelled only-run is its cancelled one — and the
// figures the header is drawn from are over every task either way.
func TestPlanStatusOpenWorkByDefault(t *testing.T) {
	mergedGH(t)
	f := planStatusRig(t, statusGraph)
	today := time.Now().UTC().Format("20060102")
	merged := &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"}}
	seedPlanRun(t, f, today+"-090000-aaaa", "plan/A", merged)
	seedPlanRun(t, f, today+"-100000-bbbb", "plan/B", &runstore.Record{Status: runstore.StatusCancelled})

	got, _ := planStatusJSON(t)
	if got.Repo != "acme/app" {
		t.Fatalf("repo = %q, want the checkout's own", got.Repo)
	}
	if len(got.Plans) != 1 {
		t.Fatalf("plans = %+v", got.Plans)
	}
	p := got.Plans[0]
	if p.ID != "plan" || p.Plan != "a plan document" {
		t.Errorf("plan = %q of %q", p.ID, p.Plan)
	}
	if len(p.Nodes) != 1 || p.Nodes[0].ID != "C" {
		t.Fatalf("nodes = %+v, want only the open work", p.Nodes)
	}
	if c := p.Nodes[0]; c.State != plan.StateBlocked || c.Release != "0.12" {
		t.Errorf("C = %+v, want blocked: B is cancelled, so it is not done", c)
	}
	if p.Tasks != 3 || p.Closed != 2 || p.Open != 1 {
		t.Errorf("plan figures = %d/%d/%d, want 3 tasks, 2 closed, 1 open", p.Tasks, p.Closed, p.Open)
	}
	if len(p.Releases) != 2 {
		t.Fatalf("releases = %+v", p.Releases)
	}
	if r := p.Releases[0]; r.Version != "0.11" || r.Name != "Camel" || !r.Finished || r.Closed != 2 || r.Open != 0 {
		t.Errorf("release 0.11 = %+v, want the shipped Camel with both tasks closed", r)
	}
	if r := p.Releases[1]; r.Version != "0.12" || r.Name != "Dolphin" || r.Finished || r.Open != 1 {
		t.Errorf("release 0.12 = %+v", r)
	}
	if got.Totals != (planStatusTotals{Plans: 1, Tasks: 3, Closed: 2, Open: 1}) {
		t.Errorf("totals = %+v", got.Totals)
	}
	if len(got.Streams) != 3 || got.Streams[0].Current != "0.11.1" {
		t.Errorf("streams = %+v", got.Streams)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %q", got.Warnings)
	}
}

// TestPlanStatusAllShowsClosed: --all lists what the counters collapsed, with
// the state each was hidden by, and the same figures.
func TestPlanStatusAllShowsClosed(t *testing.T) {
	mergedGH(t)
	f := planStatusRig(t, statusGraph)
	today := time.Now().UTC().Format("20060102")
	merged := &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"}}
	seedPlanRun(t, f, today+"-090000-aaaa", "plan/A", merged)
	seedPlanRun(t, f, today+"-100000-bbbb", "plan/B", &runstore.Record{Status: runstore.StatusCancelled})

	got, _ := planStatusJSON(t, "--all")
	p := got.Plans[0]
	if len(p.Nodes) != 3 {
		t.Fatalf("--all nodes = %+v, want every task", p.Nodes)
	}
	state := map[string]string{}
	for _, n := range p.Nodes {
		state[n.ID] = n.State
	}
	if state["A"] != plan.StateDone || state["B"] != plan.StateCancelled {
		t.Errorf("states = %v", state)
	}
	if got.Totals.Tasks != 3 || got.Totals.Closed != 2 || got.Totals.Open != 1 {
		t.Errorf("totals = %+v, want the same figures as the collapsed read", got.Totals)
	}
}

// TestPlanStatusRunningNodeShowsRecordPR: a running run's row carries the
// record's own pull request number, the run's label and the stage it is in, and
// the task it works on reads running rather than ready.
func TestPlanStatusRunningNodeShowsRecordPR(t *testing.T) {
	testutil.FakeCommand(t, "gh", "echo OPEN\n")
	f := planStatusRig(t, statusGraphOne)
	const id = "20260101-090000-abcd"
	seedPlanRun(t, f, id, "plan/R", &runstore.Record{
		Status: runstore.StatusRunning, Stage: "implement",
		PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"},
	})

	got, _ := planStatusJSON(t)
	n := got.Plans[0].Nodes[0]
	if n.State != plan.StateRunning || n.Run != "abcd" || n.PRNumber != 7 || n.Stage != "implement" {
		t.Errorf("node = %+v, want a running task naming run abcd, PR 7 and stage implement", n)
	}
	if got.Plans[0].Open != 1 {
		t.Errorf("figures = %+v", got.Plans[0])
	}
}

// TestPlanStatusIgnoresAMisshapenPlanTask: the join reads only the id the run
// reader published, so a launch.json whose plan_task is not a plan task — the
// run's own service account can write that file — joins nothing, and a stage a
// job wrote cannot reach the document or the table with a newline in it.
// Mutation caught: reading launch.json here, or taking the read's sanitised
// value and printing it unsanitised.
func TestPlanStatusIgnoresAMisshapenPlanTask(t *testing.T) {
	testutil.FakeCommand(t, "gh", "echo OPEN\n")
	f := planStatusRig(t, statusGraphOne)
	today := time.Now().UTC().Format("20060102")
	seedPlanRun(t, f, today+"-090000-aaaa", "plan/R\necho pwned", &runstore.Record{
		Status: runstore.StatusRunning, Stage: "implement\nrm -rf /",
	})

	doc, _ := planStatusJSON(t)
	n := doc.Plans[0].Nodes[0]
	if n.Run != "" || n.Stage != "" || n.State != plan.StateReady {
		t.Errorf("node = %+v, want the misshapen claim joined to nothing", n)
	}
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "pwned") || strings.Contains(human, "rm -rf") {
		t.Errorf("job-written text reached the table:\n%s", human)
	}
}
