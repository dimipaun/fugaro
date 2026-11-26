package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is the end-to-end half of the job identity (plan task 3.3): whole
// runs in a shared project job, through the same fixture admission_test.go
// uses. The identity decision's own tests — the matching, the legacy path, the
// halts — are in projectjob_internal_test.go, where they can be asked about
// values; what needs a whole run is the ordering (the refusal before any git
// credential and before any lease), the remote, and the admission.
//
// Every run here is in a *project* job: the platform reports an execution of
// gcp.ProjectJobName of a triple the published policy defines, so the run
// adopts that triple — the provider comes from the job's name and from nothing
// a launcher can set — and the clone URL is computed rather than read. The
// per-repository path these runs must not change is admission_test.go's.

// cloudRunExecution is the platform's own report of an execution of job, the
// shape internal/cli hands the runner (backend.ExecutionFromEnv's output,
// Deps.Execution). The run reads the job's name out of it and nothing else.
func cloudRunExecution(job string) string {
	return "projects/aurora-fp/locations/europe-west1/jobs/" + job + "/executions/" + job + "-aaaaa"
}

// projectJob is one run's job identity as a fixture builds it: the triple the
// job's name is computed from (gcp.ProjectJobName, plan task 3.2 step 1) and
// the two repositories the run's own halves name — the slug it was launched for
// (FUGARO_RUN) and the repository its task.json names. The two are the same
// repository in every test but the binding's own.
type projectJob struct {
	profile  string
	provider string
	account  string
	slugRepo string
	specRepo string // "" is the same repository as slugRepo (the bound case)
}

// projectJobOf is the fixture's own project job: the default profile, serving
// github, running as no Claude account — the profile lists none, so the axis
// config.ProjectJobAccounts computes for it is the empty account alone — on
// the fixture's repository.
func projectJobOf() projectJob {
	return projectJob{profile: projectpolicy.DefaultProfileName, provider: "github", slugRepo: "acme/app"}
}

// projectRun is newBK re-homed on a run in a shared project job (plan task
// 3.3): the platform reports an execution of the job's computed name — computed
// here the way internal/infra computes the deployed name, so the run matches a
// name it did not write — the storage slug is task.Slug of the repository the
// run was launched for, and the policy node the caller wrote is published under
// the rules marker, as `fugaro project policy publish` leaves it. mode and
// capUSD are the budget's, exactly as in admissionRun.
//
// Nothing here sets FUGARO_GIT_PROVIDER or a --provider kind: a project job's
// provider is the one its own name pins, so the fixture every test but the
// env-var ones starts from has no other provider anywhere. The git credential
// counter is installed (countAuths), which is what tells a refusal from a run
// that opened its provider first.
//
// A run whose slug and task.json disagree (specRepo) is the binding's own
// case: it reads and writes the bucket, the locks and the ledger under the slug
// it was launched for, which is what admissionRunCfg's slugRepo is for too.
func projectRun(t *testing.T, job projectJob, mode, capUSD, policy string) *bk {
	t.Helper()
	return projectRunCfg(t, job, gwConfig(t, ""), mode, capUSD, policy)
}

