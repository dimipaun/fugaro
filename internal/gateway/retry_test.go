package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The gateway's one retry (issue #1827, SC2): a Messages call whose upstream
// failed before any byte of its answer had reached the agent is sent once
// more -- the same request, the same upstream, the same credential and the
// same reservation -- and nothing else is ever retried. These tests are the
// narrow form and its edges: it happens exactly once, only before a byte has
// reached the agent, never for a 4xx other than 429 and never for the
// credentials service, and the ledger sees one reservation and one
// settlement.

// fastRetry shortens the gateway's retry delay for a test, and restores it
// after: these tests are about the retry's rules and not about how long it
// waits, and the suite should not spend the delay once per case.
func fastRetry(t *testing.T) {
	t.Helper()
	old := upstreamRetryDelay
	upstreamRetryDelay = 20 * time.Millisecond
	t.Cleanup(func() { upstreamRetryDelay = old })
}

// retryLines are the "budget: call retried" lines.
func retryLines(t *testing.T, l *logBuf) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range l.lines(t) {
		if m["msg"] == "budget: call retried" {
			out = append(out, m)
		}
	}
	return out
}

// onlyRetryLine is the call's one retry line: the count is observable here
// and nowhere else, so a second retry would be a second line.
func onlyRetryLine(t *testing.T, l *logBuf) map[string]any {
	t.Helper()
	lines := retryLines(t, l)
	if len(lines) != 1 {
		t.Fatalf("%d retry lines, want 1 (there is one retry); log:\n%s", len(lines), l.String())
	}
	return lines[0]
}

// noRetryLine fails when anything was retried.
func noRetryLine(t *testing.T, l *logBuf) {
	t.Helper()
	if lines := retryLines(t, l); len(lines) != 0 {
		t.Fatalf("%d retry lines, want none: %v\nlog:\n%s", len(lines), lines, l.String())
	}
}

// A transport error before the upstream said anything is the case the retry
// exists for: nothing of an answer existed, so the second attempt is the
// call's first answer as far as the agent is concerned. It is sent once --
// twice in all -- with the same body, and it is the one retry line. The
// attempt that failed had its request written, though, so the upstream may
// have served it and this gateway read nothing of what that cost: the call
// settles at its reservation, of which the retry's own usage -- still read
// off its answer, tokens included -- is the part that reconciles.
func TestRetriesOnceOnAPreByteTransportError(t *testing.T) {
	fastRetry(t)
	u := pricing.Usage{Input: 11, Output: 13}
	drop := anthropicfake.StreamOK(sonnet, u)
	drop.DropBeforeHeaders = true
	h := newHarness(t, drop, anthropicfake.StreamOK(sonnet, u))
	body := msg(sonnet, 100, `"stream":true`)
	resp, got := h.post(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "message_stop") {
		t.Fatalf("the call answered %d %q, want the second attempt's stream", resp.StatusCode, got)
	}
	if n := h.fake.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2: the call and its one retry", n)
	}
	if first, second := string(h.fake.Body(0)), string(h.fake.Body(1)); first != body || second != body {
		t.Errorf("the retry's body is not the first attempt's:\n%q\n%q\nwant\n%q", first, second, body)
	}
	w := worst(t, sonnet, body, 100, "")
	if rep := h.gw.EndStage(); rep.Calls != 1 || rep.Used != w || rep.Unreconciled != w-cost(t, sonnet, u) {
		t.Errorf("the stage recorded %d calls using %d (%d unreconciled), want 1 call at the reservation %d: the retry's usage reconciles only that much of it", rep.Calls, rep.Used, rep.Unreconciled, w)
	} else if want := int64(u.Input + u.Output); rep.Tokens != want {
		t.Errorf("the stage counted %d tokens, want %d: the usage of the attempt that answered is still read", rep.Tokens, want)
	}
	if call := h.logs.lastCall(t); call["settled"] != "reserved" {
		t.Errorf("the call settled %v, want the reservation: an attempt written and lost may have reached the upstream", call["settled"])
	}
	line := onlyRetryLine(t, h.logs)
	if line["retry"] != float64(1) || line["reason"] != "transport" || line["status"] != float64(0) {
		t.Errorf("retry line retry/reason/status = %v/%v/%v, want 1/transport/0", line["retry"], line["reason"], line["status"])
	}
	if line["stage"] != defaultStage.Name || line["model"] != sonnet {
		t.Errorf("retry line names stage %v model %v, want %s/%s", line["stage"], line["model"], defaultStage.Name, sonnet)
	}
	if ms := num(line["delay_ms"]); ms != int64(upstreamRetryDelay.Milliseconds()) || ms <= 0 {
		t.Errorf("delay_ms = %d, want the delay it waited (%d)", ms, upstreamRetryDelay.Milliseconds())
	}
}

