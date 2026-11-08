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
// The role check is the fence, and it is a rule over the value given rather
// than a read of anything: the caller resolved the person's role
// (internal/access), and this is the standing a handoff needs. owner and admin
// are one standing here, because internal/access resolves the project's GCP
// owners and editors to RoleAdmin at project scope, and it spells no "owner":
// the check is admin's rank, so a viewer and an operator are refused, and so is
// a word outside the closed set, because access.Role.AtLeast answers false for
// anything that is not a role.
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
	if !h.Role.AtLeast(access.RoleAdmin) {
		return orchestrator.Agent{}, fmt.Errorf("%w: %q holds %q", ErrHandoffRefused,
			safetext.OneLine(h.By), safetext.OneLine(string(h.Role)))
	}
	return orchestrator.Agent{ID: h.Agent.ID, Owner: h.To}, nil
}
