package gateway

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

// The chain's answers reach the ledger for two figures of its own (C1, CS8,
// D140): the model a settled call is accounted by, which is the model that
// served and not the one the question named, and the money a metered source
// spent serving a fallback.
//
// These are settled directly rather than posted through the harness because
// nothing reads the answer's members off the service's response headers yet --
// that read, and the option that makes the service write them at all, are CS2's
// (chainwire.go, Options.ChainServed) -- so no call this tree forwards carries
// an answer, and no forward-path test can post one. What is pinned here is the
// ledger's half of the member: a filled answer moves the rows, the per-stage
// split and band, the run's last-settled model and fallback_usd. It is NOT the
// price: the amount below is a figure this test hands the ledger exactly as
// forward.go hands it one, and pricing a call at the served model's rates, band
// and pinSpelling is CS2's half of the same clause, in CS2's forward.go --
// charge.chain's own comment carries the coupling between the two.

// servedRun is the gateway's money after one settled call with answer's
// members, with amount charged at the tier and the usage read: what the ledger
// did with the call, as the run reads it.
func settleChain(t *testing.T, h *harness, c charge) {
	t.Helper()
	st := h.gw.enterStage()
	defer st.calls.Done()
	if msg, status := h.gw.reserve(context.Background(), st, c.amount); status != 0 {
		t.Fatalf("reserve refused: %d %s", status, msg)
	}
	h.gw.settle(st, c.amount, c)
}

// TestTheLedgerPricesTheModelThatServed (C1, CS8, D140): the answer's model is
// the one a settled call's dollars, its model rows and the run's last-settled
// model follow, whatever name the response body carried back.
//
// The body's name is the one the agent asked for in this call and the answer's
// is not: a source entry may name a model (a written downgrade) and a route's
// own spelling differs from the canonical one (CS3), so the body carries the
// question and only the answer says what served. Pricing the call by the body
// would charge the model the agent asked for while another model answered.
//
// What this test does not do is price: the amount is handed in, exactly as
// forward.go hands the ledger one, so the rates the call was charged at are
// fromUsage's and CS2's (the package comment above says why they must land
// together, and charge.chain's comment in ledger.go says it to a reader of the
// field).
//
// The tokens a repricing of the run reads back (Ledger.ModelTokens, F8) are the
// one figure the answer does NOT move, and deliberately: they are keyed by the
// model the gateway ADMITTED the request under, which is the run's own choice,
// and the counts a later repricing asks the table for must be that choice's.
func TestTheLedgerPricesTheModelThatServed(t *testing.T) {
	const amount = pricing.Micros(1_250_000)
	usage := pricing.Usage{Input: 1000, Output: 200}
	// sonnet is the pin the request named and the name the body carried back;
	// haiku is what the service's answer says actually served it.
	h := newHarness(t)
	settleChain(t, h, charge{
		amount: amount, model: sonnet, pricedModel: sonnet, tier: TierB,
		usage: usage, usageRead: true, sent: true,
		chain: ChainAnswer{
			Source:   routing.SourceName{Kind: routing.KindProvider, Name: "openrouter"},
			Fallback: true,
			Model:    haiku,
		},
	})

	rep, led := h.gw.EndStage(), h.gw.Ledger()
	if got := rep.ByModel[haiku]; got != amount {
		t.Errorf("the stage's row for the serving model %s = %d, want the call's %d charged against it", haiku, got, amount)
	}
	if got, ok := rep.ByModel[sonnet]; ok {
		t.Errorf("the stage charged %d to %s, the model the agent asked for: the call was served by %s", got, sonnet, haiku)
	}
	if rep.Used != amount || led.Used != amount {
		t.Errorf("used = %d/%d, want the call's %d: a fallback on a key is money like any other call", rep.Used, led.Used, amount)
	}
	// The stage's tokens ride beside its dollars, and the run's per-model
	// counts stay the pin's.
	if got := rep.TokensBy[haiku].Input; got != usage.Input {
		t.Errorf("the stage's input tokens by serving model = %d, want the call's %d on %s", got, usage.Input, haiku)
	}
	if got := led.ModelTokens[sonnet].Input; got != usage.Input {
		t.Errorf("model_tokens[%s].Input = %d, want %d: the counts a repricing reads back are the pin's", sonnet, got, usage.Input)
	}
	if _, ok := led.ModelTokens[haiku]; ok {
		t.Errorf("model_tokens carries %s: the admitted model is what the run's own counts are keyed by", haiku)
	}
	// A paid fallback is real money, held to the caps as any key call is, and
	// it is the part of the run's money a fallback bought.
	if led.FallbackUSD != amount {
		t.Errorf("fallback_usd = %d, want the fallback call's %d", led.FallbackUSD, amount)
	}
	if got := rep.ByTier[TierB]; got != amount {
		t.Errorf("the tier split = %d, want the call's %d: a fallback is charged by the tier that served it", got, amount)
	}
}

// TestTheLedgerCountsOnlyPaidFallbacksInFallbackUSD (C1, CS8, D140): a fallback
// a subscription served is notional -- it is charged as the plan's usage and
// counts against its limits -- and not one dollar of it is in fallback_usd,
// because not one dollar of it was paid.
func TestTheLedgerCountsOnlyPaidFallbacksInFallbackUSD(t *testing.T) {
	const paid, notional = pricing.Micros(1_250_000), pricing.Micros(3_000_000)
	answer := ChainAnswer{Source: routing.SourceName{Kind: routing.KindPool, Name: "team"}, Fallback: true, Model: sonnet}
	h := newHarness(t)
	settleChain(t, h, charge{amount: paid, model: sonnet, pricedModel: sonnet, tier: TierB, sent: true, chain: answer})
	h.gw.EndStage()
	h.gw.BeginStage(defaultStage)
	settleChain(t, h, charge{amount: notional, model: sonnet, pricedModel: sonnet, tier: TierA, sent: true, chain: answer})

	led := h.gw.Ledger()
	if led.Used != paid {
		t.Errorf("used = %d, want the key's %d alone", led.Used, paid)
	}
	if led.Notional != notional {
		t.Errorf("notional = %d, want the subscription's %d", led.Notional, notional)
	}
	if led.FallbackUSD != paid {
		t.Errorf("fallback_usd = %d, want the paid fallback's %d: a subscription's list price is notional and is not money spent", led.FallbackUSD, paid)
	}
}

// TestNoAnswerSettlesAsBefore (C1, CS8): a call the credentials service did not
// answer -- every call of every run it does not serve -- carries no chain
// answer, and settles by the model the response named with nothing counted as a
// fallback. The member is new; the behaviour it leaves untouched is the run's
// whole history.
func TestNoAnswerSettlesAsBefore(t *testing.T) {
	const amount = pricing.Micros(2_000_000)
	h := newHarness(t)
	settleChain(t, h, charge{amount: amount, model: sonnet, pricedModel: sonnet, tier: TierB, sent: true})

	rep, led := h.gw.EndStage(), h.gw.Ledger()
	if got := rep.ByModel[sonnet]; got != amount {
		t.Errorf("the stage's row for %s = %d, want the call's %d", sonnet, got, amount)
	}
	if led.FallbackUSD != 0 {
		t.Errorf("fallback_usd = %d, want 0: no answer named a fallback", led.FallbackUSD)
	}
}
