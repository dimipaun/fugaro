package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestImageBuildServedNamesNoSecret: a repository whose root is applied with
// the credentials service serving its builds submits a request that names no
// secret, whatever this machine's own budget.mode says (B4.9, D2076, D2077).
// The switch a build is judged on is the plan's own answer written back by that
// apply (localcfg.Repo.CredentialsServiceOn), which carries the published
// node's mode with it: a build that re-derived the switch from a machine's
// budget.mode — absent here, which reads off — would submit availableSecrets
// for a build account that holds no accessor on them, and Cloud Build refuses
// such a build.
//
// Mutation (run, restore): read lc.BudgetMode() in buildServiceURL instead of
// the applied root's record, and this test fails on the git credential in
// availableSecrets.
func TestImageBuildServedNamesNoSecret(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	// No budget block at all is the loudest form of "no local budget.mode":
	// the build is uncounted and still submitted
	// (TestImageBuildWithoutABudgetBackendBuildsUncounted).
	dropBudgetBackend(t, f)
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	switched := strings.Replace(string(data), "workflows: [web] }", "workflows: [web], credentials_service_ready: true }", 1)
	if switched == string(data) {
		t.Fatalf("the fixture's repos entry is not the one this rewrites:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(switched), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("a served repository's build failed: %v\n%s", err, stderr)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("the build was submitted %d times, want one", buildPosts(fb))
	}
	last := fb.Last()
	if v, ok := last["availableSecrets"]; ok && v != nil {
		t.Errorf("a served repository's build named secrets: availableSecrets = %v", v)
	}
	subs, _ := last["substitutions"].(map[string]any)
	if subs["_CREDENTIALS_SERVICE_URL"] != credentialsServiceURL {
		t.Errorf("the credential step was handed %v, want the installation's service %s", subs["_CREDENTIALS_SERVICE_URL"], credentialsServiceURL)
	}
}

// TestBuildServiceURLReadsThePlanAndTheAppliedRoot: the switch a build is
// submitted with is this run's plan where it has one, and the repository's
// applied root otherwise, and a served repository this config records no
// address for is refused rather than submitted without one (B4.9).
func TestBuildServiceURLReadsThePlanAndTheAppliedRoot(t *testing.T) {
	on := localcfg.RecordSwitch(true)
	off := localcfg.RecordSwitch(false)
	cases := []struct {
		name    string
		lc      *localcfg.Config
		plan    bool
		wantURL string
		wantErr string
	}{
		{
			name: "an unswitched repository keeps its own mounts",
			lc:   &localcfg.Config{Repos: map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: off}}},
		},
		{
			name: "an installation with no service has nothing to switch to",
			lc:   &localcfg.Config{Repos: map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: on}}},
		},
		{
			name: "a repository this config holds no entry for",
			lc:   &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: "https://c.example.invalid"}},
		},
		{
			name: "the applied root answers for a build with no plan of its own",
			lc: &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: "https://c.example.invalid"},
				Repos: map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: on}}},
			wantURL: "https://c.example.invalid",
		},
		{
			name: "the entry is matched the way the plan matches it",
			lc: &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: "https://c.example.invalid"},
				Repos: map[string]localcfg.Repo{"ACME/App": {CredentialsServiceReady: on}}},
			wantURL: "https://c.example.invalid",
		},
		{
			name: "this run's plan is what the converge's own build reads",
			lc: &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: "https://c.example.invalid"},
				Repos: map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: off}}},
			plan:    true,
			wantURL: "https://c.example.invalid",
		},
		{
			name: "a served repository this config has no address for",
			lc: &localcfg.Config{Repos: map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: on}}},
			plan:    true,
			wantErr: "records no address for it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := buildServiceURL(tc.lc, "acme/app", tc.plan)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("url %q, err %v, want a refusal naming %q", url, err, tc.wantErr)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error", ExitCode(err))
				}
				return
			}
			if err != nil || url != tc.wantURL {
				t.Fatalf("url %q, err %v, want %q", url, err, tc.wantURL)
			}
		})
	}
}
