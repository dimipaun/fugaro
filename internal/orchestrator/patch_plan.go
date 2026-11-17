// Approving a whole imported plan in one op (G2, D2706, D2717, D2738).
//
// Every work item needs a person's own go (D1238), and a plan of twenty tasks
// would otherwise need twenty patches, each with its own read of the node and
// its own typed confirmation (F10). This op is the one approval a plan gets:
// it carries the items the import wrote — their ids, and the digests the person
// read — and approves exactly those.
//
// Carrying the ids is what makes the approval scoped, and the amendment D2706
// records is the whole reason: an op that named a plan and approved everything
// tagged with it would approve an item a session tagged with the plan's name
// after the import, which is not an item the person ever read (D2738). So the
// list is the scope, the node is the node those items are of, and an item
// whose digest has moved since the person read it is skipped rather than
// approved — an approval binds the spec that was read, the rule applyApprove
// already follows for one item.
//
// The op is person-only, like the single approval it stands for, and for the
// same reason: a session can read an item and compute a digest, so nothing but
// the class of the actor keeps an agent from approving its own work.

package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// MaxPlanApproveItems bounds one plan approval. It is the node's own item cap:
// a node holds at most MaxWorkItems items, so a list longer than that names
// something no node of this release can carry, and the bound is what keeps a
// patch from being a document nobody read.
const MaxPlanApproveItems = MaxWorkItems

// PlanApproveOp is a person's go for the items of one node — an imported
// plan's, or the standing backlog's (D2717). Node names which: a plan's folder
// under docs/, as the import wrote it on the items, or `backlog` for the node
// whose items carry no spec at all.
type PlanApproveOp struct {
	Node  string            `json:"node"`
	Items []PlanApproveItem `json:"items"`
}

// PlanApproveItem is one item of the node, at the digest the person was shown.
// Both halves are the import's own answer — it prints the list `--json` and
// this op carries it back — so the approval covers the items and the specs the
// person read and nothing else.
type PlanApproveItem struct {
	ID     string `json:"id"`
	Digest Digest `json:"digest"`
}

// applyPlanApprove approves the items the op lists, at the digests it carries.
//
// Three things leave an item unapproved, and none of them fails the patch: the
// node may no longer carry it, it may not be an item of the node the op names,
// or its spec may have moved since the person read it. The last of those is
// recorded as one fact naming the item, so that a person who approved a plan
// whose re-import changed a task learns which task did not go rather than
// finding out when it never launches (the shape D2740 gives the same
// situation). The first two are silent: an id the node does not carry is not a
// question about the item, and an item of another node is one this op was
// never about.
//
// The op is all-or-nothing in the one direction that matters: nothing here
// refuses the patch, so one moved item cannot hold up the nineteen that did
// not move.
func applyPlanApprove(s *State, op PlanApproveOp, a Actor) error {
	if a.Class != Person {
		return fmt.Errorf("%w: a %s may not approve a plan's items", ErrActorNotAllowed, a.Class)
	}
	if a.Name == "" {
		return fmt.Errorf("%w: an approval names the person who gave it", ErrInvalid)
	}
	node, ok := planApproveNode(op.Node)
	if !ok {
		return fmt.Errorf("%w: %q is not a plan folder or the backlog", ErrInvalid, safetext.OneLine(op.Node))
	}
	if len(op.Items) == 0 {
		return fmt.Errorf("%w: an approval of %s carries the items it approves", ErrInvalid, safetext.OneLine(node))
	}
	at := stamp(a.Now)
	by := cleanLine(a.Name, MaxActorNameRunes)
	for _, item := range op.Items {
		i, ok := s.indexOf(item.ID)
		if !ok {
			continue
		}
		w := &s.WorkItems[i]
		if !itemOfNode(*w, node) {
			continue
		}
		if SpecDigest(*w) != item.Digest {
			recordPlanApproveSkip(s, item.ID, a.Class)
			continue
		}
		w.Approved = &Approval{By: by, At: at, SpecDigest: item.Digest, Attempts: ApprovalAttempts}
		// The spent marker belongs to the approval it was spent on, so a new
		// one clears it — applyApprove's rule, for the same reason.
		w.SpentApprovalAt = nil
	}
	return nil
}

// planApproveNode is the node an approval names, as this package spells it: the
// standing backlog's own name, or a plan folder folded the way a work item's
// spec is (specName), because the two name the same plan — an item's spec is
// the folder the import read (shard.go).
func planApproveNode(name string) (string, bool) {
	if name == BacklogNode {
		return BacklogNode, true
	}
	return specName(name)
}

// itemOfNode reports whether an item is one of a node's: a plan's items are
// the ones tagged with its folder, and the backlog's are the ones tagged with
// nothing at all (D2715). Apply is handed one node's document and cannot see
// the others, so the node an op names is checked against the items themselves —
// which is what stops a patch that names one node from approving another's
// items.
func itemOfNode(w WorkItem, node string) bool {
	if node == BacklogNode {
		return w.Spec == ""
	}
	return w.Spec == node
}

// planApproveSkipFactKey is the fact key of one item a plan approval skipped.
// A fact key is a name (idRE) and an item id is one, but the two together
// would be over the key's own bound, so the key carries a digest of the id and
// the value names the item in full — the shape mergeFactKey takes, and for the
// same reason.
func planApproveSkipFactKey(itemID string) string {
	sum := sha256.Sum256([]byte(itemID))
	return "plan_approve_skipped_" + hex.EncodeToString(sum[:8])
}

// The skipped item's sentence, in two halves with the item between them. The
// item is clipped to what is left of the value's cap rather than trusted to
// fit: an id longer than this document accepts is still one a patch may carry,
// and an over-long value would fail the patch that records it — which would
// turn a moved digest into an approval that cannot land at all.
const (
	planApproveSkipHead = "the approval skipped "
	planApproveSkipTail = ": its spec changed since it was read, so approve the item as it stands now"
	// planApproveSkipItemRunes is what is left of a fact's value for the item.
	planApproveSkipItemRunes = MaxFactValueRunes - len(planApproveSkipHead+planApproveSkipTail)
)

// recordPlanApproveSkip writes the one fact an item whose digest moved leaves.
//
// A node at its fact bound is a node this cannot land on: the state that
// carries one fact more is the state ValidateState refuses, and the refusal
// would take the whole patch with it — the nineteen approvals beside the one
// skip. The approvals are what the person asked for and the fact is the
// diagnostic, so the fact is what is dropped when it does not fit; the item is
// skipped either way, and the next approval of it records the fact if the node
// has room by then (mergeRefusal's rule, for the same reason).
func recordPlanApproveSkip(s *State, itemID string, by Class) {
	key := planApproveSkipFactKey(itemID)
	if _, ok := s.Facts[key]; !ok && len(s.Facts) >= MaxFacts {
		return
	}
	if s.Facts == nil {
		s.Facts = map[string]Fact{}
	}
	s.Facts[key] = Fact{
		Value: planApproveSkipHead + safetext.Clip(safetext.OneLine(itemID), planApproveSkipItemRunes) + planApproveSkipTail,
		By:    by,
	}
}
