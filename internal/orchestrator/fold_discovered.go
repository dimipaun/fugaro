// The fold of what a finished run reported about the graph (E3, D2709, D2710).
//
// X2 gives the run record two things a run learned while it worked: the items
// its own work blocks and the ones it found it needed, each named by a plan
// task or by a path an item's footprint covers, and the paths a rebase or a
// push met a conflict on. X1 gives the state the name an item stands for a plan
// task under. This file is the daemon's half: when an item's run ends, what the
// run reported about the order of the work becomes edges and facts in the node
// the run belongs to, in the same patch as the outcome that ends it.
//
// Three rules bound the whole of it, and they are the security claim rather
// than an implementation note.
//
// **Add-only.** An edge is added in front of an item and never removed, a phase
// is never touched, and an item another attempt is going for is left alone: the
// run's own words are a claim about a graph, and nothing a run reports may
// reorder work already going or undo a person's decision (D2709).
//
// **Every addition goes through the fence.** An entry that names an item this
// node does not carry, an item that has launched, or an item of another node
// changes nothing at all, and every op the fold asks for is put through Apply
// on the state the ops accepted so far have made — so an addition that would
// make a cycle, pass MaxDependsOn or put the node over its cap is refused by
// the same reading the daemon's own commit makes.
//
// **Nothing here fails a tick.** A refused addition leaves one line in the
// audit log and the fold of the run's outcome rides on: a cycle or an oversized
// node is not a reason to stop folding the run that ended, and ValidateState
// checks no cycle (F42), so a state carrying one would otherwise be a node
// every tick refuses to read again.
//
// What the fold writes is the state's own: an edge the daemon adds un-approves
// the item it is added to, because `depends_on` is digested and the person
// approved a spec the daemon has just made another one — D2740's answer, with
// the fact beside it saying which item, which edge and which run.

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Footprints is what the items of one node are expected to touch, keyed by the
// item's own id: the plan graph's own footprint for the task the item stands
// for (plan.Footprint), which is what D2709's "by `plan_task` or a path its
// footprint names" is read with.
//
// It is an input and not something this package could read for itself. A
// footprint is written into the plan's own graph in the operator's checkout
// (plan.ReadFile), it is a prediction about a tree the daemon does not have
// (F33), and no node of the orchestrator subtree carries one: PlanImportNode is
// the id, the title and the tier of a task and nothing else. A daemon handed
// none — which is every daemon of this release — resolves the plan-task half of
// D2709 and no entry that names a path, and the wiring that reads an operator's
// graph and hands the footprints here is the daemon's own.
type Footprints map[string]plan.Footprint

// The closed codes one refused addition leaves in its audit line. They are
// codes and never a sentence about a node, which is the rule O3's log keeps
// everywhere (tick.go's triage codes are the same shape): a reader who finds
// one goes and reads the node.
const (
	// codeGraphCycle is an edge that would close a loop of dependencies.
	codeGraphCycle = "cycle"
	// codeGraphDeps is an item that already carries MaxDependsOn of them.
	codeGraphDeps = "deps"
	// codeGraphCap is an addition the node's own cap refuses: the state it would
	// make is one this release would not write.
	codeGraphCap = "cap"
	// codeGraphRefused is an addition the fence refused for no reason the fold
	// names — a state the validator reads as malformed, which is a bug worth a
	// line rather than a tick that stops.
	codeGraphRefused = "refused"
	// codeGraphRoom is an entry no op of the patch was left for, or an edge the
	// patch has no room for the fact beside (D2740): the fold of the outcomes is
	// what must land, and this release's MaxIntents is what makes the case all
	// but unreachable.
	codeGraphRoom = "no-room"
)

// discoveredTransition is the transition of the line a refused addition leaves,
// under the item whose declaration the edge would have changed — or the run, for
// a fact. It is a name (audit.go) and its own, so a reader finds the fold's
// refusals without reading the triage's or the ladder's lines.
const discoveredTransition = "graph-refused"

// The two fact families the fold writes. Both are the documented ones: state.go
// explains what `graph_suspect` is for, and D2740 asks for one fact per added
// edge beside it.
//
// A suspect key carries the run and an edge key carries a digest of the item.
// The two differ for a reason and not for taste: a run id is twenty runes by its
// own shape (task.ValidRunID) and a key built from one is a key this document
// carries, while an item id may already be as long as a name is allowed to be,
// so a prefix and an id whole would be a fact key validate.go refuses — and a
// fact nothing can write is a fact nobody reads (mergeFactKey makes the same
// answer for a repository).
const (
	graphSuspectPrefix = "graph_suspect_"
	graphEdgePrefix    = "graph_edge_"
)

