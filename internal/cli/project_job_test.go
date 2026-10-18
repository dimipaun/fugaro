package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The project-job half of the CLI's job naming (plan task 3.4): with one job
// shared by every repository admitted to a profile, the job no longer says
// which repository a run is for, and these tests are what says the callers
// that used to lean on it do not any more.
//
// What replaces the job's name is the FUGARO_RUN the execution itself
// reports: it is an override the launcher sets at launch, the execution
// carries it, and no run can change another execution's (design §4.1). Every
// fixture here therefore gives each execution its own FUGARO_RUN, as
// Cloud Run's own report of a launch does.

// otherRepo is the second repository the fixture admits to the same profile:
// with acme/app it shares one Cloud Run job, which is what phase 3 is.
// (otherSlug, its storage slug, is run_oauth_test.go's.)
const otherRepo = "acme/other"

// projectJobFixture is a cloudFixture whose installation has deployed one
// shared project job for the profile standard, and whose local config and
// published policy both define it, as `fugaro init` and `fugaro project
// policy publish` leave them.
type projectJobFixture struct {
	*launchBudgetFixture
	job string // the shared project job's name (gcp.ProjectJobName)
}

// projectJobPolicy is the policy the fixture publishes: the standard profile
// serving github, and every repository of the fixture's owner admitted to it.
// The profile mounts no secret, so its job runs as no Claude account and the
// job's account segment is empty (config.ClaudeAccountOfSecrets).
const projectJobPolicy = `{"version":2,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}}`

// projectJobTimeout is the profile's task timeout: what a project job's
// execution is bounded by, and so what the launcher's own reads of the job
// (the budget token's window) must answer with.
const projectJobTimeout = 6 * time.Hour

// newProjectJobFixture is newLaunchBudget plus the profile: two repositories
// admitted to one profile, the profile in the local config and in the
// published policy, and the shared job the installation would have deployed.
func newProjectJobFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	return newProjectJobFixtureMode(t, "off")
}

// newProjectJobFixtureMode is newProjectJobFixture on a budget installation
// whose local mode is mode: "enforce" is what a real enforcing installation
// runs, so a launch takes the budget precheck path as well
// (budgetBackendOn, launch_budget.go). The mode does not decide whether a
// launch mints a budget token: a published policy turns minting on whatever
// the mode says (budgetMintOn), and this fixture publishes one — so a test
// must not read the mode as "minting is on here, and off there".
func newProjectJobFixtureMode(t *testing.T, mode string) *projectJobFixture {
	t.Helper()
	f := newLaunchBudget(t, mode)
	addRepoToConfig(t, f.cloudFixture, otherRepo, "github")
	f.appendConfig(t,
		"profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h } }\n"+
			// A checkout: clone workflow runs the installation's base image,
			// which the launch gate holds to a release whose runner knows
			// FUGARO_CHECKOUT (internal/cli/recipes_skew.go).
			"base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.7.5 }\n")
	publishPolicy(t, f, projectJobPolicy)
	// The name is the caller's to compute (the fake cannot import
	// internal/backend/gcp: that package's own tests import the fake), and
	// the triple is what a test reads back out of it.
	job := gcp.ProjectJobName("standard", "github", "")
	f.run.AddProjectJob(job, "standard", "github", "", "2", "4Gi")
	f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	return &projectJobFixture{launchBudgetFixture: f, job: job}
}

// addRepoToConfig adds repo to the fixture's local config, beside acme/app:
// newCloudFixture writes the repos block whole, and a second top-level repos
// key would be a duplicate YAML key.
func addRepoToConfig(t *testing.T, f *cloudFixture, repo, provider string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const app = "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	if !strings.Contains(string(data), app) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", app, data)
	}
	added := app + "  " + repo + ": { provider: " + provider + ", base_branch: main, workflows: [web] }\n"
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), app, added, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// startRunExecution starts an execution of job for run slug/id, carrying the
// FUGARO_RUN a real launch sets (backend/gcp's Launch, the container
// override). A fixture that starts an execution without one is a fixture of
// an execution no run can claim: since task 3.4 the run's objects name an
// execution and the execution's own FUGARO_RUN is what says whose it is.
func startRunExecution(f *cloudFixture, job, slug, id string) string {
	return f.run.StartWithEnv(job, map[string]string{"FUGARO_RUN": slug + "/" + id})
}

