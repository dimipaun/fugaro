package gateway

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// A SERVED route is R12's fact at the routing layer (KS5, D1634 reversed): the
// installation's credentials service holds this repository's key for the
// provider, so a call the route claims goes to the service's provider route
// with the provider's name on it (HeaderProvider) and no key of the job's. The
// gateway keeps everything else it did with that call -- which provider claims
// the model, the route's fee, the price check before it is sent, and the
// settlement from the provider's reported usage -- and the service's refusals
// and unreachable halt are the ones a service call already gets (D143).
//
// What the other half of KS5 pins is unchanged and tested where it was: a
// route the job DOES hold the key for, beside the same service upstream, still
// goes to the provider's own endpoint with the job's key
// (TestSwitchedAPIKeyServiceUpstreamKeepsItsProviderRoutes,
// TestServiceNeverSentToProviderRoute).

// servedRouted is a gateway whose upstream is the credentials service (C1.15)
// and whose one provider route is SERVED: the route's key is the
// installation's, so its calls go to the service's provider route. svc is the
// stand-in service (the fake upstream, which records every request); prov is a
// fake with no script, so anything that reaches the provider's OWN endpoint
// fails the test -- which is exactly what a served route must never do.
type servedRouted struct {
	*harness
	svc  *anthropicfake.Fake
	prov *anthropicfake.Fake
}

// newServedRouted starts the stand-in service, the stand-in provider and a
// gateway pointed at the service, with fee on the served route and routedStage
// begun.
func newServedRouted(t *testing.T, answer func(*http.Request, []byte) anthropicfake.Reply, fee float64) *servedRouted {
	t.Helper()
	prov, psrv := anthropicfake.New(t)
	h, svc := newServiceStub(t, answer, func(o *Options) {
		o.Routes = []Route{{
			Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: psrv.URL + "/api",
			Auth: "bearer", Served: true, FeePct: fee,
		}}
	})
	h.gw.BeginStage(routedStage)
	return &servedRouted{harness: h, svc: svc, prov: prov}
}

// providerOK is the answer a routed call gets relayed through the service: the
// provider's own body, and none of the Messages path's headers -- a provider
// route carries no tier and names no credential of the project's (C1).
func providerOK(model string, u pricing.Usage) anthropicfake.Reply {
	return anthropicfake.Reply{Status: http.StatusOK, Body: anthropicfake.MessageOK(model, u).Body}
}

