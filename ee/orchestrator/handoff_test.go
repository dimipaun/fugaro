// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator_test

// The shared half's own tests: the handoff rule the spec states ("Handoff:
// owner or admin transfers an agent to another developer"), the agent the
// project owns rather than a person, and what a build without ee/ does with the
// core's seam.
//
// The role every case hands in is given rather than resolved here: Transfer is
// the rule over the value (internal/access resolves a role from a credential
// and a stored grant, and the caller does that). An owner reaches Transfer as
// an admin, because internal/access resolves the project's GCP owners and
// editors to RoleAdmin at project scope, and "owner" is not a role this tree
// spells (access.ParseRole refuses it).
//
// This test binary links internal/orchestrator and ee/orchestrator and NOT
// ee/register, which is the one thing that installs the seam — so the handoff
// TestCoreBuildsWithoutEE makes is a build without ee/'s, exactly as it is when
// the directory is deleted for the "core without ee/" workflow.

import (
	"errors"
	"testing"

	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The four names every case is built from. They are marker values, not
// addresses anybody holds.
const (
	handoffAgentID = "agent-a"
	handoffHolder  = "user:holder@example.invalid"
	handoffTaker   = "user:taker@example.invalid"
	handoffAdmin   = "user:admin@example.invalid"
)

// heldAgent is the agent every case starts from: one developer's.
func heldAgent() orchestrator.Agent {
	return orchestrator.Agent{ID: handoffAgentID, Owner: orchestrator.Owner{Member: handoffHolder}}
}

// TestHandoffNeedsOwnerOrAdmin is O17's red step and the spec's handoff rule:
// an owner or an admin moves an agent to another developer, and nobody else
// does. The refused cases answer nothing at all — a refused handoff that still
// answered the moved agent would be a caller that reads the new owner off a
// call that refused it.
func TestHandoffNeedsOwnerOrAdmin(t *testing.T) {
	moved := orchestrator.Owner{Member: handoffTaker}
	for _, tc := range []struct {
		name string
		role access.Role
		ok   bool
	}{
		{"an admin", access.RoleAdmin, true},
		{"an operator", access.RoleOperator, false},
		{"a viewer", access.RoleViewer, false},
		{"no role at all", access.RoleNone, false},
		{"a word that is not a role", access.Role("launcher"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eeorch.Transfer(orchestrator.Handoff{
				Agent: heldAgent(), To: moved, By: handoffAdmin, Role: tc.role,
			})
			if !tc.ok {
				if !errors.Is(err, eeorch.ErrHandoffRefused) {
					t.Fatalf("Transfer as %q answered %v, want a refusal: only an owner or an admin moves an agent", tc.role, err)
				}
				if got != (orchestrator.Agent{}) {
					t.Errorf("a refused handoff answered the agent %+v, want nothing", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Transfer as %q: %v", tc.role, err)
			}
			if got.ID != handoffAgentID || got.Owner != moved {
				t.Errorf("Transfer answered %+v, want the agent %s held by %s", got, handoffAgentID, moved.Member)
			}
		})
	}
}

// TestAgentOwnedByTheProjectHasNoPersonIdentity is the spec's other half: an
// agent can belong to the project rather than to a person, and then nobody's
// identity is behind it. The zero Owner is the project's, so an agent built
// without one belongs to nobody rather than to a person a reader would have to
// guess at — and the move back to a person works, because the handoff that took
// it is the one that gives it back.
func TestAgentOwnedByTheProjectHasNoPersonIdentity(t *testing.T) {
	if !(orchestrator.Owner{}).ProjectOwned() {
		t.Fatal("the zero Owner is not the project's: an agent nobody names an owner for would belong to whoever the empty member spells")
	}
	if got := (orchestrator.Agent{ID: handoffAgentID}).PersonIdentity(); got != "" {
		t.Fatalf("an agent built without an owner answers the person identity %q, want none", got)
	}

	taken, err := eeorch.Transfer(orchestrator.Handoff{
		Agent: heldAgent(), To: orchestrator.Owner{}, By: handoffAdmin, Role: access.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("handing the agent to the project: %v", err)
	}
	if taken.ID != handoffAgentID {
		t.Errorf("the handoff to the project answered the agent %q, want %q", taken.ID, handoffAgentID)
	}
	if !taken.Owner.ProjectOwned() {
		t.Errorf("the agent the project took is owned by %q, and the project is the zero owner", taken.Owner.Member)
	}
	if got := taken.PersonIdentity(); got != "" {
		t.Errorf("an agent the project owns has the person identity %q, want none: no person's credential is behind it", got)
	}

	back, err := eeorch.Transfer(orchestrator.Handoff{
		Agent: taken, To: orchestrator.Owner{Member: handoffHolder}, By: handoffAdmin, Role: access.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("handing the project's agent back to a person: %v", err)
	}
	if got := back.PersonIdentity(); got != handoffHolder {
		t.Errorf("the agent handed back answers the person identity %q, want %q", got, handoffHolder)
	}
}

// TestCoreBuildsWithoutEE is the runtime half of the boundary
// internal/eeboundary's scan holds (the task's command runs that package): this
// binary carries the core package and the feature and not the registration, so
// what it exercises is the core as a build without ee/ has it. The seam's
// default must refuse the handoff rather than perform one nothing checked.
func TestCoreBuildsWithoutEE(t *testing.T) {
	_, err := orchestrator.HandoffAgent(orchestrator.Handoff{
		Agent: heldAgent(), To: orchestrator.Owner{Member: handoffTaker}, By: handoffAdmin, Role: access.RoleAdmin,
	})
	if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
		t.Fatalf("a build that did not link ee/register answered a handoff with %v, want the core's refusal: the shared half is ee/'s, and a build without it moves no agent", err)
	}
}
