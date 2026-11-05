package cli

import (
	"context"
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

// TestClaudeAccountForIgnoresTheProjectDefaultUnderApiKey pins the ruling's
// conservative reading: default_claude_account is how an oauth run picks its
// one credential, and it never opts an api-key run into the mix — such a run
// resolves the unsuffixed default, so its coder stays on the provider key the
// gateway holds.
func TestClaudeAccountForIgnoresTheProjectDefaultUnderApiKey(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, defaultClaudeAccountConfig)
	recipeCheckout(t, f, "", nil) // agent.auth: api-key
	env := gcsEnv(t, f)
	co := checkoutConfig(context.Background(), "acme/other")
	if co == nil {
		t.Fatal("checkoutConfig found no checkout")
	}
	got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "", "", co)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.DefaultClaudeAccount {
		t.Fatalf("account = %q, want %q: default_claude_account never opts an api-key run into the mix", got, config.DefaultClaudeAccount)
	}
}
