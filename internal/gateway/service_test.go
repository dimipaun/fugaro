package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// The stand-in for the installation's credentials service is the package's
// own fake upstream, driven by Func: it records every request it is sent and
// answers whatever the case says, which is all a service is to this gateway
// (a URL, an answer, and the headers on both). Its base URL is loopback, so
// checkBaseURL admits it exactly as it admits the fake Anthropic.

// serviceIDToken and serviceRunToken are the two identities a case's calls
// carry, built at run time from the test's name (Convention 2): no literal
// in this file is, or looks like, a credential.
func serviceIDToken(t *testing.T) string {
	t.Helper()
	return "job-id-token-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

func serviceRunToken(t *testing.T) string {
	t.Helper()
	return "run-identity-token-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

// serviceJob is the Cloud Run job a case's calls report they are in.
const serviceJob = "fugaro-run-job"

// newServiceGateway starts a gateway on the credentials-service upstream
// pointed at base, with the default stage begun. edit changes the options
// before the start.
func newServiceGateway(t *testing.T, base string, edit func(*Options)) *harness {
	t.Helper()
	logs := &logBuf{}
	o := Options{
		Upstream: Upstream{
			Kind: KindService, BaseURL: base,
			Service: &Service{
				IDToken:    func(context.Context) (string, error) { return serviceIDToken(t), nil },
				RunIDToken: func() string { return serviceRunToken(t) },
				Job:        serviceJob,
			},
		},
		Prices: dsPrices(),
		Mode:   Observe,
		Log:    slog.New(slog.NewJSONHandler(logs, nil)),
	}
	if edit != nil {
		edit(&o)
	}
	gw, err := Start(context.Background(), o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(defaultStage)
	return &harness{t: t, gw: gw, logs: logs}
}

// newServiceStub starts the stand-in service and a gateway pointed at it.
func newServiceStub(t *testing.T, answer func(*http.Request, []byte) anthropicfake.Reply, edit func(*Options)) (*harness, *anthropicfake.Fake) {
	t.Helper()
	f, srv := anthropicfake.New(t)
	f.Func = answer
	return newServiceGateway(t, srv.URL, edit), f
}

// serviceOK is a served Messages call: the model's own answer, and the tier
// and credential name every Messages answer of the contract carries.
func serviceOK(tier, name string) anthropicfake.Reply {
	r := anthropicfake.MessageOK(sonnet, smallUsage)
	r.Header = http.Header{HeaderTier: []string{tier}, HeaderCredential: []string{name}}
	return r
}

// secrets are the values that must never reach the agent, a log line or a
// halt's detail.
func secrets(h *harness, t *testing.T) []string {
	t.Helper()
	return []string{h.gw.Token(), serviceIDToken(t), serviceRunToken(t)}
}

// halted reports whether the run has halted by the time the answer arrived.
// A halt is recorded before the answer that caused it is written, so this is
// a decision and not a race.
func (h *harness) halted() (Halt, bool) {
	select {
	case halt := <-h.gw.Halted():
		return halt, true
	default:
		return Halt{}, false
	}
}

func assertNoSecret(t *testing.T, what, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("%s carries a secret: %q", what, text)
		}
	}
}

// TestServiceUpstreamSendsIDTokenAndRunIdentity: a service call carries the
// job service account's Google ID token as its bearer credential (what Cloud
// Run IAM checks, F30), the run's own identity and the job's name in the
// contract's headers, the model request's own headers -- and no credential of
// the job's own, and not the gateway token.
func TestServiceUpstreamSendsIDTokenAndRunIdentity(t *testing.T) {
	h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return serviceOK(TierA, "repo-a")
	}, nil)

	// The agent sends its own credential and its own identity headers: none
	// of them may travel on, and none may become something the service reads
	// as its own answer to us.
	resp, body := h.post(msg(sonnet, 1000),
		"authorization", "Bearer sk-ant-the-agent-sent-this",
		"x-fugaro-run-identity", "an-agent-written-identity",
		"x-fugaro-tier", "A",
		"x-fugaro-credential", "a-credential-the-agent-invented",
		"x-fugaro-switch", `{"from":"x","to":"y","reason":"exhausted"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call: %d %s", resp.StatusCode, body)
	}
	seen := f.Seen()
	if len(seen) != 1 {
		t.Fatalf("the service was called %d times", len(seen))
	}
	up := seen[0]
	if up.URL.Path != "/v1/messages" {
		t.Errorf("the service was asked for %s", up.URL.Path)
	}
	if got := up.Header.Get("Authorization"); got != "Bearer "+serviceIDToken(t) {
		t.Errorf("the job's ID token didn't travel as the bearer credential (got %d bytes)", len(got))
	}
	if got := up.Header.Get(HeaderRunIdentity); got != serviceRunToken(t) {
		t.Errorf("the run's identity didn't travel in %s (got %q)", HeaderRunIdentity, got)
	}
	if got := up.Header.Get(HeaderRunJob); got != serviceJob {
		t.Errorf("%s = %q, want %q", HeaderRunJob, got, serviceJob)
	}
	if got := up.Header.Get("x-api-key"); got != "" {
		t.Errorf("the gateway token travelled to the service in x-api-key")
	}
	for _, k := range []string{HeaderTier, HeaderCredential, HeaderSwitch} {
		if got := up.Header.Values(k); len(got) > 0 {
			t.Errorf("the agent's %s reached the service as %q", k, got)
		}
	}
	// The gateway token is the agent's credential for us and goes nowhere
	// else, in any header, under any name.
	for k, vs := range up.Header {
		for _, v := range vs {
			assertNoSecret(t, "the service call's "+k, v, []string{h.gw.Token()})
		}
	}
	// The model request's own headers are the ones forwarded, as they are
	// for every other upstream.
	if got := up.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got)
	}
	if got := up.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	// And nothing about this call reached a log.
	assertNoSecret(t, "the gateway's log", h.logs.String(), secrets(h, t))
	noHalt(t, h)
}

// TestServiceUpstreamReadsTierAndCredential: the tier, the credential's name
// and the switch are read off the service's answer and handed to the runner's
// hook -- and dropped before the answer is copied on, so the agent never
// learns its project's credentials.
func TestServiceUpstreamReadsTierAndCredential(t *testing.T) {
	var answers []ServiceAnswer
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{
			Status: http.StatusOK,
			Header: http.Header{
				HeaderTier:       []string{TierB},
				HeaderCredential: []string{"repo-shared-b"},
				HeaderSwitch:     []string{`{"from":"repo-a","to":"repo-shared-b","reason":"exhausted"}`},
			},
			Body: anthropicfake.MessageOK(sonnet, smallUsage).Body,
		}
	}, func(o *Options) {
		o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
	})

	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "msg_fake") {
		t.Errorf("the model's answer didn't reach the agent: %s", body)
	}
	if len(answers) != 1 {
		t.Fatalf("the hook was told about %d answers, want 1", len(answers))
	}
	got := answers[0]
	if got.Tier != TierB || got.Credential != "repo-shared-b" {
		t.Errorf("the answer read back as tier %q credential %q, want B and repo-shared-b", got.Tier, got.Credential)
	}
	if got.Switch != `{"from":"repo-a","to":"repo-shared-b","reason":"exhausted"}` {
		t.Errorf("the switch read back as %q", got.Switch)
	}
	if got.Exhausted != nil {
		t.Errorf("a served call was read as exhausted: %+v", got.Exhausted)
	}
	// The agent is told nothing about the credential set (D143): not in the
	// headers, not in the body, not in a log line.
	for _, k := range []string{HeaderTier, HeaderCredential, HeaderSwitch} {
		if vs := resp.Header.Values(k); len(vs) > 0 {
			t.Errorf("the agent's answer carries %s: %q", k, vs)
		}
	}
	assertNoSecret(t, "the agent's answer", body, secrets(h, t))
	if strings.Contains(body, "repo-shared-b") {
		t.Errorf("the agent's answer names the credential: %s", body)
	}
	noHalt(t, h)
}

// TestServiceNeverSentToProviderRoute is D143's other half, on
// TestOAuthNeverSentToProviderRoute's shape: a route is a provider's own
// endpoint and is reached with the job's own key for that provider, which a
// cascade run does not have. A service upstream therefore takes no routes at
// all -- not at start-up, and not from a Route built in code past validation
// either.
func TestServiceNeverSentToProviderRoute(t *testing.T) {
	_, psrv := anthropicfake.New(t, anthropicfake.MessageOK(dsModel, smallUsage))
	cred := func() (string, error) { return "provider-key-" + t.Name(), nil }
	svc := func() *Service {
		return &Service{
			IDToken:    func(context.Context) (string, error) { return serviceIDToken(t), nil },
			RunIDToken: func() string { return serviceRunToken(t) },
			Job:        serviceJob,
		}
	}

	// 1. A service upstream takes no routes at all, whatever they point at.
	for _, base := range []string{psrv.URL + "/api", psrv.URL, "https://api.anthropic.com"} {
		gw, err := Start(context.Background(), Options{
			Upstream: Upstream{Kind: KindService, BaseURL: "https://creds.example", Service: svc()},
			Routes: []Route{{
				Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: base, Auth: "bearer", Credential: cred,
			}},
			Prices: dsPrices(), Mode: Observe,
		})
		if err == nil {
			_ = gw.Close(context.Background())
			t.Errorf("a service upstream with a route to %s started", base)
			continue
		}
		if !strings.Contains(err.Error(), "never reached through the credentials service") || !strings.Contains(err.Error(), "D143") {
			t.Errorf("the refusal of a route to %s does not name the invariant: %v", base, err)
		}
	}

	// 2. The refusal is in validateRoutes' kind switch, where a later edit
	//    could quietly drop it -- and no routes at all stays legal, so the
	//    guard is about the combination and not about the kind.
	r := Route{Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: psrv.URL + "/api", Auth: "bearer", Credential: cred}
	err := validateRoutes(Upstream{Kind: KindService, BaseURL: "https://creds.example"}, []Route{r})
	if err == nil {
		t.Error("validateRoutes passed a route to a service upstream")
	} else if !strings.Contains(err.Error(), "never reached through the credentials service") {
		t.Errorf("validateRoutes' refusal does not name the invariant: %v", err)
	}
	if err := validateRoutes(Upstream{Kind: KindService, BaseURL: "https://creds.example"}, nil); err != nil {
		t.Errorf("validateRoutes refused a service upstream with no routes: %v", err)
	}

	// 3. The upstream's own path refuses a provider route even if one were
	//    handed to it: the check is where the job's identity is attached,
	//    not only at start-up.
	pr := &Route{Name: "openrouter", BaseURL: psrv.URL + "/api", Auth: "bearer", Credential: cred}
	s := &Server{o: Options{Upstream: Upstream{Kind: KindService, BaseURL: "https://creds.example", Service: svc()}}}
	h, err := s.serviceUpstream(context.Background(), route{provider: pr, upstream: psrv.URL + "/api/v1/messages", path: "/v1/messages"}, http.Header{})
	if err == nil {
		t.Fatalf("the service upstream built a request for a provider route, with %v", h)
	}
	if !errors.Is(err, errServiceOnRoute) {
		t.Errorf("the refusal is %v, want errServiceOnRoute", err)
	}
	if h != nil {
		t.Errorf("a refused call wrote %v", h)
	}
}

// TestServiceUnreachableHalts: a job that cannot reach the credentials
// service halts the run the way an unreachable budget backend does (D143),
// and every later call gets the same refusal.
func TestServiceUnreachableHalts(t *testing.T) {
	// A server that was up and is now gone: the address refuses connections.
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := gone.URL
	gone.Close()

	h := newServiceGateway(t, base, nil)
	resp, body := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a call to an unreachable service: %d %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("x-should-retry"); got != "false" {
		t.Errorf("x-should-retry = %q, want false: a halt is not retried", got)
	}
	halt := haltOf(t, h)
	if halt.Reason != ReasonBudgetUnavailable {
		t.Errorf("the halt's reason is %q, want %q", halt.Reason, ReasonBudgetUnavailable)
	}
	if halt.Detail != stateDetailUnreachable {
		t.Errorf("the halt's detail is %q, want %q", halt.Detail, stateDetailUnreachable)
	}
	if !strings.Contains(body, stateDetailUnreachable) {
		t.Errorf("the agent wasn't told why: %s", body)
	}
	assertNoSecret(t, "the halt's answer", body, secrets(h, t))
	assertNoSecret(t, "the abort's log", h.logs.String(), secrets(h, t))

	// Later calls, of either path, get the same halt.
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		b := msg(sonnet, 1000)
		if path != "/v1/messages" {
			b = `{"model":"` + sonnet + `","messages":[]}`
		}
		if resp, _ := h.do(h.request(context.Background(), path, b)); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s after the halt: %d, want 403", path, resp.StatusCode)
		}
	}
}

// TestNoInJobFallback is C1.15's red step, and D143 in three parts: a service
// run has no credential of its own to fall back with, an identity it cannot
// present is never replaced by one, and an unreachable service is never
// replaced by another destination.
func TestNoInJobFallback(t *testing.T) {
	t.Run("a service upstream that carries a credential is refused", func(t *testing.T) {
		// Both shapes of an in-job credential the Upstream type can hold. A
		// gateway built with one is refused at Start rather than falling back
		// to it the first time the service cannot answer.
		for _, tc := range []struct {
			name string
			edit func(*Upstream)
		}{
			{"an API key", func(u *Upstream) { u.APIKey = testKey }},
			{"a vertex token source", func(u *Upstream) {
				u.Token = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: testKey})
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := Options{
					Upstream: Upstream{Kind: KindService, BaseURL: "https://creds.example",
						Service: &Service{
							IDToken:    func(context.Context) (string, error) { return serviceIDToken(t), nil },
							RunIDToken: func() string { return serviceRunToken(t) },
							Job:        serviceJob,
						}},
					Prices: dsPrices(),
					Mode:   Observe,
				}
				tc.edit(&o.Upstream)
				gw, err := Start(context.Background(), o)
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = gw.Close(ctx)
					t.Fatalf("Start accepted a service upstream with %s (D143)", tc.name)
				}
				if !strings.Contains(err.Error(), "D143") {
					t.Errorf("the refusal doesn't say why: %v", err)
				}
			})
		}
	})

	t.Run("a service upstream that cannot say who it is is refused", func(t *testing.T) {
		// The identities are the reason this kind exists: an upstream with
		// one of them missing is refused at Start, where the mistake is,
		// rather than at the first call.
		for _, tc := range []struct {
			name string
			edit func(*Upstream)
			want string
		}{
			{"no identity at all", func(u *Upstream) { u.Service = nil }, "no identity"},
			{"no job identity", func(u *Upstream) { u.Service.IDToken = nil }, "job identity"},
			{"no run identity", func(u *Upstream) { u.Service.RunIDToken = nil }, "run identity"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := Options{
					Upstream: Upstream{Kind: KindService, BaseURL: "https://creds.example",
						Service: &Service{
							IDToken:    func(context.Context) (string, error) { return serviceIDToken(t), nil },
							RunIDToken: func() string { return serviceRunToken(t) },
							Job:        serviceJob,
						}},
					Prices: dsPrices(),
					Mode:   Observe,
				}
				tc.edit(&o.Upstream)
				gw, err := Start(context.Background(), o)
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = gw.Close(ctx)
					t.Fatalf("Start accepted a service upstream with %s", tc.name)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("the refusal doesn't name what is missing: %v", err)
				}
			})
		}
	})

	t.Run("a run whose identity cannot be presented is not replaced", func(t *testing.T) {
		h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
			return serviceOK(TierA, "repo-a")
		}, func(o *Options) {
			o.Upstream.Service.IDToken = func(context.Context) (string, error) {
				return "", errors.New("the metadata server said no")
			}
		})
		resp, body := h.post(msg(sonnet, 1000))
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("a call with no job identity: %d %s", resp.StatusCode, body)
		}
		if f.Count() != 0 {
			t.Errorf("the service was called %d times without the job's identity", f.Count())
		}
		if strings.Contains(body, testKey) || strings.Contains(body, "sk-ant") {
			t.Errorf("the refusal carries something key-shaped: %s", body)
		}
		noHalt(t, h)
	})

	t.Run("a run whose own identity is gone is not sent under the job's", func(t *testing.T) {
		h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
			return serviceOK(TierA, "repo-a")
		}, func(o *Options) {
			o.Upstream.Service.RunIDToken = func() string { return "" }
		})
		resp, body := h.post(msg(sonnet, 1000))
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("a call with no run identity: %d %s", resp.StatusCode, body)
		}
		if f.Count() != 0 {
			t.Errorf("the service was called %d times with no run identity", f.Count())
		}
		noHalt(t, h)
	})

	t.Run("an unreachable service is not replaced", func(t *testing.T) {
		gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		base := gone.URL
		gone.Close()
		// A stand-in for anything a fallback would call: it must see nothing
		// at all, whatever the service does.
		fallback, srv := anthropicfake.New(t)
		fallback.Func = func(*http.Request, []byte) anthropicfake.Reply {
			return serviceOK(TierA, "repo-a")
		}
		defer srv.Close()
		h := newServiceGateway(t, base, nil)
		if resp, _ := h.post(msg(sonnet, 1000)); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("an unreachable service: %d, want the halt's 403", resp.StatusCode)
		}
		haltOf(t, h)
		if fallback.Count() != 0 {
			t.Errorf("%d calls reached a destination that is not the service", fallback.Count())
		}
	})
}

// TestServiceRefusalIsNeverRelayed: what the service says when it refuses is
// fugaro's own text -- it names the project's credentials -- so the agent gets
// a sentence this gateway builds from validated fields, or an answer it can
// retry, and never the service's own body. A model error the service relayed
// is another writer's body and still reaches the agent as it always did.
func TestServiceRefusalIsNeverRelayed(t *testing.T) {
	const serviceSaid = "no credential of repo-secret-name is admitted for this run"
	long := strings.Repeat("x", maxRefusalMessageBytes+1)
	tried := func(n int) string {
		var b strings.Builder
		b.WriteString("[")
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"name":"cred-` + strings.Repeat("a", i%2) + `","tier":"B","reason":"broken"}`)
		}
		return b.String() + "]"
	}

	for _, tc := range []struct {
		name   string
		status int
		body   string
		// want is the status the agent's answer must carry, and halt is
		// whether the run must be halted by it. A relayed body is compared
		// byte for byte.
		want     int
		halt     bool
		detail   string
		relayed  bool
		exhaust  *time.Time
		triedLen int
	}{
		{
			name: "state_unavailable", status: http.StatusServiceUnavailable,
			body: `{"error":"state_unavailable","message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: refusalDetail(ErrorStateUnavailable),
		},
		{
			name: "unverified_run_identity", status: http.StatusForbidden,
			body: `{"error":"unverified_run_identity","message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: refusalDetail(ErrorUnverifiedRunIdentity),
		},
		{
			name: "unverified_job_identity", status: http.StatusForbidden,
			body: `{"error":"unverified_job_identity","message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: refusalDetail(ErrorUnverifiedJobIdentity),
		},
		{
			// A type this release does not know: the service's own text, and
			// not one this gateway can account a call by.
			name: "a type this release does not know", status: http.StatusForbidden,
			body: `{"error":"a_type_of_a_later_release","message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			// A refusal body carrying a field this release does not know is
			// read strictly (Convention 6): a later service's refusal must not
			// arrive at the agent as a relayed body.
			name: "a field this release does not know", status: http.StatusForbidden,
			body: `{"error":"state_unavailable","retry_after":5,"message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			name: "something after the JSON value", status: http.StatusForbidden,
			body: `{"error":"state_unavailable","message":"` + serviceSaid + `"}{"error":"x"}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			name: "a message over the contract's bound", status: http.StatusForbidden,
			body: `{"error":"state_unavailable","message":"` + long + `"}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			name: "a tried list over the contract's bound", status: http.StatusForbidden,
			body: `{"error":"state_unavailable","tried":` + tried(maxTriedEntries+1) + `}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			name: "a tried entry that isn't one", status: http.StatusForbidden,
			body: `{"error":"state_unavailable","tried":[{"name":"a b","tier":"B","reason":"broken"}]}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			// The one refusal that is not a halt: the instant is handed to the
			// runner, and the agent is given a retryable answer.
			name: "exhausted_until", status: http.StatusServiceUnavailable,
			body:     `{"error":"exhausted_until","message":"` + serviceSaid + `","reset_at":1800000000000,"tried":[{"name":"cred-a","tier":"A","reason":"exhausted"},{"name":"cred-b","tier":"B","reason":"broken"}]}`,
			want:     http.StatusServiceUnavailable,
			exhaust:  ptrTime(time.UnixMilli(1800000000000).UTC()),
			triedLen: 2,
		},
		{
			name: "exhausted_until with no instant", status: http.StatusServiceUnavailable,
			body: `{"error":"exhausted_until","message":"` + serviceSaid + `"}`,
			want: http.StatusForbidden, halt: true, detail: stateDetailUnreadable,
		},
		{
			// A model error the service relayed: another writer's body, and
			// one the agent has always seen.
			name: "a model error the service relayed", status: http.StatusBadRequest,
			body: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too big"}}`,
			want: http.StatusBadRequest, relayed: true,
		},
		{
			name: "a body that is not JSON at all", status: http.StatusBadGateway,
			body: `<html>502 Bad Gateway</html>`,
			want: http.StatusBadGateway, relayed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []ServiceAnswer
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: tc.status, Body: tc.body,
					Header: http.Header{"Content-Type": []string{"application/json"}}}
			}, func(o *Options) {
				o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
			})

			resp, body := h.post(msg(sonnet, 1000))
			if resp.StatusCode != tc.want {
				t.Fatalf("the agent's answer: %d %s, want %d", resp.StatusCode, body, tc.want)
			}
			// What the service wrote never reaches the agent: not the
			// sentence, not a tried credential's name.
			if strings.Contains(body, serviceSaid) {
				t.Errorf("the service's own sentence reached the agent: %s", body)
			}
			if strings.Contains(body, "cred-a") || strings.Contains(body, "cred-b") {
				t.Errorf("the refusal's tried credentials reached the agent: %s", body)
			}
			if tc.relayed {
				if body != tc.body {
					t.Errorf("a relayed body arrived changed:\n got %s\nwant %s", body, tc.body)
				}
				if len(answers) != 0 {
					t.Errorf("a relayed model error was read as a service answer: %+v", answers)
				}
				if halt, ok := h.halted(); ok {
					t.Errorf("a relayed model error halted the run: %+v", halt)
				}
				return
			}
			if tc.halt {
				halt := haltOf(t, h)
				if halt.Reason != ReasonBudgetUnavailable || halt.Detail != tc.detail {
					t.Errorf("the halt is %q / %q, want %q / %q", halt.Reason, halt.Detail, ReasonBudgetUnavailable, tc.detail)
				}
				assertNoSecret(t, "the halt's answer", body, secrets(h, t))
				return
			}
			// The exhausted answer: handed to the runner, retryable for the
			// agent, and not a halt.
			if len(answers) != 1 || answers[0].Exhausted == nil {
				t.Fatalf("the hook was told %+v, want one exhausted answer", answers)
			}
			if answers[0].Tier != "" || answers[0].Credential != "" {
				t.Errorf("an exhausted answer carries a tier and a name: %+v", answers[0])
			}
			ex := answers[0].Exhausted
			if !ex.ResetAt.Equal(*tc.exhaust) {
				t.Errorf("the reset instant read back as %v, want %v", ex.ResetAt, *tc.exhaust)
			}
			if len(ex.Tried) != tc.triedLen {
				t.Fatalf("the tried list read back as %d entries, want %d", len(ex.Tried), tc.triedLen)
			}
			if ex.Tried[0].Name != "cred-a" || ex.Tried[0].Tier != TierA || ex.Tried[0].Reason != ReasonExhausted {
				t.Errorf("the first tried entry read back as %+v", ex.Tried[0])
			}
			if got := resp.Header.Get("Retry-After"); got != "" {
				t.Errorf("the exhausted answer carries a Retry-After of its own: %q", got)
			}
			if got := resp.Header.Get("x-should-retry"); got != "true" && got != "" {
				t.Errorf("x-should-retry = %q: the wait is retryable", got)
			}
			if halt, ok := h.halted(); ok {
				t.Errorf("an exhausted answer halted the run: %+v", halt)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// TestServiceCountTokensIsNotAskedForATier: a token count is free and settles
// nothing, so its answer is not read for a tier -- and a service that answers
// one without is not a service this gateway cannot read.
func TestServiceCountTokensIsNotAskedForATier(t *testing.T) {
	var answers []ServiceAnswer
	h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusOK, Body: `{"input_tokens":7}`}
	}, func(o *Options) {
		o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
	})
	resp, body := h.do(h.request(context.Background(), "/v1/messages/count_tokens",
		`{"model":"`+sonnet+`","messages":[]}`))
	if resp.StatusCode != http.StatusOK || body != `{"input_tokens":7}` {
		t.Fatalf("count_tokens: %d %s", resp.StatusCode, body)
	}
	if f.Count() != 1 {
		t.Errorf("the service was called %d times", f.Count())
	}
	if len(answers) != 0 {
		t.Errorf("a free count was handed to the ledger hook: %+v", answers)
	}
	noHalt(t, h)
}

