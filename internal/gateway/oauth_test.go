package gateway

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// oauthToken is the fake subscription token these tests use, built at run
// time from the test's name (Convention 2): no literal here is, or looks
// like, a credential.
func oauthToken(t *testing.T) string {
	t.Helper()
	return "oauth-test-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

// newOAuth starts a gateway on the tier-A OAuth upstream, pointed at a
// fake Anthropic: its loopback base stands in for Anthropic's own
// endpoint, the only base the kind accepts.
func newOAuth(t *testing.T, token string, script ...anthropicfake.Reply) *harness {
	t.Helper()
	return newHarnessWith(t, func(o *Options) {
		o.Upstream.Kind, o.Upstream.APIKey = KindOAuth, token
	}, script...)
}

// TestOAuthHeaderAllowListIsTheRecordedSet pins the allow-list against
// Check 90's FACT headers-observed line. Editing it means editing the
// evidence, deliberately.
func TestOAuthHeaderAllowListIsTheRecordedSet(t *testing.T) {
	want := []string{
		"Anthropic-Version", "Accept", "Anthropic-Beta", "Anthropic-Dangerous-Direct-Browser-Access",
		"Content-Type", "User-Agent", "X-App", "X-Claude-Code-Session-Id",
		"X-Stainless-Arch", "X-Stainless-Lang", "X-Stainless-Os", "X-Stainless-Package-Version",
		"X-Stainless-Retry-Count", "X-Stainless-Runtime", "X-Stainless-Runtime-Version", "X-Stainless-Timeout",
	}
	if len(oauthRequestHeaders) != len(want) {
		t.Fatalf("the allow-list holds %d headers, Check 90 recorded %d: %v", len(oauthRequestHeaders), len(want), oauthRequestHeaders)
	}
	for _, k := range want {
		if !contains(oauthRequestHeaders, k) {
			t.Errorf("Check 90 recorded %s and the allow-list dropped it", k)
		}
	}
}

// TestOAuthUpstreamAttachesRecordedHeaders: an OAuth call carries exactly
// Check 90's recorded headers, the subscription credential as its bearer
// token, and nothing else -- in particular not the run's own gateway token
// and not a header Check 90 never saw.
func TestOAuthUpstreamAttachesRecordedHeaders(t *testing.T) {
	token := oauthToken(t)
	h := newOAuth(t, token, anthropicfake.MessageOK(sonnet, smallUsage))

	recorded := map[string]string{
		"anthropic-version": "2023-06-01",
		"accept":            "application/json",
		"anthropic-beta":    "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		"anthropic-dangerous-direct-browser-access": "true",
		"content-type":                "application/json",
		"user-agent":                  "claude-cli/2.1.305 (external, cli)",
		"x-app":                       "cli",
		"x-claude-code-session-id":    "sess-oauth-1",
		"x-stainless-arch":            "arm64",
		"x-stainless-lang":            "js",
		"x-stainless-os":              "MacOS",
		"x-stainless-package-version": "0.60.0",
		"x-stainless-retry-count":     "0",
		"x-stainless-runtime":         "node",
		"x-stainless-runtime-version": "v22.14.0",
		"x-stainless-timeout":         "600",
	}
	hdr := make([]string, 0, 2*len(recorded)+8)
	for k, v := range recorded {
		hdr = append(hdr, k, v)
	}
	// What Claude Code sends beside them and what a hostile client might:
	// none of it may travel with a person's subscription token.
	unrecorded := []string{
		"x-claude-code-agent-id", "agent-1",
		"x-api-key", h.gw.Token(),
		"authorization", "Bearer " + h.gw.Token(),
		"cookie", "sid=1",
		"x-goog-user-project", "someone-elses-project",
		"x-custom", "v",
	}
	resp, body := h.post(msg(sonnet, 100), append(hdr, unrecorded...)...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if h.fake.Count() != 1 {
		t.Fatalf("the upstream saw %d requests", h.fake.Count())
	}
	seen := h.fake.Seen()[0].Header

	// The credential: Check 90's swap-credential-header, once, as a bearer.
	if got := seen.Values("Authorization"); len(got) != 1 || got[0] != "Bearer "+token {
		t.Errorf("upstream Authorization = %q, want one %q", got, "Bearer "+token)
	}
	// Every recorded header arrived with the value the client sent.
	for k, want := range recorded {
		if got := seen.Get(k); got != want {
			t.Errorf("upstream %s = %q, want %q", k, got, want)
		}
	}
	// Nothing else did: the allow-list is the whole set, bar the credential
	// itself and the two headers the transport and the gateway set.
	allowed := map[string]bool{"Authorization": true, "Accept-Encoding": true, "Content-Length": true}
	for _, k := range oauthRequestHeaders {
		allowed[http.CanonicalHeaderKey(k)] = true
	}
	for k := range seen {
		if !allowed[k] {
			t.Errorf("upstream got %s, which Check 90 never recorded", k)
		}
	}
	for _, k := range []string{"X-Claude-Code-Agent-Id", "Cookie", "X-Goog-User-Project", "X-Custom", "x-api-key"} {
		if seen.Get(k) != "" {
			t.Errorf("%s reached Anthropic beside the subscription token", k)
		}
	}
	for k, vs := range seen {
		for _, v := range vs {
			if strings.Contains(v, h.gw.Token()) {
				t.Errorf("upstream header %s holds the run's gateway token", k)
			}
		}
	}
	if seen.Get("Accept-Encoding") != "identity" {
		t.Errorf("Accept-Encoding = %q, want identity", seen.Get("Accept-Encoding"))
	}
	// The credential reaches neither the client nor the log.
	if strings.Contains(body, token) || strings.Contains(h.logs.String(), token) {
		t.Error("the credential came back to the client or the log")
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			if strings.Contains(v, token) {
				t.Errorf("response header %s carries the credential", k)
			}
		}
	}
}

// TestOAuthAnthropicVersionIsPassedThroughNotInvented: anthropic-version is
// the one header Check 90 proved mandatory (dropping it failed the call),
// so the client's own value must arrive -- not a default the gateway
// chose.
func TestOAuthAnthropicVersionIsPassedThroughNotInvented(t *testing.T) {
	h := newOAuth(t, oauthToken(t), anthropicfake.MessageOK(sonnet, smallUsage))
	const version = "2024-10-22"
	if resp, b := h.post(msg(sonnet, 100), "anthropic-version", version); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if got := h.fake.Seen()[0].Header.Get("Anthropic-Version"); got != version {
		t.Errorf("upstream Anthropic-Version = %q, want the client's %q", got, version)
	}
}

// TestOAuthNeverSentToProviderRoute is the D133 invariant: an OAuth token
// is only ever sent to Anthropic's own endpoint. A route is a provider's
// endpoint, so no route may carry one -- not at start-up, and not from a
// Route built in code either.
func TestOAuthNeverSentToProviderRoute(t *testing.T) {
	token := oauthToken(t)
	_, psrv := anthropicfake.New(t, anthropicfake.MessageOK(dsModel, smallUsage))
	cred := func() (string, error) { return "provider-key-" + t.Name(), nil }

	// 1. An OAuth upstream takes no routes at all, whatever they point at
	//    (Anthropic's own base included: the upstream is the only OAuth
	//    target).
	for _, base := range []string{psrv.URL + "/api", psrv.URL, "https://api.anthropic.com"} {
		gw, err := Start(context.Background(), Options{
			Upstream: Upstream{Kind: KindOAuth, APIKey: token},
			Routes: []Route{{
				Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: base, Auth: "bearer", Credential: cred,
			}},
			Prices: dsPrices(), Mode: Observe,
		})
		if err == nil {
			_ = gw.Close(context.Background())
			t.Errorf("an OAuth upstream with a route to %s started", base)
			continue
		}
		if !strings.Contains(err.Error(), "never sent to a provider route") {
			t.Errorf("the refusal of a route to %s does not name the invariant: %v", base, err)
		}
	}

	// 2. The OAuth spelling is refused by name wherever a route is
	//    validated or used, and writes no header: a Route built in code,
	//    past validation, cannot put the token on the wire either.
	for _, auth := range []string{authOAuth, "OAuth", "OAUTH", "oAuth"} {
		r := &Route{Name: "openrouter", Models: []string{"deepseek/*"}, BaseURL: psrv.URL + "/api", Auth: auth, Credential: cred}
		if err := validateRoutes(Upstream{Kind: "anthropic", APIKey: testKey}, []Route{*r}); err == nil {
			t.Errorf("auth %q: a route was validated to carry an OAuth credential", auth)
		} else if !strings.Contains(err.Error(), "never sent to a provider route") {
			t.Errorf("auth %q: the refusal does not name the invariant: %v", auth, err)
		}
		h := http.Header{}
		if _, err := r.attachCredential(h); err == nil {
			t.Errorf("auth %q: a route attached an OAuth credential", auth)
		}
		if len(h) != 0 {
			t.Errorf("auth %q: a refused attach wrote %v", auth, h)
		}
	}

	// 3. The upstream's own path refuses a provider route even if one were
	//    handed to it: the check is where the token is attached, not only
	//    at start-up.
	s := &Server{o: Options{Upstream: Upstream{Kind: KindOAuth, APIKey: token}}}
	pr := &Route{Name: "openrouter", BaseURL: psrv.URL + "/api", Auth: "bearer", Credential: cred}
	if _, err := s.oauthUpstream(route{provider: pr, upstream: psrv.URL + "/api/v1/messages", path: "/v1/messages"}, http.Header{}); err == nil {
		t.Error("the OAuth upstream built a request for a provider route")
	}
}

// TestOAuthCredentialRedactedFromErrors: an upstream answer that echoes
// the credential -- in its body or in a header -- reaches the client and
// the log with the credential scrubbed.
func TestOAuthCredentialRedactedFromErrors(t *testing.T) {
	token := oauthToken(t)
	rep := anthropicfake.Error(http.StatusUnauthorized, "authentication_error", "invalid token "+token)
	rep.Header = http.Header{"X-Echo": {"the credential was Bearer " + token}}
	h := newOAuth(t, token, rep)

	resp, body := h.post(msg(sonnet, 100))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, token) {
		t.Errorf("the answer echoes the credential: %s", body)
	}
	if !strings.Contains(body, redacted) {
		t.Errorf("the echoed credential wasn't redacted: %s", body)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			if strings.Contains(v, token) {
				t.Errorf("response header %s carries the credential", k)
			}
		}
	}
	if strings.Contains(h.logs.String(), token) {
		t.Errorf("a log line carries the credential:\n%s", h.logs.String())
	}
}

