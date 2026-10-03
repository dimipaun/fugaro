package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const (
	dsModel     = "deepseek/deepseek-v4-flash"
	providerKey = "sk-or-v1-PROVIDERKEY-0123456789abcdef"
	agentSecret = "sk-agent-OWN-CREDENTIAL-should-never-travel"
)

// routed is a gateway with the Anthropic upstream and one provider route
// (a second fake, under the path prefix /api), both begun on a stage that
// pins the DeepSeek model and a Claude background model.
type routed struct {
	*harness
	prov    *anthropicfake.Fake
	provSrv *httptest.Server
}

func newRouted(t *testing.T, auth string, stage Stage, provScript ...anthropicfake.Reply) *routed {
	t.Helper()
	return newRoutedCred(t, auth, func() (string, error) { return providerKey, nil }, stage, provScript...)
}

func newRoutedCred(t *testing.T, auth string, cred func() (string, error), stage Stage, provScript ...anthropicfake.Reply) *routed {
	t.Helper()
	prov, psrv := anthropicfake.New(t, provScript...)
	h := newHarnessWith(t, func(o *Options) {
		o.Routes = []Route{{
			Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: psrv.URL + "/api", Auth: auth,
			Credential: cred,
		}}
	})
	h.gw.BeginStage(stage)
	return &routed{harness: h, prov: prov, provSrv: psrv}
}

var routedStage = Stage{Name: "implement", Model: dsModel, Background: sonnet}

func TestRouteByModel(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	for _, auth := range []string{"bearer", "x-api-key"} {
		t.Run(auth, func(t *testing.T) {
			r := newRouted(t, auth, routedStage, anthropicfake.MessageOK(dsModel, u))
			r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}

			body := msg(dsModel, 100, `"cache_control":{"type":"ephemeral"}`)
			if resp, b := r.post(body); resp.StatusCode != 200 {
				t.Fatalf("provider model: %d %s", resp.StatusCode, b)
			}
			if r.prov.Count() != 1 || r.fake.Count() != 0 {
				t.Fatalf("provider saw %d, Anthropic saw %d", r.prov.Count(), r.fake.Count())
			}
			got := r.prov.Seen()[0]
			if got.URL.Path != "/api/v1/messages" {
				t.Errorf("provider path %q, want the base's prefix kept", got.URL.Path)
			}
			if string(r.prov.Body(0)) != body {
				t.Errorf("body changed on the compat route: %q", r.prov.Body(0))
			}
			if auth == "bearer" {
				if got.Header.Get("Authorization") != "Bearer "+providerKey || got.Header.Get("x-api-key") != "" {
					t.Errorf("credential headers: %v", got.Header)
				}
			} else if got.Header.Get("x-api-key") != providerKey || got.Header.Get("Authorization") != "" {
				t.Errorf("credential headers: %v", got.Header)
			}
			if c := r.logs.lastCall(t); c["route"] != "openrouter" {
				t.Errorf("route logged %v", c["route"])
			}

			// A Claude model keeps going to Anthropic, with its key, unchanged.
			cbody := msg(sonnet, 100, `"cache_control":{"type":"ephemeral","ttl":"1h"}`)
			if resp, b := r.post(cbody); resp.StatusCode != 200 {
				t.Fatalf("claude model: %d %s", resp.StatusCode, b)
			}
			if r.prov.Count() != 1 || r.fake.Count() != 1 {
				t.Fatalf("provider saw %d, Anthropic saw %d", r.prov.Count(), r.fake.Count())
			}
			if s := r.fake.Seen()[0]; s.URL.Path != "/v1/messages" || s.Header.Get("x-api-key") != testKey || s.Header.Get("Authorization") != "" {
				t.Errorf("claude call: %s %v", s.URL.Path, s.Header)
			}
			if string(r.fake.Body(0)) != cbody {
				t.Errorf("claude body changed: %q", r.fake.Body(0))
			}
			if c := r.logs.lastCall(t); c["route"] != nil {
				t.Errorf("a Claude call logged route %v", c["route"])
			}
		})
	}
}

func TestUnpinnedModelStill400(t *testing.T) {
	r := newRouted(t, "bearer", routedStage)
	for _, m := range []string{"deepseek/deepseek-v4-pro", "DeepSeek/deepseek-v4-flash", opus, "other/model"} {
		resp, b := r.post(msg(m, 100))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", m, resp.StatusCode)
		}
		if typ, msg := apiError(t, b); typ != "invalid_request_error" || !strings.Contains(msg, "is not pinned for stage implement") {
			t.Errorf("%s: %s %q", m, typ, msg)
		}
	}
	if r.prov.Count() != 0 || r.fake.Count() != 0 {
		t.Errorf("a refused call was forwarded (provider %d, Anthropic %d)", r.prov.Count(), r.fake.Count())
	}
	if l := r.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
}

