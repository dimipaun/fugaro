package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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
	if job.specRepo == "" {
		job.specRepo = job.slugRepo
	}
	b := newBK(t, gwConfig(t, ""), mode, capUSD)
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
		spec := &task.Spec{Version: 1, RunID: runID, Repo: job.specRepo, Ref: "main",
			Workflow: "app", Task: "Add a feature"}
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
	return `{"version":3,"allow_repos":` + project + `,"profiles":{"standard":{"provider":"github"` +
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
	return `{"version":3,"allow_repos":` + project + `,"clone_urls":{"github%2Facme%2Fapp":"` + url + `"},` +
		`"profiles":{"standard":{"provider":"github",` + profileShape + `}}}`
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

// A checkout already in the work directory is opened as it is (OpenOrClone),
// and on a per-repository job its origin is held to the task's repository by
// the admission (remoteOfAnotherRepo reads it through originURL). A project
// job reads neither (review C5): its clone URL is computed, so a checkout a
// project job's container somehow carried — a baked image built for another
// repository, or a work directory something left — would be fetched with the
// profile's credential and worked on under this run's slug. Project jobs run
// the installation's base image, which bakes no checkout, so any checkout is
// refused before any git credential — including one whose origin is the URL
// the run computes, since its .git/config and hooks are not the run's.
//
// Mutation (run, restored): drop the checkProjectCheckoutOrigin call in
// bootstrap. Both rows then open the planted checkout and ask for a credential.
func TestProjectJobRefusesACheckoutItDidNotMake(t *testing.T) {
	for name, origin := range map[string]func(computed string) string{
		"another repository": func(string) string { return "https://github.com/evil/other.git" },
		"its own URL":        func(computed string) string { return computed },
	} {
		t.Run(name, func(t *testing.T) {
			b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
			computed := projectHost(t, b)
			for _, args := range [][]string{{"init", "-q", b.deps.WorkDir}, {"-C", b.deps.WorkDir, "remote", "add", "origin", origin(computed)}} {
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "already holds a checkout", computed, "before any git credential")
		})
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
	return `{"version":3,"allow_repos":["github/acme/app"],"profiles":{"standard":{"provider":"github",` +
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
