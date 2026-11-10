package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
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

// projectJobCascadeRepoYAML is the same clone checkout under agent.auth:
// cascade, the one mode the credentials service serves (config.AuthCascade,
// infra.CredentialsServiceServes). It is what a repository launched on an
// installation whose switch is on carries, which is the shape a test about
// everything else the switch reads has to spell.
const projectJobCascadeRepoYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: cascade
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

// projectJobSentinelProfile is the profiles block of a project whose standard
// profile serves the account axis's one non-account value: the project's own
// Anthropic API key (config.AnthropicKeyAccount, D1417/D1422). A profile that
// lists it deploys one more job — the account segment of the job's name is the
// sentinel itself and nothing is mounted on it (config.ProjectJobTokenAccount
// answers no token for it, internal/infra's projectAccountMount mounts nothing)
// — and that job's review-only variant is the pairing a review served by the key
// runs in. The list is exact, as a profile that names accounts must write it
// (D3), and the sentinel's own policy list is that same list, which is where
// projectpolicy derives it from (anthropicKeySlugs).
const projectJobSentinelProfile = "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [github/acme/app], claude_accounts: [" + config.AnthropicKeyAccount + "] } }\n"

// projectJobSentinelPolicy is the node that project publishes: the profile
// above, and the accounts block entry the sentinel must have for any of this to
// be reachable — the credentials service intersects a repository's admission
// with accounts.<name>.allow_repos (credsvc's admittedAccounts), so a sentinel
// with no entry there is one no review call could ever be served by.
const projectJobSentinelPolicy = `{"version":4,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"allow_repos":["github/acme/app"],"claude_accounts":["` + config.AnthropicKeyAccount + `"]}},` +
	`"accounts":{"` + config.AnthropicKeyAccount + `":{"allow_repos":["github/acme/app"]}}}`

// newProjectJobSentinelFixture is the project that serves the key: the fixture's
// standard profile rewritten to list the sentinel, that policy published, and
// the job the installation would have deployed for it registered in the fake —
// one job per (profile, provider, account), the sentinel being the account
// segment of this one (gcp.ProjectJobName).
func newProjectJobSentinelFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	return newProjectJobSentinelFixtureMode(t, "off")
}

// newProjectJobSentinelFixtureMode is newProjectJobSentinelFixture on a budget
// installation whose local mode is mode, as newProjectJobFixtureMode is to
// newProjectJobFixture: the mix's own conditions are read in the mode that
// admits a mix at all (enforce), so the one test about a mixed run spells it.
func newProjectJobSentinelFixtureMode(t *testing.T, mode string) *projectJobFixture {
	t.Helper()
	f := newProjectJobFixtureMode(t, mode)
	rewriteProfileLine(t, f.cloudFixture, projectJobSentinelProfile)
	publishPolicy(t, f.launchBudgetFixture, projectJobSentinelPolicy)
	job := gcp.ProjectJobName("standard", "github", config.AnthropicKeyAccount)
	f.run.AddProjectJob(job, "standard", "github", config.AnthropicKeyAccount, "2", "4Gi")
	f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	return f
}

// TestProjectJobSentinelResolvesToTheJobAndVariantItDeploys is the account
// axis's placement half for the project's own Anthropic API key (AK5's
// Interfaces: run.go's claudeAccountFor and mixedRunAccount resolve the sentinel
// to the review variant that is deployed for it). The value the axis places on
// is the sentinel itself, and the three readers of that value agree, which is
// the whole of what makes a review served by the key launchable:
//
//   - the deployer builds a coding job whose account segment is the sentinel,
//     mounting nothing on it (internal/infra's ProjectJobs);
//   - the deployer builds that job's review-only variant, which is the job a
//     review served by the key runs in (ProjectReviewJobs) — and the launcher's
//     own entry derives exactly that name, so the launcher cannot launch a
//     variant nobody deployed;
//   - the runner resolves the coding job's name back to the sentinel
//     (runner.ProjectJobOfPolicy), which is what its pairing test compares
//     spec.oauth_token against.
//
// Mutation (run, restore): place the sentinel on the profile's no-account job
// (drop the `case auth == "api-key" && selected == config.AnthropicKeyAccount`
// arm) — the value-level placement below answers "" and this fails on it, and
// the review of such a repository then resolves the variant of the no-account
// job, whose account segment is not the sentinel: the runner refuses the pairing
// and the review halts inside Cloud Run, past the claim and the budget token.
func TestProjectJobSentinelResolvesToTheJobAndVariantItDeploys(t *testing.T) {
	f := newProjectJobSentinelFixture(t)
	addRepoAppIDToFixture(t, f.cloudFixture)
	lc, err := localcfg.Parse([]byte(readFileString(t, os.Getenv("FUGARO_CONFIG"))))
	if err != nil {
		t.Fatalf("the fixture's local config does not parse: %v", err)
	}
	coding, err := infra.ProjectJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the installation's coding jobs: %v", err)
	}
	variants, err := infra.ProjectReviewJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the installation's review variants: %v", err)
	}
	p, found, err := projectpolicy.Decode([]byte(projectJobSentinelPolicy))
	if err != nil || !found {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	pr := p.Profiles["standard"]

	account, err := projectJobAccountFor(p, "standard", pr, "api-key", config.AnthropicKeyAccount, "github", "acme/app")
	if err != nil {
		t.Fatalf("an api-key launch naming the project's own key, which the profile serves: %v", err)
	}
	if account != config.AnthropicKeyAccount {
		t.Fatalf("the launch's account axis = %q, want %q: the sentinel is a job's own account segment and not a name that resolves to the profile's no-account job", account, config.AnthropicKeyAccount)
	}
	job := gcp.ProjectJobName("standard", "github", account)
	c, ok := coding[job]
	if !ok {
		t.Fatalf("the launcher targets the job %s, which the installation does not deploy (%v)", job, slices.Sorted(maps.Keys(coding)))
	}
	if c.Account != config.AnthropicKeyAccount {
		t.Errorf("the deployed job %s carries the account %q, want %q", job, c.Account, config.AnthropicKeyAccount)
	}
	if tokenAccount, serves := config.ProjectJobTokenAccount(pr.JobProfile, account); serves || tokenAccount != "" {
		t.Errorf("the sentinel's job holds the token of %q (%v): the key's job holds no Claude token at all, and a job that did would be one an OAuth run could be placed on", tokenAccount, serves)
	}

	// The review entry: the variant of THAT job, and the one the installation
	// deploys. This is the review a project with no Claude account launches
	// (D1422), on the job the service admits its caller by.
	rv := projectJobReviewFor("standard", "github", account, projectJobTimeout+backend.TaskTimeoutSlack)
	if want := credsvc.ReviewJobName("standard", "github", infra.ReviewVariantAccount(config.AnthropicKeyAccount)); rv.Name != want {
		t.Fatalf("the review entry of the sentinel's job resolves %s, want %s", rv.Name, want)
	}
	v, ok := variants[rv.Name]
	if !ok {
		t.Fatalf("the launcher resolves the review job %s, which the installation does not deploy (%v)", rv.Name, slices.Sorted(maps.Keys(variants)))
	}
	if v.Account != config.AnthropicKeyAccount || len(v.SecretEnv) != 0 {
		t.Errorf("the review variant %s = account %q, mounts %v, want the sentinel's account and no mount", rv.Name, v.Account, v.SecretEnv)
	}

	// And the runner's half: the coding job's name resolves back to the sentinel
	// over the published policy, which is the value its pairing test compares the
	// task's own oauth_token against (internal/runner's pairingNamesTheProjectsKey).
	profile, provider, back, ok := runner.ProjectJobOfPolicy(p, job)
	if !ok || profile != "standard" || provider != "github" || back != config.AnthropicKeyAccount {
		t.Errorf("ProjectJobOfPolicy(%s) = (%s, %s, %q, %v), want (standard, github, %q, true): the runner pairs no review onto this job otherwise", job, profile, provider, back, ok, config.AnthropicKeyAccount)
	}
}

// TestProjectJobSentinelRefusedWhereTheProfileDoesNotServeIt is the same axis's
// refusal (design §4.4 row 5), which is the one that must not send the operator
// to a key they may not write: the sentinel is no claude_accounts entry
// (config.ValidateClaudeAccounts refuses to define it), so the sentence names
// the profile's own list and the project secret the key's reach is read from,
// and never a token called claude-oauth-token-<sentinel> — the name that entry
// would have produced and that nothing holds.
//
// Mutation (run, restore): drop the sentinel's own sentence in
// checkProjectJobAccount — the operator is told to define
// claude_accounts: {anthropic-api-key--project: ...}, which this configuration
// refuses, and the assertion on the token name fails.
func TestProjectJobSentinelRefusedWhereTheProfileDoesNotServeIt(t *testing.T) {
	f := newProjectJobFixture(t) // the standard profile serves no account at all
	isolateCache(t)
	projectJobCheckout(t) // the repository's own fugaro.yaml: agent.auth: api-key

	const run = "20261020-120000-aaaa"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.AnthropicKeyAccount, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a launch naming the project's own key on a profile that does not serve it: %v", err)
	}
	for _, want := range []string{
		config.AnthropicKeyAccount,                  // the value this run's credential is
		"not a Claude account",                      // and what it is instead
		"standard",                                  // the profile whose list decides
		"claude_accounts",                           // where that list is written
		"project_secrets." + config.AnthropicSecret, // and where the key's reach is written
		"no claim was taken",                        // and that nothing was launched
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if tok := config.ClaudeAccountSecret(config.AnthropicKeyAccount); strings.Contains(err.Error(), tok) {
		t.Errorf("the refusal names the token %s, which nothing holds: the sentinel is no claude_accounts entry and has no token to store", tok)
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

// TestProjectJobSentinelLaunchLandsOnItsOwnJob is the placement end to end: an
// api-key launch naming the project's own key — the spelling D1413's explicit
// request takes, --token — runs on the job whose account segment is the sentinel,
// which is the job the installation deployed for that profile
// (gcp.ProjectJobName, internal/infra's ProjectJobs). What the run carries is
// the sentinel as its own account (spec.OAuthToken), which is the value the
// runner's pairing compares the review variant's account segment against
// (internal/runner's pairingNamesTheProjectsKey).
//
// Mutation (run, restore): return "" from claudeAccountAllowed's sentinel arm
// (or delete it, which sends the name to the membership check) — the launch is
// refused for a claude_accounts entry that cannot exist, and every review this
// release exists to serve is unreachable.
func TestProjectJobSentinelLaunchLandsOnItsOwnJob(t *testing.T) {
	f := newProjectJobSentinelFixture(t)
	isolateCache(t)
	projectJobCheckout(t) // the repository's own fugaro.yaml: agent.auth: api-key

	const run = "20261020-120001-bbbb"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.AnthropicKeyAccount, "Add a feature"); err != nil {
		t.Fatalf("an api-key launch naming the project's own key, which the profile serves: %v", err)
	}
	profile, provider, account := launchedProjectJob(t, f.cloudFixture)
	if profile != "standard" || provider != "github" || account != config.AnthropicKeyAccount {
		t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, %q)", profile, provider, account, config.AnthropicKeyAccount)
	}
	if got := readSpec(t, f.cloudFixture, run).OAuthToken; got != config.AnthropicKeyAccount {
		t.Errorf("the run's own account in task.json = %q, want %q: the runner pairs a review by the task's own oauth_token and the job's account segment together", got, config.AnthropicKeyAccount)
	}
}

