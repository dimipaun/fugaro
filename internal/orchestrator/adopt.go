// Adoption: the runs already in flight when the daemon takes the id over
// (D2702, D2721).
//
// A daemon's first start finds work it did not begin: runs a session in a
// checkout launched by hand, with the project's money already spent on them and
// a pull request on the way. Nothing on the node records them. An item is
// written by a patch and a run id is the daemon's alone to set (D1231) —
// applyUpsert refuses run_id, phase and intent from every other class — so the
// import a session runs brings items in queued and unapproved, and the runs
// those items are about are invisible to the tick: a run with no intent is
// never folded and never counted (F4, F54).
//
// Adoption is that step, and it is the daemon's for the same reason the three
// fields are: it is the class the patch rule leaves them to. A daemon calls it
// once, at start-up, under the lease and before its first tick, so what the
// first tick reads is a node that already carries the work.
//
// What it writes is what the daemon would have written had it started the run
// itself: one running item whose id is the run id, carrying the launch's
// `plan_task`, and one committed attempt under it, so the run is folded and
// counted with the runs the daemon started (D2744) and the item meets the
// plan's import on the same name instead of standing beside it (D2702, D2746).
// What it does not write is an approval: only a person approves work (G4,
// G15), so an adopted item is one nobody has read — the ladder will not merge
// it (D2702), and the plan its run came from is named in one fact for the
// session to import and take to that person (D2721, R5).
//
// Everything here is one pass over a node this release already knows how to
// write: the reads are the node's own (store.go), the items and the intents are
// the state's own shapes (state.go), and the write is the one conditional save
// every other writer makes. Nothing here reads a run store, a clock of its own
// or a model: the runs come from the RunLister seam (W1 builds the real one)
// and the time is the database server's (D1230).

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

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// MaxAdoptedInMain is how many runs adoption takes into the state node.
	// Past it the runs are named in one fact instead (adoptNotCarriedKey),
	// because the state node is the node every other reader shares and the one
	// node a plan's runs do not go to (F44, D2735).
	//
	// It is adoption's own bound and not MaxWorkItems: a node at that cap is
	// one the daemon plans no spawn over at all (OverCap), and the runs
	// adoption meets there are ones nobody planned — a first start over a
	// checkout somebody drove by hand. Twenty is the same headroom the issue
	// ingest leaves below the backlog node's caps (D2720): enough for the work
	// a person is really running at once, and small enough that the node's
	// bytes stay for the items the daemon plans over.
	MaxAdoptedInMain = 20

	// planInFlightKeyPrefix starts the key of the one fact each adopted plan is
	// named under: `plan_in_flight_<plan>` (state.go's `plan_in_flight`). The
	// plan's node is under the same name, so the fact and the node a session
	// imports into cannot disagree.
	planInFlightKeyPrefix = "plan_in_flight_"

	// adoptNotCarriedKey is the key of the one fact naming the runs in flight
	// the daemon wrote no item for: the ones past MaxAdoptedInMain, and the
	// ones whose own record is missing what this release's validator requires
	// of an item. It is one fact rather than one per run because a daemon over
	// a busy first start would otherwise write more facts than a node holds
	// (MaxFacts), and what a person does with it is look at the runs anyway.
	adoptNotCarriedKey = "adopt_not_carried"
)