// TestARoutedCallGoesThroughTheService is KS5's acceptance for the sending
// half: for a run whose job carries R12's fact, the gateway sends a routed
// call to the service's provider route with the provider's name on it, and the
// provider's own endpoint -- the address the config holds -- is never called.
// The call is the service call every other service call is: the job's ID token
// as the bearer credential, the run's own identity, and nothing of this job's
// that a service call must not carry.
//
// Hand mutations this fails on: pointing a served route at the provider's own
// address (withRoute, upstreamRequest); sending it without the provider's name,
// or with a name the agent wrote (the service would derive an entry from
// nothing, or serve the wrong one); attaching the route's credential anyway.
//
// What it does NOT pin is serviceUpstream's dropping of an agent-written name:
// on this path the route's own name is set over it (upstreamRequest) and the
// header the agent sent never leaves the allow-list before that, so removing
// the drop leaves this test green. That guard is pinned on the Messages path,
// where the agent's headers are cloned forward and there is no route to
// overwrite them: TestServiceUpstreamSendsIDTokenAndRunIdentity.
func TestARoutedCallGoesThroughTheService(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	s := newServedRouted(t, func(*http.Request, []byte) anthropicfake.Reply {
		return providerOK(dsModel, u)
	}, 10)

	body := msg(dsModel, 1000)
	// The agent sends a provider name, a credential and a bearer of its own:
	// none of them may reach the service as the run's.
	resp, got := s.post(body,
		"x-fugaro-provider", "a-provider-the-agent-invented",
		"authorization", "Bearer sk-ant-the-agent-sent-this")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a served routed call: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "msg_fake") {
		t.Errorf("the provider's answer didn't reach the agent: %s", got)
	}
	if n := s.prov.Count(); n != 0 {
		t.Errorf("the provider's own endpoint was called %d times: a served route's key is the service's", n)
	}
	seen := s.svc.Seen()
	if len(seen) != 1 {
		t.Fatalf("the service was called %d times, want the routed call's one", len(seen))
	}
	up := seen[0]
	if up.URL.Path != ProviderRoutePath {
		t.Errorf("the service was asked for %q, want the provider route %q", up.URL.Path, ProviderRoutePath)
	}
	if up.Method != http.MethodPost {
		t.Errorf("the service was sent %s", up.Method)
	}
	// The provider's own name, and never the one a header the agent wrote
	// carried: it is what decides which of the repository's keys the service
	// serves this call with (R2).
	if v := up.Header.Get(HeaderProvider); v != "openrouter" {
		t.Errorf("%s = %q, want the route's own provider name", HeaderProvider, v)
	}
	if v := up.Header.Get("Authorization"); v != "Bearer "+serviceIDToken(t) {
		t.Errorf("the job's ID token didn't travel as the bearer credential (got %d bytes)", len(v))
	}
	if v := up.Header.Get(HeaderRunIdentity); v != serviceRunToken(t) {
		t.Errorf("the run's identity didn't travel in %s (got %q)", HeaderRunIdentity, v)
	}
	if v := up.Header.Get(HeaderRunJob); v != serviceJob {
		t.Errorf("%s = %q, want %q", HeaderRunJob, v, serviceJob)
	}
	if v := up.Header.Get("x-api-key"); v != "" {
		t.Errorf("a credential travelled to the service in x-api-key")
	}
	// The call is the agent's own request, byte for byte: a served route
	// rewrites where the call goes and nothing about what it says.
	if s.svc.Body(0) == nil || string(s.svc.Body(0)) != body {
		t.Errorf("the service was sent a different body: %q", string(s.svc.Body(0)))
	}
	// The gateway token is the agent's credential for us and goes nowhere
	// else, in any header, under any name. (The two identities above travel
	// on this call by design: they are what the service verifies it by.)
	for k, vs := range up.Header {
		for _, v := range vs {
			assertNoSecret(t, "the service call's "+k, v, []string{s.gw.Token()})
		}
	}
	// And nothing of them reached a log.
	assertNoSecret(t, "the gateway's log", s.logs.String(), secrets(s.harness, t))
	// The call is still the ROUTE's: it is logged and charged as the routed
	// call it is, which is what the fee and the price check hang off.
	if c := s.logs.lastCall(t); c["route"] != "openrouter" {
		t.Errorf("the call logged route %v, want the route it was made on", c["route"])
	}
	noHalt(t, s.harness)

	// A served route is admitted beside the service upstream alone: the key it
	// is reached with is the service's, and there is nowhere else to get one.
	// Both refusals are validateRoutes' (the start path and the exported one
	// share it), and the second is the caller that reaches for two keys at
	// once.
	served := Route{Name: "openrouter", Models: dsRoute, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Served: true}
	if err := validateRoutes(Upstream{Kind: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: testKey}, []Route{served}); err == nil {
		t.Error("validateRoutes passed a served route to the Anthropic upstream, which holds no such key")
	} else if !strings.Contains(err.Error(), "takes its key from the credentials service") {
		t.Errorf("the refusal of a served route doesn't name the invariant: %v", err)
	}
	two := served
	two.Credential = func() (string, error) { return providerKey, nil }
	if err := validateRoutes(Upstream{Kind: KindService, BaseURL: "https://creds.example"}, []Route{two}); err == nil {
		t.Error("validateRoutes passed a served route that carries a credential source of the job's as well")
	} else if !strings.Contains(err.Error(), "a served route's key is the service's") {
		t.Errorf("the refusal of a served route with a key of its own doesn't say why: %v", err)
	}
	// The same call can never be built by hand either: the check is where the
	// job's identity is attached, not only at start-up (serviceUpstream).
	srv := &Server{o: Options{Upstream: Upstream{Kind: KindService, BaseURL: "https://creds.example", Service: &Service{
		IDToken:    func(context.Context) (string, error) { return serviceIDToken(t), nil },
		RunIDToken: func() string { return serviceRunToken(t) },
		Job:        serviceJob,
	}}}}
	held := &Route{Name: "openrouter", Models: dsRoute, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Credential: func() (string, error) { return providerKey, nil }}
	if h, err := srv.serviceUpstream(context.Background(), route{provider: held, path: "/v1/messages"}, http.Header{}); err == nil || h != nil {
		t.Errorf("the service upstream built headers for a route the job holds the key for: %v, %v", h, err)
	}
	if _, err := srv.serviceUpstream(context.Background(), route{provider: &served, path: "/v1/messages"}, http.Header{}); err != nil {
		t.Errorf("the service upstream refused a served route: %v", err)
	}
}

