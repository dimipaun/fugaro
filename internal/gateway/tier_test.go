package gateway

import (
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// --- per-call tier accounting (C1.16, D140) ---

// tierReply is one served Messages answer from a tier of the cascade: the
// model's own answer, and the tier and credential name every Messages answer
// of the contract carries (C1.10's headers).
func tierReply(tier, name string, u pricing.Usage) anthropicfake.Reply {
	r := anthropicfake.MessageOK(sonnet, u)
	r.Header = http.Header{HeaderTier: []string{tier}, HeaderCredential: []string{name}}
	return r
}

// TestCapIgnoresNotional: a tier-A call is the run's subscription, not
// money. What it costs settles as notional and the cap never counts it
// (D140, F25: "only tiers B and C count against project and per-repo
// spending caps"), so a run whose cap holds one call's worst case can still
// make a second tier-A call.
func TestCapIgnoresNotional(t *testing.T) {
	u := pricing.Usage{Input: 500_000, Output: 10}
	c := cost(t, sonnet, u)
	body := msg(sonnet, 5)
	w := worst(t, sonnet, body, 5, "")
	h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return tierReply(TierA, "repo-a", u)
	}, func(o *Options) {
		// A cap that holds one call's worst case and half of a second
		// one's charge: a notional charge that were counted would refuse
		// the second call, and one that is not counted cannot.
		o.Mode, o.Cap = Enforce, w+c/2
	})

	for i := 1; i <= 2; i++ {
		resp, b := h.post(body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("tier-A call %d: %d %s", i, resp.StatusCode, b)
		}
	}
	noHalt(t, h)
	if n := f.Count(); n != 2 {
		t.Errorf("the service was sent %d calls, want 2", n)
	}
	l := h.gw.Ledger()
	if l.Used != 0 {
		t.Errorf("the cap counts %s of notional spend, want none", dollars(l.Used))
	}
	rep := h.gw.EndStage()
	if l.Notional != 2*c || rep.Notional != 2*c {
		t.Errorf("notional: ledger %s, stage %s, want 2 x %s", dollars(l.Notional), dollars(rep.Notional), dollars(c))
	}
	if rep.Used != 0 || rep.Calls != 2 || len(rep.ByTier) != 0 {
		t.Errorf("a tier-A stage reported used %s, calls %d, byTier %v", dollars(rep.Used), rep.Calls, rep.ByTier)
	}
	if l.Reserved != 0 {
		t.Errorf("the ledger still holds %s in reserve", dollars(l.Reserved))
	}
}

// TestSettleTierANotional: the tier the service's answer carries is the
// ledger's, not the log's: a call served from tier A settles as notional --
// in the stage's report, in the ledger, and in no by-tier key -- and is
// priced exactly as the same call served by any other tier would be, so the
// run still says what its model spend was.
func TestSettleTierANotional(t *testing.T) {
	u := pricing.Usage{Input: 6000, CacheRead: 300, Output: 40}
	c := cost(t, sonnet, u)
	for _, tc := range []struct {
		name  string
		reply anthropicfake.Reply
	}{
		{"buffered", tierReply(TierA, "repo-a", u)},
		{"streamed", func() anthropicfake.Reply {
			r := anthropicfake.StreamOK(sonnet, u)
			r.Header = http.Header{HeaderTier: []string{TierA}, HeaderCredential: []string{"repo-a"}}
			return r
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply { return tc.reply }, enforceCap(c*10))
			body := msg(sonnet, 100)
			if tc.name == "streamed" {
				body = msg(sonnet, 100, `"stream":true`)
			}
			if w := worst(t, sonnet, body, 100, ""); c <= w {
				t.Fatalf("the fixture no longer charges over its reservation (%s vs %s)", dollars(c), dollars(w))
			}
			if resp, b := h.post(body); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call: %d %s", resp.StatusCode, b)
			}
			rep := h.gw.EndStage()
			if rep.Used != 0 || rep.Notional != c || rep.Calls != 1 {
				t.Errorf("stage used %s notional %s calls %d, want 0, %s, 1", dollars(rep.Used), dollars(rep.Notional), rep.Calls, dollars(c))
			}
			// This call cost more than its reservation (the usage is a whole
			// context, the request asks for 100 output tokens), and a
			// notional call over its reservation is not an overrun: nothing
			// was over-released against a cap, because no cap paid it.
			if rep.Overrun != 0 {
				t.Errorf("a tier-A call over its reservation was counted as %s of overrun", dollars(rep.Overrun))
			}
			if rep.ByModel[sonnet] != c {
				t.Errorf("the model split is %v, want the notional charge in it", rep.ByModel)
			}
			if len(rep.ByTier) != 0 {
				t.Errorf("a tier-A call is in the by-tier split: %v", rep.ByTier)
			}
			if l := h.gw.Ledger(); l.Used != 0 || l.Notional != c || l.Reserved != 0 {
				t.Errorf("ledger %+v", l)
			}
			call := h.logs.lastCall(t)
			if got := pricing.Micros(num(call["charged_micros"])); got != c {
				t.Errorf("the call was charged %s, want %s: a tier-A call is priced like any other", dollars(got), dollars(c))
			}
		})
	}
}

