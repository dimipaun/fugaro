package gateway

import (
	"errors"
	"fmt"
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
	// upstream request only; an error means the call is not sent.
	Credential func() (string, error)
	// FeePct is the route fee the budget adds to every charge (T5 applies
	// it; the gateway only carries it).
	FeePct float64
}

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
		case r.FeePct < 0 || r.FeePct != r.FeePct:
			return fmt.Errorf("%s: fee %v is negative", at, r.FeePct)
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
			if (Route{Models: []string{m}}).overlaps(Route{Models: []string{"claude-*"}}) {
				return fmt.Errorf("%s: %q would claim Claude models, which go direct to Anthropic", at, m)
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
func (r *Route) attachCredential(h http.Header) error {
	key, err := r.Credential()
	if err != nil || key == "" {
		return errors.New("the provider key isn't available")
	}
	if r.Auth == "bearer" {
		h.Set("Authorization", "Bearer "+key)
	} else {
		h.Set("x-api-key", key)
	}
	return nil
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
	if q := query(r); !strings.Contains(q, s.token) && !strings.Contains(r.URL.RawQuery, url.QueryEscape(s.token)) {
		return q
	}
	return ""
}

// scrubToken drops every header value that carries the gateway token.
func (s *Server) scrubToken(h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, s.token) {
				delete(h, k)
				break
			}
		}
	}
}
