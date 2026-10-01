package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// testKey stands in for the real API key: it must reach the upstream and
// nothing else.
const testKey = "sk-ant-api03-TESTKEY-0123456789abcdefghijklmnopqrstuvwxyz"

const (
	sonnet = "claude-sonnet-5-5"
	haiku  = "claude-haiku-4-5"
	opus   = "claude-opus-5"
)

// logBuf collects the gateway's JSON log lines.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *logBuf) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// calls are the "model call" lines.
func (l *logBuf) calls(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range l.lines(t) {
		if m["msg"] == "model call" {
			out = append(out, m)
		}
	}
	return out
}

// lastCall is the last "model call" line.
func (l *logBuf) lastCall(t *testing.T) map[string]any {
	t.Helper()
	c := l.calls(t)
	if len(c) == 0 {
		t.Fatalf("no model call logged; log:\n%s", l.String())
	}
	return c[len(c)-1]
}

type harness struct {
	t    *testing.T
	gw   *Server
	fake *anthropicfake.Fake
	up   *httptest.Server
	logs *logBuf
}

var defaultStage = Stage{Name: "implement", Model: sonnet, Background: haiku}

// newHarness starts a fake upstream with script and a gateway in observe
// mode pointed at it, with the default stage begun. edit changes the
// options before the start.
func newHarnessWith(t *testing.T, edit func(*Options), script ...anthropicfake.Reply) *harness {
	t.Helper()
	fake, up := anthropicfake.New(t, script...)
	logs := &logBuf{}
	o := Options{
		Upstream: Upstream{Kind: "anthropic", BaseURL: up.URL, APIKey: testKey},
		Prices:   pricing.Embedded(),
		Mode:     Observe,
		Log:      slog.New(slog.NewJSONHandler(logs, nil)),
	}
	if edit != nil {
		edit(&o)
	}
	gw, err := Start(context.Background(), o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(defaultStage)
	return &harness{t: t, gw: gw, fake: fake, up: up, logs: logs}
}

func newHarness(t *testing.T, script ...anthropicfake.Reply) *harness {
	t.Helper()
	return newHarnessWith(t, nil, script...)
}

func enforceCap(c pricing.Micros) func(*Options) {
	return func(o *Options) { o.Mode, o.Cap = Enforce, c }
}

// request builds a request to the gateway with the run's token.
func (h *harness) request(ctx context.Context, path, body string, hdr ...string) *http.Request {
	h.t.Helper()
	req, err := http.NewRequestWithContext(ctx, "POST", h.gw.URL()+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", h.gw.Token())
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return req
}

// post sends body to /v1/messages and returns the response, its body read.
func (h *harness) post(body string, hdr ...string) (*http.Response, string) {
	h.t.Helper()
	return h.do(h.request(context.Background(), "/v1/messages", body, hdr...))
}

func (h *harness) do(req *http.Request) (*http.Response, string) {
	h.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// msg is a minimal Messages request body with extra top-level members.
func msg(model string, maxTokens int64, extra ...string) string {
	s := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"messages":[{"role":"user","content":"hello"}]`, model, maxTokens)
	for _, e := range extra {
		s += "," + e
	}
	return s + "}"
}

// msgBlocks is a Messages request whose one user message holds blocks.
func msgBlocks(model string, maxTokens int64, blocks string, extra ...string) string {
	s := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"messages":[{"role":"user","content":[%s]}]`, model, maxTokens, blocks)
	for _, e := range extra {
		s += "," + e
	}
	return s + "}"
}

func model(t *testing.T, id string) pricing.Model {
	t.Helper()
	m, ok := pricing.Embedded().Lookup(id)
	if !ok {
		t.Fatalf("model %s isn't in the table", id)
	}
	return m
}

// worst is the reservation the gateway must make for body.
func worst(t *testing.T, id, body string, maxTokens int64, ttl string) pricing.Micros {
	t.Helper()
	return model(t, id).WorstCase(pricing.Request{BodyBytes: int64(len(body)), MaxTokens: maxTokens, CacheTTL: ttl})
}

func cost(t *testing.T, id string, u pricing.Usage) pricing.Micros {
	t.Helper()
	return model(t, id).Rates.Cost(u)
}

// apiError decodes an error body.
func apiError(t *testing.T, body string) (typ, message string) {
	t.Helper()
	var e struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Type != "error" {
		t.Fatalf("not an API error body: %q (%v)", body, err)
	}
	return e.Error.Type, e.Error.Message
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- routing and auth ---

func TestHelloIsLocal(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest("HEAD", h.gw.URL()+"/api/hello", nil)
	resp, _ := h.do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD /api/hello = %d, want 200", resp.StatusCode)
	}
	if h.fake.Count() != 0 {
		t.Errorf("the upstream saw %d requests, want 0", h.fake.Count())
	}
}

func TestUnknownPath404(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/complete"},
		{"GET", "/v1/models"},
		{"GET", "/v1/messages"},
		{"POST", "/v1/messages/batches"},
		{"POST", "/v1/files"},
		{"POST", "/v1/projects/p/locations/us-east5/publishers/anthropic/models/" + sonnet + ":rawPredict"},
		{"GET", "/api/hello"},
	} {
		req := h.request(context.Background(), c.path, msg(sonnet, 10))
		req.Method = c.method
		resp, body := h.do(req)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, resp.StatusCode)
			continue
		}
		if typ, _ := apiError(t, body); typ != "not_found_error" {
			t.Errorf("%s %s: error type %q", c.method, c.path, typ)
		}
	}
	if h.fake.Count() != 0 {
		t.Errorf("the upstream saw %d requests, want 0", h.fake.Count())
	}
}

