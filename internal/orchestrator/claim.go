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
// **The claim's liveness is its holder's lease.** The node names the holder and
// the server time it claimed at or last re-stamped that claim at, and a claim
// is held while that holder is driving: an orchestrator whose lease node still
// carries a recent beat (the lease's own freshness rule) holds its claims, one
// whose beat lapsed less than ClaimGrace ago still holds them, and one whose
// beat lapsed further back has gone — its claims are any orchestrator's to
// take. Nothing here reads a clock of its own: the stamp is rtdb.ServerNow's,
// and a claim with no server time behind it is not made at all (D1230).
//
// A lease node that is **not there at all** is not a holder that has gone: a
// daemon deletes its lease on every clean exit (Lease.Release — an upgrade
// restart, `--once`, a stop) while the runs it started are still in flight, so
// the grace then runs from the claim's own stamp, which the holder re-writes on
// every beat for as long as it is driving (Refresh). That is what keeps a
// restarted daemon's items its own: without it a rival that retried in the gap
// between the old daemon's exit and the new daemon's Acquire would take an item
// whose run is still going and start a second run of it.
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
	// ClaimGrace is how long a claim outlives its holder's lease: a holder
	// whose lease lapsed less than this ago is still taken to be driving, so
	// that a daemon whose beat was missed by a slow tick or a request that
	// timed out does not lose the items it is working to a rival. Past it the
	// holder has been gone for a lease and a grace — long enough that a laptop
	// lid, a restarted daemon or a dropped request has had its turn to come
	// back — and the claim is another orchestrator's to take.
	ClaimGrace = LeaseTTL

	// claimAttempts bounds the re-read a lost claim race is answered with, the
	// way leaseAttempts bounds a lost lease race: a take, a takeover and a
	// release re-read and re-decide this many times and then report
	// ErrClaimContention.
	claimAttempts = leaseAttempts
)

// ErrClaimContention is a claim write refused because the node moved under
// every bounded attempt. The caller decides that a rival orchestrator is
// working the item and acts on nothing.
var ErrClaimContention = errors.New("orchestrator: the claim node moved under every attempt")

// claimDoc is the claim node's shape on the wire: the orchestrator that holds
// the item and the server time it claimed at. It is the lease's shape (Holder,
// At) because it is read and written by the same rules, so a reader of this
// subtree reads one idiom and not two.
type claimDoc struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}

// ClaimGuard is the project's claims as one orchestrator holds them: what a
// Starter takes before it starts an item's work (launch.go) and what a cycle
// releases once the item is finished (SettledClaims). It is an interface for
// the reason every fence in this package is one — a test holds a daemon to a
// claim it controls — and nil is a process that keeps no claims: the resident
// pass, which makes no write to the node it drives and starts only the attempts
// a person's patch already committed (D1223, D1241).
type ClaimGuard interface {
	// Take takes the claim of ref for this orchestrator, or answers false when
	// another orchestrator holds it.
	Take(ctx context.Context, ref ItemRef) (bool, error)
	// Release drops this orchestrator's own claim on ref, and answers false
	// when the claim is not its to drop.
	Release(ctx context.Context, ref ItemRef) (bool, error)
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
// moves no ETag a reader of this subtree would have to re-read. A claim whose
// holder is still driving is answered false and nothing is written (Held, by
// the one caller that matters), and one whose holder has gone — a beat lapsed
// past the grace, or no lease node and a claim stamped past the grace — is
// taken over under the ETag this read carries.
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
			if doc.Holder == c.agentID {
				return true, nil
			}
			live, err := c.holderLive(ctx, doc)
			if err != nil {
				return false, err
			}
			if live {
				return false, nil
			}
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

// Release drops this orchestrator's own claim on ref: the item is done or
// cancelled and no orchestrator's claim belongs on it any more.
//
// Only its own claim is released. A node naming another orchestrator is left
// alone (false, nil) — releasing a rival's claim is how two orchestrators end
// up on one item — and a node that is not there at all is the same answer.
func (c *Claim) Release(ctx context.Context, ref ItemRef) (bool, error) {
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
		if !found || doc.Holder != c.agentID {
			return false, nil
		}
		err = c.db.PutIfMatch(ctx, path, etag, nil)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return false, err
		}
		last = err
	}
	return false, fmt.Errorf("%w: %d releases, each refused by a moved ETag: %w", ErrClaimContention, claimAttempts, last)
}

