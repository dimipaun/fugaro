// Package runnertest runs one stage through internal/runner, for the packages
// outside it (plan task E1.2). ee/governance's tests drive the tripwires the
// core resolves — the stage's own bound and the halt it leaves — and the
// runner's own harness lives in internal/runner's _test files, which nothing
// outside that package can import.
//
// It is deliberately the minimum: seed the fixture repository, script what the
// agent does in each stage, run, and read back the record, the run log, the
// stages that ran and the pull request the run opened. It duplicates the setup
// of internal/runner/runner_test.go's harness rather than sharing it, and the
// two have to be kept in step; the runner's own harness stays there, where the
// tests that need a sidecar, a cache or an HTTP remote can reach it.
//
// E2.3a grew it for the tests the tripwires take with them: Task and Layer
// build the task a test changes one field of — the tier a launcher dispatched
// at, the project layer it resolved — and Implement, Review and Blocked are
// the steps those tests script a run with. What they assert on comes back off
// the record the harness returns, so the halt, the checkpoint and the policy
// the merge recorded are readable from outside without the harness naming
// them. Nothing here reads or sets a governance hook: a test that needs the
// enforcing pair installs it itself, as ee/governance's does.
//
// E2.3b grew it again for T7.3: Gateway points a run's stages at a real
// gateway in front of a fake upstream, and Post is how a step makes the calls
// a stage's agent would — which is what a stop the gateway's own stage limit
// makes needs. It is the least of internal/runner/gateway_test.go's frame, the
// same way the rest of this file is the least of runner_test.go's.
//
// What it still cannot express is a run with a backend: the registry case of
// runner_test.go — what the console shows a stage's deadline as — is driven
// through newBK, gwConfig and waitUntil, none of which this harness reaches. A
// package outside internal/runner that needs that either grows this one or
// leaves that test in the core.
package runnertest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// gatewayKey stands in for a run's model credential. It is a fixture: the fake
// upstream is the only thing it reaches, and the gateway logs no header.
const gatewayKey = "sk-ant-api03-TESTKEY-0123456789abcdefghijklmnopqrstuvwxyz"

// runID is the run every harness drives, as the runner's own harness names it.
const runID = "20260926-221530-abcd"

// repoSlug is the bucket slug the run lives under: the real task.Slug of the
// fixture repository, not a readable placeholder, because the run's admission
// check derives the provider kind back out of the slug it was handed and
// refuses one no job provider kind produces.
var repoSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// errUnexpectedCall answers an agent call no step was scripted for, so the
// stage fails where the extra call was made rather than in the runner.
var errUnexpectedCall = errors.New("no step for this agent call")

// Step is one stage's scripted agent call: what the agent does, in order.
type Step func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error)

// Blocked is a stage that answers only when its context ends, reporting the
// cost it had reached: what ends such a stage is the bound the run resolved
// and never the agent's own work, and the checkpoint of a halt carries the
// spend the stage had reached.
func Blocked(costUSD float64) Step {
	return func(_ *testing.T, ctx context.Context, _ agent.Request) (agent.Result, error) {
		<-ctx.Done()
		return agent.Result{CostUSD: costUSD}, ctx.Err()
	}
}

// Script runs a shell script in the stage's own checkout, as the agent's own
// commands run, and fails the test when it fails: the script is the test's own
// setup and never the stage's work. It is how a step moves the branch under
// the runner — the fix round that leaves the commit the review range was
// narrowed to behind, say (T7.3) — and it is what a step of a test's own is
// built from.
func Script(t *testing.T, req agent.Request, script string) {
	t.Helper()
	shell(t, req, script)
}

