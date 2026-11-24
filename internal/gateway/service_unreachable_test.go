package gateway

// WS12: an unreachable service is waited for (D2983).
//
// The credentials service this run's calls go to -- its own project's, or the
// workspace's when the run's route moved the model kind there (WS11, D2982) --
// can be down: a revision rolling out, a revision scaled to zero, a grant that
// has not propagated, a service that is simply gone. D2983 is what a run does
// about it, and it is not what it did before this task: the run waits for the
// service and retries, for a bounded time, and then halts with a reason that
// names which service it was.
//
// **Where the two halves are.** The gateway owns the bound and the decision
// (service.go): the first call that cannot reach the service starts the clock,
// every call inside the bound is answered with the Unreachable answer and a
// retryable 503, and the first call that meets the spent bound is the halt. The
// runner owns the wait: the answer ends the stage in flight and stage waits for
// the instant the answer names and resumes the stage's session, which is the
// mechanism an exhausted answer already uses (C1.17,
// internal/runner/creds_wait.go). These tests pin the gateway's half, which is
// the half a run's behaviour is decided by; the runner's half is pinned where
// it lives (internal/runner/creds_wait_test.go) and the two meet in
// TestStageWaitsForAnUnreachableService.
//
// **What is NOT here, stated because it is the old behaviour.** Nothing below
// asserts that a failed call halts the run: that is the red step this task
// turns (the run halted on the first failed call), and the cases that still pin
// a halt pin it for the call that meets the spent bound, which is the only call
// that halts a run now.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// serviceAnswers collects what the gateway handed the run's hook, with the lock
// a hook called from the gateway's own request path needs: the answer is read
// by the test after the call returned, and the two are different goroutines
// whenever the call is served over a connection.
type serviceAnswers struct {
	mu  sync.Mutex
	got []ServiceAnswer
}

func (c *serviceAnswers) hook(a ServiceAnswer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, a)
}

func (c *serviceAnswers) all() []ServiceAnswer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ServiceAnswer(nil), c.got...)
}

// unreachable is the only answer this run was handed, and it is an Unreachable
// one: a call that could not reach the service, with the instant the wait ends
// and the service it was.
func (c *serviceAnswers) unreachable(t *testing.T) Unreachable {
	t.Helper()
	got := c.all()
	if len(got) != 1 {
		t.Fatalf("the run was handed %d service answers, want one: %+v", len(got), got)
	}
	if got[0].Unreachable == nil {
		t.Fatalf("the answer is not an unreachable one: %+v", got[0])
	}
	return *got[0].Unreachable
}

// deadService is an address nothing is listening on: a server that was up and
// is now gone, which is the shape of a service that has just been scaled to
// zero or whose revision is rolling. The port is closed before the case starts
// and the address is loopback, so the gateway admits it exactly as it admits a
// live fake (checkBaseURL).
func deadService(t *testing.T) string {
	t.Helper()
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := gone.URL
	gone.Close()
	return base
}

// bound is a bound short enough for a test to spend it: serviceUnreachableBound
// is minutes long because a real outage is, and no case here waits one out.
func bound(t *testing.T, d time.Duration) {
	t.Helper()
	old := serviceUnreachableBound
	serviceUnreachableBound = d
	t.Cleanup(func() { serviceUnreachableBound = old })
}

