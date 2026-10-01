package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

var smallUsage = pricing.Usage{Input: 11, Output: 13}

// --- forwarding ---

func TestUpstreamAskedForIdentity(t *testing.T) {
	h := newHarness(t, anthropicfake.StreamOK(sonnet, smallUsage))
	h.post(msg(sonnet, 100, `"stream":true`), "Accept-Encoding", "gzip, br")
	if got := h.fake.Seen()[0].Header.Values("Accept-Encoding"); len(got) != 1 || got[0] != "identity" {
		t.Errorf("upstream Accept-Encoding = %q, want identity", got)
	}
}

func TestCompressedResponseSettledFromUsage(t *testing.T) {
	u := pricing.Usage{Input: 120, CacheRead: 30, Output: 40}
	for _, enc := range []string{"gzip", "deflate", "zstd"} {
		for _, stream := range []bool{true, false} {
			rep := anthropicfake.MessageOK(sonnet, u)
			body := msg(sonnet, 500)
			if stream {
				rep = anthropicfake.StreamOK(sonnet, u)
				body = msg(sonnet, 500, `"stream":true`)
			}
			rep.Encoding = enc
			h := newHarness(t, rep)
			resp, got := h.post(body, "Accept-Encoding", "gzip, br") // the client won't decode for us
			if resp.StatusCode != 200 {
				t.Fatalf("%s: status %d", enc, resp.StatusCode)
			}
			if ce := resp.Header.Get("Content-Encoding"); ce != "" {
				t.Errorf("%s: the client got Content-Encoding %q", enc, ce)
			}
			if want := `"usage"`; !strings.Contains(got, want) {
				t.Errorf("%s stream=%v: the client didn't get plain bytes: %q", enc, stream, got)
			}
			rep2 := h.gw.EndStage()
			if rep2.Used != cost(t, sonnet, u) || rep2.UsageUnparsed != 0 {
				t.Errorf("%s stream=%v: used %d (unparsed %d), want %d from usage", enc, stream, rep2.Used, rep2.UsageUnparsed, cost(t, sonnet, u))
			}
			if c := h.logs.lastCall(t); c["settled"] != "usage" {
				t.Errorf("%s: settled %v", enc, c["settled"])
			}
		}
	}
}

func TestBodyForwardedByteForByte(t *testing.T) {
	h := newHarness(t, anthropicfake.MessageOK(sonnet, smallUsage))
	body := "{ \"messages\" : [ {\"role\":\"user\",\"content\":\"h\u00e9llo \\u00e9 \\\"q\\\" \\n\"} ],\n\t\"max_tokens\":100 , \"model\":\"claude-sonnet-5-5\", \"metadata\":{\"user_id\":\"x\"}, \"temperature\": 1.0e0 }"
	if resp, got := h.post(body); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	if got := string(h.fake.Body(0)); got != body {
		t.Errorf("upstream body\n%q\nwant\n%q", got, body)
	}
	if cl := h.fake.Seen()[0].ContentLength; cl != int64(len(body)) {
		t.Errorf("upstream Content-Length %d, want %d", cl, len(body))
	}
}

func TestQueryStringForwarded(t *testing.T) {
	h := newHarness(t, anthropicfake.MessageOK(sonnet, smallUsage))
	resp, _ := h.do(h.request(context.Background(), "/v1/messages?beta=true", msg(sonnet, 100)))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	seen := h.fake.Seen()[0]
	if seen.URL.Path != "/v1/messages" || seen.URL.RawQuery != "beta=true" {
		t.Errorf("upstream URL %s", seen.URL)
	}
	if h.gw.EndStage().Used == 0 {
		t.Error("the call wasn't charged")
	}
}

