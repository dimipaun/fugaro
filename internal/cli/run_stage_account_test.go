package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// The account-choice keys of the accounts design (docs/accounts-add/spec.md
// §11.1-§11.2) are read here, and the B0 ruling
// (docs/design/mixed-credentials.md §11) is what bounds the second one: an
// api-key run may name an account for the senior review and the adversarial
// review and no other stage, and it may name one account and not two. These
// tests are that refusal matrix, plus the two rungs the chain gains.
//
// defaultClaudeAccountConfig is claudeAccountsConfig plus the project
// default: acme-overflow is admitted for acme/other, the repository every
// checkout below is.
const defaultClaudeAccountConfig = claudeAccountsConfig + "default_claude_account: acme-overflow\n"

// stagesBlock is a stages: block naming an account for each stage/account
// pair, and stageAccountConfig is claudeAccountsConfig plus one.
func stagesBlock(pairs ...string) string {
	out := "stages:\n"
	for i := 0; i+1 < len(pairs); i += 2 {
		out += "  " + pairs[i] + ":\n    claude_account: " + pairs[i+1] + "\n"
	}
	return out
}

func stageAccountConfig(pairs ...string) string {
	return claudeAccountsConfig + stagesBlock(pairs...)
}

// TestRunUsesTheProjectDefaultClaudeAccount is rung 4 of spec §11.2: with no
// flag and no agent.oauth_token, the run's account is the project's default
// from the local config, and it lands on that account's job — the job that
// holds the account's token. It sits below agent.oauth_token (rung 3) and
// above the unsuffixed default (rung 5), which the second subtest pins.
func TestRunUsesTheProjectDefaultClaudeAccount(t *testing.T) {
	t.Run("no more specific choice", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, defaultClaudeAccountConfig)
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261105-100000-aaaa", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261105-100000-aaaa").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow (the project's default_claude_account)", got)
		}
	})
	t.Run("agent.oauth_token wins", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, defaultClaudeAccountConfig)
		oauthCheckout(t, f, "  oauth_token: acme-personal\n")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-personal")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261105-100000-bbbb", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261105-100000-bbbb").OAuthToken; got != "acme-personal" {
			t.Fatalf("OAuthToken = %q, want acme-personal: the committed agent.oauth_token outranks the local default", got)
		}
	})
}

