package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// accountOpsYAML is an operator's config with one Claude account and a profile
// that serves it over one repository. The account's allow_data_to holds the
// slug, and the profile's allow_repos holds the canonical repository it
// covers: the two lists the subset rule compares (D3), one at each end.
const accountOpsYAML = "claude_accounts:\n" +
	"  ops: { allow_data_to: [acme/app] }\n" +
	"profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops], allow_repos: [\"github/acme/app\"] }\n"

// TestPublishDerivesAccounts: the accounts block is not something an operator
// writes — it is derived at publish time from the account's own allow_data_to
// in the local config, one anchor one edit (D3), and the profile carries the
// list of accounts it serves. What the node must hold is the CANONICAL
// repository: the profile's provider qualifies the slug, which is what makes
// the account's list comparable to the profile's own (both are
// provider-qualified, and AdmitRepo matches segment against segment).
//
// The mutation is publishing the slug unqualified: the account's list then
// holds "acme/app", which no run's repository matches, so the account covers
// nothing — and the node appears to state a gate it does not.
func TestPublishDerivesAccounts(t *testing.T) {
	f := newProjectFixture(t, accountOpsYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if got := p.AccountNames(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("the published accounts = %v, want the one profile serves", got)
	}
	if got := p.Accounts["ops"].AllowRepos; len(got) != 1 || got[0] != "github/acme/app" {
		t.Fatalf("accounts.ops.allow_repos = %v, want the provider-qualified repository the profile admits", got)
	}
	if got := p.Profiles["standard"].ClaudeAccounts; len(got) != 1 || got[0] != "ops" {
		t.Fatalf("profiles.standard.claude_accounts = %v, want the account the operator listed", got)
	}
	// The account no profile names is not published: a list of repositories on
	// a node every run token can read, for an account no job mounts, is a fact
	// about nothing — and `policy show` would print it as though it governed.
	unused := "claude_accounts:\n" +
		"  ops: { allow_data_to: [acme/app] }\n" +
		"  spare: { allow_data_to: [acme/app] }\n" +
		"profiles:\n" +
		"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops], allow_repos: [\"github/acme/app\"] }\n"
	f2 := newProjectFixture(t, unused)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f2).AccountNames(); len(got) != 1 || got[0] != "ops" {
		t.Errorf("an account no profile names was published: %v", got)
	}
}

// TestPublishDerivesDefaultFromProjectSecrets: the default account is not a
// claude_accounts entry ("default" is reserved there, D5) — a profile lists it
// as "default", and its repositories are project_secrets' own allow_data_to,
// the entry that already decides whether its container exists. The mutation is
// reading claude_accounts for it (which defines nothing, so the publish is
// refused and the operator is sent to a block that cannot hold it), or
// publishing it with an empty list.
func TestPublishDerivesDefaultFromProjectSecrets(t *testing.T) {
	f := newProjectFixture(t, "project_secrets:\n"+
		"  claude-oauth-token: { allow_data_to: [acme/app] }\n"+
		"profiles:\n"+
		"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [default], allow_repos: [\"github/acme/app\"] }\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if got := p.Accounts["default"].AllowRepos; len(got) != 1 || got[0] != "github/acme/app" {
		t.Fatalf("accounts.default.allow_repos = %v, want the project_secrets entry's repository", got)
	}
}

// TestPublishRefusesAProfileItsAccountsDoNotCover: the subset rule at the
// publisher, end to end — the plan's red step, through the command an operator
// runs. The profile admits github/acme/app while its account may be used by
// acme/tools only, and publish must refuse rather than write a node under
// which a run of acme/app holds a token its owner said may not be used there.
//
// The refusal names both lists and the repository (design §4.4 row 2), and no
// node is written.
func TestPublishRefusesAProfileItsAccountsDoNotCover(t *testing.T) {
	f := newProjectFixture(t, "claude_accounts:\n"+
		"  ops: { allow_data_to: [acme/tools] }\n"+
		"profiles:\n"+
		"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops], allow_repos: [\"github/acme/app\"] }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a profile admitting a repository its account does not cover succeeded:\n%s", out)
	}
	for _, want := range []string{"profiles.standard.claude_accounts", "github/acme/app", "accounts.ops.allow_repos", "profiles.standard.allow_repos"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
}

