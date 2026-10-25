package plan

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	streamcat "github.com/dimipaun/fugaro/internal/streams"
)

// The join tests (GV1.6) are all one shape: a plan's graph, a list of run
// facts, and the answers the state document must carry. They never touch a
// filesystem or a bucket — the CLI tests beside them cover the walk and the
// cloud read — so what is pinned here is the join's rules themselves.

// statusSucceeded is a finished run, in runstore's own word: the join does not
// switch on it — a run that has finished is the default case — but every test
// below needs one.
const statusSucceeded = string(runstore.StatusSucceeded)

// joinDoc is a two-level plan with one optional third task, the same shape
// every test below reuses: a -> b, and c waiting on b.
func joinDoc() Document {
	return Document{
		SchemaVersion: 2,
		Plan:          "a plan",
		Nodes: []Node{
			{ID: "A", Title: "first", Footprint: NewFootprint(nil)},
			{ID: "B", Title: "second", Footprint: NewFootprint(nil)},
			{ID: "C", Title: "third", Footprint: NewFootprint(nil)},
		},
		Edges: []Edge{
			{Task: "B", Needs: "A", Reason: "B needs A"},
			{Task: "C", Needs: "B", Reason: "C needs B"},
		},
	}
}

// mergedPRs is a MergeCheck for tests: every URL in merged answers merged,
// every other URL answers open, and a URL in unknown answers nothing at all.
func mergedPRs(merged, unknown []string) MergeCheck {
	return func(url string) (bool, bool) {
		for _, u := range unknown {
			if u == url {
				return false, false
			}
		}
		for _, u := range merged {
			if u == url {
				return true, true
			}
		}
		return false, true
	}
}

// runFact is one run of the join tests: a task of the plan, launched at
// launched (the run id's own clock), with a status and a pull request.
//
// The test's own label is the run id's suffix and, by its number, the pull
// request the run carries — r2 is pull/2 — so no two runs of one test share a
// pull request and a test says which of them is merged by naming its URL.
func runFact(label, launched, task, status string) RunFact {
	n, err := strconv.Atoi(strings.TrimPrefix(label, "r"))
	if err != nil || n <= 0 {
		panic("runFact's label is r<N>, got " + label)
	}
	return RunFact{
		RunID: "20260101-" + launched + "-" + label, Repo: "acme/widget",
		PlanTask: task, Status: status,
		PR: n, PRURL: "https://github.com/acme/widget/pull/" + strconv.Itoa(n),
	}
}

func statePlan(t *testing.T, doc Document, runs []RunFact, o StateOptions) StatePlan {
	t.Helper()
	if o.Repo == "" {
		o.Repo = "acme/widget"
	}
	got, err := State("the-plan", doc, runs, o)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return got
}

func nodeOf(t *testing.T, got StatePlan, id string) StateNode {
	t.Helper()
	for _, n := range got.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %q in %+v", id, got.Nodes)
	return StateNode{}
}

// TestStateFromRuns: the three shapes a joined run gives a node — a live run
// is running with its stage, a finished run whose pull request gh says is
// merged is done, and a task no run claims is what the graph says it is.
func TestStateFromRuns(t *testing.T) {
	live := runFact("r1", "100000", "the-plan/A", statusRunning)
	live.Stage = "implement"
	done := runFact("r2", "110000", "the-plan/B", statusSucceeded)
	done.Outcome = outcomeReady
	got := statePlan(t, joinDoc(), []RunFact{live, done},
		StateOptions{All: true, Merge: mergedPRs([]string{done.PRURL}, nil)})

	if n := nodeOf(t, got, "A"); n.State != StateRunning || n.Stage != "implement" {
		t.Errorf("A = %+v, want running at its stage", n)
	}
	if n := nodeOf(t, got, "B"); n.State != StateDone || n.Mark != "" {
		t.Errorf("B = %+v, want done with no mark", n)
	}
	if n := nodeOf(t, got, "C"); n.State != StateReady {
		t.Errorf("C = %+v, want ready: nothing waits on it but B, which is done", n)
	}
	if got.ID != "the-plan" || got.Plan != "a plan" || got.Stream != nil {
		t.Errorf("id = %q, plan = %q, stream = %v", got.ID, got.Plan, got.Stream)
	}
}

