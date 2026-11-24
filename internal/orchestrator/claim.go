// The claim: one item, one orchestrator (D3209).
//
// A project may have several orchestrators (D3206, D3207) and they read one
// plan in common: a plan task's identity is the plan's folder and the task
// (D2704), so two orchestrators that imported the same plan mean the same task
// when they name it — and two of them that both launch it would spend two runs
// on one piece of work and produce two pull requests for it. The claim is what
// keeps one of them: before an orchestrator starts a plan task or a backlog
// item it writes a claim for it at `ClaimPath` (naming.go), and an orchestrator
// that finds a claim already standing skips the item and lets the holder finish
// it.
//
// **The ETag is the fence**, as it is for the lease (lease.go, ruling M1): of
// two orchestrators that both read one absent claim node exactly one write
// lands, and the loser re-reads and re-decides rather than writing over the
// winner. The write is conditional always — an empty ETag is refused, never
// turned into a PUT — so no path through this file forces one. What the claim
// decides is **who works an item**, never what may be written: the state's own
// compare-and-swap is still what serialises the intents, and a claim has no say
// in a state write.
//
// **A claim is held until its item is finished, and never taken over.** The node
// names the holder and the server time it took that claim at or last re-stamped
// it at, and a node naming another orchestrator is that orchestrator's item:
// nothing here reads a lease, applies a grace or takes a standing claim away
// from its holder. The owner's ruling of 2026-11-23 is what settled that, and
// what it answered is why: a daemon deletes its lease node on every clean exit
// (Lease.Release — an upgrade restart, `--once`, a stop) while the runs it
// started are still in flight, so a rule that freed a claim once its holder's
// beat had lapsed would let a rival take an item whose run is still going and
// start a second run of it — a laptop lid closed for three minutes, or a
// `--once` invocation that has exited, is enough. What releases a claim is the
// item, and only the item — and what that release is depends on what the item's
// identity is.
//
// **A finished plan task's claim is recorded, not deleted.** A plan task's
// identity is project-wide: every orchestrator that imported the plan carries
// the task in its own node, and that copy still reads unfinished long after the
// holder's run for it has finished. A path deleted on the finish would read to
// such an orchestrator as an item nobody has taken, and it would take it and
// start a second run of one piece of work — the two pull requests this file
// exists to prevent, arrived at from the other side. So the holder's cycle
// writes the node it already owns with `finished` set (Refresh, and tick's last
// step) and the node stays: a rival finds a claim and skips it exactly as it did
// while the work was going, and the holder may take its own plan task again if
// the item is put back to work, because Take answers a node that names its own
// caller. A backlog item's claim is one orchestrator's own (its id is minted
// inside one node), so nothing else can be misled by its absence: that node
// comes off when the item is done or cancelled, which keeps the claims of one
// node bounded by the work in flight rather than by everything it ever ran.
//
// A release that failed, or a cycle that died after the write that finished the
// item had landed, is retried by the next cycle's own read of the same nodes.
// The beat re-stamps what this orchestrator holds for as long as it drives,
// which is what the stamp on the node records: when its holder last confirmed
// the item was still its own. A claim already marked finished is left exactly as
// it stands — there is nothing left to confirm about it, and the node is the
// project's only record that the task it names is done.
//
// Nothing here reads a clock of its own: the stamp is rtdb.ServerNow's, and a
// claim with no server time behind it is not made at all (D1230).
//
// **A node this release cannot read is not a free item.** A claim node carrying
// a document the decode refuses, or one naming no orchestrator id, is a refusal
// rather than a claim of nobody; so is a holder's lease node this release does
// not model, which is read as a live holder instead of a dead one. Both
// readings fail in the only safe direction: reading a holder we could not read
// as a gone one is precisely how two orchestrators launch one task.
//
// **The node keeps `orchestrator/`'s `.write: false`** (rules.json.tmpl,
// rules.go's PathOrchestrator). The Realtime Database's rules cannot fence one
// orchestrator from another, so the fence here is IAM — the same trust the
// state node is held under, and one project's claims are one subtree under it.
// No rules change accompanies this file (F33).

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

const (
	// claimAttempts bounds the re-read a lost claim race is answered with, the
	// way leaseAttempts bounds a lost lease race: a take re-reads and re-decides
	// this many times and then reports ErrClaimContention.
	claimAttempts = leaseAttempts
)

