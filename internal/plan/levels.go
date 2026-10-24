package plan

import "sort"

// This file is GV1.1: the graph's answers in the shape a plan document is
// written in -- the tasks in waves, one downstream count per task, and the one
// critical path -- as functions over a Document.
//
// They are the read's own answers and not a second opinion about them. The
// answers exist once, in Analyze (V2.2.3), and these functions ask it: two
// consoles reading one graph must not be able to disagree about a level or
// about which chain is critical (D26), and the only way to guarantee that is
// for there to be one walk. A caller holding a document and wanting one of the
// three shapes calls the function rather than walking the edges itself.
//
// Each function takes the document the read takes and refuses what the read
// refuses: a document this build does not read, one that names a node twice or
// that has a cycle in it gets nothing back. Nothing is what a refusal looks
// like in these shapes -- there is no error to return -- and it is deliberately
// distinguishable from the answer an empty graph gives, which is an empty wave
// list, an empty map and an empty chain rather than a null. A cycle has no
// wave, no count and no chain to give, and a screen that rendered one would be
// inventing them (C7).

// Levels is the plan's tasks in waves: wave 0 is every task that waits on
// nothing, wave n every task whose deepest dependency is at wave n-1. Each wave
// is in id order, so two readers of one graph draw the same plan and no
// renderer sorts a wave itself.
//
// It is nil for a document the read refuses, and an empty wave list for a graph
// with no tasks.
func Levels(doc Document) [][]string {
	g, ok := readAnswers(doc)
	if !ok {
		return nil
	}
	waves := make([][]string, g.Levels)
	for _, n := range g.Nodes {
		waves[n.Level] = append(waves[n.Level], n.ID)
	}
	for _, wave := range waves {
		sort.Strings(wave)
	}
	return waves
}

// Downstream is how many tasks wait on each task, directly or through others --
// the raw count D26 keeps beside the critical path as the cheap fallback for a
// reader that cannot weigh a footprint. It is a different question from the
// critical path: six tasks waiting on one are six, and six tasks waiting one
// after another are a chain of seven.
//
// It is nil for a document the read refuses, and an empty map for a graph with
// no tasks.
func Downstream(doc Document) map[string]int {
	g, ok := readAnswers(doc)
	if !ok {
		return nil
	}
	counts := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		counts[n.ID] = n.Downstream
	}
	return counts
}

// CriticalPathTasks is the plan's critical path: the longest chain of tasks, in
// the order they can be done, the smallest id deciding a tie at every step, so
// two readers of one graph name one chain (C8). The chain is counted in tasks
// and not in edges, so a task two others both wait on is one task of the chain.
//
// The name states the shape rather than the answer: this package already has a
// CriticalPath, the read's own object carrying the chain's length beside its
// tasks, and a function cannot carry the name of a type in the same package.
// GV1.1's interface named it `plan.CriticalPath(Document) []string`; this is
// that function, in the only name Go allows it.
//
// It is nil for a document the read refuses, and an empty chain for a graph
// with no tasks.
func CriticalPathTasks(doc Document) []string {
	g, ok := readAnswers(doc)
	if !ok {
		return nil
	}
	return g.CriticalPath.Tasks
}

// readAnswers is the one call the three functions above make: the read's own
// answers over a document, against no done set -- whether a task's work is done
// is a fact about runs and not about the graph, and none of the three answers
// depends on it. ok is false when the read refuses the document, in which case
// the caller answers with nothing.
func readAnswers(doc Document) (Graph, bool) {
	g, err := Analyze("the graph", doc, nil)
	if err != nil {
		return Graph{}, false
	}
	return g, true
}
