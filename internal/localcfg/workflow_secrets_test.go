package localcfg

import (
	"strings"
	"testing"
)

// workflowProvidersFixture is a local config's providers: block whose one
// entry stores its key under a name of its own, so the conventional
// <entry>-api-key is free for the conventional-name refusal below to reach
// through ReservedProviderSecret rather than ProviderBySecret.
const workflowProvidersFixture = `providers:
  acme-llm:
    kind: anthropic-compat
    base_url: https://llm.example/api
    auth: bearer
    secret: acme-llm-secret
    models: ["acme/*"]
`

// TestLocalConfigRefusesAReservedWorkflowSecretName: the workflow_secrets
// block's key rule reached through the real parse rather than by calling
// config.ValidateWorkflowSecrets directly. A config that loads is what the
// plan-time predicate and the store path read, so the refusal W0 wrote has to
// happen at localcfg.validate's call beside ValidateProjectSecrets, and a key
// the platform already mounts (config.ReservedSecrets) is the state D1261
// exists to make unreachable.
func TestLocalConfigRefusesAReservedWorkflowSecretName(t *testing.T) {
	_, err := Parse([]byte(sample + "workflow_secrets:\n  bitbucket-token:\n    allow_data_to: [acme/web]\n"))
	if err == nil {
		t.Fatal("a reserved name was accepted as a workflow_secrets key")
	}
	if !strings.Contains(err.Error(), "workflow_secrets.bitbucket-token") {
		t.Errorf("the refusal does not name the entry: %v", err)
	}
}

// TestLocalConfigRefusesAProvidersOwnSecretAsAWorkflowSecretKey: the
// cross-block half of D1261's key rule, which only this layer can compute
// because only it holds lc.Providers. A key that is the secret: of a
// configured provider would mount the owner's provider credential as the
// workflow's own variable, which is why a repository's own workflow refuses
// the same collision (CheckWorkflowProviderSecrets).
func TestLocalConfigRefusesAProvidersOwnSecretAsAWorkflowSecretKey(t *testing.T) {
	_, err := Parse([]byte(sample + workflowProvidersFixture + "workflow_secrets:\n  acme-llm-secret:\n    allow_data_to: [acme/web]\n"))
	if err == nil {
		t.Fatal("a configured provider's own secret was accepted as a workflow_secrets key")
	}
	if !strings.Contains(err.Error(), "workflow_secrets.acme-llm-secret") {
		t.Errorf("the refusal does not name the entry: %v", err)
	}
	if !strings.Contains(err.Error(), "acme-llm") {
		t.Errorf("the refusal does not name the provider the key belongs to: %v", err)
	}
}

// TestLocalConfigRefusesAProvidersConventionalNameAsAWorkflowSecretKey: the
// second cross-block collision. <entry>-api-key belongs to that provider
// whatever its secret: says today, so a value stored under it would become
// the provider's credential the day the key is renamed to the convention. The
// fixture spells the provider's secret off-convention on purpose, so this
// refusal can only come from ReservedProviderSecret.
func TestLocalConfigRefusesAProvidersConventionalNameAsAWorkflowSecretKey(t *testing.T) {
	_, err := Parse([]byte(sample + workflowProvidersFixture + "workflow_secrets:\n  acme-llm-api-key:\n    allow_data_to: [acme/web]\n"))
	if err == nil {
		t.Fatal("a configured provider's conventional key name was accepted as a workflow_secrets key")
	}
	if !strings.Contains(err.Error(), "workflow_secrets.acme-llm-api-key") {
		t.Errorf("the refusal does not name the entry: %v", err)
	}
	if !strings.Contains(err.Error(), "acme-llm-api-key") {
		t.Errorf("the refusal does not name the reserved name: %v", err)
	}
}
