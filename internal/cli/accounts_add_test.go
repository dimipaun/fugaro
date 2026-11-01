package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// accountsAddLocal is the project config `fugaro accounts add` is asked to
// edit: comments the owner keeps, a key order that is not sorted, and a
// claude_accounts entry written by hand. yaml.Marshal drops the first, sorts
// the second away and rewrites the third, so this fixture is what tells an
// edit that went through internal/yamledit from one that round-tripped the
// decoded config (spec §2.3).
const accountsAddLocal = `# aurora's own config, kept in the owner's own order.
version: 1
name: aurora
# the bucket every run of this project lands in
runs_bucket: fugaro-runs-aurora-gcp
region: us-east5
gcp_project: aurora-gcp

claude_accounts:
  # the account the 2026 migration left behind
  legacy:
    allow_data_to:
      - acme/app

profiles:
  standard:
    provider: github
    allow_repos:
      - acme/app
`

// accountsAddLocalTwoRepos is the same file with the profile admitting a
// second repository, which a new account does not cover unless --repos names
// it too (the subset rule, spec §2.2 step 2).
const accountsAddLocalTwoRepos = `version: 1
name: aurora
gcp_project: aurora-gcp
region: us-east5
runs_bucket: fugaro-runs-aurora-gcp

profiles:
  standard:
    provider: github
    allow_repos:
      - acme/app
      - acme/lib
`

// accountsAddFixture writes this machine's project config with local and
// selects it, returning the file's path.
func accountsAddFixture(t *testing.T, local string) string {
	t.Helper()
	noAgentSession(t)
	isolateProjects(t, t.TempDir())
	path, err := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	return path
}

// accountsAddCheckout is a real checkout of repo with the working directory
// moved into it, so the command's --repos default and the widening its
// confirmation names are decided by this checkout's origin.
func accountsAddCheckout(t *testing.T, repo string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/"+repo+".git")
	t.Chdir(dir)
}

// TestAccountsAddKeepsCommentsAndOrder is A1's red step (spec §2.3): the two
// keys are edited in place, and everything else in the file — the comments,
// the order the owner wrote the keys in, the claude_accounts entry added by
// hand — is byte-identical afterwards. An implementation that marshals
// localcfg.Config fails here and nowhere else.
func TestAccountsAddKeepsCommentsAndOrder(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)
	before := readFileString(t, path)

	if _, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes"); err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	after := readFileString(t, path)

	for _, keep := range []string{
		"# aurora's own config, kept in the owner's own order.\n",
		"# the bucket every run of this project lands in\n",
		"  # the account the 2026 migration left behind\n",
	} {
		if !strings.Contains(after, keep) {
			t.Errorf("the edit dropped the comment %q:\n%s", keep, after)
		}
	}
	// The order the owner wrote, which a re-marshal sorts into version, name,
	// gcp_project, region, runs_bucket, profiles, claude_accounts.
	at := -1
	for _, key := range []string{"version:", "name:", "runs_bucket:", "region:", "gcp_project:", "claude_accounts:", "profiles:"} {
		i := strings.Index(after, key)
		if i < 0 {
			t.Fatalf("the edit dropped %q:\n%s", key, after)
		}
		if i < at {
			t.Errorf("%q moved: the file is no longer in the order it was written:\n%s", key, after)
		}
		at = i
	}
	// Every byte before the block the edit lands in is untouched, so nothing
	// above it was re-serialized.
	if head := before[:strings.Index(before, "claude_accounts:")]; !strings.HasPrefix(after, head) {
		t.Errorf("the edit rewrote the part of the file above claude_accounts:\n%s", after)
	}
	// The hand-written entry is intact, comment and all.
	if !strings.Contains(after, "  legacy:\n    allow_data_to:\n      - acme/app\n") {
		t.Errorf("the edit rewrote the hand-written account:\n%s", after)
	}
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want [acme/app]", got)
	}
}

