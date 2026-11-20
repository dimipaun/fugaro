package testutil

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runner"
)

// Budget is a fake budget database: the hermetic stand-in for the Firebase
// Realtime Database a project's budget lives in, which a harness gives every
// run, launch and installation it builds unless the test asks for none
// (NoBudget).
//
// It is the address and the identity, never the policy: what a *run* is
// accounted for is the job's mode and the project's published policy, and a
// test that means a world with a budget backend says so itself (the CLI's
// buildBudgetWorld, the runner's attachBackend). What this gives is the world
// every later task's tests need before they can say anything else: the
// installation has a database, so a run has something to open a session
// against and a launch has something to mint for.
type Budget struct {
	// DB is the fake database itself, for a test that seeds a node or reads
	// back what a run wrote.
	DB *gcpfake.RTDB
	// Project is the Firebase project the database is in, as a project
	// config names it (budget.firebase_project): a GCP project ID, which is
	// also the installation's own project for the fixtures here.
	Project string
	// APIKey is the Firebase web API key a client exchanges the run's budget
	// token with. It is shaped like one (and is not one).
	APIKey string
	// Signer is the service account a launcher has sign a run's budget token
	// (budget.token_signer).
	Signer string
}

// The values a fake budget database is named by. The project and the signer are
// the fixtures' own installation (aurora, proj-1234 in internal/cli's world),
// and the key is the string internal/e2e's budget rig has always used: it is a
// shape, not a credential, and nothing in this repository holds a real one.
const (
	BudgetProject = "proj-1234"
	BudgetAPIKey  = "AIzaSyFakeWebApiKeyForTests000000000"
	BudgetSigner  = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
)

// NewBudget starts a fake budget database, which lives until the test ends.
func NewBudget(t *testing.T) *Budget {
	t.Helper()
	return &Budget{DB: gcpfake.NewRTDB(t), Project: BudgetProject, APIKey: BudgetAPIKey, Signer: BudgetSigner}
}

// NoBudget is how a test asks a harness for no database at all: a *Budget whose
// methods answer nothing, which every harness here reads as "this job, this
// launch and this installation have none". It is the state a run with no
// database address, a launch that cannot mint and an installation that has
// never had the stage run are refused for, so a test of one of those refuses
// asks for this and says so.
func NoBudget() *Budget { return nil }

// URL is the database's address, and "" for a null budget.
func (b *Budget) URL() string {
	if b == nil {
		return ""
	}
	return b.DB.URL
}

// Env is the entries a job's environment carries for this database: the address
// the run's budget client reaches it at, and the web API key the token exchange
// is made with. Empty for a null budget, so a harness can append it blindly.
//
// The names are the runner's own constants rather than respelled here, so a
// harness's environment and the run that reads it cannot drift.
func (b *Budget) Env() []string {
	if b == nil {
		return nil
	}
	return []string{runner.RTDBURLEnv + "=" + b.DB.URL, runner.FirebaseAPIKeyEnv + "=" + b.APIKey}
}

// ConfigBlock is the budget block a project config carries for this database,
// as one line for a fixture's config text: the database, and no mode — the
// block says the installation has one, never how a run is accounted for. A
// test that also means a *mode*, a per-run cap or the project the database is
// in adds that itself, the way internal/cli's buildBudgetWorld does. Empty for
// a null budget.
func (b *Budget) ConfigBlock() string {
	if b == nil {
		return ""
	}
	return "budget: { firebase_project: " + b.Project + ", firebase_api_key: " + b.APIKey + ", rtdb_url: " + b.DB.URL + " }\n"
}
