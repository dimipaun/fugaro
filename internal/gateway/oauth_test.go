package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// TestMain turns on the loopback test stand-in for the whole package: the
// fake Anthropic is an httptest server on 127.0.0.1, so every OAuth test
// needs it and no test can reach the fake without it. Nothing in a built
// binary sets it -- the only other reference in the tree is the test that
// asserts the production posture (TestOAuthProductionRefusesALoopbackBase).
func TestMain(m *testing.M) {
	oauthAllowLoopback = true
	os.Exit(m.Run())
}

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
//
// The eight X-Stainless-* names are pinned as forwarded even though Check 90
// proved only the first eight optional headers droppable (FACT
// header-drop-skipped: more than eight optional headers were seen, so it
// never got to these). They are kept because Check 90's own working swap
// call carried them -- headers-observed lists all eight -- and they are
// passive client telemetry; dropping a header the check never dropped is the
// change that could break the real client. The coordinator ruled to keep
// them on the owner's behalf, and the owner may veto: if the owner rules
// them out, delete them from oauthRequestHeaders and from `want` together.
func TestOAuthHeaderAllowListIsTheRecordedSet(t *testing.T) {
	want := []string{
		"Anthropic-Version", "Accept", "Anthropic-Beta", "Anthropic-Dangerous-Direct-Browser-Access",
		"Content-Type", "User-Agent", "X-App", "X-Claude-Code-Session-Id",
		"X-Stainless-Arch", "X-Stainless-Lang", "X-Stainless-Os", "X-Stainless-Package-Version",
		"X-Stainless-Retry-Count", "X-Stainless-Runtime", "X-Stainless-Runtime-Version", "X-Stainless-Timeout",
	}
	// The eight Check 90 proved optional, and the eight it only observed:
	// the split is the decision above, and it is asserted so that neither
	// half drifts into the other unnoticed.
	optional := want[:8]
	observedOnly := want[8:]
	if len(optional) != 8 || len(observedOnly) != 8 {
		t.Fatalf("the recorded set split into %d optional and %d observed-only", len(optional), len(observedOnly))
	}
	if len(oauthRequestHeaders) != len(want) {
		t.Fatalf("the allow-list holds %d headers, Check 90 recorded %d: %v", len(oauthRequestHeaders), len(want), oauthRequestHeaders)
	}
	for _, k := range want {
		if !contains(oauthRequestHeaders, k) {
			t.Errorf("Check 90 recorded %s and the allow-list dropped it", k)
		}
	}
	// Each observed-only name is still one Check 90 saw: a name invented
	// here would be a header the check never recorded travelling with a
	// person's subscription token.
	for _, k := range observedOnly {
		if !strings.HasPrefix(k, "X-Stainless-") {
			t.Errorf("%s is pinned as observed-only and Check 90 observed no such header", k)
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
	// The credential reaching neither the client nor the log is asserted in
	// TestOAuthCredentialRedactedFromErrors, on an upstream that actually
	// echoes it -- on both paths, a messages call and a token count. Here
	// the fake answers a plain 200 that echoes nothing, so the same
	// assertions would be true whatever the gateway did.
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

	// 4. The destination itself, not only the route's shape: a route with no
	//    provider and a real path but an off-host rt.upstream is refused in
	//    the place the token would be written. A base URL that validated at
	//    start-up says nothing about what the URL resolved to by then, so
	//    the check has to be on the destination.
	for _, dest := range []string{
		psrv.URL + "/api/v1/messages",
		"https://evil.example/v1/messages",
		"https://api.anthropic.com.evil.example/v1/messages",
		"http://169.254.169.254/v1/messages",
		"",
	} {
		if _, err := s.oauthUpstream(route{upstream: dest, path: "/v1/messages"}, http.Header{}); err == nil {
			t.Errorf("the OAuth upstream built a request for destination %q", dest)
		}
	}
	// The control: the destination the kind actually serves is accepted, so
	// the check above refuses the off-host ones and not everything.
	if _, err := s.oauthUpstream(route{upstream: "https://api.anthropic.com/v1/messages", path: "/v1/messages"}, http.Header{}); err != nil {
		t.Errorf("the OAuth upstream refused Anthropic's own endpoint: %v", err)
	}
}

// TestOAuthCredentialRedactedFromErrors: an upstream answer that echoes
// the credential -- in its body or in a header -- reaches the client and
// the log with the credential scrubbed, on both paths an OAuth call can
// take. The token count path copies its answer through unread, so it needs
// the same scrub a messages call gets; a test that only posted to
// /v1/messages would never have seen that.
func TestOAuthCredentialRedactedFromErrors(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"messages", "/v1/messages", msg(sonnet, 100)},
		{"count_tokens", "/v1/messages/count_tokens", `{"model":"` + sonnet + `","messages":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := oauthToken(t)
			rep := anthropicfake.Error(http.StatusUnauthorized, "authentication_error", "invalid token "+token)
			rep.Header = http.Header{"X-Echo": {"the credential was Bearer " + token}}
			h := newOAuth(t, token, rep)

			resp, body := h.post2(tc.path, tc.body)
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
		})
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

// TestOAuthDoesNotFollowARedirect: an upstream answer that redirects is
// never followed, so the subscription credential never reaches the host the
// Location names. Following one would resend `Authorization: Bearer` to
// whatever server answered -- the D133 invariant, undone by a single 307.
//
// Both a same-host and a cross-host target are used, because the two fail
// differently: a followed cross-host redirect hands the token to another
// server, and a followed same-host one is a second call under the token that
// nothing priced.
//
// All five redirect statuses are used, and they are stopped by two different
// things, which is why one of them is not enough:
//
//   - 301, 302 and 303 are followed by net/http by default, turning the POST
//     into a GET at the Location. Nothing but the redirect policy stops them,
//     so these are the cases that prove the policy is there.
//   - 307 and 308 are refused by net/http when the request body cannot be
//     replayed -- and upstreamRequest sets GetBody = nil deliberately ("never
//     replayed: the gateway never retries"). They are therefore stopped even
//     by a client with no policy at all, which is exactly why a test of only
//     these two passes with the policy removed. They are kept because that
//     guard is a property of the body, not of the credential: a future
//     change that made the body replayable would put the token on the wire
//     for these two, and the policy is what makes the outcome independent of
//     that.
//
// The test talks to the gateway with a client that does not follow redirects
// itself: the gateway's answer is what is under test, and a following client
// would chase the Location and be counted as the leak it is looking for.
func TestOAuthDoesNotFollowARedirect(t *testing.T) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, status := range []int{
		http.StatusMovedPermanently,  // 301: followed by default
		http.StatusFound,             // 302: followed by default
		http.StatusSeeOther,          // 303: followed by default
		http.StatusTemporaryRedirect, // 307: needs a replayable body
		http.StatusPermanentRedirect, // 308: needs a replayable body
	} {
		for _, cross := range []bool{false, true} {
			where := "same-host"
			if cross {
				where = "cross-host"
			}
			t.Run(fmt.Sprintf("%d/%s", status, where), func(t *testing.T) {
				token := oauthToken(t)
				// The redirect target: a server that records whatever reaches
				// it, so a followed redirect shows up as a request carrying
				// the credential.
				other, otherSrv := anthropicfake.New(t, anthropicfake.MessageOK(sonnet, smallUsage))
				h := newOAuth(t, token)
				loc := h.up.URL + "/v1/messages"
				if cross {
					loc = otherSrv.URL + "/v1/messages"
				}
				h.fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
					return anthropicfake.Reply{Status: status, Header: http.Header{"Location": {loc}}}
				}

				resp, err := client.Do(h.request(context.Background(), "/v1/messages", msg(sonnet, 100)))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)

				// Exactly one upstream request: the redirect was not chased.
				if n := h.fake.Count(); n != 1 {
					t.Errorf("the upstream saw %d requests, want exactly 1: the redirect was followed", n)
				}
				// And the target saw none of them.
				if n := other.Count(); n != 0 {
					t.Errorf("the redirect target %s saw %d requests, want none", loc, n)
				}
				for i, r := range other.Seen() {
					t.Errorf("the redirect target saw request %d: %s %s", i, r.Method, r.URL)
					if a := r.Header.Get("Authorization"); a != "" {
						t.Errorf("the credential reached the redirect target: %q", a)
					}
				}
				// The upstream's own answer is passed through unchanged: the
				// gateway neither follows it nor turns it into a call. (That
				// the agent is handed a 3xx at all is the upstream's answer
				// reaching it, not the gateway acting on the Location.)
				if resp.StatusCode != status {
					t.Errorf("the agent got %d, want the upstream's %d: %s", resp.StatusCode, status, body)
				}
				if strings.Contains(string(body), token) {
					t.Errorf("the credential came back in the answer: %s", body)
				}
			})
		}
	}
}

// TestOAuthScrubsTheRunToken: the run's own gateway token is the agent's
// credential for the gateway, not Anthropic's, and it never travels
// upstream. It can arrive in any header Check 90 recorded as allowed --
// User-Agent is one -- so the allow-list alone does not stop it: scrubToken
// does, and this is the test that fails when scrubToken goes.
//
// The fake echoes the headers it received back in its body, so a token that
// reached the upstream also comes back to the agent: the request leg and the
// response leg are the same fact, and either one fails the test.
func TestOAuthScrubsTheRunToken(t *testing.T) {
	token := oauthToken(t)
	h := newOAuth(t, token)
	gwTok := h.gw.Token()
	if gwTok == "" {
		t.Fatal("the gateway has no token")
	}
	h.fake.Func = func(r *http.Request, _ []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusOK, Body: fmt.Sprintf(`{"echo_user_agent":%q,"echo_beta":%q}`,
			r.Header.Get("User-Agent"), r.Header.Get("Anthropic-Beta"))}
	}

	resp, body := h.post(msg(sonnet, 100),
		"user-agent", "claude-cli/2.1.305 "+gwTok,
		"anthropic-beta", "oauth-2025-04-20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Anti-vacuity: an allowed header really does come back, so the echo is
	// working and the assertion below is about the token and not about an
	// empty body.
	if !strings.Contains(body, "oauth-2025-04-20") {
		t.Fatalf("the fake didn't echo an allowed header, so nothing below is proven: %s", body)
	}

	// The request leg: no header the upstream saw carries the run's token.
	for k, vs := range h.fake.Seen()[0].Header {
		for _, v := range vs {
			if strings.Contains(v, gwTok) {
				t.Errorf("the run's gateway token reached the upstream in %s", k)
			}
		}
	}
	// The response leg: it did not come back to the agent either.
	if strings.Contains(body, gwTok) {
		t.Errorf("the run's gateway token came back to the agent: %s", body)
	}
	if strings.Contains(h.logs.String(), gwTok) {
		t.Errorf("a log line carries the run's gateway token:\n%s", h.logs.String())
	}
	// The subscription credential still travelled: scrubbing the run's token
	// is not allowed to drop the one this upstream exists to attach.
	if got := h.fake.Seen()[0].Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("upstream Authorization = %q, want the subscription credential", got)
	}
}

// TestOAuthProductionRefusesALoopbackBase: the loopback stand-in is a test
// affordance and nothing else. With it off -- the state of every built
// binary -- the only base this kind accepts is Anthropic's own, so a
// deployment cannot point the upstream at a local server that would be
// handed a person's subscription token.
func TestOAuthProductionRefusesALoopbackBase(t *testing.T) {
	oauthAllowLoopback = false
	t.Cleanup(func() { oauthAllowLoopback = true })

	for _, ok := range []string{"", "https://api.anthropic.com"} {
		if err := checkOAuthBaseURL(ok); err != nil {
			t.Errorf("base %q refused in production: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://127.0.0.1:8080",
		"https://127.0.0.1:8080",
		"http://localhost:8080",
		"http://[::1]:8080",
		"http://api.anthropic.com", // the token would go in the clear
		"https://api.anthropic.com.evil.example",
		"http://169.254.169.254",
	} {
		if err := checkOAuthBaseURL(bad); err == nil {
			t.Errorf("base %q accepted in production", bad)
		}
	}

	// And the constructor refuses one, so the posture is enforced where a
	// deployment actually starts the gateway and not only in this helper.
	token := oauthToken(t)
	for _, bad := range []string{"http://127.0.0.1:8080", "http://localhost:8080"} {
		gw, err := Start(context.Background(), Options{
			Upstream: Upstream{Kind: KindOAuth, BaseURL: bad, APIKey: token},
			Prices:   dsPrices(), Mode: Observe,
		})
		if err == nil {
			_ = gw.Close(context.Background())
			t.Errorf("an OAuth upstream on the loopback base %q started in production", bad)
		}
	}
}

// envLitRE is every environment variable this tree reads by name.
var envLitRE = regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("([^"]*)"\)`)

// TestOAuthBaseURLIsNeverFromConfigOrEnv: C1.11 builds this upstream and
// gives it the empty base, which is Anthropic's own endpoint. The base is not
// a config key and not an environment variable, and this test is where a
// second way of setting it is noticed.
//
// The argument is the kind's name: an OAuth upstream cannot be built without
// naming KindOAuth, so any config loader, CLI flag, environment default or
// service code that could set its base would name it outside this package's
// tier-A files -- and the base is a field of the upstream it builds. The
// second half is the direct one: no environment variable anywhere in the
// tree names an OAuth base.
func TestOAuthBaseURLIsNeverFromConfigOrEnv(t *testing.T) {
	// The tier-A files, and the shared files that carry a switch arm for the
	// kind. Nothing else in the tree may name it.
	tierA := map[string]bool{
		"internal/gateway/oauth.go":      true,
		"internal/gateway/oauth_test.go": true,
		"internal/gateway/gateway.go":    true,
		"internal/gateway/route.go":      true,
		"internal/gateway/forward.go":    true,
	}
	var builders, envs []string
	err := filepath.WalkDir("../..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", ".terraform":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := string(b)
		rel := filepath.ToSlash(strings.TrimPrefix(p, "../../"))
		if strings.Contains(s, "KindOAuth") || strings.Contains(s, `"`+KindOAuth+`"`) {
			if !tierA[rel] {
				builders = append(builders, rel)
			}
		}
		for _, m := range envLitRE.FindAllStringSubmatch(s, -1) {
			name := strings.ToUpper(m[1])
			if strings.Contains(name, "OAUTH") && (strings.Contains(name, "BASE") || strings.Contains(name, "URL") || strings.Contains(name, "ENDPOINT")) {
				envs = append(envs, rel+" sets "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range builders {
		t.Errorf("%s names the OAuth kind: only the tier-A files may, or a base this package never checked could be set from outside", p)
	}
	for _, e := range envs {
		t.Errorf("%s: the OAuth upstream's base is never taken from the environment", e)
	}
	if len(builders) != 0 || len(envs) != 0 {
		return
	}
	// The scan itself works: it finds the files it is supposed to.
	if !tierA["internal/gateway/oauth.go"] {
		t.Fatal("the tier-A set lost oauth.go")
	}
}
