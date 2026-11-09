package plan

import (
	"os"
	"path/filepath"
	"strings"
)

// This file is CD3 of docs/contract-first-decoupling: the rest of the
// contract-first decoupling pass the third schema records (D1811, D1816). Two
// questions are answered here, and both are answered where the graph is read
// rather than taken from the planner's word.
//
// **What the pass bought**, as a before and an after. The artifact carries the
// `was` edges, the sizes and which nodes are the pass's own; the read rebuilds
// the plan the pass ran over from them and measures both. That is the decision
// behind the schema bump itself — the CLI, not the agent, does the arithmetic —
// and the figure it protects is the honest one: breaking a single edge adds two
// tasks, so a plan measured in tasks alone reports a *loss* for the simplest
// break (F5). The weighted path is the one "improves" turns on (D1816).
//
// **Which stubs are still in the tree.** A stub is a whole file the contract
// names (D1815), and the integration task deletes it; whether it is still there
// is a question only a tree can answer, so this is the one place in the package
// that touches the filesystem. Stub paths are untrusted plan text (F9), so the
// read resolves them through `os.Root`: a symlink — in the last element or in a
// directory above it — that leaves the checkout is refused by the root rather
// than followed out of it. The tree is the checkout the plan is read in, and a
// plan read from another tree is refused rather than reported against the wrong
// one, because "this stub is gone" said about a repository the plan does not
// describe is worse than saying nothing.

// Gain is what the decoupling pass bought: the edges it broke and left, the
// tasks it added, the plan's critical path before and after, and whether the
// break paid.
//
// Every figure in it is computed from the document — the restored `was` edges,
// the sizes, the node kinds — and none of them can be written into an artifact:
// `gain` is not a field of Document, so a planner that wrote one is refused by
// name, the same way it is refused any other key this build does not know.
type Gain struct {
	// Broken is how many edges the pass broke: the edges the document's
	// decoupling record names, each pair once, a record that names one edge
	// twice having broken one edge.
	Broken int `json:"broken"`
	// Kept is how many dependencies the pass left alone: the edges of this
	// graph that join two tasks, each pair once. An edge onto a contract, an
	// integration or a change request is the pass's scaffolding rather than a
	// dependency the plan had before it, so neither figure counts it — Broken
	// and Kept are the two halves of what the pass decided.
	Kept int `json:"kept"`
	// AddedTasks is how many tasks the pass added: every contract, integration
	// and change-request node of this graph. It is the figure F11's cost is
	// stated in, and it is the difference between the two plans' task counts.
	AddedTasks int `json:"added_tasks"`
	// Before is the plan's critical path as it stood before the pass, and After
	// is the one this document states.
	Before PathMeasure `json:"before"`
	After  PathMeasure `json:"after"`
	// Improves is whether the break paid: After.Weight is below Before.Weight.
	// It is the weighted path and not the task count for F5's reason — the
	// simplest break lengthens the count of tasks and shortens the work — so a
	// plan whose break lengthens the weighted path says so instead of reporting
	// a gain nobody got.
	Improves bool `json:"improves"`
}

// PathMeasure is one plan's critical path, measured the two ways D1816 keeps
// apart: in tasks, which is the figure a reader can always check by counting,
// and weighted by each task's size, which is the one the work was actually
// shortened in.
//
// The two are measured independently rather than one through the other: a chain
// of three small tasks and one large task are both candidates, and the longest
// chain is not always the heaviest.
type PathMeasure struct {
	// Tasks is the plan's longest chain of dependencies, in tasks.
	Tasks int `json:"tasks"`
	// Weight is the plan's heaviest chain of dependencies: every task on it
	// counted as its `size`, a task that carries none counting as one. It is
	// measured on its own rather than read off the chain Tasks names, so the two
	// need not describe one chain — a plan whose longest chain runs through small
	// tasks has a shorter, heavier one beside it.
	Weight float64 `json:"weight"`
}

// decouplingGain computes the gain of a graph that recorded a break. It is only
// called for one: a graph that broke no edge has no before graph to restore and
// no gain to report, and printing a gain of zero would be noise in place of an
// absence.
func decouplingGain(path string, doc Document) (Gain, error) {
	before, err := beforePath(path, doc)
	if err != nil {
		return Gain{}, err
	}
	// The after graph is the document as it stands, so it is a graph Normalize
	// has already walked: it is acyclic, and its edges name nodes that are
	// there.
	after, err := measurePath(path, doc.Nodes, doc.Edges)
	if err != nil {
		return Gain{}, err
	}

	kinds := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		kinds[n.ID] = kindOf(n)
	}
	broken := make([]Edge, 0, len(doc.Decoupling))
	for _, d := range doc.Decoupling {
		broken = append(broken, Edge{Task: d.Was.Task, Needs: d.Was.Needs})
	}
	kept := make([]Edge, 0, len(doc.Edges))
	for _, e := range doc.Edges {
		if kinds[e.Task] == KindTask && kinds[e.Needs] == KindTask {
			kept = append(kept, e)
		}
	}
	g := Gain{
		Broken: countPairs(broken),
		Kept:   countPairs(kept),
		Before: before,
		After:  after,
	}
	for _, n := range doc.Nodes {
		if kinds[n.ID] != KindTask {
			g.AddedTasks++
		}
	}
	g.Improves = g.After.Weight < g.Before.Weight
	return g, nil
}

