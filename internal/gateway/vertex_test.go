package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const vertexAccessToken = "ya29.TEST-VERTEX-TOKEN-0123456789"

func staticToken(s string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: s, TokenType: "Bearer"})
}

func vertexUpstream(base string) Upstream {
	return Upstream{
		Kind: "vertex", BaseURL: base, Token: staticToken(vertexAccessToken),
		VertexProject: "proj-1234", VertexLocations: []string{"us-east5", "europe-west1", "global"},
	}
}

func newVertexHarness(t *testing.T, script ...anthropicfake.Reply) *harness {
	t.Helper()
	return newHarnessWith(t, func(o *Options) { o.Upstream = vertexUpstream(o.Upstream.BaseURL) }, script...)
}

func vertexPath(project, location, model, method string) string {
	return "/v1/projects/" + project + "/locations/" + location + "/publishers/anthropic/models/" + model + ":" + method
}

// vertexBody is a Vertex Messages body: the model is in the path.
func vertexBody(maxTokens int, extra ...string) string {
	s := `{"anthropic_version":"vertex-2023-10-16","max_tokens":` + strconv.Itoa(maxTokens) + `,"messages":[{"role":"user","content":"hello"}]`
	for _, e := range extra {
		s += "," + e
	}
	return s + "}"
}

// vertexPost posts without any credential, as Claude Code does with
// CLAUDE_CODE_SKIP_VERTEX_AUTH=1.
func (h *harness) vertexPost(path, body string) (*http.Response, string) {
	h.t.Helper()
	req := h.request(context.Background(), path, body)
	req.Header.Del("x-api-key")
	return h.do(req)
}

func TestVertexNoTokenLoopback(t *testing.T) {
	u := pricing.Usage{Input: 7, Output: 9}
	h := newVertexHarness(t, anthropicfake.StreamOK(sonnet, u))
	// Whatever credential the client sends is dropped for the run's token.
	req := h.request(context.Background(), vertexPath("proj-1234", "us-east5", sonnet, "streamRawPredict"), vertexBody(100, `"stream":true`),
		"x-api-key", "client-key", "Authorization", "Bearer client-token", "x-goog-user-project", "aurora-gcp-1")
	resp, body := h.do(req)
	if resp.StatusCode != 200 || !strings.Contains(body, "message_stop") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	seen := h.fake.Seen()[0]
	if got := seen.Header.Get("Authorization"); got != "Bearer "+vertexAccessToken {
		t.Errorf("upstream Authorization = %q", got)
	}
	if seen.Header.Get("x-api-key") != "" || seen.Header.Get("x-goog-user-project") != "" {
		t.Error("a client credential or billing project reached Vertex")
	}
	if seen.URL.Path != vertexPath("proj-1234", "us-east5", sonnet, "streamRawPredict") {
		t.Errorf("upstream path %q", seen.URL.Path)
	}
	if got := h.gw.EndStage().Used; got != cost(t, sonnet, u) {
		t.Errorf("used %d, want %d", got, cost(t, sonnet, u))
	}
	// The Anthropic paths don't exist on a Vertex gateway.
	if resp, _ := h.post(msg(sonnet, 10)); resp.StatusCode != 404 {
		t.Errorf("/v1/messages on Vertex: %d, want 404", resp.StatusCode)
	}
}

func TestVertexOtherProjectRefused(t *testing.T) {
	h := newVertexHarness(t)
	for _, p := range []string{"aurora-gcp-1", "proj-12345", "proj-123", "PROJ-1234"} {
		resp, _ := h.vertexPost(vertexPath(p, "us-east5", sonnet, "rawPredict"), vertexBody(100))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("project %s: status %d, want 404", p, resp.StatusCode)
		}
	}
	if h.fake.Count() != 0 {
		t.Error("a request for another project was forwarded")
	}
}

// hostRecorder sends every request to target, recording the host the
// gateway addressed.
type hostRecorder struct {
	target *url.URL
	mu     sync.Mutex
	hosts  []string
}

func (h *hostRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.hosts = append(h.hosts, r.URL.Scheme+"://"+r.URL.Host)
	h.mu.Unlock()
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host, c.Host = h.target.Scheme, h.target.Host, ""
	return http.DefaultTransport.RoundTrip(c)
}

