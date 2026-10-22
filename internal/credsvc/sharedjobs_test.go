package credsvc_test

// The shared-job accounts the service refuses (C1.11's Interfaces paragraph,
// Global Constraint 6's gate, which holds until C3.9).
//
// `credsvc.SharedJobAccountsFor` is handed to C1.10's verifier as
// `AuthConfig.SharedJobAccounts`, and the verifier refuses a caller whose
// verified email is one of them. The plan's claim about that set is exact: the
// accounts are "the same names `internal/infra/projectjobs.go` deploys". A
// service that derived them its own way would be a gate that refuses accounts
// nobody created — the ordinary case of an installation whose jobs the service
// then admits — or, worse, admits the accounts that do exist, which is the
// gate failing open on the one thing it exists for.
//
// So this test does not check the derivation against itself: it builds ONE
// installation twice, from the local config `fugaro update` deploys from and from
// the policy node `fugaro project policy publish` publishes, and requires the
// two answers to be the same set of email addresses. It is why the file is in
// the external test package: `internal/infra` imports `internal/runner`, which
// imports this package (creds_wait.go), so an internal test file could not
// import the deployer at all.
//
// Nothing here is a credential (Convention 2): a service account email is a
// name, and the fixture holds no token.

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// sharedJobsGCPProject is the GCP project the fixture deploys into: the
	// domain every service account email of this installation ends in.
	sharedJobsGCPProject = "proj-1234"
	// sharedJobsBucket is the runs bucket the fixture's jobs are built with.
	// It decides the jobs' bucket conditions and nothing this test reads.
	sharedJobsBucket = "fugaro-runs-proj-1234"
)

// sharedJobsProfile is the one profile both spellings of the fixture declare:
// a github profile serving two Claude accounts beside its no-account job, so
// the three account segments the axis produces are all distinct and a
// derivation that dropped the account segment, or the profile, or the provider
// from the account's name would show up as a set that is not this one.
const sharedJobsProfile = `{"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
		"allow_repos": ["github/acme/webapp"],
		"claude_accounts": ["acme-personal", "acme-shared"],
		"secrets": ["github-app-key"]}`

// sharedJobsLocalConfig is the installation `fugaro update` deploys from: the M4
// config the deployer's own tests use, with the one repository a shared job is
// built for, the M5 fields an installation adds, and the profiles block.
func sharedJobsLocalConfig() string {
	return `version: 1
name: aurora
gcp_project: ` + sharedJobsGCPProject + `
region: us-east5
runs_bucket: ` + sharedJobsBucket + `
registry: us-east5-docker.pkg.dev/` + sharedJobsGCPProject + `/fugaro
build: { service_account: fugaro-build@` + sharedJobsGCPProject + `.iam.gserviceaccount.com }
user: test@example.com
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: "123456789" }
registry_host: us-east5-docker.pkg.dev/` + sharedJobsGCPProject + `
base_images: {base: us-east5-docker.pkg.dev/` + sharedJobsGCPProject + `/fugaro-base/fugaro-base:dev-0123abc}
claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
  acme-shared: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: ` + sharedJobsProfile + `
`
}

// sharedJobsPolicyDoc is the policy node the same installation publishes: the
// profiles block verbatim, and the accounts block `publish` derives from the
// local config (DeriveAccounts), which is what a profile listing accounts is
// judged against.
func sharedJobsPolicyDoc() string {
	return `{
		"version": 3,
		"budget": {"mode": "enforce", "per_run_usd": 5},
		"allow_repos": ["github/acme/webapp"],
		"accounts": {
			"acme-personal": {"allow_repos": ["github/acme/webapp"]},
			"acme-shared": {"allow_repos": ["github/acme/webapp"]}
		},
		"profiles": {"standard": ` + sharedJobsProfile + `},
		"credentials": {"fallback": "auto"}
	}`
}

// deployedSharedJobAccounts is the set of service account emails
// `internal/infra/projectjobs.go` deploys for the fixture, read off the job
// specs `fugaro update` hands to Terraform.
func deployedSharedJobAccounts(t *testing.T) []string {
	t.Helper()
	lc, err := localcfg.Parse([]byte(sharedJobsLocalConfig()))
	if err != nil {
		t.Fatalf("the test's own local config is not one the deployer parses: %v", err)
	}
	jobs, err := infra.ProjectJobs(lc, sharedJobsBucket, false)
	if err != nil {
		t.Fatalf("the deployer refused the test's own installation: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("the deployer built no job for a config that declares a profile")
	}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if j.ServiceAccount.AccountID == "" {
			t.Fatalf("the deployed job %q runs as no service account", j.Job)
		}
		out = append(out, j.ServiceAccount.AccountID+"@"+sharedJobsGCPProject+".iam.gserviceaccount.com")
	}
	slices.Sort(out)
	return out
}

// TestSharedJobAccountsAreTheNamesProjectJobsDeploys is the claim C1.11's
// Interfaces paragraph makes, checked against the deployer itself rather than
// against a second copy of the naming rule.
//
// Mutation (run, restore): derive the account from anything but the profile's
// own triple — the Fugaro project name, the provider alone, or the profile
// without its account axis — and the service's set is no longer the deployed
// one.
func TestSharedJobAccountsAreTheNamesProjectJobsDeploys(t *testing.T) {
	deployed := deployedSharedJobAccounts(t)
	p, found, err := projectpolicy.Decode([]byte(sharedJobsPolicyDoc()))
	if err != nil {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	if !found {
		t.Fatal("the test's own policy document decoded as absent")
	}
	got := credsvc.SharedJobAccountsFor(p, sharedJobsGCPProject)
	if !slices.Equal(got, deployed) {
		t.Fatalf("SharedJobAccountsFor = %v, want the accounts the deployer created, %v", got, deployed)
	}
	// The fixture is only a test of anything if the axis it covers is more
	// than one account: one profile serving two accounts beside its
	// no-account job.
	want := []string{
		serviceAccountEmailForTest(gcp.ProjectServiceAccountID("standard", "github", "")),
		serviceAccountEmailForTest(gcp.ProjectServiceAccountID("standard", "github", "acme-personal")),
		serviceAccountEmailForTest(gcp.ProjectServiceAccountID("standard", "github", "acme-shared")),
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the fixture's own axis is not the one it means to test: got %v, want %v", got, want)
	}
}

// serviceAccountEmailForTest spells a service account ID as the email Google
// names it by, the way the deployer's own tests do.
func serviceAccountEmailForTest(id string) string {
	return id + "@" + sharedJobsGCPProject + ".iam.gserviceaccount.com"
}
