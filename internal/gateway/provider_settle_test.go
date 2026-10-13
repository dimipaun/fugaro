package gateway

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// A provider call is real money, so a reply that does not say what it used
// is never free: these tests pin the conservative settlement rules (design
// §5.5 and the multi-model addendum).

// settleCase runs one provider call (request body body, route fee fee) and
// returns its log line and the stage report.
func settleCase(t *testing.T, fee float64, body string, reply anthropicfake.Reply) (map[string]any, StageReport) {
	t.Helper()
	r := feeRouted(t, fee, nil, routedStage, dsRoute, reply)
	if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	return call, r.gw.EndStage()
}

// streamEvent is a one-off SSE event, for a stream built by hand: the
// events helpers (StartEvent, DeltaEvent) always include every usage
// field, which a test naming only some of them cannot use.
func streamEvent(name, data string) anthropicfake.Event {
	return anthropicfake.Event{Name: name, Data: data}
}

// settleStream runs a hand-built provider stream (message_start's and
// message_delta's data, a text block and message_stop in between) and
// returns its log line, the stage report and the logs (for a warning's
// reason).
func settleStream(t *testing.T, startUsage, deltaUsage string) (map[string]any, StageReport, *logBuf) {
	t.Helper()
	ev := []anthropicfake.Event{streamEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"`+dsModel+`","content":[],"usage":`+startUsage+`}}`)}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	ev = append(ev, streamEvent("message_delta", `{"type":"message_delta","usage":`+deltaUsage+`}`), anthropicfake.Stop)
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	return r.logs.lastCall(t), r.gw.EndStage(), r.logs
}

// settleCutStream runs a hand-built provider stream that ends before
// message_stop (a cut connection, or the upstream simply closing early):
// message_start's data, and optionally one message_delta's data (deltaUsage
// == "": no delta at all), then nothing more. Returns the log line and the
// stage report.
func settleCutStream(t *testing.T, startUsage, deltaUsage string) (map[string]any, StageReport) {
	t.Helper()
	ev := []anthropicfake.Event{streamEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"`+dsModel+`","content":[],"usage":`+startUsage+`}}`)}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	if deltaUsage != "" {
		ev = append(ev, streamEvent("message_delta", `{"type":"message_delta","usage":`+deltaUsage+`}`))
	}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	return r.logs.lastCall(t), r.gw.EndStage()
}

// fixtureEvents parses a saved SSE response (testdata/*.sse) into the
// discrete events anthropicfake.Reply can replay, using the same
// sseParser the gateway itself reads a response with.
func fixtureEvents(t *testing.T, path string) []anthropicfake.Event {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev []anthropicfake.Event
	p := &sseParser{onEvent: func(name string, data []byte) {
		ev = append(ev, anthropicfake.Event{Name: name, Data: string(data)})
	}}
	p.feed(raw)
	return ev
}

// lastUnparsedReason is the reason the last "budget: usage unparsed"
// warning named.
func lastUnparsedReason(t *testing.T, l *logBuf) string {
	t.Helper()
	var reason string
	found := false
	for _, m := range l.lines(t) {
		if m["msg"] == "budget: usage unparsed" {
			reason, _ = m["reason"].(string)
			found = true
		}
	}
	if !found {
		t.Fatalf("no usage_unparsed warning logged; log:\n%s", l.String())
	}
	return reason
}

func TestMissingUsageChargesReservation(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 200}
	for _, c := range []struct {
		name   string
		stream bool
		reply  anthropicfake.Reply
	}{
		{"stream", true, anthropicfake.Compat{OmitUsage: true}.StreamOK(dsModel, u)},
		{"message", false, anthropicfake.Compat{OmitUsage: true}.MessageOK(dsModel, u)},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := msg(dsModel, 1000)
			if c.stream {
				body = msg(dsModel, 1000, `"stream":true`)
			}
			call, rep := settleCase(t, 10, body, c.reply)
			reserved := pricing.Micros(num(call["reserved_micros"]))
			if reserved == 0 {
				t.Fatal("nothing was reserved")
			}
			if got := pricing.Micros(num(call["charged_micros"])); got != reserved {
				t.Errorf("a reply without usage was charged %d, want the full reservation %d", got, reserved)
			}
			if call["settled"] != settledReserved {
				t.Errorf("settled %v", call["settled"])
			}
			if rep.Used != reserved || rep.Unreconciled != reserved {
				t.Errorf("stage used %d, unreconciled %d, want %d", rep.Used, rep.Unreconciled, reserved)
			}
		})
	}
}