func TestHeadersPassThrough(t *testing.T) {
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.Header = http.Header{
		"Retry-After":                       {"7"},
		"X-Should-Retry":                    {"true"},
		"Anthropic-Ratelimit-Unified-Reset": {"1767225600"},
		"Request-Id":                        {"req_fake_1"},
	}
	h := newHarness(t, rep)
	resp, _ := h.post(msg(sonnet, 100, `"stream":true`),
		"anthropic-beta", "interleaved-thinking-2025-05-14,context-1m-2025-08-07",
		"x-claude-code-session-id", "sess-1", "User-Agent", "claude-cli/2.1.283")
	seen := h.fake.Seen()[0]
	for k, want := range map[string]string{
		"anthropic-version":        "2023-06-01",
		"anthropic-beta":           "interleaved-thinking-2025-05-14,context-1m-2025-08-07",
		"x-claude-code-session-id": "sess-1",
		"User-Agent":               "claude-cli/2.1.283",
		"Content-Type":             "application/json",
	} {
		if got := seen.Header.Get(k); got != want {
			t.Errorf("upstream %s = %q, want %q", k, got, want)
		}
	}
	for k, want := range map[string]string{
		"Retry-After":                       "7",
		"X-Should-Retry":                    "true",
		"Anthropic-Ratelimit-Unified-Reset": "1767225600",
		"Request-Id":                        "req_fake_1",
		"Content-Type":                      "text/event-stream",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("client %s = %q, want %q", k, got, want)
		}
	}
}

func TestGatewayStripsClientCredentials(t *testing.T) {
	h := newHarness(t, anthropicfake.MessageOK(sonnet, smallUsage))
	h.post(msg(sonnet, 100), "Authorization", "Bearer "+h.gw.Token(), "Proxy-Authorization", "Basic "+h.gw.Token())
	seen := h.fake.Seen()[0]
	if got := seen.Header.Values("x-api-key"); len(got) != 1 || got[0] != testKey {
		t.Errorf("upstream x-api-key = %q, want the real key", got)
	}
	if seen.Header.Get("Authorization") != "" || seen.Header.Get("Proxy-Authorization") != "" {
		t.Error("a client credential reached the upstream")
	}
	for k, vs := range seen.Header {
		for _, v := range vs {
			if strings.Contains(v, h.gw.Token()) {
				t.Errorf("upstream header %s holds the run's token", k)
			}
		}
	}
}

func TestStreamNotBuffered(t *testing.T) {
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.EventDelay = 200 * time.Millisecond
	h := newHarness(t, rep)
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

func TestPingsPassThrough(t *testing.T) {
	h := newHarness(t, anthropicfake.StreamOK(sonnet, smallUsage))
	_, body := h.post(msg(sonnet, 100, `"stream":true`))
	if !strings.Contains(body, "event: ping\ndata: {\"type\": \"ping\"}\n\n") {
		t.Errorf("no ping in %q", body)
	}
}

func TestErrorBodyPassThrough(t *testing.T) {
	for _, c := range []struct {
		status int
		typ    string
	}{{429, "rate_limit_error"}, {529, "overloaded_error"}} {
		rep := anthropicfake.Error(c.status, c.typ, "slow down")
		rep.Header = http.Header{"Retry-After": {"3"}, "X-Should-Retry": {"true"}, "Request-Id": {"req_err"}}
		h := newHarness(t, rep)
		resp, body := h.post(msg(sonnet, 100))
		if resp.StatusCode != c.status {
			t.Errorf("status %d, want %d", resp.StatusCode, c.status)
		}
		if want := `{"type":"error","error":{"type":"` + c.typ + `","message":"slow down"}}`; body != want {
			t.Errorf("body %q, want %q", body, want)
		}
		if resp.Header.Get("Retry-After") != "3" || resp.Header.Get("X-Should-Retry") != "true" || resp.Header.Get("Request-Id") != "req_err" {
			t.Errorf("%d: headers %v", c.status, resp.Header)
		}
	}
}

func TestNonStreamingUsage(t *testing.T) {
	u := pricing.Usage{Input: 1000, CacheWrite5m: 200, CacheRead: 5000, Output: 321}
	h := newHarness(t, anthropicfake.MessageOK(sonnet, u))
	if resp, body := h.post(msg(sonnet, 1000, `"cache_control":{"type":"ephemeral"}`)); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	rep := h.gw.EndStage()
	if want := cost(t, sonnet, u); rep.Used != want || rep.ByModel[sonnet] != want || rep.Calls != 1 {
		t.Errorf("report %+v, want %d", rep, want)
	}
	if c := h.logs.lastCall(t); c["stream"] != false || c["settled"] != "usage" || num(c["out"]) != 321 {
		t.Errorf("log %v", c)
	}
}

func TestRequestTooLarge413(t *testing.T) {
	h := newHarness(t)
	big := msg(sonnet, 100, `"metadata":{"user_id":"`+strings.Repeat("x", MaxRequestBytes)+`"}`)
	resp, body := h.post(big)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", resp.StatusCode)
	}
	if typ, _ := apiError(t, body); typ != "request_too_large" {
		t.Errorf("error type %q", typ)
	}
	if h.fake.Count() != 0 {
		t.Error("the large body was forwarded")
	}
	// Exactly at the limit is read whole (checked without parsing 32 MiB).
	for n, want := range map[int]int{MaxRequestBytes: 0, MaxRequestBytes + 1: http.StatusRequestEntityTooLarge} {
		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Repeat("x", n)))
		b, status, _ := readBody(req)
		if status != want || (want == 0 && len(b) != n) {
			t.Errorf("%d bytes: status %d (read %d), want %d", n, status, len(b), want)
		}
	}
}