// A first attempt dropped after its request had been written, and a retry the
// upstream answered with a failure status. The 503 is the agent's answer --
// nothing else of an answer exists -- but it is not what the call cost: the
// attempt before it may have reached the upstream and generated for it, and
// this gateway read no status and no usage for it. A 503 generates nothing
// either, so pricing the call from the retry alone would charge the run for
// one call out of two that may have been served, and under `enforce` that
// spend would pass `granted` with nothing in the ledger to explain it: the
// call settles at its reservation instead, which is what the same attempt
// costs without a retry at all. The status, the error type and the call
// itself are still read off the answer the agent got.
func TestALostAttemptIsChargedWhateverTheRetryAnswered(t *testing.T) {
	fastRetry(t)
	drop := anthropicfake.StreamOK(sonnet, smallUsage)
	drop.DropBeforeHeaders = true
	h := newHarness(t, drop, anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "overloaded"))
	body := msg(sonnet, 1000, `"stream":true`)
	resp, b := h.post(body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the agent got %d %q, want the retry's own 503 relayed", resp.StatusCode, b)
	}
	if typ, _ := apiError(t, b); typ != "api_error" {
		t.Errorf("the agent got error type %q, want the retry's api_error", typ)
	}
	if n := h.fake.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2: the call and its one retry", n)
	}
	w := worst(t, sonnet, body, 1000, "")
	rep := h.gw.EndStage()
	if rep.Calls != 1 || rep.Used != w || rep.Unreconciled != w {
		t.Errorf("report %+v, want 1 call charged the whole reservation %d: the attempt it lost may have reached the upstream", rep, w)
	}
	if l := h.gw.Ledger(); l.Used != w || l.Reserved != 0 {
		t.Errorf("ledger %+v, want %d charged and nothing left reserved", l, w)
	}
	if call := h.logs.lastCall(t); call["settled"] != "reserved" || num(call["status"]) != http.StatusServiceUnavailable {
		t.Errorf("call log %v, want the retry's own 503 settled at the reservation", call)
	}
	line := onlyRetryLine(t, h.logs)
	if line["reason"] != "transport" || num(line["status"]) != 0 {
		t.Errorf("retry line reason/status = %v/%v, want transport/0: the first attempt answered nothing", line["reason"], line["status"])
	}
}

// The same rule where the failed attempt's answer is not the agent's at all:
// a provider's error is never copied to it, so the call the retry answers is
// the gateway's own 502 and the attempt the provider may have received is
// still worth its reservation. A provider route settles in fewer places than
// the Anthropic one (its non-2xx answer never reaches the pump), which is why
// the charge is decided once, where the call settles, and not on the way out
// of any one branch of it.
func TestALostAttemptOnAProviderRouteIsChargedToo(t *testing.T) {
	fastRetry(t)
	body := msg(dsModel, 100)
	drop := anthropicfake.MessageOK(dsModel, smallUsage)
	drop.DropBeforeHeaders = true
	r := feeRouted(t, 0, nil, routedStage, dsRoute,
		drop, anthropicfake.Error(http.StatusInternalServerError, "api_error", "boom"))
	resp, b := r.post(body)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(b, "the provider answered with status 500") {
		t.Fatalf("the agent got %d %q, want the gateway's own sentence for the retry's 500", resp.StatusCode, b)
	}
	if n := r.prov.Count(); n != 2 {
		t.Fatalf("the provider saw %d requests, want 2: the call and its one retry", n)
	}
	w := worst(t, dsModel, body, 100, "")
	rep := r.gw.EndStage()
	if rep.Calls != 1 || rep.Used != w || rep.Unreconciled != w {
		t.Errorf("report %+v, want 1 call charged the whole reservation %d: the provider may have received the lost attempt", rep, w)
	}
	if call := r.logs.lastCall(t); call["settled"] != "reserved" {
		t.Errorf("call log %v, want settled reserved", call)
	}
	onlyRetryLine(t, r.logs)
}

