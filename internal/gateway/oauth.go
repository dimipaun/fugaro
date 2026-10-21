package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// TIER A (D132). Everything in this file exists for one thing: a
// subscription OAuth token sent to Anthropic's own endpoint on a run's
// behalf, and nothing else. It runs in the credentials service (C1.11),
// never in a job. Deleting tier A means deleting this file and the case
// that names KindOAuth; no shared code is unpicked.
//
// The header list below is Check 90's, run by the owner on 2026-10-21 with
// the owner's own `claude setup-token` token against a throwaway loopback
// proxy that swapped the incoming credential for the real one:
//
//	FACT swap-credential-header: the credential is `authorization` (bearer)
//	FACT header-drop-anthropic-version: FAIL -- the call failed when it was
//	     dropped, so it MUST pass through (the one mandatory header)
//	FACT header-drop-accept / -anthropic-beta / -content-type / -user-agent
//	     / -x-app / -x-claude-code-session-id: PASS (optional) -- each may be
//	     forwarded or dropped
//	FACT headers-observed: accept anthropic-beta
//	     anthropic-dangerous-direct-browser-access anthropic-version
//	     content-type user-agent x-app x-claude-code-session-id
//	     x-stainless-arch x-stainless-lang x-stainless-os
//	     x-stainless-package-version x-stainless-retry-count
//	     x-stainless-runtime x-stainless-runtime-version x-stainless-timeout
//	FACT header-drop-skipped: more than eight optional headers were seen, so
//	     the x-stainless-* ones were never dropped in the check: nothing here
//	     drops a header Check 90 did not prove droppable, and nothing else
//	     travels at all.
//	FACT usage-limit: none was observed and none was provoked, so U3 stays
//	     open and the plan's 15-minute default back-off applies (C1.9).
//	     Nothing in this file reads a usage-limit or reset header: an
//	     exhausted credential is the cascade's business, in the service.
//	FACT timeout-retry: the client waited 93 s for an answer delayed by 90 s,
//	     in one attempt, and succeeded -- the bound on the read timeout below.
//	FACT control / swap / token-validity / restore: the swap is what made the
//	     call work, the token was accepted by a live call, and no value was
//	     ever printed.
//
// No header value and no token is ever logged: the gateway's log lines hold
// no header at all (log.go), the credential is attached here and nowhere
// else, and every byte of a response that could echo it is scrubbed
// (redactWriter, forward).

// KindOAuth is the tier-A upstream kind: Anthropic's Messages API with a
// subscription OAuth token instead of an API key. The token is the
// Upstream's credential field (APIKey, the one credential an upstream
// has), sent as the bearer token Check 90 recorded.
const KindOAuth = "anthropic-oauth"

// authOAuth is the Route.Auth spelling that would ask a route to carry an
// OAuth credential. No route may: a route is a provider's endpoint by
// definition (route.go), and an OAuth token is only ever sent to
// Anthropic's own.
const authOAuth = "oauth"

// errOAuthOnRoute is the refusal both validateRoutes and attachCredential
// give an OAuth credential offered to a route (D133). It is checked in
// both places: at start-up, and again where the credential would be
// written, so a Route built in code cannot carry the token either.
var errOAuthOnRoute = errors.New("an OAuth credential is never sent to a provider route: an OAuth token is only ever sent to Anthropic's own endpoint")

// oauthRequestHeaders are the client headers an OAuth call carries
// upstream: exactly the names Check 90 observed, and nothing else. An
// allow-list rather than the gateway's usual "everything but the
// credentials": the credential in this call is a person's subscription, so
// a header the client invented never reaches Anthropic beside it.
//
// Anthropic-Version is the one Check 90 proved mandatory (dropping it
// failed the call); every other name here was observed and, where the
// check dropped it, was droppable. A header not in this list -- the
// client's own credentials, its session and agent IDs, cookies, SDK
// telemetry the check never saw -- is dropped.
var oauthRequestHeaders = []string{
	"Anthropic-Version",
	"Accept",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Content-Type",
	"User-Agent",
	"X-App",
	"X-Claude-Code-Session-Id",
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
}

// oauthResponseHeaderTimeout bounds how long an OAuth call waits for the
// upstream's response headers. Check 90 measured the client waiting 93 s
// for an answer delayed by 90 s, in one attempt, and succeeding: the bound
// is well above that, so a delayed answer the client is still waiting for
// is never cut here, while a connection that never answers is. It covers
// the wait for the response headers only, never the body: the gateway
// never buffers a stream, and a streamed answer sends its headers at once.
// The cost of the bound is a non-streaming call whose first byte takes
// longer than it, which is cut; Claude Code streams.
//
// A var, like endStageWait, so a test can shorten it.
var oauthResponseHeaderTimeout = 10 * time.Minute

// oauthUpstreamClient is the client an OAuth upstream uses: the caller's,
// with the response-header bound set on its transport. A client whose
// transport is not the standard one keeps it as it is: the bound is not
// ours to set on a RoundTripper we don't know.
func oauthUpstreamClient(c *http.Client) *http.Client {
	out := *c
	tr := c.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	if t, ok := tr.(*http.Transport); ok {
		nt := t.Clone()
		nt.ResponseHeaderTimeout = oauthResponseHeaderTimeout
		out.Transport = nt
	}
	return &out
}

// checkOAuthBaseURL: an OAuth credential goes to Anthropic's own endpoint
// and nowhere else (D133). "" is https://api.anthropic.com, which is what
// a deployment uses; a loopback base is the test stand-in, where no
// credential leaves the machine. Any other host is refused, so an OAuth
// upstream cannot be pointed at somebody else's server.
func checkOAuthBaseURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("gateway: the OAuth upstream's base %q isn't a host", s)
	}
	if u.Scheme == "https" && u.Hostname() == "api.anthropic.com" {
		return nil
	}
	if u.Hostname() == "localhost" || isLoopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("gateway: the OAuth upstream's base %q isn't Anthropic's own endpoint: an OAuth token is only ever sent there (D133)", s)
}

// oauthUpstream is an OAuth call's header set and credential: the
// allow-list above, identity encoding, the run's own token scrubbed, and
// the subscription token as `Authorization: Bearer`. The credential is
// attached here and in no other place in the gateway.
//
// The invariant (D133) is checked here rather than trusted: rt is the
// Anthropic route the kind serves -- rt.path is the /v1/messages path
// anthropicBase was joined with, and rt.provider is nil because a route
// with a provider base URL is never an OAuth target.
func (s *Server) oauthUpstream(rt route, in http.Header) (http.Header, error) {
	if rt.provider != nil {
		return nil, errOAuthOnRoute
	}
	if rt.path == "" {
		return nil, errors.New("an OAuth call is only ever sent to Anthropic's own endpoint")
	}
	h := http.Header{}
	for _, k := range oauthRequestHeaders {
		if vs := in.Values(k); len(vs) > 0 {
			h[k] = append([]string(nil), vs...)
		}
	}
	h.Set("Accept-Encoding", "identity")
	s.scrubToken(h)
	if err := attachOAuth(h, s.o.Upstream.APIKey); err != nil {
		return nil, err
	}
	return h, nil
}

// attachOAuth sets the subscription credential: the header Check 90
// recorded (swap-credential-header: authorization), as a bearer token, and
// nothing else. An empty credential is refused rather than sent: an
// unauthenticated call to Anthropic under this run's headers is not a call
// this gateway makes.
func attachOAuth(h http.Header, token string) error {
	if token == "" {
		return errors.New("the OAuth credential isn't available")
	}
	h.Set("Authorization", "Bearer "+token)
	return nil
}
