package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	prov, psrv := anthropicfake.New(t, provScript...)
	h := newHarnessWith(t, func(o *Options) {
		o.Routes = []Route{{
			Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: psrv.URL + "/api", Auth: auth,
			Credential: func() (string, error) { return providerKey, nil },
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
	r := newRouted(t, "bearer", routedStage)
	r.gw.o.Routes[0].Credential = func() (string, error) { return "", fmt.Errorf("secret %s gone", providerKey) }
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
