package cli

// shared_job_e2e_test.go is I1's own half of docs/shared-jobs-default: the two
// halves of a shared project job held together, for both providers.
//
// The deployer is internal/infra's (S1: one job per (profile, provider,
// account), the provider deciding whether a profile gets a job at all) and the
// run is internal/runner's (S3: a project job's name is the run's identity, the
// provider is the one that name pins, and a Bitbucket job reaches git and the
// API through the installation's credentials service and its own loopback
// forwarder). Neither package may import the other; this one imports both, so
// this is the only place the two can be held to each other's facts.
//
// What each case drives, in order: the installation's local config, through
// infra.ProjectJobs, to the deployed job — its name and its own environment —
// then runner.Run with that name as the execution the platform reports and that
// environment as the job's. The run is the shipped one: the clone it computes
// for itself, the implement stage's commit and verify, the push, and the pull
// request it opens. GitHub's provider is the tree's own fake (the seam every
// project-job test in internal/runner uses); Bitbucket's is the real proxied
// adapter, reached through the run's own forwarder, with a stand-in
// installation besides it on loopback.
//
// The job half of each case is the deployer's answer and not a computed name: a
// profile of a provider no shared job serves is skipped by the deployer (S1's
// one-line list), so a run naming a job the deployer never built is a run in a
// job that does not exist, and asking the deployer is what says it does.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

const (
	// sharedJobE2ERunID is the run every case here drives.
	sharedJobE2ERunID = "20261117-060000-e2e1"
	// sharedJobE2EGCPProject and sharedJobE2ERegion are the project and region
	// the fixture installation's jobs are in, and the two the platform's
	// report of an execution names.
	sharedJobE2EGCPProject = "proj-1234"
	sharedJobE2ERegion     = "us-east5"
	// sharedJobE2EAPIKey is the fixture's Firebase web API key. It is a shape
	// (a Firebase web API key) and not a credential: the fake Identity Toolkit
	// is built with it.
	sharedJobE2EAPIKey = "AIzaSyFakeWebApiKeyForTheSharedJobE2E00"
	// sharedJobE2ESigner is the service account the launch's budget token is
	// minted as: the installation's token signer, whose key the fake IAM holds.
	sharedJobE2ESigner = "fugaro-token-signer@" + sharedJobE2EGCPProject + ".iam.gserviceaccount.com"
	// sharedJobE2EOwner is the requester the run's identity names.
	sharedJobE2EOwner = "owner@example.invalid"
	// sharedJobE2EGithubRepo and sharedJobE2EBitbucketRepo are the two
	// repositories the fixture installation enrolls, one per provider: a
	// project that mixes providers is what gives it one default profile per
	// provider (D2310, R8), and the two are what let one config deploy a shared
	// job of each kind.
	sharedJobE2EGithubRepo    = "acme/app"
	sharedJobE2EBitbucketRepo = "acme/widget"
	// sharedJobE2EServiceURL is the address the fixture's local config records
	// for the credentials service. It is the installation's record and not a
	// reachable host: the Bitbucket case replaces it with the stand-in beside
	// it on loopback, exactly as a platform supplies the real one.
	sharedJobE2EServiceURL = "https://fugaro-credentials-123456789012." + sharedJobE2ERegion + ".run.app"
)

// sharedJobE2EConfig is the installation the fixture deploys from: two
// repositories, one per provider, the default profile of each (the project's
// first provider's is `standard`, another's `standard-<provider>`, D2310/R8),
// the credentials service recorded, and the containers those profiles mount.
func sharedJobE2EConfig() string {
	return `version: 1
name: aurora
gcp_project: ` + sharedJobE2EGCPProject + `
region: ` + sharedJobE2ERegion + `
runs_bucket: fugaro-runs-` + sharedJobE2EGCPProject + `
registry: ` + sharedJobE2ERegion + `-docker.pkg.dev/` + sharedJobE2EGCPProject + `/fugaro
build: { service_account: fugaro-build@` + sharedJobE2EGCPProject + `.iam.gserviceaccount.com }
user: test@example.com
repos:
  ` + sharedJobE2EGithubRepo + `: { provider: github, base_branch: main, workflows: [app], github_app_id: "123456789" }
  ` + sharedJobE2EBitbucketRepo + `: { provider: bitbucket, base_branch: main, workflows: [app] }
registry_host: ` + sharedJobE2ERegion + `-docker.pkg.dev/` + sharedJobE2EGCPProject + `
base_images: {base: ` + sharedJobE2ERegion + `-docker.pkg.dev/` + sharedJobE2EGCPProject + `/fugaro-base/fugaro-base:dev-0123abc}
credentials_service: { url: ` + sharedJobE2EServiceURL + ` }
project_secrets:
  ` + config.AnthropicSecret + `: { allow_data_to: [` + sharedJobE2EGithubRepo + `, ` + sharedJobE2EBitbucketRepo + `] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
  standard-bitbucket: { provider: bitbucket, cpu: 4, memory: 16Gi, timeout_class: 12h }
`
}