// TestBlockedUntilNeedsDone: a task whose dependency is not done is blocked,
// and it stays blocked while that dependency's pull request is open — a run
// that finished is not a task that is done.
func TestBlockedUntilNeedsDone(t *testing.T) {
	open := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	open.Outcome = outcomeReady
	got := statePlan(t, joinDoc(), []RunFact{open}, StateOptions{All: true, Merge: mergedPRs(nil, nil)})

	if n := nodeOf(t, got, "A"); n.State != StateNotStarted {
		t.Errorf("A = %+v, want the graph's own answer (its run finished, its PR is open)", n)
	}
	if n := nodeOf(t, got, "B"); n.State != StateBlocked {
		t.Errorf("B = %+v, want blocked", n)
	}
	if n := nodeOf(t, got, "C"); n.State != StateBlocked {
		t.Errorf("C = %+v, want blocked", n)
	}
	// The same graph with the dependency's PR merged: B becomes ready.
	got = statePlan(t, joinDoc(), []RunFact{open}, StateOptions{All: true, Merge: mergedPRs([]string{open.PRURL}, nil)})
	if n := nodeOf(t, got, "A"); n.State != StateDone {
		t.Errorf("A = %+v, want done", n)
	}
	if n := nodeOf(t, got, "B"); n.State != StateReady {
		t.Errorf("B = %+v, want ready", n)
	}
}

// TestDoneNeedsMergeOrMarksUnknown: the merge answer is gh's, and when gh
// cannot give it a run that recorded a ready pull request is done with the
// mark saying which half is unknown. A run that is not ready is not done
// either way.
func TestDoneNeedsMergeOrMarksUnknown(t *testing.T) {
	ready := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	ready.Outcome = outcomeReady
	draft := runFact("r2", "110000", "the-plan/B", statusSucceeded)
	draft.Outcome = "draft"

	// No answer at all: the ready one is done with the mark, the draft is not.
	got := statePlan(t, joinDoc(), []RunFact{ready, draft}, StateOptions{All: true, Merge: mergedPRs(nil, []string{ready.PRURL, draft.PRURL})})
	a := nodeOf(t, got, "A")
	if a.State != StateDone || a.Mark != MergeUnknown {
		t.Errorf("A = %+v, want done with the %q mark", a, MergeUnknown)
	}
	if b := nodeOf(t, got, "B"); b.State == StateDone {
		t.Errorf("B = %+v, want not done: its run recorded a draft, not a ready PR", b)
	}
	// A merged answer for the draft run's pull request, with no answer for the
	// ready one: B is done with no mark, and A keeps the mark.
	got = statePlan(t, joinDoc(), []RunFact{ready, draft}, StateOptions{All: true, Merge: mergedPRs([]string{draft.PRURL}, []string{ready.PRURL})})
	if a, b := nodeOf(t, got, "A"), nodeOf(t, got, "B"); a.Mark != MergeUnknown || b.State != StateDone || b.Mark != "" {
		t.Errorf("A = %+v, B = %+v", a, b)
	}
	// A review run's outcome counts the same as a ready one.
	reviewed := runFact("r3", "120000", "the-plan/C", statusSucceeded)
	reviewed.Outcome = outcomeReviewed
	got = statePlan(t, joinDoc(), []RunFact{reviewed}, StateOptions{All: true, Merge: mergedPRs(nil, []string{reviewed.PRURL})})
	if c := nodeOf(t, got, "C"); c.State != StateDone || c.Mark != MergeUnknown {
		t.Errorf("C = %+v, want done with the unknown mark", c)
	}
}

// TestPlanIDFromPath: the join key is the artifact's own place under docs/,
// which is what a launch recorded in plan_task has to name.
func TestPlanIDFromPath(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"setup-wizard/W1.3", "setup-wizard"},
		{"plans/2026-10-15-desktop-console-v2/V2.2.3", "plans/2026-10-15-desktop-console-v2"},
	} {
		planID, taskID, ok := splitPlanTask(c.path)
		if !ok || planID+"/"+taskID != c.path {
			t.Errorf("splitPlanTask(%q) = %q, %q, %v", c.path, planID, taskID, ok)
		}
	}
	for _, bad := range []string{"", "A", "the-plan", "/A", "setup-wizard/"} {
		if _, _, ok := splitPlanTask(bad); ok {
			t.Errorf("splitPlanTask(%q) accepted a claim that names no task", bad)
		}
	}
	// "setup-wizard/W/1.3" splits at its last slash: a task id is a planner's
	// own name, and a plan id may itself hold a slash — it is a path under
	// docs/. The node is what decides whether a claim joins, so a task id
	// naming nothing joins nothing however it splits.
	if planID, taskID, ok := splitPlanTask("setup-wizard/W/1.3"); !ok || planID != "setup-wizard/W" || taskID != "1.3" {
		t.Errorf("splitPlanTask split = %q, %q, %v", planID, taskID, ok)
	}
}

