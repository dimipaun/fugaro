package infra

// D2313 (docs/shared-jobs-default/reconciliation.md, plan task M1): a
// repository's `secrets:` is accepted and ignored with a warning, "in a way
// similar to repository-level images" (D1900) — disabled for now, the
// containers and their values left alone, the code kept so the feature can
// return.
//
// internal/cli's secrets_disabled_test.go is one test per surface a PERSON
// reads: the warning validate prints, the notice the secrets commands answer,
// the credentials service's record, the Terraform input, and the secrets stage
// that takes no value for such a name. The two surfaces this package owns are
// the other half of that last one, and they are what decides whether a
// repository declaring `workflows.*.secrets` can be brought up at all:
// Readiness, which must not hold its job back for a value no store can write,
// and SecretCommands, which must not list a step for one. Both are here, each
// beside the control that shows the machinery the ruling keeps.
//
// TestMain clears the door for every other test in this package, exactly as
// internal/cli's TestMain does: what is here is the ruling alone.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// ignoredProbe is the name these tests are about: the secret
// deploy/sandbox/fugaro.yaml declares in the web workflow's own `secrets:`
// (SANDBOX_PROBE), which is a repository-level secret by D2313's definition and
// — with no project-wide entry for it — one no shared job mounts.
const ignoredProbe = "sandbox-probe"

// repoSecretsOn turns D2313's door on for one test: the ruling as the CLI
// ships it, which TestMain clears for every other test in this package.
func repoSecretsOn(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { config.RepoSecretsOff = false })
	config.RepoSecretsOff = true
}

// repoSecretsOff clears it again inside a test that turned it on, for the
// control: the machinery the ruling keeps, read the way it read before the
// ruling.
func repoSecretsOff(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { config.RepoSecretsOff = true })
	config.RepoSecretsOff = false
}

// versionedExcept seeds every container the spec carries with a version but the
// names given, which are left versionless: the state a repository is in when the
// only thing standing between it and a deployed job is those names.
func versionedExcept(t *testing.T, f *cloud, spec RepoSpec, names ...string) {
	t.Helper()
	for logical, id := range spec.Secrets {
		var value []byte
		if !slices.Contains(names, logical) {
			value = []byte("value-" + logical)
		}
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), value)
	}
}

