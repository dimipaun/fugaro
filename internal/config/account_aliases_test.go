package config

import (
	"strings"
	"testing"
)

// The alias rules are two decisions with one block behind them (docs/account-aliases):
// D2092 is the name — credentialNameProblem's rule, shared with a credential
// and a pool, plus the ONE namespace the alias shares with the accounts, the
// credentials and the pools — and D2093 is the target, which is one account of
// claude_accounts or "default" and nothing else.

// TestAliasNameCollisionsRefused: a name written in a choice key has to mean
// exactly one thing (D2092), and the four blocks a choice key's names come from
// — claude_accounts, credentials, pools and the aliases — are one namespace. So
// a name any of the other three holds is refused as an alias's, and the refusal
// names the block that holds it, which is the thing the operator has to look at.
//
// The alias's own name rule is credentialNameProblem's, not a second copy of
// it: an alias is a credential-choosing name like a pool's or a credential's,
// and the sentence each of them answers a reserved name with has to be the same
// sentence (D1621, D2092).
func TestAliasNameCollisionsRefused(t *testing.T) {
	accounts := map[string]ClaudeAccount{"team-a": {}}
	creds := []Credential{{Name: "openrouter-main", Type: CredentialTypeOpenRouter}}
	pools := []Pool{{Name: "claude-pool", Members: []string{"openrouter-main"}}}

	for _, tc := range []struct{ block, name string }{
		{"claude_accounts", "team-a"},
		{"credentials", "openrouter-main"},
		{"pools", "claude-pool"},
	} {
		t.Run(tc.block, func(t *testing.T) {
			ps := ValidateAccountAliases(map[string]string{tc.name: "team-a"}, accounts, creds, pools)
			if len(ps) != 1 {
				t.Fatalf("problems = %v, want one: a name %s already holds may not be an alias's", ps, tc.block)
			}
			if !strings.Contains(ps[0].Message, tc.block) {
				t.Errorf("the refusal does not name the block that holds the name (%s): %+v", tc.block, ps[0])
			}
			if ps[0].Path != "claude_account_aliases."+tc.name {
				t.Errorf("the refusal is at %q, want the alias's own entry", ps[0].Path)
			}
		})
	}

	// The name rule itself: the reserved names the choice keys cannot mean
	// either — "default", the built-in vertex, the account axis's non-account
	// values, the bundle's own reserved spelling — a name ending in the
	// review-only variant's suffix, the shape and the length cap.
	for _, name := range []string{
		"",
		"Team-A",
		DefaultClaudeAccount,
		CredentialVertex,
		AnthropicKeyAccount,
		ProviderKeyAccount,
		BitbucketSecret,
		"team-a" + ReviewVariantSuffix,
		strings.Repeat("a", MaxClaudeAccountNameLen+1),
	} {
		t.Run("a name no credential may carry: "+name, func(t *testing.T) {
			ps := ValidateAccountAliases(map[string]string{name: "team-a"}, accounts, creds, pools)
			if len(ps) != 1 {
				t.Fatalf("problems = %v, want one: %q is no alias's name either", ps, name)
			}
			if want := credentialNameProblem(name); ps[0].Message != want {
				t.Errorf("message = %q, want credentialNameProblem's %q: a name rule spelled twice is two rules", ps[0].Message, want)
			}
		})
	}

	// The control: an alias of a name no other block holds, pointing at a
	// defined account, is the block's whole reason.
	if ps := ValidateAccountAliases(map[string]string{"reviewer": "team-a"}, accounts, creds, pools); len(ps) != 0 {
		t.Errorf("problems = %v, want none: this is the shape the block exists for", ps)
	}
}

