package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The label syncer: the one writer of the forge labels (T5, docs/forge-labels).
//
// A tick derives the whole desired set from the node it read — the phase of
// each run an item has and the spec the patch declared — reads what the hosts
// hold once per repository, and writes only the difference (F15, R5). Nothing
// here keeps the desired state: a daemon that restarted has lost nothing but
// the reads its memory saves, because the tick after the restart derives the
// same set and reads a host before it writes anything to it (F10).
//
// No failure here ends a cycle. A host that will not answer, a label it will
// not create and a write it refuses are one line in the log each, and the tick
// goes on: the labels are a view of the work and never the work itself, and a
// daemon that stopped folding and launching over a label would trade the
// record for the display of it.

// LabelSeams is what one cycle keeps the pull request labels through: the host
// of each repository, the pull request behind each run, and the log every
// failure leaves a line in. All three or none — a Tick built with none labels
// nothing, which is the direction a daemon whose providers and service path
// could not be built fails in.
//
// The capability is resolved per repository rather than carried as one
// gitprov.Labeler, because a host is one repository's: the daemon holds a
// provider per repository (internal/cli opens one per repository for the
// triage too), and two repositories' labels are two hosts' and must not be
// written through one.
type LabelSeams struct {
	// Labelers is the label capability of the repository an item is on.
	Labelers LabelerLookup
	// PRs answers the pull request a run opened, which is where the number
	// every label write names comes from: the item carries the run id, not the
	// number (F6, F25).
	PRs LabelPRReader
	// Log is where a failure goes: one line per cause, and nothing else.
	Log LabelLog
	// kept is what this daemon carries between ticks (labelMemory). It is
	// made on first use, because a Tick carries its seams as a value and has
	// nowhere to build one.
	kept *labelMemory
}

// Set reports whether every seam is there.
func (s LabelSeams) Set() bool { return s.Labelers != nil && s.PRs != nil && s.Log != nil }

// LabelerLookup resolves the label capability of one repository. An error is a
// host that could not be built or found — an incomplete provider setting, a
// repository no provider of this daemon serves — and it leaves that
// repository's labels unwritten for the tick while the others are written.
type LabelerLookup interface {
	LabelerFor(ctx context.Context, repo string) (gitprov.Labeler, error)
}

// LabelPRReader answers the pull request number behind a run, by the run id the
// item's own attempt recorded.
//
// ok false is a run that has opened no pull request yet — one still starting,
// or one that ended before it did — which is not a failure and leaves the item
// unlabelled: there is nothing to label. An error is a read that did not
// happen, and the item is left for the next tick, which reads it again. The
// number is fixed once a run has opened one, so a caller caches it per run id
// (F25).
type LabelPRReader interface {
	PRNumber(ctx context.Context, runID string) (int, bool, error)
}

// LabelLog is where the syncer's failures go: one line per cause. It returns
// nothing, because a log that could not be written must not become a second
// way for the labels to end a cycle, and it is a seam rather than an io.Writer
// so that a daemon decides where its own warnings go.
type LabelLog interface {
	Warn(ctx context.Context, line string)
}

// label is one cycle's turn at the labels. Left unset, the seams label nothing
// at all; and a cycle that no longer holds its node labels nothing either, for
// the reason the triage and the ladder act on nothing from one: the phases the
// desired set is derived from are the state this cycle read.
func (c *cycle) label(ctx context.Context) {
	if !c.tick.Labels.Set() || !c.fresh {
		return
	}
	syncLabels(ctx, &c.tick.Labels, c.state)
}

// syncLabels is one tick's whole turn at the labels: the items that have a
// pull request, in the repositories they are on, each repository read once
// (D2434, D2435) and each item's difference written through it.
func syncLabels(ctx context.Context, seams *LabelSeams, s State) {
	mem := seams.memory()
	for _, g := range labelGroups(ctx, seams, mem, s) {
		syncRepo(ctx, seams, mem, g)
	}
}

// labelGroup is one repository's items for one tick: the bulk read is per
// repository, so the items are grouped before anything is read.
type labelGroup struct {
	repo  string
	items []labelItem
}