// TestAnUnreachableServiceIsWaitedFor: a call the gateway cannot reach the
// credentials service with does not halt the run. The run is handed the wait
// -- the instant the bound ends and which service it is -- and the call itself
// is answered retryably, so the agent is not told the work failed; and a call
// made after the service is back is served, which is what the wait buys.
//
// The case is the service that answers nothing (DropBeforeHeaders): the same
// failure a closed port gives, seen from the side of a service that is still
// listening -- a revision that is up and not serving yet, an instance that went
// away mid-request. It is the answer the run acts on that decides, and not
// which of the two ways the connection failed.
func TestAnUnreachableServiceIsWaitedFor(t *testing.T) {
	bound(t, time.Minute)
	var answers serviceAnswers
	var mu sync.Mutex
	down := true
	h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		mu.Lock()
		defer mu.Unlock()
		if down {
			return anthropicfake.Reply{DropBeforeHeaders: true}
		}
		return serviceOK(TierB, "repo-b")
	}, func(o *Options) { o.OnServiceAnswer = answers.hook })

	before := time.Now()
	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a call to an unreachable service inside the bound: %d %s, want the retryable 503", resp.StatusCode, body)
	}
	if got := resp.Header.Get("x-should-retry"); got == "false" {
		t.Errorf("x-should-retry = %q: the run waits for the service, so this call is not a refusal to come back to", got)
	}
	noHalt(t, h)

	u := answers.unreachable(t)
	if u.Service != serviceNameProject {
		t.Errorf("the wait names %q; this run's model calls go to its own project's service", u.Service)
	}
	if !u.RetryAt.After(before.Add(time.Minute-time.Second)) || u.RetryAt.After(before.Add(time.Minute+time.Second)) {
		t.Errorf("the wait ends at %s, want about a minute after the first call that could not reach the service (%s)", u.RetryAt, before)
	}
	if strings.Contains(body, "repo-b") {
		t.Errorf("the gateway's answer carries a credential's name: %s", body)
	}
	assertNoSecret(t, "the retryable answer", body, secrets(h, t))
	assertNoSecret(t, "the wait's log line", h.logs.String(), secrets(h, t))

	// The service is back, and the run's next call is served: the wait is what
	// the outage cost, and not the run.
	mu.Lock()
	down = false
	mu.Unlock()
	resp, body = h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call after the service came back: %d %s, want the service's own 200", resp.StatusCode, body)
	}
	if f.Count() != 2 {
		t.Fatalf("the service saw %d calls, want the outage's and the one after it", f.Count())
	}
	noHalt(t, h)
}

// TestTheWaitIsBounded: the wait ends. A service that is still unreachable when
// the bound is spent halts the run on the call that meets it, with the halt an
// unreachable budget backend already gets (ReasonBudgetUnavailable) and the
// detail that says what could not be reached -- so the failure a run reports is
// the bound's, and not a stage that timed out with the service to blame.
func TestTheWaitIsBounded(t *testing.T) {
	bound(t, 20*time.Millisecond)
	h := newServiceGateway(t, deadService(t), nil)

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the call the outage starts on: %d %s, want the wait's 503", resp.StatusCode, body)
	}
	noHalt(t, h)

	// The bound is spent: the same call, one bound later, is the halt.
	time.Sleep(25 * time.Millisecond)
	resp, body = h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a call after the bound: %d %s, want the halt's 403", resp.StatusCode, body)
	}
	halt := haltOf(t, h)
	if halt.Reason != ReasonBudgetUnavailable || halt.Scope != "run" {
		t.Errorf("the halt is %+v, want the reason an unreachable service already halts with", halt)
	}
	if halt.Detail != stateDetailUnreachable {
		t.Errorf("the halt's detail is %q, want %q", halt.Detail, stateDetailUnreachable)
	}
	if !strings.Contains(body, stateDetailUnreachable) {
		t.Errorf("the agent wasn't told why: %s", body)
	}

	// The bound is the run's and not the call's: a call after the halt gets the
	// halt, whatever it is.
	if resp, _ := h.post(msg(sonnet, 1000)); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a call after the halt: %d, want 403", resp.StatusCode)
	}
}

// TestTheHaltNamesTheService: the run's halt says *which* credentials service
// could not be reached, because the two belong to different people. A run whose
// model calls go to its own project's service reads that service; a run whose
// route moved the model kind to the workspace reads the workspace's, and never
// the sentence a member's operator would take to their own project.
func TestTheHaltNamesTheService(t *testing.T) {
	t.Run("the project's own service", func(t *testing.T) {
		bound(t, 0)
		h := newServiceGateway(t, deadService(t), nil)
		resp, body := h.post(msg(sonnet, 1000))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a call past the bound: %d %s, want the halt's 403", resp.StatusCode, body)
		}
		halt := haltOf(t, h)
		if halt.Detail != stateDetailUnreachable {
			t.Errorf("the halt's detail is %q, want %q", halt.Detail, stateDetailUnreachable)
		}
		if strings.Contains(halt.Detail, "workspace") {
			t.Errorf("the halt blames the workspace for a service this run never calls: %q", halt.Detail)
		}
	})

	t.Run("the workspace's service", func(t *testing.T) {
		bound(t, 0)
		// The run WS11 builds: its own project's service as the first
		// upstream, and the workspace's as the one the model kind is served
		// by, with the workspace's address the one that is gone. The project's
		// fake is up and would answer, which is what makes the halt's wording
		// the thing this case reads.
		h, project := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
			return serviceOK(TierA, "repo-own-a")
		}, func(o *Options) {
			o.Workspace = &Workspace{
				Address:    deadService(t),
				Job:        serviceJob,
				RunIDToken: func() string { return serviceRunToken(t) },
				IDToken:    func(context.Context, string) (string, error) { return serviceIDToken(t), nil },
			}
			o.ModelFromWorkspace = true
		})
		resp, body := h.post(msg(sonnet, 1000))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a call past the bound: %d %s, want the halt's 403", resp.StatusCode, body)
		}
		halt := haltOf(t, h)
		if halt.Detail != stateDetailUnreachableWorkspace {
			t.Errorf("the halt's detail is %q, want %q", halt.Detail, stateDetailUnreachableWorkspace)
		}
		if !strings.Contains(halt.Detail, "workspace's") {
			t.Errorf("the halt does not name the workspace's service: %q", halt.Detail)
		}
		if project.Count() != 0 {
			t.Errorf("the project's service was called %d times by a run routed to the workspace", project.Count())
		}
		assertNoSecret(t, "the halt's answer", body, secrets(h, t))
	})
}

