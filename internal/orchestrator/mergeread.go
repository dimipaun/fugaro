// The merge read: what an item waits for once its run has left a pull request
// (G3, D2705).
//
// A run that succeeded leaving a pull request leaves its item in `review`
// (foldPhase), and the item is `done` once that pull request has merged and
// `failed` once it closed without merging. Neither is anything the daemon's own
// record can say: the host is the only thing that knows, and this is the read
// that asks it.
//
// It is the second reader of a host's pull request state the daemon has — the
// label syncer reads one to write its labels — and it is not the same read:
// this one moves a phase and writes no label, and the syncer is a silent no-op
// on a host with no labels at all (Bitbucket, R7). What the two share is the
// route they go through: the credentials service's label route answers a pull
// request's state to the daemon class on both hosts (F9, D2736, G5), which is
// what keeps the daemon holding no forge credential of its own.
//
// The read keeps its own cadence (D2742): at most once per repository every
// MergeReadEvery, whatever wakes the tick. A wake is not a reason to ask a host
// anything — the loop wakes on a stream of changes and on a 30 s ceiling, and a
// read per wake would exhaust the label route's per-repository budget the runs
// themselves go through — so the cadence is the daemon's own memory of when it
// last read each repository, and the tick it is read in is the one whose clock
// passed. The number each item is read by comes from the run view's report of
// the attempt's own record (F25), which is the same answer the fold read to
// leave the item in `review` at all: one attempt left one pull request, and
// nothing here reads the record a second time.
//
// Nothing here fails a cycle, for the reason nothing in the label syncer does.
// A host that will not answer, an installation that can read no Bitbucket pull
// request and a run whose record names none are one line in the log, or one
// fact, each, and the item stays in `review`: the read is a wait and never the
// work, and a wait that failed is a wait that goes on.

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// mergeReadEvery is how often one repository's pull requests are read (D2742).
//
// It is a cadence and not a timeout: an item whose pull request merges waits at
// most this long to leave `review`, and the wait costs the label route two
// reads per repository per minute — one listing and the `get`s of what the
// listing does not carry — however many times the loop wakes in between.
const mergeReadEvery = 60 * time.Second

// MergeState is a pull request's lifecycle as the merge read reads it. The
// three names are the host's own, spelled as the label route spells them
// (creds.LabelPullRequest.State, gitprov.PRState): a read that answers a state
// this package has never heard of is not a state an item moves on.
type MergeState string

// The three states a host answers with.
const (
	MergeOpen   MergeState = "open"
	MergeMerged MergeState = "merged"
	MergeClosed MergeState = "closed"
)

// ErrMergeNoReadConsumer is the credentials service refusing a Bitbucket read
// for want of a read consumer: the installation holds none, so no pull request
// of that repository can be read until one is set (G5, D2736).
//
// It is a sentinel and not a message because the two are answered differently.
// An outage is retried by the next cadence and costs a line in the log; this
// costs the items of that repository their wait for as long as it stands, so it
// is recorded as a fact the person who owns the installation reads — naming the
// repository and the fix — rather than a line per cadence that says nothing
// about what to do.
var ErrMergeNoReadConsumer = errors.New("orchestrator: the credentials service holds no Bitbucket read consumer for this repository")

// MergeWaitReader reads the pull request state the merge wait is about: the open
// pull requests of a repository, and one pull request's own lifecycle state.
//
// It is one method set and not two seams because the two halves are one host's
// answer and one cadence's read, and because what the daemon decides with them
// is one decision: a listing that carries an item's pull request is the whole
// answer for it, and only what the listing does not carry is read one by one.
type MergeWaitReader interface {
	// ListOpen returns the open pull requests of repo, keyed by number, and
	// false where the host behind it has no such listing at all — Bitbucket,
	// where a `get` per item is the only read there is. An error is a host
	// that would not answer, and nothing is read for that repository this
	// cadence.
	ListOpen(ctx context.Context, repo string) (open map[int]bool, supported bool, err error)
	// State returns one pull request's state, open, merged or closed. A
	// refusal for want of a Bitbucket read consumer is ErrMergeNoReadConsumer,
	// which the caller records once per repository.
	State(ctx context.Context, repo string, number int) (MergeState, error)
}

