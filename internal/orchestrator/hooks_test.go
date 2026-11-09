package orchestrator_test

// The orchestrator's two seams, from the core's side: what an untagged build
// has before ee/register sets them, and the refusals that keep a build from
// carrying a handoff or a batch rule it did not intend (design
// ee-boundary-moves.md §5.5). The untagged defaults are refusals and not
// no-ops, and they are what every handoff and every raise answers until the
// shared half is registered.

import (
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
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
		Access: access.Input{Grants: []access.Grant{{
			User: "user:admin@example.invalid", Role: access.RoleAdmin, Scope: access.Scope{Kind: access.ScopeProject},
		}}},
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

// TestTheBatchSeamAnswersTheShippedDefaultAndRefusesARaise pins SetBatch's
// refusals and the default it leaves behind, and the one answer the untagged
// build must give: the batch one approval covers — the project's own setting,
// the ten Fugaro ships when the project wrote none — and no raise above it,
// because the rule that lets a person raise one is ee/'s. A request above the
// ceiling is refused rather than cut down, so a caller cannot read ten off a
// request of twenty-five and approve ten items believing it approved them all.
func TestTheBatchSeamAnswersTheShippedDefaultAndRefusesARaise(t *testing.T) {
	orchestrator.ResetBatch(t)

	if err := orchestrator.SetBatch(orchestrator.BatchFuncs{}); err == nil {
		t.Error("SetBatch accepted a hook with no Size: ApprovalBatch would panic on the first approval rather than refuse it")
	}

	for _, tc := range []struct {
		name string
		b    orchestrator.Batch
		want int64
		ok   bool
	}{
		{"nobody asked", orchestrator.Batch{By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"the project tightened it", orchestrator.Batch{ProjectCap: 4, By: orchestrator.Person}, 4, true},
		{"a person asks for the shipped default", orchestrator.Batch{Covered: policy.DefaultBatchSize, By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"a person asks for more than the shipped default", orchestrator.Batch{Covered: 25, ProjectCap: 25, By: orchestrator.Person}, 0, false},
		{"a person asks for more than the project's cap", orchestrator.Batch{Covered: 5, ProjectCap: 4, By: orchestrator.Person}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := orchestrator.ApprovalBatch(tc.b)
			if !tc.ok {
				if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
					t.Fatalf("an untagged build answered %d, %v, want the core's refusal: raising one approval above the shipped default needs the shared half this build does not carry", got, err)
				}
				if got != 0 {
					t.Errorf("a refused batch answered %d work items, want none", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApprovalBatch: %v", err)
			}
			if got != tc.want {
				t.Errorf("ApprovalBatch answered %d work items, want %d", got, tc.want)
			}
		})
	}

	installed := orchestrator.BatchFuncs{Size: func(orchestrator.Batch) (int64, error) { return 7, nil }}
	if err := orchestrator.SetBatch(installed); err != nil {
		t.Fatalf("the first SetBatch was refused: %v", err)
	}
	if err := orchestrator.SetBatch(orchestrator.BatchFuncs{Size: installed.Size}); err == nil {
		t.Error("a second SetBatch was accepted, so the batch rule a tagged binary carries could be replaced after it was registered")
	}
	if got, err := orchestrator.ApprovalBatch(orchestrator.Batch{}); err != nil || got != 7 {
		t.Errorf("ApprovalBatch answered %d, %v, want the hook that was installed", got, err)
	}
}
