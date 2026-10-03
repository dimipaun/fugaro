package gateway

import (
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// feeRouted is a routed gateway whose route charges fee percent, with the
// owner's prices edited by prices (nil: the embedded table).
func feeRouted(t *testing.T, fee float64, prices func(*pricing.Table), stage Stage, models []string, script ...anthropicfake.Reply) *routed {
	t.Helper()
	prov, psrv := anthropicfake.New(t, script...)
	h := newHarnessWith(t, func(o *Options) {
		if prices != nil {
			tbl := pricing.Embedded()
			prices(tbl)
			o.Prices = tbl
		}
		o.Routes = []Route{{
			Name: "openrouter", Models: models, BaseURL: psrv.URL + "/api", Auth: "bearer",
			Credential: func() (string, error) { return providerKey, nil }, FeePct: fee,
		}}
	})
	h.gw.BeginStage(stage)
	return &routed{harness: h, prov: prov, provSrv: psrv}
}

var dsRoute = []string{"deepseek/*"}

func TestFeeInReservation(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	r := feeRouted(t, 10, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, u))
	body := msg(dsModel, 1000)
	if resp, b := r.post(body); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	base := worst(t, dsModel, body, 1000, "")
	want := pricing.WithFee(base, 10)
	if want <= base {
		t.Fatalf("fee did not raise the worst case: %d vs %d", want, base)
	}
	if got := reservedOf(t, r.harness); got != want {
		t.Errorf("reserved %d, want %d (worst case %d plus the 10%% fee)", got, want, base)
	}
}

// A cap that fits the model's worst case but not the fee must refuse: the
// cap counts the fee.
func TestFeeInReservationCapRefuses(t *testing.T) {
	body := msg(dsModel, 1000)
	base := worst(t, dsModel, body, 1000, "")
	prov, psrv := anthropicfake.New(t, anthropicfake.MessageOK(dsModel, pricing.Usage{Input: 1, Output: 1}))
	h := newHarnessWith(t, func(o *Options) {
		o.Mode, o.Cap = Enforce, base+1 // room for the model's worst case, not for the fee
		o.Routes = []Route{{Name: "openrouter", Models: dsRoute, BaseURL: psrv.URL, Auth: "bearer",
			Credential: func() (string, error) { return providerKey, nil }, FeePct: 10}}
	})
	h.gw.BeginStage(routedStage)
	resp, b := h.post(body)
	if resp.StatusCode == 200 || prov.Count() != 0 {
		t.Fatalf("a call whose worst case plus fee exceeds the cap was sent: %d %s", resp.StatusCode, b)
	}
}

func TestFeeInSettlement(t *testing.T) {
	u := pricing.Usage{Input: 1000, CacheRead: 500, Output: 2000}
	r := feeRouted(t, 5.5, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, u))
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	want := pricing.WithFee(cost(t, dsModel, u), 5.5)
	if want <= cost(t, dsModel, u) {
		t.Fatal("no fee")
	}
	if got := pricing.Micros(num(r.logs.lastCall(t)["charged_micros"])); got != want {
		t.Errorf("charged %d, want %d", got, want)
	}
	if rep := r.gw.EndStage(); rep.Used != want {
		t.Errorf("stage used %d, want %d", rep.Used, want)
	}
}

func TestNoFeeOnClaudeCalls(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 20}
	r := feeRouted(t, 50, nil, routedStage, dsRoute)
	r.fake.Script = []anthropicfake.Reply{anthropicfake.MessageOK(sonnet, u)}
	body := msg(sonnet, 100)
	if resp, b := r.post(body); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if got := reservedOf(t, r.harness); got != worst(t, sonnet, body, 100, "") {
		t.Errorf("a Claude call reserved %d: the route fee leaked onto it", got)
	}
	if got := pricing.Micros(num(r.logs.lastCall(t)["charged_micros"])); got != cost(t, sonnet, u) {
		t.Errorf("a Claude call charged %d", got)
	}
}

func TestUnpricedModelRefused(t *testing.T) {
	const qwen = "qwen/qwen3-coder"
	r := feeRouted(t, 0, nil, Stage{Name: "implement", Model: qwen, Background: sonnet}, []string{"deepseek/*", "qwen/*"},
		anthropicfake.MessageOK(qwen, pricing.Usage{Input: 1, Output: 1}))
	resp, b := r.post(msg(qwen, 100))
	if resp.StatusCode != http.StatusBadRequest || r.prov.Count() != 0 {
		t.Fatalf("an unpriced provider model was sent: %d %s (provider saw %d)", resp.StatusCode, b, r.prov.Count())
	}
	if rep := r.gw.EndStage(); len(rep.Violations) != 1 || rep.Used != 0 {
		t.Errorf("report %+v", rep)
	}
}

func TestUnknownServingModelPricedMax(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	// The provider served a model the table has no price for.
	r := feeRouted(t, 10, nil, routedStage, dsRoute, anthropicfake.MessageOK("deepseek/surprise-model", u))
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	want := pricing.WithFee(pricing.Embedded().Max().Cost(u), 10)
	call := r.logs.lastCall(t)
	if got := pricing.Micros(num(call["charged_micros"])); got != want {
		t.Errorf("charged %d, want the table maximum %d plus fee", got, want)
	}
	if call["priced_as"] != pricedMax {
		t.Errorf("priced_as %v", call["priced_as"])
	}
	if got := pricing.Micros(num(call["charged_micros"])); got <= cost(t, dsModel, u) {
		t.Errorf("charged %d, not more than the pinned model's %d", got, cost(t, dsModel, u))
	}
}

// A served model the table knows but prices lower is still charged at the
// pinned model's rates; one it prices higher is charged at its own.
func TestServingModelNeverLowersTheCharge(t *testing.T) {
	cheap, dear := "deepseek/cheap", "deepseek/dear"
	prices := func(tbl *pricing.Table) {
		tbl.Models[cheap] = pricing.Model{ID: cheap, Rates: pricing.Rates{InputPerM: 0.01, OutputPerM: 0.02}}
		tbl.Models[dear] = pricing.Model{ID: dear, Rates: pricing.Rates{InputPerM: 20, OutputPerM: 40, CacheRead: 0.1}}
	}
	u := pricing.Usage{Input: 1000, Output: 2000}
	for _, c := range []struct{ served, rates string }{{cheap, dsModel}, {dear, dear}} {
		t.Run(c.served, func(t *testing.T) {
			r := feeRouted(t, 0, prices, routedStage, dsRoute, anthropicfake.MessageOK(c.served, u))
			if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			tbl := pricing.Embedded()
			prices(tbl)
			a, _ := tbl.Lookup(dsModel)
			b, _ := tbl.Lookup(c.served)
			want := pricing.MaxOf(a.Rates, b.Rates).Cost(u)
			if got := pricing.Micros(num(r.logs.lastCall(t)["charged_micros"])); got != want {
				t.Errorf("charged %d, want %d", got, want)
			}
			if got := pricing.Micros(num(r.logs.lastCall(t)["charged_micros"])); got < cost(t, dsModel, u) {
				t.Errorf("charged %d below the pinned model's price", got)
			}
		})
	}
}

// The same model under a different spelling than the pin is the same row:
// priced as the table says, not as a surprise.
func TestServingModelSameAsPinPricedNormally(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.MessageOK(dsModel, u))
	r.post(msg(dsModel, 4000))
	call := r.logs.lastCall(t)
	if call["priced_as"] != pricedTable || pricing.Micros(num(call["charged_micros"])) != cost(t, dsModel, u) {
		t.Errorf("call %v", call)
	}
}
