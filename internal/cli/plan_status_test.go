package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// statusGraphOther is a second plan, written under docs/plans/ so that its id is
// "plans/other": the tests that read two plans at once use it, and its version
// is the console stream's next release.
const statusGraphOther = `{
  "schema_version": 2,
  "plan": "another plan document",
  "stream": "console",
  "nodes": [
    {"id": "X", "title": "another task", "run": null, "version": "0.112", "footprint": {"prediction": true, "paths": []}}
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
    names: [Cherry, Mango, Melon]
    current: "0.111.0"
    next: Melon
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

// checkoutDir is the working directory the rig left the test in: the checkout
// planStatusRig built, for a test that writes another artifact into it.
func checkoutDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// runDay is the day these tests mint their run ids on. The read lists the runs
// the bucket still holds — its lifecycle age — so a run id dated against another
// day is one the read never sees, and every run these tests seed is dated today.
func runDay() string { return time.Now().UTC().Format("20060102") }

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

// mergedGH is a fake gh that says MERGED for every pull request number, and the
// file every invocation appends its argv to, so a test can count the lookups the
// read made.
func mergedGH(t *testing.T) (calls string) {
	t.Helper()
	return testutil.FakeCommand(t, "gh", "echo MERGED\n")
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
	today := runDay()
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
	today := runDay()
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
	id := runDay() + "-090000-abcd"
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
// run's own service account can write that file — joins nothing, and neither
// that claim nor the stage beside it reaches the document or the table.
//
// What this pins is the claim: the run joins nothing, so its stage is never read
// at all. TestPlanStatusSanitisesAJoinedStage is the one that pins safetext.Field
// on the stage of a run that does join. Mutation caught: opening launch.json
// here rather than reading the row the run reader already sanitised.
func TestPlanStatusIgnoresAMisshapenPlanTask(t *testing.T) {
	testutil.FakeCommand(t, "gh", "echo OPEN\n")
	f := planStatusRig(t, statusGraphOne)
	seedPlanRun(t, f, runDay()+"-090000-aaaa", "plan/R\necho pwned", &runstore.Record{
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
		t.Errorf("the misshapen claim reached the table:\n%s", human)
	}
}

// TestPlanStatusSanitisesAJoinedStage: a run that does join carries a stage a job
// wrote, and it is the one job-written value this read prints. It goes through
// safetext.Field like every other such field, so no line break, escape sequence
// or control character can reach the document or the table. Mutation caught:
// planRunFacts carrying the row's stage through unchanged — the document's stage
// is then the raw string, newline and all.
func TestPlanStatusSanitisesAJoinedStage(t *testing.T) {
	testutil.FakeCommand(t, "gh", "echo OPEN\n")
	f := planStatusRig(t, statusGraphOne)
	const stage = "implement\necho pwned\r\x1b[31mred"
	seedPlanRun(t, f, runDay()+"-090000-abcd", "plan/R", &runstore.Record{
		Status: runstore.StatusRunning, Stage: stage,
	})

	doc, _ := planStatusJSON(t)
	n := doc.Plans[0].Nodes[0]
	if n.State != plan.StateRunning {
		t.Fatalf("node = %+v, want the run joined and running", n)
	}
	if n.Stage != safetext.Field(stage) {
		t.Errorf("stage = %q, want the sanitised %q", n.Stage, safetext.Field(stage))
	}
	if strings.ContainsAny(n.Stage, "\n\r\x1b") {
		t.Errorf("stage = %q, want one line of printable text", n.Stage)
	}
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, stage) || strings.Contains(human, "\x1b") {
		t.Errorf("the raw stage reached the table:\n%q", human)
	}
}

// TestPlanStatusSumsEveryPlan: two artifacts are two plans, each joined on its
// own, and the header's figures are the sum of both. Mutation caught: a read that
// prints every plan but sums the first, and one whose scoping of the runs to the
// plans drops a plan's runs — the second plan's task would then read ready where
// its merged run says done.
func TestPlanStatusSumsEveryPlan(t *testing.T) {
	mergedGH(t)
	f := planStatusRig(t, statusGraph)
	writeCheckoutFile(t, checkoutDir(t), "docs/plans/other.graph.json", statusGraphOther)
	merged := func(n int) *runstore.Record {
		return &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
			PR: &runstore.PRRef{Number: n, URL: fmt.Sprintf("https://github.com/acme/app/pull/%d", n)}}
	}
	seedPlanRun(t, f, runDay()+"-090000-aaaa", "plan/A", merged(1))
	seedPlanRun(t, f, runDay()+"-100000-bbbb", "plans/other/X", merged(2))

	got, _ := planStatusJSON(t, "--all")
	if len(got.Plans) != 2 || got.Plans[0].ID != "plan" || got.Plans[1].ID != "plans/other" {
		t.Fatalf("plans = %+v, want both artifacts, in path order", got.Plans)
	}
	if got.Totals != (planStatusTotals{Plans: 2, Tasks: 4, Closed: 2, Open: 2}) {
		t.Errorf("totals = %+v, want both plans' figures added up", got.Totals)
	}
	other := got.Plans[1]
	if len(other.Nodes) != 1 || other.Nodes[0].ID != "X" || other.Nodes[0].State != plan.StateDone {
		t.Errorf("the second plan's nodes = %+v, want the merged run joined to its task", other.Nodes)
	}
	if r := other.Releases[0]; r.Version != "0.112" || r.Name != "Melon" || r.Finished || r.Done != 1 {
		t.Errorf("the second plan's release = %+v, want the console's next release, named and done", r)
	}
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"docs/plan.graph.json", "docs/plans/other.graph.json", "2 plans, 4 tasks: 2 open, 2 closed",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("the table is missing %q:\n%s", want, human)
		}
	}
}

// TestPlanStatusLooksUpOnlyJoinedRuns: the merge lookup is asked about the pull
// requests of the runs the join reads, and no others. A run of this repository
// that claims no task of any plan here — one with no plan task at all, and one
// claiming another plan — is never looked up, so the cost of a command the
// console calls to draw a graph follows the plans rather than the repository's
// whole run history. Mutation caught: handing planMerges every row the listing
// produced instead of plan.JoinedRuns.
func TestPlanStatusLooksUpOnlyJoinedRuns(t *testing.T) {
	calls := mergedGH(t)
	f := planStatusRig(t, statusGraphOne)
	merged := func(n int) *runstore.Record {
		return &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
			PR: &runstore.PRRef{Number: n, URL: fmt.Sprintf("https://github.com/acme/app/pull/%d", n)}}
	}
	seedPlanRun(t, f, runDay()+"-090000-aaaa", "plan/R", merged(1))
	seedPlanRun(t, f, runDay()+"-100000-bbbb", "", merged(2))
	seedPlanRun(t, f, runDay()+"-110000-cccc", "another-plan/R", merged(3))

	if _, errOut := planStatusJSON(t); strings.Contains(errOut, "warning") {
		t.Errorf("stderr = %q, want nothing to warn about", errOut)
	}
	asked := ghLookups(t, calls)
	if len(asked) != 1 || !strings.Contains(asked[0], " view 1 ") {
		t.Errorf("gh was asked %q, want the joined run's pull request (1) alone", asked)
	}
}

// TestPlanStatusReadsOnlyTheRunsTheBucketHolds: the listing is bounded by the
// runs bucket's own age, the window `run --pr` reads with. A run older than that
// has been deleted with its run, so it cannot join anything and is not looked up
// either. Mutation caught: a listing with no bound at all, which reads — and
// looks up — everything the repository has ever launched.
func TestPlanStatusReadsOnlyTheRunsTheBucketHolds(t *testing.T) {
	calls := mergedGH(t)
	f := planStatusRig(t, statusGraphOne)
	old := time.Now().UTC().Add(-followUpLookback - 24*time.Hour).Format("20060102")
	seedPlanRun(t, f, old+"-090000-aaaa", "plan/R", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
	})

	doc, _ := planStatusJSON(t)
	p := doc.Plans[0]
	if p.Open != 1 || p.Closed != 0 {
		t.Errorf("plan = %d open, %d closed, want the task open: its run is past the bucket's life", p.Open, p.Closed)
	}
	for _, n := range p.Nodes {
		if n.State != plan.StateReady || n.Run != "" {
			t.Errorf("node = %+v, want ready and unjoined: its run is past the bucket's life", n)
		}
	}
	if asked := ghLookups(t, calls); len(asked) != 0 {
		t.Errorf("gh was asked %q, want nothing: no run the bucket holds carries a pull request", asked)
	}
}

// ghLookups is what gh was asked, one entry per invocation: the fake gh records
// its own argv, and a lookup that never ran leaves no file at all.
func ghLookups(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestPlanStatusWarnsOnARefusedArtifact: a graph this build will not render is
// skipped with a warning naming it, and the plans that do render are still
// listed — one unreadable artifact does not take the read down with it.
// Mutation caught: failing the whole read on the first bad artifact, and listing
// it as a plan with no tasks.
func TestPlanStatusWarnsOnARefusedArtifact(t *testing.T) {
	planStatusRig(t, statusGraph)
	writeCheckoutFile(t, checkoutDir(t), "docs/future.graph.json",
		`{"schema_version": 99, "plan": "a plan from a newer CLI", "nodes": [], "edges": []}`)

	doc, errOut := planStatusJSON(t)
	if len(doc.Plans) != 1 || doc.Plans[0].ID != "plan" {
		t.Fatalf("plans = %+v, want the readable plan alone", doc.Plans)
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "docs/future.graph.json") {
		t.Errorf("warnings = %q, want one naming the artifact", doc.Warnings)
	}
	if !strings.Contains(errOut, "schema version 99") {
		t.Errorf("stderr = %q, want the read's own refusal", errOut)
	}
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "warning: docs/future.graph.json") {
		t.Errorf("the human read does not say what it skipped:\n%s", human)
	}
}

// TestPlanStatusWarnsOnAnUnreadableCatalogue: the streams catalogue names
// releases and dates them, and it is not what a task's state is made of — so a
// catalogue this build refuses is a warning, the plans are still read, and every
// release simply has no name. Mutation caught: failing the read on a bad
// catalogue, and naming a release from a catalogue that was not read.
func TestPlanStatusWarnsOnAnUnreadableCatalogue(t *testing.T) {
	planStatusRig(t, statusGraph)
	writeCheckoutFile(t, checkoutDir(t), "docs/release-streams.yaml", "streams: [\n")

	doc, errOut := planStatusJSON(t)
	if len(doc.Streams) != 0 {
		t.Errorf("streams = %+v, want none from a catalogue this build refuses", doc.Streams)
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "release-streams.yaml") {
		t.Errorf("warnings = %q, want one naming the catalogue", doc.Warnings)
	}
	if !strings.Contains(errOut, "release-streams.yaml") {
		t.Errorf("stderr = %q, want the catalogue's own refusal", errOut)
	}
	if len(doc.Plans) != 1 || doc.Plans[0].Tasks != 3 {
		t.Fatalf("plans = %+v, want the plans read anyway", doc.Plans)
	}
	if r := doc.Plans[0].Releases[0]; r.Name != "" || r.Finished {
		t.Errorf("release = %+v, want no name and nothing finished without a catalogue", r)
	}
}

// TestPlanStatusWarnsOnAnUnreadableOrigin: the checkout's repository scopes the
// join, and a checkout whose origin cannot be read joins nothing — the read is
// fail-closed rather than joining every run in the bucket. The graph itself is
// still read: it is not what the repository is for. Mutation caught: treating an
// unknown repository as "every repository".
func TestPlanStatusWarnsOnAnUnreadableOrigin(t *testing.T) {
	testutil.IsolateGit(t)
	newCloudFixture(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q") // a checkout with no origin remote
	writeCheckoutFile(t, dir, "docs/plan.graph.json", statusGraphOne)
	writeCheckoutFile(t, dir, "docs/release-streams.yaml", statusStreams)
	t.Chdir(dir)

	doc, _ := planStatusJSON(t)
	if doc.Repo != "" {
		t.Errorf("repo = %q, want unknown: the checkout has no origin to take it from", doc.Repo)
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "repository") {
		t.Errorf("warnings = %q, want one about the repository", doc.Warnings)
	}
	if len(doc.Plans) != 1 || len(doc.Plans[0].Nodes) != 1 || doc.Plans[0].Nodes[0].State != plan.StateReady {
		t.Fatalf("plans = %+v, want the plan read with its task ready and nothing joined", doc.Plans)
	}
}

// TestPlanStatusRefusesADuplicatePlanID: a plan's id is where its artifact sits,
// and two artifacts can derive one id — docs/plans/dup.graph.json and
// docs/plans/dup/other.graph.json both give "plans/dup". They cannot be joined
// apart, since a run claiming plans/dup/A would paint a task of both, so the
// second is not listed and a warning names both. Mutation caught: listing both
// under one id, where one run's state and one set of figures would be read for
// two plans.
func TestPlanStatusRefusesADuplicatePlanID(t *testing.T) {
	mergedGH(t)
	f := planStatusRig(t, statusGraph)
	dir := checkoutDir(t)
	writeCheckoutFile(t, dir, "docs/plans/dup.graph.json", statusGraph)
	writeCheckoutFile(t, dir, "docs/plans/dup/other.graph.json", statusGraphOther)
	seedPlanRun(t, f, runDay()+"-090000-aaaa", "plans/dup/A", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
	})

	doc, errOut := planStatusJSON(t, "--all")
	if len(doc.Plans) != 2 || doc.Plans[1].ID != "plans/dup" || doc.Plans[1].Artifact != "docs/plans/dup.graph.json" {
		t.Fatalf("plans = %+v, want the first artifact in path order to keep the id", doc.Plans)
	}
	if len(doc.Warnings) != 1 ||
		!strings.Contains(doc.Warnings[0], "docs/plans/dup.graph.json") ||
		!strings.Contains(doc.Warnings[0], "docs/plans/dup/other.graph.json") {
		t.Errorf("warnings = %q, want one naming both artifacts", doc.Warnings)
	}
	if !strings.Contains(errOut, "docs/plans/dup/other.graph.json") {
		t.Errorf("stderr = %q, want the refusal naming the artifact it dropped", errOut)
	}
	// The plan that kept the id is the one the run joins.
	if n := doc.Plans[1].Nodes[0]; n.ID != "A" || n.State != plan.StateDone {
		t.Errorf("the kept plan's first node = %+v, want the merged run joined to it", n)
	}
	if doc.Totals != (planStatusTotals{Plans: 2, Tasks: 6, Closed: 1, Open: 5}) {
		t.Errorf("totals = %+v, want only the two plans listed, added up", doc.Totals)
	}
}

// TestPlanStatusRefusesANonRegularArtifact: a fifo planted where an artifact
// would sit is refused rather than opened — opening one is a read that never
// returns — and the walk says so instead of hanging. Mutation caught: a walk that
// takes every entry with the artifact suffix and lets plan.ReadFile open it.
//
// It is pinned on the walk and not through the command on purpose: a mutation
// here would hang the read, and a test that hangs is a test that says nothing.
func TestPlanStatusRefusesANonRegularArtifact(t *testing.T) {
	dir := t.TempDir()
	writeCheckoutFile(t, dir, "docs/plan.graph.json", statusGraph)
	if err := syscall.Mkfifo(filepath.Join(dir, "docs", "planted.graph.json"), 0o600); err != nil {
		t.Skip(err)
	}
	if _, err := planArtifacts(filepath.Join(dir, "docs")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a fifo under docs/: err = %v, want a refusal naming what it is", err)
	}
}

// TestPlanStatusAsksOnlyAboutItsOwnRepository: a run's pr_url is job-written,
// so a job could point its record at any merged pull request and have its own
// task shown done. A GitHub URL of another repository is never handed to gh,
// and it answers "not merged" rather than "merge unknown": the task stays open
// and unmarked. Mutation caught: dropping the repository check in planMerges.
func TestPlanStatusAsksOnlyAboutItsOwnRepository(t *testing.T) {
	calls := mergedGH(t)
	f := planStatusRig(t, statusGraphOne)
	seedPlanRun(t, f, runDay()+"-090000-aaaa", "plan/R", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 5, URL: "https://github.com/someone/else/pull/5"},
	})
	doc, _ := planStatusJSON(t)
	p := doc.Plans[0]
	if len(p.Nodes) != 1 || p.Nodes[0].State != plan.StateNotStarted || p.Nodes[0].Mark != "" || p.Closed != 0 {
		t.Errorf("plan = %+v, want the task open and unmarked: its run points at another repository's pull request", p)
	}
	if asked := ghLookups(t, calls); len(asked) != 0 {
		t.Errorf("gh was asked %q, want nothing: the URL is not this checkout's repository", asked)
	}
}

// TestPlanMergesCapsTheLookups: one read spawns at most planMaxLookups gh
// processes, the newest runs' pull requests first; the rest have no answer
// (merge unknown) and a warning says so. Mutation caught: dropping the cap.
func TestPlanMergesCapsTheLookups(t *testing.T) {
	calls := mergedGH(t)
	facts := []plan.RunFact{}
	for i := 1; i <= planMaxLookups+3; i++ {
		facts = append(facts, plan.RunFact{
			RunID: fmt.Sprintf("20261025-%06d-aaaa", i), Repo: "acme/app",
			PRURL: fmt.Sprintf("https://github.com/acme/app/pull/%d", i),
		})
	}
	var warnings []string
	merge := planMerges(context.Background(), facts, "acme/app", func(s string) { warnings = append(warnings, s) })
	if asked := ghLookups(t, calls); len(asked) != planMaxLookups {
		t.Errorf("gh was asked %d times, want the cap, %d", len(asked), planMaxLookups)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "3 more pull requests") {
		t.Errorf("warnings = %q, want one naming the three left unasked", warnings)
	}
	if merged, known := merge("https://github.com/acme/app/pull/1"); merged || known {
		t.Errorf("the oldest run's pull request = %v, %v, want no answer: it is past the cap", merged, known)
	}
	if merged, known := merge(fmt.Sprintf("https://github.com/acme/app/pull/%d", planMaxLookups+3)); !merged || !known {
		t.Errorf("the newest run's pull request = %v, %v, want merged: the newest are asked first", merged, known)
	}
}

// TestPlanMergesGhLookupsStayEightWide: a read asks gh about at most
// planLookupWorkers pull requests at once. GitHub's secondary rate limits
// discourage concurrent requests, and this read is not the listing's own —
// its width is the bucket's. The fake gh is what measures it: each run marks
// itself live in a directory, counts the marks, holds for a moment and then
// unmarks itself, so the widest count any run saw is the widest the lookups
// got. Mutation caught: putting planMerges back on parallel(), which is
// lsWorkers wide.
func TestPlanMergesGhLookupsStayEightWide(t *testing.T) {
	dir := t.TempDir()
	live, counts := filepath.Join(dir, "live"), filepath.Join(dir, "counts")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.FakeCommand(t, "gh", fmt.Sprintf(`mkdir -p '%[1]s'
