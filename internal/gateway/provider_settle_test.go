package gateway

import (
	"encoding/json"
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

// TestUnpricedTierChargedAtLeastReportedCost: the flat multiple an unpriced
// service tier is charged (ConservativeTierMultiplier) is an assumed
// ceiling, not a published one -- no provider publishes a multiplier for a
// tier other than standard -- so on a provider route that reported what the
// call cost, the provider's own figure is a floor under the charge: the
// dearer of the conservative figure and the report with the route's fee, the
// same fee handling every provider charge already carries, and no more than
// the reservation the call made (the next test). It can only raise the
// charge, never lower it: a report below the conservative figure leaves the
// figure alone, and so does a call that reported no cost at all. A charge
// the floor raised is still not an overrun -- it stops at the reservation --
// and it is priced from usage the whole way, so none of it is unreconciled.
func TestUnpricedTierChargedAtLeastReportedCost(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	body := msg(dsModel, 4000, `"stream":true`)
	cons := cost(t, dsModel, u) * pricing.ConservativeTierMultiplier
	if cons <= cost(t, dsModel, u) {
		t.Fatalf("the fixture's conservative charge %d is no dearer than the standard one", cons)
	}
	// The reservation the call makes, which is what caps the floor.
	res := func(fee float64) pricing.Micros { return pricing.WithFee(worst(t, dsModel, body, 4000, ""), fee) }
	if cons >= res(0) {
		t.Fatalf("the fixture's conservative charge %d already fills the reservation %d", cons, res(0))
	}
	for _, c := range []struct {
		name, cost string
		fee        float64
		want       pricing.Micros
		reported   pricing.Micros
	}{
		{"reported above the conservative figure", "0.05", 0, res(0), 50_000},
		{"reported above it, with the route fee", "0.05", 10, res(10), 50_000},
		{"reported below the conservative figure", "0.001", 0, cons, 1_000},
		{"reported below it, with the route fee", "0.001", 10, pricing.WithFee(cons, 10), 1_000},
		{"reported exactly the conservative figure", "0.006", 0, cons, 6_000},
		{"no reported cost", "", 0, cons, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			call, rep := settleCase(t, c.fee, body, tierCostReply(t, u, "priority", c.cost))
			if got := pricing.Micros(num(call["charged_micros"])); got != c.want {
				t.Errorf("charged %d, want %d (the conservative figure is %d)", got, c.want, cons)
			}
			if got := pricing.Micros(num(call["charged_micros"])); got < cons {
				t.Errorf("charged %d, below the conservative figure %d: a report must never lower the charge", got, cons)
			}
			if got := pricing.Micros(num(call["reported_micros"])); got != c.reported {
				t.Errorf("reported_micros %v, want %d", call["reported_micros"], c.reported)
			}
			if call["priced_as"] != pricedTier {
				t.Errorf("priced_as %v, want the tier", call["priced_as"])
			}
			if rep.Used != c.want || rep.Reported != c.reported {
				t.Errorf("stage used %d reported %d, want %d and %d", rep.Used, rep.Reported, c.want, c.reported)
			}
			if rep.Overrun != 0 || rep.Unreconciled != 0 {
				t.Errorf("stage overrun %d unreconciled %d, want 0 and 0: the floor stops at the reservation and prices usage",
					rep.Overrun, rep.Unreconciled)
			}
		})
	}
}

// TestUnpricedTierFloorIsBoundedByTheReservation: the floor stops at the
// reservation the call already made. maxReportedUSD bounds a figure recorded
// for comparison, not a charge, and the provider's report is the one part of
// this charge the gateway cannot check against anything of its own: unclamped,
// a report at that bound would charge a single call millions of times its own
// worst case, halt the run on the cap and write that figure into
// result.json. What a report may do is raise the charge as far as the
// reservation; a charge above the reservation stays what the call's own usage
// made it -- an overrun, logged and counted as one -- exactly as before this
// rule, and the floor adds nothing to it.
func TestUnpricedTierFloorIsBoundedByTheReservation(t *testing.T) {
	body := msg(dsModel, 4000, `"stream":true`)
	w := worst(t, dsModel, body, 4000, "")
	u := pricing.Usage{Input: 1000, Output: 500}
	cons := cost(t, dsModel, u) * pricing.ConservativeTierMultiplier
	if cons >= w {
		t.Fatalf("the fixture's conservative charge %d already fills the reservation %d", cons, w)
	}
	// The largest report the gateway records (maxReportedUSD, a million
	// dollars): a figure five orders of magnitude over the reservation.
	const huge = "1000000"
	call, rep := settleCase(t, 0, body, tierCostReply(t, u, "priority", huge))
	if got := pricing.Micros(num(call["charged_micros"])); got != w {
		t.Errorf("charged %d, want the reservation %d: a report may raise the charge to it, never past it", got, w)
	}
	if rep.Used != w || rep.Overrun != 0 {
		t.Errorf("stage used %d overrun %d, want the reservation %d and no overrun", rep.Used, rep.Overrun, w)
	}
	// A usage that overruns the reservation on its own still does: the
	// charge is the conservative multiple of what the call used, and the
	// part above the reservation is the overrun. The report is not what
	// makes it, and the floor does not add to it.
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
// not explain is unreconciled, exactly as it is with no floor at all.
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

// TestKnownTierAndNoTierIgnoreAReportedCost: the floor is the tier's, not
// the report's: a response at the tier the table prices (standard), and one
// that reports no tier at all, are charged the table price of their usage
// plus the route fee whether or not the provider reported a cost -- the
// reported figure is recorded beside the charge and changes nothing, which
// is what every provider call did before this rule.
func TestKnownTierAndNoTierIgnoreAReportedCost(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 500}
	for _, c := range []struct{ name, tier string }{
		{"the tier the table prices", "standard"},
		{"no tier at all", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			call, rep := settleCase(t, 10, msg(dsModel, 4000, `"stream":true`), tierCostReply(t, u, c.tier, "0.05"))
			want := pricing.WithFee(cost(t, dsModel, u), 10)
			if got := pricing.Micros(num(call["charged_micros"])); got != want {
				t.Errorf("charged %d, want the table price plus the fee %d", got, want)
			}
			if rep.Used != want || rep.Reported != 50_000 {
				t.Errorf("stage used %d reported %d, want %d and 50000", rep.Used, rep.Reported, want)
			}
			if call["priced_as"] != pricedTable {
				t.Errorf("priced_as %v, want the table", call["priced_as"])
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
