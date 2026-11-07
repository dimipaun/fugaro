package orchestrator

import (
	"errors"
	"time"
)

// The refusals a patch meets. They are values so a caller can tell the cases
// apart: a stale revision is a re-read and a retry, a refusal by class is not,
// and a caller that treats the two the same either loops or gives up early.
var (
	ErrStaleRev        = errors.New("orchestrator: the patch was built from another revision")
	ErrUnknownOp       = errors.New("orchestrator: unknown op")
	ErrOpPayload       = errors.New("orchestrator: the op does not carry the payload its kind takes")
	ErrActorNotAllowed = errors.New("orchestrator: this class of actor may not do that")
	ErrNotFound        = errors.New("orchestrator: no such work item, decision or fact")
	ErrDigestMismatch  = errors.New("orchestrator: the digest is not the item's current spec digest")
	ErrSchemaVersion   = errors.New("orchestrator: the state carries a schema version this release does not write")
	ErrTooLarge        = errors.New("orchestrator: the state is over its size target")
	ErrInvalid         = errors.New("orchestrator: the state or the patch is not shaped as this release reads it")
	ErrIntentDeclared  = errors.New("orchestrator: the intent is already declared")
)

// Patch is one typed change to a state. It carries the revision it was built
// from, so a patch built against a state somebody else has since changed is
// refused rather than landing on top of it, and the ops in order.
type Patch struct {
	BaseRev int64 `json:"base_rev"`
	Ops     []Op  `json:"ops"`
}

// OpKind is an op's name. The vocabulary is closed and small on purpose: an
// agent never writes state, it can only ask for one of these, and code
// decides whether it may (G1).
type OpKind string

const (
	OpWorkItemUpsert  OpKind = "work_item_upsert"
	OpWorkItemApprove OpKind = "work_item_approve"
	OpWorkItemArchive OpKind = "work_item_archive"
	OpDecisionAdd     OpKind = "decision_add"
	OpDecisionResolve OpKind = "decision_resolve"
	OpFactSet         OpKind = "fact_set"
	OpFactDelete      OpKind = "fact_delete"
	OpGoalSet         OpKind = "goal_set"
)

// OpKinds is the whole vocabulary.
var OpKinds = []OpKind{
	OpWorkItemUpsert, OpWorkItemApprove, OpWorkItemArchive,
	OpDecisionAdd, OpDecisionResolve, OpFactSet, OpFactDelete, OpGoalSet,
}

// Known reports whether k is one of the eight ops.
func (k OpKind) Known() bool {
	for _, o := range OpKinds {
		if o == k {
			return true
		}
	}
	return false
}

// Op is one op: its kind and the payload that kind takes. Every field is a
// pointer so that "absent" and "set to the zero value" are different things:
// an upsert changes what it carries and leaves the rest alone, which is how
// the daemon moves a phase without rewriting — and re-digesting — a brief.
type Op struct {
	Op       OpKind      `json:"op"`
	WorkItem *WorkItemOp `json:"work_item,omitempty"`
	Approve  *ApproveOp  `json:"approve,omitempty"`
	Archive  *ArchiveOp  `json:"archive,omitempty"`
	Decision *DecisionOp `json:"decision,omitempty"`
	Resolve  *ResolveOp  `json:"resolve,omitempty"`
	Fact     *FactOp     `json:"fact,omitempty"`
	Goal     *GoalOp     `json:"goal,omitempty"`
}

// WorkItemOp is what a work_item_upsert carries: the declaration a person or
// a session writes, and the bookkeeping only the daemon may write. There is
// no approval field: an approval comes from work_item_approve and from
// nothing else, because the digest it binds is computable by anyone who can
// read the item.
type WorkItemOp struct {
	ID        string    `json:"id"`
	Title     *string   `json:"title,omitempty"`
	Repo      *string   `json:"repo,omitempty"`
	Recipe    *string   `json:"recipe,omitempty"`
	RigorTier *string   `json:"rigor_tier,omitempty"`
	Brief     *string   `json:"brief,omitempty"`
	DependsOn *[]string `json:"depends_on,omitempty"`
	Note      *string   `json:"note,omitempty"`

	Phase           *Phase     `json:"phase,omitempty"`
	RunID           *string    `json:"run_id,omitempty"`
	SpentApprovalAt *time.Time `json:"spent_approval_at,omitempty"`
	Attempts        *int       `json:"attempts,omitempty"`
	Intent          *IntentOp  `json:"intent,omitempty"`
}

// IntentOp declares the attempt a launch is for, or records how one ended.
// The declaration carries the run id minted before the start, the digest of
// the spec being launched and the At of the approval it spends; a later
// upsert carrying only Outcome records what the run reported.
type IntentOp struct {
	ID         string     `json:"id"`
	RunID      *string    `json:"run_id,omitempty"`
	SpecDigest *Digest    `json:"spec_digest,omitempty"`
	ApprovalAt *time.Time `json:"approval_at,omitempty"`
	Outcome    *Outcome   `json:"outcome,omitempty"`
}

// ApproveOp is a person's go for one work item. It carries the digest the
// person was shown, and Apply refuses it unless that is still the item's
// digest: the approval covers the spec that was read, not whatever the item
// has become.
type ApproveOp struct {
	ID         string `json:"id"`
	SpecDigest Digest `json:"spec_digest"`
}

// ArchiveOp moves a work item out of the work list and into the archived
// list, with the outcome it ended with.
type ArchiveOp struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

// DecisionOp raises a question and names the class that has to answer it.
type DecisionOp struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	WaitingOn On     `json:"waiting_on"`
}

// ResolveOp answers a question. Who answered is the actor, not a field: the
// class in the patch is checked against the class the decision waits on.
type ResolveOp struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

// FactOp sets or deletes one fact. A set needs a value, a delete must not
// carry one, so a patch that means one and carries the other is refused
// instead of guessed at.
type FactOp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GoalOp is the orchestrator's goal, which only a person sets: it is the
// standing instruction every loop of the daemon is measured against.
type GoalOp struct {
	Goal string `json:"goal"`
}

// Actor is who sent a patch: the class that decides what the patch may do,
// a name for the record, and the server time the caller read before building
// the patch. Stamps come from here and never from an op's own fields, so a
// caller cannot backdate an approval or an intent.
type Actor struct {
	Class Class
	Name  string
	Now   time.Time
}

// Apply returns the state a patch makes of another one, or the error that
// stops it. It is a pure function: the same state, patch and actor always
// give the same answer, which is what the money path's tests rest on.
func Apply(s State, p Patch, a Actor) (State, error) {
	next := s.Clone()
	for _, op := range p.Ops {
		switch op.Op {
		case OpDecisionResolve:
			for i := range next.Decisions {
				if next.Decisions[i].ID == op.Resolve.ID {
					at := a.Now
					next.Decisions[i].Answer = op.Resolve.Answer
					next.Decisions[i].ResolvedAt = &at
				}
			}
		}
	}
	next.Rev = s.Rev + 1
	return next, nil
}
