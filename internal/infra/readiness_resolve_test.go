package infra

import (
	"encoding/json"
	"slices"
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

// TestReadinessProjectSecretGateNeedsDiscoveryConfirmation: the project copy
// of a candidate resolves only when discovery confirmed a live,
// correctly-labelled secret at its ID (readiness.go's ex.ProjectSecretsOK
// gate). The gate is what keeps Readiness from resolving a project-wide
// copy it never saw: discovery is the one place the labels are checked, and
// a candidate the config computes but discovery did not vouch for — the
// secret is absent, or was never read — must not be mounted into a job.
//
// Handing Readiness an Existing that discovery did not fill in is exactly
// the state the gate exists for, and no live cloud is needed for the
// resolution itself: the secret at the candidate ID is live and correctly
// labelled, so without the gate it resolves.
//
// Mutation (run, restore): drop the `if !ex.ProjectSecretsOK[logical]`
// check from readiness.go's resolveProject — ProjectSecrets names the
// project copy, which discovery never confirmed, and this test fails.
func TestReadinessProjectSecretGateNeedsDiscoveryConfirmation(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	// The repository copy has no enabled version, so resolution falls to
	// the project copy; the project copy is live and correctly labelled, so
	// discovery *would* have confirmed it.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	want := f.seedProjectSecret(spec, "bitbucket-token")
	got, missing, err := Readiness(t.Context(), f.c, spec, Existing{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if id := got.ProjectSecrets["bitbucket-token"]; id != "" {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want none: discovery never confirmed the project copy (it is %s)", id, want)
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingSecret && strings.Contains(m.Reason, "bitbucket-token")
	}) {
		t.Errorf("missing = %v, want an unresolved bitbucket-token", missing)
	}
}

// Task 2.2 step 2: a project-covered name that is stored nowhere is reported
// with the command that stores it where the plan would resolve it -- the bare
// `fugaro secrets set <name>`, project scope by D2's default -- and never
// `--repo`, whose copy would then win over the project's forever.
func TestReadinessNamesTheProjectScopeFix(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	logical := "github-app-key"
	if _, ok := spec.ProjectSecretCandidates[logical]; !ok {
		t.Fatalf("no ProjectSecretCandidates entry for %s: this test needs a name the plan may resolve project-wide", logical)
	}
	// Neither copy has a version: the repository's container exists, empty.
	f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
	got, missing, _ := readinessOf(t, f, spec)
	if id, ok := got.ProjectSecrets[logical]; ok {
		t.Errorf("ProjectSecrets[%s] = %q, want no entry: nothing is stored", logical, id)
	}
	var reason string
	for _, m := range missing {
		if m.Kind == MissingSecret && strings.Contains(m.Reason, logical) {
			reason = m.Reason
		}
	}
	if reason == "" {
		t.Fatalf("missing = %v, want a MissingSecret naming %s", missing, logical)
	}
	if !strings.Contains(reason, "fugaro secrets set "+logical) || strings.Contains(reason, "--repo") {
		t.Errorf("reason = %q, want the project-scope fix (secrets set, no --repo)", reason)
	}
}
