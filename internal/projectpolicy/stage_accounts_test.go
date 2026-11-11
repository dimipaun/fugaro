package projectpolicy

import (
	"slices"
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
	p, err := decodeNode(t, `{"version": 5, "default_claude_account": "acme-overflow",`+
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
	if p, err := decodeNode(t, `{"version": 5, "default_claude_account": "default"}`); err != nil || p.DefaultClaudeAccount != "default" {
		t.Errorf("default_claude_account: default = (%v, %v)", p, err)
	}
	p, err = decodeNode(t, `{"version": 5}`)
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
	p, err = decodeNode(t, `{"version": 5, "claude_account_stages": {"review": ""}}`)
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
			`{"version": 5, "claude_account_stages": {"bootstrap": "acme-overflow"}}`,
			"claude_account_stages.bootstrap: \"bootstrap\" is not a stage of a run"},
		"a stage the tree does not have yet": {
			`{"version": 5, "claude_account_stages": {"digest": "acme-overflow"}}`,
			"claude_account_stages.digest"},
		"an account name of the wrong shape": {
			`{"version": 5, "default_claude_account": "Acme-Overflow"}`,
			"default_claude_account: account name must match"},
		"a reserved account name": {
			`{"version": 5, "claude_account_stages": {"review": "bitbucket-token"}}`,
			"claude_account_stages.review: this name is reserved"},
		"an over-long account name": {
			`{"version": 5, "claude_account_stages": {"review": "` + strings.Repeat("a", 32) + `"}}`,
			"claude_account_stages.review: account name must be at most 31 characters"},
		"a member that is not a string": {
			`{"version": 5, "default_claude_account": 7}`,
			"default_claude_account"},
		"a stage map that is not an object": {
			`{"version": 5, "claude_account_stages": ["review"]}`,
			"claude_account_stages"},
		"a stage named twice": {
			`{"version": 5, "claude_account_stages": {"review": "acme-overflow", "review": "acme-personal"}}`,
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
// members name the account a run's stages choose, and neither of the account
// axis's non-account values is one — the sentinel this test is named after (the
// project's own Anthropic API key) and the provider key AK6a added beside it.
// config.ValidClaudeAccountRef admits both, because a profile's
// claude_accounts entry may name either, so without a case of its own here a
// node published a default or a stage account no job mounts.
//
// What the test pins is the remedy: the refusal names the move that works
// (dropping the key) and no claude_accounts entry, which is the one
// config.ValidateClaudeAccounts refuses.
//
// Both values are read in one loop because the rule is one rule
// (config.NonAccountChoiceMessage answers them together): a third value added
// to the axis must be refused by these members without anyone remembering to
// add a row.
//
// Mutation (run, restore): drop either arm in Decode — the member is accepted
// and the subtest goes red.
func TestDecodeRefusesTheProjectsAnthropicKeyAsAStageAccount(t *testing.T) {
	for _, value := range []string{config.AnthropicKeyAccount, config.ProviderKeyAccount} {
		for name, raw := range map[string]string{
			"the project default": `{"version": 5, "default_claude_account": "` + value + `"}`,
			"a stage's own":       `{"version": 5, "claude_account_stages": {"review": "` + value + `"}}`,
		} {
			t.Run(value+"/"+name, func(t *testing.T) {
				_, found, err := Decode([]byte(raw))
				if err == nil || !found {
					t.Fatalf("Decode(%s) = (%v, %v), want a refusal: no job mounts this value as a Claude account", raw, found, err)
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
		`{"version": 5, "Default_Claude_Account": "acme-overflow"}`,
		`{"version": 5, "Claude_Account_Stages": {"review": "acme-overflow"}}`,
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
	p, found, err := decodeWithKeys([]byte(`{"version": 5, "default_claude_account": "acme-overflow",`+
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

// TestInstallationAtThePreviousVersionReadsThisReleasesStageMember is the
// stage member's half of what TestInstallationAtThePreviousPolicyConverges
// pins for the triage member: an installation that has not rebuilt its images
// still reads the node this release publishes.
//
// The member's *names*, unlike its shape, are read against the vocabulary of
// the reader — each stage is checked with config.CheckStageSegment, which is
// the reader's own config.RunStages — and one name it does not know refuses the
// whole node, the runner's unreadablePolicy. A reader is a job image, and
// `fugaro update` rebuilds the images behind its own confirmation, so for as
// long as an operator takes to confirm an installation runs images built by the
// release behind (an in-flight run included). A node carrying a stage added
// since would therefore stop every run of the project, which is the one thing
// an update must never do (docs/design/update-in-flight.md).
//
// The reader of the release behind is modelled the way the rest of this
// package models one — the same Decode, met with that release's vocabulary —
// because the check reads config.RunStages rather than a table looked up by the
// key set: RunStages takes the released names for the length of the test and
// the real decoder then runs the real rule. What the test pins is that the
// names the publisher may publish are exactly the ones such a reader accepts
// (config.AccountStagesAReleasedImageKnows), and that an entry outside them —
// the triage one this release's publisher holds back — is what would refuse the
// node.
func TestInstallationAtThePreviousVersionReadsThisReleasesStageMember(t *testing.T) {
	released := []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}
	if !slices.Equal(config.AccountStagesAReleasedImageKnows, released) {
		t.Fatalf("AccountStagesAReleasedImageKnows = %v, want %v: this test reads the member as the release behind does, and the two have to name one vocabulary",
			config.AccountStagesAReleasedImageKnows, released)
	}
	// The name held back is one this release does run: a vocabulary that lacked
	// it everywhere would make the assertions below pass without anything being
	// held back at all. Read before the vocabulary is narrowed for exactly that
	// reason — IsRunStage answers for the run as it is, not as this test sets it.
	if !config.IsRunStage("triage") {
		t.Fatal("triage is not a stage of this run: nothing needs holding back, and the assertions below hold vacuously")
	}
	defer func(old []string) { config.RunStages = old }(config.RunStages)
	config.RunStages = slices.Clone(released)

	// The node this release publishes for a project whose only stage account is
	// the senior review's: every name in it is one the vocabulary above carries,
	// so it decodes and reads as it does on this release.
	p, err := decodeNode(t, `{"version": 4, "claude_account_stages": {"review": "acme-overflow"}}`)
	if err != nil {
		t.Fatalf("the release behind refused this release's node: %v", err)
	}
	if p.ClaudeAccountStages["review"] != "acme-overflow" {
		t.Errorf("ClaudeAccountStages = %v: the member has to read the same on both sides", p.ClaudeAccountStages)
	}
	// The same node with the entry the publisher holds back: this is the
	// refusal an installation meets after `fugaro update` if the publisher ever
	// writes it, and it names the stage, which is what the finding reproduced.
	if _, _, err := Decode([]byte(`{"version": 4, "claude_account_stages": {"review": "acme-overflow", "triage": "acme-overflow"}}`)); err == nil ||
		!strings.Contains(err.Error(), `"triage" is not a stage of a run`) {
		t.Fatalf("the release behind read a node naming triage: %v — that is the failure config.AccountStageHeldBack exists to keep out of the node", err)
	}
}