// RunInFlight is one run of the project's repositories that has not reported,
// as the RunLister seam answers it (W1 builds the real one over the runs
// bucket: a launch record with no final record, and a live execution).
//
// The fields are the run's own record's, which is the only place they exist:
// adoption has no checkout, no graph and no brief of its own, and the item it
// writes is made of what the run was launched with. Every one of them but
// Mode and PlanTask is required by the item this release's validator accepts,
// so a lister that cannot answer one leaves the daemon no item to write and the
// run is named in one fact instead (adoptNotCarriedKey).
type RunInFlight struct {
	// RunID is the run's own id, and it becomes the id of the item adoption
	// writes: the item is the run (D2702), one run is one item, and the id is
	// what a second adoption of the same run meets.
	RunID string
	// Repo is the repository the run is working in, owner/name as the run's
	// record writes it (task.CanonicalRepo's shape).
	Repo string
	// Title is the one line that names the run: the record's own title, or the
	// first line of its brief where it was launched with none
	// (task.TitleLine).
	Title string
	// Recipe is the recipe the run's loop came from, a name of the catalog
	// (recipe.NameRE). It is what tells a ladder run in flight from a code
	// change the project launched: the two recipes the ladder starts
	// (RecipeRebase, RecipeEscalate) are left where they are (codeChangeRuns).
	Recipe string
	// RigorTier is the expectation tier the run was dispatched at — one of
	// internal/tier's four, from the run record's `tier` — and not the run's
	// rigor mode: the two are different axes, and it is the tier an item
	// carries (F12, D2711, D2730). A run launched with no `--tier` has none,
	// and what the daemon writes for one is reported in the pull request that
	// added this file rather than guessed at here.
	RigorTier string
	// Brief is the brief the run was launched with: the item's own brief, so
	// the work an adopted run is doing reads on the node exactly as it reads in
	// the run's record.
	Brief string
	// PlanTask is the plan task the launch recorded (`<plan>/<task>`, the shape
	// task.PlanTaskProblem checks, D2704), or empty for a run launched with
	// none. It is what puts the item in the plan's own node, and what the plan
	// import meets it on (D2702, F44, D2746).
	PlanTask string
	// Mode is the mode the run is: one of recipe's four, or empty for the
	// implement mode a spec that names none carries. A lister names a review
	// run ModeReview even where the spec carries no mode at all, because a
	// review task is one by its pull requests (task.Spec.ReviewPRs) and not by
	// its mode field; adoption takes the code-change runs and leaves the rest
	// (D2702).
	Mode string
}

// RunLister answers the runs of the project's repositories that are in flight:
// every run a daemon starting now finds under way, one entry per run, with what
// the run's own record says about it. It is the one seam adoption has, and it
// is the caller's to build (W1): nothing in this package reads a run store, so
// what "in flight" means — a launch record with no final record, an execution
// the platform still reports as running — is answered where the records are.
//
// An error is a read that did not happen, and adoption writes nothing at all
// for it: a daemon that cannot see the runs in flight is a daemon that cannot
// tell a run it adopted from one it never saw, and half an adoption is worse
// than none — the next start adopts the rest anyway, which is what makes the
// whole step safe to repeat.
type RunLister interface {
	RunsInFlight(ctx context.Context) ([]RunInFlight, error)
}

// Adopt takes the runs already in flight into the state, once, at start-up, and
// answers how many items it wrote.
//
// It writes each run as one running item with one committed attempt under it,
// into the plan's own node where the launch names a plan and into the state
// node otherwise (D2702, F44); it records one plan_in_flight fact per plan an
// adopted run names, so a session in a checkout imports it (D2721); and it
// names the runs it did not take in one fact. It writes only under the lease:
// the node's own save refuses a guard that no longer holds, and a node built
// with no guard at all — a person's own patch (NodeOptions.Lease) — is refused
// here, before the runs are even read, because adoption is a daemon's step and
// nothing else's.
//
// A second call adopts nothing. The run ids are the items' ids and the intents'
// runs by then, so every run it lists is one an item already carries, no item
// and no fact changes, and no node is saved: nothing here is derived from what
// this process did, only from what the node says.
//
// An error after some items have landed is answered with the count that did:
// adoption is not a transaction across nodes (each is saved under its own ETag,
// D2735), and what a partial run leaves is the state a later start completes —
// a run already an item's is adopted by nobody.
func Adopt(ctx context.Context, lister RunLister, node *Node) (int, error) {
	if lister == nil {
		return 0, errors.New("orchestrator: adoption needs a lister of the runs in flight")
	}
	if node == nil {
		return 0, errors.New("orchestrator: adoption needs the node it writes its items to")
	}
	if node.lease == nil {
		return 0, fmt.Errorf("%w: adoption writes only under the lease", ErrLeaseLost)
	}
	found, err := lister.RunsInFlight(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the runs in flight: %w", err)
	}
	runs := codeChangeRuns(found)
	if len(runs) == 0 {
		// Nothing in flight is nothing to adopt, and the one reading that can
		// say so is the lister's: a node is not asked for, and no write is
		// made, which is also what a second call over an installation already
		// adopted reaches without the early exit.
		return 0, nil
	}
	a := &adoption{
		node:    node,
		plans:   map[Shard]*adoptPlan{},
		claimed: map[string]bool{},
		named:   map[string]string{},
	}
	if err := a.read(ctx, runs); err != nil {
		return 0, err
	}
	// The server's clock is read after the node has been read, because it is
	// the header of a response and not a clock of this machine (D1230): the
	// store answers one from the moment it has answered anything at all.
	now, err := adoptNow(node)
	if err != nil {
		return 0, err
	}
	adopted := 0
	for _, r := range runs {
		if a.take(r, now) {
			adopted++
		}
	}
	return a.write(ctx, adopted)
}

