package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The account-choice keys are the *operator's*: `default_claude_account` and
// `stages.<stage>.claude_account` are written in the operator's own project
// config and nowhere else, so a repository on the cascade — the one auth whose
// credential the project's credentials service picks from its own tiers — must
// not be refused for them. That is what Check 191 met, and
// config.CascadeSkippedStageAccounts is the rule it asked for. These tests are
// that exemption at both readers that hold an auth: `fugaro validate` and the
// launch, with the refusals every other auth keeps beside them.

// cascadeStageAccountProject is the operator's state Check 191 was run
// against: a project config that names accounts for the senior review and the
// adversarial review (the project-wide default and the two stage keys), over
// the database, budget and published switch a cascade validate reads. keys is
// appended to the accounts block the caller passes.
func cascadeStageAccountProject(t *testing.T, accounts, keys string) *launchBudgetFixture {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, cascadePolicyAllowList("enforce"))
	f.appendConfig(t, accounts+keys)
	return f
}

// cascadeMixProject is the project above with one account serving both mixed
// stages, which is the configuration the B0 ruling admits for an api-key run
// and therefore the one a cascade file must be admitted for too.
func cascadeMixProject(t *testing.T) *launchBudgetFixture {
	t.Helper()
	return cascadeStageAccountProject(t, stageAccountAccountsYAML,
		"default_claude_account: acme-overflow\n"+
			stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow"))
}

// cascadeStageAccountsNote is the one line a cascade run is told about them, as
// config.CascadeSkippedStageAccounts writes it and as both readers print it:
// `fugaro validate` prefixes its notes with "note: ", and the launch prints the
// same word itself (noteLine).
const cascadeStageAccountsNote = "note: project stage accounts do not apply to a cascade repository: adversarial, review skipped"

// TestValidateSkipsTheProjectStageAccountsOfACascadeRepository is the check's
// offline half: the project config that an api-key file of the same project is
// admitted under admits a cascade file, which says on one line which keys it
// did not read and reports nothing about them. The api-key file beside it is
// judged by the ruling exactly as it was — the same keys, the same project, and
// no cascade note, because its own auth reads them.
//
// Mutation (run, restored): call config.CheckStageAccountMix with the local
// stage accounts without dropping them first — the cascade file is refused with
// "a stage account only applies to a run whose agent.auth is oauth or api-key",
// which is the state Check 191 met.
func TestValidateSkipsTheProjectStageAccountsOfACascadeRepository(t *testing.T) {
	cascadeMixProject(t)
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err != nil {
		t.Fatalf("a cascade file under a project whose config names stage accounts: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(errOut, cascadeStageAccountsNote) {
		t.Errorf("the note is not printed:\nstderr:\n%s", errOut)
	}
	if n := strings.Count(errOut, "do not apply to a cascade repository"); n != 1 {
		t.Errorf("the note is printed %d times, want once:\nstderr:\n%s", n, errOut)
	}
	if strings.Contains(out, "claude_account") {
		t.Errorf("the cascade file was refused for the project's stage accounts:\n%s", out)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("the cascade file is not reported valid:\n%s", out)
	}

	apiKey := strings.Replace(cascadeCLI, "auth: cascade", "auth: api-key", 1)
	out, errOut, err = execute(t, "validate", writeConfig(t, apiKey))
	if err != nil {
		t.Fatalf("the permitted api-key mix was refused: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if strings.Contains(errOut, "do not apply to a cascade repository") {
		t.Errorf("an api-key file was told about the cascade's exemption:\n%s", errOut)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("the api-key file is not reported valid:\n%s", out)
	}
}

// TestValidateKeepsTheRefusalEveryOtherAuthOfTheProjectMeets: the exemption is
// the cascade's and not a loosening of the B0 ruling. A project whose stage
// keys name two accounts is admitted for its cascade repository — the keys are
// the operator's project-wide ones and no account of theirs reaches a cascade
// run — and refused for its api-key one, with the sentence it was refused with
// before (TestValidateRefusesASecondStageAccount, over the same rule and a
// fixture of its own).
func TestValidateKeepsTheRefusalEveryOtherAuthOfTheProjectMeets(t *testing.T) {
	cascadeStageAccountProject(t,
		stageAccountAccountsYAML+"  acme-personal: { allow_data_to: [acme/other] }\n",
		stagesBlock("review", "acme-overflow", "adversarial", "acme-personal"))
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err != nil {
		t.Fatalf("a cascade file is refused for the project's two stage accounts: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(errOut, cascadeStageAccountsNote) {
		t.Errorf("the note is not printed:\nstderr:\n%s", errOut)
	}

	apiKey := strings.Replace(cascadeCLI, "auth: cascade", "auth: api-key", 1)
	out, _, err = execute(t, "validate", writeConfig(t, apiKey))
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "one provider key and one Claude account") {
		t.Fatalf("an api-key file naming two accounts: exit %d, want the ruling's refusal\n%s", ExitCode(err), out)
	}
}

// TestValidateRefusesARepositoryThatNamesItsOwnStageAccount is the other half
// of the exemption's scope: what is skipped is the *operator's* key, and a
// repository has none to write. The account-choice keys live in the machine's
// project config (localcfg; a shared file that carries them is refused by name),
// so a fugaro.yaml that spells `stages:` — the cascade included — is a file its
// author has put the choice in the wrong place in, and the strict decode
// refuses it rather than reading it as a repository's own declaration.
func TestValidateRefusesARepositoryThatNamesItsOwnStageAccount(t *testing.T) {
	cascadeMixProject(t)
	file := cascadeCLI + "stages:\n  review:\n    claude_account: acme-overflow\n"
	out, _, err := execute(t, "validate", writeConfig(t, file))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a fugaro.yaml naming its own stage account: exit %d, want a user refusal\n%s", ExitCode(err), out)
	}
	if !strings.Contains(out, "field stages not found in type config.Config") {
		t.Errorf("the refusal does not say the file has no such key:\n%s", out)
	}
}

// cascadeAccountCheckout makes the working directory a checkout of acme/other
// whose fugaro.yaml asks for the cascade: the launch's own reader is what these
// keys reach next, and the auth it judges them against is this document's.
func cascadeAccountCheckout(t *testing.T, f *cloudFixture) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(otherSlug, "svc"), "4", "8Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: cascade\n" +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	t.Chdir(dir)
}

// TestClaudeAccountForSkipsTheProjectStageAccountsOfACascadeRun is the check's
// launch half, at the call the launch makes once per stage it resolves: no rung
// resolves an account of the project's, nothing is refused, and the note is
// printed once for the whole launch rather than once per call.
//
// The keys here are the ones that refuse most: the default and the senior
// review's key both name an account this repository may *not* use
// (acme-elsewhere is allowed for acme/person-1), and the adversarial review's
// names a second account. A cascade launch must meet neither refusal — the keys
// are the operator's project-wide ones and no stage of this run reads them —
// which is the whole of what Check 191 asked for.
func TestClaudeAccountForSkipsTheProjectStageAccountsOfACascadeRun(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+"  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		"default_claude_account: acme-elsewhere\n"+
		stagesBlock("review", "acme-elsewhere", "adversarial", "acme-personal"))
	cascadeAccountCheckout(t, f)
	var warned bytes.Buffer
	prev := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prev })

	env := gcsEnv(t, f)
	co := checkoutConfig(context.Background(), "acme/other")
	if co == nil {
		t.Fatal("checkoutConfig found no checkout")
	}
	for _, stage := range []string{"", "review", "adversarial", "implement", "triage"} {
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", stage, "", co, nameWritten)
		if err != nil {
			t.Fatalf("stage %q: a cascade launch was refused for the project's stage accounts: %v", stage, err)
		}
		if got != config.DefaultClaudeAccount {
			t.Errorf("stage %q: account = %q, want the unsuffixed default: no account of the project's reaches a cascade run", stage, got)
		}
	}
	if got := warned.String(); got != cascadeStageAccountsNote+"\n" {
		t.Errorf("stderr = %q, want exactly %q printed once", got, cascadeStageAccountsNote)
	}
}