// TestProjectJobSentinelIsAllowedOnlyForAnAPIKeyWorkflow pins the auth half of
// claudeAccountAllowed's sentinel arm (`auth == "api-key" || auth == ""`), which
// is the condition the arm's own comment claims and which nothing else in the
// tree asserts: admitted for every value of config.AgentAuths, it would let a
// vertex or cascade repository name the project's own key, take the claim, mint
// the budget token and call Cloud Run for a run the account gate exists to
// refuse — the class path refuses those two auths (internal/runner's
// checkReviewClassModelPath), so the run would halt inside the job, past the
// claim that a refused launch never takes.
//
// The whole list is walked rather than the two refused values alone, so a value
// added to config.AgentAuths is judged here without anyone remembering to add
// it, and the admitted row is a launch and not only a nil error.
//
// Mutation (run, restore): drop the auth condition (`if name ==
// config.AnthropicKeyAccount`), which is what the missing pin allowed — the
// vertex row is admitted, with the claim taken, one execution, one jobs.run
// call and a budget token minted, and the cascade row loses the account gate's
// sentence to the later cascade gate's, which refuses it past this arm. Both
// rows go red.
func TestProjectJobSentinelIsAllowedOnlyForAnAPIKeyWorkflow(t *testing.T) {
	for i, auth := range config.AgentAuths {
		t.Run(auth, func(t *testing.T) {
			f := newProjectJobSentinelFixture(t)
			isolateCache(t)
			// The ref's own document carries the auth under test: what the launch
			// reads is the fugaro.yaml at the ref the run checks out, so the two
			// halves of the checkout are the same document here.
			projectJobCheckoutYAML(t, projectJobRefRepoYAML(auth))

			claimed := false
			launchHooks.beforeClaim = func() { claimed = true }
			t.Cleanup(func() { launchHooks.beforeClaim = nil })

			run := fmt.Sprintf("20261020-1300%02d-%04d", i, i)
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.AnthropicKeyAccount, "Add a feature")
			if auth == "api-key" {
				// The one auth the class serves the key under (D1417): the launch
				// runs on the sentinel's own job, which is the row that keeps this
				// test from passing by refusing everything.
				if err != nil {
					t.Fatalf("an api-key launch naming the project's own key: %v", err)
				}
				if !claimed {
					t.Error("the admitted launch took no claim")
				}
				if profile, provider, account := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" || account != config.AnthropicKeyAccount {
					t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, %q)", profile, provider, account, config.AnthropicKeyAccount)
				}
				return
			}
			if err == nil {
				t.Errorf("a %s launch naming the project's own key was admitted", auth)
			} else {
				if ExitCode(err) != ExitUserError {
					t.Errorf("a %s launch naming the project's own key: %v, want a refusal", auth, err)
				}
				want := "only applies to a workflow whose agent.auth is oauth"
				if auth == "oauth" {
					// The other half of the arm's comment: oauth is no way round it
					// either, because this value is no claude_accounts entry — the
					// membership sentence is the one an oauth launch gets.
					want = "claude_accounts"
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the %s refusal does not name %q: %v", auth, want, err)
				}
			}
			// Read for every row and not only the refused ones: an arm that
			// admitted this auth is a launch that took a claim, minted a budget
			// token and called Cloud Run, and these are the three facts that say
			// so.
			if claimed {
				t.Error("the refusal came after the launch claim")
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
		})
	}
}

// projectJobSentinelMixConfig is the sentinel's fixture with a mix declared:
// one Claude account named for each of the two stages the ruling admits, which
// is rung 2 (stageAccountOf) — the rung a launch resolves without reading a
// provider route. The fixture itself carries the budget mode a mix is declared
// under (enforce), so no key for it is written here.
var projectJobSentinelMixConfig = "claude_accounts:\n  acme-overflow:\n    allow_data_to: [\"acme/app\"]\n" +
	stagesBlock("review", "acme-overflow", "adversarial", "acme-overflow")

// TestProjectJobSentinelRefusedOnAMixedRun is the pair claudeAccountFor's mix
// resolution has to refuse: a launch naming the project's own Anthropic API key
// (--token, claudeAccountAllowed's sentinel arm) on a repository whose project
// declares a mix. The two are not two ways of holding the same thing — the mix
// is the one Claude account an api-key run may hold, and it is what the launch's
// own account has to be, because spec.OAuthToken is what picks the job variant
// and only the account's own job mounts its token (mixedRunAccount). The
// sentinel's job mounts no Claude token at all, while the runner resolves the
// mix for the review and adversarial stages out of the *policy* — never out of
// spec.OAuthToken (internal/runner's resolveMixedStages, agentenv's
// stageAgentEnv) — so the launch passes every gate, takes the claim, mints the
// budget token and starts Cloud Run on a job whose review stages then halt for a
// token the run does not hold. The launch is refused where the mix is already
// resolved instead, before the claim, as its neighbours are.
//
// Mutation (run, restore): drop the `mix != "" && name ==
// config.AnthropicKeyAccount` arm in claudeAccountFor — the launch is admitted,
// and the assertions below fail on the claim, on the executions, on the
// jobs.run call and on the budget token.
func TestProjectJobSentinelRefusedOnAMixedRun(t *testing.T) {
	f := newProjectJobSentinelFixtureMode(t, "enforce")
	isolateCache(t)
	f.appendConfig(t, projectJobSentinelMixConfig)
	projectJobCheckoutYAML(t, projectJobRefRepoYAML("api-key"))

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	const run = "20261020-140000-cccc"
	_, _, err := execute(t, "run", "--run-id", run, "--token", config.AnthropicKeyAccount, "Add a feature")
	if err == nil {
		t.Error("a launch naming the project's own key on a run that declares a mix was admitted")
	} else {
		if ExitCode(err) != ExitUserError {
			t.Errorf("a launch naming the project's own key on a run that declares a mix: %v, want a refusal", err)
		}
		for _, want := range []string{
			config.AnthropicKeyAccount, // the value this run's account is
			"acme-overflow",            // and the account the mix names for its stages
			"one Claude account",       // the rule the pair breaks
			"Nothing was launched",     // and that the refusal came before anything ran
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	}
	if claimed {
		t.Error("the refusal came after the launch claim")
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

// TestProjectJobSentinelRefusedWhenTheRunIsNotAnAPIKeyRun is the axis's own
// rule: the sentinel is the run's own account under agent.auth: api-key and
// under no other auth, and projectJobAccountFor is where that is judged at the
// launch — claudeAccountAllowed admits the name for api-key and for "", the
// pre-check callers that resolve with no document to read an auth from (a
// stored task repeated or retried), and says so in as many words.
//
// The two subtests are its two halves. The first walks the whole axis — the
// empty auth included, which is the pre-check's — and asserts the sentinel's
// own job for api-key and a refusal for every other value, so an auth added to
// config.AgentAuths is judged here without anyone remembering to add it. The
// second is the launch those two resolutions can disagree in: the project layer
// in force when the retry is resolved says api-key (the pre-check admits the
// sentinel), while the layer the task carries — the one the earlier run was
// launched with, and the layer the launch re-resolves the credential over
// (projectJobAgentAuth) — says something else. Vertex and oauth are both
// walked, because they are not the same row arriving: a vertex run has no
// account arm at all, while oauth has one this arm has to come before. Without
// the arm the launch takes the claim, mints the budget token and starts Cloud
// Run on the profile's no-account job, carrying the sentinel as its own
// account: a run placed on a job its own credential never named.
//
// Mutation (run, restore): drop the `selected == config.AnthropicKeyAccount`
// arm of projectJobAccountFor — the sentinel is placed on the no-account job
// with no error both in the table (the vertex and cascade rows) and end to end
// (the launch is admitted, and the assertions below fail on the claim, the
// executions, the jobs.run call and the budget token).
func TestProjectJobSentinelRefusedWhenTheRunIsNotAnAPIKeyRun(t *testing.T) {
	p, found, err := projectpolicy.Decode([]byte(projectJobSentinelPolicy))
	if err != nil || !found {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	t.Run("the axis, over every auth", func(t *testing.T) {
		pr := p.Profiles["standard"]
		for _, auth := range append([]string{""}, config.AgentAuths...) {
			got, err := projectJobAccountFor(p, "standard", pr, auth, config.AnthropicKeyAccount, "github", "acme/app")
			if auth == "api-key" {
				// The one auth D1417 pairs the key with, and the row that keeps
				// this test from passing by refusing everything: the sentinel's
				// own job, which is the job the profile deploys by listing it.
				if err != nil {
					t.Fatalf("an api-key run naming the project's own key: %v", err)
				}
				if got != config.AnthropicKeyAccount {
					t.Errorf("an api-key run naming the project's own key was placed with the account %q, want %q", got, config.AnthropicKeyAccount)
				}
				continue
			}
			if err == nil {
				t.Errorf("a run whose auth is %q named the project's own key and was placed on the job of the account %q", auth, got)
				continue
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("a run whose auth is %q naming the project's own key: %v, want a refusal", auth, err)
			}
			// The refusal names the value, the one auth it is a job's account
			// for, and that nothing ran — and the auth it read, which an empty
			// one reports as unread rather than interpolating nothing.
			want := []string{config.AnthropicKeyAccount, "api-key", "no account this run can be placed by", "Nothing was launched"}
			if auth == "" {
				want = append(want, "could not be read")
			} else {
				want = append(want, fmt.Sprintf("%q", auth))
			}
			for _, w := range want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal for the auth %q does not name %q: %v", auth, w, err)
				}
			}
		}
	})
	t.Run("the launch, where the two resolutions disagree", func(t *testing.T) {
		for _, auth := range []string{"vertex", "oauth"} {
			t.Run(auth, func(t *testing.T) {
				f := newProjectJobFixture(t)
				isolateCache(t)
				// The layer this command resolves now — the pre-check reads the
				// sentinel against it — while the repository document itself
				// names no auth of its own.
				publishedLayer(t, f.cloudFixture, testProjectLayer)
				projectJobCheckoutYAMLAt(t, projectJobAnchoredRepoYAML, projectJobAnchoredRepoYAML)

				// The layer the earlier run carried: the one the launch
				// re-resolves the credential over, and so the auth the axis is
				// asked with.
				layer := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent: { auth: " + auth + " }\n"
				ctx := context.Background()
				b, err := blob.OpenBucket(ctx, f.bucket)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				spec := &task.Spec{Version: 1, RunID: "20261021-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
					Task: "x", RequestedBy: "someone@example.com", OAuthToken: config.AnthropicKeyAccount,
					ProjectLayer: &task.ProjectLayer{YAML: layer, SHA256: config.LayerSum([]byte(layer))}}
				if err := runstore.Open(b, appSlug, spec.RunID).CreateTask(ctx, spec); err != nil {
					t.Fatal(err)
				}

				claimed := false
				launchHooks.beforeClaim = func() { claimed = true }
				t.Cleanup(func() { launchHooks.beforeClaim = nil })

				_, _, err = execute(t, "run", "--retry", spec.RunID)
				if err == nil {
					t.Errorf("a retry whose launch resolves a %s credential was admitted with the project's own key as its account", auth)
				} else {
					if ExitCode(err) != ExitUserError {
						t.Errorf("a retry whose launch resolves a %s credential: %v, want a refusal", auth, err)
					}
					for _, want := range []string{config.AnthropicKeyAccount, "no account this run can be placed by", fmt.Sprintf("%q", auth), "Nothing was launched"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the refusal does not name %q: %v", want, err)
						}
					}
				}
				if claimed {
					t.Error("the refusal came after the launch claim")
				}
				if n := len(f.run.Executions()); n != 0 {
					t.Errorf("%d executions, want none", n)
				}
				if calls := f.run.RunRequests(); len(calls) != 0 {
					t.Errorf("the refused launch made %d jobs.run calls (%+v), want none", len(calls), calls)
				}
				if tok := readToken(t, f.cloudFixture, spec.RunID); tok != "" {
					t.Errorf("the refused launch minted a budget token: %s", tok)
				}
			})
		}
	})
}

// TestProjectJobSentinelRefusedOnAPerRepositoryJob: the same name on a
// repository that runs on a job of its own. A per-repository job's name is
// derived from the account segment (gcp.WorkflowVariant), and no installation
// deploys one under this value — its job is the shared project job of a profile
// that lists it, and that job's review-only variant. Starting the derived name
// would be Cloud Run's 404 for a job this installation was never asked for, so
// the launch stops before the claim, as the account gate's refusals do.
//
// Mutation (run, restore): delete the `job == nil && spec.OAuthToken == ...`
// refusal in launchRun — the launch takes the claim, mints the budget token and
// reaches Cloud Run on a name nothing deployed, and the assertions below fail on
// the claim and on the token.
func TestProjectJobSentinelRefusedOnAPerRepositoryJob(t *testing.T) {
	f := newProjectJobSentinelFixture(t)
	isolateCache(t)
	// A baked workflow: it stays on the repository's own job however the project
	// is configured (internal/cli's launchProjectJobFor).
	projectJobCheckoutYAML(t, strings.Replace(projectJobRepoYAML, "    checkout: clone\n", "", 1))

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	const run = "20261020-120002-cccc"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.AnthropicKeyAccount, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a launch naming the project's own key on a baked workflow: %v", err)
	}
	for _, want := range []string{config.AnthropicKeyAccount, "job of its own", "clone", "no claim was taken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if claimed {
		t.Error("the refusal came after the launch claim")
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch made %d jobs.run calls (%+v), want none", len(calls), calls)
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Errorf("the refused launch minted a budget token: %s", tok)
	}
}