func TestTokenRequiredAnthropic(t *testing.T) {
	h := newHarness(t)
	for name, tok := range map[string]string{"none": "", "wrong": strings.Repeat("0", 64), "real key": testKey} {
		req := h.request(context.Background(), "/v1/messages", msg(sonnet, 10))
		req.Header.Del("x-api-key")
		if tok != "" {
			req.Header.Set("x-api-key", tok)
		}
		resp, body := h.do(req)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
			continue
		}
		if typ, _ := apiError(t, body); typ != "authentication_error" {
			t.Errorf("%s: error type %q", name, typ)
		}
	}
	// A bearer token isn't how the run's token is sent.
	req := h.request(context.Background(), "/v1/messages", msg(sonnet, 10))
	req.Header.Del("x-api-key")
	req.Header.Set("Authorization", "Bearer "+h.gw.Token())
	if resp, _ := h.do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bearer: status %d, want 401", resp.StatusCode)
	}
	if h.fake.Count() != 0 {
		t.Errorf("the upstream saw %d requests, want 0", h.fake.Count())
	}
}

func TestCountTokensFreeAndForwarded(t *testing.T) {
	h := newHarness(t, anthropicfake.Reply{Body: `{"input_tokens":42}`})
	body := `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"hi"}]}` // no max_tokens: count_tokens has none
	resp, got := h.post2("/v1/messages/count_tokens?beta=true", body)
	if resp.StatusCode != 200 || got != `{"input_tokens":42}` {
		t.Fatalf("count_tokens = %d %q", resp.StatusCode, got)
	}
	seen := h.fake.Seen()
	if len(seen) != 1 || seen[0].URL.Path != "/v1/messages/count_tokens" || seen[0].URL.RawQuery != "beta=true" {
		t.Fatalf("upstream saw %v", seen)
	}
	if string(h.fake.Body(0)) != body {
		t.Errorf("body changed: %q", h.fake.Body(0))
	}
	rep := h.gw.EndStage()
	if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 || rep.Calls != 0 || rep.Used != 0 {
		t.Errorf("count_tokens was charged: ledger %+v, report %+v", l, rep)
	}
}

func (h *harness) post2(path, body string) (*http.Response, string) {
	h.t.Helper()
	return h.do(h.request(context.Background(), path, body))
}

func TestStartListensOnLoopbackOnly(t *testing.T) {
	h := newHarness(t)
	u, err := url.Parse(h.gw.URL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" {
		t.Errorf("URL() = %q, want http://127.0.0.1:<port>", h.gw.URL())
	}
	if tok := h.gw.Token(); len(tok) != 64 || strings.Trim(tok, "0123456789abcdef") != "" {
		t.Errorf("Token() = %q, want 64 hex characters", tok)
	}
	// Each run gets its own token.
	h2 := newHarness(t)
	if h2.gw.Token() == h.gw.Token() {
		t.Error("two gateways share a token")
	}
}