// projectRunCfg is projectRun with the repository's own fugaro.yaml replaced:
// the fixture's config with one key moved, for the runs whose workflow names a
// clock the job they are on does not have (clampTotalToJob).
func projectRunCfg(t *testing.T, job projectJob, cfg, mode, capUSD, policy string) *bk {
	t.Helper()
	if job.specRepo == "" {
		job.specRepo = job.slugRepo
	}
	b := newBK(t, cfg, mode, capUSD)
	slug, err := task.Slug(job.provider, job.slugRepo)
	if err != nil {
		t.Fatal(err)
	}
	if slug == repoSlug && job.slugRepo != "acme/app" {
		// A mismatch fixture must not land on the fixture's own slug: the run
		// would be homed on its own repository and the binding would have
		// nothing to refuse (admissionRunCfg's check, for the same reason).
		t.Fatalf("task.Slug(%q, %q) is the fixture's own slug; the test would check nothing", job.provider, job.slugRepo)
	}
	b.deps.Execution = cloudRunExecution(gcp.ProjectJobName(job.profile, job.provider, job.account))
	if slug != repoSlug || job.specRepo != "acme/app" {
		store := runstore.Open(b.harness.bucket, slug, runID)
		// The project layer travels with the task, as it does at a launch: it
		// is the installation's own and names the models a repository's file
		// may no longer name (S1, D3237), so a task written without it is a
		// run with nothing to price.
		spec := b.harness.taskWithLayer(&task.Spec{Version: 1, RunID: runID, Repo: job.specRepo, Ref: "main",
			Workflow: "app", Task: "Add a feature"})
		if err := store.WriteTask(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
		b.deps.Store, b.harness.store = store, store
	}
	if slug != repoSlug {
		// newBK minted the run's token under the fixture's own slug, which this
		// run does not carry; its own token's slug claim is the identity the run
		// compares with its own (mintUnder).
		mintUnder(t, b, slug, time.Now())
		micros := func(v budget.Micros) *budget.Micros { return &v }
		b.db.Set(budget.PathCapsRepo(slug), budget.RepoCaps{
			DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: job.specRepo})
	}
	countAuths(b)
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	if policy != "" {
		b.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	}
	return b
}

// profileShape is a profile's resources as a policy node spells them. A
// profile must name cpu, memory and a timeout class, and they must be the shape
// Cloud Run accepts (projectpolicy.validProfiles, gcp.CheckResources); nothing
// in this file is about them, so one fixture carries them for every policy
// below.
const profileShape = `"cpu":2,"memory":"4Gi","timeout_class":"6h"`

// projectPolicyFor is the published policy node these tests run under: a node
// that defines the standard profile, serving github, with the fixture's own
// shape — the profile every workflow that names none runs on, and the only one
// the nodes here define.
//
// project is the node's allow_repos patterns and profile the profile's own (the
// empty string for a profile that names none, whose list falls back to the
// project's), each
// written as the JSON array an operator's node would carry.
func projectPolicyFor(project, profile string) string {
	own := ""
	if profile != "" {
		own = `,"allow_repos":` + profile
	}
	return `{"version":9,"allow_repos":` + project + `,"profiles":{"standard":{"provider":"github"` +
		own + `,` + profileShape + `}}}`
}

// projectPolicyCloning is projectPolicyFor plus the one clone_urls entry the
// remote test needs: a custom clone URL for the fixture's repository, which
// lives in the policy and not in a job's environment (design §4.1(1), plan task
// 3.3 step 3). The member name is the escaped spelling the node holds — a
// canonical repository name carries slashes and RTDB refuses a key containing
// one (projectpolicy's CloneURLs) — so the fixture publishes what a real
// publish publishes.
func projectPolicyCloning(project, url string) string {
	return `{"version":9,"allow_repos":` + project + `,"clone_urls":{"github%2Facme%2Fapp":"` + url + `"},` +
		`"profiles":{"standard":{"provider":"github",` + profileShape + `}}}`
}

// projectPolicyClass is projectPolicyFor with the standard profile's timeout
// class replaced. The class is the job's Cloud Run task timeout less
// backend.TaskTimeoutSlack, and it is the clock a run on that job is held to
// (clampTotalToJob). The fixture's own class is 6h — longer than any workflow
// these tests run — so a test that wants the clamp to bite names one of its
// own.
func projectPolicyClass(project, class string) string {
	return strings.Replace(projectPolicyFor(project, ""), `"timeout_class":"6h"`,
		`"timeout_class":"`+class+`"`, 1)
}

// providerCloneURL is repourl.CloneURL's own answer for a repository: what a
// run computes when the policy names no clone URL of its own. It fails the test
// rather than returning an error, like every other fixture helper.
func providerCloneURL(t *testing.T, provider, repo string) string {
	t.Helper()
	url, err := repourl.CloneURL(provider, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// cloneHost makes the fixture's git host local, so a run in a project job —
// whose clone URL is an https URL, because that is what the deployer writes
// into a job's environment and what repourl.CloneURL validates — can clone it
// without reaching the network.
//
// It moves the fixture's bare repository to the path the rewrite resolves url
// to (the exact URL is an entry of its own, so a spelling the policy carries
// can be the one that works), installs the rewrite, and points the fake
// provider at the moved repository: the provider reads a pull request's head
// out of it, so a run that pushed somewhere else fails. Every *other* URL on
// either provider's host is rewritten into a directory that is not a
// repository, so a run that cloned anything but url fails locally instead of
// reaching github.com — the same guard admission_test.go's refusal tests use,
// and the reason a regression cannot quietly make a test do network I/O.
//
// Deps.Remote keeps the fixture's old path, which no longer exists after the
// move: a run that read the job's environment instead of computing its remote
// finds nothing there. TestProjectJobIgnoresFUGARORemote swaps in a value of
// its own on top of that.
//
// It returns url, so a test can say which URL it made local at the call site.
func cloneHost(t *testing.T, b *bk, repo, url string) string {
	t.Helper()
	absent := filepath.Join(t.TempDir(), "no-such-host")
	bare := filepath.Join(t.TempDir(), filepath.FromSlash(repo)+".git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b.remote, bare); err != nil {
		t.Fatal(err)
	}
	b.provider.Remote, b.remote = bare, bare
	urlRewrite(t, map[string]string{
		url: bare,
		// The host's own prefix, so every other URL on it goes nowhere: git
		// uses the longest matching prefix, which is the exact entry above.
		"https://github.com/":    absent + "/",
		"https://bitbucket.org/": absent + "/",
	})
	return url
}

// projectHost is cloneHost for the fixture's own repository under github: the
// URL the run must clone is the one it computes when the policy names no clone
// URL, and it is this URL that proves the run used github's — a run that took
// its provider (or its remote) from anywhere else clones something the rewrite
// sends into an empty directory, and fails.
func projectHost(t *testing.T, b *bk) string {
	t.Helper()
	return cloneHost(t, b, "acme/app", providerCloneURL(t, "github", "acme/app"))
}

// urlRewrite writes a git global config whose url.<base>.insteadOf entries
// rewrite the given URL prefixes, and points GIT_CONFIG_GLOBAL at it.
// testutil.IsolateGit (which the fixture calls) sends git at /dev/null; this is
// the run's own config, and every git call gitops makes inherits the process
// environment it is set in. The keys are the prefixes git matches — the longest
// match wins, which is what lets a full URL be an entry of its own beside the
// host's — and the values are what they are rewritten to. Order is irrelevant.
func urlRewrite(t *testing.T, rewrites map[string]string) {
	t.Helper()
	var cfg strings.Builder
	for prefix, base := range rewrites {
		fmt.Fprintf(&cfg, "[url %q]\n\tinsteadOf = %s\n", base, prefix)
	}
	path := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(path, []byte(cfg.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", path)
}

// projectSucceeded runs the fixture's happy path — one implement step and one
// clean review — and fails unless the run produced a ready pull request. It
// also asserts the run did open its provider: the admission check is the only
// thing between bootstrap and the clone, so a run that reached its provider is
// one that was admitted.
func projectSucceeded(t *testing.T, b *bk) *runstore.Record {
	t.Helper()
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the run was not admitted", rec, err)
	}
	if b.auths == nil {
		t.Fatal("the fixture is not counting git credential requests")
	}
	if *b.auths == 0 {
		t.Error("the run finished without ever asking its provider for a git credential: it cannot have cloned anything")
	}
	return rec
}

// A run in a shared project job whose repository the profile admits runs as it
// always has: the job's name is one the published policy computes, the slug is
// the slug of the repository task.json names under the job's provider, and the
// profile admits it — so the run clones the URL it computed and finishes.
//
// Mutation (run, restored): make a project job halt unconditionally. This test
// and every other positive one here fail at once.
func TestProjectJobRunsWhenTheProfileAdmitsTheRepository(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	projectHost(t, b)
	projectSucceeded(t, b)
}

// classConfig is the fixture's config with the workflow's clock moved out to an
// hour: a total and a reserve no four-minute class can hold, which is the shape
// a repository that took the new three-hour default has on a job sized for the
// previous one.
func classConfig(t *testing.T) string {
	t.Helper()
	const (
		fixture = "timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }"
		moved   = "timeouts: { total: 1h, stage: 30m, verify: 10m, finalize_reserve: 5m }"
	)
	base := gwConfig(t, "")
	cfg := strings.Replace(base, fixture, moved, 1)
	if cfg == base {
		t.Fatalf("the fixture config no longer writes %s", fixture)
	}
	return cfg
}

// TestProjectJobHoldsTheRunBudgetToItsClass is the clamp where bootstrap puts
// it: the workflow's timeouts.total and its reserve are the job's class's
// before anything reads the workflow, so the budget, the record's own clocks
// and the lock's expiry are all the class's. The budget itself has no reader
// outside the run, so a whole run is asked about it the way it is asked about
// any other clock — by what it does when the clock is the test's: a jump past
// the clamped finalize point and well inside the workflow's own hour leaves a
// run that was clamped stopped before its next stage, and one that was not
// finishes.
//
// Mutation (run, restored): drop the clampTotalToJob call from bootstrap, or
// move it past r.budget, past the record's finalize_reserve_s, or past
// acquireLock. The first subtest fails on the reserve and on the deadline, the
// second on the review that then runs.
func TestProjectJobHoldsTheRunBudgetToItsClass(t *testing.T) {
	// A class under the default five-minute reserve: the clamp cuts the reserve
	// with the total (to 4m/5 = 48s), which is the one value of the record's
	// own that only moves when the clamp ran.
	const class = "4m"
	policy := projectPolicyClass(`["github/acme/*"]`, class)

	t.Run("the record's clocks are the class's", func(t *testing.T) {
		b := projectRunCfg(t, projectJobOf(), classConfig(t), "off", "", policy)
		projectHost(t, b)
		rec := projectSucceeded(t, b)
		if reserve, ok := rec.FinalizeReserve(); !ok || reserve != 48*time.Second {
			t.Errorf("finalize_reserve_s = %v, want 48s: the workflow's own 5m reserve does not fit in the class", rec.FinalizeReserveS)
		}
		// The lock's expiry is the record's deadline (lockcache.go's
		// lockDeadline): the class, the task timeout's slack, and the lock's own
		// minute past it. The workflow's hour is not in it.
		want := rec.StartedAt.Add(4*time.Minute + backend.TaskTimeoutSlack + time.Minute)
		if rec.Deadline == nil || !rec.Deadline.Equal(want) {
			t.Errorf("deadline = %v, want %v (started %v + the 4m class): a run on this job must finalize, and let its lock go, before the job's own task timeout kills it",
				rec.Deadline, want, rec.StartedAt)
		}
	})

	t.Run("the budget is the class's", func(t *testing.T) {
		b := projectRunCfg(t, projectJobOf(), classConfig(t), "off", "", policy)
		projectHost(t, b)
		// The clock is the test's, and the jump is free: four minutes is past
		// the clamped finalize point (4m less the 48s reserve) and nowhere near
		// the workflow's own hour less its 5m, so a budget that was left
		// unclamped runs the review this one refuses.
		var mu sync.Mutex
		clock := time.Now()
		b.deps.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
		tick := func(s step) step {
			return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				res, err := s(t, ctx, req)
				mu.Lock()
				clock = clock.Add(4 * time.Minute)
				mu.Unlock()
				return res, err
			}
		}
		rec, _ := b.run(t, tick(implement("feature")), review("ship", 0))
		if len(b.agent.calls) != 1 {
			t.Errorf("the run ran %d stages, want 1: the stage after the first was inside the budget the workflow names, not the one the job has", len(b.agent.calls))
		}
		if !strings.Contains(rec.Reason, "time budget exhausted") {
			t.Errorf("the run ended %s with reason %q, want it stopped at the job's %s class", rec.Status, rec.Reason, class)
		}
	})
}

// The clone URL is computed from task.json's repository and the job's provider,
// never read from the job's environment (design §4.1(1), plan task 3.3 step 3).
// The two subtests ask the two halves separately: the run follows the
// repository its task names — here a repository the fixture does not otherwise
// use, so the URL can only have been computed from the task — and the policy's
// own clone URL wins over the computed default when it names one.
//
// Mutation (run, restored): read Deps.Remote, or ignore the policy's clone
// URL. The first subtest's run finds no repository at the fixture's old path,
// and the second finds none at the URL it then computes.
func TestRemoteFromTaskAndPolicy(t *testing.T) {
	t.Run("the task's own repository", func(t *testing.T) {
		b := projectRun(t, projectJob{profile: "standard", provider: "github", slugRepo: "acme/other"},
			"off", "", projectPolicyFor(`["github/acme/*"]`, ""))
		cloneHost(t, b, "acme/other", providerCloneURL(t, "github", "acme/other"))
		projectSucceeded(t, b)
	})
	t.Run("a custom clone URL in the policy", func(t *testing.T) {
		// The spelling is the policy's, not the computed default: github's own
		// URL for this repository is https://github.com/acme/app.git, and the
		// rewrite makes that one resolve into a directory that is not a
		// repository, so only a run that used the policy's URL can clone.
		const custom = "https://github.com/ACME/App.git"
		b := projectRun(t, projectJobOf(), "off", "", projectPolicyCloning(`["github/acme/*"]`, custom))
		cloneHost(t, b, "acme/app", custom)
		projectSucceeded(t, b)
	})
}

// FUGARO_REMOTE stops being read on a project job (plan task 3.3 step 3). It is
// a launcher-writable value (12a item 2, D10) and a shared job sets none, so a
// launcher that overrides it must not be able to point the run at another
// repository — or at nothing at all, which is what the fixture's own remote is
// here: the job's environment names a repository that does not exist, and the
// run clones its own anyway.
//
// The variable is set both ways it can arrive: in the run's environment (which
// is what internal/cli's --remote flag defaults to) and in Deps.Remote (what
// the runner reads on the per-repository path), so a regression that reached
// for either finds the hostile value.
//
// Mutation (run, restored): consult Deps.Remote on a project job (return
// r.d.Remote from cloneURL). This run clones nothing and fails.
func TestProjectJobIgnoresFUGARORemote(t *testing.T) {
	const hostile = "https://github.com/evil/other.git"
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	t.Setenv(runner.RemoteEnv, hostile)
	b.deps.Remote = hostile
	projectHost(t, b)
	projectSucceeded(t, b)
}

// bakedProjectCheckout plants the checkout a project job's image holds for
// this run's repository: a clone of the fixture's remote — so nothing here
// needs a network — whose origin is the URL the run computes for it, at the
// per-slug path the engine's own bake stage writes (BakedDir). plant is what
// the image's build is taken to have done to it, and it is nil for a checkout
// the run is expected to accept as it is.
func bakedProjectCheckout(t *testing.T, b *bk, remote string, plant func(t *testing.T, dir string)) string {
	t.Helper()
	dir := bakedDir(t, b.harness, repoSlug)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", b.remote, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", remote)
	if plant != nil {
		plant(t, dir)
	}
	return dir
}

// bakedHook writes a hook of name into dir, executable, that appends one line
// to marker: what the image's build left behind for the run to run.
func bakedHook(t *testing.T, dir, name, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho \"$0\" >> "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeFileAt writes content at path, creating its directory: a file the
// image's build is taken to have left in the checkout.
func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitTry runs git in dir and returns its output, or "" when git exits non-zero
// — a configuration key that is not set, a remote that has no push URL. The
// keys a test expects to be gone are the ones asked this way.
func gitTry(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// A project job whose image baked this run's repository works on that baked
// checkout, once the runner has reset it to the state it works from (D2308):
// the checkout's own configuration is replaced by the one the run writes, so
// the fetch that follows goes to the URL the run computed and to nothing the
// image's configuration named.
//
// The configuration here is a hostile one — a push URL to another repository, a
// rewrite of the run's own URL onto it, and a remote whose name is that URL,
// which is the fourth door a push finds (gitops' PushURLs) — and one write
// closes all of them, because the file is replaced. A run that kept them
// fetches a URL cloneHost rewrites into a directory that is not a repository,
// and fails locally instead of reaching anything.
//
// Mutation (run, restored): return from checkProjectCheckoutOrigin after the
// origin check, without the reset. The fetch meets the rewrite, and this test
// fails on the checkout the run then works on.
func TestBakedCheckoutOnAProjectJobIsResetBeforeFetch(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	computed := projectHost(t, b)
	baked := bakedProjectCheckout(t, b, computed, func(t *testing.T, dir string) {
		testutil.Git(t, dir, "config", "remote.origin.pushurl", otherRemote)
		testutil.Git(t, dir, "config", "url."+otherRemote+".insteadOf", computed)
		testutil.Git(t, dir, "config", "remote."+computed+".url", otherRemote)
	})
	testutil.Git(t, baked, "commit", "--quiet", "--allow-empty", "-m", "baked")
	head := testutil.Git(t, baked, "rev-parse", "HEAD")
	rec := projectSucceeded(t, b)
	want, err := filepath.EvalSymlinks(baked)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.agent.calls[0].Dir; got != want {
		t.Errorf("the implement stage ran in %q, want the image's baked checkout %q", got, want)
	}
	if got := testutil.Git(t, baked, "config", "--local", "--get-regexp", `^remote\.`); got != "remote.origin.url "+computed+"\nremote.origin.fetch +refs/heads/*:refs/remotes/origin/*" {
		t.Errorf("the baked checkout's remotes are\n%s\nwant the origin the run wrote and nothing else", got)
	}
	if got := gitTry(baked, "config", "--local", "--get", "remote.origin.pushurl"); got != "" {
		t.Errorf("the baked checkout still pushes to %q", got)
	}
	if got := gitTry(baked, "config", "--local", "--get-regexp", `^url\.`); got != "" {
		t.Errorf("the baked checkout still rewrites URLs: %q", got)
	}
	if got := gitTry(baked, "config", "--local", "--get", "core.hooksPath"); filepath.Base(got) != "baked-hooks" {
		t.Errorf("the baked checkout's core.hooksPath = %q, want the run's own empty hook directory", got)
	}
	if rec.Image == nil || rec.Image.BakedCommit != head {
		t.Errorf("the record's image = %+v, want the baked commit %s", rec.Image, head)
	}
	if got := testutil.Git(t, b.remote, "rev-parse", "refs/heads/"+rec.Branch); got != rec.HeadSHA {
		t.Errorf("remote branch %s != record head %s: the run did not push its own repository", got, rec.HeadSHA)
	}
}

// A hook the image baked never runs (D2308). The image's build is free to leave
// one in the hook directory git resolves — its own configuration's
// core.hooksPath, or .git/hooks, which is git's default — and the run's agent
// commits in that checkout: without the reset, the stamp's pass-throughs chain
// to that directory (writeStampHooks reads it at bootstrap) and the image's
// hook runs inside the run's own commit.
//
// Mutation (run, restored): leave the hooks in place — drop the .git/hooks
// removal and the core.hooksPath write from ResetBakedGit. Both rows then find
// the marker, and this test is the one that fails.
func TestBakedCheckoutWithAPlantedHookNeverRunsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, b *bk, dir, marker string)
	}{
		{"the hook directory the configuration names", func(t *testing.T, b *bk, dir, marker string) {
			hooks := filepath.Join(t.TempDir(), "baked-hooks")
			bakedHook(t, hooks, "pre-commit", marker)
			testutil.Git(t, dir, "config", "core.hooksPath", hooks)
		}},
		{"the hook directory git finds by default", func(t *testing.T, b *bk, dir, marker string) {
			bakedHook(t, filepath.Join(dir, ".git", "hooks"), "pre-commit", marker)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "hook-ran")
			b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
			computed := projectHost(t, b)
			bakedProjectCheckout(t, b, computed, func(t *testing.T, dir string) {
				tc.plant(t, b, dir, marker)
			})
			projectSucceeded(t, b)
			if data, err := os.ReadFile(marker); err == nil {
				t.Errorf("a hook the image baked ran during the run: %s", strings.TrimSpace(string(data)))
			}
		})
	}
}

