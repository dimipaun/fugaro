package gateway

import (
	"encoding/json"
	"fmt"
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

// warnLine is the last log line whose message is msg, for a test that reads
// an attribute off a warning about how a call settled.
func warnLine(t *testing.T, l *logBuf, msg string) map[string]any {
	t.Helper()
	var out map[string]any
	for _, m := range l.lines(t) {
		if m["msg"] == msg {
			out = m
		}
	}
	if out == nil {
		t.Fatalf("no %q warning logged; log:\n%s", msg, l.String())
	}
	return out
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

// TestSettleOnReport: on a provider route a complete call whose usage
// carries a cost settles on that figure plus the route's fee (D2640). The
// provider charged the call, so its own figure is the charge rather than a
// comparison the table's estimate is preferred to -- the table is what
// reserved the call, and what still prices every call the provider says
// nothing about. The call log says which of the two priced it (RS4), the
// stage's charge and its per-model dollars follow it, and it is not an
// overrun: a report under the reservation is what the call cost.
func TestSettleOnReport(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	table := cost0(t, u)
	for _, c := range []struct {
		name string
		fee  float64
		want pricing.Micros
	}{
		{"no route fee", 0, 8_000},
		{"with the route fee", 10, 8_800},
	} {
		for _, stream := range []bool{true, false} {
			name := c.name + "/message"
			body := msg(dsModel, 4000)
			reply := anthropicfake.Compat{Cost: 0.008}.MessageOK(dsModel, u)
			if stream {
				name = c.name + "/stream"
				body = msg(dsModel, 4000, `"stream":true`)
				reply = anthropicfake.Compat{Cost: 0.008}.StreamOK(dsModel, u)
			}
			t.Run(name, func(t *testing.T) {
				call, rep := settleCase(t, c.fee, body, reply)
				if want := table; want >= c.want {
					t.Fatalf("the fixture's table charge %d is not below the report %d: the charge would prove nothing", want, c.want)
				}
				if got := pricing.Micros(num(call["charged_micros"])); got != c.want {
					t.Errorf("charged %d, want the provider's figure with the fee %d", got, c.want)
				}
				if call["priced_as"] != pricedReported {
					t.Errorf("priced_as %v, want %q", call["priced_as"], pricedReported)
				}
				if call["settled"] != settledUsage || rep.Used != c.want || rep.ByModel[dsModel] != c.want {
					t.Errorf("settled %v, used %d, by model %v, want a usage settle at %d", call["settled"], rep.Used, rep.ByModel, c.want)
				}
				if rep.Reported != 8_000 || rep.ReportedBy[dsModel] != 8_000 {
					t.Errorf("reported %d by model %v, want 8000", rep.Reported, rep.ReportedBy)
				}
				if rep.Overrun != 0 {
					t.Errorf("overrun %d, want 0: the report is under the reservation", rep.Overrun)
				}
			})
		}
	}
}

// TestLowReportFlooredAtTierMinimum: a report below what the table would
// have charged the same usage at the row's cheapest end is floored there
// (D2651). A provider that under-reports, or reports a zero, must not make
// a call cheaper than the price table says it was -- that floor is what
// keeps a figure the gateway cannot check from pricing a call below the
// table's own cheapest price for it (F17). The call is priced_as reported
// either way, the charge being the report read against that floor, and the
// warning names the figure it was raised to.
func TestLowReportFlooredAtTierMinimum(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	charged := pricing.Rates{InputPerM: 4, OutputPerM: 16, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.4}
	lo := pricing.Rates{InputPerM: 1, OutputPerM: 2, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	hi := pricing.Rates{InputPerM: 8, OutputPerM: 32, CacheWrite5m: 10, CacheWrite1h: 16, CacheRead: 0.8}
	// lo.Cost(u) = 1000 + 500*2 = 3000, above the report the response
	// carries (0.0005 USD): the floor is what charges this call.
	floor, report := lo.Cost(u), pricing.Micros(500)
	if !(report < floor && floor < charged.Cost(u)) {
		t.Fatalf("the fixture's report %d, floor %d and charged rates' %d are not in increasing order", report, floor, charged.Cost(u))
	}
	for _, fee := range []float64{0, 10} {
		t.Run(fmt.Sprintf("fee %v", fee), func(t *testing.T) {
			r := feeRouted(t, fee, bandRow(charged, lo, hi), routedStage, dsRoute, tierCostReply(t, u, "", "0.0005"))
			if resp, b := r.post(msg(dsModel, 4000, `"stream":true`)); resp.StatusCode != http.StatusOK {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			call, rep := r.logs.lastCall(t), r.gw.EndStage()
			want := pricing.WithFee(floor, fee)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the tier's cheapest end with the fee %d", got, want)
			}
			if got := pricing.Micros(num(call["charged_micros"])); got < want {
				t.Errorf("charged %d, below the cheapest end's %d: a report must never price a call below it", got, want)
			}
			if call["priced_as"] != pricedReported || call["settled"] != settledUsage {
				t.Errorf("priced_as %v settled %v, want the report priced from the usage", call["priced_as"], call["settled"])
			}
			if got := pricing.Micros(num(call["reported_micros"])); got != report {
				t.Errorf("reported_micros %v, want the report %d the floor was read against", call["reported_micros"], report)
			}
			if rep.Used != want || rep.ReportedBy[dsModel] != report {
				t.Errorf("stage used %d reported by model %v, want %d and %d", rep.Used, rep.ReportedBy, want, report)
			}
			w := warnLine(t, r.logs, "budget: provider reported cost below the tier's cheapest end")
			if got := pricing.Micros(num(w["floored_micros"])); got != want {
				t.Errorf("floored_micros %v, want %d: the warning names the charge the floor made", w["floored_micros"], want)
			}
			if got := pricing.Micros(num(w["reported_micros"])); got != report {
				t.Errorf("reported_micros %v on the warning, want the report %d", w["reported_micros"], report)
			}
		})
	}
}

// TestMaxPricedCallFloorFromPin: a call the table prices at its maximum --
// the response names a serving model no row of the table prices -- takes
// both the floor under its report and its band from the pin's row, not from
// the maximum rates its charge is computed at (RS7). The pin is the one
// figure the gateway has for the call: a band of Claude's dearest rates
// would claim a prediction nobody made, and a floor there would charge a
// cheap model a dearer one's money. The charge the maximum rates would make
// is the one figure neither case settles at, and the second case shows the
// floor is a floor and not a cap: a report above the pin's cheapest end
// settles at the report, in full.
func TestMaxPricedCallFloorFromPin(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	served := "deepseek/deepseek-v4.1-flash"
	charged := pricing.Rates{InputPerM: 4, OutputPerM: 16, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.4}
	lo := pricing.Rates{InputPerM: 1, OutputPerM: 2, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	hi := pricing.Rates{InputPerM: 8, OutputPerM: 32, CacheWrite5m: 10, CacheWrite1h: 16, CacheRead: 0.8}
	// lo.Cost(u) = 1000 + 500*2 = 2000, the floor; the maximum rates the
	// charge is computed at are dearer than the row's dearest end, so the
	// figure the test reads as "the table's own charge" is neither.
	floor := lo.Cost(u)
	if mx := maxRates().Cost(u); !(floor < hi.Cost(u) && hi.Cost(u) < mx) {
		t.Fatalf("the fixture's rows are not in increasing order: floor %d, dearest end %d, table maximum %d", floor, hi.Cost(u), mx)
	}
	for _, c := range []struct {
		name   string
		cost   float64
		report pricing.Micros
	}{{"below the pin's cheapest end", 0.0005, 500}, {"above it", 0.02, 20_000}} {
		t.Run(c.name, func(t *testing.T) {
			r := feeRouted(t, 5, bandRow(charged, lo, hi), routedStage, dsRoute,
				anthropicfake.Compat{Cost: c.cost, ServedBy: served}.StreamOK(dsModel, u))
			body := msg(dsModel, 4000, `"stream":true`)
			if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			call, rep := r.logs.lastCall(t), r.gw.EndStage()
			if w := pricing.WithFee(worst(t, dsModel, body, 4000, ""), 5); c.report > w {
				t.Fatalf("the fixture's report %d is above the reservation %d: it would be a malformed report's case", c.report, w)
			}
			want := pricing.WithFee(max(c.report, floor), 5)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want %d: the report %d read against the pin's cheapest end %d",
					got, want, c.report, floor)
			}
			if call["priced_as"] != pricedReported || call["settled"] != settledUsage {
				t.Errorf("priced_as %v settled %v, want the report priced", call["priced_as"], call["settled"])
			}
			if rep.ReportedBy[served] != c.report {
				t.Errorf("reported by model %v, want %d in the serving model's key", rep.ReportedBy, c.report)
			}
			if rep.TableLoBy[served] != floor || rep.TableHiBy[served] != hi.Cost(u) {
				t.Errorf("band %v-%v, want the pin's row at %d-%d", rep.TableLoBy, rep.TableHiBy, floor, hi.Cost(u))
			}
		})
	}
}

// TestReportAboveReservationIsOverrun: a report above the reservation the
// call made settles in full, and the part above the reservation is an
// overrun (D2641). The reservation is what the table could prove the call
// might cost; the provider charged more, and it is the charge that is
// recorded -- understating it would be exactly the spend a cap exists to
// refuse. It is not the malformed case below: a figure this side of ten
// times the reservation is a price, however unwelcome.
func TestReportAboveReservationIsOverrun(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 5}
	body := msg(dsModel, 100)
	w := worst(t, dsModel, body, 100, "")
	report := satMul(w, 2)
	if report <= w || report > satMul(w, malformedReportMultiple) {
		t.Fatalf("the fixture's report %d is not above the reservation %d and under ten times it", report, w)
	}
	call, rep := settleCase(t, 0, body, anthropicfake.Compat{Cost: report.USD()}.MessageOK(dsModel, u))
	if got := pricing.Micros(num(call["charged_micros"])); got != report {
		t.Errorf("charged %d, want the provider's own report %d in full", got, report)
	}
	if rep.Used != report || rep.Overrun != report-w {
		t.Errorf("stage used %d overrun %d, want %d and %d: the part above the reservation", rep.Used, rep.Overrun, report, report-w)
	}
	if call["priced_as"] != pricedReported || call["settled"] != settledUsage {
		t.Errorf("priced_as %v settled %v, want the report priced from the usage", call["priced_as"], call["settled"])
	}
}

// TestReportAboveTenTimesReservationFallsBackToTable: a report ten times
// the call's own reservation is not a price but a malformed figure -- a
// wrong currency, a provider's bug, a field the gateway read through the
// wrong lens -- and D2641 settles it at the table's own charge instead,
// with a warning. It is still recorded: it is what the provider said, and
// a band read against the table's is the only way a reader sees how far out
// it was. The cap this protects is the run's: maxReportedUSD would let one
// such figure halt a run and write itself into result.json.
func TestReportAboveTenTimesReservationFallsBackToTable(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	body := msg(dsModel, 1000)
	w := worst(t, dsModel, body, 1000, "")
	const reported = 0.5 // 500000 µ$, far past ten times the fixture's reservation
	if pricing.Micros(reported*1e6) <= satMul(w, malformedReportMultiple) {
		t.Fatalf("the fixture's report %v is not above ten times the reservation %d", reported, w)
	}
	for _, fee := range []float64{0, 10} {
		t.Run(fmt.Sprintf("fee %v", fee), func(t *testing.T) {
			r := feeRouted(t, fee, nil, routedStage, dsRoute, tierCostReply(t, u, "", "0.5"))
			if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			call, rep := r.logs.lastCall(t), r.gw.EndStage()
			want := pricing.WithFee(cost0(t, u), fee)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the table's own charge %d", got, want)
			}
			if call["priced_as"] != pricedTable || call["settled"] != settledUsage {
				t.Errorf("priced_as %v settled %v, want the table settling a complete call", call["priced_as"], call["settled"])
			}
			if rep.Used != want || rep.ReportedBy[dsModel] != 500_000 {
				t.Errorf("stage used %d reported by model %v, want %d and 500000", rep.Used, rep.ReportedBy, want)
			}
			warnLine(t, r.logs, "budget: provider reported cost above ten times the reservation")
		})
	}
}

