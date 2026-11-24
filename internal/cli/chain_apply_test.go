package cli

// chain_apply_test.go is AP6's own door (docs/composite-commands): what a
// management command does with the steps its own change owes. What each command
// answers is one of three things — the steps run here and the change is live,
// a step did not finish and D1563's sentence says so over a non-zero exit, or
// `--no-apply` saved the change and left its steps in this machine's record for
// `fugaro apply` — and every family reaches them through one runner
// (chainApply, chainSave, chainSettle, applyRunRecord), so what these tests pin
// is that each command is ON that runner and under its own door.
//
// Three of the six families run their steps here and finish: `accounts rm`,
// `project repos add` and `models add` — their steps are stages this machine
// can converge in process, and the assertion that they really did is read off
// the thing the step exists for (the installation's own report, the published
// node's prices), never off the absence of a line. The workspace family's
// owner half is the one that cannot finish: its first step is a grant confirmed
// only by a name typed at a terminal, and a chain asks nothing (D1540), so
// those runs end on D1563's sentence naming `fugaro update`
// (workspaceAssertSavedNotLive, and workspaceAssertNoSecondCommand for the
// member's half that declares no step at all).

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// chainPrinted is everything one command printed, both writers. A chain's
// answer is on stdout and its run files its own account beside it, and which of
// the two a line lands on is the runner's business and not these tests'.
func chainPrinted(out, errOut string) string { return out + errOut }

// TestAccountsRmChain is the removal's own follow-up: the account is gone from
// the local config and the secret store, and the two steps that make the
// removal live — the published node that stops naming the account, and the
// installation whose job set the account owned — run in process, under this
// command's own name, so the command's block is an account that is gone rather
// than one whose job is still deployed.
func TestAccountsRmChain(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")

	// rm's helper fails the test on a non-zero exit, which is the first half of
	// this test: a removal whose steps this machine can converge must not end
	// on D1563's sentence.
	out := r.rm(t, "team-a", "--profile", "standard")

	for _, forbidden := range []string{"saved, not live yet", "still owed", "fugaro apply", "next:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("accounts rm answered %q over a removal whose steps it ran:\n%s", forbidden, out)
		}
	}
	if got := pendingSteps(record(t)); len(got) != 0 {
		t.Errorf("accounts rm left %v owed, want nothing: the removal runs its own steps", got)
	}
	// The record is stamped, which is what says the steps it ran are the ones
	// the config now stands for (D1575): a record written and never settled
	// would read as a machine that owes nothing only because it forgot.
	if rec := record(t); rec.LastApply == "" {
		t.Errorf("the record was left unstamped, so nothing says the steps it ran answered this config")
	}
	// The account is out of the installation the run converged, and the
	// command's own block says which half of it went.
	if got := accountsAddRow(t, out, "account"); !strings.Contains(got, "claude_accounts.team-a removed") {
		t.Errorf("the block's account line reads %q, want the declaration it removed named", got)
	}
}

// TestReposAddChain is the enrolment's own follow-up: the repository's entry,
// its image mapping and its allow-list entry are the command's writes, and the
// three steps that make them live are the node the admission is published in,
// the job set that serves the repository and the repository's own Terraform
// root — all three run here, so the report's last line ("fugaro run --repo … is
// ready") is printed over an installation that really has the job set.
func TestReposAddChain(t *testing.T) {
	r := enrolRig(t)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\n%s", enrolRepo, err, chainPrinted(out, errOut))
	}
	all := chainPrinted(out, errOut)
	for _, forbidden := range []string{"saved, not live yet", "still owed", "fugaro apply"} {
		if strings.Contains(all, forbidden) {
			t.Errorf("project repos add answered %q over an enrolment whose steps it ran:\n%s", forbidden, all)
		}
	}
	if got := pendingSteps(record(t)); len(got) != 0 {
		t.Errorf("project repos add left %v owed, want nothing: the enrolment runs its own steps", got)
	}
	if !strings.Contains(all, "fugaro run --repo "+enrolEntry+" is ready") {
		t.Errorf("the enrolment does not end on its own report:\n%s", all)
	}
	// Every step the table declares for a repository was run and answered: the
	// three names are the plan's own last three, and one of them missing would
	// be a step the record never held rather than an owed one.
	if _, ok := r.addLocal(t).Repos[enrolRepo]; !ok {
		t.Errorf("the enrolment's own write is not in the config it read back")
	}
}

