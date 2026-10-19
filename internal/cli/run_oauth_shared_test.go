package cli

import (
	"context"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// An OAuth launch onto a shared project job (plan task 3.4a, the owner's ruling
// of 2026-10-17, replaced by task 3's gate — design §4.4 rows 4 to 6): a shared
// project job may serve a named Claude account, because each account has a job
// of its own whose container holds that account's token (config.ProjectJobAccounts,
// internal/infra's ProjectJobs). What remains refused is everything that would
// put a run on a job whose credential is not the run's own:
//
//   - an OAuth launch whose account the selected profile serves no job for —
//     including the default account on a profile that does not list `default`,
//     whose job mounts no token at all (design §4.4 rows 5 and 9);
//   - a launch whose repository the account's own policy list does not cover
//     (rows 3 and 4);
//   - a non-OAuth launch onto an account job, which is the runner's own half
//     (row 6).
//
// Every one of them is refused where the job is decided, before the claim,
// before the budget token and before any Cloud Run call, and the refusal is
// scoped to shared project jobs: a repository that still has its own job — a
// baked workflow, or an installation that has published no profiles — launches
// an OAuth run exactly as it always has.

// TestOAuthLaunchOntoASharedProjectJobIsRefusedBeforeLaunch is the refusal's
// first test, in the state every installation that has not adopted accounts is
// in: an `agent.auth: oauth` checkout whose clone workflow selects a published
// profile that serves no Claude account, so the job it would run on mounts no
// token at all — an OAuth run on it starts with no credential. Nothing of the
// launch happened.
//
// Mutation (run, restore): drop the config.ProjectJobTokenAccount half of
// projectJobAccountFor — the launch is admitted onto a job whose container
// holds no token, takes the claim (the hook below fires), mints a token and
// starts an execution.
func TestOAuthLaunchOntoASharedProjectJobIsRefusedBeforeLaunch(t *testing.T) {
	// The installation runs the budget backend in its enforcing mode, so this
	// launch takes the path a real one takes: the precheck runs before the job
	// selection. The mode is not what makes the token assertion below able to
	// fail — this fixture publishes a policy, and a published policy turns
	// minting on whatever the local mode says (budgetMintOn) — so a launch
	// that got past the refusal mints in either mode.
	f := newProjectJobFixtureMode(t, "enforce")
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	// The claim is the first thing a launch spends, and everything the
	// refusal must precede — the budget token and the Cloud Run call — is
	// behind it, so a claim the hook never saw is the whole of "nothing was
	// launched" in one assertion.
	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	const run = "20261017-100000-aaaa"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "standard") || !strings.Contains(err.Error(), "claude-oauth-token") {
		t.Fatalf("an OAuth launch onto the shared job of a profile that serves no Claude account: %v, want a user error naming the profile and the token its job would hold", err)
	}
	if claimed {
		t.Error("the refusal came after the launch claim: the claim, the budget token and the Cloud Run call all happen past it, so a launch that reaches it has already spent them")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch made %d jobs.run calls (%+v), want none", len(calls), calls)
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Errorf("the refused launch minted a budget token: %s", tok)
	}
}

