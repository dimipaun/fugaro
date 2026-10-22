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
// installation's outputs carry once fugaro update's credentials stage has run
// (internal/infra.CredentialsService, D146).
func credentialsService() *CredentialsService {
	return &CredentialsService{Name: "fugaro-credentials", Region: "us-east5"}
}

// serviceHoldsTheAppKey is C2.2's release, in one line: the name joins
// config.ServiceHeldSecretNames and every drop written for it turns on at once
// (readiness.go's mount drop, Addesses()'s mirror, modules/workflow/sa.tf's
// accessor and mount rules, which hold their literals to that list). It is a
// package var, so a test can put the tree in the state that release will, and
// put it back.
func serviceHoldsTheAppKey(t *testing.T) {
	t.Helper()
	config.ServiceHeldSecretNames = []string{config.GitHubAppKeySecret}
	t.Cleanup(func() { config.ServiceHeldSecretNames = []string{} })
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

// TestTheAppKeyMountStaysWhileTheRunnerReadsTheKey is the SHIPPED state of
// C2.3's drop, and it is the state review finding F1 asked for (2026-10-21):
// with the credentials service on, the job keeps the App key's mount, its
// secret id and its redaction entry, exactly as it has them with the opt-in
// off.
//
// C2.3 removes the job account's secretAccessor on the App key once an
// installation opts in, and a container that still declared
// FUGARO_GITHUB_APP_PRIVATE_KEY would then be created with a secret reference
// that account cannot resolve: Cloud Run refuses to create such a job, so
// EVERY GitHub run of that installation would fail. The mount has to go with
// the accessor -- but only once the runner stops READING the key, which is
// C2.2 (it fetches the run's installation tokens from the service instead),
// and C2.2 is not in this tree. Until it is, the job keeps both: the runner
// reads the key from its environment exactly as it always has
// (internal/gitprov/providers.appKey requires FUGARO_GITHUB_APP_PRIVATE_KEY or
// its _FILE twin and fails the provider construction without one), and the
// opt-in changes nothing about the job's plan.
//
// What turns the drop on is one name in config.ServiceHeldSecretNames, which
// C2.2's release adds -- see the test below for that side of the switch, and
// TestServiceHeldSecretNamesAreEmptyUntilTheJobGivesTheAppKeyUp for the value
// pinned in internal/config.
//
// The mutations are the gate and the list: dropping the
// `CredentialsService != nil` test in readiness.go fails the "off" premises
// below (D147 turned inside out: with the opt-in off nothing changes), and
// filling config.ServiceHeldSecretNames without C2.2's runner fails the "on"
// rows here -- which is the break this state exists to prevent.
func TestTheAppKeyMountStaysWhileTheRunnerReadsTheKey(t *testing.T) {
	spec := repoRootSpec(t, repoRootOAuthAgent)
	f := newCloud(t)
	seedRepoReady(t, f, spec)

	off, offMissing, _ := readinessOf(t, f, spec)
	onSpec := spec
	onSpec.Installation.CredentialsService = credentialsService()
	on, _, _ := readinessOf(t, f, onSpec)

	// The fixture's own premises, or everything below would pass vacuously:
	// every workflow mounts the key and names its secret, and the spec carries
	// its container.
	for _, key := range []string{"web", "web_bmb"} {
		if ws := off.Workflows[key]; ws.SecretEnv[appKeyEnv] != appKeyLogical || ws.SecretIDs[appKeyLogical] == "" {
			t.Fatalf("with the credentials service off workflow %s mounts %v / names %q: the fixture must carry the App key's container and mount", key, ws.SecretEnv, ws.SecretIDs[appKeyLogical])
		}
	}
	if spec.Secrets[appKeyLogical] == "" {
		t.Fatal("the fixture has no App key container: the container assertions below would pass vacuously")
	}

	for _, key := range []string{"web", "web_bmb"} {
		offWS, onWS := off.Workflows[key], on.Workflows[key]
		if got := onWS.SecretEnv[appKeyEnv]; got != appKeyLogical {
			t.Errorf("with the credentials service on workflow %s mounts %s -> %q, want it kept: the runner still reads the key, so the mount cannot go before C2.2 (review finding F1)", key, appKeyEnv, got)
		}
		if offWS.SecretIDs[appKeyLogical] == "" || onWS.SecretIDs[appKeyLogical] != offWS.SecretIDs[appKeyLogical] {
			t.Errorf("with the credentials service on workflow %s names the App key secret %q, want %q: the mount that stays still points at the same container (F1)", key, onWS.SecretIDs[appKeyLogical], offWS.SecretIDs[appKeyLogical])
		}
		if got, want := onWS.Env[runner.SecretEnvsVar], offWS.Env[runner.SecretEnvsVar]; got != want {
			t.Errorf("with the credentials service on workflow %s's %s = %q, want %q: the redactor masks exactly what the job mounts, and the mount has not moved", key, runner.SecretEnvsVar, got, want)
		}
		// Nothing else moves either: the plan's mounts are the off plan's.
		if !maps.Equal(onWS.SecretEnv, offWS.SecretEnv) {
			t.Errorf("with the credentials service on workflow %s mounts %v, want %v: with nothing on the service-held list, the opt-in takes no mount away (D147, F1)", key, onWS.SecretEnv, offWS.SecretEnv)
		}
	}

	// The container, its operator grants and the project-wide resolution are
	// the build account's and the service's, and the opt-in does not take them
	// away -- this is why the drop lives in Readiness and not in the
	// pre-readiness spec that feeds rs.Secrets and ProjectSecretCandidates.
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

	// The run works: the job is deployed with the key mounted, and the key's
	// absent version still holds it back -- the same gate the opt-in off has,
	// because the job reads the key either way.
	if !off.Workflows["web"].DeployJob {
		t.Errorf("the fixture's job is not deployed with the credentials service off: %v", offMissing)
	}
	if !on.Workflows["web"].DeployJob {
		t.Errorf("the job is not deployed with the credentials service on and the runner reading the key: the opt-in must not break a GitHub run (F1)")
	}
	gap := newCloud(t)
	seedRepoReady(t, gap, spec, appKeyLogical)
	gapOff, missing, _ := readinessOf(t, gap, spec)
	if gapOff.Workflows["web"].DeployJob {
		t.Errorf("the job is deployed with no App key stored and the credentials service off: %v", missing)
	}
	gapOn, missing, _ := readinessOf(t, gap, onSpec)
	if gapOn.Workflows["web"].DeployJob {
		t.Errorf("the job is deployed with no App key stored and the credentials service on: the job mounts the key and reads it, so its version is still a fact about this deploy -- the opt-in must not break a GitHub run (F1): %v", missing)
	}
}

// TestReadinessDropsTheAppKeyMountOnceTheServiceHoldsIt is the other side of
// the same switch: with C2.2's release in place -- config.ServiceHeldSecretNames
// names the App key, which is what that release lands -- the opt-in takes the
// mount away, together with the secret id and the redaction entry, and with it
// the gate on the key's version. The accessor goes in the same plan
// (modules/workflow/sa.tf, which reads the same list).
//
// It is the test the drop's own brief asked for ("when the opt-in is on, the
// job spec stops mounting the App key"), kept against the state that release
// will put the tree in rather than against this branch's, because on this
// branch the drop must not fire: C2.2's runner is not here, and a job whose
// account has no accessor on a key its container still mounts is a job Cloud
// Run refuses to create.
//
// The mutation is the drop: delete the `for _, logical := range
// config.ServiceHeldSecretNames` loop in readiness.go, or its
// `CredentialsService != nil` test, and the rows below fail.
func TestReadinessDropsTheAppKeyMountOnceTheServiceHoldsIt(t *testing.T) {
	serviceHoldsTheAppKey(t)

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