// Usage that only the final message_delta carries (message_start has none
// at all, as a provider may) still settles from that delta: the gateway
// reads a provider's usage from what the stream says once it ends, not
// from message_start alone.
func TestUsageOnlyInFinalDeltaSettles(t *testing.T) {
	u := pricing.Usage{Input: 100, Output: 200}
	ev := []anthropicfake.Event{
		{Name: "message_start", Data: `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"` + dsModel + `","content":[]}}`},
	}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	ev = append(ev, anthropicfake.DeltaEvent(u), anthropicfake.Stop)
	call, rep := settleCase(t, 0, msg(dsModel, 1000, `"stream":true`), anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if got, want := pricing.Micros(num(call["charged_micros"])), cost(t, dsModel, u); got != want || call["settled"] != settledUsage {
		t.Errorf("charged %d, settled %v, want %d from usage", got, call["settled"], want)
	}
	if rep.UsageUnparsed != 0 {
		t.Errorf("usage_unparsed %d, want 0", rep.UsageUnparsed)
	}
}

func TestStreamWithNoEventsChargedAfterStart(t *testing.T) {
	call, _ := settleCase(t, 0, msg(dsModel, 1000, `"stream":true`),
		anthropicfake.Reply{Status: http.StatusOK, Events: []anthropicfake.Event{anthropicfake.Ping}})
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 {
		t.Errorf("charged %d of %d reserved", got, res)
	}
}

func TestNoCacheFieldsChargesFullInput(t *testing.T) {
	m := model(t, dsModel)
	if !(m.Rates.CacheRead < m.Rates.InputPerM) {
		t.Fatalf("the test needs a cache read rate below the input rate: %+v", m.Rates)
	}
	// Whatever the prompt held, the reply names only input and output: the
	// whole input is charged at the input rate.
	u := pricing.Usage{Input: 50000, Output: 300}
	want := pricing.WithFee(m.Rates.Cost(pricing.Usage{Input: 50000, Output: 300}), 10)
	for _, c := range []struct {
		name   string
		stream bool
		reply  anthropicfake.Reply
	}{
		{"stream", true, anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, u)},
		{"message", false, anthropicfake.Compat{OmitCacheFields: true}.MessageOK(dsModel, u)},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := msg(dsModel, 1000)
			if c.stream {
				body = msg(dsModel, 1000, `"stream":true`)
			}
			call, _ := settleCase(t, 10, body, c.reply)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want %d (all input at the full input rate)", got, want)
			}
			if call["settled"] != settledUsage || num(call["cache_read"]) != 0 || num(call["in"]) != 50000 {
				t.Errorf("call %v", call)
			}
		})
	}
}

func TestReportedCostNeverSettles(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	want := pricing.WithFee(cost(t, dsModel, u), 10)
	for _, c := range []struct {
		name     string
		reported float64
		micros   int64
	}{
		{"lower", 0.000001, 1},
		{"higher", 1234.5, 1234500000},
	} {
		for _, stream := range []bool{true, false} {
			name := c.name + "/message"
			body := msg(dsModel, 4000)
			reply := anthropicfake.Compat{Cost: c.reported}.MessageOK(dsModel, u)
			if stream {
				name = c.name + "/stream"
				body = msg(dsModel, 4000, `"stream":true`)
				reply = anthropicfake.Compat{Cost: c.reported}.StreamOK(dsModel, u)
			}
			t.Run(name, func(t *testing.T) {
				call, rep := settleCase(t, 10, body, reply)
				if got := pricing.Micros(num(call["charged_micros"])); got != want {
					t.Errorf("charged %d, want the table's %d whatever the provider reported", got, want)
				}
				if num(call["reported_micros"]) != c.micros {
					t.Errorf("reported_micros %v, want %d", call["reported_micros"], c.micros)
				}
				if call["settled"] != settledUsage || rep.Used != want {
					t.Errorf("settled %v, stage used %d", call["settled"], rep.Used)
				}
				if int64(rep.Reported) != c.micros {
					t.Errorf("stage reported %d, want %d", rep.Reported, c.micros)
				}
			})
		}
	}
}

