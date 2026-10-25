package plan

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	streamcat "github.com/dimipaun/fugaro/internal/streams"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is GV1.6: the graph read joined to the runs that are working on it.
//
// V2.2.3's read answers what the graph alone says about a task — its level, its
// critical path, whether every task it needs is done — and it deliberately takes
// the done set as an input, because a finished run is not a done task: a run can
// finish failed, or finish with a pull request nobody has reviewed. A graph view
// wants the other half, and the runs bucket is where it is: a launch records the
// plan task it is working on (GV1.2), so "which of these tasks is being worked
// on, and where did it get to" is a join of two facts this CLI already reads.
// The join is here, in Go, beside the graph read, for the same reason V2.2.3 is:
// two consoles reading one plan and one bucket must not be able to disagree
// about what is done.
//
// Four rules decide it, and each refuses a tempting shortcut:
//
//   - **The join is display-only** (the plan's constraint C3). A run's claim is
//     a hint a job wrote, so it can paint a node — and a node's being done never
//     makes another node ready *by itself*: readiness is computed here over the
//     states this read itself derived, so no launch anywhere can move a plan
//     forward by naming it.
//   - **A run joins only a plan of its own repository** (rule ii). A run starts
//     from one checkout, so a launch in another repository naming this plan's
//     task id is not this plan's run, however exactly it spells the id. Without
//     that scope any repository able to launch could claim any task of any plan
//     a screen was pointed at — the abuse C3 names, and the reason rule (ii) is
//     the whole answer to it rather than a refinement.
//   - **Done means merged.** A run's result record says its pull request is
//     ready; whether that pull request is *merged* is a different question, and
//     the answer comes from the operator's own gh (internal/watch's PRState
//     rules: GitHub only, a strict URL, fail open). Where gh cannot answer — a
//     Bitbucket repository, no gh installed, a lookup that failed — a run that
//     recorded a ready or reviewed pull request shows done with the mark
//     MergeUnknown, so a screen says which half it knows. Showing it not done
//     instead would keep a merged task on a screen forever; calling it done
//     silently would claim a merge nobody checked.
//   - **Closed work collapses into its release's counter** (GD6) unless the read
//     was asked for everything: a plan of forty finished tasks is a wall, and
//     what a person wants from it is the two figures — how much of this release
//     is closed, and how much is left.
//
// Everything this file reads from a run is text a job wrote: a task id, a stage,
// a run id. None of it is printed here — the caller sanitises it at the display
// layer, the way every other job-written field is (the console contract §9.4).

// The run statuses and outcomes the join switches on. They are spelled here
// rather than imported so that this package keeps no dependency on the cloud
// read (internal/runview) or on the record's own package: a plan's graph is read
// locally, on a machine with no project and no bucket. The spelling is not taken
// on trust — state_test.go compares each of these against the constant its
// reader publishes, so a rename in either package fails there rather than
// quietly turning every joined run into an unjoined one.
const (
	statusRunning   = "running"
	statusPending   = "pending"
	statusLaunching = "launching"
	statusCancelled = "cancelled"

	outcomeReady    = "ready"
	outcomeReviewed = "reviewed"
)

// The six states a task can be in. They are the words the document carries and
// the words a renderer shows; a task is exactly one of them.
const (
	// StateRunning is a live run joined to the task: it is being worked on now,
	// and Stage says where.
	StateRunning = "running"
	// StateDone is a task whose run's pull request gh says is merged, or whose
	// run recorded a ready pull request and gh could not answer at all (with
	// the MergeUnknown mark).
	StateDone = "done"
	// StateCancelled is a task whose only run was cancelled: nobody is working
	// on it and nothing came of it.
	StateCancelled = "cancelled"
	// StateBlocked is a task with no done run and at least one dependency that
	// is not done.
	StateBlocked = "blocked"
	// StateReady is a task with no run joined at all and every dependency
	// done. It is the task a launch may act on.
	StateReady = "ready"
	// StateNotStarted is a task with a run joined that is neither live, done
	// nor cancelled — a run that finished with a draft, failed, was halted, or
	// could not be read. The task has been started and it is not finished.
	StateNotStarted = "not-started"
)