// discoveredFold is what one node's fold of the reported graph came to: the ops
// that land the additions, in the order they were accepted, and the lines the
// audit log keeps of the ones that were refused.
type discoveredFold struct {
	ops   []Op
	lines []Entry
}

// folder is the fold as it is being made: the state the ops accepted so far
// have made, what is left of the patch's ops, and what the fold will answer.
type folder struct {
	state State
	feet  Footprints
	room  int
	now   time.Time
	out   discoveredFold
}

// foldDiscovered reads what the finished runs behind one node's intents
// reported about the graph and answers the ops that fold it in, add-only.
//
// room is how many ops the patch that carries them has left: the fold of the
// outcomes is in the same one (recovery and the breaker read the state, not the
// graph), and a run whose entries found no room is a run whose entries are
// never folded again — its attempt reports in that same patch — which is why
// the discovered ops are the ones that wait and the reason MaxIntents and MaxOps
// being equal matters.
func foldDiscovered(s State, reports []RunReport, feet Footprints, room int, now time.Time) discoveredFold {
	f := &folder{state: s, feet: feet, room: room, now: now}
	if OverCap(s) {
		// Nothing this fold asks for can land on a node the validator already
		// refuses, and a line per addition would be the noise such a node least
		// affords: X4 leaves an over-cap node's own fold to the caller, which
		// skips it (foldNode).
		return f.out
	}
	byRun := make(map[string]RunReport, len(reports))
	for _, r := range reports {
		byRun[r.RunID] = r
	}
	for _, it := range s.Intents {
		if it.Outcome != "" {
			continue
		}
		w, ok := s.Item(IntentItem(it.ID))
		if !ok {
			// The item is gone and its attempt's outcome is not this fold's to
			// make either (foldOps leaves it to retention): there is nothing to
			// add an edge in front of.
			continue
		}
		r, ok := byRun[it.RunID]
		if !ok || !r.Finished {
			continue
		}
		f.run(w, it, r)
	}
	return f.out
}

// run folds one finished run's report into one node's items: the edges its
// `blocks` entries ask for, the edges its conflicts ask for, and the one fact it
// leaves about the graph.
func (f *folder) run(w WorkItem, it Intent, r RunReport) {
	d := r.Discovered.Bounded()
	paths := runstore.BoundedConflictPaths(r.ConflictPaths)
	if d != nil {
		for _, entry := range d.Blocks {
			f.block(w, entry, it.RunID)
		}
	}
	// The conflicts are read after the blocks, so the fact below names the item
	// as the fold last left it: the candidates are read from the node as the
	// fold found it, and the edges are added after them.
	met := f.conflicts(w, it, paths)
	f.conflictEdges(w, it.RunID, met)
	f.suspect(w, it.RunID, d, met)
}

// block adds the edge one `blocks` entry asks for: an item this run's work
// blocks must wait for the run's own item (D2709's direction, fixed after
// review). An item that has already launched is not one the graph may be
// changed under, and an edge that is already there is left as it stands: what
// this fold does is add and never remove.
func (f *folder) block(w WorkItem, entry runstore.DiscoveredItem, runID string) {
	t, ok := discoveredTarget(f.state, w, entry, f.feet)
	if !ok {
		// The entry names nothing this node carries: another node's item (F44),
		// a path no footprint here covers, or nothing at all. A claim about a
		// graph this node does not hold changes nothing and is not a refusal of
		// anything the daemon decided.
		return
	}
	if planItemLaunched(f.state, t) || slices.Contains(t.DependsOn, w.ID) {
		return
	}
	f.addEdge(t.ID, w.ID, runID)
}

// conflict is one path a run met a conflict on and the item of this node whose
// footprint names it.
type conflict struct {
	path   string
	merged string
}