// An unusable reported cost is not recorded and changes nothing.
func TestReportedCostUnusableIgnored(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	for _, cost := range []string{"-5", `"cheap"`, "1e30", "1e999", "null"} {
		t.Run(cost, func(t *testing.T) {
			reply := anthropicfake.Compat{}.MessageOK(dsModel, u)
			reply.Body = replaceUsageCost(t, reply.Body, cost)
			call, rep := settleCase(t, 0, msg(dsModel, 4000), reply)
			if _, ok := call["reported_micros"]; ok || rep.Reported != 0 {
				t.Errorf("an unusable cost %s was recorded: %v, %d", cost, call["reported_micros"], rep.Reported)
			}
			if got := pricing.Micros(num(call["charged_micros"])); got != cost0(t, u) || call["settled"] != settledUsage {
				t.Errorf("charged %d settled %v", got, call["settled"])
			}
		})
	}
}

func cost0(t *testing.T, u pricing.Usage) pricing.Micros { return cost(t, dsModel, u) }

func replaceUsageCost(t *testing.T, body, cost string) string {
	t.Helper()
	// The usage object ends with the output count: add the cost before it.
	j := indexLast(body, `"output_tokens":2000`)
	if j < 0 {
		t.Fatalf("no usage in %s", body)
	}
	return body[:j] + `"cost":` + cost + `,` + body[j:]
}