// TestAccountsAddWritesTheDeclarationAndTheProfile: the two keys the command
// owns are written, and nothing else is. The account's allow_data_to is the
// repositories --repos names; the profile's claude_accounts gains the name;
// the profile's own allow_repos and every other account are left alone.
func TestAccountsAddWritesTheDeclarationAndTheProfile(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)

	out, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app,acme/lib", "--profile", "standard", "--yes")
	if err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"acme/app", "acme/lib"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want [acme/app acme/lib]", got)
	}
	if got := lc.Profiles["standard"].ClaudeAccounts; !slices.Equal(got, []string{"team-a"}) {
		t.Errorf("profiles.standard.claude_accounts = %v, want [team-a]", got)
	}
	if got := lc.Profiles["standard"].AllowRepos; !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("profiles.standard.allow_repos = %v, want the owner's [acme/app]", got)
	}
	if got := lc.ClaudeAccounts["legacy"].AllowDataTo; !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("the other account was rewritten: allow_data_to = %v", got)
	}
	if !strings.Contains(out, "claude_accounts.team-a.allow_data_to") {
		t.Errorf("the output does not name the key it wrote:\n%s", out)
	}
	if !strings.Contains(out, "profiles.standard.claude_accounts") {
		t.Errorf("the output does not name the profile it edited:\n%s", out)
	}
}

// TestAccountsAddConfirmsTheWideningItMakes is D868 read off the text: the one
// confirmation says what the account's allow_data_to will become and which
// profile gains the name, and — only when the token may reach a repository
// beyond the checkout the command runs in — the sentence saying what that
// widening means. The rest of the block is printed either way, so the
// confirmation is never a bare "y" (spec §2.6).
func TestAccountsAddConfirmsTheWideningItMakes(t *testing.T) {
	t.Run("beyond this checkout", func(t *testing.T) {
		accountsAddCheckout(t, "acme/other")
		accountsAddFixture(t, accountsAddLocal)
		out, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
		if err != nil {
			t.Fatalf("accounts add: %v", err)
		}
		for _, want := range []string{
			"claude_accounts.team-a.allow_data_to",
			"profiles.standard.claude_accounts",
			"a launcher that may start a run on acme/app can have that run print team-a's token",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the confirmation does not print %q:\n%s", want, out)
			}
		}
	})
	t.Run("this checkout only", func(t *testing.T) {
		accountsAddCheckout(t, "acme/app")
		accountsAddFixture(t, accountsAddLocal)
		out, _, err := execute(t, "accounts", "add", "team-a", "--yes")
		if err != nil {
			t.Fatalf("accounts add: %v", err)
		}
		if !strings.Contains(out, "claude_accounts.team-a.allow_data_to = [acme/app]") {
			t.Errorf("the confirmation does not state the change:\n%s", out)
		}
		if strings.Contains(out, "a launcher that may start a run on") {
			t.Errorf("the confirmation claims a widening for the checkout's own repository:\n%s", out)
		}
	})
}