// conflicts is the items this run's conflict paths are about (D2710): the paths
// the run met, matched against the footprint of every item of this node that
// merged since the attempt began. The run records paths and never an item — it
// cannot know which pull request it met — so the matching is what says which
// item the graph was missing an edge to, and a path no footprint names is a
// suspicion nobody can place.
func (f *folder) conflicts(w WorkItem, it Intent, paths []string) []conflict {
	var out []conflict
	for _, m := range f.state.WorkItems {
		if m.ID == w.ID || m.Phase != PhaseDone || !mergedSince(f.state, m, it.At) {
			continue
		}
		if path, ok := conflictingPath(f.feet[m.ID], paths); ok {
			out = append(out, conflict{path: path, merged: m.ID})
		}
	}
	return out
}

// conflictEdges adds the edges a run's conflicts ask for: the run's own item
// waits for each item it conflicted with, which is the order the graph did not
// have (D2710).
//
// The edge is added here, before the item's next attempt, and never while an
// attempt is going: the attempt this report answers for has ended — its item is
// the one the fold is about — and an item another attempt was minted for, or
// whose work is over, is one no next attempt of which this edge is in front of.
func (f *folder) conflictEdges(w WorkItem, runID string, met []conflict) {
	if len(met) == 0 || attemptGoing(f.state, w, runID) {
		return
	}
	for _, c := range met {
		x, ok := f.state.Item(w.ID)
		if !ok || x.Phase.Done() || slices.Contains(x.DependsOn, c.merged) {
			return
		}
		f.addEdge(w.ID, c.merged, runID)
	}
}

// suspect records the one fact a run leaves when what it reported is a
// dependency the graph did not have (D2709's correction after review round 2:
// at most one, however many entries a run carries, because the fact is the note
// that the graph was wrong and not a list of everything the run said).
//
// It is written whether or not the edges landed, and the edges are what makes
// it worth reading either way: an edge the fence refused is exactly the case the
// person needs to see. What it does not carry is the run's own reasons — a fact
// is one bounded line, and the reason belongs to the entry a reader can find in
// the run's record.
func (f *folder) suspect(w WorkItem, runID string, d *runstore.Discovered, met []conflict) {
	var needed []string
	if d != nil {
		for _, entry := range d.Needed {
			needed = append(needed, entryName(entry))
		}
	}
	if len(needed) == 0 && len(met) == 0 {
		return
	}
	key := graphSuspectPrefix + runID
	value := suspectValue(runID, w.ID, needed, met)
	what := "what run " + runID + " reported about the graph was not recorded"
	f.ask(Op{Op: OpFactSet, Fact: &FactOp{Key: key, Value: value}}, runID, what, "")
}

// addEdge adds one edge in front of one item, or leaves the line for the reason
// it was not added. The item's own dependencies are read from the state the fold
// has made so far and not from the node as it was read: two runs' reports are
// folded in one patch, and the second addition to one item must be read against
// the first.
func (f *folder) addEdge(targetID, source, runID string) {
	t, ok := f.state.Item(targetID)
	if !ok {
		return
	}
	what := "the dependency " + source + " -> " + targetID + " from run " + runID + " was not added"
	code := ""
	switch {
	case reaches(f.state, source, targetID):
		// The whole of the cycle check, and the one thing ValidateState does not
		// do (F42): a state carrying a loop would be a node every reader refuses
		// again, so it is refused here, once, with a line.
		code = codeGraphCycle
	case len(t.DependsOn) >= MaxDependsOn:
		code = codeGraphDeps
	}
	deps := append(slices.Clone(t.DependsOn), source)
	if code == "" && f.room < 2 {
		// An edge and the fact beside it are one change (D2740), and a patch
		// with room for only one of them adds neither: an item left un-approved
		// with no note on the node saying why is the thing that decision exists
		// to prevent, and the entry the cycle could not record is one the next
		// tick derives again from the same report.
		code = codeGraphRoom
	}
	if !f.ask(Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: targetID, DependsOn: &deps}}, targetID, what, code) {
		return
	}
	// D2740's fact, beside the edge that un-approved the item: the person who
	// approved the spec reads on the node which item changed, which edge was
	// added and which run caused it, rather than having to guess why the item
	// they approved is waiting for them again. It is written only for an edge
	// that landed — a fact claiming an edge the fence refused would be a lie
	// about the node.
	f.ask(Op{Op: OpFactSet, Fact: &FactOp{Key: edgeFactKey(targetID), Value: edgeFactValue(targetID, source, runID)}},
		targetID, "the note beside the dependency "+source+" -> "+targetID+" was not written", "")
}

