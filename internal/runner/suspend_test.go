package runner_test

// PR2's runner half as whole runs: what a run does about the action the
// credentials service put beside its exhausted answer (R19, D3184-D3187). The
// action is the service's decision and never the job's, so every case here is
// about the answer and not about the clock: wait is the in-job wait the runner
// has always taken, halt ends the run with no_credential and the reset the
// service named, a hotfix run takes neither (R10), and pause suspends the run —
// it saves the session, pushes the work in progress, writes pause.json, tells
// the service it has saved, ends `paused` and releases its lease (R12, R18,
// D3185).
//
// The pause's own cases run in the shape a repository on the switch really has
// (B2.2): a job run whose environment carries the credentials service's URL, so
// its calls go through the service and a /pause has somewhere to go. The
// service itself is a stand-in for the deployment and not for the contract: it
// serves /pause and relays everything else to the model upstream's own
// stand-in, exactly as the real service carries both.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// answeredStage is a stage the credentials service's exhausted answer ends,
// with the action the service decided on it (C3, R19): the answer arrives the
// way a real one does, through the gateway's hand-off, and resumeAt is the
// instant only a pause carries.
func answeredStage(t *testing.T, handle func() *runner.RunHandle, reset time.Time, action string, resumeAt time.Time) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		ex := &gateway.Exhausted{ResetAt: reset, Action: action}
		if !resumeAt.IsZero() {
			ex.ResumeAt = resumeAt
		}
		handle().ServiceAnswer(gateway.ServiceAnswer{Exhausted: ex})
		<-ctx.Done() // the answer ends the stage
		return initOnly(t, req.SessionID), ctx.Err()
	}
}

// suspendingStage is a stage the pause answer ends, after the stage has done
// what a stage does: the run's model call, served by the credentials service's
// stand-in, and a commit of its own, in a session of its own. So the pause has
// all three things it acts on — work to push, a session to save, a lease to
// release — and a run that suspended on an empty checkout could not tell them
// apart.
func suspendingStage(handle func() *runner.RunHandle, reset, resumeAt time.Time, statuses *[]int) step {
	inner := calling(statuses, implement("feature"), call{sonnet, 4000})
	return withSession(func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := inner(t, ctx, req)
		if err != nil {
			t.Errorf("the stage failed before the service's answer arrived: %v", err)
		}
		handle().ServiceAnswer(gateway.ServiceAnswer{Exhausted: &gateway.Exhausted{
			ResetAt: reset, Action: gateway.ActionPause, ResumeAt: resumeAt}})
		<-ctx.Done() // the answer ends the stage
		return res, ctx.Err()
	}, "", pauseSessionBody)
}

// noWait is the wait injection every case that must not wait uses: a wait here
// is the bug the action exists to prevent.
func noWait(t *testing.T) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a reset the answer said not to wait for", d)
		return nil
	}
}

// pauseSessionBody is what the suspended run's session file holds. A pause saves
// the file it is handed and nothing else reads it back, so any line stands in
// for a conversation.
const pauseSessionBody = `{"type":"user","message":{"role":"user","content":"do the task"}}` + "\n"

// pausedCode is the refusal the stand-in answers a /pause it does not honour
// with: a coded refusal of the contract's shape (credsvc.ReadWireError), which
// the run names and never acts on. The real code belongs to the service (PR3);
// this one only has to be one this contract could have written.
const pausedCode = "pause_not_answered"

// pauseCall is one /pause the stand-in service received, whole: the pause
// carries no member of its own (R8), so what the call presents is the whole of
// what there is to assert about it.
type pauseCall struct {
	header http.Header
	body   string
}

// pauseService is the credentials service's stand-in for the pause: /pause is
// served here, and every other path is relayed to the model upstream's
// stand-in, exactly as the real service carries a run's model calls beside its
// own routes. status and body override the answer with a refusal — the
// contract's "a run must never end paused on a refused /pause" — and observe
// runs when a /pause arrives, before it is answered, which is where a case
// reads what the run had saved by then.
type pauseService struct {
	mu       sync.Mutex
	calls    []pauseCall
	upstream string
	status   int
	body     string
	observe  func()
}

