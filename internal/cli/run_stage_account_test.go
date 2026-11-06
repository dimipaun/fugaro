package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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

// TestRunAdmitsAStageAccountForTheReviewsOnly: the permitted mix, and the job
// it lands on. An api-key run may name one account for review and adversarial,
// and the launch's own account — what spec.OAuthToken names, and so the job
// variant the run starts on — is that account: the account's own job is the
// only one that mounts its token, and the run's review stages read the token
// out of the runner's environment (mixedRunAccount, claudeAccountFor). The
// coder's own credential does not move with it: the coder's stages stay on the
// provider key the gateway holds, and the runner is what builds each stage's
// environment.
//
// Mutation (remove the `name = mix` line in claudeAccountFor's launch
// resolution): OAuthToken is empty and this test fails — the run starts on the
// base job, which mounts no token, and its review stage is refused for one
// (Check 290's failure).
func TestRunAdmitsAStageAccountForTheReviewsOnly(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, stageAccountConfig("review", "acme-overflow", "adversarial", "acme-overflow"))
	recipeCheckout(t, f, "", nil)
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	if _, _, err := execute(t, "run", "--run-id", "20261105-100000-eeee", "A task"); err != nil {
		t.Fatal(err)
	}
	if got := otherSpec(t, f, "20261105-100000-eeee").OAuthToken; got != "acme-overflow" {
		t.Fatalf("OAuthToken = %q, want acme-overflow: a mixed run starts on the job that mounts the account's token", got)
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
	// mixElsewhereConfig is claudeAccountsConfig plus an account this
	// repository may not use: the name the rows about the default's allow-list
	// write into default_claude_account, which must not refuse a launch that
	// could never apply it.
	mixElsewhereConfig = claudeAccountsConfig + "  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"
	// mixElsewhereDefault is the key those rows write it in.
	mixElsewhereDefault = "default_claude_account: acme-elsewhere\n"
)

// mixCase is one row's fixture: the accounts (keys adds the account-choice
// keys the row is about), the routes and the budget mode, over a checkout of
// acme/other whose coder is on the provider route and whose reviews run on
// reviewer. It returns the env and the checkout's own document, which is what
// claudeAccountFor is asked with.
func mixCase(t *testing.T, keys, reviewer, budget string) (*cloudEnv, *config.Config) {
	t.Helper()
	return mixCaseWith(t, claudeAccountsConfig, keys, reviewer, mixCoderModel, budget)
}

