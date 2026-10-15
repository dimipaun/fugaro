package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// ProjectSecret is one entry of the local config's project_secrets block:
// the owner's decision to store a reserved secret's value once per project
// and mount it from every repository the entry allow-lists (design
// docs/design/project-secrets.md §3). It is the same shape as
// ClaudeAccount, and like it, operator-only local config: never
// fugaro.yaml, never the project layer, never published.
//
// Unlike a provider's allow_data_to (§3a, the owner's final ruling), an
// empty or absent list here means "shared with nobody", unchanged: only
// providers: gets the no-list/["*"]-means-any-repository rule. The literal
// "*" is refused as a repository slug by repoSlugRE, deliberately.
type ProjectSecret struct {
	// AllowDataTo are the repositories (owner/name) whose runs may mount
	// this project secret; empty means none.
	AllowDataTo []string `yaml:"allow_data_to,omitempty" json:"allow_data_to,omitempty"`
}

// AllowsData reports whether repo (owner/name) may mount p. Case-folded,
// like ModelProvider.AllowsData and ClaudeAccount.AllowsData.
func (p ProjectSecret) AllowsData(repo string) bool {
	return slices.ContainsFunc(p.AllowDataTo, func(r string) bool { return strings.EqualFold(r, repo) })
}

// AllowedForProjectSecret reports whether the project_secrets block lists
// repo for name. A name with no entry is never allowed.
func AllowedForProjectSecret(secrets map[string]ProjectSecret, name, repo string) bool {
	p, ok := secrets[name]
	return ok && p.AllowsData(repo)
}

// ValidateProjectSecrets reports every rule the local config's
// project_secrets block breaks, the same shape as ValidateClaudeAccounts.
// The only key rule is membership in ReservedSecrets (P1): a provider's
// .Secret can never be one of those (ValidateModelProviders refuses it
// unconditionally) and no named claude account's derived secret has that
// shape either, so no collision check against providers/claude_accounts is
// needed -- and one built on ClaudeAccountBySecret would wrongly refuse the
// bare claude-oauth-token, the default account's own name, which this
// block is meant to accept.
func ValidateProjectSecrets(secrets map[string]ProjectSecret) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(secrets) {
		at := "project_secrets." + pluginwire.Printable(name)
		if _, reserved := ReservedSecrets[name]; !reserved {
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%q is not a secret Fugaro stores project-wide: only %s", pluginwire.Printable(name), reservedSecretList())})
		}
		for _, r := range secrets[name].AllowDataTo {
			if !repoSlugRE.MatchString(r) || hasDotSegment(r) {
				ps = append(ps, Problem{Path: at + ".allow_data_to", Message: fmt.Sprintf("%q must be a repository as owner/name", pluginwire.Printable(r))})
			}
		}
	}
	return ps
}

// reservedSecretList is the four reserved names, sorted, for a message.
func reservedSecretList() string {
	return strings.Join(sortedKeys(ReservedSecrets), ", ")
}
