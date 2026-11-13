package cli

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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

// The publish is the other reader an alias is resolved at (D2091, D2090): the
// node is what every job image and every teammate's launcher reads, and it
// holds accounts and never this machine's names for them. The tests here are
// what the publish writes for a file whose keys name aliases, and the repoint —
// the one edit the aliases exist for — as the widening the operator's own hand
// confirms (F4, F10).

// aliasPublishConfig is the machine's file for the publish tests: two accounts
// of the same project, both admitting it, and the choice keys naming an alias
// rather than the account. The alias stands for target, so a repoint is one
// edit of this block and no other line (D2093), which is what makes the account
// the keys resolve to move without the keys themselves changing.
func aliasPublishConfig(target string) string {
	return "claude_accounts:\n" +
		"  team-b: { allow_data_to: [acme/app] }\n" +
		"  team-c: { allow_data_to: [acme/app] }\n" +
		"claude_account_aliases:\n  reviewer: " + target + "\n" +
		"default_claude_account: reviewer\n" +
		"stages:\n  review: { claude_account: reviewer }\n"
}

// TestPublishWritesTheResolvedAccountNotTheAlias is the publish half of the
// launch's rule, member by member and before anything is written: the account
// members a publish carries are the accounts the file's keys resolve to, so the
// node speaks the account the alias stands for (D2090, F4) — a name a job image
// knows — and the alias' own name reaches no member.
func TestPublishWritesTheResolvedAccountNotTheAlias(t *testing.T) {
	lc := parseLocalConfig(t, "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n"+
		aliasPublishConfig("team-c"))
	got := configAccountValues(lc)
	want := map[string]string{
		"default_claude_account":       "team-c",
		"claude_account_stages.review": "team-c",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("the publish carries %v, want the accounts the aliases resolve to: %v", got, want)
	}
	if slices.Contains(slices.Collect(maps.Values(got)), "reviewer") {
		t.Errorf("a member carries the alias by its own name: %v", got)
	}
}

// repointedAliasFixture is the installation a repoint is measured on: the node
// is the document the publisher's own builder writes for a file whose keys name
// an alias standing for team-b, and the file is then edited so the same keys
// name team-c. Nothing but the alias block differs between the two, which is
// what makes the account members the only thing the publish can see.
func repointedAliasFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	rewriteProjectConfig(t, f, aliasPublishConfig("team-b"))
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := policyNodeForPublish(t.Context(), lc, nil, nil, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathShared(), node)
	rewriteProjectConfig(t, f, aliasPublishConfig("team-c"))
	return f
}

// TestRepointIsAPublishWidening: a repoint moves which account the project's
// runs go out on — the review credential, chosen for every run of the project
// and mounted into the job from this machine's file — so it is the widening the
// typed name guards (F10, D846). --yes alone is refused and the refusal names
// each member and both accounts, the node keeps the account it published, and
// the project's own name at a terminal is what confirms it.
func TestRepointIsAPublishWidening(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := repointedAliasFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone repointed the project's runs onto another account:\n%s", out)
	}
	for _, want := range []string{
		"default_claude_account team-b -> team-c",
		"claude_account_stages.review team-b -> team-c",
		"fugaro update",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if p := publishedPolicy(t, f); p.DefaultClaudeAccount != "team-b" || p.ClaudeAccountStages["review"] != "team-b" {
		t.Errorf("the refused publish wrote the repointed accounts anyway: %q / %v", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}

	h = typedRig(t, f)
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the repoint: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.DefaultClaudeAccount != "team-c" || p.ClaudeAccountStages["review"] != "team-c" {
		t.Errorf("the published node states default_claude_account %q and review %q, want the account the alias stands for now",
			p.DefaultClaudeAccount, p.ClaudeAccountStages["review"])
	}
}
