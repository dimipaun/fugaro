package infra

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
)

// appKey is the App key's contract in one place: the logical name the
// platform holds it under and the variable a job mounts it as.
var (
	appKeyLogical = config.GitHubAppKeySecret
	appKeyEnv     = config.ReservedSecrets[config.GitHubAppKeySecret]
)

// credentialsService is the opt-in the tests here turn on: what an
// installation's outputs carry once fugaro init's credentials stage has run
// (internal/infra.CredentialsService, D146).
func credentialsService() *CredentialsService {
	return &CredentialsService{Name: "fugaro-credentials", Region: "us-east5"}
}

// seedRepoReady stores the fixture's secrets and image, so that nothing but
// the secrets under test can hold the job back -- the harness
// TestReadinessOptionalAnthropicKey uses, without the optional-key exception.
func seedRepoReady(t *testing.T, f *cloud, spec RepoSpec, skip ...string) {
	t.Helper()
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		if slices.Contains(skip, logical) {
			continue
		}
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	web := spec.Workflows["web"]
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID,
		strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")
}

// TestReadinessDropsTheAppKeyMountOnceTheServiceHoldsIt is the shipped state
// of C2.3's drop, now that C2.2's release is in this tree: with the credentials
// service on, the job stops mounting the App key — mount, secret id and
// redaction entry — and the gate on the key's version goes with it, because the
// job no longer reads the key (the runner fetches the run's installation tokens
// from the service instead, C2.2). The accessor goes in the same plan
// (modules/workflow/sa.tf, which reads the same list,
// config.ServiceHeldSecretNames).
//
// It is the test the drop's own brief asked for ("when the opt-in is on, the
// job spec stops mounting the App key"). Before C2.2 landed the same test was
// held against the state that release would put the tree in, because the drop
// must not fire while the job still read the key: a job whose account has no
// accessor on a key its container still mounts is a job Cloud Run refuses to
// create (review finding F1, 2026-10-21).
//
// The mutation is the drop: delete the `for _, logical := range
// config.ServiceHeldSecretNames` loop in readiness.go, or its
// `CredentialsService != nil` test, and the rows below fail.
func TestReadinessDropsTheAppKeyMountOnceTheServiceHoldsIt(t *testing.T) {
	spec := repoRootSpec(t, repoRootOAuthAgent)
	f := newCloud(t)
	seedRepoReady(t, f, spec)

	off, offMissing, _ := readinessOf(t, f, spec)
	onSpec := spec
	onSpec.Installation.CredentialsService = credentialsService()
	on, _, _ := readinessOf(t, f, onSpec)

	for _, key := range []string{"web", "web_bmb"} {
		ws := off.Workflows[key]
		if ws.SecretEnv[appKeyEnv] != appKeyLogical {
			t.Fatalf("with the credentials service off workflow %s mounts %v, want %s among them", key, ws.SecretEnv, appKeyEnv)
		}
	}
	for _, key := range []string{"web", "web_bmb"} {
		offWS, onWS := off.Workflows[key], on.Workflows[key]
		if _, mounted := onWS.SecretEnv[appKeyEnv]; mounted {
			t.Errorf("with the credentials service on workflow %s still mounts %s: its account keeps no accessor on the App key, so the mount goes with the accessor (C2.3, F1)", key, appKeyEnv)
		}
		if id, named := onWS.SecretIDs[appKeyLogical]; named {
			t.Errorf("with the credentials service on workflow %s still names the App key secret %q: the job has no mount of it left to point at", key, id)
		}
		if got := onWS.Env[runner.SecretEnvsVar]; strings.Contains(got, appKeyEnv) {
			t.Errorf("with the credentials service on workflow %s's %s = %q, want the variables that are left: the redactor masks exactly what the job mounts", key, runner.SecretEnvsVar, got)
		}
		// The drop is the App key and nothing else: every other mount of that
		// workflow is exactly what it was with the opt-in off.
		want := map[string]string{}
		for env, logical := range offWS.SecretEnv {
			if env != appKeyEnv {
				want[env] = logical
			}
		}
		if !maps.Equal(onWS.SecretEnv, want) {
			t.Errorf("with the credentials service on workflow %s mounts %v, want %v: only the App key goes (C1.20 is the task that drops the model credentials' mounts)", key, onWS.SecretEnv, want)
		}
	}

	// The App key's CONTAINER, its operator grants and the project-wide
	// resolution are the build account's and the service's: the opt-in takes
	// none of them away, which is why the drop lives in Readiness and not in
	// the pre-readiness spec that feeds rs.Secrets and ProjectSecretCandidates.
	if got, want := on.Secrets[appKeyLogical], spec.Secrets[appKeyLogical]; got != want {
		t.Errorf("rs.Secrets[%s] = %q with the credentials service on, want %q: the container stays -- the build account reads it and C2.1's mint needs it (D148, F3)", appKeyLogical, got, want)
	}
	if _, ok := on.ProjectSecretCandidates[appKeyLogical]; ok != func() bool { _, off := spec.ProjectSecretCandidates[appKeyLogical]; return off }() {
		t.Errorf("ProjectSecretCandidates[%s] = %v with the credentials service on, want what it was with the opt-in off: the operator's project-wide copy still resolves (project-secrets.md §5)", appKeyLogical, on.ProjectSecretCandidates[appKeyLogical])
	}
	for _, logical := range off.BuildSecrets {
		if !slices.Contains(on.BuildSecrets, logical) {
			t.Errorf("BuildSecrets = %v with the credentials service on, want %v: the build account's own secrets, the App key included, do not move (D148, F3)", on.BuildSecrets, off.BuildSecrets)
		}
	}
	// The check job runs as the BUILD account, whose accessor on the App key
	// stays, so its own mount stays.
	if on.Check == nil {
		t.Fatal("the fixture deploys no check job: the assertion below would pass vacuously")
	}
	if got := on.Check.SecretEnv[appKeyEnv]; got != appKeyLogical {
		t.Errorf("the check job's SecretEnv = %v with the credentials service on, want %s -> %s: it runs as the build account, which keeps the accessor (D148, F3)", on.Check.SecretEnv, appKeyEnv, appKeyLogical)
	}

	// With the drop on, the job no longer reads the key, so its absent version
	// stops holding the job back: the service mints the run's installation
	// tokens instead (C2.1).
	if !off.Workflows["web"].DeployJob {
		t.Errorf("the fixture's job is not deployed with the credentials service off: %v", offMissing)
	}
	gap := newCloud(t)
	seedRepoReady(t, gap, spec, appKeyLogical)
	noKey, missing, _ := readinessOf(t, gap, spec)
	if noKey.Workflows["web"].DeployJob {
		t.Errorf("the job is deployed with no App key stored and the credentials service off: %v", missing)
	}
	onGapSpec := spec
	onGapSpec.Installation.CredentialsService = credentialsService()
	onGap, missing, _ := readinessOf(t, gap, onGapSpec)
	if !onGap.Workflows["web"].DeployJob {
		t.Errorf("the job is held back by the App key's absent version with the credentials service on, though it mounts no key: %v", missing)
	}

	// And the mirror moves with the module: the same plan's Addresses() drops
	// the job's accessor on the name, which is what Terraform's job_secret_names
	// drops in the same plan (TestServiceHeldSecretsMatchTheCredentialsService
	// holds the module's literal to this list).
	gone := `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["` + appKeyLogical + `"]`
	if !slices.Contains(off.Addresses(), gone) {
		t.Fatalf("the off plan does not produce %s: the mirror assertion below would pass vacuously", gone)
	}
	if slices.Contains(on.Addresses(), gone) {
		t.Errorf("the on plan still produces %s: the job's account keeps no accessor on the App key once the service holds it (C2.3)", gone)
	}
}
