package runner

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// discoveredFile is what a run's agent writes what it found out about the
// order of the work in while it worked
// (docs/orchestrator-graph-estimates/implementation-plan.md E2, D2709): the
// items its own work blocks, which must wait for it, and the items it found it
// needed first. The brief names the path (prompts.go), the run reads it as it
// ends (recordend.go), and the daemon that owns the graph folds what it says
// in, add-only (E3).
//
// It lives in the runner's own state directory, which bootstrap keeps outside
// the checkout (stateDirOutsideCheckout), and the reader opens that one path
// and no other: a file of this name inside the checkout, which a base branch
// could plant and finalize would then commit, is never read. The state
// directory's own file is cleared with the run's other files at bootstrap
// (clearStateDir), so an execution that reuses a directory does not inherit
// what an earlier one's agent wrote.
const discoveredFile = "discovered.json"

// maxDiscoveredFileBytes bounds how much of the agent's file is read. What the
// record can carry is at most runstore.MaxDiscoveredItems entries of
// runstore.MaxDiscoveredText runes in each list — a few KiB — so a file this
// size is already far more than any block the brief asks for, and everything
// past the cut is not read at all: the object is decoded from the first
// maxDiscoveredFileBytes bytes, and an object the cut lands in the middle of
// fails to decode and is dropped like any other file no reader can use.
const maxDiscoveredFileBytes = 64 << 10

// copyDiscovered reads what the run's agent wrote about the graph and puts it
// on the record, bounded and redacted as everything the run publishes is.
//
// It is called as the run ends rather than at a stage boundary: the record is
// what survives the run, and the agent may write the file at any point of the
// implement or fix stage — including after the last boundary a stage would
// have read it at. A run that wrote none, or wrote one no reader can use,
// records the block it always had, and nothing here can fail a run (D2709).
func (r *run) copyDiscovered() {
	if d := r.readDiscovered(); d != nil {
		r.rec.Discovered = d
	}
}

// readDiscovered reads the block the agent wrote, or answers nil when there is
// none to read. A file that is absent is the ordinary case — the run found out
// nothing about the order of the work — and says nothing; one that is there
// but unreadable, not a regular file, or not the object the brief asked for is
// dropped with one log line, because it is an agent's own note about the graph
// and a run is never failed over one.
//
// The shape is the record's own block, and it is decoded strictly: a file with
// a key the brief never named is not the object the brief asked for, and
// reading it as a nearly-right one would silently drop whatever it meant.
func (r *run) readDiscovered() *runstore.Discovered {
	if r.d.StateDir == "" {
		return nil
	}
	f, err := openNoFollow(filepath.Join(r.d.StateDir, discoveredFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.d.Log.Warn("reading "+discoveredFile+" failed", "err", r.redact(err.Error()))
		}
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		r.d.Log.Warn(discoveredFile + " is not a regular file; ignoring it")
		return nil
	}
	var block runstore.Discovered
	dec := json.NewDecoder(io.LimitReader(f, maxDiscoveredFileBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&block); err != nil {
		r.d.Log.Warn(discoveredFile+" is not the JSON object the brief asks for; ignoring it", "err", r.redact(err.Error()))
		return nil
	}
	// One object and nothing after it: a second one is not the file the brief
	// asked for either, and reading the first of two would be a guess.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		r.d.Log.Warn(discoveredFile + " carries more than the one JSON object; ignoring it")
		return nil
	}
	red := r.redactor()
	return runstore.NewDiscovered(redactedItems(block.Blocks, red), redactedItems(block.Needed, red))
}

// redactedItems is items with every text redacted: an agent wrote them, and
// the record they are copied into is published whole, like the agent's pull
// request text and its account of a follow-up.
func redactedItems(items []runstore.DiscoveredItem, red func(string) string) []runstore.DiscoveredItem {
	out := make([]runstore.DiscoveredItem, len(items))
	for i, it := range items {
		out[i] = runstore.DiscoveredItem{
			PlanTask: red(it.PlanTask),
			Path:     red(it.Path),
			Reason:   red(it.Reason),
		}
	}
	return out
}

// noteConflictPaths records the paths git stopped a rebase of this run on
// (D2710): two tasks' work meeting on one path is the sign the graph they were
// launched from was missing an edge between them, and the daemon reads these
// beside the footprints of the items merged since the attempt began.
//
// Each path once, in the order the run first met it: a rebase that stops on
// the same path at every commit is one fact about the graph, and the record's
// bound is spent on distinct paths rather than on repeats.
func (r *run) noteConflictPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	met := slices.Clone(r.rec.ConflictPaths)
	seen := make(map[string]bool, len(met))
	for _, p := range met {
		seen[p] = true
	}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		met = append(met, p)
	}
	r.rec.ConflictPaths = runstore.NewConflictPaths(met)
}
