package plan

import (
	"sort"
	"strings"
)

// This file is the console v2 plan's V2.2.3: what a read answers about a graph
// beyond what the planner wrote in it.
//
// The design puts the answers here rather than in a renderer (finding F7, "two
// of the four colours are computable today and two are not"): `ready` is a
// property of the graph — a task whose every dependency is done — and "ready" is
// also what a launch is allowed to act on, so a screen that guessed it would be
// a screen that can launch a task whose dependency is half-built. Two consoles
// reading the same graph and the same facts must therefore get the same answer,
// and the only way to guarantee that is for the answer to be computed once, in
// Go, beside the read.
//
// Three things are computed, and they are one feature:
//
//   - **levels**: the longest chain of dependencies ending at each task, so a
//     view can draw a plan in waves rather than by guesswork;
//   - **done / ready / blocked**, per task, against a done set the caller
//     states;
//   - **the critical path** and its length, with the raw downstream count
//     beside it (D26).
//
// The done set is an input and not an inference, and that is the one decision
// this file takes that the design left open. A task is done when the work is
// done; a *run* finishing is a different claim — a run can finish failed, or
// finish with a pull request nobody has reviewed — and this read is a local one
// that opens no bucket (the command takes no cloud flag of any kind), so it
// cannot see a run's state in the first place. The definition of readiness is
// this package's, so two callers cannot disagree about it; the facts it is
// applied to are the caller's, and the document echoes them per task
// (NodeState.Done) so a reader never has to remember what it passed.

// Graph is what a read prints: the artifact's own document, with the answers
// this file computes added to it.
//
// The added fields are additive and the artifact is unchanged — a graph
// artifact is still schema version 1 with the field set Document states, and
// this type is the *read's* output. That is the whole skew story: a console
// that does not know `level`, `ready` or `critical_path` ignores them and draws
// what it drew before, while a graph written by a newer planner is still
// refused by the version check in Parse. The read's own output carries no
// version of its own to bump: `schema_version` is the artifact's, and adding a
// field to what a read prints does not change what a planner may write.
type Graph struct {
	SchemaVersion int         `json:"schema_version"`
	Plan          string      `json:"plan"`
	Nodes         []NodeState `json:"nodes"`
	Edges         []Edge      `json:"edges"`
	// Levels is how many levels the plan has: the longest chain of
	// dependencies plus one, and 0 for a graph with no tasks. It is the count
	// of the waves a view can draw, which is the one figure a per-task level
	// cannot state on its own.
	Levels int `json:"levels"`
	// CriticalPath is the longest chain of tasks in the plan, in tasks. It is
	// what D26's answer ranks a plan by, and it is not the same question as
	// "which task the most others wait on".
	CriticalPath CriticalPath `json:"critical_path"`
	// Bottleneck is the task the critical path runs through, null only when
	// the graph has no tasks. One name rather than a ranking, so a view has
	// one thing to highlight; the figures behind it are per task
	// (NodeState.CriticalPathLength and NodeState.Downstream).
	Bottleneck *string `json:"bottleneck"`
}

// NodeState is one task of a read graph: the artifact's own node, and what the
// read says about it.
//
// The artifact's fields are embedded rather than copied so that the two shapes
// cannot drift: a field added to Node reaches every read's output without this
// type being touched.
type NodeState struct {
	Node
	// Level is the longest chain of dependencies ending at this task: 0 for a
	// task that waits on nothing, otherwise one more than the deepest task it
	// waits for. It is not "how far down the document lists it" — a task's
	// place in a plan is its dependencies, not its line number.
	Level int `json:"level"`
	// Done is the caller's own statement, echoed: this task's work is done.
	// It is not derived from the run the node names, because a finished run is
	// not the same claim (see the file's comment).
	Done bool `json:"done"`
	// Ready is true when the task is not done and every task it needs is done.
	// This is the field a launch may act on (F7).
	Ready bool `json:"ready"`
	// Blocked is true when the task is not done and at least one task it needs
	// is not done. A task is done, ready or blocked — exactly one of the three,
	// always — so a renderer colours a node from the document and never from a
	// rule of its own.
	Blocked bool `json:"blocked"`
	// CriticalPathLength is the longest chain of tasks that starts here,
	// counting this one: 1 for a task nothing waits on. The longest of these is
	// CriticalPath.Length.
	CriticalPathLength int `json:"critical_path_length"`
	// Downstream is how many tasks wait on this one, directly or through
	// others — the raw count D26 keeps as the cheap fallback for a reader that
	// cannot weigh a footprint. It is a different question from
	// CriticalPathLength: six one-line dependents are six, and three dependents
	// that are each a week of work are three.
	Downstream int `json:"downstream"`
}

