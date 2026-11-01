package cli

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// published policy both define it, as the converge and `fugaro project
// policy publish` leave them.
type projectJobFixture struct {
	*launchBudgetFixture
	job string // the shared project job's name (gcp.ProjectJobName)
}

// projectJobPolicy is the policy the fixture publishes: the standard profile
// serving github, and every repository of the fixture's owner admitted to it.
// The profile lists no Claude account, so its job runs as no account and the
// job's account segment is empty (config.ProjectJobAccounts).
const projectJobPolicy = `{"version":4,"allow_repos":["github/acme/*"],` +
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
			// FUGARO_CHECKOUT and, for the launches in this fixture, reads the
			// sealed budget token (internal/cli/recipes_skew.go: cloneSince and
			// sealSince — the fixture publishes a policy, so a launch here
			// mints whatever the local mode says).
			"base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+sealSince+" }\n")
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
	// The object in the bucket is a sealed envelope, not the token (the
	// seal's launcher half): launchedToken opens it with the key this
	// launch's execution received, and fails if the launch minted none.
	tok := launchedToken(t, f.cloudFixture, run)
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
// fugaro.yaml spelled by the caller, committed on main and published as
// origin/main — the ref a launch's spec.Ref names, and the one the launch
// reads agent.auth from (projectJobAgentAuth): a checkout with no commit has
// no ref for a launch to be judged on, which is not the state these tests are
// about.
func projectJobCheckoutYAML(t *testing.T, yaml string) string {
	t.Helper()
	return projectJobCheckoutYAMLAt(t, yaml, yaml)
}

// projectJobCheckoutYAMLAt is projectJobCheckoutYAML with the working tree's
// fugaro.yaml (working) spelled apart from the one committed on main and
// published as origin/main (committed), so a test can put the two documents at
// odds — the state a launcher whose working tree has moved ahead of the ref
// the run clones is in, and the one the launch has to resolve from.
func projectJobCheckoutYAMLAt(t *testing.T, working, committed string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": committed})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": working})
	t.Chdir(dir)
	return dir
}

// projectJobRefRepoYAML is the anchored checkout (projectJobAnchoredRepoYAML)
// with an agent block of its own: the document a launch resolves agent.auth
// from when the working tree and the ref disagree, spelled by the auth it
// carries.
func projectJobRefRepoYAML(auth string) string {
	return strings.Replace(projectJobAnchoredRepoYAML, "git:\n", "agent: { auth: "+auth+" }\ngit:\n", 1)
}

// TestSharedJobLaunchJudgesTheRefNotTheWorkingTree is the finding that the
// launcher's own resolution is not the run's: the run resolves agent.auth from
// the fugaro.yaml at the ref it checks out (spec.Ref, over the layer the task
// carries), so the working tree is a document the run never reads. Here the
// working tree says api-key while origin/main — the ref this launch names —
// says oauth, the state a checkout left on another branch, or one carrying an
// uncommitted edit, is in, and the launch must be refused exactly as if the
// working tree said oauth too.
//
// Mutation (run, restore): read the working tree again
// (readCheckoutFugaroYAML(filepath.Join(root, "fugaro.yaml"))) — this launch is
// judged api-key and taken onto the shared job, whose profile holds no OAuth
// token: the run halts inside Cloud Run, past the claim and the budget token,
// which is the failure the account gate exists to refuse.
func TestSharedJobLaunchJudgesTheRefNotTheWorkingTree(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	projectJobCheckoutYAMLAt(t, projectJobRefRepoYAML("api-key"), projectJobRefRepoYAML("oauth"))

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261019-090000-aaaa", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") || !strings.Contains(err.Error(), "standard") {
		t.Fatalf("a launch whose ref says oauth and whose working tree says api-key: %v, want the account gate's refusal", err)
	}
	if claimed {
		t.Error("the refusal came after the launch claim")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}