// TestOAuthAccountRefusalNamesTokenProfileAndFix is the gate's text: one
// function decides both halves — the account a launch may target and the
// refusal of the ones it may not — and every refusal names the account, the
// token and the profile it is about, while nothing else is refused at all.
//
// Mutation (run, restore): read the run's selection without the profile's own
// job set — `if !slices.Contains(...)` dropped from projectJobAccountFor — and
// the oauth rows naming an account the profile serves no job for stop being
// refused: the launch is admitted onto a job whose token the run's own
// configuration did not choose.
func TestOAuthAccountRefusalNamesTokenProfileAndFix(t *testing.T) {
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal", config.DefaultClaudeAccount},
	}}
	plain := projectpolicy.Profile{JobProfile: config.JobProfile{Provider: "github"}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal":             {AllowRepos: []string{"github/acme/app"}},
		config.DefaultClaudeAccount: {AllowRepos: []string{"github/acme/app"}},
	}}
	cases := []struct {
		name    string
		auth    string
		acct    string
		profile projectpolicy.Profile
		want    string // the account the launch targets; "" is the no-account job
		refuse  bool
	}{
		{name: "an oauth workflow on the default account, which the profile serves", auth: "oauth", profile: serving},
		{name: "an oauth workflow on a named account the profile serves", auth: "oauth", acct: "acme-personal", profile: serving, want: "acme-personal"},
		{name: "an oauth workflow on the default account of a profile that does not list it", auth: "oauth", profile: plain, refuse: true},
		{name: "an oauth workflow on a named account of a profile that serves none", auth: "oauth", acct: "acme-personal", profile: plain, refuse: true},
		// A named account is only ever selected by a workflow whose auth is
		// oauth (claudeAccountAllowed, run.go), and spec.OAuthToken is empty
		// for the default account (run.go sets it only for another one), so a
		// selected name on a non-oauth run is refused before this gate. What
		// the gate itself must not do is put such a run on an account job: the
		// job a run is launched on is chosen by the run's own credential, and
		// nothing a task.json or a flag carries can move it there.
		{name: "an api-key workflow", auth: "api-key", profile: serving},
		{name: "a vertex workflow with a stale selection", auth: "vertex", acct: "acme-personal", profile: serving},
		{name: "no auth resolved", auth: "", profile: serving},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := projectJobAccountFor(p, "standard", c.profile, c.auth, c.acct, "github", "acme/app")
			if !c.refuse {
				if err != nil {
					t.Fatalf("no Claude account is refused here, so the launch is admitted: %v", err)
				}
				if got != c.want {
					t.Fatalf("the launch targets the account %q, want %q", got, c.want)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v (%d), want a user error", err, ExitCode(err))
			}
			msg := err.Error()
			for _, want := range []string{"standard", "claude_accounts", "CLAUDE_CODE_OAUTH_TOKEN", "no claim was taken"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			account := c.acct
			if account == "" {
				account = config.DefaultClaudeAccount
			}
			if !strings.Contains(msg, account) || !strings.Contains(msg, config.ClaudeAccountSecret(account)) {
				t.Errorf("the refusal does not name the account %q and its token %s: %v", account, config.ClaudeAccountSecret(account), err)
			}
			// It names the setting, never the file that carries it: the profile
			// a launch runs on is the published policy's, and the account list
			// is the project's local config's, so blaming fugaro.yaml would
			// send the operator to the wrong place.
			if strings.Contains(msg, "fugaro.yaml") {
				t.Errorf("the refusal blames a file for a setting it cannot attribute: %v", err)
			}
		})
	}
}

// TestOAuthLaunchOnALegacyJobIsUnchanged is the ruling's second test: the
// refusal is scoped to shared project jobs, so an OAuth run whose workflow
// keeps a per-repository job — here a baked one, which design §4.4 leaves on
// the per-repository path — launches exactly as it did, on the job of its own
// (slug, workflow, account) variant.
//
// Mutation (run, restore): refuse every OAuth launch instead of only the ones
// that target a shared job — this launch stops, and the run's own job, the one
// that mounts its account's token, is never used.
func TestOAuthLaunchOnALegacyJobIsUnchanged(t *testing.T) {
	f := newProjectJobFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [\"acme/app\"]\n")
	// The fixture's policy is published and defines standard, so a launch that
	// went to the shared job would be refused: what keeps this one on its own
	// job is the workflow's being baked, not the absence of a profile.
	projectJobCheckoutYAML(t, strings.Replace(projectJobOAuthRepoYAML, "    checkout: clone\n", "", 1))

	// The variant job of the named account, and the build record that says the
	// image's runner knows task.Spec.OAuthToken (checkImageSince) and reads the
	// sealed budget token (the seal gate, sealSince — this fixture publishes a
	// policy, so this launch mints).
	perRepo := gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-personal"))
	f.run.AddJob(perRepo, "2", "4Gi")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseSeal)

	const run = "20261017-110000-bbbb"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", "acme-personal", "Add a feature"); err != nil {
		t.Fatalf("an OAuth launch on its own per-repository job: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s, never the profile's %s", len(calls), calls, perRepo, f.job)
	}
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, appSlug, run).ReadTask(context.Background())
	if err != nil || spec.OAuthToken != "acme-personal" {
		t.Errorf("the stored task = %+v, %v; want the account this run's own job mounts", spec, err)
	}
}
