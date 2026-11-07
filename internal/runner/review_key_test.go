package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The in-job api-key review, named and pinned (docs/api-key-review, AK1; F1,
// F2, F5, F7). The ask's configuration is one gateway with two upstreams: a
// repository whose agent.auth is api-key, a coder the project's provider route
// claims and a reviewer on a Claude ID no route claims, so the coder's stages
// go to the provider and the review's to Anthropic on the run's own key.
//
// The behaviour these tests assert is already correct (F1, F2), which is why
// this file has no red step: what it proves is that a careless change to either
// half turns one of them red — publishing a review stage account makes the
// review notional (TestAPIKeyReviewIsChargedRealDollars,
// TestAPIKeyReviewIsNotionalWhenAStageAccountServesIt), and dropping the
// gateway from the review stage sends its call direct
// (TestAPIKeyReviewGoesThroughTheGateway).
//
// The fourth test is the contrast the first three are worth writing for: the
// same configuration with a review stage account published runs the review
// straight to Anthropic on the account, notional, with no gateway in the path
// at all (F7). The two runs side by side are the whole of the in-job slice.

func TestAPIKeyReviewGoesThroughTheGateway(t *testing.T) {
	coderUse := pricing.Usage{Input: 1000, Output: 500}
	g := apiKeyReviewRun(t, "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, coderUse)},
		[]anthropicfake.Reply{anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 4000})})

	rec, err := g.run(t,
		callDeepseek(new(int), new(string), implement("feature")),
		callModel(sonnet, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v, want the coder's stage and the review's", got)
	}
	onlyPR(t, g.provider)

	// One gateway, two upstreams: the review's call reached Anthropic and the
	// coder's the provider, and each carries the model its own stage pinned.
	if n := g.fake.Count(); n != 1 {
		t.Fatalf("Anthropic saw %d calls, want the review's alone", n)
	}
	if n := g.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls, want the coder's alone: %+v", n, g.compat.Seen())
	}
	if body := string(g.fake.Body(0)); !strings.Contains(body, `"model":"`+sonnet+`"`) {
		t.Errorf("Anthropic's call is not the reviewer's model: %s", body)
	}
	if body := string(g.compat.Body(0)); !strings.Contains(body, `"model":"`+deepseek+`"`) {
		t.Errorf("the provider's call is not the coder's model: %s", body)
	}
}

