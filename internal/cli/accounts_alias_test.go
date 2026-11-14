package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is `fugaro accounts alias`: the one command that repoints the keys
// naming an account, and the two readers that had to learn about aliases with it
// (docs/account-aliases/spec.md §6, §7; D2096, D2099, D2101).

// aliasFixture is a budget fixture's local config with the block the whole file
// is about: two accounts one repository may use, one alias pointed at the first,
// and the choice keys naming the alias rather than the account. extra is the
// caller's own top-level YAML, for the fixtures that need a pool or a credential
// beside the block.
func aliasFixture(t *testing.T, extra string) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	f.appendConfig(t, `claude_accounts:
  team-a: { allow_data_to: ["acme/app"] }
  team-b: { allow_data_to: ["acme/app"] }
claude_account_aliases:
  reviewer: team-a
default_claude_account: reviewer
stages:
  review: { claude_account: reviewer }
`+extra)
	return f
}

// aliasConfig is the local config's bytes as the file holds them: every refusal
// below is asserted against the file itself, so a refusal that wrote anything
// is red wherever a command printed nothing.
func aliasConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// aliasRigLocal is the converge rig's local config: the installation rig's own
// shape (accounts, a profile serving both of them, one GitHub repository) with
// the alias block and the choice keys naming the alias. The alias is what this
// command repoints, and the keys are what has to move with it.
const aliasRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
claude_accounts:
  team-a: { allow_data_to: [owner/repo-a] }
  team-b: { allow_data_to: [owner/repo-a] }
claude_account_aliases:
  reviewer: team-a
default_claude_account: reviewer
stages:
  review: { claude_account: reviewer }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts: [team-a, team-b]
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// TestAccountsAliasSetWritesAndConverges is the command end to end: one edit of
// the local config, and the three stages `fugaro update` runs, in-process.
//
// The published node is seeded with the post-repoint members before the run,
// because the repoint IS a widening of the account a stage goes out on: a node
// that names the account the keys now resolve to is the state the publish has
// nothing to widen, and a fixture that left the node naming the old account
// would be answered by the typed confirmation a headless run cannot give — which
// is the typed form's own test, not this one (policyAccountWidening).
//
// What the assertions are about is F4: the alias never reaches the node. The
// command writes the local config, and every reader of the node — the launch,
// the job set, the run record — holds the account and never the alias.
func TestAccountsAliasSetWritesAndConverges(t *testing.T) {
	r := newAddRigWith(t, aliasRigLocal)
	node := addPolicyNode()
	node["default_claude_account"] = "team-b"
	node["claude_account_stages"] = map[string]any{"review": "team-b"}
	r.db.Set(projectpolicy.PathShared(), node)

	out, _, err := execute(t, "accounts", "alias", "set", "reviewer", "team-b", "--yes")
	if err != nil {
		t.Fatalf("accounts alias set: %v\nstdout:\n%s", err, out)
	}
	lc := r.addLocal(t)
	if got := lc.ClaudeAccountAliases["reviewer"]; got != "team-b" {
		t.Fatalf("claude_account_aliases.reviewer = %q, want team-b — the file the command wrote is what every reader resolves\n%s", got, out)
	}
	// The keys are the alias and stay the alias: this command moves the block's
	// entry and nothing else, which is the whole point of the indirection.
	if lc.DefaultClaudeAccount != "reviewer" || lc.ClaudeAccountStages["review"].ClaudeAccount != "reviewer" {
		t.Fatalf("default_claude_account = %q and stages.review.claude_account = %q, want both reviewer: the keys name the alias, and only the alias's target moves",
			lc.DefaultClaudeAccount, lc.ClaudeAccountStages["review"].ClaudeAccount)
	}
	p := r.policy(t)
	if p.DefaultClaudeAccount != "team-b" {
		t.Errorf("the node's default_claude_account = %q, want team-b: the publish resolves the alias before it writes a member (F4)", p.DefaultClaudeAccount)
	}
	if got := p.ClaudeAccountStages["review"]; got != "team-b" {
		t.Errorf("the node's claude_account_stages.review = %q, want team-b", got)
	}
	for member, value := range policyAccountValues(p) {
		if value == "reviewer" {
			t.Errorf("the node's %s names the alias itself: no job image in the field may meet an alias by its own name (F4)", member)
		}
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the run does not close on the one thing left to do, which is nothing:\n%s", out)
	}
	if !strings.Contains(out, "reviewer") {
		t.Errorf("the block does not name the alias it repointed:\n%s", out)
	}
}

// TestAccountsAliasSetRefusedInAgentSession: the command moves which credential
// this project's stages spend, so it is the owner's own step at their own
// terminal and a coding agent's session is refused whatever --yes says (D2097,
// the no-prompt policy D1540). The refusal comes before anything is read, so
// the file is byte-identical afterwards.
func TestAccountsAliasSetRefusedInAgentSession(t *testing.T) {
	aliasFixture(t, "")
	t.Setenv(agentMarkers[0], "1")
	before := aliasConfig(t)

	out, errOut, err := execute(t, "accounts", "alias", "set", "reviewer", "team-b", "--yes")
	if err == nil {
		t.Fatalf("the repoint was admitted in a coding agent's session:\n%s\n%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal does not name the session it refuses: %v", err)
	}
	if after := aliasConfig(t); after != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", after)
	}
}