// TestPolicyShowPrintsAccounts: D10's ruling — the operator reads which
// repositories each account's token may be used by, and which accounts a
// profile serves, off `policy show`. The account is a line of its own (it is
// not a profile's field: it is the block a run of the account's job is
// bounded by), and the profile's line names the accounts it serves, because a
// profile that serves one and a profile that serves none map to different
// jobs.
func TestPolicyShowPrintsAccounts(t *testing.T) {
	newProjectFixture(t, accountOpsYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  accounts: ops",
		"  account ops: allow_repos github/acme/app",
		"claude_accounts ops",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("policy show does not print %q:\n%s", want, out)
		}
	}

	// And a policy with no accounts reads as one: the line says so, rather
	// than being absent (which a reader could take for a command that does not
	// print the block at all).
	newProjectFixture(t, projectProfilesYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  accounts: none (no profile and no repository entry serves a Claude account)") {
		t.Errorf("policy show does not say a policy serves no account:\n%s", out)
	}
}

// TestPublishDiffShowsTheAccounts: the diff is what an operator reads before
// the write, and an account's list is which repositories may hold that token —
// a list that grew is one more repository whose runs hold it. The mutation is
// leaving the block out of the diff: the node changes under a publish that
// printed no line about it, which is the shape of a silent widening.
func TestPublishDiffShowsTheAccounts(t *testing.T) {
	f := newProjectFixture(t, projectProfilesYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The same machine, now with an account: the operator adds the
	// claude_accounts entry and the profile that serves it, and publishes
	// again. The node keeps its project_secrets-free standard profile and its
	// wide one, so the only new lines are the account's.
	cfg, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	const servedRow = "  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [deploy-key] }"
	if !strings.Contains(string(cfg), servedRow) {
		t.Fatalf("the fixture's profile row moved:\n%s", cfg)
	}
	cfg = []byte("claude_accounts:\n  ops: { allow_data_to: [acme/app] }\n" +
		strings.Replace(string(cfg), servedRow,
			"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops], allow_repos: [\"github/acme/app\"] }", 1))
	if err := os.WriteFile(f.path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		// The summary row was already there, reading "none": the change is
		// what it becomes, and the per-account line is new because the block
		// had no entry before.
		"~ accounts: none (no profile and no repository entry serves a Claude account) → ops",
		"+ account ops: allow_repos github/acme/app",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish diff does not print %q:\n%s", want, out)
		}
	}
}

// repoAccountYAML is an operator's config whose ONLY source of the accounts
// block is a repository's own entry: no profile names an account at all. It is
// the shape a per-repository job needs (a Bitbucket repository, or any
// repository no workflow sends to a shared job), and the shape that had no way
// to be admitted to an account before repos.<owner/name>.claude_accounts
// existed.
const repoAccountYAML = "claude_accounts:\n" +
	"  ops: { allow_data_to: [acme/other] }\n"

