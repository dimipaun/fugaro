package gateway

// C1.8's tests: the exported forwarding helpers the credentials service
// reuses must be the gateway's own forward path, not a second copy of it.
//
// Both tests are written to fail if a wrapper drifts: the first if the tier-A
// invariant (D133) is not enforced where the credential would be written, the
// second if any header an exported helper builds differs from the one
// Server.upstreamRequest builds for the same call -- so a change to the
// gateway's header rules that the helpers do not follow is a red test, not a
// silent divergence between two forwarders.
//
// Every credential here is built at run time (Convention 2); the ones the
// other tests in this package already declare (testKey, vertexAccessToken)
// are reused rather than restated.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// oauthRoute is a route shaped the way validateRoutes accepts one, with its
// Auth left to the caller: the OAuth spelling is the case under test. It
// counts how often its credential source is called: a refused attach must
// refuse before the value exists.
func oauthRoute(t *testing.T, auth string) (*Route, *int) {
	t.Helper()
	calls := 0
	return &Route{
		Name:    "openrouter",
		Models:  []string{"deepseek/*"},
		BaseURL: "https://openrouter.ai/api",
		Auth:    auth,
		Credential: func() (string, error) {
			calls++
			return oauthToken(t), nil
		},
	}, &calls
}

// TestExportedAttachRefusesOAuthOnProviderRoute: an OAuth credential is never
// attached to a provider route, and the exported helpers refuse it in every
// shape the credentials service can reach them with -- the attach itself, the
// header builder over it (RouteHeaders), and the validation the service runs
// before it forwards anything (ValidateRoutes). The credential source is
// never called: the refusal comes before the value exists, so there is
// nothing to send and nothing to leak.
func TestExportedAttachRefusesOAuthOnProviderRoute(t *testing.T) {
	r, calls := oauthRoute(t, authOAuth)

	h := http.Header{}
	key, err := AttachCredential(r, h)
	if err == nil {
		t.Fatalf("an OAuth credential was attached to a provider route: %q", h.Get("Authorization"))
	}
	if key != "" || h.Get("Authorization") != "" || h.Get("x-api-key") != "" {
		t.Fatal("a refused attach left a credential on the headers")
	}
	if *calls != 0 {
		t.Fatalf("the credential source was called %d times for a refused route", *calls)
	}

	if _, _, err := RouteHeaders(r, http.Header{}, oauthToken(t)); err == nil {
		t.Error("RouteHeaders attached an OAuth credential to a provider route")
	}
	if *calls != 0 {
		t.Fatalf("the credential source was called %d times for a refused route", *calls)
	}

	// The same route is refused by the check the service runs before it
	// forwards anything, under either upstream it could hold.
	for _, u := range []Upstream{{Kind: "anthropic"}, tierAUpstream(t)} {
		if err := ValidateRoutes(u, []Route{*r}); err == nil {
			t.Errorf("ValidateRoutes accepted an OAuth route under the %q upstream", u.Kind)
		}
	}
	// A route is a provider's endpoint, so it travels only with the anthropic
	// upstream: the credentials service builds its upstream per credential
	// and must hear this refusal before it forwards a call.
	ok, _ := oauthRoute(t, "bearer")
	if err := ValidateRoutes(Upstream{Kind: "vertex"}, []Route{*ok}); err == nil {
		t.Error("ValidateRoutes accepted routes under the vertex upstream")
	}
	if err := ValidateRoutes(Upstream{Kind: "anthropic"}, []Route{*ok}); err != nil {
		t.Errorf("ValidateRoutes refused a bearer route under the anthropic upstream: %v", err)
	}
}