// MergeUnknown is the mark a done task carries when its pull request's state
// could not be read: the run recorded a ready or reviewed pull request, and
// whether it was merged is unknown. It is a sentence fixed here rather than
// composed by a renderer, so two screens that show a mark show the same one.
const MergeUnknown = "merge unknown"

// MergeCheck answers whether the pull request at url is merged, and whether the
// answer is available at all: known is false for a URL no lookup can be sent to
// (a Bitbucket URL, or one that is not a pull request URL) and for a lookup that
// failed, and a caller with no lookup at all passes nil. It has no error to
// return: a failure to answer is MergeUnknown's case, not a refusal.
type MergeCheck func(url string) (merged, known bool)

// RunFact is one run as the join sees it: which plan task it claims, which
// repository it is in, what it is doing and which pull request is its own. It is
// built by the caller from the runs read (runview.Row), and every field of it is
// text a job wrote or a record carried — never a decision.
type RunFact struct {
	// RunID is the run's id, which is also its launch order: the runs of one
	// task are compared by it, and the latest launched wins (rule iii).
	RunID string
	// Repo is the repository the run started from, in the launch's own
	// spelling. It is what rule (ii) compares the checkout's repository
	// against, canonicalised before the comparison.
	Repo string
	// PlanTask is the plan task the launch recorded, "<plan id>/<task id>", or
	// "" when it recorded none. It is a hint, and it is shape-checked by the
	// read that publishes it (internal/task's PlanTaskProblem): a claim that
	// does not split into a plan and a task joins nothing.
	PlanTask string
	// Status is the run's own status, in runview's vocabulary.
	Status string
	// Stage is the stage a live run is in, "" for one that is not live.
	Stage string
	// Outcome is the state of the run's pull request, as its record settled it.
	Outcome string
	// PR is the number of the pull request the run itself carries: for a
	// follow-up, the one it follows.
	PR int
	// RecordPR is the number of the pull request the run's own record carries,
	// which is what a running run shows (F9). 0 when the record has none.
	RecordPR int
	// PRURL is the pull request's URL, and the only field a merge lookup is
	// ever given. A URL that is not a GitHub pull request URL is not looked up
	// by any caller of this package.
	PRURL string
}

// StateOptions is what a status read is given beyond the graph: the checkout's
// repository, the streams catalogue and the merge lookup.
type StateOptions struct {
	// Artifact is the graph artifact as the caller named it. It is what a
	// refusal names, and it is echoed in the read, so a screen can say which
	// file a plan came from.
	Artifact string
	// Repo is the checkout's own repository, owner/name. It is what scopes the
	// join (rule ii). "" is an unknown repository and joins nothing, which is
	// the fail-closed reading: a checkout whose origin cannot be read has no
	// runs of its own to claim anything with.
	Repo string
	// All lists closed tasks instead of collapsing them into their release's
	// counter (GD6).
	All bool
	// Catalogue is the release streams catalogue (GV1.4). It is an input
	// because this package reads no file: the zero value names no stream, so no
	// release is named and none is finished.
	Catalogue streamcat.Catalogue
	// Merge answers whether a pull request is merged. nil answers nothing,
	// which reads as MergeUnknown for a run that recorded a ready one.
	Merge MergeCheck
}

