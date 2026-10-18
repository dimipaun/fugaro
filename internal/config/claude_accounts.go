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

// ValidClaudeAccountRef checks a name written where an account is REFERENCED
// rather than defined — a profile's claude_accounts entry (JobProfile) — and
// returns "" for a valid one, else the sentence saying what is wrong with it.
//
// It is the shape half of ValidateClaudeAccounts' rule, spelled once so the
// reference and the definition cannot disagree: the reserved DefaultClaudeAccount
// is valid here and only here (claude_accounts itself refuses to define it, the
// account being the unsuffixed claude-oauth-token every repository already
// has), every other name is a named account's (ClaudeAccountNameRE and
// MaxClaudeAccountNameLen). The message never echoes the name: a value that
// fails this check reached it exactly because it is untrusted text, and the
// sentence is printed to a terminal (the same treatment ValidateClaudeAccounts
// gives one).
func ValidClaudeAccountRef(name string) string {
	switch {
	case name == "":
		return `an account name must not be empty: name the account whose token this profile's runs use, or remove the entry`
	case name == DefaultClaudeAccount:
		return ""
	case len(name) > MaxClaudeAccountNameLen:
		return fmt.Sprintf("account name must be at most %d characters", MaxClaudeAccountNameLen)
	case !ClaudeAccountNameRE.MatchString(name):
		return fmt.Sprintf("account name must match %s, or be %q for the default account", ClaudeAccountNameRE, DefaultClaudeAccount)
	}
	return ""
}

// MountsAnthropicKey reports whether a profile's secrets: entry is an
// Anthropic API key, in either of the two spellings a profile can hold one in:
// the reserved secret name anthropic-api-key (AnthropicSecret, mounted as
// ANTHROPIC_API_KEY), or the variable name ANTHROPIC_API_KEY itself — an
// operator who writes the variable where the name goes means the same
// credential.
//
// It is the predicate of D4's rule that a profile listing Claude accounts may
// not also carry an API key (design project-named-oauth-tokens §4.1): an
// account job's container would then hold a key no OAuth run of it can use,
// and "which credential does this job serve" would stop being answerable from
// the profile. The design words the same rule as "a provider whose key
// variable is ANTHROPIC_API_KEY"; this release cannot reach that case through
// a profile, and refusing the variable's own spelling is what covers it: a
// provider's key is mounted as FUGARO_PROVIDER_KEY_<SECRET>
// (ProviderKeyEnv), never as the bare ANTHROPIC_API_KEY, a secrets: entry is
// held to CheckSecretName, and a provider's secret may never be a reserved
// secret's name (reservedSecretName) — so the two spellings below are the
// ones a profile can be written with, and neither a provider entry nor any
// other secrets: entry can spell an Anthropic key in a third way.
func MountsAnthropicKey(secret string) bool {
	return secret == AnthropicSecret || secret == ReservedSecrets[AnthropicSecret]
}

// ValidateProfileClaudeAccounts reports every rule the local config's
// profiles break in their claude_accounts entries, the same shape as
// ValidateClaudeAccounts and ValidateModelProviders.
//
// It resolves each entry against the two blocks an account can be defined in,
// because that is the question this document alone can answer: "default" is
// the unsuffixed claude-oauth-token, whose project-wide copy exists only when
// project_secrets carries the entry that says so (ProjectScopeByDefault
// refuses the bare name as a swept-in reserved secret, deliberately), and any
// other name must be a claude_accounts entry — an account's token is derived
// from its name (ClaudeAccountSecret) and its repositories are the account's
// own allow_data_to, so a profile that names one the file does not define
// names no credential at all. The refusal names the entry, the account and
// where it belongs (design §4.4 row 1).
//
// A name that is not a name is reported by ValidClaudeAccountRef and not
// resolved: the resolution's question is about a name, and asking it about
// arbitrary text would answer with a sentence about the wrong thing.
func ValidateProfileClaudeAccounts(profiles map[string]JobProfile, accounts map[string]ClaudeAccount, projectSecrets map[string]ProjectSecret) []Problem {
	var ps []Problem
	for _, name := range profileNames(profiles) {
		seen := map[string]bool{}
		for i, ref := range profiles[name].ClaudeAccounts {
			at := fmt.Sprintf("profiles.%s.claude_accounts[%d]", pluginwire.Printable(name), i)
			if msg := ValidClaudeAccountRef(ref); msg != "" {
				ps = append(ps, Problem{Path: at, Message: msg})
				continue
			}
			if seen[ref] {
				ps = append(ps, Problem{Path: at, Message: "an account is listed twice; list each account once"})
				continue
			}
			seen[ref] = true
			if ref == DefaultClaudeAccount {
				if _, ok := projectSecrets[ClaudeAccountSecret(DefaultClaudeAccount)]; !ok {
					ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("the default account is not stored project-wide here: its token is %s, and a project job mounts it only when project_secrets carries a %s entry with the repositories that may use it — add project_secrets: {%s: {allow_data_to: [owner/name]}} to this file, or drop the entry from the profile",
						ClaudeAccountSecret(DefaultClaudeAccount), ClaudeAccountSecret(DefaultClaudeAccount), ClaudeAccountSecret(DefaultClaudeAccount))})
				}
				continue
			}
			if _, ok := accounts[ref]; !ok {
				ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("the account %q is not defined here: a profile may serve only an account of this file's claude_accounts block, where the account's own allow_data_to decides which repositories may use its token — define it as claude_accounts: {%s: {allow_data_to: [owner/name]}}, or drop the entry from the profile", pluginwire.Printable(ref), pluginwire.Printable(ref))})
			}
		}
	}
	return ps
}

// profileNames is a stable order for error messages (a map's range is random,
// so a file with two bad entries would otherwise name a different one on each
// read). localcfg keeps its own copy for its own messages; this one is here so
// the validation above is deterministic on its own.
func profileNames(profiles map[string]JobProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
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