// seedSharedRun writes a run of repo, stored under slug, whose launch.json
// names exec: the two bucket objects every view reads, and the only record
// of which execution a run is following.
func seedSharedRun(t *testing.T, f *cloudFixture, slug, repo, id, exec string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, slug, id)
	spec := &task.Spec{Version: 1, RunID: id, Repo: repo, Ref: "main", Workflow: "web", Task: "x", RequestedBy: "someone@example.com"}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// rowsByRun is ls's answer, keyed by "<slug>/<run-id>".
func rowsByRun(t *testing.T, args ...string) map[string]runviewRow {
	t.Helper()
	got, raw := lsJSON(t, append([]string{"--since", "0"}, args...)...)
	out := map[string]runviewRow{}
	for _, r := range got.Runs {
		out[r.Run] = runviewRow{Status: r.Status, Reason: r.Reason, Execution: r.Execution}
	}
	if len(out) != len(got.Runs) {
		t.Fatalf("ls listed a run twice: %s", raw)
	}
	return out
}

// runviewRow is the part of a listing row these tests judge.
type runviewRow struct {
	Status    string
	Reason    string
	Execution string
}

// TestListAndLogsAcrossRepositorySharedJobs is the core of task 3.4 step 2 and
// 3: two repositories share one Cloud Run job, so the job's name says nothing
// about which run an execution belongs to, and ls, logs and the execution
// check have to go by the FUGARO_RUN the execution itself reports.
//
// Mutation (run, restore): make CheckRunExecution compare the run's job name
// again — the two runs' executions are of one job, and the first assertion
// fails with both rows refused, while the lying run at the end is accepted.
func TestListAndLogsAcrossRepositorySharedJobs(t *testing.T) {
	f := newProjectJobFixture(t)
	const appRun, otherRun = "20261016-100000-aaaa", "20261016-100000-bbbb"
	appExec := f.run.StartWithEnv(f.job, map[string]string{"FUGARO_RUN": appSlug + "/" + appRun})
	otherExec := f.run.StartWithEnv(f.job, map[string]string{"FUGARO_RUN": otherSlug + "/" + otherRun})
	f.run.SetState(appExec, backend.StateRunning)
	f.run.SetState(otherExec, backend.StateRunning)
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", appRun, appExec)
	seedSharedRun(t, f.cloudFixture, otherSlug, otherRepo, otherRun, otherExec)
	// The entries are stamped now, after the launches above: logs reads from
	// shortly before its launch (logQuery), so a fixed timestamp from the
	// past would be filtered out and this assertion would hold for the wrong
	// reason.
	at := time.Now().UTC()
	f.logging.AddJSONLines(appExec, []byte(`{"time":"`+at.Format(time.RFC3339)+`","message":"acme/app said this"}`))
	f.logging.AddJSONLines(otherExec, []byte(`{"time":"`+at.Add(time.Second).Format(time.RFC3339)+`","message":"acme/other said that"}`))

	// ls: one shared job, two runs, each followed by its own FUGARO_RUN.
	rows := rowsByRun(t)
	for run, exec := range map[string]string{appSlug + "/" + appRun: appExec, otherSlug + "/" + otherRun: otherExec} {
		row, ok := rows[run]
		if !ok {
			t.Fatalf("ls left %s out: %+v", run, rows)
		}
		if row.Status != "running" || row.Execution != exec {
			t.Errorf("ls %s = status %q, execution %q (%s); want running and %s", run, row.Status, row.Execution, row.Reason, exec)
		}
	}

	// logs: the shared job's other execution's entries are not this run's,
	// however they share a job.
	out, _, err := execute(t, "logs", appSlug+"/"+appRun)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out, "acme/app said this") || strings.Contains(out, "acme/other said that") {
		t.Errorf("logs %s = %q", appSlug+"/"+appRun, out)
	}

	// A run whose own objects name another run's execution is refused, even
	// though that execution is of the same job: the run's service account
	// can write those objects, and with a shared job they are the only thing
	// that names the execution.
	const lie = "20261016-100000-cccc"
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", lie, otherExec)
	_, _, err = execute(t, "logs", appSlug+"/"+lie)
	if err == nil || !strings.Contains(err.Error(), "not following") {
		t.Errorf("logs of a run naming another run's execution: %v", err)
	}
	if row := rowsByRun(t)[appSlug+"/"+lie]; row.Status != "error" || row.Execution != "" {
		t.Errorf("ls of the run naming another run's execution = %+v", row)
	}
}

