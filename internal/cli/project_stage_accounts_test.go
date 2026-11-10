package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The account-choice keys have two ends that meet nowhere else: the local
// config holds them and `project policy publish` copies them into the node
// (this file's first test), while the ruling that bounds them
// (config.CheckStageAccountMix, docs/design/mixed-credentials.md §11) needs an
// auth the node does not carry and fires at the launch and in `fugaro validate`
// (the second test).

// stageAccountProjectYAML is an operator's config with one Claude account, a
// profile that serves it, and the two account-choice keys naming it: the
// default account every stage uses, and the account the senior review uses
// instead. The account is defined here because the keys name an account of this
// file's own block (localcfg's choiceRefProblem), and the profile serves it
// because the publisher derives the node's accounts block from the profiles.
const stageAccountProjectYAML = "claude_accounts:\n" +
	"  acme-overflow: { allow_data_to: [acme/app] }\n" +
	"profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [acme-overflow], allow_repos: [\"github/acme/app\"] }\n" +
	"default_claude_account: acme-overflow\n" +
	"stages:\n" +
	"  review:\n" +
	"    claude_account: acme-overflow\n"

// TestPublishCarriesTheAccountChoiceKeys is the round trip the node's
// members exist for: the local config's default account and stage account are
// published as the node's own members, and the node decodes them back — the
// runner reads them from there, since the node is the only layer a run can
// reach. A project that names neither publishes neither, so its node is what
// it was before the keys existed and no teammate of it meets a skew.
func TestPublishCarriesTheAccountChoiceKeys(t *testing.T) {
	f := newProjectFixture(t, stageAccountProjectYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.DefaultClaudeAccount != "acme-overflow" {
		t.Errorf("DefaultClaudeAccount = %q, want acme-overflow", p.DefaultClaudeAccount)
	}
	if len(p.ClaudeAccountStages) != 1 || p.ClaudeAccountStages["review"] != "acme-overflow" {
		t.Errorf("ClaudeAccountStages = %v, want review: acme-overflow", p.ClaudeAccountStages)
	}
	raw := nodeJSON(t, f, projectpolicy.PathShared())
	for _, want := range []string{`"default_claude_account"`, `"claude_account_stages"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the node does not carry %s:\n%s", want, raw)
		}
	}

	g := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, g); p.DefaultClaudeAccount != "" || p.ClaudeAccountStages != nil {
		t.Errorf("a project that names no account published one: %q, %v", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}
	if raw := nodeJSON(t, g, projectpolicy.PathShared()); strings.Contains(raw, "claude_account") {
		t.Errorf("a project that names no account published a member:\n%s", raw)
	}
}

// stageAccountProject writes a project config that holds the given accounts
// block and account-choice keys over the budget a validate needs, and returns
// the repository file beside it — one that names agent.auth: api-key, which is
// the auth the mix rule is about.
func stageAccountProject(t *testing.T, extra string) (projectPath, repoPath string) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("budget: { mode: enforce, per_run_usd: 2 }\n" + extra); err != nil {
		t.Fatal(err)
	}
	return path, writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:",
		"agent: { auth: api-key, model: claude-sonnet-5-5, models: { background: claude-haiku-4-5 } }\nworkflows:", 1))
}

// stageAccountAccountsYAML defines the one account every case below names.
const stageAccountAccountsYAML = "claude_accounts:\n  acme-overflow: { allow_data_to: [acme/other] }\n"

// TestValidateRefusesAStageAccountOnTheCodersStage is the ruling's offline
// half (decision 1: "the rule is the rule at every layer that judges it, and
// fugaro validate says the same as the run"): the file names api-key, the
// machine's config names an account for implement, and validate refuses with
// the sentence the launch refuses with — the mix's three stages named as the
// launch names them. The permitted mix beside it is the same file and the same
// account, named for the reviews and the triage that follows them instead.
func TestValidateRefusesAStageAccountOnTheCodersStage(t *testing.T) {
	_, repo := stageAccountProject(t, stageAccountAccountsYAML+
		"stages:\n  implement:\n    claude_account: acme-overflow\n")
	out, _, err := execute(t, "validate", repo)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want a user refusal:\n%s", ExitCode(err), out)
	}
	for _, want := range []string{"stages.implement.claude_account", "would never be used", "review, adversarial and triage"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}

	_, repo = stageAccountProject(t, stageAccountAccountsYAML+
		"stages:\n  review:\n    claude_account: acme-overflow\n"+
		"  adversarial:\n    claude_account: acme-overflow\n")
	if out, _, err := execute(t, "validate", repo); err != nil {
		t.Fatalf("the permitted mix was refused: %v\n%s", err, out)
	}
}

// TestValidateRefusesASecondStageAccount: the ruling's other half, judged in
// the same place. Two stages naming two accounts is one provider key and two
// Claude accounts, which an api-key run does not have.
func TestValidateRefusesASecondStageAccount(t *testing.T) {
	// Both accounts have to be defined before they can be named, so the
	// fixture's own block is replaced rather than added to.
	_, repo := stageAccountProject(t, "claude_accounts:\n"+
		"  acme-overflow: { allow_data_to: [acme/other] }\n"+
		"  acme-personal: { allow_data_to: [acme/other] }\n"+
		"stages:\n"+
		"  review:\n    claude_account: acme-overflow\n"+
		"  adversarial:\n    claude_account: acme-personal\n")
	out, _, err := execute(t, "validate", repo)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "one provider key and one Claude account") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	if !strings.Contains(out, "acme-overflow") || !strings.Contains(out, "acme-personal") {
		t.Errorf("the refusal does not name both accounts:\n%s", out)
	}
}

// TestAccountChoiceKeysAreNeverShared pins the local-only rule at its third
// door (localcfg.Config.Shared drops the keys and sharedForbidden refuses
// them): the shared file is the one any launcher with objectAdmin on the runs
// bucket can write, and an account choice published there would be that
// launcher's to set — the account choice is the owner's, made on the machine
// that holds the credential.
func TestAccountChoiceKeysAreNeverShared(t *testing.T) {
	for _, key := range []string{"default_claude_account", "stages", "claude_account_auto"} {
		if !slices.Contains(sharedForbidden, key) {
			t.Errorf("sharedForbidden does not name %s, so a published account choice would be read", key)
		}
	}
	for name, block := range map[string]string{
		"the project default": "default_claude_account: acme-overflow\n",
		"a stage's account":   "stages:\n  review:\n    claude_account: acme-overflow\n",
		"the auto switch":     "claude_account_auto: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseShared([]byte(validShared()+block), sharedAnchor)
			if err == nil {
				t.Fatalf("a shared config carrying %s was accepted", block)
			}
			for _, want := range []string{"never published", "fugaro update"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// stageAccountHeldBackYAML is stageAccountProjectYAML plus an account for the
// triage stage — the stage this release runs and no released image does: an
// operator who writes it has made a choice the local config keeps, and the
// publish below is where the choice either reaches a run or does not.
const stageAccountHeldBackYAML = stageAccountProjectYAML +
	"  triage:\n" +
	"    claude_account: acme-overflow\n"

// TestPublishHoldsBackAStageTheReleaseBehindCannotRead (T-TR5, the review's
// finding): the node's claude_account_stages may name only the stages every job
// image that reads it knows. The reader is an image — the runner of the release
// behind, which `fugaro update` rebuilds behind its own confirmation and which
// an installation can still be running, an in-flight run included — and it
// refuses the whole node when one of those names is not a stage of its own run
// (projectpolicy, through config.CheckStageSegment). Publishing the triage
// entry would therefore break every run of the project on an image that is not
// rebuilt yet, which is the one thing `fugaro update` must never do (F15,
// docs/design/update-in-flight.md).
//
// What the operator's config says is kept where it was written and published by
// the first release whose images carry the stage; the entry's own staging is
// config.AccountStageHeldBack, and the vocabulary behind it is
// AccountStagesAReleasedImageKnows — whose second reader this test is.
func TestPublishHoldsBackAStageTheReleaseBehindCannotRead(t *testing.T) {
	f := newProjectFixture(t, stageAccountHeldBackYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if _, ok := p.ClaudeAccountStages["triage"]; ok {
		t.Errorf("the node carries a triage entry: the reader of the release behind refuses the whole node over that name, so every run of this project on an image that is not rebuilt yet would be refused")
	}
	// The keys the release behind does read are published as they are: the
	// hold-back is one stage's entry and not the member.
	if p.ClaudeAccountStages["review"] != "acme-overflow" {
		t.Errorf("ClaudeAccountStages = %v, want review: acme-overflow beside the held-back stage", p.ClaudeAccountStages)
	}
	// The installation at the previous version converges: the reader of the
	// release 0.22.0 is this six-name vocabulary and nothing else, and the node
	// this release publishes carries no name outside it. Written out here
	// rather than derived — it is the second reader's copy of
	// config.AccountStagesAReleasedImageKnows, which
	// TestAccountStagesAReleasedImageKnowsIsRunStagesMinusThisReleases holds to
	// the run's own stage list.
	released := []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}
	for stage := range p.ClaudeAccountStages {
		if !slices.Contains(released, stage) {
			t.Errorf("the node names %q, which the release behind has no stage for: its reader refuses the node entire (%s is not a stage of a run)", stage, stage)
		}
	}
	// The control: the entry left out is the one that breaks that reader, so
	// what makes this node readable is the hold-back and not the shape of the
	// config — a test whose node happened to carry no such stage would pass
	// without the publisher doing anything.
	if !slices.Contains(config.RunStages, "triage") {
		t.Fatal("RunStages has no triage: this test's config names a stage the run does not have, so it would pass for the wrong reason")
	}
	if slices.Contains(released, "triage") {
		t.Fatal("the release-behind vocabulary carries triage: nothing needs holding back, and this test would pass for the wrong reason")
	}
}