func TestVariantSuffixPinRefused(t *testing.T) {
	for _, m := range []string{dsModel + ":online", dsModel + ":free", dsModel + ":nitro"} {
		r := newRouted(t, "bearer", Stage{Name: "implement", Model: m, Background: m})
		resp, b := r.post(msg(m, 100))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", m, resp.StatusCode)
		}
		if _, msg := apiError(t, b); !strings.Contains(msg, "variant suffix") {
			t.Errorf("%s: message %q", m, msg)
		}
		if r.prov.Count() != 0 {
			t.Errorf("%s: forwarded", m)
		}
	}
	// The plain ID pinned does not let the suffixed one through.
	r := newRouted(t, "bearer", routedStage)
	if resp, _ := r.post(msg(dsModel+":online", 100)); resp.StatusCode != http.StatusBadRequest || r.prov.Count() != 0 {
		t.Errorf("a suffixed request passed a plain pin: %d", resp.StatusCode)
	}
}

func TestNoCrossHostRedirect(t *testing.T) {
	other, osrv := anthropicfake.New(t, anthropicfake.MessageOK(dsModel, pricing.Usage{Input: 1, Output: 1}))
	redir := anthropicfake.Reply{Status: http.StatusTemporaryRedirect, Header: http.Header{"Location": {osrv.URL + "/v1/messages"}}}
	r := newRouted(t, "bearer", routedStage, redir)
	resp, b := r.post(msg(dsModel, 100))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d %s, want 502", resp.StatusCode, b)
	}
	if resp.Header.Get("Location") != "" || strings.Contains(b, osrv.URL) {
		t.Errorf("the redirect target reached the agent: %v %s", resp.Header, b)
	}
	if other.Count() != 0 {
		t.Fatal("the gateway followed a redirect to another host")
	}
	if l := r.gw.Ledger(); l.Reserved != 0 {
		t.Errorf("reserved %d after the redirect", l.Reserved)
	}
}

// A redirect that keeps the host is not followed either.
func TestNoSameHostRedirect(t *testing.T) {
	r := newRouted(t, "bearer", routedStage)
	r.prov.Func = func(req *http.Request, _ []byte) anthropicfake.Reply {
		if req.URL.Path == "/api/v1/messages" {
			return anthropicfake.Reply{Status: http.StatusPermanentRedirect, Header: http.Header{"Location": {"/elsewhere"}}}
		}
		return anthropicfake.MessageOK(dsModel, pricing.Usage{Input: 1, Output: 1})
	}
	if resp, _ := r.post(msg(dsModel, 100)); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
	if r.prov.Count() != 1 {
		t.Errorf("the provider saw %d calls", r.prov.Count())
	}
}

func TestAgentAuthHeaderNeverForwarded(t *testing.T) {
	for _, auth := range []string{"bearer", "x-api-key"} {
		t.Run(auth, func(t *testing.T) {
			u := pricing.Usage{Input: 1, Output: 1}
			r := newRouted(t, auth, routedStage, anthropicfake.MessageOK(dsModel, u))
			resp, b := r.post(msg(dsModel, 100),
				"Authorization", "Bearer "+agentSecret,
				"Proxy-Authorization", "Basic "+agentSecret,
				"X-Goog-Api-Key", agentSecret,
				"Anthropic-Beta", "prompt-caching-2024-07-31")
			if resp.StatusCode != 200 {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			h := r.prov.Seen()[0].Header
			for k, vs := range h {
				for _, v := range vs {
					if strings.Contains(v, agentSecret) {
						t.Errorf("the agent's credential reached the provider in %s", k)
					}
				}
			}
			want := "Bearer " + providerKey
			if auth == "x-api-key" {
				want = providerKey
			}
			name := map[string]string{"bearer": "Authorization", "x-api-key": "X-Api-Key"}[auth]
			if vs := h.Values(name); len(vs) != 1 || vs[0] != want {
				t.Errorf("%s = %q, want only the route key", name, vs)
			}
			if h.Get("Anthropic-Beta") == "" {
				t.Error("an ordinary header was dropped")
			}
			if strings.Contains(fmt.Sprint(h), testKey) {
				t.Error("the Anthropic key reached the provider")
			}
		})
	}
	// The provider key never goes to Anthropic either.
	r := newRouted(t, "bearer", routedStage)
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 1, Output: 1})}
	if resp, _ := r.post(msg(sonnet, 100), "Authorization", "Bearer "+agentSecret); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if h := fmt.Sprint(r.fake.Seen()[0].Header); strings.Contains(h, providerKey) || strings.Contains(h, agentSecret) {
		t.Errorf("Anthropic saw a credential that isn't its own: %s", h)
	}
}

