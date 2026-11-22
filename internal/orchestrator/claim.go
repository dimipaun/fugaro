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
// the server time it claimed at, and a claim is held while that holder is
// driving: an orchestrator whose lease node still carries a recent beat (the
// lease's own freshness rule) holds its claims, one whose beat lapsed less than
// ClaimGrace ago still holds them, and one whose beat lapsed further back has
// gone — its claims are any orchestrator's to take. Nothing here reads a clock
// of its own: the stamp is rtdb.ServerNow's, and a claim with no server time
// behind it is not made at all (D1230).
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

// Claim is one orchestrator's claims, read and written through one store.
// agentID is the orchestrator that takes them — the id whose state node the
// items belong to and whose lease decides whether its claims outlive it.
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
// a claim is kept alive by its holder's lease and not by rewriting its node, so
// an id that claims an item every cycle moves no ETag a reader of this subtree
// would have to re-read. A claim whose holder is still driving is answered
// false and nothing is written (Held, by the one caller that matters), and one
// whose holder has gone — no lease node, or a beat lapsed past the grace — is
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
			live, err := c.holderLive(ctx, doc.Holder)
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
// An absent lease node is a lapsed claim and not a holder inside its grace: an
// orchestrator that has gone releases its lease on the way out (Lease.Release
// deletes the node) and starts no run afterwards, so there is nothing to wait
// for. A lease node this release does not model is the opposite reading — a
// live holder — because a holder that cannot be read must not be taken for a
// dead one.
func (c *Claim) holderLive(ctx context.Context, holder string) (bool, error) {
	path, err := claimLeasePath(holder)
	if err != nil {
		return false, err
	}
	var raw json.RawMessage
	_, found, err := c.db.GetETag(ctx, path, &raw)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	doc, err := decodeStrict[leaseDoc](raw)
	if err != nil {
		return true, nil
	}
	if !idRE.MatchString(doc.Holder) {
		return true, nil
	}
	now, ok := c.now()
	if !ok {
		return false, ErrNoServerTime
	}
	at := stamp(doc.At)
	switch {
	case at.Sub(now) > LeaseSkew:
		// A stamp further ahead than LeaseSkew is not a reading of the server's
		// clock, so it holds nothing (the lease's own rule): no stamp keeps an
		// item out of every rival's hands for ever.
		return false, nil
	case now.Sub(at) < LeaseTTL:
		return true, nil
	default:
		return now.Sub(at) < LeaseTTL+ClaimGrace, nil
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
