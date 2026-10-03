package runner_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
)

const providerKey = "sk-or-v1-0123456789abcdef-not-a-real-key"

var orProvider = config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"deepseek/*"}}

// withProviderKey mounts the provider's key as the job does: a variable in
// the runner's environment, listed in FUGARO_SECRET_ENVS.
func withProviderKey(h *harness) {
	env := orProvider.SecretEnv()
	h.deps.Env = append(h.deps.Env, env+"="+providerKey, runner.SecretEnvsVar+"="+env)
}

// TestAgentEnvHasNoProviderKey: with the provider's key mounted in the
// runner, no agent call carries it, in any variable, however the run goes.
func TestAgentEnvHasNoProviderKey(t *testing.T) {
	h := newHarness(t, "", nil)
	withProviderKey(h)
	if _, err := h.run(t, implement("feature"), review("revise", 1), idle, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("no agent calls")
	}
	for i, req := range h.agent.calls {
		for _, kv := range req.Env {
			if strings.Contains(kv, providerKey) || config.IsProviderKeyEnv(strings.SplitN(kv, "=", 2)[0]) {
				t.Errorf("call %d: the provider key is in the agent's env: %s", i, kv)
			}
		}
	}
}

// TestKeyNotInResultJSON: a key the agent got hold of (it is in the runner's
// /proc environ) and echoes into its transcript, stderr, a log line or its
// own report is stored in no bucket object, result.json included, and not
// logged.
func TestKeyNotInResultJSON(t *testing.T) {
	h := newHarness(t, "", nil)
	withProviderKey(h)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Transcript.Write([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"OPENROUTER=` + providerKey + `"}]}}` + "\n"))
		_, _ = req.Stderr.Write([]byte(providerKey + "\n"))
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if keys := objectsContaining(t, h, providerKey); len(keys) > 0 {
		t.Fatalf("the provider key is stored unredacted in %v", keys)
	}
	if strings.Contains(logs.String(), providerKey) {
		t.Fatalf("the log leaks the provider key: %s", logs.String())
	}
}

func TestProviderCredential(t *testing.T) {
	env := []string{"PATH=/bin", orProvider.SecretEnv() + "=" + providerKey}
	cred, value, err := runner.ProviderCredential(env, orProvider)
	if err != nil || value != providerKey {
		t.Fatalf("value = %q, err = %v", value, err)
	}
	if got, err := cred(); err != nil || got != providerKey {
		t.Fatalf("cred = %q, %v", got, err)
	}
	// Only the provider's own variable: the Anthropic key is never a stand-in.
	for name, env := range map[string][]string{
		"missing":        {"ANTHROPIC_API_KEY=" + providerKey},
		"short":          {orProvider.SecretEnv() + "=abc"},
		"other provider": {config.ProviderKeyEnv("other-key") + "=" + providerKey},
	} {
		_, _, err := runner.ProviderCredential(env, orProvider)
		if err == nil || strings.Contains(err.Error(), providerKey) || !strings.Contains(err.Error(), "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestUpstreamErrorEchoingKeyIsRedacted: a provider that echoes the key it
// was sent in an error body ("invalid key sk-or-...") must not put it in
// the agent's hands, the gateway log, the transcript or result.json.
//
// TODO(T8): needs the runner to build gateway routes from the providers
// (runner.ProviderCredential gives the Route.Credential func) and the
// compat-fake (Compat) answering 401 with the key in the body. The
// gateway today copies upstream error bodies to the agent unchanged, so T8
// (or a gateway follow-up) must scrub the route's credential from them;
// this test is then the proof.
func TestUpstreamErrorEchoingKeyIsRedacted(t *testing.T) {
	t.Skip("TODO(T8): the runner does not wire provider routes yet")
}
