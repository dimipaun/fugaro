package credsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/task"
)

// These tests are LM24's: the orchestrator's class at the issuer is bound to the
// orchestrator's own record, and to nothing else (D3136 as amended, F34). What
// each of them holds is one of the things the class may not be issued — an
// attempt the record does not carry, an attempt that has started or ended (an
// item the record has finished, edited since the commit, or whose approval a
// person has replaced; an intent something has reported on), and an ask that
// names a context repository — plus the case that must go through, which is a
// recorded attempt of the ask's repository, of work the record has not finished,
// that nothing has reported on, under the approval that covers it.
//
// The judgement itself is not this file's: it is the launcher's own gate, asked
// through orchestrator.ReissuableAttempt, so the issuer and the gate cannot
// drift. That is why every fixture here carries what a real record carries — an
// item approved over the spec it holds, and an attempt whose digest is that
// spec's and whose approval is the one standing on the item — and why the
// refusals below are the gate's own: an item edited since the commit and one
// whose approval a person has replaced are exactly what the gate's `reissuable`
// refuses and its recovery abandons.
//
// The two sides of the binding are spelled differently and the tests carry
// each in its own spelling, because reading them as one string is what the
// class's first cut did: an item holds its repository canonically ("acme/web")
// and an ask holds the storage slug a run is keyed by ("acme-web-3130f6e03f9febba",
// task.Slug), which never holds a slash. A fixture that spelled both the same
// would pass against a check that admits nothing.
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

// orchestratedApprovedAt is the instant every fixture's approval and attempt
// share. The gate covers an attempt only by the approval it spent, and the two
// name it by one instant (orchestrator's approvalCovers): a fixture that left
// them apart would be refused for a reason the test did not mean.
var orchestratedApprovedAt = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

// orchestratedItem is one work item of the orchestrator's record, in the phase a
// committed attempt leaves it in (orchestrator's commitIntent writes running),
// approved over the spec it carries. Its repository is the canonical one the
// node stores (orchestrator-state's schema: task.CanonicalRepo's owner/name).
func orchestratedItem(id, repo string) orchestrator.WorkItem {
	return orchestratedItemIn(id, repo, orchestrator.PhaseRunning)
}

// orchestratedItemIn is that item in a phase of the test's own: the two the
// record has finished (done, cancelled) are the ones the issuer must refuse an
// attempt of, exactly as the orchestrator's own gate does (`reissuable`).
func orchestratedItemIn(id, repo string, phase orchestrator.Phase) orchestrator.WorkItem {
	w := orchestrator.WorkItem{ID: id, Repo: repo, Brief: "the brief the person approved", Phase: phase}
	w.Approved = &orchestrator.Approval{At: orchestratedApprovedAt, SpecDigest: orchestrator.SpecDigest(w), Attempts: 1}
	return w
}

// orchestratedAttempt is one committed attempt of an item the record carries:
// the id internal/orchestrator's IntentID writes, the run id it minted, the
// digest the item carried when the attempt was committed, and the approval it
// spent (orchestratedApprovedAt, the one every item here carries) — with the
// outcome empty while nothing has reported on it.
func orchestratedAttempt(item orchestrator.WorkItem, attempt int, run string, outcome orchestrator.Outcome) orchestrator.Intent {
	return orchestrator.Intent{
		ID:         orchestrator.IntentID(item.ID, attempt),
		RunID:      run,
		SpecDigest: orchestrator.SpecDigest(item),
		ApprovalAt: orchestratedApprovedAt,
		Outcome:    outcome,
	}
}

// orchestratedSlug is the storage slug a launch and a run of repo are keyed by,
// which is what an ask names (task.Slug) — the other spelling of the repository
// an item carries canonically.
func orchestratedSlug(t *testing.T, kind, repo string) string {
	t.Helper()
	slug, err := task.Slug(kind, repo)
	if err != nil {
		t.Fatalf("task.Slug(%s, %q): %v", kind, repo, err)
	}
	return slug
}

