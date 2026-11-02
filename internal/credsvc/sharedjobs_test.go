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
	"cmp"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
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
		"version": 4,
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

// sharedJobsNoProfileLocalConfig is an installation that publishes no profile:
// `fugaro update` deploys no project job for it and the policy node it
// publishes carries no profile. Both sides of the review set are empty for it,
// which is what makes the class inert until someone asks for a review job.
func sharedJobsNoProfileLocalConfig() string {
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
`
}

// sharedJobsPolicyDocNoProfiles is the policy node of the same installation:
// the profiles block left out, which is the shape Decode accepts of an
// installation that has never published one.
func sharedJobsPolicyDocNoProfiles() string {
	return `{
		"version": 4,
		"budget": {"mode": "enforce", "per_run_usd": 5},
		"allow_repos": ["github/acme/webapp"],
		"accounts": {
			"acme-personal": {"allow_repos": ["github/acme/webapp"]},
			"acme-shared": {"allow_repos": ["github/acme/webapp"]}
		},
		"credentials": {"fallback": "auto"}
	}`
}

// deployedReviewJobAccounts is the set of review-variant service account emails
// the fixture's installation implies, read off the coding jobs
// `internal/infra/projectjobs.go` builds: one review job per (profile,
// provider, account) it deploys a coding job for, derived through the one
// spelling the service and the deployer share
// (credsvc.ReviewServiceAccountID). CSR7 is the task that deploys the variant
// itself, so the deployer's own triples are what this reads rather than a
// second job set.
func deployedReviewJobAccounts(t *testing.T, localConfig string) []string {
	t.Helper()
	lc, err := localcfg.Parse([]byte(localConfig))
	if err != nil {
		t.Fatalf("the test's own local config is not one the deployer parses: %v", err)
	}
	jobs, err := infra.ProjectJobs(lc, sharedJobsBucket, false)
	if err != nil {
		t.Fatalf("the deployer refused the test's own installation: %v", err)
	}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		account := j.Account
		if account == "" {
			account = config.DefaultClaudeAccount
		}
		out = append(out, serviceAccountEmailForTest(credsvc.ReviewServiceAccountID(j.Profile, j.Provider, account)))
	}
	slices.Sort(out)
	return out
}

// reviewJobEmails is the service's own answer as plain emails, which is what
// the deployer's side of this test speaks.
func reviewJobEmails(accounts []credsvc.ReviewJobAccount) []string {
	out := make([]string, 0, len(accounts))
	for _, ra := range accounts {
		out = append(out, ra.Email)
	}
	slices.Sort(out)
	return out
}

// TestReviewAccountsAreTheNamesProjectJobsDeploys is spec §2.2's claim, checked
// the way the shared set's is: one installation built twice, from the local
// config `fugaro update` deploys from and from the policy node `fugaro project
// policy publish` publishes, and the two answers required to be the same set.
// A service that spelled the review variant its own way would admit an account
// nobody deployed — or, worse, refuse the one that exists.
func TestReviewAccountsAreTheNamesProjectJobsDeploys(t *testing.T) {
	deployed := deployedReviewJobAccounts(t, sharedJobsLocalConfig())
	p, found, err := projectpolicy.Decode([]byte(sharedJobsPolicyDoc()))
	if err != nil {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	if !found {
		t.Fatal("the test's own policy document decoded as absent")
	}
	got := credsvc.ReviewJobAccountsFor(p, sharedJobsGCPProject)
	if emails := reviewJobEmails(got); !slices.Equal(emails, deployed) {
		t.Fatalf("ReviewJobAccountsFor = %v, want the review variants of the accounts the deployer created, %v", emails, deployed)
	}
	// The fixture is only a test of anything if the axis it covers is more than
	// one account, and if the no-account job of a profile that lists no
	// default account is spelled the way the published admissions spell the
	// default account rather than as the empty string the axis produces.
	want := []credsvc.ReviewJobAccount{
		{Email: serviceAccountEmailForTest(credsvc.ReviewServiceAccountID("standard", "github", config.DefaultClaudeAccount)), Account: config.DefaultClaudeAccount},
		{Email: serviceAccountEmailForTest(credsvc.ReviewServiceAccountID("standard", "github", "acme-personal")), Account: "acme-personal"},
		{Email: serviceAccountEmailForTest(credsvc.ReviewServiceAccountID("standard", "github", "acme-shared")), Account: "acme-shared"},
	}
	slices.SortFunc(want, func(a, b credsvc.ReviewJobAccount) int { return cmp.Compare(a.Email, b.Email) })
	if !slices.Equal(got, want) {
		t.Fatalf("the fixture's own axis is not the one it means to test: got %v, want %v", got, want)
	}

	// An installation with no review job: no profile on either side, and so an
	// empty set from both (§2.2's fail-closed shape). It is the deployment that
	// has never asked for a review job, and the class is inert for it.
	none, noneFound, err := projectpolicy.Decode([]byte(sharedJobsPolicyDocNoProfiles()))
	if err != nil || !noneFound {
		t.Fatalf("the test's own profile-less policy document is not one Decode accepts: %v", err)
	}
	if deployed := deployedReviewJobAccounts(t, sharedJobsNoProfileLocalConfig()); len(deployed) != 0 {
		t.Errorf("the deployer built review jobs for an installation with no profile: %v", deployed)
	}
	if got := credsvc.ReviewJobAccountsFor(none, sharedJobsGCPProject); len(got) != 0 {
		t.Errorf("ReviewJobAccountsFor = %v, want none: the policy publishes no profile", got)
	}
}

// TestSharedAndReviewSetsAreDisjoint: an account in both sets is refused
// everywhere (verifyJob), so the review job could never mint and the class
// would be dead on every route. The two sets are disjoint by construction —
// the variant's ID is derived over a different account segment — and this is
// what holds that.
func TestSharedAndReviewSetsAreDisjoint(t *testing.T) {
	p, found, err := projectpolicy.Decode([]byte(sharedJobsPolicyDoc()))
	if err != nil || !found {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	shared := credsvc.SharedJobAccountsFor(p, sharedJobsGCPProject)
	review := reviewJobEmails(credsvc.ReviewJobAccountsFor(p, sharedJobsGCPProject))
	if len(shared) == 0 || len(review) == 0 {
		t.Fatalf("the fixture admits nobody to one of the two classes: shared %v, review %v", shared, review)
	}
	for _, email := range review {
		if slices.Contains(shared, email) {
			t.Errorf("the account %q is in both the shared and the review set, and a caller in both is refused everywhere", email)
		}
	}
}

// serviceAccountEmailForTest spells a service account ID as the email Google
// names it by, the way the deployer's own tests do.
func serviceAccountEmailForTest(id string) string {
	return id + "@" + sharedJobsGCPProject + ".iam.gserviceaccount.com"
}