// MergeWaitLog is where the merge read's failures go: one line per cause. Its shape
// is the label syncer's (LabelLog) and it is a second interface rather than the
// same one because a daemon wires the two separately, and a line about a merge
// read in the label syncer's log would be read as the syncer's own.
type MergeWaitLog interface {
	Warn(ctx context.Context, line string)
}

// MergeWaitSeams is what one cycle keeps the merge wait through: the reader of the
// repositories' pull requests and the log every failure leaves a line in. Both
// or none — a Tick built with neither reads nothing at all, which is the
// direction a daemon whose path to the credentials service could not be built
// fails in: an item whose pull request is never read stays in `review`, where
// its dependents wait, rather than being called done on nobody's word.
type MergeWaitSeams struct {
	// Reader answers the pull request state of the repository an item is on.
	Reader MergeWaitReader
	// Log is where a read that failed goes: one line per cause, and nothing
	// else.
	Log MergeWaitLog
	// kept is what this daemon carries between ticks (mergeMemory). It is
	// made on first use, because a Tick carries its seams as a value and has
	// nowhere to build one.
	kept *mergeMemory
}

// Set reports whether every seam is there.
func (s MergeWaitSeams) Set() bool { return s.Reader != nil && s.Log != nil }

// merge is one cycle's turn at the merge read over every node it works, and the
// writes its answers land in.
//
// It is the fold's own question asked one step further: the fold has just moved
// every item whose run succeeded with a pull request into `review` — in whatever
// node that item is — and this is what asks the hosts which of those pull
// requests have since merged. An item it moves is moved before the plan is made,
// so the work that depends on it starts in this same cycle rather than the next
// one, and an item in a plan's node or the backlog's is moved in the node that
// carries it (D2735). A node the read never reaches is a wait the project never
// leaves: the cycle reads every node's `review` as a reason to keep waiting
// (hasReviewWait, outcome), so the node would sit in `review` for good and its
// dependents would never start.
//
// The reads of every node are made together and each node is written with its
// own (mergeOps): one repository is read at most once per cadence whatever nodes
// its items are on, which is what keeps the cadence the host's per-repository
// budget and not one read per node (D2742).
func (c *cycle) merge(ctx context.Context) error {
	if !c.tick.Merge.Set() {
		return nil
	}
	for i, ops := range c.mergeOps(ctx) {
		if len(ops) == 0 {
			continue
		}
		c.bind(i)
		next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
		if err != nil {
			return err
		}
		if err := c.commit(ctx, next); err != nil {
			return err
		}
		c.settle()
	}
	// The read reaches every node, so the cycle comes back to the node it was
	// working on: the state node's own turn — the plan, then the ladder, the
	// long-run rule and the decision — follows the merge.
	c.bind(0)
	return nil
}

// mergeFacts is what one cadence has done about the missing read consumer: the
// repositories it has answered for — the fact queued, or the one line saying
// the node will not take it — and how many fact ops it has queued for each node
// in the patch it is building.
//
// The count is what the fact bound is read against beside the facts the node
// already carries: what ValidateState refuses is the state the patch makes, so a
// fact queued for one repository is one the next repository's fact does not fit
// beside — and a patch is one node's, which is why the count is one node's too.
type mergeFacts struct {
	answered map[string]bool
	queued   map[int]int
}

// mergeOps is one cycle's whole turn at the read, over every node it works:
// every node's items in `review`, grouped by repository, each repository read at
// most once per cadence (D2742) — one read answers for the items of every node
// that are on it, because the budget the cadence spends is the host's per
// repository and not the daemon's per node — as the ops each node records what
// the hosts answered with, in the order the cycle works its nodes. A node with
// no ops is one this read had nothing to say about.
func (c *cycle) mergeOps(ctx context.Context) [][]Op {
	seams := &c.tick.Merge
	mem := seams.memory()
	// answered starts empty every cycle and the nodes are what it is seeded
	// from instead: the fact is the record and the node is where it lives, so a
	// write that did not land is a fact the next cycle writes, and a daemon that
	// restarted does not write what is already there.
	facts := &mergeFacts{answered: map[string]bool{}, queued: map[int]int{}}
	states := c.states()
	ops := make([][]Op, len(states))
	for _, g := range c.mergeWaits(states) {
		if !mem.due(g.repo, c.now) {
			continue
		}
		// The read is marked before it is made and not after it lands: a host
		// that would not answer is a host to ask again in a minute, not one to
		// ask again on the next wake.
		mem.markRead(g.repo, c.now)
		for _, no := range c.mergeRepo(ctx, seams, facts, states, g) {
			ops[no.node] = append(ops[no.node], no.op)
		}
	}
	return ops
}