// withRepoAccounts puts a claude_accounts list on one of the fixture's own
// repos entries: the config's per-repository block, written where the operator
// writes it, beside the provider that qualifies the repository.
func withRepoAccounts(t *testing.T, f *projectFixture, entry, accounts string) {
	t.Helper()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	before := "  " + entry + " { provider: bitbucket, workflows: [web] }"
	if !strings.Contains(string(raw), before) {
		t.Fatalf("the fixture's repos entry moved:\n%s", raw)
	}
	after := "  " + entry + " { provider: bitbucket, workflows: [web], claude_accounts: [" + accounts + "] }"
	if err := os.WriteFile(f.path, []byte(strings.Replace(string(raw), before, after, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPublishAdmitsARepositorysOwnAccount — THE RED STEP — is the whole point
// of the repository-level source, end to end through the command an operator
// runs: a repository that no profile serves names its account in its own entry,
// and publish writes BOTH halves — the account's allow_repos in the shared node
// (with the repository's canonical name, qualified by the entry's provider) and
// the repository's own child admission under config/policy/repos/$slug, which
// is what the credentials service intersects the account list with.
//
// Without either half the service offers no candidate for that repository's
// runs and every model call is answered 503 state_unavailable: the node half
// alone leaves the child's admission empty, and the child half alone names an
// account the node does not cover.
//
// The mutation is dropping the repos argument from the publish's derivation
// (DeriveAccounts' fourth parameter): the block comes back empty, `policy show`
// prints "none", and no child is written at all.
func TestPublishAdmitsARepositorysOwnAccount(t *testing.T) {
	f := newProjectFixture(t, repoAccountYAML)
	withRepoAccounts(t, f, "acme/other:", "ops")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if got := p.AccountNames(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("the published accounts = %v, want the one repository entry names", got)
	}
	if got := p.Accounts["ops"].AllowRepos; len(got) != 1 || got[0] != "bitbucket/acme/other" {
		t.Fatalf("accounts.ops.allow_repos = %v, want the repository's canonical name (qualified by the entry's provider)", got)
	}
	// The child: the account names the repository's slug may be served by, and
	// nothing else (O4).
	slug, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	raw := f.db.Value(projectpolicy.PathRepos() + "/" + slug)
	if raw == nil {
		t.Fatalf("publishing a repository-level account wrote no child at %s/%s:\n%s", projectpolicy.PathRepos(), slug, out)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	child, found, err := projectpolicy.DecodeRepo(body)
	if err != nil || !found {
		t.Fatalf("the child does not decode (found=%v): %v", found, err)
	}
	if got := child.AdmittedAccounts(); len(got) != 1 || got[0] != "ops" {
		t.Errorf("the child admits %v, want [ops]", got)
	}
	// `policy show` names the account, with the repository the entry admitted
	// (D10): a block that filled the node and printed "none" would tell the
	// operator their per-repository entry did nothing.
	shown, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  accounts: ops", "  account ops: allow_repos bitbucket/acme/other"} {
		if !strings.Contains(shown, want) {
			t.Errorf("policy show does not print %q:\n%s", want, shown)
		}
	}
}

// TestPublishRefusesARepositoryEntryItsAccountDoesNotCover: the D3 subset rule
// at the publisher, through the command an operator runs, for a repository's
// own entry. The entry names an account whose allow_data_to holds a different
// repository, and publish must refuse rather than write a node under which the
// runs of acme/other hold a token its owner said may not be used there — the
// refusal names the entry and the account's own list, which are the two edits
// that fix it.
//
// The mutation is skipping the coverage check (repoAccountCovers): the node and
// the child are both written, and the account serves a repository its own
// allow_data_to excludes — a widening no publish may make.
func TestPublishRefusesARepositoryEntryItsAccountDoesNotCover(t *testing.T) {
	f := newProjectFixture(t, "claude_accounts:\n  ops: { allow_data_to: [acme/app] }\n")
	withRepoAccounts(t, f, "acme/other:", "ops")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a repository entry naming an account that does not cover it succeeded:\n%s", out)
	}
	for _, want := range []string{"repos.acme/other.claude_accounts", "acme/other", "claude_accounts.ops.allow_data_to", "acme/app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
	slug, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if f.db.Value(projectpolicy.PathRepos()+"/"+slug) != nil {
		t.Errorf("a refused publish wrote the child at %s/%s", projectpolicy.PathRepos(), slug)
	}
}

// TestUpdateConvergesOnAPolicyPublishedBeforeRepositoryEntries: the accounts
// block gained a second source, and `fugaro update` must bring an installation
// published by the release before it to the same node with no extra command and
// no admission lost. The file here is the one such an installation has — an
// account a profile names, and no repos entry naming one — so what the new
// derivation publishes is byte for byte what the old one published: the drift
// list the update stage's write is decided by is empty, which is the assertion
// (m.drift: policyConfigDrift is what sets m.pub, and an empty list writes no
// node), and the account's own admission is still on the node afterwards.
//
// The mutation is making the repository source the GATE of the derivation
// rather than a second source of it — `if len(repoAccounts) == 0 { return nil,
// nil }` in place of the two-source guard — which is the plausible mistake:
// this config names no account per repository, and the publish then refuses the
// profile's own account ("the policy defines no account \"ops\""), so the test
// fails at its first command.
func TestUpdateConvergesOnAPolicyPublishedBeforeRepositoryEntries(t *testing.T) {
	f := newProjectFixture(t, accountOpsYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	before := publishedPolicy(t, f)
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if drift := policyConfigDrift(lc, before); len(drift) != 0 {
		t.Errorf("a config that names no accounts per repository drifts on %v, want nothing: the update would rewrite a node it already agrees with", membersOf(drift))
	}
	after := publishedPolicy(t, f)
	if got := after.Accounts["ops"].AllowRepos; len(got) != 1 || got[0] != "github/acme/app" {
		t.Errorf("accounts.ops.allow_repos = %v after the update, want the admission the node already carried", got)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if f.db.Value(projectpolicy.PathRepos()+"/"+slug) == nil {
		t.Errorf("the child at %s/%s is gone: an update may never take an admission away", projectpolicy.PathRepos(), slug)
	}
}

// membersOf is a drift list's member names, for a failure message that says
// which member moved.
func membersOf(drift []policyDrift) []string {
	out := make([]string, 0, len(drift))
	for _, d := range drift {
		out = append(out, d.Member)
	}
	return out
}
