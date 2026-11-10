package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The end-to-end half of plan Task 1b.2, through the policy path: the switch
// arrives in the project's published policy, `resolvePolicy` applies it, and
// the run it produces is asserted where it lands — the pins the agent is
// given, the route the gateway registers, the row the call is charged from and
// the model the gateway records it as serving.
//
// These are the tests the review's critical finding demanded, and the ones the
// unit tests could not be: each of those called `applyRouting` directly with
// the run's spend already set, so none of them could see that in a real run —
// where `r.spend` is assigned *after* the call site — `gatewayOn()` was always
// false and the switch never took effect at all.

// routedPolicyNode is the published policy node the routed fixtures read: the
// switch D1a delivers (routing.claude), beside the providers block the node's
// own decode holds it against (projectpolicy.Decode validates the switch
// against that block, so a node naming a route nothing in it claims is not
// readable at all). The run routes by the *job's* copy of the routes rather
// than by this block — the finding the docs were corrected for — which is why
// the fixture gives the job the same route and the tests below read the pins.
func routedPolicyNode(via string, models ...string) string {
	quoted := make([]string, len(models))
	for i, m := range models {
		quoted[i] = `"` + m + `"`
	}
	return `{"version":4,` +
		`"providers":{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api/v1",` +
		`"auth":"bearer","secret":"openrouter-key","models":["` + claimedSenior + `"],"allow_data_to":["*"]}},` +
		`"routing":{"claude":{"via":"` + via + `","models":[` + strings.Join(quoted, ",") + `]}}}`
}

// publishPolicy puts the node on the harness's database, with the rules marker
// the owner's deploy writes, and attaches the budget backend the read needs.
func publishPolicy(t *testing.T, g *gw, policy string) *bk {
	t.Helper()
	b := attachBackend(t, g, "enforce")
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	b.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	return b
}

// routedCall is a stage that asserts the pins it was given name the route's own
// ID and makes one call on it, as Claude Code would: the model it posts is the
// one the run pinned, so the gateway's route decides where the call goes.
func routedCall(model string, roleVar string, status *int, body *string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if got := envValue(req.Env, roleVar); got != model {
			t.Errorf("%s = %q, want the route's own ID %q: the switch did not reach the pins", roleVar, got, model)
		}
		if got := envValue(req.Env, "CLAUDE_CODE_SUBAGENT_MODEL"); got != model {
			t.Errorf("CLAUDE_CODE_SUBAGENT_MODEL = %q, want %q", got, model)
		}
		*status, *body = post(t, req, model, 4096)
		return inner(t, ctx, req)
	}
}