// labelGroups is the pull requests this tick is responsible for, in the
// repositories they are on: one per run of one item (labelRuns). An item whose
// run has opened no pull request yet is left to the tick after it opens one.
//
// The repositories come in the order the node carries their items, and an
// item's runs in the order the node records their attempts, so a test reads the
// calls back in the order it seeded them. A run the reader cannot answer is one
// line in the log and no item: nothing else about the cycle depends on a pull
// request number.
func labelGroups(ctx context.Context, seams *LabelSeams, mem *labelMemory, s State) []labelGroup {
	var (
		out []labelGroup
		at  = map[string]int{}
	)
	for _, w := range s.WorkItems {
		if w.Repo == "" {
			continue
		}
		for _, r := range labelRuns(s, w) {
			number, ok, err := seams.PRs.PRNumber(ctx, r.runID)
			if err != nil {
				warnLabel(ctx, seams, fmt.Sprintf("the pull request of run %s could not be read: %s",
					r.runID, safetext.OneLine(err.Error())))
				continue
			}
			if !ok {
				continue
			}
			it := labelItem{repo: w.Repo, number: number, runID: r.runID, phase: r.phase, spec: labelSpec(ctx, seams, mem, w)}
			if i, seen := at[w.Repo]; seen {
				out[i].items = append(out[i].items, it)
				continue
			}
			at[w.Repo] = len(out)
			out = append(out, labelGroup{repo: w.Repo, items: []labelItem{it}})
		}
	}
	return out
}

// labelRun is one run of one item whose pull request this tick keeps, and the
// phase its label follows.
type labelRun struct {
	runID string
	phase Phase
}

// labelRuns is every run of one item the tick labels (R8).
//
// The item's run is the attempt in flight, whose label follows the item's own
// phase (R4), and the attempts before it are the item's history. A retry is a
// new run on a new pull request (F21) and the carry writes the item's new run
// id in the same cycle the fold records the failure, so the run it replaced is
// named by nothing else: its pull request was left carrying `fugaro:running`
// when that run was the item's, and a syncer that walked only the item's own
// run id would leave that label on it for good, never setting `fugaro:failed`
// on a failed run's pull request and never clearing it when it closes (R4,
// R8, F16). A ladder's attempt replaced by the next rung is the same case.
//
// Each attempt's label follows its own ending — foldPhase's reading of the
// outcome, which is the same phase the failure moved the item to — so a run
// that ended is labelled as it ended whatever the item has moved on to since.
// An attempt nothing was ever started under — abandoned — has no pull request
// at all and is left out, and a run id no intent of the item names is labelled
// from the item's phase, which is the reading the phase's own mapping takes.
func labelRuns(s State, w WorkItem) []labelRun {
	var out []labelRun
	seen := make(map[string]bool, len(s.Intents)+1)
	for _, it := range s.Intents {
		if IntentItem(it.ID) != w.ID || seen[it.RunID] {
			continue
		}
		seen[it.RunID] = true
		if it.Outcome == OutcomeAbandoned {
			continue
		}
		if it.Outcome == "" {
			out = append(out, labelRun{runID: it.RunID, phase: w.Phase})
			continue
		}
		if phase, ok := foldPhase(it.Outcome); ok {
			out = append(out, labelRun{runID: it.RunID, phase: phase})
		}
	}
	if w.RunID != "" && !seen[w.RunID] {
		out = append(out, labelRun{runID: w.RunID, phase: w.Phase})
	}
	return out
}

// labelSpec is the spec name an item's spec label is built from, and the one
// line a name no label can be built from leaves behind (R3, F7).
//
// The refusal costs the spec label alone and never the item: a pull request
// whose item carries a name this daemon will not write still gets its status
// label, because what is unshapeable is the plan's name and not the work. The
// refusal is remembered so that a name the node carries for as long as the item
// is on it costs one line and not one every tick.
func labelSpec(ctx context.Context, seams *LabelSeams, mem *labelMemory, w WorkItem) string {
	if w.Spec == "" {
		return ""
	}
	if _, ok := specLabel(w.Spec); !ok {
		if !mem.didRefuseShape(w.Repo, w.Spec) {
			mem.markRefusedShape(w.Repo, w.Spec)
			warnLabel(ctx, seams, fmt.Sprintf("the spec of %s is not a name a label can be built from, so its pull request carries no spec label: %s",
				w.ID, safetext.OneLine(w.Spec)))
		}
		return ""
	}
	return w.Spec
}

