package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// withUsage returns ev with extra fields set in its usage (message_start's
// message.usage, or message_delta's usage).
func withUsage(t *testing.T, ev anthropicfake.Event, fields map[string]any) anthropicfake.Event {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
		t.Fatal(err)
	}
	usage, _ := m["usage"].(map[string]any)
	if msg, ok := m["message"].(map[string]any); ok {
		usage = msg["usage"].(map[string]any)
	}
	for k, v := range fields {
		usage[k] = v
	}
	b, _ := json.Marshal(m)
	ev.Data = string(b)
	return ev
}

func maxRates() pricing.Rates { return pricing.Embedded().Max() }

// --- settlement ---

func TestNon2xxChargesZero(t *testing.T) {
	for _, c := range []struct {
		status int
		typ    string
	}{{400, "invalid_request_error"}, {429, "rate_limit_error"}, {500, "api_error"}, {529, "overloaded_error"}} {
		h := newHarness(t, anthropicfake.Error(c.status, c.typ, "no"))
		resp, _ := h.post(msg(sonnet, 1000, `"stream":true`))
		if resp.StatusCode != c.status {
			t.Errorf("status %d, want %d", resp.StatusCode, c.status)
		}
		rep := h.gw.EndStage()
		if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 || rep.Used != 0 || rep.Unreconciled != 0 {
			t.Errorf("%d: ledger %+v report %+v, want nothing charged", c.status, l, rep)
		}
		call := h.logs.lastCall(t)
		if call["settled"] != "zero" || num(call["charged_micros"]) != 0 || call["error_type"] != c.typ || num(call["status"]) != int64(c.status) {
			t.Errorf("%d: log %v", c.status, call)
		}
	}
}

func TestUnparsed2xxChargesReservation(t *testing.T) {
	cut := anthropicfake.Reply{Events: []anthropicfake.Event{anthropicfake.Ping, anthropicfake.Ping}, CutAfter: 1}
	br := anthropicfake.StreamOK(sonnet, smallUsage)
	br.Encoding = "br"
	for _, c := range []struct {
		name   string
		reply  anthropicfake.Reply
		stream bool
	}{
		{"malformed JSON", anthropicfake.Reply{Body: `{"type":"message","usage":{"input_tokens":`}, false},
		{"JSON without usage", anthropicfake.Reply{Body: `{"type":"message","model":"claude-sonnet-5-5"}`}, false},
		{"not JSON", anthropicfake.Reply{Body: `ok`, Header: http.Header{"Content-Type": {"text/plain"}}}, false},
		{"stream cut before message_start", cut, true},
		{"stream ends before message_start", anthropicfake.Reply{Events: []anthropicfake.Event{anthropicfake.Ping, anthropicfake.Stop}}, true},
		{"br", br, true},
		{"gzip then br", func() anthropicfake.Reply {
			r := anthropicfake.StreamOK(sonnet, smallUsage)
			r.Encoding = "gzip, br"
			return r
		}(), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.reply)
			body := msg(sonnet, 1000)
			if c.stream {
				body = msg(sonnet, 1000, `"stream":true`)
			}
			resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body, "Accept-Encoding", "identity"))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			w := worst(t, sonnet, body, 1000, "")
			rep := h.gw.EndStage()
			if rep.Used != w || rep.Unreconciled != w || rep.UsageUnparsed != 1 {
				t.Errorf("report %+v, want the reservation %d, unreconciled, one unparsed", rep, w)
			}
			if call := h.logs.lastCall(t); call["settled"] != "reserved" || num(call["charged_micros"]) != int64(w) {
				t.Errorf("log %v", call)
			}
		})
	}
}

func TestNoStatusAfterBodyWrittenChargesReservation(t *testing.T) {
	h := newHarness(t, anthropicfake.Reply{DropBeforeHeaders: true})
	body := msg(sonnet, 1000, `"stream":true`)
	resp, b := h.post(body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
	if typ, _ := apiError(t, b); typ != "api_error" {
		t.Errorf("error type %q", typ)
	}
	w := worst(t, sonnet, body, 1000, "")
	rep := h.gw.EndStage()
	if rep.Used != w || rep.Unreconciled != w {
		t.Errorf("report %+v, want the reservation %d", rep, w)
	}
	if call := h.logs.lastCall(t); call["settled"] != "reserved" || num(call["status"]) != 0 {
		t.Errorf("log %v", call)
	}
}

func TestNoStatusBeforeWriteChargesZero(t *testing.T) {
	h := newHarnessWith(t, func(o *Options) { o.Upstream.BaseURL = closedPort(t) })
	resp, _ := h.post(msg(sonnet, 1000))
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
	if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Errorf("ledger %+v, want nothing charged", l)
	}
	if call := h.logs.lastCall(t); call["settled"] != "zero" {
		t.Errorf("log %v", call)
	}
}