// sharedJobE2EPolicy is the node the fixture installation has published: one
// profile per provider, each admitting its own repository, the project admitting
// both, and the bitbucket_token block turned on with the proxy for the
// repository the Bitbucket profile serves — the block a shared Bitbucket run
// reaches git through (S6's switch, read back as the block's proxy member).
//
// githubRepos and bitbucketRepos are the two profiles' own allow_repos, written
// as the JSON arrays an operator's node carries; the project's list is their
// union, so a case that wants a repository refused narrows a profile and the
// project with it.
func sharedJobE2EPolicy(githubRepos, bitbucketRepos string) string {
	project := "[" + strings.Trim(githubRepos, "[]") + "," + strings.Trim(bitbucketRepos, "[]") + "]"
	// The policy's own budget block: a mode a shared job needs. A job that
	// carries the credentials service's address has every one of its
	// repositories on the switch, and a repository on the switch reaches the
	// service through the gateway, which only observe or enforce starts
	// (D2071): with the mode off the run halts at bootstrap saying exactly
	// that, and there is no credential of its own to fall back to (D143).
	return `{"version":` + strconv.Itoa(projectpolicy.Version) + `,"allow_repos":` + project + `,` +
		`"budget":{"mode":"observe","per_run_usd":20},` +
		`"bitbucket_token":{"proxy":true,"allow_repos":` + bitbucketRepos + `},` +
		`"profiles":{` +
		`"standard":{"provider":"github","allow_repos":` + githubRepos + `,` +
		`"cpu":2,"memory":"8Gi","timeout_class":"6h"},` +
		`"standard-bitbucket":{"provider":"bitbucket","allow_repos":` + bitbucketRepos + `,` +
		`"cpu":4,"memory":"16Gi","timeout_class":"12h"}}}`
}

// sharedJobE2E is one installation with its jobs deployed and one run ready to
// happen in one of them.
type sharedJobE2E struct {
	t *testing.T
	// Job is the shared job the run is in, as the deployer named it, and env is
	// that job's own environment plus what a platform supplies: what the
	// process in the container is handed.
	Job string
	env []string
	// provider and repo are the run's: the kind its job serves and the
	// repository its task names.
	provider string
	repo     string
	slug     string
	// remote is the bare repository the run's git lands on.
	remote string
	// opener is the git provider the run's pull request is opened through: the
	// tree's fake for GitHub. A Bitbucket run opens the adapter the runner
	// builds itself (Deps.OpenProvider stays nil), so this is nil there.
	opener *fake.Provider
	// service is the credentials service stand-in a Bitbucket run reaches git
	// and the API through, and meta its metadata server. Both nil for GitHub.
	service *sharedJobE2EService
	meta    *httptest.Server

	// layer is the project layer the task carries: the fixture file's own
	// lifted keys, which a repository's file may no longer decide (S1, D3237).
	layer      string
	job        infra.ProjectJob
	store      *runstore.Store
	bucket     *blobx.Bucket
	workDir    string
	stateDir   string
	managedDir string
	logs       *lockedSlogBuf
	policy     string
	sealKey    []byte
	db         *gcpfake.RTDB
	iam        *gcpfake.IAMCredentials
	itk        *gcpfake.IdentityToolkit
	agentCall  []agent.Request
}

// sharedJobE2EUp brings one installation up: it parses the fixture's local
// config, asks the deployer for the jobs, and takes the one the run is to be in
// out of what it built.
func sharedJobE2EUp(t *testing.T, provider, repo, policy string) *sharedJobE2E {
	t.Helper()
	testutil.IsolateGit(t)
	ctx := context.Background()
	lc, err := localcfg.Parse([]byte(sharedJobE2EConfig()))
	if err != nil {
		t.Fatalf("the fixture's local config is not one the deployer parses: %v", err)
	}
	jobs, err := infra.ProjectJobs(lc, lc.RunsBucket, false)
	if err != nil {
		t.Fatalf("the deployer refused the fixture's installation: %v", err)
	}
	profile := projectpolicy.DefaultProfileNameFor(provider, provider == gitprov.KindGitHub)
	job, ok := jobs[gcp.ProjectJobName(profile, provider, "")]
	if !ok {
		t.Fatalf("the installation deploys no shared job for a %s profile (it deploys %v): a run in a shared job of that provider has no job to be in",
			provider, sharedJobE2EJobNames(jobs))
	}
	if _, mounted := job.SecretEnv[config.BitbucketSecret]; mounted {
		t.Errorf("the shared %s job mounts a Bitbucket token: a shared job holds no Bitbucket credential at all (D2302)", provider)
	}

	e := &sharedJobE2E{t: t, Job: job.Job, provider: provider, repo: repo, policy: policy,
		logs: &lockedSlogBuf{}}
	if e.slug, err = task.Slug(provider, repo); err != nil {
		t.Fatal(err)
	}
	if provider == gitprov.KindGitHub {
		e.opener = &fake.Provider{Repo: repo}
	}
	_, e.layer = sharedJobE2EWorkflowConfig(t, provider)
	e.publishInstallation(t, ctx, lc)
	e.mintRun(t, ctx)
	e.job = job
	return e
}

// sharedJobE2EJobNames is the deployed jobs' names, for a failure message.
func sharedJobE2EJobNames(jobs map[string]infra.ProjectJob) []string {
	out := make([]string, 0, len(jobs))
	for name := range jobs {
		out = append(out, name)
	}
	return out
}

