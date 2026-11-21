package credsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// These tests are LM24's: the orchestrator's class at the issuer is bound to the
// orchestrator's own record, and to nothing else (D3136 as amended, F34). What
// each of them holds is one of the three things the class may not be issued —
// an attempt the record does not carry, an attempt that has started or ended,
// and an ask that names a context repository — plus the case that must go
// through, which is a recorded attempt of the ask's repository that nothing has
// reported on.
//
// The red step is TestOrchestratedAskForAnotherRunIsRefused against C1's stub
// check, which admits the orchestrator's class whole: it was written before this
// file's check existed, and it is what the route answers the class with until
// NewOrchestratedScope is wired in (I1).

// errNextJudged is what the check for the other classes answers in these tests.
// It is a value of the test's own so that "this check refused" and "the other
// classes' check refused" cannot be read as each other: every refusal this
// file's check makes is ErrOrchestratedAsk, and nothing else reaches a caller of
// the orchestrator's class.
var errNextJudged = errors.New("test: the other classes' check judged this ask")

// judgingScope stands for LM23's resolver: it judges every class that is not the
// orchestrator's and refuses them all, which is the direction a test of the
// OTHER class's arm can read — a call that comes back with errNextJudged was
// handed over, and one that comes back with anything else was judged here.
type judgingScope struct {
	calls []IdentityCaller
}

func (s *judgingScope) Check(_ context.Context, caller IdentityCaller, _ identitywire.TicketRequest) error {
	s.calls = append(s.calls, caller)
	return errNextJudged
}

// recordFake is the orchestrator's own record as these tests hand it over: a
// state node, an error a read may answer with, and the count of reads — because
// an ask refused without one is a refusal that costs the database nothing.
type recordFake struct {
	state orchestrator.State
	err   error
	reads int
}

func (r *recordFake) State(context.Context) (orchestrator.State, error) {
	r.reads++
	if r.err != nil {
		return orchestrator.State{}, r.err
	}
	return r.state, nil
}

// orchestratedItem is one work item of the orchestrator's record, in the phase a
// committed attempt leaves it in (orchestrator's commitIntent writes running).
func orchestratedItem(id, repo string) orchestrator.WorkItem {
	return orchestrator.WorkItem{ID: id, Repo: repo, Phase: orchestrator.PhaseRunning}
}

// orchestratedIntent is one committed attempt: the id internal/orchestrator's
// IntentID writes, the run id it minted, and how it ended — empty while nothing
// has reported on it.
func orchestratedIntent(id, run string, outcome orchestrator.Outcome) orchestrator.Intent {
	return orchestrator.Intent{ID: id, RunID: run, Outcome: outcome}
}

// orchestratedAsk is an ask of the orchestrator's class for one attempt.
func orchestratedAsk(slug, run, attempt string) identitywire.TicketRequest {
	return identitywire.TicketRequest{
		Slug:              slug,
		Run:               run,
		Account:           testJobSA,
		Launcher:          "orchestrator",
		MaxRuntimeSeconds: 3600,
		Attempt:           identitywire.AttemptID(attempt),
	}
}

// orchestratedScopeFor builds the check under test over one record, with the
// other classes' check refusing everything.
func orchestratedScopeFor(t *testing.T, rec OrchestratorRecord) (ScopeCheck, *judgingScope) {
	t.Helper()
	next := &judgingScope{}
	check, err := NewOrchestratedScope(next, rec)
	if err != nil {
		t.Fatalf("NewOrchestratedScope: %v", err)
	}
	return check, next
}