func TestClientCancelBeforeHeadersChargesReservation(t *testing.T) {
	h := newHarness(t, anthropicfake.Reply{Hold: true})
	body := msg(sonnet, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(h.request(ctx, "/v1/messages", body))
		if err == nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	waitFor(t, "the upstream to get the body", func() bool { return h.fake.Count() == 1 })
	cancel()
	<-errc
	select {
	case <-h.fake.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request wasn't cancelled")
	}
	rep := h.gw.EndStage()
	if w := worst(t, sonnet, body, 1000, ""); rep.Used != w || rep.Unreconciled != w {
		t.Errorf("report %+v, want the reservation %d", rep, w)
	}
	if l := h.gw.Ledger(); l.Reserved != 0 {
		t.Errorf("reserved %d", l.Reserved)
	}
}

func TestCloseBeforeHeadersChargesReservation(t *testing.T) {
	h := newHarness(t, anthropicfake.Reply{Hold: true})
	body := msg(sonnet, 1000)
	go func() {
		resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
		if err == nil {
			resp.Body.Close()
		}
	}()
	waitFor(t, "the upstream to get the body", func() bool { return h.fake.Count() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.gw.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if l := h.gw.Ledger(); l.Used != worst(t, sonnet, body, 1000, "") || l.Reserved != 0 {
		t.Errorf("ledger %+v, want the reservation %d", l, worst(t, sonnet, body, 1000, ""))
	}
}

func TestPartialChargesInputAndCache(t *testing.T) {
	start := pricing.Usage{Input: 200, CacheWrite5m: 40, CacheRead: 3000}
	ev := []anthropicfake.Event{anthropicfake.StartEvent(sonnet, start), anthropicfake.Ping}
	ev = append(ev, anthropicfake.TextEvents("partial")...)
	h := newHarness(t, anthropicfake.Reply{Events: ev, CutAfter: 3})
	body := msg(sonnet, 2000, `"stream":true`, `"cache_control":{"type":"ephemeral"}`)
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(got), "message_start") {
		t.Errorf("the client didn't get the partial stream: %q", got)
	}
	charged := start
	charged.Output = 2000
	reported := start
	reported.Output = 1
	rep := h.gw.EndStage()
	want := cost(t, sonnet, charged)
	if rep.Used != want {
		t.Errorf("used %d, want %d (input and cache from message_start, plus the reserved output)", rep.Used, want)
	}
	if rep.Unreconciled != want-cost(t, sonnet, reported) {
		t.Errorf("unreconciled %d, want %d", rep.Unreconciled, want-cost(t, sonnet, reported))
	}
	if call := h.logs.lastCall(t); call["settled"] != "partial" || num(call["cache_read"]) != 3000 || num(call["cache_write_5m"]) != 40 {
		t.Errorf("log %v", call)
	}
}

func TestDisconnectAfterStartChargesReservedOutput(t *testing.T) {
	u := pricing.Usage{Input: 500, Output: 300}
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.CutAfter = 1
	h := newHarness(t, rep)
	body := msg(sonnet, 4000, `"stream":true`)
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	r := h.gw.EndStage()
	want := cost(t, sonnet, pricing.Usage{Input: 500, Output: 4000})
	if r.Used != want || r.Unreconciled != want-cost(t, sonnet, pricing.Usage{Input: 500, Output: 1}) || r.Unreconciled <= 0 {
		t.Errorf("report %+v, want %d with the output unreconciled", r, want)
	}
}

func TestSettleCompleteStream(t *testing.T) {
	u := pricing.Usage{Input: 120, CacheWrite5m: 30, CacheWrite1h: 5, CacheRead: 400, Output: 55}
	h := newHarness(t, anthropicfake.StreamOK(sonnet, u))
	resp, body := h.post(msg(sonnet, 1000, `"stream":true`, `"cache_control":{"type":"ephemeral","ttl":"1h"}`))
	if resp.StatusCode != 200 || !strings.Contains(body, "message_stop") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	rep := h.gw.EndStage()
	want := cost(t, sonnet, u)
	if rep.Calls != 1 || rep.Used != want || rep.ByModel[sonnet] != want || rep.Unreconciled != 0 || rep.Overrun != 0 || rep.UsageUnparsed != 0 {
		t.Errorf("report %+v, want %d", rep, want)
	}
	if rep.Tokens != u.Input+u.CacheWrite5m+u.CacheWrite1h+u.CacheRead+u.Output {
		t.Errorf("tokens %d", rep.Tokens)
	}
	if l := h.gw.Ledger(); l.Used != want || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
	call := h.logs.lastCall(t)
	if call["settled"] != "usage" || call["priced_as"] != "table" || num(call["charged_micros"]) != int64(want) || call["serving_model"] != sonnet {
		t.Errorf("log %v", call)
	}
}

func TestUnsplitCacheWritesAtTheRequestTTL(t *testing.T) {
	// A response that gives only cache_creation_input_tokens: with a 1-hour
	// TTL in the request, the writes are priced at the 1-hour rate.
	data := `{"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":1}}}`
	delta := `{"type":"message_delta","usage":{"output_tokens":20}}`
	rep := anthropicfake.Reply{Events: []anthropicfake.Event{{Name: "message_start", Data: data}, {Name: "message_delta", Data: delta}, anthropicfake.Stop}}
	for ttl, u := range map[string]pricing.Usage{
		`"ttl":"1h"`: {Input: 10, CacheWrite1h: 1000, Output: 20},
		`"ttl":"5m"`: {Input: 10, CacheWrite5m: 1000, Output: 20},
	} {
		h := newHarness(t, rep)
		h.post(msg(sonnet, 100, `"stream":true`, `"cache_control":{"type":"ephemeral",`+ttl+`}`))
		if got := h.gw.EndStage().Used; got != cost(t, sonnet, u) {
			t.Errorf("%s: used %d, want %d", ttl, got, cost(t, sonnet, u))
		}
	}
}

func TestMessageDeltaCumulativeLastWins(t *testing.T) {
	ev := []anthropicfake.Event{
		anthropicfake.StartEvent(sonnet, pricing.Usage{Input: 100}),
		anthropicfake.DeltaEvent(pricing.Usage{Input: 100, Output: 10}),
		{Name: "message_delta", Data: `{"type":"message_delta","usage":{"output_tokens":25}}`},
		{Name: "message_delta", Data: `{"type":"message_delta","usage":{"input_tokens":150,"cache_read_input_tokens":null,"output_tokens":40}}`},
		anthropicfake.Stop,
	}
	h := newHarness(t, anthropicfake.Reply{Events: ev})
	h.post(msg(sonnet, 1000, `"stream":true`))
	want := cost(t, sonnet, pricing.Usage{Input: 150, Output: 40})
	if got := h.gw.EndStage().Used; got != want {
		t.Errorf("used %d, want %d (the last value of each field, not a sum)", got, want)
	}
}

func TestServiceTierPriorityIsSurprise(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"service_tier": "priority"})
	h := newHarness(t, rep)
	h.post(msg(sonnet, 1000, `"stream":true`))
	r := h.gw.EndStage()
	want := maxRates().Cost(u) * pricing.SurpriseMultiplier
	if r.Used != want {
		t.Errorf("used %d, want %d (the maximum rates, doubled)", r.Used, want)
	}
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], `service_tier "priority"`) {
		t.Errorf("violations %q", r.Violations)
	}
	if call := h.logs.lastCall(t); call["priced_as"] != "surprise" {
		t.Errorf("log %v", call)
	}
}