// publishInstallation is what `fugaro update` leaves behind on the database
// side: the project's name, the rules marker the publish writes, and the policy
// node itself. The database is the fixture's (gcpfake's Realtime Database) and
// the read the run makes is the shipped one.
func (e *sharedJobE2E) publishInstallation(t *testing.T, ctx context.Context, lc *localcfg.Config) {
	t.Helper()
	e.db = gcpfake.NewRTDB(t)
	e.iam = gcpfake.NewIAMCredentials(t)
	e.iam.AddSigner(sharedJobE2ESigner)
	e.itk = gcpfake.NewIdentityToolkit(t, e.iam, sharedJobE2EAPIKey, sharedJobE2EGCPProject)
	e.db.Set("fugaro/project", lc.Name)
	e.db.Set(budget.PathMode, "observe")
	micros := func(v budget.Micros) *budget.Micros { return &v }
	e.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: micros(5_000_000)})
	e.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: micros(150_000_000), PerRunMicros: micros(20_000_000)})
	e.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	e.db.Set(budget.PathCapsRepo(e.slug), budget.RepoCaps{
		DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: e.repo})
	e.publishPolicy(t, e.policy)
}

// publishPolicy is the installation's policy as it stands now: a case that
// wants a repository refused publishes another node before it runs.
func (e *sharedJobE2E) publishPolicy(t *testing.T, policy string) {
	t.Helper()
	e.policy = policy
	e.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
}