// TestSettleTierBReal: a tier-B (and a tier-C) call is money: it settles
// against the cap in the stage's Used, in the ledger, and in its own
// by-tier key, and it never reaches the notional counter.
func TestSettleTierBReal(t *testing.T) {
	u := pricing.Usage{Input: 6000, CacheRead: 300, Output: 40}
	c := cost(t, sonnet, u)
	for _, tier := range []string{TierB, TierC} {
		t.Run(tier, func(t *testing.T) {
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				return tierReply(tier, "repo-"+tier, u)
			}, enforceCap(c*10))

			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call: %d %s", resp.StatusCode, b)
			}
			rep := h.gw.EndStage()
			body := msg(sonnet, 100)
			if w := worst(t, sonnet, body, 100, ""); c <= w {
				t.Fatalf("the fixture no longer charges over its reservation (%s vs %s)", dollars(c), dollars(w))
			} else if rep.Overrun != c-w {
				t.Errorf("overrun %s, want %s (charged %s, reserved %s)", dollars(rep.Overrun), dollars(c-w), dollars(c), dollars(w))
			}
			if rep.Used != c || rep.Notional != 0 || rep.Calls != 1 {
				t.Errorf("stage used %s notional %s calls %d, want %s, 0, 1", dollars(rep.Used), dollars(rep.Notional), rep.Calls, dollars(c))
			}
			if rep.ByTier[tier] != c || len(rep.ByTier) != 1 {
				t.Errorf("the by-tier split is %v, want %s under %q alone", rep.ByTier, dollars(c), tier)
			}
			if l := h.gw.Ledger(); l.Used != c || l.Notional != 0 || l.Reserved != 0 {
				t.Errorf("ledger %+v", l)
			}
		})
	}
}

// TestNoTierRunHasNoByTier: a call that did not go through the service has
// no tier, so it belongs to no by-tier key. It is real money all the same,
// and the by-tier split is a split of tiered calls, not a second total: a
// run with no cascade writes nothing under byTier, which is what "written
// only by a cascade run" means at the ledger (C1.16, C1.3).
func TestNoTierRunHasNoByTier(t *testing.T) {
	u := pricing.Usage{Input: 1200, Output: 40}
	c := cost(t, sonnet, u)
	h := newHarnessWith(t, enforceCap(c*10), anthropicfake.MessageOK(sonnet, u))
	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the call: %d %s", resp.StatusCode, b)
	}
	rep := h.gw.EndStage()
	if rep.Used != c || rep.Notional != 0 {
		t.Errorf("stage used %s notional %s, want %s, 0", dollars(rep.Used), dollars(rep.Notional), dollars(c))
	}
	if len(rep.ByTier) != 0 {
		t.Errorf("a call with no tier is in the by-tier split: %v", rep.ByTier)
	}
}