// TestJoinIgnoresOtherRepo: a run from another repository naming one of this
// plan's tasks is not this plan's run. It is the whole point of scoping the
// join to the checkout's own repository: a prompt-injected launch elsewhere
// could otherwise claim any task and paint it running or done (C3).
func TestJoinIgnoresOtherRepo(t *testing.T) {
	foreign := runFact("r1", "100000", "the-plan/A", statusRunning)
	foreign.Repo = "other/repo"
	foreign.Stage = "implement"
	got := statePlan(t, joinDoc(), []RunFact{foreign}, StateOptions{All: true, Merge: mergedPRs(nil, nil)})
	if n := nodeOf(t, got, "A"); n.State != StateReady || n.Run != "" {
		t.Errorf("A = %+v, want ready and unjoined: the run is another repository's", n)
	}
	// An unknown checkout matches nothing: the join fails closed.
	got = statePlan(t, joinDoc(), []RunFact{runFact("r1", "100000", "the-plan/A", statusRunning)}, StateOptions{Repo: "-", All: true})
	if n := nodeOf(t, got, "A"); n.State != StateReady || n.Run != "" {
		t.Errorf("A = %+v, want unjoined when no repository is known", n)
	}
}

// TestLatestRunWins: of several runs on one task the latest launched decides,
// and a cancelled latest with another run behind it is not the task's end.
func TestLatestRunWins(t *testing.T) {
	first := runFact("r1", "100000", "the-plan/A", statusCancelled)
	second := runFact("r2", "110000", "the-plan/A", statusRunning)
	second.Stage = "review"
	got := statePlan(t, joinDoc(), []RunFact{second, first}, StateOptions{All: true, Merge: mergedPRs(nil, nil)})
	a := nodeOf(t, got, "A")
	if a.State != StateRunning || a.Stage != "review" || a.Run != "r2" {
		t.Errorf("A = %+v, want the latest run's state and its id's own suffix", a)
	}

	// The latest cancelled and nothing else: cancelled.
	got = statePlan(t, joinDoc(), []RunFact{runFact("r1", "100000", "the-plan/A", statusCancelled)}, StateOptions{All: true})
	if a := nodeOf(t, got, "A"); a.State != StateCancelled {
		t.Errorf("A = %+v, want cancelled", a)
	}
	// The latest cancelled with a merged run behind it: done, because the
	// cancelled run is not the only one and the merge is a confirmed fact.
	mergedRun := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	mergedRun.Outcome = outcomeReady
	got = statePlan(t, joinDoc(), []RunFact{mergedRun, runFact("r2", "110000", "the-plan/A", statusCancelled)},
		StateOptions{All: true, Merge: mergedPRs([]string{mergedRun.PRURL}, nil)})
	if a := nodeOf(t, got, "A"); a.State != StateDone {
		t.Errorf("A = %+v, want done: an earlier run's pull request is merged", a)
	}
}

// TestFollowUpJoinsByPR: a follow-up inherits a plan task only when it was
// given one; otherwise it is joined through the pull request it follows.
func TestFollowUpJoinsByPR(t *testing.T) {
	first := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	first.Outcome = outcomeReady
	follow := RunFact{RunID: "20260101-110000-bbbb", Repo: "acme/widget", Status: statusRunning, Stage: "fix", PR: first.PR}
	got := statePlan(t, joinDoc(), []RunFact{first, follow}, StateOptions{All: true, Merge: mergedPRs(nil, nil)})
	a := nodeOf(t, got, "A")
	if a.State != StateRunning || a.Stage != "fix" || a.Run != "bbbb" {
		t.Errorf("A = %+v, want the follow-up joined through the PR it follows", a)
	}
	// A follow-up on an unknown PR joins nothing.
	stranger := RunFact{RunID: "20260101-120000-cccc", Repo: "acme/widget", Status: statusRunning, PR: 77}
	got = statePlan(t, joinDoc(), []RunFact{first, stranger}, StateOptions{All: true, Merge: mergedPRs(nil, []string{first.PRURL})})
	if a := nodeOf(t, got, "A"); a.Run != "r1" || a.State != StateDone {
		t.Errorf("A = %+v, want the run it was joined from and done", a)
	}
	if n := nodeOf(t, got, "B"); n.Run != "" {
		t.Errorf("B = %+v, want unjoined", n)
	}
}