// TestCancelRefusesAnotherRunsExecution is task 3.4 step 2's security half.
// A run's own service account can write the objects that name its execution,
// so a run could point the operator's cancel at another run's execution. The
// two cases are the same repository's two runs — which share a job today,
// before any project job exists — and another repository's run on the shared
// job.
//
// Mutation (run, restore): compare the run's job name in CheckRunExecution
// again. The same-repository case is accepted (both runs are in the one job
// of that repository and workflow) and cancel hard-cancels the other run.
func TestCancelRefusesAnotherRunsExecution(t *testing.T) {
	f := newProjectJobFixture(t)
	const mine, theirs = "20261016-110000-aaaa", "20261016-110000-bbbb"
	// The other run is in the repository's own job: one job per (repository,
	// workflow) is today's model, and its two runs share it.
	theirsExec := f.run.StartWithEnv(gcp.JobName(appSlug, "web"), map[string]string{"FUGARO_RUN": appSlug + "/" + theirs})
	f.run.SetState(theirsExec, backend.StateRunning)
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", theirs, theirsExec)
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", mine, theirsExec)

	_, _, err := execute(t, "cancel", "--now", appSlug+"/"+mine)
	if err == nil || !strings.Contains(err.Error(), "not following") {
		t.Fatalf("cancel of a run naming another run's execution: %v", err)
	}
	if got := f.run.State(theirsExec); got != backend.StateRunning {
		t.Errorf("the other run's execution is %s: cancel acted on an execution that is not the run's", got)
	}
	if cancelled, err := runCancelRequested(t, f.cloudFixture, appSlug, mine); err != nil || cancelled {
		t.Errorf("the refused run was left a cancel marker (%v, %v): nothing of it may be touched", cancelled, err)
	}

	// The same, across repositories: the shared job holds both.
	const away = "20261016-110000-cccc"
	awayExec := f.run.StartWithEnv(f.job, map[string]string{"FUGARO_RUN": otherSlug + "/20261016-110000-dddd"})
	f.run.SetState(awayExec, backend.StateRunning)
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", away, awayExec)
	if _, _, err := execute(t, "cancel", "--now", appSlug+"/"+away); err == nil || !strings.Contains(err.Error(), "not following") {
		t.Fatalf("cancel of a run naming another repository's execution: %v", err)
	}
	if got := f.run.State(awayExec); got != backend.StateRunning {
		t.Errorf("the other repository's execution is %s", got)
	}
}

// runCancelRequested reports whether run id of slug has a cancel marker.
func runCancelRequested(t *testing.T, f *cloudFixture, slug, id string) (bool, error) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	return runstore.Open(b, slug, id).CancelRequested(ctx)
}