func TestMissingMaxTokens400NotForwarded(t *testing.T) {
	h := newHarness(t)
	for name, body := range map[string]string{
		"no max_tokens":     `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"hi"}]}`,
		"null max_tokens":   `{"model":"claude-sonnet-5-5","max_tokens":null,"messages":[]}`,
		"zero max_tokens":   msg(sonnet, 0),
		"negative":          msg(sonnet, -5),
		"string max_tokens": `{"model":"claude-sonnet-5-5","max_tokens":"100","messages":[]}`,
		"fractional":        `{"model":"claude-sonnet-5-5","max_tokens":10.5,"messages":[]}`,
		"not JSON":          `model=claude-sonnet-5-5`,
		"trailing data":     msg(sonnet, 100) + `{}`,
		"array":             `[` + msg(sonnet, 100) + `]`,
		"no model":          `{"max_tokens":100,"messages":[]}`,
		"model not string":  `{"model":5,"max_tokens":100,"messages":[]}`,
		"stream not bool":   msg(sonnet, 100, `"stream":"yes"`),
		"no messages":       `{"model":"claude-sonnet-5-5","max_tokens":100}`,
		"messages not list": `{"model":"claude-sonnet-5-5","max_tokens":100,"messages":{}}`,
		"empty":             ``,
	} {
		resp, b := h.post(body)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
			continue
		}
		if typ, m := apiError(t, b); typ != "invalid_request_error" || !strings.HasPrefix(m, "fugaro: ") {
			t.Errorf("%s: %s %q", name, typ, m)
		}
	}
	if h.fake.Count() != 0 {
		t.Error("a malformed request was forwarded")
	}
	if v := h.gw.EndStage().Violations; len(v) != 0 {
		t.Errorf("a malformed body counted as a violation: %q", v)
	}
}