// A checkout the image holds that is not this run's repository is refused, on
// every path a project job can find one (D2308; review C5's refusal, which this
// replaces): the directory the image baked this run's repository at (BakedDir),
// the work directory, which is where a hand-written Dockerfile's own clone sits,
// and both at once — where the run adopts the per-slug checkout and would never
// read the work directory again, which is the case review F25 found unguarded.
// The origin is what says which repository an image's checkout is a checkout
// OF, and another repository's is one this run may not work on or push from
// under its own slug, whatever state its .git is put into.
//
// Mutation (run, restored): drop the remoteAdmissionErr call from
// checkProjectCheckoutOrigin, or the refuseForeignWorkDirCheckout call from the
// adoption below it. Every row then works on another repository's checkout and
// pushes the run's branch into it.
func TestBakedCheckoutWithAnotherOriginHalts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, b *bk)
	}{
		{"the directory the run would adopt", func(t *testing.T, b *bk) {
			bakedProjectCheckout(t, b, otherRemote, nil)
		}},
		{"the work directory", func(t *testing.T, b *bk) {
			testutil.Git(t, filepath.Dir(b.deps.WorkDir), "clone", "--quiet", b.remote, b.deps.WorkDir)
			testutil.Git(t, b.deps.WorkDir, "remote", "set-url", "origin", otherRemote)
		}},
		// Both at once, which is what an entry whose Dockerfile clones into its
		// own WORKDIR leaves beside a generated bake stage: the run adopts the
		// per-slug checkout and the work directory holds another repository's.
		{"beside the checkout the run adopts", func(t *testing.T, b *bk) {
			bakedProjectCheckout(t, b, providerCloneURL(t, "github", "acme/app"), nil)
			testutil.Git(t, filepath.Dir(b.deps.WorkDir), "clone", "--quiet", b.remote, b.deps.WorkDir)
			testutil.Git(t, b.deps.WorkDir, "remote", "set-url", "origin", otherRemote)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
			projectHost(t, b)
			tc.plant(t, b)
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "is not this run's repository", "evil/other", "acme/app", "before any git credential")
		})
	}
}