// StateNode is one task of a plan status read: the plan's own node, and what the
// join says about it.
type StateNode struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// State is one of the six above, and every field below it is read in its
	// light: a screen colours a node from this word and applies no rule of its
	// own.
	State string `json:"state"`
	// Closed is true for a task the read collapsed: its work is finished
	// (state done with no mark) or abandoned (state cancelled). It is the field
	// `--all` turns on, and it is a field rather than a rule so that a screen
	// asked for everything draws the closed nodes the way the counter counted
	// them.
	Closed bool `json:"closed"`
	// Level and Critical are V2.2.3's own answers, carried through: the longest
	// chain of dependencies ending at this task, and whether it is on the
	// plan's critical path. A view highlights the chain GV1.1 computed rather
	// than one of its own.
	Level    int  `json:"level"`
	Critical bool `json:"critical"`
	// Release is the target version the plan gives this task, as the plan
	// spells it, absent when it gives none. It is the key a release box groups
	// by.
	Release string `json:"release,omitempty"`
	// Run is the suffix of the run this state came from — the four characters a
	// person types and a launch prints (20261025-121805-d204 is d204) — and ""
	// for a task no run is joined to. It is a label: the run's full id is
	// `fugaro runs ls`'s own field, and nothing is authorised by this string.
	Run string `json:"run,omitempty"`
	// Stage is where a live run is, "" when there is none.
	Stage string `json:"stage,omitempty"`
	// PRNumber is the pull request number the run has recorded, whenever it has
	// recorded one — while it is running as much as after it finished (F9) —
	// and 0 when no run is joined or none of its runs has a pull request.
	PRNumber int `json:"pr_number,omitempty"`
	// Mark is why a state is not what its word alone would say, currently only
	// MergeUnknown. It is empty for every other task.
	Mark string `json:"mark,omitempty"`
}

// StateRelease is one target version of a plan: what it is called, whether it
// has shipped, and how its tasks stand. A plan that gives no version to any of
// its tasks has no release boxes at all; one whose versions the plan writes in
// free text (a schema-1 artifact) groups by that text.
type StateRelease struct {
	// Version is the target version as the plan spells it, which is the key its
	// tasks carry in StateNode.Release.
	Version string `json:"version"`
	// Name is the release's name from the streams catalogue — the fruit, bird
	// or animal this minor spends — and "" when the catalogue does not reach
	// it: a plan naming no stream, a version past the catalogue's list, or a
	// version the stream has not scheduled. It is never guessed: a name is
	// spent once, by the file that names it (GD7).
	Name string `json:"name,omitempty"`
	// Stream is the stream this box belongs to, from the plan's own stream, and
	// "" when the plan names none.
	Stream string `json:"stream,omitempty"`
	// Finished is true when the stream's catalogue says the stream is at or
	// past this release: it has shipped. It is false for every box of a plan
	// that names no stream, which is the honest answer — nothing here knows.
	Finished bool `json:"finished"`
	// Total, Done, Closed and Open are the box's figures, over every task of
	// the release, listed or collapsed. Done counts the tasks whose merge was
	// confirmed; Closed adds the cancelled ones, which is what the collapsed
	// read hides; Open is Total minus Closed. A task that is done with an
	// unknown merge is open, so it stays on the screen it is asked about on.
	Total  int `json:"total"`
	Done   int `json:"done"`
	Closed int `json:"closed"`
	Open   int `json:"open"`
}

// StatePlan is one plan as a status read prints it: the plan, the stream it
// ships in, its tasks with their states, and a box per target version.
type StatePlan struct {
	// ID is the join's plan id: what a launch writes before the slash in
	// plan_task (rule i). For an artifact directly under docs/ it is the file's
	// stem, for one in a directory it is that directory's path under docs/, and
	// for docs/plans/ it is "plans/<stem>" — the id is where the artifact sits,
	// so moving a plan renames it.
	ID string `json:"id"`
	// Artifact is the graph artifact, as the walk named it.
	Artifact string `json:"artifact"`
	// Plan is the plan the artifact came from, as the artifact names it.
	Plan string `json:"plan"`
	// Stream is the release stream the plan ships in (GV1.3), absent when the
	// plan does not say.
	Stream *string `json:"stream,omitempty"`
	// Nodes are the plan's tasks, in the artifact's own order — every task with
	// `--all`, the open ones alone without it.
	Nodes []StateNode `json:"nodes"`
	// Releases is one box per target version, in version order.
	Releases []StateRelease `json:"releases"`
	// Tasks, Closed and Open are the plan's own figures over every task it
	// carries, listed or collapsed, so a header drawn from them reads the same
	// in both reads. They are the sum of the release boxes plus the tasks a
	// plan gives no version (which are in no box), which is why they are
	// stated here rather than left to a renderer to add up.
	Tasks  int `json:"tasks"`
	Closed int `json:"closed"`
	Open   int `json:"open"`
}