// TestOneReservationCoversServiceRetry: the service's own retry -- a second
// credential tried inside one client request (C1.11) -- is the service's
// business, and the run pays for it exactly once. The gateway sends the
// request upstream once, holds one reservation for it, and settles the call
// once, from the usage of the answer that arrived, at that answer's tier;
// the switch the service wrote says which credential it moved to, and the
// charge is not doubled by it.
func TestOneReservationCoversServiceRetry(t *testing.T) {
	u := pricing.Usage{Input: 900, Output: 50}
	c := cost(t, sonnet, u)
	// The service tried tier A, found it exhausted, and answered from tier
	// B: one answer, one usage, one switch.
	h, f := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		r := tierReply(TierB, "repo-shared-b", u)
		r.Header.Set(HeaderSwitch, `{"from":"repo-a","to":"repo-shared-b","reason":"exhausted"}`)
		return r
	}, func(o *Options) {
		o.Mode, o.Cap = Enforce, c*10
	})

	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the call: %d %s", resp.StatusCode, b)
	}
	if n := f.Count(); n != 1 {
		t.Errorf("the gateway sent the service %d requests for one client request, want 1", n)
	}
	rep := h.gw.EndStage()
	if rep.Calls != 1 || rep.Used != c {
		t.Errorf("one client request settled as %d calls of %s, want 1 of %s", rep.Calls, dollars(rep.Used), dollars(c))
	}
	if l := h.gw.Ledger(); l.Reserved != 0 {
		t.Errorf("the ledger still holds %s: the retry took a reservation of its own", dollars(l.Reserved))
	}
	if call := h.logs.lastCall(t); num(call["reserved_micros"]) == 0 {
		t.Error("the call settled with no reservation recorded")
	}
}

// TestSwitchLogRecorded: what a switch costs is the gateway's estimate, not
// the service's (F24): the cached context of the last call, re-read at the
// input rate instead of the cache-read rate on the tier that answered. The
// run's switch log is {run, from, to, reason, estimated cache-miss cost}
// (D140); the record itself is written where the run's answers are (C1.19),
// and this is the answer it writes from -- the service's own record,
// verbatim, beside a figure only the gateway can compute.
func TestSwitchLogRecorded(t *testing.T) {
	const sw = `{"from":"repo-a","to":"repo-shared-b","reason":"exhausted"}`
	cached := int64(300)
	u := pricing.Usage{Input: 900, CacheRead: cached, Output: 40}
	// 300 cached tokens at sonnet's list rates -- $2/M in, cache reads at
	// 0.1 x in -- is 300 x $1.8/M of miss.
	const want = 0.00054
	for _, tc := range []struct {
		tier string
		cost SwitchCost
	}{
		{TierB, SwitchCost{USD: want}},
		// Tier C is OpenRouter, where Claude's cache behaviour is
		// unmeasured: the record says so rather than $0 (F24, Checks 46/50).
		{TierC, SwitchCost{Unmeasured: true}},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			var answers []ServiceAnswer
			var n atomic.Int64
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				if n.Add(1) == 1 {
					return tierReply(TierA, "repo-a", u)
				}
				r := tierReply(tc.tier, "repo-shared-b", pricing.Usage{Input: 800, Output: 40})
				r.Header.Set(HeaderSwitch, sw)
				return r
			}, func(o *Options) {
				o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
			})

			// The first call is the one whose cache a switch re-reads: it
			// is settled before the second answer is read (one stage each,
			// so the order is the ledger's and not the scheduler's).
			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call before the switch: %d %s", resp.StatusCode, b)
			}
			h.gw.EndStage()
			h.gw.BeginStage(defaultStage)
			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call that switched: %d %s", resp.StatusCode, b)
			}
			h.gw.EndStage()

			if len(answers) != 2 {
				t.Fatalf("the hook was told about %d answers, want 2", len(answers))
			}
			if got := answers[0]; got.Switch != "" || got.SwitchCost != (SwitchCost{}) {
				t.Errorf("a call that switched nothing carried a switch: %+v", got)
			}
			got := answers[1]
			if got.Switch != sw {
				t.Errorf("the switch read back as %q, want the service's own record verbatim", got.Switch)
			}
			if got.Tier != tc.tier {
				t.Errorf("the switch's answer was read as tier %q, want %q", got.Tier, tc.tier)
			}
			if got.SwitchCost.Unmeasured != tc.cost.Unmeasured || math.Abs(got.SwitchCost.USD-tc.cost.USD) > 1e-9 {
				t.Errorf("the switch's cache miss was estimated at %+v, want %+v", got.SwitchCost, tc.cost)
			}
		})
	}
}