// TestOrchestratedAskNeedsARecordedUnstartedAttempt is the case that must go
// through and the ones that keep it from being a recorded, unstarted attempt:
// an attempt nothing has reported on, of the repository the ask names, is what
// the class may be issued; an attempt that has ended, one the record does not
// carry, and a reference the orchestrator's own spelling could not have written
// are each refused. And a record that could not be read is the SERVICE's
// failure and never a verdict about the caller (R16's ScopeFault rule).
func TestOrchestratedAskNeedsARecordedUnstartedAttempt(t *testing.T) {
	const (
		slug = "acme/web"
		run  = "run-1"
	)
	open := func() *recordFake {
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{orchestratedItem("wi-1", slug)},
			Intents:   []orchestrator.Intent{orchestratedIntent("wi-1:1", run, "")},
		}}
	}

	t.Run("a recorded attempt nothing has reported on is issued", func(t *testing.T) {
		rec := open()
		check, next := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, orchestratedAsk(slug, run, "wi-1:1")); err != nil {
			t.Fatalf("the ask for a recorded, unstarted attempt was refused: %v", err)
		}
		if rec.reads != 1 {
			t.Errorf("the orchestrator's record was read %d times, want 1: the class's judgement IS that record", rec.reads)
		}
		if len(next.calls) != 0 {
			t.Errorf("the other classes' check judged an orchestrator's ask: %+v", next.calls)
		}
	})

	refusals := []struct {
		name  string
		ask   identitywire.TicketRequest
		state func() *recordFake
	}{
		{
			name: "an attempt that has ended",
			ask:  orchestratedAsk(slug, run, "wi-1:1"),
			state: func() *recordFake {
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{orchestratedItem("wi-1", slug)},
					Intents:   []orchestrator.Intent{orchestratedIntent("wi-1:1", run, orchestrator.OutcomeFailed)},
				}}
			},
		},
		{
			name:  "an attempt the record does not carry",
			ask:   orchestratedAsk(slug, run, "wi-1:2"),
			state: open,
		},
		{
			name:  "a reference the orchestrator's own spelling could not have written",
			ask:   orchestratedAsk(slug, run, "wi-1:1x"),
			state: open,
		},
		{
			name:  "an ask that names no attempt at all",
			ask:   orchestratedAsk(slug, run, ""),
			state: open,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.state()
			check, _ := orchestratedScopeFor(t, rec)
			err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, tc.ask)
			if !errors.Is(err, ErrOrchestratedAsk) {
				t.Fatalf("the ask was answered %v, want a refusal of the orchestrator's own record (%v)", err, ErrOrchestratedAsk)
			}
		})
	}

	t.Run("a record that cannot be read is the service's own failure", func(t *testing.T) {
		rec := &recordFake{err: errors.New("test: the node could not be read")}
		check, _ := orchestratedScopeFor(t, rec)
		err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, orchestratedAsk(slug, run, "wi-1:1"))
		var fault *ScopeFault
		if !errors.As(err, &fault) {
			t.Fatalf("an unreadable record answered %v, want a *ScopeFault: a node nobody could read must not be refused as a caller who is not admitted (R16)", err)
		}
		if errors.Is(err, ErrOrchestratedAsk) {
			t.Errorf("an unreadable record was answered as a refusal of the caller: %v", err)
		}
	})

	t.Run("a record that carries nothing is not an admission", func(t *testing.T) {
		rec := &recordFake{}
		check, _ := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, orchestratedAsk(slug, run, "wi-1:1")); !errors.Is(err, ErrOrchestratedAsk) {
			t.Fatalf("an ask against an orchestrator that recorded no attempt was answered %v, want a refusal", err)
		}
	})
}

// TestOrchestratedAskForAnotherRunIsRefused is the red step and D3136's core:
// the run id the ask names must be the one the attempt it names was recorded
// under. The record here carries two attempts of one repository, each with its
// own run id, and one attempt of another repository; every ask that points one
// attempt at another run, or at an attempt no intent carries, is refused.
func TestOrchestratedAskForAnotherRunIsRefused(t *testing.T) {
	const slug = "acme/web"
	record := func() *recordFake {
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{
				orchestratedItem("wi-1", slug),
				orchestratedItem("wi-2", slug),
				orchestratedItem("wi-3", "acme/other"),
			},
			Intents: []orchestrator.Intent{
				orchestratedIntent("wi-1:1", "run-1", ""),
				orchestratedIntent("wi-2:1", "run-2", ""),
				orchestratedIntent("wi-3:1", "run-3", ""),
			},
		}}
	}

	asks := []struct {
		name string
		ask  identitywire.TicketRequest
	}{
		{
			name: "the attempt is recorded under another run",
			ask:  orchestratedAsk(slug, "run-2", "wi-1:1"),
		},
		{
			name: "the run is another attempt's",
			ask:  orchestratedAsk(slug, "run-1", "wi-2:1"),
		},
		{
			name: "the run is one no attempt carries",
			ask:  orchestratedAsk(slug, "run-9", "wi-1:1"),
		},
		{
			name: "the run is real and the attempt is not recorded",
			ask:  orchestratedAsk(slug, "run-2", "wi-2:2"),
		},
		{
			name: "the attempt is a recorded one of another repository",
			ask:  orchestratedAsk(slug, "run-3", "wi-3:1"),
		},
		{
			name: "the attempt is recorded for an item the record does not carry",
			ask:  orchestratedAsk(slug, "run-4", "wi-4:1"),
		},
	}
	for _, tc := range asks {
		t.Run(tc.name, func(t *testing.T) {
			rec := record()
			check, _ := orchestratedScopeFor(t, rec)
			err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, tc.ask)
			if !errors.Is(err, ErrOrchestratedAsk) {
				t.Fatalf("the ask for another run was answered %v, want a refusal: the class is issued only the attempt its own record carries, under the run id that attempt recorded (D3136)", err)
			}
		})
	}
}

