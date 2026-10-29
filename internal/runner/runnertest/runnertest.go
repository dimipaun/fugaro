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
// What it cannot express is a run with a backend and a gateway: the registry
// case of that file — what the console shows a stage's deadline as — is driven
// through newBK, gwConfig and waitUntil, none of which this harness reaches. A
// package outside internal/runner that needs those either grows this one or
// leaves that test in the core; the tripwires E1.2 moves need neither, which is
// why the minimum is where this stops.
package runnertest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

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

// envValue is one variable of the environment a stage is handed.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
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
	Provider *fake.Provider
	// Log is the run log, in the runner's own slog text format.
	Log *bytes.Buffer

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
	logs := &bytes.Buffer{}
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
