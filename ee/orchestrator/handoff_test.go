// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator_test

// The shared half's own tests: the handoff rule the spec states ("Handoff:
// owner or admin transfers an agent to another developer"), the agent the
// project owns rather than a person, and what a build without ee/ does with the
// core's seam.
//
// What every case hands in is the resolver's input rather than a role
// (internal/orchestrator.Handoff.Access), because a role does not carry the
// scope it is held at: an admin of every repository and an admin of one
// repository are both "admin" and neither holds a project-level thing (F19), so
// the fence has to be asked about the act, not about a rank. A GCP owner of the
// project reaches Transfer as an implicit project admin, because internal/access
// resolves the project's owners and editors that way, and "owner" is not a role
// this tree spells (access.ParseRole refuses it).
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

// holds is the input of a reader who has stored one grant: by's role at the
// scope given. Everything else the resolver decides against — the other grants,
// the implicit admins, allow_repos — is empty, which is the installation whose
// only answer about by is this one pair.
func holds(by string, role access.Role, scope access.Scope) access.Input {
	return access.Input{Grants: []access.Grant{{User: by, Role: role, Scope: scope}}}
}

// projectAdmin is the standing a handoff needs: admin at project scope, which
// is the project and every repository in it (access.ScopeProject).
func projectAdmin() access.Input {
	return holds(handoffAdmin, access.RoleAdmin, access.Scope{Kind: access.ScopeProject})
}

// TestHandoffNeedsOwnerOrAdmin is O17's red step and the spec's handoff rule:
// an owner or an admin moves an agent to another developer, and nobody else
// does. The refused cases answer nothing at all — a refused handoff that still
// answered the moved agent would be a caller that reads the new owner off a
// call that refused it.
//
// The scope cases are the half a bare role could not state: an all-repos admin
// is an admin of every repository the policy admits (access.ScopeAllRepos) and
// an admin of one repository holds that repository (access.ScopeRepos), and
// neither holds the project's agents, so both are refused — the same answer a
// viewer and a project operator get.
func TestHandoffNeedsOwnerOrAdmin(t *testing.T) {
	moved := orchestrator.Owner{Member: handoffTaker}
	for _, tc := range []struct {
		name string
		in   access.Input
		ok   bool
	}{
		{"an admin at project scope", projectAdmin(), true},
		{"a GCP owner of the project", access.Input{Implicit: []string{handoffAdmin}}, true},
		{"an all-repos admin", holds(handoffAdmin, access.RoleAdmin, access.Scope{Kind: access.ScopeAllRepos}), false},
		{"an admin of one repository", holds(handoffAdmin, access.RoleAdmin,
			access.Scope{Kind: access.ScopeRepos, Repos: []string{"github/acme/app"}}), false},
		{"an operator at project scope", holds(handoffAdmin, access.RoleOperator, access.Scope{Kind: access.ScopeProject}), false},
		{"a viewer at project scope", holds(handoffAdmin, access.RoleViewer, access.Scope{Kind: access.ScopeProject}), false},
		{"no role at all", access.Input{}, false},
		{"a project grant whose role is not a role", holds(handoffAdmin, access.Role("launcher"),
			access.Scope{Kind: access.ScopeProject}), false},
		{"a project grant whose scope does not parse", holds(handoffAdmin, access.RoleAdmin,
			access.Scope{Kind: access.ScopeProject, Repos: []string{"github/acme/app"}}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eeorch.Transfer(orchestrator.Handoff{
				Agent: heldAgent(), To: moved, By: handoffAdmin, Access: tc.in,
			})
			if !tc.ok {
				if !errors.Is(err, eeorch.ErrHandoffRefused) {
					t.Fatalf("Transfer for %s answered %v, want a refusal: only a project-scope owner or admin moves an agent", tc.name, err)
				}
				if got != (orchestrator.Agent{}) {
					t.Errorf("a refused handoff answered the agent %+v, want nothing", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Transfer for %s: %v", tc.name, err)
			}
			if got.ID != handoffAgentID || got.Owner != moved {
				t.Errorf("Transfer answered %+v, want the agent %s held by %s", got, handoffAgentID, moved.Member)
			}
		})
	}
}

// TestHandoffNamesAnAgentAndWhoAskedForIt is Transfer's other two refusals, the
// ones that are not about the standing at all: a request that names no agent,
// or nobody who asked for it, is refused even for a project admin. Neither is a
// move — a handoff of no agent moves nothing, and one recorded against an empty
// member is one nobody could be held to — so both answer the refusal and
// nothing else, and neither is reached through the standing check.
func TestHandoffNamesAnAgentAndWhoAskedForIt(t *testing.T) {
	moved := orchestrator.Owner{Member: handoffTaker}
	for _, tc := range []struct {
		name string
		h    orchestrator.Handoff
	}{
		{"no agent", orchestrator.Handoff{To: moved, By: handoffAdmin, Access: projectAdmin()}},
		{"an agent with no id", orchestrator.Handoff{
			Agent: orchestrator.Agent{Owner: orchestrator.Owner{Member: handoffHolder}},
			To:    moved, By: handoffAdmin, Access: projectAdmin(),
		}},
		{"nobody who asked for it", orchestrator.Handoff{
			Agent: heldAgent(), To: moved, Access: projectAdmin(),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eeorch.Transfer(tc.h)
			if !errors.Is(err, eeorch.ErrHandoffRefused) {
				t.Fatalf("Transfer answered a request with %s with %v, want a refusal: a handoff names the agent it moves and the person who asked for it", tc.name, err)
			}
			if got != (orchestrator.Agent{}) {
				t.Errorf("a refused handoff answered the agent %+v, want nothing", got)
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
		Agent: heldAgent(), To: orchestrator.Owner{}, By: handoffAdmin, Access: projectAdmin(),
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
		Agent: taken, To: orchestrator.Owner{Member: handoffHolder}, By: handoffAdmin, Access: projectAdmin(),
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
		Agent: heldAgent(), To: orchestrator.Owner{Member: handoffTaker}, By: handoffAdmin, Access: projectAdmin(),
	})
	if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
		t.Fatalf("a build that did not link ee/register answered a handoff with %v, want the core's refusal: the shared half is ee/'s, and a build without it moves no agent", err)
	}
}
