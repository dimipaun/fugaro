package projectpolicy

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// The account-choice members of the node (docs/accounts-add/spec.md §11.1):
// the project's default account and the account a stage uses instead. They are
// additive — a reader that predates them lists them in UnknownKeys and reads
// the rest of the node — and the node carries no agent.auth, so the shape rules
// are all this reader can hold them to. Which stage may take an account is
// config.CheckStageAccountMix, applied where the run's auth is known.

// TestDecodeReadsTheAccountChoiceMembers is the members' own shape, and the
// absent state beside it: a project that names neither publishes neither, and
// the node of such a project is what it was before the keys existed.
func TestDecodeReadsTheAccountChoiceMembers(t *testing.T) {
	p, err := decodeNode(t, `{"version": 4, "default_claude_account": "acme-overflow",`+
		`"claude_account_stages": {"review": "acme-overflow", "adversarial": "acme-personal"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultClaudeAccount != "acme-overflow" {
		t.Errorf("DefaultClaudeAccount = %q, want acme-overflow", p.DefaultClaudeAccount)
	}
	if len(p.ClaudeAccountStages) != 2 || p.ClaudeAccountStages["review"] != "acme-overflow" || p.ClaudeAccountStages["adversarial"] != "acme-personal" {
		t.Errorf("ClaudeAccountStages = %v", p.ClaudeAccountStages)
	}
	// The default account is a legal value here for the reason it is a legal
	// key of the accounts block: it is the unsuffixed secret, not a name
	// claude_accounts reserves.
	if p, err := decodeNode(t, `{"version": 4, "default_claude_account": "default"}`); err != nil || p.DefaultClaudeAccount != "default" {
		t.Errorf("default_claude_account: default = (%v, %v)", p, err)
	}
	p, err = decodeNode(t, `{"version": 4}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultClaudeAccount != "" || p.ClaudeAccountStages != nil {
		t.Errorf("an absent member = %q, %v, want the zero values", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}
	// A written empty entry names nothing, the way an absent member does, and a
	// member left with no entry is normalized to nil: the publisher writes
	// neither, so a node that carries one is a node the reader reads as if it
	// did not.
	p, err = decodeNode(t, `{"version": 4, "claude_account_stages": {"review": ""}}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.ClaudeAccountStages != nil {
		t.Errorf("ClaudeAccountStages = %v, want nil for a member whose only entry names nothing", p.ClaudeAccountStages)
	}
}

// TestDecodeRefusesBadAccountChoiceMembers: the segment's vocabulary
// (config.RunStages) and the account name's shape (config.ValidClaudeAccountRef)
// are the two rules this reader can apply, and both are the rules the local
// config and the publisher apply, so a node can never carry a member the
// config that published it would have refused.
func TestDecodeRefusesBadAccountChoiceMembers(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"a segment that names no stage": {
			`{"version": 4, "claude_account_stages": {"bootstrap": "acme-overflow"}}`,
			"claude_account_stages.bootstrap: \"bootstrap\" is not a stage of a run"},
		"a stage the tree does not have yet": {
			`{"version": 4, "claude_account_stages": {"digest": "acme-overflow"}}`,
			"claude_account_stages.digest"},
		"an account name of the wrong shape": {
			`{"version": 4, "default_claude_account": "Acme-Overflow"}`,
			"default_claude_account: account name must match"},
		"a reserved account name": {
			`{"version": 4, "claude_account_stages": {"review": "bitbucket-token"}}`,
			"claude_account_stages.review: this name is reserved"},
		"an over-long account name": {
			`{"version": 4, "claude_account_stages": {"review": "` + strings.Repeat("a", 32) + `"}}`,
			"claude_account_stages.review: account name must be at most 31 characters"},
		"a member that is not a string": {
			`{"version": 4, "default_claude_account": 7}`,
			"default_claude_account"},
		"a stage map that is not an object": {
			`{"version": 4, "claude_account_stages": ["review"]}`,
			"claude_account_stages"},
		"a stage named twice": {
			`{"version": 4, "claude_account_stages": {"review": "acme-overflow", "review": "acme-personal"}}`,
			"claude_account_stages names \"review\" twice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, found, err := Decode([]byte(tc.raw))
			if err == nil || !found {
				t.Fatalf("Decode(%s) = (%v, %v), want a refusal", tc.raw, found, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}

// TestDecodeRefusesTheProjectsAnthropicKeyAsAStageAccount: the account-choice
// members name the account a run's stages choose, and the sentinel the account
// axis spells for the project's own Anthropic API key is not one.
// config.ValidClaudeAccountRef admits the value, because a profile's
// claude_accounts entry may name it, so without a case of its own here a node
// published a default or a stage account no job mounts.
//
// What the test pins is the remedy: the refusal names the move that works
// (dropping the key) and no claude_accounts entry, which is the one
// config.ValidateClaudeAccounts refuses.
//
// Mutation (run, restore): drop either sentinel arm in Decode — the member is
// accepted and the subtest goes red.
func TestDecodeRefusesTheProjectsAnthropicKeyAsAStageAccount(t *testing.T) {
	for name, raw := range map[string]string{
		"the project default": `{"version": 4, "default_claude_account": "` + config.AnthropicKeyAccount + `"}`,
		"a stage's own":       `{"version": 4, "claude_account_stages": {"review": "` + config.AnthropicKeyAccount + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, found, err := Decode([]byte(raw))
			if err == nil || !found {
				t.Fatalf("Decode(%s) = (%v, %v), want a refusal: no job mounts the project's own key as a Claude account", raw, found, err)
			}
			for _, banned := range []string{"claude_accounts", "allow_data_to"} {
				if strings.Contains(err.Error(), banned) {
					t.Errorf("the refusal names %q: %v — that is the entry config.ValidateClaudeAccounts refuses, so it is a remedy nobody can carry out", banned, err)
				}
			}
			if !strings.Contains(err.Error(), "drop the key") {
				t.Errorf("the refusal does not name the move that works (dropping the key): %v", err)
			}
		})
	}
}

// TestDecodeRefusesAMisspeltAccountChoiceMember: the top-level spelling rule,
// which is checkTopLevelNames': a modelled key spelled in another case is a
// second key nothing reads, and the value under it is then absent as far as
// every reader is concerned — the same silence a duplicated key has, reached by
// a spelling an operator would not expect to matter. It is the members' own
// case because they are read by looking the key up, never by decoding the node
// into a struct.
func TestDecodeRefusesAMisspeltAccountChoiceMember(t *testing.T) {
	for _, raw := range []string{
		`{"version": 4, "Default_Claude_Account": "acme-overflow"}`,
		`{"version": 4, "Claude_Account_Stages": {"review": "acme-overflow"}}`,
	} {
		if _, _, err := Decode([]byte(raw)); err == nil || !strings.Contains(err.Error(), "spells") {
			t.Errorf("Decode(%s) = %v, want the misspelt-key refusal", raw, err)
		}
	}
}

// TestDecodeToleratesTheAccountChoiceMembersUnderAnOlderReadersKeys is the
// additive promise (P8, and CLAUDE.md's field rule): a reader that predates
// these two members reads a node that carries them, lists them as unknown and
// reads every other key — so no release has to bump the node's version to add
// them, and an older CLI is not bricked by a newer node.
func TestDecodeToleratesTheAccountChoiceMembersUnderAnOlderReadersKeys(t *testing.T) {
	older := map[string]bool{}
	for k := range knownTopLevelKeys {
		if k != "default_claude_account" && k != "claude_account_stages" {
			older[k] = true
		}
	}
	p, found, err := decodeWithKeys([]byte(`{"version": 4, "default_claude_account": "acme-overflow",`+
		`"claude_account_stages": {"review": "acme-overflow"}, "base_branch": "main"}`), older)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode under an older reader's keys = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q: the rest of the node must still be read", p.BaseBranch)
	}
	if got := strings.Join(p.UnknownKeys, ","); got != "claude_account_stages,default_claude_account" {
		t.Errorf("UnknownKeys = %q, want the two members", got)
	}
}