// TestAnUnreachableServiceNeverFallsBack: an unreachable service is never
// replaced by another destination (D143, D2989). The model kind's holder is the
// workspace's service for the whole run, and when that service cannot be
// reached the run waits for *it*: the project's service is never called, not
// while the wait is on and not once the bound is spent, and the run halts
// naming the service it was waiting for rather than being answered from the
// other one.
//
// The other half of never falling back -- that a service run holds no
// credential of the job's own to reach for, and that a gateway built with one
// is refused at Start -- is TestNoInJobFallback's, where it lives.
func TestAnUnreachableServiceNeverFallsBack(t *testing.T) {
	bound(t, 20*time.Millisecond)
	var answers serviceAnswers
	h, project := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return serviceOK(TierA, "repo-own-a")
	}, func(o *Options) {
		o.Workspace = &Workspace{
			Address:    deadService(t),
			Job:        serviceJob,
			RunIDToken: func() string { return serviceRunToken(t) },
			IDToken:    func(context.Context, string) (string, error) { return serviceIDToken(t), nil },
		}
		o.ModelFromWorkspace = true
		o.OnServiceAnswer = answers.hook
	})

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the call the outage starts on: %d %s, want the wait's 503", resp.StatusCode, body)
	}
	noHalt(t, h)
	u := answers.unreachable(t)
	if u.Service != serviceNameWorkspace {
		t.Errorf("the wait names %q, want the workspace's service: this run's model calls go there and nowhere else", u.Service)
	}
	if strings.Contains(body, "project") {
		t.Errorf("the agent was told about the project's service: %s", body)
	}

	time.Sleep(25 * time.Millisecond)
	resp, _ = h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a call past the bound: %d, want the halt's 403", resp.StatusCode)
	}
	halt := haltOf(t, h)
	if halt.Detail != stateDetailUnreachableWorkspace {
		t.Errorf("the halt's detail is %q, want %q", halt.Detail, stateDetailUnreachableWorkspace)
	}
	if n := project.Count(); n != 0 {
		t.Errorf("the project's service was called %d times: an unreachable workspace's service is waited for, and never fallen through to (D2989)", n)
	}
}

// wsRosterUnreadable is a workspace's credentials service that ANSWERS, and
// answers that it cannot read the state it must read: R3's second shape, where
// the wait is for a service that is reachable and unable to serve this call
// (WC8). It counts the calls it was sent, which is what says whether the run
// waited for it at all and whether it was ever fallen through from.
//
// marker is what the service writes beside that refusal when it is refusing it
// AS the workspace — its cap, stop or settle (workspace_refusal.go) — and "" is
// the roster it could not read, which is the service's own state and no verdict
// about the call. The two arrive as the same 503 state_unavailable, which is why
// the marker and not the status is what tells them apart.
func wsRosterUnreadable(t *testing.T, marker string) (address string, calls func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		if marker != "" {
			w.Header().Set(WorkspaceRefusalHeader, marker)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"` + ErrorStateUnavailable + `","message":"the workspace's roster cannot be read"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// wsRosterHaltStub starts a run whose model kind the route serves from the
// workspace (WS11) and whose calls go to the workspace's service the caller
// built, with the workspace's own slip on them when withSlip is set. marker is
// what that service marks its refusals with, "" for the roster it could not
// read (wsRosterUnreadable).
func wsRosterHaltStub(t *testing.T, withSlip bool, marker string, edit func(*Options)) (*harness, *anthropicfake.Fake, func() int) {
	t.Helper()
	address, calls := wsRosterUnreadable(t, marker)
	h, project := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return serviceOK(TierA, "repo-own-a")
	}, func(o *Options) {
		o.Workspace = &Workspace{
			Address:    address,
			Job:        serviceJob,
			RunIDToken: func() string { return serviceRunToken(t) },
			IDToken:    func(context.Context, string) (string, error) { return serviceIDToken(t), nil },
		}
		if withSlip {
			o.Workspace.SlipID, o.Workspace.SlipClaim = "ws-slip-"+t.Name(), "ws-claim-"+t.Name()
		}
		o.ModelFromWorkspace = true
		if edit != nil {
			edit(o)
		}
	})
	return h, project, calls
}