// orchestratedAsk is an ask of the orchestrator's class for one attempt, naming
// the repository the way a launch does: by its storage slug.
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
// other classes' check refusing everything and a service that recorded no pause
// at all: the two records a resume is judged against are the record's own and
// this service's, and a test that is not about a resume hands the empty second
// one.
func orchestratedScopeFor(t *testing.T, rec OrchestratorRecord) (ScopeCheck, *judgingScope) {
	t.Helper()
	return orchestratedScopeOver(t, rec, &pausesFake{})
}

// orchestratedScopeOver is that same check over the service's own record of the
// pauses it wrote, which the test hands in (D3189): the resume's evidence is that
// record and nothing else, so a test about a resume states it here.
func orchestratedScopeOver(t *testing.T, rec OrchestratorRecord, pauses PausedNodes) (ScopeCheck, *judgingScope) {
	t.Helper()
	next := &judgingScope{}
	check, err := NewOrchestratedScope(next, rec, pauses)
	if err != nil {
		t.Fatalf("NewOrchestratedScope: %v", err)
	}
	return check, next
}

// TestOrchestratedAskNeedsARecordedUnstartedAttempt is the case that must go
// through and the ones that keep it from being a recorded, unstarted attempt:
// an attempt nothing has reported on, of the repository the ask names, under the
// approval that covers it, is what the class may be issued; an attempt that has
// ended, one the record does not carry, one of an item the record has finished,
// edited since the commit or approved again since, and a reference the
// orchestrator's own spelling could not have written are each refused. And a
// record that could not be read is the SERVICE's failure and never a verdict
// about the caller (R16's ScopeFault rule).
func TestOrchestratedAskNeedsARecordedUnstartedAttempt(t *testing.T) {
	const (
		repo = "acme/web"
		run  = "run-1"
	)
	slug := orchestratedSlug(t, "github", repo)
	item := orchestratedItem("wi-1", repo)
	open := func() *recordFake {
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{item},
			Intents:   []orchestrator.Intent{orchestratedAttempt(item, 1, run, "")},
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

	t.Run("the item's repository on the other provider kind is still it", func(t *testing.T) {
		// The record carries no provider kind — an item's repository is the
		// canonical owner/name and nothing else — so the slug of that name on
		// either kind a repository may use is that repository's, and which job
		// serves it is the account check's question and not this one (R4).
		rec := open()
		check, _ := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, orchestratedAsk(orchestratedSlug(t, "bitbucket", repo), run, "wi-1:1")); err != nil {
			t.Fatalf("the ask naming the item's repository on the other provider kind was refused: %v", err)
		}
	})

	refusals := []struct {
		name string
		ask  identitywire.TicketRequest
		// wantReads is how many times the record is read for this ask: an ask
		// that names no attempt the orchestrator's own spelling could have
		// written is refused before the node is touched, and every other one is
		// judged against it.
		wantReads int
		state     func() *recordFake
	}{
		{
			name:      "an attempt that has ended",
			ask:       orchestratedAsk(slug, run, "wi-1:1"),
			wantReads: 1,
			state: func() *recordFake {
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{item},
					Intents:   []orchestrator.Intent{orchestratedAttempt(item, 1, run, orchestrator.OutcomeFailed)},
				}}
			},
		},
		{
			name:      "an attempt the record does not carry",
			ask:       orchestratedAsk(slug, run, "wi-1:2"),
			wantReads: 1,
			state:     open,
		},
		{
			// The orchestrator's own gate refuses to start an attempt of an
			// item that is done or cancelled (`reissuable`), and a launcher
			// with a modified CLI must not be able to ask the issuer for what
			// that gate refuses: work a person called off is not re-issued.
			name:      "an attempt of an item that was cancelled",
			ask:       orchestratedAsk(slug, run, "wi-1:1"),
			wantReads: 1,
			state: func() *recordFake {
				cancelled := orchestratedItemIn("wi-1", repo, orchestrator.PhaseCancelled)
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{cancelled},
					Intents:   []orchestrator.Intent{orchestratedAttempt(cancelled, 1, run, "")},
				}}
			},
		},
		{
			name:      "an attempt of an item that is done",
			ask:       orchestratedAsk(slug, run, "wi-1:1"),
			wantReads: 1,
			state: func() *recordFake {
				done := orchestratedItemIn("wi-1", repo, orchestrator.PhaseDone)
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{done},
					Intents:   []orchestrator.Intent{orchestratedAttempt(done, 1, run, "")},
				}}
			},
		},
		{
			// A person edited the item after the attempt was committed: the
			// item is no longer the spec the attempt was committed for, and the
			// gate refuses to start it under the spec the person withdrew
			// (abandonEditedSpec, orchestrator's recovery). The approval the
			// attempt spent still stands on the item, naming the spec the
			// attempt ran — a state a document may hold, and the one that leaves
			// the digest as the only thing refusing this — and an edit that
			// revoked the approval instead is refused the same way. The intent
			// is still open in the record: the abandon is the daemon's write,
			// and the issuer does not wait for it.
			name:      "an attempt of an item edited since the commit",
			ask:       orchestratedAsk(slug, run, "wi-1:1"),
			wantReads: 1,
			state: func() *recordFake {
				committed := orchestratedItem("wi-1", repo)
				edited := committed
				edited.Brief = "a brief the attempt was not committed for"
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{edited},
					Intents:   []orchestrator.Intent{orchestratedAttempt(committed, 1, run, "")},
				}}
			},
		},
		{
			// A person approved the item again since the attempt was committed:
			// the standing approval is another one, and the attempt under the
			// one it replaced is not one to start (abandonReplacedApproval). The
			// item's spec is untouched, so the digest still matches and this is
			// the approval's own refusal.
			name:      "an attempt under an approval a person has replaced",
			ask:       orchestratedAsk(slug, run, "wi-1:1"),
			wantReads: 1,
			state: func() *recordFake {
				committed := orchestratedItem("wi-1", repo)
				again := committed
				again.Approved = &orchestrator.Approval{
					At:         orchestratedApprovedAt.Add(time.Minute),
					SpecDigest: orchestrator.SpecDigest(again),
					Attempts:   1,
				}
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{again},
					Intents:   []orchestrator.Intent{orchestratedAttempt(committed, 1, run, "")},
				}}
			},
		},
		{
			// The gate starts only the item's open attempt (openIntentOf): a
			// record carrying two open attempts — a state the daemon does not
			// write, and one the gate reads as the first of them — is refused
			// for the second, exactly as the launcher's own gate refuses it.
			name:      "an open attempt that is not the item's first",
			ask:       orchestratedAsk(slug, "run-2", "wi-1:2"),
			wantReads: 1,
			state: func() *recordFake {
				return &recordFake{state: orchestrator.State{
					WorkItems: []orchestrator.WorkItem{item},
					Intents: []orchestrator.Intent{
						orchestratedAttempt(item, 1, run, ""),
						orchestratedAttempt(item, 2, "run-2", ""),
					},
				}}
			},
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
			if rec.reads != tc.wantReads {
				t.Errorf("the orchestrator's record was read %d times, want %d: the refusals the record can answer are judged against it, and the ones it cannot are refused before it is read", rec.reads, tc.wantReads)
			}
		})
	}

	t.Run("a record that cannot be read is the service's own failure", func(t *testing.T) {
		rec := &recordFake{err: errors.New("test: the node could not be read")}
		check, _ := orchestratedScopeFor(t, rec)
		caller := IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}
		err := check.Check(context.Background(), caller, orchestratedAsk(slug, run, "wi-1:1"))
		var fault *ScopeFault
		if !errors.As(err, &fault) {
			t.Fatalf("an unreadable record answered %v, want a *ScopeFault: a node nobody could read must not be refused as a caller who is not admitted (R16)", err)
		}
		if errors.Is(err, ErrOrchestratedAsk) {
			t.Errorf("an unreadable record was answered as a refusal of the caller: %v", err)
		}
		// The fault carries the caller the service verified, which is the only
		// thing the refusal's one audit line can be written from: this class's
		// caller holds no run identity for C1's ScopeFault.Caller to name, and
		// a fault that carried none would be answered with no line at all
		// (OrchestratedFault, refuseResolve).
		var orchestrated *OrchestratedFault
		if !errors.As(err, &orchestrated) {
			t.Fatalf("the fault does not carry the caller it was verified for: %v", err)
		}
		if orchestrated.Caller != caller {
			t.Errorf("the fault carries the caller %+v, want the verified one %+v", orchestrated.Caller, caller)
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
	const (
		repo  = "acme/web"
		other = "acme/other"
	)
	slug := orchestratedSlug(t, "github", repo)
	otherSlug := orchestratedSlug(t, "github", other)
	record := func() *recordFake {
		one, two, three := orchestratedItem("wi-1", repo), orchestratedItem("wi-2", repo), orchestratedItem("wi-3", other)
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{one, two, three},
			Intents: []orchestrator.Intent{
				orchestratedAttempt(one, 1, "run-1", ""),
				orchestratedAttempt(two, 1, "run-2", ""),
				orchestratedAttempt(three, 1, "run-3", ""),
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

	t.Run("an attempt of the record's other repository is issued when the ask names it", func(t *testing.T) {
		// The class is not bound to one repository: what it may be issued is an
		// attempt the record carries, under that attempt's own run id, and the
		// record here carries one of another repository as well.
		rec := record()
		check, _ := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, orchestratedAsk(otherSlug, "run-3", "wi-3:1")); err != nil {
			t.Fatalf("the ask for a recorded attempt of the record's other repository was refused: %v", err)
		}
	})
}

// TestOrchestratedAskWithContextReposIsRefused holds the second half of D3136:
// an ask of this class may name no context repository at all. The list is signed
// into the run's identity and judged by no scope for this class, so an ask that
// carries one is refused whole — and the refusal costs the database no read,
// because an ask that cannot be the record's does not reach it.
func TestOrchestratedAskWithContextReposIsRefused(t *testing.T) {
	const (
		repo = "acme/web"
		run  = "run-1"
	)
	slug := orchestratedSlug(t, "github", repo)
	for _, repos := range [][]string{{"acme/other"}, {"acme/other", "acme/third"}} {
		t.Run(repos[0], func(t *testing.T) {
			item := orchestratedItem("wi-1", repo)
			rec := &recordFake{state: orchestrator.State{
				WorkItems: []orchestrator.WorkItem{item},
				Intents:   []orchestrator.Intent{orchestratedAttempt(item, 1, run, "")},
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
		repo = "acme/web"
		run  = "run-1"
	)
	slug := orchestratedSlug(t, "github", repo)
	ask := orchestratedAsk(slug, run, "wi-1:1")
	record := func() *recordFake {
		item := orchestratedItem("wi-1", repo)
		return &recordFake{state: orchestrator.State{
			WorkItems: []orchestrator.WorkItem{item},
			Intents:   []orchestrator.Intent{orchestratedAttempt(item, 1, run, "")},
		}}
	}

	for _, class := range []identitywire.CallerClass{identitywire.ClassPerson, identitywire.ClassServiceAccount} {
		t.Run(string(class), func(t *testing.T) {
			rec := record()
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
		rec := record()
		check, next := orchestratedScopeFor(t, rec)
		if err := check.Check(context.Background(), IdentityCaller{Address: "orch@example.invalid", Class: identitywire.ClassOrchestrator}, ask); err != nil {
			t.Fatalf("the orchestrator's ask was refused: %v", err)
		}
		if len(next.calls) != 0 {
			t.Errorf("the orchestrator's ask reached the other classes' check: %+v", next.calls)
		}
	})
}