func TestInferenceGeoDefaultAllowed(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	for _, geo := range []any{"", "global", nil} {
		rep := anthropicfake.StreamOK(sonnet, u)
		if geo != nil {
			rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"inference_geo": geo})
		}
		h := newHarness(t, rep)
		h.post(msg(sonnet, 1000, `"stream":true`))
		r := h.gw.EndStage()
		if r.Used != cost(t, sonnet, u) || len(r.Violations) != 0 {
			t.Errorf("geo %v: report %+v", geo, r)
		}
	}
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"inference_geo": "us"})
	h := newHarness(t, rep)
	h.post(msg(sonnet, 1000, `"stream":true`))
	r := h.gw.EndStage()
	if r.Used != maxRates().Cost(u)*pricing.SurpriseMultiplier || len(r.Violations) != 1 || !strings.Contains(r.Violations[0], `inference_geo "us"`) {
		t.Errorf("geo us: report %+v", r)
	}
}

func TestUsageSpeedPricedAsSurprise(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	rep := anthropicfake.StreamOK(sonnet, u)
	d := len(rep.Events) - 2
	rep.Events[d] = withUsage(t, rep.Events[d], map[string]any{"speed": "fast"})
	h := newHarness(t, rep)
	h.post(msg(sonnet, 1000, `"stream":true`))
	r := h.gw.EndStage()
	if want := maxRates().Cost(u) * pricing.SurpriseMultiplier; r.Used != want {
		t.Errorf("used %d, want %d", r.Used, want)
	}
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], `speed "fast"`) {
		t.Errorf("violations %q", r.Violations)
	}
	// A non-streaming reply with usage.speed standard is fine.
	m := anthropicfake.MessageOK(sonnet, u)
	m.Body = strings.Replace(m.Body, `"service_tier":"standard"`, `"service_tier":"standard","speed":"standard"`, 1)
	h = newHarness(t, m)
	h.post(msg(sonnet, 1000))
	if r := h.gw.EndStage(); r.Used != cost(t, sonnet, u) || len(r.Violations) != 0 {
		t.Errorf("standard speed: %+v", r)
	}
}