// A baked .git this run cannot put into a fixed state halts the run, and the
// halt names which shape it met (D2308): a .git that is a file naming a git
// directory elsewhere, objects complemented by a store outside the checkout, a
// commondir, a submodule's own configuration, hooks, commondir or alternates —
// the file shapes being the checkout's own, one level in — a symbolic link
// anywhere under .git/modules being one of them too, since git follows it and no
// walk of the directory does, and a submodule's working directory whose .git
// file names a git directory outside this checkout's own .git/modules, whether it says so or
// reaches one through such a link: each of them a way for what this run reads
// and runs to be decided by something other than the checkout it works on, and
// none of them reachable by a reset of this .git.
//
// Mutation (run, restored): drop the shape refusals (refuseUnresettableBakedGit
// returns nil). Every row then resets the checkout and runs.
func TestBakedCheckoutWithAGitdirFileAlternatesOrSubmoduleHooksHalts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		plant func(t *testing.T, b *bk, dir string)
	}{
		{"a .git file", "naming a git directory outside the checkout", func(t *testing.T, b *bk, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../elsewhere\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"alternates", "objects from a store outside the checkout", func(t *testing.T, b *bk, dir string) {
			writeFileAt(t, filepath.Join(dir, ".git", "objects", "info", "alternates"), "../elsewhere/objects\n")
		}},
		{"a commondir", "another repository's git directory", func(t *testing.T, b *bk, dir string) {
			writeFileAt(t, filepath.Join(dir, ".git", "commondir"), "../elsewhere\n")
		}},
		{"a submodule's configuration", "a submodule's configuration", func(t *testing.T, b *bk, dir string) {
			writeFileAt(t, filepath.Join(dir, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = "+otherRemote+"\n")
		}},
		{"a submodule's hooks", "a submodule's hook directory", func(t *testing.T, b *bk, dir string) {
			bakedHook(t, filepath.Join(dir, ".git", "modules", "sub", "hooks"), "pre-commit", filepath.Join(t.TempDir(), "ran"))
		}},
		// The two shapes the top-level .git is refused for, one level in: the
		// configuration git reads for a submodule, and the objects it reads for
		// it, are those of another git directory.
		{"a submodule's commondir", "names another git directory as the one shared by the submodule", func(t *testing.T, b *bk, dir string) {
			testutil.Git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",vendor/dep")
			writeFileAt(t, filepath.Join(dir, "vendor", "dep", ".git"), "gitdir: ../../.git/modules/dep\n")
			writeFileAt(t, filepath.Join(dir, ".git", "modules", "dep", "commondir"), filepath.Join(t.TempDir(), "external")+"\n")
		}},
		{"a submodule's alternates", "objects from a store outside the checkout", func(t *testing.T, b *bk, dir string) {
			writeFileAt(t, filepath.Join(dir, ".git", "modules", "dep", "objects", "info", "alternates"), "../elsewhere/objects\n")
		}},
		// The shape clean -ffdx leaves alone: a submodule's path is tracked, so
		// the whole directory stays, and the .git file in it decides which git
		// directory — and so which configuration — git reads for that submodule
		// in the checkout this run works in.
		{"a submodule's working directory", "this checkout's own .git/modules does not hold", func(t *testing.T, b *bk, dir string) {
			testutil.Git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",vendor/dep")
			writeFileAt(t, filepath.Join(dir, "vendor", "dep", ".git"), "gitdir: "+t.TempDir()+"\n")
		}},
		// The shape a link makes: that same worktree, whose .git file names a
		// path under .git/modules that is a symbolic link, so the configuration
		// git reads for the submodule is the image's and sits outside the
		// checkout this run works on.
		{"a submodule's worktree behind a symlink", "is a symbolic link", func(t *testing.T, b *bk, dir string) {
			testutil.Git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",vendor/dep")
			writeFileAt(t, filepath.Join(dir, "vendor", "dep", ".git"), "gitdir: ../../.git/modules/dep\n")
			if err := os.MkdirAll(filepath.Join(dir, ".git", "modules"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(dir, ".git", "modules", "dep")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
			computed := projectHost(t, b)
			bakedProjectCheckout(t, b, computed, func(t *testing.T, dir string) {
				tc.plant(t, b, dir)
			})
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "cannot be reset", tc.want)
		})
	}
}