// mintRun writes the run the launch left ready: the task in the runs bucket and
// the launcher's budget token sealed under its own key, which is what a job
// whose project has published a policy is started with, plus the branch claim a
// coding run's identity carries (G6: the credentials service admits a push only
// for the branch the identity names).
func (e *sharedJobE2E) mintRun(t *testing.T, ctx context.Context) {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	e.bucket = blobx.Wrap(bucket)
	e.store = runstore.Open(bucket, e.slug, sharedJobE2ERunID)
	spec := &task.Spec{Version: 1, RunID: sharedJobE2ERunID, Repo: e.repo, Ref: "main",
		Workflow: "app", Task: "Add a feature", RequestedBy: sharedJobE2EOwner,
		ProjectLayer: &task.ProjectLayer{YAML: e.layer, SHA256: config.LayerSum([]byte(e.layer))}}
	if err := e.store.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	e.sealKey = make([]byte, token.KeySize)
	rand.New(rand.NewSource(0x5a)).Read(e.sealKey)
	signer, err := token.NewIAMSigner(sharedJobE2ESigner,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(e.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	custom, err := token.MintAt(ctx, signer, token.Claims{
		Slug: e.slug, Run: sharedJobE2ERunID, FX: token.ExpiryFX(now, 3*time.Hour),
		FP: "aurora", RB: sharedJobE2EOwner, Branch: "fugaro/" + sharedJobE2ERunID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(ctx, e.bucket, e.slug, sharedJobE2ERunID, custom, e.sealKey); err != nil {
		t.Fatal(err)
	}
}

// runEnv is the environment the run is handed: the deployed job's own, which is
// what a real execution's container carries, plus the things a platform
// supplies and a test supplies instead — a PATH and a HOME, the model key the
// agent would be given, the metadata server the forwarder reads the job's
// identity from, and, for a Bitbucket job, the installation's credentials
// service.
//
// The service's address is the one value of the job's environment a test moves:
// the deployed job names the installation's own service, and the stand-in
// beside it on loopback takes that place, as a platform supplies the real one.
func (e *sharedJobE2E) runEnv(t *testing.T) []string {
	t.Helper()
	if e.env != nil {
		return e.env
	}
	env := make([]string, 0, len(e.job.Env)+12)
	for _, k := range sortedKeys(e.job.Env) {
		if k == providers.EnvCredentialsServiceURL {
			continue
		}
		env = append(env, k+"="+e.job.Env[k])
	}
	env = append(env,
		"PATH="+os.Getenv("PATH"),
		"HOME="+t.TempDir(),
		"ANTHROPIC_API_KEY=test-key",
	)
	if e.provider == gitprov.KindBitbucket {
		if e.service == nil {
			e.service = newSharedJobE2EService(t, e.Job, e.remote)
		}
		e.meta = sharedJobE2EMetadata(t)
		env = append(env,
			providers.EnvCredentialsServiceURL+"="+e.service.srv.URL,
			"GCE_METADATA_HOST="+strings.TrimPrefix(e.meta.URL, "http://"),
		)
	}
	e.env = env
	return env
}

// deps is the run's dependencies: the deployed job's name as the platform's own
// report of the execution, the fixture's database and identity exchange as the
// project's backend, and — for GitHub — the provider its pull request is opened
// through.
func (e *sharedJobE2E) deps(t *testing.T) runner.Deps {
	t.Helper()
	managed := e.layout(t)
	var open gitprov.Opener
	if e.opener != nil {
		open = gitprov.Static(e.opener)
	}
	return runner.Deps{
		Store: e.store, OpenProvider: open, Agent: &sharedJobE2EAgent{e: e},
		WorkDir: e.workDir, StateDir: e.stateDir, Env: e.runEnv(t),
		// A remote that does not exist: a project job holds no per-repository
		// remote, so a run that read one instead of computing its clone URL
		// fails here rather than cloning something nobody named (the fixture
		// internal/runner's own project-job runs use).
		Remote: filepath.Join(filepath.Dir(e.workDir), "no-such-remote"),
		Log:    slog.New(slog.NewTextHandler(e.logs, nil)),
		// A run in the cloud, in the job the fixture's deployer built.
		Execution: "projects/" + sharedJobE2EGCPProject + "/locations/" + sharedJobE2ERegion +
			"/jobs/" + e.Job + "/executions/" + e.Job + "-aaaaa",
		Project: "aurora", Bucket: e.bucket,
		Backend: runner.Backend{
			RTDBURL: e.db.URL, APIKey: sharedJobE2EAPIKey,
			TokenMarked: true, TokenKey: e.sealKey,
			Tune: func(c *budget.Config) {
				c.IdentityURL, c.SecureTokenURL = e.itk.URL, e.itk.URL
				c.HeartbeatEvery, c.RetryEvery, c.CapRetryWait = 30*time.Millisecond, 20*time.Millisecond, 30*time.Millisecond
				c.StaleBackoff, c.KillRestart = 2*time.Millisecond, 100*time.Millisecond
				c.StreamBackoffMin, c.StreamBackoffMax = 10*time.Millisecond, 50*time.Millisecond
				if c.Grace == 0 || c.Grace == budget.DefaultGrace {
					c.Grace = 600 * time.Millisecond
				}
			},
		},
		Spend:               e.spend(t),
		CancelPoll:          20 * time.Millisecond,
		ManagedSettingsPath: managed,
	}
}

// layout is the run's own directories, made once per case: the work directory
// the run clones into, its state directory, and the directory the managed
// settings are written to. A case that needs the work directory before the run
// starts (a baked checkout) asks for them too.
func (e *sharedJobE2E) layout(t *testing.T) string {
	t.Helper()
	if e.workDir == "" {
		tmp := t.TempDir()
		e.workDir = filepath.Join(tmp, "work")
		e.stateDir = filepath.Join(tmp, "state")
		e.managedDir = filepath.Join(tmp, "claude-code")
	}
	if err := os.MkdirAll(e.managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(e.managedDir, "managed-settings.json")
}

// spend is the run's own budget mode and cap, as the launcher's environment
// carries them (runner.SpendFromEnv): observe, which is the mode the published
// policy names and the weakest one a repository on the credentials service's
// switch may run under.
func (e *sharedJobE2E) spend(t *testing.T) runner.Spend {
	t.Helper()
	env := map[string]string{runner.BudgetModeEnv: "observe", runner.MaxRunUSDEnv: "20"}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return spend
}

// run drives a whole run and returns the record it wrote.
func (e *sharedJobE2E) run(t *testing.T, steps ...sharedJobE2EStep) (*runstore.Record, error) {
	t.Helper()
	deps := e.deps(t)
	ag := deps.Agent.(*sharedJobE2EAgent)
	ag.steps = steps
	return runner.Run(context.Background(), deps)
}

// sharedJobE2EStep is one stage's scripted agent call.
type sharedJobE2EStep func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error)

// sharedJobE2EAgent answers the run's stages with the steps a case gave, and
// keeps every request, which is where a case reads what the run handed a stage.
type sharedJobE2EAgent struct {
	e     *sharedJobE2E
	steps []sharedJobE2EStep
}

func (a *sharedJobE2EAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.e.agentCall = append(a.e.agentCall, req)
	i := len(a.e.agentCall) - 1
	if i >= len(a.steps) {
		a.e.t.Errorf("unexpected agent call #%d", i+1)
		return agent.Result{}, errors.New("no step for this agent call")
	}
	return a.steps[i](a.e.t, ctx, req)
}

// implement is the implement stage's work: a commit, the workflow's test
// command through the run's own verify, and the pull request body the stage
// writes. It is internal/runner's fixture step, spelled here because the two
// packages' tests cannot share one.
func implement(name string) sharedJobE2EStep {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		t.Helper()
		shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'Add "+name+"'")
		if _, err := verify.Run(ctx, verify.Options{
			StateDir: envValue(req.Env, "FUGARO_STATE_DIR"), Kind: verify.KindTest,
			Env: req.Env, Stdout: io.Discard, Stderr: io.Discard,
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md"),
			[]byte("# Add "+name+"\n\nAdds "+name+".txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
}

// review is a review stage's clean answer.
func review(verdict string, findings int) sharedJobE2EStep {
	return func(t *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
		t.Helper()
		fs := make([]map[string]string, findings)
		for i := range fs {
			fs[i] = map[string]string{"summary": "fix it"}
		}
		b, err := json.Marshal(map[string]any{"verdict": verdict, "findings": fs})
		if err != nil {
			t.Fatal(err)
		}
		return agent.Result{Structured: b, CostUSD: 0.5}, nil
	}
}

// shell runs script in the stage's own checkout, as the agent's commands run.
func shell(t *testing.T, req agent.Request, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
}

// lockedSlogBuf is a log sink the run's goroutines may write to.
type lockedSlogBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedSlogBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedSlogBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// sharedJobE2EHost makes the fixture's git host local, so a run whose clone URL
// is an https URL can clone it without reaching the network: the fixture's bare
// repository is moved to the path the rewrite resolves the run's own URL to, and
// every other URL on either provider's host is rewritten into a directory that
// is not a repository. A run that cloned anything else fails locally instead of
// making a network call, which is the guard this fixture exists to have.
//
// GitHub's URL is one entry of its own beside the host's (git uses the longest
// matching prefix, so the run's own repository resolves and nothing else on the
// host does); a Bitbucket run needs no rewrite at all, because its clone is
// rewritten to its own execution's forwarder by the run itself.
func (e *sharedJobE2E) host(t *testing.T) string {
	t.Helper()
	files := testutil.FixtureFiles(t)
	file, _ := sharedJobE2EWorkflowConfig(t, e.provider)
	files["fugaro.yaml"] = file
	source := testutil.NewRemote(t, files)
	bare := filepath.Join(t.TempDir(), filepath.FromSlash(e.repo)+".git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, bare); err != nil {
		t.Fatal(err)
	}
	e.remote = bare
	if e.opener != nil {
		e.opener.Remote = bare
	}
	url, err := repourl.CloneURL(e.provider, e.repo, "")
	if err != nil {
		t.Fatal(err)
	}
	// The other provider's whole host goes nowhere, so a run that cloned
	// across providers fails locally rather than making a network call. This
	// run's own host is rewritten only for GitHub: a Bitbucket run's clone URL
	// is rewritten to its own execution's forwarder by the run's own git
	// environment, and a second rewrite of the same prefix in the global file
	// would be a second answer to the same question.
	absent := filepath.Join(t.TempDir(), "no-such-host")
	rewrites := map[string]string{}
	if e.provider == gitprov.KindGitHub {
		rewrites["https://bitbucket.org/"] = absent + "/"
		rewrites[url] = bare
	} else {
		rewrites["https://github.com/"] = absent + "/"
	}
	urlRewrite(t, rewrites)
	return bare
}

// sharedJobE2EWorkflowConfig is the fixture repository's config as the run
// reads it: the fixture's own, with the provider its job serves written in —
// the run refuses a config whose git.provider is not the kind its job's name
// pins — and with the workflow's secrets block dropped.
//
// The block names fixture-fails, which the fixture's test suite reads from an
// environment variable, and a shared job mounts no per-repository secret: a
// profile mounts project-wide containers (internal/infra's projectJobSecrets),
// and a container for the fixture's own suite is nothing this installation has.
// The suite tests the same three cases with the variable unset, which is the
// one thing the block was for.
func sharedJobE2EWorkflowConfig(t *testing.T, provider string) (file, layer string) {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	const block = `    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
`
	out := strings.Replace(cfg, block, "", 1)
	if out == cfg {
		t.Fatalf("the fixture repository's config no longer writes the secrets block this fixture drops:\n%s", cfg)
	}
	// The models a run under a budget must pin (config.CheckPins): the
	// published policy carries a budget mode, so the coder, the reviewer and
	// the background model each name a model ID the price table holds.
	out = strings.Replace(out, "  review_rounds: 2\n", `  review_rounds: 2
  model: claude-sonnet-5-5
  models: { coder: claude-sonnet-5-5, reviewer: claude-sonnet-5-5, background: claude-haiku-4-5 }
`, 1)
	out = strings.Replace(out, "provider: github", "provider: "+provider, 1)
	if !strings.Contains(out, "provider: "+provider) {
		t.Fatalf("the fixture repository's config no longer writes a github provider:\n%s", cfg)
	}
	// The pins are a repository's file's no longer (S1, D3237): config.Resolve
	// drops them before the merge, so a run seeded with this file alone names no
	// model and bootstrap refuses it. The task carries them as the project layer
	// instead — where an installation's arrive from — and the file is anchored
	// to the project that layer names, which is what makes it apply.
	return testutil.FixturePins(out, config.LiftedKeyPaths())
}

// urlRewrite writes a git global configuration whose url.<base>.insteadOf
// entries rewrite the given URL prefixes, and points GIT_CONFIG_GLOBAL at it.
// testutil.IsolateGit sends git at /dev/null; this is the run's own config, and
// every git call gitops makes inherits the process environment it is set in.
func urlRewrite(t *testing.T, rewrites map[string]string) {
	t.Helper()
	var cfg strings.Builder
	for prefix, base := range rewrites {
		cfg.WriteString("[url \"" + base + "\"]\n\tinsteadOf = " + prefix + "\n")
	}
	path := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(path, []byte(cfg.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", path)
}

// branch is the branch the run pushes: the fugaro branch of its own run id.
func (e *sharedJobE2E) branch() string { return "fugaro/" + sharedJobE2ERunID }

// TestSharedJobEndToEndGitHub is I1's GitHub half: a whole run in the shared job
// the installation deploys for its GitHub profile — the clone the run makes for
// itself, the push, and the pull request it opens — with a repository the policy
// does not admit refused, and a baked checkout accepted only after the reset
// S9 gives it.
//
// The run is in the job the deployer built and is handed that job's own
// environment: a job the deployer deploys and the runner cannot run in is the
// half of I1 no test in either package alone can see.
func TestSharedJobEndToEndGitHub(t *testing.T) {
	admitted := sharedJobE2EPolicy(`["github/`+sharedJobE2EGithubRepo+`"]`, `["bitbucket/`+sharedJobE2EBitbucketRepo+`"]`)

	t.Run("clone, push and open a pull request", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindGitHub, sharedJobE2EGithubRepo, admitted)
		e.host(t)
		rec, err := e.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("the run errored: %v\n%s", err, e.logs.String())
		}
		if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("record = %+v: a run the profile admits on a shared job ends ready\n%s", rec, e.logs.String())
		}
		e.assertClonedAndPushed(t, rec, "")
	})

	t.Run("a repository the policy does not admit is refused", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindGitHub, sharedJobE2EGithubRepo, admitted)
		e.host(t)
		// The profile of the job this run is in admits another repository: the
		// admission is the published list and never the run's own say-so.
		e.publishPolicy(t, sharedJobE2EPolicy(`["github/acme/elsewhere"]`, `["bitbucket/`+sharedJobE2EBitbucketRepo+`"]`))
		rec, err := e.run(t, implement("feature"))
		if err == nil {
			t.Fatalf("the run was admitted: %+v\n%s", rec, e.logs.String())
		}
		if !strings.Contains(err.Error(), "github/"+sharedJobE2EGithubRepo) {
			t.Errorf("the refusal does not name the repository: %v", err)
		}
		if e.cloned() {
			t.Error("the run cloned a repository the policy does not admit")
		}
	})

	t.Run("a baked checkout is reset before the fetch", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindGitHub, sharedJobE2EGithubRepo, admitted)
		e.host(t)
		marker := e.bakeCheckout(t)
		rec, err := e.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("the run errored: %v\n%s", err, e.logs.String())
		}
		if rec.Status != runstore.StatusSucceeded {
			t.Fatalf("record = %+v: a baked checkout the run can reset is not a refusal\n%s", rec, e.logs.String())
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the hook the baked checkout carried ran: %v", err)
		}
		e.assertClonedAndPushed(t, rec, e.bakedDir(t))
	})
}

