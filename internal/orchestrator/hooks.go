package orchestrator

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/policy"
)

// The shared half of the orchestrator — handing an agent from one developer to
// another, an agent the project owns rather than a person, and how many work
// items one approval covers — needs the project's identity and governance, so it
// lives in ee/ (D1222, F14). This file is the seam that half fills, and the seam
// a build the owner moved a feature into reaches the core through: the core
// carries the vocabulary and a default, `ee/register` installs the
// implementation once, and nothing here names ee/, so the core builds and tests
// with the directory deleted (the guard is internal/eeboundary's
// TestCoreNeverImportsEE).
//
// The default is a refusal and not a no-op, which is the seam's one safety
// property: a handoff moves an agent, and everything under its state node, from
// one person's credential to another's, so a build that does not carry the
// shared half must say it cannot rather than perform a move nothing checked.
// The batch is the same argument one step weaker — a build without the shared
// half may cover the work items Fugaro ships and no more — and it refuses the
// raise rather than cutting it down, because a caller answered ten for a request
// of twenty-five would approve ten believing it had approved twenty-five.

// Owner is who an orchestrator agent belongs to: one member of the project, or
// the project itself. The zero Owner is the project's, so an agent built
// without one belongs to nobody personally — which is what the spec's shared
// central orchestrators are, "agents owned by the project rather than a person;
// no special handling" — and no reader can read a missing owner as a person it
// should act as.
type Owner struct {
	// Member is the member the agent belongs to, in the spelling the rest of
	// the tree uses ("user:<address>"), or "" when the project owns it.
	Member string
}

// ProjectOwned reports whether the project owns the agent rather than one
// person.
func (o Owner) ProjectOwned() bool { return o.Member == "" }

// Agent is an orchestrator agent as the shared operations see it: the id its
// state node is keyed by (`orchestrator/<id>` in the project's database) and
// who it belongs to.
type Agent struct {
	ID    string
	Owner Owner
}

// PersonIdentity is the member the agent belongs to, or "" when the project
// owns it. A project-owned agent has no person identity: there is no person's
// credential behind it to act as, so a reader that wants one is told there is
// none rather than handed a name nothing stands behind.
func (a Agent) PersonIdentity() string { return a.Owner.Member }

// Handoff is one move of an agent between developers: which agent, who it
// belongs to now, who it moves to, who asked for it and what that person holds.
// To is an Owner, so the move that leaves an agent belonging to nobody — the
// shared central orchestrator — is spelled the same way as a move to a person.
//
// What is given about the person who asked is Access, the resolver's own input,
// and not a role: a role is held at a scope, and an all-repos or a repository
// grant holds no project-level thing (F19), so a bare access.Role here would
// read a repository admin as a project admin and let them move the
// orchestrators of the whole project. The caller reads the input
// (internal/access's Caller, the grants node and the implicit admins — the same
// values every command hands access.Allowed); the rule over the value — only a
// project-scope admin moves an agent — is code on the other side of the seam
// and asks access.Allowed for it, so this value cannot carry a decision its
// caller made up.
type Handoff struct {
	Agent Agent
	To    Owner
	// By is the member who asked for the handoff, in the spelling Grant.User
	// and access.Caller use ("user:<address>").
	By string
	// Access is what the caller resolved about By: the stored grants, the
	// implicit project admins and the published allow_repos.
	Access access.Input
}

// HandoffFuncs is the shared half of the orchestrator: the one operation that
// moves an agent between developers. The core default refuses every handoff, so
// an untagged build has the seam and not the feature.
type HandoffFuncs struct {
	// Transfer performs one handoff, or the refusal that stops it.
	Transfer func(Handoff) (Agent, error)
}

// handoffHooks is the installed pair. Only ee/register may call SetHandoff: the
// core never sets it, so an untagged build refuses every handoff.
var handoffHooks = coreHandoffFuncs()

// handoffHooksSet records that a caller installed one, which only SetHandoff
// does.
var handoffHooksSet bool

// SetHandoff installs the shared half, refusing a second call: what a build
// carries is decided once, by ee/register, and a hook that could be replaced
// after it would be a way to install a handoff rule the build did not intend
// (design ee-boundary-moves.md §5.5). It refuses a nil Transfer for the reason
// the policy hook refuses a nil half — HandoffAgent calls it on every handoff,
// and a nil one is a panic rather than a refusal.
func SetHandoff(f HandoffFuncs) error {
	if handoffHooksSet {
		return errors.New("orchestrator: the handoff hook is already set")
	}
	if f.Transfer == nil {
		return errors.New("orchestrator: the handoff hook needs Transfer")
	}
	handoffHooks, handoffHooksSet = f, true
	return nil
}

// HandoffAgent moves an agent between developers through the build's seam. It
// is the one way a handoff runs, so a build that carries the shared half cannot
// be bypassed by a caller reaching for the implementation itself.
func HandoffAgent(h Handoff) (Agent, error) { return handoffHooks.Transfer(h) }