// TestOrchestratedAskWithContextReposIsRefused holds the second half of D3136:
// an ask of this class may name no context repository at all. The list is signed
// into the run's identity and judged by no scope for this class, so an ask that
// carries one is refused whole — and the refusal costs the database no read,
// because an ask that cannot be the record's does not reach it.
func TestOrchestratedAskWithContextReposIsRefused(t *testing.T) {
	const (
		slug = "acme/web"
		run  = "run-1"
	)
	for _, repos := range [][]string{{"acme/other"}, {"acme/other", "acme/third"}} {
		t.Run(repos[0], func(t *testing.T) {
			rec := &recordFake{state: orchestrator.State{
				WorkItems: []orchestrator.WorkItem{orchestratedItem("wi-1", slug)},
				Intents:   []orchestrator.Intent{orchestratedIntent("wi-1:1", run, "")},
			}}
			check, _ := orchestratedScopeFor(t, rec)
			ask := orchestratedAsk(slug, run, "wi-1:1")
			ask.ContextRepos = repos
			err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, ask)
			if !errors.Is(err, ErrOrchestratedAsk) {
				t.Fatalf("an orchestrator's ask naming %d context repositories was answered %v, want a refusal", len(repos), err)
			}
			if rec.reads != 0 {
				t.Errorf("the orchestrator's record was read %d times for an ask that names a context repository, want 0", rec.reads)
			}
		})
	}
}

// TestOrchestratedAskLeavesOtherClassesToTheNext is the other half of "no scope
// is judged for this class" (D3136): a person's ask and a roster service
// account's are handed to the check for the other classes — the same ask that
// this check admits for the orchestrator is refused for them — and an
// orchestrator's ask never reaches it.
func TestOrchestratedAskLeavesOtherClassesToTheNext(t *testing.T) {
	const (
		slug = "acme/web"
		run  = "run-1"
	)
	ask := orchestratedAsk(slug, run, "wi-1:1")

	for _, class := range []identitywire.CallerClass{identitywire.ClassPerson, identitywire.ClassServiceAccount} {
		t.Run(string(class), func(t *testing.T) {
			rec := &recordFake{state: orchestrator.State{
				WorkItems: []orchestrator.WorkItem{orchestratedItem("wi-1", slug)},
				Intents:   []orchestrator.Intent{orchestratedIntent("wi-1:1", run, "")},
			}}
			check, next := orchestratedScopeFor(t, rec)
			err := check.Check(context.Background(), IdentityCaller{Address: "someone@example.invalid", Class: class}, ask)
			if !errors.Is(err, errNextJudged) {
				t.Fatalf("a %s's ask was answered %v, want the other classes' check's own answer: this class is judged by the resolver (LM23)", class, err)
			}
			if len(next.calls) != 1 {
				t.Fatalf("the other classes' check judged %d asks, want 1", len(next.calls))
			}
			if rec.reads != 0 {
				t.Errorf("the orchestrator's record was read %d times for another class's ask, want 0", rec.reads)
			}
		})
	}

	t.Run("the orchestrator's own ask does not reach it", func(t *testing.T) {
		rec := &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{orchestratedItem("wi-1", slug)},
			Intents:   []orchestrator.Intent{orchestratedIntent("wi-1:1", run, "")},
		}}
		check, next := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, ask); err != nil {
			t.Fatalf("the orchestrator's ask was refused: %v", err)
		}
		if len(next.calls) != 0 {
			t.Errorf("the orchestrator's ask reached the other classes' check: %+v", next.calls)
		}
	})
}