// codeChangeRuns is the runs adoption takes, in the one order a node is written
// in: the code-change runs of the project's repositories, sorted by run id, so
// two starts over one set of runs write the same document.
//
// A review run and an artifact run are the runs the daemon does not adopt
// (D2702): they are a job somebody else asked for — a second reading of a pull
// request, a render — and not work of the project's that a dependent waits on,
// so an item for one would be an item nothing folds and nothing launches. An
// evaluation is the same shape and is left the same way: a mode the catalog has
// that is neither the implement mode nor the empty one a spec that names none
// carries is a run that is not a code change.
//
// The ladder's own runs are left the same way, and for the sharper reason: a
// rebase and the one stronger pass are runs of an item this node already
// carries, and the id of one is the ledger's and never the item's
// (Escalation.RunID, E12). A ladder run declares no mode, so it reads as an
// implement run, and nothing claims its id — a second item for work that
// already has one, sitting running with an intent no runner ever reports on and
// no rung that could merge it. The claim (adoption.read) covers the ladder run
// whose move has landed; this covers the one a crash left with the launch and
// not the ledger.
//
// What is left out here is named nowhere: a run id that is not one
// (task.ValidRunID) cannot be an item's id at all (idRE), which makes it a
// lister's bug rather than a run in flight, and a fact naming it would be a
// fact carrying whatever the bug carried.
func codeChangeRuns(found []RunInFlight) []RunInFlight {
	out := make([]RunInFlight, 0, len(found))
	for _, r := range found {
		if r.Mode != "" && r.Mode != recipe.ModeImplement {
			continue
		}
		if ladderRecipe(r.Recipe) {
			continue
		}
		if !task.ValidRunID(r.RunID) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b RunInFlight) int { return strings.Compare(a.RunID, b.RunID) })
	return out
}

// ladderRecipe reports whether a run is one the escalation ladder started: rung
// 0's rebase or the one stronger pass of rung 2, the two recipes this release's
// ladder launches (ladderOuter). What it is for is adoption's own bound and not
// the ladder's: a run of either is a run of an item already on the node, so
// there is never an item for adoption to write.
func ladderRecipe(name string) bool { return name == RecipeRebase || name == RecipeEscalate }

// adoptNow is the server's clock as adoption stamps with it (D1230). The store
// is asked rather than the machine because a stamp under this subtree is the
// lease's and the caps' to read: a laptop's clock is not one (ErrNoServerTime).
func adoptNow(node *Node) (time.Time, error) {
	reader, ok := node.db.(Reader)
	if !ok {
		return time.Time{}, fmt.Errorf("%w: the store adoption writes through serves no server time", ErrNoServerTime)
	}
	at, ok := reader.ServerNow()
	if !ok {
		return time.Time{}, ErrNoServerTime
	}
	return stamp(at), nil
}