func TestStartRejectsBadOptions(t *testing.T) {
	ok := Options{Upstream: Upstream{Kind: "anthropic", APIKey: testKey}, Prices: pricing.Embedded(), Mode: Observe}
	for name, edit := range map[string]func(*Options){
		"no prices":             func(o *Options) { o.Prices = nil },
		"no mode":               func(o *Options) { o.Mode = "" },
		"unknown mode":          func(o *Options) { o.Mode = "off" },
		"enforce without cap":   func(o *Options) { o.Mode = Enforce },
		"negative cap":          func(o *Options) { o.Mode, o.Cap = Enforce, -1 },
		"unknown upstream":      func(o *Options) { o.Upstream.Kind = "bedrock" },
		"anthropic without key": func(o *Options) { o.Upstream.APIKey = "" },
		"plain http elsewhere":  func(o *Options) { o.Upstream.BaseURL = "http://api.example.invalid" },
		"base with a path":      func(o *Options) { o.Upstream.BaseURL = "https://api.example.invalid/proxy" },
		"vertex without token": func(o *Options) {
			o.Upstream = Upstream{Kind: "vertex", VertexProject: "proj-1234", VertexLocations: []string{"us-east5"}}
		},
		"vertex without project": func(o *Options) {
			o.Upstream = Upstream{Kind: "vertex", Token: staticToken("t"), VertexLocations: []string{"us-east5"}}
		},
		"vertex without locations": func(o *Options) {
			o.Upstream = Upstream{Kind: "vertex", Token: staticToken("t"), VertexProject: "proj-1234"}
		},
		"vertex bad location": func(o *Options) {
			o.Upstream = Upstream{Kind: "vertex", Token: staticToken("t"), VertexProject: "proj-1234", VertexLocations: []string{"evil.com/x"}}
		},
	} {
		o := ok
		edit(&o)
		if gw, err := Start(context.Background(), o); err == nil {
			_ = gw.Close(context.Background())
			t.Errorf("%s: Start succeeded", name)
		}
	}
	gw, err := Start(context.Background(), ok)
	if err != nil {
		t.Fatalf("valid options: %v", err)
	}
	_ = gw.Close(context.Background())
}

// --- pins ---

func TestUnpinnedModelRefused400(t *testing.T) {
	h := newHarness(t)
	resp, body := h.post(msg(opus, 100))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
	typ, m := apiError(t, body)
	if typ != "invalid_request_error" || m != "fugaro: model claude-opus-5 is not pinned for stage implement" {
		t.Errorf("error = %s %q", typ, m)
	}
	if h.fake.Count() != 0 {
		t.Error("the refused request was forwarded")
	}
	rep := h.gw.EndStage()
	if len(rep.Violations) != 1 || rep.Violations[0] != "model claude-opus-5 is not pinned for stage implement" {
		t.Errorf("violations = %q", rep.Violations)
	}
	if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
}

func TestBackgroundModelAllowed(t *testing.T) {
	h := newHarness(t, anthropicfake.MessageOK(haiku, pricing.Usage{Input: 5, Output: 5}))
	if resp, body := h.post(msg(haiku, 100)); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if v := h.gw.EndStage().Violations; len(v) != 0 {
		t.Errorf("violations %q", v)
	}
}

func TestAliasOfPinnedAllowed(t *testing.T) {
	h := newHarness(t,
		anthropicfake.MessageOK("claude-haiku-4-5-20251001", pricing.Usage{Input: 5, Output: 5}),
		anthropicfake.MessageOK(haiku, pricing.Usage{Input: 5, Output: 5}))
	if resp, body := h.post(msg("claude-haiku-4-5-20251001", 100)); resp.StatusCode != 200 {
		t.Fatalf("dated alias of the pinned background model: %d %s", resp.StatusCode, body)
	}
	// The other way round: the stage pins an alias, the request names the ID.
	h.gw.EndStage()
	h.gw.BeginStage(Stage{Name: "review", Model: sonnet, Background: "claude-haiku-4-5@20251001"})
	if resp, body := h.post(msg(haiku, 100)); resp.StatusCode != 200 {
		t.Fatalf("ID of a pinned alias: %d %s", resp.StatusCode, body)
	}
	if v := h.gw.EndStage().Violations; len(v) != 0 {
		t.Errorf("violations %q", v)
	}
	// A prefix is not an alias.
	h.gw.BeginStage(defaultStage)
	if resp, _ := h.post(msg("claude-haiku-4", 100)); resp.StatusCode != 400 {
		t.Errorf("a prefix of the pinned model: status %d, want 400", resp.StatusCode)
	}
}