// TestModelsAddChain is the model's own follow-up: the price the command wrote
// into the local config is published into the node the project's runs are
// admitted by, in process and under this command's own name — which is what
// makes "the model is added" true for the runs of every repository at once,
// rather than a local edit waiting for a command nobody runs (D1566).
func TestModelsAddChain(t *testing.T) {
	f := modelsChainFixture(t)
	// The node the installation holds: a publish of the config as it is now, so
	// the difference the chain has to carry is this command's own entry and not
	// the whole document the first publish would write anyway.
	if out, errOut, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("project policy publish: %v\n%s", err, chainPrinted(out, errOut))
	}
	if _, ok := publishedPolicy(t, f).Prices[modelsChainID]; ok {
		t.Fatalf("the fixture's node already prices %s", modelsChainID)
	}

	out, errOut, err := execute(t, "models", "add", modelsChainID, "--price-in", "1", "--price-out", "2")
	if err != nil {
		t.Fatalf("models add %s: %v\n%s", modelsChainID, err, chainPrinted(out, errOut))
	}
	all := chainPrinted(out, errOut)
	for _, forbidden := range []string{"saved, not live yet", "still owed", "fugaro apply", "next:"} {
		if strings.Contains(all, forbidden) {
			t.Errorf("models add answered %q over a change whose step it ran:\n%s", forbidden, all)
		}
	}
	// The run is the command's own and not `fugaro apply`'s, and the thing
	// that says so is the log it files (D1351: a run is filed under the name a
	// person typed): one log, under this command's name, which is the name the
	// door the chain opens its reporter with is converted to.
	onlyLog(t, "models-add")
	price, ok := publishedPolicy(t, f).Prices[modelsChainID]
	if !ok {
		t.Fatalf("the node does not price %s after models add: the publish did not run", modelsChainID)
	}
	if price.InputPerM == nil || *price.InputPerM != 1 || price.OutputPerM == nil || *price.OutputPerM != 2 {
		t.Errorf("the node prices %s at %+v, want the rates the command was given", modelsChainID, price)
	}
	if got := pendingSteps(record(t)); len(got) != 0 {
		t.Errorf("models add left %v owed, want nothing: the publish is the change's own step", got)
	}
}

// modelsChainID is the model this file's own chain test adds: one the fixture's
// provider claims (`deepseek/*`), so the change is the price entry alone and the
// providers block is not rewritten beside it.
const modelsChainID = "deepseek/deepseek-v4-pro"

