package gateway

// C1.8: the exported forwarding helpers the credentials service reuses.
//
// internal/credsvc is the credentials service (D143): it holds the run's real
// credentials, picks one per call, attaches it and forwards the call itself,
// because no job is given a model credential at all. What it must not do is
// write a second copy of the gateway's forwarding rules -- which headers a
// call carries, the one place a credential is written, the refusal that keeps
// a subscription token off a provider's endpoint (D133), the unbuffered pump
// that streams an answer back, and the redaction that keeps a credential out
// of what is handed on. Every function here is a thin wrapper over code the
// gateway already runs on its own forward path, so the two forwarders cannot
// drift: each one delegates, adds no rule of its own, and the headers they
// build are pinned equal by TestExportedHelpersMatchInternal.
//
// The service forwards three things, and each has the helper named for it: an
// API-key upstream (tier B) and a Vertex upstream through UpstreamHeaders, a
// subscription credential through OAuthHeaders, and a provider route through
// RouteHeaders.
//
// Nothing here changes the gateway's behaviour. This file adds no route, no
// listener, no flag and no log line, and the gateway's own ServeHTTP path does
// not call it. Where a helper stands without the Start-time checks a Server
// gets (Options.validate), it refuses rather than forwards: an upstream kind
// whose call these helpers don't describe, a Vertex upstream with no token
// source, and a tier-A call to a destination that isn't Anthropic's own all
// fail closed. No helper logs anything, and every error names the field or the
// kind and never a credential value (the package's no-echo rule).
//
// The subscription kind's own name is deliberately not written in this file.
// oauth_test.go's TestOAuthBaseURLIsNeverFromConfigOrEnv lists the files that
// may name it, and this is not one of them (C1.8 writes only this file and its
// test); the name is not needed here either, because its calls are built by
// OAuthHeaders, which delegates to the tier-A code in oauth.go and inherits
// the destination check that code carries. A tier-A upstream handed to
// UpstreamHeaders is refused, not forwarded.
//
// The service's own retry is the one thing the gateway does not do: the
// in-runner gateway never retries, and a forwarder built on these helpers
// retries only before the first byte is streamed (internal/credsvc).

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// RequestPin is the model rule a request is checked against, apart from the
// Stage the gateway's own path usually takes it from: a caller with no stage
// -- the credentials service, which has no repository's configuration and no
// stage of its own -- applies the SAME check with the model rule it does have,
// and nothing about the check is respelled for it.
type RequestPin struct {
	// Models are the model IDs a request may name, in the gateway's own sense
	// of "may name" (sameModel: the same ID, or two spellings the price table
	// resolves to one model). The gateway's own path passes the stage's two
	// pins, Model and Background.
	//
	// An EMPTY list is no model rule: the check then applies everything else
	// (the shape, the server tools, the model's own max_tokens) and pins
	// nothing. That is what a caller with no list of its own means, and it is
	// deliberately not the same as a list that names nothing: a policy that
	// publishes no allow-list sets no model ceiling (H1), while a list with no
	// model in it could never admit a call. The credentials service, which is
	// the caller that reads that policy, refuses such a call before it builds a
	// pin at all (internal/credsvc, the owner's ruling of 2026-10-22); this
	// reading is unchanged, because the gateway's own path pins a call to the
	// stage's models and never to the project's.
	Models []string
	// Where names the list in the refusal, after "is not pinned for": the
	// gateway's own path passes "stage " + Stage.Name, so its sentence is
	// unchanged, and the credentials service names the project's allow-list
	// instead. It is read only when Models is not empty.
	Where string
	// Model is the one of Models the caller's own max_tokens bound applies to
	// (the stage's own model: a stage's limit does not hold the background
	// model's calls). "" is none.
	Model string
	// MaxOutputTokens bounds max_tokens for Model's calls only; 0 is no bound.
	MaxOutputTokens int64
}