// TestPartialCallIgnoresReport: a partial call settles as it did before
// this rule (RS2). A cut stream's usage is what the provider had sent when
// the connection ended, not what the call cost, so a cost on such a
// response is no more trustworthy than its token counts: the call is
// charged its reported input and cache plus the reserved output, the
// report goes unrecorded unless it floors an unpriced tier's charge, and
// the line does not claim the report priced it.
func TestPartialCallIgnoresReport(t *testing.T) {
	call, rep := settleCutStream(t, `{"input_tokens":10,"output_tokens":0,"cost":0.05}`, "")
	// The partial rule: the reported input, plus the output the call
	// reserved (max_tokens 1000), at the table's rates.
	read, reserved := pricing.Usage{Input: 10}, pricing.Usage{Input: 10, Output: 1000}
	want := cost(t, dsModel, reserved)
	if got := pricing.Micros(num(call["charged_micros"])); got != want {
		t.Errorf("charged %d, want the partial rule's %d", got, want)
	}
	if call["settled"] != settledPartial || call["priced_as"] != pricedTable {
		t.Errorf("settled %v priced_as %v, want a partial settle at the table", call["settled"], call["priced_as"])
	}
	if _, ok := call["reported_micros"]; ok || rep.Reported != 0 {
		t.Errorf("a partial call recorded the provider's report: %v, %d", call["reported_micros"], rep.Reported)
	}
	if rep.Unreconciled != want-cost(t, dsModel, read) {
		t.Errorf("unreconciled %d, want the reserved output the usage does not explain", rep.Unreconciled)
	}
}

