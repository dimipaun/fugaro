package credsvc

// PR3's tests: the action the credentials service decides when every source a
// run's chain can reach is exhausted, the record it writes when the run tells it
// it has saved, and the grant the resident orchestrator is later given from that
// record (docs/model-credentials-routing/contract-pause.md, R8, R12, R19, D3184-
// D3191).
//
// The fixture is the slip tests' own (newSlips, slip_verify_test.go's claimedSlip
// and the two headers a call under a slip carries), wired one step further: an
// installation that runs a RESIDENT ORCHESTRATOR (D3186), which is what the
// pause's two times need — a project with no orchestrator suspends nothing — and
// the real record store over a fake database, because what a resume is granted
// from is a record that survived the call that wrote it and must be readable
// through the same store the deployment reads it with.
//
// Nothing here states a credential's value: the names are the contract's own and
// the fixture's, and the only thing a line may carry is a name.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// pauses is the slip fixture's service, with an installation that has a resident
// orchestrator and a run holding a claimed slip of its own.
type pauses struct{ *slips }

func newPauses(t *testing.T, mutate func(*svc, *Config)) *pauses {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	store := slipStore(t, db)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.Slips = store
		ac := s.auth.cfg
		ac.LauncherAccounts = []string{testLauncherAccount}
		// The resident orchestrator (D3186): the class that resumes, and the
		// reason a pause is a pause here rather than a halt.
		ac.OrchestratorAccounts = []string{testOrchestratorSA}
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("the fixture's classes cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = a, a
		cfg.JobAccounts = append(cfg.JobAccounts, JobAccount{Email: testSiblingSA, Slug: s.slug, Repo: testRepo})
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	return &pauses{slips: &slips{svc: s, db: db, store: store}}
}

// withPause writes the project's pause block into the published policy the
// fixture serves: the settings the decision is made with (PR9).
func (s *pauses) withPause(waitInJob, minPause, maxPause string) {
	s.policy.shared.Pause = &projectpolicy.Pause{WaitInJob: waitInJob, MinPause: minPause, MaxPause: maxPause}
}

// withoutOrchestrator turns the project's own switch off, which is D3186's
// second half: the installation runs one and this project does not use it.
func (s *pauses) withoutOrchestrator() {
	off := false
	s.policy.shared.OrchestratorEnabled = &off
}

// personLauncher is the fixture's own run launcher: a person, which is the
// launcher a run's pause is recorded with and the one a resume is granted to.
var personLauncher = routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}

// answerPause is the exhausted answer's own half: what the service decides for
// the fixture's run when every source is exhausted, and what it records.
func (s *pauses) answerPause(t *testing.T, resumes string, reset time.Time) (runstore.PauseAction, time.Time) {
	t.Helper()
	return s.answerPauseAs(t, personLauncher, resumes, reset)
}

// answerPauseAs is the same answer for a run whose own slip records launcher as
// its launcher (R8): the launcher is the service's record of the run's grant and
// never the job's word about itself, and it is what decides whether a pause
// could ever be resumed.
func (s *pauses) answerPauseAs(t *testing.T, launcher routing.Principal, resumes string, reset time.Time) (runstore.PauseAction, time.Time) {
	t.Helper()
	return s.server.answerExhausted(context.Background(), s.slug, s.run, resumes, launcher, s.now, reset)
}

// pauseCall is the run's own report that it has saved: the /pause POST under the
// slip it holds and the two identities every run call carries.
func (s *pauses) pauseCall(t *testing.T, sl aSlip, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, runstore.PausePath, "", append([]func(*http.Request){
		s.jobIdentities(), withSlip(sl.id, sl.secret),
	}, mutate...)...)
}