: > "%[1]s/$$"
printf '%%s\n' "$(ls '%[1]s' | wc -l)" >> '%[2]s'
sleep 0.1
rm -f "%[1]s/$$"
echo MERGED
`, live, counts))

	facts := []plan.RunFact{}
	for i := 1; i <= planMaxLookups; i++ {
		facts = append(facts, plan.RunFact{
			RunID: fmt.Sprintf("20261025-%06d-aaaa", i), Repo: "acme/app",
			PRURL: fmt.Sprintf("https://github.com/acme/app/pull/%d", i),
		})
	}
	planMerges(context.Background(), facts, "acme/app", func(string) {})

	b, err := os.ReadFile(counts)
	if err != nil {
		t.Fatalf("gh was never asked: %v", err)
	}
	counted := strings.Fields(string(b))
	if len(counted) != planMaxLookups {
		t.Fatalf("the fake gh ran %d times, want one per lookup, %d", len(counted), planMaxLookups)
	}
	widest := 0
	for _, field := range counted {
		n, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("the fake gh counted %q: %v", field, err)
		}
		widest = max(widest, n)
	}
	if widest > planLookupWorkers {
		t.Errorf("gh was asked about %d pull requests at once, want at most %d: GitHub's secondary rate limits discourage concurrent requests", widest, planLookupWorkers)
	}
}
