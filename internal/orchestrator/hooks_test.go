package orchestrator_test

// The orchestrator's three seams, from the core's side: what an untagged build
// has before ee/register sets them, and the refusals that keep a build from
// carrying a handoff, a batch or a workspace rule it did not intend (design
// ee-boundary-moves.md §5.5). The untagged defaults are refusals and not
// no-ops, and they are what every handoff and every raise answers until the
// shared half is registered — except the workspace's, whose default is the
// one answer that must be safe without a rule over it: no refusal at all.

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
		{"nobody asked and the project carries a raised setting", orchestrator.Batch{ProjectCap: 25, By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"a batch of a negative number of work items", orchestrator.Batch{Covered: -1, By: orchestrator.Person}, 0, false},
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

// TestTheCoreDefaultClaimsNoRefusal pins the third seam's untagged answer: no
// run is refused by a workspace. It is the opposite of the other two defaults
// on purpose — a handoff and a raise are refused because the rule that allows
// one is ee/'s, and a refusal the core cannot check is exactly what must not
// pause a queue. A build without the shared half behaves as it did before the
// seam existed: the run counts as an ordinary failed attempt and the breaker
// pauses after BreakerThreshold of them (O6).
func TestTheCoreDefaultClaimsNoRefusal(t *testing.T) {
	orchestrator.ResetWorkspace(t)

	// Every report a run can produce, including one whose record carries a
	// marker: what tells a real marker from a claim is ee/'s, so the core
	// answers false to all of them rather than guessing.
	for _, tc := range []struct {
		name string
		rep  orchestrator.RunReport
	}{
		{"a run still in flight", orchestrator.RunReport{RunID: "run-1", Started: true}},
		{"a run that failed", orchestrator.RunReport{RunID: "run-1", Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed}},
		{"a run whose record claims the workspace stopped it", orchestrator.RunReport{
			RunID: "run-1", Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed,
			WorkspaceRefusal: "stop",
		}},
		{"a claim with a halt beside it", orchestrator.RunReport{
			RunID: "run-1", Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed,
			WorkspaceRefusal: "stop",
			Halt:             orchestrator.Halt{Reason: "budget_unavailable", Scope: "run", Detail: "whatever the claim says"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if orchestrator.WorkspaceRefused(tc.rep) {
				t.Error("an untagged build claimed a run was refused by a workspace: a queue paused on a claim no rule checked is worse than one that retries")
			}
		})
	}
}

// TestSetWorkspaceRefusesASecondCall pins the two refusals the setter makes,
// on the pattern the other two seams follow: a hook with no Refused is a panic
// on the first fold rather than an answer, and a second call would replace the
// rule ee/register installed after the build decided what it carries (§5.5).
func TestSetWorkspaceRefusesASecondCall(t *testing.T) {
	orchestrator.ResetWorkspace(t)

	if err := orchestrator.SetWorkspace(orchestrator.WorkspaceFuncs{}); err == nil {
		t.Error("SetWorkspace accepted a hook with no Refused: the fold would panic on the first finished run rather than answer it")
	}
	if orchestrator.WorkspaceRefused(orchestrator.RunReport{WorkspaceRefusal: "stop"}) {
		t.Error("a refused SetWorkspace left a rule installed, and the untagged default is the one it must leave behind")
	}

	installed := orchestrator.WorkspaceFuncs{Refused: func(orchestrator.RunReport) bool { return true }}
	if err := orchestrator.SetWorkspace(installed); err != nil {
		t.Fatalf("the first SetWorkspace was refused: %v", err)
	}
	if err := orchestrator.SetWorkspace(orchestrator.WorkspaceFuncs{Refused: installed.Refused}); err == nil {
		t.Error("a second SetWorkspace was accepted, so the pause rule a tagged binary carries could be replaced after it was registered")
	}
	if !orchestrator.WorkspaceRefused(orchestrator.RunReport{}) {
		t.Error("WorkspaceRefused did not reach the hook that was installed")
	}
}