// A 5xx, or a 429: the upstream's own failure, or its answer that the call
// should come back later. Neither is a fact about the work, and nothing of
// either reached the agent, so the call goes again. Neither is a lost attempt
// either -- the upstream refused the call and generated nothing for it -- so
// the retry's own answer is the whole charge.
func TestRetriesOnceOnAnUpstreamFailureStatus(t *testing.T) {
	fastRetry(t)
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			u := pricing.Usage{Input: 7, Output: 9}
			h := newHarness(t, anthropicfake.Error(status, "api_error", "come back later"), anthropicfake.StreamOK(sonnet, u))
			resp, got := h.post(msg(sonnet, 100, `"stream":true`))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the call answered %d %q, want the retry's 200", resp.StatusCode, got)
			}
			if n := h.fake.Count(); n != 2 {
				t.Fatalf("the upstream saw %d requests, want 2", n)
			}
			line := onlyRetryLine(t, h.logs)
			if line["reason"] != "status" || num(line["status"]) != int64(status) {
				t.Errorf("retry line reason/status = %v/%v, want status/%d", line["reason"], line["status"], status)
			}
			if rep := h.gw.EndStage(); rep.Used != cost(t, sonnet, u) {
				t.Errorf("the stage used %d, want %d (one settlement)", rep.Used, cost(t, sonnet, u))
			}
		})
	}
}

// Every other status is the call's answer: a 4xx is about the request or the
// credential, which the same request sent again would get again, and the
// agent is handed it.
func TestNoRetryOnAStatusThatIsAboutTheRequest(t *testing.T) {
	fastRetry(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
		h := newHarness(t, anthropicfake.Error(status, "invalid_request_error", "no"), anthropicfake.StreamOK(sonnet, smallUsage))
		resp, _ := h.post(msg(sonnet, 100))
		if resp.StatusCode != status {
			t.Errorf("%d: the agent got %d, want the upstream's own status relayed", status, resp.StatusCode)
		}
		if n := h.fake.Count(); n != 1 {
			t.Errorf("%d: the upstream saw %d requests, want 1: a 4xx is not retried", status, n)
		}
		noRetryLine(t, h.logs)
	}
	// The same on a provider route, whose error is never copied to the agent:
	// the gateway answers its own sentence, and the provider is not asked
	// again.
	r := feeRouted(t, 0, nil, routedStage, dsRoute,
		anthropicfake.Error(http.StatusBadRequest, "invalid_request_error", "no"), anthropicfake.MessageOK(dsModel, smallUsage))
	resp, b := r.post(msg(dsModel, 100))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(b, "the provider answered with status 400") {
		t.Errorf("a provider's 400: the agent got %d %q, want the gateway's own 400", resp.StatusCode, b)
	}
	if n := r.prov.Count(); n != 1 {
		t.Errorf("a provider's 400: the provider saw %d requests, want 1", n)
	}
	noRetryLine(t, r.logs)
}

// Once a byte of the answer has reached the agent, the answer is its and not
// the gateway's: a stream cut after its first event is never replayed. The
// call settles as it always did (from the cut stream, or at its reservation).
func TestNoRetryOnceAByteHasReachedTheAgent(t *testing.T) {
	fastRetry(t)
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.CutAfter = 1 // message_start reaches the agent, then the upstream's end drops
	h := newHarness(t, rep, anthropicfake.StreamOK(sonnet, smallUsage))
	resp, got := h.post(msg(sonnet, 100, `"stream":true`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the agent got %d, want the 200 the upstream had already answered", resp.StatusCode)
	}
	if !strings.Contains(got, "message_start") {
		t.Fatalf("the agent got no byte of the stream: %q", got)
	}
	if n := h.fake.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1: a stream the agent has begun reading is never sent again", n)
	}
	noRetryLine(t, h.logs)
}