// The other direction, which is what makes the first one a statement about the
// ref rather than about refusing more: the ref's copy says api-key while the
// working tree says oauth, so this is a run the account gate allows and it must
// launch — on the shared job — over the file the run will actually read.
//
// Mutation (run, restore): read the working tree again — this launch is
// refused by the account gate although the run it starts is an api-key one, which
// is a false refusal on a document the run never reads.
func TestSharedJobLaunchLaunchesOnTheRefsApiKey(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	projectJobCheckoutYAMLAt(t, projectJobRefRepoYAML("oauth"), projectJobRefRepoYAML("api-key"))

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261019-090001-bbbb", "Add a feature"); err != nil {
		t.Fatalf("a launch whose ref says api-key and whose working tree says oauth: %v, want it launched", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != f.job {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the shared job %s", len(calls), calls, f.job)
	}
}

// projectJobRefAccountYAML is projectJobAnchoredRepoYAML with an agent block
// naming an OAuth Claude account — or, with "", no account at all: the two
// documents a checkout can put at odds about which account a run is.
func projectJobRefAccountYAML(token string) string {
	agent := "agent: { auth: oauth }"
	if token != "" {
		agent = "agent: { auth: oauth, oauth_token: " + token + " }"
	}
	return strings.Replace(projectJobAnchoredRepoYAML, "git:\n", agent+"\ngit:\n", 1)
}

// TestProjectJobAccountComesFromTheRef is the account's own half of the rule
// above, and the finding that the selection is not: agent.auth was already
// judged on the fugaro.yaml at the ref the run checks out, while the account
// the launch runs as came from the working tree — so a checkout left on another
// branch, or carrying an uncommitted agent.oauth_token edit, put the run on
// another account's job (that account's token in the container, and that
// account's allow_repos deciding whether this repository may use it) with
// nothing to refuse it.
//
// Both directions are pinned, because a source is only pinned if the two
// documents disagree about which it is. Here the ref names acme-personal and
// the working tree names none: the launch runs on acme-personal's job, and the
// task it stores selects acme-personal — not the profile's no-account job,
// which holds the default account's token. The other half, below, has the
// working tree name an account the ref does not: the ref's own answer is "no
// account", so the launch rides the no-account job and the working tree's name
// is nowhere in the task.
//
// Mutation (run, restore): resolve the name from the checkout again
// (co.Agent.OAuthToken) — the first half launches on the profile's no-account
// job, holding the default account's token for a run whose own document names
// acme-personal, and the second launches on acme-personal's job for a run whose
// document names none: both halves fail.
func TestProjectJobAccountComesFromTheRef(t *testing.T) {
	t.Run("the ref names the account the working tree omits", func(t *testing.T) {
		f := newProjectJobServingFixture(t)
		isolateCache(t)
		projectJobCheckoutYAMLAt(t, projectJobRefAccountYAML(""), projectJobRefAccountYAML("acme-personal"))

		const run = "20261019-100000-aaaa"
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
			t.Fatalf("a launch whose ref names acme-personal and whose working tree names none: %v", err)
		}
		profile, provider, account := launchedProjectJob(t, f.cloudFixture)
		if profile != "standard" || provider != "github" || account != "acme-personal" {
			t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, acme-personal) — the account the ref names", profile, provider, account)
		}
		if got := readSpec(t, f.cloudFixture, run).OAuthToken; got != "acme-personal" {
			t.Errorf("the stored task selects %q, want acme-personal: the run re-decides the account its job holds from its own task.json (the runner's checkProjectJobAccount)", got)
		}
	})
	t.Run("the working tree's account is not the ref's", func(t *testing.T) {
		f := newProjectJobServingFixture(t)
		isolateCache(t)
		projectJobCheckoutYAMLAt(t, projectJobRefAccountYAML("acme-personal"), projectJobRefAccountYAML(""))

		const run = "20261019-100001-bbbb"
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
			t.Fatalf("a launch whose ref names no account and whose working tree names acme-personal: %v", err)
		}
		profile, provider, account := launchedProjectJob(t, f.cloudFixture)
		if profile != "standard" || provider != "github" || account != "" {
			t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, \"\") — the ref names no account, so the run's own credential is the default one and it rides the profile's no-account job", profile, provider, account)
		}
		if got := readSpec(t, f.cloudFixture, run).OAuthToken; got != "" {
			t.Errorf("the stored task selects %q, want the default account (empty): the working tree's own name is not a selection the run's document makes", got)
		}
	})
}

// projectJobLayerAuthRepoYAML is the anchored checkout with an agent block
// that only names the account and writes no auth of its own: the shape a
// project that has adopted Claude accounts writes, because agent.auth is the
// project layer's to set (docs/design/layered-config.md's scopes table, and
// the fixture layer's defaults say `agent: { auth: oauth }`) while only a
// repository may name an account (named-oauth-tokens T2 — no layer may).
func projectJobLayerAuthRepoYAML(token string) string {
	return strings.Replace(projectJobAnchoredRepoYAML, "git:\n", "agent: { oauth_token: "+token+" }\ngit:\n", 1)
}

