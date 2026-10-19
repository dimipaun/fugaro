package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// DefaultClaudeAccount is the always-available, implicit name: the
// unsuffixed claude-oauth-token secret every repository already uses
// (design docs/design/named-oauth-tokens.md §1). It is reserved and may
// not appear as a key of claude_accounts.
const DefaultClaudeAccount = "default"

// ClaudeAccountNameRE is a named Claude OAuth account's name: never
// "default" (checked separately, so the message names the reservation).
// This tightens the plan text's `^[a-z][a-z0-9-]{0,30}$` on purpose: that
// regex accepts a trailing dash ("a-") and a doubled dash ("a--b"). A
// trailing dash would make an invalid Cloud Run job name once Task 2 builds
// the workflow_account variant name (gcp.WorkflowVariant joins workflow and
// account with "_", not "-"); requiring every dash to be immediately
// followed by an alphanumeric rules that out, and a doubled dash along with
// it, even though a doubled dash is no longer the collision risk it once
// was when an earlier draft of gcp.WorkflowVariant joined with "--" instead
// of "_" (that separator could not tell "w" + "x--y" apart from "w--x" +
// "y"; "_" never appears in either name's charset, so no such collision is
// possible any more).
//
// This regex alone does not bound a name's length to MaxClaudeAccountNameLen:
// each of its {0,30} repetitions may consume either one character or, via
// the dash alternative, two ("-" plus the alphanumeric it must precede), so
// a dash-heavy name such as "a-a-a-...-a" (30 repetitions of "-a") is 61
// characters and still matches. ValidateClaudeAccounts checks the length
// explicitly, separately from this regex.
var ClaudeAccountNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9]|-[a-z0-9]){0,30}$`)

// MaxClaudeAccountNameLen is the longest a claude_accounts name may be.
// ClaudeAccountSecret prepends "claude-oauth-token-" (19 characters) to
// build the secret name, which SecretNameRE elsewhere caps at 63
// characters; the name is also concatenated into gcp.WorkflowVariant's
// "workflow_name" key, which Cloud Run job and service-account identifiers
// bound tightly (design docs/design/named-oauth-tokens.md). 31 keeps every
// derived identifier comfortably inside those limits.
const MaxClaudeAccountNameLen = 31

// ClaudeOAuthTokenPrefix is ClaudeAccountSecret's prefix for every name but
// the default: the whole claude-oauth-token-<name> namespace is reserved
// for a named Claude account's derived secret ID and may never be a
// workflow's own secret (Validate, below) or a model provider's key — a
// name merely shaped like one, whether or not its suffix names a
// configured account, must be refused wherever a secret name is accepted
// (internal/cli/secrets.go, internal/cli/initsecrets.go), not only when it
// happens to collide with a real one.
const ClaudeOAuthTokenPrefix = "claude-oauth-token-"

// ClaudeAccount is one named Claude OAuth credential the owner has
// configured in the local config (never fugaro.yaml or the project layer:
// design §3 T1), parallel to ModelProvider. Its secret is derived
// (ClaudeAccountSecret), never independently settable (design §3 T4).
type ClaudeAccount struct {
	// AllowDataTo are the repositories (owner/name) whose runs may choose
	// this account; empty means none.
	AllowDataTo []string `yaml:"allow_data_to,omitempty" json:"allow_data_to,omitempty"`
}

// AllowsData reports whether repo (owner/name) may choose a. Case-folded,
// like ModelProvider.AllowsData.
func (a ClaudeAccount) AllowsData(repo string) bool {
	return slices.ContainsFunc(a.AllowDataTo, func(r string) bool { return strings.EqualFold(r, repo) })
}

// ClaudeAccountSecret is the logical secret name holding name's token:
// "claude-oauth-token" for "" or "default", else
// "claude-oauth-token-"+name. It is a pure function of name: no local
// config lookup, so it never needs claude_accounts to exist for the
// default account, which is how backward compatibility holds (design §5).
func ClaudeAccountSecret(name string) string {
	if name == "" || name == DefaultClaudeAccount {
		return "claude-oauth-token"
	}
	return ClaudeOAuthTokenPrefix + name
}

// ClaudeAccountOfSecrets is the Claude account a job that mounts exactly
// these secrets runs as: DefaultClaudeAccount when the list holds the
// reserved claude-oauth-token (ClaudeAccountSecret(DefaultClaudeAccount)),
// the name when it holds a named account's token (ClaudeAccountSecret(name)),
// and "" when it holds neither — an api-key or Vertex profile runs as no
// Claude account and its job's account segment is empty. ok is false in that
// last case.
//
// It is a pure function of the secret NAMES, and it exists because the two
// places that need the answer hold different documents: internal/infra builds
// a project job's name from the operator's local config (projectJobSecrets,
// which resolves the account through the claude_accounts block) and
// internal/runner has to reproduce that name from the policy node, which
// carries a profile's mount list and nothing else (plan task 3.3 step 1). One
// function for the axis both sides share, so the two cannot disagree about
// which job a run is in — the runner matching a name the deployer would not
// have built is a halt on every run of that profile.
//
// A profile listing two Claude accounts is refused at deploy time (both mount
// CLAUDE_CODE_OAUTH_TOKEN, and projectJobSecrets refuses the duplicate
// variable), and a name shaped like a named account's secret that no
// claude_accounts entry defines is refused there too (projectSecretEnv: a
// secret Fugaro neither reserves nor has configured). This reads the first
// account it finds and does not try to be the second door on either rule:
// neither case can reach a deployed job.
func ClaudeAccountOfSecrets(secrets []string) (string, bool) {
	for _, s := range secrets {
		if s == ClaudeAccountSecret(DefaultClaudeAccount) {
			return DefaultClaudeAccount, true
		}
		if name, ok := strings.CutPrefix(s, ClaudeOAuthTokenPrefix); ok && name != "" {
			return name, true
		}
	}
	return "", false
}

// ClaudeAccountBySecret is the account name whose secret is secret, the
// mirror of ProviderBySecret: used where a secret ID must be traced back
// to the name a human chose (fugaro secrets set's validation, Task 5).
func ClaudeAccountBySecret(accounts map[string]ClaudeAccount, secret string) (name string, ok bool) {
	if secret == ClaudeAccountSecret(DefaultClaudeAccount) {
		return DefaultClaudeAccount, true
	}
	for _, n := range sortedKeys(accounts) {
		if ClaudeAccountSecret(n) == secret {
			return n, true
		}
	}
	return "", false
}

// AllowedClaudeAccounts are the names repo may choose among: "default"
// always, plus every configured name whose allow_data_to lists repo,
// sorted. This is the one place that decides the question in design §6a;
// infra's job-variant loop (Task 4) and the CLI's --token resolution
// (Task 6) both call it, never duplicate the logic.
func AllowedClaudeAccounts(accounts map[string]ClaudeAccount, repo string) []string {
	out := []string{DefaultClaudeAccount}
	for _, n := range sortedKeys(accounts) {
		if accounts[n].AllowsData(repo) {
			out = append(out, n)
		}
	}
	return out
}

// ValidateClaudeAccounts reports every rule the local config's
// claude_accounts block breaks, the same shape as ValidateModelProviders.
// A name or repository slug that fails its own check is never echoed raw:
// Path and Message run it through pluginwire.Printable first (the same
// treatment layer.go's showKey gives a project layer key), since the value
// that triggers this function reached it exactly because it is untrusted
// input (a bad regex match), and this text is printed to a terminal.
func ValidateClaudeAccounts(accounts map[string]ClaudeAccount) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(accounts) {
		at := "claude_accounts." + pluginwire.Printable(name)
		switch {
		case name == DefaultClaudeAccount:
			ps = append(ps, Problem{Path: at, Message: `"default" is reserved for the unnamed claude-oauth-token secret and may not be configured here`})
		case len(name) > MaxClaudeAccountNameLen:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("account name must be at most %d characters", MaxClaudeAccountNameLen)})
		case !ClaudeAccountNameRE.MatchString(name):
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("account name must match %s", ClaudeAccountNameRE)})
		}
		for _, r := range accounts[name].AllowDataTo {
			if !repoSlugRE.MatchString(r) || hasDotSegment(r) {
				ps = append(ps, Problem{Path: at + ".allow_data_to", Message: fmt.Sprintf("%q must be a repository as owner/name", pluginwire.Printable(r))})
			}
		}
	}
	return ps
}

// hasDotSegment reports whether repo's owner or name half is "." or "..":
// repoSlugRE's charset allows both (it permits '.', for a dotted owner or
// repository name), so "../.." and "./." pass it but name no repository.
// Checked for claude_accounts.*.allow_data_to only: a provider's
// allow_data_to (ValidateModelProviders) is a pre-existing, separate check
// this task does not touch.
func hasDotSegment(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return !ok || owner == "." || owner == ".." || name == "." || name == ".."
}
