package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Route sends the models it claims to a non-Anthropic endpoint that
// accepts Anthropic Messages requests (design m10-multi-model.md §3, §6).
// Claude models are never routed: they go to Options.Upstream, so Claude's
// prompt caching and the Anthropic key are untouched.
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
	// Credential is the provider key, read per call. It comes from the
	// runner (never from the agent's environment) and is attached to the
	// upstream request only; an error means the call is not sent. It is
	// called from concurrent calls, so it must be safe for that.
	Credential func() (string, error)
	// FeePct is the route fee, in percent, that the budget adds to every
	// call on this route: to the reservation (so the cap counts it) and to
	// the settled charge.
	FeePct float64
}

// feePct is the fee of r, 0 for no route (a Claude call).
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
	for _, pat := range r.Models {
		if pre, ok := strings.CutSuffix(pat, "*"); ok {
			if strings.HasPrefix(model, pre) {
				return true
			}
		} else if pat == model {
			return true
		}
	}
	return false
}

// overlaps reports whether some model ID is claimed by both routes.
func (r Route) overlaps(o Route) bool {
	for _, a := range r.Models {
		for _, b := range o.Models {
			pa, ga := strings.CutSuffix(a, "*")
			pb, gb := strings.CutSuffix(b, "*")
			switch {
			case ga && gb && (strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)),
				ga && !gb && strings.HasPrefix(b, pa),
				gb && !ga && strings.HasPrefix(a, pb),
				!ga && !gb && a == b:
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

func validateRoutes(u Upstream, routes []Route) error {
	if len(routes) == 0 {
		return nil
	}
	if u.Kind != "anthropic" {
		return fmt.Errorf("gateway: routes need the anthropic upstream, not %q (a run does not mix credentials)", u.Kind)
	}
	for i, r := range routes {
		at := fmt.Sprintf("gateway: route %q", r.Name)
		switch {
		case !routeNameRE.MatchString(r.Name):
			return fmt.Errorf("gateway: route name %q isn't a name", r.Name)
		case r.Credential == nil:
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
			if m == "" || strings.Count(m, "*") > 1 || (strings.Contains(m, "*") && !strings.HasSuffix(m, "*")) {
				return fmt.Errorf("%s: %q must be a model ID or a prefix ending in *", at, m)
			}
			for _, c := range []string{"claude-*", "anthropic/*"} {
				if (Route{Models: []string{m}}).overlaps(Route{Models: []string{c}}) {
					return fmt.Errorf("%s: %q would claim Claude models, which go direct to Anthropic", at, m)
				}
			}
			if pre, wild := strings.CutSuffix(m, "*"); wild && len(pre) < 2 {
				return fmt.Errorf("%s: %q is too broad", at, m)
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
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("gateway: route base %q must be a scheme, a host and at most a path", s)
	}
	if u.RawPath != "" || strings.ContainsAny(u.Path, "\\%") {
		return fmt.Errorf("gateway: route base %q has an escaped path", s)
	}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("gateway: route base %q has a dot segment", s)
		}
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("gateway: route base %q must be https, or http on a loopback address", s)
}

// routeFor is the route that claims model, or nil: Claude and any model no
// route claims go to Options.Upstream.
func (s *Server) routeFor(model string) *Route {
	for i := range s.o.Routes {
		if s.o.Routes[i].matches(model) {
			return &s.o.Routes[i]
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
func (s *Server) withRoute(rt route, model string) route {
	if pr := s.routeFor(model); pr != nil && rt.path != "" {
		rt.provider = pr
		rt.upstream = trimSlash(pr.BaseURL) + rt.path + rt.query
	}
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
func (s *Server) carriesToken(v string) bool {
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

// redactWriter replaces every occurrence of key in what is written through
// it, also when a chunk boundary splits one. It holds back only a tail that
// could still become the key, so a stream is delayed by nothing else.
type redactWriter struct {
	w    io.Writer
	key  []byte
	hold []byte
}

func newRedactWriter(w io.Writer, key string) *redactWriter {
	return &redactWriter{w: w, key: []byte(key)}
}

func (r *redactWriter) Write(p []byte) (int, error) {
	if len(r.key) == 0 {
		return r.w.Write(p)
	}
	buf := append(r.hold, p...)
	r.hold = nil
	buf = bytes.ReplaceAll(buf, r.key, []byte(redacted))
	// Keep the longest suffix that is a proper prefix of the key.
	keep := 0
	for n := min(len(r.key)-1, len(buf)); n > 0; n-- {
		if bytes.HasSuffix(buf, r.key[:n]) {
			keep = n
			break
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