// TestProjectJobNamedAccountJudgedWithTheLayersAuth is the other half of the
// account rule: the account comes from the ref's own file, but the auth it is
// judged against is the *layer-resolved* document — the one the run resolves
// (projectJobAgentAuth) and doctor reports on. Reading the ref's bytes raw
// answered Fugaro's default for this shape (applyDefaults' vertex, the file
// naming no auth) and refused the launch with "acme-personal only applies to a
// workflow whose agent.auth is oauth; acme/app's agent.auth is vertex" — a
// false refusal, in the direction this gate exists to make safe, of a run the
// run's own document resolves as oauth.
//
// Mutation (run, restore): judge the auth on the ref's raw decode (a.Auth from
// config.AgentOf, project_cmd's old read) — this launch is refused before the
// claim, and acme-personal's job is never asked for.
func TestProjectJobNamedAccountJudgedWithTheLayersAuth(t *testing.T) {
	f := newProjectJobServingFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, projectJobOAuthLayer)
	projectJobCheckoutYAML(t, projectJobLayerAuthRepoYAML("acme-personal"))

	const run = "20261019-100002-cccc"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
		t.Fatalf("a launch whose account the repository names and whose auth the project layer sets: %v", err)
	}
	profile, provider, account := launchedProjectJob(t, f.cloudFixture)
	if profile != "standard" || provider != "github" || account != "acme-personal" {
		t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, acme-personal) — the layer's oauth resolves the name the repository makes", profile, provider, account)
	}
	if got := readSpec(t, f.cloudFixture, run).OAuthToken; got != "acme-personal" {
		t.Errorf("the stored task selects %q, want acme-personal", got)
	}
}

// And a checkout that does not hold the ref at all cannot show the document
// the run resolves: the launch is refused, naming the ref, rather than judged
// on another ref or on the working tree. Nothing is fetched to make the read —
// a fetch is a network write into a repository this command does not own.
//
// Mutation (run, restore): fall back to the working tree when no ref resolves
// (readCheckoutFugaroYAML) — the launch is taken onto the shared job, judged
// on a document whose ref the run may not even have.
func TestSharedJobLaunchRefusesACheckoutWithoutTheRef(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": projectJobRefRepoYAML("api-key")})
	t.Chdir(dir)

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261019-090002-cccc", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") || !strings.Contains(err.Error(), "main") {
		t.Fatalf("a launch from a checkout that holds no main ref: %v, want a refusal naming the ref", err)
	}
	if claimed {
		t.Error("the refusal came after the launch claim")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}

// projectJobAccountConfig is the fixture's local config addition: the two named
// Claude accounts this repository may select (config.AllowedClaudeAccounts, so
// that a launch naming one passes the allow-list gate and reaches the launch
// gate's own account rules), and the project-wide copy of the default account's
// token, which is what lets the standard profile list `default` at all
// (config.ValidateProfileClaudeAccounts resolves "default" against
// project_secrets, never against claude_accounts, where the name is reserved).
const projectJobAccountConfig = "claude_accounts:\n" +
	"  acme-personal:\n    allow_data_to: [\"acme/app\"]\n" +
	"  acme-work:\n    allow_data_to: [\"acme/app\"]\n" +
	"project_secrets:\n  claude-oauth-token:\n    allow_data_to: [\"acme/app\"]\n"

// projectJobAccountsPolicy is the published policy of a project whose standard
// profile serves both accounts: the profile lists them and writes the exact
// allow_repos the subset rule requires (projectpolicy.validProfileAccounts),
// and the accounts block carries one exact list per account — the document that
// says which repositories each token may be used by, derived at publish from
// the local config above (projectpolicy.DeriveAccounts).
const projectJobAccountsPolicy = `{"version":4,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"claude_accounts":["acme-personal","default"],"allow_repos":["github/acme/app"]}},` +
	`"accounts":{"acme-personal":{"allow_repos":["github/acme/app"]},` +
	`"default":{"allow_repos":["github/acme/app"]}}}`

// projectJobServingProfile is the profiles block of a project whose standard
// profile serves the two accounts, spelled as the fixture's own line is: the
// accounts it lists, and its own exact allow_repos, which is what a profile
// that lists accounts must write.
const projectJobServingProfile = "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [github/acme/app], claude_accounts: [acme-personal, default] } }\n"

// newProjectJobAccountFixture is newProjectJobFixture with the accounts
// configured and no profile serving them: the state of an installation that has
// defined Claude accounts without adopting them on a profile, whose jobs are
// unchanged (config.ProjectJobAccounts of a profile listing nothing is the
// no-account job alone) and whose runs hold no token at all.
func newProjectJobAccountFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobFixture(t)
	f.appendConfig(t, projectJobAccountConfig)
	return f
}

// newProjectJobServingFixture is the project that has adopted them: the
// standard profile serves acme-personal and the default account, the published
// policy carries the accounts block that bounds each token, and the
// installation has deployed the account's own job beside the profile's
// no-account one — one job per (profile, provider, account)
// (config.ProjectJobAccounts, internal/infra's ProjectJobs).
func newProjectJobServingFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobAccountFixture(t)
	rewriteProfileLine(t, f.cloudFixture, projectJobServingProfile)
	publishPolicy(t, f.launchBudgetFixture, projectJobAccountsPolicy)
	job := gcp.ProjectJobName("standard", "github", "acme-personal")
	f.run.AddProjectJob(job, "standard", "github", "acme-personal", "2", "4Gi")
	f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	return f
}

