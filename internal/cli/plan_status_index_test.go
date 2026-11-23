package cli

// The runs these tests join come from the run index, and this file is the
// reader they come through.
//
// IR2 turns `plan status` from a listing of the runs bucket into a join over
// C-INDEX's Reader: one query per plan task of the checkout's plans, one per
// pull request the runs of those tasks carry, and the estimate table off the
// summary. Neither the bucket nor its ninety-day window is read at all any more
// (D3205, F1), and the tests the task names are what pin that: the read joins a
// run the bucket does not hold, it keeps its bounded merge lookups, and
// --no-merges makes none.
//
// The fake is deliberately narrow. It answers the two questions the contract
// has -- List and Summary -- it serves only the filters this read is allowed to
// make, and it refuses anything else loudly rather than answering it: a read
// that reached for a window, a state or a run id here is the scan this task
// removed, and a fake that quietly answered it would let that back in.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// planIndexFake is the run index a test's read is pointed at: the documents the
// history job's fold would have written, held in memory. It is the same shape
// C-INDEX's Reader has, so the command under test reads it exactly as it reads
// the real one.
type planIndexFake struct {
	mu      sync.Mutex
	entries []runindex.Entry
	// complete and folded are the summary's own two fields (D3215): the default
	// is a walk that has reached the end, so a test that is not about the
	// still-building line is not asked to think about it.
	complete bool
	folded   *time.Time
	// now is the summary's UpdatedAt and the clock the estimate table it
	// answers with is stamped at.
	now time.Time
	// queries is every filter the read asked, in the order it asked them.
	queries []runindex.Filter
}

func newPlanIndexFake() *planIndexFake {
	return &planIndexFake{complete: true, now: time.Now().UTC()}
}

// List answers one page: the entries the filter keeps, newest first, which is
// the index's own order (UpdatedAt descending, then the run id). It serves the
// filters this read is allowed to make and refuses every other one, so a read
// that went looking for the window it used to list fails here rather than
// quietly reading something else.
func (f *planIndexFake) List(_ context.Context, q runindex.Filter) (runindex.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.allowed(q); err != nil {
		return runindex.Page{}, err
	}
	f.queries = append(f.queries, q)
	out := []runindex.Entry{}
	for _, e := range f.entries {
		if len(q.Slugs) > 0 && !indexHasSlug(q.Slugs, e.Slug) {
			continue
		}
		if q.PlanTask != "" && e.PlanTask != q.PlanTask {
			continue
		}
		if q.PR != 0 && indexEntryPR(e) != q.PR {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].RunID > out[j].RunID
	})
	return runindex.Page{Entries: out, IndexComplete: f.complete, FoldedThrough: f.folded}, nil
}

// allowed is the one filter plan status may ask with: the checkout's own
// repository, and a plan task or a pull request. Everything else C-INDEX's
// Filter carries belongs to another read, and a plan status that used one would
// be listing rather than joining.
func (f *planIndexFake) allowed(q runindex.Filter) error {
	switch {
	case len(q.Slugs) != 1:
		return fmt.Errorf("the read asked the index about %d repositories, want the checkout's own alone: %+v", len(q.Slugs), q)
	case q.PlanTask != "" && q.PR != 0:
		return fmt.Errorf("the read asked the index for a plan task and a pull request at once: %+v", q)
	case q.PlanTask == "" && q.PR == 0:
		return fmt.Errorf("the read scanned the index with neither a plan task nor a pull request: %+v", q)
	case !q.Since.IsZero() || len(q.States) > 0 || q.Mine != "" || q.Batch != "" || q.Active || q.Limit != 0 || q.After != "":
		return fmt.Errorf("the read used a filter plan status has no business with: %+v", q)
	}
	return nil
}

// Summary is the installation's one summary document, built from the entries
// this fake holds: the counts, the completeness, and the estimate table the
// read draws its history from (F13, F15) -- built the same way the fold builds
// it, from every run the index carries.
func (f *planIndexFake) Summary(context.Context) (runindex.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	records, states := []runstore.Record{}, map[string]int64{}
	for _, e := range f.entries {
		if e.Record != nil {
			records = append(records, *e.Record)
		}
		if e.Status != "" {
			states[string(e.Status)]++
		}
	}
	sum := runindex.Summary{
		Version: runindex.Version, Complete: f.complete, FoldedThrough: f.folded,
		UpdatedAt: f.now, Runs: int64(len(f.entries)), States: states,
	}
	if len(records) > 0 {
		sum.Estimate = estimate.Build(records, f.now)
	}
	return sum, nil
}