// CriticalPath is the longest chain of tasks in a graph: how many tasks it runs
// through, and which ones, from the task nothing waits in front of to the task
// nothing waits behind.
type CriticalPath struct {
	// Length is the chain's length in tasks, 0 for a graph with no tasks. It is
	// the same figure as the longest NodeState.CriticalPathLength.
	Length int `json:"length"`
	// Tasks are the tasks along the chain, in the order they can be done, and
	// always an array. Ties are broken by id, so two reads of one graph name
	// the same chain.
	Tasks []string `json:"tasks"`
}

// Analyze computes the read's answers over a document, against a done set.
//
// doc is expected to have been through Normalize, and the call below is that
// same guard rather than a second copy of it. Analyze is exported, so a caller
// that built a Document by hand -- a test, or the day something reads a graph
// out of something other than a file -- would otherwise hand every walk below a
// graph it cannot finish: a cycle is a walk that never returns, an edge to a
// node that is not there is a map that is not there, and a repeated id is two
// tasks the same answers are written for. It gets the read's own refusals, in
// the read's own words, instead.
//
// done names the tasks whose work is done, by node id. An id that is not a task
// of this graph, or an empty one, is refused rather than ignored: an unknown id
// reads exactly like a task nobody has done, which is a wrong answer to the
// question the caller asked.
func Analyze(doc Document, done []string) (Graph, error) {
	name := doc.Plan
	if strings.TrimSpace(name) == "" {
		name = "the graph"
	}
	doc, err := Normalize(name, doc)
	if err != nil {
		return Graph{}, err
	}

	// The graph as sets, because the answers below count tasks and not edges: a
	// dependency written twice is one dependency, and a downstream count that
	// counted it twice would rank a task by how often its edge was repeated.
	needs := make(map[string]map[string]bool, len(doc.Nodes))
	dependents := make(map[string]map[string]bool, len(doc.Nodes))
	for _, n := range doc.Nodes {
		needs[n.ID] = map[string]bool{}
		dependents[n.ID] = map[string]bool{}
	}
	for _, e := range doc.Edges {
		needs[e.Task][e.Needs] = true
		dependents[e.Needs][e.Task] = true
	}

	doneSet := make(map[string]bool, len(done))
	for _, id := range done {
		if strings.TrimSpace(id) == "" {
			return Graph{}, refuse(name, "the done set names an empty task id")
		}
		if _, ok := needs[id]; !ok {
			return Graph{}, refuse(name, "the done set names %q, which is not a task of this graph", id)
		}
		doneSet[id] = true
	}

	// Levels and the critical path lengths are the two directions of the same
	// walk over an acyclic graph: the deepest dependency behind a task, and the
	// longest chain of dependents in front of it. Both are memoized, so a
	// diamond is walked once per node and not once per path.
	level := map[string]int{}
	var levelOf func(string) int
	levelOf = func(id string) int {
		if got, ok := level[id]; ok {
			return got
		}
		deepest := 0
		for _, dep := range sortedKeys(needs[id]) {
			if l := levelOf(dep) + 1; l > deepest {
				deepest = l
			}
		}
		level[id] = deepest
		return deepest
	}
	chain := map[string]int{}
	var chainOf func(string) int
	chainOf = func(id string) int {
		if got, ok := chain[id]; ok {
			return got
		}
		longest := 1
		for _, next := range sortedKeys(dependents[id]) {
			if l := chainOf(next) + 1; l > longest {
				longest = l
			}
		}
		chain[id] = longest
		return longest
	}

	levels := 0
	for _, n := range doc.Nodes {
		if l := levelOf(n.ID) + 1; l > levels {
			levels = l
		}
	}

	g := Graph{
		SchemaVersion: doc.SchemaVersion,
		Plan:          doc.Plan,
		Nodes:         make([]NodeState, 0, len(doc.Nodes)),
		Edges:         doc.Edges,
		Levels:        levels,
	}
	if g.Edges == nil {
		g.Edges = []Edge{}
	}

	for _, n := range doc.Nodes {
		blocked := false
		for dep := range needs[n.ID] {
			if !doneSet[dep] {
				blocked = true
				break
			}
		}
		isDone := doneSet[n.ID]
		g.Nodes = append(g.Nodes, NodeState{
			Node:               n,
			Level:              levelOf(n.ID),
			Done:               isDone,
			Ready:              !isDone && !blocked,
			Blocked:            !isDone && blocked,
			CriticalPathLength: chainOf(n.ID),
			Downstream:         downstreamCount(n.ID, dependents),
		})
	}

	path, bottleneck := criticalPath(doc, chain)
	g.CriticalPath = path
	g.Bottleneck = bottleneck
	return g, nil
}