// rewriteProfileLine writes profile in place of the profiles block
// newProjectJobFixture appended: a second profiles: key would be a duplicate
// YAML key, and the profile a test serves accounts on is the fixture's own.
func rewriteProfileLine(t *testing.T, f *cloudFixture, profile string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const old = "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h } }\n"
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", old, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, profile, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// launchedProjectJob is the (profile, provider, account) of the project job the
// launch made, read back out of the fake: the triple is what the launcher had
// to resolve, so a test never compares against a name it computed itself.
func launchedProjectJob(t *testing.T, f *cloudFixture) (profile, provider, account string) {
	t.Helper()
	calls := f.run.RunRequests()
	if len(calls) == 0 {
		t.Fatal("the launch asked Cloud Run for no job at all")
	}
	last := calls[len(calls)-1]
	profile, provider, account, ok := f.run.ProjectJobOf(last.Job)
	if !ok {
		t.Fatalf("the launch ran the job %q, which is not one of the fixture's shared project jobs", last.Job)
	}
	return profile, provider, account
}

// TestProjectJobOAuthAllowedWhenAccountCovered is the gate's admit case (plan
// task 3, design §4.4 row 4): an OAuth run whose own credential is an account
// the profile serves, and whose repository is on that account's policy list,
// targets the account's own job — and the account it resolves is one the
// deployer built a job for and whose token the gate judged
// (config.ProjectJobAccounts, config.ProjectJobTokenAccount: the one rule all
// three sides compute the axis with).
//
// Mutation (run, restore): refuse auth == "oauth" outright, as task 3.4a's
// checkProjectJobOAuth did (the owner's ruling of 2026-10-17, which Task 3
// replaces) — every account launch is then refused for every profile, and the
// account jobs task 2 deploys can never be used by anyone.
func TestProjectJobOAuthAllowedWhenAccountCovered(t *testing.T) {
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal", config.DefaultClaudeAccount},
	}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal":             {AllowRepos: []string{"github/acme/app"}},
		config.DefaultClaudeAccount: {AllowRepos: []string{"github/acme/app"}},
	}}
	for _, tc := range []struct {
		why      string
		selected string
		want     string
	}{
		{"the default account rides the profile's no-account job", config.DefaultClaudeAccount, ""},
		{"no selection at all is the default account", "", ""},
		{"a named account the profile serves is a job of its own", "acme-personal", "acme-personal"},
	} {
		got, err := projectJobAccountFor(p, "standard", serving, "oauth", tc.selected, "github", "acme/app")
		if err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		if got != tc.want {
			t.Errorf("%s: projectJobAccountFor(%q) = %q, want %q", tc.why, tc.selected, got, tc.want)
		}
		if !slices.Contains(config.ProjectJobAccounts(serving.JobProfile), got) {
			t.Errorf("%s: the launcher targets the account %q, which the profile's job set %v does not hold — the job it names is one nothing deployed",
				tc.why, got, config.ProjectJobAccounts(serving.JobProfile))
		}
		if tokenAccount, serves := config.ProjectJobTokenAccount(serving.JobProfile, got); !serves || tokenAccount == "" {
			t.Errorf("%s: the job of the account %q holds no token the gate could have judged (config.ProjectJobTokenAccount = %q, %v): an OAuth run on it starts with no credential",
				tc.why, got, tokenAccount, serves)
		}
	}
	// The same run on a profile that serves neither is refused: an OAuth run
	// whose own credential is the default account has no credential on a job
	// that mounts no token (design §4.4 row 9). The end-to-end refusal is
	// TestOAuthLaunchOntoASharedProjectJobIsRefusedBeforeLaunch
	// (run_oauth_shared_test.go).
	plain := projectpolicy.Profile{JobProfile: config.JobProfile{Provider: "github"}}
	if _, err := projectJobAccountFor(p, "standard", plain, "oauth", "", "github", "acme/app"); err == nil || !strings.Contains(err.Error(), "claude-oauth-token") {
		t.Errorf("an OAuth run on a profile that serves no account: %v", err)
	}
}

