package config

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// ValidateWorkflowSecrets reports every rule the local config's
// workflow_secrets block breaks, the same shape as ValidateProjectSecrets.
// The block is D1260's: a repository's own application secret — a name no
// project_secrets entry can hold, that block's keys being the reserved names,
// which is why D1260 gives it a second block rather than widening the first —
// stored once per project and mounted from the repositories its entry
// allow-lists (docs/project-workflow-secrets).
//
// The key rule is D1261's, and this function holds the half of it that is a
// function of the name alone: a name a secret may carry, and none that a
// reservation the tree already makes claims — a key of ReservedSecrets,
// either harness's OAuth namespace, or the credentials service's bundle.
// Those are the names Validate refuses for a repository's own workflow,
// restated here rather than invented. The other half — a configured provider's
// own secret, or the conventional name its provider map gives it — needs the
// operator's providers, which this signature does not take, and is checked by
// the layer that holds them (CheckWorkflowProviderSecrets' shape;
// internal/localcfg).
//
// The type is ProjectSecret, reused rather than copied: AllowsData,
// matchRepoPattern and invalidAllowDataToEntry are one implementation for both
// blocks. The entry rules are that block's, unchanged (D1276), and proxy is
// refused outright (D1275).
func ValidateWorkflowSecrets(secrets map[string]ProjectSecret) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(secrets) {
		at := "workflow_secrets." + pluginwire.Printable(name)
		if msg := workflowSecretKeyRefusal(name); msg != "" {
			ps = append(ps, Problem{Path: at, Message: msg})
		}
		for _, r := range secrets[name].AllowDataTo {
			if msg := invalidAllowDataToEntry(r); msg != "" {
				ps = append(ps, Problem{Path: at + ".allow_data_to", Message: msg})
			}
		}
		// proxy is the Bitbucket token's switch, and D493 is why it exists at
		// all: the credentials service reads it to decide whether to forward
		// that one token, and it reads it from project_secrets.bitbucket-token
		// alone. That name is already refused as a key here, so a proxy on any
		// entry of this block is a setting nothing reads — the failure mode
		// this block's checks exist to prevent, an operator who wrote it
		// believing their installation is proxied (D1275).
		if secrets[name].Proxy {
			ps = append(ps, Problem{Path: at + ".proxy", Message: fmt.Sprintf("%s is the Bitbucket token's switch: no workflow_secrets entry may set it, because the credentials service forwards that token alone and reads its switch from project_secrets.%s (D493, D1275)", pluginwire.Printable("proxy"), BitbucketSecret)})
		}
	}
	return ps
}

// AllowedForWorkflowSecret reports whether the workflow_secrets block lists
// repo for name. A name with no entry is never allowed: D1262 gives this block
// no sweep, so an entry is the whole gate and absent or empty means nobody
// mounts the value.
func AllowedForWorkflowSecret(secrets map[string]ProjectSecret, name, repo string) bool {
	p, ok := secrets[name]
	return ok && p.AllowsData(repo)
}

// workflowSecretKeyRefusal returns "" when name may be a key of
// workflow_secrets, else the sentence saying why not. Each refusal is the one
// a repository's own workflow already gets for the same name (Validate), so
// the tree words it once.
func workflowSecretKeyRefusal(name string) string {
	if !SecretNameRE.MatchString(name) {
		return fmt.Sprintf("%s must be lower-case letters, digits and dashes", pluginwire.Printable(name))
	}
	if _, reserved := ReservedSecrets[name]; reserved {
		return fmt.Sprintf("%s is reserved for the platform's own secrets", pluginwire.Printable(name))
	}
	// The whole claude-oauth-token-<name> namespace is a named Claude
	// account's derived secret ID, reserved whether or not the suffix names a
	// configured account: a workflow secret under one would be read as that
	// person's credential.
	if ClaudeOAuthSecret(name) {
		return fmt.Sprintf("names starting %s are reserved for named Claude accounts (claude_accounts in the local config), not a workflow secret", ClaudeOAuthTokenPrefix)
	}
	// Nothing mounts a Codex credential yet, so unlike the Claude namespace
	// there is no legitimate case at all: any name of this shape is a typo or
	// an attempt to have the platform's future credential read a value this
	// block chose. Refused whether or not a Codex provider ever names it.
	if CodexOAuthSecret(name) {
		return fmt.Sprintf("%s and names starting %s are reserved for the Codex harness's OAuth credential, not a workflow secret", CodexOAuthToken, CodexOAuthTokenPrefix)
	}
	// The credentials service's one bundle container: its document is every
	// model credential the installation holds, and a project copy of that name
	// would mount the whole set into a job a run's agent can read.
	if name == CredentialsBundleSecret {
		return fmt.Sprintf("%s is the credentials service's own bundle container (the platform's), not a workflow secret", CredentialsBundleSecret)
	}
	return ""
}