// TestOAuthUpstreamStreamsUnbuffered: the credential is scrubbed from the
// stream without holding it back; the events arrive as the upstream sends
// them.
func TestOAuthUpstreamStreamsUnbuffered(t *testing.T) {
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.EventDelay = 200 * time.Millisecond
	h := newOAuth(t, oauthToken(t), rep)
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", msg(sonnet, 100, `"stream":true`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	first := readEvent(t, r)
	t1 := time.Now()
	second := readEvent(t, r)
	gap := time.Since(t1)
	if !strings.Contains(first, "message_start") || !strings.Contains(second, "ping") {
		t.Fatalf("events %q, %q", first, second)
	}
	if gap < 120*time.Millisecond {
		t.Errorf("the second event came %v after the first: the first was held back", gap)
	}
}

// TestOAuthRefusesANonAnthropicBase: the kind cannot be pointed anywhere
// but Anthropic's own endpoint, so the token has nowhere else to go.
func TestOAuthRefusesANonAnthropicBase(t *testing.T) {
	token := oauthToken(t)
	start := func(base string) error {
		gw, err := Start(context.Background(), Options{
			Upstream: Upstream{Kind: KindOAuth, BaseURL: base, APIKey: token},
			Prices:   dsPrices(), Mode: Observe,
		})
		if gw != nil {
			_ = gw.Close(context.Background())
		}
		return err
	}
	for _, base := range []string{"", "https://api.anthropic.com", "http://127.0.0.1:1"} {
		if err := start(base); err != nil {
			t.Errorf("base %q: %v", base, err)
		}
	}
	for _, base := range []string{
		"https://api.example.invalid",
		"https://evil.example",
		"https://api.anthropic.com.evil.example",
		"https://proxy.internal:8443",
	} {
		if err := start(base); err == nil {
			t.Errorf("an OAuth upstream pointed at %s started", base)
		}
	}
}

