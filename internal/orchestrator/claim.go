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
// **What a refresh reads is what this orchestrator's own nodes carry, and never
// the claims node itself.** The node is not a list to walk: it accumulates one
// standing claim per plan task the project ever claimed, and every one of them
// outlives the work it stands on, so a walk of it would grow with that history
// rather than with the daemon's own work — a read per claim ever made, on every
// cycle and every beat, forever. A refresh is handed the claim of each item the
// caller's nodes carry that has been started (Held, HeldClaims) and reads that
// and nothing else, which is what bounds it: by the work in flight, and by the
// state's own retention. A claim whose item those nodes no longer carry — a plan
// the holder no longer has imported among them — is left exactly as it stands,
// neither re-stamped nor released, because nothing the holder reads names it any
// more: it is one node, and a rival that still carries the plan still skips it.
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
	// Refresh re-stamps the claims of the items the caller's own nodes carry,
	// records the finish on the plan tasks among them whose item has finished
	// and releases the work items among them (Held, HeldClaims).
	Refresh(ctx context.Context, held []Held) error
}

// Held is one item of this orchestrator's own nodes as its claim is settled: the
// reference the item's claim is keyed by (ClaimRef) and whether the item has
// finished — done or cancelled (ClaimGuard.Refresh, HeldClaims).
type Held struct {
	Ref      ItemRef
	Finished bool
}

// Claim is one orchestrator's claims, read and written through one store.
// agentID is the orchestrator that takes them — the id whose state node the
// items belong to and whose claims an item's own nodes carry.
type Claim struct {
	db      CAS
	agentID string
	now     ServerNow
}

// The guard a starter and a cycle are handed is this type.
var _ ClaimGuard = (*Claim)(nil)

// NewClaim builds the claims of one orchestrator. The id is held to C-ORCH's
// rule and not to agentRE, which refuses the `~` of a named orchestrator: a
// claim is keyed by, and read back through, an orchestrator id.
func NewClaim(db CAS, agentID string, now ServerNow) (*Claim, error) {
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

// Refresh settles the claims of the items this orchestrator's own nodes carry:
// every one is re-stamped with the server's current time, a plan task whose item
// has finished is marked finished and left standing, and a work item whose item
// has finished has its node taken off (this file's header).
//
// held is the claim of each item those nodes carry that has been started (Held,
// HeldClaims), and it is the whole of what is read here: one node per item in
// flight and no walk of the claims node, which grows with every claim the
// project ever made (this file's header). What a caller hands is its own nodes'
// reading, so a claim whose item those nodes no longer carry is left exactly as
// it stands.
//
// It is what makes a release part of the refresh: a plan task or a backlog item
// that finished is work nobody is on, and a release that did not land, or that a
// cycle died before making, is made by the next cycle reading the same nodes.
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
func (c *Claim) Refresh(ctx context.Context, held []Held) error {
	for _, h := range held {
		if err := c.settleOne(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

// settleOne settles the claim this orchestrator holds on one of the items its
// own nodes carry, as held says the item reads. What the finish means is the
// item's identity (this file's header): a plan task's claim is one node for the
// whole project, so the finish is recorded on it and the node stands — a rival
// whose own copy of the task still reads approved would take a node that is not
// there as an item nobody has taken and start a second run of it — while a work
// item's id is minted inside one node (D26), so nothing else can be misled by
// its absence and the node comes off. A node already marked finished and whose
// item is still finished is left exactly as it stands, so a plan task that is
// done is not rewritten on every beat; the cycle after the item is put back to
// work re-stamps it, which takes the mark off.
//
// A node naming another orchestrator, one this release cannot read and one that
// is not there are all left exactly as they stand.
func (c *Claim) settleOne(ctx context.Context, h Held) error {
	path, err := ClaimPath(h.Ref)
	if err != nil {
		return err
	}
	etag, doc, found, err := c.read(ctx, path)
	if err != nil || !found || doc.Holder != c.agentID {
		return nil
	}
	if !h.Finished {
		return c.restamp(ctx, path, etag)
	}
	if isPlanTask(h.Ref.Item) {
		if doc.Finished {
			return nil
		}
		return c.write(ctx, path, etag, claimDoc{Holder: c.agentID, At: doc.At, Finished: true})
	}
	return c.write(ctx, path, etag, nil)
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

// claimKey is where one item of one node's claim stands (ClaimRef, ClaimPath),
// and it is one function because two places have to agree on it exactly: the
// cycle that learned an item is another orchestrator's by being refused its
// claim, and the plan that leaves that item out (tick's carry, planNode). A
// reference that cannot be built is the empty key, which no cycle can learn
// anything about: an item Start refused a claim on had a reference to refuse.
func claimKey(agentID string, w WorkItem) string {
	path, err := ClaimPath(ClaimRef(agentID, w))
	if err != nil {
		return ""
	}
	return path
}

// HeldClaims is the claim of every item this orchestrator's nodes carry that has
// been started: what a cycle hands Refresh, and what the daemon re-stamps on its
// beat (Tick.Held). One Held per claim and not one per item, because two nodes
// that carry the same plan task are one claim for the whole project (ClaimRef):
// they are one reference here, finished when either copy of the item says so.
//
// An item no attempt was committed for holds no claim — a claim is taken by the
// starter, and only a launching action reaches it (launch.go) — so what this
// reads is the work in flight and never a node's whole history. That is the
// whole of the bound a refresh has: by the items the nodes carry, and by the
// state's own retention, and never by every claim the project ever made (this
// file's header).
func HeldClaims(states []State) ([]Held, error) {
	var (
		held  []Held
		index = map[string]int{}
	)
	for _, s := range states {
		for _, w := range s.WorkItems {
			if w.Attempts == 0 && w.RunID == "" {
				continue
			}
			ref := ClaimRef(s.AgentID, w)
			path, err := ClaimPath(ref)
			if err != nil {
				return nil, err
			}
			finished := w.Phase.Done()
			if i, ok := index[path]; ok {
				held[i].Finished = held[i].Finished || finished
				continue
			}
			index[path] = len(held)
			held = append(held, Held{Ref: ref, Finished: finished})
		}
	}
	return held, nil
}