func TestGatewayTokenNeverSentUpstream(t *testing.T) {
	u := pricing.Usage{Input: 1, Output: 1}
	r := newRouted(t, "x-api-key", routedStage, anthropicfake.MessageOK(dsModel, u), anthropicfake.MessageOK(dsModel, u))
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}
	tok := r.gw.Token()
	send := func(model string) {
		t.Helper()
		req := r.request(context.Background(), "/v1/messages?beta=true&k="+tok, msg(model, 100),
			"X-Custom", "pre-"+tok, "Authorization", "Bearer "+tok)
		resp, b := r.do(req)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", model, resp.StatusCode, b)
		}
	}
	send(dsModel)
	send(sonnet)
	for name, f := range map[string]*anthropicfake.Fake{"provider": r.prov, "anthropic": r.fake} {
		for i, req := range f.Seen() {
			if s := fmt.Sprint(req.Header, req.URL.String()); strings.Contains(s, tok) {
				t.Errorf("%s request %d carried the gateway token: %s", name, i, s)
			}
			if string(f.Body(i)) == "" || strings.Contains(string(f.Body(i)), tok) {
				t.Errorf("%s body %d odd", name, i)
			}
		}
	}
	if got := r.prov.Seen()[0].Header.Get("x-api-key"); got != providerKey {
		t.Errorf("provider x-api-key = %q", got)
	}
	if got := r.fake.Seen()[0].Header.Get("x-api-key"); got != testKey {
		t.Errorf("anthropic x-api-key = %q", got)
	}
}

func TestProviderKeyUnavailableSendsNothing(t *testing.T) {
	r := newRoutedCred(t, "bearer", func() (string, error) { return "", fmt.Errorf("secret %s gone", providerKey) }, routedStage)
	resp, b := r.post(msg(dsModel, 100))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if strings.Contains(b, providerKey) || r.prov.Count() != 0 {
		t.Errorf("leak or send: %s", b)
	}
	if l := r.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
}

func TestKillSwitchCancelsProviderStream(t *testing.T) {
	slow := anthropicfake.StreamOK(dsModel, pricing.Usage{Input: 10, Output: 10})
	slow.EventDelay = 300 * time.Millisecond
	r := newRouted(t, "bearer", routedStage, slow)
	resp, err := http.DefaultClient.Do(r.request(context.Background(), "/v1/messages", msg(dsModel, 100, `"stream":true`)))
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(resp.Body)
	readEvent(t, br)
	if !r.gw.HaltExternal(Halt{Reason: "kill_switch", Scope: "global", Detail: "the global kill switch is on"}) {
		t.Fatal("HaltExternal had no effect")
	}
	start := time.Now()
	rest, _ := io.ReadAll(br)
	resp.Body.Close()
	if strings.Contains(string(rest), "message_stop") || time.Since(start) > 2*time.Second {
		t.Errorf("the provider stream wasn't cancelled (%s)", time.Since(start))
	}
	select {
	case <-r.prov.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the provider request wasn't cancelled")
	}
	waitFor(t, "settle", func() bool { return r.gw.Ledger().Reserved == 0 })
}

func TestCountTokensOfRoutedModelNotForwarded(t *testing.T) {
	r := newRouted(t, "bearer", routedStage)
	r.fake.Script = []anthropicfake.Reply{{Body: `{"input_tokens":7}`}}
	for _, body := range []string{
		`{"model":"` + dsModel + `","messages":[{"role":"user","content":"secret code"}]}`,
		`{"Model":"` + dsModel + `","messages":[]}`,
	} {
		resp, _ := r.post2("/v1/messages/count_tokens", body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status %d, want 404", resp.StatusCode)
		}
	}
	if r.fake.Count() != 0 || r.prov.Count() != 0 {
		t.Errorf("a routed model's count was forwarded (Anthropic %d, provider %d)", r.fake.Count(), r.prov.Count())
	}
	// A Claude count still goes to Anthropic.
	if resp, got := r.post2("/v1/messages/count_tokens", `{"model":"`+sonnet+`","messages":[]}`); resp.StatusCode != 200 || got != `{"input_tokens":7}` {
		t.Errorf("claude count: %d %s", resp.StatusCode, got)
	}
}

