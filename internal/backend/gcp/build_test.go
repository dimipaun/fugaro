package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

// repoRegistry is slug's own image registry in proj-1234.
func repoRegistry(slug string) string {
	return "us-east5-docker.pkg.dev/proj-1234/" + RegistryRepoID(slug)
}

func buildSpec(t *testing.T) BuildSpec {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return BuildSpec{
		Slug: slug, GitProvider: "bitbucket", RepoURL: "https://bitbucket.org/acme/app.git", BaseBranch: "main", Workflow: "web",
		Base: "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc", Image: ImageName(repoRegistry(slug), slug, "web"),
		GitSecretID: SecretID(slug, "bitbucket-token"), GitUser: "x-token-auth",
		ServiceAccount: BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
		WorkflowSecrets: []BuildSecret{{Name: "npm-token", Env: "NPM_TOKEN", ID: SecretID(slug, "npm-token")}},
		Bucket:          "gs://fugaro-runs-proj-1234",
	}
}

func TestBuildRequest(t *testing.T) {
	spec := buildSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.Substitutions["_SECRET_ENVS"] != "NPM_TOKEN" || b.Substitutions["_GIT_USER"] != "x-token-auth" || b.Substitutions["_IMAGE"] != spec.Image {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	gitVersion := "projects/proj-1234/secrets/" + spec.GitSecretID + "/versions/latest"
	if b.Substitutions["_REPO_URL"] != spec.RepoURL ||
		b.Substitutions["_FUGARO_BASE"] != spec.Base || b.Substitutions["_WORKFLOW"] != "web" || b.Substitutions["_BASE_BRANCH"] != "main" {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	var versions []string
	for _, s := range b.AvailableSecrets.SecretManager {
		versions = append(versions, s.Env+"="+s.VersionName)
	}
	want := []string{"GIT_TOKEN=" + gitVersion, "NPM_TOKEN=projects/proj-1234/secrets/" + SecretID(spec.Slug, "npm-token") + "/versions/latest"}
	if !slices.Equal(versions, want) {
		t.Fatalf("availableSecrets = %v", versions)
	}
	secretEnvs := map[string][]string{}
	for _, st := range b.Steps {
		secretEnvs[st.Id] = st.SecretEnv
	}
	if !slices.Equal(secretEnvs["build"], []string{"NPM_TOKEN"}) || !slices.Equal(secretEnvs["credential"], []string{"GIT_TOKEN"}) ||
		len(secretEnvs["source"])+len(secretEnvs["render"])+len(secretEnvs["prep"]) != 0 {
		t.Fatalf("secretEnv = %v", secretEnvs)
	}
	// The provider's own three, then the fetch's four: the credential step
	// reads the service, the workflow and the variables it would fetch when the
	// repository is switched to the credentials service (B4.5).
	if env := step(t, b, "credential").Env; !slices.Equal(env, []string{
		"PROVIDER=bitbucket", "REPO_URL=${_REPO_URL}", "GIT_USER=${_GIT_USER}",
		"FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}", "WORKFLOW=${_WORKFLOW}",
		"SECRET_ENVS=${_SECRET_ENVS}", "SECRET_DIR=/secrets",
	}) {
		t.Fatalf("credential env = %v", env)
	}
	if _, ok := b.Substitutions["_GITHUB_APP_ID"]; ok {
		t.Fatalf("a Bitbucket build sends _GITHUB_APP_ID: %v", b.Substitutions)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+spec.ServiceAccount || b.Options.MachineType != "E2_HIGHCPU_8" || b.Options.Logging != "CLOUD_LOGGING_ONLY" || b.Timeout != "3600s" || b.QueueTtl != "3600s" {
		t.Fatalf("build = %+v / %+v / %s / %s", b.ServiceAccount, b.Options, b.Timeout, b.QueueTtl)
	}
	// The build pushes only its candidate; promote moves latest.
	if len(b.Images) != 0 {
		t.Fatalf("images = %v", b.Images)
	}
	if b.Substitutions["_BUCKET"] != "gs://fugaro-runs-proj-1234" || b.Substitutions["_SLUG"] != spec.Slug {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	// The request is built fresh each time: a second call does not see the
	// first one's workflow secrets.
	again, err := BuildRequest("proj-1234", BuildSpec{Slug: spec.Slug, GitProvider: spec.GitProvider, RepoURL: spec.RepoURL, BaseBranch: "main", Workflow: "web",
		Base: spec.Base, Image: spec.Image, GitSecretID: spec.GitSecretID, GitUser: "x-token-auth", ServiceAccount: spec.ServiceAccount, Bucket: spec.Bucket})
	if err != nil || len(again.AvailableSecrets.SecretManager) != 1 || len(step(t, again, "build").SecretEnv) != 0 || again.Substitutions["_SECRET_ENVS"] != "" {
		t.Fatalf("second request = %+v, %v", again, err)
	}
	for _, env := range []string{"BAD NAME", "lower", "1X", "", "GIT_TOKEN", "GIT_USER", "GITHUB_APP_KEY", "GITHUB_APP_ID",
		// The build step's own variables, and ones bash or docker read.
		"IMAGE", "REPO_URL", "BASE_BRANCH", "SECRET_ENVS", "FUGARO_BASE", "DOCKER_BUILDKIT", "DOCKER_HOST", "BUILDKIT_PROGRESS",
		"PATH", "HOME", "TMPDIR", "IFS", "BASH_ENV", "LC_ALL"} {
		s := spec
		s.WorkflowSecrets = []BuildSecret{{Name: "x", Env: env, ID: spec.WorkflowSecrets[0].ID}}
		if _, err := BuildRequest("proj-1234", s); !errors.Is(err, ErrBadBuildSpec) {
			t.Errorf("the secret env name %q was accepted (%v)", env, err)
		}
	}
	dup := spec
	dup.WorkflowSecrets = []BuildSecret{
		{Name: "a", Env: "X_TOKEN", ID: spec.WorkflowSecrets[0].ID},
		{Name: "b", Env: "X_TOKEN", ID: spec.WorkflowSecrets[0].ID},
	}
	if _, err := BuildRequest("proj-1234", dup); !errors.Is(err, ErrBadBuildSpec) {
		t.Errorf("a repeated secret env name was accepted (%v)", err)
	}
	for _, mutate := range []func(*BuildSpec){
		func(s *BuildSpec) { s.RepoURL = "ssh://git@bitbucket.org/acme/app.git" },
		func(s *BuildSpec) { s.RepoURL = "https://user:tok@bitbucket.org/acme/app.git" },
		// The token is scoped to the provider's host; the clone must never
		// send it anywhere else.
		func(s *BuildSpec) { s.RepoURL = "https://evil.example/acme/app.git" },
		func(s *BuildSpec) { s.RepoURL = "https://bitbucket.org.evil.example/acme/app.git" },
		func(s *BuildSpec) { s.RepoURL = "https://bitbucket.org:8443/acme/app.git" },
		func(s *BuildSpec) { s.RepoURL = "https://github.com/acme/app.git" },
		func(s *BuildSpec) { s.GitProvider = "" },
		func(s *BuildSpec) { s.GitProvider = "github"; s.GitHubAppID = "12345" },
		func(s *BuildSpec) { s.GitProvider = "gitlab" },
		func(s *BuildSpec) { s.GitUser = "" },
		func(s *BuildSpec) { s.GitSecretID = "" },
		// A secret with no container would put an empty ID in the available
		// secret's version name, which is a request Cloud Build answers with
		// nothing a reader can act on: the caller drops such an entry
		// (internal/cli's buildSecrets), and one that reaches here is refused.
		func(s *BuildSpec) { s.WorkflowSecrets = []BuildSecret{{Name: "npm-token", Env: "NPM_TOKEN"}} },
		func(s *BuildSpec) { s.ServiceAccount = "" },
		func(s *BuildSpec) { s.Image = "" },
		func(s *BuildSpec) { s.Base = "" },
		// The base is substituted into the build and written into the image's
		// labels, so it is a reference and nothing else.
		func(s *BuildSpec) { s.Base = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc\n" },
		func(s *BuildSpec) { s.Base = `us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-"abc` },
		// The record goes to the runs bucket on GCS, and nowhere else.
		func(s *BuildSpec) { s.Bucket = "" },
		func(s *BuildSpec) { s.Bucket = "file:///tmp/runs" },
		func(s *BuildSpec) { s.Bucket = "gs://runs/prefix" },
		func(s *BuildSpec) { s.Bucket = "fugaro-runs-proj-1234" },
	} {
		s := spec
		mutate(&s)
		if _, err := BuildRequest("proj-1234", s); err == nil {
			t.Errorf("an incomplete or unsafe spec was accepted: %+v", s)
		}
	}
}

// TestEveryBuildIsBoundedByTheEstimatorsClock: the two numbers a submitted
// build is bounded by are the two the budget reasons about — the timeout the
// estimate is computed from, and the queue wait the lease's deadline adds to
// it. A build bounded by anything else would be a build the estimator never
// priced, so both request kinds are pinned against internal/budget's constants
// and not against a second copy of the two numbers here.
func TestEveryBuildIsBoundedByTheEstimatorsClock(t *testing.T) {
	timeout, queue := strconv.Itoa(budget.MaxBuildSeconds), strconv.Itoa(budget.MaxBuildQueueSeconds)
	requests := map[string]func() (*cloudbuild.Build, error){
		"a derived build": func() (*cloudbuild.Build, error) { return BuildRequest("proj-1234", buildSpec(t)) },
		"a catalog build": func() (*cloudbuild.Build, error) { return CatalogBuildRequest("proj-1234", catalogBuildSpec(t)) },
	}
	for name, request := range requests {
		b, err := request()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b.Timeout != timeout+"s" || b.QueueTtl != queue+"s" {
			t.Errorf("%s carries timeout %q and queue TTL %q, want %ss and %ss", name, b.Timeout, b.QueueTtl, timeout, queue)
		}
	}
}

// step is the request's step id, or fails the test.
func step(t *testing.T, b *cloudbuild.Build, id string) *cloudbuild.BuildStep {
	t.Helper()
	for _, st := range b.Steps {
		if st.Id == id {
			return st
		}
	}
	t.Fatalf("the request has no step %s", id)
	return nil
}

func githubSpec(t *testing.T) BuildSpec {
	t.Helper()
	slug, err := task.Slug("github", "acme/webapp")
	if err != nil {
		t.Fatal(err)
	}
	return BuildSpec{
		Slug: slug, GitProvider: "github", RepoURL: "https://github.com/acme/webapp.git", BaseBranch: "main", Workflow: "web",
		Base: "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc", Image: ImageName(repoRegistry(slug), slug, "web"),
		GitSecretID: SecretID(slug, "github-app-key"), GitHubAppID: "12345",
		ServiceAccount: BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8", Bucket: "gs://fugaro-runs-proj-1234",
	}
}

// TestBuildRequestGitHub: a GitHub build's credential step reads the App's
// private key and ID, and mints its token itself; nothing else changes.
func TestBuildRequestGitHub(t *testing.T) {
	spec := githubSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	sm := b.AvailableSecrets.SecretManager
	if len(sm) != 1 || sm[0].Env != "GITHUB_APP_KEY" || sm[0].VersionName != "projects/proj-1234/secrets/"+spec.GitSecretID+"/versions/latest" {
		t.Fatalf("availableSecrets = %+v", sm)
	}
	cred := step(t, b, "credential")
	if !slices.Equal(cred.SecretEnv, []string{"GITHUB_APP_KEY"}) ||
		!slices.Equal(cred.Env, []string{
			"PROVIDER=github", "REPO_URL=${_REPO_URL}", "GITHUB_APP_ID=${_GITHUB_APP_ID}",
			"FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}", "WORKFLOW=${_WORKFLOW}",
			"SECRET_ENVS=${_SECRET_ENVS}", "SECRET_DIR=/secrets",
		}) {
		t.Fatalf("credential step = %v / %v", cred.SecretEnv, cred.Env)
	}
	if b.Substitutions["_GITHUB_APP_ID"] != "12345" {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	if _, ok := b.Substitutions["_GIT_USER"]; ok {
		t.Fatalf("a GitHub build sends _GIT_USER, which nothing references: %v", b.Substitutions)
	}
	for _, st := range b.Steps {
		if st.Id != "credential" && (slices.Contains(st.SecretEnv, "GITHUB_APP_KEY") || slices.Contains(st.SecretEnv, "GIT_TOKEN")) {
			t.Errorf("step %s sees the provider secret", st.Id)
		}
	}
	for _, mutate := range []func(*BuildSpec){
		func(s *BuildSpec) { s.GitHubAppID = "" },
		func(s *BuildSpec) { s.GitHubAppID = "12a" },
		func(s *BuildSpec) { s.RepoURL = "https://bitbucket.org/acme/webapp.git" },
	} {
		s := spec
		mutate(&s)
		if _, err := BuildRequest("proj-1234", s); !errors.Is(err, ErrBadBuildSpec) {
			t.Errorf("a bad GitHub spec was accepted (%v): %+v", err, s)
		}
	}
}

// TestBuildRequestUsesBuildSA: the build runs as the repository's own
// build account, and only that one.
func TestBuildRequestUsesBuildSA(t *testing.T) {
	spec := buildSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if want := "projects/proj-1234/serviceAccounts/" + BuildServiceAccountID(spec.Slug) + "@proj-1234.iam.gserviceaccount.com"; b.ServiceAccount != want {
		t.Fatalf("serviceAccount = %q, want %q", b.ServiceAccount, want)
	}
	other, _ := task.Slug("bitbucket", "acme/other")
	for _, sa := range []string{
		"fugaro-build@proj-1234.iam.gserviceaccount.com",
		BuildServiceAccountID(other) + "@proj-1234.iam.gserviceaccount.com",
		BuildServiceAccountID(spec.Slug) + "@other-proj.iam.gserviceaccount.com",
		ServiceAccountID(spec.Slug, "web") + "@proj-1234.iam.gserviceaccount.com",
	} {
		s := spec
		s.ServiceAccount = sa
		if _, err := BuildRequest("proj-1234", s); !errors.Is(err, ErrBadBuildSpec) {
			t.Errorf("the build account %s was accepted (%v)", sa, err)
		}
	}
}

// TestBuildRequestRefusesNoServiceAccount: a request that names no account
// runs as the project's default build identity, which needs no actAs.
func TestBuildRequestRefusesNoServiceAccount(t *testing.T) {
	for _, spec := range []BuildSpec{buildSpec(t), githubSpec(t)} {
		spec.ServiceAccount = ""
		if b, err := BuildRequest("proj-1234", spec); !errors.Is(err, ErrBadBuildSpec) || !strings.Contains(err.Error(), "service account") {
			t.Errorf("a %s request without a service account = %+v, %v", spec.GitProvider, b, err)
		}
	}
}

// TestBuildRequestImageInRepoRegistry: the build pushes only to the
// repository's own registry in the build's project, which only its build
// account writes.
func TestBuildRequestImageInRepoRegistry(t *testing.T) {
	spec := buildSpec(t)
	if b, err := BuildRequest("proj-1234", spec); err != nil || b.Substitutions["_IMAGE"] != spec.Image {
		t.Fatalf("request = %v, %v", b, err)
	}
	other, _ := task.Slug("bitbucket", "acme/other")
	for _, image := range []string{
		ImageName("us-east5-docker.pkg.dev/proj-1234/fugaro", spec.Slug, "web"),
		ImageName("us-east5-docker.pkg.dev/proj-1234/fugaro-base", spec.Slug, "web"),
		ImageName(repoRegistry(other), spec.Slug, "web"),
		ImageName("us-east5-docker.pkg.dev/other-proj/"+RegistryRepoID(spec.Slug), spec.Slug, "web"),
		ImageName(repoRegistry(spec.Slug), spec.Slug, "other"),
		ImageName("ghcr.io/proj-1234/"+RegistryRepoID(spec.Slug), spec.Slug, "web"),
		ImageName(repoRegistry(spec.Slug), spec.Slug, "web") + ":latest",
	} {
		s := spec
		s.Image = image
		if _, err := BuildRequest("proj-1234", s); !errors.Is(err, ErrBadBuildSpec) {
			t.Errorf("the image %s was accepted (%v)", image, err)
		}
	}
}

func TestRegistryExists(t *testing.T) {
	spec := buildSpec(t)
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{GCPProject: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	reg := repoRegistry(spec.Slug)
	if ok, err := b.RegistryExists(context.Background(), reg); err != nil || ok {
		t.Fatalf("a missing registry: %v, %v", ok, err)
	}
	fb.AddRegistry("proj-1234", "us-east5", RegistryRepoID(spec.Slug))
	if ok, err := b.RegistryExists(context.Background(), reg); err != nil || !ok {
		t.Fatalf("an existing registry: %v, %v", ok, err)
	}
	fb.ForbidRegistries = true
	if _, err := b.RegistryExists(context.Background(), reg); !errors.Is(err, ErrRegistryUnchecked) {
		t.Errorf("a 403 = %v, want ErrRegistryUnchecked", err)
	}
	if _, err := b.RegistryExists(context.Background(), "us-east5-docker.pkg.dev/other-proj/"+RegistryRepoID(spec.Slug)); err == nil {
		t.Error("a registry of another project was looked up")
	}
}

// TestBuildRequestUsesEverySubstitution: Cloud Build rejects a request whose
// substitutions include a key the build doesn't reference ("key … in the
// substitution data is not matched in the template"), so every key sent must
// appear as ${KEY} in the request itself.
func TestBuildRequestUsesEverySubstitution(t *testing.T) {
	for _, spec := range []BuildSpec{buildSpec(t), githubSpec(t)} {
		checkEverySubstitution(t, spec)
	}
}

func checkEverySubstitution(t *testing.T, spec BuildSpec) {
	t.Helper()
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	subs := b.Substitutions
	b.Substitutions = nil
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for k := range subs {
		if !strings.Contains(string(body), "${"+k+"}") {
			t.Errorf("substitution %s is sent but not referenced, which Cloud Build rejects", k)
		}
	}
}

// TestBuildRequestCarriesTheServiceURL: a repository switched to the
// installation's credentials service (B4.5) submits the address its credential
// step fetches at, and the step reads that address from its env and never from
// its argv — the step runs the base image's fugaro, so a flag it does not know
// would be a build that fails on an installation whose base predates this
// release, while a variable it does not read is one it ignores.
//
// The mount of the provider credential is untouched here, which is what B4.9
// drops: until it lands, a switched build is served both the mounted secret and
// the service, and a build of a repository that is not switched is served the
// mount alone — its request carries the empty address and is otherwise exactly
// the request it was before this release.
//
// Mutation (verified, run and restored): set `_CREDENTIALS_SERVICE_URL` from a
// constant instead of s.CredentialsServiceURL and the empty case is the one
// that fails: a switched repository would submit "the service is off" to a step
// that then reads a secret its installation does not mount.
func TestBuildRequestCarriesTheServiceURL(t *testing.T) {
	spec := buildSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := b.Substitutions["_CREDENTIALS_SERVICE_URL"]; !ok || v != "" {
		t.Fatalf("a build of a repository that is not switched sends _CREDENTIALS_SERVICE_URL %q (present %v): %v", v, ok, b.Substitutions)
	}
	spec.CredentialsServiceURL = "https://credentials.example.com"
	b, err = BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Substitutions["_CREDENTIALS_SERVICE_URL"]; got != spec.CredentialsServiceURL {
		t.Fatalf("_CREDENTIALS_SERVICE_URL = %q, want %q", got, spec.CredentialsServiceURL)
	}
	cred := step(t, b, "credential")
	if !slices.Contains(cred.Env, "FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}") {
		t.Fatalf("the credential step does not read the service's address from its env: %v", cred.Env)
	}
	if args := strings.Join(cred.Args, " "); strings.Contains(args, spec.CredentialsServiceURL) {
		t.Errorf("the credential step's argv carries the address: %q", args)
	}
	// The mounts are the flip's own half and this spec is read with the class
	// list filled, so they are gone here: TestSwitchedBuildRequestHasNoAvailableSecrets
	// is the test for that, and this one is about the address.
	if b.AvailableSecrets != nil || len(cred.SecretEnv) != 0 {
		t.Errorf("a switched build still mounts secrets (%+v, %v): with the class list filled the request names none of them (B4.9, D2077)", b.AvailableSecrets, cred.SecretEnv)
	}
}

// TestSwitchedBuildRequestHasNoAvailableSecrets is B4.9's request half pinned
// (D2076, D2077): a repository the credentials service serves submits a
// request that names no secret at all — no availableSecrets, no step's
// secretEnv and no git credential ID to check — because its credential step
// fetches the git credential and the workflow's own build secrets from the
// service with the build account's own identity (B4.5), and the account holds
// no accessor on any of them (modules/repo/build.tf).
//
// What stays is the part that is not a value: the address, and the list of
// variables to fetch (_SECRET_ENVS, the same list either way, so the build
// step reads each fetched secret under the name it always read it under).
//
// It was written red: with config.BuildServedSecretClasses still empty the
// first assertion below fails and every request mounts what it mounted before,
// which is the state B4.5 shipped.
func TestSwitchedBuildRequestHasNoAvailableSecrets(t *testing.T) {
	if !config.BuildServesClass(config.BuildSecretClassBuild) {
		t.Fatalf("config.BuildServedSecretClasses = %v and does not carry %q: B4.9's flip is what makes a switched build name no secret and fetch them at the service instead (D2076, D2077)", config.BuildServedSecretClasses, config.BuildSecretClassBuild)
	}
	spec := buildSpec(t)
	spec.CredentialsServiceURL = "https://credentials.example.com"
	// No ID: a served request names no container, so the check must not be the
	// one that refuses it (internal/cli's build paths never resolve one for
	// such a repository — the grant it would be checked against is gone).
	spec.GitSecretID = ""
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatalf("a served build's request was refused: %v", err)
	}
	if b.AvailableSecrets != nil {
		t.Errorf("a served build carries availableSecrets %+v, want none: the account holds no accessor on them and Cloud Build refuses a build that names a secret its account cannot read (D2076)", b.AvailableSecrets)
	}
	for _, id := range []string{"credential", "build"} {
		if got := step(t, b, id).SecretEnv; len(got) != 0 {
			t.Errorf("a served build's %s step mounts %v, want none: the values come from the service (B4.5, D2076)", id, got)
		}
	}
	if got := b.Substitutions["_SECRET_ENVS"]; got != "NPM_TOKEN" {
		t.Errorf("_SECRET_ENVS = %q, want %q: the list of variables to fetch is a list of names and is the same one either way, so the build step reads each secret under the name it always read it under (B4.9)", got, "NPM_TOKEN")
	}
	if got := b.Substitutions["_CREDENTIALS_SERVICE_URL"]; got != spec.CredentialsServiceURL {
		t.Errorf("_CREDENTIALS_SERVICE_URL = %q, want %q: the step fetches at the address and nowhere else", got, spec.CredentialsServiceURL)
	}

	// The two halves of the fact, one row each: a build that names no address
	// keeps every mount whatever the class list says, and the class list is
	// what decides the address moves anything at all.
	unswitched := buildSpec(t)
	bl, err := BuildRequest("proj-1234", unswitched)
	if err != nil {
		t.Fatal(err)
	}
	if len(bl.AvailableSecrets.SecretManager) != 2 || !slices.Contains(step(t, bl, "credential").SecretEnv, gitTokenEnv) {
		t.Errorf("a build of a repository the service does not serve mounts %+v / %v, want the provider credential and each workflow secret: only a switched repository names none (G9, D147)", bl.AvailableSecrets, step(t, bl, "credential").SecretEnv)
	}
	saved := config.BuildServedSecretClasses
	config.BuildServedSecretClasses = nil
	t.Cleanup(func() { config.BuildServedSecretClasses = saved })
	// The spec's own ID comes back with the list: an empty list is the state
	// every build resolved its ID in, which is why the id is what a served
	// request stops naming rather than what it never had.
	prev := spec
	prev.GitSecretID = buildSpec(t).GitSecretID
	bi, err := BuildRequest("proj-1234", prev)
	if err != nil {
		t.Fatal(err)
	}
	if bi.AvailableSecrets == nil || len(bi.AvailableSecrets.SecretManager) != 2 {
		t.Errorf("with the class list empty a build naming an address carries %+v, want every mount it carried before the flip: the address alone is B4.5's state, which changed no mount (D147)", bi.AvailableSecrets)
	}
	config.BuildServedSecretClasses = saved
}

// TestCatalogBuildNamesNoSecretWhenServed pins the one build the flip never
// moves (B4.9, D147): a request with no mount at all is left alone whatever
// the class list says, and a catalog entry's build is that request — its
// credential step fetches its git credential from the service like a
// repository's, so it has no secret to move and the flip changes nothing for
// it.
func TestCatalogBuildNamesNoSecretWhenServed(t *testing.T) {
	if !config.BuildServesClass(config.BuildSecretClassBuild) {
		t.Fatalf("config.BuildServedSecretClasses = %v and does not carry %q: this case is about the flip being filled and a catalog build still naming no secret (D147)", config.BuildServedSecretClasses, config.BuildSecretClassBuild)
	}
	spec := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.AvailableSecrets != nil {
		t.Errorf("a catalog build's request carries %+v, want no secret at all: the class list moving a secret cannot move one a catalog entry's build never named (IS8, D3330)", b.AvailableSecrets)
	}
	for _, st := range b.Steps {
		if st.SecretEnv != nil {
			t.Errorf("step %q carries secretEnv %v, want none: a catalog entry's build reads its git credential from the service, not from a mount (IS8, D3330)", st.Id, st.SecretEnv)
		}
	}
	if got := b.Substitutions["_SECRET_ENVS"]; got != "" {
		t.Errorf("a catalog build's _SECRET_ENVS = %q, want the empty value it always had: an entry's build is handed no secret of the entry's (D2311, B9)", got)
	}
}

func TestSubmitAndWait(t *testing.T) {
	spec := buildSpec(t)
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{GCPProject: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Submit(context.Background(), spec)
	if err != nil || res.ID == "" || res.LogURL == "" || res.Image != spec.Image+":latest" {
		t.Fatalf("Submit = %+v, %v", res, err)
	}
	last := fb.Last()
	if subs, _ := last["substitutions"].(map[string]any); subs["_REPO_URL"] != spec.RepoURL {
		t.Fatalf("the fake got %v", last)
	}
	for _, r := range fb.Requests() {
		if r.Method == "POST" && !strings.HasPrefix(r.Path, "/v1/projects/proj-1234/locations/us-east5/builds") {
			t.Errorf("submitted to %s", r.Path)
		}
	}
	done, err := b.Wait(context.Background(), res.ID, 0)
	if err != nil || done.Status != "SUCCESS" || done.Digest == "" || done.ID != res.ID {
		t.Fatalf("Wait = %+v, %v", done, err)
	}
	fb.Outcome = "FAILURE"
	res, _ = b.Submit(context.Background(), spec)
	done, err = b.Wait(context.Background(), res.ID, 0)
	if err == nil || done.Status != "FAILURE" || !strings.Contains(err.Error(), "FAILURE") || !strings.Contains(err.Error(), done.LogURL) {
		t.Fatalf("failed build = %+v, %v", done, err)
	}
}

func TestWaitRetriesTransientErrors(t *testing.T) {
	defer func(n int, d time.Duration) { waitRetries, waitRetryBase = n, d }(waitRetries, waitRetryBase)
	waitRetries, waitRetryBase = 3, time.Millisecond
	spec := buildSpec(t)
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{GCPProject: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Submit(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	// A few 503s and 429s in a row are ridden out.
	fb.FailGets = 2
	if done, err := b.Wait(context.Background(), res.ID, 0); err != nil || done.Status != "SUCCESS" {
		t.Fatalf("Wait after 503s = %+v, %v", done, err)
	}
	fb.FailGets, fb.FailGetCode = 3, http.StatusTooManyRequests
	if done, err := b.Wait(context.Background(), res.ID, 0); err != nil || done.Status != "SUCCESS" {
		t.Fatalf("Wait after 429s = %+v, %v", done, err)
	}

	// Past the limit it gives up, saying the build may still be running.
	fb.FailGets, fb.FailGetCode = 10, 0
	_, err = b.Wait(context.Background(), res.ID, 0)
	if err == nil || !strings.Contains(err.Error(), "may still be running") || !strings.Contains(err.Error(), res.ID) {
		t.Fatalf("Wait past the retry limit = %v", err)
	}

	// A 4xx other than 408/429 is not retried.
	fb.FailGets, fb.FailGetCode = 10, http.StatusForbidden
	before := len(fb.Requests())
	if _, err := b.Wait(context.Background(), res.ID, 0); err == nil || len(fb.Requests()) != before+1 {
		t.Fatalf("a 403 was retried (%d calls) or accepted: %v", len(fb.Requests())-before, err)
	}

	// A SUCCESS without pushed-image results has no digest, and no error.
	fb.FailGets, fb.NoResults = 0, true
	if done, err := b.Wait(context.Background(), res.ID, 0); err != nil || done.Status != "SUCCESS" || done.Digest != "" {
		t.Fatalf("Wait without results = %+v, %v", done, err)
	}
}

// TestRegistryLookupNotThroughRealCloudBuildOverride: a real (authenticated)
// Cloud Build endpoint override, such as a regional one, serves no Artifact
// Registry, so the lookup keeps Google's own endpoint; only a fake (no_auth)
// serves both.
func TestRegistryLookupNotThroughRealCloudBuildOverride(t *testing.T) {
	if got := registryEndpoint(Endpoints{CloudBuild: "https://us-east5-cloudbuild.googleapis.com/"}); got != "" {
		t.Errorf("an authenticated Cloud Build override sends Artifact Registry lookups to %q", got)
	}
	if got := registryEndpoint(Endpoints{CloudBuild: "http://127.0.0.1:9/", NoAuth: true}); got != "http://127.0.0.1:9/" {
		t.Errorf("the fake's endpoint = %q", got)
	}
}

// TestBuildRequestNoSmoke: --no-smoke leaves out the smoke step and
// nothing else.
func TestBuildRequestNoSmoke(t *testing.T) {
	spec := buildSpec(t)
	ids := func(b *cloudbuild.Build) []string {
		var out []string
		for _, st := range b.Steps {
			out = append(out, st.Id)
		}
		return out
	}
	full, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.NoSmoke = true
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.DeleteFunc(ids(full), func(id string) bool { return id == "smoke" })
	if got := ids(b); !slices.Equal(got, want) || slices.Contains(got, "smoke") || len(got) != len(ids(full))-1 {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	checkEverySubstitution(t, spec)
}

// TestWaitReadsStepOutputDigest: the build pushes the candidate itself, so
// Cloud Build reports no images; Wait reads the pushed digest from the
// build step's output, and promote's, which says when a newer record kept
// latest where it was. Without step outputs it falls back to the pushed
// images.
func TestWaitReadsStepOutputDigest(t *testing.T) {
	spec := buildSpec(t)
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{GCPProject: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Submit(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	done, err := b.Wait(context.Background(), res.ID, 0)
	latest := fb.Tag(spec.Image, "latest")
	if err != nil || latest == "" || done.Digest != latest || done.Image != spec.Image+":latest" || done.Superseded {
		t.Fatalf("Wait = %+v, %v; latest is %q", done, err, latest)
	}

	// A superseded build: its digest, but latest is not it.
	fb.Steps = map[string]func(dir string) error{"gate": func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "superseded"), nil, 0o644)
	}}
	res, err = b.Submit(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	done, err = b.Wait(context.Background(), res.ID, 0)
	if err != nil || done.Digest != fb.Tag(spec.Image, "candidate-"+res.ID) || done.Digest == latest || !done.Superseded || done.Image != spec.Image {
		t.Fatalf("superseded Wait = %+v, %v", done, err)
	}
	if fb.Tag(spec.Image, "latest") != latest {
		t.Errorf("a superseded build moved latest to %s", fb.Tag(spec.Image, "latest"))
	}

	// An older build form that pushed through images: reports results.images.
	fb.Steps, fb.NoStepOutputs = nil, true
	res, _ = b.Submit(context.Background(), spec)
	done, err = b.Wait(context.Background(), res.ID, 0)
	if err != nil || done.Digest == "" || done.Image != spec.Image+":latest" {
		t.Fatalf("Wait from results.images = %+v, %v", done, err)
	}
}

// TestTemplateSalt: the salt covers the embedded cloudbuild.yaml and the
// fugaro version.
func TestTemplateSalt(t *testing.T) {
	a, b := TemplateSalt("1.2.3"), TemplateSalt("1.2.4")
	if len(a) != 64 || a == b || a != TemplateSalt("1.2.3") {
		t.Fatalf("TemplateSalt = %q, %q", a, b)
	}
}

// TestBuilderStatus: one builds get, without waiting; a build Cloud Build
// doesn't know is ErrBuildNotFound.
func TestBuilderStatus(t *testing.T) {
	fb := gcpfake.NewBuild(t)
	fb.FailStep = "smoke"
	b, err := NewBuilder(context.Background(), Options{GCPProject: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Submit(context.Background(), buildSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := b.Status(context.Background(), res.ID); err != nil || st != "FAILURE" {
		t.Fatalf("Status = %q, %v", st, err)
	}
	if _, err := b.Status(context.Background(), "b9999"); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("unknown build: %v", err)
	}
	if _, err := b.Status(context.Background(), "../x"); err == nil {
		t.Fatal("a path in the build ID was accepted")
	}
	if !IsDigest("sha256:"+strings.Repeat("0", 64)) || IsDigest("sha256:x") {
		t.Fatal("IsDigest")
	}
}

// TestBuildRequestCarriesTheLayerSHA: the project layer's sha256 reaches
// the build as _PROJECT_LAYER_SHA256, "" for a build without one, and a
// malformed sum is refused before any request is built.
func TestBuildRequestCarriesTheLayerSHA(t *testing.T) {
	spec := buildSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := b.Substitutions["_PROJECT_LAYER_SHA256"]; !ok || v != "" {
		t.Fatalf("no layer: substitution %q, %v", v, ok)
	}
	spec.ProjectLayerSHA256 = strings.Repeat("a", 64)
	if b, err = BuildRequest("proj-1234", spec); err != nil || b.Substitutions["_PROJECT_LAYER_SHA256"] != spec.ProjectLayerSHA256 {
		t.Fatalf("a layer: %v, %v", b.Substitutions, err)
	}
	spec.ProjectLayerSHA256 = "short"
	if _, err := BuildRequest("proj-1234", spec); !errors.Is(err, ErrBadBuildSpec) {
		t.Fatalf("a bad sum: %v", err)
	}
}

// TestCatalogDockerfileStep: the step a catalog build reads its copied
// Dockerfile with (PIC3's copy, PIC4's first step). The key is derived from
// the entry's name here and never taken from the caller, so a request cannot
// point the step at another object in the runs bucket, and the script follows
// the build's own rule for the values it reads (images/derived/cloudbuild.yaml:
// values arrive through env: and are read as quoted "$$VAR", never as a
// ${…} the request would substitute textually).
func TestCatalogDockerfileStep(t *testing.T) {
	key, err := config.CatalogDockerfileKey("agent")
	if err != nil {
		t.Fatal(err)
	}
	st, err := CatalogDockerfileStep("gs://fugaro-runs-proj-1234", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if st.Id != catalogDockerfileStepID || st.Entrypoint != "bash" || st.Name == "" {
		t.Fatalf("step = %+v", st)
	}
	if want := []string{"BUCKET=gs://fugaro-runs-proj-1234", "KEY=" + key}; !slices.Equal(st.Env, want) {
		t.Fatalf("env = %v, want %v", st.Env, want)
	}
	// Reading the copy needs no secret and no shared volume: the step is the
	// build account reading its own bucket, and one that mounted either would
	// be a wider read path than the copy needs.
	if len(st.SecretEnv) != 0 || len(st.Volumes) != 0 {
		t.Fatalf("secretEnv = %v, volumes = %v", st.SecretEnv, st.Volumes)
	}
	if len(st.Args) != 2 || st.Args[0] != "-c" {
		t.Fatalf("args = %v", st.Args)
	}
	script := st.Args[1]
	if strings.Contains(script, "${") {
		t.Fatalf("the script holds a substitution the request would rewrite: %q", script)
	}
	for _, read := range []string{`"$$BUCKET"`, `"$$BUCKET/$$KEY"`} {
		if !strings.Contains(script, read) {
			t.Fatalf("the script does not read %s: %q", read, script)
		}
	}
	if !strings.Contains(script, "/workspace/context/Dockerfile") {
		t.Fatalf("the script does not write the Dockerfile into the context: %q", script)
	}
	// The context is readable by the later steps, which run as other users,
	// and writable only by this one (it writes the Dockerfile as itself).
	if !strings.Contains(script, "install -d -m 0755 /workspace/context") || strings.Contains(script, "0777") {
		t.Fatalf("the context directory's mode: %q", script)
	}
	for _, bucket := range []string{"", "fugaro-runs-proj-1234", "gs://", "gs://runs/prefix", "gs://runs-proj-1234/"} {
		if _, err := CatalogDockerfileStep(bucket, "agent"); err == nil {
			t.Errorf("the bucket %q was accepted", bucket)
		}
	}
	for _, name := range []string{"", "Agent", "a/b", "..", "agent/../agent", "agent\n", "-agent", strings.Repeat("a", 41)} {
		if _, err := CatalogDockerfileStep("gs://fugaro-runs-proj-1234", name); err == nil {
			t.Errorf("the name %q was accepted", name)
		}
	}
	// The refusal names the entry it is about — an operator copying a
	// definition has no entry index to look one up — and the name it prints
	// is escaped, so a name carrying a control character cannot land raw in
	// the log the refusal is written to.
	_, err = CatalogDockerfileStep("gs://fugaro-runs-proj-1234", "Agent")
	if err == nil || !strings.Contains(err.Error(), "Agent") {
		t.Fatalf("the refusal does not name the entry: %v", err)
	}
	_, err = CatalogDockerfileStep("gs://fugaro-runs-proj-1234", "agent\nname")
	if err == nil || !strings.Contains(err.Error(), `agent\u000aname`) {
		t.Fatalf("the refusal does not name the entry: %v", err)
	}
	if strings.ContainsAny(err.Error(), "\n\r") {
		t.Fatalf("the refusal carries the raw name: %q", err)
	}
}