// TestTheWorkspaceRosterWaitIsNotRearmedByEveryRefusal: a refusal this run
// waits for is not an answer that ends the outage, so the bound is measured
// from the FIRST such refusal and the halt at its end stays reachable (R3,
// WC8). An answer of any kind clears the outage clock (noteServiceReached), so
// a refusal read after that clearing has to put it back: re-armed by every
// call, the bound is never spent, the run idles in cycles of it until the job's
// own clock stops it, and the halt that is supposed to name the workspace's
// roster is never reached.
//
// Two calls inside one bound are the whole of the case: the second waits for
// the same instant as the first, which is true only of a clock the first one
// started. The bound is a minute, so neither call can meet it however slow the
// machine is, and every call this run makes is answered by the workspace's
// service — the project's is never reached (D2989).
func TestTheWorkspaceRosterWaitIsNotRearmedByEveryRefusal(t *testing.T) {
	bound(t, time.Minute)
	var answers serviceAnswers
	h, project, wsCalls := wsRosterHaltStub(t, true, "", func(o *Options) { o.OnServiceAnswer = answers.hook })

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a call the workspace refused as unavailable: %d %s, want the wait's retryable 503", resp.StatusCode, body)
	}
	noHalt(t, h)
	resp, body = h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the second such call: %d %s, want the wait's 503 while the bound is unspent", resp.StatusCode, body)
	}
	noHalt(t, h)

	waits := answers.all()
	if len(waits) != 2 || waits[0].Unreachable == nil || waits[1].Unreachable == nil {
		t.Fatalf("the run was handed %d answers, want two waits", len(waits))
	}
	if first, second := waits[0].Unreachable.RetryAt, waits[1].Unreachable.RetryAt; !first.Equal(second) {
		t.Errorf("the second refusal moved the wait from %s to %s: the bound is the run's, and a refusal is not the service coming back (R3)", first, second)
	}
	if got := wsCalls(); got != 2 {
		t.Errorf("the workspace's service received %d calls, want two: every call goes to the service the route names", got)
	}
	if got := project.Count(); got != 0 {
		t.Errorf("the project's own service was called %d times by a run routed to the workspace (D2989)", got)
	}
}

// TestTheWorkspaceRosterWaitIsBounded: the wait ends, and the halt it ends on
// names the roster rather than the connection (R3, WC8). A bound already spent
// when the first refusal arrives is the halt on that call: the service
// answered, so the sentence an operator reads is the state it could not read,
// and not the one a service that could not be reached gets.
func TestTheWorkspaceRosterWaitIsBounded(t *testing.T) {
	bound(t, 0)
	h, project, _ := wsRosterHaltStub(t, true, "", nil)

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a refusal meeting the spent bound: %d %s, want the halt's 403", resp.StatusCode, body)
	}
	halt := haltOf(t, h)
	if halt.Reason != ReasonBudgetUnavailable || halt.Scope != "run" {
		t.Errorf("the halt is %+v, want the reason an unavailable service already halts with", halt)
	}
	if halt.Detail != stateDetailWorkspaceRoster {
		t.Errorf("the halt's detail is %q, want %q: the service answered, and what it could not do is read the roster", halt.Detail, stateDetailWorkspaceRoster)
	}
	if !strings.Contains(body, stateDetailWorkspaceRoster) {
		t.Errorf("the agent wasn't told why: %s", body)
	}
	if strings.Contains(halt.Detail, "could not be reached") {
		t.Errorf("the halt blames the connection for a service that answered: %q", halt.Detail)
	}
	if got := project.Count(); got != 0 {
		t.Errorf("the project's own service was called %d times by a run routed to the workspace (D2989)", got)
	}
	assertNoSecret(t, "the halt's answer", body, secrets(h, t))
}