// TestSettingsWritesTheAccountChoiceKeys is D873's three keys on the generic
// write path (spec §11.1): each resolves, each is the local config's alone, a
// layer or a value a key refuses leaves the file byte-identical, and turning
// `claude_account_auto` on is the sensitive direction — the project's own name
// is what confirms it, and --yes never does (D846, D875).
func TestSettingsWritesTheAccountChoiceKeys(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "\nclaude_accounts:\n  team-b:\n    allow_data_to: [acme/app]\n")
	before := readFileString(t, localPath)

	// The two scalars and the per-stage key all resolve: a write at
	// --layer local lands, and the read prints each row.
	for _, kv := range [][2]string{
		{"default_claude_account", "team-b"},
		{"stages.review.claude_account", "team-b"},
	} {
		if _, _, err := execute(t, "settings", "set", kv[0], kv[1], "--layer", "local", "--yes"); err != nil {
			t.Fatalf("settings set %s: %v", kv[0], err)
		}
	}
	for _, key := range []string{"default_claude_account", "stages.*.claude_account", "claude_account_auto"} {
		out, _, err := execute(t, "settings", "get", key)
		if err != nil {
			t.Errorf("settings get %s: %v", key, err)
			continue
		}
		if !strings.Contains(out, key) {
			t.Errorf("settings get %s does not print its own row:\n%s", key, out)
		}
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, localPath)))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if lc.DefaultClaudeAccount != "team-b" {
		t.Errorf("default_claude_account = %q, want team-b", lc.DefaultClaudeAccount)
	}
	if got := lc.ClaudeAccountStages["review"].ClaudeAccount; got != "team-b" {
		t.Errorf("stages.review.claude_account = %q, want team-b", got)
	}

	// The local config is the only document that may name an account, so a
	// write at any other layer is refused before the file is read.
	written := readFileString(t, localPath)
	for _, tc := range []struct{ key, value, layer string }{
		{"default_claude_account", "team-b", "repo"},
		{"stages.review.claude_account", "team-b", "project"},
		{"claude_account_auto", "true", "repo"},
	} {
		_, _, err := execute(t, "settings", "set", tc.key, tc.value, "--layer", tc.layer, "--yes")
		if err == nil {
			t.Fatalf("settings set %s --layer %s: local config is not the only document that may name an account", tc.key, tc.layer)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%s at --layer %s: exit %d, want a user error: %v", tc.key, tc.layer, ExitCode(err), err)
		}
		if !strings.Contains(err.Error(), "local") {
			t.Errorf("%s at --layer %s: the refusal does not name the layer it may be set in: %v", tc.key, tc.layer, err)
		}
	}
	if readFileString(t, localPath) != written {
		t.Error("a write at the wrong layer changed the local config")
	}

	// A value the key cannot hold is refused by the local config's own loader,
	// before the write: a name a profile may reference is what these keys take.
	for _, tc := range []struct{ key, value string }{
		{"default_claude_account", "Team-B"},
		{"stages.review.claude_account", "bitbucket-token"},
	} {
		_, _, err := execute(t, "settings", "set", tc.key, tc.value, "--layer", "local", "--yes")
		if err == nil {
			t.Fatalf("settings set %s %q was accepted", tc.key, tc.value)
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s: the refusal does not name the key it refused: %v", tc.key, err)
		}
	}
	if readFileString(t, localPath) != written {
		t.Error("a refused value changed the local config")
	}

	// Off is the default and the ordinary write; on is the sensitive
	// direction, and a session with no terminal writes nothing rather than
	// taking --yes for the owner's own hand.
	_, _, err = execute(t, "settings", "set", "claude_account_auto", "true", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("turning claude_account_auto on was answered by --yes")
	}
	if !strings.Contains(err.Error(), "needs a real terminal") {
		t.Errorf("the refusal does not say what it needs: %v", err)
	}
	if readFileString(t, localPath) != written {
		t.Error("a refused claude_account_auto write changed the local config")
	}
	if _, _, err := execute(t, "settings", "set", "claude_account_auto", "false", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("settings set claude_account_auto false: %v", err)
	}
	if lc, err := localcfg.Parse([]byte(readFileString(t, localPath))); err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	} else if lc.ClaudeAccountAuto {
		t.Error("claude_account_auto was written true by a write that asked for false")
	}
	if readFileString(t, localPath) == before {
		t.Error("no write reached the local config at all")
	}
}

// TestAccountsAddPrintsNoValueShape: the command carries no flag that could
// hold a token — in any spelling — and no --json, and nothing it prints is a
// credential's value or a secret's name. The machine-readable surface stays
// fugaro accounts ls --json (spec §2.1, §2.6).
func TestAccountsAddPrintsNoValueShape(t *testing.T) {
	accountsAddFixture(t, accountsAddLocal)
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"accounts", "add"})
	if err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	for _, banned := range []string{"json", "token", "secret", "value", "token-value", "token-stdin"} {
		if cmd.Flags().Lookup(banned) != nil {
			t.Errorf("--%s is registered: this command takes no value and has no --json (spec §2.1)", banned)
		}
	}

	out, errOut, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	if err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	printed := out + errOut
	for _, forbidden := range []string{config.ClaudeOAuthToken, "sk-ant"} {
		if strings.Contains(printed, forbidden) {
			t.Errorf("the output carries %q, and this command stores no secret at all:\n%s", forbidden, printed)
		}
	}
}