// TestMaxParallelCountsExecutionsNotJobs is task 3.4 step 3: max_parallel
// counts a run's executions, and with one job shared by every repository of a
// profile the count can no longer be read as a count of jobs. One active
// execution of another repository's run is what the fixture starts with, and
// the launch that follows is allowed because it is the second execution, not
// the second job; the launch after it is refused.
//
// Mutation (run, restore): count jobs instead of executions (deduplicate the
// listing by Job) — the second launch is allowed and the refusal below never
// comes.
func TestMaxParallelCountsExecutionsNotJobs(t *testing.T) {
	f := newProjectJobFixture(t)
	projectJobCheckout(t)
	// One active execution of the shared job, of the other repository.
	other := f.run.StartWithEnv(f.job, map[string]string{"FUGARO_RUN": otherSlug + "/20261016-120000-bbbb"})
	f.run.SetState(other, backend.StateRunning)

	const run = "20261016-120000-aaaa"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
		t.Fatalf("the first launch: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch made %d jobs.run calls: %+v", len(calls), calls)
	}
	// Which job it ran, asked of the fake by name: the triple is what the
	// launcher had to resolve from the policy, and asserting on the name the
	// test computed itself would pass for the wrong profile's job.
	profile, provider, account, ok := f.run.ProjectJobOf(calls[0].Job)
	if !ok || profile != "standard" || provider != "github" || account != "" {
		t.Fatalf("the launch ran on job %q = (%s, %s, %s), %v; want the shared job of the profile standard on github with no Claude account",
			calls[0].Job, profile, provider, account, ok)
	}
	if got := calls[0].Env["FUGARO_RUN"]; got != appSlug+"/"+run {
		t.Errorf("FUGARO_RUN = %q, want %q", got, appSlug+"/"+run)
	}

	// max_parallel is 2 and two executions are now active — the other
	// repository's and this launch's. A third is refused: the two are two
	// runs, and one job holding them is not one run.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261016-120000-cccc", "Add another feature")
	if err == nil || !strings.Contains(err.Error(), "2 runs active; max_parallel is 2") {
		t.Fatalf("the third launch: %v", err)
	}
	if n := len(f.run.Executions()); n != 2 {
		t.Errorf("%d executions after the refused launch, want 2 (%v)", n, f.run.Executions())
	}
}

// TestLaunchMintsTheSelectedJobsTimeout pins the launcher's other reader of
// the job: the budget token's window is the target job's own task timeout
// (mintBudgetToken), and on a project job that job is the profile's, not the
// per-repository job of the slug the run is stored under — which on a
// migrated installation does not exist at all, and would leave every run of a
// profile with a long timeout with a token that expires in the default ten
// minutes.
//
// Mutation (run, restore): read TaskTimeout(slug, variant) unconditionally
// again — the token's fx falls back to the ten-minute default and this test
// fails.
func TestLaunchMintsTheSelectedJobsTimeout(t *testing.T) {
	f := newProjectJobFixture(t)
	projectJobCheckout(t)
	const run = "20261016-130000-aaaa"
	start := time.Now()
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	tok := readToken(t, f.cloudFixture, run)
	if tok == "" {
		t.Fatal("the launch minted no budget token")
	}
	fx, _ := claimsOf(t, f.launchBudgetFixture, tok)["fx"].(json.Number)
	ms, err := fx.Int64()
	if err != nil {
		t.Fatalf("the token's fx = %v", fx)
	}
	// The profile's six hours, plus the queueing margin the mint adds. The
	// default ten minutes is well below it.
	if got := time.UnixMilli(ms); got.Before(start.Add(projectJobTimeout)) {
		t.Errorf("the token expires at %s, before the profile's own %s", got, projectJobTimeout)
	}
}

// projectJobRepoYAML is the fixture's checkout: acme/app, one checkout: clone
// workflow named web, which is the workflow the fixture's local config names.
const projectJobRepoYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  review_rounds: 2
workflows:
  web:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// projectJobOAuthRepoYAML is the same checkout under agent.auth: oauth, the
// mode a named Claude account is only legal in (claudeAccountAllowed), which
// is what a repository whose fugaro.yaml names one has.
const projectJobOAuthRepoYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: oauth
workflows:
  web:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// projectJobCheckout puts a checkout of acme/app in the working directory, so
// a launch of it reads the repository's own fugaro.yaml — which is what says
// whether its workflow is a checkout: clone one, and so whether it runs on a
// shared job at all (design §4.4: a baked workflow stays on the
// per-repository path).
func projectJobCheckout(t *testing.T) string {
	t.Helper()
	return projectJobCheckoutYAML(t, projectJobRepoYAML)
}

// projectJobCheckoutYAML is projectJobCheckout with the repository's own
// fugaro.yaml spelled by the caller.
func projectJobCheckoutYAML(t *testing.T, yaml string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	t.Chdir(dir)
	return dir
}

// projectJobAccountConfig is the fixture's local config addition: the two
// named Claude accounts this repository may select (config.AllowedClaudeAccounts),
// so that a launch naming one passes the allow-list gate and reaches the
// account check. Neither is mounted: no profile may mount a Claude OAuth token
// in this release (projectpolicy.validProfiles; internal/infra's
// projectJobSecrets has always refused to deploy one), so the fixture's profile
// mounts none and the job it names runs as no account.
const projectJobAccountConfig = "claude_accounts:\n" +
	"  acme-personal:\n    allow_data_to: [\"acme/app\"]\n" +
	"  acme-work:\n    allow_data_to: [\"acme/app\"]\n"