// TestOAuthRefusesAnEmptyCredential: the kind exists to attach a
// credential, so a gateway without one does not start, and the attach
// itself refuses an empty token -- an unauthenticated call to Anthropic
// under this run's own headers is never sent.
func TestOAuthRefusesAnEmptyCredential(t *testing.T) {
	gw, err := Start(context.Background(), Options{
		Upstream: Upstream{Kind: KindOAuth}, Prices: dsPrices(), Mode: Observe,
	})
	if err == nil {
		_ = gw.Close(context.Background())
		t.Fatal("an OAuth upstream with no credential started")
	}
	h := http.Header{}
	if err := attachOAuth(h, ""); err == nil {
		t.Error("an empty OAuth credential was attached")
	}
	if len(h) != 0 {
		t.Errorf("an empty credential wrote %v", h)
	}
	s := &Server{o: Options{Upstream: Upstream{Kind: KindOAuth}}}
	up, sent, err := s.upstreamRequest(context.Background(),
		httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(msg(sonnet, 10))),
		route{upstream: "https://api.anthropic.com/v1/messages", path: "/v1/messages"}, []byte(msg(sonnet, 10)))
	if err == nil {
		t.Fatalf("a credential-less OAuth request was built: %v", up)
	}
	if up != nil || (sent != nil && sent.Load()) {
		t.Error("a refused OAuth request was built or marked as sent")
	}
}

// TestOAuthResponseHeaderTimeoutBoundsTheWait: an upstream that never
// answers is given up on inside the bound (Check 90: the client itself
// waited 93 s for a delayed answer, so the bound is far above that), and
// the call is reported as a bad gateway rather than held open.
func TestOAuthResponseHeaderTimeoutBoundsTheWait(t *testing.T) {
	old := oauthResponseHeaderTimeout
	oauthResponseHeaderTimeout = 150 * time.Millisecond
	t.Cleanup(func() { oauthResponseHeaderTimeout = old })

	rep := anthropicfake.MessageOK(sonnet, smallUsage)
	rep.Hold = true
	h := newOAuth(t, oauthToken(t), rep)
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Do(h.request(context.Background(), "/v1/messages", msg(sonnet, 100)))
	if err != nil {
		t.Fatalf("the gateway held the call open: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502: %s", resp.StatusCode, b)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("the bound took %v, not the %v it was set to", el, oauthResponseHeaderTimeout)
	}
	if h.fake.Count() != 1 {
		t.Errorf("the upstream saw %d requests, want 1", h.fake.Count())
	}
}
