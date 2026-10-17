package infra

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// Check 40 step 40i's bug, at the layer the run actually reads: the
// "Still missing" steps come from SecretVersions through SecretCommands, and
// a project-wide copy with an enabled version must cover the name there too.
// The repository's own container exists with no version at all, which is
// what Terraform's module leaves behind (it creates the container, and only
// the operator stores a value).
func TestProjectSecretCoverageProjectCopyIsNotMissing(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	f.seedProjectSecret(spec, "bitbucket-token")
	versions, err := SecretVersions(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !versions["bitbucket-token"] {
		t.Error("versions[bitbucket-token] = false, want the project-wide copy's enabled version to cover the name")
	}
	for _, st := range SecretCommands(spec, versions, "fugaro", func(s string) string { return s }) {
		if st.Name == "bitbucket-token" {
			t.Errorf("a covered name is still listed missing: %+v", st)
		}
	}
	// The one place Readiness decides it already answers yes, and the two
	// must not be able to drift: the run reads both.
	if _, missing, _ := readinessOf(t, f, spec); missingNames(missing, "bitbucket-token") {
		t.Errorf("Readiness = %v, want none for the project-covered name", missing)
	}
}

// A project container whose only version is DISABLED covers nothing: the
// container is there, and no enabled version is, so the name stays missing
// and the fix names the project-wide command -- the bare one, since D2 made
// project scope the default store (`--project-wide` is a synonym) -- never a
// repository copy, which would then win forever over the project's (the
// silent precedence flip, design docs/design/project-secrets.md §5).
func TestProjectSecretCoverageDisabledProjectVersionIsMissing(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	f.sm.DisableVersions(f.seedProjectSecret(spec, "bitbucket-token"))
	versions, err := SecretVersions(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if versions["bitbucket-token"] {
		t.Error("versions[bitbucket-token] = true, want false: every project version is disabled")
	}
	steps := SecretCommands(spec, versions, "fugaro", func(s string) string { return s })
	if !slices.ContainsFunc(steps, func(st SecretStep) bool { return st.Name == "bitbucket-token" }) {
		t.Fatalf("steps = %+v, want the name still missing", steps)
	}
	for _, st := range steps {
		if st.Name != "bitbucket-token" {
			continue
		}
		if !strings.Contains(st.Commands[0], "fugaro secrets set bitbucket-token ") ||
			strings.Contains(st.Commands[0], "--repo") {
			t.Errorf("step = %+v, want the project-wide command, never a repository copy", st)
		}
	}
	if _, missing, _ := readinessOf(t, f, spec); !missingNames(missing, "bitbucket-token") {
		t.Errorf("Readiness = %v, want the disabled-version name missing", missing)
	}
}

// A name that is a project candidate but is stored nowhere is still the real
// missing case, and its fix is the project-wide command (the bare one, D2's
// default store): storing a repository copy would resolve to it forever
// after.
func TestProjectSecretCoverageNeitherStoredNamesProjectWideFix(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	versions, err := SecretVersions(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if versions["bitbucket-token"] {
		t.Error("versions[bitbucket-token] = true with nothing stored anywhere")
	}
	var got string
	for _, st := range SecretCommands(spec, versions, "fugaro", func(s string) string { return s }) {
		if st.Name == "bitbucket-token" {
			got = st.Commands[0]
		}
	}
	if want := "fugaro secrets set bitbucket-token < PATH-TO-THE-VALUE-FILE"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// A repository the allow-list does not name gets no project coverage at all,
// whatever the project container holds, so its own copy is the only one that
// counts and its fix names --repo.
func TestProjectSecretCoverageUnlistedRepoIsNotCovered(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := projectSpec(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
	if _, ok := spec.ProjectSecretCandidates["bitbucket-token"]; ok {
		t.Fatal("an unlisted repository got a ProjectSecretCandidates entry")
	}
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	f.sm.Seed(gcp.ProjectSecretID(spec.Installation.ProjectName, "bitbucket-token"),
		with(managed, gcp.LabelProject, spec.Installation.ProjectName, gcp.LabelSecret, "bitbucket-token"), []byte("v"))
	versions, err := SecretVersions(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if versions["bitbucket-token"] {
		t.Error("versions[bitbucket-token] = true for a repository the allow-list excludes")
	}
	var got string
	for _, st := range SecretCommands(spec, versions, "fugaro", func(s string) string { return s }) {
		if st.Name == "bitbucket-token" {
			got = st.Commands[0]
		}
	}
	if want := "fugaro secrets set bitbucket-token --repo acme/sandbox < PATH-TO-THE-VALUE-FILE"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// missingNames reports whether any missing entry names logical.
func missingNames(missing []Missing, logical string) bool {
	return slices.ContainsFunc(missing, func(m Missing) bool { return strings.Contains(m.Reason, logical) })
}