// hasReviewWait reports whether the node holds an item waiting for its pull
// request to merge. It is what the loop reads the wait by: the attempt has
// reported, so nothing else on the node says the daemon is waiting, and a cycle
// whose only work is this one is in flight rather than empty (D1228's reading
// of an unreported attempt, D2705, G3).
//
// An item with no repository counts here as it does not in mergeWaits: what
// ends the wait is a person merging the pull request, with or without a
// repository on the record, so a cycle spent waiting for one is not a cycle the
// breaker is for — and the pause a trip raises is what refuses the launch a
// merge unlocks.
func hasReviewWait(s State) bool {
	for _, w := range s.WorkItems {
		if w.Phase == PhaseReview {
			return true
		}
	}
	return false
}

// mergeGroup is one repository's items waiting on a merge: the bulk read is per
// repository, so the items of every node the cycle works are grouped together
// before anything is read.
type mergeGroup struct {
	repo  string
	items []mergeRun
}

// mergeRun is one item waiting on a merge as the node records it: the node that
// carries the item and the run its attempt left, which is what the pull
// request's number is read by.
type mergeRun struct {
	node  int
	id    string
	runID string
}

// nodeOp is one op and the node it lands on: the read is per repository and its
// answer belongs to the node that carries the item it is about (D2735).
type nodeOp struct {
	node int
	op   Op
}

// mergeWaits is the items in `review` of every node this cycle may write, in the
// repositories they are on, in the order the cycle works the nodes and, inside
// one node, in the order the node carries them.
//
// A node another writer has moved, and one over its size target, is left out
// with its items: Apply validates the state a patch is made from, so an op built
// for either is a patch the node refuses, and that refusal would end the whole
// cycle — the state node's launches and every node beside it included — where a
// node over its cap is that node's own affair (OverCap, planNode, and X4's
// acceptance that it stops that node alone).
//
// An item with no repository is left out, as it is of every read a repository is
// the unit of; and an item whose phase is `review` but which no run of the node
// reaches is in the group all the same, because what is wrong with it is its
// number and not the read.
func (c *cycle) mergeWaits(states []State) []mergeGroup {
	var (
		out []mergeGroup
		at  = map[string]int{}
	)
	for i, s := range states {
		if !c.nodes[i].fresh || OverCap(s) {
			continue
		}
		for _, w := range s.WorkItems {
			if w.Phase != PhaseReview || w.Repo == "" {
				continue
			}
			waits := mergeRun{node: i, id: w.ID, runID: w.RunID}
			if j, seen := at[w.Repo]; seen {
				out[j].items = append(out[j].items, waits)
				continue
			}
			at[w.Repo] = len(out)
			out = append(out, mergeGroup{repo: w.Repo, items: []mergeRun{waits}})
		}
	}
	return out
}

// mergeItem is one item's pull request as this cadence reads it: the node the
// item is in, the item, and the number the run view answered for its run.
type mergeItem struct {
	node   int
	id     string
	number int
}

// mergeRepo is one repository's turn: the number each item waits on, one
// listing of what the host reports open, and a `get` for each item the listing
// does not carry (D2742) — or for each item at all, where the host has no
// listing (Bitbucket) — as the ops the nodes that carry those items take.
func (c *cycle) mergeRepo(ctx context.Context, seams *MergeWaitSeams, facts *mergeFacts, states []State, g mergeGroup) []nodeOp {
	items, err := c.mergeItems(ctx, g)
	if err != nil {
		warnMerge(ctx, seams, fmt.Sprintf("the pull requests of %s could not be read: %s",
			g.repo, safetext.OneLine(err.Error())))
		return nil
	}
	open, supported, err := seams.Reader.ListOpen(ctx, g.repo)
	if err != nil {
		return c.mergeRefusal(ctx, seams, facts, states, g.items[0].node, g.repo, err)
	}
	var ops []nodeOp
	for _, it := range items {
		if supported && open[it.number] {
			// The host reports the pull request open, which is the whole
			// answer for it: the item waits, and this cadence has nothing to
			// write. What the listing does not carry is one it does not report
			// as open — merged, or closed — and that is what a `get` is for.
			continue
		}
		state, err := seams.Reader.State(ctx, g.repo, it.number)
		if err != nil {
			ops = append(ops, c.mergeRefusal(ctx, seams, facts, states, it.node, g.repo, err)...)
			continue
		}
		if op, ok := c.mergeOp(states[it.node], it, state); ok {
			ops = append(ops, op)
		}
	}
	return ops
}