// The reset is what makes the tree the run works on the repository's and not
// the image's: a commit the image's build made and a tracked file it changed
// never reach the run's branch, because the run checks out the ref it fetched
// onto the reset checkout (a hard reset to the fetched ref), and what the build
// left untracked or ignored is removed from the tree before that (clean -ffdx).
// The replace refs and grafts go with them: a checkout carrying one answers a
// read of a commit with another object, so a base, a head or a diff this run
// measured would be the image's answer and not the repository's.
//
// A tracked file the build marked skip-worktree — what a sparse checkout sets,
// and what an edit of its own leaves behind — is the one the run's own checkout
// cannot put back, because git reads that bit and keeps the working-tree copy:
// the reset's own removal of the index is what restores it, and without it the
// build's edit reaches the agent's tree and the agent's own edit to that file
// is never staged.
//
// The ignored file is what tells the two cleans apart — an ignore rule from
// .git/info/exclude, which the run's own clean -fd keeps (a warm build cache)
// and which the reset's -x removes — and it is committed by the agent's
// `git add -A` in the implement step, so the run's own pull request is where it
// would surface.
//
// Mutation (run, restored): drop the clean -ffdx from ResetBakedGit, or the
// replace-ref and graft removal. The first row's branch then carries the
// build's leftovers; the second's reads answer with the replaced object.
// Dropping the index removal and the reset --hard HEAD leaves README.md reading
// the build's edit.
func TestBakedCheckoutDropsReplaceRefsAndGraftsAndTrackedEdits(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	computed := projectHost(t, b)
	baked := bakedProjectCheckout(t, b, computed, func(t *testing.T, dir string) {
		writeFileAt(t, filepath.Join(dir, ".git", "info", "exclude"), "build-output/\n")
		writeFileAt(t, filepath.Join(dir, "build-output", "stale.bin"), "stale\n")
		writeFileAt(t, filepath.Join(dir, "build.sh"), "#!/bin/sh\nexit 1\n")
		testutil.Git(t, dir, "commit", "--quiet", "-am", "the image's build")
		// The build's own checkout state: a tracked file it marked
		// skip-worktree, holding an edit of its own, which git leaves alone
		// through every checkout this run makes.
		testutil.Git(t, dir, "update-index", "--skip-worktree", "README.md")
		writeFileAt(t, filepath.Join(dir, "README.md"), "the build edited this\n")
	})
	// A replacement that would make the fetched main answer with the image's
	// own commit, loose beside the packed one the build's gc left.
	planted := testutil.Git(t, baked, "commit-tree", "-m", "replaced", testutil.Git(t, baked, "rev-parse", "HEAD^{tree}"))
	testutil.Git(t, baked, "replace", testutil.Git(t, baked, "rev-parse", "HEAD"), planted)
	testutil.Git(t, baked, "pack-refs", "--all")
	writeFileAt(t, filepath.Join(baked, ".git", "info", "grafts"), testutil.Git(t, baked, "rev-parse", "HEAD")+"\n")
	rec := projectSucceeded(t, b)
	if _, err := os.Stat(filepath.Join(baked, "build-output")); err == nil {
		t.Error("the ignored output the image's build left is still in the checkout the run worked on")
	}
	if got := gitTry(baked, "for-each-ref", "refs/replace/"); got != "" {
		t.Errorf("the reset checkout still carries the replacement %q", got)
	}
	if _, err := os.Stat(filepath.Join(baked, ".git", "info", "grafts")); err == nil {
		t.Error("the reset checkout still carries a graft")
	}
	if changed := testutil.Git(t, b.remote, "diff", "--name-only", "main.."+rec.Branch); changed != "feature.txt" {
		t.Errorf("the run's branch changes\n%s\nwant the agent's own file alone: the image's edited build.sh and its ignored output are not the run's", changed)
	}
	want := testutil.Git(t, b.remote, "show", "main:README.md")
	if got, err := os.ReadFile(filepath.Join(baked, "README.md")); err != nil || strings.TrimSpace(string(got)) != want {
		t.Errorf("README.md in the checkout the run worked on = %q (%v), want the fetched ref's %q: a skip-worktree bit must not carry the image's edit into the run", got, err, want)
	}
}