func TestMaxTokensLimitPerModel(t *testing.T) {
	h := newHarness(t, anthropicfake.MessageOK(haiku, pricing.Usage{Input: 5, Output: 5}), anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 5, Output: 5}))
	h.gw.EndStage()
	h.gw.BeginStage(Stage{Name: "implement", Model: sonnet, Background: haiku, MaxOutputTokens: 8000})

	resp, body := h.post(msg(sonnet, 16000))
	if resp.StatusCode != 400 {
		t.Fatalf("role model above the stage's limit: status %d", resp.StatusCode)
	}
	if _, m := apiError(t, body); m != "fugaro: max_tokens 16000 for model claude-sonnet-5-5 is above the stage's limit 8000" {
		t.Errorf("message %q", m)
	}
	// The background model chooses its own max_tokens.
	if resp, body := h.post(msg(haiku, 16000)); resp.StatusCode != 200 {
		t.Errorf("background model: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.post(msg(sonnet, 8000)); resp.StatusCode != 200 {
		t.Errorf("role model at the limit: %d %s", resp.StatusCode, body)
	}
	rep := h.gw.EndStage()
	if len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0], "above the stage's limit 8000") {
		t.Errorf("violations %q", rep.Violations)
	}

	// When the background model is the role's model, the limit applies to both.
	h.gw.BeginStage(Stage{Name: "review", Model: haiku, Background: haiku, MaxOutputTokens: 8000})
	if resp, _ := h.post(msg(haiku, 16000)); resp.StatusCode != 400 {
		t.Errorf("same model for both: status %d, want 400", resp.StatusCode)
	}
}

func TestNoStageRefused(t *testing.T) {
	h := newHarness(t)
	h.gw.EndStage()
	resp, body := h.post(msg(sonnet, 100))
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
	if _, m := apiError(t, body); !strings.Contains(m, "no stage") {
		t.Errorf("message %q", m)
	}
	if h.fake.Count() != 0 {
		t.Error("forwarded outside a stage")
	}
}

// --- lifecycle ---

func TestEndStageWaitsForInFlight(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.EventDelay = 50 * time.Millisecond
	h := newHarness(t, rep)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := h.post(msg(sonnet, 100, `"stream":true`))
		if resp.StatusCode != 200 {
			t.Errorf("status %d", resp.StatusCode)
		}
	}()
	waitFor(t, "the upstream to see the call", func() bool { return h.fake.Count() == 1 })
	r := h.gw.EndStage()
	if r.Calls != 1 || r.Used != cost(t, sonnet, u) {
		t.Errorf("report %+v, want 1 call at %d", r, cost(t, sonnet, u))
	}
	<-done
}

func TestEndStageGivesUpAfterItsWait(t *testing.T) {
	old := endStageWait
	endStageWait = 100 * time.Millisecond
	t.Cleanup(func() { endStageWait = old })
	h := newHarness(t, anthropicfake.Reply{Hold: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		resp, err := http.DefaultClient.Do(h.request(ctx, "/v1/messages", msg(sonnet, 100)))
		if err == nil {
			resp.Body.Close()
		}
	}()
	waitFor(t, "the upstream to see the call", func() bool { return h.fake.Count() == 1 })
	start := time.Now()
	h.gw.EndStage()
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("EndStage took %v", d)
	}
}

func TestCloseCancelsInFlight(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.EventDelay = 2 * time.Second
	h := newHarness(t, rep)
	go func() {
		resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", msg(sonnet, 100, `"stream":true`)))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	waitFor(t, "the upstream to see the call", func() bool { return h.fake.Count() == 1 })
	time.Sleep(100 * time.Millisecond) // message_start is through
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.gw.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-h.fake.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request wasn't cancelled")
	}
	l := h.gw.Ledger()
	if l.Reserved != 0 {
		t.Errorf("reserved %d after Close", l.Reserved)
	}
	want := cost(t, sonnet, pricing.Usage{Input: 10, Output: 100}) // input plus the reserved output
	if l.Used != want {
		t.Errorf("used %d, want %d (partial)", l.Used, want)
	}
	if c := h.logs.lastCall(t); c["settled"] != "partial" {
		t.Errorf("settled %v", c["settled"])
	}
	// Closed means closed.
	if _, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", msg(sonnet, 100))); err == nil {
		t.Error("the gateway still answers after Close")
	}
	if err := h.gw.Close(ctx); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

// readEvent reads one SSE event (up to its blank line).
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		b.WriteString(line)
		if err != nil {
			return b.String()
		}
		if line == "\n" {
			return b.String()
		}
	}
}

// closedPort is an address nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