// countPairs is how many dependencies a list of edges states: a dependency
// written twice is one dependency, the rule analyze.go counts a diamond's arms
// by — a figure that counted a repeated edge twice would state the pass's work
// in terms of how often a planner repeated itself.
func countPairs(edges []Edge) int {
	seen := map[string]map[string]bool{}
	n := 0
	for _, e := range edges {
		if seen[e.Task] == nil {
			seen[e.Task] = map[string]bool{}
		}
		if seen[e.Task][e.Needs] {
			continue
		}
		seen[e.Task][e.Needs] = true
		n++
	}
	return n
}

// beforePath rebuilds the plan the pass ran over and measures it.
//
// The reconstruction is the only one the document admits: every contract,
// integration and change-request node is the pass's own and is removed, the
// recorded `was` edges are restored beside the edges that were kept, and every
// edge that ran through a removed integration node is rewritten onto the
// endpoints of the edges that node re-joins — a task that waited on the
// integration node was waiting on both sides of the break, because the
// integration node is the task that runs them together.
//
// An edge onto a removed node that is not a rewrite is dropped, which is what
// the pass's own scaffolding is: the edge a consumer gained onto the contract
// stands where the restored `was` edge used to, and counting both would count
// one dependency twice.
func beforePath(path string, doc Document) (PathMeasure, error) {
	kinds := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		kinds[n.ID] = kindOf(n)
	}
	// What each integration node re-joins, in the entries' own order, so the
	// rewrite below is a function of the document rather than of a map walk.
	rejoined := map[string][]string{}
	for _, d := range doc.Decoupling {
		rejoined[d.Integration] = append(rejoined[d.Integration], d.Was.Task, d.Was.Needs)
	}

	nodes := make([]Node, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if kinds[n.ID] == KindTask {
			nodes = append(nodes, n)
		}
	}
	edges := make([]Edge, 0, len(doc.Edges)+len(doc.Decoupling))
	for _, e := range doc.Edges {
		switch {
		case kinds[e.Task] == KindTask && kinds[e.Needs] == KindTask:
			edges = append(edges, e)
		case kinds[e.Task] == KindTask && kinds[e.Needs] == KindIntegration:
			for _, endpoint := range rejoined[e.Needs] {
				edges = append(edges, Edge{Task: e.Task, Needs: endpoint, Reason: e.Reason})
			}
		}
	}
	for _, d := range doc.Decoupling {
		// The edge is restored onto the two tasks that stood on either side of
		// it. A break whose recorded edge names a node of the pass itself is a
		// document disagreeing with itself — the before graph has no such node
		// to restore the edge onto — so it is refused rather than dropped, which
		// would report the gain of a plan nobody wrote.
		if kinds[d.Was.Task] != KindTask || kinds[d.Was.Needs] != KindTask {
			return PathMeasure{}, refuse(path, "the decoupling entry for the contract %q records the edge %s -> %s, and one of them is a node of the pass; the before graph restores the edge onto the two tasks that stood on either side of it",
				d.Contract, d.Was.Task, d.Was.Needs)
		}
		edges = append(edges, Edge{Task: d.Was.Task, Needs: d.Was.Needs, Reason: d.Reason})
	}
	return measurePath(path, nodes, edges)
}