// TestExportedHelpersMatchInternal: for every call the credentials service
// forwards -- the Anthropic upstream with an API key (tier B), the OAuth
// upstream (tier A), Vertex, and a provider route (tier C) -- the headers the
// exported helper builds are the headers Server.upstreamRequest builds, and
// the refusals agree. The service forwards the run's calls; if these two ever
// disagree, one of them is sending something the other would not.
func TestExportedHelpersMatchInternal(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		h := newHarness(t)
		in := forwardedHeaders(t)
		dest := AnthropicBase(h.gw.o.Upstream) + "/v1/messages"
		rt := route{path: "/v1/messages", upstream: dest}
		up, _, err := h.gw.upstreamRequest(context.Background(), &http.Request{Header: in}, rt, nil)
		if err != nil {
			t.Fatalf("upstreamRequest: %v", err)
		}
		got, key, err := UpstreamHeaders(h.gw.o.Upstream, rt.upstream, in, h.gw.Token())
		if err != nil {
			t.Fatalf("UpstreamHeaders: %v", err)
		}
		if !sameHeaders(t, got, up.Header) {
			t.Error("the exported helper's headers are not the gateway's")
		}
		if key != h.gw.o.Upstream.APIKey || key != keyFrom(up.Header) {
			t.Error("the exported helper did not return the credential it attached")
		}
	})

	t.Run("vertex", func(t *testing.T) {
		h := newVertexHarness(t)
		in := forwardedHeaders(t)
		rt := route{pathModel: sonnet, upstream: h.up.URL + vertexPath("proj-1234", "us-east5", sonnet, "streamRawPredict")}
		up, _, err := h.gw.upstreamRequest(context.Background(), &http.Request{Header: in}, rt, nil)
		if err != nil {
			t.Fatalf("upstreamRequest: %v", err)
		}
		got, key, err := UpstreamHeaders(h.gw.o.Upstream, rt.upstream, in, h.gw.Token())
		if err != nil {
			t.Fatalf("UpstreamHeaders: %v", err)
		}
		if !sameHeaders(t, got, up.Header) {
			t.Error("the exported helper's headers are not the gateway's")
		}
		if key != vertexAccessToken {
			t.Error("the exported helper did not return the credential it attached")
		}
	})

	t.Run("oauth", func(t *testing.T) {
		token := oauthToken(t)
		h := newOAuth(t, token)
		in := forwardedHeaders(t)
		u := h.gw.o.Upstream
		rt := route{path: "/v1/messages", upstream: AnthropicBase(u) + "/v1/messages"}
		up, _, err := h.gw.upstreamRequest(context.Background(), &http.Request{Header: in}, rt, nil)
		if err != nil {
			t.Fatalf("upstreamRequest: %v", err)
		}
		got, key, err := OAuthHeaders(u, rt.upstream, in, h.gw.Token())
		if err != nil {
			t.Fatalf("UpstreamHeaders: %v", err)
		}
		if !sameHeaders(t, got, up.Header) {
			t.Error("the exported helper's headers are not the gateway's")
		}
		if key != token || key != keyFrom(up.Header) {
			t.Error("the exported helper did not return the credential it attached")
		}
		if got.Get("Accept-Encoding") != "identity" {
			t.Error("an OAuth call did not ask for an unencoded answer")
		}
	})

	t.Run("route", func(t *testing.T) {
		key := oauthToken(t) // a fake provider key, built at run time
		r := newRoutedCred(t, "bearer", func() (string, error) { return key, nil }, routedStage)
		in := forwardedHeaders(t)
		pr := &r.gw.o.Routes[0]
		rt := route{path: "/v1/messages", upstream: trimSlash(pr.BaseURL) + "/v1/messages", provider: pr}
		up, _, err := r.gw.upstreamRequest(context.Background(), &http.Request{Header: in}, rt, nil)
		if err != nil {
			t.Fatalf("upstreamRequest: %v", err)
		}
		got, gotKey, err := RouteHeaders(pr, in, r.gw.Token())
		if err != nil {
			t.Fatalf("RouteHeaders: %v", err)
		}
		if !sameHeaders(t, got, up.Header) {
			t.Error("the exported helper's headers are not the gateway's")
		}
		if gotKey != key || gotKey != keyFrom(up.Header) {
			t.Error("the exported helper did not return the credential it attached")
		}
	})

	t.Run("refusals", func(t *testing.T) {
		// A keyless Anthropic upstream is refused by name, not sent with
		// x-api-key: "" -- the same error the gateway's own path gives.
		_, _, err := UpstreamHeaders(Upstream{Kind: "anthropic"}, "https://api.anthropic.com/v1/messages", http.Header{}, "")
		if err == nil || !strings.Contains(err.Error(), config.MissingAnthropicKey) {
			t.Errorf("a keyless Anthropic upstream: err = %v", err)
		}
		// An upstream kind the gateway doesn't know is refused rather than
		// forwarded without a credential (Start refuses it through
		// Options.validate; a helper has no Start).
		if _, _, err := UpstreamHeaders(Upstream{Kind: "openai"}, "https://api.openai.com/v1/messages", http.Header{}, ""); err == nil {
			t.Error("an unknown upstream kind was forwarded")
		}
		// A Vertex upstream with no token source is refused, not panicked on.
		if _, _, err := UpstreamHeaders(Upstream{Kind: "vertex", VertexProject: "proj-1234"}, "https://aiplatform.googleapis.com/x", http.Header{}, ""); err == nil {
			t.Error("a vertex upstream with no token source was forwarded")
		}
		// A subscription upstream is refused by UpstreamHeaders rather than
		// forwarded with an x-api-key, and its calls are built by
		// OAuthHeaders: a caller reaches the tier-A path by asking for it.
		u := tierAUpstream(t)
		if _, _, err := UpstreamHeaders(u, AnthropicBase(u)+"/v1/messages", http.Header{}, oauthToken(t)); err == nil {
			t.Error("a subscription upstream's call was built as an API-key call")
		}
		// Tier A goes to the upstream's own Anthropic endpoint and nowhere
		// else, checked on the destination rather than on the route (D133):
		// a provider's host, Anthropic's real address when the upstream is
		// pointed somewhere else, a host that merely starts with Anthropic's
		// name, and a call with no path at all are each refused.
		for _, dest := range []string{
			"https://openrouter.ai/api/v1/messages",
			"https://api.anthropic.com/v1/messages",
			"https://api.anthropic.com.evil.example/v1/messages",
			"https://api.anthropic.com",
			"",
		} {
			if _, _, err := OAuthHeaders(u, dest, http.Header{}, ""); err == nil {
				t.Errorf("a subscription credential was sent to %q", dest)
			}
		}
		// The upstream the caller built is where it may go -- and the
		// credential comes back so the caller can redact the answer.
		got, key, err := OAuthHeaders(u, AnthropicBase(u)+"/v1/messages", http.Header{"X-App": {"cli"}}, oauthToken(t))
		if err != nil {
			t.Fatalf("the tier-A upstream's own endpoint was refused: %v", err)
		}
		if key != u.APIKey || got.Get("Authorization") != "Bearer "+u.APIKey {
			t.Error("the tier-A credential was not returned as it was attached")
		}
	})

	t.Run("scrub", func(t *testing.T) {
		// The caller's own token is scrubbed out of whatever it forwards (the
		// gateway scrubs the run's token), and an empty one scrubs nothing --
		// not everything, which is what scrubToken's "the value contains the
		// token" rule would do with "".
		own := oauthToken(t) + "-gateway"
		in := http.Header{"X-Claude-Code-Session-Id": {own}, "Anthropic-Version": {"2023-06-01"}}
		u := Upstream{Kind: "anthropic", APIKey: testKey}
		got, _, err := UpstreamHeaders(u, "https://api.anthropic.com/v1/messages", in, own)
		if err != nil {
			t.Fatalf("UpstreamHeaders: %v", err)
		}
		if got.Get("X-Claude-Code-Session-Id") != "" {
			t.Error("the caller's own token reached the upstream")
		}
		if got.Get("Anthropic-Version") == "" {
			t.Error("a scrub deleted a header that never carried the token")
		}
		none, _, err := UpstreamHeaders(u, "https://api.anthropic.com/v1/messages", in, "")
		if err != nil {
			t.Fatalf("UpstreamHeaders: %v", err)
		}
		if len(none) == 0 || none.Get("Anthropic-Version") == "" {
			t.Error("an empty scrub deleted the whole header set")
		}
		pr := &Route{Name: "openrouter", Models: []string{dsModel}, BaseURL: "https://openrouter.ai/api",
			Auth: "bearer", Credential: func() (string, error) { return testKey, nil }}
		rh, _, err := RouteHeaders(pr, in, own)
		if err != nil {
			t.Fatalf("RouteHeaders: %v", err)
		}
		if rh.Get("X-Claude-Code-Session-Id") != "" {
			t.Error("the caller's own token reached a provider")
		}
	})

	t.Run("stream", func(t *testing.T) {
		// IsSSE and Pump are the gateway's: the same media-type rule and the
		// same flush-after-every-write loop the credentials service streams
		// with, so an answer that never buffers in the gateway never buffers
		// in the service either.
		for _, c := range []struct {
			ct        string
			requested bool
			want      bool
		}{
			{"text/event-stream", false, true},
			{"text/event-stream; charset=utf-8", false, true},
			{"application/json", true, false},
			{"text/plain", true, true},
			{"text/plain", false, false},
			{"", true, true},
		} {
			h := http.Header{}
			if c.ct != "" {
				h.Set("Content-Type", c.ct)
			}
			if got := IsSSE(h, c.requested); got != c.want {
				t.Errorf("IsSSE(%q, %v) = %v, want %v", c.ct, c.requested, got, c.want)
			}
		}
		var out strings.Builder
		rec := &flushRecorder{ResponseWriter: httptest.NewRecorder()}
		var tee []string
		eof := Pump(&out, http.NewResponseController(rec), strings.NewReader("one\ntwo\n"), func(b []byte) { tee = append(tee, string(b)) })
		if !eof || out.String() != "one\ntwo\n" || len(tee) == 0 {
			t.Fatalf("Pump: eof=%v out=%q tee=%v", eof, out.String(), tee)
		}
		if rec.flushes == 0 {
			t.Error("Pump wrote an unflushed stream")
		}
	})

	t.Run("redaction", func(t *testing.T) {
		// The no-echo rule over an answer: a credential the caller attached
		// never travels back out through these, in a header or in a body --
		// not even when a chunk boundary splits it.
		key := oauthToken(t)
		h := http.Header{"X-Request-Id": {"seen " + key}, "Anthropic-Request-Id": {"clean"}}
		RedactHeaders(h, key)
		if h.Get("X-Request-Id") != "" || h.Get("Anthropic-Request-Id") != "clean" {
			t.Fatalf("RedactHeaders left %v", h)
		}
		var out strings.Builder
		w := NewRedactWriter(&out, key)
		if _, err := w.Write([]byte("prefix " + key[:8])); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(key[8:] + " suffix")); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), key) {
			t.Fatalf("the credential survived the redacting writer: %q", out.String())
		}
		if !strings.Contains(out.String(), redacted) {
			t.Fatalf("the credential was not replaced: %q", out.String())
		}
	})
}