// pauseOK takes the run's whole pause: the service is made to decide it, the run
// reports it, and the test fails if either half was refused.
func (s *pauses) pauseOK(t *testing.T, sl aSlip) {
	t.Helper()
	if a, _ := s.answerPause(t, "", s.now.Add(2*time.Hour)); a != runstore.ActionPause {
		t.Fatalf("the service decided %q where the fixture means to pause", a)
	}
	resp, body := s.pauseCall(t, sl)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the run's own report is answered %d, want 200: %s", resp.StatusCode, body)
	}
}

// record reads a pause back through the store the service wrote it with, which
// is what a resume is granted from.
func (s *pauses) record(t *testing.T, slug, run string) (creds.Pause, bool) {
	t.Helper()
	rec, found, err := s.store.Pause(context.Background(), slug, run)
	if err != nil {
		t.Fatalf("reading the pause of %s/%s back: %v", slug, run, err)
	}
	return rec, found
}

// node reads the paused node the service wrote, through the contract's own
// reader: the database's own record is what the orchestrator resumes.
func (s *pauses) node(t *testing.T, slug, run string) (*runstore.PauseNode, bool) {
	t.Helper()
	raw, ok := s.nodes.nodes[runstore.PauseNodePath(slug, run)]
	if !ok {
		return nil, false
	}
	b, ok := raw.(json.RawMessage)
	if !ok {
		t.Fatalf("the node is held as %T, want the object the service stored", raw)
	}
	n, err := runstore.ParsePauseNode(b)
	if err != nil {
		t.Fatalf("the node the service wrote is not one this contract reads: %v", err)
	}
	return n, true
}

// writePause records a pause as the service would have, for the resume tests:
// what a resume is granted from is a record, and the tests below ask about the
// grant rather than about the recording of it.
func (s *pauses) writePause(t *testing.T, slug, run string, rec creds.Pause) {
	t.Helper()
	if rec.ResumeAt == 0 {
		rec.ResumeAt = s.now.Add(-time.Minute).UnixMilli()
	}
	if rec.Chain == "" {
		rec.Chain = run
	}
	if err := s.store.WritePause(context.Background(), slug, run, rec); err != nil {
		t.Fatalf("writing the pause of %s/%s: %v", slug, run, err)
	}
}

// resumeBody is the ask of a run that continues a pause: the run the grant is
// for, the account it will call as, and the paused run it continues.
func resumeBody(t *testing.T, slug, run, resumes string, credentials ...string) string {
	t.Helper()
	return mustBody(t, SlipGrantRequest{
		Slug: slug, Run: run, Account: testJobSA,
		Credentials: credentials, MaxRuntimeSeconds: 3600, Resumes: resumes,
	})
}

// grantAs sends one grant call as the caller holding tok, whatever class that
// token is of.
func (s *pauses) grantAs(t *testing.T, body, tok string) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, SlipPath, body, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok)
	})
}

// slipFound reports whether a slip of that id is in the store this service
// writes with: what a refused grant must leave unwritten (slipIDs' one-id
// reading, for a test that has the id it looked for).
func (s *pauses) slipFound(t *testing.T, id string) (creds.Slip, bool) {
	t.Helper()
	sl, found, err := s.store.Slip(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the slip %q back: %v", id, err)
	}
	return sl, found
}

// resumeOK asks for a resume as the orchestrator's class and answers what the
// service granted, failing the test on any refusal.
func (s *pauses) resumeOK(t *testing.T, body string) SlipGrantResponse {
	t.Helper()
	resp, answer := s.grantAs(t, body, s.orchestratorToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the resume is answered %d, want 200: %s", resp.StatusCode, answer)
	}
	var ans SlipGrantResponse
	if err := json.Unmarshal(answer, &ans); err != nil {
		t.Fatalf("the resume's answer is not one this contract reads: %v", err)
	}
	if ans.Slip == "" {
		t.Fatal("the resume answered no slip id")
	}
	return ans
}

// recordingPools is the launcher's-pool lookup as the resume tests read it: what
// it was asked about, and the one pool it designates.
type recordingPools struct {
	principal routing.Principal
	named     string
	reads     int
	pool      string
}

