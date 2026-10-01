package gateway

import (
	"net"
	"net/http"
	"regexp"
	"slices"
)

var (
	// locationRE is a Vertex location ("us-east5", "global"): it becomes
	// part of the upstream host, so nothing else may pass.
	locationRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// vertexPathRE is a Vertex Anthropic call: project, location, model
	// and method.
	vertexPathRE = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/publishers/anthropic/models/([^/:]+):(rawPredict|streamRawPredict)$`)
	// vertexModelRE is a model as a Vertex path names it.
	vertexModelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.@_-]*$`)
)

// vertexRoute accepts a Vertex path only for the run's GCP project and
// an allowed location, from loopback. Claude Code sends no credential to
// Vertex when told to skip its auth, so none is required.
func (s *Server) vertexRoute(r *http.Request) (route, bool, string) {
	u := s.o.Upstream
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !isLoopback(host) {
		return route{}, false, "the gateway serves loopback only"
	}
	if r.Method != http.MethodPost || r.URL.RawPath != "" {
		return route{}, false, "the budget gateway doesn't serve " + r.Method + " " + logValue(r.URL.Path)
	}
	m := vertexPathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return route{}, false, "the budget gateway doesn't serve " + r.Method + " " + logValue(r.URL.Path)
	}
	project, location, model, method := m[1], m[2], m[3], m[4]
	if project != u.VertexProject {
		return route{}, false, "GCP project " + logValue(project) + " isn't this run's"
	}
	if !slices.Contains(u.VertexLocations, location) || !locationRE.MatchString(location) {
		return route{}, false, "location " + logValue(location) + " isn't allowed for this run"
	}
	upstream := vertexBase(u, location) + r.URL.Path + query(r)
	if model == "count-tokens" {
		if method != "rawPredict" {
			return route{}, false, "the budget gateway doesn't serve count-tokens:" + method
		}
		return route{count: true, upstream: upstream}, true, ""
	}
	if !vertexModelRE.MatchString(model) {
		return route{}, false, "model " + logValue(model) + " isn't a model name"
	}
	return route{upstream: upstream, pathModel: model}, true, ""
}

// vertexBase is the Vertex host for a location: regional, or the global
// endpoint. A test base URL replaces it.
func vertexBase(u Upstream, location string) string {
	if u.BaseURL != "" {
		return trimSlash(u.BaseURL)
	}
	if location == "global" {
		return "https://aiplatform.googleapis.com"
	}
	return "https://" + location + "-aiplatform.googleapis.com"
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