// TestRunWithUnknownPlanTaskIgnored: a run claiming a task of another plan, or
// a task that is not a node, joins nothing. The claim is display-only (C3), so
// a wrong one is dropped rather than honoured.
func TestRunWithUnknownPlanTaskIgnored(t *testing.T) {
	for _, claim := range []string{"another-plan/A", "the-plan/Z", "the-plan/", "A", ""} {
		r := runFact("r1", "100000", claim, statusRunning)
		r.Stage = "implement"
		got := statePlan(t, joinDoc(), []RunFact{r}, StateOptions{All: true})
		if n := nodeOf(t, got, "A"); n.State != StateReady || n.Run != "" {
			t.Errorf("plan_task %q: A = %+v, want ready and unjoined", claim, n)
		}
	}
	// A run with no plan task that follows no PR is not joined either.
	r := RunFact{RunID: "20260101-100000-aaaa", Repo: "acme/widget", Status: statusRunning}
	got := statePlan(t, joinDoc(), []RunFact{r}, StateOptions{All: true})
	if n := nodeOf(t, got, "A"); n.State != StateReady {
		t.Errorf("A = %+v, want ready", n)
	}
}

// TestStateRunningNodeShowsRecordPR: a node's pr_number is the PR the run has
// recorded, whether the run is running or finished (F9).
func TestStateRunningNodeShowsRecordPR(t *testing.T) {
	live := runFact("r1", "100000", "the-plan/A", statusRunning)
	live.Stage, live.RecordPR = "implement", 41
	live.PR, live.PRURL = 0, ""
	got := statePlan(t, joinDoc(), []RunFact{live}, StateOptions{All: true, Merge: mergedPRs(nil, nil)})
	if a := nodeOf(t, got, "A"); a.State != StateRunning || a.PRNumber != 41 {
		t.Errorf("A = %+v, want running with its record's PR number", a)
	}
	// A finished run carries it the same way.
	done := runFact("r2", "100000", "the-plan/A", statusSucceeded)
	done.RecordPR = 42
	done.Outcome = outcomeReady
	got = statePlan(t, joinDoc(), []RunFact{done}, StateOptions{All: true, Merge: mergedPRs([]string{done.PRURL}, nil)})
	if a := nodeOf(t, got, "A"); a.State != StateDone || a.PRNumber != 42 {
		t.Errorf("A = %+v, want done with its record's PR number", a)
	}
}

// TestOpenWorkByDefault: closed work — a merged task and a cancelled one —
// collapses into its release's counter unless the read was asked for
// everything (GD6). The counters are over every task of the release, listed
// or not.
func TestOpenWorkByDefault(t *testing.T) {
	doc := joinDoc()
	doc.Nodes[0].Version = strPtr("0.112")
	doc.Nodes[1].Version = strPtr("0.112")
	doc.Nodes[2].Version = strPtr("0.113")
	mergedRun := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	mergedRun.Outcome = outcomeReady
	cancelled := runFact("r2", "110000", "the-plan/B", statusCancelled)

	open := statePlan(t, doc, []RunFact{mergedRun, cancelled}, StateOptions{Merge: mergedPRs([]string{mergedRun.PRURL}, nil)})
	if len(open.Nodes) != 1 || open.Nodes[0].ID != "C" {
		t.Fatalf("default nodes = %+v, want only the open work", open.Nodes)
	}
	if len(open.Releases) != 2 {
		t.Fatalf("releases = %+v", open.Releases)
	}
	first := open.Releases[0]
	if first.Version != "0.112" || first.Total != 2 || first.Done != 1 || first.Closed != 2 || first.Open != 0 {
		t.Errorf("release 0.112 = %+v, want 2 tasks, 1 done, 2 closed, 0 open", first)
	}
	if second := open.Releases[1]; second.Version != "0.113" || second.Total != 1 || second.Closed != 0 || second.Open != 1 {
		t.Errorf("release 0.113 = %+v", second)
	}
}

// TestAllShowsClosed: --all lists what the counter collapsed, and the closed
// nodes keep the state they were hidden with.
func TestAllShowsClosed(t *testing.T) {
	doc := joinDoc()
	for i := range doc.Nodes {
		doc.Nodes[i].Version = strPtr("0.112")
	}
	mergedRun := runFact("r1", "100000", "the-plan/A", statusSucceeded)
	mergedRun.Outcome = outcomeReady
	cancelled := runFact("r2", "110000", "the-plan/B", statusCancelled)

	all := statePlan(t, doc, []RunFact{mergedRun, cancelled}, StateOptions{All: true, Merge: mergedPRs([]string{mergedRun.PRURL}, nil)})
	if len(all.Nodes) != 3 {
		t.Fatalf("--all nodes = %+v, want every task", all.Nodes)
	}
	if a, b := nodeOf(t, all, "A"), nodeOf(t, all, "B"); a.State != StateDone || b.State != StateCancelled {
		t.Errorf("A = %+v, B = %+v", a, b)
	}
	rel := all.Releases[0]
	if rel.Closed != 2 || rel.Open != 1 || rel.Done != 1 {
		t.Errorf("release = %+v", rel)
	}
}