// StateStream is one row of the streams catalogue, in the shape a console draws
// a column from. The keys are the catalogue file's own, so the document and the
// file cannot drift about what a stream is.
type StateStream struct {
	Name      string   `json:"name"`
	Theme     string   `json:"theme"`
	Names     []string `json:"names"`
	Current   string   `json:"current"`
	Next      string   `json:"next"`
	TagPrefix string   `json:"tag_prefix"`
}

// StreamList is the catalogue as the status document carries it, one row per
// stream in the file's own order.
func StreamList(c streamcat.Catalogue) []StateStream {
	out := make([]StateStream, 0, len(c.Streams))
	for _, s := range c.Streams {
		names := s.Names
		if names == nil {
			names = []string{}
		}
		out = append(out, StateStream{
			Name: s.Name, Theme: s.Theme, Names: names,
			Current: s.Current.String(), Next: s.Next, TagPrefix: s.TagPrefix,
		})
	}
	return out
}

// State joins one plan's graph to the runs that claim it.
//
// planID is the join's plan id (rule i), and it is what a run's plan_task is
// compared against. doc is the graph artifact, refused in the read's own words
// when this build will not render it — a cycle among them, since a cyclic graph
// has no ready or blocked answer to give and this read computes one per task.
//
// The runs are facts the caller read. Nothing here opens a bucket, spawns
// anything or writes anything.
func State(planID string, doc Document, runs []RunFact, o StateOptions) (StatePlan, error) {
	name := o.Artifact
	if strings.TrimSpace(name) == "" {
		name = planID
	}
	// The read's own refusals, in its own words and in its own order: the
	// version check first (Analyze states it), then every shape rule, the cycle
	// among them.
	if _, err := Analyze(name, doc, nil); err != nil {
		return StatePlan{}, err
	}
	normalized, err := Normalize(name, doc)
	if err != nil {
		return StatePlan{}, err
	}
	doc = normalized

	level := map[string]int{}
	for i, wave := range Levels(doc) {
		for _, id := range wave {
			level[id] = i
		}
	}
	critical := map[string]bool{}
	for _, id := range CriticalPathTasks(doc) {
		critical[id] = true
	}

	needs := make(map[string][]string, len(doc.Nodes))
	for _, e := range doc.Edges {
		needs[e.Task] = append(needs[e.Task], e.Needs)
	}
	res := &resolver{
		needs:  needs,
		joined: joinRuns(planID, doc, runs, o.Repo),
		o:      o,
		memo:   map[string]nodeAnswer{},
	}

	out := StatePlan{
		ID: planID, Artifact: o.Artifact, Plan: doc.Plan, Stream: doc.Stream,
		Nodes: []StateNode{}, Releases: []StateRelease{},
	}
	states := make(map[string]StateNode, len(doc.Nodes))
	for _, node := range doc.Nodes {
		sn := StateNode{
			ID: node.ID, Title: node.Title,
			Level: level[node.ID], Critical: critical[node.ID],
		}
		if node.Version != nil {
			sn.Release = *node.Version
		}
		sn.State, sn.Mark, sn.Run, sn.Stage, sn.PRNumber = res.stateOf(node.ID)
		// The collapse (GD6) is per task: a task whose merge was confirmed and a
		// cancelled one are closed, and nothing else is — a task whose merge is
		// unknown is open, so the screen keeps asking about it.
		sn.Closed = (sn.State == StateDone && sn.Mark == "") || sn.State == StateCancelled
		states[node.ID] = sn
		out.Tasks++
		if sn.Closed {
			out.Closed++
		}
		if o.All || !sn.Closed {
			out.Nodes = append(out.Nodes, sn)
		}
	}
	out.Open = out.Tasks - out.Closed
	out.Releases = releases(doc, states, o)
	return out, nil
}

