package agent

import (
	"slices"
	"strings"
	"testing"
)

// testGateway is the gateway every api-key EnvSpec in this package's tests
// carries: since AB1b an api-key run has one in every mode (D3009), so a test
// about what the agent's environment holds builds the one a run has. The
// address and the token are placeholders, never credentials.
var testGateway = &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "gw-token-abcdef"}

// TestNoAPIKeyRunPutsTheKeyInTheAgentsEnvironment (AB1b, F15, D3009): no
// api-key run's environment carries the job's key, and the arm that used to
// mount one for want of a gateway is gone. The gateway holds the key and the
// agent presents the gateway's per-run token; a run that reached here without
// a gateway is refused rather than handed the key, exactly as the cascade arm
// refuses it (D143).
//
// Red step: with no gateway — the state every api-key run whose job left the
// mode unset was in — the key itself was in the environment.
//
// Hand mutation this fails on: restoring the fallback (an api-key arm that
// writes p["ANTHROPIC_API_KEY"] into the environment when spec.Gateway is nil),
// which hands the agent a credential no call of its can be accounted for on.
func TestNoAPIKeyRunPutsTheKeyInTheAgentsEnvironment(t *testing.T) {
	realKey := "key-" + strings.ReplaceAll(t.Name(), "/", "-")
	parent := []string{"PATH=/bin", "ANTHROPIC_API_KEY=" + realKey}

	// Without a gateway there is no environment to hold the key: the run is
	// refused, and nothing at all is handed back.
	for _, declared := range [][]string{nil, {"ANTHROPIC_API_KEY"}} {
		env, secretValues, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Secrets: declared})
		if err == nil {
			t.Fatalf("declared %v: an api-key run with no gateway was built: %v", declared, env)
		}
		if env != nil || secretValues != nil {
			t.Errorf("declared %v: a refused run returned env %v, secrets %v", declared, env, secretValues)
		}
		if !strings.Contains(err.Error(), "runs through the gateway") {
			t.Errorf("declared %v: the refusal does not name the gateway: %v", declared, err)
		}
	}

	// With one, the gateway's token is what the agent presents and the job's
	// key is registered for redaction and nothing else — a workflow that
	// declares a mount of ANTHROPIC_API_KEY does not put it back.
	env, secretValues, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: testGateway, Secrets: []string{"ANTHROPIC_API_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(env, "ANTHROPIC_API_KEY"); got != testGateway.Token {
		t.Errorf("ANTHROPIC_API_KEY = %q, want the gateway's per-run token", got)
	}
	if got := envValue(env, "ANTHROPIC_BASE_URL"); got != testGateway.BaseURL {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the gateway's address", got)
	}
	if !slices.Contains(secretValues, realKey) {
		t.Errorf("the job's key is not registered for redaction: %v", secretValues)
	}
	for _, kv := range env {
		if strings.Contains(kv, realKey) {
			t.Errorf("the job's key is in the agent's environment: %s", kv)
		}
	}

	// A declared secret that is missing keeps the plain sentence: the key's
	// own actionable one went with the arm that mounted it.
	if _, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: testGateway, Secrets: []string{"MISSING"}}); err == nil || !strings.Contains(err.Error(), "secret MISSING is not set in the runner environment") {
		t.Errorf("a missing declared secret err = %v, want the plain message", err)
	}
}

// TestAnOAuthRunIsUnchanged (AB1b, D3026, R12): oauth is the one credential
// the gateway does not carry — it is Anthropic's subscription token, presented
// to Anthropic's own endpoint — so its arm, and OAuthStageEnv with it, is
// exactly what it was.
func TestAnOAuthRunIsUnchanged(t *testing.T) {
	token := "oauth-" + strings.ReplaceAll(t.Name(), "/", "-")
	parent := []string{"PATH=/bin", "ANTHROPIC_API_KEY=key-placeholder-123", "CLAUDE_CODE_OAUTH_TOKEN=" + token}
	env, _, err := BuildEnv(parent, EnvSpec{Auth: "oauth"})
	if err != nil {
		t.Fatalf("an oauth run: %v", err)
	}
	if got := envValue(env, "CLAUDE_CODE_OAUTH_TOKEN"); got != token {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the run's own token", got)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") || strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Errorf("an oauth run carries a routing variable: %s", kv)
		}
	}
	// The run presents its own token, so a gateway is an error and a missing
	// token is the same refusal.
	if _, _, err := BuildEnv(parent, EnvSpec{Auth: "oauth", Gateway: testGateway}); err == nil || !strings.Contains(err.Error(), "oauth never goes through the gateway") {
		t.Errorf("oauth with a gateway err = %v, want the unchanged refusal", err)
	}
	if _, _, err := BuildEnv([]string{"PATH=/bin"}, EnvSpec{Auth: "oauth"}); err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("oauth without a token err = %v, want the unchanged refusal", err)
	}
	// A stage on an account of its own is built the way it was: the gateway's
	// routing dropped, the account's token set (its own test is
	// TestOAuthStageEnvDropsTheGatewayAndSetsTheToken).
	gwEnv, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: testGateway})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := OAuthStageEnv(gwEnv, token)
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(stage, "CLAUDE_CODE_OAUTH_TOKEN"); got != token {
		t.Errorf("the stage presents %q, want the account's token", got)
	}
	if envValue(stage, "ANTHROPIC_BASE_URL") != "" || envValue(stage, "ANTHROPIC_API_KEY") != "" {
		t.Errorf("the stage still carries the run's gateway routing: %v", stage)
	}
}
