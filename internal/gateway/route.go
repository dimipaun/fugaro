package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// Route sends the models it claims to a non-Anthropic endpoint that
// accepts Anthropic Messages requests (design m10-multi-model.md §3, §6).
// Claude models are never routed unless a route claims that exact ID (Phase 2
// of the mixed-credentials opt-in: one explicit "anthropic/<id>", never a
// pattern); everything else under the prefix goes to Options.Upstream, so
// Claude's prompt caching and the Anthropic key are untouched.
type Route struct {
	// Name is the provider's name, for the log (never a credential).
	Name string
	// Models are the model IDs the route serves: exact IDs, or a prefix
	// ending in "*" such as "deepseek/*". A request's model must also be
	// pinned by the stage, and is matched exactly (no alias, no variant
	// suffix such as ":online").
	Models []string
	// BaseURL is https, or http on a loopback host (tests). Unlike the
	// Anthropic upstream's it may carry a path prefix ("https://host/api").
	BaseURL string
	// Auth is how Credential is sent: "bearer" (Authorization) or
	// "x-api-key".
	Auth string
	// Served, when set, is R12's fact about this route (KS5): the
	// installation's credentials service holds this repository's key for the
	// provider, so the call goes to that service's provider route with the
	// provider's own name on it (HeaderProvider) and Credential is nil — the
	// job mounts no key for this provider at all (R1, R12). A served route is
	// admitted beside the service upstream alone (validateRoutes): the key it
	// is reached with is the service's, and there is nowhere else to get one.
	//
	// What the route still carries is everything else: its name, its models,
	// its fee and the address the service forwards to (BaseURL, the config's
	// own — a served call sends no address of its own, F45).
	Served bool
	// Credential is the provider key, read per call. It comes from the
	// runner (never from the agent's environment) and is attached to the
	// upstream request only; an error means the call is not sent. It is
	// called from concurrent calls, so it must be safe for that. It is nil
	// exactly when Served is set: a served route's key is the credentials
	// service's and never this job's.
	Credential func() (string, error)
	// FeePct is the route fee, in percent, that the budget adds to every
	// call on this route: to the reservation (so the cap counts it) and to
	// the settled charge.
	FeePct float64
}

// feePct is the fee of r, 0 for no route -- a Claude call only when a route
// claims it.
func (r *Route) feePct() float64 {
	if r == nil {
		return 0
	}
	return r.FeePct
}

// MaxFeePct bounds a route's fee, the same limit as the project config's
// route_fee_pct (config.MaxRouteFeePct; a test keeps the two equal): a larger
// fee is a typo, and +Inf would make every reservation saturate.
const MaxFeePct = 50

var routeNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

func (r Route) matches(model string) bool {
	return slices.ContainsFunc(r.Models, func(pat string) bool { return config.PatternMatches(pat, model) })
}

// overlaps reports whether some model ID is claimed by both routes.
func (r Route) overlaps(o Route) bool {
	for _, a := range r.Models {
		for _, b := range o.Models {
			if config.PatternsOverlap(a, b) {
				return true
			}
		}
	}
	return false
}

// copyRoutes is a deep copy: the gateway's routes never change under it
// because the caller edits its own slices.
func copyRoutes(rs []Route) []Route {
	if rs == nil {
		return nil
	}
	out := make([]Route, len(rs))
	for i, r := range rs {
		out[i] = r
		out[i].Models = append([]string(nil), r.Models...)
	}
	return out
}

// missingAnthropicKey is how a call that needs the Anthropic upstream is
// refused when the run has no key stored (design project-centric §6.1): a
// stored key is mounted whenever the secret has a version, so storing one and
// re-running `fugaro update` is the whole fix, and a run whose models all
// route through providers never needs it.
const missingAnthropicKey = config.MissingAnthropicKey