func TestDuplicateKeysRefused(t *testing.T) {
	h := newHarness(t)
	for name, body := range map[string]string{
		"model twice":      `{"model":"claude-opus-5","model":"claude-sonnet-5-5","max_tokens":100,"messages":[]}`,
		"max_tokens twice": `{"model":"claude-sonnet-5-5","max_tokens":100000,"max_tokens":1,"messages":[]}`,
		"another case":     `{"model":"claude-sonnet-5-5","Model":"claude-opus-5","max_tokens":1,"messages":[]}`,
		"escaped twin":     `{"model":"claude-sonnet-5-5","max_tokens":1,"max\u005ftokens":100000,"messages":[]}`,
		"nested twice":     msgBlocks(sonnet, 100, `{"type":"image","source":{"type":"base64","type":"url","url":"x"}}`),
	} {
		if resp, _ := h.post(body); resp.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
	if h.fake.Count() != 0 {
		t.Error("an ambiguous body was forwarded")
	}
}

// --- request shapes ---

// refused posts body and checks it is refused with a violation that names
// what.
func refused(t *testing.T, h *harness, body, what string) {
	t.Helper()
	before := h.fake.Count()
	resp, b := h.post(body)
	if resp.StatusCode != 400 {
		t.Errorf("%s: status %d, want 400", what, resp.StatusCode)
		return
	}
	typ, m := apiError(t, b)
	if typ != "invalid_request_error" || !strings.HasPrefix(m, "fugaro: ") || !strings.Contains(m, what) {
		t.Errorf("%s: error %s %q", what, typ, m)
	}
	if resp.Header.Get("X-Should-Retry") != "false" {
		t.Errorf("%s: x-should-retry %q", what, resp.Header.Get("X-Should-Retry"))
	}
	if h.fake.Count() != before {
		t.Errorf("%s: forwarded", what)
	}
	rep := h.gw.EndStage()
	found := false
	for _, v := range rep.Violations {
		found = found || strings.Contains(v, what)
	}
	if !found {
		t.Errorf("%s: no violation in %q", what, rep.Violations)
	}
	h.gw.BeginStage(defaultStage)
}

func allowed(t *testing.T, h *harness, body, what string) {
	t.Helper()
	if resp, b := h.post(body); resp.StatusCode != 200 {
		t.Errorf("%s: status %d: %s", what, resp.StatusCode, b)
	}
}

func okReplies(n int) []anthropicfake.Reply {
	out := make([]anthropicfake.Reply, n)
	for i := range out {
		out[i] = anthropicfake.MessageOK(sonnet, smallUsage)
	}
	return out
}

func TestFastModeRefused(t *testing.T) {
	h := newHarness(t, okReplies(1)...)
	_, b := h.post(msg(sonnet, 100, `"speed":"fast"`))
	if _, m := apiError(t, b); m != `fugaro: speed "fast" is not allowed (it isn't priced by the budget)` {
		t.Errorf("message %q", m)
	}
	h.gw.EndStage()
	h.gw.BeginStage(defaultStage)
	refused(t, h, msg(sonnet, 100, `"speed":"fast"`), `speed "fast" is not allowed`)
	refused(t, h, msg(sonnet, 100, `"speed":null`), `speed`)
	allowed(t, h, msg(sonnet, 100, `"speed":"standard"`), "standard speed")
}

func TestInferenceGeoRefused(t *testing.T) {
	h := newHarness(t, okReplies(1)...)
	refused(t, h, msg(sonnet, 100, `"inference_geo":"us"`), `inference_geo "us" is not allowed`)
	refused(t, h, msg(sonnet, 100, `"inference_geo":"global"`), `inference_geo "global" is not allowed`)
	allowed(t, h, msg(sonnet, 100, `"inference_geo":""`), "the default geo")
}

func TestServiceTierRefused(t *testing.T) {
	h := newHarness(t, okReplies(2)...)
	refused(t, h, msg(sonnet, 100, `"service_tier":"priority"`), `service_tier "priority" is not allowed`)
	refused(t, h, msg(sonnet, 100, `"service_tier":"flex"`), `service_tier "flex"`)
	allowed(t, h, msg(sonnet, 100, `"service_tier":"auto"`), "auto")
	allowed(t, h, msg(sonnet, 100, `"service_tier":"standard_only"`), "standard_only")
}

func TestRefusedShapes(t *testing.T) {
	for _, c := range []struct{ name, body, what string }{
		{"image url", msgBlocks(sonnet, 100, `{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}}`), "image source url"},
		{"image file", msgBlocks(sonnet, 100, `{"type":"image","source":{"type":"file","file_id":"file_1"}}`), "image source file"},
		{"document url", msgBlocks(sonnet, 100, `{"type":"document","source":{"type":"url","url":"https://example.invalid/a.pdf"}}`), "document source url"},
		{"document file", msgBlocks(sonnet, 100, `{"type":"document","source":{"type":"file","file_id":"file_1"}}`), "document source file"},
		{"url image in a tool result", msgBlocks(sonnet, 100, `{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}}]}`), "image source url"},
		{"url image in the system prompt", `{"model":"claude-sonnet-5-5","max_tokens":100,"system":[{"type":"image","source":{"type":"url","url":"x"}}],"messages":[]}`, "image source url"},
		{"unknown block", msgBlocks(sonnet, 100, `{"type":"container_upload","file_id":"file_1"}`), "content block type container_upload"},
		{"mcp_servers", msg(sonnet, 100, `"mcp_servers":[{"type":"url","url":"https://example.invalid/mcp","name":"x"}]`), "mcp_servers"},
		{"empty mcp_servers", msg(sonnet, 100, `"mcp_servers":[]`), "mcp_servers"},
		{"container", msg(sonnet, 100, `"container":"container_1"`), "container"},
		{"fallbacks", msg(sonnet, 100, `"fallbacks":[{"model":"claude-opus-5"}]`), "fallbacks"},
		{"compaction", msg(sonnet, 100, `"context_management":{"edits":[{"type":"compact_20260112"}]}`), "context_management edit compact_20260112"},
		{"unknown edit", msg(sonnet, 100, `"context_management":{"edits":[{"type":"future_edit_20990101"}]}`), "context_management edit future_edit_20990101"},
		{"a refused edit beside an allowed one", msg(sonnet, 100, `"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"compact_20260112"}]}`), "compact_20260112"},
		{"edit without a type", msg(sonnet, 100, `"context_management":{"edits":[{}]}`), "context_management edit"},
		{"other context_management keys", msg(sonnet, 100, `"context_management":{"edits":[],"compaction":{}}`), "context_management.compaction"},
		{"context_management not an object", msg(sonnet, 100, `"context_management":"auto"`), "context_management"},
		{"edits not a list", msg(sonnet, 100, `"context_management":{"edits":{"type":"clear_thinking_20251015"}}`), "context_management"},
		{"max_tokens above the model's maximum", msg(sonnet, 128001), "max_tokens 128001 for model claude-sonnet-5-5 is above the model's maximum 128000"},
		{"max_tokens above the background model's maximum", msg(haiku, 64001), "above the model's maximum 64000"},
		{"tools not a list", msg(sonnet, 100, `"tools":{"type":"custom"}`), "tools"},
		{"tool type not a string", msg(sonnet, 100, `"tools":[{"type":5,"name":"x"}]`), "tool type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			refused(t, newHarness(t), c.body, c.what)
		})
	}
}

