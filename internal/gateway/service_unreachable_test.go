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
