package credsvc

// PR4's issuer test: the ticket route serves a resume only where the node is one
// the credentials service itself wrote (D3189, the one case added to D3136).
//
// The class is judged against two records, and this file is about the second:
// the orchestrator's own state node is what a re-issue is bound to, and this
// service's own record of the pauses it wrote is what a resume is bound to. What
// is pinned here is the whole of that judgement — a pause this service recorded
// is served, one it did not is refused, a record it cannot read is the SERVICE's
// failure, an ask that names both references is refused, and the class that is
// not the orchestrator's never reaches this arm — and, beside it, the route's own
// shape checks for the member.
//
// The fixtures are identity_orchestrated_test.go's (the record, the item, the
// slug, the ask) and identity_ticket_test.go's (the route over a fake database),
// because what is under test is the same class and the same route.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// pausesFake is this service's own record of the pauses it wrote, as a test
// hands it over: the nodes it holds, the failure a read may answer with, and the
// reads it was asked for — because an ask refused without one is a refusal that
// costs the database nothing, and an ask that reads the orchestrator's record
// instead is reading the wrong one.
type pausesFake struct {
	held  map[string]bool
	err   error
	reads []string
}

func (p *pausesFake) Paused(_ context.Context, slug, run string) error {
	p.reads = append(p.reads, slug+" "+run)
	if p.err != nil {
		return p.err
	}
	if !p.held[slug+" "+run] {
		return ErrNoPause
	}
	return nil
}

// resumeAsk is an ask of the orchestrator's class for a resume: the run the
// ticket is for, and the paused run it continues. It names no attempt, because a
// run re-issues an attempt of the orchestrator's own record or continues a pause
// this service wrote, and never both (D3189).
func resumeAsk(slug, run, resumes string) identitywire.TicketRequest {
	return identitywire.TicketRequest{
		Slug:              slug,
		Run:               run,
		Account:           testJobSA,
		Launcher:          "orchestrator",
		MaxRuntimeSeconds: 3600,
		Resumes:           resumes,
	}
}

