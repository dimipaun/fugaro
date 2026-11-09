package plan

import (
	"math"
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
// edge that ran through a removed integration node is rewritten onto what that
// node waits for — a task that waited on the integration node was waiting on
// everything the integration runs, the two sides of the break included, because
// the integration node is the task that runs them together.
//
// A dependency the plan had before the pass is never dropped for want of a
// place to put it, and a wait the document gives nowhere to stand is refused
// rather than measured with one of the plan's own dependencies missing: a gain
// computed without it is a gain the plan did not get.
//
// A task waits on a contract because the pass moved its edge there, and the
// record says where that edge stood before: the entry whose `was` names the
// task, whose restored edge stands in for this one, so a consumer no entry
// names is refused. A task waits on an integration because it waited on what
// the integration runs — every task behind that node, through whatever
// integration nodes it waits on — so an integration with no task behind it is
// refused, and so is one whose wait stands on a contract or a change request,
// which is work no plan before the pass held.
//
// What is dropped is the pass's own scaffolding, each edge removed with the
// node it hangs from: the work a contract, an integration or a change request
// waits for is work the pass added, counted in AddedTasks, and a dependent's
// edge onto a change request (D1812) is a wait raised against a contract the
// pass had already merged — one no plan before the pass could hold.
//
// The rewrite is a product of two sides that both come from the file — every
// edge onto an integration node against every task behind the node it names —
// and finding those tasks is the other half of its cost, one walk of the graph
// under the node per edge that asks. The edges built and the nodes walked are
// both counted, as they are built, against maxBeforeEdges, and the graph is
// refused past it the way the contract-file rule counts its own pairs (F10).
func beforePath(path string, doc Document) (PathMeasure, error) {
	kinds := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		kinds[n.ID] = kindOf(n)
	}
	// Which tasks the record moved onto each contract, and what each
	// integration node runs, both read in the document's own order so the
	// before graph below is a function of the file rather than of a map walk.
	consumers := map[string]map[string]bool{}
	for _, d := range doc.Decoupling {
		if consumers[d.Contract] == nil {
			consumers[d.Contract] = map[string]bool{}
		}
		consumers[d.Contract][d.Was.Task] = true
	}
	deps := map[string][]string{}
	for _, e := range doc.Edges {
		deps[e.Task] = append(deps[e.Task], e.Needs)
	}
	// What an integration node runs: the tasks it waits for itself, and through
	// every integration node it waits on, theirs — a task that waited on an
	// integration waited on the whole of the work behind it, however many hops
	// away, so one hop of the graph is not the answer. The walk is from the
	// entry, each node under it reached once, so a diamond in a chain is one
	// walk rather than one per path and a cycle could not spin it; it is not
	// kept between waits, because a document can state a chain of integrations
	// whose work grows with the chain, and holding every one of them would be
	// the product of two artifact-sized figures that the bound below exists to
	// refuse to build. Not keeping it makes the walk the other half of the
	// rewrite's cost — one walk per edge that asks — so the nodes walked are
	// counted against that same bound, below.
	//
	// A node of the pass that is not an integration, behind one, is work the
	// before graph has no task to put it on: what a contract or a change request
	// stands for is the pass's own, and no plan before the pass held it.
	walked := 0
	runsOf := func(id string) ([]string, error) {
		seen := map[string]bool{id: true}
		queue := []string{id}
		var ran []string
		for len(queue) > 0 {
			walked++
			if walked > maxBeforeEdges {
				return nil, refuse(path, "the graph's edges onto integration nodes ask for more than %d nodes walked behind them; the before graph finds the tasks an integration node runs by walking the graph under it, and a graph whose rewrite walks further than this is refused rather than read slowly", maxBeforeEdges)
			}
			next := queue[0]
			queue = queue[1:]
			for _, dep := range deps[next] {
				if seen[dep] {
					continue
				}
				switch kinds[dep] {
				case KindTask:
					seen[dep] = true
					ran = append(ran, dep)
				case KindIntegration:
					seen[dep] = true
					queue = append(queue, dep)
				case KindContract, KindContractChange:
					return nil, refuse(path, "the integration node %q waits on the %s %q, and the wait of a task on that integration has to stand on what the integration runs; no task of the plan before the pass stands behind the %s, so the wait is refused rather than dropped",
						next, kinds[dep], dep, kinds[dep])
				}
			}
		}
		return ran, nil
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
			// A task that waited on the integration node waited on what the
			// integration runs, so the wait is put there. An integration node
			// that runs no task at all — one no decoupling entry covers, or one
			// that waits only on integrations that run none either — states
			// nothing the wait can stand on, so the document is refused rather
			// than read with one of the plan's own dependencies left out of it.
			ran, err := runsOf(e.Needs)
			if err != nil {
				return PathMeasure{}, err
			}
			if len(ran) == 0 {
				return PathMeasure{}, refuse(path, "the graph has %s waiting on the integration node %q, and that node waits on no task; the before graph rewrites a wait on an integration onto the tasks the integration runs, and a wait with none to stand on is refused rather than dropped",
					e.Task, e.Needs)
			}
			for _, dep := range ran {
				edges = append(edges, Edge{Task: e.Task, Needs: dep, Reason: e.Reason})
			}
		case kinds[e.Task] == KindTask && kinds[e.Needs] == KindContract:
			if !consumers[e.Needs][e.Task] {
				return PathMeasure{}, refuse(path, "the graph has %s waiting on the contract %q and no decoupling entry records an edge from %s for the pass to have moved onto it; the before graph restores a consumer's edge from the record, and one the record does not name has nowhere to stand",
					e.Task, e.Needs, e.Task)
			}
		}
		if len(edges) > maxBeforeEdges {
			return PathMeasure{}, refuse(path, "the graph's edges onto integration nodes ask for more than %d edges in the before graph, one per task each of them runs; a graph that large is refused rather than read slowly", maxBeforeEdges)
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

// maxBeforeEdges bounds the before graph's rewrite. An artifact inside the
// one-megabyte cap can state a product of tens of millions of edges from a few
// thousand decoupling entries and a few hundred edges onto one integration
// node — a 541 KB file of that shape was measured allocating 2.4 GB — and the
// read is local: a console spawns it, so the process a product that size would
// take the memory of is the one the person who asked for a graph is looking at.
// beforePath counts both figures the rewrite has as it builds them — the edges
// it adds and the nodes it walks to find the tasks behind an integration — and
// refuses the graph past the bound. The count covers the walk because the walk
// is where a document can multiply without adding a single edge: a long chain of
// integration nodes under one that a hundred tasks wait on is a hundred walks of
// the chain, and every one of them ends in the same one task. The recorded `was`
// edges restored beside them are not part of that count: they are one per break,
// the artifact's own list rather than a product of two of its figures, and the
// cap the artifact arrived in bounds them already. A plan an operator wrote does
// not come near the bound: the
// before graph is the plan that stood before the pass, so it is the size of a
// plan rather than of a product.
const maxBeforeEdges = 1 << 18

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
	// A size is a positive number and a path is a sum of them, so a path of
	// sizes large enough to be a float's own edge weighs infinity, and infinity
	// is not a figure any caller can print: a field of this shape is marshalled
	// into `fugaro plan graph --json`, and a graph the read accepted would fail
	// there with `json: unsupported value: +Inf` instead of with a sentence
	// about the plan. The sum is known here, so the refusal is made here.
	if math.IsInf(out.Weight, 0) {
		return PathMeasure{}, refuse(path, "the graph's sizes weigh a path past what a number can hold; the before and after paths are reported as figures, and a plan that weighs infinity is refused rather than reported")
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
// graph artifact, which is the tree the plan describes and the one the
// integration task deletes them in.
//
// A graph that names stubs and stands in a checkout of its own is refused
// rather than read against the working directory's, and the alternative is the
// reason: every checkout of a repository has the same stub paths, so a read
// that resolved them against another tree would report "this stub is gone"
// about a repository the plan does not describe. The tree the artifact is in is
// therefore asked for by name rather than assumed from the working directory,
// and the two are required to be one checkout: a checkout nested inside the
// working directory's is a tree of its own, and the parent's answer about a stub
// the plan's own checkout does not carry is exactly the wrong one. The two are
// the same tree when they resolve to it, however they are spelled: one directory
// has more than one name on a machine with symbolic links, and a refusal over
// which of them was written would be a refusal about this machine rather than
// about the plan. A working directory, or an artifact, in no checkout at all has
// no tree to read them in either, and is refused the same way.
func stubsRoot(path string) (string, error) {
	root := checkoutRoot(".")
	if root == "" {
		return "", refuse(path, "the graph names stubs and the working directory is in no git checkout; a stub is a file of the checkout its plan is read in, and there is none here to report against")
	}
	if strings.TrimSpace(path) != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", refuse(path, "the graph names stubs and its own path could not be resolved: %v", err)
		}
		// Two spellings of one directory are one directory: macOS's /var is
		// /private/var and a workspace reached through a symbolic link is the
		// very tree the working directory stands in, so the two roots are
		// compared as trees where both resolve rather than as the strings they
		// were spelled with. A read refused over the spelling would be a
		// refusal about this machine's links rather than about the plan.
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		here := checkoutRoot(filepath.Dir(abs))
		resolvedHere, hereErr := filepath.EvalSymlinks(here)
		if hereErr != nil || rootErr != nil || resolvedHere != resolvedRoot {
			return "", refuse(path, "the graph names stubs and %s is outside the working directory's checkout at %s; a stub is a file of the checkout its plan is read in, and one read from another tree is refused rather than reported against the wrong one — a checkout nested inside this one included", path, root)
		}
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