func (s *pauseService) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, pauseCall{header: r.Header.Clone(), body: string(body)})
	status, refusal, observe := s.status, s.body, s.observe
	s.mu.Unlock()
	if observe != nil {
		// On the service's own goroutine, so it may report with t.Errorf and
		// may not use t.Fatal.
		observe()
	}
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, refusal)
		return
	}
	_, _ = io.WriteString(w, "{}")
}

// asked is every /pause the service received, in order.
func (s *pauseService) asked() []pauseCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pauseCall{}, s.calls...)
}

// relayTo carries a call the stand-in does not serve to the model upstream's
// stand-in, which is what a stand-in needs to be a stand-in for the service
// rather than for the route: a stand-in that refused a run's model calls would
// turn a stray call into a mystery.
func relayTo(upstream string, w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// pauseRun is the whole run the pause is tested on: the fixture's job, on the
// switch, whose job carries the credentials service's URL — so its model calls
// are the service's to serve (relayed to the model upstream's stand-in) and a
// /pause has somewhere to go. It is secretsRunConfig's wiring without the
// secrets: the fixture's own secret is mounted, so nothing is fetched at
// bootstrap and the only call to the service is the pause's own.
func pauseRun(t *testing.T, svc *pauseService) *bk {
	t.Helper()
	h := newHarnessFiles(t, secretsConfig(t, "  first_line_review: off\n"), nil, nil)
	fake, up := anthropicfake.New(t)
	fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 1000, Output: 3})
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierA},
			gateway.HeaderCredential: []string{"repo-shared-a"},
		}
		return rep
	}
	svc.upstream = up.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == runstore.PausePath {
			svc.serve(w, r)
			return
		}
		relayTo(svc.upstream, w, r)
	}))
	t.Cleanup(srv.Close)
	// The job's own Google ID token, which the service verifies a call by: the
	// metadata server's answer, from a stand-in.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)

	h.deps.Env = append(filterEnv(h.deps.Env, runner.CredentialsServiceURLEnv),
		runner.CredentialsServiceURLEnv+"="+srv.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"))
	mode := map[string]string{runner.BudgetModeEnv: "observe"}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := mode[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend = spend
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	// A paused run is a job run: `fugaro exec` reaches no service and is never
	// resumed by one (D149).
	h.deps.Local = false
	h.deps.Project = "aurora"
	h.deps.Execution = cloudRunExecution(fixtureJob())
	g := &gw{harness: h, fake: fake, logs: logs}
	// The switch is what makes the calls go through the service at all (O2):
	// without it the run does not start.
	return publishPolicy(t, g, fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":"enforce","allowed_models":["claude-haiku-4-5","claude-sonnet-5-5"]}}`, projectpolicy.Version))
}

// TestSuspendFollowsTheAnswer: the action the credentials service decided is
// what the run does about an exhausted chain, and the run decides nothing
// about time itself (R19). wait is the in-job wait the runner has always taken
// (D3184); halt ends the run with no_credential and the reset the service
// named; pause is the suspension the service resumes (D3185); and a hotfix run
// never waits and never pauses (R10).
//
// Every case is about the answer and not about the clock: the halt and pause
// cases name a reset inside the job's own timeout, so a run that halted on its
// own clock rather than on the action would wait and be red.
func TestSuspendFollowsTheAnswer(t *testing.T) {
	t.Run("wait waits the reset out in the job", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		var waits []time.Duration
		runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		})
		reset := time.Now().Add(3 * time.Minute)
		resumed := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if !req.Resume {
				t.Error("the stage did not resume the session the answer interrupted")
			}
			return implement("feature")(t, ctx, req)
		}
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionWait, time.Time{}), resumed, review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Halt != nil {
			t.Fatalf("rec = %+v, err = %v; want a run that waited the reset out", rec, err)
		}
		if len(waits) != 1 {
			t.Fatalf("waits = %v; want one wait, for the reset the service named", waits)
		}
		if n := len(h.agent.calls); n != 3 {
			t.Fatalf("%d agent calls; want the implement stage twice (before and after the wait) and the review", n)
		}
	})

	t.Run("halt ends the run with no_credential", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionHalt, time.Time{}))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
			rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
			t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
		}
		if !strings.Contains(rec.Halt.Detail, reset.UTC().Format(time.RFC3339)) {
			t.Errorf("the halt's detail lacks the reset the service named, %s: %q", reset.UTC().Format(time.RFC3339), rec.Halt.Detail)
		}
		if n := len(h.agent.calls); n != 1 {
			t.Fatalf("%d agent calls; the stage must not run again", n)
		}
		assertStoredRecordValidates(t, h.store)
	})

	// The pause the service decided: the run saves what a resume reads, pushes
	// the work in progress, tells the service it has saved, ends `paused` and
	// lets its container stop (R12, R18, D3185). Nothing waits: a job idling
	// for the reset is the whole cost the pause exists to remove, and the run
	// asks the service rather than deciding for itself.
	t.Run("pause suspends the run until the service resumes it", func(t *testing.T) {
		svc := &pauseService{}
		b := pauseRun(t, svc)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		resumeAt := reset.Add(30 * time.Minute)
		var st []int
		rec, err := b.run(t, suspendingStage(handle, reset, resumeAt, &st))
		if err != nil {
			t.Fatalf("a suspended run returned %v, want nil so the job exits 0", err)
		}
		if rec.Status != runstore.StatusPaused || rec.Outcome != runstore.OutcomeNone || rec.Halt != nil {
			t.Fatalf("rec = %+v, halt = %+v; want a run that ended %q", rec, rec.Halt, runstore.StatusPaused)
		}
		if !strings.Contains(rec.Reason, resumeAt.UTC().Format(time.RFC3339)) {
			t.Errorf("the record's reason lacks the instant the service will resume it at, %s: %q",
				resumeAt.UTC().Format(time.RFC3339), rec.Reason)
		}
		if n := len(b.agent.calls); n != 1 {
			t.Fatalf("%d agent calls; the run must not run the stage again", n)
		}
		if len(st) != 1 || st[0] != http.StatusOK {
			t.Fatalf("the stage's model calls returned %v; want the one call the service served", st)
		}
		// The service was told once, with the identity every call to it is
		// signed by (D143) and no member of the run's own choosing (R8).
		calls := svc.asked()
		if len(calls) != 1 {
			t.Fatalf("%d /pause calls; want exactly one", len(calls))
		}
		if calls[0].body != "" {
			t.Errorf("the pause carried %q; it carries no member of its own", calls[0].body)
		}
		if got := calls[0].header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") || got == "Bearer " {
			t.Errorf("the pause presented %q as its bearer; want the job's own ID token", got)
		}
		if got := calls[0].header.Get(gateway.HeaderRunIdentity); got == "" {
			t.Error("the pause presented no run identity")
		}
		if got := calls[0].header.Get(gateway.HeaderRunJob); got != fixtureJob() {
			t.Errorf("the pause reported the job %q, want %q", got, fixtureJob())
		}
		// A suspended run touches no pull request: it is not finished, and the
		// work it pushed is a checkpoint its own branch carries.
		if n := len(b.provider.State.PRs); n != 0 {
			t.Fatalf("%d pull requests; a suspended run opens none", n)
		}
		assertStoredRecordValidates(t, b.store)
	})

	// A refused /pause means the service wrote no node, so nothing will resume
	// this run: ending `paused` here would be a pause no run continues, the one
	// failure this part of the contract exists to prevent. The run ends the way
	// a run whose credential ran out ends, naming the refusal
	// (contract-pause.md §"/pause").
	t.Run("a refused /pause ends the run as a credential-starved one", func(t *testing.T) {
		svc := &pauseService{status: http.StatusForbidden, body: `{"error":"` + pausedCode + `"}`}
		b := pauseRun(t, svc)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		resumeAt := reset.Add(30 * time.Minute)
		var st []int
		rec, err := b.run(t, suspendingStage(handle, reset, resumeAt, &st))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
			rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
			t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
		}
		for _, want := range []string{resumeAt.UTC().Format(time.RFC3339), "403", pausedCode} {
			if !strings.Contains(rec.Halt.Detail, want) {
				t.Errorf("the halt's detail lacks %q: %q", want, rec.Halt.Detail)
			}
		}
		if out := b.outcome(); out == nil || out["status"] != string(runstore.StatusHalted) {
			t.Errorf("outcome = %v; a run refused its pause must not be filed as %q", out, runstore.StatusPaused)
		}
		assertStoredRecordValidates(t, b.store)
	})

	// A run that cannot tell the service it has saved has nothing that would
	// resume it — a local run reaches no service and is never resumed by one
	// (D149) — so it ends as a credential-starved run rather than `paused`.
	// The half-written record it leaves costs a resumed run nothing: a run
	// reads pause.json only when it is launched, and this one is never
	// launched.
	t.Run("a run that cannot tell the service ends as a credential-starved one", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		resumeAt := reset.Add(30 * time.Minute)
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionPause, resumeAt))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
			rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
			t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
		}
		if !strings.Contains(rec.Halt.Detail, resumeAt.UTC().Format(time.RFC3339)) {
			t.Errorf("the halt's detail lacks the instant the service named, %s: %q",
				resumeAt.UTC().Format(time.RFC3339), rec.Halt.Detail)
		}
		if n := len(h.agent.calls); n != 1 {
			t.Fatalf("%d agent calls; the run must not run the stage again", n)
		}
		assertStoredRecordValidates(t, h.store)
	})

	// R10: a hotfix is the run with the least room to spend, so neither a wait
	// that spends the job nor a pause that hands the work to a run of tomorrow
	// is open to it. Whatever the service decided, this run ends — and the halt
	// it would have taken anyway is left to speak for itself.
	t.Run("a hotfix run never waits and never pauses", func(t *testing.T) {
		cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
			"agent:\n", "rigor:\n  default: hotfix\nagent:\n", 1)
		for _, tc := range []struct {
			name, action   string
			resumeAt       func(reset time.Time) time.Time
			theRuleIsNamed bool
		}{
			{"wait", gateway.ActionWait, func(time.Time) time.Time { return time.Time{} }, true},
			{"pause", gateway.ActionPause, func(reset time.Time) time.Time { return reset.Add(30 * time.Minute) }, true},
			{"halt", gateway.ActionHalt, func(time.Time) time.Time { return time.Time{} }, false},
			{"no action at all", "", func(time.Time) time.Time { return time.Time{} }, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t, cfg, nil)
				handle := watchRun(t)
				runner.SetCredsWait(t, noWait(t))
				reset := time.Now().Add(3 * time.Minute)
				rec, err := h.run(t, answeredStage(t, handle, reset, tc.action, tc.resumeAt(reset)))
				if err != nil {
					t.Fatal(err)
				}
				if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
					rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
					t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
				}
				if got := strings.Contains(rec.Halt.Detail, "hotfix"); got != tc.theRuleIsNamed {
					t.Errorf("the halt's detail names the hotfix rule (%v), want %v: %q",
						got, tc.theRuleIsNamed, rec.Halt.Detail)
				}
				if n := len(h.agent.calls); n != 1 {
					t.Fatalf("%d agent calls; the stage must not run again", n)
				}
				assertStoredRecordValidates(t, h.store)
			})
		}
	})
}