// RequestRefusal reads a Messages request body the way the gateway's own path
// reads it and returns the refusal the gateway would give it: the model pin,
// the variant-suffix refusal, the shape refusal (a server tool among them), the
// foreign-field refusal and the max_tokens bounds (checkRequest, forward.go).
// "" means the gateway would accept the request; a non-nil error is a body the
// gateway cannot read at all, which its own path answers as a 400 too.
//
// It is the gateway's own check and not a copy of it: Server.handleMessages
// applies the same function through Server.check, so the two cannot drift --
// which is the whole point of exporting it. The credentials service calls it
// before it reads a credential (internal/credsvc), so a request the gateway
// would refuse never reaches one.
//
// prices is the price table the check reads a model's own bounds from (its
// maximum max_tokens, whether its images are priced); nil is the embedded
// table, and a model the table does not know is judged at the table's maximum,
// exactly as the gateway's own path judges it. routes are the provider routes
// the variant and foreign-field rules are about; a caller with none passes nil,
// and those two rules then cannot fire -- they are about what a *route* would
// do with the call, and a caller that routes nothing has no such call.
//
// pathModel is the model a Vertex-style path names, "" when the body carries
// it (parseRequest).
func RequestRefusal(body []byte, pathModel string, pin RequestPin, prices *pricing.Table, routes []Route) (string, error) {
	p, err := parseRequest(body, pathModel)
	if err != nil {
		return "", err
	}
	m, _ := modelFor(prices, p.model)
	return checkRequest(p, m, pin, prices, routes), nil
}

// ValidateRoutes reports whether routes may travel with u at all: a route is a
// provider's endpoint, so it needs the anthropic upstream and never a
// subscription credential (validateRoutes, route.go). It is the check the
// gateway makes through Options.validate at Start; the credentials service
// builds an upstream per credential and has no Start, so it runs it here.
func ValidateRoutes(u Upstream, routes []Route) error { return validateRoutes(u, routes) }

// AttachCredential attaches r's credential to h and returns it -- the value
// the caller must then keep out of everything it hands on. It refuses a
// subscription credential on any route (attachCredential, route.go): a route
// is a provider's endpoint, and such a token is only ever sent to Anthropic's
// own (D133).
func AttachCredential(r *Route, h http.Header) (string, error) { return r.attachCredential(h) }

// AnthropicBase is where u's own endpoint is: u.BaseURL, or Anthropic's real
// address when it is "" (anthropicBase, gateway.go). A caller building the URL
// of a call has no other way to learn it, and inventing one would be inventing
// the endpoint a subscription credential is sent to.
func AnthropicBase(u Upstream) string { return anthropicBase(u) }

// UpstreamHeaders builds the headers of a call to u's own endpoint, with u's
// credential attached, and returns that credential. It is what
// Server.upstreamRequest builds for a call no route claims, for the two kinds
// that carry an API key or a GCP token:
//
//   - anthropic: the client's headers less its credentials and hop-by-hop
//     headers, the caller's own token scrubbed, then x-api-key.
//   - vertex: the client's headers as above and the run's access token.
//
// Any other kind is refused: a subscription credential's call is built by
// OAuthHeaders (where the destination rule lives) and a provider's by
// RouteHeaders, so a call can never leave with a header set no rule was
// written for.
//
// There is no destination argument: what these headers carry does not depend on
// where the call goes, and a destination this helper took and did not judge
// read as one it had judged. The destination is the caller's, and it is the one
// the gateway's own handler builds -- the request's path, held to the endpoints
// the upstream serves, joined to AnthropicBase(u) for the anthropic kind
// (gateway.go's ServeHTTP). A caller that named a different host would be
// building the call itself, not reusing this one; the one credential whose
// destination is checked is tier A's, in OAuthHeaders.
//
// scrub is the caller's own token -- the one it presented on the call it is
// forwarding -- which is never sent upstream (the gateway scrubs the run's
// token; the service scrubs the run identity it verified). "" scrubs
// nothing: carriesToken's rule is that a Server with no token carries no
// token anywhere, so an empty scrub leaves the header set whole. Reading ""
// as a token would be the opposite -- every value contains it -- and the
// tier-A path below would go out with nothing but Authorization.
func UpstreamHeaders(u Upstream, in http.Header, scrub string) (http.Header, string, error) {
	h := forwardHeaders(in)
	scrubOwn(scrub, h)
	switch u.Kind {
	case "anthropic":
		if u.APIKey == "" {
			// The same refusal the gateway's own path gives: x-api-key: ""
			// is an unauthenticated call to Anthropic under this run's
			// headers (missingAnthropicKey, route.go).
			return nil, "", errors.New(config.MissingAnthropicKey)
		}
		h.Set("x-api-key", u.APIKey)
		return h, u.APIKey, nil
	case "vertex":
		if u.Token == nil {
			// Options.validate refuses this at Start; a helper that would
			// dereference it refuses instead.
			return nil, "", errors.New("the Vertex access token isn't available")
		}
		tok, err := u.Token.Token()
		if err != nil || tok == nil || tok.AccessToken == "" {
			return nil, "", errors.New("the Vertex access token isn't available")
		}
		tok.SetAuthHeader(&http.Request{Header: h})
		return h, tok.AccessToken, nil
	}
	return nil, "", fmt.Errorf("gateway: upstream kind %q takes neither an x-api-key nor a Vertex token", u.Kind)
}

