package config

import (
	"strings"
	"testing"
)

// TestSecretEnvMayNotShadowProcessBehavior pins S-M1: a workflow secret's
// variable must not be one that changes how the runner, git, a shell or a
// language runtime behaves, since the job mounts it into the runner's own
// environment.
func TestSecretEnvMayNotShadowProcessBehavior(t *testing.T) {
	for _, env := range []string{
		"GODEBUG", "GOFLAGS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "PATH", "HOME", "SHELL", "IFS", "TMPDIR",
		"PYTHONPATH", "PYTHONSTARTUP", "NODE_OPTIONS", "BASH_ENV", "ENV", "SSL_CERT_FILE",
		"GOOGLE_APPLICATION_CREDENTIALS", "CLOUD_RUN_EXECUTION", "DOCKER_HOST",
		"GIT_TOKEN", "FUGARO_RUN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_VERTEX",
	} {
		t.Run(env, func(t *testing.T) {
			y := minimalYAML + "    secrets:\n      - { name: tok, env: " + env + " }\n"
			_, ps := Parse([]byte(y))
			var found bool
			for _, p := range ps {
				if p.Path == "workflows.server.secrets[0].env" && strings.Contains(p.Message, "reserved") {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s accepted as a secret variable: %v", env, ps)
			}
		})
	}
}

// TestReservedEnvIgnoresCase: proxies are read in either case, so the
// check is case-blind even though secret variables must be upper-case.
func TestReservedEnvIgnoresCase(t *testing.T) {
	for _, env := range []string{"https_proxy", "no_proxy", "ld_preload", "GoDebug"} {
		if !ReservedEnv(env) {
			t.Errorf("ReservedEnv(%q) = false", env)
		}
	}
	for _, env := range []string{"NPM_TOKEN", "SENTRY_DSN", "GOOGLE_MAPS_KEY", "PATHWAY_KEY", "HOMEBREW_TOKEN"} {
		if ReservedEnv(env) {
			t.Errorf("ReservedEnv(%q) = true, want an ordinary secret name allowed", env)
		}
	}
}
