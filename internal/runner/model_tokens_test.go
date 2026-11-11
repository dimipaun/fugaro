package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// otherModel is a model no key of a run's record may be: the per-model token
// counts are the run's own, keyed by the model the gateway priced each call
// for, and a serving model an upstream response names is a claim (G1.2). The
// fixture's upstream answers a call for the coder's model with a body naming
// this one.
const otherModel = "another-vendor/other-model"

// The three usage reports the gateway fixture's readable calls carry.
// coderUsage and namedUsage are both on the coder's model -- the second one
// only in the model the request named -- so their sum is what that model's
// entry must carry, whatever the responses claimed.
var (
	coderUsage    = pricing.Usage{Input: 1000, CacheWrite5m: 30, CacheWrite1h: 5, CacheRead: 400, Output: 500}
	namedUsage    = pricing.Usage{Input: 100, Output: 20}
	reviewerUsage = pricing.Usage{Input: 2000, Output: 800}
)

// gatewayTokenRun is the gateway fixture the per-model token tests share: an
// implement stage whose three calls are all on the coder's model — one the
// fake answers as itself, one whose usage does not parse at all, and one
// whose response names another model — and a review stage whose call is on
// the reviewer's. Two models are priced, so counts keyed under one model, or
// under a name a response claimed, cannot pass.
func gatewayTokenRun(t *testing.T) *runstore.Record {
	t.Helper()
	g := newGW(t, gwRolesConfig(t), "observe", "",
		anthropicfake.MessageOK(sonnet, coderUsage),
		unparsed2xx(),
		anthropicfake.MessageOK(otherModel, namedUsage),
		anthropicfake.MessageOK(opus, reviewerUsage))
	var st []int
	rec, err := g.run(t,
		calling(&st, implement("feature"),
			call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}),
		calling(&st, review("ship", 0), call{opus, 4000}))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for i, code := range st {
		if code != 200 {
			t.Fatalf("call %d got %d, want the gateway to forward it", i, code)
		}
	}
	return rec
}

// TestRunFillsModelTokensGateway (G1.2, F8): with the gateway on, the run's
// cost carries each model's token counts, summed from the calls' own usage
// under the model the gateway priced them for. The coder's entry is exactly
// the two readable calls' counts on its model — the third call of the stage
// settled at its reservation with no usage anyone could read, so it adds no
// tokens and nothing is invented for it — and the response that named another
// model is in no key of its own.
func TestRunFillsModelTokensGateway(t *testing.T) {
	rec := gatewayTokenRun(t)
	if rec.Cost == nil {
		t.Fatal("the run recorded no cost")
	}
	want := map[string]runstore.ModelTokenCounts{
		sonnet: {
			Input:        coderUsage.Input + namedUsage.Input,
			Output:       coderUsage.Output + namedUsage.Output,
			CacheRead:    coderUsage.CacheRead,
			CacheWrite5m: coderUsage.CacheWrite5m,
			CacheWrite1h: coderUsage.CacheWrite1h,
		},
		opus: {Input: reviewerUsage.Input, Output: reviewerUsage.Output},
	}
	got := rec.Cost.ModelTokens
	if len(got) != len(want) {
		t.Fatalf("model_tokens = %+v, want one entry per model the gateway priced (%+v)", got, want)
	}
	for model, w := range want {
		if got[model] != w {
			t.Errorf("model_tokens[%q] = %+v, want %+v", model, got[model], w)
		}
	}
	if _, ok := got[otherModel]; ok {
		t.Errorf("model_tokens has an entry for %q: the counts are keyed by the model the gateway priced, never by the name a response carried", otherModel)
	}
}

// TestCostCacheTotalsEqualModelTokensSums (G1.2, F8): the run's prompt-cache
// read and write totals are the sums of the per-model figures, so a reader
// that wants a total for a run whose path records no totals of its own — a
// run without a gateway — adds them the same way.
func TestCostCacheTotalsEqualModelTokensSums(t *testing.T) {
	rec := gatewayTokenRun(t)
	if rec.Cost == nil {
		t.Fatal("the run recorded no cost")
	}
	var read, write int64
	for _, c := range rec.Cost.ModelTokens {
		read += c.CacheRead
		write += c.CacheWrite5m + c.CacheWrite1h
	}
	if read == 0 || write == 0 {
		t.Fatalf("the fixture recorded no cache tokens to sum: cost = %+v", rec.Cost)
	}
	if read != rec.Cost.CacheReadTokens || write != rec.Cost.CacheWriteTokens {
		t.Fatalf("cache_read_tokens = %d and cache_write_tokens = %d, want the per-model sums %d and %d",
			rec.Cost.CacheReadTokens, rec.Cost.CacheWriteTokens, read, write)
	}
}

