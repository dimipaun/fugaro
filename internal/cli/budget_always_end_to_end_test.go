package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// budget_always_end_to_end_test.go is task I1's end-to-end half: the two states
// docs/always-a-budget is about, taken through `fugaro update` alone rather than
// through the stage that writes one of them.
//
// The fixtures are the ones the tasks before I1 built — AB7's installation whose
// project holds a database this machine records none of, AB8b's config that says
// the word that used to be a mode, and the run in flight of
// update_in_flight_test.go — and what is new here is that all of them are one
// installation at once, that the door is `fugaro update` and not a stage, and
// that the assertions are the acceptance the plan states: the database exists,
// the policy and the local config say observe, the run in flight finishes, and a
// new run has a gateway and no key in the agent's environment.

// offNode is the published policy the release before this one left: this
// binary's version, because the word the vocabulary dropped left the node
// without a bump (projectpolicy's migrateRemovedMode, D3025), saying the word.
func offNode() map[string]any {
	n := currentNode()
	n["budget"] = map[string]any{"mode": "off", "per_run_usd": 5}
	return n
}

// policyNode reads the published node back through the marker gate and the
// strict decoder, which is what a run's own reader does, so an assertion about
// its mode is about the document a run acts on rather than a field picked out
// of the raw value.
func policyNode(t *testing.T, db *gcpfake.RTDB) *projectpolicy.Policy {
	t.Helper()
	cl, err := newBudgetClient(t.Context(), db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Read(t.Context(), cl)
	if err != nil || !found {
		t.Fatalf("the published policy could not be read (found=%v): %v", found, err)
	}
	return p
}

// TestUpdateConvergesAnInstallationThatHadNoBudget: an installation at the
// previous release whose project holds a Realtime Database this machine records
// none of (F32), whose local config says the word that was a mode, and whose
// published policy says it too. One `fugaro update` is the whole of the
// upgrade, and what it leaves is the state the rest of the plan is built on:
// the database exists and is recorded, both documents say observe, and a run of
// this installation has an identity to mint with and a gateway in front of the
// agent.
//
// The red step is the release before this one, in both halves. Its Firebase
// stage needed --firebase-project to adopt what was there, so an update over
// this installation recorded no database at all; and with the word off its
// launch minted no identity and its job mounted the key the gateway would have
// held, so the environment asserted at the end is one no run of that release
// ever had.
func TestUpdateConvergesAnInstallationThatHadNoBudget(t *testing.T) {
	r := alwaysRig(t)
	mayCreate(t, r)
	updating(t, r.initRig)
	// The database the previous release wrote here is this installation's, and
	// this machine records none of it (F32) — the Firebase state was deleted, or
	// the config that named it is on another machine. That is the state the
	// previous release's own door could not converge from without a flag, and
	// what the update below records again.
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.db.Set(budget.PathProject, initProjectName)
	r.appendConfig(t, offBudgetBlock)
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	r.db.Set(projectpolicy.PathShared(), offNode())
	fakeTerminal(t)

	out, err := updateRun(t, r.initRig, "update")
	if err != nil {
		t.Fatalf("an update over an installation that had no budget stopped: %v\n%s", err, out)
	}

	// The database exists, and it is this installation's: the mark and the
	// project name are the ones the stage writes, and it is the one that was
	// there rather than a second one (F32, R2).
	if r.db.Value(budget.PathMark) == nil || r.db.Value(budget.PathProject) != initProjectName {
		t.Errorf("the run left the database with mark %v and project %v, want this installation's:\n%s", r.db.Value(budget.PathMark), r.db.Value(budget.PathProject), out)
	}
	// This machine records it, and says observe: the address is the database's,
	// and the word the file held is rewritten in place rather than left for
	// every later reader to interpret (AB8b, D3009).
	lc := r.localConfig(t)
	if lc.Budget == nil || lc.Budget.RTDBURL != r.db.URL {
		t.Fatalf("the local config records budget %+v, want the database this run found (%s)", lc.Budget, r.db.URL)
	}
	if lc.Budget.Mode != policy.ModeObserve {
		t.Errorf("the local config's budget.mode is %q, want %q", lc.Budget.Mode, policy.ModeObserve)
	}
	// The published policy says observe at its own version: the node was
	// rewritten in place, not walked forward (R4, D3025), which is what makes
	// every reader of this release able to read it at all.
	p := policyNode(t, r.db)
	if p.Budget.Mode != policy.ModeObserve {
		t.Errorf("the published policy's budget mode is %q, want %q: the node the previous release published is one no reader of this release serves", p.Budget.Mode, policy.ModeObserve)
	}
	if p.Version != projectpolicy.Version {
		t.Errorf("the published policy is version %d, want this binary's %d: the node's version does not move for the word", p.Version, projectpolicy.Version)
	}
	// A new run has a database to mint its identity with — the launch's own
	// decision, made on the config this run wrote (R3: the mode decides nothing
	// here any more).
	if on, err := budgetBackendOn(lc); !on || err != nil {
		t.Errorf("a launch on the converged installation is refused (on=%v): %v", on, err)
	}
	// And the agent's environment is the gateway's: the per-run token where the
	// key used to be, and the job's key nowhere in it (AB1b, F15). The job env
	// here is the one a keyed installation's job carries — the project's key,
	// mounted for the gateway to hold — and what the agent gets instead is the
	// token of the gateway this run's database minted for it.
	const jobKey = "sk-ant-api03-not-a-real-credential"
	env, _, err := agent.BuildEnv([]string{"ANTHROPIC_API_KEY=" + jobKey}, agent.EnvSpec{
		Auth:    config.AuthAPIKey,
		Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: "per-run-token"},
	})
	if err != nil {
		t.Fatalf("the agent's environment could not be built for a run with a gateway: %v", err)
	}
	if got := envValue(env, "ANTHROPIC_API_KEY"); got != "per-run-token" {
		t.Errorf("the agent's ANTHROPIC_API_KEY is %q, want the gateway's per-run token", got)
	}
	if strings.Contains(strings.Join(env, "\n"), jobKey) {
		t.Errorf("the job's own key reaches the agent's environment:\n%s", strings.Join(env, "\n"))
	}
}