// TestReadinessBringsUpARepositoryThatDeclaresASecret answers the finding that a
// repository declaring `workflows.*.secrets` could not be brought up under
// D2313 at all: the secrets stage took no value for such a name while Readiness
// still held the job back for one and printed a fix (`fugaro secrets set NAME
// --repo OWNER/NAME`) the same ruling refuses — so the operator ran a command
// that stored nothing and the job waited for it forever.
//
// What the ruling makes of such a name is a fact about the file and not a step:
// no shared job mounts it, nothing can be stored for it, and the job deploys
// without it. The assertions below are that sentence — the job is deployable,
// the mount is gone while the container stays (D2313 destroys nothing and keeps
// the value the returning feature wants), and no step is listed — with the one
// line the person is owed instead, in the ruling's own words.
func TestReadinessBringsUpARepositoryThatDeclaresASecret(t *testing.T) {
	repoSecretsOn(t)
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	versionedExcept(t, f, spec, ignoredProbe)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID, strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")

	got, missing, err := Readiness(ctx, f.c, spec, ex, false, "update")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Workflows["web"].DeployJob {
		t.Errorf("web is not deployable while its own declared secret has no value: missing = %v", missing)
	}
	if _, ok := got.Workflows["web"].SecretEnv["SANDBOX_PROBE"]; ok {
		t.Errorf("the job still mounts SANDBOX_PROBE (%v): a secret_key_ref on a container with no version is a job Cloud Run refuses to create, and no shared job reads the name (D2313)", got.Workflows["web"].SecretEnv)
	}
	if got.Secrets[ignoredProbe] != spec.Secrets[ignoredProbe] {
		t.Errorf("the container moved to %q, want %q: the ruling leaves every container and its value where it is", got.Secrets[ignoredProbe], spec.Secrets[ignoredProbe])
	}
	for _, m := range missing {
		if m.Kind == MissingSecret {
			t.Errorf("the job is still held back for a secret: %v", m)
		}
	}
	notes := 0
	for _, m := range missing {
		if m.Kind != MissingNote || !strings.Contains(m.Reason, ignoredProbe) {
			continue
		}
		notes++
		if want := config.RepoSecretsIgnoredNotice([]string{ignoredProbe}); m.Reason != want {
			t.Errorf("the note reads\n  %s\nwant the ruling's own sentence\n  %s", m.Reason, want)
		}
	}
	if notes != 1 {
		t.Errorf("%d note(s) about %s, want exactly one for the repository: %v", notes, ignoredProbe, missing)
	}
	// And no step: the line it would have carried is the store the ruling
	// answers with a notice, so the note above is the whole of what a person is
	// told about the name.
	versions := map[string]bool{}
	for logical := range got.Secrets {
		versions[logical] = logical != ignoredProbe
	}
	for _, st := range SecretCommands(got, versions, "fugaro", "update", func(s string) string { return s }) {
		if st.Name == ignoredProbe {
			t.Errorf("a step is still listed for %s: %v", ignoredProbe, st.Commands)
		}
	}
	// And the first image build is offered, not held back by the ignored name.
	// NeedsBuild reads the mounts of the spec Readiness returned, where the name
	// is no longer one (dropMount above), so it holds nothing on it: the one
	// command that brings a repository up -- update builds what NeedsBuild names
	// and plans again, and that plan is the job -- can do it. A gate here would
	// leave the job with no image and no offered way to get one: the same
	// "cannot be brought up" the mount and the gate above are about.
	names, err := NeedsBuild(ctx, f.c, got, versions)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "web") {
		t.Errorf("the first image build of web is not offered (%v): a name the ruling ignores holds it back, and the job it is meant to deploy can never come", names)
	}

	// The control: with the door off, the same state is what it was before the
	// ruling — the job waits for the name and the fix is the repository's own
	// container — so what the assertions above pin is the ruling and not a
	// Readiness that stopped needing secrets at all.
	repoSecretsOff(t)
	before, missing, err := Readiness(ctx, f.c, spec, ex, false, "update")
	if err != nil {
		t.Fatal(err)
	}
	if before.Workflows["web"].DeployJob {
		t.Error("with the door off the job deploys without the secret its workflow declares: the machinery the ruling keeps is not reachable")
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingSecret && strings.Contains(m.Reason, "fugaro secrets set "+ignoredProbe+" --repo acme/sandbox")
	}) {
		t.Errorf("with the door off the fix is not the repository-scope store: %v", missing)
	}
	if _, ok := before.Workflows["web"].SecretEnv["SANDBOX_PROBE"]; !ok {
		t.Errorf("with the door off the job does not mount SANDBOX_PROBE: %v", before.Workflows["web"].SecretEnv)
	}
	// The build gate too: with the door off the name is required and missing, so
	// no build is offered for a job that could not run -- the machinery the
	// ruling keeps, and the reason the assertion above is about the ruling.
	names, err = NeedsBuild(ctx, f.c, before, versions)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "web") {
		t.Errorf("with the door off the first image build is offered for a workflow whose secret is missing (%v)", names)
	}
}

// TestProjectCoveredWorkflowSecretIsStillAStep is ignoredRepoSecret's other
// half, and the one that must not become collateral damage: a name the project
// holds — a `workflow_secrets` entry allow-listing this repository, a
// `project_secrets` entry, a provider's key, the node's own token — is a mount
// the job really has, so its absence IS a step, and the step is one that works.
// Reading the ruling as "every name a workflow declares is ignored" would take
// the run's credential away and leave a repository no way to say how to get it.
func TestProjectCoveredWorkflowSecretIsStillAStep(t *testing.T) {
	repoSecretsOn(t)
	spec, err := Repo(sandboxInputs(t, m5Additions+workflowSecretLC))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.ProjectSecretCandidates[ignoredProbe]; !ok {
		t.Fatalf("the fixture is not the state this case is about: the entry does not make %s project-wide for acme/sandbox", ignoredProbe)
	}
	versions := map[string]bool{}
	for logical := range spec.Secrets {
		versions[logical] = logical != ignoredProbe
	}
	var step SecretStep
	found := false
	for _, st := range SecretCommands(spec, versions, "fugaro", "update", func(s string) string { return s }) {
		if st.Name == ignoredProbe {
			step, found = st, true
		}
	}
	if !found {
		t.Fatal("no step for a name the project's own entry makes project-wide: a shared job mounts it, so its absence is a step")
	}
	// The command is SecretFix's answer for a candidate: the bare store, which
	// writes the project's copy — the one a run reads — rather than the
	// repository-scope store the ruling answers a notice for.
	if want := []string{"fugaro secrets set " + ignoredProbe + " < PATH-TO-THE-VALUE-FILE"}; !slices.Equal(step.Commands, want) {
		t.Errorf("the step's commands = %v, want %v", step.Commands, want)
	}
	if ignoredRepoSecret(spec, ignoredProbe) {
		t.Error("the ruling ignores a name this repository may mount the project's copy of")
	}
}