// The other repositories the image baked are removed before the agent starts
// (D2309, review F23): a catalog image bakes the checkout of every repository
// its entry serves (D2301), and this run neither works on nor holds a grant
// over any but its own — so what is left under the image's baked root when the
// agent runs is this run's own directory alone.
//
// Mutation (run, restored): drop the removeOtherBakedRepos call from
// checkProjectCheckoutOrigin. The step below finds the other repositories'
// checkouts, and their source is a file of the run's.
func TestOtherBakedReposAreRemovedBeforeTheAgentStarts(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	b.deps.Local, b.deps.Project = false, "aurora"
	computed := projectHost(t, b)
	own := bakedProjectCheckout(t, b, computed, nil)
	other := bakedDir(t, b.harness, mustSlug("github", "evil/other"))
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, filepath.Join(other, "secret.txt"), "another repository's source\n")
	readable := ""
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		root := filepath.Dir(own)
		entries, err := os.ReadDir(root)
		if err != nil {
			return agent.Result{}, err
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		readable = strings.Join(names, " ")
		return implement("feature")(t, ctx, req)
	}
	if _, err := b.run(t, step, review("ship", 0)); err != nil {
		t.Fatalf("the run did not finish: %v", err)
	}
	if want := filepath.Base(own); readable != want {
		t.Errorf("the baked root held %q when the agent ran, want only this run's own %q", readable, want)
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("the other repository's baked checkout is still there after the run")
	}
}

// The provider of a project job is the one its own name pins, and the
// environment may only tighten: FUGARO_GIT_PROVIDER (the --provider flag's
// default) that agrees with the job changes nothing — it is not the source
// either way, which the clone URL proves: the run clones github's URL for its
// repository, and the only repository the fixture's host tree holds is that
// one.
//
// Mutation (run, restored): take the provider from Deps.ProviderKind when it
// is set (the pre-3.3 shape). The agreeing case still passes — the value is the
// same — which is why the disagreement case below is the one that carries the
// property.
func TestProjectJobIgnoresTheProviderEnvVar(t *testing.T) {
	policy := projectPolicyFor(`["github/acme/*"]`, "")
	t.Run("unset", func(t *testing.T) {
		b := projectRun(t, projectJobOf(), "off", "", policy)
		projectHost(t, b)
		projectSucceeded(t, b)
	})
	t.Run("the job's own provider", func(t *testing.T) {
		b := projectRun(t, projectJobOf(), "off", "", policy)
		b.deps.ProviderKind = "github"
		projectHost(t, b)
		projectSucceeded(t, b)
	})
}

// A launcher's provider claim that disagrees with the job is refused, and it is
// the only reachable form of "another provider" on a project job: the provider
// the run acts as comes from the matched candidate — the profile's own — and
// the slug binding has already pinned the repository to that provider, so the
// canonical repository the admission is asked about is always one the profile
// serves (projectpolicy.Admits' provider gate is that package's own test).
// What a launcher can write is this variable, and the run stops rather than
// acting as the bitbucket job the flag claims while its own name says github —
// naming the job, the provider its name pins and the value it was handed.
//
// Mutation (run, restored): ignore a disagreeing Deps.ProviderKind. The run
// opens a provider the job's name does not name, and this test fails on the
// refusal.
func TestProjectJobOfAnotherProviderIsRefused(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	b.deps.ProviderKind = "bitbucket"
	projectHost(t, b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err,
		"bitbucket", "github", runner.GitProviderEnv, "project job",
		gcp.ProjectJobName("standard", "github", ""), "may only tighten")
}