// TestARunWithNoWorkspaceSlipHaltsOnTheStateItCannotRead: R3's roster re-read
// happens under the workspace's slip, so a run routed to the workspace that
// holds none made no read for this call to fail, and its "I cannot read the
// state" is the halt it always was — the sentence a run of the member's own
// service gets, with no wait and no workspace roster named (R4's last clause,
// D143).
//
// The bound is a minute, so a wait wrongly taken here is the 503 this test
// would fail on rather than a halt reached by a spent clock.
func TestARunWithNoWorkspaceSlipHaltsOnTheStateItCannotRead(t *testing.T) {
	bound(t, time.Minute)
	var answers serviceAnswers
	h, project, wsCalls := wsRosterHaltStub(t, false, "", func(o *Options) { o.OnServiceAnswer = answers.hook })

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a refusal to a run holding no workspace slip: %d %s, want the halt's 403", resp.StatusCode, body)
	}
	halt := haltOf(t, h)
	if want := refusalDetail(ErrorStateUnavailable); halt.Detail != want {
		t.Errorf("the halt's detail is %q, want %q: a run with no workspace slip is served exactly as it was", halt.Detail, want)
	}
	if strings.Contains(halt.Detail, "workspace") {
		t.Errorf("the halt names a roster this call never read: %q", halt.Detail)
	}
	if got := answers.all(); len(got) != 0 {
		t.Errorf("the run was handed %+v: a run with no workspace slip waits for no roster (R4)", got)
	}
	if got := wsCalls(); got != 1 {
		t.Errorf("the workspace's service received %d calls, want one", got)
	}
	if got := project.Count(); got != 0 {
		t.Errorf("the project's own service was called %d times by a run routed to the workspace (D2989)", got)
	}
}

// TestAWorkspaceRefusalIsNeverWaitedFor: an answer the workspace's own service
// MARKED is a verdict about the call and not an outage, so it halts on the call
// that met it — the run is not sent to wait for a service that answered, and the
// halt keeps the marker the run's record is read for (D2022's O19, R3).
//
// All three of the workspace's refusals reach a member as this same 503
// state_unavailable, which is what the wait's trigger reads and why the marker
// and not the status has to decide: waited for, a cap, a stop or a settle would
// idle the run for the whole bound and then halt it naming the roster, with the
// refusal the workspace really made absent from its record. The bound is a
// minute, so a wait taken wrongly here is the 503 this test fails on.
func TestAWorkspaceRefusalIsNeverWaitedFor(t *testing.T) {
	bound(t, time.Minute)
	for _, reason := range WorkspaceRefusals() {
		t.Run(reason, func(t *testing.T) {
			var answers serviceAnswers
			h, project, wsCalls := wsRosterHaltStub(t, true, reason, func(o *Options) { o.OnServiceAnswer = answers.hook })

			resp, body := h.post(msg(sonnet, 1000))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("the workspace's %s refusal: %d %s, want the halt's 403", reason, resp.StatusCode, body)
			}
			halt := haltOf(t, h)
			if halt.WorkspaceRefusal != reason {
				t.Errorf("the halt for the workspace's %s refusal carries %q: the marker is the one fact the refusal is read for", reason, halt.WorkspaceRefusal)
			}
			if want := refusalDetail(ErrorStateUnavailable); halt.Detail != want {
				t.Errorf("the halt's detail is %q, want %q: a marked refusal is the state the service could not read, as it was before the roster wait (R3)", halt.Detail, want)
			}
			if got := answers.all(); len(got) != 0 {
				t.Errorf("the run was handed %+v: a service that answered is not an outage to wait out (R3)", got)
			}
			if got := wsCalls(); got != 1 {
				t.Errorf("the workspace's service received %d calls, want one: a marked refusal is a verdict, and is not sent again", got)
			}
			if got := project.Count(); got != 0 {
				t.Errorf("the project's own service was called %d times by a run routed to the workspace (D2989)", got)
			}
			assertNoSecret(t, "the halt's answer", body, secrets(h, t))
		})
	}
}