// validateRoutes: routes need the Anthropic upstream's kind, never its key
// (a run whose models are all routed has no key, and that is exactly when
// they matter) — or the service upstream, which is where a served route's key
// is (KS5), or a switched api-key run, whose own provider keys stay mounted.
func validateRoutes(u Upstream, routes []Route) error {
	if len(routes) == 0 {
		return nil
	}
	if u.Kind == KindOAuth {
		// Tier A (D133, oauth.go): a route is a provider's endpoint, and an
		// OAuth token is only ever sent to Anthropic's own. An OAuth
		// upstream takes no routes at all, whatever they name.
		return fmt.Errorf("gateway: the OAuth upstream takes no routes: %w", errOAuthOnRoute)
	}
	if u.Kind != KindService {
		// A served route's key is the credentials service's (Served, KS5), and
		// the service's provider route is the only place it is reached: an
		// upstream that is not the service cannot carry one, the Anthropic one
		// included -- a served route there would be sent to a path Anthropic
		// does not serve, with no credential of the job's that it could
		// present to the service either.
		for _, r := range routes {
			if r.Served {
				return fmt.Errorf("gateway: route %q takes its key from the credentials service, and this upstream is %q, which is not the service (R1, R12)", r.Name, u.Kind)
			}
		}
	}
	if u.Kind != "anthropic" && u.Kind != KindService {
		return fmt.Errorf("gateway: routes need the anthropic upstream, not %q (a run does not mix credentials)", u.Kind)
	}
	if u.Kind == KindService {
		// C1.15 (service.go): a route the job holds the key for is reached
		// with that key, and a cascade run has no key of its own (D143). The
		// one service upstream that does have one is a switched api-key run
		// (B2.2): its own repository's auth is what AuthMode declares, and
		// R6/D1634 keep that repository's provider keys on the runner path
		// and mounted, so the calls a route claims go to the provider's own
		// endpoint with the job's key and every other call goes to the
		// service. A declaration of oauth is no such run — a provider pin
		// under oauth is refused before a gateway is built
		// (config.CheckProviderAuth) — and an upstream that declares nothing
		// is a cascade's.
		//
		// A served route (Served, KS5) is the other half of that, and it is
		// R12's fact rather than the auth's: the service holds this
		// repository's provider keys, so the route is reached on the
		// service's provider route and the job mounts nothing. It is
		// admitted whatever auth the run declares, because the fact is about
		// the repository's keys and not about the auth its Anthropic calls
		// go out on.
		if u.AuthMode != "api-key" {
			for _, r := range routes {
				if !r.Served {
					return fmt.Errorf("gateway: the service upstream takes no routes: %w", errServiceOnRoute)
				}
			}
		}
	}
	for i, r := range routes {
		at := fmt.Sprintf("gateway: route %q", r.Name)
		switch {
		case !routeNameRE.MatchString(r.Name):
			return fmt.Errorf("gateway: route name %q isn't a name", r.Name)
		case strings.EqualFold(r.Auth, authOAuth):
			// Tier A (D133): a route is a provider's endpoint, so it never
			// carries an OAuth credential -- refused by name, here and in
			// attachCredential, rather than by the generic auth message.
			return fmt.Errorf("%s: %w", at, errOAuthOnRoute)
		case r.Served && r.Credential != nil:
			// A served route's key is the credentials service's (Served,
			// KS5): one that also carries a source of the job's own is a
			// caller reaching for two keys at once, and which of them a call
			// was made with would depend on the path taken.
			return fmt.Errorf("%s is served by the credentials service and carries a credential source as well: a served route's key is the service's", at)
		case r.Credential == nil && !r.Served:
			return fmt.Errorf("%s has no credential source", at)
		case r.Auth != "bearer" && r.Auth != "x-api-key":
			return fmt.Errorf("%s: auth %q must be bearer or x-api-key", at, r.Auth)
		case len(r.Models) == 0:
			return fmt.Errorf("%s serves no models", at)
		case r.FeePct < 0 || r.FeePct != r.FeePct || r.FeePct > MaxFeePct:
			return fmt.Errorf("%s: fee %v must be between 0 and %d percent", at, r.FeePct, MaxFeePct)
		}
		if r.BaseURL == "" {
			return fmt.Errorf("%s has no base URL", at)
		}
		if err := checkRouteBaseURL(r.BaseURL); err != nil {
			return err
		}
		for _, m := range r.Models {
			if msg := config.CheckModelPattern(m); msg != "" {
				return fmt.Errorf("%s: %s", at, msg)
			}
		}
		for _, o := range routes[:i] {
			if r.overlaps(o) {
				return fmt.Errorf("%s overlaps route %q: a model belongs to one route", at, o.Name)
			}
		}
	}
	return nil
}

// checkRouteBaseURL is checkBaseURL for a provider: https, or http on a
// loopback host, and a path prefix is allowed ("https://openrouter.ai/api");
// no credentials, query or fragment, and no path that could climb out.
func checkRouteBaseURL(s string) error {
	if err := config.CheckProviderBaseURL(s); err != nil {
		return fmt.Errorf("gateway: route base %w", err)
	}
	return nil
}