// ErrClaimContention is a claim write refused because the node moved under
// every bounded attempt. The caller decides that a rival orchestrator is
// working the item and acts on nothing.
var ErrClaimContention = errors.New("orchestrator: the claim node moved under every attempt")

// claimDoc is the claim node's shape on the wire: the orchestrator that holds
// the item, the server time it claimed at or last re-stamped it at, and whether
// the item it stands on has finished. The first two are the lease's shape
// (Holder, At), because it is read and written by the same rules, so a reader of
// this subtree reads one idiom and not two; `finished` is the claim's own,
// because a plan task's claim outlives the item it stands on while a lease never
// outlives its holder — it is what tells a rival orchestrator that the task its
// own node still carries as approved is done, rather than an item nobody has
// taken (this file's header).
type claimDoc struct {
	Holder   string    `json:"holder"`
	At       time.Time `json:"at"`
	Finished bool      `json:"finished,omitempty"`
}

// ClaimGuard is the project's claims as one orchestrator holds them: what a
// Starter takes before it starts an item's work (launch.go) and what a cycle
// re-stamps, marks finished and releases once the items it holds are finished
// (Refresh, and tick's last step). It is an interface for the reason every fence
// in this package is one — a test holds a daemon to a claim it controls — and
// nil is a process that keeps no claims: the resident pass, which makes no write
// to the node it drives and starts only the attempts a person's patch already
// committed (D1223, D1241).
type ClaimGuard interface {
	// Take takes the claim of ref for this orchestrator, or answers false when
	// another orchestrator holds it.
	Take(ctx context.Context, ref ItemRef) (bool, error)
	// Refresh re-stamps this orchestrator's own claims, records the finish on
	// the plan tasks among them and releases the work items whose item the
	// caller's nodes say has finished.
	Refresh(ctx context.Context, settled map[string]bool) error
}

// ClaimStore is the store a claim is read, written and re-stamped through: the
// one-node conditional read and write every fence in this package uses (CAS),
// and the shallow listing Refresh finds this orchestrator's own claims with
// (Keys). *rtdb.Client is one.
type ClaimStore interface {
	CAS
	Keys
}

// Claim is one orchestrator's claims, read and written through one store.
// agentID is the orchestrator that takes them — the id whose state node the
// items belong to and whose lease decides whether its claims outlive it.
type Claim struct {
	db      ClaimStore
	agentID string
	now     ServerNow
}

// The guard a starter and a cycle are handed is this type.
var _ ClaimGuard = (*Claim)(nil)

// NewClaim builds the claims of one orchestrator. The id is held to C-ORCH's
// rule and not to agentRE, which refuses the `~` of a named orchestrator: a
// claim is keyed by, and read back through, an orchestrator id.
func NewClaim(db ClaimStore, agentID string, now ServerNow) (*Claim, error) {
	switch {
	case db == nil:
		return nil, errors.New("orchestrator: a claim needs a store to be read and written through")
	case now == nil:
		return nil, errors.New("orchestrator: a claim needs the server's clock")
	}
	if !ValidOrchestratorID(agentID) {
		return nil, fmt.Errorf("%w: %q is not an orchestrator id, so it can hold no claim", ErrInvalid, safetext.OneLine(agentID))
	}
	return &Claim{db: db, agentID: agentID, now: now}, nil
}

// Take takes the claim of ref for this orchestrator, or reports that another
// holds it.
//
// A claim that is already this orchestrator's is answered true without a write:
// taking is not what keeps a claim alive — the holder's own beat re-stamps the
// claims it holds (Refresh) — so an id that asks for an item it already has
// moves no ETag a reader of this subtree would have to re-read. A claim naming
// another orchestrator is answered false and nothing is written, whatever its
// holder's lease says: the claim stands until the item does (this file's
// header), so there is no takeover here and no grace to run out.
//
// A node marked finished is answered the same way as one that is not: its holder
// takes it again (an item put back to work is that orchestrator's own), and
// nobody else does — a plan task finished by another orchestrator is work that is
// done, not an item that has come free.
func (c *Claim) Take(ctx context.Context, ref ItemRef) (bool, error) {
	path, err := ClaimPath(ref)
	if err != nil {
		return false, err
	}
	var last error
	for attempt := 0; attempt < claimAttempts; attempt++ {
		etag, doc, found, err := c.read(ctx, path)
		if err != nil {
			return false, err
		}
		if found {
			return doc.Holder == c.agentID, nil
		}
		at, err := c.stamped()
		if err != nil {
			return false, err
		}
		err = c.db.PutIfMatch(ctx, path, etag, claimDoc{Holder: c.agentID, At: at})
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return false, err
		}
		last = err
	}
	return false, fmt.Errorf("%w: %d claims, each refused by a moved ETag: %w", ErrClaimContention, claimAttempts, last)
}