func (p *recordingPools) LauncherPool(_ context.Context, principal routing.Principal, named string) (routing.PoolRef, bool, error) {
	p.reads++
	p.principal, p.named = principal, named
	return routing.PoolRef{Pool: p.pool}, true, nil
}

// designatePools installs that lookup for the duration of one test. The service
// assigns the real one as it is built (PL4), so a test that means to read what
// the RESUME asked it must install its own after construction.
func designatePools(t *testing.T, p *recordingPools) {
	t.Helper()
	prev := LauncherPools
	LauncherPools = p
	t.Cleanup(func() { LauncherPools = prev })
}

// ---------------------------------------------------------------------------
// The decision
// ---------------------------------------------------------------------------

// TestTheActionIsTheServices is the acceptance's first sentence (D3184-D3190):
// the service chooses the exhausted answer's action out of its own knowledge of
// the reset, the installation and the project's settings, and the job decides
// nothing about time itself.
//
// One sub-test per branch of the order the contract states, plus the two facts
// the ANSWER is held to: the members are carried only for a caller that sent the
// stage header (R24, F57), and a staged answer names the instant the run will be
// resumed at and not one the run chose.
func TestTheActionIsTheServices(t *testing.T) {
	t.Run("a reset nearer than wait_in_job is waited out in the job", func(t *testing.T) {
		s := newPauses(t, nil)
		a, at := s.answerPause(t, "", s.now.Add(3*time.Minute))
		if a != runstore.ActionWait {
			t.Errorf("the action is %q, want %q: the reset is nearer than wait_in_job (D3184)", a, runstore.ActionWait)
		}
		if !at.IsZero() {
			t.Errorf("a wait names the instant %v, want none: nothing is paused", at)
		}
	})

	t.Run("an installation with no resident orchestrator halts", func(t *testing.T) {
		// D3186: nothing would resume a pause this service recorded, so the run
		// ends where a credential-starved one ends.
		s := newPauses(t, func(_ *svc, cfg *Config) {
			ac := cfg.Auth.(*Authenticator).cfg
			ac.OrchestratorAccounts = nil
			a, err := NewAuthenticator(ac)
			if err != nil {
				t.Fatalf("the fixture's classes cannot be configured: %v", err)
			}
			cfg.Auth = a
		})
		a, at := s.answerPause(t, "", s.now.Add(2*time.Hour))
		if a != runstore.ActionHalt {
			t.Errorf("the action is %q, want %q: the installation runs no orchestrator (D3186)", a, runstore.ActionHalt)
		}
		if !at.IsZero() {
			t.Errorf("a halt names the instant %v, want none", at)
		}
	})

	t.Run("a project whose own switch is off halts", func(t *testing.T) {
		// The other half of the same reading: the installation runs one and this
		// project does not use it (OrchestratorOn).
		s := newPauses(t, nil)
		s.withoutOrchestrator()
		if a, _ := s.answerPause(t, "", s.now.Add(2*time.Hour)); a != runstore.ActionHalt {
			t.Errorf("the action is %q, want %q: this project's orchestrator switch is off", a, runstore.ActionHalt)
		}
	})

	t.Run("a service account's run halts", func(t *testing.T) {
		// The launcher is the run's own slip's, and a service account's run is
		// never resumed: a slip's owner is a launcher's verified numeric account
		// id and a service account has none (noGrantForTheOrchestrator), so a
		// pause recorded for one is a record the resume grant refuses. The
		// decision halts it rather than stranding the chain.
		s := newPauses(t, nil)
		sa := routing.Principal{Kind: routing.PrincipalServiceAccount, ID: testJobSA}
		a, at := s.answerPauseAs(t, sa, "", s.now.Add(2*time.Hour))
		if a != runstore.ActionHalt {
			t.Errorf("the action is %q, want %q: a service account's run has no owner a resumed run's slip could record", a, runstore.ActionHalt)
		}
		if !at.IsZero() {
			t.Errorf("a halt names the instant %v, want none", at)
		}
		// A reset the run would wait out is still a wait: it needs nothing of
		// this service and nothing of a resume.
		if a, _ := s.answerPauseAs(t, sa, "", s.now.Add(3*time.Minute)); a != runstore.ActionWait {
			t.Errorf("the action is %q, want %q: a near reset is waited out wherever the run was launched from", a, runstore.ActionWait)
		}
	})

	t.Run("a reset beyond max_pause halts", func(t *testing.T) {
		s := newPauses(t, nil) // defaults: 5m, 30m, 6h
		a, at := s.answerPause(t, "", s.now.Add(7*time.Hour))
		if a != runstore.ActionHalt {
			t.Errorf("the action is %q, want %q: the reset is beyond max_pause (D3187)", a, runstore.ActionHalt)
		}
		if !at.IsZero() {
			t.Errorf("a halt names the instant %v, want none", at)
		}
	})

	t.Run("otherwise it pauses", func(t *testing.T) {
		s := newPauses(t, nil)
		reset := s.now.Add(2 * time.Hour)
		a, at := s.answerPause(t, "", reset)
		if a != runstore.ActionPause {
			t.Fatalf("the action is %q, want %q", a, runstore.ActionPause)
		}
		if !at.Equal(reset) {
			t.Errorf("the resume instant is %v, want the reset %v: it is this service's figure (R8)", at, reset)
		}
	})

	t.Run("the answer carries the action only for a caller that sent the stage", func(t *testing.T) {
		s := newPauses(t, nil)
		reset := s.now.Add(2 * time.Hour)
		binding := &slipBinding{
			id: anIdNobodyWrote, owner: testLauncherSub, allowed: []string{testSlipCredential},
			slip: &creds.Slip{
				ID: anIdNobodyWrote, Owner: testLauncherSub, Slug: s.slug, Run: s.run,
				Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
			},
		}
		id := Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA, JobClass: ClassRepository}

		asked := func(stage bool) ExhaustedUntil {
			t.Helper()
			rec := httptest.NewRecorder()
			ctx := withCallSlip(context.Background(), binding)
			s.server.refuseSelection(ctx, rec, id, &creds.ExhaustedUntil{At: reset}, nil, stage)
			if rec.Code != StatusExhaustedUntil {
				t.Fatalf("the answer is %d, want the typed wait (%d): %s", rec.Code, StatusExhaustedUntil, rec.Body)
			}
			eu, ok, err := ReadExhaustedUntil(rec.Code, rec.Body.Bytes())
			if err != nil || !ok {
				t.Fatalf("the answer is not one this contract reads (ok %v): %v %s", ok, err, rec.Body)
			}
			return eu
		}

		staged := asked(true)
		if staged.Action != runstore.ActionPause {
			t.Errorf("the staged answer's action is %q, want %q", staged.Action, runstore.ActionPause)
		}
		if !staged.ResumeAt.Equal(reset) {
			t.Errorf("the staged answer's resume instant is %v, want %v", staged.ResumeAt, reset)
		}
		// A gateway of a release behind decodes the refusal strictly, so the
		// members must be absent for a caller that sent no stage (R24, F57): a
		// member it does not know would halt a run this release means to pause.
		unstaged := asked(false)
		if unstaged.Action != "" || !unstaged.ResumeAt.IsZero() {
			t.Errorf("the unstaged answer carries action %q and the instant %v, want neither", unstaged.Action, unstaged.ResumeAt)
		}
	})
}