// TestARunOfACascadeRepositoryPrintsTheNote: the note reaches a launch's own
// stderr through the call the launch itself makes, and the launch itself is
// what prints it — claudeAccountFor alone would be pinned by the test above.
//
// The launch is refused on purpose, at the gate a cascade installation without
// a service meets (cascadeLaunchRefusal, TestCascadeRefusedWithoutService): the
// account is resolved while the spec is built, before that gate, so what this
// pins is that a person typing `fugaro run` reads the operator's project
// accounts here and is told on one line which of them the cascade skips —
// whatever answers next in the launch.
func TestARunOfACascadeRepositoryPrintsTheNote(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+"  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		"default_claude_account: acme-elsewhere\n"+
		stagesBlock("review", "acme-elsewhere", "adversarial", "acme-personal"))
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	var warned bytes.Buffer
	prev := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prev })

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261111-100000-ca91", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade launch: err = %v, want the installation's refusal", err)
	}
	if !strings.Contains(err.Error(), "credentials service") {
		t.Errorf("the launch was refused for something other than the missing service — the accounts of the project's, say: %v", err)
	}
	if got := warned.String(); got != cascadeStageAccountsNote+"\n" {
		t.Errorf("stderr = %q, want exactly %q printed once by the launch", got, cascadeStageAccountsNote)
	}
}

// TestClaudeAccountForKeepsTheApiKeyMixOfTheSameProject: the same fixture with
// the repository asking for api-key instead. Nothing about the exemption
// reaches it — the stage keys are the mix's, which is the rung
// TestClaudeAccountForResolvesTheStagesOwnKey pins over its own fixture, and the
// default that names an account this repository may not use is refused where
// rung 4 would hand it to a stage (mixedStageAccount's allow-list).
func TestClaudeAccountForKeepsTheApiKeyMixOfTheSameProject(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+
		stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow"))
	recipeCheckout(t, f, "", nil)
	var warned bytes.Buffer
	prev := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prev })

	env := gcsEnv(t, f)
	co := checkoutConfig(context.Background(), "acme/other")
	if co == nil {
		t.Fatal("checkoutConfig found no checkout")
	}
	if got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co, nameWritten); err != nil || got != "acme-overflow" {
		t.Errorf("review: account = %q, err = %v, want acme-overflow (the stage's own key)", got, err)
	}
	if warned.Len() != 0 {
		t.Errorf("an api-key launch was told about the cascade's exemption: %q", warned.String())
	}
}