// projectJobProviderKeyProfile is the profiles block of a project whose
// standard profile serves the account axis's SECOND non-account value: the
// project's provider key (config.ProviderKeyAccount, docs/api-key-review's
// AK6a). A profile that lists it deploys one more job — the account segment of
// its name is the value itself and nothing is mounted on it — and that job's
// review-only variant is the pairing a review served by the provider key runs
// in. The list is exact, as a profile that names accounts must write it (D3).
const projectJobProviderKeyProfile = "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [github/acme/app], claude_accounts: [" + config.ProviderKeyAccount + "] } }\n"

// projectJobProviderKeyPolicy is the node that project publishes: the profile
// above, and the accounts block entry the provider key must have for any of
// this to be reachable (credsvc's admittedAccounts intersects a repository's
// admission with accounts.<name>.allow_repos). It carries no entry for the
// sentinel: the two values are two credentials, and a node that keyed one of
// them by the other's name would admit a repository to a key its job is not
// the one for.
const projectJobProviderKeyPolicy = `{"version":4,"allow_repos":["github/acme/*"],` +
	`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"allow_repos":["github/acme/app"],"claude_accounts":["` + config.ProviderKeyAccount + `"]}},` +
	`"accounts":{"` + config.ProviderKeyAccount + `":{"allow_repos":["github/acme/app"]}}}`

// newProjectJobProviderKeyFixture is newProjectJobSentinelFixture's shape for
// the other non-account value: the standard profile rewritten to list the
// provider key, that policy published, and the job the installation would have
// deployed for it registered in the fake.
func newProjectJobProviderKeyFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobFixture(t)
	rewriteProfileLine(t, f.cloudFixture, projectJobProviderKeyProfile)
	publishPolicy(t, f.launchBudgetFixture, projectJobProviderKeyPolicy)
	job := gcp.ProjectJobName("standard", "github", config.ProviderKeyAccount)
	f.run.AddProjectJob(job, "standard", "github", config.ProviderKeyAccount, "2", "4Gi")
	f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	return f
}

// TestProviderKeyGetsItsOwnValueOnTheAccountAxis is AK6a's red step, and the
// whole of what a second axis value buys (docs/cloud-senior-review §6.7's
// CSR17, the reading the reconciliation of 2026-11-09 kept): the profile's
// provider key is a value of the account axis of its OWN, so
//
//   - a policy that publishes it derives an accounts entry keyed by it
//     (projectpolicy.DeriveAccounts) and by nothing beside it — the axis value
//     is the admission unit, so one value for two credentials would leave a
//     repository admitted to a key its job is not the one for;
//   - the deployer builds the profile's job for it and that job's review-only
//     variant (internal/infra's ProjectJobs and ProjectReviewJobs), mounting
//     nothing on either (the credential is the credentials service's, AK6b);
//   - the launcher resolves that job — and the variant of it — when the run's
//     own credential is the value.
//
// Mutation (run, restore): let the provider key ride config.AnthropicKeyAccount
// (the loser's reading, §1) — the derive answers one entry where the profile
// named the other value, and the assertion that the sentinel is absent from the
// block goes red.
func TestProviderKeyGetsItsOwnValueOnTheAccountAxis(t *testing.T) {
	f := newProjectJobProviderKeyFixture(t)
	addRepoAppIDToFixture(t, f.cloudFixture)
	lc, err := localcfg.Parse([]byte(readFileString(t, os.Getenv("FUGARO_CONFIG"))))
	if err != nil {
		t.Fatalf("the fixture's local config does not parse: %v", err)
	}
	profiles := map[string]projectpolicy.Profile{}
	for name, pr := range lc.Profiles {
		profiles[name] = projectpolicy.Profile{JobProfile: pr}
	}
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets)
	if err != nil {
		t.Fatalf("a profile naming the provider key derived no block: %v", err)
	}
	entry, ok := accounts[config.ProviderKeyAccount]
	if !ok {
		t.Fatalf("the derived accounts block = %v, want an entry keyed by %s: the value is the admission unit the credentials service intersects a repository's child with", accounts, config.ProviderKeyAccount)
	}
	if want := []string{"github/acme/app"}; !slices.Equal(entry.AllowRepos, want) {
		t.Errorf("accounts.%s.allow_repos = %v, want %v (the naming profile's own list)", config.ProviderKeyAccount, entry.AllowRepos, want)
	}
	if _, sentinel := accounts[config.AnthropicKeyAccount]; sentinel {
		t.Errorf("the derived block carries %s as well as %s: one axis value for two credentials is one repository admitted to a key its job is not the one for", config.AnthropicKeyAccount, config.ProviderKeyAccount)
	}

	coding, err := infra.ProjectJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the installation's coding jobs: %v", err)
	}
	variants, err := infra.ProjectReviewJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the installation's review variants: %v", err)
	}
	job := gcp.ProjectJobName("standard", "github", config.ProviderKeyAccount)
	c, ok := coding[job]
	if !ok {
		t.Fatalf("the installation deploys no job %s (%v): the launcher would ask Cloud Run for a name nothing deployed", job, slices.Sorted(maps.Keys(coding)))
	}
	if c.Account != config.ProviderKeyAccount {
		t.Errorf("the deployed job %s carries the account %q, want %q", job, c.Account, config.ProviderKeyAccount)
	}
	if _, mounted := c.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; mounted {
		t.Errorf("the provider key's job mounts %s: the value names a credential of the credentials service's, so there is no token of its own to mount", config.ReservedSecrets[config.ClaudeOAuthToken])
	}
	variantName := credsvc.ReviewJobName("standard", "github", infra.ReviewVariantAccount(config.ProviderKeyAccount))
	v, ok := variants[variantName]
	if !ok {
		t.Fatalf("the installation deploys no review variant %s (%v): the class serves its calls from that job alone", variantName, slices.Sorted(maps.Keys(variants)))
	}
	if v.Account != config.ProviderKeyAccount || len(v.SecretEnv) != 0 {
		t.Errorf("the review variant %s = account %q, mounts %v, want the provider key's account and no mount", variantName, v.Account, v.SecretEnv)
	}

	// The launcher: the value is placed on the job the deployer built for it,
	// and the review entry is that job's variant — the one the installation
	// deployed a moment ago.
	p, found, err := projectpolicy.Decode([]byte(projectJobProviderKeyPolicy))
	if err != nil || !found {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	pr := p.Profiles["standard"]
	account, err := projectJobAccountFor(p, "standard", pr, "api-key", config.ProviderKeyAccount, "github", "acme/app")
	if err != nil {
		t.Fatalf("an api-key launch naming the provider key, which the profile serves: %v", err)
	}
	if account != config.ProviderKeyAccount {
		t.Fatalf("the launch's account axis = %q, want %q", account, config.ProviderKeyAccount)
	}
	rv := projectJobReviewFor("standard", "github", account, projectJobTimeout+backend.TaskTimeoutSlack)
	if rv.Name != variantName {
		t.Fatalf("the review entry of the provider key's job resolves %s, want %s", rv.Name, variantName)
	}

	// And the runner's half: the job's name resolves back to the value over the
	// published policy, which is the name its pairing test compares the task's
	// own oauth_token against.
	profile, provider, back, ok := runner.ProjectJobOfPolicy(p, job)
	if !ok || profile != "standard" || provider != "github" || back != config.ProviderKeyAccount {
		t.Errorf("ProjectJobOfPolicy(%s) = (%s, %s, %q, %v), want (standard, github, %q, true)", job, profile, provider, back, ok, config.ProviderKeyAccount)
	}
}