func TestStartRejectsBadRoutes(t *testing.T) {
	cred := func() (string, error) { return "k", nil }
	good := Route{Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Credential: cred}
	with := func(edit func(*Route)) []Route { r := good; edit(&r); return []Route{r} }
	ok := func(rs []Route, u Upstream) error {
		_, err := Start(context.Background(), Options{Upstream: u, Routes: rs, Prices: pricing.Embedded(), Mode: Observe})
		return err
	}
	anth := Upstream{Kind: "anthropic", APIKey: testKey}
	if err := ok([]Route{good}, anth); err != nil {
		t.Fatalf("a path prefix on a provider base was refused: %v", err)
	}
	if err := ok([]Route{good, {Name: "loop", Models: []string{"x/y"}, BaseURL: "http://127.0.0.1:9/v1", Auth: "x-api-key", Credential: cred}}, anth); err != nil {
		t.Errorf("loopback http: %v", err)
	}
	for name, rs := range map[string][]Route{
		"http, not loopback": with(func(r *Route) { r.BaseURL = "http://openrouter.ai/api" }),
		"query":              with(func(r *Route) { r.BaseURL = "https://openrouter.ai/api?x=1" }),
		"credentials":        with(func(r *Route) { r.BaseURL = "https://u:p@openrouter.ai/api" }),
		"dot segment":        with(func(r *Route) { r.BaseURL = "https://openrouter.ai/api/../x" }),
		"no base":            with(func(r *Route) { r.BaseURL = "" }),
		"no credential":      with(func(r *Route) { r.Credential = nil }),
		"bad auth":           with(func(r *Route) { r.Auth = "basic" }),
		"no models":          with(func(r *Route) { r.Models = nil }),
		"claims claude":      with(func(r *Route) { r.Models = []string{"claude-*"} }),
		"claims everything":  with(func(r *Route) { r.Models = []string{"*"} }),
		"claims cl*":         with(func(r *Route) { r.Models = []string{"cl*"} }),
		"mid wildcard":       with(func(r *Route) { r.Models = []string{"a*b"} }),
		"overlap":            {good, {Name: "second", Models: []string{"deepseek/deepseek-v4-flash"}, BaseURL: "https://x.example", Auth: "bearer", Credential: cred}},
		"negative fee":       with(func(r *Route) { r.FeePct = -1 }),
		"NaN fee":            with(func(r *Route) { r.FeePct = math.NaN() }),
		"infinite fee":       with(func(r *Route) { r.FeePct = math.Inf(1) }),
		"fee over the max":   with(func(r *Route) { r.FeePct = MaxFeePct + 0.5 }),
	} {
		if err := ok(rs, anth); err == nil {
			t.Errorf("%s: Start accepted the route", name)
		}
	}
	for name, rs := range map[string][]Route{
		"claims anthropic/*": with(func(r *Route) { r.Models = []string{"anthropic/*"} }),
		"claims anthropic/x": with(func(r *Route) { r.Models = []string{"anthropic/claude-sonnet-4"} }),
		"upper-case name":    with(func(r *Route) { r.Name = "OpenRouter" }),
		"name with a space":  with(func(r *Route) { r.Name = "open router" }),
		"empty name":         with(func(r *Route) { r.Name = "" }),
		"name too long":      with(func(r *Route) { r.Name = "a" + strings.Repeat("b", 20) }),
	} {
		if err := ok(rs, anth); err == nil {
			t.Errorf("%s: Start accepted the route", name)
		}
	}
	if err := ok([]Route{good}, Upstream{Kind: "vertex", Token: staticToken("t"), VertexProject: "proj-1234", VertexLocations: []string{"us-east5"}}); err == nil {
		t.Error("routes with the vertex upstream were accepted")
	}
	// The Anthropic upstream stays strict: no path.
	if err := ok(nil, Upstream{Kind: "anthropic", APIKey: testKey, BaseURL: "https://example.com/proxy"}); err == nil {
		t.Error("a path on the Anthropic base was accepted")
	}
}

// A provider echoes what it was sent in its 401s (masked or whole). Nothing
// of the key may reach the agent or the log, and Claude Code must still see
// the error type it acts on.
func TestUpstreamErrorEchoingKeyIsRedacted(t *testing.T) {
	cases := []struct {
		status int
		typ    string
	}{
		{401, "authentication_error"}, {429, "rate_limit_error"}, {529, "overloaded_error"}, {400, "invalid_request_error"},
	}
	for _, auth := range []string{"bearer", "x-api-key"} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/%d", auth, c.status), func(t *testing.T) {
				r := newRouted(t, auth, routedStage)
				r.prov.Func = func(req *http.Request, _ []byte) anthropicfake.Reply {
					key := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
					if key == "" {
						key = req.Header.Get("x-api-key")
					}
					masked := key[:9] + "..." + key[len(key)-4:]
					return anthropicfake.Reply{Status: c.status,
						Header: http.Header{
							"X-Echo":           {"key " + key},
							"X-Masked":         {masked},
							"Www-Authenticate": {"Bearer realm=" + key},
							"Set-Cookie":       {"k=" + key},
							"Retry-After":      {"7"},
							"X-Request-Id":     {"req_123"},
							"X-Provider-Org":   {"org-secret"},
						},
						Body: fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":"invalid key %s (%s) in %s"},"user_id":"u-secret"}`,
							c.typ, key, masked, key[:12])}
				}
				resp, b := r.post(msg(dsModel, 100))
				if resp.StatusCode != c.status {
					t.Fatalf("status %d, want %d: %s", resp.StatusCode, c.status, b)
				}
				typ, m := apiError(t, b)
				if typ != c.typ {
					t.Errorf("error type %q, want %q", typ, c.typ)
				}
				if strings.Contains(b, "u-secret") || strings.Contains(m, "invalid key") {
					t.Errorf("provider text reached the agent: %s", b)
				}
				hs := fmt.Sprint(resp.Header)
				for _, leak := range []string{providerKey, providerKey[:9], providerKey[:12], providerKey[len(providerKey)-4:], "org-secret"} {
					if strings.Contains(b+hs, leak) {
						t.Errorf("%q reached the agent: %s %s", leak, b, hs)
					}
					if strings.Contains(r.logs.String(), leak) {
						t.Errorf("%q reached the log: %s", leak, r.logs.String())
					}
				}
				if resp.Header.Get("Retry-After") != "7" || resp.Header.Get("X-Request-Id") != "req_123" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
					t.Errorf("an allowlisted header was lost: %v", resp.Header)
				}
				if resp.Header.Get("Set-Cookie") != "" {
					t.Error("a cookie reached the agent")
				}
				if l := r.gw.Ledger(); l.Reserved != 0 {
					t.Errorf("reserved %d", l.Reserved)
				}
			})
		}
	}
}