// routeFor is the route that claims model, or nil: any model no route claims
// goes to Options.Upstream (the direct Anthropic route); a CLAIMED Claude ID is not one of them: it matches its provider route first.
//
// It is a function of the routes rather than of the Server because the request
// check reads it (checkRequest, forward.go) and the check is shared with the
// credentials service, which has no Server and no routes of its own
// (RequestRefusal, export.go): one reading of "which route claims this model",
// not a second one that could disagree.
func routeFor(model string, routes []Route) *Route {
	for i := range routes {
		if routes[i].matches(model) {
			return &routes[i]
		}
	}
	return nil
}

// hasVariantSuffix: a routed model ID with ":" carries a provider variant
// (":online", ":free", ":nitro") that changes routing and price.
func hasVariantSuffix(model string) bool { return strings.Contains(model, ":") }

// attachCredential sets the route's key on the upstream request. The agent's
// own credentials were already dropped by forwardHeaders.
func (r *Route) attachCredential(h http.Header) (key string, err error) {
	if strings.EqualFold(r.Auth, authOAuth) {
		// Tier A (D133, oauth.go): a route is a provider's endpoint, and an
		// OAuth token is only ever sent to Anthropic's own. Refused here as
		// well as in validateRoutes, so a Route built in code -- past
		// validation -- cannot put the token on the wire either.
		return "", errOAuthOnRoute
	}
	key, err = r.Credential()
	if err != nil || key == "" {
		return "", errors.New("the provider key isn't available")
	}
	if r.Auth == "bearer" {
		h.Set("Authorization", "Bearer "+key)
	} else {
		h.Set("x-api-key", key)
	}
	return key, nil
}

// withRoute points a call at the provider that claims model. It runs after
// the pin check, so only a pinned model is ever sent to a provider.
//
// Where it points depends on where the route's key is (KS5, R1): a served
// route's key is the credentials service's, so its call goes to that service's
// provider route — the service's own base, the provider route's prefix, and
// the endpoint's own path behind it, which is the path the service forwards
// upstream (ProviderRoutePath, ProviderCountTokensPath) — while a route the
// job holds a key for still goes straight to the provider's own address.
func (s *Server) withRoute(rt route, model string) route {
	pr := routeFor(model, s.o.Routes)
	if pr == nil || rt.path == "" {
		return rt
	}
	rt.provider = pr
	if pr.Served {
		rt.upstream = anthropicBase(s.o.Upstream) + ProviderRoutePrefix + rt.path + rt.query
		return rt
	}
	rt.upstream = trimSlash(pr.BaseURL) + rt.path + rt.query
	return rt
}

// upstreamQuery is the request's raw query, unless it carries the gateway
// token (the run's token goes nowhere but to the agent's calls to us).
func (s *Server) upstreamQuery(r *http.Request) string {
	if q := query(r); !s.carriesToken(r.URL.RawQuery) {
		return q
	}
	return ""
}

// carriesToken: v holds the gateway token in any case, or percent-encoded
// (a bad escape is left as it is, so it cannot hide one).
//
// A Server with no token carries no token: strings.Contains(v, "") is true for
// every v, so without this arm scrubToken deletes every header it is handed and
// upstreamQuery drops every query -- the caller's allow-list would never be
// exercised on a Server built without Start, which is the shape a test that
// wants only the allow-list reaches for. Every started gateway has a token
// (Start makes one), so this is a property of the function rather than a route
// case.
func (s *Server) carriesToken(v string) bool {
	if s.token == "" {
		return false
	}
	tok := strings.ToLower(s.token)
	lv := strings.ToLower(v)
	return strings.Contains(lv, tok) || strings.Contains(strings.ToLower(lenientUnescape(lv)), tok)
}

// lenientUnescape decodes every valid %XX (and "+") and keeps the rest.
func lenientUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		case c == '+':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// scrubToken drops every header value that carries the gateway token.
func (s *Server) scrubToken(h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			if s.carriesToken(v) {
				delete(h, k)
				break
			}
		}
	}
}

// providerRequestHeaders are the only request headers a provider gets:
// the agent's session and agent IDs, cookies and SDK telemetry stay here.
var providerRequestHeaders = []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "User-Agent"}