// nodeAnswer is one task's state, as resolver.stateOf answers it and as it is
// remembered while the walk is on the stack.
type nodeAnswer struct {
	state string
	mark  string
	run   string
	stage string
	pr    int
}

// done reports whether this answer is a task a dependency may be built on: the
// merge is confirmed, or the run recorded a ready pull request and nothing in
// this process could say otherwise. The second is the fail-open half, and it is
// the reason this read does not stall on a machine with no gh (the plan's states
// table, GV1.6).
func (a nodeAnswer) done() bool { return a.state == StateDone }

// resolver answers one plan's states: the runs joined to each task, the edges,
// and the options the read was given.
//
// The walk is recursive through a task's dependencies and memoized, so a diamond
// is answered once per node rather than once per path. It terminates because the
// graph is acyclic — the read refused it otherwise before this walk exists — so
// a task's answer never depends on its own.
type resolver struct {
	needs  map[string][]string
	joined map[string][]RunFact
	o      StateOptions
	memo   map[string]nodeAnswer
}

// stateOf is one task's state, which is the whole priority order in one place:
// a confirmed merge, then a live run, then a cancel that is the task's only run,
// then what the graph says.
func (r *resolver) stateOf(id string) (state, mark, run, stage string, pr int) {
	a := r.answer(id)
	return a.state, a.mark, a.run, a.stage, a.pr
}

func (r *resolver) answer(id string) nodeAnswer {
	if a, ok := r.memo[id]; ok {
		return a
	}
	facts := r.joined[id]
	sortRuns(facts)
	// The memo is filled before the walk below recurses, so a diamond is
	// walked once. Nothing can ask about this id again before it is filled:
	// the graph has no cycle to walk back through.
	r.memo[id] = nodeAnswer{}
	a := r.answerOf(id, facts)
	r.memo[id] = a
	return a
}

// answerOf computes one task's answer. It is the whole priority order in one
// place: a confirmed merge, then a live run, then a cancel that is the task's
// only run, then what the graph says.
func (r *resolver) answerOf(id string, facts []RunFact) nodeAnswer {
	a := nodeAnswer{}
	if len(facts) > 0 {
		latest := facts[len(facts)-1]
		// The run a screen names, and the pull request number it shows, come
		// from the latest run: the record's own number when it has one (F9),
		// the run's otherwise.
		a.run, a.pr = runSuffix(latest.RunID), prOf(latest)

		// A merged pull request anywhere in the task's runs is the task's own
		// done fact — a run cancelled after the merge, or a follow-up that
		// replaced the run which opened it, does not unmerge anything — and it
		// is looked for from the latest run backwards, so the run named on the
		// screen is the one that carried it.
		for i := len(facts) - 1; i >= 0; i-- {
			if merged, known := mergeOf(facts[i], r.o.Merge); known && merged {
				return nodeAnswer{state: StateDone, run: runSuffix(facts[i].RunID), pr: prOf(facts[i])}
			}
		}
		switch latest.Status {
		case statusRunning, statusPending, statusLaunching:
			a.state, a.stage = StateRunning, latest.Stage
			return a
		case statusCancelled:
			// The latest run cancelled: that is the task's answer only when it
			// is the task's only run. With another behind it the task is what
			// that other run left it as, which is the walk below.
			if len(facts) == 1 {
				a.state = StateCancelled
				return a
			}
		default:
			// A run that has finished. Its record's outcome is the half of the
			// answer the record carries; the merge above is the half gh
			// carries, and this is the case where gh could not answer at all.
			if recorded(facts) && !mergeAnswered(facts, r.o.Merge) {
				a.state, a.mark = StateDone, MergeUnknown
				return a
			}
		}
	}

	// No merged or live run settles the task, so the graph does.
	if !r.needsDone(id) {
		a.state = StateBlocked
		return a
	}
	if len(facts) == 0 {
		a.state, a.run, a.pr = StateReady, "", 0
		return a
	}
	a.state = StateNotStarted
	return a
}

