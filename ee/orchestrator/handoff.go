// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// ErrHandoffRefused is a handoff this package turned down: no owner or admin
// asked for it, or the request is not one this tree can act on. It is a value
// so a caller can tell a refusal from a failure to reach whatever holds the
// agent — the first is a person's answer, the second is a re-read.
var ErrHandoffRefused = errors.New("ee/orchestrator: only an owner or an admin may hand an agent to another developer")

// Transfer moves an agent from the developer who holds it to another one, or to
// the project, and answers the agent as it stands afterwards. It is the
// implementation ee/register installs in the core's handoff seam
// (internal/orchestrator.SetHandoff), and the whole of the rule the spec states
// for it: "Handoff: owner or admin transfers an agent to another developer".
//
// The check is the fence, and it is a rule over the value given rather than a
// read of anything: the caller resolved what the person holds
// (internal/access), and this asks the tree's own gate for the act — a
// project-scope admin, which is the standing a handoff needs.
//
// It asks access.Allowed for a project-level action and not for a role,
// because a role on its own does not carry the scope it is held at: admin of
// every repository the policy admits and admin of one repository are both
// "admin", and neither holds a project-level thing (F19). An agent is the
// project's — its state node is the project's database's — so handing one over
// is project work, and a repository admin who could move it would be able to
// take an agent out of a project they only hold a repository in. So the
// all-repos and the repository admin are refused, the viewer and the operator
// are refused, an implicit project admin (the GCP owners and editors) is
// allowed, and a grant whose role or scope does not parse grants nothing and is
// refused with it. owner and admin are one standing here for the same reason
// internal/access spells no "owner": the project's GCP owners resolve to a
// project admin through the implicit list.
//
// A request that names no agent, or nobody who asked for it, is refused the
// same way: neither is a move, and a handoff recorded against an empty member
// would be one nobody could be held to.
func Transfer(h orchestrator.Handoff) (orchestrator.Agent, error) {
	if h.Agent.ID == "" {
		return orchestrator.Agent{}, fmt.Errorf("%w: the request names no agent", ErrHandoffRefused)
	}
	if h.By == "" {
		return orchestrator.Agent{}, fmt.Errorf("%w: the request names no one who asked for it", ErrHandoffRefused)
	}
	if !access.Allowed(h.Access, h.By, access.ActionHandoffAgent, "") {
		return orchestrator.Agent{}, fmt.Errorf("%w: %q does not hold admin at project scope", ErrHandoffRefused,
			safetext.OneLine(h.By))
	}
	return orchestrator.Agent{ID: h.Agent.ID, Owner: h.To}, nil
}
