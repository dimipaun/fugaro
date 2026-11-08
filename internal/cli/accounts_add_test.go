package cli

import (
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
runs_bucket: fugaro-runs-proj-1234
region: us-east5
gcp_project: proj-1234

claude_accounts:
  # the account the 2026 migration left behind
  legacy:
    allow_data_to:
      - acme/app

profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/acme/app

repos:
  acme/app: { provider: github, github_app_id: "12345", workflows: [web] }
`

// accountsAddLocalTwoRepos is the same file with the profile admitting a
// second repository, which a new account does not cover unless --repos names
// it too (the subset rule, spec §2.2 step 2).
const accountsAddLocalTwoRepos = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234

profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/acme/app
      - github/acme/lib

repos:
  acme/app: { provider: github, github_app_id: "12345", workflows: [web] }
`

// accountsAddLocalExistingAccount already declares the account the command is
// asked for, so re-declaring it is a rotation (spec §2.2 step 1) and its
// allow_data_to grows instead of being written from --repos alone.
const accountsAddLocalExistingAccount = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234

claude_accounts:
  legacy:
    allow_data_to:
      - acme/app

profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/acme/app
    claude_accounts:
      - legacy

repos:
  acme/app: { provider: github, github_app_id: "12345", workflows: [web] }
`

// accountsAddFixture writes this machine's project config with local and
// selects it, returning the file's path.
//
// The command converges on the file it wrote, so the fixture is the world that
// converge needs (newAddRigWith): the local config the test is about, plus the
// endpoints, the budget backend and the runs bucket of the rig's fakes. A
// fixture that stopped at the file would have these tests asking a real Cloud
// Run about a project that does not exist, which is why the converge half's own
// fixture is what they are built on.
func accountsAddFixture(t *testing.T, local string) string {
	t.Helper()
	return newAddRigWith(t, local).cfg
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
// keys are edited in place, and nothing else in the file — the comments, the
// order the owner wrote the keys in, the claude_accounts entry added by hand —
// is touched. An implementation that marshals localcfg.Config fails here and
// nowhere else.
//
// The evidence is the diff the command prints for its own write, and not the
// file when the command returns. The command's second half converges the
// installation (A2), and the stages it invokes write the local config the way
// `fugaro update` does — the engine's own marshal of the resolved config, on
// top of the file this edit wrote. That write is the stage's and not this
// edit's, so the edit is read where it is still the file's last writer: the
// diff of the write this command makes, which is printed before the stages run.
//
// A re-marshal in *this* write shows up as removals: yaml.Marshal drops the
// comments, sorts the keys the owner ordered and rewrites the entry added by
// hand, so every one of those lines comes back as a "  - " line. An edit that
// goes through internal/yamledit adds the keys it was asked for and removes
// nothing at all.
func TestAccountsAddKeepsCommentsAndOrder(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	if err != nil {
		t.Fatalf("accounts add: %v\n%s", err, out)
	}

	// The edit's diff is what this command prints up to its confirmation block;
	// everything the stages print is on stderr, under the run's own writer.
	edit := out
	if i := strings.Index(edit, "⚠ CONFIRM"); i >= 0 {
		edit = edit[:i]
	}
	lines := strings.Split(strings.TrimRight(edit, "\n"), "\n")
	if len(lines) == 0 || lines[0] != path {
		t.Fatalf("the command did not print the diff of its own write to %s:\n%s", path, out)
	}
	var added, removed []string
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "  + "):
			added = append(added, strings.TrimPrefix(line, "  + "))
		case strings.HasPrefix(line, "  - "):
			removed = append(removed, strings.TrimPrefix(line, "  - "))
		default:
			t.Errorf("the edit's diff carries %q, which is neither an addition nor a removal", line)
		}
	}
	if len(removed) != 0 {
		t.Errorf("the edit removed %d line(s) from the file, and it edits two keys in place: a removal is a line the edit was not asked for — a comment, the key order the owner wrote, the account added by hand — which is what a re-marshal of localcfg.Config prints:\n%s\nremoved: %q",
			len(removed), out, removed)
	}
	// The keys the edit was asked for, in the file's own spelling: the
	// claude_accounts block keeps the two-space indent it was written with, and
	// the profile's list is the profile's own.
	if want := []string{
		"  team-a:",
		"    allow_data_to:",
		"      - acme/app",
		"    claude_accounts:",
		"      - team-a",
	}; !slices.Equal(added, want) {
		t.Errorf("the edit added %q, want exactly the two keys it was asked for: %q\n%s", added, want, out)
	}

	// The file the command leaves reads back strictly, and carries the account:
	// whatever the stages wrote over it, this edit's key is in the config every
	// command of the owner's reads.
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want [acme/app]", got)
	}
	if got := lc.ClaudeAccounts["legacy"].AllowDataTo; !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("the account added by hand was lost: claude_accounts.legacy.allow_data_to = %v", got)
	}
}

// TestAccountsAddWritesTheDeclarationAndTheProfile: the two keys the command
// owns are written, and nothing else is. The account's allow_data_to is the
// repositories --repos names; the profile's claude_accounts gains the name;
// the profile's own allow_repos and every other account are left alone.
func TestAccountsAddWritesTheDeclarationAndTheProfile(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app,acme/lib", "--profile", "standard", "--yes")
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
	if got := lc.Profiles["standard"].AllowRepos; !slices.Equal(got, []string{"github/acme/app"}) {
		t.Errorf("profiles.standard.allow_repos = %v, want the owner's [github/acme/app]", got)
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
		out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
		if err != nil {
			t.Fatalf("accounts add: %v", err)
		}
		for _, want := range []string{
			"claude_accounts.team-a.allow_data_to",
			"profiles.standard.claude_accounts",
			"; a launcher that may start a run on acme/app can have that run print team-a's token",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the confirmation does not print %q:\n%s", want, out)
			}
		}
	})
	t.Run("this checkout only", func(t *testing.T) {
		accountsAddCheckout(t, "acme/app")
		accountsAddFixture(t, accountsAddLocal)
		out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--yes")
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

// TestAccountsAddConfirmsTheRotationItMakes: re-declaring an account the file
// already has is a rotation (spec §2.2 step 1), so the confirmation says so,
// and it names the list the account ends up with — the one it already has plus
// --repos, which is what the write leaves — rather than the flag alone
// (spec §2.6).
func TestAccountsAddConfirmsTheRotationItMakes(t *testing.T) {
	accountsAddCheckout(t, "acme/app")
	path := accountsAddFixture(t, accountsAddLocalExistingAccount)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "legacy", "--repos", "acme/other", "--yes")
	if err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	if !strings.Contains(out, "claude_accounts.legacy.allow_data_to = [acme/app, acme/other]") {
		t.Errorf("the confirmation does not name the list the account ends up with:\n%s", out)
	}
	if !strings.Contains(out, "rotation") {
		t.Errorf("the confirmation does not say the account already exists:\n%s", out)
	}
	if after := readFileString(t, path); !strings.Contains(after, "- acme/other") {
		t.Errorf("the account's allow_data_to did not grow, so the confirmation and the write disagree:\n%s", after)
	}
}

// TestSettingsWritesTheAccountChoiceKeys is D873's three keys on the generic
// write path (spec §11.1): each resolves, each is the local config's alone, a
// layer or a value a key refuses leaves the file byte-identical, and turning
// `claude_account_auto` on is the sensitive direction — a coding agent's
// session cannot make it, and the owner's own terminal is asked nothing
// (D846, D875, D1552, D1555).
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
	// direction, whose fence is the coding agent's session rather than a
	// terminal (D1552, D1555): nothing is written there, and the owner's own
	// session is asked nothing for it.
	agentSession(t)
	_, _, err = execute(t, "settings", "set", "claude_account_auto", "true", "--layer", "local")
	if err == nil {
		t.Fatal("turning claude_account_auto on was written in an agent session")
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal does not say what refused it: %v", err)
	}
	if readFileString(t, localPath) != written {
		t.Error("a refused claude_account_auto write changed the local config")
	}
	noAgentSession(t)
	noTerminal(t)
	if _, _, err := execute(t, "settings", "set", "claude_account_auto", "true", "--layer", "local"); err != nil {
		t.Fatalf("turning claude_account_auto on asked for an answer: %v", err)
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

// TestSettingsRefusesAPerStageAccountTheProjectDoesNotAllow is D873's
// per-stage override held to the floor every rung of the chain stands on: an
// account a run may use is one this file's claude_accounts defines, so a
// per-stage key naming anything else is refused — before any write, with the
// file byte-identical and no backup taken. A key accepted here would name an
// account no run can use, and the stage would silently keep the account the
// key was meant to replace.
func TestSettingsRefusesAPerStageAccountTheProjectDoesNotAllow(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "\nclaude_accounts:\n  team-b:\n    allow_data_to: [acme/app]\n")
	before := readFileString(t, localPath)

	for _, tc := range []struct{ key, value string }{
		{"stages.review.claude_account", "team-z"},
		{"default_claude_account", "team-z"},
	} {
		_, _, err := execute(t, "settings", "set", tc.key, tc.value, "--layer", "local", "--yes")
		if err == nil {
			t.Fatalf("settings set %s %s was accepted, and no run can use that account", tc.key, tc.value)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%s: exit %d, want a user error: %v", tc.key, ExitCode(err), err)
		}
		for _, want := range []string{tc.key, "is not defined here"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not say %q: %v", tc.key, want, err)
			}
		}
	}
	if after := readFileString(t, localPath); after != before {
		t.Errorf("a refused account choice wrote the file:\n%s", after)
	}
	backups, err := filepath.Glob(localPath + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Errorf("a refused account choice backed the file up: %v", backups)
	}

	// The same key with an account the file does define is written, so the
	// refusal above is the account's and not the key's.
	if _, _, err := execute(t, "settings", "set", "stages.review.claude_account", "team-b", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("settings set stages.review.claude_account team-b: %v", err)
	}
	if readFileString(t, localPath) == before {
		t.Error("a per-stage override naming a defined account was not written")
	}
}

// TestAccountsAddPrintsNoValueShape: the command carries no flag that could
// hold a token — in any spelling — and no --json, and the value it reads at the
// hidden prompt and stores project-wide reaches neither stream. The machine-
// readable surface stays fugaro accounts ls --json (spec §2.1, §2.6).
//
// The secret the command stores is named and the value is not, which is the
// half the confirmation of the converge owes the person reading it: A1's own
// command stored nothing, so it forbade the secret's name outright, and the
// secret is this command's own since A2 (spec §2.6: the block says which secret
// gets a new version). What may never be printed is the value, in any spelling.
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

	out, errOut, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	if err != nil {
		t.Fatalf("accounts add: %v", err)
	}
	printed := out + errOut
	for _, forbidden := range []string{addToken(), "sk-ant"} {
		if strings.Contains(printed, forbidden) {
			t.Errorf("the output carries %q, and the value this command stores is never printed:\n%s", forbidden, printed)
		}
	}
	if secret := config.ClaudeAccountSecret("team-a"); !strings.Contains(out, secret) {
		t.Errorf("the command stores a secret project-wide and never names it, so the person who confirmed it cannot see where it went (want %s):\n%s", secret, out)
	}
}