// needsDone is whether every task this one needs is done — V2.2.3's question,
// asked of the states this read itself computed rather than of a done set a
// caller stated, because the whole point of a status read is that it found out.
func (r *resolver) needsDone(id string) bool {
	for _, need := range r.needs[id] {
		if !r.answer(need).done() {
			return false
		}
	}
	return true
}

// recorded reports whether any of the task's runs left a record that says its
// pull request is ready — or that it reviewed one, which is the same statement
// about the work (an outcome the runs read publishes for a review run).
func recorded(facts []RunFact) bool {
	for _, f := range facts {
		if (f.Outcome == outcomeReady || f.Outcome == outcomeReviewed) && (f.PR > 0 || f.PRURL != "") {
			return true
		}
	}
	return false
}

// mergeAnswered reports whether any of the task's runs had its pull request's
// state answered at all. A task whose merge answer was available and did not say
// merged is not done; one nobody could answer about is done with the mark, which
// is what the caller sees as the difference.
func mergeAnswered(facts []RunFact, merge MergeCheck) bool {
	for _, f := range facts {
		if _, known := mergeOf(f, merge); known {
			return true
		}
	}
	return false
}

// mergeOf asks the caller's lookup about one run's pull request. A run that
// recorded no URL, or a read with no lookup at all, is never asked: there is no
// pull request to look up, and a lookup handed "" would be a lookup about
// nothing.
func mergeOf(f RunFact, merge MergeCheck) (merged, known bool) {
	if merge == nil || f.PRURL == "" {
		return false, false
	}
	return merge(f.PRURL)
}

// prOf is the pull request number a task shows for a run: the record's own
// number when it has one, the run's otherwise.
func prOf(f RunFact) int {
	if f.RecordPR > 0 {
		return f.RecordPR
	}
	return f.PR
}

// sortRuns puts one task's runs in launch order: a run id carries the launch
// time, so ordering by it is ordering by when each was launched (rule iii).
func sortRuns(facts []RunFact) {
	sort.SliceStable(facts, func(i, j int) bool { return facts[i].RunID < facts[j].RunID })
}