// TestTheInstantsMemberRidesWithTheAction is the answer's own body: resume_at
// is written for a pause and for nothing else. A wait, a halt and the
// actionless answer that carried the member would be a body this release's own
// gateway refuses ("only a pause carries a resume instant"), which halts a run
// the service meant to hold in its job, and, for the actionless answer, one a
// gateway of a release behind refuses too — its reader decodes the refusal
// strictly, so the member is exactly what R24 and F57 are about. The zero
// time's milliseconds are a large negative number, which omitempty does not
// drop, so the member is written only where the decision has an instant.
func TestTheInstantsMemberRidesWithTheAction(t *testing.T) {
	instant := time.UnixMilli(1800000000000).UTC()
	for _, tc := range []struct {
		name string
		ans  ExhaustedUntil
		want string
	}{
		{"a pause carries it", ExhaustedUntil{ResetAt: instant, Action: runstore.ActionPause, ResumeAt: instant.Add(30 * time.Minute)},
			`"resume_at":1800001800000`},
		{"a wait carries none", ExhaustedUntil{ResetAt: instant, Action: runstore.ActionWait}, ""},
		{"a halt carries none", ExhaustedUntil{ResetAt: instant, Action: runstore.ActionHalt}, ""},
		{"the actionless answer carries none", ExhaustedUntil{ResetAt: instant}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteExhaustedUntil(rec, tc.ans, instant)
			body := rec.Body.String()
			if got := strings.Contains(body, `"resume_at"`); got != (tc.want != "") {
				t.Fatalf("the body carries resume_at: %v, want %v: %s", got, tc.want != "", body)
			}
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("the body is %s, want it to carry the instant %s", body, tc.want)
			}
			if _, ok, err := ReadExhaustedUntil(rec.Code, rec.Body.Bytes()); err != nil || !ok {
				t.Errorf("the answer this service wrote is not one its own reader reads (ok %v): %v %s", ok, err, body)
			}
		})
	}
}