// TestNoReportIsTable: a complete provider call whose response carries no
// cost at all settles exactly as it did before D2640 -- on the table's own
// charge, plus the route's fee -- and is counted as a call the provider
// reported nothing for. A null cost is no cost (the field's own rule): it
// is not a reported zero, which would floor the charge instead.
func TestNoReportIsTable(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	for _, cost := range []string{"", "null"} {
		t.Run("cost "+strconv.Quote(cost), func(t *testing.T) {
			call, rep := settleCase(t, 10, msg(dsModel, 4000, `"stream":true`), tierCostReply(t, u, "", cost))
			want := pricing.WithFee(cost0(t, u), 10)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the table's own charge with the fee %d", got, want)
			}
			if call["priced_as"] != pricedTable || call["settled"] != settledUsage {
				t.Errorf("priced_as %v settled %v, want the table settling a complete call", call["priced_as"], call["settled"])
			}
			if _, ok := call["reported_micros"]; ok || rep.Reported != 0 {
				t.Errorf("a call with no cost recorded one: %v, %d", call["reported_micros"], rep.Reported)
			}
			if rep.UnreportedCalls != 1 || rep.Calls != 1 {
				t.Errorf("unreported_calls %d of %d calls, want 1 of 1", rep.UnreportedCalls, rep.Calls)
			}
		})
	}
}

// TestSurpriseCallKeepsItsPenaltyCharge: a report does not talk the gateway
// out of the penalty charge for a pricing dimension the request did not allow
// (O6). A speed the caller never asked for is charged the table's maximum
// rates with the surprise multiple, exactly as it was before a report settled
// anything, and the report is recorded beside that charge rather than charged:
// the charge follows the violation, and the record still says what the
// provider itself charged.
func TestSurpriseCallKeepsItsPenaltyCharge(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	rep := anthropicfake.Compat{}.StreamOK(dsModel, u)
	rep.Events[0] = rawUsage(t, rep.Events[0], `"speed":"fast","cost":0.05`)
	call, stage := settleCase(t, 10, msg(dsModel, 4000, `"stream":true`), rep)
	want := pricing.WithFee(satMul(maxRates().Cost(u), pricing.SurpriseMultiplier), 10)
	if settled := pricing.WithFee(50_000, 10); settled == want {
		t.Fatalf("the fixture's penalty charge %d is the report's own: the test proves nothing", want)
	}
	if got := pricing.Micros(num(call["charged_micros"])); got != want {
		t.Errorf("charged %d, want the penalty charge %d", got, want)
	}
	if call["priced_as"] != pricedSurprise {
		t.Errorf("priced_as %v, want %q", call["priced_as"], pricedSurprise)
	}
	if stage.Reported != 50_000 || stage.ReportedBy[dsModel] != 50_000 {
		t.Errorf("reported %d by model %v, want the provider's 50000 recorded beside the charge", stage.Reported, stage.ReportedBy)
	}
	if got := pricing.Micros(num(call["reported_micros"])); got != 50_000 {
		t.Errorf("the call log's reported_micros %v, want 50000", got)
	}
}

