package config

import "strings"

// The harness OAuth credential families Fugaro reserves: the names a
// person's own subscription token for one coding-agent harness is stored
// under. One harness is driven today and one is reserved ahead of it, and
// both are declared here so every reader — a workflow's secret names
// (Validate), a provider's (ValidateModelProviders), a profile's, `fugaro
// secrets set` (internal/cli/secrets.go) and init's secrets stage
// (internal/cli/initsecrets.go) — asks one question rather than keeping a
// list of its own.
const (
	// ClaudeOAuthToken is the bare, default Claude account's OAuth token:
	// the reserved name every repository's oauth jobs mount
	// (ClaudeAccountSecret(DefaultClaudeAccount), ReservedSecrets).
	ClaudeOAuthToken = "claude-oauth-token"
	// CodexOAuthToken is the bare, default Codex credential's OAuth token.
	//
	// Fugaro does not drive Codex yet (docs/base-image.md: the harness is
	// installed in the base image and nothing wires it to a run), so nothing
	// mounts this name and no block configures a credential of that harness.
	// It is reserved *now*, wherever a secret name is accepted or written
	// (owner ruling of 2026-10-18), because the day the harness lands the
	// name becomes a credential's: a repository's own secret stored under it
	// today would be the token the harness reads tomorrow, and the value
	// would have been written by whoever declared the name first.
	CodexOAuthToken = "codex-oauth-token"
)

// ClaudeOAuthTokenPrefix and CodexOAuthTokenPrefix are the two harnesses'
// named-credential namespaces: <prefix><name> is a *named*
// credential's derived secret ID, never a workflow's own name. Claude's is
// declared beside ClaudeAccountSecret in claude_accounts.go, with the rules
// a named account's name is held to.
const CodexOAuthTokenPrefix = CodexOAuthToken + "-"

// ClaudeOAuthSecret reports whether name is a Claude OAuth credential's
// secret: the bare claude-oauth-token (the default account's) or a name in
// the claude-oauth-token-<name> namespace. A name merely *shaped* like a
// named account's is one, whether or not a claude_accounts entry defines the
// suffix: the whole namespace is reserved.
func ClaudeOAuthSecret(name string) bool {
	return name == ClaudeOAuthToken || strings.HasPrefix(name, ClaudeOAuthTokenPrefix)
}

// CodexOAuthSecret reports whether name is a Codex OAuth credential's
// secret: the bare codex-oauth-token or a name in its namespace, the same
// two shapes ClaudeOAuthSecret reads for the harness Fugaro drives.
func CodexOAuthSecret(name string) bool {
	return name == CodexOAuthToken || strings.HasPrefix(name, CodexOAuthTokenPrefix)
}

// HarnessOAuthSecret reports whether name is any harness's OAuth credential
// secret (Claude's or Codex's). It is the question a check that holds a
// *provider's* or a *profile's* own secret name to the harness families
// asks: those names belong to a person's credential, and a provider's key or
// a workflow's application secret stored under one would be read as that
// credential.
func HarnessOAuthSecret(name string) bool {
	return ClaudeOAuthSecret(name) || CodexOAuthSecret(name)
}

// CredentialsBundleSecret is the logical name of the ONE Secret Manager
// container the credentials service reads (Q6, docs/credentials-service's
// implementation plan, C1.11; the reservation is its review finding n2). It
// is a name of the platform's, and the reason is stronger than the harness
// names above rather than weaker: the container's document is the service's
// whole credential set, written by `fugaro creds set NAME` alone (C1.25), so
// a workflow that declared the name in its `secrets:` would mount the
// service's document into a job, and an operator who stored a value under it
// with `fugaro secrets set` would replace every credential at once. Refusing
// the name at both write paths is what keeps "one container, one writer and
// one reader" true by construction rather than by convention.
//
// It is deliberately NOT in ReservedSecrets, which is the set of names the
// platform MOUNTS into a job (ReservedSecrets' own doc, and
// ProjectScopeByDefault's rule that the project-wide scope is that set):
// nothing mounts this one, and its value is not a plain secret a job's
// environment may carry. The container itself is derived by
// gcp.CredsBundleSecretID, which is ProjectSecretID of this name.
const CredentialsBundleSecret = "creds-bundle"

// ReviewSigningKeySuffix is appended to an account's own secret name
// (ClaudeAccountSecret) to build the name of its review-signing key's bundle
// entry: the HMAC key the credentials service signs a review run's verdict
// with, and verifies it against (docs/c39-review-identity/spec.md §6.1, D1069).
//
// The doubled dash is the separator on purpose, and it is what keeps the
// family out of the account namespace: ClaudeAccountNameRE refuses a doubled
// dash in a name, so no account can ever be named such that its own
// ClaudeAccountSecret collides with a review-signing name — one name, two
// credentials, which is the collision claude-oauth-token-bitbucket-token needs
// a reservation for (CredentialKey, reservedClaudeAccountName). The family
// therefore reserves no account name and the two cannot drift apart.
const ReviewSigningKeySuffix = "--review-signing"

// ReviewSigningKeyPreviousSuffix is the rotation window's second entry: the
// value the rotation replaced, carried beside the new one so there is no
// instant in which the service holds neither, and accepted by the verify route
// and by nothing older (spec.md §6.3, D1069).
const ReviewSigningKeyPreviousSuffix = "-previous"

// ReviewSigningKeySecret is the bundle entry name holding account's
// review-signing key, and ReviewSigningKeyPreviousSecret the name the rotation
// carries the replaced key under. Like ClaudeAccountSecret they are pure
// functions of the account name — the default (or empty) account is the
// unsuffixed claude-oauth-token — and one entry per account is the property
// D892 asks for: the bundle is where it lives, because Q6 pins the service's
// Secret Manager reach to one container and one accessor grant.
func ReviewSigningKeySecret(account string) string {
	return ClaudeAccountSecret(account) + ReviewSigningKeySuffix
}

// ReviewSigningKeyPreviousSecret is ReviewSigningKeySecret of the rotation
// window's previous entry, read beside the current one and never alone.
func ReviewSigningKeyPreviousSecret(account string) string {
	return ReviewSigningKeySecret(account) + ReviewSigningKeyPreviousSuffix
}

// ReviewSigningKeyAccount is the two functions above read backwards: it
// reports whether name is a review-signing entry's name, which account it
// belongs to (DefaultClaudeAccount for the unsuffixed default) and whether it
// is the rotation's previous entry.
//
// A name whose account part is not one an account may be named — empty, over
// MaxClaudeAccountNameLen, or outside ClaudeAccountNameRE — is not a
// review-signing name: the same rule CredentialKey applies to the account
// family, so a key the config path can never produce is a credential that
// cannot be written either, rather than one that loads and is unreachable.
func ReviewSigningKeyAccount(name string) (account string, previous, ok bool) {
	rest, found := strings.CutSuffix(name, ReviewSigningKeyPreviousSuffix)
	if !found {
		rest = name
	}
	previous = found
	own, found := strings.CutSuffix(rest, ReviewSigningKeySuffix)
	if !found {
		return "", false, false
	}
	if own == ClaudeOAuthToken {
		return DefaultClaudeAccount, previous, true
	}
	account, found = strings.CutPrefix(own, ClaudeOAuthTokenPrefix)
	if !found || len(account) > MaxClaudeAccountNameLen || !ClaudeAccountNameRE.MatchString(account) {
		return "", false, false
	}
	return account, previous, true
}
