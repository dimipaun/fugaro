package runner

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// svcTierARun is a cascade run whose service answers the subscription's
// credential (tier A) and reports the switch it made getting there: C1.16's
// flow end to end inside the job -- the run's own gateway on the service
// upstream, the answer reaching the run through r.serviceAnswer, and the
// settled figures reaching the record through the run's own stage bookkeeping.
func svcTierARun(t *testing.T, edit func(*run)) *run {
	t.Helper()
	// The first call is the run's own subscription call; the second is one the
	// service moves, which is also the only one whose switch can carry a
	// cache-miss estimate: what a move re-reads is the last settled call's
	// cache, and the first call of a run has none behind it (F24). Both report
	// a cached context, so the estimate is a figure rather than a zero.
	var calls int32
	r := svcServedRunWith(t, func() (http.Header, pricing.Usage) {
		h := http.Header{
			gateway.HeaderTier:       []string{gateway.TierA},
			gateway.HeaderCredential: []string{"repo-shared-a"},
		}
		if atomic.AddInt32(&calls, 1) > 1 {
			h.Set(gateway.HeaderSwitch, `{"from":"repo-shared-b","to":"repo-shared-a","reason":"exhausted"}`)
		}
		return h, pricing.Usage{Input: tierACacheRead, CacheRead: tierACacheRead, Output: 3}
	}, func(r *run) {
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Status: runstore.StatusRunning}
		if edit != nil {
			edit(r)
		}
	})
	r.sess = svcSession(t)
	return r
}

// tierACacheRead is the cached context the fixture's tier-A calls report, in
// tokens: enough that a switch's estimated miss is a figure a test can tell
// from zero.
const tierACacheRead = 1_000_000

// startServedGateway starts the run's own gateway (startGateway, so the service
// answer callback and the lease are the run's own) and stops it when the test
// ends.
func startServedGateway(t *testing.T, r *run) {
	t.Helper()
	if err := r.startGateway(context.Background()); err != nil {
		t.Fatalf("the run's own gateway: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.gw.Close(ctx)
	})
}

// TestGatewayStageCostTellsNotionalFromMoney (C1.16, D140, P12): a stage whose
// calls were served on the run's subscription settles as notional, and the
// record says so where the two are told apart -- the stage's and the run's
// model_notional_usd beside model_usd, which keeps its documented meaning (the
// whole model figure, notional dollars included) so the sum invariant holds.
// total_usd is what was billed and does not carry the notional part, the
// models' split measures the same whole figure the stages carry, and the switch
// the service reported is in the run's log beside the cache-miss estimate the
// gateway computed for it.
func TestGatewayStageCostTellsNotionalFromMoney(t *testing.T) {
	r := svcTierARun(t, nil)
	startServedGateway(t, r)

	for i := 0; i < 2; i++ {
		if code, body := svcPost(t, r); code != http.StatusOK {
			t.Fatalf("the agent's call %d was answered %d: %s", i+1, code, body)
		}
	}
	// The stage's own end, as stageOnce drives it: stageExtra is the run's
	// endGatewayStage, which closes the stage in the gateway and takes its
	// figures into the run's.
	if violations, _ := r.stageExtra("implement"); len(violations) != 0 {
		t.Fatalf("the stage's calls were refused: %v", violations)
	}

	// The ledger settled one tier-A call and no money at all: the call is the
	// subscription's, so nothing here is billed.
	r.mu.Lock()
	used, notional, rep := r.gwUsed, r.gwNotional, r.lastStage
	r.mu.Unlock()
	if used != 0 {
		t.Fatalf("the run charged %v as money for a tier-A call", used.USD())
	}
	if notional <= 0 || rep == nil || rep.Notional != notional {
		t.Fatalf("the run's notional figure is %v, the stage's report %+v", notional.USD(), rep)
	}
	// A tier-A call is in no by-tier leaf: that split holds the tiers that paid
	// in money, and A paid in the plan (C1.16).
	if len(rep.ByTier) != 0 {
		t.Fatalf("the tier-A stage has a by-tier split %v", rep.ByTier)
	}

	// The run's own bookkeeping for the stage that just ended, in the order
	// runner.go does it: the cost, then the timing.
	r.recordGatewayStageCost("implement", notional.USD(), false)
	r.updateCost()
	r.rec.Stages = append(r.rec.Stages, r.stageTiming("implement", time.Now().Add(-time.Minute), notional.USD()))

	want := notional.USD()
	if len(r.rec.Stages) != 1 {
		t.Fatalf("the record's stages are %+v", r.rec.Stages)
	}
	st := r.rec.Stages[0]
	if st.ModelUSD != want || st.ModelNotionalUSD != want {
		t.Fatalf("the stage is %+v, want model_usd and model_notional_usd both %v", st, want)
	}
	if r.rec.CostUSD != want {
		t.Fatalf("cost_usd = %v, want the whole model figure %v", r.rec.CostUSD, want)
	}
	c := r.rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	if c.ModelUSD != want || c.ModelNotionalUSD != want {
		t.Fatalf("cost = %+v, want model_usd and model_notional_usd both %v", c, want)
	}
	if c.ModelSource != "gateway" || c.ModelBasis != runstore.BasisAPIList {
		t.Fatalf("cost = %+v, want the gateway's figure on the run's own basis", c)
	}
	// Billed dollars: the whole model figure is notional, so none of it is.
	if c.TotalUSD != 0 {
		t.Fatalf("total_usd = %v, want the notional part left out of it", c.TotalUSD)
	}
	// The models' split is measured against the same whole figure the stages
	// carry, so it adds up to model_usd with no phantom residual.
	if got := c.ModelBy["claude-sonnet-5-5"] + c.ModelBy[unattributedModel]; got != want {
		t.Fatalf("model_by = %v, want it to add up to the model figure %v", c.ModelBy, want)
	}
	if c.ModelBy[unattributedModel] != 0 {
		t.Fatalf("model_by = %v, want no unattributed residual: the call carried a stage", c.ModelBy)
	}

	// The switch log: the service's record of the move, and the gateway's
	// estimate beside it. A tier-A call's cache is measured, so this one is a
	// figure rather than "unmeasured" -- its size is the gateway's own, and
	// only this run's usage can make it zero, which the answer's one input
	// token does.
	if len(r.rec.Switches) != 1 {
		t.Fatalf("the switch log is %+v, want the switch the answer carried", r.rec.Switches)
	}
	sw := r.rec.Switches[0]
	if sw.From != "repo-shared-b" || sw.To != "repo-shared-a" || sw.Reason != "exhausted" {
		t.Fatalf("the switch is %+v, want the service's own from, to and reason", sw)
	}
	if sw.CacheMissUnmeasured || sw.CacheMissUSD <= 0 {
		t.Fatalf("the switch is %+v, want the gateway's own estimate of the miss beside it", sw)
	}
	if r.rec.OAuthAccount != "repo-shared-a" {
		t.Fatalf("the record names the credential %q", r.rec.OAuthAccount)
	}
}