// TestSurpriseCallWithATierWarnsIfDearer: a response that named both a speed
// the request didn't allow and a service tier the table has no multiplier for
// is charged the surprise penalty (O6), and its tier's warning says the report
// only floors that charge -- "if dearer" -- rather than claiming the call
// settled on the report, which is what the tier alone would have made true
// (D2640). The two figures differ here, so the line is checked against the
// one the charge actually is: a call priced as a surprise never settles on
// its report, however the tier is priced.
func TestSurpriseCallWithATierWarnsIfDearer(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	rep := tierCostReply(t, u, "priority", "0.05")
	rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"speed": "fast"})
	r := feeRouted(t, 10, nil, routedStage, dsRoute, rep)
	if resp, b := r.post(msg(dsModel, 4000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call, stage := r.logs.lastCall(t), r.gw.EndStage()
	want := pricing.WithFee(satMul(maxRates().Cost(u), pricing.SurpriseMultiplier), 10)
	reported := pricing.WithFee(50_000, 10)
	if reported == want {
		t.Fatalf("the fixture's penalty charge %d is the report's own: the test proves nothing", want)
	}
	if got := pricing.Micros(num(call["charged_micros"])); got != want {
		t.Errorf("charged %d, want the penalty charge %d rather than the report's %d", got, want, reported)
	}
	if call["priced_as"] != pricedSurprise {
		t.Errorf("priced_as %v, want %q: a call priced as a surprise keeps its penalty charge", call["priced_as"], pricedSurprise)
	}
	if stage.Reported != 50_000 || stage.ReportedBy[dsModel] != 50_000 {
		t.Errorf("reported %d by model %v, want the provider's 50000 recorded beside the charge", stage.Reported, stage.ReportedBy)
	}
	// The tier's warning may not say the call settled on the report: the
	// multiple is the charge, and the report is only a floor under it.
	if got, want := r.logs.tierWarnChargedAs(t), "2x the rates the call is priced at, or the provider's reported cost with the route fee if dearer"; got != want {
		t.Errorf("charged_as %q, want %q", got, want)
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

// tierCostReply is a provider stream whose usage reports the service tier
// tier ("" for no service_tier key at all) and the cost cost (a JSON literal,
// "" for no cost key at all): the cost is spliced into the usage the fake
// builds, which json.Marshal cannot be told to produce.
func tierCostReply(t *testing.T, u pricing.Usage, tier, cost string) anthropicfake.Reply {
	t.Helper()
	rep := anthropicfake.Compat{}.StreamOK(dsModel, u)
	if tier != "" {
		rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"service_tier": tier})
	}
	if cost != "" {
		rep.Events[0] = rawUsage(t, rep.Events[0], `"cost":`+cost)
	}
	return rep
}

// rawUsage inserts a raw JSON fragment at the front of ev's usage object, for
// a literal -- a cost that isn't a number, a number past the range of a
// float64 -- that no Go value marshalled by the fake can express.
func rawUsage(t *testing.T, ev anthropicfake.Event, frag string) anthropicfake.Event {
	t.Helper()
	const key = `"usage":{`
	i := strings.Index(ev.Data, key)
	if i < 0 {
		t.Fatalf("no usage object in %s", ev.Data)
	}
	j := i + len(key)
	ev.Data = ev.Data[:j] + frag + "," + ev.Data[j:]
	return ev
}

// TestUnpricedTierSettlesOnTheReport: a service tier no one prices is
// charged the conservative multiple only where the table is what prices the
// call. On a provider route that reported what the call cost, the report is
// the charge (D2640) -- the multiple's whole reason was a price the
// provider's own figure could only be guessed at (F12) -- so a report below
// the multiple lowers the charge, which is the correction the spec is for,
// and one above it settles in full. The call is priced_as reported, and the
// per-call warning still names the tier: the response was served at one the
// table has no multiplier for, whatever priced it, and the tier's own
// multiple is what the record's band and the run's estimate fall back on.
func TestUnpricedTierSettlesOnTheReport(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	body := msg(dsModel, 4000, `"stream":true`)
	m := model(t, dsModel)
	lo, hi := m.Rates, m.Rates
	lo.InputPerM, lo.OutputPerM = 0.1, 0.4 // the cheapest end's own charge is 300, well under every report below
	cost0u := m.Rates.Cost(u)
	cons := satMul(cost0u, pricing.ConservativeTierMultiplier)
	if cons <= cost0u {
		t.Fatalf("the fixture's conservative charge %d is no dearer than the standard one", cons)
	}
	// The reservation the call makes, which is what a report is read against.
	res := worst(t, dsModel, body, 4000, "")
	if cons >= res {
		t.Fatalf("the fixture's conservative charge %d already fills the reservation %d", cons, res)
	}
	for _, c := range []struct {
		name, cost string
		fee        float64
		want       pricing.Micros
		reported   pricing.Micros
	}{
		{"reported between the multiple and the reservation", "0.008", 0, 8_000, 8_000},
		{"reported below the multiple", "0.001", 0, 1_000, 1_000},
		{"reported below it, with the route fee", "0.001", 10, 1_100, 1_000},
		{"reported exactly the conservative figure", "0.006", 0, cons, 6_000},
		{"no reported cost", "", 0, cons, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := feeRouted(t, c.fee, bandRow(m.Rates, lo, hi), routedStage, dsRoute, tierCostReply(t, u, "priority", c.cost))
			if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			call, rep := r.logs.lastCall(t), r.gw.EndStage()
			if got := pricing.Micros(num(call["charged_micros"])); got != c.want {
				t.Errorf("charged %d, want %d (the conservative figure is %d)", got, c.want, cons)
			}
			if got := pricing.Micros(num(call["reported_micros"])); got != c.reported {
				t.Errorf("reported_micros %v, want %d", call["reported_micros"], c.reported)
			}
			// The report prices a call that carried one; the multiple prices
			// the call that reported nothing.
			wantAs := pricedReported
			if c.cost == "" {
				wantAs = pricedTier
			}
			if call["priced_as"] != wantAs {
				t.Errorf("priced_as %v, want %q", call["priced_as"], wantAs)
			}
			if rep.Used != c.want || rep.Reported != c.reported {
				t.Errorf("stage used %d reported %d, want %d and %d", rep.Used, rep.Reported, c.want, c.reported)
			}
			if rep.Unreconciled != 0 {
				t.Errorf("stage unreconciled %d, want 0: the charge is priced from usage either way", rep.Unreconciled)
			}
			// The warning names the multiple, and the report where the record
			// carries one: a call that reported no cost has no figure to
			// name and the line stops at the multiple, and a complete call
			// that did settles on the report rather than on the multiple
			// (D2640), so the line says that.
			wantChargedAs := "2x the rates the call is priced at"
			if c.cost != "" {
				wantChargedAs += ", or the provider's reported cost with the route fee, which is what the call settled on, floored at the row's cheapest end"
			}
			if got := r.logs.tierWarnChargedAs(t); got != wantChargedAs {
				t.Errorf("charged_as %q, want %q", got, wantChargedAs)
			}
		})
	}
}

// TestUnpricedTierHugeReportStaysTheConservativeFigure: the largest report
// the gateway records (maxReportedUSD, a million dollars) is five orders of
// magnitude over any reservation a call can make, so it is the malformed
// case (D2641) and the call settles at the table's own charge -- here the
// conservative multiple, the call being served at a tier no one prices --
// and never at the report, the reservation the call made, or anything
// between. maxReportedUSD bounds a figure the record keeps; what keeps a
// single call from halting a run on the cap and writing that figure into
// result.json is this rule.
func TestUnpricedTierHugeReportStaysTheConservativeFigure(t *testing.T) {
	body := msg(dsModel, 4000, `"stream":true`)
	w := worst(t, dsModel, body, 4000, "")
	u := pricing.Usage{Input: 1000, Output: 500}
	cons := cost(t, dsModel, u) * pricing.ConservativeTierMultiplier
	if cons >= w {
		t.Fatalf("the fixture's conservative charge %d already fills the reservation %d", cons, w)
	}
	const huge = "1000000"
	r := feeRouted(t, 0, nil, routedStage, dsRoute, tierCostReply(t, u, "priority", huge))
	if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call, rep := r.logs.lastCall(t), r.gw.EndStage()
	if got := pricing.Micros(num(call["charged_micros"])); got != cons {
		t.Errorf("charged %d, want the conservative figure %d: a report past ten times the reservation is not a price", got, cons)
	}
	// The tier's warning names the multiple alone: a malformed report is not
	// in the charge at all, so the line may not offer it as the figure the
	// call settled on or floored the charge at.
	if got, want := r.logs.tierWarnChargedAs(t), "2x the rates the call is priced at"; got != want {
		t.Errorf("charged_as %q, want %q", got, want)
	}
	if rep.Used != cons || rep.Overrun != 0 {
		t.Errorf("stage used %d overrun %d, want the conservative %d and no overrun", rep.Used, rep.Overrun, cons)
	}
	if call["priced_as"] != pricedTier {
		t.Errorf("priced_as %v, want the tier: the multiple is what priced this call", call["priced_as"])
	}
	// The report is still recorded: it is what the provider said, and the
	// band it is read against is the only thing that shows how far out it is.
	if rep.ReportedBy[dsModel] != 1_000_000_000_000 {
		t.Errorf("reported by model %v, want the malformed figure the provider sent", rep.ReportedBy)
	}
	// A usage that overruns the reservation on its own still does: the
	// charge is the conservative multiple of what the call used, and the
	// part above the reservation is the overrun. The report is not what
	// makes it.
	over := pricing.Usage{Input: 1000, Output: 8000}
	big := cost(t, dsModel, over) * pricing.ConservativeTierMultiplier
	if big <= w {
		t.Fatalf("the fixture's overrunning usage charges %d, not over the reservation %d", big, w)
	}
	call, rep = settleCase(t, 0, body, tierCostReply(t, over, "priority", huge))
	if got := pricing.Micros(num(call["charged_micros"])); got != big {
		t.Errorf("charged %d, want the conservative %d of a usage over the reservation", got, big)
	}
	if rep.Overrun != big-w {
		t.Errorf("overrun %d, want %d: the part of the charge above its reservation", rep.Overrun, big-w)
	}
}