// TestUpdateLeavesARunStartedWithoutABudgetRunning is R5 over the whole update:
// a run of the previous release is going when the update runs, and it finishes
// afterwards — its lease, the report its gateway settled, the counters that
// report moved, the outcome it ends with and the record it leaves are all
// written after the update and all read back. The installation is the one AB8b
// built (this machine's config says the word) with the node I1 is about (the
// published policy says it too), so what the run crosses is the state in which
// the word was still a mode: the update says that run keeps running, and it
// does.
//
// The run is a real session against the real database and its record is the
// runner's own (update_in_flight_test.go's fixture), because the claim is about
// the writes it makes: a stand-in could not show that a step of the update had
// taken a path or an object away from it.
func TestUpdateLeavesARunStartedWithoutABudgetRunning(t *testing.T) {
	r := offLocalConverge(t, offNode(), refreshYAML)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// The line R5 owes, printed while this machine's config still says the
	// word: such a run holds its key already and finishes. And never a request
	// to act: the owner's ruling of 2026-11-09 is that the update asks no run
	// to finish first.
	line := inFlightLine(t, out)
	if !strings.Contains(line, "a run that started with the budget off holds its key and finishes") {
		t.Errorf("the update does not say a run started with the budget off keeps running:\n%s", line)
	}
	for _, banned := range []string{"wait for them", "finish first", "refus"} {
		if strings.Contains(line, banned) {
			t.Errorf("the update asks the run's owner to act (%q):\n%s", banned, line)
		}
	}
	// It finishes: everything it writes after the update still lands, in the
	// database and the bucket the update left where they were.
	run.laterWrites(t, r)

	// And the installation it crossed is converged: both documents say observe
	// at the end of the same run, with a run in flight throughout.
	if p := policyNode(t, r.db); p.Budget.Mode != policy.ModeObserve {
		t.Errorf("the published policy's budget mode is %q after the update, want %q", p.Budget.Mode, policy.ModeObserve)
	}
	if b := updatedConfig(t).Budget; b == nil || b.Mode != policy.ModeObserve {
		t.Errorf("the local config says budget %+v after the update, want %s", b, policy.ModeObserve)
	}
}