// mergeItems is the pull request each item of one group waits on: the number
// the run view answered for the attempt's run id (F25), which is the same read
// the fold made to leave the item in `review`.
//
// A run the view does not answer for, and one whose report names no pull
// request, leaves its item out of the read: an item in `review` whose attempt
// left none is one nothing said to move there, and what holds it is not this
// read.
func (c *cycle) mergeItems(ctx context.Context, g mergeGroup) ([]mergeItem, error) {
	ids := make([]string, 0, len(g.items))
	for _, it := range g.items {
		if it.runID != "" {
			ids = append(ids, it.runID)
		}
	}
	reports, err := c.tick.Runs.Runs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byRun := make(map[string]RunReport, len(reports))
	for _, r := range reports {
		byRun[r.RunID] = r
	}
	var out []mergeItem
	for _, it := range g.items {
		if r, ok := byRun[it.runID]; ok && r.PRNumber > 0 {
			out = append(out, mergeItem{node: it.node, id: it.id, number: r.PRNumber})
		}
	}
	return out, nil
}

// mergeOp is what one pull request's state means for its item: `done` for one
// that merged, `failed` for one that closed without merging, and nothing for
// one that is still open or for a state this release cannot place. s is the
// state of the node the item is in, and the op is that node's.
//
// A move the phase table does not allow is left out rather than refused, for
// the reason the fold leaves one out: the item was moved by somebody else while
// the read was going, and their word stands.
func (c *cycle) mergeOp(s State, it mergeItem, state MergeState) (nodeOp, bool) {
	var phase Phase
	switch state {
	case MergeMerged:
		phase = PhaseDone
	case MergeClosed:
		phase = PhaseFailed
	default:
		// Open, or a state no host of this release answers with: the item
		// waits where it is. A state that is not one of the three is a host
		// answering something new, and moving an item on a state nobody has
		// read the meaning of is the one thing this must not do.
		return nodeOp{}, false
	}
	w, ok := s.Item(it.id)
	if !ok || !w.Phase.CanMoveTo(phase) {
		return nodeOp{}, false
	}
	c.rep.Merged++
	return nodeOp{node: it.node, op: Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: w.ID, Phase: &phase}}}, true
}

// mergeRefusal is what a read that did not happen leaves: one line in the log,
// and — for a refusal for want of a Bitbucket read consumer — one fact naming
// the repository and the fix, recorded once per repository and not once per
// cadence (D2736).
//
// The fact is once per project and not once per node, so it lands on the node
// whose items are in that repository — the node the read was made for, handed in
// as node — and a project that already carries it anywhere carries it
// (carriesMergeFact): a second node's copy is a fact nobody reads twice, and the
// person's own view of the project is not a node's to decide.
//
// The items are left in `review` by both: a read that failed is not an answer,
// and the wait it was reading for goes on.
func (c *cycle) mergeRefusal(ctx context.Context, seams *MergeWaitSeams, facts *mergeFacts, states []State, node int, repo string, err error) []nodeOp {
	if !errors.Is(err, ErrMergeNoReadConsumer) {
		warnMerge(ctx, seams, fmt.Sprintf("the pull requests of %s could not be read: %s",
			repo, safetext.OneLine(err.Error())))
		return nil
	}
	key := mergeFactKey(repo)
	if carriesMergeFact(states, key) || facts.answered[repo] {
		return nil
	}
	facts.answered[repo] = true
	// A node at its fact bound is a node this op cannot land on: the state that
	// carries one fact more is the state ValidateState refuses, and the refusal
	// takes the whole patch with it — which is the patch the same cadence's
	// merged pull requests move their items in. The facts this cadence has
	// queued for other repositories are a part of that state and not of the
	// node, so they are counted here beside it: the bound is over what the patch
	// makes, and a guard reading the node alone would let the second
	// repository's fact through and lose the patch to the state it built. The
	// move is what must land and the fact is the diagnostic, so the fact is what
	// is dropped when it does not fit; the line is what the person reads
	// instead, and the read is made again on the next cadence, so a node with
	// room again records it then.
	if len(states[node].Facts)+facts.queued[node] >= MaxFacts {
		warnMerge(ctx, seams, fmt.Sprintf("the missing read consumer of %s is not recorded: the node's facts reach their bound of %d with the ones this cadence queues; set one with `fugaro creds set bitbucket-read-consumer`",
			repo, MaxFacts))
		return nil
	}
	facts.queued[node]++
	return []nodeOp{{node: node, op: Op{Op: OpFactSet, Fact: &FactOp{Key: key, Value: mergeFactValue(repo)}}}}
}