// seedEntry indexes one run, the way the history job's fold would have.
func (f *planIndexFake) seedEntry(e runindex.Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
}

// planIndex is the fake the current test's read is pointed at. Tests in this
// package run one at a time -- none of them calls t.Parallel -- so one pointer
// is enough, and installPlanIndex puts the seam back when the test ends.
var planIndex *planIndexFake

// installPlanIndex points the CLI's plan reads at a fresh fake index and
// returns it, restoring whatever was there when the test ends.
func installPlanIndex(t *testing.T) *planIndexFake {
	t.Helper()
	idx := newPlanIndexFake()
	prevReader, prevIndex := planIndexReader, planIndex
	planIndex, planIndexReader = idx, func(*cloudEnv) runindex.Reader { return idx }
	t.Cleanup(func() { planIndex, planIndexReader = prevIndex, prevReader })
	return idx
}

// planIndexFakeFor is the fake the current test's read is pointed at.
func planIndexFakeFor(t *testing.T) *planIndexFake {
	t.Helper()
	if planIndex == nil {
		t.Fatal("no run index is installed: planStatusRig installs one, and a test that builds its own checkout calls installPlanIndex")
	}
	return planIndex
}

// seedIndexRun folds one run into the index and writes no object of it
// anywhere: it is a run whose records the fold has read, whether or not the
// bucket still holds them.
func seedIndexRun(t *testing.T, id, planTask string, rec *runstore.Record) {
	t.Helper()
	e := runindex.Entry{
		RunID: id, Slug: appSlug, Repo: "acme/app", PlanTask: planTask,
		UpdatedAt: indexUpdatedAt(id, rec),
	}
	if rec != nil {
		e.Record, e.Status, e.FinishedAt = rec, rec.Status, rec.FinishedAt
		if !rec.StartedAt.IsZero() {
			started := rec.StartedAt
			e.StartedAt = &started
		}
		if rec.PR != nil {
			e.PR = rec.PR.Number
		}
	}
	planIndexFakeFor(t).seedEntry(e)
}

// indexUpdatedAt is a run's sort key as the fold reads it: the record's own
// finish, else its start, else the id's own mint time (C-INDEX's rule for
// `updated_at`).
func indexUpdatedAt(id string, rec *runstore.Record) time.Time {
	switch {
	case rec != nil && rec.FinishedAt != nil:
		return *rec.FinishedAt
	case rec != nil && !rec.StartedAt.IsZero():
		return rec.StartedAt
	}
	if minted, err := runstore.RunTime(id); err == nil {
		return minted
	}
	return time.Now().UTC()
}

// indexEntryPR is the pull request number an entry carries: the record's own
// number when it recorded one, the run's otherwise (runview.Row.PR). It is
// spelled here rather than taken from the read's own helper so that the queries
// the read makes are answered by the contract and not by the code under test.
func indexEntryPR(e runindex.Entry) int {
	if e.Record != nil && e.Record.PR != nil && e.Record.PR.Number > 0 {
		return e.Record.PR.Number
	}
	return e.PR
}

func indexHasSlug(slugs []string, slug string) bool {
	for _, s := range slugs {
		if s == slug {
			return true
		}
	}
	return false
}

// historyRecord is one run of the repository's own history as a record: the
// tier, mode, window and cost a sample is drawn from, claiming no plan task. It
// is what a plan's tasks are estimated from, never work on the plan.
func historyRecord(tr tier.Tier, minutes, usd float64) *runstore.Record {
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Duration(minutes) * time.Minute)
	return &runstore.Record{
		Status: runstore.StatusSucceeded, StartedAt: start, FinishedAt: &end,
		Tier: &runstore.TierRecord{Tier: tr}, Rigor: &runstore.RigorRecord{Level: rigor.Pro},
		Cost: &runstore.Cost{TotalUSD: usd},
	}
}