// OAuthHeaders builds the headers of a tier-A call -- a subscription
// credential sent to Anthropic's own endpoint -- and returns that credential,
// which the caller must keep out of everything it hands on. It is the
// gateway's own tier-A path (oauth.go): Check 90's recorded header allow-list
// and nothing else, identity encoding, the caller's own token scrubbed, the
// credential as the bearer token Check 90 recorded, and checkOAuthDest's
// refusal of any destination that is not the upstream's own Anthropic
// endpoint. That last check is why this path is the only one a subscription
// credential may take: whatever upstream the caller built, the token can only
// ever reach Anthropic's own host (D133).
//
// u carries the credential in APIKey; BaseURL is Anthropic's real address when
// it is "", and a test's stand-in otherwise (the only other base the check
// accepts, and only while the package's tests have it on).
//
// scrub is the caller's own token, as in UpstreamHeaders, and "" scrubs
// nothing for the same reason: the allow-list comes through whole, with
// Anthropic-Version -- the one header Check 90 proved mandatory -- in it.
func OAuthHeaders(u Upstream, dest string, in http.Header, scrub string) (http.Header, string, error) {
	s := &Server{o: Options{Upstream: u}, token: scrub}
	h, err := s.oauthUpstream(route{path: destPath(dest), upstream: dest}, in)
	if err != nil {
		return nil, "", err
	}
	return h, u.APIKey, nil
}

// RouteHeaders builds the headers of a routed call -- one a provider claims --
// with r's credential attached, and returns that credential. It is what
// Server.upstreamRequest builds for a routed call: only the headers a provider
// needs, the caller's own token scrubbed, then AttachCredential (which refuses
// a subscription credential on a route, D133). The destination is the route's
// own base, which the caller joins the request's path and query to; nothing
// here checks it, because a route's base is a provider's endpoint by
// definition (validateRoutes).
func RouteHeaders(r *Route, in http.Header, scrub string) (http.Header, string, error) {
	h := providerHeaders(in)
	scrubOwn(scrub, h)
	key, err := AttachCredential(r, h)
	if err != nil {
		return nil, "", err
	}
	return h, key, nil
}

// IsSSE reports whether an upstream answer is an event stream, by its media
// type, falling back to the caller's own request when the type says nothing
// (isSSE, forward.go).
func IsSSE(h http.Header, requested bool) bool { return isSSE(h, requested) }

// Pump copies src to w, flushing after every write, and hands each chunk to
// tee after it was written; it reports whether src ended rather than failing,
// and it stops when the client goes away (pump, forward.go). It never buffers:
// the credentials service streams an answer exactly as the gateway does.
func Pump(w io.Writer, rc *http.ResponseController, src io.Reader, tee func([]byte)) (eof bool) {
	return pump(w, rc, src, tee)
}

// RedactHeaders drops every header value that carries key (redactHeaders,
// route.go), and NewRedactWriter returns a writer that replaces every
// occurrence of key in what is written through it, also when a chunk boundary
// splits one. They are the gateway's no-echo rule over an answer, for a
// forwarder that must hold a credential out of the headers and the body it
// hands on: the gateway's own forward redacts a provider's key and a tier-A
// credential this way.
func RedactHeaders(h http.Header, key string) { redactHeaders(h, key) }

// NewRedactWriter is the writer half of the rule above; Close writes the tail
// it held back because it was not the key after all.
func NewRedactWriter(w io.Writer, key string) io.WriteCloser { return newRedactWriter(w, key) }

// scrubOwn scrubs the caller's own token out of a header set the way the
// gateway scrubs the run's token from a forwarded call (Server.scrubToken).
// An empty token scrubs nothing -- the rule is carriesToken's ("a Server
// with no token carries no token anywhere"), which is also what the tier-A
// path below relies on.
func scrubOwn(token string, h http.Header) {
	(&Server{token: token}).scrubToken(h)
}

// destPath is the path of a call's destination, "" when it has none. The
// gateway's own route keeps a call's path and its destination together
// (gateway.go); a helper is handed only the destination, and the tier-A
// destination check reads the path as "this is a call to a path on the
// upstream, not a bare host" before a subscription credential is written
// anywhere.
func destPath(dest string) string {
	u, err := url.Parse(dest)
	if err != nil {
		return ""
	}
	return u.Path
}