// TestSharedJobEndToEndBitbucket is I1's Bitbucket half, and the clause the
// switch is for: the same whole run in the shared job the installation deploys
// for its Bitbucket profile, where the job mounts no Bitbucket token at all
// (D2302) and every byte of the run's git and REST traffic reaches Bitbucket
// through the run's own loopback forwarder to the installation's credentials
// service and nowhere else.
//
// Nothing here stands in for the run's own half of that path: the forwarder is
// the shipped one, it mints the job's ID token from the metadata server the
// fixture serves, and the adapter that opens the pull request is the shipped
// proxied one. What stands in is the installation: the service's route answers
// the two halves a run's traffic has there (git, over the fixture's bare
// repository, and the REST calls the adapter makes), and it refuses anything
// that did not arrive under this job's identity, which is what says the
// forwarder — and not the run's git — put it there.
func TestSharedJobEndToEndBitbucket(t *testing.T) {
	admitted := sharedJobE2EPolicy(`["github/`+sharedJobE2EGithubRepo+`"]`, `["bitbucket/`+sharedJobE2EBitbucketRepo+`"]`)

	t.Run("clone, push and open a pull request", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindBitbucket, sharedJobE2EBitbucketRepo, admitted)
		e.host(t)
		rec, err := e.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("the run errored: %v\n%s", err, e.logs.String())
		}
		if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("record = %+v: a run the installed proxy serves ends ready\n%s", rec, e.logs.String())
		}
		e.assertClonedAndPushed(t, rec, "")
		// The run reached git and the API through its own forwarder: the
		// service saw the clone and the push, and then the pull request the
		// adapter created.
		if len(e.service.gitCalls) == 0 {
			t.Error("nothing of the run's git reached the credentials service")
		}
		if len(e.service.apiCalls) == 0 {
			t.Error("none of the run's Bitbucket API traffic reached the credentials service")
		}
		if len(e.service.prs) != 1 {
			t.Fatalf("the service holds %d pull requests, want the one the run opened", len(e.service.prs))
		}
		if pr := e.service.prs[0]; pr.Branch != e.branch() {
			t.Errorf("the pull request's branch is %q, want %q", pr.Branch, e.branch())
		}
		// The job holds no token of the provider it serves, and this run was
		// handed none: nothing it wrote carried one, and the only identity the
		// service was called under is the job's own.
		for _, call := range append(append([]sharedJobE2EServiceCall{}, e.service.gitCalls...), e.service.apiCalls...) {
			if call.auth != "Bearer "+sharedJobE2EJobIDToken {
				t.Errorf("%s %s reached the service under %q", call.method, call.path, call.auth)
			}
		}
	})

	t.Run("a repository the installed proxy does not serve is refused", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindBitbucket, sharedJobE2EBitbucketRepo, admitted)
		e.host(t)
		// The published block admits another repository: a shared job's run has
		// no token to fall back to, so this is the refusal that stands between
		// it and a clone with no credential at all (S3's halt).
		e.publishPolicy(t, sharedJobE2EPolicy(`["github/`+sharedJobE2EGithubRepo+`"]`, `["bitbucket/acme/elsewhere"]`))
		rec, err := e.run(t, implement("feature"))
		if err == nil {
			t.Fatalf("the run was admitted: %+v\n%s", rec, e.logs.String())
		}
		if !strings.Contains(err.Error(), "bitbucket/"+sharedJobE2EBitbucketRepo) {
			t.Errorf("the refusal does not name the repository: %v", err)
		}
		if e.cloned() {
			t.Error("the run cloned a repository the installed proxy does not serve")
		}
		if len(e.service.gitCalls) != 0 {
			t.Errorf("a refused run reached the service: %v", e.service.gitCalls)
		}
	})

	t.Run("a baked checkout is reset before the fetch", func(t *testing.T) {
		e := sharedJobE2EUp(t, gitprov.KindBitbucket, sharedJobE2EBitbucketRepo, admitted)
		e.host(t)
		marker := e.bakeCheckout(t)
		rec, err := e.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("the run errored: %v\n%s", err, e.logs.String())
		}
		if rec.Status != runstore.StatusSucceeded {
			t.Fatalf("record = %+v: a baked checkout the run can reset is not a refusal\n%s", rec, e.logs.String())
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the hook the baked checkout carried ran: %v", err)
		}
		e.assertClonedAndPushed(t, rec, e.bakedDir(t))
	})
}

