package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

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
		Base: "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc", Image: ImageName(repoRegistry(slug), slug, "web"),
		GitSecretID: SecretID(slug, "bitbucket-token"), GitUser: "x-token-auth",
		ServiceAccount: BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
		WorkflowSecrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}},
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
	if env := step(t, b, "credential").Env; !slices.Equal(env, []string{"PROVIDER=bitbucket", "REPO_URL=${_REPO_URL}", "GIT_USER=${_GIT_USER}"}) {
		t.Fatalf("credential env = %v", env)
	}
	if _, ok := b.Substitutions["_GITHUB_APP_ID"]; ok {
		t.Fatalf("a Bitbucket build sends _GITHUB_APP_ID: %v", b.Substitutions)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+spec.ServiceAccount || b.Options.MachineType != "E2_HIGHCPU_8" || b.Options.Logging != "CLOUD_LOGGING_ONLY" || b.Timeout != "3600s" {
		t.Fatalf("build = %+v / %+v / %s", b.ServiceAccount, b.Options, b.Timeout)
	}
	if len(b.Images) != 1 || b.Images[0] != "${_IMAGE}:latest" {
		t.Fatalf("images = %v", b.Images)
	}
	// The request is built fresh each time: a second call does not see the
	// first one's workflow secrets.
	again, err := BuildRequest("proj-1234", BuildSpec{Slug: spec.Slug, GitProvider: spec.GitProvider, RepoURL: spec.RepoURL, BaseBranch: "main", Workflow: "web",
		Base: spec.Base, Image: spec.Image, GitSecretID: spec.GitSecretID, GitUser: "x-token-auth", ServiceAccount: spec.ServiceAccount})
	if err != nil || len(again.AvailableSecrets.SecretManager) != 1 || len(step(t, again, "build").SecretEnv) != 0 || again.Substitutions["_SECRET_ENVS"] != "" {
		t.Fatalf("second request = %+v, %v", again, err)
	}
	for _, env := range []string{"BAD NAME", "lower", "1X", "", "GIT_TOKEN", "GIT_USER", "GITHUB_APP_KEY", "GITHUB_APP_ID",
		// The build step's own variables, and ones bash or docker read.
		"IMAGE", "REPO_URL", "BASE_BRANCH", "SECRET_ENVS", "FUGARO_BASE", "DOCKER_BUILDKIT", "DOCKER_HOST", "BUILDKIT_PROGRESS",
		"PATH", "HOME", "TMPDIR", "IFS", "BASH_ENV", "LC_ALL"} {
		s := spec
		s.WorkflowSecrets = []config.Secret{{Name: "x", Env: env}}
		if _, err := BuildRequest("proj-1234", s); !errors.Is(err, ErrBadBuildSpec) {
			t.Errorf("the secret env name %q was accepted (%v)", env, err)
		}
	}
	dup := spec
	dup.WorkflowSecrets = []config.Secret{{Name: "a", Env: "X_TOKEN"}, {Name: "b", Env: "X_TOKEN"}}
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
		func(s *BuildSpec) { s.ServiceAccount = "" },
		func(s *BuildSpec) { s.Image = "" },
		func(s *BuildSpec) { s.Base = "" },
	} {
		s := spec
		mutate(&s)
		if _, err := BuildRequest("proj-1234", s); err == nil {
			t.Errorf("an incomplete or unsafe spec was accepted: %+v", s)
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
		Base: "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc", Image: ImageName(repoRegistry(slug), slug, "web"),
		GitSecretID: SecretID(slug, "github-app-key"), GitHubAppID: "12345",
		ServiceAccount: BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
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
		!slices.Equal(cred.Env, []string{"PROVIDER=github", "REPO_URL=${_REPO_URL}", "GITHUB_APP_ID=${_GITHUB_APP_ID}"}) {
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
	b, err := NewBuilder(context.Background(), Options{Project: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
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

func TestSubmitAndWait(t *testing.T) {
	spec := buildSpec(t)
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{Project: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
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
	b, err := NewBuilder(context.Background(), Options{Project: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
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
