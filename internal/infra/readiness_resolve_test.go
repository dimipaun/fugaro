package infra

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// resolveSM is a Secret Manager client on the rig's fake, for the live
// resolution helpers' own tests (ResolveSecretID, GrantedSecretID).
func resolveSM(t *testing.T, f *cloud) *secretmanager.Service {
	t.Helper()
	sm, err := gcp.NewSecretManager(t.Context(), f.options(nil))
	if err != nil {
		t.Fatal(err)
	}
	return sm
}

// The precedence itself, extracted from Readiness: a repository copy with an
// enabled version wins even when the project copy has one too.
func TestResolveSecretIDPrefersRepoWhenVersioned(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("repo"))
	f.sm.Seed("project-id", nil, []byte("project"))
	id, ok, err := ResolveSecretID(t.Context(), resolveSM(t, f), "proj-1234", "repo-id", "project-id")
	if err != nil || !ok || id != "repo-id" {
		t.Fatalf("id %q, hasVersion %v, err %v: want the repository's own copy", id, ok, err)
	}
}

func TestResolveSecretIDFallsBackToProject(t *testing.T) {
	f := newCloud(t)
	// The repository copy exists but has no enabled version.
	f.sm.Seed("repo-id", nil, nil)
	f.sm.Seed("project-id", nil, []byte("project"))
	id, ok, err := ResolveSecretID(t.Context(), resolveSM(t, f), "proj-1234", "repo-id", "project-id")
	if err != nil || !ok || id != "project-id" {
		t.Fatalf("id %q, hasVersion %v, err %v: want the project copy", id, ok, err)
	}
}

func TestResolveSecretIDNoCandidateStaysRepoOnly(t *testing.T) {
	f := newCloud(t)
	// No version anywhere, and no candidate at all: today's unconditional
	// repo-only behavior for a name this repository may not use
	// project-wide.
	id, ok, err := ResolveSecretID(t.Context(), resolveSM(t, f), "proj-1234", "repo-id", "")
	if err != nil || ok || id != "repo-id" {
		t.Fatalf("id %q, hasVersion %v, err %v: want the repository's own ID, versioned or not", id, ok, err)
	}
}

func TestResolveSecretIDNeitherHasVersion(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, nil)
	f.sm.Seed("project-id", nil, nil)
	id, ok, err := ResolveSecretID(t.Context(), resolveSM(t, f), "proj-1234", "repo-id", "project-id")
	if err != nil || ok || id != "repo-id" {
		t.Fatalf("id %q, hasVersion %v, err %v: want the repository's own ID with hasVersion false, so a caller can report a Missing-style error", id, ok, err)
	}
}

// Readiness's check branch carries the resolved ID into the check job's own
// spec, so the rebuild the job submits names the secret the job itself
// reads. The assertion is on the re-marshaled env var, never on a field
// nobody reads: an implementation that set CheckJobSpec.GitSecretID without
// writing it back would pass a struct check and fail this one.
func TestCheckJobSpecCarriesResolvedGitSecretID(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	if spec.Check == nil {
		t.Fatal("the sandbox fixture has no check job")
	}
	raw, ok := spec.Check.Env[CheckSpecEnv]
	if !ok {
		t.Fatalf("the fixture's check job has no %s", CheckSpecEnv)
	}
	var before CheckJobSpec
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	for _, logical := range spec.Check.SecretEnv {
		if logical == "bitbucket-token" {
			// The repository's own copy exists but has no version.
			f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
			continue
		}
		f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	want := f.seedProjectSecret(spec, "bitbucket-token")
	got, _, _ := readinessOf(t, f, spec)
	raw2, ok := got.Check.Env[CheckSpecEnv]
	if !ok {
		t.Fatalf("the resolved check job has no %s", CheckSpecEnv)
	}
	var after CheckJobSpec
	if err := json.Unmarshal([]byte(raw2), &after); err != nil {
		t.Fatal(err)
	}
	if after.GitSecretID != want {
		t.Errorf("git_secret_id = %q, want the project copy %q", after.GitSecretID, want)
	}
	if before.GitSecretID != "" || raw2 == raw {
		t.Errorf("%s did not change: %q -> %q", CheckSpecEnv, raw, raw2)
	}
	// The rest of the spec is untouched: this is a field written into an
	// existing env var, not a spec regenerated from scratch.
	if !strings.Contains(raw2, `"repo":"`+before.Repo+`"`) || len(after.Workflows) != len(before.Workflows) {
		t.Errorf("the re-marshaled spec lost what it had: %q", raw2)
	}
}
