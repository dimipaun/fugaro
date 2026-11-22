package testutil

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The job environment variables that name a run's budget backend. They are
// internal/runner's own (its RTDBURLEnv and FirebaseAPIKeyEnv), respelled: this
// package is imported by that one's tests, internal ones among them, so it
// cannot import it back — and this package's own test pins the two spellings to
// those constants, so a rename fails there rather than in a harness.
const (
	rtdbURLEnv        = "FUGARO_RTDB_URL"
	firebaseAPIKeyEnv = "FUGARO_FIREBASE_API_KEY"
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
	// APIKey is the Firebase web API key a client exchanges the run's budget
	// token with. It is shaped like one and is not one, and it is the string
	// internal/e2e's budget rig has always used.
	APIKey string
}

// The database lives in the installation's own GCP project, which every
// fixture's config already names (gcp_project), so the block says nothing about
// a Firebase project of its own: a test that means a database *elsewhere* is a
// test about the stage that picks one, and it says so itself.
const (
	// BudgetAPIKey is the web API key a fake budget database is named by.
	BudgetAPIKey = "AIzaSyFakeWebApiKeyForTests000000000"
	// BudgetSigner is the signer account a fake budget database's installation
	// mints its runs' identities with. It is a service account in the Firebase
	// project (the name `fugaro update --firebase-project` writes), shaped like
	// one and not one.
	BudgetSigner = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
)

// NewBudget starts a fake budget database, which lives until the test ends.
func NewBudget(t *testing.T) *Budget {
	t.Helper()
	return &Budget{DB: gcpfake.NewRTDB(t), APIKey: BudgetAPIKey}
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
func (b *Budget) Env() []string {
	if b == nil {
		return nil
	}
	return []string{rtdbURLEnv + "=" + b.DB.URL, firebaseAPIKeyEnv + "=" + b.APIKey}
}

// ConfigBlock is the budget block a project config carries for this database,
// as one line for a fixture's config text: the database, the key a client
// exchanges a run's token with and the signer account the launcher mints it
// with, and no mode — the block says the installation has one, never how a run
// is accounted for, which is the job's mode and the project's published policy.
// A test that also means a mode, a per-run cap or a different signer adds that
// itself, the way internal/cli's buildBudgetWorld does. Empty for a null
// budget.
//
// The signer is part of the block because it is part of what an installation
// with a database has: the Firebase root that creates the database creates the
// web key, the signer account, the custom role and the grants with it (F4), and
// every launch of such an installation mints its run's identity through that
// signer (AB4, F7). A config block naming a database and no signer is a state
// no update leaves behind — and one a launch refuses (budgetSignerOn).
func (b *Budget) ConfigBlock() string {
	if b == nil {
		return ""
	}
	return "budget: { firebase_api_key: " + b.APIKey + ", rtdb_url: " + b.DB.URL + ", token_signer: " + BudgetSigner + " }\n"
}
