package config

import (
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// An alias is a name the owner points at one named Claude account, so the keys
// that choose an account may name the alias rather than the account, and moving
// every stage that names it to another account is one edit rather than one per
// key (docs/account-aliases/spec.md §1, the owner's ruling of 2026-11-10).
//
// It lives in the owner's local project config alone and is never published
// (D2090): a reader resolves it to its target before it asks anything about the
// name or writes one out, so the policy node, the task, the run record and the
// jobs keep their formats and no job image in the field ever meets an alias by
// its own name (F4). This file is the rule and the resolution and nothing else:
// which keys may name an alias is the key's own business (localcfg's
// choiceRefProblem), and where a name is resolved is the reader's.
//
// The readers that write a name out or hold one to a rule each resolve first,
// and each resolves the names a person wrote in this invocation rather than the
// ones a record already holds — a task's OAuthToken and the account a follow-up
// inherits were written after a launch resolved them, so they name the account
// itself and a name this block answers to is refused there (internal/cli's
// nameOrigin): the publish (the two members it copies into the node), the
// launch (a written --token, the stage keys and the project default, before
// the allow-list is asked — claudeAccountFor and the three helpers beside it),
// `fugaro validate` (the same stage accounts, stageAccountProblems), the job
// set (the variant it
// builds and the token that variant mounts, internal/infra's
// mixedStageAccounts and mixedDefaultAccount), the craft migration
// (internal/cli/init_craft_migrate.go, D2102: the accounts the installation's
// stages ran on, resolved before a craft key is derived from one) and a local
// diagnosis (troubleshoot's default account).

// ResolveAccountAlias is name resolved through aliases: the account the name
// stands for, and the alias it came through ("" when the name is not an alias).
//
// It is ONE step and never a walk (D2093: no chains), which is why it answers
// an alias with its own target even where a file written by hand makes that
// target another alias: a reader that then finds no account of the name refuses
// it exactly as it refuses any undefined account, rather than following a chain
// this rule refuses at load.
func ResolveAccountAlias(aliases map[string]string, name string) (account, alias string) {
	if target, ok := aliases[name]; ok {
		return target, name
	}
	return name, ""
}

// aliasTargetProblem is the target rule (D2093), answered for a target that
// already satisfies the alias's own name rule: the one account the alias stands
// for, and the five things it may not be. It takes the three blocks a name can
// be held by because each refusal has to say what the name actually is — a
// chain into another alias, a pool (resolved in the run, which is the
// re-resolution the ruling forbids), a credential of another type, one of the
// account axis's non-account values, or a name nothing defines — and the
// message the operator needs is the one that names the move that works.
//
// The account axis's non-account values are refused in the same words as the
// choice keys refuse them (NonAccountChoiceMessage is the shared half of it):
// they name no account of any run's, so an alias pointing at one would be a key
// that chooses a run's account naming a credential no job mounts.
//
// No arm of it prescribes a claude_accounts entry that ValidateClaudeAccounts
// refuses — the trap the choice keys' own sentence exists to avoid. The axis
// values are the arm that answers a name that rule admits and this one does
// not; a target that could not be an account's name at all is the other, and
// credentialNameProblem answers it, so the operator reads the sentence that
// entry would be refused with rather than an entry to write.
func aliasTargetProblem(target string, aliases map[string]string, accounts map[string]ClaudeAccount, creds []Credential, pools []Pool) string {
	switch {
	case target == "":
		return fmt.Sprintf("an alias stands for one account, and this one names none: name the account of this file's claude_accounts block it points at, or %q for the unsuffixed account", DefaultClaudeAccount)
	case target == DefaultClaudeAccount:
		return ""
	case AxisNonAccountValue(target):
		return fmt.Sprintf("this is one of the account axis's values that names no Claude account — the project's own Anthropic API key, or the project's provider key: the credential under it belongs to the platform or to the credentials service and no job this installation deploys mounts it for a stage, so an alias cannot stand for it — point the alias at one account of this file's claude_accounts block, or at %q for the unsuffixed account", DefaultClaudeAccount)
	}
	if _, ok := aliases[target]; ok {
		return "an alias stands for one account and never for another alias: a chain would need a cycle check and a reader that prints the path a name came through, and it would move every stage that names this alias the day the other one is repointed — point this alias straight at the account"
	}
	if _, ok := accounts[target]; ok {
		return ""
	}
	if slices.ContainsFunc(pools, func(p Pool) bool { return p.Name == target }) {
		return fmt.Sprintf("this name is a pool's, and a pool is walked inside a run — its members are chosen there, at the stage boundary — which is the re-resolution a repoint must not cause: an alias stands for one account of this file's claude_accounts block, or %q for the unsuffixed account", DefaultClaudeAccount)
	}
	for _, c := range creds {
		if c.Name == target {
			return fmt.Sprintf("this name is a credential's (type %s) and no account's: an alias stands for one account of this file's claude_accounts block, whose own allow_data_to decides which repositories may use its token — point the alias at the account, or at %q for the unsuffixed account",
				pluginwire.Printable(c.Type), DefaultClaudeAccount)
		}
	}
	// A target the name rule refuses can never be an account's — the reserved
	// bundle spelling, a name ending in the review variant's suffix, a bad shape,
	// an over-long name — so the sentence below would send the operator to an
	// entry ValidateClaudeAccounts refuses. The sentence here is
	// credentialNameProblem's (D2092's rule, shared with a credential and a pool)
	// so that the refusal is one rule in two places rather than two rules.
	if msg := credentialNameProblem(target); msg != "" {
		return fmt.Sprintf("the name %q is no account's, and an alias stands for one account of this file's claude_accounts block: %s",
			pluginwire.Printable(target), msg)
	}
	return fmt.Sprintf("the account %q is not defined here: an alias stands for one account of this file's claude_accounts block, where the account's own allow_data_to decides which repositories may use its token, or for %q for the unsuffixed account — define it as claude_accounts: {%s: {allow_data_to: [owner/name]}}, or drop the alias",
		pluginwire.Printable(target), DefaultClaudeAccount, pluginwire.Printable(target))
}

// ValidateAccountAliases reports every rule the local config's
// claude_account_aliases block breaks, the same shape as ValidateClaudeAccounts
// and ValidatePools. It takes the whole account side of the document — the
// accounts, the credentials and the pools — because both of its rules are about
// the names those blocks hold, and no one of them alone can answer either.
//
// The name rule is credentialNameProblem's, shared with a credential and a pool
// of the same shape (D1621, D2092): an alias is a credential-choosing name like
// theirs, and the reserved names it holds — "default", "vertex", the two
// axis non-account values, the bundle's own reserved spelling and a -review
// suffix — are refused in the words those blocks refuse them, so a name can
// never mean two things.
//
// The namespace rule (D2092) is the other half: an alias, an account, a
// credential and a pool share ONE namespace, so a name a choice key holds means
// exactly one thing. Both directions of a clash are this loop, because the
// alias's own names are what it looks up in the other three blocks, and the
// message names the block holding the name rather than sending the operator
// looking for it.
func ValidateAccountAliases(aliases map[string]string, accounts map[string]ClaudeAccount, creds []Credential, pools []Pool) []Problem {
	var ps []Problem
	held := map[string]string{}
	for _, name := range sortedKeys(accounts) {
		held[name] = "claude_accounts"
	}
	for _, c := range creds {
		if c.Name != "" {
			held[c.Name] = "credentials"
		}
	}
	for _, p := range pools {
		if p.Name != "" {
			held[p.Name] = "pools"
		}
	}
	for _, name := range sortedKeys(aliases) {
		at := "claude_account_aliases." + pluginwire.Printable(name)
		if msg := credentialNameProblem(name); msg != "" {
			ps = append(ps, Problem{Path: at, Message: msg})
			continue
		}
		if block, ok := held[name]; ok {
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("this name is taken by a %s entry of the same config: an alias shares one namespace with the accounts, the credentials and the pools — a name written in a choice key has to mean exactly one thing — so pick a different alias name", block)})
			continue
		}
		if msg := aliasTargetProblem(aliases[name], aliases, accounts, creds, pools); msg != "" {
			ps = append(ps, Problem{Path: at, Message: msg})
		}
	}
	return ps
}