func TestClientCancelCancelsUpstream(t *testing.T) {
	rep := anthropicfake.StreamOK(sonnet, smallUsage)
	rep.EventDelay = 300 * time.Millisecond
	h := newHarness(t, rep)
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := http.DefaultClient.Do(h.request(ctx, "/v1/messages", msg(sonnet, 1000, `"stream":true`)))
	if err != nil {
		t.Fatal(err)
	}
	readEvent(t, bufio.NewReader(resp.Body))
	cancel()
	resp.Body.Close()
	select {
	case <-h.fake.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request went on after the client left")
	}
	r := h.gw.EndStage()
	if want := cost(t, sonnet, pricing.Usage{Input: smallUsage.Input, Output: 1000}); r.Used != want {
		t.Errorf("used %d, want %d (partial)", r.Used, want)
	}
}

func TestRetryAfter529ChargesOnce(t *testing.T) {
	u := pricing.Usage{Input: 900, Output: 90}
	h := newHarness(t, anthropicfake.Error(529, "overloaded_error", "busy"), anthropicfake.StreamOK(sonnet, u))
	body := msg(sonnet, 1000, `"stream":true`)
	if resp, _ := h.post(body); resp.StatusCode != 529 {
		t.Fatalf("first: %d", resp.StatusCode)
	}
	if resp, _ := h.post(body); resp.StatusCode != 200 {
		t.Fatalf("retry: %d", resp.StatusCode)
	}
	r := h.gw.EndStage()
	if r.Used != cost(t, sonnet, u) || r.Calls != 2 {
		t.Errorf("report %+v, want one charge of %d over two calls", r, cost(t, sonnet, u))
	}
}

func TestServingModelPricesCall(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	h := newHarness(t, anthropicfake.StreamOK("claude-opus-5-5", u))
	h.post(msg(sonnet, 1000, `"stream":true`))
	r := h.gw.EndStage()
	if want := cost(t, "claude-opus-5-5", u); r.Used != want || r.ByModel["claude-opus-5-5"] != want {
		t.Errorf("report %+v, want %d at the serving model's rates", r, want)
	}
	if call := h.logs.lastCall(t); call["model"] != sonnet || call["serving_model"] != "claude-opus-5-5" || call["priced_as"] != "table" {
		t.Errorf("log %v", call)
	}
}

func TestUnknownServingModelPricedAtMax(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 1000}
	h := newHarness(t, anthropicfake.StreamOK("claude-mystery-9", u))
	body := msg(sonnet, 1000, `"stream":true`)
	h.post(body)
	r := h.gw.EndStage()
	want := maxRates().Cost(u)
	w := worst(t, sonnet, body, 1000, "")
	if want <= w {
		t.Fatalf("test usage doesn't overrun: %d <= %d", want, w)
	}
	if r.Used != want || r.Overrun != want-w || r.ByModel["claude-mystery-9"] != want {
		t.Errorf("report %+v, want %d with overrun %d", r, want, want-w)
	}
	if call := h.logs.lastCall(t); call["priced_as"] != "max" {
		t.Errorf("log %v", call)
	}
}

func TestCacheControl1hReservesAt2x(t *testing.T) {
	h := newHarness(t, okReplies(2)...)
	b5 := msgBlocks(sonnet, 100, `{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}`)
	b1 := msgBlocks(sonnet, 100, `{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}`)
	h.post(b5)
	r5 := reservedOf(t, h)
	h.post(b1)
	r1 := reservedOf(t, h)
	if r5 != worst(t, sonnet, b5, 100, "5m") || r1 != worst(t, sonnet, b1, 100, "1h") {
		t.Errorf("reserved %d and %d, want %d and %d", r5, r1, worst(t, sonnet, b5, 100, "5m"), worst(t, sonnet, b1, 100, "1h"))
	}
	// The input part at 2x the input rate against 1.25x.
	m := model(t, sonnet)
	out := pricing.Micros(100 * m.Rates.OutputPerM)
	in := float64(int64(len(b1))+pricing.RequestOverheadTokens) * m.Rates.InputPerM
	if got := float64(r1 - out); got < 2*in-1 || got > 2*in+1 {
		t.Errorf("1h input part %v, want 2 x %v", got, in)
	}
}

// --- caps ---