// TestAliasTargetRules: D2093 — an alias stands for one account defined in
// claude_accounts, or for "default" for the unsuffixed account, and for nothing
// else: never another alias (a chain would need a cycle check and a reader that
// prints a path), never a pool (its members are walked inside a run, which is
// the re-resolution of a repoint the ruling forbids), never a credential that
// is no account (its credential is served another way, and an alias names the
// token a job mounts), never one of the account axis's non-account values, and
// never a name nothing defines.
//
// The remedy is part of the rule here: the two axis values are refused in the
// same words the choice keys refuse them (NonAccountChoiceMessage's claim), and
// the refusal never prescribes a claude_accounts entry of one of them — that
// entry is the one ValidateClaudeAccounts refuses.
func TestAliasTargetRules(t *testing.T) {
	accounts := map[string]ClaudeAccount{"team-a": {}, "team-b": {}}
	creds := []Credential{
		// MC4 derives a claude-oauth credential per account, so this is the
		// ordinary shape of an account that is also a credential.
		{Name: "team-a", Type: CredentialTypeClaudeOAuth},
		{Name: "openrouter-main", Type: CredentialTypeOpenRouter},
		{Name: "claude-oauth-two", Type: CredentialTypeClaudeOAuth},
	}
	pools := []Pool{{Name: "claude-pool", Members: []string{"claude-oauth-two"}}}

	for _, tc := range []struct{ name, target, want string }{
		{"no target at all", "", "names none"},
		{"a chain", "reviewer-b", "never for another alias"},
		{"a pool", "claude-pool", "is a pool's"},
		{"a credential of another type", "openrouter-main", "a credential's (type openrouter) and no account's"},
		{"a credential that is no account", "claude-oauth-two", "a credential's (type claude-oauth) and no account's"},
		{"the project's Anthropic key", AnthropicKeyAccount, "names no Claude account"},
		{"the project's provider key", ProviderKeyAccount, "names no Claude account"},
		{"an undefined account", "team-z", "is not defined here"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aliases := map[string]string{"reviewer": tc.target, "reviewer-b": "team-b"}
			ps := ValidateAccountAliases(aliases, accounts, creds, pools)
			if len(ps) != 1 {
				t.Fatalf("problems = %v, want one: an alias stands for one account and for nothing else", ps)
			}
			if ps[0].Path != "claude_account_aliases.reviewer" || !strings.Contains(ps[0].Message, tc.want) {
				t.Errorf("the refusal does not name the target's own defect (%q): %+v", tc.want, ps[0])
			}
			if AxisNonAccountValue(tc.target) && strings.Contains(ps[0].Message, "claude_accounts: {") {
				t.Errorf("the refusal prescribes a claude_accounts entry of an axis value, which ValidateClaudeAccounts refuses: %+v", ps[0])
			}
			// And the alias whose target IS an account is left alone: the
			// chain's other half is the proof that only the target is refused.
			if tc.name == "a chain" && ValidateAccountAliases(map[string]string{"reviewer-b": "team-b"}, accounts, creds, pools) != nil {
				t.Error("the alias a chain points at was refused on its own")
			}
		})
	}

	for _, target := range []string{"team-a", DefaultClaudeAccount} {
		if ps := ValidateAccountAliases(map[string]string{"reviewer": target}, accounts, creds, pools); len(ps) != 0 {
			t.Errorf("an alias pointing at %q: %v, want no problem: one account of claude_accounts, and the unsuffixed account, are the two targets D2093 admits", target, ps)
		}
	}

	// The resolution is one step and never a walk (D2093): a map that holds a
	// chain anyway — a file written by hand, a caller that skipped the load —
	// answers with the alias's own target, and the reader that finds no account
	// under it refuses the name there.
	if account, alias := ResolveAccountAlias(map[string]string{"reviewer": "reviewer-b", "reviewer-b": "team-b"}, "reviewer"); account != "reviewer-b" || alias != "reviewer" {
		t.Errorf("ResolveAccountAlias(a chain) = %q, %q, want the one step (reviewer-b, reviewer)", account, alias)
	}
	if account, alias := ResolveAccountAlias(map[string]string{"reviewer": "team-a"}, "team-a"); account != "team-a" || alias != "" {
		t.Errorf("ResolveAccountAlias(an account) = %q, %q, want it unchanged and no alias", account, alias)
	}
}
