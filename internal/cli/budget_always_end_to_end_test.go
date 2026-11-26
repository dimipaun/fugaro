package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// budget_always_end_to_end_test.go is task I1 of docs/always-a-budget: the
// whole of it, on the installation the release behind leaves behind. Every
// task of the plan pinned its own half on a fixture built for it; what is here
// is the one installation all of those halves are about, taken through
// `fugaro update` alone — a machine with no budget at all (no database it
// records, the word in its config, the word in its published policy, a run
// already going), one command later holding a database, a policy and a config
// that say observe, with the run in flight finished and the next one running
// through a gateway.
//
// The two tests are the two halves of that state and they share the fixture:
// the installation that had no budget converges, and the run that started
// before it did finishes.

// noBudgetConverge is an installation of the release behind whose budget was
// off. It is the shape the plan's own sweep describes: this machine's config
// records no database (a release that had the word never wrote one), the word
// is in the config's budget block, and the published node — the release
// behind's, which this binary's readers refuse — says the word too.
func noBudgetConverge(t *testing.T) *convergeRig {
	t.Helper()
	node := staleNode(t)
	node["budget"] = map[string]any{"mode": "off", "per_run_usd": 5}
	r := newConvergeRigYAML(t, node, apiKeyRepoYAML)
	// The Firebase project's own database is there while this machine records
	// none (F23, F32): the update adopts it rather than standing up a second
	// one, which is the state a budget that was off leaves.
	r.firebaseState(t)
	mayCreate(t, r.fbRig)
	// The credential this repository's runs are launched with, as the release
	// behind stored it: a run of an api-key repository is the case whose
	// environment the last assertion below is about.
	r.seedProjectKey(t, config.AnthropicSecret)
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	off := strings.Replace(string(data),
		"budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+r.db.URL+", firebase_project: "+initProject+" }\n",
		offBudgetBlock, 1)
	if off == string(data) {
		t.Fatalf("the rig's local config does not name the database in the shape this fixture replaces, so it cannot be given one that had none:\n%s", data)
	}
	if err := os.WriteFile(r.cfg, []byte(off), 0o600); err != nil {
		t.Fatal(err)
	}
	registerInstallation(t, r.initRig)
	return r
}

// apiKeyRepoYAML is the rig's checkout with a credential the gateway carries.
// What the acceptance's last clause is about is a run through the gateway, and
// an oauth run is the one that reaches Anthropic with a token of its own and so
// has none (D3026, R12): the fixture has to be a repository whose runs the
// gateway serves, or the environment it builds is not the one the clause names.
var apiKeyRepoYAML = strings.Replace(refreshYAML, "auth: oauth", "auth: api-key", 1)

