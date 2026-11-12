package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The launch is one of the readers an alias is resolved at (D2091, D2092): a
// choice key and the --token flag may name an alias, and what the run is given
// is the account the alias stands for. The tests here are that resolution at
// each rung the launch reads a name from, the allow-list asked about the
// account rather than the alias (F6), and the stored task carrying the account
// and never the alias by its own name (F4).

// aliasBlock is the local config's claude_account_aliases block: one alias
// standing for one account of the same file's claude_accounts.
func aliasBlock(alias, target string) string {
	return "claude_account_aliases:\n  " + alias + ": " + target + "\n"
}

// TestLaunchResolvesAnAliasOnEveryRung is D2091 read at the launch: every name
// a rung reads out of the local config — the flag, the stage key of rung 2 and
// the project default of rung 4 — may name an alias, and the run goes to the
// account it stands for. Each subtest asserts the *account*: the allow-list
// admits it (an alias handed to it would be a name no account answers to, F6)
// and the task carries it (F4).
//
// Mutation (drop the ResolveAccount calls in claudeAccountFor): every subtest
// fails, refused as an account nothing defines.
func TestLaunchResolvesAnAliasOnEveryRung(t *testing.T) {
	t.Run("the flag", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow"))
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a1a1", "--token", "reviewer", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261111-120000-a1a1").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow: --token named the alias reviewer", got)
		}
	})
	t.Run("the stage key", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+stagesBlock("review", "reviewer"))
		oauthCheckout(t, f, "  oauth_token: acme-personal\n")
		env := gcsEnv(t, f)
		co := checkoutConfig(context.Background(), "acme/other")
		if co == nil {
			t.Fatal("checkoutConfig found no checkout")
		}
		// The account the reviews run on, which is what the runner's environment
		// and the stage's own job variant are built from.
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co)
		if err != nil {
			t.Fatal(err)
		}
		if got != "acme-overflow" {
			t.Fatalf("the review stage's account = %q, want acme-overflow: its key names the alias reviewer", got)
		}
	})
	t.Run("the project default", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a1a2", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261111-120000-a1a2").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow: the project default names the alias reviewer", got)
		}
	})
}

// TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted is F6 read as a refusal:
// the allow-list is asked about the account the alias stands for, so an alias
// whose target this repository may not use is refused in the account's own
// name — the sentence names acme-elsewhere and the claude_accounts entry that
// admits a token, which is the move that works — and nothing is started.
func TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		aliasBlock("reviewer", "acme-elsewhere")+"default_claude_account: reviewer\n")
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	_, _, err := execute(t, "run", "--run-id", "20261111-120000-a2a2", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user refusal", err)
	}
	for _, want := range []string{"acme-elsewhere", "claude_accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestLaunchTaskCarriesTheResolvedAccount is F4 at the launch: the task is the
// thing every later reader of the run holds — the runner, the console, a
// follow-up, a resume — so the alias has to stop here or it reaches all of
// them. The spec names the account, and no field of it carries the alias by
// its own name.
func TestLaunchTaskCarriesTheResolvedAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a3a3", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := otherSpec(t, f, "20261111-120000-a3a3")
	if spec.OAuthToken != "acme-overflow" {
		t.Fatalf("OAuthToken = %q, want acme-overflow", spec.OAuthToken)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "reviewer") {
		t.Fatalf("the stored task carries the alias by its own name: %s", raw)
	}
}