// TestAPauseIsNeverShorterThanTheMinimum is D3190 as amended: a pause is the
// reset's own length only from min_pause upward, so a run is never resumed
// against a state that has not moved.
func TestAPauseIsNeverShorterThanTheMinimum(t *testing.T) {
	s := newPauses(t, nil)
	s.withPause("5m", "30m", "6h")
	reset := s.now.Add(10 * time.Minute) // past wait_in_job, short of min_pause
	a, at := s.answerPause(t, "", reset)
	if a != runstore.ActionPause {
		t.Fatalf("the action is %q, want %q", a, runstore.ActionPause)
	}
	if want := s.now.Add(30 * time.Minute); !at.Equal(want) {
		t.Errorf("the resume instant is %v, want %v: no pause is shorter than min_pause", at, want)
	}
}

// TestTotalPausedTimeIsBounded is D3190's other half: the chain's paused time is
// carried forward by the record of the pause a run continues, and a pause that
// would take the chain's total past max_pause is a halt rather than a pause.
//
// The service cannot walk the runs bucket a chain lives in, so the carry is the
// only reading it has — and it is what keeps a chain from pausing for ever, one
// follow-up at a time.
func TestTotalPausedTimeIsBounded(t *testing.T) {
	const paused = "20261120-090000-abcd"
	s := newPauses(t, nil)
	// The chain has already been suspended for five of its six hours.
	s.writePause(t, s.slug, paused, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
		Chain:    "chain-root", PausedSeconds: int64(5 * time.Hour / time.Second),
	})

	a, at := s.answerPause(t, paused, s.now.Add(2*time.Hour))
	if a != runstore.ActionHalt {
		t.Fatalf("the action is %q, want %q: the chain's paused time is five hours and this pause would take it past six (D3190)", a, runstore.ActionHalt)
	}
	if !at.IsZero() {
		t.Errorf("a halt names the instant %v, want none", at)
	}

	// A reset short enough to fit inside the bound still pauses, which is what
	// makes that a fact about the bound rather than about the record's presence.
	a, _ = s.answerPause(t, paused, s.now.Add(30*time.Minute))
	if a != runstore.ActionPause {
		t.Errorf("the action is %q, want %q: half an hour still fits inside the chain's bound", a, runstore.ActionPause)
	}
}

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