// A 2xx whose body never arrived is the same rule one step further: the
// gateway has already written the status and the headers to the agent, so
// there is nothing left to retry -- a second answer could not be sent to a
// client that is already reading the first.
func TestNoRetryOnceTheStatusHasReachedTheAgent(t *testing.T) {
	fastRetry(t)
	// A body shorter than the length it announced: the upstream's end closes
	// the connection with the status already sent and not one byte of the
	// body, which is as close to "the answer never began" as a 2xx gets.
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.Events = nil
	rep.Body = ""
	rep.Header = http.Header{"Content-Length": {"64"}}
	h := newHarness(t, rep, anthropicfake.StreamOK(sonnet, smallUsage))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, _ := h.do(h.request(ctx, "/v1/messages", msg(sonnet, 100, `"stream":true`)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the agent got %d, want the 200 the upstream had already answered", resp.StatusCode)
	}
	if n := h.fake.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1: the status is the agent's already", n)
	}
	noRetryLine(t, h.logs)
}

// The reservation is the one the client request made and the settlement is
// the one the attempt that answered earns: the retry happens inside
// handleMessages' single reserve/settle pair, so the lease is asked once,
// nothing is released early, and the call is charged once.
func TestRetryKeepsOneReservationAndOneSettlement(t *testing.T) {
	fastRetry(t)
	u := pricing.Usage{Input: 11, Output: 13}
	l := &fakeLease{}
	h := newHarnessWith(t, withLease(l, Enforce),
		anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "overloaded"),
		anthropicfake.StreamOK(sonnet, u))
	if resp, b := h.post(msg(sonnet, 100, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if n := h.fake.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2", n)
	}
	if n := l.grantCalls(); n != 1 {
		t.Errorf("the lease was asked %d times, want 1: the retry reserves nothing", n)
	}
	led := h.gw.Ledger()
	if led.Reserved != 0 {
		t.Errorf("reserved %d after the call settled, want 0", led.Reserved)
	}
	if want := cost(t, sonnet, u); led.Used != want {
		t.Errorf("used %d, want %d: one call, charged once", led.Used, want)
	}
	if rep := h.gw.EndStage(); rep.Calls != 1 || rep.Used != cost(t, sonnet, u) {
		t.Errorf("the stage recorded %d calls using %d, want 1 call at %d", rep.Calls, rep.Used, cost(t, sonnet, u))
	}
	l.mu.Lock()
	released := len(l.released)
	l.mu.Unlock()
	if released != 0 {
		t.Errorf("the lease was released %d times before Close, want 0: the retry releases nothing", released)
	}
}

// retryDialFails is an upstream the gateway reaches once and cannot dial
// again: the first request goes to the fake as usual, and the second fails
// before anything is written -- the shape of a retry that never leaves the
// client, whatever the first attempt did.
type retryDialFails struct {
	tries atomic.Int32
}

func (d *retryDialFails) RoundTrip(r *http.Request) (*http.Response, error) {
	if d.tries.Add(1) > 1 {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// A retry that never left the client is not an attempt: the call is answered
// for as the first attempt left it. The first attempt's own flag decides the
// charge -- an attempt the upstream may have received is charged the whole
// reservation even though the retry after it never got out of the client --
// and the status the upstream did send is the agent's, not a gateway error put
// in its place. Either mistake would be a fact this gateway does not have.
func TestARetryThatNeverLeftTheClientIsNotAnAttempt(t *testing.T) {
	fastRetry(t)
	for _, c := range []struct {
		name        string
		reply       anthropicfake.Reply
		wantStatus  int
		wantCharged bool
	}{
		{"a dropped connection", anthropicfake.Reply{DropBeforeHeaders: true}, http.StatusBadGateway, true},
		{"a 503", anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "busy"), http.StatusServiceUnavailable, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dial := &retryDialFails{}
			h := newHarnessWith(t, func(o *Options) { o.Client = &http.Client{Transport: dial} }, c.reply)
			body := msg(sonnet, 1000, `"stream":true`)
			resp, b := h.post(body)
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("the agent got %d %q, want %d", resp.StatusCode, b, c.wantStatus)
			}
			if n := dial.tries.Load(); n != 2 {
				t.Fatalf("the gateway made %d requests, want 2: the call and its one retry", n)
			}
			if n := h.fake.Count(); n != 1 {
				t.Fatalf("the upstream saw %d requests, want 1: the retry never left the client", n)
			}
			onlyRetryLine(t, h.logs)
			w := worst(t, sonnet, body, 1000, "")
			rep := h.gw.EndStage()
			call := h.logs.lastCall(t)
			if c.wantCharged {
				if rep.Used != w || rep.Unreconciled != w {
					t.Errorf("report %+v, want the reservation %d charged: the first attempt may have reached the upstream", rep, w)
				}
				if call["settled"] != "reserved" {
					t.Errorf("log %v, want settled reserved", call)
				}
				return
			}
			if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
				t.Errorf("ledger %+v, want nothing charged: the upstream answered the first attempt", l)
			}
			if call["settled"] != "zero" || num(call["status"]) != int64(c.wantStatus) {
				t.Errorf("log %v, want the upstream's own %d settled zero", call, c.wantStatus)
			}
		})
	}
}