func indexLast(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A stream cut after message_start is charged at least what the partial
// rule says: its reported input plus the reserved output.
func TestCutProviderStreamChargesPartial(t *testing.T) {
	u := pricing.Usage{Input: 2000, Output: 5}
	reply := anthropicfake.Compat{OmitCacheFields: true, Cost: 0.000001}.StreamOK(dsModel, u)
	reply.CutAfter = 3
	call, rep := settleCase(t, 10, msg(dsModel, 1000, `"stream":true`), reply)
	want := pricing.WithFee(cost(t, dsModel, pricing.Usage{Input: 2000, Output: 1000}), 10)
	if got := pricing.Micros(num(call["charged_micros"])); got != want {
		t.Errorf("charged %d, want exactly the partial charge %d", got, want)
	}
	if call["settled"] != settledPartial || rep.Used != want {
		t.Errorf("settled %v, used %d", call["settled"], rep.Used)
	}
	if _, ok := call["reported_micros"]; ok {
		t.Error("a cut stream's partial cost was recorded as the reported cost")
	}
}

// A stream that breaks before message_stop still partially settles once a
// later message_delta reports the real input (message_start's zero input
// must not have been taken as the final word): charged input plus the
// reserved output, same partial rule as TestCutProviderStreamChargesPartial.
func TestCutStreamPartialFromLaterDeltaInput(t *testing.T) {
	call, rep := settleCutStream(t,
		`{"input_tokens":0,"output_tokens":1}`,
		`{"input_tokens":32,"output_tokens":10}`)
	want := cost(t, dsModel, pricing.Usage{Input: 32, Output: 1000})
	if got := pricing.Micros(num(call["charged_micros"])); got != want || call["settled"] != settledPartial {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
	if rep.Used != want {
		t.Errorf("stage used %d, want %d", rep.Used, want)
	}
}

// Same, but the only count that ever named anything is a cache read at
// message_start itself (no delta ever arrives): heavy caching with zero
// input is still enough to partially settle, the cache read charged plus
// the reserved output.
func TestCutStreamPartialFromCacheReadOnly(t *testing.T) {
	call, rep := settleCutStream(t,
		`{"input_tokens":0,"cache_read_input_tokens":900,"output_tokens":1}`,
		"")
	want := cost(t, dsModel, pricing.Usage{CacheRead: 900, Output: 1000})
	if got := pricing.Micros(num(call["charged_micros"])); got != want || call["settled"] != settledPartial {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
	if rep.Used != want {
		t.Errorf("stage used %d, want %d", rep.Used, want)
	}
}

// A stream that breaks before message_stop with input (and cache) still
// zero in the last delta it managed to send has nothing to partially
// settle on either: the full reservation, same as a stream with no events
// at all.
func TestCutStreamStillZeroChargesReservation(t *testing.T) {
	call, rep := settleCutStream(t,
		`{"input_tokens":0,"output_tokens":1}`,
		`{"input_tokens":0,"output_tokens":10}`)
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v", got, res, call["settled"])
	}
	if rep.Used != pricing.Micros(num(call["charged_micros"])) {
		t.Errorf("stage used %d, want the reservation", rep.Used)
	}
}

func TestZeroOutputTokensChargesInput(t *testing.T) {
	u := pricing.Usage{Input: 4000}
	call, _ := settleCase(t, 0, msg(dsModel, 1000, `"stream":true`), anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, u))
	// message_start's one output token stays: a later count never lowers it.
	want := cost(t, dsModel, pricing.Usage{Input: 4000, Output: 1})
	if got := pricing.Micros(num(call["charged_micros"])); got != want || got == 0 || call["settled"] != settledUsage {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
}

func TestUnknownEventsSkipped(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	plain := anthropicfake.Compat{}.StreamOK(dsModel, u)
	ev := []anthropicfake.Event{plain.Events[0],
		{Name: "provider_note", Data: `{"type":"provider_note","usage":{"input_tokens":99999999}}`},
		{Data: `{"type":"openrouter_meta","cost":0.5}`}}
	ev = append(ev, plain.Events[1:]...)
	plain.Events = ev
	call, _ := settleCase(t, 0, msg(dsModel, 4000, `"stream":true`), plain)
	if got := pricing.Micros(num(call["charged_micros"])); got != cost(t, dsModel, u) || call["settled"] != settledUsage {
		t.Errorf("charged %d settled %v", got, call["settled"])
	}
}

// route is recorded per call and per stage; a Claude call has none.
func TestRouteRecordedPerCall(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, u))
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if resp, b := r.post(msg(sonnet, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	rep := r.gw.EndStage()
	if got := rep.ByRoute["openrouter"]; got != cost(t, dsModel, u) {
		t.Errorf("by route %v, want openrouter %d", rep.ByRoute, cost(t, dsModel, u))
	}
	if len(rep.ByRoute) != 1 {
		t.Errorf("a Claude call has a route: %v", rep.ByRoute)
	}
}

// Later usage events on a provider route never lower the charge: each
// count is the largest any event reported, and a call whose input (and
// cache) stay zero throughout is not readable usage, whatever output it
// produced: a real request always sends some input, so a provider whose
// usage never names one simply left it out (see TestZeroInputHeavyCacheSettles
// for the heavy-caching case, where a cache read does name it).
func TestProviderUsageNeverLowersCharge(t *testing.T) {
	full := pricing.Usage{Input: 1000, Output: 2000}
	events := func(ev ...anthropicfake.Event) anthropicfake.Reply {
		all := append([]anthropicfake.Event{}, ev[0])
		all = append(all, anthropicfake.TextEvents("ok")...)
		all = append(all, ev[1:]...)
		all = append(all, anthropicfake.Stop)
		return anthropicfake.Reply{Status: http.StatusOK, Events: all}
	}
	start, delta := anthropicfake.StartEvent, anthropicfake.DeltaEvent
	for _, c := range []struct {
		name  string
		reply anthropicfake.Reply
		want  pricing.Usage // zero: settles at the reservation
	}{
		{"delta reports no input", events(start(dsModel, pricing.Usage{Input: 1000}), delta(pricing.Usage{Output: 2000})), full},
		{"duplicate start with lower numbers", events(start(dsModel, pricing.Usage{Input: 1000}), start(dsModel, pricing.Usage{Input: 500}), delta(full)), full},
		{"lower output in a later delta", events(start(dsModel, pricing.Usage{Input: 1000}), delta(full), delta(pricing.Usage{Input: 1000, Output: 100})), full},
		{"input only ever zero", events(start(dsModel, pricing.Usage{}), delta(pricing.Usage{Output: 2000})), pricing.Usage{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			call, rep := settleCase(t, 10, msg(dsModel, 4000, `"stream":true`), c.reply)
			got := pricing.Micros(num(call["charged_micros"]))
			if c.want == (pricing.Usage{}) {
				res := pricing.Micros(num(call["reserved_micros"]))
				if got != res || res == 0 || call["settled"] != settledReserved || rep.Used != res {
					t.Errorf("charged %d of %d reserved, settled %v, used %d", got, res, call["settled"], rep.Used)
				}
				return
			}
			want := pricing.WithFee(cost(t, dsModel, c.want), 10)
			if got != want || call["settled"] != settledUsage || rep.Used != want {
				t.Errorf("charged %d, want %d; settled %v, used %d", got, want, call["settled"], rep.Used)
			}
		})
	}
}

// A non-streaming provider reply with a zero input count is missing
// usage: a real request always sends some input, so the provider must
// simply have left the field out of its reply, whatever output it
// produced.
func TestProviderMessageZeroInputChargesReservation(t *testing.T) {
	reply := anthropicfake.Compat{}.MessageOK(dsModel, pricing.Usage{Output: 2000})
	call, _ := settleCase(t, 10, msg(dsModel, 4000), reply)
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v", got, res, call["settled"])
	}
}

// The exact shape a live run captured from OpenRouter routing to
// deepseek/deepseek-v4.1-flash served by DeepInfra (fixture:
// testdata/openrouter_deepinfra_stream.sse): message_start names an
// all-zero usage and the real, cumulative counts, plus the provider's own
// cost, arrive only in message_delta. This is the regression the gap
// between the gateway's charge and OpenRouter's own Activity figure
// traced to: every such call used to settle at its reservation.
func TestZeroInputAtStartSettlesFromDelta(t *testing.T) {
	u := pricing.Usage{Input: 32, CacheRead: 5, Output: 47}
	call, rep, _ := settleStream(t,
		`{"input_tokens":0,"output_tokens":1}`,
		`{"input_tokens":32,"cache_read_input_tokens":5,"output_tokens":47,"cost":0.0001}`)
	want := cost(t, dsModel, u)
	if got := pricing.Micros(num(call["charged_micros"])); got != want || call["settled"] != settledUsage {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
	if rep.UsageUnparsed != 0 {
		t.Errorf("usage_unparsed %d, want 0", rep.UsageUnparsed)
	}
	if got := num(call["reported_micros"]); got != 100 {
		t.Errorf("reported_micros %v, want 100 (the provider's $0.0001)", call["reported_micros"])
	}
}

// TestFixtureEndToEndTableMaximum sends the abridged OpenRouter/DeepInfra
// fixture (testdata/openrouter_deepinfra_stream.sse) through the full
// gateway (not just the tee) with a 5.5% route fee, pinned to the model
// the embedded table knows (deepseek/deepseek-v4-flash). The fixture's
// message_delta names 32 input, 0 cache read, 47 output tokens for a
// served model (deepseek/deepseek-v4.1-flash) the table has no row for,
// so it prices at the table's maximum rates: claude-fable-5-1's $10 input
// / $50 output per M. 32*10 + 47*50 = 320 + 2350 = 2670; +5.5% fee
// ceil(2670*0.055) = ceil(146.85) = 147; total 2817.
func TestFixtureEndToEndTableMaximum(t *testing.T) {
	ev := fixtureEvents(t, "testdata/openrouter_deepinfra_stream.sse")
	r := feeRouted(t, 5.5, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	if got := num(call["charged_micros"]); got != 2817 {
		t.Errorf("charged %d, want 2817 (32*10 + 47*50 = 2670, +5.5%% fee ceil(146.85)=147)", got)
	}
	if call["priced_as"] != pricedMax {
		t.Errorf("priced_as %v, want %q: the served model isn't in the table", call["priced_as"], pricedMax)
	}
}

// TestFixtureEndToEndOwnerRates is the same fixture and fee, but the
// owner has configured model_prices for the served model itself
// (deepseek/deepseek-v4.1-flash), pinned directly so there is no
// pin/serving mismatch to take the dearer of two rates (MaxOf):
// input $0.31, output $1.25 per M, cache_read a 0.02 multiplier of
// input_per_m (unused: the fixture's cache_read_input_tokens is 0), the
// model_prices shape documented in docs/multi-model.md §2.
// 32*0.31 + 47*1.25 = 9.92 + 58.75 = 68.67, rounded to the nearest
// micro-dollar: 69; +5.5% fee ceil(69*0.055) = ceil(3.795) = 4; total 73.
func TestFixtureEndToEndOwnerRates(t *testing.T) {
	const v41 = "deepseek/deepseek-v4.1-flash"
	prices := func(tbl *pricing.Table) {
		tbl.Models[v41] = pricing.Model{ID: v41, Rates: pricing.Rates{InputPerM: 0.31, OutputPerM: 1.25, CacheRead: 0.02}}
	}
	stage := Stage{Name: "implement", Model: v41, Background: sonnet}
	ev := fixtureEvents(t, "testdata/openrouter_deepinfra_stream.sse")
	r := feeRouted(t, 5.5, prices, stage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(v41, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	if got := num(call["charged_micros"]); got != 73 {
		t.Errorf("charged %d, want 73 (32*0.31 + 47*1.25 = 68.67, rounded 69, +5.5%% fee ceil(3.795)=4)", got)
	}
	if call["priced_as"] != pricedTable {
		t.Errorf("priced_as %v, want %q: the pin is the served model, no MaxOf", call["priced_as"], pricedTable)
	}
}

// Input 0 with cache_read_input_tokens > 0 and no other input is heavy
// caching, not a reply that didn't say: it is priced, not unparsed.
func TestZeroInputHeavyCacheSettles(t *testing.T) {
	u := pricing.Usage{CacheRead: 900, Output: 50}
	call, rep, _ := settleStream(t,
		`{"input_tokens":0,"cache_read_input_tokens":900,"output_tokens":1}`,
		`{"input_tokens":0,"cache_read_input_tokens":900,"output_tokens":50}`)
	want := cost(t, dsModel, u)
	if got := pricing.Micros(num(call["charged_micros"])); got != want || call["settled"] != settledUsage {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
	if rep.UsageUnparsed != 0 {
		t.Errorf("usage_unparsed %d, want 0", rep.UsageUnparsed)
	}
}

// Code review (low): hasInput()'s provider branch required a.in.set even
// though a cache read or write alone is documented as enough. A usage
// object that never includes an input_tokens key at all (as opposed to
// sending input_tokens:0 explicitly, every other test's shape) must still
// settle from its cache read, not fall back to the reservation.
func TestCacheReadOnlyNoInputKeySettles(t *testing.T) {
	u := pricing.Usage{CacheRead: 900, Output: 50}
	call, rep, _ := settleStream(t,
		`{"cache_read_input_tokens":900,"output_tokens":1}`,
		`{"cache_read_input_tokens":900,"output_tokens":50}`)
	want := cost(t, dsModel, u)
	if got := pricing.Micros(num(call["charged_micros"])); got != want || call["settled"] != settledUsage {
		t.Errorf("charged %d, want %d, settled %v", got, want, call["settled"])
	}
	if rep.UsageUnparsed != 0 {
		t.Errorf("usage_unparsed %d, want 0", rep.UsageUnparsed)
	}
}

// An all-zero usage (input, cache and output, every field) on a 2xx is
// still unparsed: the fail-safe for a provider whose usage reporting
// never says anything, whatever text the response held.
func TestAllZeroUsageUnparsed(t *testing.T) {
	call, rep, logs := settleStream(t,
		`{"input_tokens":0,"output_tokens":0}`,
		`{"input_tokens":0,"output_tokens":0}`)
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v", got, res, call["settled"])
	}
	if rep.UsageUnparsed != 1 {
		t.Errorf("usage_unparsed %d, want 1", rep.UsageUnparsed)
	}
	if reason := lastUnparsedReason(t, logs); reason != "input_zero_no_cache" {
		t.Errorf("reason %q, want input_zero_no_cache", reason)
	}
}

// A negative count still breaks a provider call: never settled as usage,
// whatever a later event claims, with a reason naming it.
func TestNegativeCountUnparsedWithReason(t *testing.T) {
	call, rep, logs := settleStream(t,
		`{"input_tokens":5,"output_tokens":1}`,
		`{"input_tokens":-1,"output_tokens":1}`)
	if call["settled"] != settledReserved || rep.UsageUnparsed != 1 {
		t.Errorf("settled %v, usage_unparsed %d, want reserved and 1", call["settled"], rep.UsageUnparsed)
	}
	if reason := lastUnparsedReason(t, logs); reason != "negative_count" {
		t.Errorf("reason %q, want negative_count", reason)
	}
}

// A negative count that arrives in a message_delta AFTER message_stop (a
// malformed or buggy stream, but one the gateway already read as
// complete) must still break the call: complete, once set, never implied
// the usage that came after it stayed trustworthy.
func TestNegativeCountAfterStopStillBreaks(t *testing.T) {
	ev := []anthropicfake.Event{
		streamEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"`+dsModel+`","content":[],"usage":{"input_tokens":5000,"output_tokens":1}}}`),
	}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	ev = append(ev, streamEvent("message_delta", `{"type":"message_delta","usage":{"input_tokens":5000,"output_tokens":50}}`), anthropicfake.Stop,
		// Arrives after message_stop, once the call already read as complete.
		streamEvent("message_delta", `{"type":"message_delta","usage":{"input_tokens":-1}}`))
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v, want the reservation: a negative count after message_stop was hidden", got, res, call["settled"])
	}
	if reason := lastUnparsedReason(t, r.logs); reason != "negative_count" {
		t.Errorf("reason %q, want negative_count", reason)
	}
}

// settleDuplicateStart runs a provider stream with a normal message_start
// and message_delta, then a second message_start carrying secondUsage,
// either before or after message_stop, and returns its log line and the
// stage report. A duplicate or late message_start is not gated the way a
// message_delta is: it always merges, so a negative count in it must
// still be caught.
func settleDuplicateStart(t *testing.T, secondUsage string, afterStop bool) (map[string]any, StageReport) {
	t.Helper()
	ev := []anthropicfake.Event{
		streamEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"`+dsModel+`","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`),
	}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	ev = append(ev, streamEvent("message_delta", `{"type":"message_delta","usage":{"output_tokens":500}}`))
	second := streamEvent("message_start", `{"type":"message_start","message":{"id":"m2","model":"`+dsModel+`","content":[],"usage":`+secondUsage+`}}`)
	if afterStop {
		ev = append(ev, anthropicfake.Stop, second)
	} else {
		ev = append(ev, second, anthropicfake.Stop)
	}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	return r.logs.lastCall(t), r.gw.EndStage()
}

// chargedReservation asserts call settled at its full, nonzero reservation.
func chargedReservation(t *testing.T, call map[string]any) {
	t.Helper()
	got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"]))
	if got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v, want the reservation", got, res, call["settled"])
	}
}

// A duplicate message_start carrying a negative output count, merged
// before message_stop, must still break the call. Before this fix:
// message_start never set broken on a negative count, so the provider's
// message_stop branch (deltas && !broken && hasInput) still matched —
// complete, with output floored to 0 by usage() — charging ~100 of the
// 8213 reservation (input only, at the model's rate) instead of it.
func TestNegativeDuplicateStartBeforeStopOutput(t *testing.T) {
	call, _ := settleDuplicateStart(t, `{"input_tokens":100,"output_tokens":-1}`, false)
	chargedReservation(t, call)
}

// The same negative output, but the duplicate message_start arrives AFTER
// message_stop, once the call already read as complete. Before this fix:
// same ~100-of-8213 undercharge as the before-stop case — complete, once
// set, was never reconsidered.
func TestNegativeDuplicateStartAfterStopOutput(t *testing.T) {
	call, _ := settleDuplicateStart(t, `{"input_tokens":100,"output_tokens":-1}`, true)
	chargedReservation(t, call)
}

// A negative cache_creation_input_tokens in a second message_start. Before
// this fix: charged ~2100 of the 8213 reservation (input and the
// placeholder output, cache creation floored to 0) instead of it.
func TestNegativeCacheCreationDuplicateStart(t *testing.T) {
	call, _ := settleDuplicateStart(t, `{"input_tokens":100,"output_tokens":1,"cache_creation_input_tokens":-1000}`, false)
	chargedReservation(t, call)
}

// A negative input_tokens alongside a real cache_read_input_tokens in a
// second message_start: hasInput() alone (cache_read > 0) would otherwise
// still read this as settleable. Before this fix: charged ~2005 of the
// 8213 reservation (the placeholder output and the cache read, input
// floored to 0) instead of it.
func TestNegativeInputWithCacheReadDuplicateStart(t *testing.T) {
	call, _ := settleDuplicateStart(t, `{"input_tokens":-1,"cache_read_input_tokens":50,"output_tokens":1}`, false)
	chargedReservation(t, call)
}

// A message_delta the gateway can't read as usage breaks the call: the
// reservation, with a reason naming it, since it may hold the real output
// count behind a shape the gateway doesn't expect.
func TestMalformedDeltaUnparsedWithReason(t *testing.T) {
	call, rep, logs := settleStream(t,
		`{"input_tokens":5,"output_tokens":1}`,
		`{"input_tokens":"not-a-number","output_tokens":1}`)
	if call["settled"] != settledReserved || rep.UsageUnparsed != 1 {
		t.Errorf("settled %v, usage_unparsed %d, want reserved and 1", call["settled"], rep.UsageUnparsed)
	}
	if reason := lastUnparsedReason(t, logs); reason != "malformed_usage" {
		t.Errorf("reason %q, want malformed_usage", reason)
	}
}

// Code review (BLOCKER): a message_start whose usage object fails to
// unmarshal was silently ignored (no broken, no malformedEvent), so a
// later message_delta — not gated on started the way the Claude route's
// delta handling is — could still merge fine and settle the call from
// usage, with the type-mismatched count never charged at all. Review's
// own measurements (reservation 8213, unfixed head): case A 2001,
// B/C/D 2100. All four must settle at the reservation instead.
func TestMalformedMessageStartUnparsed(t *testing.T) {
	for _, c := range []struct {
		name       string
		startUsage string
		deltaUsage string
	}{
		{"A input_tokens string, delta has cache_read only", `{"input_tokens":"5000","output_tokens":1}`, `{"cache_read_input_tokens":10,"output_tokens":500}`},
		{"B input_tokens float, delta has real input", `{"input_tokens":5000.0,"output_tokens":1}`, `{"input_tokens":100,"output_tokens":500}`},
		{"C input_tokens overflow float, delta has real input", `{"input_tokens":1e30,"output_tokens":1}`, `{"input_tokens":100,"output_tokens":500}`},
		{"D input_tokens string, delta has real input", `{"input_tokens":"5000","output_tokens":1}`, `{"input_tokens":100,"output_tokens":500}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			call, rep, logs := settleStream(t, c.startUsage, c.deltaUsage)
			if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
				t.Errorf("charged %d of %d reserved, settled %v, want the reservation", got, res, call["settled"])
			}
			if rep.UsageUnparsed != 1 {
				t.Errorf("usage_unparsed %d, want 1", rep.UsageUnparsed)
			}
			if reason := lastUnparsedReason(t, logs); reason != "malformed_usage" {
				t.Errorf("reason %q, want malformed_usage", reason)
			}
		})
	}
}

// A hostile event type name (a key-looking string, embedded newlines, an
// unbounded length) must never reach the usage_unparsed WARN line: the
// comment on usageTee.events and logUsageUnparsed says "never the
// provider's text", and this proves it end to end.
func TestUnparsedEventNamesSanitizedInLog(t *testing.T) {
	hostile := "sk-ant-api03-LEAKEDSECRETKEY\ninjected: true\n" + strings.Repeat("x", 10000)
	ev := []anthropicfake.Event{{Data: `{"type":` + strconv.Quote(hostile) + `}`}}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	logs := r.logs.String()
	if strings.Contains(logs, "sk-ant-api03") || strings.Contains(logs, "injected:") {
		t.Errorf("a hostile event name reached the log:\n%.500s", logs)
	}
	if !strings.Contains(logs, "unrecognized") {
		t.Errorf("the sanitized placeholder is missing from the log:\n%.500s", logs)
	}
}