// TestRunFillsModelTokensSubscription (G1.2, F8): without a gateway — an
// oauth run, whose every call goes straight to Anthropic — the counts are
// the agent's own per-model usage, and the single cache-creation count the
// agent reports is booked as cache_write_5m, the way the budget books it: the
// path has no 5m/1h split to keep. The run records no cache totals either,
// because those are the gateway's (G1.1).
func TestRunFillsModelTokensSubscription(t *testing.T) {
	cfg := strings.Replace(gwRolesConfig(t), "auth: api-key", "auth: oauth", 1)
	g := newGW(t, cfg, "observe", "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	usage := map[string]agent.Usage{
		sonnet: {Input: 1000, CacheCreation: 700, CacheRead: 250, Output: 300},
		haiku:  {Input: 50, CacheCreation: 10, Output: 5},
	}
	rec, err := g.run(t, withUsage(implement("feature"), agent.Usage{}, usage), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil {
		t.Fatal("the run recorded no cost")
	}
	if rec.Cost.ModelBasis != runstore.BasisSubscription {
		t.Fatalf("model_basis = %q, want the subscription's", rec.Cost.ModelBasis)
	}
	want := map[string]runstore.ModelTokenCounts{
		sonnet: {Input: 1000, Output: 300, CacheRead: 250, CacheWrite5m: 700},
		haiku:  {Input: 50, Output: 5, CacheWrite5m: 10},
	}
	got := rec.Cost.ModelTokens
	if len(got) != len(want) {
		t.Fatalf("model_tokens = %+v, want the agent's per-model usage (%+v)", got, want)
	}
	for model, w := range want {
		if got[model] != w {
			t.Errorf("model_tokens[%q] = %+v, want %+v (every cache creation is a 5m write)", model, got[model], w)
		}
	}
	if rec.Cost.CacheReadTokens != 0 || rec.Cost.CacheWriteTokens != 0 {
		t.Errorf("cache totals = %d/%d: a run with no gateway records none, because they are the gateway's",
			rec.Cost.CacheReadTokens, rec.Cost.CacheWriteTokens)
	}
}

// TestRunFillsModelTokensNoGatewayAPIKey (G1.2, F8): an api-key run whose
// budget mode is off has no gateway either, and its counts come from the same
// place: the agent's own per-model usage, stage by stage, so a two-stage run's
// second model is added beside the first rather than replacing it.
func TestRunFillsModelTokensNoGatewayAPIKey(t *testing.T) {
	g := newGW(t, gwRolesConfig(t), "off", "")
	rec, err := g.run(t,
		withUsage(implement("feature"), agent.Usage{},
			map[string]agent.Usage{sonnet: {Input: 1000, CacheCreation: 700, CacheRead: 250, Output: 300}}),
		withUsage(review("ship", 0), agent.Usage{},
			map[string]agent.Usage{opus: {Input: 2000, Output: 800}}))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil {
		t.Fatal("the run recorded no cost")
	}
	if rec.Cost.ModelBasis != runstore.BasisAPIList {
		t.Fatalf("model_basis = %q, want the list-price basis", rec.Cost.ModelBasis)
	}
	want := map[string]runstore.ModelTokenCounts{
		sonnet: {Input: 1000, Output: 300, CacheRead: 250, CacheWrite5m: 700},
		opus:   {Input: 2000, Output: 800},
	}
	got := rec.Cost.ModelTokens
	if len(got) != len(want) {
		t.Fatalf("model_tokens = %+v, want both stages' models (%+v)", got, want)
	}
	for model, w := range want {
		if got[model] != w {
			t.Errorf("model_tokens[%q] = %+v, want %+v", model, got[model], w)
		}
	}
}