// ask is the fence, or the line for a refusal already known, and whether the op
// was accepted. code is "" for an op this fold has not refused yet: then the
// answer is Apply's, on the state the ops accepted so far have made, which is
// the same reading the commit that lands them makes.
func (f *folder) ask(op Op, subject, what, code string) bool {
	if code == "" && f.room <= 0 {
		code = codeGraphRoom
	}
	if code == "" {
		next, err := Apply(f.state, Patch{BaseRev: f.state.Rev, Ops: []Op{op}}, Actor{Class: Daemon, Now: f.now})
		if err == nil {
			f.state = next
			f.out.ops = append(f.out.ops, op)
			f.room--
			return true
		}
		code = opRefusal(err)
	}
	f.out.lines = append(f.out.lines, Entry{
		Subject:    subject,
		Transition: discoveredTransition,
		Text:       "graph: " + what + " (" + code + ")",
	})
	return false
}

// auditFold writes the lines the fold's refusals left, once the patch carrying
// the rest of the fold has landed: a refusal stands whether or not the additions
// beside it landed, and the line is evidence of it rather than its fence
// (audit.go).
//
// A log this package cannot write fails the cycle with its error, which is what
// every other writer of this log does (auditRefusal, triaged, judge). What it
// does not do is change a decision: the additions the fold did not make were not
// made, and the next tick derives the same refusals from the node again.
func (c *cycle) auditFold(ctx context.Context, lines []Entry) error {
	if c.tick.Audit == nil {
		return nil
	}
	for _, e := range lines {
		if _, err := c.tick.Audit.Append(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// opRefusal is the closed code of an op the fence refused: the node's own caps
// (the size target, the fact bound, the item bound) are ErrTooLarge, and
// everything else is a state or a patch this release would not write, which is a
// line worth keeping rather than a tick that stops.
func opRefusal(err error) string {
	if errors.Is(err, ErrTooLarge) {
		return codeGraphCap
	}
	return codeGraphRefused
}

// discoveredTarget is the item of this node one entry names, and false when the
// entry names nothing here.
//
// A dependency never crosses nodes (F44, shard.go), so an entry is read against
// the node the run's own item is in and against nothing else: for a run of a
// plan, that plan's own node; for a run with no plan at all, the items of that
// node whose repository is the run's own — D2709's correction after review round
// 2, which is what keeps one path like `go.mod` from reaching every item of a
// project.
func discoveredTarget(s State, of WorkItem, entry runstore.DiscoveredItem, feet Footprints) (WorkItem, bool) {
	backlog := of.PlanTask == ""
	for _, w := range s.WorkItems {
		if w.ID == of.ID {
			continue
		}
		if backlog && w.Repo != of.Repo {
			continue
		}
		if namesItem(entry, w, feet) {
			return w, true
		}
	}
	return WorkItem{}, false
}

// namesItem reports whether one entry names one item: by the plan task the two
// carry, or by a path the item's footprint covers. An entry names a task or a
// path and never both (runstore.DiscoveredItem) and an item that stands for no
// plan task is named by no plan task, so the two readings cannot disagree about
// one entry.
//
// The plan task is compared and never parsed: the item's own is shape-checked on
// the way into the node (validate.go, task.PlanTaskProblem) and an entry that
// spells it another way names another task, which is a match that does not
// happen rather than a name this fold folds.
func namesItem(entry runstore.DiscoveredItem, w WorkItem, feet Footprints) bool {
	if entry.PlanTask != "" {
		return w.PlanTask != "" && w.PlanTask == entry.PlanTask
	}
	return entry.Path != "" && feet[w.ID].Matches(entry.Path)
}

// conflictingPath is the first path of a run's list an item's footprint names,
// and false when the footprint covers none of them. The paths are in the order
// the run met them (runstore.NewConflictPaths), so the one the fact and the line
// name is the first one the run stopped on and not whichever a map happened to
// give.
func conflictingPath(f plan.Footprint, paths []string) (string, bool) {
	for _, p := range paths {
		if f.Matches(p) {
			return p, true
		}
	}
	return "", false
}

// mergedSince reports whether an item's merge came after the attempt began: the
// run met a conflict on it, so the item merged while the attempt was going, and
// one that had already merged when the attempt began is an edge the graph should
// have carried — the run's report says nothing about it.
//
// The item's own newest attempt's stamp is what answers the question, and it is
// the only thing on the node that can: no merge time is recorded anywhere and
// the attempt that made the pull request is the newest thing this document
// knows about the item (G3's merge read writes the phase and nothing else). An
// item no attempt of was ever recorded — one a person moved to done — is not one
// this fold draws an edge to.
func mergedSince(s State, m WorkItem, since time.Time) bool {
	it, ok := newestAttempt(s, m.ID)
	return ok && !it.At.Before(since)
}

// attemptGoing reports whether an item has an attempt going that this report is
// not about: the item's newest minted run is another one, or one of its intents
// has not reported and belongs to another run. Such an item is left alone,
// because what an added edge changes is the declaration the attempt in flight
// was launched under.
func attemptGoing(s State, w WorkItem, runID string) bool {
	if w.RunID != "" && w.RunID != runID {
		return true
	}
	for _, it := range s.Intents {
		if IntentItem(it.ID) == w.ID && it.Outcome == "" && it.RunID != runID {
			return true
		}
	}
	return false
}

// reaches reports whether from is target, or depends on it directly or through
// other items. An edge is only ever added from the run's item to the item that
// must wait for it, so a cycle is exactly the case where the far end already
// reaches back.
func reaches(s State, from, target string) bool {
	if from == target {
		return true
	}
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		w, ok := s.Item(queue[0])
		queue = queue[1:]
		if !ok {
			continue
		}
		for _, dep := range w.DependsOn {
			if dep == target {
				return true
			}
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return false
}

// edgeFactKey is the fact key of one item whose dependencies the daemon added
// (D2740): the item, under a prefix that says which fact family it is.
func edgeFactKey(itemID string) string {
	sum := sha256.Sum256([]byte(itemID))
	return graphEdgePrefix + hex.EncodeToString(sum[:8])
}

// edgeFactValue is what D2740 asks the fact to name: the item, the edge and the
// run that caused it, so a person reading the node sees why the item they
// approved is no longer approved — the edge is part of the spec the digest
// covers (patch.go's applyUpsert clears the approval when the digest moves) and
// the item waits for a person's approval again.
func edgeFactValue(targetID, source, runID string) string {
	return "run " + runID + " reported that it blocks " + targetID +
		", and the daemon added the dependency " + source + " -> " + targetID +
		": the item waits for a person's approval again (D2740)"
}

// suspectValue is the line the one fact a run leaves carries: the run, the item
// it was for, and — in the run's own words, clipped — what it needed and what it
// met a conflict on.
func suspectValue(runID, itemID string, needed []string, met []conflict) string {
	clauses := make([]string, 0, 2)
	if len(needed) > 0 {
		clauses = append(clauses, fmt.Sprintf("%d item(s) it needed first: %s", len(needed), nameList(needed)))
	}
	if len(met) > 0 {
		parts := make([]string, 0, len(met))
		for _, c := range met {
			parts = append(parts, safetext.Clip(c.path, maxFactNameRunes)+" (item "+c.merged+")")
		}
		clauses = append(clauses, "a conflict on "+strings.Join(parts, ", ")+", with an item merged since this attempt began")
	}
	return "run " + runID + " (item " + itemID + ") reported what the graph did not have: " + strings.Join(clauses, "; ")
}

// maxFactNameRunes bounds one entry name inside a fact's line. The fact is one
// bounded line (MaxFactValueRunes) carrying up to two of them beside the run's
// own sentence, so a name longer than this would push the rest of the line off
// the end the op clips at.
const maxFactNameRunes = 60

// nameList is a few entry names as one line, with the rest counted rather than
// carried: the names are an agent's own text, and how many there were is the
// part a person reads.
func nameList(names []string) string {
	const most = 2
	shown := names
	if len(shown) > most {
		shown = shown[:most]
	}
	out := make([]string, 0, len(shown)+1)
	for _, n := range shown {
		out = append(out, safetext.Clip(safetext.OneLine(n), maxFactNameRunes))
	}
	if rest := len(names) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf("and %d more", rest))
	}
	return strings.Join(out, ", ")
}

// entryName is one entry as a line names it: the plan task or the path it
// carries, which is exactly one of the two.
func entryName(entry runstore.DiscoveredItem) string {
	if entry.PlanTask != "" {
		return entry.PlanTask
	}
	return entry.Path
}
