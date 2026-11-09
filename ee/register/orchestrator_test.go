// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The orchestrator's two seams, at the one point that sets them (O17, O18):
// what none of the feature's own tests can see — that the functions handed to
// internal/orchestrator.SetHandoff and SetBatch are ee/orchestrator's own
// Transfer and BatchSize and not other functions of the right shape, and that
// the names are taken for good.

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
	admin := access.Input{Grants: []access.Grant{{
		User: "user:admin@example.invalid", Role: access.RoleAdmin, Scope: access.Scope{Kind: access.ScopeProject},
	}}}
	operator := access.Input{Grants: []access.Grant{{
		User: "user:operator@example.invalid", Role: access.RoleOperator, Scope: access.Scope{Kind: access.ScopeProject},
	}}}

	got, err := orchestrator.HandoffAgent(orchestrator.Handoff{
		Agent: agent, To: to, By: "user:admin@example.invalid", Access: admin,
	})
	if err != nil {
		t.Fatalf("the tagged binary refused an admin's handoff (%v): this package registered the core's default rather than ee/orchestrator's Transfer", err)
	}
	if got.ID != agent.ID || got.Owner != to {
		t.Errorf("the tagged binary's handoff answered %+v, want the agent %s held by %s", got, agent.ID, to.Member)
	}

	if moved, err := orchestrator.HandoffAgent(orchestrator.Handoff{
		Agent: agent, To: to, By: "user:operator@example.invalid", Access: operator,
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

// TestTaggedBinaryRaisesABatchForAPerson is the batch seam's identity half, read
// through the seam rather than by comparing functions: a build that compiled
// this package answers a person's raise with the project's own number and
// refuses the daemon's, which is ee/orchestrator.BatchSize's rule and not the
// core default's — the default refuses both raises, so the person's case is what
// catches a registration that installed anything else.
func TestTaggedBinaryRaisesABatchForAPerson(t *testing.T) {
	project := orchestrator.Batch{ProjectCap: 25}

	person := project
	person.Covered, person.By = 25, orchestrator.Person
	got, err := orchestrator.ApprovalBatch(person)
	if err != nil {
		t.Fatalf("the tagged binary refused a person's batch of 25 (%v): this package registered the core's default rather than ee/orchestrator's BatchSize", err)
	}
	if got != 25 {
		t.Errorf("the tagged binary answered a person's batch with %d work items, want the project's 25", got)
	}

	daemon := project
	daemon.Covered, daemon.By = 25, orchestrator.Daemon
	if raised, err := orchestrator.ApprovalBatch(daemon); !errors.Is(err, eeorch.ErrBatchRefused) {
		t.Errorf("the tagged binary answered the daemon's batch with %d, %v, want ee/orchestrator's refusal: a class that acts with nobody watching raises no batch", raised, err)
	}

	// The name is taken here too, for the reason it is on the handoff seam: a
	// rule installed after this package decided one would be a batch the build
	// did not intend.
	if err := orchestrator.SetBatch(orchestrator.BatchFuncs{Size: func(orchestrator.Batch) (int64, error) {
		return 0, nil
	}}); err == nil {
		t.Error("a second SetBatch was accepted: the batch rule a tagged binary carries could be replaced after it was registered")
	}
}
