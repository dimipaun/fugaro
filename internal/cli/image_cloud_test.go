package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The Cloud Build clone authenticates with the Bitbucket token, so an
// origin on any host but bitbucket.org is refused before anything is
// submitted: the token would go to that host.
func TestImageBuildCloudPinsTheProviderHost(t *testing.T) {
	for _, origin := range []string{
		"https://bitbucket.example.com/acme/app.git",
		"https://github.com/acme/app.git",
		"https://bitbucket.org:8443/acme/app.git",
	} {
		t.Run(origin, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			testutil.Git(t, ".", "remote", "set-url", "origin", origin)
			_, _, err := execute(t, "image", "build", "--base", "b:1")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bitbucket") {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(fb.Requests()) != 0 {
				t.Error("a build cloning from another host reached Cloud Build")
			}
		})
	}
}

// buildPosts are the builds submitted to the fake.
func buildPosts(fb *gcpfake.Build) int {
	n := 0
	for _, r := range fb.Requests() {
		if r.Method == "POST" {
			n++
		}
	}
	return n
}

// TestImageBuildCloudNeedsRegistry: the build pushes to the repository's
// own registry, which fugaro init --repo creates; without it, nothing is
// submitted.
func TestImageBuildCloudNeedsRegistry(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, true)
	_, _, err := execute(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro init --repo") || !strings.Contains(err.Error(), "proj-1234") ||
		!strings.Contains(err.Error(), gcp.RegistryRepoID(mustSlug("bitbucket", "acme/app"))) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if buildPosts(fb) != 0 {
		t.Error("a build without its registry reached Cloud Build")
	}
}

// TestImageBuildCloudGitHub: a GitHub repository builds with its App's key
// and ID, as its own build account, into its own registry.
func TestImageBuildCloudGitHub(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	fb := gcpfake.NewBuild(t)
	newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "workflows: [web] }", `workflows: [web], github_app_id: "12345" }`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(appSlug))
	if _, _, err := execute(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatal(err)
	}
	last := fb.Last()
	subs, _ := last["substitutions"].(map[string]any)
	if subs["_GITHUB_APP_ID"] != "12345" || subs["_REPO_URL"] != "https://github.com/acme/app.git" ||
		subs["_IMAGE"] != gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(appSlug), appSlug, "app") {
		t.Fatalf("substitutions = %v", subs)
	}
	if _, ok := subs["_GIT_USER"]; ok {
		t.Errorf("a GitHub build sent _GIT_USER: %v", subs)
	}
	secrets, _ := last["availableSecrets"].(map[string]any)
	sm, _ := secrets["secretManager"].([]any)
	first, _ := sm[0].(map[string]any)
	if first["env"] != "GITHUB_APP_KEY" || first["versionName"] != "projects/proj-1234/secrets/"+gcp.SecretID(appSlug, "github-app-key")+"/versions/latest" {
		t.Fatalf("availableSecrets = %v", sm)
	}
	if sa, _ := last["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(appSlug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

// TestImageBuildCloudGitHubNeedsAppID: without the App's ID the build
// could not mint a token, so nothing is submitted.
func TestImageBuildCloudGitHubNeedsAppID(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	fb := gcpfake.NewBuild(t)
	newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(appSlug))
	_, _, err := execute(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "GitHub App ID") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a build without an App ID reached Cloud Build")
	}
}

// TestImageBuildIgnoresDeprecatedBuildSA: build.service_account still
// loads, with a warning, but the build runs as the repository's own build
// account.
func TestImageBuildIgnoresDeprecatedBuildSA(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	f.appendConfig(t, "build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n")
	_, stderr, err := execute(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "build.service_account is deprecated") {
		t.Errorf("stderr = %q", stderr)
	}
	slug := mustSlug("bitbucket", "acme/app")
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(slug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

// TestImageBuildCloudRefusesOtherProjectRegistry: images are pushed with
// the project's credentials, so a registry_host of the file's project
// can't be used against --project of another one.
func TestImageBuildCloudRefusesOtherProjectRegistry(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	f.appendConfig(t, "registry_host: us-east5-docker.pkg.dev/proj-1234\n")
	_, _, err := execute(t, "image", "build", "--base", "b:1", "--project", "other-proj")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-proj") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a build for another project's registry reached Cloud Build")
	}
}

// TestImageBuildCloudRegistryForbidden: an operator may submit builds but
// not read the repository's registry. A 403 on the check is not a missing
// registry: the build is submitted, with a warning.
func TestImageBuildCloudRegistryForbidden(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	fb.ForbidRegistries = true
	_, stderr, err := execute(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "could not check") {
		t.Errorf("stderr = %q", stderr)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("builds submitted = %d", buildPosts(fb))
	}
}