// downstreamCount is how many tasks wait on id, directly or through others.
// The walk is over the dependents, and it counts each task once however many
// ways it can reach id: a task that waits on two of id's dependents is still
// one task waiting.
func downstreamCount(id string, dependents map[string]map[string]bool) int {
	seen := map[string]bool{}
	queue := sortedKeys(dependents[id])
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		queue = append(queue, sortedKeys(dependents[next])...)
	}
	return len(seen)
}

// criticalPath is the longest chain in the graph, and the task it runs through.
//
// The chain is found from its head: the task with the longest
// CriticalPathLength, then at each step a task that waits on the one before and
// is itself one shorter. Ties are broken by id at both steps, so one graph has
// one critical path rather than whichever one a map iteration happened to
// yield.
//
// The bottleneck is the head of that chain, and it is picked by the same
// tie-break rather than by the raw downstream count: the count answers a
// different question -- how many tasks wait on this one, not how long the work
// in front of it is -- and it is a per-task figure the document carries beside
// every task (D26's "cheap fallback"), not the ranking's own rule. Letting it
// decide a tie on length would name a task that is not on the chain at all,
// which is the one thing "the task the critical path runs through" cannot mean:
// Bottleneck is always CriticalPath.Tasks[0], so the two answers cannot drift
// apart.
func criticalPath(doc Document, chain map[string]int) (CriticalPath, *string) {
	path := CriticalPath{Tasks: []string{}}
	if len(doc.Nodes) == 0 {
		return path, nil
	}
	ids := make([]string, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)

	head := ids[0]
	for _, id := range ids[1:] {
		if chain[id] > chain[head] {
			head = id
		}
	}
	bottleneck := head
	path.Length = chain[head]

	// The chain itself: from the head, the smallest id among the tasks that
	// wait on the current one and are exactly one shorter. There is always one
	// while the length is above 1 — that is what the length means.
	waits := map[string][]string{}
	for _, e := range doc.Edges {
		waits[e.Needs] = append(waits[e.Needs], e.Task)
	}
	for id := head; ; {
		path.Tasks = append(path.Tasks, id)
		if chain[id] <= 1 {
			break
		}
		next := ""
		for _, candidate := range sortedStrings(waits[id]) {
			if chain[candidate] == chain[id]-1 {
				next = candidate
				break
			}
		}
		if next == "" {
			// Unreachable for a graph Normalize accepted: a chain of length n
			// has a task of length n-1 in front of it. Breaking rather than
			// looping is what keeps a surprise from hanging a read.
			break
		}
		id = next
	}
	return path, &bottleneck
}

// sortedKeys is a set's members in id order, so every walk above visits a
// graph's tasks in the same order whatever order the map hands them back in.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedStrings is one slice in id order, for the same reason.
func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