// TestUnpricedTierFloorOnAPartialCall: a partial call is charged its reported
// input and cache tokens plus the reserved output, and the floor applies to it
// too -- without it a call that was cut short could settle below what the
// provider says it cost, which is the hole the floor closes. The floor is
// still bounded by the reservation, and the part of the charge the usage does
// not explain is unreconciled, exactly as it is with no floor at all. The
// report that set the floor is recorded beside the charge the way a complete
// call's is: it is what the provider said the call cost, and it is the only
// figure that explains the charge the reader sees.
func TestUnpricedTierFloorOnAPartialCall(t *testing.T) {
	body := msg(dsModel, 1, `"stream":true`)
	w := worst(t, dsModel, body, 1, "")
	// A cut stream: message_start's usage (input only, no output), no
	// message_delta and no message_stop.
	ev := []anthropicfake.Event{streamEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"`+dsModel+
		`","content":[],"usage":{"input_tokens":10,"output_tokens":0,"service_tier":"priority","cost":0.004}}}`)}
	ev = append(ev, anthropicfake.TextEvents("ok")...)
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call, rep := r.logs.lastCall(t), r.gw.EndStage()
	// What the usage explains: the reported input, at the conservative
	// multiple. The gateway measures the charge against the table price of
	// the usage it read, before the reserved output is added to it, so the
	// reserved output is unreconciled here along with the floor.
	table := pricing.WithFee(satMul(cost(t, dsModel, pricing.Usage{Input: 10}), pricing.ConservativeTierMultiplier), 0)
	floor := pricing.WithFee(4_000, 0) // the report, with the route's fee
	if floor <= table || floor > w {
		t.Fatalf("the fixture's floor %d does not sit between the table's %d and the reservation %d", floor, table, w)
	}
	if got := pricing.Micros(num(call["charged_micros"])); got != floor {
		t.Errorf("charged %d, want the floor %d", got, floor)
	}
	if call["settled"] != settledPartial {
		t.Errorf("settled %v, want a partial settle", call["settled"])
	}
	if rep.Overrun != 0 || rep.Unreconciled != floor-table {
		t.Errorf("stage overrun %d unreconciled %d, want 0 and %d", rep.Overrun, rep.Unreconciled, floor-table)
	}
	// The report is the floor here, so the record carries it: without it the
	// stage's Reported -- and the run's reported_usd -- would leave out the
	// one figure that explains the charge, and diagnose's Tier clause, which
	// is printed only where the record carries a report, would go silent on
	// the run that needs it.
	if got := pricing.Micros(num(call["reported_micros"])); got != 4_000 {
		t.Errorf("reported_micros %v, want the report %d the floor was made of", call["reported_micros"], 4_000)
	}
	if rep.Reported != 4_000 {
		t.Errorf("stage reported %d, want the report %d the floor was made of", rep.Reported, 4_000)
	}
	// And the band is over the usage that report is over -- the input the
	// call sent -- not over the output it reserved, which the provider never
	// produced and which would put the report below the band for good. It is
	// the row's own charge, with no multiple: the conservative tier multiple
	// is what the charge adds to an estimate, and the band is the price the
	// report is read against (F12, D2650).
	band := cost(t, dsModel, pricing.Usage{Input: 10})
	if rep.TableLoBy[dsModel] != band || rep.TableHiBy[dsModel] != band {
		t.Errorf("band %v-%v, want %d each over the reported usage", rep.TableLoBy, rep.TableHiBy, band)
	}
	if rep.ReportedBy[dsModel] != 4_000 {
		t.Errorf("reported by model %v, want the report 4000 the floor was made of", rep.ReportedBy)
	}
	// The warning's words are the partial call's: the multiple is the charge
	// here and the report only floors it, which is the one case left of the
	// "if dearer" sentence a complete call no longer gets (D2640).
	if got, want := r.logs.tierWarnChargedAs(t), "2x the rates the call is priced at, or the provider's reported cost with the route fee if dearer"; got != want {
		t.Errorf("charged_as %q, want %q", got, want)
	}
}

// TestUnpricedTierReportedCostUnusableStaysConservative: a reported cost the
// gateway refuses to record is not a floor either -- it is the malformed
// field the record already ignores -- so the tier's charge stays the
// conservative figure. It must never fall below it: a provider cannot talk
// the charge down with a bad number. The shape-not-number cases (null, a
// string) are TestUsageAccNullCostNotReported's.
func TestUnpricedTierReportedCostUnusableStaysConservative(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	want := cost(t, dsModel, u) * pricing.ConservativeTierMultiplier
	for _, cost := range []string{"-5", "1e30", "1e999", `"cheap"`, "null"} {
		t.Run(cost, func(t *testing.T) {
			call, rep := settleCase(t, 0, msg(dsModel, 4000, `"stream":true`), tierCostReply(t, u, "priority", cost))
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the conservative %d: an unusable report must not move the charge", got, want)
			}
			if _, ok := call["reported_micros"]; ok || rep.Reported != 0 {
				t.Errorf("an unusable cost %s was recorded: %v, %d", cost, call["reported_micros"], rep.Reported)
			}
		})
	}
}

