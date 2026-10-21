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
	// The deploy is right, but it is not silent: with a budget that isn't
	// enforcing there is no gateway, so every call goes direct and the key
	// is needed -- a note says so, and names the fix. It gates nothing.
	note := ""
	for _, m := range missing {
		if m.Kind == MissingNote && strings.Contains(m.Reason, config.AnthropicSecret) {
			note = m.String()
		} else if strings.Contains(m.Reason, config.AnthropicSecret) {
			t.Errorf("the missing key is still a gate: %v", m)
		}
	}
	if note == "" {
		t.Fatalf("no note about the absent key: %v", missing)
	}
	for _, want := range []string{"deploys without it", "fugaro secrets set anthropic-api-key", "fugaro update"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q: %s", want, note)
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

// TestReadinessOptionalKeyResolvesProjectWide: the key is optional, not
// unconditionally dropped. When the repository copy has no version but the
// project-wide one does -- this repository may use it -- the mount stays and
// the project copy resolves it, exactly as for any other secret
// (docs/design/project-secrets.md §5).
func TestReadinessOptionalKeyResolvesProjectWide(t *testing.T) {
	in := sandboxInputs(t, m5Additions+`project_secrets:
  anthropic-api-key: { allow_data_to: [acme/sandbox] }
`)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	if web.SecretEnv["ANTHROPIC_API_KEY"] != config.AnthropicSecret {
		t.Fatalf("the fixture does not mount the Anthropic key: %v", web.SecretEnv)
	}
	if _, ok := spec.ProjectSecretCandidates[config.AnthropicSecret]; !ok {
		t.Fatalf("the fixture has no project-wide candidate for the key: %v", spec.ProjectSecretCandidates)
	}

	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		if logical == config.AnthropicSecret {
			continue // the repository copy has no version anywhere
		}
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	// Only the project-wide copy has a version.
	projectID := f.seedProjectSecret(spec, config.AnthropicSecret)
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID,
		strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")

	got, missing, _ := readinessOf(t, f, spec)
	ws := got.Workflows["web"]
	if !ws.DeployJob {
		t.Errorf("the job is not deployed with the project-wide key: %v", missing)
	}
	if ws.SecretEnv["ANTHROPIC_API_KEY"] != config.AnthropicSecret {
		t.Errorf("the mount was dropped though the project-wide copy has a version: %v", ws.SecretEnv)
	}
	if got.ProjectSecrets[config.AnthropicSecret] != projectID {
		t.Errorf("ProjectSecrets = %v, want the key -> %s", got.ProjectSecrets, projectID)
	}
	if !strings.Contains(ws.Env[runner.SecretEnvsVar], "ANTHROPIC_API_KEY") {
		t.Errorf("%s = %q, want the mounted key named", runner.SecretEnvsVar, ws.Env[runner.SecretEnvsVar])
	}
	for _, m := range missing {
		if strings.Contains(m.Reason, config.AnthropicSecret) {
			t.Errorf("the project-wide key is still reported missing: %v", m)
		}
	}
}

// TestReadinessOptionalKeyNoteOnAnExistingJob (review Low 7): the P7a path
// refactored dropMount too, so an existing job with no key must behave like a
// new one: it stays deployed, the mount goes, and the note still says why.
func TestReadinessOptionalKeyNoteOnAnExistingJob(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]

	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		if logical == config.AnthropicSecret {
			continue
		}
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID,
		strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")
	// The job already exists, with the key in its current environment: the
	// update must take the mount out, never fail the whole job.
	f.run.SetJob(web.Job, web.Labels, web.Image+":latest")

	got, missing, _ := readinessOf(t, f, spec)
	ws := got.Workflows["web"]
	if !ws.DeployJob {
		t.Fatalf("an existing job was not kept deployed: %v", missing)
	}
	if _, mounted := ws.SecretEnv["ANTHROPIC_API_KEY"]; mounted {
		t.Errorf("the existing job still mounts the key: %v", ws.SecretEnv)
	}
	var note bool
	for _, m := range missing {
		if m.Kind == MissingNote && strings.Contains(m.Reason, config.AnthropicSecret) {
			note = true
		}
		if m.Kind == MissingSecret && strings.Contains(m.Reason, config.AnthropicSecret) {
			t.Errorf("the absent key still gates the existing job: %v", m)
		}
	}
	if !note {
		t.Errorf("no note about the absent key: %v", missing)
	}
}