// TestProviderKeyIsNotAClaudeAccount is the same value's three doors, in AK5's
// own three directions (docs/api-key-review AK6a's Interfaces): the value is
// admissible at the account flag `--token` and by inheritance from a launch
// that used one, refused in a repository's own fugaro.yaml, and refused as a
// stage account. No door opens wider than the sentinel's: a stage's account is
// a token the job's environment already carries, and this value's job carries
// nothing.
//
// Mutation (run, restore): admit the value as a *stage* account — which mounts
// a token the job has none of, and hands a coding run the review's credential —
// and the stage direction goes red; drop claudeAccountAllowed's arm and the
// --token direction is refused for a claude_accounts entry that cannot exist.
func TestProviderKeyIsNotAClaudeAccount(t *testing.T) {
	t.Run("admissible at the account flag", func(t *testing.T) {
		f := newProjectJobProviderKeyFixture(t)
		isolateCache(t)
		projectJobCheckout(t) // the repository's own fugaro.yaml: agent.auth: api-key

		const run = "20261109-120000-aaaa"
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.ProviderKeyAccount, "Add a feature"); err != nil {
			t.Fatalf("an api-key launch naming the provider key, which the profile serves: %v", err)
		}
		profile, provider, account := launchedProjectJob(t, f.cloudFixture)
		if profile != "standard" || provider != "github" || account != config.ProviderKeyAccount {
			t.Errorf("the launch ran the project job of (%s, %s, %q), want (standard, github, %q)", profile, provider, account, config.ProviderKeyAccount)
		}
		if got := readSpec(t, f.cloudFixture, run).OAuthToken; got != config.ProviderKeyAccount {
			t.Errorf("the run's own account in task.json = %q, want %q: the runner pairs a review by the task's own oauth_token and the job's account segment together", got, config.ProviderKeyAccount)
		}
	})
	t.Run("refused for every other agent.auth", func(t *testing.T) {
		for i, auth := range config.AgentAuths {
			if auth == "api-key" {
				continue // the one auth it is admitted under, asserted above
			}
			f := newProjectJobProviderKeyFixture(t)
			isolateCache(t)
			projectJobCheckoutYAML(t, projectJobRefRepoYAML(auth))
			run := fmt.Sprintf("20261109-1210%02d-%04d", i, i)
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--token", config.ProviderKeyAccount, "Add a feature")
			if err == nil {
				t.Fatalf("a %s launch naming the provider key was admitted", auth)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("a %s launch naming the provider key: %v, want a refusal", auth, err)
			}
			if len(f.run.Executions()) != 0 || len(f.run.RunRequests()) != 0 {
				t.Errorf("the refused %s launch reached Cloud Run", auth)
			}
			if tok := readToken(t, f.cloudFixture, run); tok != "" {
				t.Errorf("the refused %s launch minted a budget token: %s", auth, tok)
			}
		}
	})
	t.Run("refused in a repository's own fugaro.yaml", func(t *testing.T) {
		const base = "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n" +
			"  oauth_token: " + config.ProviderKeyAccount + "\n" +
			"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
		_, ps := config.Parse([]byte(base))
		if len(ps) != 1 || ps[0].Path != "agent.oauth_token" || !strings.Contains(ps[0].Message, "must be a Claude account name") {
			t.Fatalf("a repository naming the provider key in its own file: %v, want one refusal at agent.oauth_token's shape rule", ps)
		}
	})
	t.Run("refused as a stage account", func(t *testing.T) {
		f := newProjectJobFixture(t)
		f.appendConfig(t, "stages:\n  review:\n    claude_account: "+config.ProviderKeyAccount+"\n")
		_, err := localcfg.Parse([]byte(readFileString(t, os.Getenv("FUGARO_CONFIG"))))
		if err == nil {
			t.Fatalf("a config naming the provider key as a stage's account was accepted: no job mounts it as a Claude account")
		}
		for _, banned := range []string{"claude_accounts", "allow_data_to"} {
			if strings.Contains(err.Error(), banned) {
				t.Errorf("the refusal names %q: %v — the account lookup's sentence prescribes an entry config.ValidateClaudeAccounts refuses, so it is a remedy nobody can carry out", banned, err)
			}
		}
		// The value itself is not echoed: it is a file's own text, and the
		// loader's sentences never print one (config.ValidClaudeAccountRef's own
		// rule). What the refusal must say is the move that works.
		if !strings.Contains(err.Error(), "drop the key") {
			t.Errorf("the refusal does not name the move that works (dropping the key): %v", err)
		}
	})
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
// workflow's checkout: kind and its profile are read — so, for a project that
// has published profiles, it refuses rather than silently guessing the
// per-repository job, which is the wrong job for a clone workflow. The remedy
// is named: run it from the repository's own checkout.
//
// A3 narrows the refusal rather than deleting it: where the installation has a
// credentials service the document is read at the ref instead (D901, D902) and
// this fixture — which configures none — is the installation that has not opted
// in, whose one line names the missing service and keeps the checkout as the way
// out (spec.md §7).
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
	for _, want := range []string{"acme/app", "checkout", "credentials service"} {
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
// fugaro.yaml cannot be read, and the two are not one case: the second has
// objects to read (D909), and a repository whose document is nowhere — not in
// the working tree and not at the ref either, which is this checkout — has no
// clone workflow and no profile for a file to select. Its launch therefore
// keeps the per-repository job, the answer the checkout path gives for a
// workflow the repository's own document does not define, and it is never
// sent back to the checkout it is standing in (spec.md §2, §7; D905 refuses a
// read that *failed*, not a repository that has no document to read).
func TestRunRepoInItsCheckoutWithNoFugaroYAMLKeepsThePerRepositoryJob(t *testing.T) {
	f := newProjectJobFixture(t)
	perRepo := gcp.JobName(appSlug, "web")
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
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-094000-ffff", "Add a feature"); err != nil {
		t.Fatalf("a launch of a repository whose fugaro.yaml is nowhere: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s", len(calls), calls, perRepo)
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

// TestNoCheckoutBitbucketKeepsItsPerRepositoryJob is the first question a
// launch with no checkout asks, asked before anything is read: whether a
// shared project job is possible for this repository at all. No shared job
// serves Bitbucket (infra.SharedJobProvider: a Bitbucket credential cannot be
// scoped to one project's repositories, the owner's ruling of 2026-10-15), so
// the per-repository job is not a guess about a document nobody read — it is
// the answer the checkout path gives for the same repository, from the same
// fact (D908). Before this the provider was never consulted on that path: the
// no-checkout refusal fired first, for a project that had published a profile,
// and every Bitbucket launch from outside its checkout — which is every launch
// the console makes, from its own private directory (F9) — was refused for a
// question with exactly one answer.
//
// Mutation (run, restore): read the policy before the provider, and let the
// profile question refuse a launch whose provider no shared job serves — the
// launch is refused and no jobs.run call is made.
func TestNoCheckoutBitbucketKeepsItsPerRepositoryJob(t *testing.T) {
	f := newProjectJobFixture(t)
	const bbRepo = "acme/bb"
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	bbSlug, err := task.Slug("bitbucket", bbRepo)
	if err != nil {
		t.Fatal(err)
	}
	perRepo := gcp.JobName(bbSlug, "web")
	f.run.AddJob(perRepo, "4", "8Gi")
	// The build record the fixture's own repository has: a launch on a
	// per-repository job mints the sealed budget token, which the job's image
	// has to know how to read (launch_seal_test.go's fixture writes the same
	// record for acme/app's web workflow).
	writeBuildRecord(t, f.cloudFixture, bbSlug, "web", releaseSeal)
	// The project publishes a profile (the fixture's own github one, which is
	// what makes the refusal fire) and admits the Bitbucket repository, so
	// nothing below is about an admission.
	publishPolicy(t, f.launchBudgetFixture, strings.Replace(projectJobPolicy, `["github/acme/*"]`, `["github/acme/*","bitbucket/acme/*"]`, 1))

	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	if _, _, err := execute(t, "run", "--repo", bbRepo, "--run-id", "20261110-090000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s, which is the same job the checkout path gives this repository", len(calls), calls, perRepo)
	}
}

// TestCheckoutWithoutTheFileReadsTheRefsObjects is D909's own case: the
// operator is standing in the repository's checkout and the working tree has
// no fugaro.yaml, while the ref the run checks out carries one. The launcher
// reads the ref's document out of the checkout's own object database — the
// reader the credential axis already uses two gates later — and decides the
// job from it, exactly as if the file were in the tree. The case that raised
// the decision is a checkout on a branch whose tree predates the file, which
// is why deleting it here stands in for it.
//
// Mutation (run, restore): refuse where the ref's objects answer — the launch
// is refused over a document the checkout holds.
func TestCheckoutWithoutTheFileReadsTheRefsObjects(t *testing.T) {
	f := newProjectJobFixture(t)
	dir := projectJobCheckout(t)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-091000-bbbb", "Add a feature"); err != nil {
		t.Fatalf("a launch from a checkout whose working tree has no fugaro.yaml: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the standard/github project job the ref's fugaro.yaml selects", profile, provider)
	}
}

