package orchestrator

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/access"
)

// The shared half of the orchestrator — handing an agent from one developer to
// another, and an agent the project owns rather than a person — needs the
// project's identity and governance, so it lives in ee/ (D1222, F14). This file
// is the seam that half fills, and the seam a build the owner moved a feature
// into reaches the core through: the core carries the vocabulary and a default,
// `ee/register` installs the implementation once, and nothing here names ee/,
// so the core builds and tests with the directory deleted (the guard is
// internal/eeboundary's TestCoreNeverImportsEE).
//
// The default is a refusal and not a no-op, which is the seam's one safety
// property: a handoff moves an agent, and everything under its state node, from
// one person's credential to another's, so a build that does not carry the
// shared half must say it cannot rather than perform a move nothing checked.

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