// TestLauncherAccountDerivationSharedFunction: the launcher's account axis is
// the profile's own job set, config.ProjectJobAccounts — the function
// internal/infra built the deployed jobs with and internal/runner recomputes
// the names it matches CLOUD_RUN_JOB against — spelled the way a launch selects
// from it. A launch can therefore only ever target a job those two know: not a
// name nothing deployed, and not one the runner would refuse to place.
//
// Mutation (run, restore): derive the account from the profile's secrets
// (config.ClaudeAccountOfSecrets, the pre-task reading) — a profile serving
// accounts by its claude_accounts field then refuses every account it serves,
// and a launch naming one never reaches the job the deployer built.
func TestLauncherAccountDerivationSharedFunction(t *testing.T) {
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal", config.DefaultClaudeAccount},
	}}
	plain := projectpolicy.Profile{JobProfile: config.JobProfile{Provider: "github"}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal":             {AllowRepos: []string{"github/acme/app"}},
		config.DefaultClaudeAccount: {AllowRepos: []string{"github/acme/app"}},
	}}
	for _, tc := range []struct {
		why      string
		selected string
		want     string
	}{
		{"no selection is the profile's no-account job", "", ""},
		{"the default account is the profile's no-account job", config.DefaultClaudeAccount, ""},
		{"a named account the profile serves is a job of its own", "acme-personal", "acme-personal"},
	} {
		got, err := projectJobAccountFor(p, "standard", serving, "oauth", tc.selected, "github", "acme/app")
		if err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		if got != tc.want {
			t.Errorf("%s: projectJobAccountFor(%q) = %q, want %q", tc.why, tc.selected, got, tc.want)
		}
		if !slices.Contains(config.ProjectJobAccounts(serving.JobProfile), got) {
			t.Errorf("%s: the launcher targets the account %q, which the profile's job set %v does not hold — the job it names is one nothing deployed",
				tc.why, got, config.ProjectJobAccounts(serving.JobProfile))
		}
	}
	// A profile that serves nothing has no job holding a Claude token, so an
	// OAuth run selects nothing it may run as — not even the no-account job it
	// does have (checkProjectJobAccount).
	if _, err := projectJobAccountFor(p, "standard", plain, "oauth", "", "github", "acme/app"); err == nil || !strings.Contains(err.Error(), "standard") {
		t.Errorf("an OAuth run on a profile that serves no Claude account: %v", err)
	}
	// And an account the profile serves no job for is refused, naming the
	// account and the profile. The launch's own gate, end to end, is
	// TestProjectJobOAuthRefusedAccountNotListed.
	if _, err := projectJobAccountFor(p, "standard", serving, "oauth", "acme-work", "github", "acme/app"); err == nil || !strings.Contains(err.Error(), "acme-work") || !strings.Contains(err.Error(), "standard") {
		t.Errorf("a launch selecting acme-work on a profile serving acme-personal: %v", err)
	}
	if _, err := projectJobAccountFor(p, "standard", plain, "oauth", "acme-personal", "github", "acme/app"); err == nil || !strings.Contains(err.Error(), "acme-personal") {
		t.Errorf("a launch selecting an account on a profile serving none: %v", err)
	}
}

// TestProjectJobOAuthRefusedRepoNotAllowed is the gate's coverage refusal
// (design §4.4 rows 3 and 4): the job's container holds one account's token for
// every run of it, and the account's own policy list is what bounds which
// repositories may use that token. A repository it does not cover is refused
// where the job is chosen, before the claim and before the token.
//
// It is a values-level test by necessity: a *decoded* policy cannot reach this
// refusal, because projectpolicy's subset rule holds a profile listing accounts
// to a list every one of its accounts covers exactly (validProfileAccounts) —
// so a repository the profile admits is one the account covers. The refusal is
// the second door on the one rule that bounds the token, and this is the test
// that it is still a door: what it is handed is a policy value the decoder
// would have refused.
//
// Mutation (run, restore): return nil instead of refusing when the account's
// list does not admit the repository — the launch is admitted onto a job whose
// token this repository is not on the list of, which is exactly the state the
// accounts block exists to prevent.
func TestProjectJobOAuthRefusedRepoNotAllowed(t *testing.T) {
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal"},
	}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal": {AllowRepos: []string{"github/acme/other"}},
	}}
	_, err := projectJobAccountFor(p, "standard", serving, "oauth", "acme-personal", "github", "acme/app")
	if err == nil {
		t.Fatal("a launch onto the job of an account whose list does not cover this repository was admitted")
	}
	for _, want := range []string{
		"acme-personal",                      // the account whose token the job holds
		"claude-oauth-token-acme-personal",   // the token itself
		"accounts.acme-personal.allow_repos", // the rule that refused it
		"github/acme/other",                  // the list as published
		"github/acme/app",                    // the repository that is not on it
		"standard",                           // the profile, and so the job
		"no claim was taken",                 // and that nothing was launched
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The account's job with the token's own list on it is admitted: the same
	// run, on a list that covers the repository.
	p.Accounts["acme-personal"] = projectpolicy.Account{AllowRepos: []string{"github/acme/app"}}
	if _, err := projectJobAccountFor(p, "standard", serving, "oauth", "acme-personal", "github", "acme/app"); err != nil {
		t.Errorf("the same launch on an account that covers the repository: %v", err)
	}
}