// An error body that is not an Anthropic error still maps to a usable type.
func TestUpstreamErrorWithoutTypeGetsOneFromStatus(t *testing.T) {
	for status, want := range map[int]string{401: "authentication_error", 403: "permission_error", 404: "not_found_error", 429: "rate_limit_error", 500: "api_error", 502: "api_error", 503: "overloaded_error", 422: "invalid_request_error"} {
		r := newRouted(t, "bearer", routedStage, anthropicfake.Reply{Status: status, Body: "<html>" + providerKey + "</html>"})
		resp, b := r.post(msg(dsModel, 100))
		if typ, _ := apiError(t, b); resp.StatusCode != status || typ != want || strings.Contains(b, providerKey) {
			t.Errorf("%d: got %d %q %s", status, resp.StatusCode, typ, b)
		}
	}
}

// A routed 2xx passes only an allowlist of headers (as an error does): a
// cookie, an account identifier or any unknown header never reaches the
// agent. A Claude call's headers are unchanged.
func TestRoutedSuccessHeadersAreAllowlisted(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	r := newRouted(t, "bearer", routedStage)
	hdr := http.Header{
		"Set-Cookie": {"sid=abc"}, "X-Provider-Org": {"org-secret"}, "X-Generation-Id": {"gen-1"},
		"Retry-After": {"7"}, "X-Request-Id": {"req_123"}, "X-Ratelimit-Remaining": {"41"}, "Anthropic-Ratelimit-Requests-Remaining": {"9"},
	}
	r.prov.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK(dsModel, u)
		rep.Header = hdr.Clone()
		return rep
	}
	resp, b := r.post(msg(dsModel, 100))
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	for k, want := range map[string]string{"Retry-After": "7", "X-Request-Id": "req_123", "X-Ratelimit-Remaining": "41", "Anthropic-Ratelimit-Requests-Remaining": "9"} {
		if resp.Header.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, resp.Header.Get(k), want)
		}
	}
	if resp.Header.Get("Content-Type") == "" {
		t.Error("the content type was lost")
	}
	for _, k := range []string{"Set-Cookie", "X-Provider-Org", "X-Generation-Id"} {
		if resp.Header.Get(k) != "" {
			t.Errorf("%s reached the agent: %v", k, resp.Header)
		}
	}
	// Claude's headers still pass through.
	r.fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK(sonnet, u)
		rep.Header = http.Header{"X-Generation-Id": {"claude-gen"}, "Anthropic-Ratelimit-Requests-Limit": {"50"}}
		return rep
	}
	if resp, b := r.post(msg(sonnet, 100)); resp.StatusCode != 200 || resp.Header.Get("X-Generation-Id") != "claude-gen" || resp.Header.Get("Anthropic-Ratelimit-Requests-Limit") != "50" {
		t.Errorf("claude headers: %d %v %s", resp.StatusCode, resp.Header, b)
	}
}

// Whatever else comes back on a routed 2xx (a stream, a body, a header)
// is scrubbed of the exact key too.
func TestRoutedSuccessScrubsKey(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	t.Run("body", func(t *testing.T) {
		rep := anthropicfake.MessageOK(dsModel, u)
		rep.Body = strings.Replace(rep.Body, `"msg_fake"`, `"`+providerKey+`"`, 1)
		rep.Header = http.Header{"X-Echo": {"Bearer " + providerKey}, "Request-Id": {"Bearer " + providerKey}, "Retry-After": {"fine"}}
		r := newRouted(t, "bearer", routedStage, rep)
		resp, b := r.post(msg(dsModel, 100))
		if resp.StatusCode != 200 || strings.Contains(b+fmt.Sprint(resp.Header), providerKey) || resp.Header.Get("Retry-After") != "fine" {
			t.Errorf("%d %s %v", resp.StatusCode, b, resp.Header)
		}
	})
	t.Run("stream", func(t *testing.T) {
		rep := anthropicfake.StreamOK(dsModel, u)
		rep.Events = append([]anthropicfake.Event(nil), rep.Events...)
		rep.Events[0].Data = strings.Replace(rep.Events[0].Data, `"msg_fake"`, `"`+providerKey+`"`, 1)
		r := newRouted(t, "bearer", routedStage, rep)
		resp, b := r.do(r.request(context.Background(), "/v1/messages", msg(dsModel, 100, `"stream":true`)))
		if resp.StatusCode != 200 || strings.Contains(b, providerKey) || !strings.Contains(b, "message_stop") || !strings.Contains(b, "[redacted]") {
			t.Errorf("%d %s", resp.StatusCode, b)
		}
	})
}