// cloned reports whether the run made itself a checkout in the work directory.
func (e *sharedJobE2E) cloned() bool {
	_, err := os.Stat(filepath.Join(e.workDir, ".git"))
	return err == nil
}

// assertClonedAndPushed is the run's git half, out loud: the checkout's origin
// is the URL the run computed for its own repository (a project job holds no
// per-repository remote), the branch it made is on the remote that URL resolves
// to, and the pull request it opened names it.
func (e *sharedJobE2E) assertClonedAndPushed(t *testing.T, rec *runstore.Record, dir string) {
	t.Helper()
	if dir == "" {
		dir = e.workDir
	}
	want, err := repourl.CloneURL(e.provider, e.repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dir, "config", "--get", "remote.origin.url"); got != want {
		t.Errorf("the checkout's origin is %q, want %q: the run computes its remote for the repository its task names", got, want)
	}
	if head := testutil.Git(t, e.remote, "rev-parse", "refs/heads/"+e.branch()); head == "" {
		t.Fatalf("the branch %s is not on the remote: the run did not push", e.branch())
	}
	// The fetch the run made for itself before it committed: the base it
	// works on is the remote's, and it is the base the record names.
	base := testutil.Git(t, e.remote, "rev-parse", "refs/heads/"+rec.BaseBranch)
	if base == "" {
		t.Fatalf("the remote holds no base branch %s", rec.BaseBranch)
	}
	if fetched := testutil.Git(t, dir, "rev-parse", "origin/"+rec.BaseBranch); fetched != base {
		t.Errorf("the checkout's origin/%s is %q, want the remote's %q: the run fetches the base it works on",
			rec.BaseBranch, fetched, base)
	}
	if rec.StartSHA != base {
		t.Errorf("the record starts the run at %q, want the fetched base %q", rec.StartSHA, base)
	}
	if rec.HeadSHA == "" {
		t.Error("the record names no head commit")
	}
	if len(e.agentCall) == 0 {
		t.Fatal("the run called no stage")
	}
	for i, call := range e.agentCall {
		if call.Dir != dir {
			t.Errorf("stage %d ran in %s, want the checkout the run made at %s", i+1, call.Dir, dir)
		}
	}
	if e.opener == nil {
		return
	}
	if len(e.opener.State.PRs) != 1 {
		t.Fatalf("want exactly one pull request, got %+v", e.opener.State.PRs)
	}
	if pr := e.opener.State.PRs[0]; pr.Spec.Branch != e.branch() {
		t.Errorf("the pull request's branch is %q, want %q", pr.Spec.Branch, e.branch())
	}
}