// TestProjectJobNonOAuthRefusedWhereTheJobTokenIsNotAllowed is the coverage
// refusal's other half (design §4.4 rows 3 and 4, D5): a profile that lists
// `default` mounts that token into its no-account job, and every run of that
// job holds it — including a run whose own credential selects no account at
// all, which the axis rule above admits onto that job precisely because it is
// not an OAuth one. The job's token is what has a list, so the rule is asked of
// the *job's* token account and not of the run's selection: a non-OAuth launch
// is refused where the default account's list does not cover the repository it
// runs for, exactly as an OAuth one is. Without this, a check that asked the
// coverage question only of OAuth runs would leave an api-key or Vertex run
// holding a token its account's list does not admit this repository to.
//
// Values-level, for the reason its sibling above is: the subset rule
// (projectpolicy.validProfileAccounts) means no *decoded* policy can state a
// profile admitting a repository its default account does not cover, so this is
// the backstop's own door.
//
// Mutation (run, restore): ask the coverage rule only of an OAuth run
// (`if servesToken && auth == "oauth"` in projectJobAccountFor, or an early
// return for every other auth) — the launches below are admitted onto a job
// whose container hands them the default account's token, for a repository that
// token's list does not cover.
func TestProjectJobNonOAuthRefusedWhereTheJobTokenIsNotAllowed(t *testing.T) {
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal", config.DefaultClaudeAccount},
	}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal":             {AllowRepos: []string{"github/acme/app"}},
		config.DefaultClaudeAccount: {AllowRepos: []string{"github/acme/other"}},
	}}
	for _, auth := range []string{"api-key", "vertex", "fake", ""} {
		got, err := projectJobAccountFor(p, "standard", serving, auth, "", "github", "acme/app")
		if err == nil {
			t.Fatalf("a launch whose agent.auth is %q was admitted as the account %q onto the no-account job, which holds the default account's token for a repository its list does not cover", auth, got)
		}
		for _, want := range []string{
			config.DefaultClaudeAccount,                                // the account whose token the job holds
			config.ClaudeAccountSecret(config.DefaultClaudeAccount),    // the token itself
			"accounts." + config.DefaultClaudeAccount + ".allow_repos", // the rule that refused it
			"github/acme/other",                                        // the list as published
			"github/acme/app",                                          // the repository that is not on it
			"project_secrets." + config.ClaudeAccountSecret(config.DefaultClaudeAccount) + ".allow_data_to", // where the default account's list is derived from
			"no claim was taken", // and that nothing was launched
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal of an agent.auth %q launch does not name %q: %v", auth, want, err)
			}
		}
	}
	// The same launches on a default account that covers the repository are
	// admitted, onto the profile's no-account job: the refusal is the uncovered
	// repository's, not the run's.
	p.Accounts[config.DefaultClaudeAccount] = projectpolicy.Account{AllowRepos: []string{"github/acme/app"}}
	for _, auth := range []string{"api-key", "vertex", "fake", ""} {
		got, err := projectJobAccountFor(p, "standard", serving, auth, "", "github", "acme/app")
		if err != nil {
			t.Fatalf("a launch whose agent.auth is %q, on a default account that covers the repository: %v", auth, err)
		}
		if got != "" {
			t.Errorf("a launch whose agent.auth is %q targets the account %q, want the no-account job", auth, got)
		}
	}
}

// TestProjectJobOAuthSelectsAccountJob is the gate end to end: an OAuth run's
// own credential picks the job it runs on — the account's own job for a named
// account, the profile's no-account job for the default one — and that job is
// the one the installation deployed for the account
// (gcp.ProjectJobName(profile, provider, account)). Both launches pass every
// gate above, take their claim, mint their token and reach Cloud Run.
//
// Mutation (run, restore): target the profile's no-account job for every OAuth
// run (the account axis dropped) — the named account's launch runs on a job
// whose container holds the default account's token, so the account the
// repository's fugaro.yaml chose is not the credential the run holds, and the
// first assertion fails.
func TestProjectJobOAuthSelectsAccountJob(t *testing.T) {
	f := newProjectJobServingFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-160000-aaaa", "--token", "acme-personal", "Add a feature"); err != nil {
		t.Fatalf("an OAuth launch selecting acme-personal, which the profile serves: %v", err)
	}
	profile, provider, account := launchedProjectJob(t, f.cloudFixture)
	if profile != "standard" || provider != "github" || account != "acme-personal" {
		t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, acme-personal)", profile, provider, account)
	}

	// The default account — the run's own credential when nothing is selected —
	// rides the profile's no-account job, which is the job every profile has
	// (D5, D9).
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-160000-bbbb", "Add a feature"); err != nil {
		t.Fatalf("an OAuth launch whose own credential is the default account: %v", err)
	}
	profile, provider, account = launchedProjectJob(t, f.cloudFixture)
	if profile != "standard" || provider != "github" || account != "" {
		t.Errorf("the default account's launch ran the project job of (%s, %s, %q), want (standard, github, \"\")", profile, provider, account)
	}
}

