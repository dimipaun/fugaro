package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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
// cap counts the fee. With the fee's room in the cap the same call goes.
func TestFeeInReservationCapRefuses(t *testing.T) {
	body := msg(dsModel, 1000)
	base := worst(t, dsModel, body, 1000, "")
	withFee := pricing.WithFee(base, 10)
	run := func(capM pricing.Micros) (*http.Response, string, int) {
		prov, psrv := anthropicfake.New(t, anthropicfake.MessageOK(dsModel, pricing.Usage{Input: 1, Output: 1}))
		h := newHarnessWith(t, func(o *Options) {
			o.Mode, o.Cap = Enforce, capM
			o.Routes = []Route{{Name: "openrouter", Models: dsRoute, BaseURL: psrv.URL, Auth: "bearer",
				Credential: func() (string, error) { return providerKey, nil }, FeePct: 10}}
		})
		h.gw.BeginStage(routedStage)
		resp, b := h.post(body)
		return resp, b, prov.Count()
	}
	resp, b, sent := run(base + 1) // room for the model's worst case, not for the fee
	typ, m := apiError(t, b)
	want := fmt.Sprintf("fugaro: budget halted: run cap %s cannot hold a call that needs up to %s", dollars(base+1), dollars(withFee))
	if resp.StatusCode != http.StatusForbidden || typ != "permission_error" || m != want || sent != 0 {
		t.Fatalf("a call whose worst case plus fee exceeds the cap: %d %s %q (sent %d), want 403 permission_error %q", resp.StatusCode, typ, m, sent, want)
	}
	// Control: the cap with room for the fee sends it.
	if resp, b, sent := run(withFee); resp.StatusCode != 200 || sent != 1 {
		t.Fatalf("a call that fits the cap with its fee was refused: %d %s (sent %d)", resp.StatusCode, b, sent)
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
// priced as the table says, not as a surprise (and not at the dearer of two
// rows: it is one row).
func TestServingModelSameAsPinPricedNormally(t *testing.T) {
	const alias = "deepseek/deepseek-v4-flash-0001"
	prices := func(tbl *pricing.Table) {
		m := tbl.Models[dsModel]
		m.Aliases = []string{alias}
		tbl.Models[dsModel] = m
	}
	u := pricing.Usage{Input: 1000, Output: 2000}
	r := feeRouted(t, 0, prices, routedStage, dsRoute, anthropicfake.MessageOK(alias, u))
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r.logs.lastCall(t)
	if call["serving_model"] != alias {
		t.Fatalf("the call was served as %v, not the alias", call["serving_model"])
	}
	if call["priced_as"] != pricedTable || pricing.Micros(num(call["charged_micros"])) != cost(t, dsModel, u) {
		t.Errorf("call %v", call)
	}
}

// A cut stream settles input plus the reserved output, and the fee counts
// on both the charge and the part that is unreconciled.
func TestPartialSettlementCarriesFee(t *testing.T) {
	start := pricing.Usage{Input: 500}
	rep := anthropicfake.StreamOK(dsModel, pricing.Usage{Input: 500, Output: 300})
	rep.CutAfter = 1
	r := feeRouted(t, 10, nil, routedStage, dsRoute, rep)
	resp, err := http.DefaultClient.Do(r.request(context.Background(), "/v1/messages", msg(dsModel, 4000, `"stream":true`)))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	charged, reported := start, start
	charged.Output, reported.Output = 4000, 1
	want := pricing.WithFee(cost(t, dsModel, charged), 10)
	seen := pricing.WithFee(cost(t, dsModel, reported), 10)
	rp := r.gw.EndStage()
	if rp.Used != want || rp.Unreconciled != want-seen || rp.Unreconciled <= 0 {
		t.Errorf("report %+v, want used %d, unreconciled %d", rp, want, want-seen)
	}
	if call := r.logs.lastCall(t); call["settled"] != settledPartial || pricing.Micros(num(call["charged_micros"])) != want {
		t.Errorf("call %v", call)
	}
}

// A serving model that is the pin with a date or other suffix (or the pin
// a prefix of it) is the pin's own model: priced at the pin's rates, not at
// the table's maximum. An unrelated unknown model keeps the maximum and the
// log names the serving model and the pin.
func TestServingModelSuffixPricedAtPin(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	served := dsModel + "-20261001"
	r0 := feeRouted(t, 10, nil, routedStage, dsRoute, anthropicfake.MessageOK(served, u))
	if resp, b := r0.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	call := r0.logs.lastCall(t)
	if want := pricing.WithFee(cost(t, dsModel, u), 10); pricing.Micros(num(call["charged_micros"])) != want {
		t.Errorf("%s charged %v, want the pin's price %d", served, call["charged_micros"], want)
	}
	if call["serving_model"] != served {
		t.Errorf("serving_model %v", call["serving_model"])
	}
	// The served name a prefix of the pin.
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.MessageOK("deepseek/deepseek-v4", u))
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if got := pricing.Micros(num(r.logs.lastCall(t)["charged_micros"])); got != cost(t, dsModel, u) {
		t.Errorf("prefix serving name charged %d, want %d", got, cost(t, dsModel, u))
	}
}

func TestUnrelatedUnknownServingModelNamesCause(t *testing.T) {
	u := pricing.Usage{Input: 1000, Output: 2000}
	r := feeRouted(t, 0, nil, routedStage, dsRoute, anthropicfake.MessageOK("deepseek/surprise-model", u))
	if resp, b := r.post(msg(dsModel, 4000)); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if call := r.logs.lastCall(t); call["priced_as"] != pricedMax {
		t.Errorf("priced_as %v", call["priced_as"])
	}
	logs := r.logs.String()
	if !strings.Contains(logs, "deepseek/surprise-model") || !strings.Contains(logs, dsModel) || !strings.Contains(logs, "not in the price table") {
		t.Errorf("the log doesn't name the cause:\n%s", logs)
	}
}