// TestRunRefusesAStageAccountOnTheCodersStage is the B0 ruling's first
// refusal, and the reason it is a refusal rather than a silent no-op: an
// api-key run's coder runs on the provider key the gateway holds, so an
// account named for implement (or review_first, or fix, or plan) would never
// be used, and the operator would believe their coding stage ran on it.
func TestRunRefusesAStageAccountOnTheCodersStage(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, stageAccountConfig("implement", "acme-overflow"))
	recipeCheckout(t, f, "", nil) // agent.auth: api-key
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	_, _, err := execute(t, "run", "--run-id", "20261105-100000-cccc", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user refusal", err)
	}
	for _, want := range []string{"implement", "api-key", "review", "adversarial"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesASecondStageAccount is the ruling's "never a second
// provider's key" half read as what it admits: one provider key and one
// Claude account. Two stage keys naming two accounts is one account too many,
// and the launch is refused before anything is started.
func TestRunRefusesASecondStageAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, stageAccountConfig("review", "acme-overflow", "adversarial", "acme-personal"))
	recipeCheckout(t, f, "", nil)
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	_, _, err := execute(t, "run", "--run-id", "20261105-100000-dddd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "one") {
		t.Fatalf("err = %v, want a refusal naming the one account a mixed run may hold", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunAdmitsAStageAccountForTheReviewsOnly: the permitted mix. An api-key
// run may name one account for review and adversarial, and the run's own
// credential does not move: the launch still lands on the coder's own job
// (OAuthToken stays empty), because the account is the stages' and not the
// run's.
func TestRunAdmitsAStageAccountForTheReviewsOnly(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, stageAccountConfig("review", "acme-overflow", "adversarial", "acme-overflow"))
	recipeCheckout(t, f, "", nil)
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	if _, _, err := execute(t, "run", "--run-id", "20261105-100000-eeee", "A task"); err != nil {
		t.Fatal(err)
	}
	if got := otherSpec(t, f, "20261105-100000-eeee").OAuthToken; got != "" {
		t.Fatalf("OAuthToken = %q, want empty: a stage account is the stages' and never the run's own credential", got)
	}
}

// TestRunRefusesAStageAccountThisRepositoryMayNotUse: the allow-list still
// holds every rung. acme-elsewhere is a real account of the local config and
// its allow_data_to names another repository, so a stage key naming it for
// this one is refused exactly as --token would be.
func TestRunRefusesAStageAccountThisRepositoryMayNotUse(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		stagesBlock("review", "acme-elsewhere"))
	recipeCheckout(t, f, "", nil) // acme/other is not in acme-elsewhere's allow_data_to
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	_, _, err := execute(t, "run", "--run-id", "20261105-100000-ffff", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-elsewhere") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v, want the allow-list refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestClaudeAccountForResolvesTheStagesOwnKey is rung 2 of spec §11.2 read
// where it is read: claudeAccountFor with the stage named. The stage's key
// outranks the committed agent.oauth_token (rung 3), and a stage with no key
// of its own falls through to the run's own chain.
func TestClaudeAccountForResolvesTheStagesOwnKey(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, defaultClaudeAccountConfig+stagesBlock("review", "acme-overflow"))
	oauthCheckout(t, f, "  oauth_token: acme-personal\n")
	env := gcsEnv(t, f)
	co := checkoutConfig(context.Background(), "acme/other")
	if co == nil {
		t.Fatal("checkoutConfig found no checkout")
	}
	cases := []struct {
		stage string
		want  string
	}{
		{"review", "acme-overflow"},      // rung 2
		{"adversarial", "acme-personal"}, // rung 3, nothing more specific
		{"implement", "acme-personal"},   // the run's own chain, unchanged
		{"", "acme-personal"},            // the run's own resolution
	}
	for _, tc := range cases {
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", tc.stage, "", co)
		if err != nil {
			t.Fatalf("stage %q: %v", tc.stage, err)
		}
		if got != tc.want {
			t.Errorf("stage %q: account = %q, want %q", tc.stage, got, tc.want)
		}
	}
}

// The mix's own fixtures. An api-key run mixes only where the run has the
// shape the ruling names: the coder on a provider route this repository may
// use, budget.mode: enforce (the only mode that routes a call to a provider at
// all), and a Claude model on the review stage the account would serve.
const (
	// mixCoderModel is the coder's pin: a model the route below claims.
	mixCoderModel = "deepseek/deepseek-v4-flash"
	// claudeReviewer is a review model the account may serve.
	claudeReviewer = "claude-sonnet-5"
	// mixRouteConfig is the project's own route table: openrouter serves acme/other
	// (the fixture checkout's repository) and claims the coder's model.
	mixRouteConfig = "providers:\n" +
		"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n" +
		"model_prices:\n  \"" + mixCoderModel + "\": { input_per_m: 0.1, output_per_m: 0.2 }\n"
	// mixEnforce is the budget mode a provider-served coder requires.
	mixEnforce = "budget: { mode: enforce, per_run_usd: 5 }\n"
)

// mixCase is one row's fixture: the accounts (keys adds the account-choice
// keys the row is about), the routes and the budget mode, over a checkout of
// acme/other whose coder is on the provider route and whose reviews run on
// reviewer. It returns the env and the checkout's own document, which is what
// claudeAccountFor is asked with.
func mixCase(t *testing.T, keys, reviewer, budget string) (*cloudEnv, *config.Config) {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+mixRouteConfig+budget+keys)
	// agent.auth: api-key (recipeCheckout's own line), the coder on the
	// provider route, and the reviews on reviewer.
	recipeCheckout(t, f, "  models: { coder: "+mixCoderModel+", reviewer: "+reviewer+", background: "+mixCoderModel+" }\n", nil)
	env := gcsEnv(t, f)
	co := checkoutConfig(context.Background(), "acme/other")
	if co == nil {
		t.Fatal("checkoutConfig found no checkout")
	}
	return env, co
}

