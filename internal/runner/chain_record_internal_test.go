package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/routing"
)

// The record's chain members (C1, CS8): which credential source served a
// stage, whether the stage ever fell off its chain's first source, and the
// real money the run's fallbacks spent.

const (
	chainSonnet = "claude-sonnet-5-5"
	chainHaiku  = "claude-haiku-4-5"
)

// TestTheRecordNamesTheSource (C1, CS8): the stage's row carries the source the
// credentials service served its calls from and whether any of them was a
// fallback, and the wire carries the members the contract fixes.
//
// The two calls are the fold's two halves: the first came from a provider the
// chain only reaches when its first source could not serve (a fallback), and
// the second from the chain's first source, which is the one the row names
// because it is the last one the stage ended on. The flag is not the last
// call's answer but the fold of them all, so the stage is a fallback stage
// whatever came last.
//
// The mutation it fails on is the member being absent: a record that cannot say
// which source served a step is what this task closes.
func TestTheRecordNamesTheSource(t *testing.T) {
	r := costRun(t)
	r.chainAnswered(gateway.ChainAnswer{
		Source:   routing.SourceName{Kind: routing.KindProvider, Name: "openrouter"},
		Fallback: true, Model: chainHaiku,
	})
	r.chainAnswered(gateway.ChainAnswer{
		Source: routing.SourceName{Kind: routing.KindAnthropic},
		Model:  chainSonnet,
	})
	r.rec.Stages = append(r.rec.Stages, r.stageTiming("implement", time.Now(), 0, 0, 0))

	st := r.rec.Stages[0]
	if st.Source == nil {
		t.Fatal("the stage names no source: the service's answer reached no row")
	}
	if st.Source.Kind != routing.KindAnthropic || st.Source.Name != "" {
		t.Errorf("the stage's source is %+v, want the source of its last answered call (anthropic, which names nothing)", *st.Source)
	}
	if !st.Fallback {
		t.Error("the stage is not flagged a fallback: one of its calls came from a source that is not the chain's first")
	}

	// The members the contract fixes are the wire's, not the Go names: a
	// member renamed in Go is a compile error at every call site, while one
	// renamed in JSON is silent (contract-table.md's rule for a reader
	// outside this binary).
	b, err := json.Marshal(r.rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"source":{"kind":"anthropic","name":""}`, `"fallback":true`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the record does not carry %s:\n%s", want, b)
		}
	}

	// The fold is the stage's and not the run's: the next stage starts with
	// nothing of this one's, which is what keeps a source off a stage the
	// service answered no call of.
	next := r.stageTiming("review", time.Now(), 0, 0, 0)
	if next.Source != nil || next.Fallback {
		t.Errorf("the next stage is %+v, want no source and no fallback", next)
	}
}

// TestFallbackUSDIsRealMoneyOnly (C1, CS8, D140): the record's fallback_usd is
// the real money the run's fallback calls spent, as the gateway's ledger
// settled it, and it is a part of the run's model figure rather than a figure
// beside it.
//
// The rule that a subscription's fallback is not money is the ledger's, and it
// is pinned where it is decided (internal/gateway's
// TestTheLedgerCountsOnlyPaidFallbacksInFallbackUSD). What is pinned here is the
// record's half: the figure is the ledger's own, it is inside model_usd and not
// added to it, and the notional part of the run -- a fallback the plan served
// among it -- stays in model_notional_usd where no cap and no bill counts it.
//
// The mutation it fails on is the field being left unwritten, or being written
// as the run's whole model figure: a fallback a subscription served would then
// be reported as money nobody paid.
func TestFallbackUSDIsRealMoneyOnly(t *testing.T) {
	r := costRun(t)
	// A run whose chain fell back onto a key (the money) and then onto a
	// subscription (the plan's usage): 0.75 of its 2.00 model dollars are the
	// fallback's, and the 3.00 the subscription's calls would have cost at
	// list rates are notional.
	r.recordLedgerCost(gateway.Ledger{Used: 2_000_000, Notional: 3_000_000, FallbackUSD: 750_000})
	r.rec.CostUSD = 5
	r.updateCost()

	c := r.rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	if c.FallbackUSD != 0.75 {
		t.Errorf("fallback_usd = %v, want the ledger's 0.75", c.FallbackUSD)
	}
	if c.ModelUSD != 5 || c.ModelNotionalUSD != 3 {
		t.Errorf("model_usd/model_notional_usd = %v/%v, want 5/3: the fallback money is inside the run's figure", c.ModelUSD, c.ModelNotionalUSD)
	}
	if c.ModelMoneyUSD() != 2 {
		t.Errorf("the run's money = %v, want 2: the subscription's share is notional", c.ModelMoneyUSD())
	}
	if c.FallbackUSD > c.ModelMoneyUSD() {
		t.Errorf("fallback_usd %v is more than the run's money %v: the notional plan price reached the paid figure", c.FallbackUSD, c.ModelMoneyUSD())
	}
	if c.TotalUSD != c.ModelMoneyUSD() {
		t.Errorf("total_usd = %v, want the billed %v: fallback_usd is a part of the model figure and never a second charge", c.TotalUSD, c.ModelMoneyUSD())
	}

	// A run whose fallbacks a subscription served in full records none at
	// all: the member is absent, which is the same fact as zero for a reader.
	sub := costRun(t)
	sub.recordLedgerCost(gateway.Ledger{Notional: 3_000_000})
	sub.rec.CostUSD = 3
	sub.updateCost()
	if sub.rec.Cost.FallbackUSD != 0 {
		t.Errorf("fallback_usd = %v for a run whose fallbacks the plan served, want none", sub.rec.Cost.FallbackUSD)
	}
	b, err := json.Marshal(sub.rec.Cost)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fallback_usd") {
		t.Errorf("the cost block carries fallback_usd for a run with no paid fallback:\n%s", b)
	}
}