// TestCheckoutWithoutTheFileStillJudgesTheCloneGate is the case the launch
// document exists for: a checkout whose working tree has no fugaro.yaml, whose
// ref does carry one, on an installation whose base image predates cloneSince
// (0.7.5). The ref's document runs a checkout: clone workflow, so this launch
// is refused by the clone gate — exactly as the same launch is with the file
// in the tree. Before the launch resolved its document once for every reader
// of it, the gates read only the working tree, judged the launch a non-clone
// workflow and passed it, and the decision below read the ref's document and
// put the run on the shared job: every clone gate (cloneSince, seal, recipe,
// rigor, governance, compression) skipped, with a claim and a budget token
// taken on the way.
//
// Mutation (run, restore): drop withLaunchDocument from finishLaunch — the
// launch succeeds on the shared job and this test fails on the refusal, with a
// jobs.run call where the third assertion wants none.
func TestCheckoutWithoutTheFileStillJudgesTheCloneGate(t *testing.T) {
	f := newProjectJobFixture(t)
	rewriteConfig(t, "fugaro-base:"+sealSince, "fugaro-base:0.7.0")
	dir := projectJobCheckout(t)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-095000-aaaa", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "how to provision a blobless clone (FUGARO_CHECKOUT)") || !strings.Contains(err.Error(), "0.7.0") {
		t.Errorf("the refusal is not the clone gate's: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// projectJobRecipeRepoYAML is projectJobRepoYAML with agent.recipe named: the
// document that decides the run's recipe, which the launch resolves before the
// task is written.
func projectJobRecipeRepoYAML(name string) string {
	return strings.Replace(projectJobRepoYAML, "  auth: api-key\n", "  auth: api-key\n  recipe: "+name+"\n", 1)
}

// TestCheckoutWithoutTheFileStillResolvesTheRefsRecipe is the same launch one
// reader further in than the clone gate: the attempt plan. The plan runs before
// finishLaunch and resolved the recipe from the working tree alone
// (resolveAttemptPlan, chooseRecipe), so a checkout whose tree has no
// fugaro.yaml and whose ref names agent.recipe wrote no recipe into its task —
// spec.Recipe nil — and the runner, which reads agent.recipe at the ref after
// cloning it, then used a recipe the launch never resolved: no note, no
// mode: artifact carry, no review-mode refusal and no image gate, where the
// same launch with the file in the tree has all four.
//
// Mutation (run, restore): drop the withLaunchDocument call from
// resolveAttemptPlan — this task's recipe is nil again.
func TestCheckoutWithoutTheFileStillResolvesTheRefsRecipe(t *testing.T) {
	f := newProjectJobFixture(t)
	dir := projectJobCheckoutYAML(t, projectJobRecipeRepoYAML("solo"))
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	const run = "20261110-100000-aaaa"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
		t.Fatalf("a launch whose ref names a recipe and whose tree has no fugaro.yaml: %v", err)
	}
	if r := readSpecOf(t, f.cloudFixture, appSlug, run).Recipe; r == nil || r.Name != "solo" {
		t.Fatalf("the task's recipe = %+v, want the solo the ref's fugaro.yaml names", r)
	}
}

// TestRetryWithNoProviderEntryRefusesInsteadOfGuessingTheJob is the provider
// the job decision must not guess at. The local config's entry for a repository
// may name no provider at all (localcfg: the key is optional) and repoSlug then
// falls back to the checkout's fugaro.yaml — a fallback a --retry never makes,
// because its slug comes from the stored run (retrySpec, newFollowUpSpec): the
// decision is reached with no checkout and no provider. Reading "" as "no
// shared job serves this repository" (SharedJobProvider("") is false) put a
// GitHub clone workflow of a project that published a profile on the
// per-repository job — the claim taken, the budget token minted, and nothing
// downstream to disagree with it, since a per-repository job carries no
// profile. An unknown provider is not a Bitbucket one: only a provider this
// launcher knows rules the shared job out (D908), and what is left for the rest
// is the refusal every other launch of a repository with no checkout gets
// (D912).
//
// Mutation (run, restore): drop the `provider != ""` guard — this retry
// launches on the per-repository job and the assertions below fail on it.
func TestRetryWithNoProviderEntryRefusesInsteadOfGuessingTheJob(t *testing.T) {
	f := newProjectJobFixture(t)
	rewriteConfig(t, "acme/app: { provider: github, base_branch: main, workflows: [web] }",
		"acme/app: { base_branch: main, workflows: [web] }")
	const run = "20261110-120000-aaaa"
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: run, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature", RequestedBy: "someone@example.com"}
	if err := runstore.Open(b, appSlug, run).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, err = execute(t, "run", "--retry", run)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "acme/app") || !strings.Contains(err.Error(), "checkout") {
		t.Errorf("the refusal does not name the repository and its checkout: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestCheckoutWithoutTheFileStillResolvesTheRefsWorkflow is the workflow's own
// half of the same finding, and the one a reader met first: resolveWorkflow ran
// with the checkout closure bound before withLaunchDocument, so a checkout whose
// tree has no fugaro.yaml and whose ref's document names exactly one workflow
// was refused with "cannot tell which workflow of acme/app to run; pass
// --workflow" — where the same launch with the file in the tree resolves that
// same document (spec.md §7: decided as if the file were in the tree). The
// local config names no workflow for this repository, which is what leaves the
// file as the only place the answer can come from.
//
// Both launch shapes that resolve a workflow through it are pinned: a first
// run (resolveAttemptPlan) and a review (reviewSpec, the same read with the
// same ordering).
//
// Mutation (run, restore): move the withLaunchDocument call in either spec
// builder below its resolveWorkflow — that shape's launch is refused.
func TestCheckoutWithoutTheFileStillResolvesTheRefsWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		run  string
	}{
		{"a first run", []string{"run", "--repo", "acme/app", "--run-id", "20261110-110000-aaaa", "Add a feature"}, "20261110-110000-aaaa"},
		{"a review", []string{"run", "--repo", "acme/app", "--run-id", "20261110-110001-bbbb", "--recipe", "review-only", "--pr", "7"}, "20261110-110001-bbbb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectJobFixture(t)
			rewriteConfig(t, "acme/app: { provider: github, base_branch: main, workflows: [web] }",
				"acme/app: { provider: github, base_branch: main }")
			dir := projectJobCheckout(t)
			if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
				t.Fatal(err)
			}
			if _, _, err := execute(t, tc.args...); err != nil {
				t.Fatalf("a launch whose ref names one workflow and whose tree has no fugaro.yaml: %v", err)
			}
			if got := readSpecOf(t, f.cloudFixture, appSlug, tc.run).Workflow; got != "web" {
				t.Fatalf("the task's workflow = %q, want the web the ref's fugaro.yaml names", got)
			}
		})
	}
}