// TestARoutedCallThroughTheServiceCostsWhatItCostBefore: R1's "the gateway
// keeps its own routing, fee, price check and settle". The same call, the same
// model, the same answer and the same fee cost the run exactly what they cost
// on the path where the job holds the key -- the same reservation (the cap
// counts it), the same charge, the same price table used, the same band, and
// the same stage totals -- and the fee, the price check and the settle are the
// route's own and not the service's.
//
// Hand mutations this fails on: dropping the route from a served call (a
// provider's real money charged as an Anthropic call, with no fee and no route
// on the line); reading the service's Messages-path headers for it, which is
// what a served route is not (its answer is the provider's); settling a served
// call anywhere but the route arm of fromUsage.
func TestARoutedCallThroughTheServiceCostsWhatItCostBefore(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	body := msg(dsModel, 4000)
	const fee = 10

	before := feeRouted(t, fee, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, u))
	if resp, b := before.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("the routed call the job holds the key for: %d %s", resp.StatusCode, b)
	}
	after := newServedRouted(t, func(*http.Request, []byte) anthropicfake.Reply {
		return providerOK(dsModel, u)
	}, fee)
	if resp, b := after.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("the same call through the service: %d %s", resp.StatusCode, b)
	}
	noHalt(t, after.harness)

	want := pricing.WithFee(cost(t, dsModel, u), fee)
	b, a := before.logs.lastCall(t), after.logs.lastCall(t)
	for _, k := range []string{"model", "serving_model", "route", "status", "stream", "max_tokens", "priced_as", "settled", "reserved_micros", "charged_micros", "in", "out"} {
		if b[k] != a[k] {
			t.Errorf("%s: %v with the job's key, %v through the service", k, b[k], a[k])
		}
	}
	if got := pricing.Micros(num(a["charged_micros"])); got != want {
		t.Errorf("charged %d, want the model's own cost with the route's %v%% fee (%d)", got, fee, want)
	}
	// The whole stage agrees, not only the one call: what the run's record is
	// built from is the report, and a served call that landed in another
	// bucket -- a tier, a route-less charge -- would be money the cap and the
	// record read differently for the same call.
	rb, ra := before.gw.EndStage(), after.gw.EndStage()
	if !reflect.DeepEqual(rb, ra) {
		t.Errorf("the same call settled differently through the service:\nwith the job's key: %+v\nthrough the service: %+v", rb, ra)
	}
	if rb.Used != want || rb.ByRoute["openrouter"] != want {
		t.Errorf("the stage settled %d (route %d), want %d", rb.Used, rb.ByRoute["openrouter"], want)
	}
}

// TestAProviderErrorOnAServedRouteIsRelayedNotHalted: the error path's half of
// R1's parity. A served route's answer is the provider's, relayed by the
// service, so a provider's own moderation 403, its 401 or its rate limit
// reaches the agent with exactly the answer the same body gets where the job
// holds the key: the status, the error type the agent acts on, and a sentence
// this gateway writes in place of the provider's. It never halts the run, and
// it is not retried by this gateway -- a service call's retry is the service's
// (sendRetryingOnce).
//
// Hand mutations this fails on: reading a served answer as a Messages answer,
// where the platform's front door on a 401 or 403 halts the run
// (stateDetailUnadmitted) and a body this contract cannot read halts it as
// unreadable (serviceAnswer's two early branches); relaying the provider's own
// sentence; answering a served call's provider error with another status or
// type than the mount path's; retrying a service call in the gateway.
func TestAProviderErrorOnAServedRouteIsRelayedNotHalted(t *testing.T) {
	fastRetry(t)
	for _, tc := range []struct {
		name   string
		status int
		said   string
		body   string
		typ    string
	}{
		{"a moderation 403", http.StatusForbidden,
			"this input was flagged by the provider's moderation",
			`{"error":{"code":403,"message":"this input was flagged by the provider's moderation"}}`,
			"permission_error"},
		{"a 401", http.StatusUnauthorized,
			"No auth credentials found",
			`{"error":{"code":401,"message":"No auth credentials found"}}`,
			"authentication_error"},
		{"a string error", http.StatusTooManyRequests,
			"rate limited, come back shortly",
			`{"error":"rate limited, come back shortly"}`,
			"rate_limit_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := anthropicfake.Reply{Status: tc.status, Body: tc.body}
			body := msg(dsModel, 1000)
			// The mount path retries a 429 once by itself, so its fake holds
			// the answer twice; a served call is the service's to retry.
			script := []anthropicfake.Reply{rep}
			if tc.status == http.StatusTooManyRequests {
				script = append(script, rep)
			}
			mount := newRouted(t, "bearer", routedStage, script...)
			mresp, mbody := mount.post(body)
			served := newServedRouted(t, func(*http.Request, []byte) anthropicfake.Reply { return rep }, 10)
			sresp, sbody := served.post(body)

			if mresp.StatusCode != tc.status || sresp.StatusCode != tc.status {
				t.Fatalf("the same provider answer is %d where the job holds the key and %d through the service, want %d:\n%s\n%s",
					mresp.StatusCode, sresp.StatusCode, tc.status, mbody, sbody)
			}
			if sbody != mbody {
				t.Errorf("a served route answered a provider's %d differently:\nwhere the job holds the key: %s\nthrough the service:      %s",
					tc.status, mbody, sbody)
			}
			if typ, _ := apiError(t, sbody); typ != tc.typ {
				t.Errorf("the agent was told %q, want %q: %s", typ, tc.typ, sbody)
			}
			if strings.Contains(sbody, tc.said) {
				t.Errorf("the provider's own sentence reached the agent: %s", sbody)
			}
			noHalt(t, served.harness)
			if n := served.prov.Count(); n != 0 {
				t.Errorf("the provider's own endpoint saw %d calls: a served route's key is the service's", n)
			}
			if n := len(served.svc.Seen()); n != 1 {
				t.Errorf("the service saw %d calls: a service call is not retried by this gateway", n)
			}
		})
	}
}