// The profile's own allow_repos is the list a shared job's admission adds to
// the project's (design §4.1, plan task 3.3 step 4): a repository the profile
// does not admit is refused on that profile, with the profile's list named, and
// it is refused before any git credential exists and before any lease — so a
// repository's own fugaro.yaml cannot widen its access by selecting a wider
// profile, and the launch check stays advisory.
//
// Mutation (run, restored): drop the profile half and check the project's list
// alone (AdmitRepo(p.AllowRepos, ...)). The first subtest's repository is on the
// project's list, so only the profile's refusal stops it.
func TestRunnerRefusesSlugNotInProfileAllowList(t *testing.T) {
	t.Run("the profile's own list", func(t *testing.T) {
		b := projectRun(t, projectJobOf(), "enforce", "50", projectPolicyFor(`["github/acme/*"]`, `["github/other/*"]`))
		projectHost(t, b)
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "github/acme/app", "standard", "github/other/*", "never widen")
	})
	t.Run("the project's list still gates a project job", func(t *testing.T) {
		b := projectRun(t, projectJobOf(), "enforce", "50", projectPolicyFor(`["github/other/*"]`, ""))
		projectHost(t, b)
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "github/acme/app", "allow_repos", "github/other/*")
	})
}

// The slug binding on a project job (plan task 3.3 step 2): the slug the run
// was launched for keys its bucket paths, its locks, its cache and its budget
// session, and a shared job serves every repository its profile admits — so
// nothing but this comparison ties the slug to the repository task.json names.
// A run launched for one repository whose task names another is refused before
// any git credential exists and before any lease.
//
// The policy admits the repository task.json names, not the one the slug names,
// which is the point: the admission is asked about the *task's* repository, so
// the refusal here is the binding's own.
//
// Mutation (run, restored): trust task.json's repository for the bucket paths
// (skip the comparison). The run writes another repository's lease and locks
// under a slug that is not its own.
func TestProjectJobBindsTheSlugToTheJobsRepository(t *testing.T) {
	b := projectRun(t, projectJob{profile: "standard", provider: "github", slugRepo: "acme/other", specRepo: "acme/app"},
		"off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	projectHost(t, b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, b.deps.Store.Slug(), "acme/app", "project job",
		gcp.ProjectJobName("standard", "github", ""), "must be the slug task.Slug(github, acme/app)")
}

// projectPolicyServing is projectPolicyFor with the standard profile serving
// one Claude account, whose own list is the fixture's repository: the pair a
// published node carries for a profile that lists an account. The profile
// writes its own allow_repos exactly — the subset rule requires it, and
// requires every account it lists to cover it — so the two lists here are the
// same one repository.
func projectPolicyServing(account string) string {
	return `{"version":9,"allow_repos":["github/acme/app"],"profiles":{"standard":{"provider":"github",` +
		`"claude_accounts":["` + account + `"],"allow_repos":["github/acme/app"],` + profileShape + `}},` +
		`"accounts":{"` + account + `":{"allow_repos":["github/acme/app"]}}}`
}

// refusedBeforeTheAgent fails unless the run stopped at the runner's own
// credential check: the run failed (an infra error, so the job shows failed
// and the reason is published in its record) carrying every want, with no
// stage run, no lease reserved, no lock taken and no outcome written.
//
// It is refusedAdmission's counterpart for a refusal that must happen AFTER
// the configuration is read, and it therefore asserts nothing about the git
// credential count: which credential a run is, is a fact of the repository's
// own fugaro.yaml (agent.auth), which exists only once the checkout is there.
// What this check must come before is the agent, the lease and the budget, and
// that is what the assertions below are.
func refusedBeforeTheAgent(t *testing.T, b *bk, rec *runstore.Record, err error, want ...string) {
	t.Helper()
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v: the run was not refused", rec, err)
	}
	for _, w := range want {
		if !strings.Contains(rec.Reason, w) {
			t.Errorf("reason %q lacks %q", rec.Reason, w)
		}
	}
	if strings.Contains(rec.Reason, "\n") {
		t.Errorf("the reason is not one line: %q", rec.Reason)
	}
	if len(b.agent.calls) != 0 {
		t.Errorf("%d stages ran after the refusal", len(b.agent.calls))
	}
	slug := b.deps.Store.Slug()
	if v := b.db.Value(budget.PathRun(slug, runID) + "/reserved"); v != nil {
		t.Errorf("the refused run reserved %v in its ledger", v)
	}
	if ok, lerr := b.bucket.Exists(context.Background(), lock.Key(slug, "fugaro/"+runID)); lerr != nil || ok {
		t.Errorf("the refused run took the branch lock (ok = %v, err = %v)", ok, lerr)
	}
	if v := b.db.Value(budget.PathOutcome(b.day(), slug, runID)); v != nil {
		t.Errorf("the refused run wrote an outcome: %v", v)
	}
}

// A run that is not an OAuth run may only be in the profile's no-account job
// (plan task 3, design §4.4 row 8). This is the end-to-end half of the
// runner's own check: the run is launched onto the job of the account
// acme-personal — whose container the platform hands that account's token to,
// for every run of it — while the repository's own fugaro.yaml resolves to
// agent.auth: api-key. The launcher's gate never selects an account job for a
// run that is not an OAuth one (design §4.4 row 7), so this state is one it
// does not produce; what this test runs is Runner.Run itself, with no launcher
// anywhere, which is the independence the design asks for: a launcher is
// trusted, so a launch check alone is not a boundary.
//
// Mutation (run, restored): return nil from checkProjectJobAccount without
// looking (or drop its call in runner.go). The run goes on in a job holding a
// credential its own configuration never chose: it reaches its provider,
// reserves a lease and its first stage runs, and every assertion here fails.
func TestRunnerRefusesNonOAuthOnAccountJob(t *testing.T) {
	const account = "acme-personal"
	job := projectJob{profile: projectpolicy.DefaultProfileName, provider: "github", account: account, slugRepo: "acme/app"}
	b := projectRun(t, job, "off", "", projectPolicyServing(account))
	projectHost(t, b)
	rec, err := b.run(t)
	refusedBeforeTheAgent(t, b, rec, err,
		gcp.ProjectJobName(job.profile, job.provider, account), `"`+account+`"`, config.ClaudeAccountSecret(account),
		`"api-key"`, "CLAUDE_CODE_OAUTH_TOKEN", "The run stops")
}

