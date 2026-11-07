package orchestrator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestAgentActorCannotResolveAPersonDecision is G2's fence inside the state
// machine: a decision that waits on a person is closed by a person-class
// actor and by nothing else. The classes below are the ones that act with
// nobody watching — the session carrying the work, the daemon's own tick and
// the decision agent — and each of them must be refused, or the fence holds
// only where the CLI remembers to check it (O5) and not where the state is
// actually changed.
//
// Mutation: make Apply ignore the waiting-on class (any actor closes any
// decision) and every class below closes the decision, so the loop fails;
// make it refuse every actor and the person's own close fails at the end.
func TestAgentActorCannotResolveAPersonDecision(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAPersonDecision(t, now)

	closing := orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{{
		Op:      orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: "ask-1", Answer: "run it"},
	}}}

	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.Daemon, orchestrator.DecisionAgent} {
		_, err := orchestrator.Apply(s, closing, orchestrator.Actor{Class: class, Name: "session:20261107-090000-abcd", Now: now})
		if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor closed a decision that waits on a person: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}

	next, err := orchestrator.Apply(s, closing, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now})
	if err != nil {
		t.Fatalf("the person could not close their own decision: %v", err)
	}
	if got := next.Decisions[0].Answer; got != "run it" {
		t.Fatalf("the closed decision's answer is %q, want %q", got, "run it")
	}
	if next.Decisions[0].ResolvedAt == nil {
		t.Fatal("the closed decision carries no resolution time")
	}
}

// stateWithAPersonDecision is a small state that passes the validator: one
// queued work item and one decision waiting on a person. Tests that need
// something else start from it and change the one thing they are about.
func stateWithAPersonDecision(t *testing.T, now time.Time) orchestrator.State {
	t.Helper()
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           7,
		AgentID:       "orchestrator-a",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "keep the repository's pull requests moving without a person watching",
		WorkItems: []orchestrator.WorkItem{{
			ID:        "wi-1",
			Title:     "land the state migration",
			Repo:      "owner/repo",
			Recipe:    "standard",
			RigorTier: "medium",
			Brief:     "Move the sweep to the daemon.",
			Phase:     orchestrator.PhaseQueued,
		}},
		Decisions: []orchestrator.Decision{{
			ID:        "ask-1",
			Question:  "the migration needs a person's call: keep the old node or delete it?",
			WaitingOn: orchestrator.OnPerson,
			At:        now.Add(-time.Hour),
		}},
		Facts:    map[string]orchestrator.Fact{"repo.primary": {Value: "owner/repo", By: orchestrator.Daemon}},
		Archived: []orchestrator.Archived{},
		Intents:  []orchestrator.Intent{},
	}
}