// TestNoCheckoutGitHubRefusalNamesTheCheckoutAndTheRef is D912's first
// message as A3 leaves it: a launch with no checkout of the repository
// anywhere, on a project that has published a profile and an installation that
// has no credentials service, is refused — the provider is one a shared job
// serves, and the read that would narrow it cannot be made (D902) — and the
// refusal names the repository, the ref and the way out (spec.md §7). The ref
// is named because it is half of what the launch had to read: the document is
// the one at the ref the run checks out, and only from the repository's
// checkout can this operator see it.
//
// Mutation (run, restore): drop the ref from the message — this test fails on
// the ref alone, which is the field an operator with two checkouts of the
// repository needs to know which document placed (or failed to place) a run.
func TestNoCheckoutGitHubRefusalNamesTheCheckoutAndTheRef(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "run", "--repo", "acme/app", "--ref", "release-2", "--run-id", "20261110-092000-cccc", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"acme/app", "release-2", "checkout", "credentials service"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestRefusalMessagesAreNotInterchanged is D912's rule read from the other
// side: the two causes have two remedies, so neither message may carry the
// other's. The operator with no checkout of the repository is the one the
// checkout is the way out for — the document is read at the forge where the
// installation has a credentials service, and where it has none that read does
// not exist and the checkout is the remedy; the operator standing *in* the
// checkout whose ref could not be read out of its objects is not — telling them
// to run it from the checkout they are in is the sentence this design exists to
// delete (F2, D909), and their read never touches the service at all.
//
// Mutation (run, restore): make one message serve both causes — one of the
// two assertions below fails, whichever text is kept.
func TestRefusalMessagesAreNotInterchanged(t *testing.T) {
	newProjectJobFixture(t)

	// Neither cause has a checkout to name: the working directory holds
	// nothing of the repository.
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, noCheckout := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-093000-dddd", "Add a feature")

	// A checkout of the repository is here, its working tree has no file, and
	// the ref this launch names is not one the checkout holds: the objects
	// cannot answer either.
	dir := projectJobCheckout(t)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, unreadable := execute(t, "run", "--repo", "acme/app", "--ref", "topic", "--run-id", "20261110-093001-eeee", "Add a feature")

	if ExitCode(noCheckout) != ExitUserError || ExitCode(unreadable) != ExitUserError {
		t.Fatalf("no-checkout: %v; unreadable ref: %v; want two user errors", noCheckout, unreadable)
	}
	if !strings.Contains(noCheckout.Error(), "Run it from acme/app's checkout") {
		t.Errorf("the no-checkout refusal does not name the checkout: %v", noCheckout)
	}
	if strings.Contains(noCheckout.Error(), "out of this checkout") {
		t.Errorf("the no-checkout refusal talks about a checkout the operator is not in: %v", noCheckout)
	}
	if !strings.Contains(unreadable.Error(), "out of this checkout") {
		t.Errorf("the unreadable-ref refusal does not name the checkout the read was made from: %v", unreadable)
	}
	if strings.Contains(unreadable.Error(), "Run it from acme/app's checkout") {
		t.Errorf("the unreadable-ref refusal sends the operator to the checkout they are standing in: %v", unreadable)
	}
	if strings.Contains(unreadable.Error(), "credentials service") {
		t.Errorf("the unreadable-ref refusal talks about a service this read never reached: %v", unreadable)
	}
	for _, want := range []string{"acme/app", "fugaro.yaml"} {
		if !strings.Contains(unreadable.Error(), want) {
			t.Errorf("the unreadable-ref refusal does not say %q: %v", want, unreadable)
		}
	}
	if !strings.Contains(unreadable.Error(), "topic") {
		t.Errorf("the unreadable-ref refusal does not name the ref it could not read: %v", unreadable)
	}
}

// TestCheckoutWithAnUnreadableRefDocumentRefuses is D905's other half, and the
// one a read's exit status cannot answer: the ref's tree names a fugaro.yaml
// and this checkout cannot produce the object of it — one the clone never got
// (a promisor clone's lazy fetch did not run), one pruned, one corrupt. That
// is a read that failed, so the launch is refused; it is not a repository
// without a document, which is the only state that keeps the per-repository
// job. Reading the two as one places the run on a job the project's policy did
// not choose, past the claim and the budget token, and nothing downstream
// catches it: a per-repository job carries no profile to disagree with.
//
// Mutation (run, restore): answer errNoConfigAtRef for any failed cat-file
// (the shape before the tree was asked) — this launch succeeds on the
// per-repository job and the assertion below fails on it.
func TestCheckoutWithAnUnreadableRefDocumentRefuses(t *testing.T) {
	f := newProjectJobFixture(t)
	dir := projectJobCheckout(t)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	removeRefDocumentObject(t, dir, "refs/remotes/origin/main")

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-094000-ffff", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "out of this checkout") {
		t.Errorf("the refusal does not say the read was made from this checkout: %v", err)
	}
	if !strings.Contains(err.Error(), "acme/app") || !strings.Contains(err.Error(), "main") {
		t.Errorf("the refusal does not name the repository and the ref it could not read: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestFugaroYAMLAtRefTellsAnAbsentFileFromAFailedRead is the reader's own
// contract, which the launch above and the absent-file case both rest on: the
// sentinel comes back for a tree that has no fugaro.yaml and for nothing else.
func TestFugaroYAMLAtRefTellsAnAbsentFileFromAFailedRead(t *testing.T) {
	ctx := context.Background()

	// A repository whose commit carries no document at all: the definite
	// answer, and the one state a launch keeps the per-repository job for.
	absent := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, absent, "init", "-q")
	testutil.Git(t, absent, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, absent, map[string]string{"README.md": "no document here\n"})
	testutil.Git(t, absent, "add", "README.md")
	testutil.Git(t, absent, "commit", "-q", "-m", "the repository without a fugaro.yaml")
	testutil.Git(t, absent, "update-ref", "refs/remotes/origin/main", "HEAD")
	if _, err := checkoutFugaroYAMLAtRef(ctx, absent, "main"); !errors.Is(err, errNoConfigAtRef) {
		t.Errorf("a ref whose tree has no fugaro.yaml: err = %v, want errNoConfigAtRef", err)
	}

	// The same ref with the file in its tree and its object unreadable: a
	// failure, never the sentinel.
	dir := projectJobCheckout(t)
	removeRefDocumentObject(t, dir, "refs/remotes/origin/main")
	_, err := checkoutFugaroYAMLAtRef(ctx, dir, "main")
	if err == nil {
		t.Fatal("a fugaro.yaml whose object is gone was read without an error")
	}
	if errors.Is(err, errNoConfigAtRef) {
		t.Errorf("a failed read is answered as a repository without a document: %v", err)
	}
	if !strings.Contains(err.Error(), "could not read") {
		t.Errorf("the failure does not say the read failed: %v", err)
	}
}

// removeRefDocumentObject deletes the object git would answer
// rev:fugaro.yaml with, which is how a checkout that holds the ref and cannot
// produce its document is made without a network. A repository this test
// created keeps its objects loose, so the file is the blob itself.
func removeRefDocumentObject(t *testing.T, dir, rev string) {
	t.Helper()
	ctx := context.Background()
	sha, err := gitRead(ctx, dir, "rev-parse", "--verify", "--end-of-options", rev+":fugaro.yaml")
	if err != nil {
		t.Fatalf("resolve %s:fugaro.yaml: %v", rev, err)
	}
	gitDir, err := gitRead(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(gitDir, "objects", sha[:2], sha[2:])); err != nil {
		t.Fatalf("remove the object of %s:fugaro.yaml: %v", rev, err)
	}
	if _, err := gitRead(ctx, dir, "cat-file", "-e", sha); err == nil {
		t.Fatalf("the object of %s:fugaro.yaml is still readable after its removal", rev)
	}
}

// credentialsServiceFixtureURL is the address of the credentials service an
// opted-in fixture's local config records: the one value a variant's
// environment carries from its deployment, and never a secret.
const credentialsServiceFixtureURL = "https://fugaro-credentials-abc-uc.a.run.app"

// addRepoAppIDToFixture records the App's ID on the fixture's repository: a
// shared project job is built with it — the runner refuses to start without it
// — and the fixture's own repos block names none (it exists for launches, and
// the App's ID is what deploys a job).
func addRepoAppIDToFixture(t *testing.T, f *cloudFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const old = "  acme/app: { provider: github, base_branch: main, workflows: [web] }"
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", old, data)
	}
	withApp := "  acme/app: { provider: github, base_branch: main, workflows: [web], github_app_id: \"123456789\" }"
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, withApp, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReviewJobVariantIsHandedNoToken: the review job a launch resolves is the
// deployed variant (the launcher's entry agrees with the deployer's job set),
// and the whole of what reaches its execution is the credentials service's
// address — no App key, no Claude token, no signing key, no installation token.
// The read-only installation token is not handed in at any point: the run
// fetches it by name from the service, which is what the address is for
// (D893 (c), the fetch path of internal/gitprov/github's ServiceTokens, which
// the variable's presence opens).
//
// The launch itself adds nothing: a job named at launch is not told the
// address (credentialsServiceLaunchEnv's rule for a shared job), so the value
// has to come from the variant's own deployment, and the assertion below is
// that it does.
//
// Mutation (run, restore): mount github-app-key or a Claude token on the
// variant (internal/infra's reviewVariant), or hand the address in as a
// per-launch environment entry with a minted installation token beside it —
// this test fails on the variable it adds, and internal/infra's own tests fail
// on the mount.
func TestReviewJobVariantIsHandedNoToken(t *testing.T) {
	f := newProjectJobServingFixture(t)
	addRepoAppIDToFixture(t, f.cloudFixture)
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	lc, err := localcfg.Parse([]byte(readFileString(t, os.Getenv("FUGARO_CONFIG"))))
	if err != nil {
		t.Fatalf("the fixture's local config does not parse: %v", err)
	}
	deployed, err := infra.ProjectReviewJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the installation's review variants: %v", err)
	}
	if len(deployed) == 0 {
		t.Fatal("the fixture's installation deploys no review variant, so this pins nothing")
	}
	// The address is what a switched-on installation hands a launch: a
	// repository's own job, of a repository the service serves, is told it,
	// which is why the nil below is this rule and not a config with no
	// service in it. The other two halves of the switch are the record above
	// and the cascade repository a checked-out launch reads its auth from.
	if env := credentialsServiceLaunchEnv(lc, "", "acme/app", config.AuthCascade, false); len(env) != 1 || env[0] != providers.EnvCredentialsServiceURL+"="+credentialsServiceFixtureURL {
		t.Fatalf("a repository launch on this config is handed %v, want the service's address alone: the fixture is not an opted-in installation", env)
	}
	for _, account := range []string{"acme-personal", ""} {
		job := projectJobReviewFor("standard", "github", account, projectJobTimeout+backend.TaskTimeoutSlack)
		if want := credsvc.ReviewJobName("standard", "github", infra.ReviewVariantAccount(account)); job.Name != want {
			t.Errorf("the review entry of the account %q resolves %s, want %s (credsvc.ReviewJobName over the policy's spelling of the account)", account, job.Name, want)
			continue
		}
		if job.Profile != "standard" || job.Timeout != projectJobTimeout+backend.TaskTimeoutSlack {
			t.Errorf("the review job %s = %+v, want the profile's own name and task timeout", job.Name, job)
		}
		v, ok := deployed[job.Name]
		if !ok {
			t.Errorf("the launcher resolves the review job %s, which the installation does not deploy (%v): a launcher cannot launch a job that does not exist", job.Name, slices.Sorted(maps.Keys(deployed)))
			continue
		}
		if env := credentialsServiceLaunchEnv(lc, job.Name, "acme/app", config.AuthCascade, false); env != nil {
			t.Errorf("a launch on the review job %s hands the execution %v: the variant carries the service's address from its deployment, and a per-launch entry is a value an execution object records", job.Name, env)
		}
		if got := v.Env[providers.EnvCredentialsServiceURL]; got != credentialsServiceFixtureURL {
			t.Errorf("the review job %s carries %s = %q, want %q: the address is the fetch path, and without it the run holds no installation token at all", job.Name, providers.EnvCredentialsServiceURL, got, credentialsServiceFixtureURL)
		}
		for secret, name := range config.ReservedSecrets {
			if value, ok := v.Env[name]; ok {
				t.Errorf("the review job %s carries %s = %q: that is the variable the %s secret is mounted as, and the variant mounts nothing", job.Name, name, oneLineCLI(value), secret)
			}
			if mounted, ok := v.SecretEnv[name]; ok {
				t.Errorf("the review job %s mounts the %s secret as %s: the variant mounts no secret at all", job.Name, mounted, name)
			}
		}
		if len(v.SecretEnv) != 0 {
			t.Errorf("the review job %s mounts %v: the variant mounts no secret at all", job.Name, v.SecretEnv)
		}
	}
}

// identityTokenFixture and readTokenFixture are the two credentials a source-3
// read presents and receives in these tests: the operator's own Google ID
// token (personIDToken, which no test mints for real) and the installation
// token the service answers with. Neither is a secret and neither is shaped
// like one: the fixture values exist to be compared, never to be used.
const (
	identityTokenFixture = "identity-token-of-the-operator-fixture"
	readTokenFixture     = "read-token-the-service-minted-fixture"
)

// serviceReadFixture is the installation source 3 needs (spec.md §5, D902): a
// published credentials service and the repository's own App ID, and one TLS
// server that answers as both the service's read mint and the GitHub API root
// the read asks. It is one server because a launch reaches both, and TLS
// because localcfg's validCredentialsServiceURL admits nothing but the https
// URL Cloud Run serves the service on — so the client that trusts this
// server's certificate is the one the read is given.
//
// Every answer is spelled here rather than replayed from a fixture file: the
// read is two calls of two protocols (the service's own JSON and the contents
// API), and what these tests ask about is which of them the launch made.
type serviceReadFixture struct {
	srv *httptest.Server

	mu         sync.Mutex
	mints      int    // read-mint calls the service answered
	reads      int    // contents calls the forge answered
	confirm    int    // the repository and the ref an absent file was confirmed against
	identity   string // the Authorization header the mint was presented with
	serviceURL string // the URL personIDToken was asked for
	appID      string // the app_id the mint's body named
	document   string // the fugaro.yaml the ref carries; "" is the absent-file 404
	notFound   string // the message of the contents route's 404 ("" is GitHub's own "Not Found")
	status     int    // the contents answer's status (0: 200)
	message    string // the body of that non-200 answer
}

func newServiceReadFixture(t *testing.T) *serviceReadFixture {
	t.Helper()
	s := &serviceReadFixture{document: projectJobRepoYAML}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// use points the launcher's read at s: the local config records the service's
// address and the repository's App ID, the operator's own identity is the
// fixture's, and the API root the read asks is the fixture's own server.
func (s *serviceReadFixture) use(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.appendConfig(t, "credentials_service: { url: "+s.srv.URL+" }\n")
	addRepoAppIDToFixture(t, f)
	oldIdentity, oldURL, oldClient := personIDToken, githubAPIURL, githubAPIClient
	personIDToken = func(ctx context.Context, url string) (string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.serviceURL = url
		return identityTokenFixture, nil
	}
	githubAPIURL, githubAPIClient = s.srv.URL, s.srv.Client()
	t.Cleanup(func() { personIDToken, githubAPIURL, githubAPIClient = oldIdentity, oldURL, oldClient })
}

// counts is what the launch asked the two servers, read under the lock the
// handlers write them with.
func (s *serviceReadFixture) counts() (mints, reads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mints, s.reads
}

// confirmations is how many of confirmAbsent's two questions the forge was
// asked, read under the same lock.
func (s *serviceReadFixture) confirmations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirm
}

func (s *serviceReadFixture) presented() (identity, serviceURL, appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity, s.serviceURL, s.appID
}

func (s *serviceReadFixture) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == github.ServiceReadPath:
		s.mints++
		s.identity = r.Header.Get("Authorization")
		var body struct {
			Repository string `json:"repository"`
			AppID      string `json:"app_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"message":"the read mint's body is not JSON"}`, http.StatusBadRequest)
			return
		}
		s.appID = body.AppID
		if body.Repository != "acme/app" {
			// spec.md §7's second row: a repository the installation does not
			// hold is the service's own refusal, and it is not this fixture's
			// subject beyond being relayed.
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"this installation does not hold that repository"}`)
			return
		}
		fmt.Fprintf(w, `{"token":%q,"expires_at":%q,"repository":%q}`,
			readTokenFixture, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), body.Repository)
	case strings.HasSuffix(r.URL.Path, "/contents/fugaro.yaml"):
		s.reads++
		if s.status != 0 {
			w.WriteHeader(s.status)
			fmt.Fprintf(w, `{"message":%q}`, s.message)
			return
		}
		if s.notFound != "" {
			// The 404 the forge words about the REF rather than the path ("No
			// commit found for the ref …"): the absent-ref row, which refuses
			// and names the ref, and which no confirmation is asked about
			// (fileread.go). The body is GitHub's own prose, so the fixture
			// spells it rather than deriving it.
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":%q}`, s.notFound)
			return
		}
		if s.document == "" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","size":%d,"content":%q}`,
			len(s.document), base64.StdEncoding.EncodeToString([]byte(s.document)))
	case r.URL.Path == "/repos/acme/app":
		// confirmAbsent's first question, asked of the repository the read was
		// for: the contents API answers the same {"message":"Not Found"} for a
		// path the ref does not carry and for a repository this token cannot
		// reach, and only the first of those is the absent-file row that
		// selects nothing (fileread.go). A fixture that answers no such route
		// leaves every absent file a refusal instead, which is the row a
		// document-less ref never reaches.
		s.confirm++
		fmt.Fprint(w, `{"full_name":"acme/app","default_branch":"main"}`)
	case r.URL.Path == "/repos/acme/app/contents":
		// And its second, asked of the ref: a 404 body that still says "Not
		// Found" when the ref is gone is what a rewording of GitHub's own "No
		// commit found for the ref …" would produce, so the ref is confirmed
		// rather than believed.
		s.confirm++
		fmt.Fprint(w, `[]`)
	default:
		http.Error(w, `{"message":"the fixture answers no such route"}`, http.StatusNotFound)
	}
}

