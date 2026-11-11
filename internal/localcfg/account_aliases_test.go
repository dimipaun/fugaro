package localcfg

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// The account-choice keys' alias half (docs/account-aliases, D2090–D2093). The
// ruling says a stage's claude_account may name an alias, repointed by one
// command, and D2091 widened that to the project's default_claude_account as
// well, because under oauth it is the default and not a stage key that moves a
// run. AL0 ships the vocabulary alone: the loader admits the alias, the rules
// that hold it are applied here, and the keys keep the alias as written — the
// launch is what resolves it, so these tests pin that the loader does not.
const (
	aliasAccounts = "claude_accounts:\n    team-a:\n        allow_data_to:\n            - acme/web\n    team-b:\n        allow_data_to:\n            - acme/web\n"
	aliasBlock    = "claude_account_aliases:\n    reviewer: team-a\n"
)

// TestChoiceKeysMayNameAnAlias is the ruling's own case: a stage's
// claude_account and the project's default_claude_account hold an alias, the
// loader admits both, and this file resolves the name on the one step a reader
// takes — before any allow-list is asked, which is why the resolved account and
// the alias it came through are returned together.
func TestChoiceKeysMayNameAnAlias(t *testing.T) {
	c, err := Parse([]byte(sample + aliasAccounts + aliasBlock + "default_claude_account: reviewer\nstages:\n    review:\n        claude_account: reviewer\n"))
	if err != nil {
		t.Fatalf("a config whose choice keys name an alias was refused: %v", err)
	}
	// The keys keep the alias as written. Resolution is the reader's step and
	// not the loader's, so a repoint moves every key at once and no key is
	// rewritten.
	if c.DefaultClaudeAccount != "reviewer" {
		t.Errorf("default_claude_account = %q, want the alias as written", c.DefaultClaudeAccount)
	}
	if got := c.ClaudeAccountStages["review"].ClaudeAccount; got != "reviewer" {
		t.Errorf("stages.review.claude_account = %q, want the alias as written", got)
	}
	for _, tc := range []struct{ name, account, alias string }{
		{"reviewer", "team-a", "reviewer"},
		{"team-b", "team-b", ""},
		{config.DefaultClaudeAccount, config.DefaultClaudeAccount, ""},
	} {
		account, alias := c.ResolveAccount(tc.name)
		if account != tc.account || alias != tc.alias {
			t.Errorf("ResolveAccount(%q) = %q, %q, want %q, %q", tc.name, account, alias, tc.account, tc.alias)
		}
	}
	// A repoint is a write to this block and nothing else: the stage key still
	// names the alias, and now resolves to the account it points at.
	repointed, err := Parse([]byte(sample + aliasAccounts + strings.Replace(aliasBlock, "team-a", "team-b", 1) + "stages:\n    review:\n        claude_account: reviewer\n"))
	if err != nil {
		t.Fatal(err)
	}
	if account, alias := repointed.ResolveAccount(repointed.ClaudeAccountStages["review"].ClaudeAccount); account != "team-b" || alias != "reviewer" {
		t.Errorf("after a repoint the stage key resolves to %q, %q, want team-b through reviewer", account, alias)
	}
	// The block's own rules are the loader's, so a block they refuse never
	// reaches a launch, a publish or a job set.
	for _, tc := range []struct{ name, yaml, want string }{
		{"a target nothing defines", "claude_account_aliases:\n    reviewer: team-z\n", "is not defined here"},
		{"a chain", "claude_account_aliases:\n    reviewer: other\n    other: team-a\n", "never for another alias"},
		{"a name an account holds", "claude_account_aliases:\n    team-a: team-b\n", "is taken by a claude_accounts entry"},
		{"an axis value that is no account", "claude_account_aliases:\n    reviewer: " + config.AnthropicKeyAccount + "\n", "names no Claude account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(sample + aliasAccounts + tc.yaml))
			if err == nil {
				t.Fatalf("a claude_account_aliases block the rules refuse was accepted")
			}
			for _, want := range []string{tc.want, "claude_account_aliases."} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestSharedDropsAliases: the alias block is the choice keys' own vocabulary
// and local-only for their reason (D2090) — an alias in a published file would
// be a way around an account's own allow_data_to, one indirection in, because
// whoever can write the runs bucket could then point a key at an account the
// file never admitted. The publish writes the resolved account instead, and
// MergeShared never takes an alias back from a published object: what Shared
// drops is not merged from published either.
func TestSharedDropsAliases(t *testing.T) {
	lc := sharedBase(t, aliasAccounts+aliasBlock)
	s := lc.Shared()
	if s.ClaudeAccountAliases != nil {
		t.Errorf("the published config carries aliases: %v", s.ClaudeAccountAliases)
	}
	if len(lc.ClaudeAccountAliases) != 1 {
		t.Errorf("Shared cleared the receiver's own aliases: %v", lc.ClaudeAccountAliases)
	}
	m := MergeShared(sharedBase(t, aliasAccounts+strings.Replace(aliasBlock, "team-a", "team-b", 1)), lc)
	if m.ClaudeAccountAliases != nil {
		t.Errorf("a published alias was merged back in: %v", m.ClaudeAccountAliases)
	}
}

// TestLocalConfigWithoutAliasesRoundTrips is CLAUDE.md's HARD RULE at the
// block's own scale: the block is new and optional, and an installation that
// writes no alias is left byte for byte as it is. `fugaro update` writes this
// file through the one settings path, so a field that marshalled an empty block
// would rewrite every operator's config for a feature they do not use. The
// fixture is a file of the shape this release writes around the block, and the
// block's own turn comes after: an installation that has aliases converges too.
func TestLocalConfigWithoutAliasesRoundTrips(t *testing.T) {
	// The file such an installation already holds, as this release writes it:
	// the two accounts the choice keys name, and no alias block. The second
	// fixture is the same file with the block written where the writer puts it,
	// which is where the loader reads it back from too.
	const before = `version: 2
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
build:
    machine_type: E2_HIGHCPU_8
claude_accounts:
    team-a:
        allow_data_to:
            - acme/web
    team-b:
        allow_data_to:
            - acme/web
default_claude_account: team-a
stages:
    review:
        claude_account: team-a
max_parallel: 20
repos: {}
`
	if strings.Contains(before, "claude_account_aliases") {
		t.Fatal("the fixture writes an alias block, so it cannot pin that a file without one is left alone")
	}
	after := strings.Replace(before, "default_claude_account:", aliasBlock+"default_claude_account:", 1)
	for _, tc := range []struct{ name, want string }{
		{"an installation with no aliases", before},
		{"an installation with one", after},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(tc.want))
			if err != nil {
				t.Fatal(err)
			}
			data, err := c.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Errorf("the file is not what it was:\n--- got\n%s\n--- want\n%s", data, tc.want)
			}
			if tc.name == "an installation with no aliases" && strings.Contains(string(data), "claude_account_aliases") {
				t.Errorf("a config that writes no alias marshalled one:\n%s", data)
			}
		})
	}
}