// measurePath is one graph's critical path, in tasks and weighted.
//
// The two walks are the deepest-dependency walk analyze.go's levels are, one
// node at a time, and each node's answer carries both figures: a graph of tasks
// with no sizes gives the same chain for both, and a graph that weighs them can
// give the two different chains, which is exactly why both are reported.
//
// A cycle is refused, in the read's own voice. The after graph cannot have one
// — Normalize refused it — but the before graph is a reconstruction, and a
// document whose recorded breaks restore an edge the pass also kept elsewhere
// can close one. A walk that followed it would never return, and the answers
// here are a function of the graph, so a cyclic one has none to give.
func measurePath(path string, nodes []Node, edges []Edge) (PathMeasure, error) {
	size := make(map[string]float64, len(nodes))
	deps := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		size[n.ID] = nodeSize(n)
		deps[n.ID] = nil
	}
	for _, e := range edges {
		if _, ok := size[e.Task]; !ok {
			continue
		}
		if _, ok := size[e.Needs]; !ok {
			continue
		}
		deps[e.Task] = append(deps[e.Task], e.Needs)
	}

	const (
		white = 0 // not visited
		grey  = 1 // on the stack
		black = 2 // finished
	)
	colour := make(map[string]int, len(nodes))
	memo := make(map[string]PathMeasure, len(nodes))
	var walk func(string) (PathMeasure, error)
	walk = func(id string) (PathMeasure, error) {
		if got, ok := memo[id]; ok {
			return got, nil
		}
		colour[id] = grey
		best := PathMeasure{Tasks: 1, Weight: size[id]}
		for _, dep := range sortedStrings(deps[id]) {
			if colour[dep] == grey {
				return PathMeasure{}, refuse(path, "the graph's recorded breaks restore a cycle through %q; the before graph has no critical path to measure, and every ready and blocked answer is a function of a graph this read cannot walk", dep)
			}
			got, err := walk(dep)
			if err != nil {
				return PathMeasure{}, err
			}
			if got.Tasks+1 > best.Tasks {
				best.Tasks = got.Tasks + 1
			}
			if got.Weight+size[id] > best.Weight {
				best.Weight = got.Weight + size[id]
			}
		}
		colour[id] = black
		memo[id] = best
		return best, nil
	}
	out := PathMeasure{}
	for _, n := range nodes {
		got, err := walk(n.ID)
		if err != nil {
			return PathMeasure{}, err
		}
		if got.Tasks > out.Tasks {
			out.Tasks = got.Tasks
		}
		if got.Weight > out.Weight {
			out.Weight = got.Weight
		}
	}
	return out, nil
}

// nodeSize is a task's weight: the size the planner wrote, or one when it wrote
// none. A size of zero or less is refused by Normalize, so the fallback here is
// never a task weighed as nothing.
func nodeSize(n Node) float64 {
	if n.Size == nil {
		return 1
	}
	return *n.Size
}

// stubsPresent is, per contract node, the stub paths that still exist under the
// checkout the graph was read in. It is the half of D1815 a command can check:
// the contract names the whole files it leaves behind, the integration task
// deletes them, and what remains is reported here.
//
// A contract whose stubs are all gone carries no entry, and the read prints no
// key for it: the node's own `stubs` list says it had some, so an absent
// `stubs_present` beside a non-empty `stubs` is "none remain" rather than "the
// CLI did not say".
func stubsPresent(path string, doc Document) (map[string][]string, error) {
	contracts := make([]Node, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if kindOf(n) == KindContract && len(n.Stubs) > 0 {
			contracts = append(contracts, n)
		}
	}
	if len(contracts) == 0 {
		return nil, nil
	}
	root, err := stubsRoot(path)
	if err != nil {
		return nil, err
	}
	tree, err := os.OpenRoot(root)
	if err != nil {
		return nil, refuse(path, "the graph names stubs and the checkout at %s could not be opened: %v", root, err)
	}
	defer tree.Close()

	present := make(map[string][]string, len(contracts))
	for _, n := range contracts {
		var there []string
		for _, stub := range n.Stubs {
			info, err := tree.Stat(stub)
			// Anything the root will not answer for is not a file of this
			// checkout: a path that is not there, and a path a symlink — its
			// last element or a directory above it — would have to leave the
			// checkout to reach (F9). Reporting either as present would say the
			// integration task still has work it does not have.
			if err != nil || info.IsDir() {
				continue
			}
			there = append(there, stub)
		}
		if len(there) > 0 {
			present[n.ID] = there
		}
	}
	return present, nil
}

// stubsRoot is the checkout the stubs are read in: the git top level of the
// working directory, which is also the top level of the graph artifact when the
// artifact is where a plan is read.
//
// A graph that names stubs and was read from outside that checkout is refused,
// and the alternative is the reason: the same stub path exists in every
// checkout of the repository, so a read that resolved it against whatever tree
// it happened to stand in would report "this stub is gone" about a repository
// the plan does not describe. A working directory in no checkout at all has no
// tree to read them in either, and is refused the same way.
func stubsRoot(path string) (string, error) {
	root := checkoutRoot(".")
	if root == "" {
		return "", refuse(path, "the graph names stubs and the working directory is in no git checkout; a stub is a file of the checkout its plan is read in, and there is none here to report against")
	}
	artifact := root
	if strings.TrimSpace(path) != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", refuse(path, "the graph names stubs and its own path could not be resolved: %v", err)
		}
		artifact = abs
	}
	if artifact != root && !strings.HasPrefix(artifact, root+string(filepath.Separator)) {
		return "", refuse(path, "the graph names stubs and %s is outside the working directory's checkout at %s; a stub is a file of the checkout its plan is read in, and one read from another tree is refused rather than reported against the wrong one", path, root)
	}
	return root, nil
}

// checkoutRoot is the git top level above dir, or "" when dir is in no
// checkout. A `.git` entry is the whole test: a directory in an ordinary
// checkout and a file in one that was created by `git worktree`, and both are a
// working tree this read can resolve a stub against.
func checkoutRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}