// Refresh re-stamps the claims this orchestrator holds with the server's
// current time, records the finish on the plan tasks whose item has finished and
// releases the work items among them — a plan task's node stays, marked
// finished, because deleting it would read to a rival whose own copy of the task
// is still approved as an item nobody took (this file's header).
//
// settled is the claim paths whose item is done or cancelled in the nodes the
// caller last read — a cycle hands what its own nodes say (settledClaimPaths),
// and the beat hands nil, having read no nodes, so that every claim this
// orchestrator holds is re-stamped. It is what makes the release part of the
// refresh: a plan task or a backlog item that finished is work nobody is on, and
// a release that did not land, or that a cycle died before making, is made by
// the next cycle reading the same nodes.
//
// It is the beat and not the take, because an item's run outlives its take: a
// daemon takes a claim once, when it starts the run, and a run of hours is a
// claim whose take is hours old, so the stamp is re-written on the beat's own
// cadence for as long as the holder drives. A node already marked finished is
// left exactly as it stands — the tail of this file's header says why — so a
// plan task that is done is not rewritten on every beat.
//
// A node this orchestrator does not hold is not written: one naming another
// orchestrator is left exactly as it stands (a refresh is not a takeover, and
// releasing a rival's claim is how two orchestrators end up on one item), one
// this release cannot read is left for Take to refuse rather than repaired
// here, and one that is not there is nothing to re-stamp. A conditional write
// refused because the node moved under the read is the same answer, so a
// refresh never fails over a claim that has become somebody else's.
func (c *Claim) Refresh(ctx context.Context, settled map[string]bool) error {
	keys, err := c.db.Keys(ctx, ClaimsRoot)
	if err != nil {
		return err
	}
	for _, key := range keys {
		path := ClaimsRoot + "/" + key
		if key == budget.Key(c.agentID) {
			// This orchestrator's own work items, one node each (ClaimPath):
			// releasing one is safe, because no other orchestrator mints its id.
			items, err := c.db.Keys(ctx, path)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := c.holdItem(ctx, path+"/"+item, settled); err != nil {
					return err
				}
			}
			continue
		}
		if ValidOrchestratorID(key) {
			// Another orchestrator's own items: not this daemon's to walk.
			continue
		}
		// A plan task's claim, which is one node for the whole project: a plan
		// task's key always carries an escaped `/` and an id never does, so the
		// two shapes cannot be taken for each other (ClaimPath).
		if err := c.holdPlanTask(ctx, path, settled); err != nil {
			return err
		}
	}
	return nil
}

// holdPlanTask settles the claim this orchestrator holds on one plan task, which
// is one node for the whole project: the finish is recorded on it and the node
// stands, because a rival whose own copy of the task still reads approved would
// take a node that is not there as an item nobody has taken and start a second
// run of it (this file's header). A node already marked finished and whose item
// is still finished is left exactly as it stands, so a plan task that is done is
// not rewritten on every beat; the cycle after the item is put back to work
// re-stamps it, which takes the mark off. A caller that read no nodes (nil
// settled, the beat) names nothing as finished, so the node's own mark decides
// there. A node naming another orchestrator, one this release cannot read and one
// that is not there are all left exactly as they stand.
func (c *Claim) holdPlanTask(ctx context.Context, path string, settled map[string]bool) error {
	etag, doc, found, err := c.read(ctx, path)
	if err != nil || !found || doc.Holder != c.agentID {
		return nil
	}
	finished := settled != nil && settled[path]
	if doc.Finished && (settled == nil || finished) {
		return nil
	}
	if finished {
		return c.write(ctx, path, etag, claimDoc{Holder: c.agentID, At: doc.At, Finished: true})
	}
	return c.restamp(ctx, path, etag)
}