// adoption is one call's work: the nodes it has read, the runs that are already
// items', and the plans the adopted runs named.
type adoption struct {
	node *Node
	// plans is every node this call read, by shard: the state node is
	// ShardState and is always one of them, because the facts go there whether
	// or not an item does.
	plans map[Shard]*adoptPlan
	// order is the shards to write, plans first and the state node last: a
	// write that fails half way leaves the facts that are about the items
	// already written out of the node rather than facts about items that never
	// landed.
	order []Shard
	// claimed is every run id an item of a node this call read already carries,
	// as an item's id or as the run it is going: what makes a second call
	// adopt nothing, and what keeps a run the daemon itself launched from being
	// adopted beside its own item.
	claimed map[string]bool
	// named is the plan_in_flight fact each adopted plan is named under, by the
	// plan the fact names, and left is the runs no item was written for.
	named map[string]string
	left  []string
}

// adoptPlan is one node adoption may write: its state as it was read, the ETag
// that write goes under, what this call added to it, and how many items it may
// still take.
type adoptPlan struct {
	node  *Node
	state State
	etag  ETag
	// room is how many items this node may still carry: what MaxWorkItems
	// leaves, and — for the state node — what MaxAdoptedInMain leaves of the
	// runs adopted into it, so a node another daemon already adopted into does
	// not take twenty more on every start.
	room int
	// added is what this call wrote into it, and changed whether it is to be
	// saved at all: a node nothing landed in is not written, which is what
	// keeps a second call from moving an ETag nobody's write moved.
	added   int
	changed bool
}

// read reads every node the runs name, plus the state node, and remembers which
// run ids the items of those nodes already carry: their own, the run each is
// going, and the ladder run an item's ledger names (claimed).
func (a *adoption) read(ctx context.Context, runs []RunInFlight) error {
	shards := []Shard{ShardState}
	for _, r := range runs {
		shards = append(shards, a.shardOf(r))
	}
	slices.Sort(shards)
	shards = slices.Compact(shards)
	for _, shard := range shards {
		p, err := a.load(ctx, shard)
		if err != nil {
			return err
		}
		if shard != ShardState {
			a.order = append(a.order, shard)
		}
		for _, w := range p.state.WorkItems {
			if w.ID != "" {
				a.claimed[w.ID] = true
			}
			if w.RunID != "" {
				a.claimed[w.RunID] = true
			}
			// A ladder run is the ledger's and not the item's: its id is
			// written to the block beside the item (Escalation.RunID, E12) and
			// never to the item's own run_id, so what claims it is this. Left
			// out, a rebase or a stronger pass in flight at start-up would be
			// adopted as a second item for an item that already stands.
			if w.Escalation != nil && w.Escalation.RunID != "" {
				a.claimed[w.Escalation.RunID] = true
			}
		}
	}
	a.order = append(a.order, ShardState)
	return nil
}

// load reads one node and its item budget. A node that does not exist yet
// loads as this orchestrator's empty state (store.go), which is how a plan's
// node is created by the first write that lands in it.
func (a *adoption) load(ctx context.Context, shard Shard) (*adoptPlan, error) {
	node, err := newShardNode(a.node, shard)
	if err != nil {
		return nil, err
	}
	state, etag, err := node.Load(ctx)
	if err != nil {
		return nil, err
	}
	p := &adoptPlan{node: node, state: state, etag: etag, room: MaxWorkItems - len(state.WorkItems)}
	if shard == ShardState {
		p.room = min(p.room, MaxAdoptedInMain-adoptedItems(state))
	}
	a.plans[shard] = p
	return p, nil
}

// shardOf is the node one run's item belongs in: the plan's own node where the
// launch names a plan this release can build one for (F44, D2735), and the
// state node otherwise — which is every run launched with no plan task, and the
// runs whose plan task names a plan no node can be built for, a graph id that
// is not a folder name (specName, PathPlan).
func (a *adoption) shardOf(r RunInFlight) Shard {
	name, ok := planOf(r.PlanTask)
	if !ok {
		return ShardState
	}
	return PlanShard(name)
}

