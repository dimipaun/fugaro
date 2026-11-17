package runstore

// What a run reports about the graph it found while it worked
// (docs/orchestrator-graph-estimates/implementation-plan.md X2, D2709): the
// items its own work blocks, the items it found it needed, and the paths a
// rebase or a push met a conflict on. The daemon that owns the graph folds
// them into it add-only (E3), and this file is the shape and the bounds; the
// file the run's agent writes them in, and the reader that copies it onto the
// record, are the runner's (E2).
//
// Every entry is the agent's own words, written by the run whose work the
// graph is meant to constrain, so the record carries the least that can draw
// an edge and a reader bounds it again: at most MaxDiscoveredItems entries per
// list, each text at MaxDiscoveredText runes, and at most MaxConflictPaths
// paths. A bound here is about the record's size and nothing else — whether an
// entry may become an edge (a cycle, MaxDependsOn, a node over its cap) is the
// folding daemon's decision, never the run's claim.
const (
	// MaxDiscoveredItems is how many entries each of a run's two discovered
	// lists carries. The cap keeps the entries in the order the run named
	// them, which is the run's own priority.
	MaxDiscoveredItems = 16
	// MaxDiscoveredText bounds one entry's plan task, path and reason, in
	// runes: a plan task id and a footprint path are short by construction,
	// and a reason is one line.
	MaxDiscoveredText = 200
	// MaxConflictPaths is how many conflicting paths a run records, in the
	// order it met them.
	MaxConflictPaths = 64
)

// DiscoveredItem is one thing a run found out about the graph: an item its own
// work blocks (that one must wait for this run) or an item this run found it
// needed, named by a plan task or by a path the item's footprint covers, with
// the run's one-line why. Exactly one of PlanTask and Path is set — an entry
// names a task or a path and never both — so a reader that draws an edge knows
// what it is drawing it from, and the block's schema requires the same.
type DiscoveredItem struct {
	// PlanTask is the plan task or backlog item the entry names, by the id
	// its plan graph gives it. Absent when the entry names a path.
	PlanTask string `json:"plan_task,omitempty"`
	// Path is the footprint path the entry names. Absent when the entry
	// names a plan task, and clipped to MaxDiscoveredText runes otherwise.
	Path string `json:"path,omitempty"`
	// Reason is the run's own one-line why, clipped to MaxDiscoveredText
	// runes and untrusted text: a reader shows it as data.
	Reason string `json:"reason"`
}

// Discovered is what a run reported about the graph while it worked. Blocks is
// the items that must wait for this run's work; Needed is the items this run
// found it needed, which the daemon records as graph suspects rather than
// reading as an order (D2709: an entry cannot reorder a run already going).
//
// Neither list is ever null: an empty array is a fact ("this run blocked
// nothing") and a null is not.
type Discovered struct {
	Blocks []DiscoveredItem `json:"blocks"`
	Needed []DiscoveredItem `json:"needed"`
}

// NewDiscovered bounds blocks and needed and returns the block to record, or
// nil when neither list holds an entry a reader could draw an edge from. A run
// that discovered nothing writes the record it always wrote (F21), so an old
// reader sees the record it always saw and the format's version stays 1.
func NewDiscovered(blocks, needed []DiscoveredItem) *Discovered {
	d := &Discovered{Blocks: boundDiscovered(blocks), Needed: boundDiscovered(needed)}
	if len(d.Blocks) == 0 && len(d.Needed) == 0 {
		return nil
	}
	return d
}

// Bounded is d as a reader may show it. The record is an object the job's
// service account can write, so neither list is trusted to be the bounded,
// honest one NewDiscovered wrote: what comes back is bounded again — clipped,
// emptied of entries no reader can place, and capped. nil stays nil: a record
// with no block is "no record", never an empty one.
func (d *Discovered) Bounded() *Discovered {
	if d == nil {
		return nil
	}
	return NewDiscovered(d.Blocks, d.Needed)
}

// boundDiscovered bounds one list of entries and returns what is kept. An
// entry that names no item, names two, or carries no why is one no reader can
// place or account for, so it is dropped rather than carried: the block is
// written from an agent's file, and the record it lands in is one every reader
// parses.
func boundDiscovered(items []DiscoveredItem) []DiscoveredItem {
	out := make([]DiscoveredItem, 0, min(len(items), MaxDiscoveredItems))
	for _, it := range items {
		if len(out) >= MaxDiscoveredItems {
			break
		}
		it.PlanTask = clipRunes(it.PlanTask, MaxDiscoveredText)
		it.Path = clipRunes(it.Path, MaxDiscoveredText)
		it.Reason = clipRunes(it.Reason, MaxDiscoveredText)
		if it.Reason == "" || (it.PlanTask == "") == (it.Path == "") {
			continue
		}
		out = append(out, it)
	}
	return out
}

// NewConflictPaths bounds paths and returns the list to record: the paths a
// rebase or a push of this run met a conflict on, at most MaxConflictPaths of
// them in the order it met them, each clipped to MaxDiscoveredText runes. An
// empty path is not a path, and a list that comes to nothing is nil rather
// than empty, so a run that met no conflict writes the record it always wrote.
func NewConflictPaths(paths []string) []string {
	out := make([]string, 0, min(len(paths), MaxConflictPaths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if len(out) >= MaxConflictPaths {
			break
		}
		out = append(out, clipRunes(p, MaxDiscoveredText))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// BoundedConflictPaths is paths as a reader may show them, bounded again on
// read for the reason Discovered.Bounded is: a record's list is re-bounded
// rather than trusted, and a list that comes to nothing reads as nil.
func BoundedConflictPaths(paths []string) []string { return NewConflictPaths(paths) }