func TestEnforceRefusesPastCap(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 800}
	body := msg(sonnet, 1000, `"stream":true`)
	w := worst(t, sonnet, body, 1000, "")
	c := cost(t, sonnet, u)
	capM := w + c/2 // the first call fits; the second doesn't once the first is spent
	h := newHarnessWith(t, enforceCap(capM), anthropicfake.StreamOK(sonnet, u))
	if resp, _ := h.post(body); resp.StatusCode != 200 {
		t.Fatalf("first call: %d", resp.StatusCode)
	}
	resp, b := h.post(body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second call: %d, want 403", resp.StatusCode)
	}
	if resp.Header.Get("X-Should-Retry") != "false" {
		t.Errorf("x-should-retry %q", resp.Header.Get("X-Should-Retry"))
	}
	typ, m := apiError(t, b)
	wantMsg := fmt.Sprintf("fugaro: budget halted: run cap $%.2f reached ($%.2f spent)", capM.USD(), c.USD())
	if typ != "permission_error" || m != wantMsg {
		t.Errorf("error %s %q, want permission_error %q", typ, m, wantMsg)
	}
	if h.fake.Count() != 1 {
		t.Errorf("the refused call was forwarded")
	}
	select {
	case halt := <-h.gw.Halted():
		wantDetail := fmt.Sprintf("run cap $%.2f reached ($%.2f spent, $%.2f needed)", capM.USD(), c.USD(), w.USD())
		if halt.Reason != "run_cap" || halt.Detail != wantDetail || halt.At.IsZero() {
			t.Errorf("halt %+v, want detail %q", halt, wantDetail)
		}
	case <-time.After(time.Second):
		t.Fatal("Halted() didn't fire")
	}
	if l := h.gw.Ledger(); l.Granted != capM || l.Used != c || l.Reserved != 0 {
		t.Errorf("ledger %+v", l)
	}
}

func TestHaltIsSticky(t *testing.T) {
	h := newHarnessWith(t, enforceCap(1), okReplies(1)...)
	body := msg(sonnet, 1, `"stream":true`)
	resp, first := h.post(body)
	if resp.StatusCode != 403 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, req := range []*http.Request{
		h.request(context.Background(), "/v1/messages", msg(haiku, 1)),
		h.request(context.Background(), "/v1/messages/count_tokens", `{"model":"claude-haiku-4-5","messages":[]}`),
		h.request(context.Background(), "/v1/messages", msg(opus, 1)), // unpinned: still the halt
	} {
		resp, b := h.do(req)
		if resp.StatusCode != 403 || b != first {
			t.Errorf("%s after the halt: %d %q, want the same 403", req.URL.Path, resp.StatusCode, b)
		}
	}
	req, _ := http.NewRequest("HEAD", h.gw.URL()+"/api/hello", nil)
	if resp, _ := h.do(req); resp.StatusCode != 200 {
		t.Errorf("hello after the halt: %d", resp.StatusCode)
	}
	if h.fake.Count() != 0 {
		t.Errorf("forwarded %d requests after the halt", h.fake.Count())
	}
}

func TestHaltedFiresOnce(t *testing.T) {
	h := newHarnessWith(t, enforceCap(1))
	for range 3 {
		h.post(msg(sonnet, 1))
	}
	ch := h.gw.Halted()
	if _, ok := <-ch; !ok {
		t.Fatal("no halt delivered")
	}
	select {
	case h2, ok := <-ch:
		if ok {
			t.Errorf("a second halt: %+v", h2)
		}
	case <-time.After(time.Second):
		t.Error("Halted() isn't closed after its one delivery")
	}
}

func TestObserveNeverRefuses(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 10}
	h := newHarnessWith(t, func(o *Options) { o.Cap = 1 }, anthropicfake.StreamOK(sonnet, u), anthropicfake.StreamOK(sonnet, u))
	for range 2 {
		if resp, _ := h.post(msg(sonnet, 100, `"stream":true`)); resp.StatusCode != 200 {
			t.Fatalf("observe refused a call: %d", resp.StatusCode)
		}
	}
	r := h.gw.EndStage()
	if r.WouldHalt != 2 || r.Used != 2*cost(t, sonnet, u) {
		t.Errorf("report %+v, want 2 would-halts", r)
	}
	n := 0
	for _, l := range h.logs.lines(t) {
		if l["msg"] == "budget: would halt (observe)" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d would-halt log lines, want 2", n)
	}
	select {
	case <-h.gw.Halted():
		t.Error("observe halted")
	default:
	}
	// Observe with no cap accounts only.
	h = newHarness(t, anthropicfake.StreamOK(sonnet, u))
	h.post(msg(sonnet, 100, `"stream":true`))
	if r := h.gw.EndStage(); r.WouldHalt != 0 {
		t.Errorf("no cap: %d would-halts", r.WouldHalt)
	}
}

func TestInFlightCallsFinishAfterHalt(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	slow := anthropicfake.StreamOK(sonnet, u)
	slow.EventDelay = 100 * time.Millisecond
	body := msg(sonnet, 1000, `"stream":true`)
	w := worst(t, sonnet, body, 1000, "")
	h := newHarnessWith(t, enforceCap(w), slow)
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)
	// A call whose own worst case exceeds the cap halts the run even
	// with another call in flight.
	if resp2, _ := h.post(msg(sonnet, 100000, `"stream":true`)); resp2.StatusCode != 403 {
		t.Fatalf("second call: %d, want 403", resp2.StatusCode)
	}
	<-h.gw.Halted()
	rest, _ := io.ReadAll(r)
	resp.Body.Close()
	if !strings.Contains(string(rest), "message_stop") {
		t.Errorf("the in-flight call was cut: %q", rest)
	}
	rep := h.gw.EndStage()
	if rep.Used != cost(t, sonnet, u) || rep.Calls != 1 {
		t.Errorf("report %+v, want the in-flight call settled from usage", rep)
	}
}