func TestAPIKeyReviewIsChargedRealDollars(t *testing.T) {
	coderUse := pricing.Usage{Input: 1000, Output: 500}
	reviewUse := pricing.Usage{Output: 4000}
	g := apiKeyReviewRun(t, "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, coderUse)},
		[]anthropicfake.Reply{anthropicfake.MessageOK(sonnet, reviewUse)})
	// Both figures are the run's own table's, read rather than named: what is
	// under test is which model paid the review and that its dollars are real,
	// not the catalog's rates.
	coderRow, ok := g.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the coder's model")
	}
	reviewRow, ok := g.deps.Spend.Prices.Lookup(sonnet)
	if !ok {
		t.Fatal("the run's table has no row for the reviewer's model")
	}
	wantCoder, wantReview := coderRow.Rates.Cost(coderUse), reviewRow.Rates.Cost(reviewUse)

	rec, err := g.run(t,
		callDeepseek(new(int), new(string), implement("feature")),
		callModel(sonnet, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil {
		t.Fatalf("the run recorded no cost: %+v", rec)
	}
	// The review's charge is its own model's row, and the run's notional figure
	// is nothing: the gateway served the review on the run's own Anthropic key,
	// so the dollars are billed ones.
	if got := micros(t, rec.Cost.ModelBy[sonnet]); got != wantReview {
		t.Errorf("model_by[%s] = %v µ$, want the table's figure for the review (%v)", sonnet, got, wantReview)
	}
	if rec.Cost.ModelNotionalUSD != 0 {
		t.Errorf("model_notional_usd = %v, want none: the review's call was the gateway's", rec.Cost.ModelNotionalUSD)
	}
	// The route's figure carries the coder's stages alone: a routed figure for
	// the review would say a provider served it.
	if got := micros(t, rec.Cost.RouteBy["openrouter"]); got != wantCoder {
		t.Errorf("route_by[openrouter] = %v µ$, want the coder's stages alone (%v)", got, wantCoder)
	}
	if len(rec.Cost.RouteBy) != 1 {
		t.Errorf("route_by names %v, want the coder's route alone", rec.Cost.RouteBy)
	}
	// The stage says the same: the reviewer's model, no credential (the run's
	// own key served it) and nothing notional.
	review := rec.Stages[1]
	if review.Model != sonnet || review.Credential != "" || review.ModelNotionalUSD != 0 {
		t.Errorf("the review stage = %+v, want the reviewer's model, no credential and no notional figure", review)
	}
}

func TestAPIKeyReviewCountsAgainstTheCap(t *testing.T) {
	// The review's call asks for the stage's own limit, which is what its
	// reservation is computed from.
	const reviewTokens = 4096
	coderUse := pricing.Usage{Input: 1000, Output: 500}
	g := apiKeyReviewRun(t, "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, coderUse)},
		[]anthropicfake.Reply{anthropicfake.MessageOK(sonnet, pricing.Usage{Output: reviewTokens})})
	reviewRow, ok := g.deps.Spend.Prices.Lookup(sonnet)
	if !ok {
		t.Fatal("the run's table has no row for the reviewer's model")
	}
	// Half the review's own worst case, read from the run's table: the coder's
	// call — a fraction of a cent at the catalog's row for its model — fits
	// under it and the review's 4,096 output tokens of a Claude model do not,
	// so the cap is reached by the review's call and by nothing else.
	g.deps.Spend.Cap = reviewRow.Rates.Cost(pricing.Usage{Output: reviewTokens}) / 2

	var st []int
	rec, err := g.run(t,
		callDeepseek(new(int), new(string), implement("feature")),
		calling(&st, review("ship", 0), call{sonnet, reviewTokens}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0] != 403 {
		t.Fatalf("the review's call got %v, want the cap's 403", st)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v, want the run halted on the cap", rec)
	}
	// A refused call is never forwarded, and no further call follows the halt:
	// the provider answered the coder's stage alone and Anthropic nothing.
	if n := g.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls, want the coder's alone", n)
	}
	if n := g.fake.Count(); n != 0 {
		t.Fatalf("Anthropic saw %d calls of a call the cap refused", n)
	}
}

// TestAPIKeyReviewIsNotionalWhenAStageAccountServesIt is the contrast: the
// same configuration with a review stage account published. The review no
// longer goes through the gateway at all — the account's token is in the
// stage's own environment — so its figure is Claude Code's own, all of it
// notional, and the credential that says so is recorded (F7). It is the test
// that makes the difference between the two configurations visible, and the
// one a change to the account precedence turns red.
func TestAPIKeyReviewIsNotionalWhenAStageAccountServesIt(t *testing.T) {
	const accountUSD = 0.42 // what Claude Code reports for the review stage it paid for itself
	coderUse := pricing.Usage{Input: 1000, Output: 500}
	g := apiKeyReviewRun(t, "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, coderUse)}, nil)
	g.deps.Env = append(filterEnv(g.deps.Env, "CLAUDE_CODE_OAUTH_TOKEN"), "CLAUDE_CODE_OAUTH_TOKEN="+accountToken)
	b := publishPolicy(t, g, accountNode("acme-overflow"))

	rec, err := b.run(t,
		callDeepseek(new(int), new(string), implement("feature")),
		accountReview("ship", accountUSD))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v, want the coder's stage and the review's", got)
	}
	review := rec.Stages[1]
	if review.Credential != runstore.CredentialOAuth {
		t.Errorf("the review stage's credential = %q, want %q: the account served it", review.Credential, runstore.CredentialOAuth)
	}
	if micros(t, review.ModelUSD) != micros(t, accountUSD) || micros(t, review.ModelNotionalUSD) != micros(t, review.ModelUSD) {
		t.Errorf("the review stage = %+v, want Claude Code's own figure (%v) and all of it notional", review, accountUSD)
	}
	if rec.StageAccounts["review"] != "acme-overflow" {
		t.Errorf("stage_accounts = %v, want the review stage's account", rec.StageAccounts)
	}
	// The account stage's calls go straight to Anthropic on the account's
	// token, so the gateway's upstreams see the coder's call alone.
	if n := b.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls, want the coder's alone", n)
	}
	if n := b.fake.Count(); n != 0 {
		t.Fatalf("Anthropic saw %d calls: the account stage never reaches the gateway", n)
	}
}
