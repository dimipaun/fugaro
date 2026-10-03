package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestProvidersEnvRoundTrip(t *testing.T) {
	p := orProvider
	p.AllowDataTo = []string{"acme/app"}
	q := orProvider
	q.Secret, q.Models, q.AllowDataTo = "other-key", []string{"qwen/*"}, []string{"acme/else"}
	all := map[string]config.ModelProvider{"openrouter": p, "other": q}
	v, err := runner.ProvidersEnv(all, "Acme/App")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v, "other-key") {
		t.Fatalf("a provider the repo may not use: %s", v)
	}
	got, err := runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: v}))
	if err != nil || len(got) != 1 || got["openrouter"].Secret != p.Secret || got["openrouter"].Models[0] != "deepseek/*" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if v, _ := runner.ProvidersEnv(all, "acme/none"); v != "" {
		t.Fatalf("no provider allows it, got %s", v)
	}
	if got, err := runner.ProvidersFromEnv(lookup(nil)); got != nil || err != nil {
		t.Fatalf("unset: %v %v", got, err)
	}
}

// A damaged variable is an error, never "no providers".
func TestProvidersFromEnvRefusesDamage(t *testing.T) {
	for name, v := range map[string]string{
		"not json":      `{`,
		"empty":         ``,
		"unknown field": `{"a":{"kind":"anthropic-compat","base_url":"https://x.example","auth":"bearer","secret":"a-key","models":["a/*"],"key":"sk-123"}}`,
		"invalid":       `{"a":{"kind":"anthropic-compat","base_url":"http://x.example","auth":"bearer","secret":"a-key","models":["a/*"]}}`,
	} {
		if got, err := runner.ProvidersFromEnv(lookup(map[string]string{runner.ModelProvidersEnv: v})); err == nil {
			t.Errorf("%s: accepted %+v", name, got)
		}
	}
}