// TestRoutedClaudePinEndToEnd is the whole run with the switch delivered the
// way a real one is: the project's policy node carries `routing.claude: {via:
// openrouter, models: [claude-sonnet-5-5]}`, the job carries the route that
// claims `anthropic/claude-sonnet-5.5`, and the owner priced that ID under
// model_prices (the shipped catalog has no Claude row yet).
//
// Everything the design's §6.2 asks for is asserted on the run that results:
// the agent is pinned to the route's ID on both stages, both calls reach the
// PROVIDER's upstream and Anthropic's sees none, the charge is the route's row
// plus route_fee_pct and not the direct Claude rate, and the gateway's own call
// log names the route's ID as the serving model with `priced_as: table` — the
// two records the design says must agree (Review Focus 11).
//
// Hand mutation this fails on: applying the switch before `r.spend` exists (the
// critical finding: gatewayOn is then always false, the pins stay canonical,
// the calls go to Anthropic and are charged the direct rate); and, once that is
// fixed, resolving the pins without registering the route, charging the
// canonical ID's row, or recording a different serving model.
func TestRoutedClaudePinEndToEnd(t *testing.T) {
	const canonical, routed = sonnet, claimedSenior
	use := pricing.Usage{Input: 1000, Output: 500}
	// The coder and the reviewer are both Claude sonnet; first_line_review is
	// off so the plan is implement, review, and the two stages are the two
	// routed pins.
	cfg := gwConfig(t, "  first_line_review: off\n")
	g := routeRun(t, cfg, "enforce", "50", []string{routed}, claimPricesJSON,
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(routed, use), anthropicfake.Compat{}.MessageOK(routed, use)},
		"acme/app")
	// The route's fee is what makes the charge the route's rather than the
	// provider's own quote: 10% on top of the row.
	prov := g.deps.Providers["openrouter"]
	prov.RouteFeePct = 10
	g.deps.Providers["openrouter"] = prov
	b := publishPolicy(t, g, routedPolicyNode("openrouter", canonical))

	// The premise: the direct row prices the canonical ID and charges a
	// different figure from the route's row, so a run that failed to route is
	// both possible and detectable.
	direct, ok := pricing.Embedded().Lookup(canonical)
	if !ok {
		t.Fatalf("the built-in table has no direct row for %s: this test's premise is gone", canonical)
	}
	ov, err := pricing.ParseOverrides(claimPricesJSON)
	if err != nil {
		t.Fatal(err)
	}
	routeRates, ok := ov[routed]
	if !ok {
		t.Fatalf("the fixture's model_prices has no row for %s", routed)
	}
	wantRouted := pricing.WithFee(routeRates.Cost(use), 10)
	wantDirect := pricing.WithFee(direct.Rates.Cost(use), 10)
	if wantRouted == wantDirect {
		t.Fatal("the route's row and the direct row charge the same: this test proves nothing")
	}

	var coderStatus, reviewStatus int
	var coderBody, reviewBody string
	rec, err := b.run(t,
		routedCall(routed, "ANTHROPIC_DEFAULT_SONNET_MODEL", &coderStatus, &coderBody, implement("feature")),
		routedCall(routed, "ANTHROPIC_DEFAULT_OPUS_MODEL", &reviewStatus, &reviewBody, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if coderStatus != 200 || reviewStatus != 200 {
		t.Fatalf("statuses = %d, %d: the routed call was not served\ncoder: %s\nreviewer: %s", coderStatus, reviewStatus, coderBody, reviewBody)
	}
	onlyPR(t, g.provider)
	if n := g.compat.Count(); n != 2 {
		t.Fatalf("the provider saw %d calls, want both stages': %+v", n, g.compat.Seen())
	}
	if n := g.fake.Count(); n != 0 {
		t.Fatalf("Anthropic saw %d calls of a run the switch routed: the pins did not reach the agent, or the route did not register", n)
	}
	for i := 0; i < 2; i++ {
		if body := string(g.compat.Body(i)); !strings.Contains(body, `"model":"`+routed+`"`) {
			t.Errorf("provider call %d is not on the route's own ID: %s", i, body)
		}
	}

	// The charge: the route's row plus the fee, and no direct-path figure
	// anywhere in the run's totals.
	if rec.Cost == nil {
		t.Fatalf("the run recorded no cost: %+v", rec)
	}
	// Both stages are on the routed model, so its row carries both charges.
	if got := micros(t, rec.Cost.ModelBy[routed]); got != 2*wantRouted {
		t.Errorf("the routed model was charged %v µ$, want %v over the two stages (the route's row plus the fee); the direct Claude rate would have charged %v",
			got, 2*wantRouted, 2*wantDirect)
	}
	if got := rec.Cost.ModelBy[canonical]; got != 0 {
		t.Errorf("a direct Claude rate is in the run's figures: %s = %v µ$", canonical, got)
	}
	if got := micros(t, rec.Cost.RouteBy["openrouter"]); got != 2*wantRouted {
		t.Errorf("route_by[openrouter] = %v µ$, want the two stages' charges (%v)", got, 2*wantRouted)
	}

	// The record the gateway keeps of the call names the route's ID as the
	// serving model. priced_as is an enum ("table", "max", …) and says nothing
	// about which model was served, so a run that recorded a different
	// spelling there would pass every assertion above: serving_model is the
	// one field that names it.
	logs := g.logs.String()
	if n := strings.Count(logs, "serving_model="+routed); n != 2 {
		t.Errorf("the gateway's call log names %s as the serving model %d times, want both stages' calls:\n%s", routed, n, callLines(logs))
	}
	if strings.Contains(logs, "serving_model="+canonical) {
		t.Errorf("the gateway recorded the canonical ID as the serving model:\n%s", callLines(logs))
	}
	if !strings.Contains(logs, "priced_as=table") {
		t.Errorf("the routed call was not priced from a table row:\n%s", callLines(logs))
	}
}

// TestRoutedSwitchKeepsTheFirstLineLoop: the case the two fixtures above hid.
// Both set first_line_review: off, so neither could see that the run's plan is
// derived from the pins *before* the routing switch rewrites them
// (internal/runner/runner.go), and that the cheap first-line loop is therefore
// decided by an Agent.FirstLineOn answer about models the switch had not
// touched yet.
//
// With first_line_review: auto, FirstLineOn is "the coder's model is claimed by
// a route and the reviewer's is not" (Agent.FirstLineOn). Here the switch names
// the coder's Claude ID alone: the reviewer keeps an ID no route claims, so the
// answer is true — but only after the rewrite. Before it the coder still carries
// the canonical claude-sonnet-5-5, which no route entry claims (the claim is
// anthropic/claude-sonnet-5.5), so the derived plan dropped the first-line step
// and exactly the runs a switch routed lost their cheap loop.
//
// Hand mutation this fails on: deriving the plan where it used to be derived —
// beside `derived`, before resolvePolicy — which drops review_first from the
// stages below (and leaves the fixture's four steps for a two-step plan).
func TestRoutedSwitchKeepsTheFirstLineLoop(t *testing.T) {
	const routed = claimedSenior
	use := pricing.Usage{Input: 1000, Output: 500}
	// gwConfig pins coder and reviewer to sonnet; the reviewer moves to opus,
	// a Claude ID the route below does not claim, so `auto` has one claimed pin
	// and one unclaimed one — the state FirstLineOn reads as "on".
	// first_line_rounds comes with the first line: this case scripts one cheap
	// round, and an unset knob would take the resolved mode's row instead
	// (internal/rigor's DefaultRounds).
	cfg := strings.Replace(gwConfig(t, "  first_line_review: auto\n  first_line_rounds: 1\n"), "reviewer: "+sonnet, "reviewer: "+opus, 1)
	if !strings.Contains(cfg, "reviewer: "+opus) {
		t.Fatal("the fixture did not take the reviewer model: this test's premise is gone")
	}
	g := routeRun(t, cfg, "enforce", "50", []string{routed}, claimPricesJSON,
		[]anthropicfake.Reply{
			anthropicfake.Compat{}.MessageOK(routed, use),
			anthropicfake.Compat{}.MessageOK(routed, use),
			anthropicfake.Compat{}.MessageOK(routed, use),
		}, "acme/app")
	// The reviewer's stage is not routed: its pin is a Claude ID no route
	// claims, so it reaches Anthropic directly, and the senior call is answered
	// by Anthropic's fake (the three coder stages answer from the provider's).
	g.fake.Script = append(g.fake.Script, anthropicfake.Compat{}.MessageOK(opus, use))
	b := publishPolicy(t, g, routedPolicyNode("openrouter", sonnet))

	rec, err := b.run(t,
		routedCall(routed, "ANTHROPIC_DEFAULT_SONNET_MODEL", new(int), new(string), implement("feature")),
		routedCall(routed, "ANTHROPIC_DEFAULT_SONNET_MODEL", new(int), new(string), review("changes", 1)),
		routedCall(routed, "ANTHROPIC_DEFAULT_SONNET_MODEL", new(int), new(string), fixStep),
		routedCall(opus, "ANTHROPIC_DEFAULT_OPUS_MODEL", new(int), new(string), review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review") {
		t.Fatalf("stages = %v: the routed run lost the first-line loop its auto switch turned on", got)
	}
	if n := g.compat.Count(); n != 3 {
		t.Errorf("the provider saw %d calls, want the three coder stages': %+v", n, g.compat.Seen())
	}
	if n := g.fake.Count(); n != 1 {
		t.Errorf("Anthropic saw %d calls, want the senior review alone: an unrouted pin must keep its Claude ID", n)
	}
}

// callLines is the log's model-call lines, so a failure prints the record
// rather than the whole run's log.
func callLines(logs string) string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `msg="model call"`) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// TestUnresolvableRoutingSwitchHaltsBeforeAnyCall: the switch names a Claude ID
// the run's routes do not claim (the node's own providers block claims it, so
// the node is readable — the run's routes are the job's, which is the whole
// point). The run halts at bootstrap, before the agent starts and before any
// call reaches either upstream: D1b's default is what an *absent* switch means,
// never what a written one means when its route is gone.
//
// Hand mutation this fails on: resolve-or-ignore (the fall-through D1b exists to
// prevent), which sends the call to Anthropic at the direct Claude rate for a
// call the owner routed elsewhere — and, before the ordering fix, the switch
// being skipped entirely in a real run.
func TestUnresolvableRoutingSwitchHaltsBeforeAnyCall(t *testing.T) {
	cfg := gwConfig(t, "  first_line_review: off\n")
	// The job carries the provider route, but not one that claims the Claude ID
	// the switch names.
	g := routeRun(t, cfg, "enforce", "50", []string{"deepseek/*"}, "", nil, "acme/app")
	b := publishPolicy(t, g, routedPolicyNode("openrouter", sonnet))

	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a switch this run cannot resolve was applied without a refusal: rec = %+v", rec)
	}
	if rec.Outcome != runstore.OutcomeNone || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("a refused bootstrap wrote an outcome: rec = %+v", rec)
	}
	for _, want := range []string{"routing", sonnet} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("the refusal does not name %q: %s", want, rec.Reason)
		}
	}
	if len(g.agent.calls) != 0 {
		t.Errorf("the agent ran %d stage(s) of a run whose switch cannot resolve", len(g.agent.calls))
	}
	if n := g.fake.Count(); n != 0 {
		t.Errorf("Anthropic saw %d calls: the unresolved pin went direct", n)
	}
	if n := g.compat.Count(); n != 0 {
		t.Errorf("the provider saw %d calls of a run that halted at bootstrap", n)
	}
}