// TestLedgerNeverExceedsGranted runs many concurrent calls of random
// allowed shapes and in-bound usage against a cap, and checks the ledger
// after every change: what is used and reserved never passes what is
// granted, except by the overrun of a call priced above its reservation
// (an unknown serving model here).
func TestLedgerNeverExceedsGranted(t *testing.T) {
	const workers, perWorker = 50, 4
	capM := pricing.Micros(5_000_000)
	var (
		mu       sync.Mutex
		breaches []string
		checks   int
	)
	fakeReply := func(r *http.Request, body []byte) anthropicfake.Reply {
		var q struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &q)
		var u pricing.Usage
		f := strings.Split(r.Header.Get("x-test-usage"), ",")
		for i, p := range []*int64{&u.Input, &u.CacheWrite5m, &u.CacheWrite1h, &u.CacheRead, &u.Output} {
			*p, _ = strconv.ParseInt(f[i], 10, 64)
		}
		serving := q.Model
		if r.Header.Get("x-test-unknown") == "1" {
			serving = "claude-mystery-9"
		}
		if q.Stream {
			return anthropicfake.StreamOK(serving, u)
		}
		return anthropicfake.MessageOK(serving, u)
	}
	h := newHarnessWith(t, enforceCap(capM))
	h.fake.Func = fakeReply
	h.gw.onLedger = func(l Ledger, overrun pricing.Micros) {
		mu.Lock()
		defer mu.Unlock()
		checks++
		if l.Used < 0 || l.Reserved < 0 || l.Used+l.Reserved > l.Granted+overrun {
			breaches = append(breaches, fmt.Sprintf("%+v overrun %d", l, overrun))
		}
	}
	png := base64.StdEncoding.EncodeToString(make([]byte, 90))
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), 7))
			for range perWorker {
				m := []string{sonnet, haiku}[rng.IntN(2)]
				maxTok := int64(1 + rng.IntN(8000))
				ttl := []string{"", "5m", "1h"}[rng.IntN(3)]
				var blocks []string
				blocks = append(blocks, `{"type":"text","text":"`+strings.Repeat("a", rng.IntN(20000))+`"}`)
				for range rng.IntN(3) {
					blocks = append(blocks, `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+png+`"}}`)
				}
				if ttl != "" {
					cc := `{"type":"ephemeral"}`
					if ttl == "1h" {
						cc = `{"type":"ephemeral","ttl":"1h"}`
					}
					blocks = append(blocks, `{"type":"text","text":"c","cache_control":`+cc+`}`)
				}
				extra := []string{}
				if rng.IntN(2) == 0 {
					extra = append(extra, `"stream":true`)
				}
				if rng.IntN(2) == 0 {
					extra = append(extra, `"tools":[{"name":"Read","input_schema":{"type":"object"}}]`)
				}
				body := msgBlocks(m, maxTok, strings.Join(blocks, ","), extra...)
				// Usage within the bound: input and cache tokens at most the
				// body's bytes, output at most max_tokens, cache writes only
				// with the TTL asked for.
				budget := int64(len(body))
				in := rng.Int64N(budget + 1)
				budget -= in
				cr := rng.Int64N(budget + 1)
				budget -= cr
				var w5, w1 int64
				switch ttl {
				case "5m":
					w5 = rng.Int64N(budget + 1)
				case "1h":
					w1 = rng.Int64N(budget + 1)
					w5 = rng.Int64N(budget - w1 + 1)
				}
				out := rng.Int64N(maxTok + 1)
				hdr := []string{"x-test-usage", fmt.Sprintf("%d,%d,%d,%d,%d", in, w5, w1, cr, out)}
				if rng.IntN(20) == 0 {
					hdr = append(hdr, "x-test-unknown", "1")
				}
				resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body, hdr...))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 && resp.StatusCode != 403 && resp.StatusCode != 429 {
					t.Errorf("status %d", resp.StatusCode)
				}
			}
		}()
	}
	wg.Wait()
	rep := h.gw.EndStage()
	mu.Lock()
	defer mu.Unlock()
	if len(breaches) > 0 {
		t.Errorf("%d breaches, first: %s", len(breaches), breaches[0])
	}
	if checks < 2*rep.Calls {
		t.Errorf("only %d ledger checks for %d calls", checks, rep.Calls)
	}
	l := h.gw.Ledger()
	if l.Reserved != 0 || l.Used != rep.Used {
		t.Errorf("ledger %+v, report used %d", l, rep.Used)
	}
	var logged pricing.Micros
	for _, c := range h.logs.calls(t) {
		logged += pricing.Micros(num(c["charged_micros"]))
		// Only a call priced above the table (an unknown serving model)
		// may cost more than its reservation.
		if c["priced_as"] == "table" && num(c["charged_micros"]) > num(c["reserved_micros"]) {
			t.Errorf("a table-priced call overran its reservation: %v", c)
		}
	}
	if logged != l.Used {
		t.Errorf("the log charges %d, the ledger %d", logged, l.Used)
	}
	if rep.Calls == 0 || rep.Calls == workers*perWorker {
		t.Errorf("%d calls went through: the cap should stop some but not all", rep.Calls)
	}
	t.Logf("%d calls, used %d of %d, overrun %d", rep.Calls, l.Used, capM, rep.Overrun)
}