// TestUsageAccReportedCostOutOfRangeNotReported: the bounds the record
// already applies to a provider's cost are what keep the tier floor honest,
// and a value no JSON number can hold is not a number: a NaN, an infinity
// and a figure past maxReportedUSD are all left unrecorded, so a call that
// reported one falls back to the conservative figure rather than to a
// lower -- or an unbounded -- charge. A NaN and an infinity cannot be sent
// over the wire at all (they are not JSON), which is why this is pinned at
// the accumulator and the wire-level shapes are in the test above.
func TestUsageAccReportedCostOutOfRangeNotReported(t *testing.T) {
	for _, raw := range []string{"NaN", "Inf", "-Inf", "-0.5", "1e30", "1e999"} {
		t.Run(raw, func(t *testing.T) {
			var a usageAcc
			a.merge(&wireUsage{Cost: json.RawMessage(raw)})
			if a.hasReported {
				t.Errorf("cost %s was recorded as %d: it must stay unrecorded", raw, a.reported)
			}
		})
	}
}

// TestUnpricedTierOnTheClaudeRouteIgnoresAReportedCost: the floor is a
// provider's own report -- usage.cost, OpenRouter's field -- and it applies
// on a provider route. A Claude call has no route, so a cost field in its
// usage, which Anthropic does not send, neither raises nor lowers its tier
// charge.
func TestUnpricedTierOnTheClaudeRouteIgnoresAReportedCost(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	want := cost(t, sonnet, u) * pricing.ConservativeTierMultiplier
	rep := anthropicfake.StreamOK(sonnet, u)
	rep.Events[0] = withUsage(t, rep.Events[0], map[string]any{"service_tier": "priority", "cost": 1000})
	h := newHarness(t, rep)
	h.post(msg(sonnet, 1000, `"stream":true`))
	r := h.gw.EndStage()
	if r.Used != want {
		t.Errorf("used %d, want the conservative %d", r.Used, want)
	}
	if _, ok := h.logs.lastCall(t)["reported_micros"]; ok {
		t.Errorf("a Claude call recorded a reported cost: %v", h.logs.lastCall(t))
	}
}