// retryWroteThenFails is an upstream the gateway reaches twice where the retry
// is written and then lost: the first request goes to the fake as usual, and
// the second gets as far as its request headers before the connection dies --
// the shape of a retry that left the client and came back with nothing. The
// first attempt's answer is wrapped so the test can see it closed.
type retryWroteThenFails struct {
	tries atomic.Int32
	first *countedCloser // the first attempt's body, nil when it answered none
}

func (d *retryWroteThenFails) RoundTrip(r *http.Request) (*http.Response, error) {
	if d.tries.Add(1) > 1 {
		if tr := httptrace.ContextClientTrace(r.Context()); tr != nil && tr.WroteHeaders != nil {
			tr.WroteHeaders() // the request was written before the connection died
		}
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	}
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	d.first = &countedCloser{ReadCloser: resp.Body}
	resp.Body = d.first
	return resp, nil
}

// countedCloser counts the Close calls on a response body.
type countedCloser struct {
	io.ReadCloser
	closes atomic.Int32
}

func (c *countedCloser) Close() error {
	c.closes.Add(1)
	return c.ReadCloser.Close()
}

// A retry that left the client is the call's answer, whatever it came back
// with: the attempt whose request was written is the one that may have reached
// the upstream, and the first attempt's answer is the gateway's to discard --
// never relayed, its body closed where it is dropped. Answering for the call
// from the first attempt instead would hand the agent a 503 this gateway has
// decided not to use, and settle an attempt that was written at zero, which is
// the under-charge the reservation exists to prevent.
func TestARetryThatLeftTheClientIsTheCallsAnswer(t *testing.T) {
	fastRetry(t)
	for _, c := range []struct {
		name        string
		first       anthropicfake.Reply
		wantBodyOut bool // the first attempt answered, so it had a body to close
	}{
		{"a 503 first attempt", anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "busy"), true},
		{"a dropped first attempt", anthropicfake.Reply{DropBeforeHeaders: true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			up := &retryWroteThenFails{}
			h := newHarnessWith(t, func(o *Options) { o.Client = &http.Client{Transport: up} }, c.first)
			body := msg(sonnet, 1000, `"stream":true`)
			resp, b := h.post(body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("the agent got %d %q, want the gateway's 502: the retry left the client and answered nothing", resp.StatusCode, b)
			}
			if n := up.tries.Load(); n != 2 {
				t.Fatalf("the gateway made %d requests, want 2: the call and its one retry", n)
			}
			if n := h.fake.Count(); n != 1 {
				t.Fatalf("the upstream saw %d requests, want 1: the retry died in the transport", n)
			}
			onlyRetryLine(t, h.logs)
			w := worst(t, sonnet, body, 1000, "")
			rep := h.gw.EndStage()
			if rep.Calls != 1 || rep.Used != w || rep.Unreconciled != w {
				t.Errorf("the stage recorded %d calls using %d (%d unreconciled), want 1 call at the reservation %d: the written attempt may have reached the upstream", rep.Calls, rep.Used, rep.Unreconciled, w)
			}
			if call := h.logs.lastCall(t); call["settled"] != "reserved" {
				t.Errorf("the call settled %v, want settled reserved", call["settled"])
			}
			if !c.wantBodyOut {
				return
			}
			if up.first == nil {
				t.Fatalf("the first attempt's body never reached the gateway")
			}
			if n := up.first.closes.Load(); n != 1 {
				t.Errorf("the first attempt's body was closed %d times, want 1: it is dropped unread", n)
			}
		})
	}
}

// tokenGoneAfterOneCall is a Vertex credential the run can present once: the
// first attempt's request is built with it and the retry's cannot be -- a
// token that expired between the two attempts, the one way this happens.
type tokenGoneAfterOneCall struct {
	calls atomic.Int32
}

