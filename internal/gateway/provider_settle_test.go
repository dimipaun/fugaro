package gateway

import (
	"net/http"
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

// Usage that only the final message_delta carries (no usage on
// message_start) is not readable as a whole call: the reservation.
func TestUsageOnlyInFinalDeltaChargesReservation(t *testing.T) {
	ev := []anthropicfake.Event{
		{Name: "message_start", Data: `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"` + dsModel + `","content":[]}}`},
	}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	ev = append(ev, anthropicfake.DeltaEvent(pricing.Usage{Input: 100, Output: 200}), anthropicfake.Stop)
	call, _ := settleCase(t, 0, msg(dsModel, 1000, `"stream":true`), anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v", got, res, call["settled"])
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
// count is the largest any event reported, and a call whose input stays
// zero is not readable usage.
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

// A non-streaming provider reply with a zero input count is missing usage.
func TestProviderMessageZeroInputChargesReservation(t *testing.T) {
	reply := anthropicfake.Compat{}.MessageOK(dsModel, pricing.Usage{Output: 2000})
	call, _ := settleCase(t, 10, msg(dsModel, 4000), reply)
	if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
		t.Errorf("charged %d of %d reserved, settled %v", got, res, call["settled"])
	}
}