// TestSuspendSavesBeforeItExits: a run ends `paused` only once everything a
// resume reads is there and the service has been told — the order the contract
// fixes (contract-pause.md §"/pause"). The assertions are made at the ask,
// from the service's own goroutine, because that is the only moment the
// question can be asked: what the run had saved when it asked. A run that
// asked first and saved afterwards would leave a pause nothing picks up, and
// by the time the run is over the difference is invisible.
func TestSuspendSavesBeforeItExits(t *testing.T) {
	svc := &pauseService{}
	b := pauseRun(t, svc)
	handle := watchRun(t)
	runner.SetCredsWait(t, noWait(t))
	reset := time.Now().Add(3 * time.Minute)
	resumeAt := reset.Add(30 * time.Minute)
	svc.observe = func() {
		ctx := context.Background()
		p, err := b.store.ReadPause(ctx)
		if err != nil {
			t.Errorf("the run asked the service before it wrote %s: %v", runstore.PauseFile, err)
			return
		}
		if p.Version != runstore.PauseVersion || p.Stage != "implement" || !agent.ValidSessionID(p.Session) || !gitops.ValidCommitSHA(p.Checkpoint) {
			t.Errorf("the pause it wrote is not one a resume reads: %+v", p)
		}
		m, data, err := b.store.ReadSession(ctx)
		switch {
		case err != nil:
			t.Errorf("the run asked the service before it saved the session it names: %v", err)
		case m.ID != p.Session:
			t.Errorf("the pause names session %q and the saved session is %q", p.Session, m.ID)
		case !m.Pushed || !gitprov.SameCommit(m.HeadSHA, p.Checkpoint) || int64(len(data)) != m.Bytes:
			// A session saved before the push records that its head was not
			// pushed, and a resume refuses such a session (prepareResume, R18):
			// the run would come back to nothing.
			t.Errorf("the saved session does not stand on the commit the pause pushed: %+v", m)
		}
		rec, err := b.store.ReadRecord(ctx)
		switch {
		case err != nil:
			t.Errorf("reading the run's own record at the ask failed: %v", err)
		case rec.Status != runstore.StatusRunning:
			t.Errorf("the run's record already ended %q when it asked to be suspended", rec.Status)
		}
	}
	var st []int
	rec, err := b.run(t, suspendingStage(handle, reset, resumeAt, &st))
	if err != nil || rec.Status != runstore.StatusPaused {
		t.Fatalf("rec = %+v, err = %v; want a suspended run", rec, err)
	}
	if len(svc.asked()) != 1 {
		t.Fatalf("%d /pause calls; want exactly one", len(svc.asked()))
	}
	// What the pause left is what a resume reads: the session it saved, the
	// commit it pushed — on the remote, which is the one place a run resumes
	// from — and a record that agrees with both.
	p, err := b.store.ReadPause(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tip := remoteTip(t, b.harness); tip != p.Checkpoint {
		t.Errorf("the remote's %s is %s and the pause names %s: a resume would check out no work", "fugaro/"+runID, tip, p.Checkpoint)
	}
	if m, _, err := b.store.ReadSession(context.Background()); err != nil || m.ID != p.Session || !m.Pushed {
		t.Errorf("the session the pause names is not the one a resume would restore: %+v, %v", m, err)
	}
	if !strings.Contains(rec.Reason, p.Checkpoint[:7]) {
		t.Errorf("the record's reason does not name the checkpoint %s: %q", p.Checkpoint[:7], rec.Reason)
	}
}

// TestSuspendReleasesTheLease: a suspended run stops, and everything it held
// while it ran goes with it (R12). The lease is the run's own budget, so a
// pause that kept it would hold money for a job that is not running — and the
// branch lock is what makes the run the only one on its branch: a pause that
// kept it would stop the resumed run from taking it. The outcome node is what
// every reader of the budget sees, so a pause that filed the run as anything
// but `paused` would draw it as a failure or as a success (R15).
func TestSuspendReleasesTheLease(t *testing.T) {
	svc := &pauseService{}
	b := pauseRun(t, svc)
	handle := watchRun(t)
	runner.SetCredsWait(t, noWait(t))
	reset := time.Now().Add(3 * time.Minute)
	resumeAt := reset.Add(30 * time.Minute)
	var st []int
	rec, err := b.run(t, suspendingStage(handle, reset, resumeAt, &st))
	if err != nil || rec.Status != runstore.StatusPaused {
		t.Fatalf("rec = %+v, err = %v; want a suspended run", rec, err)
	}
	if reserved, released, spent := b.runLeaf("reserved"), b.runLeaf("released"), b.runLeaf("spent"); reserved <= 0 || reserved-released != spent {
		t.Errorf("reserved %d, released %d, spent %d: the run holds a lease it is not using", reserved, released, spent)
	}
	if ok, _ := b.bucket.Exists(context.Background(), lock.Key(repoSlug, "fugaro/"+runID)); ok {
		t.Error("a suspended run still holds its branch lock: the run that resumes it could not take it")
	}
	if out := b.outcome(); out == nil || out["status"] != string(runstore.StatusPaused) {
		t.Errorf("outcome = %v; want the run filed %q", out, runstore.StatusPaused)
	}
	if e := b.entry(); e != nil {
		t.Errorf("the run's registry entry was left behind: %v", e)
	}
}