// TestLaunchWithoutCheckoutReadsTheRefThroughTheService is source 3 (spec.md
// §2, D901, D902): a launch of a GitHub repository from a directory that holds
// no checkout of it, on a project whose policy publishes a profile and an
// installation whose credentials service is published, is decided by the
// document the service reads for the operator — and lands on the shared job of
// the profile that document names.
//
// Red against main, where the launch is refused before any read.
func TestLaunchWithoutCheckoutReadsTheRefThroughTheService(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("a launch from outside the checkout, on an installation with a credentials service: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the standard/github project job the ref's fugaro.yaml selects", profile, provider)
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the launch made %d mint(s) and %d read(s), want exactly one of each: one read, held for the command and written nowhere", mints, reads)
	}
}

// TestLaunchWithoutCheckoutJudgesACloneWorkflowByTheCloneGates is the gates'
// half of source 3 (spec.md §2, D902), and the reason the read cannot be the
// job decision's alone: the image gates a launch passes are judged by the
// workflow's own kind and checkout mode, and a checkout: clone workflow runs
// the installation's base image — it is never built per repository, so it has
// no build record for a derived image's gates to find (image_refresh.go), and
// the shared job exists only for clone workflows (launchProjectJobFor). A
// launch from outside the checkout whose ref selects one is therefore judged
// by the clone gates, which it reaches only through the same read the job
// decision makes.
//
// The fixture's build record is removed for exactly this: with it there, the
// derived-image gates pass a clone workflow on a record the fixture wrote and
// no real repository has, so the launch looks fine while it is judged as the
// wrong kind of workflow.
//
// Mutation (run, restore): read the document in launchProjectJobFor alone, as
// the job decision's own — the launch is refused, "has no build record".
func TestLaunchWithoutCheckoutJudgesACloneWorkflowByTheCloneGates(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	if err := removeBuildRecord(f.cloudFixture); err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130005-ffff", "Add a feature"); err != nil {
		t.Fatalf("a launch of a checkout: clone workflow from outside its checkout: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the standard/github project job the ref's clone workflow selects", profile, provider)
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the launch made %d mint(s) and %d read(s), want one of each: the gates and the job decision share the one read", mints, reads)
	}
}

// TestLaunchWithoutCheckoutOnARefWithNoDocumentKeepsThePerRepositoryJob is
// spec.md §7's other source-3 row: the read answered, and the ref's tree
// carries no fugaro.yaml at all. A repository with no document selects no
// clone workflow and no profile — the project layer is never resolved on its
// own — so the per-repository job is not a fallback here but the answer, the
// same one the checkout path gives for a repository whose file defines no
// workflow.
//
// It is reachable only where the absent file is CONFIRMED: FileAt answers
// ErrFileNotFound for a 404 whose body says "Not Found" and only after the
// repository and the ref have been asked about (github's confirmAbsent), so
// the fixture answers both — a fixture that answers neither leaves every
// absent file a refusal, which is the direction that halts a launch rather
// than the one that places a run.
//
// Mutation (run, restore): drop the ErrFileNotFound case from
// repoConfigAtTheRef — the launch is refused instead of running on its own job.
func TestLaunchWithoutCheckoutOnARefWithNoDocumentKeepsThePerRepositoryJob(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.document = ""
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130006-beef", "Add a feature"); err != nil {
		t.Fatalf("a launch whose ref carries no fugaro.yaml: %v", err)
	}
	perRepo := gcp.JobName(appSlug, "web")
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != perRepo {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the per-repository job %s: a repository with no document selects no profile", len(calls), calls, perRepo)
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the launch made %d mint(s) and %d read(s), want one of each: one read, held for the command and written nowhere", mints, reads)
	}
	if got := s.confirmations(); got != 2 {
		t.Errorf("the absent file was confirmed against %d of the two things it takes (the repository and the ref), want both: an unconfirmed 404 is a refusal", got)
	}
}

// TestLaunchWithoutCheckoutNamesAForgeStatusAndNotItsBody: a forge that
// answers the read with a status Fugaro does not read as one of its own rows
// is reported by that status and nothing else. httpjson keeps up to 512 bytes
// of the answer's body (httpjson.maxErrorBody), and GitHub's own words about a
// failure are not Fugaro's to print — the promise the 403 row already keeps
// (spec.md §7), kept here for every other status a forge can answer with.
func TestLaunchWithoutCheckoutNamesAForgeStatusAndNotItsBody(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.status = http.StatusInternalServerError
	s.message = "the forge's own words about this failure"
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130007-cafe", "Add a feature")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want a remote error: the service answered and the read is what failed", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main as yourself: the read that decides the run's job could not be made (HTTP 500). Run it from acme/app's checkout"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is not spec.md §7's:\n got: %v\nwant: %s", err, want)
	}
	if strings.Contains(err.Error(), s.message) {
		t.Errorf("the refusal relays the forge's own body (%q): another writer's words about a failure do not belong in it: %v", s.message, err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestLaunchWithoutCheckoutWhereTheRefDoesNotExistNamesTheRef is spec.md §7's
// "the ref does not exist at the forge" row. The forge answers the read with
// the 404 it words about the ref rather than the path ("No commit found for the
// ref …"), which is a deleted, force-pushed or mistyped branch: it is refused,
// and refused in the ref's own row — not read as the absent-file row that keeps
// the per-repository job, because no other ref's document places this run
// (spec.md §3) — with the ref named and the checkout as the way out.
//
// Mutation (run, restore): drop the ErrRefNotFound case from serviceReadRefusal
// — the launch is refused by the default arm instead, which reports HTTP 404
// and never names the ref.
func TestLaunchWithoutCheckoutWhereTheRefDoesNotExistNamesTheRef(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.notFound = "No commit found for the ref main"
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130008-abcd", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main: the repository has no such ref, and a deleted or never-pushed branch is not a reason to read another one"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is not spec.md §7's row for a ref that does not exist:\n got: %v\nwant: %s", err, want)
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the launch made %d mint(s) and %d read(s), want one of each: the mint answered and the ref is what does not exist", mints, reads)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestLaunchWithoutCheckoutWithoutARecordedAppIDNamesTheRead is spec.md §7's row
// for the other thing a read needs from the installation's own records: the App
// ID the read mint is signed as (localcfg.Repo.GitHubAppID). A project whose
// local config published a credentials service but records no App for this
// repository cannot ask for a read at all, and the refusal says so — naming the
// recorded value and the checkout — rather than reporting a read that failed.
//
// Mutation (run, restore): drop the errNoGitHubAppID case from
// serviceReadRefusal — the default arm reports "this repository's github_app_id
// is not recorded" as a remote failure instead, and names no fix.
func TestLaunchWithoutCheckoutWithoutARecordedAppIDNamesTheRead(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	// The service is published; the App ID the mint is signed as is not
	// recorded, so there is nothing to ask the service with.
	f.appendConfig(t, "credentials_service: { url: "+s.srv.URL+" }\n")
	old := personIDToken
	personIDToken = func(ctx context.Context, url string) (string, error) {
		t.Errorf("the identity was minted for %s although this launch records no App ID to read with", url)
		return identityTokenFixture, nil
	}
	t.Cleanup(func() { personIDToken = old })
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130009-abcd", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main as yourself: this installation's local config records no github_app_id for acme/app, and the read the credentials service mints is signed as that App, so the read that decides the run's job cannot be made. Run it from acme/app's checkout"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is not spec.md §7's row for a missing App ID:\n got: %v\nwant: %s", err, want)
	}
	if mints, reads := s.counts(); mints != 0 || reads != 0 {
		t.Errorf("a launch with no App ID recorded made %d mint(s) and %d read(s), want none: there is nothing to ask with", mints, reads)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestProjectJobAgentAuthRefusesAnAgentSessionBeforeTheForgeRead is the
// credential gate's own half of D902's invariant (spec.md §5): the read this
// gate makes of the ref's document is made with the OPERATOR's own credential,
// so a coding agent's session must never make it — no identity minted, no
// service called, no forge asked.
//
// The gate reaches the forge through the same choke point the job decision does
// (serviceReadAtTheForge), and that is what this pins. The state it covers is
// the one where the decision answered from the working tree's checkout and this
// gate cannot find that checkout a moment later (checkoutRoot asks git again,
// and a directory whose git answers once and not twice is the state the old
// refusal named): with the guard on the decision's path alone, this call minted
// the operator's identity token and made the forge read.
//
// Mutation (run, restore): move the guard out of serviceReadAtTheForge and back
// into repoConfigAtTheRef — this call mints the operator's identity token and
// makes the forge read (1 mint, 1 read, no error).
func TestProjectJobAgentAuthRefusesAnAgentSessionBeforeTheForgeRead(t *testing.T) {
	s := newServiceReadFixture(t)
	oldIdentity, oldURL, oldClient := personIDToken, githubAPIURL, githubAPIClient
	personIDToken = func(ctx context.Context, url string) (string, error) {
		t.Errorf("an identity token was minted for %s in a coding agent's session", url)
		return identityTokenFixture, nil
	}
	githubAPIURL, githubAPIClient = s.srv.URL, s.srv.Client()
	t.Cleanup(func() { personIDToken, githubAPIURL, githubAPIClient = oldIdentity, oldURL, oldClient })

	testutil.IsolateGit(t)
	t.Chdir(t.TempDir()) // no checkout: the gate reads at the forge
	t.Setenv(agentenv.AgentMarkers[0], "1")

	e := &cloudEnv{lc: &localcfg.Config{
		CredentialsService: &localcfg.CredentialsService{URL: s.srv.URL},
		Repos:              map[string]localcfg.Repo{"acme/app": {GitHubAppID: "123456789"}},
	}}
	_, err := e.projectJobAgentAuth(context.Background(), &task.Spec{Repo: "acme/app", Ref: "main"})
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"fugaro run", "coding agent's session"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if mints, reads := s.counts(); mints != 0 || reads != 0 {
		t.Errorf("the credential gate made %d mint(s) and %d read(s) in a coding agent's session, want none: the refusal comes before anything is read", mints, reads)
	}
}

// TestProjectJobAgentAuthWordsAReadFailureLikeTheDecision is the other half of
// that same choke point: what this gate reports when the read fails is the
// read's own refusal, in spec.md §7's words — not a sentence about the
// credential. The decision refuses this launch with exactly this line, and the
// two readers of one read that disagreed about it would tell the operator about
// agent.auth when what failed is the read (and would report an unreachable
// service as a local condition).
//
// Mutation (run, restore): word the failure here as checkProjectJobAuthUnknown
// again — the refusal is the long credential sentence instead, and the row
// spec.md §7 gives an installation with no credentials service is lost.
func TestProjectJobAgentAuthWordsAReadFailureLikeTheDecision(t *testing.T) {
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir()) // no checkout: the gate reads at the forge

	// No credentials service: the row is the installation's own, and it is the
	// decision's line for the same state.
	e := &cloudEnv{lc: &localcfg.Config{
		Name:  "aurora",
		Repos: map[string]localcfg.Repo{"acme/app": {GitHubAppID: "123456789"}},
	}}
	_, err := e.projectJobAgentAuth(context.Background(), &task.Spec{Repo: "acme/app", Ref: "main"})
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main as yourself: this installation has no credentials service, so the read that decides the run's job cannot be made. Run it from acme/app's checkout"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the credential gate does not refuse in the decision's own words:\n got: %v\nwant: %s", err, want)
	}
}

// TestLaunchWithACheckoutNeverReadsTheService is the trigger's own boundary
// (spec.md §2: none of this runs unless the code is about to refuse). The
// checkout answers, so no credential is minted, no service is called and no
// App ID is needed: the launch is decided by the objects, exactly as it was
// before this task.
func TestLaunchWithACheckoutNeverReadsTheService(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.use(t, f.cloudFixture)
	isolateCache(t)
	projectJobCheckout(t)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130001-bbbb", "Add a feature"); err != nil {
		t.Fatalf("a launch from the repository's own checkout: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the standard/github project job", profile, provider)
	}
	if mints, reads := s.counts(); mints != 0 || reads != 0 {
		t.Errorf("a launch its own checkout answered made %d mint(s) and %d read(s), want none: the read is the refusal's, not the launch's", mints, reads)
	}
}