func TestRedactWriterAcrossChunks(t *testing.T) {
	var out strings.Builder
	rw := newRedactWriter(&out, "SECRETKEY")
	for _, c := range []string{"a SEC", "RETK", "EY b SECRETKEYSECR", "ETKEY c SECRE", "x SECRET"} {
		if _, err := rw.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	rw.Close()
	if got, want := out.String(), "a [redacted] b [redacted][redacted] c SECREx SECRET"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProviderGetsOnlyAllowlistedHeaders(t *testing.T) {
	u := pricing.Usage{Input: 1, Output: 1}
	r := newRouted(t, "bearer", routedStage, anthropicfake.MessageOK(dsModel, u))
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}
	hdr := []string{"Accept", "application/json", "Anthropic-Beta", "b1", "User-Agent", "claude-cli/9",
		"Cookie", "sid=1", "X-Claude-Code-Session-Id", "sess", "X-Claude-Code-Agent-Id", "ag",
		"X-Stainless-Os", "Linux", "X-App", "cli", "X-Custom", "v"}
	if resp, b := r.post(msg(dsModel, 100), hdr...); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	h := r.prov.Seen()[0].Header
	for _, k := range []string{"Cookie", "X-Claude-Code-Session-Id", "X-Claude-Code-Agent-Id", "X-Stainless-Os", "X-App", "X-Custom"} {
		if h.Get(k) != "" {
			t.Errorf("%s reached the provider", k)
		}
	}
	for k, want := range map[string]string{"Content-Type": "application/json", "Anthropic-Version": "2023-06-01", "Anthropic-Beta": "b1", "User-Agent": "claude-cli/9", "Accept": "application/json"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	// Anthropic gets everything it got before.
	if resp, b := r.post(msg(sonnet, 100), hdr...); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	ah := r.fake.Seen()[0].Header
	for _, k := range []string{"Cookie", "X-Claude-Code-Session-Id", "X-Stainless-Os", "X-Custom"} {
		if ah.Get(k) == "" {
			t.Errorf("Anthropic lost %s", k)
		}
	}
}

func TestScrubTokenStrict(t *testing.T) {
	r := newRouted(t, "bearer", routedStage)
	s := r.gw
	tok := s.Token()
	for name, v := range map[string]string{
		"plain":      "a" + tok,
		"upper":      strings.ToUpper(tok),
		"percent":    "x%" + fmt.Sprintf("%02X", tok[0]) + tok[1:],
		"lower pct":  "x%" + fmt.Sprintf("%02x", tok[0]) + tok[1:],
		"half pct":   "x%" + fmt.Sprintf("%02x", tok[0]) + strings.ToUpper(tok[1:]),
		"bad escape": "100%zz" + tok,
	} {
		h := http.Header{"X-A": {v}, "X-B": {"keep"}}
		s.scrubToken(h)
		if h.Get("X-A") != "" || h.Get("X-B") != "keep" {
			t.Errorf("%s: %v", name, h)
		}
		if q := s.upstreamQuery(&http.Request{URL: &url.URL{RawQuery: "k=" + v}}); q != "" {
			t.Errorf("%s: the query kept the token: %q", name, q)
		}
	}
	if q := s.upstreamQuery(&http.Request{URL: &url.URL{RawQuery: "beta=true"}}); q != "?beta=true" {
		t.Errorf("an innocent query: %q", q)
	}
}

func TestStartCopiesRoutes(t *testing.T) {
	routes := []Route{{Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Credential: func() (string, error) { return "k", nil }}}
	gw, err := Start(context.Background(), Options{Upstream: Upstream{Kind: "anthropic", APIKey: testKey}, Routes: routes, Prices: pricing.Embedded(), Mode: Observe})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gw.Close(context.Background()) })
	routes[0].Models[0] = "claude-*"
	routes[0].BaseURL = "https://evil.example"
	routes[0].Auth = "x-api-key"
	if r := gw.routeFor(dsModel); r == nil || r.BaseURL != "https://openrouter.ai/api" || r.Auth != "bearer" {
		t.Errorf("the caller's slice changed the gateway's routes: %+v", r)
	}
	if gw.routeFor(sonnet) != nil {
		t.Error("a Claude model got routed after the caller edited its slice")
	}
}

// Route.Credential is called from concurrent calls.
func TestConcurrentRoutedCalls(t *testing.T) {
	u := pricing.Usage{Input: 1, Output: 1}
	var calls atomic.Int64
	r := newRoutedCred(t, "bearer", func() (string, error) { calls.Add(1); return providerKey, nil }, routedStage)
	r.prov.Func = func(*http.Request, []byte) anthropicfake.Reply { return anthropicfake.MessageOK(dsModel, u) }
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, b := r.post(msg(dsModel, 100)); resp.StatusCode != 200 {
				t.Errorf("%d %s", resp.StatusCode, b)
			}
		}()
	}
	wg.Wait()
	if r.prov.Count() != 8 || calls.Load() < 8 {
		t.Errorf("provider saw %d, credential read %d times", r.prov.Count(), calls.Load())
	}
}