func TestVertexRegionOverrideAllowed(t *testing.T) {
	u := pricing.Usage{Input: 3, Output: 4}
	var rec *hostRecorder
	h := newHarnessWith(t, func(o *Options) {
		target, _ := url.Parse(o.Upstream.BaseURL)
		rec = &hostRecorder{target: target}
		o.Upstream = vertexUpstream("") // the real hosts, derived from the path
		o.Client = &http.Client{Transport: rec}
	}, anthropicfake.MessageOK(sonnet, u), anthropicfake.MessageOK(haiku, u), anthropicfake.MessageOK(haiku, u))
	for _, c := range []struct{ loc, model string }{{"us-east5", sonnet}, {"europe-west1", haiku}, {"global", haiku}} {
		if resp, body := h.vertexPost(vertexPath("proj-1234", c.loc, c.model, "rawPredict"), vertexBody(100)); resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", c.loc, resp.StatusCode, body)
		}
	}
	want := []string{"https://us-east5-aiplatform.googleapis.com", "https://europe-west1-aiplatform.googleapis.com", "https://aiplatform.googleapis.com"}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if strings.Join(rec.hosts, " ") != strings.Join(want, " ") {
		t.Errorf("hosts %q, want %q", rec.hosts, want)
	}
}

func TestVertexUnknownLocationRefused(t *testing.T) {
	h := newVertexHarness(t)
	for _, loc := range []string{"asia-east1", "us-east5.evil", "US-EAST5", ""} {
		resp, _ := h.vertexPost(vertexPath("proj-1234", loc, sonnet, "rawPredict"), vertexBody(100))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("location %q: status %d, want 404", loc, resp.StatusCode)
		}
	}
	for _, p := range []string{
		vertexPath("proj-1234", "us-east5", sonnet, "predict"),
		vertexPath("proj-1234", "us-east5", "count-tokens", "streamRawPredict"),
		"/v1/projects/proj-1234/locations/us-east5/publishers/google/models/" + sonnet + ":rawPredict",
		"/v1/projects/proj-1234/locations/us-east5/publishers/anthropic/models/a/b:rawPredict",
	} {
		if resp, _ := h.vertexPost(p, vertexBody(100)); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
		}
	}
	if h.fake.Count() != 0 {
		t.Error("a refused path was forwarded")
	}
}

func TestVertexModelFromPath(t *testing.T) {
	u := pricing.Usage{Input: 3, Output: 4}
	h := newVertexHarness(t, anthropicfake.MessageOK(haiku, u))
	// The path names an unpinned model; the body's model doesn't count.
	resp, body := h.vertexPost(vertexPath("proj-1234", "us-east5", opus, "rawPredict"), vertexBody(100, `"model":"claude-sonnet-5-5"`))
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
	if _, m := apiError(t, body); !strings.Contains(m, "model claude-opus-5 is not pinned") {
		t.Errorf("message %q", m)
	}
	// Vertex's "@" spelling of the background model is one of its aliases.
	if resp, body := h.vertexPost(vertexPath("proj-1234", "us-east5", "claude-haiku-4-5@20251001", "rawPredict"), vertexBody(100)); resp.StatusCode != 200 {
		t.Fatalf("aliased model: %d %s", resp.StatusCode, body)
	}
	if c := h.logs.lastCall(t); c["model"] != "claude-haiku-4-5@20251001" {
		t.Errorf("logged model %v", c["model"])
	}
}

func TestVertexCountTokensFree(t *testing.T) {
	h := newVertexHarness(t, anthropicfake.Reply{Body: `{"input_tokens":5}`})
	resp, body := h.vertexPost(vertexPath("proj-1234", "us-east5", "count-tokens", "rawPredict"), `{"model":"claude-sonnet-5-5","messages":[]}`)
	if resp.StatusCode != 200 || body != `{"input_tokens":5}` {
		t.Fatalf("count-tokens: %d %q", resp.StatusCode, body)
	}
	if got := h.fake.Seen()[0].Header.Get("Authorization"); got != "Bearer "+vertexAccessToken {
		t.Errorf("Authorization %q", got)
	}
	if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
}

func TestVertexBaseHosts(t *testing.T) {
	for loc, want := range map[string]string{
		"us-east5":     "https://us-east5-aiplatform.googleapis.com",
		"europe-west1": "https://europe-west1-aiplatform.googleapis.com",
		"global":       "https://aiplatform.googleapis.com",
	} {
		if got := vertexBase(Upstream{Kind: "vertex"}, loc); got != want {
			t.Errorf("vertexBase(%s) = %s, want %s", loc, got, want)
		}
	}
	if got := vertexBase(Upstream{Kind: "vertex", BaseURL: "http://127.0.0.1:9"}, "us-east5"); got != "http://127.0.0.1:9" {
		t.Errorf("a test base is used as is: %s", got)
	}
}