// The call log says what Claude Code asked for: max_tokens and the tool
// types, so a live run shows the request shapes without a body in the log.
func TestCallLogHasMaxTokensAndToolTypes(t *testing.T) {
	h := newHarness(t, okReplies(2)...)
	allowed(t, h, msg(sonnet, 777, `"tools":[{"name":"Bash","input_schema":{"type":"object"}},{"type":"custom","name":"x","input_schema":{"type":"object"}}]`), "client tools")
	c := h.logs.lastCall(t)
	if num(c["max_tokens"]) != 777 || c["tool_types"] != "custom" {
		t.Errorf("log line %v", c)
	}
	refused(t, h, msg(sonnet, 55, `"tools":[{"type":"web_search_20260209","name":"web_search"}]`), "tool type web_search_20260209")
	c = h.logs.lastCall(t)
	if num(c["max_tokens"]) != 55 || c["tool_types"] != "web_search_20260209" {
		t.Errorf("log line of the refused call %v", c)
	}
}

// Headers that name the organization behind the real key, and cookies,
// stay with the gateway.
func TestResponseHeadersOfTheOrganizationAreDropped(t *testing.T) {
	r := anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 1, Output: 1})
	r.Header = http.Header{
		"Anthropic-Organization-Id": {"org-secret"}, "Set-Cookie": {"a=b"}, "Request-Id": {"req_1"},
	}
	h := newHarness(t, r, anthropicfake.Reply{Body: `{"input_tokens":1}`, Header: http.Header{"Anthropic-Organization-Id": {"org-secret"}}})
	// Ordered: the fake's replies are scripted, so a random map order would
	// hand the count_tokens reply to the messages call.
	for _, tc := range []struct {
		name string
		do   func() *http.Response
	}{
		{"messages", func() *http.Response { resp, _ := h.post(msg(sonnet, 10)); return resp }},
		{"count_tokens", func() *http.Response {
			resp, _ := h.post2("/v1/messages/count_tokens", `{"model":"claude-sonnet-5-5","messages":[]}`)
			return resp
		}},
	} {
		name, do := tc.name, tc.do
		resp := do()
		for _, k := range []string{"Anthropic-Organization-Id", "Set-Cookie"} {
			if v := resp.Header.Get(k); v != "" {
				t.Errorf("%s: %s = %q reached the agent", name, k, v)
			}
		}
		if name == "messages" && resp.Header.Get("Request-Id") != "req_1" {
			t.Errorf("request-id was dropped")
		}
	}
}

func TestCountTokensIsLoggedWithoutContent(t *testing.T) {
	h := newHarness(t, anthropicfake.Reply{Body: `{"input_tokens":42}`})
	body := `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"a-secret-prompt"}]}`
	h.post2("/v1/messages/count_tokens", body)
	var found map[string]any
	for _, l := range h.logs.lines(t) {
		if l["msg"] == "token count" {
			found = l
		}
	}
	if found == nil || num(found["status"]) != 200 || num(found["request_bytes"]) != int64(len(body)) || num(found["response_bytes"]) != int64(len(`{"input_tokens":42}`)) {
		t.Fatalf("token count log line %v in\n%s", found, h.logs.String())
	}
	if strings.Contains(h.logs.String(), "a-secret-prompt") {
		t.Error("the prompt reached the log")
	}
}

func TestCountTokensConcurrencyBounded(t *testing.T) {
	script := make([]anthropicfake.Reply, maxCountsInFlight)
	for i := range script {
		script[i] = anthropicfake.Reply{Hold: true}
	}
	h := newHarness(t, script...)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range maxCountsInFlight {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.DefaultClient.Do(h.request(ctx, "/v1/messages/count_tokens", `{"model":"claude-sonnet-5-5","messages":[]}`))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	waitFor(t, "the counts to reach the upstream", func() bool { return h.fake.Count() == maxCountsInFlight })
	resp, b := h.post2("/v1/messages/count_tokens", `{"model":"claude-sonnet-5-5","messages":[]}`)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("X-Should-Retry") == "false" {
		t.Errorf("an extra count: %d %s (x-should-retry %q), want a retryable 429", resp.StatusCode, b, resp.Header.Get("X-Should-Retry"))
	}
	if h.fake.Count() != maxCountsInFlight {
		t.Errorf("the extra count was forwarded")
	}
	cancel()
	wg.Wait()
}

// Every typed tool is in the log, though the first one names the refusal.
func TestToolTypesLogAllDisallowedTools(t *testing.T) {
	h := newHarness(t)
	refused(t, h, msg(sonnet, 10, `"tools":[{"name":"a","input_schema":{"type":"object"}},{"type":"web_search_20260209","name":"w"},{"type":"bash_20250124","name":"b"}]`), "tool type web_search_20260209")
	if tt := h.logs.lastCall(t)["tool_types"]; tt != "custom,web_search_20260209,bash_20250124" {
		t.Errorf("tool_types = %v", tt)
	}
}