// TestSwitchCostUnmeasuredWithoutALastCall: the estimate needs a last call
// whose cached context is known. Before any call has reported usage there is
// nothing to re-read, and a call that settled at its reservation because its
// usage could not be read leaves what it had cached unknown -- which is not
// zero. A switch in either place says unmeasured rather than a miss that
// looks free.
func TestSwitchCostUnmeasuredWithoutALastCall(t *testing.T) {
	const sw = `{"to":"repo-shared-b","reason":"not_admitted"}`
	for _, tc := range []struct {
		name  string
		first *anthropicfake.Reply
	}{
		{"no call yet", nil},
		{"a call whose usage was not readable", &anthropicfake.Reply{
			// A 2xx the gateway cannot read (an encoding it does not
			// decode), so the call settles at its reservation: it was
			// served, it is paid for, and what it had cached is not known.
			Events:   []anthropicfake.Event{anthropicfake.Ping},
			Encoding: "br",
			Header:   http.Header{HeaderTier: []string{TierA}, HeaderCredential: []string{"repo-a"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []ServiceAnswer
			var n atomic.Int64
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				if n.Add(1) == 1 && tc.first != nil {
					return *tc.first
				}
				r := tierReply(TierB, "repo-shared-b", pricing.Usage{Input: 800, Output: 40})
				r.Header.Set(HeaderSwitch, sw)
				return r
			}, func(o *Options) {
				o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
			})
			if tc.first != nil {
				if resp, _ := h.post(msg(sonnet, 100, `"stream":true`)); resp.StatusCode != http.StatusOK {
					t.Fatalf("the call before the switch: %d", resp.StatusCode)
				}
				if rep := h.gw.EndStage(); rep.UsageUnparsed != 1 {
					t.Fatalf("the unreadable call settled as parsed: %+v", rep)
				}
				h.gw.BeginStage(defaultStage)
			}
			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call that switched: %d %s", resp.StatusCode, b)
			}
			h.gw.EndStage()
			last := answers[len(answers)-1]
			if last.Switch != sw || !last.SwitchCost.Unmeasured {
				t.Errorf("the switch's estimate is %+v, want unmeasured", last.SwitchCost)
			}
		})
	}
}