// TestAPauseNobodyAnsweredIsRefused is the service's half of R12's once-only
// rule: a /pause is honoured only for a run this service last answered `pause`,
// and only once per answer. Everything else — a run it answered `wait`, a run it
// answered nothing, a run reporting the same pause twice — is refused, writes no
// record and no node, and leaves exactly one audit line.
func TestAPauseNobodyAnsweredIsRefused(t *testing.T) {
	newRunSlip := func(t *testing.T, s *pauses) aSlip { return s.claimedSlip(t, 3600, testSlipCredential) }

	t.Run("a run the service answered nothing", func(t *testing.T) {
		s := newPauses(t, nil)
		sl := newRunSlip(t, s)
		before := s.audit.Len()
		resp, body := s.pauseCall(t, sl)
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
		if _, found := s.record(t, s.slug, s.run); found {
			t.Error("a refused pause left a record")
		}
		if _, ok := s.node(t, s.slug, s.run); ok {
			t.Error("a refused pause left a node")
		}
		wantOnePauseLine(t, s, before, ErrorRequestRefused)
	})

	t.Run("a run the service answered wait", func(t *testing.T) {
		s := newPauses(t, nil)
		sl := newRunSlip(t, s)
		if a, _ := s.answerPause(t, "", s.now.Add(3*time.Minute)); a != runstore.ActionWait {
			t.Fatalf("the fixture means to be answered a wait, got %q", a)
		}
		resp, body := s.pauseCall(t, sl)
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
		if _, ok := s.node(t, s.slug, s.run); ok {
			t.Error("a run answered `wait` wrote a node")
		}
	})

	t.Run("the same pause reported twice", func(t *testing.T) {
		s := newPauses(t, nil)
		sl := newRunSlip(t, s)
		s.pauseOK(t, sl)
		rec, _ := s.record(t, s.slug, s.run)
		resp, body := s.pauseCall(t, sl)
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
		again, found := s.record(t, s.slug, s.run)
		if !found || again.ResumeAt != rec.ResumeAt {
			t.Errorf("the record moved on a second report: %+v, want %+v", again, rec)
		}
	})

	t.Run("a run presenting no slip", func(t *testing.T) {
		// The launcher and the pool a pause is recorded with are the run's own
		// grant, so a run with no slip has nothing this service could record
		// (R8). It is a refusal on the CALL and carries no run identity of its
		// own to bind.
		s := newPauses(t, nil)
		resp, body := s.slipPost(t, runstore.PausePath, "", s.jobIdentities())
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a slip-less report is answered 200: %s", body)
		}
		if _, ok := s.node(t, s.slug, s.run); ok {
			t.Error("a slip-less report wrote a node")
		}
	})
}

// TestThePausedLauncherIsTheServicesRecord is R8's load-bearing clause: the
// launcher the pause is recorded with — and so the principal a resume spends —
// is the one this service verified when it wrote the run's slip, and never
// anything the run's own container says about itself.
func TestThePausedLauncherIsTheServicesRecord(t *testing.T) {
	s := newPauses(t, nil)
	sl := s.claimedSlip(t, 3600, testSlipCredential)
	s.pauseOK(t, sl)

	rec, found := s.record(t, s.slug, s.run)
	if !found {
		t.Fatal("the pause left no record")
	}
	if rec.Launcher.ID != testLauncherSub || rec.Launcher.Kind != routing.PrincipalPerson {
		t.Errorf("the record's launcher is %+v, want the slip's own principal %q", rec.Launcher, testLauncherSub)
	}
	if rec.Chain != s.run {
		t.Errorf("the chain is %q, want the run's own id %q: a run that continues no pause roots a chain of its own", rec.Chain, s.run)
	}
	if rec.ResumedBy != "" || rec.Cancelled {
		t.Errorf("a fresh record carries resumed_by %q and cancelled %v", rec.ResumedBy, rec.Cancelled)
	}

	n, ok := s.node(t, s.slug, s.run)
	if !ok {
		t.Fatal("the pause wrote no node: nothing would resume this run")
	}
	if n.Launcher != testLauncherSub {
		t.Errorf("the node's launcher is %q, want %q", n.Launcher, testLauncherSub)
	}
	if n.Launcher == testJobSA {
		t.Error("the node names the job's own account, which is the run's word about itself and not this service's record of its grant")
	}
	if n.Chain != s.run {
		t.Errorf("the node's chain is %q, want %q", n.Chain, s.run)
	}
	if n.ResumeAt != rec.ResumeAt || int64(n.PausedSeconds) != rec.PausedSeconds {
		t.Errorf("the node and the record disagree: node %+v, record %+v", n, rec)
	}
}