func providerHeaders(in http.Header) http.Header {
	out := http.Header{}
	for _, k := range providerRequestHeaders {
		if vs := in.Values(k); len(vs) > 0 {
			out[k] = append([]string(nil), vs...)
		}
	}
	out.Set("Accept-Encoding", "identity")
	return out
}

// providerResponseHeaders are the only headers of a provider's error that
// reach the agent: the rest may echo the key or identify the account.
var providerResponseHeaders = []string{"Retry-After", "X-Request-Id", "Request-Id", "Anthropic-Request-Id"}

// providerError is the gateway's own error for a provider's non-2xx
// answer. Providers echo the key they were sent (masked or whole) in these,
// so nothing of the body or its message is kept: only the error type the
// agent acts on (from the body when it is one of Anthropic's, else from
// the status), a generic message and an allowlist of headers.
func providerError(w http.ResponseWriter, resp *http.Response, body []byte) string {
	typ := errorTypeFromBody(body)
	if typ == "" {
		typ = errorTypeForProviderStatus(resp.StatusCode)
	}
	h := w.Header()
	for _, k := range providerResponseHeaders {
		if vs := resp.Header.Values(k); len(vs) > 0 {
			h[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
		}
	}
	h.Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":%s,"message":%s}}`, jsonString(typ),
		jsonString(fmt.Sprintf("fugaro: the provider answered with status %d", resp.StatusCode)))
	return typ
}

var providerErrTypeRE = regexp.MustCompile(`^[a-z][a-z_]{0,39}$`)

func errorTypeFromBody(body []byte) string {
	var e struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || !providerErrTypeRE.MatchString(e.Error.Type) {
		return ""
	}
	return e.Error.Type
}

func errorTypeForProviderStatus(status int) string {
	switch status {
	case 401:
		return "authentication_error"
	case 403:
		return "permission_error"
	case 404:
		return "not_found_error"
	case 413:
		return "request_too_large"
	case 429:
		return "rate_limit_error"
	case 503, 529:
		return "overloaded_error"
	}
	if status >= 400 && status < 500 {
		return "invalid_request_error"
	}
	return "api_error"
}

// keyFrom is the credential attachCredential set on h.
func keyFrom(h http.Header) string {
	if k := h.Get("x-api-key"); k != "" {
		return k
	}
	return strings.TrimPrefix(h.Get("Authorization"), "Bearer ")
}

const redacted = "[redacted]"

// redactWriter replaces every occurrence of every key in what is written
// through it, also when a chunk boundary splits one. It holds back only a
// tail that could still become one of the keys, so a stream is delayed by
// nothing else.
//
// It takes more than one key because a service call carries two credentials
// of this run -- the job's ID token in the Authorization header and the run's
// own identity beside it (C1.15, HeaderRunIdentity) -- and a body the service
// echoes back may carry either. An empty key is dropped rather than kept: an
// empty needle would match everywhere.
type redactWriter struct {
	w    io.Writer
	keys [][]byte
	hold []byte
}

func newRedactWriter(w io.Writer, keys ...string) *redactWriter {
	r := &redactWriter{w: w}
	for _, k := range keys {
		if k != "" {
			r.keys = append(r.keys, []byte(k))
		}
	}
	return r
}

func (r *redactWriter) Write(p []byte) (int, error) {
	if len(r.keys) == 0 {
		return r.w.Write(p)
	}
	buf := append(r.hold, p...)
	r.hold = nil
	for _, k := range r.keys {
		buf = bytes.ReplaceAll(buf, k, []byte(redacted))
	}
	// Keep the longest suffix that is a proper prefix of any key.
	keep := 0
	for _, k := range r.keys {
		for n := min(len(k)-1, len(buf)); n > keep; n-- {
			if bytes.HasSuffix(buf, k[:n]) {
				keep = n
				break
			}
		}
	}
	r.hold = append([]byte(nil), buf[len(buf)-keep:]...)
	if _, err := r.w.Write(buf[:len(buf)-keep]); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close writes what was held back (it was not the key after all).
func (r *redactWriter) Close() error {
	if len(r.hold) == 0 {
		return nil
	}
	_, err := r.w.Write(r.hold)
	r.hold = nil
	return err
}

// redactHeaders drops every header value that carries key.
func redactHeaders(h http.Header, key string) {
	if key == "" {
		return
	}
	for k, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, key) {
				delete(h, k)
				break
			}
		}
	}
}
