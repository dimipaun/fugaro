package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// A streamed response that ends before its terminal event is one fact about
// the call whatever it settles as (issue #1827): the model never finished
// its answer. These tests pin what the gateway says about it -- the stage
// report's count, which a runner reads to tell a lost stream from a failure
// about the work, and the warning that says which end ended the stream and
// how far it got, without carrying a byte of anyone's content.

// streamCutLine is the last "budget: stream ended before its terminal
// event" line, and the raw line itself (for the no-content checks).
func streamCutLine(t *testing.T, l *logBuf) (map[string]any, string) {
	t.Helper()
	var out map[string]any
	var raw string
	found := false
	for _, line := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if !strings.Contains(line, "stream ended before its terminal event") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		raw, found = line, true
	}
	if !found {
		t.Fatalf("no stream-cut warning logged; log:\n%s", l.String())
	}
	return out, raw
}

// A provider stream cut mid-body is counted on the stage and logged with the
// side that ended it (the upstream, not the agent) and how far it got: the
// bytes and events the agent was sent, and the last event's own name.
func TestProviderStreamCutIsLoggedAndCounted(t *testing.T) {
	reply := anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 3
	r := feeRouted(t, 0, nil, routedStage, dsRoute, reply)
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := r.gw.EndStage(); rep.StreamsCut != 1 {
		t.Errorf("stage counted %d cut streams, want 1", rep.StreamsCut)
	}
	line, _ := streamCutLine(t, r.logs)
	if line["stage"] != routedStage.Name || line["model"] != dsModel {
		t.Errorf("stage/model = %v/%v, want %s/%s", line["stage"], line["model"], routedStage.Name, dsModel)
	}
	if line["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200 (the upstream answered, then its body ended)", line["status"])
	}
	if line["terminal_event_seen"] != false {
		t.Errorf("terminal_event_seen = %v, want false: message_stop never arrived", line["terminal_event_seen"])
	}
	// StreamOK's third event is the text block's start: the stream was cut
	// after it, so three events were forwarded and that is the last one.
	if line["events"] != float64(3) || line["last_event"] != "content_block_start" {
		t.Errorf("events/last_event = %v/%v, want 3/content_block_start", line["events"], line["last_event"])
	}
	if b, _ := line["bytes"].(float64); b <= 0 {
		t.Errorf("bytes = %v, want the bytes the agent was sent", line["bytes"])
	}
	if ms, _ := line["ms"].(float64); ms < 0 {
		t.Errorf("ms = %v, want a duration", line["ms"])
	}
	if line["cut"] != cutUpstream {
		t.Errorf("cut = %v, want %q: the upstream's body ended, not the agent's read", line["cut"], cutUpstream)
	}
}

// The same on a Claude call: the diagnostic is not the provider route's, it
// is every streamed call's.
func TestClaudeStreamCutIsLoggedAndCounted(t *testing.T) {
	reply := anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 2
	h := newHarness(t, reply)
	if resp, b := h.post(msg(sonnet, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := h.gw.EndStage(); rep.StreamsCut != 1 {
		t.Errorf("stage counted %d cut streams, want 1", rep.StreamsCut)
	}
	line, _ := streamCutLine(t, h.logs)
	if line["cut"] != cutUpstream || line["last_event"] != "ping" {
		t.Errorf("cut/last_event = %v/%v, want upstream/ping", line["cut"], line["last_event"])
	}
	if line["terminal_event_seen"] != false {
		t.Error("terminal_event_seen is true for a stream cut before message_stop")
	}
}

// A whole stream is not a cut one: the count stays 0 and no warning is
// written, which is what makes the count mean "the model did not finish".
func TestWholeStreamIsNotACutOne(t *testing.T) {
	h := newHarness(t, anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5}))
	if resp, b := h.post(msg(sonnet, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := h.gw.EndStage(); rep.StreamsCut != 0 {
		t.Errorf("stage counted %d cut streams for a complete response, want 0", rep.StreamsCut)
	}
	if s := h.logs.String(); strings.Contains(s, "stream ended before its terminal event") {
		t.Errorf("a complete stream warned about a cut one:\n%s", s)
	}
}

// The line names the call and carries no content, no header and no
// credential: neither the run's own token (the caller's, scrubbed from every
// forwarded call) nor the provider's key, and not one byte of the request or
// of the response the upstream sent.
func TestStreamCutLineCarriesNoContent(t *testing.T) {
	const nonce = "NONCE-PROMPT-f3a1c9"
	reply := anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 3
	r := feeRouted(t, 0, nil, routedStage, dsRoute, reply)
	body := `{"model":"` + dsModel + `","max_tokens":1000,"stream":true,"messages":[{"role":"user","content":"` + nonce + `"}]}`
	if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	_, raw := streamCutLine(t, r.logs)
	for _, bad := range []string{nonce, providerKey, r.gw.Token(), "x-api-key", "authorization", "anthropic-version"} {
		if strings.Contains(raw, bad) {
			t.Errorf("the stream-cut line carries %q: %s", bad, raw)
		}
	}
}