// take writes one run into the node it belongs to, and reports whether an item
// was written. A run an item of a node already carries is left where it is —
// that is the second call, and it is also a run the daemon itself launched —
// and a run the state would refuse leaves the run's id in the one fact.
//
// The item is validated per run rather than per node, because one record
// missing a field is a run nobody can do anything about and not a reason to
// lose the other nineteen: what the validator refuses is checked on the state
// this node would have, so what lands is a document this release accepts.
func (a *adoption) take(r RunInFlight, now time.Time) bool {
	if a.claimed[r.RunID] {
		return false
	}
	p := a.plans[a.shardOf(r)]
	if p == nil || p.room <= 0 {
		a.left = append(a.left, r.RunID)
		return false
	}
	item, intent := adoptedRun(r, now)
	next := p.state.Clone()
	// A plan's node holds the plan's items and nothing derived from them: the
	// item goes in alone, with no dependency to resolve (validate.go resolves
	// depends_on inside the one state, F44) and no escalation (X1's Adopted
	// mark is what the ladder reads).
	next.WorkItems = append(next.WorkItems, item)
	next.Intents = append(next.Intents, intent)
	if err := ValidateState(next); err != nil {
		a.left = append(a.left, r.RunID)
		return false
	}
	next.Rev = p.state.Rev + 1
	p.state, p.added, p.changed = next, p.added+1, true
	p.room--
	a.claimed[r.RunID] = true
	if plan, ok := planName(r.PlanTask); ok {
		a.named[plan] = planInFlightValue(plan)
	}
	return true
}

// write saves every node this call changed, plans first and the state node
// last, with the facts the state node carries. It answers the count of items
// written, and a save that fails answers the count of those already on a node:
// the items of the node that refused are not among them.
func (a *adoption) write(ctx context.Context, adopted int) (int, error) {
	a.facts()
	saved := 0
	for _, shard := range a.order {
		p := a.plans[shard]
		if !p.changed {
			continue
		}
		if err := p.node.Save(ctx, p.state, p.etag); err != nil {
			return saved, err
		}
		saved += p.added
	}
	return adopted, nil
}

// facts writes the facts the state node carries: one naming each plan an
// adopted run belongs to, and one naming the runs no item was written for. Each
// is written only where it is not already what the node says, which is what
// leaves a second call with nothing to save.
func (a *adoption) facts() {
	p := a.plans[ShardState]
	for _, plan := range sortedKeys(a.named) {
		a.setFact(p, planInFlightKey(plan), a.named[plan])
	}
	if len(a.left) > 0 {
		a.setFact(p, adoptNotCarriedKey, adoptNotCarriedValue(a.left))
	}
}

func (a *adoption) setFact(p *adoptPlan, key, value string) {
	if p.state.Facts[key].Value == value {
		return
	}
	next := p.state.Clone()
	if next.Facts == nil {
		next.Facts = map[string]Fact{}
	}
	next.Facts[key] = Fact{Value: value, By: Daemon}
	next.Rev = p.state.Rev + 1
	p.state, p.changed = next, true
}

// adoptedRun is the one item and the one intent adoption writes for a run.
//
// The item is the run (D2702): its id is the run id, it is running because the
// run is, its plan task is the launch's, and it is marked adopted, which is
// what the ladder reads before it merges anything (X1). It carries no approval:
// a person approves work, and an item nobody has read is not one.
//
// The intent is D2744's: the record a run with no intent never gets, so the run
// is folded and counted with the runs the daemon started (openRunIDs and
// CountsOf read intents and nothing else, F54). Its approval stamp is the
// adoption's own, because an intent commits under an approval and an adopted
// run has none — nothing reads that field except against an approval the item
// carries (approvalCovers, spentBy), and the item carries none, which is
// exactly why the ladder leaves the item to a person.
func adoptedRun(r RunInFlight, now time.Time) (WorkItem, Intent) {
	item := WorkItem{
		ID:        r.RunID,
		Title:     r.Title,
		Repo:      r.Repo,
		Recipe:    r.Recipe,
		RigorTier: r.RigorTier,
		PlanTask:  r.PlanTask,
		Brief:     r.Brief,
		Phase:     PhaseRunning,
		RunID:     r.RunID,
		Attempts:  1,
		Adopted:   true,
	}
	return item, Intent{
		ID:         IntentID(item.ID, 1),
		RunID:      item.RunID,
		SpecDigest: SpecDigest(item),
		ApprovalAt: now,
		At:         now,
	}
}