// mixCaseWith is mixCase with the accounts block, the reviewer and the coder
// spelled out: the row about the coder's own route needs a coder no provider
// route serves, and the rows about the default's allow-list need an account
// this repository may not use. The checkout is agent.auth: api-key (recipeCheckout's
// own line), with the coder and the reviews on the models given.
func mixCaseWith(t *testing.T, accounts, keys, reviewer, coder, budget string) (*cloudEnv, *config.Config) {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, accounts+mixRouteConfig+budget+keys)
	recipeCheckout(t, f, "  models: { coder: "+coder+", reviewer: "+reviewer+", background: "+coder+" }\n", nil)
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
// Four mutations fail this test: dropping the allow-list from mixedStageAccount
// serves the account to implement, review_first and fix; dropping the model
// condition from it serves the project default to a review stage that runs on
// the provider; dropping the provider-served condition serves it to a run whose
// coder is Claude's and whose gateway holds no provider key (the last row);
// and reading the sentinel default as an account serves the unsuffixed default
// to the two review stages of a project that named none.
func TestClaudeAccountForTheMixMatrix(t *testing.T) {
	// rung 2 is the stage's own key — both mixed stages name one account,
	// which is the shape the one-account rule admits; rung 4 is the project
	// default alone; "both" names the same account in each; "neither" declares
	// no mix at all and is the control; and the sentinel row is the value a
	// local config may spell the default key with — "default" is the unsuffixed
	// default account, which an api-key run's job carries no token for, so it
	// names no account and declares no mix whatever the stage's model is.
	rungs := []struct{ name, keys string }{
		{"rung 2", stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow")},
		{"rung 4", "default_claude_account: acme-overflow\n"},
		{"both", "default_claude_account: acme-overflow\n" + stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow")},
		{"the sentinel default", "default_claude_account: " + config.DefaultClaudeAccount + "\n"},
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
	// The coder's own route is a condition of the mix and not a detail of it: an
	// api-key run whose coder is a Claude model reaches Anthropic's endpoint
	// direct, so the gateway holds no provider key for the account to sit
	// beside, and rung 4 admits nothing — whatever the stage's own model is and
	// whatever the ruling's other conditions say.
	t.Run("a coder no provider route serves", func(t *testing.T) {
		for _, stage := range config.MixedAccountStages {
			env, co := mixCaseWith(t, claudeAccountsConfig, "default_claude_account: acme-overflow\n", claudeReviewer, claudeReviewer, mixEnforce)
			got, err := claudeAccountFor(context.Background(), env, "acme/other", "", stage, "", co)
			if err != nil {
				t.Fatalf("stage %q: %v", stage, err)
			}
			if got != config.DefaultClaudeAccount {
				t.Errorf("stage %q: account = %q, want %q: a coder on Claude's own endpoint is served no provider key", stage, got, config.DefaultClaudeAccount)
			}
		}
	})
}

// TestClaudeAccountForLandsAMixedRunOnTheMixsAccount is the launch's own
// resolution — no stage asked for — over the rungs a mix is named by and the
// conditions that admit it: the account a mixed run starts on is the account
// its review stages are served by, because spec.OAuthToken is what names the
// job variant and only the account's own job mounts its token
// (mixedRunAccount). It is the launch's account and not the run's
// *credential*: the coder's stages stay on the provider key the gateway holds
// (the runner builds each stage's environment), so a run with no admitted mix
// resolves the unsuffixed default exactly as it did.
//
// Mutation (remove the `name = mix` line in claudeAccountFor's launch
// resolution): the two admitted rows fail, which is the bug — the launch
// resolves the base job and the review stage it serves finds no token.
func TestClaudeAccountForLandsAMixedRunOnTheMixsAccount(t *testing.T) {
	cases := []struct{ name, keys, reviewer, want string }{
		{"rung 2: the stage's own key", stagesBlock("review", "acme-overflow"), claudeReviewer, "acme-overflow"},
		{"rung 4: the project default, an Anthropic reviewer", "default_claude_account: acme-overflow\n", claudeReviewer, "acme-overflow"},
		// rung 4 is the default's own rung and carries its own condition: a
		// provider reviewer is no mix and no error, and the key names nothing.
		{"rung 4, a provider reviewer", "default_claude_account: acme-overflow\n", mixCoderModel, config.DefaultClaudeAccount},
		{"the sentinel default names no account", "default_claude_account: " + config.DefaultClaudeAccount + "\n", claudeReviewer, config.DefaultClaudeAccount},
		{"no mix at all", "", claudeReviewer, config.DefaultClaudeAccount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, co := mixCase(t, tc.keys, tc.reviewer, mixEnforce)
			got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("account = %q, want %q", got, tc.want)
			}
		})
	}
	// A coder no provider route serves is no mix either: the account sits
	// beside a provider key the gateway holds, and such a run's gateway holds
	// none.
	t.Run("a coder no provider route serves", func(t *testing.T) {
		env, co := mixCaseWith(t, claudeAccountsConfig, "default_claude_account: acme-overflow\n", claudeReviewer, claudeReviewer, mixEnforce)
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
		if err != nil {
			t.Fatal(err)
		}
		if got != config.DefaultClaudeAccount {
			t.Fatalf("account = %q, want %q", got, config.DefaultClaudeAccount)
		}
	})
	// The one-account rule is the launch's too, and it is asked of the
	// launch's own resolution and not only of a stage's: a project default
	// that disagrees with a stage key is a second credential, refused before
	// anything is started.
	t.Run("a second, differing account", func(t *testing.T) {
		env, co := mixCase(t, "default_claude_account: acme-overflow\n"+stagesBlock("review", "acme-personal"), claudeReviewer, mixEnforce)
		_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "second credential") {
			t.Fatalf("err = %v, want the one-account refusal", err)
		}
	})
}

