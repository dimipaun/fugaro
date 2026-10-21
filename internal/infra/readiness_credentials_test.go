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

// TestReadinessDropsTheAppKeyMountWhenTheServiceIsOn is the mount half of what
// TestRepoAddressesDropServiceHeldSecretsWhenTheServiceIsOn pins for the
// grants (C2.3, review finding F1): once an installation has opted into the
// credentials service, its job's account keeps no secretAccessor on the GitHub
// App's private key, and the mount has to go with the accessor. A container
// that still declared FUGARO_GITHUB_APP_PRIVATE_KEY would be created with a
// secret reference that account cannot resolve, so EVERY GitHub run of that
// installation would fail -- broken by the opt-in that was meant to be the
// careful one (D146). The brief's F1 is exactly this: C2.3 removed the
// accessor, no task owned the mount, and a mount with no accessor is worse
// than the accessor itself.
//
// It pins what the drop does NOT touch as much as what it does, because the
// drop's placement is the whole argument for it: the App key's CONTAINER, its
// operator grants and its project-wide resolution are not the job's and must
// survive -- the build account reads that key to clone (`fugaro image build`
// and the daily check, D148/F3), the credentials service mints the run's
// installation tokens from it (C2.1), and the operator stores its versions.
// So rs.Secrets, BuildSecrets and the check job's own mount all keep it, and
// only the workflows' jobs let it go. And only the App key: the model
// credentials and the workflow's own secrets stay in the mount, since C1.20 is
// the task that drops those.
//
// The mutations are the drop and its gate. Delete the dropMount call in
// readiness.go and every "on" row below keeps the mount; drop its
// `CredentialsService != nil` test and the "off" rows lose one, which is D147
// turned inside out: with the opt-in off nothing changes.
func TestReadinessDropsTheAppKeyMountWhenTheServiceIsOn(t *testing.T) {
	spec := repoRootSpec(t, repoRootOAuthAgent)
	f := newCloud(t)
	seedRepoReady(t, f, spec)

	off, offMissing, _ := readinessOf(t, f, spec)
	onSpec := spec
	onSpec.Installation.CredentialsService = credentialsService()
	on, _, _ := readinessOf(t, f, onSpec)

	// The fixture's own premises, or everything below would pass vacuously:
	// with the opt-in off every workflow mounts the key and names its secret,
	// and the spec carries its container.
	for _, key := range []string{"web", "web_bmb"} {
		ws := off.Workflows[key]
		if ws.SecretEnv[appKeyEnv] != appKeyLogical {
			t.Fatalf("with the credentials service off workflow %s mounts %v, want %s among them", key, ws.SecretEnv, appKeyEnv)
		}
		if ws.SecretIDs[appKeyLogical] == "" {
			t.Fatalf("with the credentials service off workflow %s names no App key secret: the drop would pass vacuously", key)
		}
	}
	if spec.Secrets[appKeyLogical] == "" {
		t.Fatal("the fixture has no App key container: the container assertions below would pass vacuously")
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

	// The container, its operator grants and the project-wide resolution are
	// the build account's and the service's, and the opt-in does not take
	// them away -- this is why the drop lives in Readiness and not in the
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
	// stays, so its own mount stays: dropping that one would break the daily
	// check instead of fixing the job.
	if on.Check == nil {
		t.Fatal("the fixture deploys no check job: the assertion below would pass vacuously")
	}
	if got := on.Check.SecretEnv[appKeyEnv]; got != appKeyLogical {
		t.Errorf("the check job's SecretEnv = %v with the credentials service on, want %s -> %s: it runs as the build account, which keeps the accessor (D148, F3)", on.Check.SecretEnv, appKeyEnv, appKeyLogical)
	}

	// With the opt-in off the job is deployed with the key mounted, and its
	// absence still holds the job back -- the gate the drop removes only when
	// the mount goes.
	if !off.Workflows["web"].DeployJob {
		t.Errorf("the fixture's job is not deployed with the credentials service off: %v", offMissing)
	}
	gap := newCloud(t)
	seedRepoReady(t, gap, spec, appKeyLogical)
	noKey, missing, _ := readinessOf(t, gap, spec)
	if noKey.Workflows["web"].DeployJob {
		t.Errorf("the job is deployed with no App key stored and the credentials service off: %v", missing)
	}
	// ...and the same absent key does not hold the job back once the service
	// is on: the job no longer reads it, so its version is no longer a fact
	// about this job's deploy.
	onGapSpec := spec
	onGapSpec.Installation.CredentialsService = credentialsService()
	onGap, missing, _ := readinessOf(t, gap, onGapSpec)
	if !onGap.Workflows["web"].DeployJob {
		t.Errorf("the job is held back by the App key's absent version with the credentials service on, though it mounts no key: %v", missing)
	}
}