// Odd spellings of the model: each is the pin exactly (and goes where the
// pin goes) or is refused as unpinned; none reaches another upstream.
func TestOddModelSpellingsNeverMisroute(t *testing.T) {
	u := pricing.Usage{Input: 1, Output: 1}
	for name, body := range map[string]string{
		"leading space":   msg(" "+dsModel, 100),
		"trailing space":  msg(dsModel+" ", 100),
		"fullwidth slash": msg("deepseek\uff0fdeepseek-v4-flash", 100),
		"json-escaped /":  strings.Replace(msg(dsModel, 100), "deepseek/", `deepseek\/`, 1),
		"json-escaped d":  strings.Replace(msg(dsModel, 100), "deepseek", `\u0064eepseek`, 1),
		"upper":           msg(strings.ToUpper(dsModel), 100),
		"nul suffix":      msg(dsModel+`\u0000`, 100),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRouted(t, "bearer", routedStage, anthropicfake.MessageOK(dsModel, u))
			r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}
			resp, b := r.post(body)
			if r.fake.Count() != 0 {
				t.Fatalf("a routed-looking model reached Anthropic: %d %s", resp.StatusCode, b)
			}
			switch resp.StatusCode {
			case 400:
				if r.prov.Count() != 0 {
					t.Error("refused, yet sent to the provider")
				}
			case 200:
				if r.prov.Count() != 1 {
					t.Error("accepted, but not at the provider")
				}
			default:
				t.Errorf("status %d %s", resp.StatusCode, b)
			}
		})
	}
	// A pin with the odd spelling is matched only by that spelling.
	for _, pin := range []string{" " + dsModel, dsModel + " "} {
		r := newRouted(t, "bearer", Stage{Name: "implement", Model: pin, Background: pin})
		if resp, _ := r.post(msg(dsModel, 100)); resp.StatusCode != 400 || r.prov.Count() != 0 || r.fake.Count() != 0 {
			t.Errorf("pin %q let the plain ID through: %d", pin, resp.StatusCode)
		}
	}
}

func TestPinsAcrossProviders(t *testing.T) {
	u := pricing.Usage{Input: 1, Output: 1}
	// A Claude pin does not admit a routed model.
	r := newRouted(t, "bearer", Stage{Name: "implement", Model: sonnet, Background: haiku})
	if resp, _ := r.post(msg(dsModel, 100)); resp.StatusCode != 400 || r.prov.Count() != 0 || r.fake.Count() != 0 {
		t.Errorf("claude pin, routed request: %d", resp.StatusCode)
	}
	// A routed pin does not admit a Claude model, by ID or alias.
	r = newRouted(t, "bearer", Stage{Name: "implement", Model: dsModel, Background: dsModel})
	for _, m := range []string{sonnet, "claude-sonnet-4-5", haiku} {
		if resp, _ := r.post(msg(m, 100)); resp.StatusCode != 400 || r.prov.Count() != 0 || r.fake.Count() != 0 {
			t.Errorf("routed pin, %s request: %d", m, resp.StatusCode)
		}
	}
	// The background model may be the routed one.
	r = newRouted(t, "bearer", Stage{Name: "implement", Model: sonnet, Background: dsModel}, anthropicfake.MessageOK(dsModel, u))
	if resp, b := r.post(msg(dsModel, 100)); resp.StatusCode != 200 || r.prov.Count() != 1 || r.fake.Count() != 0 {
		t.Errorf("routed background: %d %s", resp.StatusCode, b)
	}
}

func TestStageMaxOutputAppliesToRoutedModel(t *testing.T) {
	r := newRouted(t, "bearer", Stage{Name: "implement", Model: dsModel, Background: sonnet, MaxOutputTokens: 500})
	resp, b := r.post(msg(dsModel, 501))
	if resp.StatusCode != 400 || !strings.Contains(b, "above the stage's limit") || r.prov.Count() != 0 {
		t.Errorf("%d %s", resp.StatusCode, b)
	}
	r.prov.Script = []anthropicfake.Reply{anthropicfake.MessageOK(dsModel, pricing.Usage{Input: 1, Output: 1})}
	if resp, b := r.post(msg(dsModel, 500)); resp.StatusCode != 200 {
		t.Errorf("%d %s", resp.StatusCode, b)
	}
}