// TestServiceCountTokensTierIsDropped: a count's answer is copied through
// unread, so the headers the service answers with are dropped here instead.
func TestServiceCountTokensTierIsDropped(t *testing.T) {
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusOK, Body: `{"input_tokens":7}`,
			Header: http.Header{HeaderTier: []string{TierA}, HeaderCredential: []string{"repo-a"}}}
	}, nil)
	resp, _ := h.do(h.request(context.Background(), "/v1/messages/count_tokens",
		`{"model":"`+sonnet+`","messages":[]}`))
	for _, k := range []string{HeaderTier, HeaderCredential, HeaderSwitch} {
		if vs := resp.Header.Values(k); len(vs) > 0 {
			t.Errorf("the agent's count answer carries %s: %q", k, vs)
		}
	}
}

// TestServiceAnswerIsScrubbedOfTheCallersIdentities: the job's ID token
// travels as the call's bearer credential, and a service that echoes it back
// -- in a relayed body, or in headers this path copies through unread -- must
// not hand it to the agent. The run's own identity is dropped from the answer
// headers for the same reason.
func TestServiceAnswerIsScrubbedOfTheCallersIdentities(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		t.Run(path, func(t *testing.T) {
			h, _ := newServiceStub(t, func(r *http.Request, _ []byte) anthropicfake.Reply {
				// The service echoes what it was sent, which is what a hop
				// that renders its own request into an error does.
				echo := "echoed " + r.Header.Get("Authorization")
				return anthropicfake.Reply{Status: http.StatusBadRequest,
					Header: http.Header{
						"X-Echoed":        []string{r.Header.Get("Authorization")},
						HeaderRunIdentity: []string{r.Header.Get(HeaderRunIdentity)},
						HeaderCredential:  []string{"repo-a"},
					},
					Body: `{"type":"error","error":{"type":"invalid_request_error","message":"` + echo + `"}}`,
				}
			}, nil)
			b := msg(sonnet, 1000)
			if path != "/v1/messages" {
				b = `{"model":"` + sonnet + `","messages":[]}`
			}
			resp, body := h.do(h.request(context.Background(), path, b))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
			}
			assertNoSecret(t, "the agent's answer", body, secrets(h, t))
			assertNoSecret(t, "the agent's answer headers", resp.Header.Get("X-Echoed"), secrets(h, t))
			assertNoSecret(t, "the agent's answer headers", resp.Header.Get(HeaderRunIdentity), secrets(h, t))
			if !strings.Contains(body, redacted) {
				t.Errorf("the echoed credential wasn't what was removed: %s", body)
			}
		})
	}
}