// TestPlanStatusReadsIndexNoScan: the runs this read joins come from the run
// index -- one query per plan task, one per pull request those runs carry --
// and the estimate's history comes off the summary. Neither the runs bucket nor
// its ninety-day window is read at all (D3205, F1): a run the index holds and
// the bucket does not still joins, and a run the bucket holds and the index
// does not is nowhere in the document. Mutation caught: the listing of the
// bucket's objects this read used to make, which would join the run nobody
// indexed, and a history read from the window rather than the summary.
func TestPlanStatusReadsIndexNoScan(t *testing.T) {
	calls := mergedGH(t)
	f := planStatusRig(t, statusTieredGraph)
	today := runDay()
	// The history is the table the summary carries: five runs of the plan's own
	// tier, in the index and in no bucket anywhere.
	for i := range estimate.MinRuns {
		seedIndexRun(t, fmt.Sprintf("%s-08000%d-aa0%d", today, i, i), "", historyRecord(tier.Small, 30, 2))
	}
	// The plan's first task: a run the index holds and the bucket does not.
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	seedIndexRun(t, today+"-120000-bbbb", "plan/A", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR:        &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
		StartedAt: start, FinishedAt: &end,
		Tier: &runstore.TierRecord{Tier: tier.Medium}, Rigor: &runstore.RigorRecord{Level: rigor.Pro},
		Cost: &runstore.Cost{TotalUSD: 2.5},
	})
	// And the second task: the same run, written where the old read found it
	// and in no index, so nothing here may join it.
	seedRunObjects(t, f, today+"-130000-cccc", "plan/B", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 2, URL: "https://github.com/acme/app/pull/2"},
	})

	got, _ := planStatusJSON(t, "--all")
	if len(got.Plans) != 1 || got.Plans[0].ID != "plan" {
		t.Fatalf("plans = %+v", got.Plans)
	}
	if !got.IndexComplete {
		t.Error("index_complete = false, want true: this index has finished a walk")
	}
	state := map[string]plan.StateNode{}
	for _, n := range got.Plans[0].Nodes {
		state[n.ID] = n
	}
	if a := state["A"]; a.State != plan.StateDone || a.Run == "" {
		t.Errorf("A = %+v, want the indexed run joined and the task done", a)
	}
	if b := state["B"]; b.State != plan.StateReady || b.Run != "" {
		t.Errorf("B = %+v, want ready and unjoined: its run is in the bucket alone, and the index is where this read's runs come from", b)
	}
	if asked := ghLookups(t, calls); len(asked) != 1 || !strings.Contains(asked[0], " view 1 ") {
		t.Errorf("gh was asked %q, want the indexed run's pull request (1) alone", asked)
	}
	// The history behind the plan is the summary's own table, and it is in no
	// bucket either: a read that still scanned the window would find none of it.
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	if want := "  ready in 30–30 min, $4.5–$4.5 (2 tasks; history for 2)\n"; !strings.Contains(human, want) {
		t.Errorf("the estimate line is not %q:\n%s", want, human)
	}
	if strings.Contains(human, "still building") {
		t.Errorf("the read says the index is building, and it has finished a walk:\n%s", human)
	}
}

// TestPlanStatusDefaultStillReadsMerges: the default read keeps its merge
// lookups, because `closed`, `mark` and `--all` are drawn from them by every
// console already installed, and a default that stopped asking would redraw
// every one of them with no error (F37). A merged pull request makes the task
// done and closed with no mark, and the document carries no `merges_read` at
// all -- that field is `--no-merges`'s own. Mutation caught: making the new
// flag the default.
func TestPlanStatusDefaultStillReadsMerges(t *testing.T) {
	calls := mergedGH(t)
	planStatusRig(t, statusGraphOne)
	seedIndexRun(t, runDay()+"-090000-abcd", "plan/R", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
	})

	got, _ := planStatusJSON(t, "--all")
	p := got.Plans[0]
	if p.Closed != 1 || p.Open != 0 {
		t.Errorf("plan = %d closed, %d open, want the merged task closed", p.Closed, p.Open)
	}
	if n := p.Nodes[0]; n.State != plan.StateDone || n.Mark != "" || !n.Closed {
		t.Errorf("node = %+v, want done, unmarked and closed: gh answered MERGED", n)
	}
	if asked := ghLookups(t, calls); len(asked) != 1 || !strings.Contains(asked[0], " view 1 ") {
		t.Errorf("gh was asked %q, want the run's own pull request (1)", asked)
	}
	// And the field is absent from the document, not false: a screen tells "no
	// merges were read" from "there was no merge to read" by its presence.
	out, _, err := execute(t, "plan", "status", "--json", "--all")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, present := raw["merges_read"]; present {
		t.Errorf("the default read carries merges_read: %s", out)
	}
}