// newProjectJobAccountFixture is newProjectJobFixture with those accounts: the
// profile is the fixture's own (mounting no Claude token), so the job the
// installation deploys for it is gcp.ProjectJobName("standard", "github", "")
// — the empty account segment — and every run on it reads no account's token.
func newProjectJobAccountFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobFixture(t)
	f.appendConfig(t, projectJobAccountConfig)
	return f
}

// TestLaunchRefusesAnAccountTheProfileDoesNotMount is the account half of task
// 3.4 step 1: the account is an axis of the project job's own name, fixed by
// the profile's secrets where the job is deployed, so a run that selects an
// account the profile does not mount would run as one its own fugaro.yaml did
// not choose — and the credential the job holds is not the one the launch's
// allow_data_to check was made for. The launch stops instead.
//
// Mutation (run, restore): drop checkProjectJobAccount — the two --token
// launches below are accepted, take their claim, mint their budget token and
// reach Cloud Run on the profile's job, whose task.json then names an account
// the run did not choose.
//
// Since task 3.4a (the owner's ruling of 2026-10-17) an OAuth account on a
// shared project job is refused up front, and no profile may mount a Claude
// OAuth token at all (projectpolicy.validProfiles), so the account the fixture's
// profile runs as is none and the launch whose own credential is the default
// Claude OAuth account is the first refusal below — task 3.4a's check, not this
// one. The profile this fixture used to deploy (mounting acme-personal's token)
// is now a node the decoder refuses: TestLaunchRefusesAPolicyMountingAClaudeOAuthToken
// is that case.
func TestLaunchRefusesAnAccountTheProfileDoesNotMount(t *testing.T) {
	f := newProjectJobAccountFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	// The run's own credential is the default Claude OAuth account, and no
	// shared job holds one until phase 5: the launch stops here, before the
	// claim, the budget token and the Cloud Run call (task 3.4a).
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261016-140000-aaaa", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "phase 5") || !strings.Contains(err.Error(), "standard") {
		t.Fatalf("a launch whose own credential is the default OAuth account: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}

	// --token naming an account the repository may use is this check's own
	// refusal: the allow-list gate above it passed, and the profile's job runs
	// as no account at all.
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261016-140000-bbbb", "--token", "acme-work", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-work") || !strings.Contains(err.Error(), "standard") {
		t.Fatalf("a launch selecting acme-work: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}

	// And the account an earlier revision's profile mounted is no different:
	// no account is this profile's, so the selection is refused before the
	// claim, the budget token and the Cloud Run call.
	const run = "20261016-140000-cccc"
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", "acme-personal", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "standard") {
		t.Fatalf("a launch selecting acme-personal: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Fatalf("the refused launch made %d jobs.run calls (%+v), want none", len(calls), calls)
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Errorf("the refused launch minted a budget token: %s", tok)
	}
}

// projectJobReservedTokenPolicy is the node an owner can write by hand (the
// node is theirs), with the standard profile mounting the reserved
// claude-oauth-token. The account segment of the job that profile names is the
// default account on the launcher's and the runner's side and the empty account
// on the deployer's, and `fugaro init` refuses to deploy it at all
// (internal/infra's projectJobSecrets) — the state in which the launcher took
// the claim, minted the budget token and reached Cloud Run on a job that does
// not exist, and every run of the profile halted with cannotPlaceJob, which
// blames a stale module version instead of the profile.
const projectJobReservedTokenPolicy = `{"version":2,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"secrets":["claude-oauth-token"]}}}`

// TestLaunchRefusesAPolicyMountingAClaudeOAuthToken is the reserved token end
// to end, on the launcher's side: the node is written straight to the database
// (as an owner's own write, or an earlier CLI's publish, leaves it), and the
// launch of a repository the profile admits is refused where the node is read —
// projectpolicy.validProfiles, the one rule publish, init and both readers
// share — rather than on a job `fugaro init` refused to deploy. Nothing was
// launched: no claim, no budget token, no Cloud Run call.
func TestLaunchRefusesAPolicyMountingAClaudeOAuthToken(t *testing.T) {
	f := newProjectJobFixture(t)
	projectJobCheckout(t)
	publishPolicy(t, f.launchBudgetFixture, projectJobReservedTokenPolicy)

	const run = "20261016-150000-aaaa"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "person-owned") || !strings.Contains(err.Error(), "phase 5") {
		t.Fatalf("a launch on a policy mounting the reserved claude-oauth-token: %v", err)
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

// TestReadViewsRefuseAGoneExecution is the #460 review's D1: a run's own
// service account can write the result.json that names its execution, and an
// execution Cloud Run no longer has reports no FUGARO_RUN to check. Before
// the fix checkExecution treated "not found" as followable for every view, so
// a run naming a deleted execution of another repository's job — whose logs
// the log bucket still holds — had `fugaro logs` and `fugaro diagnose` print
// that repository's logs. The read views now refuse it; cancel keeps its
// tolerance of a vanished execution, and ls (which reads no logs) shows the
// row without one.
//
// Mutation (run, restore): make ownerLaunch follow a gone execution again
// (checkExecution's ErrNotFound case back to nil, nil for every caller) —
// logs prints the other repository's line and this test fails.
func TestReadViewsRefuseAGoneExecution(t *testing.T) {
	f := newProjectJobFixture(t)
	other := gcp.JobName(otherSlug, "web")
	f.run.AddJob(other, "4", "8Gi")
	// A name of the other repository's job that Cloud Run does not have (any
	// longer): its logs are still in the log bucket.
	live := f.run.Start(other)
	id, ok := backend.ParseExecution(live)
	if !ok {
		t.Fatalf("ParseExecution(%q) failed", live)
	}
	id.Name += "-zzzzz"
	gone := id.String()
	at := time.Now().UTC()
	f.logging.AddJSONLines(gone, []byte(`{"time":"`+at.Format(time.RFC3339)+`","message":"acme/other said this"}`))

	const lie = "20261016-120000-aaaa"
	seedSharedRun(t, f.cloudFixture, appSlug, "acme/app", lie, gone)
	writeRecord(t, f.cloudFixture, lie, &runstore.Record{Version: 1, RunID: lie, Execution: gone, Status: runstore.StatusFailed, Stage: "implement"})

	out, _, err := execute(t, "logs", appSlug+"/"+lie)
	if strings.Contains(out, "acme/other said") {
		t.Fatalf("logs printed another repository's entries for a gone execution: %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("logs of a run naming a gone execution: %v, want a refusal", err)
	}
	out, errOut, err := execute(t, "diagnose", "--json", appSlug+"/"+lie)
	if err != nil {
		t.Fatalf("diagnose of a run whose execution is gone: %v (%s)", err, errOut)
	}
	if strings.Contains(out+errOut, "acme/other said") {
		t.Fatalf("diagnose printed another repository's entries for a gone execution: %q", out)
	}
	if !strings.Contains(errOut, "no longer exists") {
		t.Errorf("diagnose stderr = %q, want a note on why there are no logs", errOut)
	}
}

// TestLaunchKeepsAnotherProvidersRepositoryOnItsOwnJob is the stack review's
// provider finding: a project job serves exactly its profile's provider (the
// provider is an axis of its name, and the runner binds the run's slug with
// it), and Bitbucket stays on the per-repository path in phase 3 (design
// §4.5, D5 as amended: no shared job ever serves bitbucket). Before the fix
// the launcher never compared the repository's provider with the profile's,
// so a Bitbucket repository's checkout: clone workflow was launched onto the
// github profile's job — after a claim and a budget token were spent — and
// the runner then halted on the slug binding. The launch now keeps it on its
// own per-repository job, as every other launch the shared job does not serve.
//
// Mutation (run, restore): drop the provider comparison in
// launchProjectJobFor — the launch goes to the github project job.
func TestLaunchKeepsAnotherProvidersRepositoryOnItsOwnJob(t *testing.T) {
	f := newProjectJobFixture(t)
	const bbRepo = "acme/bb"
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	bbSlug, err := task.Slug("bitbucket", bbRepo)
	if err != nil {
		t.Fatal(err)
	}
	perRepo := gcp.JobName(bbSlug, "web")
	f.run.AddJob(perRepo, "4", "8Gi")
	// The project admits the repository: what decides its job is the provider.
	publishPolicy(t, f.launchBudgetFixture, strings.Replace(projectJobPolicy, `["github/acme/*"]`, `["github/acme/*","bitbucket/acme/*"]`, 1))

	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@bitbucket.org:acme/bb.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1)})
	t.Chdir(dir)

	if _, _, err := execute(t, "run", "--repo", bbRepo, "--run-id", "20261017-090000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s, never the github profile's %s", len(calls), calls, perRepo, f.job)
	}
}

