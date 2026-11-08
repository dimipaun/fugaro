// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The handoff seam, at the one point that sets it (O17): what none of the
// feature's own tests can see — that the function handed to
// internal/orchestrator.SetHandoff is
// ee/orchestrator's own Transfer and not some other function of the right
// shape, and that the name is taken for good.

import (
	"errors"
	"testing"

	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestTaggedBinaryMovesAnAgent is the identity half, read through the seam
// rather than by comparing functions (which Go cannot do): a build that
// compiled this package moves an agent for an admin and refuses an operator,
// which is ee/orchestrator.Transfer's rule and not the core default's — the
// default refuses an admin too, so a registration that installed anything else
// is what the allowed case catches.
func TestTaggedBinaryMovesAnAgent(t *testing.T) {
	agent := orchestrator.Agent{ID: "agent-a", Owner: orchestrator.Owner{Member: "user:holder@example.invalid"}}
	to := orchestrator.Owner{Member: "user:taker@example.invalid"}

	got, err := orchestrator.HandoffAgent(orchestrator.Handoff{
		Agent: agent, To: to, By: "user:admin@example.invalid", Role: access.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("the tagged binary refused an admin's handoff (%v): this package registered the core's default rather than ee/orchestrator's Transfer", err)
	}
	if got.ID != agent.ID || got.Owner != to {
		t.Errorf("the tagged binary's handoff answered %+v, want the agent %s held by %s", got, agent.ID, to.Member)
	}

	if moved, err := orchestrator.HandoffAgent(orchestrator.Handoff{
		Agent: agent, To: to, By: "user:operator@example.invalid", Role: access.RoleOperator,
	}); !errors.Is(err, eeorch.ErrHandoffRefused) {
		t.Fatalf("the tagged binary answered an operator's handoff with %+v, %v, want ee/orchestrator's refusal: a viewer or an operator moves no agent", moved, err)
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{Transfer: func(orchestrator.Handoff) (orchestrator.Agent, error) {
		return orchestrator.Agent{}, nil
	}}); err == nil {
		t.Error("a second SetHandoff was accepted: the handoff rule a tagged binary carries could be replaced after it was registered")
	}
}