// TestIncompleteUsageChargesReservation: a 2xx whose usage lacks the input
// or output count, or reports a negative one, can't be priced from usage;
// it settles at its reservation, never at 0 for the missing field.
func TestIncompleteUsageChargesReservation(t *testing.T) {
	start := func(usage string) anthropicfake.Event {
		return anthropicfake.Event{Name: "message_start", Data: `{"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":` + usage + `}}`}
	}
	delta := func(usage string) anthropicfake.Event {
		return anthropicfake.Event{Name: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + usage + `}`}
	}
	body := func(usage string) anthropicfake.Reply {
		return anthropicfake.Reply{Body: `{"type":"message","model":"claude-sonnet-5-5","content":[],"usage":` + usage + `}`}
	}
	stream := func(ev ...anthropicfake.Event) anthropicfake.Reply {
		return anthropicfake.Reply{Events: append(ev, anthropicfake.Stop)}
	}
	for _, c := range []struct {
		name     string
		reply    anthropicfake.Reply
		isStream bool
	}{
		{"body with empty usage", body(`{}`), false},
		{"body without output_tokens", body(`{"input_tokens":500}`), false},
		{"body without input_tokens", body(`{"output_tokens":500}`), false},
		{"body with a negative count", body(`{"input_tokens":500,"output_tokens":-400}`), false},
		{"body with a negative cache count", body(`{"input_tokens":5,"cache_read_input_tokens":-1,"output_tokens":5}`), false},
		{"delta with empty usage", stream(start(`{"input_tokens":500,"output_tokens":1}`), delta(`{}`)), true},
		{"start without input_tokens", stream(start(`{"output_tokens":1}`), delta(`{"output_tokens":300}`)), true},
		{"negative output in the delta", stream(start(`{"input_tokens":500,"output_tokens":1}`), delta(`{"output_tokens":-300}`)), true},
		{"negative input at the start", stream(start(`{"input_tokens":-500,"output_tokens":1}`), delta(`{"output_tokens":300}`)), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.reply)
			b := msg(sonnet, 1000)
			if c.isStream {
				b = msg(sonnet, 1000, `"stream":true`)
			}
			if resp, _ := h.post(b); resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
			w := worst(t, sonnet, b, 1000, "")
			rep := h.gw.EndStage()
			if rep.Used != w || rep.UsageUnparsed != 1 || rep.Unreconciled != w {
				t.Errorf("report %+v, want the reservation %d, one unparsed", rep, w)
			}
			if call := h.logs.lastCall(t); call["settled"] != "reserved" {
				t.Errorf("settled %v", call["settled"])
			}
		})
	}
}

// TestLongContextTierPricedFromUsage: a call whose reported input passes
// a long-context tier (a beta such as context-1m can make one) is charged
// at the tier's rates, and any excess over its reservation is overrun,
// never dropped.
func TestLongContextTierPricedFromUsage(t *testing.T) {
	tier := pricing.Rates{InputPerM: 2, OutputPerM: 10, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1,
		LongContext: &pricing.Tier{AboveInputTokens: 200_000, InputPerM: 4, OutputPerM: 15}}
	table, err := pricing.Embedded().With(pricing.Overrides{sonnet: tier})
	if err != nil {
		t.Fatal(err)
	}
	u := pricing.Usage{Input: 150_000, CacheRead: 100_000, Output: 500}
	h := newHarnessWith(t, func(o *Options) { o.Prices = table }, anthropicfake.StreamOK(sonnet, u))
	b := msg(sonnet, 1000, `"stream":true`)
	h.post(b, "anthropic-beta", "context-1m-2025-08-07")
	rep := h.gw.EndStage()
	want := tier.Cost(u)
	flat := tier
	flat.LongContext = nil
	if want <= flat.Cost(u) {
		t.Fatalf("the tier doesn't raise the price: %d <= %d", want, flat.Cost(u))
	}
	m, _ := table.Lookup(sonnet)
	w := m.WorstCase(pricing.Request{BodyBytes: int64(len(b)), MaxTokens: 1000})
	if rep.Used != want || rep.Overrun != want-w {
		t.Errorf("report %+v, want %d at the tier's rates, overrun %d", rep, want, want-w)
	}
}

