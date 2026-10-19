package cli

import (
	"context"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// An OAuth launch onto a shared project job (plan task 3.4a, the owner's
// ruling of 2026-10-17): every Claude OAuth token is refused on a shared
// (profile) job — a profile is shared by every repository admitted to it, and
// an account's allow_data_to is a per-repository list a shared job cannot
// honour — and `fugaro init` refuses to deploy one, so an installation whose
// runs use `agent.auth: oauth` has no shared job at all until phase 5 decides
// how an account reaches one (design docs/design/project-centric.md §4.1 and
// the shared-job section; docs/project-layer.md's "Which runs work on a
// shared job today").
//
// Before the refusal, such a launch was started on a job that cannot serve
// it: the claim was taken, a budget token was minted, and the launch then
// failed in Cloud Run — the profile's own job does not exist when the profile
// mounts an account (`internal/infra`'s projectJobSecrets refuses to deploy
// it) and reads no token when it does not. The launcher now refuses it where
// it decides the job, before any of that (internal/cli/project_job.go).
//
// The refusal is scoped to shared project jobs: a repository that still has
// its own job — a baked workflow, or an installation that has published no
// profiles — launches an OAuth run exactly as it always has, and the tests
// below pin both halves.

// TestOAuthLaunchOntoASharedProjectJobIsRefusedBeforeLaunch is the ruling's
// first test: an `agent.auth: oauth` checkout whose clone workflow selects a
// published profile is refused, and nothing of the launch happened.
//
// Mutation (run, restore): drop checkProjectJobOAuth from launchProjectJobFor
// — the launch takes the claim (the hook below fires), mints a token and
// starts an execution on the shared job.
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "standard") || !strings.Contains(err.Error(), "phase 5") {
		t.Fatalf("an OAuth launch onto a shared project job: %v, want a user error naming the profile and phase 5", err)
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
	// image's runner knows task.Spec.OAuthToken (checkImageSince).
	perRepo := gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-personal"))
	f.run.AddJob(perRepo, "2", "4Gi")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", release065)

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

// TestOAuthRefusalNamesAccountProfileAndFix is the ruling's third test: one
// function, one text, naming the account the run uses, the profile it would
// have run on and the fix — and refusing nothing at all when no OAuth account
// is involved, which is what keeps every other launch byte-identical.
//
// Mutation (run, restore): read the selected account as well as the auth —
// `if auth != "oauth" && acct == "" { return nil }` — and the api-key row
// carrying a named account stops launching, which is the case the check must
// leave alone.
func TestOAuthRefusalNamesAccountProfileAndFix(t *testing.T) {
	cases := []struct {
		name   string
		auth   string
		acct   string
		refuse bool
	}{
		{name: "an oauth workflow on the default account", auth: "oauth", refuse: true},
		{name: "an oauth workflow on a named account", auth: "oauth", acct: "acme-personal", refuse: true},
		{name: "a named account selected by flag", auth: "oauth", acct: "acme-work", refuse: true},
		// The check reads the resolved agent.auth and nothing else: a named
		// account is only ever selected by a run whose auth is oauth
		// (claudeAccountAllowed), and spec.OAuthToken is empty for the default
		// account (run.go sets it only for another one), so an api-key or
		// Vertex run is untouched here whatever account it carries.
		{name: "an api-key workflow on a named account", auth: "api-key", acct: "acme-work"},
		{name: "an api-key workflow", auth: "api-key"},
		{name: "a vertex workflow", auth: "vertex"},
		{name: "no auth resolved", auth: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkProjectJobOAuth("standard", c.auth, c.acct)
			if !c.refuse {
				if err != nil {
					t.Fatalf("no Claude OAuth account is selected, so the launch is not refused here: %v", err)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v (%d), want a user error", err, ExitCode(err))
			}
			msg := err.Error()
			for _, want := range []string{"standard", "phase 5", "agent.auth", "API-key", "per-repository"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if c.acct != "" && !strings.Contains(msg, c.acct) {
				t.Errorf("the refusal does not name the account %q: %v", c.acct, err)
			}
			// It names the setting, never the file that carries it: the resolved
			// agent.auth may come from the project layer rather than the
			// repository's own fugaro.yaml (config.Resolve overlays the layer's
			// defaults), and blaming a file that does not set it would send the
			// operator to the wrong place.
			if strings.Contains(msg, "fugaro.yaml") {
				t.Errorf("the refusal blames a file for a setting it cannot attribute: %v", err)
			}
		})
	}
}
