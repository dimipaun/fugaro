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
const ClaudeOAuthTokenPrefix = ClaudeOAuthToken + "-"

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
		return ClaudeOAuthToken
	}
	return ClaudeOAuthTokenPrefix + name
}

// ProjectJobAccounts is the account axis of every project job a profile
// serves, sorted, the empty account first: the profile's own no-account job
// (""), which every profile keeps whatever it lists, plus one entry per
// account the profile lists other than the default.
//
// The default account is deliberately not an entry of its own. Its token is
// the unsuffixed claude-oauth-token and it rides the profile's no-account job
// (design project-named-oauth-tokens D5): that is the job every profile has,
// so a profile listing `default` serves it there and a profile listing
// nothing serves no token on that same job. That is also why the no-account
// job's name never moves when a profile gains an account (D9): a job's name is
// a function of its (profile, provider, account) triple
// (gcp.ProjectJobName) and of no mount.
//
// It is the ONE function the three sides of a project job compute a profile's
// job set with (the plan's review focus 2): internal/infra deploys one job per
// entry, internal/runner recomputes the names it matches CLOUD_RUN_JOB against
// (projectJobCandidates), and internal/cli picks the entry a launch targets
// (projectJobAccountFor). A second copy of this rule would let one side build
// or launch a job another would not recompute, and the run would halt on the
// name it is in (the runner's cannotPlaceJob).
//
// The input is the profile's own field and nothing else. The account used to
// be read out of the profile's secrets:, which was the same answer while no
// profile could mount a Claude token at all; the field is what a profile
// states now (config.JobProfile.ClaudeAccounts), and a Claude token in
// secrets: is still refused (projectpolicy's isClaudeOAuthSecret), so the two
// readings cannot disagree.
func ProjectJobAccounts(pr JobProfile) []string {
	// A fresh slice: the caller's list is never written to, and "default" is
	// spelled as the empty account here because that is what the axis of a
	// job's name holds for it.
	out := append([]string{""}, pr.ClaudeAccounts...)
	for i, name := range out {
		if name == DefaultClaudeAccount {
			out[i] = ""
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ProjectJobTokenAccount is the account whose token a profile's job holds, for
// the account a job's name is built from (ProjectJobAccounts: "" is the
// profile's no-account job, and the account axis spells the default account
// that way too) — the account as the policy's accounts block spells it, which
// is where the list of repositories that may use the token lives — and whether
// the profile serves a token on that job at all.
//
// It is the join the launcher's gate and the runner's re-check are both made of
// (design project-named-oauth-tokens §4.4, plan task 3): which credential a run
// holds is not a fact of the run but of the job it is in, and a job holds the
// token of exactly one account (or none). A profile's jobs are its no-account
// one plus one per account it lists, so:
//
//   - an account the profile lists is a job of its own, and the job holds that
//     account's token;
//   - the no-account job holds the default account's token when the profile
//     lists "default" — D5's one case of an account riding a job that is not
//     its own, the job every profile has — and no token at all when it does
//     not. The default account is a thing a profile states, never a thing it
//     gets: a profile that lists only named accounts mounts no unsuffixed
//     token anywhere, which is why an OAuth run whose own credential is the
//     default account has no credential on such a profile's job (the runner
//     refuses it rather than running it with none).
//
// The account it returns is never "" (no job that holds a token holds an
// unnamed one) and is spelled the way accounts entries are keyed, because that
// is the block its allow_repos is read from.
func ProjectJobTokenAccount(pr JobProfile, account string) (string, bool) {
	name := account
	if name == "" {
		name = DefaultClaudeAccount
	}
	if !slices.Contains(pr.ClaudeAccounts, name) {
		return "", false
	}
	return name, true
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

// reservedAccountNameMessage is the ONE sentence both halves of the name rule
// answer the reserved name with. The message never echoes the name (the rule
// every message here keeps: the value that reached the check is untrusted text
// printed to a terminal) and it names the fix, which is the operator's next
// move.
const reservedAccountNameMessage = "this name is reserved: the platform holds another credential under it, so an account spoken by it would leave one name meaning two credentials — pick a different account name"

// reservedClaudeAccountName reports whether an account name is the bundle's own
// reserved spelling: config.BitbucketSecret, and no other.
//
// It is the bundle's answer rather than this file's, and the bundle says it on
// its own surface: an account's key in the credentials bundle is
// ClaudeAccountSecret(name), CredentialKey resolves that key back to the bare
// name, and for this one name it refuses it outright — because the bare name is
// what the git entry's document key, the policy's block, the job's own mount and
// the proxy's audit line all speak for the upstream Bitbucket credential, so the
// account would leave one name meaning two credentials (B1.2). The published
// bundle schema pins the same single spelling (creds-bundle.schema.json's `not`,
// held to the loader by TestBundleSchemaMatchesTheLoader).
//
// Every OTHER name a credential of the platform goes by stays a legal account
// name, and that is why this is one name and not ReservedSecrets' four:
// the bundle holds the App key beside an account named after it
// (Bundle.byName's own rule — an account may be named github-app-key), it
// refuses a DOCUMENT that gives one model name to two credentials
// (anthropic-api-key beside an account of that name) rather than the account's
// name, and claude-oauth-token is the default account's secret, whose bundle
// entry is named config.DefaultClaudeAccount. Refusing those would refuse
// accounts the bundle holds.
//
// TestReservedAccountNamesAgreeWithTheBundle (internal/credsvc) holds the two
// ends to each other in both directions, so a reservation added on either side
// alone is red.
func reservedClaudeAccountName(name string) bool { return name == BitbucketSecret }

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
		case reservedClaudeAccountName(name):
			ps = append(ps, Problem{Path: at, Message: reservedAccountNameMessage})
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
// MaxClaudeAccountNameLen) and is not the bundle's one reserved spelling
// (reservedClaudeAccountName, reservedAccountNameMessage: a name the bundle
// refuses an account of is refused here exactly as it is refused as a key of
// claude_accounts, so a policy cannot admit an account no bundle can hold).
// The message never echoes the name: a value that fails this check
// reached it exactly because it is untrusted text, and the sentence is printed
// to a terminal (the same treatment ValidateClaudeAccounts gives one).
func ValidClaudeAccountRef(name string) string {
	switch {
	case name == "":
		return `an account name must not be empty: name the account whose token this profile's runs use, or remove the entry`
	case name == DefaultClaudeAccount:
		return ""
	case reservedClaudeAccountName(name):
		return reservedAccountNameMessage
	case len(name) > MaxClaudeAccountNameLen:
		return fmt.Sprintf("account name must be at most %d characters", MaxClaudeAccountNameLen)
	case !ClaudeAccountNameRE.MatchString(name):
		return fmt.Sprintf("account name must match %s, or be %q for the default account", ClaudeAccountNameRE, DefaultClaudeAccount)
	}
	return ""
}

// MountsAnthropicKey reports whether a profile's secrets: entry means the
// Anthropic API key, in either spelling of it: the reserved secret's name
// (anthropic-api-key, mounted as ANTHROPIC_API_KEY), or that variable's name
// itself — an operator who writes the variable where the name goes means the
// same credential, and reading it as a name of some other secret would let the
// key ride in beside a profile's Claude accounts.
//
// It is the predicate of D4's rule that a profile listing Claude accounts may
// not also carry an API key (design project-named-oauth-tokens §4.1): an
// account job's container would then hold a key no OAuth run of it can use,
// and "which credential does this job serve" would stop being answerable from
// the profile.
//
// The design words the same rule as "a provider whose key variable is
// ANTHROPIC_API_KEY", and no profile can be written with that spelling in this
// release: a provider's key is mounted as FUGARO_PROVIDER_KEY_<SECRET>
// (ProviderKeyEnv), which is never the bare Anthropic variable, a secrets:
// entry is held to CheckSecretName (and "ANTHROPIC_API_KEY" is not a
// lower-case secret name), and a provider's secret may never be a reserved
// secret's name (reservedSecretName) — so the two spellings below are the ones
// reachable, and the variable's own is the one that stands in for the
// provider case (it is refused by CheckSecretName before this predicate, on
// every profile and not only one that lists accounts; internal/projectpolicy's
// TestProfileAccountWithAnthropicKeyRefused pins that ordering). The
// comparison is kept because this is the function that answers "is this entry
// the Anthropic key", and an entry spelling it that way is the key whether or
// not another rule refuses it first.
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
