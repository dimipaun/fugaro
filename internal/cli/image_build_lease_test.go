package cli

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// image_build_lease_test.go holds the two fixtures every test of a submitted
// build needs now that a build leases its cost (T4): the price lookup, and the
// caps the lease is judged against. Both are shared by the build tests in
// image_test.go, image_gate_test.go, image_build_managed_test.go and
// image_refresh_steps_test.go, because a world that submits a build without
// them is a world whose build is refused before it is submitted.

// stubBuildPricer prices every build of a test from this package's own book
// (pricedBook), instead of reading the Cloud Billing catalog on the caller's
// credentials: a hermetic test has no account for that read, and the price of a
// build is not what any of these tests is about. The real lookup is
// newBuildPricer's own default, restored when the test ends.
func stubBuildPricer(t *testing.T) {
	t.Helper()
	was := newBuildPricer
	newBuildPricer = func(*localcfg.Config) buildPricer { return fakePricer{book: pricedBook()} }
	t.Cleanup(func() { newBuildPricer = was })
}

// buildBudgetWorld makes a checkout's installation one whose builds can be
// leased: the budget block pointing at the RTDB fake, the caps, the price
// lookup, and the database's own mode. It returns the fake database for the
// tests that go on to read what the build left in it.
//
// One fake per fixture, made on the first call: cloudBuildCheckout already gives
// every checkout that can build one, and a world built on top of it (the managed
// gate's, the catalog arm's) would otherwise append the key twice — a config this
// program does not read.
func buildBudgetWorld(t *testing.T, f *cloudFixture) *gcpfake.RTDB {
	t.Helper()
	if f.db != nil {
		return f.db
	}
	db := gcpfake.NewRTDB(t)
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 50, firebase_project: proj-1234, rtdb_url: "+db.URL+" }\n")
	adoptBudgetFake(db)
	buildBudgetCaps(db)
	stubBuildPricer(t)
	f.db = db
	return db
}

// adoptBudgetFake marks a budget fake as this installation's own, the way
// `fugaro update --firebase-project` leaves one: the project's name at
// /fugaro/project and the mark beside it, both written by that door's
// infra.DB.Plan. The two are what every writer into the database asks before
// it writes — the name gate (checkDBProject) and then the ownership question
// (requireOwnBudgetDB) — so a database holding data and no mark, or a name
// another project owns, is not ours. auroraMark is that mark, and every fixture
// here is the same installation: name aurora, gcp_project proj-1234.
func adoptBudgetFake(db *gcpfake.RTDB) {
	db.Set(budget.PathProject, "aurora")
	db.Set(budget.PathMark, auroraMark())
}

// buildBudgetCaps gives a world's budget database what a build lease is judged
// against, wide enough that one e2-highcpu-8 build's $0.94 fits with room to
// spare: these worlds are about what a build does, not about the refusal an
// over-cap build gets — that refusal is its own tests' (image_cost_test.go's
// fixtures and TestImageBuildOverCapFailsWithTheSpecError), and each of them
// states the cap its own case needs.
//
// The mode is enforce, and it has to be said: the predicate reads the mode from
// the database's own node (budget.PathMode), an absent one is observe (D1548),
// and a world in observe records an over-cap build and submits it anyway — so a
// test that seeds a cap below what a build reserves and expects the build NOT to
// run is a test that needs this. (The local config's own `mode: observe` above
// is the jobs' budget mode, a different knob.)
func buildBudgetCaps(db *gcpfake.RTDB) {
	db.Set(budget.PathMode, budget.ModeEnforce)
	db.Set(budget.PathCapsGlobal, map[string]any{"perRunMicros": 50 * usd1, "dailyMicros": 1000 * usd1})
	db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 100 * usd1, "repoPerRunMicros": 50 * usd1})
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 100 * usd1})
}

// buildBudgetForRig is buildBudgetWorld for an init rig, whose config lives at
// r.cfg rather than in a cloud fixture: the refresh tests build a target out of
// the rig's loaded config, so the budget block has to be in it before the load.
// One fake per rig, made on the first call: the config block is appended once,
// and a rig that builds two targets (a test's own, then the command's) would
// otherwise carry the key twice — a config this program does not read.
func buildBudgetForRig(t *testing.T, r *initRig) *gcpfake.RTDB {
	t.Helper()
	if r.budget != nil {
		return r.budget
	}
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 50, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	adoptBudgetFake(db)
	buildBudgetCaps(db)
	stubBuildPricer(t)
	r.budget = db
	return db
}

// rigBudgetClient is a client of the budget database the installation an init
// engine loaded points at: a test that has to change a cap or a counter AFTER
// its world was built — and that therefore cannot reach the fake the world was
// built with — writes it through this. The path's own leaves are written as
// the database rules read them (budget.PathCapsRepo and friends).
func rigBudgetClient(t *testing.T, e *initEngine) *rtdb.Client {
	t.Helper()
	db, err := newBudgetClient(context.Background(), e.lc.Budget.RTDBURL, true)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
