package orchestrator_test

// The handoff seam itself, from the core's side: what an untagged build has
// before ee/register sets it, and the two refusals that keep a build from
// carrying a handoff rule it did not intend (design ee-boundary-moves.md §5.5).
// The untagged default is a refusal and not a no-op, and it is what every
// handoff answers until the shared half is registered.

import (
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestTheHandoffSeamRefusesAHookThatWouldPanic pins SetHandoff's refusals and
// the default it leaves behind: a hook with no Transfer is refused, because
// HandoffAgent calls it on every handoff and a nil one would panic the caller
// instead of refusing it; a refused call changes nothing, so the core default
// still refuses an admin's handoff; a second call is refused, so what a tagged
// binary carries cannot be replaced after ee/register decided it; and the hook
// that was installed is the one HandoffAgent runs.
func TestTheHandoffSeamRefusesAHookThatWouldPanic(t *testing.T) {
	orchestrator.ResetHandoff(t)

	admin := orchestrator.Handoff{
		Agent: orchestrator.Agent{ID: "agent-a", Owner: orchestrator.Owner{Member: "user:holder@example.invalid"}},
		To:    orchestrator.Owner{Member: "user:taker@example.invalid"},
		By:    "user:admin@example.invalid",
		Role:  access.RoleAdmin,
	}

	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{}); err == nil {
		t.Error("SetHandoff accepted a hook with no Transfer: HandoffAgent would panic on the first handoff rather than refuse it")
	}
	if _, err := orchestrator.HandoffAgent(admin); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
		t.Errorf("after a refused SetHandoff a handoff answered %v, want the core's refusal: a refused call must leave the untagged default installed", err)
	}

	installed := orchestrator.HandoffFuncs{Transfer: func(h orchestrator.Handoff) (orchestrator.Agent, error) {
		return orchestrator.Agent{ID: h.Agent.ID, Owner: h.To}, nil
	}}
	if err := orchestrator.SetHandoff(installed); err != nil {
		t.Fatalf("the first SetHandoff was refused: %v", err)
	}
	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{Transfer: installed.Transfer}); err == nil {
		t.Error("a second SetHandoff was accepted, so the handoff rule a tagged binary carries could be replaced after it was registered")
	}
	got, err := orchestrator.HandoffAgent(admin)
	if err != nil {
		t.Fatalf("the installed hook was not the one HandoffAgent ran: %v", err)
	}
	if got.ID != admin.Agent.ID || got.Owner != admin.To {
		t.Errorf("HandoffAgent answered %+v, want the agent %s held by %s", got, admin.Agent.ID, admin.To.Member)
	}
}