// TestSwitchCostUnmeasuredAfterACallThatReportedNoUsage (F24, C1.16): the
// estimate is the last call's cached context, and a call that settled with
// no usage at all -- an error the upstream relayed, say -- read none of it.
// Its absent usage is not zero cache reads: taking it for zero would turn
// the next switch into a measured $0, while the call before it may well have
// read a whole cached context, and re-reading that is exactly what the
// switch costs. Unknown stays unknown, whichever of the two settled last.
func TestSwitchCostUnmeasuredAfterACallThatReportedNoUsage(t *testing.T) {
	const sw = `{"to":"repo-shared-b","reason":"not_admitted"}`
	var answers []ServiceAnswer
	var n atomic.Int64
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		if n.Add(1) == 1 {
			// The upstream's error, relayed: the call is paid for, and its
			// answer carries no usage at all.
			return anthropicfake.Reply{Status: http.StatusBadRequest, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"too long"}}`}
		}
		r := tierReply(TierB, "repo-shared-b", pricing.Usage{Input: 800, Output: 40})
		r.Header.Set(HeaderSwitch, sw)
		return r
	}, func(o *Options) {
		o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
	})
	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode == http.StatusOK {
		t.Fatalf("the call that was to report no usage was served: %s", b)
	}
	if rep := h.gw.EndStage(); rep.Calls != 1 || rep.UsageUnparsed != 0 {
		t.Fatalf("that call settled as %+v, want one counted call and no usage read", rep)
	}
	h.gw.BeginStage(defaultStage)
	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the call that switched: %d %s", resp.StatusCode, b)
	}
	h.gw.EndStage()
	last := answers[len(answers)-1]
	if last.Switch != sw {
		t.Fatalf("the switch was not read off the answer: %+v", last)
	}
	if !last.SwitchCost.Unmeasured || last.SwitchCost.USD != 0 {
		t.Errorf("the switch after a call with no usage is estimated at %+v, want unmeasured", last.SwitchCost)
	}
}

// probeReply is a served Messages answer by a call whose credential was
// probed first (C1.12): the contract's own headers, plus HeaderProbe naming
// the tiers the service's probes were made on.
func probeReply(tier, name string, u pricing.Usage, probed ...string) anthropicfake.Reply {
	r := tierReply(tier, name, u)
	r.Header.Set(HeaderProbe, strings.Join(probed, ","))
	return r
}

// TestAProbeSettlesByTheTierItWasMadeOn is C1.16's rule for a probe: the
// service asked a backed-off credential whether it works before serving this
// call (C1.12), so a call was made that the run pays for -- accounted
// notional on tier A and money on any other tier, by the tier the PROBE was
// made on and not the tier that answered.
//
// The two split apart on the third case, which is the one that matters: a
// probe that found tier A dead leaves the call to be served from B, and the
// probe's figure must land as notional while the call's lands as money.
func TestAProbeSettlesByTheTierItWasMadeOn(t *testing.T) {
	u := pricing.Usage{Input: 6000, Output: 40}
	body := msg(sonnet, 100)
	c := cost(t, sonnet, u)
	// The call's own usage is dearer than its reservation, which is the one
	// overrun in this test: what a probe must not add to.
	w := worst(t, sonnet, body, 100, "")
	if c <= w {
		t.Fatalf("the fixture no longer charges over its reservation (%s vs %s)", dollars(c), dollars(w))
	}
	// What one probe can cost: the minimal call the service builds, priced
	// at the model's worst case (one output token, the body's own floor).
	p := model(t, sonnet).WorstCase(pricing.Request{MaxTokens: probeMaxTokens})
	if p <= 0 || p >= c {
		t.Fatalf("the fixture's probe figure (%s) is not smaller than the call's (%s)", dollars(p), dollars(c))
	}
	for _, tc := range []struct {
		name     string
		tier     string
		probed   []string
		used     pricing.Micros
		notional pricing.Micros
		byTier   map[string]pricing.Micros
	}{
		{"tier A's own probe is notional", TierA, []string{TierA}, 0, c + p, map[string]pricing.Micros{}},
		{"tier B's probe is money", TierB, []string{TierB}, c + p, 0, map[string]pricing.Micros{TierB: c + p}},
		{"a probe on A under a call served from B", TierB, []string{TierA}, c, p, map[string]pricing.Micros{TierB: c}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				return probeReply(tc.tier, "repo-a", u, tc.probed...)
			}, enforceCap(p+c*10))
			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
				t.Fatalf("the call: %d %s", resp.StatusCode, b)
			}
			rep := h.gw.EndStage()
			if rep.Used != tc.used || rep.Notional != tc.notional {
				t.Errorf("the stage reports used %s notional %s, want %s and %s",
					dollars(rep.Used), dollars(rep.Notional), dollars(tc.used), dollars(tc.notional))
			}
			// The probe and the call it was made for: two calls reached an
			// upstream and the run says so.
			if rep.Calls != 2 {
				t.Errorf("the stage counts %d calls, want the probe and the call", rep.Calls)
			}
			if len(rep.ByTier) != len(tc.byTier) {
				t.Fatalf("the by-tier split is %v, want %v", rep.ByTier, tc.byTier)
			}
			for tier, want := range tc.byTier {
				if rep.ByTier[tier] != want {
					t.Errorf("the by-tier split's %s is %s, want %s", tier, dollars(rep.ByTier[tier]), dollars(want))
				}
			}
			// Everything charged is in the model split, whichever way it
			// settled: the probe's model is the run's own.
			if rep.ByModel[sonnet] != tc.used+tc.notional {
				t.Errorf("the model split is %v, want every figure charged", rep.ByModel)
			}
			if l := h.gw.Ledger(); l.Used != tc.used || l.Notional != tc.notional || l.Reserved != 0 {
				t.Errorf("ledger %+v, want used %s notional %s and nothing reserved", l, dollars(tc.used), dollars(tc.notional))
			}
			// A probe is charged whole and is not an overrun: it holds no
			// reservation of its own. It sits inside the answering
			// request's, which is what C1.11's one reservation covers, and
			// the only overrun here is the call's own usage over what that
			// reservation held.
			wantOver := pricing.Micros(0)
			if tc.tier != TierA {
				wantOver = c - w
			}
			if rep.Overrun != wantOver || rep.Unreconciled != 0 {
				t.Errorf("overrun %s (want the call's own %s) and %s unreconciled",
					dollars(rep.Overrun), dollars(wantOver), dollars(rep.Unreconciled))
			}
		})
	}
}

// TestAProbeCountsAgainstTheCapOnTierB: money is money (D140, F25): a
// tier-B probe is spend like any other, so a cap that holds the call and its
// probe but not a second call refuses the second one.
func TestAProbeCountsAgainstTheCapOnTierB(t *testing.T) {
	u := pricing.Usage{Input: 6000, Output: 40}
	body := msg(sonnet, 100)
	w := worst(t, sonnet, body, 100, "")
	p := model(t, sonnet).WorstCase(pricing.Request{MaxTokens: probeMaxTokens})
	// Room for one call (its reservation, w) and its probe, and not for a
	// second call's reservation on top of them.
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return probeReply(TierB, "repo-a", u, TierB)
	}, enforceCap(w+p+1))

	if resp, b := h.post(body); resp.StatusCode != http.StatusOK {
		t.Fatalf("the first call: %d %s", resp.StatusCode, b)
	}
	if resp, b := h.post(body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the second call was not refused by the cap the probe helped reach: %d %s", resp.StatusCode, b)
	}
	if halt, ok := h.halted(); !ok || halt.Reason != "run_cap" {
		t.Errorf("the halt is %+v, want the run cap", halt)
	}
}

// TestAProbeLeavesWhatTheRunHadCached: a probe's own answer is closed unread
// by the service (C1.12), so this gateway knows nothing of what the probe
// read -- and what the run had cached is the answering call's usage. A
// switch that follows a call which was served after a probe must still
// estimate the miss from that call (F24): a probe mistaken for it would make
// the figure unmeasured, and a figure that reads "unmeasured" for a cache
// the gateway saw is a switch reported as costing nothing.
func TestAProbeLeavesWhatTheRunHadCached(t *testing.T) {
	cached := int64(300)
	u := pricing.Usage{Input: 900, CacheRead: cached, Output: 40}
	const want = 0.00054 // 300 cached tokens at sonnet's rates, as TestSwitchLogRecorded
	var answers []ServiceAnswer
	var n atomic.Int64
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		if n.Add(1) == 1 {
			return probeReply(TierA, "repo-a", u, TierA)
		}
		r := tierReply(TierB, "repo-shared-b", pricing.Usage{Input: 800, Output: 40})
		r.Header.Set(HeaderSwitch, `{"from":"repo-a","to":"repo-shared-b","reason":"exhausted"}`)
		return r
	}, func(o *Options) {
		o.OnServiceAnswer = func(a ServiceAnswer) { answers = append(answers, a) }
	})

	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the probed call: %d %s", resp.StatusCode, b)
	}
	h.gw.EndStage()
	h.gw.BeginStage(defaultStage)
	if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the call that switched: %d %s", resp.StatusCode, b)
	}
	h.gw.EndStage()

	if len(answers) != 2 {
		t.Fatalf("the hook was told about %d answers, want 2", len(answers))
	}
	if got := answers[0]; len(got.Probed) != 1 || got.Probed[0] != TierA {
		t.Errorf("the probed answer was read as %v, want the tier the probe was made on", got.Probed)
	}
	got := answers[1]
	if len(got.Probed) != 0 {
		t.Errorf("a call that probed nothing carries %v", got.Probed)
	}
	if got.SwitchCost.Unmeasured || math.Abs(got.SwitchCost.USD-want) > 1e-9 {
		t.Errorf("the switch's cache miss was estimated at %+v, want the probed call's own cache reads", got.SwitchCost)
	}
}

// TestAProbedTierThatIsNotTheContractsHalts: the tiers of the probe header
// are a closed set the ledger charges by (C1.16's by-tier split has a leaf
// for B and C and none for anything else), so an answer naming one this
// contract does not carry is a halt rather than a figure nobody can place.
func TestAProbedTierThatIsNotTheContractsHalts(t *testing.T) {
	for _, value := range []string{"D", "A,A", "A,B,C,A", "a", "repo-a", ""} {
		t.Run(value, func(t *testing.T) {
			h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
				r := tierReply(TierA, "repo-a", pricing.Usage{Input: 900, Output: 40})
				r.Header.Set(HeaderProbe, value)
				return r
			}, enforceCap(pricing.Micros(1_000_000)))
			resp, _ := h.post(msg(sonnet, 100))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a probe header of %q was accepted: %d", value, resp.StatusCode)
			}
			if halt, ok := h.halted(); !ok {
				t.Error("the run was not halted")
			} else if len(value) > 1 && strings.Contains(halt.Detail, value) {
				t.Errorf("the halt's detail quotes the header: %q", halt.Detail)
			}
			if rep := h.gw.EndStage(); rep.Calls != 1 {
				t.Errorf("the stage counts %d calls, want the one that settled at its reservation", rep.Calls)
			}
		})
	}
}