// TestPlanStatusNoMergesNoGh: --no-merges is the read a screen paints before
// merge state (D3217 as amended). It spawns no gh at all, so every task whose
// run recorded a ready pull request reads done with the `merge unknown` mark --
// the same word this read already uses where no answer could be had -- and
// nothing is counted closed. The document says `merges_read` is false, and only
// this read says it. Mutation caught: a --no-merges that still looks a pull
// request up, and one that leaves such a task reading as if no pull request had
// been recorded at all.
func TestPlanStatusNoMergesNoGh(t *testing.T) {
	calls := mergedGH(t)
	planStatusRig(t, statusGraphOne)
	seedIndexRun(t, runDay()+"-090000-abcd", "plan/R", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
	})

	got, _ := planStatusJSON(t, "--all", "--no-merges")
	if got.MergesRead == nil || *got.MergesRead {
		t.Fatalf("merges_read = %v, want a false the document carries", got.MergesRead)
	}
	p := got.Plans[0]
	if p.Closed != 0 || p.Open != 1 {
		t.Errorf("plan = %d closed, %d open, want nothing closed while no merge was read", p.Closed, p.Open)
	}
	if n := p.Nodes[0]; n.State != plan.StateDone || n.Mark != plan.MergeUnknown || n.Closed {
		t.Errorf("node = %+v, want done with the %q mark and not closed", n, plan.MergeUnknown)
	}
	if asked := ghLookups(t, calls); len(asked) != 0 {
		t.Errorf("gh was asked %q, want nothing: --no-merges spawns no gh", asked)
	}
}

// TestPlanStatusJoinsAFollowUpByPullRequest: a follow-up is given no plan task
// of its own, so the only thing that places it on a task is the pull request it
// follows -- which is why the read makes a second pass of index queries, one
// per pull request the plans' own runs carry (the join's rule iii). Mutation
// caught: a single pass, which would leave the run actually working the task
// off the screen and the task reading as the older run left it.
func TestPlanStatusJoinsAFollowUpByPullRequest(t *testing.T) {
	testutil.FakeCommand(t, "gh", "echo OPEN\n")
	planStatusRig(t, statusGraphOne)
	today := runDay()
	seedIndexRun(t, today+"-090000-aaaa", "plan/R", &runstore.Record{
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"},
	})
	seedIndexRun(t, today+"-100000-bbbb", "", &runstore.Record{
		Status: runstore.StatusRunning, Stage: "implement",
		PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"},
	})

	got, _ := planStatusJSON(t)
	if len(got.Plans) != 1 {
		t.Fatalf("plans = %+v", got.Plans)
	}
	n := got.Plans[0].Nodes[0]
	if n.State != plan.StateRunning || n.Run != "bbbb" || n.Stage != "implement" {
		t.Errorf("node = %+v, want the follow-up joined through pull request 7 and the task running", n)
	}
}

// TestPlanStatusSaysIndexBuilding: while the index has never finished a walk,
// the runs a task is joined to are the ones folded so far, and the read says so
// rather than letting a short list pass for the whole truth (D3215, and the
// constraint the plan holds over every reader of the index). Mutation caught:
// printing the list alone, and dropping the fold's stamp where there is one.
func TestPlanStatusSaysIndexBuilding(t *testing.T) {
	planStatusRig(t, statusGraphOne)
	folded := time.Date(2026, 11, 22, 4, 15, 0, 0, time.UTC)
	idx := planIndexFakeFor(t)
	idx.mu.Lock()
	idx.complete, idx.folded = false, &folded
	idx.mu.Unlock()

	got, _ := planStatusJSON(t)
	if got.IndexComplete {
		t.Error("index_complete = true, want false: no walk has reached the end")
	}
	if got.FoldedThrough == nil || !got.FoldedThrough.Equal(folded) {
		t.Errorf("folded_through = %v, want the fold's own %s", got.FoldedThrough, folded)
	}
	human, _, err := execute(t, "plan", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "index: still building") {
		t.Errorf("the human read does not say the index is still building:\n%s", human)
	}
}