func TestContextManagementClearEditsAllowed(t *testing.T) {
	h := newHarness(t, okReplies(4)...)
	allowed(t, h, msg(sonnet, 100, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`), "clear_thinking")
	allowed(t, h, msg(sonnet, 100, `"context_management":{"edits":[{"type":"clear_tool_uses_20250919","trigger":{"type":"input_tokens","value":30000}}]}`), "clear_tool_uses")
	allowed(t, h, msg(sonnet, 100, `"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"clear_tool_uses_20250919"}]}`), "both")
	allowed(t, h, msg(sonnet, 100, `"context_management":{"edits":[]}`), "no edits")
}

func TestToolTypeAllowList(t *testing.T) {
	tool := func(typ string) string {
		if typ == "" {
			return `"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object","properties":{"type":{"type":"string"}}}}]`
		}
		return `"tools":[{"type":"` + typ + `","name":"t"}]`
	}
	h := newHarness(t, okReplies(2)...)
	allowed(t, h, msg(sonnet, 100, tool("")), "a tool without a type")
	allowed(t, h, msg(sonnet, 100, tool("custom")), "a custom tool")
	for _, typ := range []string{
		"web_search_20260209", "web_fetch_20260209", "code_execution_20260521", "tool_search_tool_regex_20251119",
		"bash_20250124", "text_editor_20250728", "memory_20250818", "future_tool_20990101",
	} {
		refused(t, h, msg(sonnet, 100, tool(typ)), "tool type "+typ+" is not allowed")
	}
}

// reservedOf is the reservation logged for the last call.
func reservedOf(t *testing.T, h *harness) pricing.Micros {
	t.Helper()
	return pricing.Micros(num(h.logs.lastCall(t)["reserved_micros"]))
}

func TestPDFBlockReservedAtContext(t *testing.T) {
	h := newHarness(t, okReplies(1)...)
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 tiny"))
	body := msgBlocks(sonnet, 100, `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"`+pdf+`"}}`)
	allowed(t, h, body, "a base64 PDF")
	want := model(t, sonnet).WorstCase(pricing.Request{BodyBytes: int64(len(body)), HasPDF: true, MaxTokens: 100})
	if got := reservedOf(t, h); got != want {
		t.Errorf("reserved %d, want %d (the whole context window)", got, want)
	}
	if want <= worst(t, sonnet, body, 100, "") {
		t.Error("a PDF reserves no more than its bytes")
	}
}

func TestImagesReservedAtCeiling(t *testing.T) {
	h := newHarness(t, okReplies(2)...)
	png := base64.StdEncoding.EncodeToString(make([]byte, 225)) // 300 base64 bytes
	if len(png) != 300 {
		t.Fatalf("png is %d bytes", len(png))
	}
	img := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + png + `"}}`
	body := msgBlocks(sonnet, 100, img)
	allowed(t, h, body, "a base64 image")
	want := model(t, sonnet).WorstCase(pricing.Request{BodyBytes: int64(len(body)), ImageCount: 1, MaxTokens: 100})
	if got := reservedOf(t, h); got != want {
		t.Errorf("reserved %d, want %d (one image at its ceiling)", got, want)
	}
	// Images inside a tool result count too.
	body = msgBlocks(sonnet, 100, img+`,{"type":"tool_result","tool_use_id":"t","content":[`+img+`,{"type":"text","text":"x"}]}`)
	allowed(t, h, body, "two images")
	want = model(t, sonnet).WorstCase(pricing.Request{BodyBytes: int64(len(body)), ImageCount: 2, MaxTokens: 100})
	if got := reservedOf(t, h); got != want {
		t.Errorf("reserved %d, want %d (two images)", got, want)
	}
}

func TestImagesRefusedWithoutCeiling(t *testing.T) {
	table, err := pricing.Embedded().With(pricing.Overrides{"claude-custom-1": {InputPerM: 1, OutputPerM: 2, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWith(t, func(o *Options) { o.Prices = table })
	h.gw.EndStage()
	h.gw.BeginStage(Stage{Name: "implement", Model: "claude-custom-1", Background: haiku})
	png := base64.StdEncoding.EncodeToString(make([]byte, 30))
	body := msgBlocks("claude-custom-1", 100, `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+png+`"}}`)
	before := h.fake.Count()
	resp, b := h.post(body)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, m := apiError(t, b); m != "fugaro: images aren't priced for model claude-custom-1" {
		t.Errorf("message %q", m)
	}
	if h.fake.Count() != before {
		t.Error("forwarded")
	}
	if v := h.gw.EndStage().Violations; len(v) != 1 {
		t.Errorf("violations %q", v)
	}
}

func TestCacheTTLFromRequest(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"none", msg(sonnet, 10), ""},
		{"top-level automatic", msg(sonnet, 10, `"cache_control":{"type":"ephemeral"}`), "5m"},
		{"block without ttl", msgBlocks(sonnet, 10, `{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}`), "5m"},
		{"block 5m", msgBlocks(sonnet, 10, `{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"5m"}}`), "5m"},
		{"block 1h", msgBlocks(sonnet, 10, `{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}`), "1h"},
		{"1h on a tool", msg(sonnet, 10, `"tools":[{"name":"x","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"1h"}}]`), "1h"},
		{"1h in the system prompt", `{"model":"claude-sonnet-5-5","max_tokens":10,"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[]}`, "1h"},
		{"5m and 1h", msgBlocks(sonnet, 10, `{"type":"text","text":"x","cache_control":{"type":"ephemeral"}},{"type":"text","text":"y","cache_control":{"type":"ephemeral","ttl":"1h"}}`), "1h"},
		{"unknown ttl", msgBlocks(sonnet, 10, `{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"24h"}}`), "1h"},
		{"in a tool result", msgBlocks(sonnet, 10, `{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}`), "5m"},
		{"null", msg(sonnet, 10, `"cache_control":null`), ""},
	} {
		p, err := parseRequest([]byte(c.body), "")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if p.cacheTTL != c.want {
			t.Errorf("%s: cacheTTL %q, want %q", c.name, p.cacheTTL, c.want)
		}
	}
}
