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

// MergeReader reads the pull request state the merge wait is about: the open
// pull requests of a repository, and one pull request's own lifecycle state.
//
// It is one method set and not two seams because the two halves are one host's
// answer and one cadence's read, and because what the daemon decides with them
// is one decision: a listing that carries an item's pull request is the whole
// answer for it, and only what the listing does not carry is read one by one.
type MergeReader interface {
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

// MergeLog is where the merge read's failures go: one line per cause. Its shape
// is the label syncer's (LabelLog) and it is a second interface rather than the
// same one because a daemon wires the two separately, and a line about a merge
// read in the label syncer's log would be read as the syncer's own.
type MergeLog interface {
	Warn(ctx context.Context, line string)
}

// MergeSeams is what one cycle keeps the merge wait through: the reader of the
// repositories' pull requests and the log every failure leaves a line in. Both
// or none — a Tick built with neither reads nothing at all, which is the
// direction a daemon whose path to the credentials service could not be built
// fails in: an item whose pull request is never read stays in `review`, where
// its dependents wait, rather than being called done on nobody's word.
type MergeSeams struct {
	// Reader answers the pull request state of the repository an item is on.
	Reader MergeReader
	// Log is where a read that failed goes: one line per cause, and nothing
	// else.
	Log MergeLog
	// kept is what this daemon carries between ticks (mergeMemory). It is
	// made on first use, because a Tick carries its seams as a value and has
	// nowhere to build one.
	kept *mergeMemory
}

// Set reports whether every seam is there.
func (s MergeSeams) Set() bool { return s.Reader != nil && s.Log != nil }

// merge is one cycle's turn at the merge read, and the write its answer lands
// in.
//
// It is the fold's own question asked one step further: the fold has just moved
// every item whose run succeeded with a pull request into `review`, and this is
// what asks the hosts which of those pull requests have since merged. An item
// it moves is moved before the plan is made, so the work that depends on it
// starts in this same cycle rather than the next one.
func (c *cycle) merge(ctx context.Context) error {
	if !c.tick.Merge.Set() || !c.fresh {
		return nil
	}
	ops := c.mergeOps(ctx)
	if len(ops) == 0 {
		return nil
	}
	next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	return c.commit(ctx, next)
}

// mergeOps is one cycle's whole turn at the read: every repository's items in
// `review`, each repository read at most once per cadence, as the ops that
// record what the hosts answered and the one fact an unreadable installation
// leaves.
func (c *cycle) mergeOps(ctx context.Context) []Op {
	seams := &c.tick.Merge
	mem := seams.memory()
	// recorded is the repositories this cycle has already written the missing
	// consumer's fact for, seeded from the node: the fact is the record and the
	// node is where it lives, so a write that did not land is a fact the next
	// cycle writes, and a daemon that restarted does not write what is already
	// there.
	recorded := map[string]bool{}
	var ops []Op
	for _, g := range mergeGroups(c.state) {
		if !mem.due(g.repo, c.now) {
			continue
		}
		// The read is marked before it is made and not after it lands: a host
		// that would not answer is a host to ask again in a minute, not one to
		// ask again on the next wake.
		mem.markRead(g.repo, c.now)
		ops = append(ops, c.mergeRepo(ctx, seams, recorded, g)...)
	}
	return ops
}

// mergeGroup is one repository's items waiting on a merge: the bulk read is per
// repository, so the items are grouped before anything is read.
type mergeGroup struct {
	repo  string
	items []mergeRun
}

// mergeRun is one item waiting on a merge as the node records it: the item and
// the run its attempt left, which is what the pull request's number is read by.
type mergeRun struct {
	id    string
	runID string
}

// mergeGroups is the items in `review`, in the repositories they are on, in the
// order the node carries them.
//
// An item with no repository is left out, as it is of every read a repository
// is the unit of; and an item whose phase is `review` but which no run of the
// node reaches is in the group all the same, because what is wrong with it is
// its number and not the read.
func mergeGroups(s State) []mergeGroup {
	var (
		out []mergeGroup
		at  = map[string]int{}
	)
	for _, w := range s.WorkItems {
		if w.Phase != PhaseReview || w.Repo == "" {
			continue
		}
		if i, seen := at[w.Repo]; seen {
			out[i].items = append(out[i].items, mergeRun{id: w.ID, runID: w.RunID})
			continue
		}
		at[w.Repo] = len(out)
		out = append(out, mergeGroup{repo: w.Repo, items: []mergeRun{{id: w.ID, runID: w.RunID}}})
	}
	return out
}

// mergeItem is one item's pull request as this cadence reads it: the item and
// the number the run view answered for its run.
type mergeItem struct {
	id     string
	number int
}

// mergeRepo is one repository's turn: the number each item waits on, one
// listing of what the host reports open, and a `get` for each item the listing
// does not carry (D2742) — or for each item at all, where the host has no
// listing (Bitbucket).
func (c *cycle) mergeRepo(ctx context.Context, seams *MergeSeams, recorded map[string]bool, g mergeGroup) []Op {
	items, err := c.mergeItems(ctx, g)
	if err != nil {
		warnMerge(ctx, seams, fmt.Sprintf("the pull requests of %s could not be read: %s",
			g.repo, safetext.OneLine(err.Error())))
		return nil
	}
	open, supported, err := seams.Reader.ListOpen(ctx, g.repo)
	if err != nil {
		return c.mergeRefusal(ctx, seams, recorded, g.repo, err)
	}
	var ops []Op
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
			ops = append(ops, c.mergeRefusal(ctx, seams, recorded, g.repo, err)...)
			continue
		}
		if op, ok := c.mergeOp(g.repo, it, state); ok {
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
			out = append(out, mergeItem{id: it.id, number: r.PRNumber})
		}
	}
	return out, nil
}

// mergeOp is what one pull request's state means for its item: `done` for one
// that merged, `failed` for one that closed without merging, and nothing for
// one that is still open or for a state this release cannot place.
//
// A move the phase table does not allow is left out rather than refused, for
// the reason the fold leaves one out: the item was moved by somebody else while
// the read was going, and their word stands.
func (c *cycle) mergeOp(repo string, it mergeItem, state MergeState) (Op, bool) {
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
		return Op{}, false
	}
	w, ok := c.state.Item(it.id)
	if !ok || !w.Phase.CanMoveTo(phase) {
		return Op{}, false
	}
	c.rep.Merged++
	return Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: w.ID, Phase: &phase}}, true
}

// mergeRefusal is what a read that did not happen leaves: one line in the log,
// and — for a refusal for want of a Bitbucket read consumer — one fact naming
// the repository and the fix, recorded once per repository and not once per
// cadence (D2736).
//
// The items are left in `review` by both: a read that failed is not an answer,
// and the wait it was reading for goes on.
func (c *cycle) mergeRefusal(ctx context.Context, seams *MergeSeams, recorded map[string]bool, repo string, err error) []Op {
	if !errors.Is(err, ErrMergeNoReadConsumer) {
		warnMerge(ctx, seams, fmt.Sprintf("the pull requests of %s could not be read: %s",
			repo, safetext.OneLine(err.Error())))
		return nil
	}
	key := mergeFactKey(repo)
	if _, ok := c.state.Facts[key]; ok || recorded[repo] {
		return nil
	}
	recorded[repo] = true
	return []Op{{Op: OpFactSet, Fact: &FactOp{Key: key, Value: mergeFactValue(repo)}}}
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
func warnMerge(ctx context.Context, seams *MergeSeams, line string) {
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
func (s *MergeSeams) memory() *mergeMemory {
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