// TestTheIssuerServesOnlyAServiceWrittenPause is D3189's headline: the
// orchestrator's class is served a resume for a pause this service recorded and
// for nothing else.
//
// The whole of the evidence is that record, and the test states it two ways: the
// orchestrator's own node is not read at all for a resume — the record carries no
// attempt of a run that has not started, and a resume derived from it is the case
// the class was not widened by (F52) — and a pause this service holds no node for
// is refused, whatever the ask says about it.
func TestTheIssuerServesOnlyAServiceWrittenPause(t *testing.T) {
	const (
		repo      = "acme/web"
		pausedRun = "20261120-090000-abcd"
		resumedBy = "20261120-120000-beef"
	)
	slug := orchestratedSlug(t, "github", repo)
	// The record of the run that suspended itself: the paused run was an attempt
	// of the item, and nothing has reported on it — which is what a run that
	// suspended looks like from here, and why the record cannot be the evidence a
	// resume is served on (a resume's own run is a new one and is not in it).
	item := orchestratedItem("wi-1", repo)
	rec := func() *recordFake {
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{item},
			Intents:   []orchestrator.Intent{orchestratedAttempt(item, 1, pausedRun, "")},
		}}
	}
	caller := IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}

	t.Run("a pause this service recorded is served", func(t *testing.T) {
		record, pauses := rec(), &pausesFake{held: map[string]bool{slug + " " + pausedRun: true}}
		check, next := orchestratedScopeOver(t, record, pauses)
		if err := check.Check(context.Background(), caller, resumeAsk(slug, resumedBy, pausedRun)); err != nil {
			t.Fatalf("the ask to resume a pause this service recorded was refused: %v", err)
		}
		if record.reads != 0 {
			t.Errorf("the orchestrator's own record was read %d time(s) for a resume, want none: the record carries no attempt of a run that has not started, and a resume is served on this service's own record of the pause (D3189)", record.reads)
		}
		if len(pauses.reads) != 1 || pauses.reads[0] != slug+" "+pausedRun {
			t.Errorf("this service's record was asked about %v, want the repository and the paused run the ask names", pauses.reads)
		}
		if len(next.calls) != 0 {
			t.Errorf("the other classes' check judged an orchestrator's ask: %+v", next.calls)
		}
	})

	t.Run("a pause this service did not record is refused", func(t *testing.T) {
		// The ask names a paused run this service holds no node for — the case a
		// resume derived from the paused run's own bucket would be, and the case
		// the class is refused whole: the ask's word about a run it says it
		// suspended is not evidence of anything (R23, F52).
		record, pauses := rec(), &pausesFake{}
		check, _ := orchestratedScopeOver(t, record, pauses)
		err := check.Check(context.Background(), caller, resumeAsk(slug, resumedBy, pausedRun))
		if !errors.Is(err, ErrOrchestratedAsk) {
			t.Fatalf("an ask to resume a pause this service recorded nothing for is answered %v, want %v", err, ErrOrchestratedAsk)
		}
		if !strings.Contains(err.Error(), pausedRun) {
			t.Errorf("the refusal does not name the run it refused to resume: %v", err)
		}
		if record.reads != 0 {
			t.Errorf("the orchestrator's own record was read %d time(s) for a refused resume, want none", record.reads)
		}
	})

	t.Run("a record this service cannot read is the service's failure", func(t *testing.T) {
		// R16's ScopeFault rule: a node that could not be read is not a node this
		// service holds none for, and answering it as a refusal would tell the
		// orchestrator its pause is not recorded when the service could not tell.
		record, pauses := rec(), &pausesFake{err: errors.New("the database could not be read")}
		check, _ := orchestratedScopeOver(t, record, pauses)
		err := check.Check(context.Background(), caller, resumeAsk(slug, resumedBy, pausedRun))
		var fault *OrchestratedFault
		if !errors.As(err, &fault) {
			t.Fatalf("a record that could not be read is answered %v, want the service's own fault", err)
		}
		if errors.Is(err, ErrOrchestratedAsk) {
			t.Errorf("a record that could not be read is answered as a refusal of the caller: %v", err)
		}
		if fault.Caller != caller {
			t.Errorf("the fault carries caller %+v, want the verified caller %+v: it is what the refusal's one audit line is written from", fault.Caller, caller)
		}
	})

	t.Run("an ask that names both references is refused", func(t *testing.T) {
		// The two are judged against different records, so an ask that names both
		// would be judged against whichever arm ran.
		record, pauses := rec(), &pausesFake{held: map[string]bool{slug + " " + pausedRun: true}}
		check, _ := orchestratedScopeOver(t, record, pauses)
		ask := resumeAsk(slug, resumedBy, pausedRun)
		ask.Attempt = "wi-1:1"
		if err := check.Check(context.Background(), caller, ask); !errors.Is(err, ErrOrchestratedAsk) {
			t.Fatalf("an ask naming an attempt and a resume is answered %v, want %v", err, ErrOrchestratedAsk)
		}
		if len(pauses.reads) != 0 {
			t.Errorf("the pause was looked up for an ask that names both references: %v", pauses.reads)
		}
	})

	t.Run("another class never reaches this arm", func(t *testing.T) {
		// The class is the service's own reading of the caller, so a person's ask
		// that names a resume is judged by the check for the other classes — and
		// the route refuses the member for that class before either (the shape
		// checks, below).
		record, pauses := rec(), &pausesFake{held: map[string]bool{slug + " " + pausedRun: true}}
		check, next := orchestratedScopeOver(t, record, pauses)
		person := IdentityCaller{Address: testLauncherAccount, Class: identitywire.ClassPerson}
		if err := check.Check(context.Background(), person, resumeAsk(slug, resumedBy, pausedRun)); !errors.Is(err, errNextJudged) {
			t.Fatalf("a person's ask is answered %v, want it handed to the other classes' check", err)
		}
		if len(next.calls) != 1 || next.calls[0] != person {
			t.Errorf("the other classes' check was asked about %+v, want the caller this service verified", next.calls)
		}
		if len(pauses.reads) != 0 {
			t.Errorf("this service's record of its pauses was read for a class that is not the orchestrator's: %v", pauses.reads)
		}
	})
}

// TestTicketRefusesAResumeOfAnotherClassOrShape is the route's own half: the
// resume member is the orchestrator's, so a launcher's ask that names one is
// refused before any record is read, and an orchestrator's ask that names
// something which is no run id of this installation is refused too — while an
// orchestrator's well-formed resume reaches the class's check and is issued the
// ticket for the run it is about.
func TestTicketRefusesAResumeOfAnotherClassOrShape(t *testing.T) {
	const pausedRun = "20261120-090000-abcd"

	t.Run("another class may not name a resume", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Resumes = pausedRun
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, "resume") {
			t.Errorf("the refusal does not name the member it refused: %s", e.Message)
		}
		f.nothingWritten(t)
	})

	t.Run("a resume that is no run id", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Resumes = "../../etc/passwd"
		caller := IdentityCaller{Address: testOrchestratorSA, Class: identitywire.ClassOrchestrator}
		rec, body := f.ask(t, caller, ask)
		f.refused(t, rec, body, testOrchestratorSA, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		f.nothingWritten(t)
	})

	t.Run("an orchestrator's resume is issued", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Resumes = pausedRun
		caller := IdentityCaller{Address: testOrchestratorSA, Class: identitywire.ClassOrchestrator}
		rec, body := f.ask(t, caller, ask)
		f.issued(t, rec, body)
		if got := f.record(t, f.slug, f.run); got.Attempt != "" {
			t.Errorf("the record carries attempt %q, want none: a resume names no attempt of the orchestrator's own record", got.Attempt)
		}
	})
}