// TestNonOAuthNeverTargetsAccountJob is the other half of the axis (design §4.4
// row 6): a run whose own credential is not a Claude OAuth account never
// targets an account job — the job it has always run on is the profile's
// no-account one, whatever the profile serves — so adopting Claude accounts
// changes nothing about an api-key or Vertex installation's launches.
//
// Mutation (run, restore): derive the account from the profile's accounts
// rather than from the run's own credential (the first account the profile
// lists, say) — every api-key launch then runs on an account's job, holding
// that account's token in a container whose run never uses it.
func TestNonOAuthNeverTargetsAccountJob(t *testing.T) {
	f := newProjectJobServingFixture(t)
	projectJobCheckout(t) // the repository's own fugaro.yaml: agent.auth: api-key

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-160000-cccc", "Add a feature"); err != nil {
		t.Fatalf("an api-key launch on a profile that serves Claude accounts: %v", err)
	}
	profile, provider, account := launchedProjectJob(t, f.cloudFixture)
	if profile != "standard" || provider != "github" || account != "" {
		t.Errorf("the api-key launch ran the project job of (%s, %s, %q), want (standard, github, \"\") — the profile's no-account job", profile, provider, account)
	}

	// And no selection can put a non-OAuth run on an account job: the gate
	// reads the run's own credential, not the name a task.json or a flag
	// carries. `--token` is refused before the gate for an api-key workflow
	// (claudeAccountAllowed), so that half is tested at the value level.
	serving := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "github",
		ClaudeAccounts: []string{"acme-personal", config.DefaultClaudeAccount},
	}}
	p := &projectpolicy.Policy{Version: projectpolicy.Version, Accounts: map[string]projectpolicy.Account{
		"acme-personal":             {AllowRepos: []string{"github/acme/app"}},
		config.DefaultClaudeAccount: {AllowRepos: []string{"github/acme/app"}},
	}}
	for _, auth := range []string{"api-key", "vertex", "fake", ""} {
		got, err := projectJobAccountFor(p, "standard", serving, auth, "acme-personal", "github", "acme/app")
		if err != nil {
			t.Fatalf("a run whose credential is %q: %v", auth, err)
		}
		if got != "" {
			t.Errorf("a run whose agent.auth is %q targets the account %q, want the no-account job", auth, got)
		}
	}
}