// TestCriticalAndLevels: a node carries the read's own two answers, so a
// screen highlights the chain GV1.1 computed rather than one of its own.
func TestCriticalAndLevels(t *testing.T) {
	got := statePlan(t, joinDoc(), nil, StateOptions{All: true})
	c := nodeOf(t, got, "C")
	if c.Level != 2 || !c.Critical {
		t.Errorf("C = %+v, want level 2 on the critical path", c)
	}
	if a := nodeOf(t, got, "A"); a.Level != 0 || !a.Critical {
		t.Errorf("A = %+v, want level 0 on the critical path", a)
	}
}

// TestReleaseNamesAndFinished: a release box's name comes from the catalogue
// and is spent one minor at a time; finished says whether the stream has
// already shipped the release the box is for.
func TestReleaseNamesAndFinished(t *testing.T) {
	doc := joinDoc()
	doc.Stream = strPtr(streamcat.Console)
	doc.Nodes[0].Version = strPtr("0.111")
	doc.Nodes[1].Version = strPtr("0.112.0")
	doc.Nodes[2].Version = strPtr("0.113")
	cat := streamcat.Catalogue{Streams: []streamcat.Stream{{
		Name: streamcat.Console, Theme: "fruits", Names: []string{"Cherry", "Mango", "Melon"},
		Current: streamcat.Version{Major: 0, Minor: 111}, Next: "Melon", TagPrefix: "console-v",
	}}}
	got := statePlan(t, doc, nil, StateOptions{All: true, Catalogue: cat})
	if got.Stream == nil || *got.Stream != streamcat.Console {
		t.Fatalf("stream = %v", got.Stream)
	}
	want := map[string]struct {
		name     string
		finished bool
	}{
		"0.111":   {"Mango", true},
		"0.112.0": {"Melon", false},
		"0.113":   {"", false},
	}
	if len(got.Releases) != 3 {
		t.Fatalf("releases = %+v", got.Releases)
	}
	for _, rel := range got.Releases {
		w := want[rel.Version]
		if rel.Name != w.name || rel.Finished != w.finished || rel.Stream != streamcat.Console {
			t.Errorf("release %s = %+v, want %+v", rel.Version, rel, w)
		}
	}
	// A plan that names no stream has no release name and nothing finished.
	doc.Stream = nil
	got = statePlan(t, doc, nil, StateOptions{All: true, Catalogue: cat})
	for _, rel := range got.Releases {
		if rel.Name != "" || rel.Finished || rel.Stream != "" {
			t.Errorf("unstreamed release = %+v", rel)
		}
	}
	// The catalogue prints as the document carries it.
	list := StreamList(cat)
	if len(list) != 1 || list[0].Name != streamcat.Console || list[0].Current != "0.111.0" ||
		list[0].TagPrefix != "console-v" || strings.Join(list[0].Names, ",") != "Cherry,Mango,Melon" {
		t.Errorf("StreamList = %+v", list)
	}
}

// TestStateVocabularyMatchesTheReaders: the statuses and outcomes the join
// switches on are runview's and runstore's own words, spelled here so this
// package keeps no dependency on the cloud read. A rename in either package
// fails here rather than turning every joined run into an unjoined one.
func TestStateVocabularyMatchesTheReaders(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{statusRunning, string(runstore.StatusRunning)},
		{statusPending, runview.StatusPending},
		{statusLaunching, runview.StatusLaunching},
		{statusCancelled, string(runstore.StatusCancelled)},
		{outcomeReady, string(runstore.OutcomeReady)},
		{outcomeReviewed, string(runstore.OutcomeReviewed)},
	} {
		if c.got != c.want {
			t.Errorf("the join spells %q and its reader says %q", c.got, c.want)
		}
	}
	if (runview.Row{}).PlanTask != "" {
		t.Error("runview.Row has no PlanTask field any more: the join's key is gone")
	}
}

// TestStateRefusesWhatTheReadRefuses: a graph the read will not render has no
// states to give, and the refusal is the read's own sentence.
func TestStateRefusesWhatTheReadRefuses(t *testing.T) {
	doc := joinDoc()
	doc.Edges = append(doc.Edges, Edge{Task: "A", Needs: "C", Reason: "a cycle"})
	if _, err := State("the-plan", doc, nil, StateOptions{}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("State = %v, want the read's cycle refusal", err)
	}
}

func strPtr(s string) *string { return &s }