// TestKnownTierSettlesOnTheReport: the report settles a complete provider
// call whatever tier served it. A response at the tier the table prices
// (standard), and one that reports no tier at all, are the ordinary shape
// of D2640 -- the figure the provider charged, plus the route fee -- where
// before this rule the table's own price was charged and the report was
// only recorded beside it. Neither tier is recorded: only a tier the table
// has no multiplier for is worth naming (the unpriced-tier tests above).
func TestKnownTierSettlesOnTheReport(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	for _, c := range []struct{ name, tier string }{
		{"the tier the table prices", "standard"},
		{"no tier at all", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			call, rep := settleCase(t, 10, msg(dsModel, 4000, `"stream":true`), tierCostReply(t, u, c.tier, "0.008"))
			want := pricing.WithFee(8_000, 10)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the provider's report with the fee %d", got, want)
			}
			if rep.Used != want || rep.Reported != 8_000 {
				t.Errorf("stage used %d reported %d, want %d and 8000", rep.Used, rep.Reported, want)
			}
			if call["priced_as"] != pricedReported {
				t.Errorf("priced_as %v, want %q", call["priced_as"], pricedReported)
			}
			if len(rep.ServiceTiers.List()) != 0 {
				t.Errorf("service tiers %q, want none", rep.ServiceTiers.List())
			}
		})
	}
}

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
	// And it is in none of the per-model figures that figure is the total of
	// (SA1.2): a report recorded beside a band over the output the call
	// reserved would put the run's report below its own band.
	if rep.Reported != 0 || len(rep.ReportedBy) != 0 || len(rep.TableLoBy) != 0 || len(rep.TableHiBy) != 0 {
		t.Errorf("a cut stream's partial cost reached the report split: %d %v %v-%v",
			rep.Reported, rep.ReportedBy, rep.TableLoBy, rep.TableHiBy)
	}
	if rep.UnreportedCalls != 1 {
		t.Errorf("unreported_calls %d, want the cut call counted as one with no recorded report", rep.UnreportedCalls)
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

// route is recorded per call and per stage; a Claude call has none. One
// stage makes both calls -- a routed one and a Claude one -- at different
// usage, so each model's own charge shows in ByModel under its own rate and
// Used is the two added: a figure that swapped the models' rates, or that
// reported only one of them, fails here (mixed-credentials plan Task 2,
// Review Focus 7).
func TestRouteRecordedPerCall(t *testing.T) {
	routed, claude := pricing.Usage{Input: 1000, Output: 2000}, pricing.Usage{Input: 3000, Output: 1000}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, routed))
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, claude)}
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if resp, b := r.post(msg(sonnet, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	rep := r.gw.EndStage()
	wantRouted, wantClaude := cost(t, dsModel, routed), cost(t, sonnet, claude)
	if wantRouted == wantClaude {
		t.Fatal("the two models' charges coincide: a swapped rate would not show")
	}
	if got := rep.ByRoute["openrouter"]; got != wantRouted {
		t.Errorf("by route %v, want openrouter %d", rep.ByRoute, wantRouted)
	}
	if len(rep.ByRoute) != 1 {
		t.Errorf("a Claude call has a route: %v", rep.ByRoute)
	}
	if got := rep.ByModel[dsModel]; got != wantRouted {
		t.Errorf("by model %v, want the routed model at its own rate %d", rep.ByModel, wantRouted)
	}
	if got := rep.ByModel[sonnet]; got != wantClaude {
		t.Errorf("by model %v, want the Claude model at its own rate %d", rep.ByModel, wantClaude)
	}
	if got := rep.Used; got != wantRouted+wantClaude {
		t.Errorf("stage used %d, want the two models added: %d", got, wantRouted+wantClaude)
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

// TestFixtureEndToEndFlooredAtThePin sends the abridged OpenRouter/DeepInfra
// fixture (testdata/openrouter_deepinfra_stream.sse) through the full
// gateway (not just the tee) with a 5.5% route fee, pinned to the model
// the embedded table knows (deepseek/deepseek-v4-flash). The fixture's
// message_delta names 32 input, 0 cache read, 47 output tokens for a served
// model (deepseek/deepseek-v4.1-flash) the table has no row for, and
// reports what it charged for them ($0.00002422). The served model's own
// figure is a tenth of what the pin's row charges the same tokens -- the
// row this test table carries is a placeholder at $1 in / $4 out per M --
// so the call settles at the pin's cheapest end (D2651, RS7): 32*1 + 47*4 =
// 220 µ$, +5.5% fee ceil(220*0.055) = ceil(12.1) = 13; total 233. The
// report priced the call, so the line says so (RS4), and the warning names
// the figure the floor raised.
func TestFixtureEndToEndFlooredAtThePin(t *testing.T) {
	ev := fixtureEvents(t, "testdata/openrouter_deepinfra_stream.sse")
	r := feeRouted(t, 5.5, nil, routedStage, dsRoute, anthropicfake.Reply{Status: http.StatusOK, Events: ev})
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	if got := num(call["charged_micros"]); got != 233 {
		t.Errorf("charged %d, want 233 (32*1 + 47*4 = 220, +5.5%% fee ceil(12.1)=13)", got)
	}
	if got := num(call["reported_micros"]); got != 24 {
		t.Errorf("reported_micros %v, want 24 (the provider's $0.00002422)", call["reported_micros"])
	}
	if call["priced_as"] != pricedReported {
		t.Errorf("priced_as %v, want %q: the provider's figure is what the call settles at", call["priced_as"], pricedReported)
	}
	warnLine(t, r.logs, "budget: provider reported cost below the tier's cheapest end")
}

// TestFixtureEndToEndOwnerRates is the same fixture and fee, but the
// owner has configured model_prices for the served model itself
// (deepseek/deepseek-v4.1-flash), pinned directly so there is no
// pin/serving mismatch to take the dearer of two rates (MaxOf):
// input $0.31, output $1.25 per M, cache_read a 0.02 multiplier of
// input_per_m (unused: the fixture's cache_read_input_tokens is 0), the
// model_prices shape documented in docs/multi-model.md §2. 32*0.31 +
// 47*1.25 = 9.92 + 58.75 = 68.67, rounded to the nearest micro-dollar: 69;
// +5.5% fee ceil(69*0.055) = ceil(3.795) = 4; total 73. The provider's own
// figure for these tokens is $0.00002422, a third of what this row charges
// them, so the call is floored at the row's cheapest end -- here the row's
// only number, as flatEnds gives an owner's override (D2651) -- and settles
// at the same 73 it settled at before reports became the settlement, now
// saying which figure the call was priced from (RS4).
func TestFixtureEndToEndOwnerRates(t *testing.T) {
	const v41 = "deepseek/deepseek-v4.1-flash"
	prices := func(tbl *pricing.Table) {
		r := pricing.Rates{InputPerM: 0.31, OutputPerM: 1.25, CacheRead: 0.02}
		tbl.Models[v41] = pricing.Model{ID: v41, Rates: r, Lo: r, Hi: r}
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
	if call["priced_as"] != pricedReported {
		t.Errorf("priced_as %v, want %q: the provider's figure priced the call, and the row's floor raised it", call["priced_as"], pricedReported)
	}
	if got := num(call["reported_micros"]); got != 24 {
		t.Errorf("reported_micros %v, want 24 (the provider's $0.00002422)", call["reported_micros"])
	}
	warnLine(t, r.logs, "budget: provider reported cost below the tier's cheapest end")
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

// OWNER RULING (round 3, overriding round 2's low-severity fix): a usage
// object that never includes an input_tokens key at all, in any event —
// as opposed to sending input_tokens:0 explicitly, TestZeroInputHeavyCacheSettles's
// shape, which keeps settling from usage — has left input out entirely,
// not merely reported it as zero; the gateway settles at the reservation
// for that, a real cache read elsewhere in the same usage notwithstanding.
// Review-measured (JSON path, reservation 8199): a round-2 fix made this
// charge 2005 instead; this reverts it. Covers the stream path too (input
// absent in every event).
func TestCacheReadOnlyNoInputKeyChargesReservation(t *testing.T) {
	t.Run("stream", func(t *testing.T) {
		call, rep, logs := settleStream(t,
			`{"cache_read_input_tokens":900,"output_tokens":1}`,
			`{"cache_read_input_tokens":900,"output_tokens":50}`)
		if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
			t.Errorf("charged %d of %d reserved, settled %v, want the reservation", got, res, call["settled"])
		}
		if rep.UsageUnparsed != 1 {
			t.Errorf("usage_unparsed %d, want 1", rep.UsageUnparsed)
		}
		if reason := lastUnparsedReason(t, logs); reason != "input_zero_no_cache" {
			t.Errorf("reason %q, want input_zero_no_cache", reason)
		}
	})
	t.Run("json", func(t *testing.T) {
		reply := anthropicfake.Reply{Status: http.StatusOK,
			Body: `{"type":"message","model":"` + dsModel + `","content":[],"usage":{"cache_read_input_tokens":900,"output_tokens":50}}`}
		call, rep := settleCase(t, 0, msg(dsModel, 1000), reply)
		if got, res := pricing.Micros(num(call["charged_micros"])), pricing.Micros(num(call["reserved_micros"])); got != res || res == 0 || call["settled"] != settledReserved {
			t.Errorf("charged %d of %d reserved, settled %v, want the reservation", got, res, call["settled"])
		}
		if rep.UsageUnparsed != 1 {
			t.Errorf("usage_unparsed %d, want 1", rep.UsageUnparsed)
		}
	})
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

// --- the per-model report and the table's band (SA1.2, D2650) ---

// bandRow sets dsModel's row to one whose charged rates r sit between two
// different ends: the shape a catalog-priced row has once the tier it was
// selected from carries a range, which is what the band is read against
// (SA1.6).
func bandRow(r, lo, hi pricing.Rates) func(*pricing.Table) {
	return func(tbl *pricing.Table) {
		m := tbl.Models[dsModel]
		m.Rates, m.Lo, m.Hi = r, lo, hi
		tbl.Models[dsModel] = m
	}
}

// TestStageReportCarriesReportedByModel: the provider's own figure reaches the
// stage's report split by the model that served the call and with no route
// fee, and it is the figure the call settles at with the fee (D2640) -- so the
// charge the split carries and the report it carries are the same call's two
// sides. The fee is why they differ: reported_usd names what the provider
// charged, by_model what the run paid.
func TestStageReportCarriesReportedByModel(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	call, rep := settleCase(t, 5, msg(dsModel, 4000, `"stream":true`), tierCostReply(t, u, "", "0.05"))
	if rep.Reported != 50_000 || rep.ReportedBy[dsModel] != 50_000 {
		t.Errorf("reported %d by model %v, want 50000 in the serving model's key", rep.Reported, rep.ReportedBy)
	}
	want := pricing.WithFee(50_000, 5)
	if rep.Used != want || rep.ByModel[dsModel] != want {
		t.Errorf("charged %d, want the report with the fee %d", rep.Used, want)
	}
	if table := pricing.WithFee(cost0(t, u), 5); table == want {
		t.Fatalf("the fixture's report and table charge coincide (%d): the split proves nothing", want)
	}
	if rep.UnreportedCalls != 0 {
		t.Errorf("unreported_calls %d, want 0: the response carried a cost", rep.UnreportedCalls)
	}
	if got := num(call["reported_micros"]); got != 50_000 {
		t.Errorf("the call log's reported_micros %v, want 50000", got)
	}
	if call["priced_as"] != pricedReported {
		t.Errorf("priced_as %v, want %q", call["priced_as"], pricedReported)
	}
}

// TestTableBandOverReportedCallsOnly: the band is what the table would have
// charged the calls a provider reported a cost for, at the selected row's two
// ends and with no route fee. A call whose response carried no cost is in
// neither end -- the table settled it, so its own charge would make the band
// agree with itself -- and is counted in unreported_calls instead.
func TestTableBandOverReportedCallsOnly(t *testing.T) {
	reported := pricing.Usage{Input: 1000, Output: 2000}
	silent := pricing.Usage{Input: 5000, Output: 5000}
	charged := pricing.Rates{InputPerM: 1, OutputPerM: 4, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	lo := pricing.Rates{InputPerM: 0.5, OutputPerM: 2, CacheWrite5m: 0.625, CacheWrite1h: 1, CacheRead: 0.05}
	hi := pricing.Rates{InputPerM: 2, OutputPerM: 8, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.2}
	r := feeRouted(t, 10, bandRow(charged, lo, hi), routedStage, dsRoute,
		tierCostReply(t, reported, "", "0.05"),
		anthropicfake.Compat{}.StreamOK(dsModel, silent))
	for i := 0; i < 2; i++ {
		if resp, b := r.post(msg(dsModel, 4000, `"stream":true`)); resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d %s", i+1, resp.StatusCode, b)
		}
	}
	rep := r.gw.EndStage()
	wantLo, wantHi := lo.Cost(reported), hi.Cost(reported)
	if charge := pricing.WithFee(charged.Cost(reported), 10); !(wantLo < charge && charge < wantHi) {
		t.Fatalf("the fixture's band %d-%d doesn't bracket the charge %d: the ends prove nothing", wantLo, wantHi, charge)
	}
	if lo.Cost(silent) == 0 {
		t.Fatal("the unreported call's own band is zero: counting it would not show")
	}
	if rep.TableLoBy[dsModel] != wantLo || rep.TableHiBy[dsModel] != wantHi {
		t.Errorf("band %v-%v, want %d-%d over the reported call alone",
			rep.TableLoBy, rep.TableHiBy, wantLo, wantHi)
	}
	if rep.UnreportedCalls != 1 || rep.Calls != 2 {
		t.Errorf("unreported_calls %d of %d calls, want 1 of 2", rep.UnreportedCalls, rep.Calls)
	}
	if rep.ReportedBy[dsModel] != 50_000 {
		t.Errorf("reported by model %v, want the reported call's 50000", rep.ReportedBy)
	}
	l := r.gw.Ledger()
	if l.TableLoBy[dsModel] != wantLo || l.TableHiBy[dsModel] != wantHi || l.UnreportedCalls != 1 {
		t.Errorf("ledger band %v-%v unreported %d, want %d-%d and 1",
			l.TableLoBy, l.TableHiBy, l.UnreportedCalls, wantLo, wantHi)
	}
}

// TestBandFromPinWhenServingUnknown: a call the table prices at its maximum
// has no row of its own, so its band is the pin's row's ends (RS7) -- the
// live shape, where a provider serves a model ID the catalog doesn't price --
// and a call whose pin the table doesn't know either is in the report and in
// neither end of the band. A call on the Anthropic upstream records no report
// at all, whatever its response carries: the report split is of the figure
// the run's own Reported holds.
func TestBandFromPinWhenServingUnknown(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	served := "deepseek/deepseek-v4.1-flash"
	r := feeRouted(t, 0, nil, routedStage, dsRoute,
		anthropicfake.Compat{Cost: 0.05, ServedBy: served}.StreamOK(dsModel, u))
	if resp, b := r.post(msg(dsModel, 4000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	rep := r.gw.EndStage()
	want := cost0(t, u)
	if rep.TableLoBy[served] != want || rep.TableHiBy[served] != want {
		t.Errorf("band %v-%v, want the pin's row at %d each", rep.TableLoBy, rep.TableHiBy, want)
	}
	if got := maxRates().Cost(u); got == want {
		t.Fatalf("the fixture's rows coincide (%d): the band proves nothing about which row it came from", want)
	}
	if rep.ByModel[served] != 50_000 {
		t.Errorf("the call was charged %d, want the provider's 50000: the band is the pin's row, not the charge",
			rep.ByModel[served])
	}
	if rep.ReportedBy[served] != 50_000 {
		t.Errorf("reported by model %v, want the serving model's 50000", rep.ReportedBy)
	}

	// A call the gateway sends to the Anthropic upstream, whose response
	// carries a cost all the same: the report the gateway records is the one
	// its own Reported total holds, and that is the figure a route reports
	// for a call the table priced -- so this one is in no split, in no band,
	// and counted as a call with no recorded report. Keeping it out is what
	// makes a stage's reported calls and its report total the same set.
	anthropic := "claude-mystery-9"
	h := newHarness(t, anthropicfake.Compat{Cost: 0.05}.StreamOK(anthropic, u))
	h.gw.BeginStage(Stage{Name: "implement", Model: anthropic})
	if resp, b := h.post(msg(anthropic, 4000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	direct := h.gw.EndStage()
	if direct.Reported != 0 || len(direct.ReportedBy) != 0 {
		t.Errorf("an Anthropic call's cost reached the report: %d %v", direct.Reported, direct.ReportedBy)
	}
	if len(direct.TableLoBy) != 0 || len(direct.TableHiBy) != 0 {
		t.Errorf("a call in no report has a band: %v-%v", direct.TableLoBy, direct.TableHiBy)
	}
	if direct.UnreportedCalls != 1 || direct.Calls != 1 {
		t.Errorf("unreported_calls %d of %d calls, want the call counted as one with no recorded report",
			direct.UnreportedCalls, direct.Calls)
	}
	if l := h.gw.Ledger(); len(l.ReportedBy) != 0 || l.UnreportedCalls != 1 {
		t.Errorf("ledger report %v unreported %d, want the call in neither", l.ReportedBy, l.UnreportedCalls)
	}
}
