package gcp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

func buildSpec(t *testing.T) BuildSpec {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return BuildSpec{
		Slug: slug, RepoURL: "https://bitbucket.org/acme/app.git", BaseBranch: "main", Workflow: "web",
		Base: "us-east5-docker.pkg.dev/p/fugaro/fugaro-web-node:dev-abc", Image: ImageName("us-east5-docker.pkg.dev/p/fugaro", slug, "web"),
		GitSecretID: SecretID(slug, "bitbucket-token"), GitUser: "x-token-auth",
		ServiceAccount: "fugaro-build@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
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
	if b.Substitutions["_GIT_SECRET"] != gitVersion || b.Substitutions["_REPO_URL"] != spec.RepoURL ||
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
	build := b.Steps[2]
	if build.Id != "build" || !slices.Contains(build.SecretEnv, "NPM_TOKEN") || slices.Contains(b.Steps[1].SecretEnv, "NPM_TOKEN") ||
		slices.Contains(build.SecretEnv, "GIT_TOKEN") || !slices.Equal(b.Steps[0].SecretEnv, []string{"GIT_TOKEN"}) {
		t.Fatalf("secretEnv = %v / %v / %v", b.Steps[0].SecretEnv, b.Steps[1].SecretEnv, build.SecretEnv)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+spec.ServiceAccount || b.Options.MachineType != "E2_HIGHCPU_8" || b.Options.Logging != "CLOUD_LOGGING_ONLY" || b.Timeout != "3600s" {
		t.Fatalf("build = %+v / %+v / %s", b.ServiceAccount, b.Options, b.Timeout)
	}
	if len(b.Images) != 1 || b.Images[0] != "${_IMAGE}:latest" {
		t.Fatalf("images = %v", b.Images)
	}
	// The request is built fresh each time: a second call does not see the
	// first one's workflow secrets.
	again, err := BuildRequest("proj-1234", BuildSpec{Slug: spec.Slug, RepoURL: spec.RepoURL, BaseBranch: "main", Workflow: "web",
		Base: spec.Base, Image: spec.Image, GitSecretID: spec.GitSecretID, GitUser: "x-token-auth", ServiceAccount: spec.ServiceAccount})
	if err != nil || len(again.AvailableSecrets.SecretManager) != 1 || len(again.Steps[2].SecretEnv) != 0 || again.Substitutions["_SECRET_ENVS"] != "" {
		t.Fatalf("second request = %+v, %v", again, err)
	}
	for _, bad := range []config.Secret{{Name: "x", Env: "BAD NAME"}, {Name: "x", Env: "lower"}, {Name: "x", Env: "1X"}, {Name: "x", Env: "GIT_TOKEN"}, {Name: "x", Env: ""}} {
		s := spec
		s.WorkflowSecrets = []config.Secret{bad}
		if _, err := BuildRequest("proj-1234", s); err == nil {
			t.Errorf("the secret env name %q was accepted", bad.Env)
		}
	}
	for _, mutate := range []func(*BuildSpec){
		func(s *BuildSpec) { s.RepoURL = "ssh://git@bitbucket.org/acme/app.git" },
		func(s *BuildSpec) { s.RepoURL = "https://user:tok@bitbucket.org/acme/app.git" },
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