// TestAccountsAliasRefusesChainPoolAndUnknownTarget: the target rule is
// config.ValidateAccountAliases', asked of the file this command would write
// (D2092, D2093). Each refusal is checked to leave the file alone, because the
// one thing a validation that ran after the write could do is leave a block the
// loader then refuses.
func TestAccountsAliasRefusesChainPoolAndUnknownTarget(t *testing.T) {
	aliasFixture(t, `credentials:
  - name: team-cred
    type: claude-oauth
pools:
  - name: reviewer-pool
    strategy: failover
    members: [team-cred]
`)
	before := aliasConfig(t)
	for _, tc := range []struct{ name, alias, target, want string }{
		{"chain", "second", "reviewer", "never for another alias"},
		{"pool", "second", "reviewer-pool", "this name is a pool's"},
		{"credential", "second", "team-cred", "this name is a credential's"},
		{"unknown", "second", "nope", "is not defined here"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := execute(t, "accounts", "alias", "set", tc.alias, tc.target, "--yes")
			if err == nil {
				t.Fatalf("accounts alias set %s %s was admitted:\n%s\n%s", tc.alias, tc.target, out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if after := aliasConfig(t); after != before {
				t.Errorf("the refusal wrote the file anyway:\n%s", after)
			}
		})
	}
}

// TestAccountsAliasRmRefusedWhileAKeyNamesIt: an alias a choice key still names
// is not removed — the key would resolve to a name this file no longer defines,
// which is the state the loader refuses. The refusal names the keys and the one
// edit that settles it (D2101's twin, spec §6).
func TestAccountsAliasRmRefusedWhileAKeyNamesIt(t *testing.T) {
	aliasFixture(t, "")
	before := aliasConfig(t)

	out, errOut, err := execute(t, "accounts", "alias", "rm", "reviewer", "--yes")
	if err == nil {
		t.Fatalf("the alias was removed while default_claude_account and stages.review.claude_account still name it:\n%s\n%s", out, errOut)
	}
	for _, want := range []string{"default_claude_account", "stages.review.claude_account", "fugaro accounts alias set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if after := aliasConfig(t); after != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", after)
	}
}

// TestAccountsRmRefusesAnAliasTarget: `accounts rm` removes a declaration, and
// an alias standing for it would be left pointing at an account nothing defines
// (D2101). The refusal names the alias and the command that moves it.
func TestAccountsRmRefusesAnAliasTarget(t *testing.T) {
	aliasFixture(t, "")
	before := aliasConfig(t)

	out, errOut, err := execute(t, "accounts", "rm", "team-a", "--yes")
	if err == nil {
		t.Fatalf("the target of an alias was removed:\n%s\n%s", out, errOut)
	}
	for _, want := range []string{"reviewer", "fugaro accounts alias set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if after := aliasConfig(t); after != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", after)
	}
}

// TestAccountsLsJSONListsAliasesAdditively: the read names each alias and its
// target, and --json gains one member and changes none (D2099, D848): a reader
// of an older release sees the document it always did.
func TestAccountsLsJSONListsAliasesAdditively(t *testing.T) {
	aliasFixture(t, "")
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	// The document is read as the WIRE document and not through the Go struct:
	// "additively" is a claim about the bytes a caller parses, and a test that
	// decoded into accountsListDoc would pass on a member the encoder dropped.
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc["repo"] != "acme/app" {
		t.Errorf("repo = %v, want acme/app", doc["repo"])
	}
	accounts, _ := doc["accounts"].([]any)
	if !slices.Contains(accounts, any("team-a")) || !slices.Contains(accounts, any("team-b")) {
		t.Errorf("accounts = %v, want the declared accounts beside the always-available default", accounts)
	}
	aliases, ok := doc["aliases"].(map[string]any)
	if !ok {
		t.Fatalf("the document carries no aliases member: %v", doc)
	}
	if got := aliases["reviewer"]; got != "team-a" {
		t.Errorf("aliases.reviewer = %v, want team-a (%v)", got, aliases)
	}
	// The read is names only: the alias's own member is the alias and its
	// target, and nothing here reads or writes a token.
	if len(aliases) != 1 {
		t.Errorf("aliases = %v, want exactly the one the file declares", aliases)
	}

	text, _, err := execute(t, "accounts", "ls", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "reviewer -> team-a") {
		t.Errorf("the text read does not name the alias and its target:\n%s", text)
	}
}

// TestSettingsRegistryHasTheAliasRow: the block the command writes is a key a
// reader can look up, and its owner is the family that writes it (D1178,
// D2096). The bare row is the map the file holds and the wildcard is one entry
// of it — `settings set` carries neither, so both name `fugaro accounts`.
func TestSettingsRegistryHasTheAliasRow(t *testing.T) {
	for _, key := range []string{"claude_account_aliases", "claude_account_aliases.*"} {
		r, ok := settings.Lookup(key)
		if !ok {
			t.Errorf("%s: no registry row, so the read cannot name a key the file holds", key)
			continue
		}
		if r.Owner != "accounts" {
			t.Errorf("%s: Owner = %q, want accounts: the command that writes it is fugaro accounts alias set/rm (D2096), and a laundered row is one the generic path then refuses in another command's name", key, r.Owner)
		}
		if !slices.Equal(r.Layers, []string{settings.LayerLocal}) {
			t.Errorf("%s: Layers = %v, want the local config alone: the block is never published (D2090)", key, r.Layers)
		}
		if r.Default.Unset == "" || r.Doc == "" {
			t.Errorf("%s: Default = %+v and Doc = %q, and a row a reader can act on names both", key, r.Default, r.Doc)
		}
		if r.Sensitive {
			t.Errorf("%s: marked sensitive, and an alias is a name rather than a secret", key)
		}
	}
}