// tierAUpstream is the tier-A upstream the package's own harness builds, read
// back from a started gateway. This file does not name the kind: the guard in
// oauth_test.go lists the files that may, and the upstream a deployment gives
// the service is built where the kind already is (C1.11).
func tierAUpstream(t *testing.T) Upstream {
	t.Helper()
	return newOAuth(t, oauthToken(t)).gw.o.Upstream
}

// forwardedHeaders is a client header set with what a forward must drop and
// what it must keep: credentials, a session ID, a billing project and the
// version header the API needs.
func forwardedHeaders(t *testing.T) http.Header {
	t.Helper()
	return http.Header{
		"X-Api-Key":                {testKey},
		"Authorization":            {"Bearer " + oauthToken(t)},
		"Content-Type":             {"application/json"},
		"Anthropic-Version":        {"2023-06-01"},
		"X-Claude-Code-Session-Id": {"sess-export-1"},
		"X-Goog-User-Project":      {"aurora-gcp-1"},
		"Accept-Encoding":          {"gzip"},
	}
}

// sameHeaders compares two header sets, values included: a header the helper
// added, dropped or duplicated is a failure, and so is a differing value.
func sameHeaders(t *testing.T, got, want http.Header) bool {
	t.Helper()
	same := len(got) == len(want)
	if !same {
		t.Errorf("headers %v, want %v", got, want)
	}
	for k, gv := range got {
		wv := want.Values(k)
		if len(gv) != len(wv) {
			t.Errorf("%s = %v, want %v", k, gv, wv)
			same = false
			continue
		}
		for i := range gv {
			if gv[i] != wv[i] {
				t.Errorf("%s = %v, want %v", k, gv, wv)
				same = false
				break
			}
		}
	}
	return same
}

// flushRecorder is a ResponseWriter with a Flusher, so Pump's flush is
// exercised rather than skipped: an unflushable writer would make the test
// pass for the wrong reason.
type flushRecorder struct {
	http.ResponseWriter
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++; f.ResponseWriter.(http.Flusher).Flush() }
