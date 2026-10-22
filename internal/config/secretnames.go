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