// adoptedItems is how many items of a node the daemon adopted: what
// MaxAdoptedInMain is spent against, so a start over a node another start
// already adopted into does not take twenty more.
func adoptedItems(s State) int {
	n := 0
	for _, w := range s.WorkItems {
		if w.Adopted {
			n++
		}
	}
	return n
}

// planOf is the plan part of a plan task as a node is built from it: the folded
// folder name PathPlan would write, and whether the plan task names one at all
// (`<plan>/<task>`, task.PlanTaskProblem). False for a run launched with no
// plan task and for a graph id no folder name can carry.
func planOf(planTask string) (string, bool) {
	plan, ok := planName(planTask)
	if !ok {
		return "", false
	}
	name, foldable := specName(plan)
	if !foldable {
		return "", false
	}
	return name, true
}

// planName is the plan part of a plan task, folded where it is a plan folder
// name and as written where it is not: what a fact and a session name the plan
// by. It is everything up to the task's own element, which is the last one, a
// plan task being a plan's path and one task of it (PlanTaskProblem).
func planName(planTask string) (string, bool) {
	if task.PlanTaskProblem(planTask) != "" {
		return "", false
	}
	plan := planTask[:strings.LastIndex(planTask, "/")]
	if name, foldable := specName(plan); foldable {
		return name, true
	}
	return plan, true
}

// planInFlightKey is the key the fact about one plan is written under. A plan
// folder name is a key already — the same shape an item's spec is (specName) —
// and one that is not, a graph id like `plans/<file>`, is named by a digest of
// it: a fact key is a property name in the database, which refuses `. $ # [ ] /`
// (validate.go), so a key carrying one would be a node no write ever lands in.
// The value names the plan in full either way.
func planInFlightKey(plan string) string {
	if idRE.MatchString(plan) {
		return planInFlightKeyPrefix + plan
	}
	sum := sha256.Sum256([]byte(plan))
	return planInFlightKeyPrefix + hex.EncodeToString(sum[:8])
}

// planInFlightValue is the one fact an adopted plan leaves: what the session
// that opens a checkout afterwards does with it (D2721, R5). It asks for the
// import and nothing else — the approval is the person's, and the fact is not
// where a session is told to make one.
func planInFlightValue(plan string) string {
	return cleanLine("a run of the plan "+plan+" was already in flight when the daemon took this project's orchestrator over, so the daemon has no item for the plan's other tasks: import it with `fugaro orchestrator import --plan "+plan+"` and ask the person to approve it", MaxFactValueRunes)
}

// adoptNotCarriedValue is the one fact naming the runs in flight the daemon
// wrote no item for. It is a listing and not a reason per run — the two reasons
// are the node's cap and a record missing what an item needs — and it names the
// runs so that whoever reads it can go and look at them.
func adoptNotCarriedValue(runs []string) string {
	return cleanLine("these runs were in flight when the daemon took this project's orchestrator over and it wrote no item for them, so nothing folds or counts them: "+strings.Join(runs, ", "), MaxFactValueRunes)
}

// newShardNode is one node of the same orchestrator as another node: the state
// node itself, or the node of a shard beside it (D2735). The plan's name is
// checked where the path is built, so this is where a plan task naming
// something that is not a plan folder is found — which is what shardOf asks.
func newShardNode(from *Node, shard Shard) (*Node, error) {
	return NewNode(NodeOptions{
		DB:      from.db,
		AgentID: from.agentID,
		Role:    from.role,
		Lease:   from.lease,
		Shard:   shard,
	})
}