// Implement is the implement stage's work: a commit, the workflow's verify
// command, and the pull request body the stage writes. It is what a test that
// is about a later stage scripts first.
func Implement(name string) Step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		t.Helper()
		shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'Add "+name+"'")
		stateDir := envValue(req.Env, "FUGARO_STATE_DIR")
		if _, err := verify.Run(ctx, verify.Options{StateDir: stateDir, Kind: verify.KindTest,
			Env: req.Env, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "pr.md"),
			[]byte("# Add "+name+"\n\nAdds "+name+".txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
}

// Review is a review stage's answer: a verdict and the findings it reports,
// in the shape the runner's own review threshold reads.
func Review(verdict string, findings int) Step {
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

// shell runs script in the stage's own checkout, as the agent's commands run;
// a failure is the test's rather than the stage's, since it is the test's own
// setup that did not work.
func shell(t *testing.T, req agent.Request, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
}

// StateDir is the state directory the stage was handed, which is where a step
// that verifies its own work writes the record (verify.Run), the way Implement
// does. A step that rewrites the commit an earlier verify was made on has to
// make one of its own, and this is the lookup it needs to.
func StateDir(req agent.Request) string { return envValue(req.Env, "FUGARO_STATE_DIR") }

// envValue is one variable of the environment a stage is handed.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Log is the run log, in the runner's own slog text format. It is not a bare
// bytes.Buffer because a run with a gateway writes to it from the gateway's
// own goroutines — a stage's calls are served there while the run goroutine
// carries on — and a buffer two goroutines share is a data race however the
// test reads it.
type Log struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// String is the whole log so far.
func (l *Log) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// scriptedAgent answers the run's stages with the steps a test gave, and
// records the requests it was handed.
type scriptedAgent struct {
	t     *testing.T
	steps []Step
	calls []agent.Request
}

func (a *scriptedAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.calls = append(a.calls, req)
	i := len(a.calls) - 1
	if i >= len(a.steps) {
		a.t.Errorf("unexpected agent call #%d", i+1)
		return agent.Result{}, errUnexpectedCall
	}
	return a.steps[i](a.t, ctx, req)
}

// Harness is one seeded run: a fixture repository on a local remote, a stored
// task, and a scripted agent.
type Harness struct {
	// Provider is the fake git provider the run opened its pull request on.
	// Its Remote is the bare repository the run was seeded from and pushed to,
	// which is how a test puts a branch of its own there before the run.
	Provider *fake.Provider
	// Log is the run log, in the runner's own slog text format.
	Log *Log

	deps  runner.Deps
	store *runstore.Store
	agent *scriptedAgent
}

// Task is the task the harness stores when a test names none: the run's own
// id and repository, and the fixture's only workflow, which is also the one a
// task naming no workflow resolves to. A test that needs more — the tier a
// launcher dispatched the run at, the project layer it resolved — takes this
// and sets that field, so the two never drift apart.
func Task() *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
}

// Layer is a project layer as a launcher carries it: the text the run is
// handed, its sum and a generation, for the ProjectLayer of a task a test
// built with Task.
func Layer(text string) *task.ProjectLayer {
	return &task.ProjectLayer{SHA256: config.LayerSum([]byte(text)), Generation: 3, YAML: text}
}

// New seeds a remote with the fixture repository — cfg replaces its
// fugaro.yaml when non-empty — and stores spec, which is the harness's own
// default when nil.
func New(t *testing.T, cfg string, spec *task.Spec) *Harness {
	t.Helper()
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	if cfg != "" {
		files["fugaro.yaml"] = cfg
	}
	remote := testutil.NewRemote(t, files)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	if spec == nil {
		// The task names the fixture's only workflow because a run's job is
		// named from it: a task naming no workflow cannot be placed in the job
		// it is in, and a run that gives itself a Cloud Run execution would
		// stop on that instead of on what the test is about.
		spec = Task()
	}
	store := runstore.Open(bucket, repoSlug, spec.RunID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	managed := filepath.Join(tmp, "claude-code")
	if err := os.Mkdir(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	logs := &Log{}
	a := &scriptedAgent{t: t}
	p := &fake.Provider{Repo: spec.Repo, Remote: remote}
	return &Harness{
		Provider: p,
		Log:      logs,
		store:    store,
		agent:    a,
		deps: runner.Deps{
			Store: store, OpenProvider: gitprov.Static(p), Agent: a,
			WorkDir: filepath.Join(tmp, "work"), Remote: remote, StateDir: filepath.Join(tmp, "state"),
			// The fixture's workflow declares a secret, so the environment has
			// to carry its variable: a run whose secret is missing stops at
			// bootstrap rather than at what the test is about. The credential
			// is a placeholder and no stage of a test dials a model with it.
			Env: []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
				"ANTHROPIC_API_KEY=test-key", "FIXTURE_FAILS_FILE=" + filepath.Join(tmp, "fails"),
			},
			Log: slog.New(slog.NewTextHandler(logs, nil)),
			// A local run, as `fugaro exec --task-file` is: nothing here is a
			// platform's signal (design compute-backend.md §2.5).
			Local: true, CancelPoll: 20 * time.Millisecond,
			ManagedSettingsPath: filepath.Join(managed, "managed-settings.json"),
		},
	}
}

// Run drives the run with the steps a test gave, one per stage call, and
// returns the record it wrote.
func (h *Harness) Run(t *testing.T, steps ...Step) (*runstore.Record, error) {
	t.Helper()
	h.agent.steps = steps
	return runner.Run(context.Background(), h.deps)
}

// Gateway points the run's stages at a gateway in front of a fake upstream —
// the run's budget in budget mode (observe or enforce, with capUSD when not
// empty), every stage's calls answered by script — and returns the fake
// upstream. It is what a package outside internal/runner needs to make a
// stage's calls at all, and so to have one stopped by the gateway's own stage
// limit (T7.2, T7.3): the stop is delivered on the gateway's halt channel,
// which the runner is watching.
//
// The environment the stages are handed carries the gateway's own URL and
// token — stageEnv does that — so a step makes its calls with Post. The
// gateway's tests proper are internal/runner's and internal/gateway's; this is
// the least a test of what the runner does with a stop needs.
func (h *Harness) Gateway(t *testing.T, mode, capUSD string, script ...anthropicfake.Reply) *anthropicfake.Fake {
	t.Helper()
	fake, up := anthropicfake.New(t, script...)
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+gatewayKey)
	env := map[string]string{runner.BudgetModeEnv: mode, runner.MaxRunUSDEnv: capUSD}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend, h.deps.GatewayUpstream = spend, up.URL
	return fake
}

// Post sends one Messages call through the gateway the run was pointed at, as
// the stage's own agent would, and returns the status and the whole body. The
// request's own Env is where the gateway's URL and token are, exactly as a
// real stage's agent reads them.
func Post(t *testing.T, req agent.Request, model string, maxTokens int64) (int, string) {
	t.Helper()
	return PostAs(t, req, "", model, maxTokens)
}

// PostAs is Post from one sub-agent of the stage: agentID is the caller the
// fan-out limit counts distinct values of (T7.2), and "" is the main session,
// which is what Claude Code sends for it.
func PostAs(t *testing.T, req agent.Request, agentID, model string, maxTokens int64) (int, string) {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"messages":[{"role":"user","content":"hello"}]}`, model, maxTokens)
	r, err := http.NewRequest(http.MethodPost, envValue(req.Env, "ANTHROPIC_BASE_URL")+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("anthropic-version", "2023-06-01")
	if key := envValue(req.Env, "ANTHROPIC_API_KEY"); key != "" {
		r.Header.Set("x-api-key", key)
	}
	if agentID != "" {
		r.Header.Set("x-claude-code-agent-id", agentID)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// filterEnv is env without key, so a harness's own credential is not doubled
// by the one a gateway installs.
func filterEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == key {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Calls are the agent requests the run made, in order: which stages ran and
// what each was handed.
func (h *Harness) Calls() []agent.Request { return h.agent.calls }

// PR is the one pull request the run opened, and it fails the test when the
// run opened none or more than one: a test that asserts on a pull request is
// about the run's own.
func (h *Harness) PR(t *testing.T) fake.PRState {
	t.Helper()
	if len(h.Provider.State.PRs) != 1 {
		t.Fatalf("want exactly one PR, got %+v", h.Provider.State.PRs)
	}
	return h.Provider.State.PRs[0]
}