// syncRepo is one repository's turn: its host, one bulk read of its open pull
// requests, and each item's own difference against that listing (R5).
//
// A host with no labels at all — Bitbucket — is a silent no-op here: no read,
// no write and no line each tick, which is what R7 asks for and what keeps a
// project on such a host from filling the daemon's log with a feature it does
// not have.
func syncRepo(ctx context.Context, seams *LabelSeams, mem *labelMemory, g labelGroup) {
	host, err := seams.Labelers.LabelerFor(ctx, g.repo)
	switch {
	case err != nil:
		warnLabel(ctx, seams, fmt.Sprintf("no host to label %s through: %s", g.repo, safetext.OneLine(err.Error())))
		return
	case host == nil:
		warnLabel(ctx, seams, "no host to label "+g.repo+" through")
		return
	case !host.Supported():
		return
	}
	open, err := host.ListOpenPRLabels(ctx)
	if err != nil {
		warnLabel(ctx, seams, fmt.Sprintf("the open pull requests of %s could not be read: %s",
			g.repo, safetext.OneLine(err.Error())))
		return
	}
	for _, it := range g.items {
		syncItem(ctx, seams, mem, g.repo, host, it, open)
	}
}

// syncItem is one item's difference: what its phase and its spec want against
// what the host reports, with only that difference written (F15, R5).
//
// A pull request the open listing does not carry is not an empty one: it is
// one the host does not report as open — merged, or closed — so it is read by
// number, and a closed one loses both status labels and is remembered, which
// is what keeps that read from happening every tick.
func syncItem(ctx context.Context, seams *LabelSeams, mem *labelMemory, repo string, host gitprov.Labeler, it labelItem, open map[int][]string) {
	have, listed := open[it.number]
	if !listed {
		if mem.isClosed(repo, it.number) {
			return
		}
		state, names, err := host.PRLabels(ctx, it.number)
		if err != nil {
			warnLabel(ctx, seams, fmt.Sprintf("the state of %s#%d could not be read: %s",
				repo, it.number, safetext.OneLine(err.Error())))
			return
		}
		if state != gitprov.PROpen {
			clearClosed(ctx, seams, mem, repo, host, it, names)
			return
		}
		// The listing was a moment behind: the pull request is open, and what
		// this read returned is what it carries.
		have = names
	}
	add, remove := labelDiff(desiredLabels(it), have)
	if len(add) > 0 {
		if add = createLabels(ctx, seams, mem, repo, host, add); len(add) > 0 {
			if err := host.AddLabels(ctx, it.number, it.runID, add); err != nil {
				warnLabel(ctx, seams, fmt.Sprintf("the labels %s could not be added to %s#%d: %s",
					labelNames(add), repo, it.number, safetext.OneLine(err.Error())))
			}
		}
	}
	if len(remove) > 0 {
		if err := host.RemoveLabels(ctx, it.number, it.runID, remove); err != nil {
			warnLabel(ctx, seams, fmt.Sprintf("the labels %s could not be removed from %s#%d: %s",
				labelNames(remove), repo, it.number, safetext.OneLine(err.Error())))
		}
	}
}

// clearClosed is a pull request that is merged or closed: both status labels go
// and nothing else does (R4, R8). The spec label stays — it is what a person
// filters a spec's pull requests by, and taking it off a closed one buys
// nothing — and so does every label that is not Fugaro's.
//
// The number is remembered as closed and clean only once nothing is left to
// remove, so a removal the host refused is tried again by the next tick rather
// than written off as done.
func clearClosed(ctx context.Context, seams *LabelSeams, mem *labelMemory, repo string, host gitprov.Labeler, it labelItem, have []string) {
	if gone := statusesOn(have); len(gone) > 0 {
		if err := host.RemoveLabels(ctx, it.number, it.runID, gone); err != nil {
			warnLabel(ctx, seams, fmt.Sprintf("the labels %s could not be removed from %s#%d: %s",
				labelNames(gone), repo, it.number, safetext.OneLine(err.Error())))
			return
		}
	}
	mem.markClosed(repo, it.number)
}

// createLabels creates the labels of one add that this host has not already
// been shown to carry, and answers the names the add may keep (D2440, R6).
//
// A name whose creation failed is left out of the add rather than added anyway:
// an add of a label the host does not have creates it with the host's own
// colour, and the colour is the table's (D2437) — so the add waits for the
// ensure instead of writing a label Fugaro did not choose the look of.
//
// A spec label the host refused is remembered and never asked for again: the
// spec name is already in the pull request's title, so the label is
// best-effort, and one line in the log is the whole of what the refusal costs
// (spec, R5). A status label is asked for again by the next tick — a
// permission the owner fixes has to heal the labels without a restart of the
// daemon — and one line each time is what that costs.
func createLabels(ctx context.Context, seams *LabelSeams, mem *labelMemory, repo string, host gitprov.Labeler, names []string) []string {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		switch {
		case mem.didEnsure(repo, name):
			kept = append(kept, name)
			continue
		case mem.didRefuse(repo, name):
			continue
		}
		colour, description := labelText(name)
		if err := host.EnsureLabel(ctx, name, colour, description); err != nil {
			if isSpecLabel(name) {
				mem.markRefused(repo, name)
			}
			warnLabel(ctx, seams, fmt.Sprintf("the label %s could not be created on %s: %s",
				name, repo, safetext.OneLine(err.Error())))
			continue
		}
		mem.markEnsured(repo, name)
		kept = append(kept, name)
	}
	return kept
}