// ---------------------------------------------------------------------------
// The resume
// ---------------------------------------------------------------------------

// TestAResumedRunDrawsOnTheOriginalLaunchersPool is PR3's red step (D3191): the
// pool a resumed run is served from is the PAUSED run's launcher and the pool
// its own launch named — resolved through PL6 — and never the orchestrator's,
// which designates nothing for anybody.
//
// The grant's own slip is the evidence: its owner and its launcher are the
// record's, so a run that continues a person's paused run spends that person's
// tokens.
func TestAResumedRunDrawsOnTheOriginalLaunchersPool(t *testing.T) {
	const (
		pausedRun  = "20261120-090000-abcd"
		resumedRun = "20261120-120000-beef"
	)
	s := newPauses(t, nil)
	pools := &recordingPools{pool: "team"}
	designatePools(t, pools)
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
		Pool:     "team", Chain: pausedRun, PausedSeconds: 1800,
	})

	ans := s.resumeOK(t, resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential))
	sl := s.slip(t, ans.Slip)

	if sl.Launcher.ID != testLauncherSub || sl.Launcher.Kind != routing.PrincipalPerson {
		t.Errorf("the resumed run's slip names %+v as its launcher, want the paused run's own principal %q (D3191)", sl.Launcher, testLauncherSub)
	}
	if sl.Owner != testLauncherSub {
		t.Errorf("the slip's owner is %q, want the paused run's launcher %q: a resumed run spends that principal's tokens", sl.Owner, testLauncherSub)
	}
	if !sl.Launcher.Valid() {
		t.Error("the resumed run's slip names no launcher, and its every call would fail to audit")
	}
	if pools.reads != 1 {
		t.Fatalf("the launcher's pool was resolved %d time(s), want once", pools.reads)
	}
	if pools.principal.ID != testLauncherSub {
		t.Errorf("the pool was resolved for %+v, want the paused run's launcher %q and never the orchestrator's", pools.principal, testLauncherSub)
	}
	if pools.named != "team" {
		t.Errorf("the pool was resolved out of %q, want the pool the paused run's launch named (PL6)", pools.named)
	}
	if sl.Pool != "team" {
		t.Errorf("the slip's pool is %q, want the one the lookup designated", sl.Pool)
	}
	if sl.Resumes != pausedRun {
		t.Errorf("the slip names %q as the pause it continues, want %q", sl.Resumes, pausedRun)
	}
}

// TestOnlyTheOrchestratorMayResume is R13: the resume grant is the resident
// orchestrator's act. A person's and a roster service account's class are
// admitted the grant of a NEW run alone, whatever their token says.
func TestOnlyTheOrchestratorMayResume(t *testing.T) {
	const (
		pausedRun  = "20261120-090000-abcd"
		resumedRun = "20261120-120000-beef"
	)
	s := newPauses(t, nil)
	designatePools(t, &recordingPools{pool: "team"})
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}, Chain: pausedRun,
	})
	body := resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential)

	resp, answer := s.grantAs(t, body, s.launcherToken())
	wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)

	// And the same ask from the orchestrator's own class is granted, which is
	// what makes that a fact about the class rather than about the ask.
	if _, answer := s.grantAs(t, body, s.orchestratorToken()); strings.Contains(string(answer), "\"error\"") {
		t.Fatalf("the orchestrator's own resume is refused: %s", answer)
	}
}