// runSuffix is the label a run is shown by: the four characters after its last
// dash, which is the part a person reads out loud and a launch prints. A run id
// is shape-checked where it is read (internal/runstore); a string that is not
// one is shown whole rather than cut into a suffix it does not have.
func runSuffix(id string) string {
	if i := strings.LastIndexByte(id, '-'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// joinRuns assigns the runs to the tasks they claim, by the join's rules (i)
// through (iii).
//
// A run joins a task by the plan task its launch recorded — never by a title,
// and never by the plan's own `run` field, which is the planner's note rather
// than a launch's claim — and only when that claim names this plan and a task of
// it. A follow-up that was given no plan task of its own joins through the pull
// request it follows: it is the same work, continued.
func joinRuns(planID string, doc Document, runs []RunFact, repo string) map[string][]RunFact {
	joined := map[string][]RunFact{}
	for i, taskID := range claimIndex(planID, taskIDs(doc), runs, repo) {
		if taskID != "" {
			joined[taskID] = append(joined[taskID], runs[i])
		}
	}
	return joined
}

// claimIndex is the join itself, one entry per run: the task of this plan that
// the run joins, and "" for a run that joins none of them. State's per-task map
// and JoinedRuns' subset are both built from it, so the runs a read joins and
// the runs a caller may spend something per run on cannot come apart.
func claimIndex(planID string, ids map[string]bool, runs []RunFact, repo string) []string {
	out := make([]string, len(runs))
	owner := map[int]string{} // pull request number -> the task that carries it
	for i, r := range runs {
		if !sameRepo(r.Repo, repo) {
			continue
		}
		taskID, ok := claimedTask(planID, r.PlanTask, ids)
		if !ok {
			continue
		}
		out[i] = taskID
		// The first run to carry a pull request owns it, and the runs are read
		// in the order the read listed them: a follow-up follows the task its
		// previous run is on, and two tasks cannot both claim one pull request.
		if n := prOf(r); n > 0 {
			if _, taken := owner[n]; !taken {
				owner[n] = taskID
			}
		}
	}
	for i, r := range runs {
		if !sameRepo(r.Repo, repo) || r.PlanTask != "" {
			continue
		}
		if n := prOf(r); n > 0 {
			if taskID, ok := owner[n]; ok {
				out[i] = taskID
			}
		}
	}
	return out
}

// taskIDs is a plan's own task ids: what a run's claim has to name to join it.
func taskIDs(doc Document) map[string]bool {
	ids := make(map[string]bool, len(doc.Nodes))
	for _, n := range doc.Nodes {
		ids[n.ID] = true
	}
	return ids
}

// PlanRef is one plan and the id the join knows it by (rule i): what a caller
// holds after reading the artifacts, before it asks for any task's state.
type PlanRef struct {
	ID  string
	Doc Document
}

// JoinedRuns returns the runs that join at least one of the plans — the runs a
// State read of them is made of — in the order they were given.
//
// It is the join's own rules (i) through (iii), answered for the whole set of
// plans at once, and it exists so that a caller can scope what it spends per
// run to the runs a screen will actually ask about. A merge lookup spawns a
// process, and one spawn per distinct pull request of a repository's entire run
// history is a cost that grows with the history rather than with the plan: a
// run that joins no plan of the set is one no state in this read is ever made
// of.
func JoinedRuns(plans []PlanRef, runs []RunFact, repo string) []RunFact {
	joins := make([]bool, len(runs))
	for _, p := range plans {
		for i, taskID := range claimIndex(p.ID, taskIDs(p.Doc), runs, repo) {
			if taskID != "" {
				joins[i] = true
			}
		}
	}
	out := make([]RunFact, 0, len(runs))
	for i, r := range runs {
		if joins[i] {
			out = append(out, r)
		}
	}
	return out
}

// claimedTask is the task a run's plan_task names, when it names one of this
// plan's.
func claimedTask(planID, planTask string, ids map[string]bool) (string, bool) {
	if planTask == "" {
		return "", false
	}
	gotPlan, taskID, ok := splitPlanTask(planTask)
	if !ok || gotPlan != planID || !ids[taskID] {
		return "", false
	}
	return taskID, true
}

// splitPlanTask splits a plan task id into the plan it names and the task it
// names, at the last slash: a plan id may itself hold slashes (it is a path
// under docs/, so plans/2026-10-15-desktop-console-v2 is one), while a task id
// is a planner's own name and is what an edge refers to.
//
// The shape is the reading side's (internal/task's PlanTaskProblem refuses a
// claim with no task in it, an empty path element, a traversal or a control
// character), so what is left here is the split: a claim that does not split
// into two non-empty halves joins nothing, which is what an unshaped claim
// deserves.
func splitPlanTask(planTask string) (planID, taskID string, ok bool) {
	i := strings.LastIndexByte(planTask, '/')
	if i <= 0 || i == len(planTask)-1 {
		return "", "", false
	}
	return planTask[:i], planTask[i+1:], true
}

// sameRepo compares two repositories the way the read that publishes them
// compares them (internal/task's CanonicalRepo: the ASCII fold, a trailing .git
// trimmed), so a checkout spelled acme/app and a launch that recorded
// Acme/App.git are one repository, and a launch that recorded another one is
// not.
//
// A repository that is not owner/name — including the empty one an unreadable
// origin gives — matches nothing, and an unknown checkout therefore joins
// nothing at all.
func sameRepo(a, b string) bool {
	ca, errA := task.CanonicalRepo(a)
	cb, errB := task.CanonicalRepo(b)
	return errA == nil && errB == nil && ca == cb
}

// releases groups the plan's tasks by the target version they carry, in version
// order, and counts each box's figures.
//
// A task the plan gives no version is in no box: the plan has not said which
// release it ships in, and a box named "" would be this read inventing one.
func releases(doc Document, states map[string]StateNode, o StateOptions) []StateRelease {
	versions := []string{}
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Version == nil || seen[*n.Version] {
			continue
		}
		seen[*n.Version] = true
		versions = append(versions, *n.Version)
	}
	sort.Slice(versions, func(i, j int) bool { return releaseLess(versions[i], versions[j]) })

	stream := ""
	if doc.Stream != nil {
		stream = *doc.Stream
	}
	out := make([]StateRelease, 0, len(versions))
	for _, v := range versions {
		rel := StateRelease{Version: v}
		for _, n := range doc.Nodes {
			if n.Version == nil || *n.Version != v {
				continue
			}
			sn := states[n.ID]
			rel.Total++
			if sn.State == StateDone && sn.Mark == "" {
				rel.Done++
			}
			if sn.Closed {
				rel.Closed++
			}
		}
		rel.Open = rel.Total - rel.Closed
		if stream != "" {
			if s, ok := o.Catalogue.Lookup(stream); ok {
				rel.Stream = stream
				rel.Name = releaseName(s, v)
				rel.Finished = releaseFinished(s, v)
			}
		}
		out = append(out, rel)
	}
	return out
}

// releaseVersion is a target version read as three numbers.
type releaseVersion struct{ major, minor, patch int }

// releaseLess orders two target versions: the ones this build can read as
// numbers first, by their numbers, and anything else after them by its own text.
// A schema-1 artifact's versions are the planner's own words, so the order has
// to have an answer for them rather than assuming a number.
func releaseLess(a, b string) bool {
	va, oka := releaseNumbers(a)
	vb, okb := releaseNumbers(b)
	switch {
	case oka && okb:
		if va != vb {
			return releaseBefore(va, vb)
		}
		return a < b
	case oka != okb:
		return oka
	}
	return a < b
}

// releaseNumbers reads a target version as three numbers. The shape is a
// schema-2 promise (MAJOR.MINOR, optionally with .PATCH), and a version that is
// not one — a schema-1 planner's free text — has no numbers to give.
func releaseNumbers(v string) (releaseVersion, bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return releaseVersion{}, false
	}
	var out releaseVersion
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return releaseVersion{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return releaseVersion{}, false
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		default:
			out.patch = n
		}
	}
	return out, true
}