// TestLaunchWithoutCheckoutWithoutACredentialsServiceNamesTheCheckout is
// spec.md §7's first row, and the cost of D902's answer: the credentials
// service ships dormant, and where an installation has not opted in the
// launcher holds nothing to read a repository's configuration with — so the
// refusal stands, in the shape it has today, with the checkout as the way out.
func TestLaunchWithoutCheckoutWithoutACredentialsServiceNamesTheCheckout(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	// The service's server is there and the App ID is recorded; what is
	// missing is the installation's own opt-in, which is the block the
	// credentials stage writes.
	old := personIDToken
	personIDToken = func(ctx context.Context, url string) (string, error) {
		t.Errorf("the identity was minted for %s although this installation has no credentials service", url)
		return identityTokenFixture, nil
	}
	t.Cleanup(func() { personIDToken = old })
	addRepoAppIDToFixture(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130002-cccc", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main as yourself: this installation has no credentials service, so the read that decides the run's job cannot be made. Run it from acme/app's checkout"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is not spec.md §7's:\n got: %v\nwant: %s", err, want)
	}
	if mints, reads := s.counts(); mints != 0 || reads != 0 {
		t.Errorf("a launch with no credentials service made %d mint(s) and %d read(s), want none", mints, reads)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy is
// spec.md §7's row for a service that answers and a mint that still cannot read
// a file: A0's read mint asks with the set a file needs
// (github.PersonReadTokenPermissions), and an installation whose service still
// mints the set from before it — or whose App is not granted contents — is
// refused by the forge. The message names the service and the redeploy, and it
// carries neither the token nor GitHub's own text: the first is a credential
// and the second is another writer's words.
func TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.status = http.StatusForbidden
	s.message = "Resource not accessible by integration"
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130003-dddd", "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := "cannot read acme/app's fugaro.yaml at main: the installation's credentials service minted a token that cannot read a file; redeploy it with fugaro update"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal is not spec.md §7's:\n got: %v\nwant: %s", err, want)
	}
	for _, notWant := range []string{readTokenFixture, identityTokenFixture, s.message} {
		if strings.Contains(err.Error(), notWant) {
			t.Errorf("the refusal relays %q: neither a credential nor another writer's words belong in it: %v", notWant, err)
		}
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the launch made %d mint(s) and %d read(s), want one of each: the mint answered and the read was what failed", mints, reads)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d job(s): %+v", len(calls), calls)
	}
}

// TestLaunchWithoutCheckoutPresentsTheOperatorsOwnIdentity is the other half
// of D902: what the read presents is the OPERATOR's own Google ID token, minted
// for the service's own URL (the audience its person class admits), and the
// mint's body carries the App ID the project's local config records for this
// repository — not one read out of an environment variable, and not a key.
func TestLaunchWithoutCheckoutPresentsTheOperatorsOwnIdentity(t *testing.T) {
	f := newProjectJobFixture(t)
	s := newServiceReadFixture(t)
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130004-eeee", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	identity, serviceURL, appID := s.presented()
	if identity != "Bearer "+identityTokenFixture {
		t.Errorf("the mint was presented with %q, want the operator's own identity token as a bearer credential", identity)
	}
	if serviceURL != s.srv.URL {
		t.Errorf("the identity was minted for %q, want the credentials service's own URL %q, which is the audience it verifies", serviceURL, s.srv.URL)
	}
	if appID != "123456789" {
		t.Errorf("the read mint's body named app_id %q, want the value the project's local config records (123456789)", appID)
	}
}

// TestReadRepoConfigAtRefIsMemoizedPerRepoAndRef is D904 and spec.md §6: a mint
// is an act the credentials service AUDITS, so one command makes one read per
// (repository, ref) — the job decision and the credential gate share it — while
// a read of another ref is another read, because the document is the one at the
// ref the run checks out and nothing else's answer may stand in for it.
func TestReadRepoConfigAtRefIsMemoizedPerRepoAndRef(t *testing.T) {
	s := newServiceReadFixture(t)
	oldIdentity, oldURL, oldClient := personIDToken, githubAPIURL, githubAPIClient
	personIDToken = func(context.Context, string) (string, error) { return identityTokenFixture, nil }
	githubAPIURL, githubAPIClient = s.srv.URL, s.srv.Client()
	t.Cleanup(func() { personIDToken, githubAPIURL, githubAPIClient = oldIdentity, oldURL, oldClient })

	e := &cloudEnv{lc: &localcfg.Config{
		CredentialsService: &localcfg.CredentialsService{URL: s.srv.URL},
		Repos:              map[string]localcfg.Repo{"acme/app": {GitHubAppID: "123456789"}},
	}}
	ctx := withServiceReadCache(context.Background())

	for range 2 {
		data, err := readRepoConfigAtRef(ctx, e, "acme/app", "main")
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(data) != projectJobRepoYAML {
			t.Fatalf("the read answered %q, want the ref's own document", data)
		}
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("two reads of one (repo, ref) made %d mint(s) and %d read(s), want one of each", mints, reads)
	}
	if _, err := readRepoConfigAtRef(ctx, e, "acme/app", "release-2"); err != nil {
		t.Fatalf("read of another ref: %v", err)
	}
	if mints, reads := s.counts(); mints != 2 || reads != 2 {
		t.Errorf("a read of another ref made %d mint(s) and %d read(s) in total, want two of each: another ref is another document", mints, reads)
	}
}

// TestServiceReadDoesNotTouchTheWorkingTree is the seam's own contract: the
// read is one HTTP call to the service and one to the forge, and it writes
// nothing, fetches nothing and reads no checkout — the working directory it runs
// in is left exactly as it was found. It is why a launch from a machine that
// holds no checkout of the repository at all can make it.
func TestServiceReadDoesNotTouchTheWorkingTree(t *testing.T) {
	s := newServiceReadFixture(t)
	oldIdentity, oldURL, oldClient := personIDToken, githubAPIURL, githubAPIClient
	personIDToken = func(context.Context, string) (string, error) { return identityTokenFixture, nil }
	githubAPIURL, githubAPIClient = s.srv.URL, s.srv.Client()
	t.Cleanup(func() { personIDToken, githubAPIURL, githubAPIClient = oldIdentity, oldURL, oldClient })

	testutil.IsolateGit(t)
	dir := t.TempDir()
	t.Chdir(dir)

	e := &cloudEnv{lc: &localcfg.Config{
		CredentialsService: &localcfg.CredentialsService{URL: s.srv.URL},
		Repos:              map[string]localcfg.Repo{"acme/app": {GitHubAppID: "123456789"}},
	}}
	if _, err := readRepoConfigAtRef(context.Background(), e, "acme/app", "main"); err != nil {
		t.Fatalf("read: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the read left %d entr(ies) in the working directory: %v — it writes nothing and fetches nothing", len(entries), entries)
	}
	if mints, reads := s.counts(); mints != 1 || reads != 1 {
		t.Errorf("the read made %d mint(s) and %d read(s), want one of each: the document comes from the service, not from a disk", mints, reads)
	}
}

// TestServiceReadSelectsTheProfileTheRefNames is the parsed document walking
// the existing gates unchanged (A3's own interface note): a ref whose
// fugaro.yaml names a profile other than the default lands on that profile's
// job, because the read is the whole document and not a lookup of the
// per-repository job.
func TestServiceReadSelectsTheProfileTheRefNames(t *testing.T) {
	f := newProjectJobFixture(t)
	policy := strings.Replace(projectJobPolicy,
		`"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}`,
		`"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"},`+
			`"fast":{"provider":"github","cpu":4,"memory":"8Gi","timeout_class":"6h"}`, 1)
	publishPolicy(t, f.launchBudgetFixture, policy)
	fast := gcp.ProjectJobName("fast", "github", "")
	f.run.AddProjectJob(fast, "fast", "github", "", "4", "8Gi")
	f.run.SetJobTimeout(fast, projectJobTimeout+backend.TaskTimeoutSlack)

	s := newServiceReadFixture(t)
	s.document = strings.Replace(projectJobRepoYAML, "  web:\n    checkout: clone\n", "  web:\n    checkout: clone\n    job_profile: fast\n", 1)
	if !strings.Contains(s.document, "job_profile: fast") {
		t.Fatal("the fixture has no web: workflow to put a job_profile under")
	}
	s.use(t, f.cloudFixture)
	isolateCache(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261110-130005-ffff", "Add a feature"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, f.cloudFixture); profile != "fast" || provider != "github" {
		t.Fatalf("the launch ran on %s/%s, want the fast/github job the ref's job_profile names", profile, provider)
	}
}