// modelsChainFixture is the publisher's fixture with the one thing a models
// write needs and a publish does not: a runs bucket named the way this
// installation's own review requires (fugaro-runs-<gcp-project>, init_review).
// The publisher never reads the name, which is why the fixture it exists for
// carries a placeholder, and a chain test that wants the node AND the write has
// to have both.
func modelsChainFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	text := strings.Replace(f.configText(t, projectConfigText(f.dir, f.db.URL, projectProvidersYAML, "")),
		"runs_bucket: unused-bucket", "runs_bucket: fugaro-runs-proj-1234", 1)
	if err := os.WriteFile(f.path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestWorkspaceWritesChain is this family's two halves (apply's own rows say
// why they differ): the member's half — the membership key and the pin — is
// live the moment the file is written and owes no step at all, and the owner's
// half owes the workspace's own project and the record that service reads.
func TestWorkspaceWritesChain(t *testing.T) {
	// The member's half: nothing to run, so nothing is queued and no command is
	// named as the next thing to do.
	t.Run("the member's half declares no step", func(t *testing.T) {
		_, path := workspaceWriteHome(t)
		out, errOut, err := execute(t, workspaceAddArgs()...)
		if err != nil {
			t.Fatalf("workspace add: %v\n%s", err, chainPrinted(out, errOut))
		}
		// The half owes no apply STEP — nothing this machine's own run can
		// converge — and it is still not live: the record its runs read is
		// published by this project's own `fugaro update` (R3, R4), which is
		// the one command the write names.
		workspaceAssertNamesUpdate(t, "workspace add", chainPrinted(out, errOut), "aurora", "aurora-gcp")
		if !strings.Contains(readFileString(t, path), "workspace: workspace-alpha\n") {
			t.Errorf("the membership was not written:\n%s", readFileString(t, path))
		}
	})

	// The owner's half, under no flag: the two steps run here and neither
	// finishes — the grant is confirmed only by a name typed at a terminal, and
	// this fixture has no Firebase backend to publish the record in — so the
	// command exits non-zero with the sentence that names the door which can.
	t.Run("the owner's half runs the two steps it owes", func(t *testing.T) {
		_, _, owner := workspaceOwnerHome(t)
		out, errOut, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50")
		all := chainPrinted(out, errOut)
		workspaceAssertSavedNotLive(t, "workspace cap set", all, err)
		// The run is filed under the command the person ran (D1351): one log,
		// under this command's own name and not `fugaro apply`'s.
		onlyLog(t, "workspace-cap-set")
		lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
		if err != nil {
			t.Fatal(err)
		}
		if lc.WorkspaceCap == nil || lc.WorkspaceCap.Monthly != 50 {
			t.Errorf("the cap is %+v, want the 50 a month the command was given", lc.WorkspaceCap)
		}
	})

	// And the same write under --no-apply (D1565): the change is saved, both
	// steps are in this machine's record in the order they run, and the command
	// names `fugaro apply` — the one word that opens the run it did not open.
	t.Run("--no-apply saves the change and queues its steps", func(t *testing.T) {
		_, _, owner := workspaceOwnerHome(t)
		out, errOut, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", "--no-apply")
		if err != nil {
			t.Fatalf("workspace cap set --no-apply: %v\n%s", err, chainPrinted(out, errOut))
		}
		all := chainPrinted(out, errOut)
		owed := []string{initflow.WorkspaceProject, initflow.Workspace}
		if want := "saved, not live yet: " + strings.Join(owed, ", ") + " owed"; !strings.Contains(all, want) {
			t.Errorf("--no-apply does not say the change is saved and not live (%q):\n%s", want, all)
		}
		if !strings.Contains(all, "Run `fugaro apply`") {
			t.Errorf("--no-apply does not name the command that runs what it saved:\n%s", all)
		}
		if got := workspaceOwedFor(t, "workspace-alpha", "ws-gcp"); !slices.Equal(got, owed) {
			t.Errorf("--no-apply left %v owed, want %v in the order they run", got, owed)
		}
		lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
		if err != nil {
			t.Fatal(err)
		}
		if lc.WorkspaceCap == nil || lc.WorkspaceCap.Monthly != 50 {
			t.Errorf("--no-apply did not write the change it saved: cap = %+v", lc.WorkspaceCap)
		}
	})
}

// TestWorkspaceCapSetMonthlyRefusedInAnAgentSession is D1576: a cap this
// command's chain would publish is the owner's own step, and --no-apply does
// not become a way around that — the flag saves the change FOR `fugaro apply`,
// and a record a coding agent's session could queue would be the raise the
// session may not make at all. The fence is the one every local write already
// meets (applySettingsEdit, the same fence `workspace project add` meets), and
// it is met before the chain: nothing is written and nothing is queued, with
// and without the flag.
func TestWorkspaceCapSetMonthlyRefusedInAnAgentSession(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, "workspace_cap:\n  monthly: 40\n")
	before := readFileString(t, owner)
	t.Setenv(agentMarkers[0], "1")

	invocation := []string{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50"}
	for _, args := range [][]string{invocation, append(slices.Clone(invocation), "--no-apply")} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v raised the cap in a coding agent's session:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "coding agent's session") {
			t.Errorf("%v: the refusal is not the fence's: %v", args, err)
		}
		if !strings.Contains(err.Error(), "run this yourself at your own terminal") {
			t.Errorf("%v: the refusal does not print the command to run: %v", args, err)
		}
		if got := readFileString(t, owner); got != before {
			t.Errorf("%v wrote the file it refused:\n%s", args, got)
		}
		if got := workspaceOwedFor(t, "workspace-alpha", "ws-gcp"); len(got) != 0 {
			t.Errorf("%v queued %v for `fugaro apply`: the raise is the owner's own step with the flag as without it", args, got)
		}
	}
}

// TestPolicyPublishStillTheStagesDoor is D1566's one exception: `project policy
// publish` is itself the step the table names (initflow.Policy), so it owes
// nothing and records nothing — it publishes in place and that is the whole of
// its answer. The test reads the two halves of that: the node carries the
// publish, and this machine has no record of work left behind it.
func TestPolicyPublishStillTheStagesDoor(t *testing.T) {
	f := newProjectFixture(t, "")
	out, errOut, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("project policy publish: %v\n%s", err, chainPrinted(out, errOut))
	}
	all := chainPrinted(out, errOut)
	for _, forbidden := range []string{"saved, not live yet", "still owed", "fugaro apply", "next:"} {
		if strings.Contains(all, forbidden) {
			t.Errorf("the publish answered %q, and it is the step and not a command that owes one:\n%s", forbidden, all)
		}
	}
	if len(publishedPolicy(t, f).AllowRepos) == 0 {
		t.Errorf("the publish wrote no allow-list, so nothing was published")
	}
	if _, err := os.Stat(recordPath(t)); err == nil {
		t.Errorf("the publish recorded a step for later:\n%s", readFileString(t, recordPath(t)))
	}
}