// TestReviewVariantIsNotTheCodingJob (CSR8): the end-to-end half of the class
// check, and the pair of TestReviewClassIsDecidedByTheMarkerAndTheJob, the
// other direction of the same check (a review run in the shared coding job).
// A run that is not a review — no launcher marker, no pull request in
// its task — sitting in the review variant's job is refused at the runner's own
// admission, before any git credential is requested, before anything is cloned
// and before any lease: the class is decided by the launcher's marker and the
// job the run is in, and never by the job's name, so a container cannot talk
// itself into the identity of a job nobody launched it into.
//
// Mutation (run, restored): match the variant's name without asking the run's
// class. This run is admitted, clones with the profile's credential and runs
// its first stage in a container built for a review.
func TestReviewVariantIsNotTheCodingJob(t *testing.T) {
	const account = "acme-personal"
	job := projectJob{profile: projectpolicy.DefaultProfileName, provider: "github",
		account: account + config.ReviewVariantSuffix, slugRepo: "acme/app"}
	b := projectRun(t, job, "off", "", projectPolicyServing(account))
	projectHost(t, b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err,
		gcp.ProjectJobName(job.profile, job.provider, job.account),
		"review-only variant", "neither a review nor a diagnosis", "The run stops")
}

// TestProjectJobDefaultProfileIsTheProvidersOwn is D2310 and R8's rule as this
// package answers it: the profile a workflow that names no job profile runs on
// is the default profile of its *repository's own* provider — `standard` for
// the project's first provider, and `standard-<provider>` for another — and
// every job name a launcher computes from that answer is a name this package's
// candidate set places, on the same (profile, provider, account) triple.
//
// It is the runner's half of the agreement C1 requires (contract.md: the
// launcher and the runner's candidate logic "must agree name for name"), and
// its other half is internal/cli's TestUnnamedWorkflowResolvesTheProfileOfItsProvider
// and TestRunnerCandidatesMatchTheLauncher, which drive the launcher itself.
// The rule lives here rather than in internal/cli because this package is
// where a job name is computed from the published policy at run time: the
// launcher reads it (internal/cli's launchProjectJobFor) and a second copy of
// it on either side is one that can drift.
//
// The account loop is not decoration. The account segment of the job's name is
// the axis config.ProjectJobAccounts derives for the profile, and a launcher
// that derived it for itself — or read it off the profile's secrets, as the
// runner once did — would compute the name of a job the deployer never built:
// a 404 from Cloud Run after the claim and the budget token were spent. Every
// account of the profile's axis is therefore placed here, not one of them.
func TestProjectJobDefaultProfileIsTheProvidersOwn(t *testing.T) {
	const (
		bitbucketAndGitHub = `{"version":9,"allow_repos":["github/acme/*","bitbucket/acme/*"],` +
			`"profiles":{"standard":{"provider":"github",` + profileShape + `},` +
			`"standard-bitbucket":{"provider":"bitbucket",` + profileShape + `}}}`
		bitbucketFirst = `{"version":9,"allow_repos":["github/acme/*","bitbucket/acme/*"],` +
			`"profiles":{"standard":{"provider":"bitbucket",` + profileShape + `},` +
			`"standard-github":{"provider":"github",` + profileShape + `}}}`
	)
	for _, tc := range []struct {
		name     string
		policy   string
		provider string
		want     string
	}{
		{"the project's default serves the repository's provider", projectPolicyFor(`["github/acme/*"]`, ""), "github", projectpolicy.DefaultProfileName},
		{"another provider resolves to standard-<provider>", bitbucketAndGitHub, "bitbucket", projectpolicy.DefaultProfileName + "-bitbucket"},
		{"the first provider's repository keeps the project's default", bitbucketFirst, "bitbucket", projectpolicy.DefaultProfileName},
		{"the second provider's resolves to its own", bitbucketFirst, "github", projectpolicy.DefaultProfileName + "-github"},
		{"a provider the policy serves no default for", projectPolicyFor(`["github/acme/*"]`, ""), "bitbucket", ""},
		{"a name that lies about its provider", `{"version":9,"allow_repos":["github/acme/*","bitbucket/acme/*"],` +
			`"profiles":{"standard":{"provider":"github",` + profileShape + `},` +
			`"standard-bitbucket":{"provider":"github",` + profileShape + `}}}`, "bitbucket", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := projectpolicy.Decode([]byte(tc.policy))
			if err != nil || !found || p == nil {
				t.Fatalf("the fixture's policy node does not decode: %v", err)
			}
			got := runner.ProjectJobDefaultProfile(p, tc.provider)
			if got != tc.want {
				t.Fatalf("the default profile of a repository on %s is %q, want %q", tc.provider, got, tc.want)
			}
			if got == "" {
				// The choice of no profile: the launch keeps the per-repository
				// job, which is the same answer every launch of a project with
				// no profile for the provider has always had.
				return
			}
			pr, err := p.ProfileFor(got)
			if err != nil {
				t.Fatalf("the resolved profile is not one the policy defines: %v", err)
			}
			if pr.Provider != tc.provider {
				t.Fatalf("the resolved profile %q serves %s, and the repository is on %s: a launch on it would run the repository's work on another provider's job", got, pr.Provider, tc.provider)
			}
			for _, account := range config.ProjectJobAccounts(pr.JobProfile) {
				name := gcp.ProjectJobName(got, pr.Provider, account)
				profile, provider, from, ok := runner.ProjectJobOfPolicy(p, name)
				if !ok || profile != got || provider != pr.Provider || from != account {
					t.Fatalf("the candidate set places the name %s as (%s, %s, %s, %v), and the launcher computes (%s, %s, %s)", name, profile, provider, from, ok, got, pr.Provider, account)
				}
			}
		})
	}
	if got := runner.ProjectJobDefaultProfile(nil, "github"); got != "" {
		t.Fatalf("a project that published no policy resolves the default profile %q: a project with no profile has no job to run one on", got)
	}
	if got := runner.ProjectJobDefaultProfile(nil, ""); got != "" {
		t.Fatalf("a provider no repository is on resolves the default profile %q", got)
	}
}