// coreHandoffFuncs is the untagged build's shared half: every handoff refused,
// because the rule that guards one — an owner or an admin, over the project's
// own governance — is ee/'s. A build that links ee/register replaces this with
// the one ee/orchestrator carries (D1222, F14).
func coreHandoffFuncs() HandoffFuncs {
	return HandoffFuncs{
		Transfer: func(Handoff) (Agent, error) {
			return Agent{}, fmt.Errorf("%w: handing an agent to another developer needs the shared half this build does not carry", ErrActorNotAllowed)
		},
	}
}

// Batch is what one approval would cover: how many work items the approval
// names at once, the ceiling the project's own guardrails put on that number,
// and the class of the actor that would land it.
//
// An approval is one person's go for work items, and how many one go may cover
// is a project's setting rather than the person's: the spec states it as
// "Batch approval | Project setting, default 50 tasks in one batch; can be
// raised or disabled", and D1239 answers it with the conservative ten Fugaro
// ships. So the value carries the number the project's policy resolved — which
// is a fact about the installation the caller reads — beside the class of the
// actor, and the rule over the two is the shared half's: it is the project's
// governance that says who may raise a batch, which is what ee/ carries.
type Batch struct {
	// Covered is how many work items the approval names at once. Zero means
	// nobody said, and one approval covers the project's own setting.
	Covered int64
	// ProjectCap is the project's setting for how many work items one
	// approval covers, as its policy resolved it (the orchestrator block's
	// batch_size). Zero means the project wrote none, and the ceiling is the
	// ten Fugaro ships (policy.DefaultBatchSize, D1239).
	ProjectCap int64
	// By is the class of the actor that would land the approval. Only Person
	// raises one above the shipped default (G2): the other three act with
	// nobody watching, and a batch nobody read is not a decision a person
	// made.
	By Class
}

// BatchFuncs is the shared half's second operation: how many work items one
// approval covers. The core default answers the shipped default and refuses
// every raise, so an untagged build carries the seam and not the feature.
type BatchFuncs struct {
	// Size answers how many work items one approval covers, or the refusal
	// that stops it. A refusal answers zero with the error, the way Transfer
	// answers no agent: a caller that read a number off a refused call would
	// approve a batch nothing allowed.
	Size func(Batch) (int64, error)
}

// batchHooks is the installed half. Only ee/register may call SetBatch: the
// core never sets it, so an untagged build raises no batch.
var batchHooks = coreBatchFuncs()

// batchHooksSet records that a caller installed one, which only SetBatch does.
var batchHooksSet bool

// SetBatch installs the shared half's batch rule, refusing a second call for
// the reason SetHandoff does — what a build carries is decided once, by
// ee/register, and a rule that could be replaced afterwards would be a way to
// install a batch the build did not intend (§5.5) — and refusing a nil Size,
// which is a panic on the first approval rather than a refusal.
func SetBatch(f BatchFuncs) error {
	if batchHooksSet {
		return errors.New("orchestrator: the batch hook is already set")
	}
	if f.Size == nil {
		return errors.New("orchestrator: the batch hook needs Size")
	}
	batchHooks, batchHooksSet = f, true
	return nil
}

// ApprovalBatch answers how many work items one approval covers through the
// build's seam. It is the one way the question is asked, so a build that
// carries the shared half cannot be bypassed by a caller reaching for the rule
// itself.
func ApprovalBatch(b Batch) (int64, error) { return batchHooks.Size(b) }

// coreBatchFuncs is the untagged build's batch rule: the project's own setting,
// the shipped default when it wrote none, and no raise above that default at
// all — what a person may raise is ee/'s rule, over the project's governance
// (D1222, F14). A build that links ee/register replaces this with the one
// ee/orchestrator carries. A request above the ceiling is refused rather than
// cut down: an answer of ten to a request of twenty-five is a caller approving
// ten work items believing it approved twenty-five.
func coreBatchFuncs() BatchFuncs {
	return BatchFuncs{
		Size: func(b Batch) (int64, error) {
			ceiling := b.ProjectCap
			if ceiling <= 0 {
				ceiling = policy.DefaultBatchSize
			}
			if b.Covered <= 0 {
				return ceiling, nil
			}
			if b.Covered > ceiling {
				return 0, fmt.Errorf("%w: %d work items in one approval is above the project's %d, and an approval never widens the project's cap", ErrActorNotAllowed, b.Covered, ceiling)
			}
			if b.Covered > policy.DefaultBatchSize {
				return 0, fmt.Errorf("%w: one approval covers at most the %d work items Fugaro ships, and raising a batch needs the shared half this build does not carry", ErrActorNotAllowed, policy.DefaultBatchSize)
			}
			return b.Covered, nil
		},
	}
}