// TestCrossCheckCostComparesTheWholeModelFigure (C1.16, F25): the cross-check
// holds Claude Code's own total_cost_usd against the gateway's whole model
// figure -- the money and the notional part together -- because that is what
// Claude Code's total counts: it prices every call the stage made, whichever
// credential paid for it. Compared against the billed part alone, a cascade
// stage on the subscription would read as a total disagreement.
func TestCrossCheckCostComparesTheWholeModelFigure(t *testing.T) {
	const used, notional = pricing.Micros(0), pricing.Micros(2_500_000) // $2.50, all of it the plan's
	whole := (used + notional).USD()

	logs := &costLogs{}
	r := &run{d: Deps{Log: slog.New(logs)}, lastStage: &gateway.StageReport{Used: used, Notional: notional}}
	r.crossCheckCost("implement", whole, false)
	if got := logs.records(); len(got) != 0 {
		t.Fatalf("the cross-check warned when Claude Code agreed with the gateway's whole model figure: %v", got)
	}
	// The billed part alone is not that figure: $0 against $2.50 is exactly the
	// disagreement the cross-check exists to see.
	r.crossCheckCost("implement", used.USD(), false)
	got := logs.records()
	if len(got) != 1 {
		t.Fatalf("the cross-check recorded %d warnings, want the one for the stage whose money and notional part were confused", len(got))
	}
	if gw, ok := got[0]["gateway_usd"].(float64); !ok || gw != whole {
		t.Fatalf("the warning's gateway figure is %v, want the whole model figure %v", got[0]["gateway_usd"], whole)
	}
}

// TestSwitchLogDropsAHeaderItCannotRead: a switch header this build cannot read
// is logged and dropped rather than recorded as something it is not. The value
// is text from another process and reaches no record and no sentence.
func TestSwitchLogDropsAHeaderItCannotRead(t *testing.T) {
	logs := &costLogs{}
	r := &run{d: Deps{Log: slog.New(logs)}, rec: &runstore.Record{Version: 1, RunID: svcRunID}}
	// No reason at all, and a reason this contract does not carry: neither is a
	// record of a switch.
	r.recordSwitch(`{"from":"repo-a","to":"repo-b"}`, gateway.SwitchCost{USD: 0.01})
	r.recordSwitch(`{"from":"repo-a","to":"repo-b","reason":"maybenot"}`, gateway.SwitchCost{})
	if len(r.rec.Switches) != 0 {
		t.Fatalf("an unreadable switch reached the log: %+v", r.rec.Switches)
	}
	if len(logs.records()) != 2 {
		t.Fatalf("the dropped switches were not logged: %v", logs.records())
	}
	// An answer that carries no switch is nothing to record, and one that does
	// is the service's own record beside the gateway's estimate.
	r.recordSwitch("", gateway.SwitchCost{})
	r.recordSwitch(`{"to":"repo-b","reason":"broken"}`, gateway.SwitchCost{Unmeasured: true})
	if len(r.rec.Switches) != 1 {
		t.Fatalf("the log is %+v, want the one switch the answer carried", r.rec.Switches)
	}
	sw := r.rec.Switches[0]
	if sw.From != "" || sw.To != "repo-b" || sw.Reason != "broken" || !sw.CacheMissUnmeasured {
		t.Fatalf("the switch is %+v", sw)
	}
}

// costLogs is a slog handler that keeps what a run logged, for the tests that
// assert on a warning the run makes rather than on a value it returns.
type costLogs struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (h *costLogs) Enabled(context.Context, slog.Level) bool { return true }

func (h *costLogs) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
	h.mu.Lock()
	h.recs = append(h.recs, m)
	h.mu.Unlock()
	return nil
}

func (h *costLogs) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *costLogs) WithGroup(string) slog.Handler      { return h }

func (h *costLogs) records() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.recs...)
}