// releaseBefore orders two versions by their numbers.
func releaseBefore(a, b releaseVersion) bool {
	switch {
	case a.major != b.major:
		return a.major < b.major
	case a.minor != b.minor:
		return a.minor < b.minor
	}
	return a.patch < b.patch
}

// releaseName is the name the catalogue gives a release version: the name a
// minor spends, counted from the stream's own place. The catalogue says which
// version a stream is at and which name is next, so the version the stream is at
// carries the name before next, and each further minor is one name further along
// the list. Nothing is guessed beyond that: a version past the list, or one of
// another major line, has no name and gets none.
func releaseName(s streamcat.Stream, version string) string {
	v, ok := releaseNumbers(version)
	if !ok || s.Current.IsZero() || v.major != s.Current.Major {
		return ""
	}
	i := slices.Index(s.Names, s.Next)
	if i < 0 {
		return ""
	}
	i += v.minor - s.Current.Minor - 1
	if i < 0 || i >= len(s.Names) {
		return ""
	}
	return s.Names[i]
}

// releaseFinished is whether the stream has already shipped this release: the
// catalogue's current version is at or past it.
//
// The comparison is written here rather than called from internal/streams
// because that package's own is unexported — it compares the versions a
// catalogue is validated with, and a catalogue this build accepted is the only
// thing on either side of this one.
func releaseFinished(s streamcat.Stream, version string) bool {
	v, ok := releaseNumbers(version)
	if !ok {
		return false
	}
	cur := s.Current
	switch {
	case cur.Major != v.major:
		return cur.Major > v.major
	case cur.Minor != v.minor:
		return cur.Minor > v.minor
	}
	return cur.Patch >= v.patch
}
