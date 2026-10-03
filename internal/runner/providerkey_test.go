package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
// The runner builds each call's environment with agent.BuildEnv from its own,
// so this fails if BuildEnv's refusal regresses; the per-auth and declared
// variable cases are in internal/agent TestAgentEnvHasNoProviderKey.
func TestAgentEnvHasNoProviderKey(t *testing.T) {
	h := newHarness(t, "", nil)
	withProviderKey(h)
	if !slices.Contains(h.deps.Env, orProvider.SecretEnv()+"="+providerKey) {
		t.Fatal("the key is not in the runner's environment, so the test proves nothing")
	}
	if _, err := h.run(t, implement("feature"), review("revise", 1), idle, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("no agent calls")
	}
	for i, req := range h.agent.calls {
		if envValue(req.Env, "FUGARO_STATE_DIR") == "" {
			t.Errorf("call %d: the env is not the runner's built one: %v", i, req.Env)
		}
		for _, kv := range req.Env {
			if strings.Contains(kv, providerKey) || config.IsProviderKeyEnv(strings.SplitN(kv, "=", 2)[0]) {
				t.Errorf("call %d: the provider key is in the agent's env: %s", i, kv)
			}
		}
	}
}

// TestKeyNotInResultJSON: a key the agent got hold of (it is in the runner's
// /proc environ) and echoes into its transcript, stderr or the PR text it
// writes (title, body, the report comment) is stored in no bucket object,
// result.json included, is in no PR text the provider got, and is in no log
// line the runner wrote.
func TestKeyNotInResultJSON(t *testing.T) {
	h := newHarness(t, "", nil)
	withProviderKey(h)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Transcript.Write([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"OPENROUTER=` + providerKey + `"}]}}` + "\n"))
		_, _ = req.Stderr.Write([]byte(providerKey + "\n"))
		res, err := implement("feature")(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if werr := os.WriteFile(pr, []byte("# Add feature "+providerKey+"\n\nBody "+providerKey+"."), 0o644); werr != nil {
			t.Fatal(werr)
		}
		res.Text = "done " + providerKey
		return res, err
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	pr := onlyPR(t, h.provider)
	if got, _ := json.Marshal(pr); strings.Contains(string(got), providerKey) {
		t.Fatalf("the provider key reached the PR: %s", got)
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
		"trailing LF":    {orProvider.SecretEnv() + "=" + providerKey + "\n"},
		"trailing CRLF":  {orProvider.SecretEnv() + "=" + providerKey + "\r\n"},
		"interior space": {orProvider.SecretEnv() + "=sk-or-v1 0123456789"},
		"tab":            {orProvider.SecretEnv() + "=sk-or-v1\t0123456789"},
		"control char":   {orProvider.SecretEnv() + "=sk-or-v1\x01-0123456789"},
		"non-ASCII":      {orProvider.SecretEnv() + "=sk-or-v1-0123456789-\u00e9"},
	} {
		_, _, err := runner.ProviderCredential(env, orProvider)
		if err == nil || strings.Contains(err.Error(), providerKey) || !strings.Contains(err.Error(), "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// The shortest redactable key (4 bytes) is accepted; 3 is not.
func TestProviderCredentialLength(t *testing.T) {
	if _, v, err := runner.ProviderCredential([]string{orProvider.SecretEnv() + "=abcd"}, orProvider); err != nil || v != "abcd" {
		t.Errorf("4 bytes: %q, %v", v, err)
	}
	if _, _, err := runner.ProviderCredential([]string{orProvider.SecretEnv() + "=abc"}, orProvider); err == nil {
		t.Error("3 bytes accepted")
	}
}