// TestRunLandsAMixedRunOnTheMixsAccount is the same rule read off a launch:
// the stored task's oauth_token is the mix's account, which is what picks the
// job variant the run starts on. A run with no admitted mix keeps the base job
// (no oauth_token at all).
func TestRunLandsAMixedRunOnTheMixsAccount(t *testing.T) {
	cases := []struct {
		name, keys, reviewer, runID, want string
	}{
		{"the project default on an Anthropic review", "default_claude_account: acme-overflow\n", claudeReviewer, "20261105-100000-1111", "acme-overflow"},
		{"no mix: a provider reviewer", "default_claude_account: acme-overflow\n", mixCoderModel, "20261105-100000-2222", ""},
		{"no mix: no key at all", "", claudeReviewer, "20261105-100000-3333", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			f.appendConfig(t, claudeAccountsConfig+mixRouteConfig+mixEnforce+tc.keys)
			recipeCheckout(t, f, "  models: { coder: "+mixCoderModel+", reviewer: "+tc.reviewer+", background: "+mixCoderModel+" }\n", nil)
			writeBuildRecord(t, f, otherSlug, "svc", release065)
			// The account's own job variant, which a mixed run has to start
			// on: the base job is registered by recipeCheckout.
			f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
			if _, _, err := execute(t, "run", "--run-id", tc.runID, "A task"); err != nil {
				t.Fatal(err)
			}
			if got := otherSpec(t, f, tc.runID).OAuthToken; got != tc.want {
				t.Fatalf("OAuthToken = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunRetryLaunchesAMixedRunOnTheMixsAccount: --retry replays a stored task
// as it stands, and a mixed run's task is the shape this launcher itself
// writes — a named account under agent.auth: api-key. That is exactly the name
// claudeAccountAllowed refuses outright, so a retry of a mixed run was refused
// for the name the same binary had just written, with a sentence ("only
// applies to a workflow whose agent.auth is oauth") that is false about it.
// retrySpec asks the whole resolver instead (claudeAccountFor), the way the
// repeated --run-id path does, and the stored token stays what the launch
// carries: it starts on the account's own job variant, which is the only job
// that mounts the token its review stages read.
//
// Mutation (retrySpec back to claudeAccountAllowed): the retry is refused and
// no execution starts.
func TestRunRetryLaunchesAMixedRunOnTheMixsAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, defaultClaudeAccountConfig+mixRouteConfig+mixEnforce)
	recipeCheckout(t, f, "  models: { coder: "+mixCoderModel+", reviewer: "+claudeReviewer+", background: "+mixCoderModel+" }\n", nil)
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	variant := gcp.WorkflowVariant("svc", "acme-overflow")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")
	// The task a mixed launch of this repository stores, never started —
	// --retry's own case.
	runID := wallRunIDAt(0, "100000", "a3c1")
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/other", Ref: "develop", Workflow: "svc", Task: "x",
		RequestedBy: "someone@example.com", OAuthToken: "acme-overflow"}
	if err := runstore.Open(b, otherSlug, runID).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	var got gcpfake.RunCall
	f.run.OnRun = func(c gcpfake.RunCall) { got = c }
	if _, _, err := execute(t, "run", "--retry", runID); err != nil {
		t.Fatal(err)
	}
	if want := gcp.JobName(otherSlug, variant); got.Job != want {
		t.Fatalf("launched job = %q, want %q", got.Job, want)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// TestClaudeAccountForReadsTheSentinelDefaultAsNoAccount: "default" is the
// unsuffixed default account — the sentinel task.Spec.OAuthToken's own rule
// reads as "no token of the run's own", which is where a local config may
// spell either account-choice key (localcfg's choiceRefProblem admits it) — and
// an api-key run's job carries no OAuth token for the default account at all.
// So a project that writes default_claude_account: default declares no mix: the
// key is not a way into the review stages, the launch is not refused under a
// mode that could not carry one, and the key is not a second credential beside
// the other mixed stage's own.
func TestClaudeAccountForReadsTheSentinelDefaultAsNoAccount(t *testing.T) {
	sentinel := "default_claude_account: " + config.DefaultClaudeAccount + "\n"
	t.Run("the key declares no mix", func(t *testing.T) {
		env, co := mixCase(t, sentinel, claudeReviewer, "budget: { mode: observe, per_run_usd: 5 }\n")
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
		if err != nil {
			t.Fatalf("err = %v, want no refusal: the sentinel names no account", err)
		}
		if got != config.DefaultClaudeAccount {
			t.Fatalf("account = %q, want %q", got, config.DefaultClaudeAccount)
		}
	})
	t.Run("the key is no second credential", func(t *testing.T) {
		env, co := mixCase(t, sentinel+stagesBlock("adversarial", "acme-overflow"), claudeReviewer, mixEnforce)
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
		if err != nil {
			t.Fatalf("err = %v, want no refusal: the sentinel is not an account the stage key could disagree with", err)
		}
		if got != config.DefaultClaudeAccount {
			t.Fatalf("review account = %q, want %q", got, config.DefaultClaudeAccount)
		}
		if got, err = claudeAccountFor(context.Background(), env, "acme/other", "", "adversarial", "", co); err != nil || got != "acme-overflow" {
			t.Fatalf("adversarial account = %q, err = %v: the stage's own key is served as written", got, err)
		}
	})
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

// TestClaudeAccountForHoldsTheDefaultWhereRungFourCanApply: the project default
// is written once for every run the project has, so it is held to this
// repository's allow-list exactly where rung 4 would hand it to something. The
// finding this test answers was the first cut's unconditional check: a default
// no stage of *this* run could use refused every launch of a repository its
// allow_data_to does not name — an api-key run with a provider reviewer (the
// ruling's own "no mix and no error"), an oauth run whose --token names an
// allowed account, a vertex run — none of which could ever reach the name.
//
// What the check may not stop doing is guarding the rung it belongs to, and the
// last three subtests are that half: an api-key run whose reviews *are* Claude's
// is refused, an oauth run whose own credential is the default is refused by the
// walk's own claudeAccountAllowed, and an auth the launch cannot place yet ("")
// fails closed, because no rung of that launch reads the key.
//
// Mutation: restoring the unconditional check fails the first two subtests,
// which is exactly the bug — a refusal for a name the launch could not use.
func TestClaudeAccountForHoldsTheDefaultWhereRungFourCanApply(t *testing.T) {
	t.Run("an api-key run whose reviews are not Claude's", func(t *testing.T) {
		// The coder is on the provider route and the reviewer on the same
		// provider model, so nothing about this run is Claude's: the default is
		// not a way into any stage and is not judged.
		env, co := mixCaseWith(t, mixElsewhereConfig, mixElsewhereDefault, mixCoderModel, mixCoderModel, mixEnforce)
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
		if err != nil {
			t.Fatalf("err = %v, want no refusal: the default can never apply to this run", err)
		}
		if got != config.DefaultClaudeAccount {
			t.Fatalf("account = %q, want %q", got, config.DefaultClaudeAccount)
		}
	})
	t.Run("an oauth run that names its own account", func(t *testing.T) {
		// agent.oauth_token (rung 3) and the --token flag (rung 1) both sit
		// above the project default, so the walk never picks it, and a name the
		// run does not use is not one this launch has to refuse.
		f := newCloudFixture(t)
		f.appendConfig(t, mixElsewhereConfig+mixElsewhereDefault)
		oauthCheckout(t, f, "  oauth_token: acme-overflow\n")
		env := gcsEnv(t, f)
		co := checkoutConfig(context.Background(), "acme/other")
		if co == nil {
			t.Fatal("checkoutConfig found no checkout")
		}
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
		if err != nil {
			t.Fatalf("err = %v, want no refusal: acme-overflow is this run's own account", err)
		}
		if got != "acme-overflow" {
			t.Fatalf("account = %q, want acme-overflow (agent.oauth_token's rung)", got)
		}
		got, err = claudeAccountFor(context.Background(), env, "acme/other", "acme-personal", "", "", co)
		if err != nil {
			t.Fatalf("err = %v, want no refusal: the flag's own account is allowed here", err)
		}
		if got != "acme-personal" {
			t.Fatalf("account = %q, want acme-personal (the flag's rung)", got)
		}
	})
	t.Run("an api-key run whose reviews are Claude's", func(t *testing.T) {
		// The mix is admitted for this stage, so the default is a name this run
		// will use — and it is not one the repository may use.
		env, co := mixCaseWith(t, mixElsewhereConfig, mixElsewhereDefault, claudeReviewer, mixCoderModel, mixEnforce)
		_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want the allow-list refusal", err)
		}
		for _, want := range []string{"acme-elsewhere", "not one acme/other may use", "allowed: default, acme-overflow, acme-personal"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	})
	t.Run("an oauth run whose own account is the default", func(t *testing.T) {
		// Nothing above rung 4 names an account, so the walk picks the default
		// and the run would land on a job holding a token this repository may
		// not use: the refusal is claudeAccountAllowed's, at the end of the walk.
		f := newCloudFixture(t)
		f.appendConfig(t, mixElsewhereConfig+mixElsewhereDefault)
		oauthCheckout(t, f, "")
		env := gcsEnv(t, f)
		co := checkoutConfig(context.Background(), "acme/other")
		if co == nil {
			t.Fatal("checkoutConfig found no checkout")
		}
		_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-elsewhere") {
			t.Fatalf("err = %v, want the allow-list refusal", err)
		}
	})
	t.Run("an auth the launch cannot place", func(t *testing.T) {
		// No checkout means no resolved auth, and the committed fugaro.yaml may
		// name anything: the launch fails closed on the default rather than
		// leaving the allow-list to a rung it cannot see.
		f := newCloudFixture(t)
		f.appendConfig(t, mixElsewhereConfig+mixElsewhereDefault)
		env := gcsEnv(t, f)
		_, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", nil)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-elsewhere") {
			t.Fatalf("err = %v, want the allow-list refusal", err)
		}
	})
}
