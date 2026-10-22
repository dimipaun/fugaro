package gateway

import (
	"net/http"
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