func (s *tokenGoneAfterOneCall) Token() (*oauth2.Token, error) {
	if s.calls.Add(1) > 1 {
		return nil, errors.New("oauth2: token expired and refresh token is not available")
	}
	return &oauth2.Token{AccessToken: vertexAccessToken, TokenType: "Bearer"}, nil
}

// A retry the gateway cannot build is not a retry: nothing is sent again, so
// the call is answered for exactly as the first attempt left it -- the status
// the upstream sent, or the gateway's own error when that attempt was written
// and lost -- and never as a call with no attempt behind it, which would
// answer from a request that does not exist and lose the charge an attempt the
// upstream may have received still owes. No second request was made, so no
// line says one was.
func TestARetryThatCannotBeBuiltIsNotARetry(t *testing.T) {
	fastRetry(t)
	for _, c := range []struct {
		name        string
		reply       anthropicfake.Reply
		wantStatus  int
		wantCharged bool
	}{
		{"a dropped connection", anthropicfake.Reply{DropBeforeHeaders: true}, http.StatusBadGateway, true},
		{"a 503", anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "busy"), http.StatusServiceUnavailable, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tok := &tokenGoneAfterOneCall{}
			h := newHarnessWith(t, func(o *Options) {
				o.Upstream = vertexUpstream(o.Upstream.BaseURL)
				o.Upstream.Token = tok
			}, c.reply)
			body := vertexBody(1000, `"stream":true`)
			resp, b := h.vertexPost(vertexPath("proj-1234", "us-east5", sonnet, "streamRawPredict"), body)
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("the agent got %d %q, want %d", resp.StatusCode, b, c.wantStatus)
			}
			if n := tok.calls.Load(); n != 2 {
				t.Fatalf("the credential was asked %d times, want 2: the call and the retry it could not build", n)
			}
			if n := h.fake.Count(); n != 1 {
				t.Fatalf("the upstream saw %d requests, want 1: nothing was sent again", n)
			}
			noRetryLine(t, h.logs)
			w := worst(t, sonnet, body, 1000, "")
			rep := h.gw.EndStage()
			call := h.logs.lastCall(t)
			if c.wantCharged {
				if rep.Used != w || rep.Unreconciled != w {
					t.Errorf("report %+v, want the reservation %d charged: the first attempt may have reached the upstream", rep, w)
				}
				if call["settled"] != "reserved" {
					t.Errorf("log %v, want settled reserved", call)
				}
				return
			}
			if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
				t.Errorf("ledger %+v, want nothing charged: the upstream answered the first attempt", l)
			}
			if call["settled"] != "zero" || num(call["status"]) != int64(c.wantStatus) {
				t.Errorf("log %v, want the upstream's own %d settled zero", call, c.wantStatus)
			}
		})
	}
}

// The retry line, and every other line the call writes, carries the call and
// no credential: not the provider's key, not the run's own gateway token and
// not one byte of the request. The same call is pinned on a provider route,
// whose key is the one this gateway holds for another account.
func TestRetryLineCarriesNoCredential(t *testing.T) {
	fastRetry(t)
	const nonce = "NONCE-PROMPT-7c21ab"
	u := pricing.Usage{Input: 11, Output: 13}
	r := feeRouted(t, 0, nil, routedStage, dsRoute,
		anthropicfake.Error(http.StatusServiceUnavailable, "api_error", "overloaded"), anthropicfake.MessageOK(dsModel, u))
	body := `{"model":"` + dsModel + `","max_tokens":1000,"messages":[{"role":"user","content":"` + nonce + `"}]}`
	if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	line := onlyRetryLine(t, r.logs)
	if line["reason"] != "status" || num(line["status"]) != http.StatusServiceUnavailable {
		t.Errorf("retry line reason/status = %v/%v, want status/503", line["reason"], line["status"])
	}
	seen := r.prov.Seen()
	if len(seen) != 2 {
		t.Fatalf("the provider saw %d requests, want 2", len(seen))
	}
	first, second := seen[0].Header.Get("Authorization"), seen[1].Header.Get("Authorization")
	if first == "" || first != second {
		t.Errorf("the retry used credential %q after %q, want the same one", second, first)
	}
	for _, bad := range []string{nonce, providerKey, r.gw.Token(), "x-api-key", "authorization", "anthropic-version"} {
		if strings.Contains(r.logs.String(), bad) {
			t.Errorf("a log line of the retried call carries %q:\n%s", bad, r.logs.String())
		}
	}
}