// TestAServiceRefusalOnARouteHaltsAsAServiceCallDoes: the provider route's own
// two refusals (C1, KS5) take the path every service refusal takes -- the run
// halts (D143), the agent is told a sentence this gateway built, and the
// service's own body is never relayed. A job that carries R12's fact is the
// owner's record that this repository's provider keys are served, so an absent
// key there is a snapshot of the installation and not a state a run papers
// over with a mounted key -- and there is none mounted for a served route at
// all.
//
// Hand mutations this fails on: letting the refusal reach the agent as an
// answer (the service's sentence names the project's bundle and the agent has
// no business seeing it, D143); treating provider_not_allowed's 400 as a model
// error to relay; charging the run for a call no model generated for.
func TestAServiceRefusalOnARouteHaltsAsAServiceCallDoes(t *testing.T) {
	const serviceSaid = "the bundle holds no provider entry for this repository"
	for _, tc := range []struct {
		typ    string
		status int
	}{
		{ErrorProviderNotServed, http.StatusServiceUnavailable},
		{ErrorProviderNotAllowed, http.StatusBadRequest},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			s := newServedRouted(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: tc.status,
					Body: `{"error":"` + tc.typ + `","message":"` + serviceSaid + `"}`}
			}, 10)

			resp, body := s.post(msg(dsModel, 1000))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a refused routed call: %d %s, want the halt's 403", resp.StatusCode, body)
			}
			if got := resp.Header.Get("x-should-retry"); got != "false" {
				t.Errorf("x-should-retry = %q, want false: a halt is not retried", got)
			}
			halt := haltOf(t, s.harness)
			if halt.Reason != ReasonBudgetUnavailable || halt.Scope != "run" {
				t.Errorf("the halt is %+v", halt)
			}
			if halt.Detail != refusalDetail(tc.typ) {
				t.Errorf("the halt's detail is %q, want %q", halt.Detail, refusalDetail(tc.typ))
			}
			if !strings.Contains(halt.Detail, tc.typ) {
				t.Errorf("the halt's detail doesn't name the refusal: %q", halt.Detail)
			}
			if !strings.Contains(body, refusalDetail(tc.typ)) {
				t.Errorf("the agent wasn't told why: %s", body)
			}
			// The service's own sentence is never relayed, and no identity of
			// this job's is in either the answer or the halt.
			for what, text := range map[string]string{"the halt's answer": body, "the halt's detail": halt.Detail} {
				if strings.Contains(text, serviceSaid) {
					t.Errorf("%s carries the service's own text: %s", what, text)
				}
				assertNoSecret(t, what, text, secrets(s.harness, t))
			}
			assertNoSecret(t, "the abort's log", s.logs.String(), secrets(s.harness, t))
			// Nothing was generated for it, and the provider's own endpoint was
			// never reached either.
			if n := s.prov.Count(); n != 0 {
				t.Errorf("the provider's own endpoint saw %d calls", n)
			}
			if c := s.logs.lastCall(t); num(c["charged_micros"]) != 0 || c["settled"] != settledZero {
				t.Errorf("a refused call was charged: %v %v", c["charged_micros"], c["settled"])
			}
			// Later calls get the same halt, whatever they name.
			if resp, _ := s.post(msg(dsModel, 1000)); resp.StatusCode != http.StatusForbidden {
				t.Errorf("a call after the halt: %d, want 403", resp.StatusCode)
			}
		})
	}
}