// carriesMergeFact reports whether any node this cycle works already records the
// missing read consumer of one repository. The fact is the project's and it is
// read wherever it landed: the node it was written on is the node the read that
// found the refusal was made for, and the same repository's items may sit on
// another node — or on another node entirely by the time a person sets the
// credential — so a check of any one node would write the second copy the
// record is meant not to carry.
func carriesMergeFact(states []State, key string) bool {
	for _, s := range states {
		if _, ok := s.Facts[key]; ok {
			return true
		}
	}
	return false
}

// mergeFactKey is the fact key of one repository. A fact key is a name
// (validate.go's idRE) and a repository is owner/name, which is not one, so the
// key carries a digest of the repository and the value names it in full: a
// digest because a name long enough to disagree with the key's own bound would
// be refused, and a fact nothing can write is a fact nobody reads.
func mergeFactKey(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return "merge_read_no_consumer_" + hex.EncodeToString(sum[:8])
}

// The fact's sentence, in two halves with the repository between them. The
// repository is clipped to what is left of MaxFactValueRunes rather than
// trusted to fit: a name longer than any host issues is still one this document
// accepts, and an over-long value would fail the patch that records it — which
// would turn a missing credential into a tick that cannot write at all.
const (
	mergeFactHead = "no Bitbucket read consumer is set, so the pull requests of "
	mergeFactTail = " cannot be read and its items wait in review; set one with `fugaro creds set bitbucket-read-consumer`"
	// mergeFactRepoRunes is what is left of a fact's value for the repository.
	mergeFactRepoRunes = MaxFactValueRunes - len(mergeFactHead+mergeFactTail)
)

// mergeFactValue is the one fact an installation that can read no Bitbucket
// pull request leaves: the repository it is about, and the fix.
func mergeFactValue(repo string) string {
	return mergeFactHead + safetext.Clip(safetext.OneLine(repo), mergeFactRepoRunes) + mergeFactTail
}

// warnMerge leaves one line in the seam's log.
func warnMerge(ctx context.Context, seams *MergeWaitSeams, line string) {
	seams.Log.Warn(ctx, line)
}

// mergeMemory is what a daemon carries between ticks, and it holds the one
// thing worth not repeating (D2742): when each repository was last read.
//
// Neither what a host answered nor which items it answered for is kept: the
// node says which items wait, and what a host reports is read again on the next
// cadence, which is what makes a daemon that restarted behave as the daemon it
// was a minute earlier. The fact a missing read consumer leaves is not kept
// either — it lives on the node, which is what makes it once per repository
// rather than once per daemon, and what makes a write that did not land one the
// next cycle tries again.
type mergeMemory struct {
	at map[string]time.Time
}

// memory is this seam's memory, made on first use. A daemon that restarted is
// a daemon whose memory is empty, which costs one read per repository before its
// cadence is known.
func (s *MergeWaitSeams) memory() *mergeMemory {
	if s.kept == nil {
		s.kept = &mergeMemory{at: map[string]time.Time{}}
	}
	return s.kept
}

// due reports whether one repository may be read at this reading of the
// server's clock: the first reading of a repository always is, and one inside
// its cadence never is. A clock that reads earlier than the reading it last
// stamped — the server's clock, which this package reads and does not keep
// (D1230) — holds the read back rather than making one early, which is the
// direction the budget is spent in.
func (m *mergeMemory) due(repo string, now time.Time) bool {
	last, ok := m.at[repo]
	return !ok || !now.Before(last.Add(mergeReadEvery))
}

// markRead records that one repository has been read at this reading.
func (m *mergeMemory) markRead(repo string, now time.Time) {
	m.at[repo] = now
}