func TestCountTokensMalformedWithRoutesIs400(t *testing.T) {
	r := newRouted(t, "bearer", routedStage)
	r.fake.Script = []anthropicfake.Reply{{Body: `{"input_tokens":7}`}}
	for name, body := range map[string]string{
		"duplicate model": `{"model":"` + sonnet + `","model":"` + dsModel + `","messages":[]}`,
		"duplicate case":  `{"model":"` + sonnet + `","Model":"` + dsModel + `","messages":[]}`,
		"malformed":       `{"model":"` + dsModel + `",`,
		"not an object":   `[1]`,
	} {
		resp, b := r.post2("/v1/messages/count_tokens", body)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d %s, want 400", name, resp.StatusCode, b)
		}
	}
	if r.fake.Count() != 0 || r.prov.Count() != 0 {
		t.Errorf("a malformed count was forwarded (Anthropic %d, provider %d)", r.fake.Count(), r.prov.Count())
	}
	// Without routes the body still goes to Anthropic, as before.
	h := newHarness(t, anthropicfake.Reply{Body: `{"input_tokens":7}`})
	if resp, _ := h.post2("/v1/messages/count_tokens", `{"model":"`+sonnet+`","model":"x"}`); resp.StatusCode == 400 && h.fake.Count() == 0 {
		t.Error("the no-routes gateway changed its count_tokens behaviour")
	}
}

// The gateway's fee limit mirrors the project config's (the gateway cannot
// import config in its own code, so this is where the two meet).
func TestMaxFeePctMatchesConfig(t *testing.T) {
	if MaxFeePct != config.MaxRouteFeePct {
		t.Errorf("gateway.MaxFeePct = %d, config.MaxRouteFeePct = %d", MaxFeePct, config.MaxRouteFeePct)
	}
}

// The owner's config (config.ValidateModelProviders) and the gateway's routes
// must accept and refuse the same base URLs and model patterns: a config that
// validates must start, and what the gateway refuses must not validate.
func TestRouteAndConfigAgreeOnBaseURLsAndPatterns(t *testing.T) {
	cred := func() (string, error) { return "k", nil }
	anth := Upstream{Kind: "anthropic", APIKey: testKey}
	for _, u := range []string{
		"https://openrouter.ai/api", "https://openrouter.ai", "https://openrouter.ai/api/", "http://127.0.0.1:9/v1", "http://localhost:9",
		"http://openrouter.ai/api", "https://openrouter.ai/api?", "https://openrouter.ai/api?x=1", "https://openrouter.ai/api#f",
		"https://u:p@openrouter.ai/api", "https://openrouter.ai/a%2Fb", "https://openrouter.ai/a%2e%2e/b", "https://openrouter.ai/api/../x",
		"https://openrouter.ai/./api", "https://openrouter.ai/a\\b", "ftp://openrouter.ai", "openrouter.ai/api", "",
	} {
		_, gerr := Start(context.Background(), Options{Upstream: anth, Prices: pricing.Embedded(), Mode: Observe,
			Routes: []Route{{Name: "p", Models: []string{"deepseek/*"}, BaseURL: u, Auth: "bearer", Credential: cred}}})
		p := config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: u, Auth: "bearer", Secret: "p-key", Models: []string{"deepseek/*"}}
		cerr := len(config.ValidateModelProviders(map[string]config.ModelProvider{"p": p})) > 0
		if (gerr != nil) != cerr {
			t.Errorf("base URL %q: gateway refuses=%v (%v), config refuses=%v", u, gerr != nil, gerr, cerr)
		}
	}
	for _, m := range []string{
		"deepseek/*", "deepseek/deepseek-v4-flash", "x/y", "claude-*", "claude-sonnet-5-5", "cl*", "anthropic/*", "anthropic/claude-sonnet-4", "anth*",
		"*", "a*b", "a*", "", "has space/*", "-lead/*", strings.Repeat("a", 101), "d**",
	} {
		_, gerr := Start(context.Background(), Options{Upstream: anth, Prices: pricing.Embedded(), Mode: Observe,
			Routes: []Route{{Name: "p", Models: []string{m}, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Credential: cred}}})
		p := config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "p-key", Models: []string{m}}
		cerr := len(config.ValidateModelProviders(map[string]config.ModelProvider{"p": p})) > 0
		if (gerr != nil) != cerr {
			t.Errorf("model %q: gateway refuses=%v (%v), config refuses=%v", m, gerr != nil, gerr, cerr)
		}
	}
	// Match and overlap are one function; pin a table anyway.
	for _, c := range []struct {
		a, b    string
		overlap bool
	}{
		{"deepseek/*", "deepseek/deepseek-v4-flash", true},
		{"deepseek/*", "deep*", true},
		{"deepseek/a", "deepseek/b", false},
		{"x/*", "y/*", false},
		{"a", "a", true},
	} {
		if got := config.PatternsOverlap(c.a, c.b); got != c.overlap {
			t.Errorf("PatternsOverlap(%q, %q) = %v", c.a, c.b, got)
		}
		r, o := Route{Models: []string{c.a}}, Route{Models: []string{c.b}}
		if r.overlaps(o) != c.overlap || r.matches(c.b) != config.PatternMatches(c.a, c.b) {
			t.Errorf("route and config disagree on %q / %q", c.a, c.b)
		}
	}
}