// holdItem releases the claim this orchestrator holds on one of its own work
// items or re-stamps it: the item's id is minted inside one node (D26), so
// nothing else can be misled by the claim's absence and the node comes off when
// the item is finished. A node naming another orchestrator, one this release
// cannot read and one that is not there are left exactly as they stand.
func (c *Claim) holdItem(ctx context.Context, path string, settled map[string]bool) error {
	etag, doc, found, err := c.read(ctx, path)
	if err != nil || !found || doc.Holder != c.agentID {
		return nil
	}
	if settled[path] {
		return c.write(ctx, path, etag, nil)
	}
	return c.restamp(ctx, path, etag)
}

// restamp writes one claim node this orchestrator holds with the server's
// current time, which is the reading the node carries: when its holder last
// confirmed the item was still its own.
func (c *Claim) restamp(ctx context.Context, path, etag string) error {
	at, err := c.stamped()
	if err != nil {
		return err
	}
	return c.write(ctx, path, etag, claimDoc{Holder: c.agentID, At: at})
}

// write is one conditional write of a claim node the caller has already read:
// the node's new value, or nil to take the node away. A precondition refusal is
// the node having moved under that read, which is not this writer's to answer.
func (c *Claim) write(ctx context.Context, path, etag string, v any) error {
	err := c.db.PutIfMatch(ctx, path, etag, v)
	if errors.Is(err, rtdb.ErrPrecondition) {
		return nil
	}
	return err
}

// read is one conditional read of a claim node: the ETag a take or a release
// would carry and the claim the node holds. A node this release cannot read is
// a refusal (NewClaim's header), never a claim of nobody.
func (c *Claim) read(ctx context.Context, path string) (string, claimDoc, bool, error) {
	var raw json.RawMessage
	etag, found, err := c.db.GetETag(ctx, path, &raw)
	if err != nil {
		return "", claimDoc{}, false, err
	}
	if !found {
		return etag, claimDoc{}, false, nil
	}
	doc, err := decodeStrict[claimDoc](raw)
	if err != nil {
		return "", claimDoc{}, false, err
	}
	if !ValidOrchestratorID(doc.Holder) {
		return "", claimDoc{}, false, fmt.Errorf("%w: the claim names %q, which is not an orchestrator id", ErrInvalid, safetext.OneLine(doc.Holder))
	}
	return etag, doc, true, nil
}

// stamped is the server's time as this subtree records it, and a refusal when
// the clock has never answered.
func (c *Claim) stamped() (time.Time, error) {
	now, ok := c.now()
	if !ok {
		return time.Time{}, ErrNoServerTime
	}
	return stamp(now), nil
}

// ClaimRef is the reference an item's claim is keyed by (ClaimPath): a plan
// item by the plan task it stands for — one node for every orchestrator of the
// project that imported the plan, which is what makes the claim project-wide
// (D3209) — and any other item by its own id under the orchestrator whose node
// holds it, because a work item id is minted inside one node (D26): the same id
// in two orchestrators is two items and must not share a claim.
func ClaimRef(agentID string, w WorkItem) ItemRef {
	if w.PlanTask != "" {
		return ItemRef{Orchestrator: agentID, Item: w.PlanTask}
	}
	return ItemRef{Orchestrator: agentID, Item: w.ID}
}

// settledClaimPaths is the claim of every item the nodes have finished: the
// claim paths whose item is done or cancelled, keyed the way ClaimPath keys
// them, which is what a cycle hands Refresh so that a finished plan task is
// marked and a finished work item's claim comes off, while the rest are
// re-stamped (D3209).
//
// It reads every done item of every node and not only the ones that moved in
// this cycle, which is the whole of the retry the release gets: a claim whose
// release failed, and one a cycle died before releasing, are released by the
// first cycle that reads the node after the item's own write has landed.
func settledClaimPaths(states []State) (map[string]bool, error) {
	settled := map[string]bool{}
	for _, s := range states {
		for _, w := range s.WorkItems {
			if !w.Phase.Done() {
				continue
			}
			path, err := ClaimPath(ClaimRef(s.AgentID, w))
			if err != nil {
				return nil, err
			}
			settled[path] = true
		}
	}
	return settled, nil
}