// Refresh re-stamps the claims this orchestrator holds with the server's
// current time, and is what makes a claim's own At the stamp a rival measures a
// missing lease node against (holderLive): a daemon that is driving keeps the
// stamp of every claim it holds inside the beat's own cadence, so the grace
// runs from when its holder last drove rather than from the day it took the
// item.
//
// It is the beat and not the take, because an item's run outlives its take: a
// daemon takes a claim once, when it starts the run, and a run of hours is a
// claim whose take is hours old — while the state this exists for is exactly
// the gap Lease.Release leaves, where the lease node is gone and the runs that
// daemon started are not. A beat that is missed by one slow cycle is inside the
// grace, which is what the grace is for.
//
// A node that has moved is not taken back: one naming another orchestrator is
// left alone (a rival that took an item this daemon went away from holds it
// now), one this release cannot read is left for Take to refuse rather than
// repaired by a beat, and one that is not there is nothing to re-stamp. A
// conditional write refused because the node moved under the read is the same
// answer, so a beat never fails over a claim that has become somebody else's.
func (c *Claim) Refresh(ctx context.Context) error {
	keys, err := c.db.Keys(ctx, ClaimsRoot)
	if err != nil {
		return err
	}
	for _, key := range keys {
		path := ClaimsRoot + "/" + key
		if key == budget.Key(c.agentID) {
			// This orchestrator's own work items, one node each (ClaimPath).
			items, err := c.db.Keys(ctx, path)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := c.restamp(ctx, path+"/"+item); err != nil {
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
		if err := c.restamp(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

// restamp rewrites one claim node with this orchestrator's own holder and the
// server's current time, and changes nothing unless the node names this
// orchestrator: a refresh is not a takeover.
func (c *Claim) restamp(ctx context.Context, path string) error {
	etag, doc, found, err := c.read(ctx, path)
	if err != nil || !found || doc.Holder != c.agentID {
		return nil
	}
	at, err := c.stamped()
	if err != nil {
		return err
	}
	err = c.db.PutIfMatch(ctx, path, etag, claimDoc{Holder: c.agentID, At: at})
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

// holderLive reports whether the orchestrator a claim names is still driving
// the project: it reads that orchestrator's lease node and applies the lease's
// own freshness rule, with ClaimGrace after it.
//
// An absent lease node is measured from the claim's own stamp instead, and it
// is not a holder that has gone: a daemon deletes its lease node on every clean
// exit (Lease.Release) while the runs it started are still in flight, and it
// re-stamps every claim it holds on each beat until it stops (Refresh), so that
// stamp is when that holder was last known to be driving. The grace runs from
// there — a daemon that restarts takes its lease back inside it and keeps its
// items, and one that is gone for good loses them a grace after it stopped.
// Reading the absent node as "gone at once" is what would let a rival that
// retried every tick take an item out of the gap between an exit and the next
// Acquire, and start a second run of a task whose run is still going.
//
// A lease node this release does not model is the opposite reading — a live
// holder — because a holder that cannot be read must not be taken for a dead
// one.
func (c *Claim) holderLive(ctx context.Context, claimed claimDoc) (bool, error) {
	path, err := claimLeasePath(claimed.Holder)
	if err != nil {
		return false, err
	}
	var raw json.RawMessage
	_, found, err := c.db.GetETag(ctx, path, &raw)
	if err != nil {
		return false, err
	}
	// at is the stamp the freshness is measured from and grace how long past it
	// the holder is still taken to be driving: the holder's own last beat, with
	// the lease's TTL and grace after it, while its lease node stands, and the
	// claim's own stamp with ClaimGrace after it when the node does not.
	at, grace := stamp(claimed.At), ClaimGrace
	if found {
		lease, err := decodeStrict[leaseDoc](raw)
		if err != nil {
			return true, nil
		}
		if !idRE.MatchString(lease.Holder) {
			return true, nil
		}
		at, grace = stamp(lease.At), LeaseTTL+ClaimGrace
	}
	now, ok := c.now()
	if !ok {
		return false, ErrNoServerTime
	}
	switch {
	case at.Sub(now) > LeaseSkew:
		// A stamp further ahead than LeaseSkew is not a reading of the server's
		// clock, so it holds nothing (the lease's own rule): no stamp keeps an
		// item out of every rival's hands for ever.
		return false, nil
	case now.Sub(at) < LeaseTTL:
		return true, nil
	default:
		return now.Sub(at) < grace, nil
	}
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

// claimLeasePath is the lease node of the orchestrator a claim names, read to
// decide whether that orchestrator is still driving. It is spelled from the id
// here rather than through PathLease because checkAgentID still holds every id
// to agentRE — which carries no `~` — until CG1a lands
// (docs/console-fixes/contract-orchestrators.md, "What this contract does not
// yet do"): a named orchestrator's claim would otherwise be unreadable to the
// very claimant deciding whether to skip the item.
func claimLeasePath(holder string) (string, error) {
	if !ValidOrchestratorID(holder) {
		return "", fmt.Errorf("%w: %q is not an orchestrator id", ErrInvalid, safetext.OneLine(holder))
	}
	return SubtreeRoot + "/" + budget.Key(holder) + "/" + LeaseNode, nil
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

// SettledClaims releases this orchestrator's claims on the items a node has
// moved into a finished phase, and answers how many it released. before is the
// node as a cycle read it and after as it stands once the cycle's writes have
// landed.
//
// It is the other half of the claim's life: a plan task or a backlog item that
// is done or cancelled is work nobody is on any more, so the claim comes off
// and the project's other orchestrators may take the item the next time one of
// them has it in front of them. Only the items that *moved* are released, and
// that is what keeps the step off the read of every item a plan has ever
// finished: a node that finished its work in an earlier cycle released its
// claims then, and a claim left standing by a cycle that died is still released
// by the holder's lease lapsing (Take).
//
// A nil guard is a process that keeps no claims and releases none.
func SettledClaims(ctx context.Context, claims ClaimGuard, before, after State) (int, error) {
	if claims == nil {
		return 0, nil
	}
	was := map[string]bool{}
	for _, w := range before.WorkItems {
		if !w.Phase.Done() {
			continue
		}
		key, err := ClaimPath(ClaimRef(before.AgentID, w))
		if err != nil {
			return 0, err
		}
		was[key] = true
	}
	released := 0
	for _, w := range after.WorkItems {
		if !w.Phase.Done() {
			continue
		}
		ref := ClaimRef(after.AgentID, w)
		key, err := ClaimPath(ref)
		if err != nil {
			return released, err
		}
		if was[key] {
			continue
		}
		ok, err := claims.Release(ctx, ref)
		if err != nil {
			return released, err
		}
		if ok {
			released++
		}
	}
	return released, nil
}
