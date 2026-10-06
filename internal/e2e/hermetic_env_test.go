package e2e

import (
	"os"
	"strings"
	"testing"
)

// The hermetic environment is the child's alone: this process (and so any
// helper it starts, such as the docker base-image build) keeps the real one.
func TestHermeticEnvIsPerSubprocess(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://real-proxy.example:3128")
	env := hermeticEnv(os.Environ())
	last := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		last[k] = v
	}
	if last["HTTPS_PROXY"] != "http://127.0.0.1:1" || !strings.HasPrefix(last["GOOGLE_APPLICATION_CREDENTIALS"], "/nonexistent/") {
		t.Errorf("the child environment is not hermetic: %v", last)
	}
	if os.Getenv("HTTPS_PROXY") != "http://real-proxy.example:3128" || strings.Contains(os.Getenv("GCE_METADATA_HOST"), "127.0.0.1:1") {
		t.Errorf("the test process's own environment was changed")
	}
}