// TestProjectJobOAuthRefusedAccountNotListed is the gate end to end, on the
// refusal side (design §4.4 row 5): the account a run selects is one the
// repository may use (the allow-list gate passed) and one the local config
// defines, but the profile the workflow selected serves no job for it — so the
// launch stops, naming the account, the token and the profile, before the
// claim, before the budget token and before any Cloud Run call.
//
// Mutation (run, restore): drop the config.ProjectJobTokenAccount /
// ProjectJobAccounts half of projectJobAccountFor — the launch is admitted onto
// the profile's no-account job, whose container holds the default account's
// token, while the run's own task.json and the run record name acme-work.
func TestProjectJobOAuthRefusedAccountNotListed(t *testing.T) {
	f := newProjectJobServingFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	const run = "20261018-160000-dddd"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", "acme-work", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a launch selecting an account the profile serves no job for: %v", err)
	}
	for _, want := range []string{"acme-work", "claude-oauth-token-acme-work", "standard", "claude_accounts", "no claim was taken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
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

// projectJobReservedTokenPolicy is the node an owner can write by hand (the
// node is theirs), with the standard profile mounting the reserved
// claude-oauth-token. The account segment of the job that profile names is the
// default account on the launcher's and the runner's side and the empty account
// on the deployer's, and the converge refuses to deploy it at all
// (internal/infra's projectJobSecrets) — the state in which the launcher took
// the claim, minted the budget token and reached Cloud Run on a job that does
// not exist, and every run of the profile halted with cannotPlaceJob, which
// blames a stale module version instead of the profile.
const projectJobReservedTokenPolicy = `{"version":4,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"secrets":["claude-oauth-token"]}}}`

// TestLaunchRefusesAPolicyMountingAClaudeOAuthToken is the reserved token end
// to end, on the launcher's side: the node is written straight to the database
// (as an owner's own write, or an earlier CLI's publish, leaves it), and the
// launch of a repository the profile admits is refused where the node is read —
// projectpolicy.validProfiles, the one rule publish, init and both readers
// share — rather than on a job the converge refused to deploy. Nothing was
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

// TestLaunchRefusesARepositoryTheProfileDoesNotAdmit: the admission a shared
// project job adds to the project's allow-list (design §4.1, plan task 3.3
// step 4). The two lists are asked separately and the profile's is the
// narrower one here, so the repository passes the project's gate and only the
// profile's refusal stops it — which is the half the launcher did not have:
// the launch reached Cloud Run, took the claim and minted the budget token,
// and the repository was refused by the runner instead, one run at a time and
// with an operator no longer watching. A repository's own fugaro.yaml selects
// the profile, so this is also what keeps a repository from widening its own
// access by selecting a profile whose list is wider.
//
// The second half is the paired mutation-proof: the profile admits the other
// repository of the fixture, and its launch still takes the shared job.
//
// Mutation (run, restore): drop the profileAdmissionRefusal call in
// launchProjectJobFor — the refused repository's launch makes a jobs.run call
// on the shared job and arrives at the runner.
func TestLaunchRefusesARepositoryTheProfileDoesNotAdmit(t *testing.T) {
	f := newProjectJobFixture(t)
	// The project admits both of the fixture's repositories; the profile
	// admits one of them.
	publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/*"],`+
		`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",`+
		`"allow_repos":["github/acme/other"]}}}`)
	projectJobCheckout(t)

	const run = "20261017-110000-cccc"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	for _, want := range []string{
		"github/acme/app", "standard", "github/acme/other", "never widen", "before any execution exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a launch the profile does not admit", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Errorf("the refused launch minted a budget token: %q", tok)
	}
	// The same policy, the repository the profile does admit: the launch takes
	// the shared job, so what refused the one above is the profile's list and
	// not the profile's admission being applied to every launch.
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": projectJobRepoYAML})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	t.Chdir(dir)
	if _, _, err := execute(t, "run", "--repo", otherRepo, "--run-id", "20261017-110001-dddd", "Add a feature"); err != nil {
		t.Fatalf("launch of the repository the profile admits: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 1 || calls[0].Job != f.job {
		t.Fatalf("the admitted repository's launch made %d jobs.run calls (%+v), want one on the shared job %s", len(calls), calls, f.job)
	}
}

// TestLaunchKeepsABitbucketProfileOnThePerRepositoryJob is the stack review's
// other half of the same finding: the comparison above is on the repository's
// provider against the profile's, so a Bitbucket repository whose workflow
// selects a *Bitbucket* profile agreed with it and the launch went onto
// gcp.ProjectJobName(profile, "bitbucket", "") — a job the converge deploys
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
	publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/*","bitbucket/acme/*"],`+
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

// Item 2: a launch of a repository from outside its checkout cannot tell
// which workflow it is about — the working tree's fugaro.yaml is where a
// workflow's checkout: kind and its profile are read — so, for a project
// that has published profiles, it refuses rather than silently guessing the
// per-repository job, which is the wrong job for a clone workflow. The
// remedy is named: run it from the repository's own checkout.
//
// Mutation (run, restore): return nil, nil where this refuses (the
// per-repository path) — the launch goes through, on a job the run may not
// belong to.
func TestRunRepoWithoutItsCheckoutErrorsClearly(t *testing.T) {
	newProjectJobFixture(t)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "run", "--repo", "acme/app", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"acme/app", "checkout", "fugaro run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// Item 2's other half: from the repository's own checkout nothing changes —
// the launch still reads the workflow's checkout: and profile there, and
// targets the shared project job.
func TestRunRepoFromItsCheckoutUnchanged(t *testing.T) {
	f := newProjectJobFixture(t)
	projectJobCheckout(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the standard/github project job", profile, provider)
	}
}

// checkoutParse answers nil both for a working directory outside the
// repository's checkout and for the repository's own checkout when its
// fugaro.yaml cannot be read, so the refusal has to name the second cause:
// a message naming only the first is wrong where the file is what is
// missing, and sends the user to look for a checkout they are already in.
func TestRunRepoInItsCheckoutWithUnreadableFugaroYAML(t *testing.T) {
	newProjectJobFixture(t)
	isolateCache(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"README.md": "nothing to configure yet\n"})
	testutil.Git(t, dir, "add", "README.md")
	testutil.Git(t, dir, "commit", "-q", "-m", "no fugaro.yaml yet")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--repo", "acme/app", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"acme/app", "fugaro.yaml", "fugaro validate", "checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// With no profiles published the per-repository job is not a guess — it is
// the only job there is — so a launch from outside the checkout still takes
// it. Both shapes of "no profiles" are pinned: an installation that has
// published no policy at all, and one whose policy defines no profile.
func TestRunRepoWithoutItsCheckoutKeepsThePerRepositoryJob(t *testing.T) {
	perRepo := gcp.JobName(appSlug, "web")
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *launchBudgetFixture
	}{
		{"no policy published", func(t *testing.T) *launchBudgetFixture {
			return newLaunchBudget(t, "enforce")
		}},
		{"a policy that defines no profiles", func(t *testing.T) *launchBudgetFixture {
			f := newProjectJobFixture(t)
			publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/*"]}`)
			return f.launchBudgetFixture
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.build(t)
			isolateCache(t)
			testutil.IsolateGit(t)
			t.Chdir(t.TempDir())
			if _, _, err := execute(t, "run", "--repo", "acme/app", "Add a feature"); err != nil {
				t.Fatalf("launch: %v", err)
			}
			calls := f.run.RunRequests()
			if len(calls) != 1 || calls[0].Job != perRepo {
				t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s", len(calls), calls, perRepo)
			}
		})
	}
}