// TestClaudeAccountForTheMixMatrix is the whole rule of the owner's ruling of
// 2026-11-05 (docs/design/mixed-credentials.md §11, amending the 2026-11-04
// conservative reading) as a matrix: which account a *stage* resolves for an
// api-key run, over the rung the account is named by, the stage asked for and
// the model that stage runs.
//
// An account is served to the senior review and the adversarial review and no
// other stage, from rung 2 (the stage's own key) or rung 4 (the project
// default, which is what this ruling adds) or both. Every other stage resolves
// the run's own credential (the unsuffixed default), which is the ruling's
// "never implement, never review_first, never a later stage".
//
// The stage's own model is what separates the two rungs, and the matrix says so
// twice: rung 4 is a *default*, so the ruling conditions it on the stage (an
// Anthropic reviewer), and with a provider reviewer it is not a rung at all —
// nothing changes and nothing is refused. Rung 2 is a declaration about that
// very stage, which B1 serves as written.
//
// Two mutations fail this test: dropping the allow-list from mixedStageAccount
// serves the account to implement, review_first and fix, and dropping the
// model condition from it serves the project default to a review stage that
// runs on the provider.
func TestClaudeAccountForTheMixMatrix(t *testing.T) {
	// rung 2 is the stage's own key — both mixed stages name one account,
	// which is the shape the one-account rule admits; rung 4 is the project
	// default alone; "both" names the same account in each; "neither" declares
	// no mix at all and is the control.
	rungs := []struct{ name, keys string }{
		{"rung 2", stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow")},
		{"rung 4", "default_claude_account: acme-overflow\n"},
		{"both", "default_claude_account: acme-overflow\n" + stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow")},
		{"neither", ""},
	}
	models := []struct {
		name      string
		reviewer  string
		anthropic bool
	}{
		{"an Anthropic reviewer", claudeReviewer, true},
		{"a provider reviewer", mixCoderModel, false},
	}
	stages := []string{"review", "adversarial", "implement", "review_first", "fix", "plan"}
	for _, rung := range rungs {
		for _, model := range models {
			t.Run(rung.name+", "+model.name, func(t *testing.T) {
				env, co := mixCase(t, rung.keys, model.reviewer, mixEnforce)
				for _, stage := range stages {
					mixed := (rung.name == "rung 2" || rung.name == "both" ||
						(rung.name == "rung 4" && model.anthropic)) &&
						slices.Contains(config.MixedAccountStages, stage)
					want := config.DefaultClaudeAccount
					if mixed {
						want = "acme-overflow"
					}
					got, err := claudeAccountFor(context.Background(), env, "acme/other", "", stage, "", co)
					if err != nil {
						t.Fatalf("stage %q: %v", stage, err)
					}
					if got != want {
						t.Errorf("stage %q: account = %q, want %q", stage, got, want)
					}
				}
			})
		}
	}
}

// TestClaudeAccountForLeavesTheRunsOwnCredentialAlone: the account a mixed
// stage is served by is the stages' and never the run's. Under an api-key run
// the run's own resolution (no stage asked for) stays the unsuffixed default,
// whatever default_claude_account says — that key is rung 4 for a *stage*, not
// a run's own credential, and such a run's coder stays on the provider key the
// gateway holds.
func TestClaudeAccountForLeavesTheRunsOwnCredentialAlone(t *testing.T) {
	env, co := mixCase(t, "default_claude_account: acme-overflow\n", claudeReviewer, mixEnforce)
	got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.DefaultClaudeAccount {
		t.Fatalf("account = %q, want %q: an api-key run's own credential is not a stage's", got, config.DefaultClaudeAccount)
	}
}

// TestClaudeAccountForRefusesAMixThatWouldBeASecondAccount: the run holds one
// Claude account beside the provider key the gateway holds. A project default
// that disagrees with the other mixed stage's own key would be a second one,
// and it is refused rather than ignored — naming the same account in both keys
// is the fix, and the matrix above pins that shape as admitted.
func TestClaudeAccountForRefusesAMixThatWouldBeASecondAccount(t *testing.T) {
	env, co := mixCase(t, "default_claude_account: acme-overflow\n"+stagesBlock("review", "acme-personal"),
		claudeReviewer, mixEnforce)
	_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "adversarial", "", co)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a refusal", err)
	}
	for _, want := range []string{"acme-overflow", "acme-personal", "stages.review.claude_account", "second credential"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestClaudeAccountForRefusesAMixTheModeCannotCarry: the mix exists because
// the gateway holds the provider key and routes the coder's calls to it, and
// only budget.mode: enforce routes a call to a provider at all. A
// configuration that declares a mix under any other mode declares a key that
// could never apply, so the launch refuses it and names the mode.
func TestClaudeAccountForRefusesAMixTheModeCannotCarry(t *testing.T) {
	for _, mode := range []string{"off", "observe"} {
		t.Run(mode, func(t *testing.T) {
			env, co := mixCase(t, "default_claude_account: acme-overflow\n", claudeReviewer, "budget: { mode: "+mode+", per_run_usd: 5 }\n")
			_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a refusal", err)
			}
			for _, want := range []string{"budget.mode", mode, "enforce", "default_claude_account"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestClaudeAccountForHoldsTheDeclaredAccountsToTheAllowList: the allow-list
// covers every declared name, not only the one a launch could resolve. A
// stage account is a name the *run's review stages* will use, so it is checked
// wherever it is written — including a launch that resolves no auth at all
// (no checkout), which is exactly the state that used to skip the check.
func TestClaudeAccountForHoldsTheDeclaredAccountsToTheAllowList(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		stagesBlock("review", "acme-elsewhere")+"default_claude_account: acme-elsewhere\n")
	env := gcsEnv(t, f)
	_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", nil)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-elsewhere") {
		t.Fatalf("err = %v, want the allow-list refusal", err)
	}
}