// A call that fits the cap but not beside the calls in flight (whose
// reservations over-count their cost) is told to retry, not halted; the
// retry goes through once they settle.
func TestReservationsInFlightRetryNotHalt(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	slow := anthropicfake.StreamOK(sonnet, u)
	slow.EventDelay = 100 * time.Millisecond
	body := msg(sonnet, 1000, `"stream":true`)
	w := worst(t, sonnet, body, 1000, "")
	h := newHarnessWith(t, enforceCap(2*w-1), slow, anthropicfake.StreamOK(sonnet, u))
	resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)

	resp2, b := h.post(body)
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call: %d %s, want 429", resp2.StatusCode, b)
	}
	if v := resp2.Header.Get("X-Should-Retry"); v == "false" {
		t.Errorf("x-should-retry %q: the 429 must be retryable", v)
	}
	if resp2.Header.Get("Retry-After") == "" {
		t.Error("no retry-after")
	}
	if typ, _ := apiError(t, b); typ != "rate_limit_error" {
		t.Errorf("error type %q", typ)
	}
	select {
	case halt := <-h.gw.Halted():
		t.Fatalf("a retryable refusal halted the run: %+v", halt)
	default:
	}
	if h.fake.Count() != 1 {
		t.Errorf("the refused call was forwarded")
	}
	_, _ = io.ReadAll(r)
	resp.Body.Close()
	waitFor(t, "the first call to settle", func() bool { return h.gw.Ledger().Reserved == 0 })
	if resp3, b := h.post(body); resp3.StatusCode != 200 {
		t.Fatalf("the retry: %d %s, want 200", resp3.StatusCode, b)
	}
}

// Parallel calls near the cap never halt a run whose spend fits: each is
// served or told to retry, and retrying them all succeeds. What is used
// and reserved never passes the cap.
func TestParallelCallsNearCapRetryWithoutHalt(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 100}
	body := msg(sonnet, 1000, `"stream":true`)
	w := worst(t, sonnet, body, 1000, "")
	const workers = 12
	var mu sync.Mutex
	var breaches []string
	h := newHarnessWith(t, enforceCap(3*w+w/2))
	h.fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		r := anthropicfake.StreamOK(sonnet, u)
		r.EventDelay = 30 * time.Millisecond
		return r
	}
	h.gw.onLedger = func(l Ledger, overrun pricing.Micros) {
		mu.Lock()
		defer mu.Unlock()
		if l.Used+l.Reserved > l.Granted+overrun {
			breaches = append(breaches, fmt.Sprintf("%+v", l))
		}
	}
	var wg sync.WaitGroup
	var retried atomic.Int64
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for try := 0; try < 400; try++ {
				resp, err := http.DefaultClient.Do(h.request(context.Background(), "/v1/messages", body))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				switch resp.StatusCode {
				case 200:
					return
				case 429:
					retried.Add(1)
					time.Sleep(5 * time.Millisecond)
				default:
					t.Errorf("status %d", resp.StatusCode)
					return
				}
			}
			t.Error("never served")
		}()
	}
	wg.Wait()
	select {
	case halt := <-h.gw.Halted():
		t.Fatalf("halted with spend far under the cap: %+v", halt)
	default:
	}
	if retried.Load() == 0 {
		t.Error("no call was told to retry: the cap was not near enough")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(breaches) > 0 {
		t.Errorf("%d breaches, first %s", len(breaches), breaches[0])
	}
	if rep := h.gw.EndStage(); rep.Calls != workers {
		t.Errorf("%d calls served, want %d", rep.Calls, workers)
	}
}

func TestDollarsShowsSubCentAmounts(t *testing.T) {
	for _, c := range []struct {
		m    pricing.Micros
		want string
	}{{0, "$0.00"}, {2000, "$0.002"}, {9999, "$0.009999"}, {10000, "$0.01"}, {12_340_000, "$12.34"}, {1, "$0.000001"}} {
		if got := dollars(c.m); got != c.want {
			t.Errorf("dollars(%d) = %s, want %s", c.m, got, c.want)
		}
	}
}

// A settle that releases more than is reserved is a bug elsewhere; it is
// clamped, and said loudly.
func TestSettleBeyondReservedIsLogged(t *testing.T) {
	h := newHarness(t)
	st := h.gw.enterStage()
	defer st.calls.Done()
	h.gw.settle(st, 5, charge{})
	var warned bool
	for _, l := range h.logs.lines(t) {
		if l["level"] == "ERROR" && l["msg"] == "budget: a settle released more than was reserved" {
			warned = true
		}
	}
	if !warned || h.gw.Ledger().Reserved != 0 {
		t.Errorf("warned %v, ledger %+v", warned, h.gw.Ledger())
	}
}