// TestLaunchKeepsABitbucketProfileOnThePerRepositoryJob is the stack review's
// other half of the same finding: the comparison above is on the repository's
// provider against the profile's, so a Bitbucket repository whose workflow
// selects a *Bitbucket* profile agreed with it and the launch went onto
// gcp.ProjectJobName(profile, "bitbucket", "") — a job `fugaro init` deploys
// for no profile (internal/infra's ProjectJobs skips a provider no shared job
// serves; the owner's ruling of 2026-10-15, D5 as amended). Cloud Run answers
// that jobs.run with a 404, which the backend wraps as a missing job, after
// the claim and the budget token were spent: the launch failed on a job that
// was never deployed, and the comment beside the comparison promised the
// opposite — that such a repository "stays on its per-repository job".
//
// The provider is therefore asked of the *profile*, not only compared: a
// profile of a provider no shared job serves is one no launch runs on, and a
// repository selecting it keeps the job it has always had — exactly as a
// repository selecting no profile the policy defines does. The runner needs
// nothing: on a per-repository job the selected profile is only checked to be
// defined in the policy (internal/runner's checkJobProfile), and a Bitbucket
// profile is defined — which is why the policy layer admits one
// (projectpolicy's validProfiles, and TestProfileAdmitsOnlyItsOwnProvider).
//
// Mutation (run, restore): make the comparison the whole rule again — drop
// the infra.SharedJobProvider(pr.Provider) half of the guard — and the launch
// is made on the Bitbucket profile's job, which no installation has.
func TestLaunchKeepsABitbucketProfileOnThePerRepositoryJob(t *testing.T) {
	f := newProjectJobFixture(t)
	const bbRepo = "acme/bb"
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	bbSlug, err := task.Slug("bitbucket", bbRepo)
	if err != nil {
		t.Fatal(err)
	}
	perRepo := gcp.JobName(bbSlug, "web")
	f.run.AddJob(perRepo, "4", "8Gi")
	// A policy a project with repositories on both providers publishes: the
	// github standard profile, and the Bitbucket one a Bitbucket workflow
	// selects. The project admits the Bitbucket repository, and the Bitbucket
	// profile admits it too — nothing below is about an admission.
	publishPolicy(t, f.launchBudgetFixture, `{"version":2,"allow_repos":["github/acme/*","bitbucket/acme/*"],`+
		`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"},`+
		`"standard-bitbucket":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h"}}}`)
	// The job a buggy launcher would compute, added to the fake so the failure
	// is the assertion below and not an unknown-job error: no installation
	// deploys it (ProjectJobs skips the profile), and a launch must not make it
	// a real one.
	never := gcp.ProjectJobName("standard-bitbucket", "bitbucket", "")
	f.run.AddProjectJob(never, "standard-bitbucket", "bitbucket", "", "2", "4Gi")

	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@bitbucket.org:acme/bb.git")
	yaml := strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1)
	yaml = strings.Replace(yaml, "  web:\n", "  web:\n    job_profile: standard-bitbucket\n", 1)
	if !strings.Contains(yaml, "job_profile: standard-bitbucket") {
		t.Fatal("the fixture has no web: workflow to put a job_profile under")
	}
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	t.Chdir(dir)

	if _, _, err := execute(t, "run", "--repo", bbRepo, "--run-id", "20261017-090000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s, never the Bitbucket profile's %s, which no installation deploys", len(calls), calls, perRepo, never)
	}
}