// TestUpdateConvergesAnInstallationThatHadNoBudget is I1's first half: one
// `fugaro update` over an installation whose budget was off gives it the whole
// of the release — the database it never had, an installation whose policy and
// config say observe — and the runs it launches from now on go through a
// gateway with the key kept out of the agent's environment.
//
// Red step: with the stage put back behind the old flag, this run creates
// nothing, the word stays in the config and the node, and every assertion below
// is about an installation that did not converge.
func TestUpdateConvergesAnInstallationThatHadNoBudget(t *testing.T) {
	r := noBudgetConverge(t)
	// The one check a fixture of the release behind is stale on here is the
	// checkout's pin: the policy checks are not read from this installation at
	// all, because no database is recorded on it (they are absent from doctor's
	// list rather than failing), and the base image the fixture's config records
	// is already this release's.
	out := convergeOn(t, r, []string{"plugin-pin"})
	// The database exists, and it is this installation's: the same mark and
	// project name an installation that had one from the start carries (F28).
	if r.db.Value(budget.PathMark) == nil || r.db.Value(budget.PathProject) != initProjectName {
		t.Errorf("the database holds mark %v and project %v, want this installation's:\n%s", r.db.Value(budget.PathMark), r.db.Value(budget.PathProject), out)
	}

	// This machine's own config: the word is gone, it says observe, and it now
	// records the database — which is what a launch, the gate and doctor read.
	// The file is the one the commands read, the copy doctor selects by name
	// rather than the rig's own.
	cfgPath, err := localcfg.ProjectPath(os.Getenv, initProjectName)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(cfgPath)
	if err != nil {
		t.Fatalf("the local config the update left does not load: %v", err)
	}
	if lc.Budget == nil || lc.Budget.Mode != policy.ModeObserve {
		t.Errorf("the local config says budget %+v, want %s", lc.Budget, policy.ModeObserve)
	}
	if b := lc.Budget; b == nil || b.RTDBURL != r.db.URL || b.FirebaseProject != fpID {
		t.Errorf("the local config records budget %+v, want the database this run gave it (%s, %s)", b, fpID, r.db.URL)
	}

	// And the policy, which is the one a run of this installation reads: the
	// node the release behind published said the word, which this release's
	// readers refuse.
	p := convergeNode(t, r)
	if p.Budget.Mode != policy.ModeObserve {
		t.Errorf("the published policy's budget is %+v, want %s", p.Budget, policy.ModeObserve)
	}
	if p.Version != projectpolicy.Version {
		t.Errorf("the published policy is version %d, want %d", p.Version, projectpolicy.Version)
	}

	// A new run of the repository has a gateway, and the key its job mounts
	// never reaches the agent's own environment. The two facts are read through
	// the runner's own functions: the repository's credential is one the
	// gateway carries (AB1b), and the environment the stage is handed holds
	// that gateway's per-run token where the job's key would be.
	repo, problems := config.Parse([]byte(apiKeyRepoYAML))
	if len(problems) != 0 {
		t.Fatalf("the fixture's repository config does not parse: %v", problems)
	}
	auth := repo.Agent.Auth
	if !runner.GatewayOn(auth) {
		t.Fatalf("a run of this repository has no gateway: its credential is %q", auth)
	}
	const planted = "sk-ant-planted-job-key"
	env, _, err := agent.BuildEnv([]string{"ANTHROPIC_API_KEY=" + planted, "CLOUD_ML_REGION=us-east5"},
		agent.EnvSpec{Auth: auth, Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:8787", Token: "run-token"}})
	if err != nil {
		t.Fatalf("the environment of a run of this installation could not be built: %v", err)
	}
	if got := envOf(env, "ANTHROPIC_BASE_URL"); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the gateway's address", got)
	}
	if got := envOf(env, "ANTHROPIC_API_KEY"); got == planted {
		t.Errorf("ANTHROPIC_API_KEY = the job's own key, want the gateway's per-run token (AB1b, F15)")
	}
}

// TestUpdateLeavesARunStartedWithoutABudgetRunning is I1's other half: the run
// that was already going when the update ran is the one that started with the
// budget off. It holds its key, opens no session, and no step of the update
// reaches it, so it finishes — and the update says so before it changes
// anything (R5).
func TestUpdateLeavesARunStartedWithoutABudgetRunning(t *testing.T) {
	r := offLocalConverge(t, currentNode(), apiKeyRepoYAML)
	// The credential is the one the release behind stored, so the update has no
	// step left for the person and the run about to go in flight is the only
	// thing this fixture is about.
	r.seedProjectKey(t, config.AnthropicSecret)
	run := startRunInFlight(t, r)

	line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"}))
	if !strings.Contains(line, "a run that started with the budget off holds its key and finishes") {
		t.Errorf("the note over an installation whose config says the word does not say the run keeps running:\n%s", line)
	}

	// Everything the run does after the update lands: its lease, its report,
	// its ledger and its counters in the database the update has just taken
	// over, and its record and its review session in the bucket.
	run.laterWrites(t, r)
}

// envOf is one variable out of a built environment, "" when it is not there.
func envOf(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}
