package infra

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
)

// TestReadinessOptionalAnthropicKey (design docs/design/project-centric.md
// §6.1): the Anthropic key is mounted whenever the secret has a version, a
// fact about data rather than about policy, and its absence never gates the
// job: a run whose models all route through providers needs no such key, and
// a call that needs one is refused by the gateway by name.
func TestReadinessOptionalAnthropicKey(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	if web.SecretEnv["ANTHROPIC_API_KEY"] != config.AnthropicSecret {
		t.Fatalf("the fixture does not mount the Anthropic key: %v", web.SecretEnv)
	}
	keyID, ok := spec.Secrets[config.AnthropicSecret]
	if !ok {
		t.Fatal("the spec does not carry the Anthropic secret's ID")
	}

	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		if logical == config.AnthropicSecret {
			continue // no version anywhere: the case under test
		}
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	// The image is built, so nothing but the secrets can hold the job back.
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID,
		strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")

	got, missing, _ := readinessOf(t, f, spec)
	ws := got.Workflows["web"]
	if !ws.DeployJob {
		t.Errorf("the job is not deployed without the optional key: %v", missing)
	}
	if _, mounted := ws.SecretEnv["ANTHROPIC_API_KEY"]; mounted {
		t.Error("the Anthropic key is mounted with no version: Cloud Run would refuse to create the job")
	}
	if _, id := ws.SecretIDs[config.AnthropicSecret]; id {
		t.Error("the Anthropic secret's ID is still on the job")
	}
	if names := ws.Env[runner.SecretEnvsVar]; strings.Contains(names, "ANTHROPIC_API_KEY") {
		t.Errorf("%s = %q, want the variables that are left", runner.SecretEnvsVar, names)
	}
	for _, m := range missing {
		if strings.Contains(m.Reason, config.AnthropicSecret) && !m.Check {
			t.Errorf("the missing key is still a gate: %v", m)
		}
	}

	// Once the secret has a version the mount comes back: the flip is data,
	// so no routing setting is involved.
	f.sm.Seed(keyID, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, config.AnthropicSecret), []byte("v"))
	got, missing, _ = readinessOf(t, f, spec)
	ws = got.Workflows["web"]
	if ws.SecretEnv["ANTHROPIC_API_KEY"] != config.AnthropicSecret || ws.SecretIDs[config.AnthropicSecret] != keyID {
		t.Errorf("with a version: secret_env %v, secret_ids %v", ws.SecretEnv, ws.SecretIDs)
	}
	if !strings.Contains(ws.Env[runner.SecretEnvsVar], "ANTHROPIC_API_KEY") {
		t.Errorf("%s = %q, want the mounted key named", runner.SecretEnvsVar, ws.Env[runner.SecretEnvsVar])
	}
	if !ws.DeployJob {
		t.Errorf("the job is not deployed with every secret present: %v", missing)
	}
}

// TestOptionalSecretIsOnlyTheAnthropicKey: every other secret a job mounts is
// required, so its absence still holds the job back.
func TestOptionalSecretIsOnlyTheAnthropicKey(t *testing.T) {
	if !OptionalSecret(config.AnthropicSecret) {
		t.Error("the Anthropic key is not optional")
	}
	for logical := range config.ReservedSecrets {
		if logical == config.AnthropicSecret {
			continue
		}
		if OptionalSecret(logical) {
			t.Errorf("%s is optional, and it must not be", logical)
		}
	}
}
