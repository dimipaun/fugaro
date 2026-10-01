package gateway

import (
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

var callFields = []string{
	"agent_id", "cache_read", "cache_write_1h", "cache_write_5m", "charged_micros", "in", "level", "max_tokens", "model",
	"msg", "out", "priced_as", "reserved_micros", "serving_model", "session_id", "settled", "stage", "status",
	"stream", "time", "tool_types", "web_searches",
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestGatewayLogFields(t *testing.T) {
	u := pricing.Usage{Input: 10, CacheWrite5m: 2, CacheWrite1h: 3, CacheRead: 4, Output: 5}
	h := newHarness(t, anthropicfake.StreamOK(sonnet, u), anthropicfake.Error(529, "overloaded_error", "busy"))
	secretPrompt := "the prompt text must not be logged"
	body := msgBlocks(sonnet, 100, `{"type":"text","text":"`+secretPrompt+`"}`, `"stream":true`, `"cache_control":{"type":"ephemeral","ttl":"1h"}`)
	h.post(body, "x-claude-code-session-id", "sess-42", "x-claude-code-agent-id", "agent-7", "anthropic-beta", "beta-header-value")
	h.post(body, "x-claude-code-session-id", "sess-42")

	calls := h.logs.calls(t)
	if len(calls) != 2 {
		t.Fatalf("%d model call lines, want 2", len(calls))
	}
	ok, failed := calls[0], calls[1]
	if got := strings.Join(keys(ok), ","); got != strings.Join(callFields, ",") {
		t.Errorf("fields\n%s\nwant\n%s", got, strings.Join(callFields, ","))
	}
	want := map[string]any{
		"stage": "implement", "model": sonnet, "serving_model": sonnet, "status": 200.0, "stream": true,
		"in": 10.0, "cache_write_5m": 2.0, "cache_write_1h": 3.0, "cache_read": 4.0, "out": 5.0, "web_searches": 0.0,
		"priced_as": "table", "settled": "usage", "session_id": "sess-42", "agent_id": "agent-7",
		"max_tokens": 100.0, "tool_types": "",
		"charged_micros":  float64(cost(t, sonnet, u)),
		"reserved_micros": float64(worst(t, sonnet, body, 100, "1h")),
	}
	for k, v := range want {
		if ok[k] != v {
			t.Errorf("%s = %v, want %v", k, ok[k], v)
		}
	}
	withErr := append(append([]string{}, callFields...), "error_type")
	sort.Strings(withErr)
	if got := strings.Join(keys(failed), ","); got != strings.Join(withErr, ",") {
		t.Errorf("error line fields\n%s\nwant\n%s", got, strings.Join(withErr, ","))
	}
	if failed["error_type"] != "overloaded_error" || failed["settled"] != "zero" || failed["agent_id"] != "" {
		t.Errorf("error line %v", failed)
	}
	all := h.logs.String()
	for _, s := range []string{secretPrompt, "busy", "beta-header-value", "2023-06-01"} {
		if strings.Contains(all, s) {
			t.Errorf("the log holds %q", s)
		}
	}
}

func TestGatewayLogsNoSecrets(t *testing.T) {
	echo := anthropicfake.Error(401, "authentication_error", "invalid x-api-key: "+testKey[:20]+"...")
	h := newHarness(t, echo, anthropicfake.StreamOK(sonnet, smallUsage))
	resp, body := h.post(msg(sonnet, 100))
	if resp.StatusCode != 401 || !strings.Contains(body, testKey[:20]) {
		t.Fatalf("the upstream's error didn't pass through: %d %q", resp.StatusCode, body)
	}
	h.post(msg(sonnet, 100, `"stream":true`))
	h.post(msg(opus, 100)) // a refusal
	h.gw.EndStage()
	all := h.logs.String()
	for name, s := range map[string]string{"the key": testKey, "the key's prefix": testKey[:20], "the run's token": h.gw.Token(), "the token's prefix": h.gw.Token()[:16]} {
		if strings.Contains(all, s) {
			t.Errorf("the log holds %s", name)
		}
	}
	if !strings.Contains(all, `"error_type":"authentication_error"`) {
		t.Errorf("the log lacks the error type:\n%s", all)
	}
}

func TestLogValuesBounded(t *testing.T) {
	if got := logValue(strings.Repeat("a", 500)); len(got) > maxLogValue {
		t.Errorf("logValue kept %d bytes", len(got))
	}
	if got := errorTypeValue("overloaded_error"); got != "overloaded_error" {
		t.Errorf("errorTypeValue = %q", got)
	}
	if got := errorTypeValue("sk-ant-api03-xyz"); got != "unrecognized" {
		t.Errorf("errorTypeValue of an odd type = %q", got)
	}
}
