package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestAccountsLsNoProjectConfigNeedsNoCloud: with no project config selected
// at all (no FUGARO_CONFIG, nothing in $XDG_CONFIG_HOME/fugaro/projects), the
// command still answers with the always-available default account, never
// failing for lack of a GCP project, credentials or a runs bucket. This is
// the offline claim: claude_accounts lives only in the local config, so with
// none selected there is nothing to read but "default" is still real.
func TestAccountsLsNoProjectConfigNeedsNoCloud(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Repo != "acme/app" || len(doc.Accounts) != 1 || doc.Accounts[0] != "default" {
		t.Fatalf("doc = %+v, want repo acme/app, accounts [default]", doc)
	}
}

// TestAccountsLsDefaultFirstThenNamedSorted pins config.AllowedClaudeAccounts'
// own order (design docs/design/named-oauth-tokens.md §6a): "default" always
// first, then every allowed name sorted.
func TestAccountsLsDefaultFirstThenNamedSorted(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig) // acme-personal, acme-overflow allow acme/other
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	want := []string{"default", "acme-overflow", "acme-personal"}
	if len(doc.Accounts) != len(want) {
		t.Fatalf("accounts = %v, want %v", doc.Accounts, want)
	}
	for i, w := range want {
		if doc.Accounts[i] != w {
			t.Fatalf("accounts = %v, want %v", doc.Accounts, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none: accounts ls must never launch anything", n)
	}
}

// TestAccountsLsExcludesUnlistedRepo: a named account's allow_data_to not
// listing the repository leaves that repository with "default" only, exactly
// the set fugaro run --token would refuse every other name for
// (claudeAccountAllowed).
func TestAccountsLsExcludesUnlistedRepo(t *testing.T) {
	f := newCloudFixture(t) // acme/app
	f.appendConfig(t, claudeAccountsConfig)
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0] != "default" {
		t.Fatalf("accounts = %v, want [default]", doc.Accounts)
	}
}

// TestAccountsLsDefaultsToCheckoutOrigin: with no --repo, the repository is
// the checkout's origin remote, the same default every other repo-scoped
// command (secrets ls, run) uses.
func TestAccountsLsDefaultsToCheckoutOrigin(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	out, _, err := execute(t, "accounts", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Repo != "acme/other" || len(doc.Accounts) != 3 {
		t.Fatalf("doc = %+v, want repo acme/other, 3 accounts", doc)
	}
}

// TestAccountsLsTextOutputIsNamesOnly: without --json, one name per line,
// nothing else (never a secret ID, a repo list or a token value).
func TestAccountsLsTextOutputIsNamesOnly(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{"default", "acme-overflow", "acme-personal"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	}
}

// TestAccountsLsBadRepoRefused: a malformed --repo is refused before
// anything is read, the same message task.CanonicalRepo gives every other
// repo-scoped command.
func TestAccountsLsBadRepoRefused(t *testing.T) {
	_, _, err := execute(t, "accounts", "ls", "--repo", "not-a-repo")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "owner/name") {
		t.Fatalf("err = %v, want a user error naming owner/name", err)
	}
}

// TestAccountsLsNoOriginRefused: with no --repo and no origin remote, the
// command refuses exactly as secrets ls and run do, rather than guessing.
func TestAccountsLsNoOriginRefused(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	t.Chdir(dir)
	_, _, err := execute(t, "accounts", "ls")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want a user error naming --repo", err)
	}
}

// TestAccountsLsNeverTouchesSecretManager: the fixture registers no
// secret_manager endpoint at all, yet the command succeeds — proof it never
// opens a Secret Manager client (unlike fugaro secrets ls, which would fail
// closed here). Pins the design's "no Secret Manager call" claim.
func TestAccountsLsNeverTouchesSecretManager(t *testing.T) {
	f := newCloudFixture(t) // no secret_manager endpoint among extraEndpoints
	f.appendConfig(t, claudeAccountsConfig)
	if _, _, err := execute(t, "accounts", "ls", "--repo", "acme/other"); err != nil {
		t.Fatalf("accounts ls failed without a secret_manager endpoint configured: %v", err)
	}
}