// TestAResumeIsSingleUse is the once-only rule (D3189): a pause is resumed once,
// and the mark is taken by the grant that is served — a second ask for the same
// pause is refused, whatever else it names.
func TestAResumeIsSingleUse(t *testing.T) {
	const (
		pausedRun  = "20261120-090000-abcd"
		resumedRun = "20261120-120000-beef"
	)
	s := newPauses(t, nil)
	designatePools(t, &recordingPools{pool: "team"})
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}, Chain: pausedRun,
	})
	tok := s.orchestratorToken()
	resp, answer := s.grantAs(t, resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first resume is answered %d, want 200: %s", resp.StatusCode, answer)
	}
	rec, _ := s.record(t, s.slug, pausedRun)
	if rec.ResumedBy != resumedRun {
		t.Errorf("the record names %q as the run that resumed it, want %q", rec.ResumedBy, resumedRun)
	}

	other := "20261120-130000-cafe"
	resp, answer = s.grantAs(t, resumeBody(t, s.slug, other, pausedRun, testSlipCredential), tok)
	wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)
	// And the second ask wrote no slip of its own: a resume granted twice would
	// be two runs spending one launcher's tokens for one pause.
	if _, found := s.slipFound(t, other); found {
		t.Error("the refused resume wrote a slip")
	}
}

// TestNoResumeBeforeItsTime is the service's own instant (R8, D3190): a pause is
// not resumable before the time the SERVICE computed, and a caller that believes
// otherwise is refused.
func TestNoResumeBeforeItsTime(t *testing.T) {
	const (
		pausedRun  = "20261120-090000-abcd"
		resumedRun = "20261120-120000-beef"
	)
	s := newPauses(t, nil)
	designatePools(t, &recordingPools{pool: "team"})
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
		ResumeAt: s.now.Add(time.Hour).UnixMilli(), Chain: pausedRun,
	})
	resp, answer := s.grantAs(t, resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential), s.orchestratorToken())
	wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)
	if rec, _ := s.record(t, s.slug, pausedRun); rec.ResumedBy != "" {
		t.Errorf("an early ask marked the pause resumed by %q", rec.ResumedBy)
	}

	// The same ask once the instant has passed is served, which is what makes
	// the refusal a fact about the time and not about the record.
	s.now = s.now.Add(2 * time.Hour)
	if _, answer := s.grantAs(t, resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential), s.orchestratorToken()); strings.Contains(string(answer), "\"error\"") {
		t.Fatalf("the resume is refused once its time has passed: %s", answer)
	}
}

// TestNoResumeOfACancelledPause is D3197: a pause its launcher cancelled is
// never resumed, and the mark is what stops it — the record is still readable as
// the pause it was.
func TestNoResumeOfACancelledPause(t *testing.T) {
	const (
		pausedRun  = "20261120-090000-abcd"
		resumedRun = "20261120-120000-beef"
	)
	s := newPauses(t, nil)
	designatePools(t, &recordingPools{pool: "team"})
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
		Chain:    pausedRun, Cancelled: true,
	})
	resp, answer := s.grantAs(t, resumeBody(t, s.slug, resumedRun, pausedRun, testSlipCredential), s.orchestratorToken())
	wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)
	if rec, found := s.record(t, s.slug, pausedRun); !found || !rec.Cancelled || rec.ResumedBy != "" {
		t.Errorf("a cancelled pause was disturbed: %+v (found %v)", rec, found)
	}
}

// ---------------------------------------------------------------------------
// The lines
// ---------------------------------------------------------------------------

// wantOnePauseLine holds a refused /pause to the rule every refusal of this
// service is held to (C1.13): exactly one audit line, of the pause's own purpose,
// carrying no value of the run's.
func wantOnePauseLine(t *testing.T, s *pauses, before int, status string) {
	t.Helper()
	lines := parseAuditLines(t, s.audit.String()[before:])
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit line(s), want exactly one", len(lines))
	}
	wantSlipLine(t, lines[0], PurposePause, status, nil)
}