// bakedDir is where the engine's bake stage writes this run's repository: the
// per-slug path beside the work directory (runner.BakedDir), which is the
// checkout a project job adopts in preference to the work directory's own.
func (e *sharedJobE2E) bakedDir(t *testing.T) string {
	t.Helper()
	e.layout(t)
	return filepath.Join(filepath.Dir(e.workDir), "baked", e.slug)
}

// bakeCheckout lays out the checkout a baked image holds for this run: a
// checkout of the run's own repository at the run's own URL, at the per-slug
// path the engine's bake stage writes, with a hook planted in it and a tracked
// file an entry's build changed. It is the state S9 says a run must reset before
// it fetches anything, and it returns the marker the hook would touch.
func (e *sharedJobE2E) bakeCheckout(t *testing.T) string {
	t.Helper()
	e.layout(t)
	dir := e.bakedDir(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", e.remote, dir)
	url, err := repourl.CloneURL(e.provider, e.repo, "")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "remote", "set-url", "origin", url)
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "planted-hook-ran")
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"),
		[]byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("edited by a build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

// sharedJobE2EMetadata is the job's metadata server: the Google ID token the
// forwarder presents as the job's identity, asked for over GCE_METADATA_HOST,
// which is where the job's environment points this fixture too.
func sharedJobE2EMetadata(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/identity") || r.URL.Query().Get("audience") == "" {
			t.Errorf("the metadata server was asked for %s, which is not an identity", r.URL)
		}
		_, _ = io.WriteString(w, sharedJobE2EJobIDToken)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sharedJobE2EJobIDToken is what the fixture's metadata server answers and what
// must arrive at the service as the run's job identity. It is a shape and not a
// credential: nothing verifies it here, and the assertion is that the run
// presented this job's own identity and no other.
const sharedJobE2EJobIDToken = "job-id-token-shared-job-e2e"

// sharedJobE2EService is the installation's credentials service as a shared
// Bitbucket run meets it: one route, under the proxy's own prefix, with the two
// halves the run's traffic has there. What follows the prefix and begins with
// "2.0/" is the Bitbucket API, and everything else is git — the classification
// internal/credsvc's own route makes, reduced to the two answers this fixture
// has to give. Neither half decides anything: the run reaches this service with
// its own job identity, through its own forwarder, and that is what the case is
// about.
type sharedJobE2EService struct {
	t       *testing.T
	job     string
	bare    string
	root    string
	backend *cgi.Handler
	srv     *httptest.Server

	mu       sync.Mutex
	gitCalls []sharedJobE2EServiceCall
	apiCalls []sharedJobE2EServiceCall
	prs      []sharedJobE2EPR
}

// sharedJobE2EServiceCall is one request that reached the service.
type sharedJobE2EServiceCall struct {
	method, path, auth, run, job string
}

func newSharedJobE2EService(t *testing.T, job, bare string) *sharedJobE2EService {
	t.Helper()
	testutil.Git(t, bare, "config", "http.receivepack", "true")
	s := &sharedJobE2EService{
		t: t, job: job, bare: bare, root: filepath.Dir(bare),
		backend: &cgi.Handler{
			Path: filepath.Join(testutil.Git(t, bare, "--exec-path"), "git-http-backend"),
		},
	}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sharedJobE2EService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := sharedJobE2EServiceCall{method: r.Method, path: r.URL.Path,
		auth: r.Header.Get("Authorization"), run: r.Header.Get("X-Fugaro-Run-Identity"),
		job: r.Header.Get("X-Fugaro-Run-Job")}
	rest, ok := strings.CutPrefix(r.URL.Path, credsvc.BitbucketProxyPrefix)
	if !ok {
		s.t.Errorf("%s %s: not the proxy route's path", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if call.auth != "Bearer "+sharedJobE2EJobIDToken {
		s.t.Errorf("%s %s: the request did not arrive under this job's identity (%q)", r.Method, r.URL.Path, call.auth)
		http.Error(w, "no identity", http.StatusUnauthorized)
		return
	}
	if call.job != s.job {
		s.t.Errorf("%s %s: the request names the job %q, want %q", r.Method, r.URL.Path, call.job, s.job)
	}
	if call.run == "" {
		s.t.Errorf("%s %s: the request carries no run identity", r.Method, r.URL.Path)
	}
	if strings.HasPrefix(rest, "2.0/") {
		s.mu.Lock()
		s.apiCalls = append(s.apiCalls, call)
		s.mu.Unlock()
		s.serveAPI(w, r, rest)
		return
	}
	s.mu.Lock()
	s.gitCalls = append(s.gitCalls, call)
	s.mu.Unlock()
	s.serveGit(w, r)
}

// serveGit answers the git half of the route with git's own http-backend over
// the fixture's bare repository, the way internal/testutil's HTTP remote does.
// Nothing here authenticates: the job's identity arrived on the request and was
// checked above, which is the whole of what a proxied run's git credential is.
//
// The caller's path carries the repository the URL names, and the backend is
// served the path its own project root and repository name spell — the rewrite
// internal/credsvc's route makes for the same reason.
func (s *sharedJobE2EService) serveGit(w http.ResponseWriter, r *http.Request) {
	s.backend.Env = []string{
		"GIT_PROJECT_ROOT=" + s.root,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=" + gitPathInfo(r.URL.Path, s.bare),
	}
	s.backend.ServeHTTP(w, r)
}

// gitPathInfo is the path git's http-backend is served: the bare repository's
// own directory name and the operation the caller asked for.
func gitPathInfo(path, bare string) string {
	i := strings.Index(path, "/"+filepath.Base(bare))
	if i < 0 {
		return path
	}
	return path[i:]
}

// sharedJobE2EPR is one pull request the service's API half holds.
type sharedJobE2EPR struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Draft bool   `json:"draft"`
	State string `json:"state"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"source"`
	Destination struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"destination"`
	Branch string `json:"-"`
}

// serveAPI is the Bitbucket half of the route: the calls the adapter makes for
// one run — a pull request it creates, reads and updates, and the comments it
// writes on it. Every answer is the shape the real API gives, because the
// adapter reads it.
func (s *sharedJobE2EService) serveAPI(w http.ResponseWriter, r *http.Request, rest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	repo := "2.0/repositories/" + sharedJobE2EBitbucketRepo
	switch {
	case r.Method == http.MethodPost && rest == repo+"/pullrequests":
		var in struct {
			Title  string `json:"title"`
			Draft  bool   `json:"draft"`
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"source"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		pr := sharedJobE2EPR{ID: len(s.prs) + 1, Title: in.Title, Draft: in.Draft, State: "OPEN",
			Branch: in.Source.Branch.Name}
		pr.Source.Branch.Name = pr.Branch
		pr.Source.Repository.FullName = sharedJobE2EBitbucketRepo
		pr.Links.HTML.Href = "https://bitbucket.org/" + sharedJobE2EBitbucketRepo + "/pull-requests/1"
		s.prs = append(s.prs, pr)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodGet && rest == repo+"/pullrequests":
		values := []sharedJobE2EPR{}
		for _, pr := range s.prs {
			if strings.Contains(r.URL.Query().Get("q"), `"`+pr.Branch+`"`) {
				values = append(values, pr)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"values": values})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, repo+"/pullrequests/"):
		if len(s.prs) == 0 {
			http.Error(w, "no pull request", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(s.prs[0])
	case r.Method == http.MethodPut && strings.HasPrefix(rest, repo+"/pullrequests/"):
		if len(s.prs) == 0 {
			http.Error(w, "no pull request", http.StatusNotFound)
			return
		}
		// The adapter marks a pull request ready by putting the draft flag
		// and the title back, and reads the answer: this half applies what
		// it was asked for, which is what Bitbucket does with a field it
		// honors (the adapter's own comment covers the host that does not).
		var in struct {
			Title string `json:"title"`
			Draft bool   `json:"draft"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.prs[0].Title, s.prs[0].Draft = in.Title, in.Draft
		_ = json.NewEncoder(w).Encode(s.prs[0])
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/comments"):
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	default:
		s.t.Errorf("unexpected %s %s at the service's API half", r.Method, rest)
		http.Error(w, "unexpected", http.StatusNotFound)
	}
}