// warnLabel leaves one line in the seam's log.
func warnLabel(ctx context.Context, seams *LabelSeams, line string) {
	seams.Log.Warn(ctx, line)
}

// labelNames is a set of label names as one log line names them.
func labelNames(names []string) string { return strings.Join(names, ", ") }

// labelMemory is what a daemon carries between ticks, and it holds only what is
// worth not repeating (F10, R5): the labels a host has been shown to have, the
// spec labels it refused to create, the spec names no label can be built from,
// and the pull requests known closed and clean.
//
// Neither the desired set nor what a host holds is kept: the first is derived
// from the node every tick, which is what makes a tick that lost its memory the
// tick it would have been, and the second the bulk read answers afresh. A cycle
// runs on the daemon's one goroutine and holds the seams itself, so the maps
// need no lock; they key on the repository as well as the name, because two
// repositories are two hosts with a catalogue of labels each.
//
// The spec names that are no label's name are a fourth map and not a second use
// of refused's: refused is keyed by a label name and this one by the spec name
// as the node wrote it, and a spec name may be spelled like a label —
// `spec:forge-labels` is one — so one map would let a name that cannot be a
// label stop another item's label from being created.
//
// The closed numbers are the one part that grows with the run of the daemon:
// it is one small entry per pull request that closed while its item was still
// on the node, which is bounded by the daemon's own runs, and forgetting one
// costs a read rather than a wrong write.
type labelMemory struct {
	ensured   map[string]bool
	refused   map[string]bool
	shapeless map[string]bool
	closed    map[string]bool
}

// memory is this seam's memory, made on first use. A daemon that restarted is
// a daemon whose memory is empty, which is the whole of what "reads a host
// before writing to it" has to survive (F10).
func (s *LabelSeams) memory() *labelMemory {
	if s.kept == nil {
		s.kept = &labelMemory{
			ensured:   map[string]bool{},
			refused:   map[string]bool{},
			shapeless: map[string]bool{},
			closed:    map[string]bool{},
		}
	}
	return s.kept
}

// didEnsure reports whether this daemon has already shown this host to carry a
// label, which is what makes an ensure once and not once per tick (R5).
func (m *labelMemory) didEnsure(repo, name string) bool {
	return m.ensured[labelKey(repo, name)]
}

// markEnsured records a label this host answered for, so no tick of this
// daemon asks it to create that one again.
func (m *labelMemory) markEnsured(repo, name string) {
	m.ensured[labelKey(repo, name)] = true
}

// didRefuse reports whether this host refused to create a spec label.
func (m *labelMemory) didRefuse(repo, name string) bool {
	return m.refused[labelKey(repo, name)]
}

// markRefused records a spec label this host would not create, so the daemon
// stops asking for it (R5).
func (m *labelMemory) markRefused(repo, name string) {
	m.refused[labelKey(repo, name)] = true
}

// didRefuseShape reports whether this daemon has already said its one line
// about a spec name no label can be built from (R3, F7).
func (m *labelMemory) didRefuseShape(repo, name string) bool {
	return m.shapeless[labelKey(repo, name)]
}

func (m *labelMemory) markRefusedShape(repo, name string) {
	m.shapeless[labelKey(repo, name)] = true
}

// isClosed reports whether a pull request is known merged or closed and
// carrying none of Fugaro's status labels.
func (m *labelMemory) isClosed(repo string, number int) bool {
	return m.closed[labelKey(repo, strconv.Itoa(number))]
}

func (m *labelMemory) markClosed(repo string, number int) {
	m.closed[labelKey(repo, strconv.Itoa(number))] = true
}

// labelKey is one memory key: a repository and what is looked up under it. The
// separator is a byte the first half cannot carry — a repository is owner/name
// — so no two pairs read as one other whatever the second half is, and it is
// any of the three kinds this memory looks up: a label name, a spec name as the
// node wrote it, or a number.
func labelKey(repo, what string) string { return repo + "\x00" + what }
