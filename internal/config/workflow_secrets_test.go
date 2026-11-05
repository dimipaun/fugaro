package config

import (
	"strings"
	"testing"
)

// The block D1260 adds, workflow_secrets.<name>. Its key rule is D1261's,
// split by what a validator holding the block alone can compute: the rules
// that are a function of the name. W0 holds that half here — rather than
// letting each later reader (the local config's parse, the plan predicate,
// the CLI's store path) keep a rule of its own — and the two rules that read
// the operator's configured providers belong to the layer holding that map.
func TestWorkflowSecretRefusesAReservedName(t *testing.T) {
	names := sortedKeys(ReservedSecrets)
	names = append(names,
		ClaudeOAuthTokenPrefix+"x", // a named Claude account's derived secret
		CodexOAuthToken,            // the Codex harness's bare credential
		CodexOAuthTokenPrefix+"y",  // and that credential's namespace
		CredentialsBundleSecret,    // the credentials service's own container
	)
	for _, name := range names {
		ps := ValidateWorkflowSecrets(map[string]ProjectSecret{name: {AllowDataTo: []string{"workspace-a/repo-a"}}})
		if len(ps) != 1 {
			t.Errorf("%s: ValidateWorkflowSecrets = %+v, want one problem", name, ps)
			continue
		}
		if ps[0].Path != "workflow_secrets."+name {
			t.Errorf("%s: problem path = %q, want the entry the operator has to edit", name, ps[0].Path)
		}
	}
}

// TestWorkflowSecretRefusesANameNoSecretMayCarry: D1261 accepts a name that
// passes SecretNameRE, so a key spelled otherwise is refused here rather than
// reaching a container ID. The block's own type carries no name check, and
// hand-editing is what this refusal exists for.
func TestWorkflowSecretRefusesANameNoSecretMayCarry(t *testing.T) {
	for _, name := range []string{"Deploy-Token", "deploy_token", "deploy.token", "-deploy"} {
		ps := ValidateWorkflowSecrets(map[string]ProjectSecret{name: {AllowDataTo: []string{"workspace-a/repo-a"}}})
		if len(ps) != 1 || ps[0].Path != "workflow_secrets."+name {
			t.Errorf("%q: ValidateWorkflowSecrets = %+v, want one problem at the entry", name, ps)
		}
	}
}

// TestWorkflowSecretRefusesTheProxySwitch: D1275 — proxy is the Bitbucket
// token's switch and nothing else's, so no workflow_secrets entry sets it.
// D1261 already refuses bitbucket-token as a key, which makes this belt and
// braces, and it is what keeps the forwarding feature from acquiring a second
// spelling: a proxy written here would be a setting nothing reads.
func TestWorkflowSecretRefusesTheProxySwitch(t *testing.T) {
	ps := ValidateWorkflowSecrets(map[string]ProjectSecret{"deploy-token": {AllowDataTo: []string{"workspace-a/repo-a"}, Proxy: true}})
	if len(ps) != 1 || ps[0].Path != "workflow_secrets.deploy-token.proxy" {
		t.Fatalf("ValidateWorkflowSecrets = %+v, want one problem at workflow_secrets.deploy-token.proxy", ps)
	}
	if !strings.Contains(ps[0].Message, "Bitbucket token's switch") {
		t.Errorf("message %q does not say what proxy is", ps[0].Message)
	}
}

// TestWorkflowSecretRefusesABareStar: D1276 — the entry rules are
// project_secrets' own, held here again so an edit to one block's rule cannot
// change the other's silently. A bare "*" is every workspace, the widening
// this list never makes.
func TestWorkflowSecretRefusesABareStar(t *testing.T) {
	ps := ValidateWorkflowSecrets(map[string]ProjectSecret{"deploy-token": {AllowDataTo: []string{"*"}}})
	if len(ps) != 1 || ps[0].Path != "workflow_secrets.deploy-token.allow_data_to" {
		t.Fatalf("ValidateWorkflowSecrets = %+v, want one problem at workflow_secrets.deploy-token.allow_data_to", ps)
	}
	if !strings.Contains(ps[0].Message, allowDataToBareStar) {
		t.Errorf("message %q is not the refusal project_secrets gives the same entry", ps[0].Message)
	}
}

// TestWorkflowSecretAcceptsASegmentWildcard: the entries the ruling allows,
// and the matcher the new block shares with project_secrets — segment against
// segment, case-folded, a "*" never crossing "/". AllowedForWorkflowSecret is
// AllowedForProjectSecret's body over the new map, so a name with no entry is
// never allowed.
func TestWorkflowSecretAcceptsASegmentWildcard(t *testing.T) {
	secrets := map[string]ProjectSecret{"deploy-token": {AllowDataTo: []string{"workspace-a/*", "workspace-b/api-*"}}}
	if ps := ValidateWorkflowSecrets(secrets); len(ps) != 0 {
		t.Fatalf("a segment wildcard was refused: %+v", ps)
	}
	for _, repo := range []string{"workspace-a/repo-a", "WORKSPACE-A/repo-b", "workspace-b/api-core"} {
		if !AllowedForWorkflowSecret(secrets, "deploy-token", repo) {
			t.Errorf("%s was refused by an entry that admits it", repo)
		}
	}
	for _, repo := range []string{"workspace-a/nested/repo", "workspace-c/repo-a"} {
		if AllowedForWorkflowSecret(secrets, "deploy-token", repo) {
			t.Errorf("%s was admitted by an entry that does not reach it", repo)
		}
	}
	if AllowedForWorkflowSecret(secrets, "other-token", "workspace-a/repo-a") {
		t.Error("a name with no entry was allowed")
	}
}
