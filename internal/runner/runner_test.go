package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/headroomproc"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

const runID = "20260926-221530-abcd"

// repoSlug is the storage slug every fixture's run lives under: the real
// task.Slug of the fixture repository (acme/app, under github), not a
// readable placeholder. The run's admission check (plan task 1.5) derives the
// provider kind back out of the slug it was handed and refuses a slug no job
// provider kind produces — which a bare repoSlug is, so a fixture carrying
// one could never get past bootstrap.
var repoSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

type step func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error)

type scriptedAgent struct {
	t     *testing.T
	steps []step
	calls []agent.Request
	// before, when set, runs at the start of every stage's call: the
	// harness's hook for what a test wants to happen mid-stage.
	before func(ctx context.Context)
}

func (a *scriptedAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.calls = append(a.calls, req)
	i := len(a.calls) - 1
	if i >= len(a.steps) {
		a.t.Errorf("unexpected agent call #%d", i+1)
		return agent.Result{}, errors.New("unexpected call")
	}
	if a.before != nil {
		a.before(ctx)
	}
	return a.steps[i](a.t, ctx, req)
}

type harness struct {
	files     map[string]string
	deps      runner.Deps
	store     *runstore.Store
	bucket    *blob.Bucket
	provider  *fake.Provider
	agent     *scriptedAgent
	remote    string
	failsFile string
	// sidecar is the fake process the run started, when compression is on;
	// the three counters below say what Deps.StartHeadroom and Stop were
	// called with.
	sidecar            *fakeSidecar
	compressionStarts  int
	lastCompressionEnv []string
	compressionStops   int
	// sidecarFailsDuring is the stage during which the fake sidecar's Dead
	// channel closes, set by killSidecarDuring and hangSidecarDuring.
	sidecarFailsDuring string
}

// sidecarURL is what the fake sidecar says it listens on. Nothing dials it:
// the stages of these tests make no model calls.
const sidecarURL = "http://127.0.0.1:9100"

// fakeSidecar is the harness's stand-in for a running compression sidecar.
// Nothing is spawned: Deps.StartHeadroom returns it, Dead closes when a test
// says the sidecar died, and Stop is counted.
type fakeSidecar struct {
	dead  chan struct{}
	once  sync.Once
	stops *int
}

func (f *fakeSidecar) URL() string { return sidecarURL }

func (f *fakeSidecar) Dead() <-chan struct{} { return f.dead }

func (f *fakeSidecar) Stop(context.Context) error {
	*f.stops++
	return nil
}

// die is the crash, or the liveness failure a hang is: one channel either
// way, which is the point -- the runner has no second path for a hang.
func (f *fakeSidecar) die() { f.once.Do(func() { close(f.dead) }) }

// startFakeSidecar is Deps.StartHeadroom: it records what the runner built
// the sidecar's environment from and hands back the fake.
func (h *harness) startFakeSidecar(_ context.Context, o headroomproc.Options) (runner.SidecarProcess, error) {
	h.compressionStarts++
	h.lastCompressionEnv = o.Env
	h.sidecar = &fakeSidecar{dead: make(chan struct{}), stops: &h.compressionStops}
	return h.sidecar, nil
}

// beforeStage is the scripted agent's hook: the stage the runner recorded
// before it called the agent is the one a test named, so this is where a
// sidecar death or hang lands mid-stage.
func (h *harness) beforeStage(ctx context.Context) {
	if h.sidecarFailsDuring == "" || h.sidecar == nil {
		return
	}
	rec, err := h.store.ReadRecord(ctx)
	if err != nil || rec.Stage != h.sidecarFailsDuring {
		return
	}
	h.sidecar.die()
}

// killSidecarDuring makes the fake sidecar die while the named stage runs,
// as a crash does: Dead() closes and the runner must fail that stage.
func (h *harness) killSidecarDuring(stage string) { h.sidecarFailsDuring = stage }

// hangSidecarDuring is killSidecarDuring for a sidecar that stays up and
// stops answering: P8's liveness probe closes Dead() the same way, and the
// runner must treat the two identically (P10).
func (h *harness) hangSidecarDuring(stage string) { h.sidecarFailsDuring = stage }

// newHarness seeds a remote with the fixture repo (cfg replaces fugaro.yaml
// when non-empty) and stores a task spec.
func newHarness(t *testing.T, cfg string, spec *task.Spec) *harness {
	t.Helper()
	return newHarnessFiles(t, cfg, spec, nil)
}

// newHarnessFiles is newHarness with extra files committed to the remote.
func newHarnessFiles(t *testing.T, cfg string, spec *task.Spec, extra map[string]string) *harness {
	t.Helper()
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	if cfg != "" {
		files["fugaro.yaml"] = cfg
	}
	for k, v := range extra {
		files[k] = v
	}
	remote := testutil.NewRemote(t, files)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	if spec == nil {
		// The workflow is the fixture's only one (testdata/fixture-repo's
		// fugaro.yaml), which is also what SelectWorkflow resolves a task that
		// names none to. It is written out because a run's job is named from it
		// (a per-repository job is gcp.JobName of the slug and the workflow's
		// variant): a task naming no workflow cannot be placed in the job it is
		// in, and every test here that gives the run a Cloud Run execution
		// would stop on that instead of on what it is about.
		spec = &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	}
	store := runstore.Open(bucket, repoSlug, spec.RunID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	failsFile := filepath.Join(tmp, "fails")
	// The managed settings never go to the real /etc/claude-code in a test.
	managed := filepath.Join(tmp, "claude-code")
	if err := os.Mkdir(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &scriptedAgent{t: t}
	p := &fake.Provider{Repo: spec.Repo, Remote: remote}
	h := &harness{
		files: files,
		deps: runner.Deps{
			Store: store, OpenProvider: gitprov.Static(p), Agent: a,
			WorkDir: filepath.Join(tmp, "work"), Remote: remote, StateDir: filepath.Join(tmp, "state"),
			Env:        []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "ANTHROPIC_API_KEY=test-key", "FIXTURE_FAILS_FILE=" + failsFile},
			CancelPoll: 20 * time.Millisecond, ManagedSettingsPath: filepath.Join(managed, "managed-settings.json"),
			// The harness is a local run, as `fugaro exec --task-file` is:
			// nothing here is a platform's signal. A test that wants the
			// cloud sets Local false, which is also the field's zero value
			// (design compute-backend.md §2.5).
			Local: true,
		},
		store: store, bucket: bucket, provider: p, agent: a, remote: remote, failsFile: failsFile,
	}
	// Every harness starts its sidecar through the fake, so no test spawns a
	// real process; a test that never turns compression on never calls it.
	h.deps.StartHeadroom = h.startFakeSidecar
	a.before = h.beforeStage
	return h
}

// useHTTPRemote replaces the harness's remote with the same files served
// over HTTP behind basic auth that allow decides. Its host names no
// provider, so the run names one up front, as `--provider github` would:
// the clone itself needs credentials.
func (h *harness) useHTTPRemote(t *testing.T, allow func(user, pass string) bool) *testutil.HTTPRemote {
	t.Helper()
	remote := testutil.NewHTTPRemote(t, h.files, allow)
	h.deps.Remote, h.remote = remote.URL, remote.Bare
	h.provider.Remote = remote.Bare
	h.deps.ProviderKind = "github"
	return remote
}

// filterEnv returns env without the entry for key.
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

func (h *harness) run(t *testing.T, steps ...step) (*runstore.Record, error) {
	t.Helper()
	h.agent.steps = steps
	return runner.Run(context.Background(), h.deps)
}

func (h *harness) fails(t *testing.T, names string) {
	t.Helper()
	if err := os.WriteFile(h.failsFile, []byte(names), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func shell(t *testing.T, req agent.Request, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
}

func verifyTest(t *testing.T, ctx context.Context, req agent.Request) {
	t.Helper()
	_, err := verify.Run(ctx, verify.Options{StateDir: envValue(req.Env, "FUGARO_STATE_DIR"), Kind: verify.KindTest, Env: req.Env, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
}

// implement commits a file, verifies, and writes pr.md.
func implement(name string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'Add "+name+"'")
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add "+name+"\n\nAdds "+name+".txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
}

func review(verdict string, findings int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fs := make([]map[string]string, findings)
		for i := range fs {
			fs[i] = map[string]string{"summary": "fix it"}
		}
		b, _ := json.Marshal(map[string]any{"verdict": verdict, "findings": fs})
		return agent.Result{Structured: b, CostUSD: 0.5}, nil
	}
}

// reviewScored is review with findings the mode's threshold table can read:
// each one's JSON, so a case says what the reviewer scored (T3.1).
func reviewScored(verdict string, findings ...map[string]any) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b, _ := json.Marshal(map[string]any{"verdict": verdict, "findings": findings})
		return agent.Result{Structured: b, CostUSD: 0.5}, nil
	}
}

func blockUntilDone(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{}, ctx.Err()
}

func onlyPR(t *testing.T, p *fake.Provider) fake.PRState {
	t.Helper()
	if len(p.State.PRs) != 1 {
		t.Fatalf("want exactly one PR, got %+v", p.State.PRs)
	}
	return p.State.PRs[0]
}

func TestReadyPR(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("record = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || pr.Title != "Add feature" || gitprov.StripStatus(pr.Body) != "Adds feature.txt." || pr.Spec.Base != "main" || pr.Spec.Branch != "fugaro/"+runID {
		t.Fatalf("PR = %+v", pr)
	}
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "ready for review") {
		t.Fatalf("comments = %q", pr.Comments)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Status != runstore.StatusSucceeded || stored.FinishedAt == nil || stored.CostUSD != 1.5 {
		t.Fatalf("stored record = %+v, %v", stored, err)
	}
	impl, rev := h.agent.calls[0], h.agent.calls[1]
	if impl.Resume || impl.Prompt != "Add a feature" || !strings.Contains(impl.AppendSystemPrompt, "fugaro/"+runID) {
		t.Fatalf("implement request = %+v", impl)
	}
	if rev.SessionID == impl.SessionID || rev.JSONSchema != runner.VerdictSchema || rev.AppendSystemPrompt != "" {
		t.Fatalf("review request = %+v", rev)
	}
}

// TestTranscriptIsRedacted checks that the stored transcript for a stage
// never carries a secret value in the clear, even when the agent itself
// writes one to the transcript stream.
func TestTranscriptIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		if err != nil {
			return res, err
		}
		fmt.Fprintf(req.Transcript, "{\"leaked\":%q}\n", secret)
		return res, err
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	data, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/implement-1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("transcript is not redacted: %q", data)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("transcript leaks the secret: %q", data)
	}
}

func TestFailingTestsOpenDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "tests failing on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestFixRoundResumesImplementSession(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !strings.Contains(req.Prompt, "- fix it") {
			t.Errorf("fix prompt lacks the finding: %q", req.Prompt)
		}
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || len(rec.Reviews) != 2 {
		t.Fatalf("record = %+v", rec)
	}
	if f := h.agent.calls[2]; !f.Resume || f.SessionID != h.agent.calls[0].SessionID {
		t.Fatalf("fix did not resume the implement session: %+v", f)
	}
}

func TestReviewRoundsExhausted(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Try again'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 2 still has 1 finding" {
		t.Fatalf("record = %+v", rec)
	}
}

// TestDecideToleratedRoundOpensAReadyPR: T3.1's whole feature at the loop. The
// repository opted in with a rigor: block and runs at Startup, whose floor is
// above the finding the reviewer scored, so the round the run left open is
// tolerated: the pull request is opened ready rather than as a draft, the
// record's reason names the mode and the count, and what the mode let through
// is in the record and the pull request's status section — nothing is filed
// (D7, D9, D30). The unscored findings the other cases use are deliberately
// not this: an unscored finding blocks in every mode (D4).
func TestDecideToleratedRoundOpensAReadyPR(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n", "rigor:\n  default: startup\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	tolerated := reviewScored("changes", map[string]any{"summary": "fix it", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"})
	rec, err := h.run(t, implement("feature"), tolerated, fix, tolerated)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v; want a ready run: the round's only finding is below startup's floor", rec)
	}
	if rec.Reason != "review round 2 left 1 finding, tolerated at startup" {
		t.Errorf("reason = %q; want the mode and the count the round was tolerated at (D7)", rec.Reason)
	}
	if len(rec.Backlog) != 0 || branchBacklogItems(t, h, rec.Branch) != "" {
		t.Errorf("the run filed %v; want nothing written into the repository: what it left open is in the record and the status section", rec.Backlog)
	}
	// And the record keeps the finding itself, with the count and the
	// reviewer's own words for it, so what the mode let through is readable
	// after the job and the pull request are gone (T3.4, F19).
	if rec.Tolerated == nil || rec.Tolerated.Total != 1 || len(rec.Tolerated.Findings) != 1 {
		t.Fatalf("the record's tolerated findings = %+v; want the one startup let through (T3.4)", rec.Tolerated)
	}
	if got := rec.Tolerated.Findings[0]; got.Title != "fix it" || got.Label != rigor.Low || got.Mode != rigor.Startup ||
		got.Likelihood != 2 || got.Impact != 2 || got.FixCost != 1 || got.Class != rigor.Correctness {
		t.Errorf("tolerated finding = %+v; want the reviewer's finding, scored, labelled and with the mode that tolerated it", got)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
	// The status section is where a reader of the pull request sees what the
	// mode let through, now that nothing is written into the tree.
	if !strings.Contains(pr.Body, "tolerated at startup") {
		t.Errorf("the status section does not name the tolerated round:\n%s", pr.Body)
	}
}

// TestDecideToleratedHotfixRoundFilesNothing: Hotfix's row is the one that
// tolerates and defers (D30), so this is the run that used to write two items.
// It writes none: the round it left open is in the record's tolerated findings
// and the mode is in the status section, and the deferred proper fix — which no
// line of the record carried — is not written anywhere, because a commit made
// after the suite ran is exactly what a tree-pinned test cannot survive.
func TestDecideToleratedHotfixRoundFilesNothing(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n", "rigor:\n  default: hotfix\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	// The mildest finding the scale carries: Hotfix tolerates everything but
	// what would make the fix wrong or unsafe, so this is one it tolerates and
	// an unscored or catastrophic finding would not be.
	tolerated := reviewScored("changes", map[string]any{"summary": "fix it", "likelihood": 1, "impact": 1, "fix_cost": 1, "class": "correctness"})
	rec, err := h.run(t, implement("feature"), tolerated, fix, tolerated)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v; want a ready run: the round's only finding is one a hotfix tolerates", rec)
	}
	if rec.Reason != "review round 2 left 1 finding, tolerated at hotfix" {
		t.Errorf("reason = %q; want the mode and the count the round was tolerated at (D7)", rec.Reason)
	}
	if len(rec.Backlog) != 0 || branchBacklogItems(t, h, rec.Branch) != "" {
		t.Fatalf("the run filed %v; want nothing written into the repository", rec.Backlog)
	}
	if rec.Tolerated == nil || rec.Tolerated.Total != 1 {
		t.Errorf("the record's tolerated findings = %+v; want the round's own finding", rec.Tolerated)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
	if !strings.Contains(pr.Body, "rigor: hotfix (repo)") {
		t.Errorf("the status section does not name the mode the run was held to:\n%s", pr.Body)
	}
}

// TestRigorRoundMomentRaisesTheModeFromTheRunsOwnDiff: D22's second moment, at
// the loop. The repository starts the run at its own default, and the fix in
// round 1 writes the one file the Critical area rule matches — so the mode the
// run has reached is Critical from round 1 on, and the round is the moment that
// decided it. The block's own moment is what proves the call site exists:
// finalize resolves the same mode from the same diff and, answering the same
// thing, leaves the block as the round left it.
func TestRigorRoundMomentRaisesTheModeFromTheRunsOwnDiff(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"rigor:\n  default: startup\n  areas:\n    - { name: app, match: \"feature.txt\", mode: critical }\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	rg := rec.Rigor
	if rg == nil {
		t.Fatal("the record carries no rigor block")
	}
	if rg.Mode != rigor.Critical || rg.From != rigor.FromArea {
		t.Fatalf("the mode = %q from %q; want critical from the area the run's own file matched", rg.Mode, rg.From)
	}
	if rg.At != runstore.RigorAtRound {
		t.Errorf("the block was decided at %q; want %q — a mode a run's own diff reached must not wait for finalize (D22)", rg.At, runstore.RigorAtRound)
	}
	want := []runstore.RigorArea{{Name: "app", Mode: rigor.Critical, At: runstore.RigorAtRound}}
	if !slices.Equal(rg.Areas, want) {
		t.Errorf("the areas = %+v; want %+v", rg.Areas, want)
	}
}

// seniorRounds counts the senior review rounds a record holds, which is what a
// run's mode ceiling is spent on: a first-line round is not one.
func seniorRounds(rec *runstore.Record) int {
	n := 0
	for _, rv := range rec.Reviews {
		if rv.Tier != runstore.TierFirst {
			n++
		}
	}
	return n
}

// TestReviewRoundsTheModeRaisedBetweenRoundsBounds: T3.2's ceiling is read
// under F6's precedence and read again on every round, so a mode a run raises
// between rounds buys the rounds that remain and never reaches back to the ones
// already spent (D22). The repository asks for four rounds and starts at
// Startup, whose ceiling is two: without the raise the loop would stop after
// round 2, and the diff round 1's fix writes matches the Critical area, so the
// run spends the four rounds the precedence asked for.
func TestReviewRoundsTheModeRaisedBetweenRoundsBounds(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "review_rounds: 2", "review_rounds: 4", 1)
	cfg = strings.Replace(cfg, "agent:\n",
		"rigor:\n  default: startup\n  areas:\n    - { name: app, match: \"feature.txt\", mode: critical }\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1), fix, review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := seniorRounds(rec); got != 4 {
		t.Fatalf("the run spent %d senior rounds; want 4 — Startup's ceiling must not stop the rounds the raised mode bought (D22)", got)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Critical {
		t.Fatalf("the record's mode = %+v; want critical, or the case proves nothing", rec.Rigor)
	}
}

// TestReviewRoundsUnconfiguredRepositoryIsUnchanged: the mode's ceiling is read
// only where the repository opted in (reviewRoundCeiling), the gate the
// findings policy reads too. A repository that configures no rigor keeps the
// rounds F6 gives it, whatever mode it resolved for its budget (D19's ruling):
// it asks for five and spends five, though the Pro it resolves has a lower
// ceiling.
func TestReviewRoundsUnconfiguredRepositoryIsUnchanged(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "review_rounds: 2", "review_rounds: 5", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1), fix, review("changes", 1), fix, review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := seniorRounds(rec); got != 5 {
		t.Fatalf("the run spent %d senior rounds; want the five the repository asked for, unlowered by the mode it resolved", got)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Default {
		t.Fatalf("the record's mode = %+v; want the default, or the case proves nothing", rec.Rigor)
	}
}

// TestReviewRoundsTheModesCeilingLowers: T3.2's ceiling lowers the rounds F6
// agrees on, and it lowers them where it acts — in the loop's own bound, not
// only in the plan the loop no longer reads for its exit. The repository opted
// in and runs at POC, whose ceiling is two, and asks for three: the run spends
// two senior rounds, one fewer than the precedence gave it.
func TestReviewRoundsTheModesCeilingLowers(t *testing.T) {
	base := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg := strings.Replace(base, "review_rounds: 2", "review_rounds: 3", 1)
	if cfg == base {
		t.Fatal("the fixture has no review_rounds: 2 to raise, so the case would not ask for three rounds")
	}
	withRigor := strings.Replace(cfg, "agent:\n", "rigor:\n  default: poc\nagent:\n", 1)
	if withRigor == cfg {
		t.Fatal("the fixture has no agent: block to put rigor: before, so the repository would not opt in")
	}
	cfg = withRigor
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := seniorRounds(rec); got != 2 {
		t.Fatalf("the run spent %d senior rounds; want 2 — POC's ceiling lowers the three the repository asked for (D7b)", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %q; want a draft: the run stopped on the ceiling with a finding open", rec.Outcome)
	}
}

// TestTheRunPushesTheCommitItsSuitePassed: the run's own last commit is the one
// the suite ran on, so nothing finalize writes can break a test the run passed.
// A test that pins a fact about the tree — this repository's own census of
// docs/backlog/ is the one that bit — is read again by a later run against the
// commit the branch carries, and a commit made after the suite's last run moved
// the tree under it. The mode here is one whose row tolerates, so the run this
// case is about is exactly the one that used to write such a commit.
func TestTheRunPushesTheCommitItsSuitePassed(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n", "rigor:\n  default: pro\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Try again'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 2 still has 1 finding" {
		t.Fatalf("record = %+v; want the draft a finding the mode blocks leaves", rec)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("the remote branch is %s, the record's head is %s: the run pushed a commit it did not test", got, rec.HeadSHA)
	}
	last := rec.Verify[len(rec.Verify)-1]
	if last.Kind != verify.KindTest || !last.Passed || !last.CleanTree || last.HeadSHA != rec.HeadSHA {
		t.Fatalf("the last verify record = %+v; want a passing test run on the clean commit the run pushed", last)
	}
	if items := branchBacklogItems(t, h, rec.Branch); items != "" {
		t.Errorf("the branch carries %s; want nothing the suite did not count", items)
	}
	// The report names that commit as the tested one, rather than reporting
	// that no test ran because a later commit moved the head (R3).
	report := onlyPR(t, h.provider).Comments[0]
	if want := fmt.Sprintf("**Tests:** passed on %s (", rec.HeadSHA[:7]); !strings.Contains(report, want) {
		t.Errorf("the report does not name the tested commit %s:\n%s", rec.HeadSHA[:7], report)
	}
}

// branchBacklogItems lists the backlog item files the run's branch carries,
// read from the remote the run pushed to.
func branchBacklogItems(t *testing.T, h *harness, branch string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, h.remote, "ls-tree", "-r", "--name-only", branch, backlog.Dir+"/"))
}

func TestUncommittedWorkIsCommittedButNotVerified(t *testing.T) {
	h := newHarness(t, "", nil)
	leaveDirty := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		testutil.WriteFiles(t, req.Dir, map[string]string{"forgotten.txt": "x\n"})
		return res, err
	}
	rec, err := h.run(t, leaveDirty, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "uncommitted changes were committed at finalize, after the last verified test run" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "fugaro: uncommitted work at finalize" {
		t.Fatalf("last pushed commit = %q", got)
	}
	// The record's set of changed paths is read once the run's own commits
	// are final, so the file the agent forgot is in it too: it is this run's
	// work, and a set that stopped at the agent's last commit would miss it
	// (design D27).
	cf := rec.ChangedFiles
	if cf == nil {
		t.Fatal("the record carries no changed-file set")
	}
	for _, want := range []string{"feature.txt", "forgotten.txt"} {
		if !slices.Contains(cf.Paths, want) {
			t.Fatalf("changed files = %v, want %s among them", cf.Paths, want)
		}
	}
	if cf.Truncated || cf.Total != len(cf.Paths) {
		t.Fatalf("a two-file run: %+v", cf)
	}
}

func TestNoCommitsStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	nothing := func(*testing.T, context.Context, agent.Request) (agent.Result, error) { return agent.Result{}, nil }
	rec, err := h.run(t, nothing, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "the agent made no commits" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "rev-list", "--count", "main..refs/heads/fugaro/"+runID); got != "1" {
		t.Fatalf("commits ahead = %s, want the one empty commit", got)
	}
}

func TestStageTimeoutOpensDraft(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 1m, stage: 1s, verify: 30s, finalize_reserve: 10s }", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockUntilDone)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 1s" || len(h.agent.calls) != 1 {
		t.Fatalf("record = %+v, calls = %d", rec, len(h.agent.calls))
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestCancelledRunStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, cancelThenBlock)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "cancelled during implement" {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

// TestSigtermWithoutMarkerSaysInterrupted: the execution is stopped (the
// runner's context is cancelled from outside) with no cancel marker. The
// reason says so, not "stage implement failed: context canceled"; the status
// stays what it was (failed).
func TestSigtermWithoutMarkerSaysInterrupted(t *testing.T) {
	h := newHarness(t, "", nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h.agent.steps = []step{func(t *testing.T, sctx context.Context, req agent.Request) (agent.Result, error) {
		stop()
		return blockUntilDone(t, sctx, req)
	}}
	rec, err := runner.Run(ctx, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "interrupted (execution stopped) during implement" {
		t.Fatalf("record = %+v", rec)
	}
	if strings.Contains(rec.Reason, "context canceled") {
		t.Fatalf("reason = %q", rec.Reason)
	}
}

// TestCancelNowSaysCancelled: cancel --now writes the marker and stops the
// execution at once, so the runner's context is cancelled before its poll
// reads the marker. The reason says the run was cancelled.
func TestCancelNowSaysCancelled(t *testing.T) {
	h := newHarness(t, "", nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h.agent.steps = []step{func(t *testing.T, sctx context.Context, req agent.Request) (agent.Result, error) {
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		stop()
		return blockUntilDone(t, sctx, req)
	}}
	rec, err := runner.Run(ctx, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Reason, "cancelled") || strings.Contains(rec.Reason, "context canceled") || strings.Contains(rec.Reason, "failed") {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Reason != "cancelled during implement" && rec.Reason != "cancelled (stopped by fugaro cancel --now) during implement" {
		t.Fatalf("reason = %q", rec.Reason)
	}
}

// TestAgentLoopPanicStillOpensDraft checks that a panicking stage does not
// skip finalize: the run must still push and open a draft PR.
func TestAgentLoopPanicStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	panicky := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		panic("boom")
	}
	rec, err := h.run(t, panicky)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || !strings.Contains(rec.Reason, "panicked") {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

// TestCancelSeenAfterLastStageStillOpensDraft checks that a cancel request
// noticed only after the agent loop has already returned successfully (no
// stage caught it) still turns the run into a cancelled draft, instead of
// being silently ignored.
func TestCancelSeenAfterLastStageStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	shipThenCancel := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := review("ship", 0)(t, ctx, req)
		if err != nil {
			return res, err
		}
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		// Give the background cancel watcher time to notice and mark
		// runCtx done before this stage (and the agent loop) returns.
		time.Sleep(5 * h.deps.CancelPoll)
		return res, nil
	}
	rec, err := h.run(t, implement("feature"), shipThenCancel)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "cancelled" {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestInvalidConfigIsInfraError(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: gitlab", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "git.provider") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || len(h.provider.State.PRs) != 0 {
		t.Fatalf("record = %+v", rec)
	}
	if stored, _ := h.store.ReadRecord(context.Background()); stored == nil || stored.Status != runstore.StatusInfraError {
		t.Fatalf("stored record = %+v", stored)
	}
}

func TestBootstrapRejections(t *testing.T) {
	t.Run("state dir inside checkout", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.StateDir = filepath.Join(h.deps.WorkDir, "state")
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "outside the checkout") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
	t.Run("parent of the checkout", func(t *testing.T) {
		h := newHarness(t, "", nil)
		marker := seedCheckoutMarker(t, h.deps.WorkDir)
		h.deps.StateDir = filepath.Dir(h.deps.WorkDir)
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "outside the checkout") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		assertMarkerSurvived(t, marker)
	})
	t.Run("..state sibling inside the checkout", func(t *testing.T) {
		h := newHarness(t, "", nil)
		marker := seedCheckoutMarker(t, h.deps.WorkDir)
		h.deps.StateDir = filepath.Join(h.deps.WorkDir, "..state")
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "outside the checkout") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		assertMarkerSurvived(t, marker)
	})
	t.Run("empty", func(t *testing.T) {
		h := newHarness(t, "", nil)
		marker := seedCheckoutMarker(t, h.deps.WorkDir)
		h.deps.StateDir = ""
		if rec, err := h.run(t); err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "must not be empty") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		assertMarkerSurvived(t, marker)
	})
	t.Run("missing secret", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.Env = filterEnv(h.deps.Env, "FIXTURE_FAILS_FILE")
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "FIXTURE_FAILS_FILE") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
}

// seedCheckoutMarker creates workDir with a marker file, simulating the
// checkout baked into the container image, and returns the marker's path.
func seedCheckoutMarker(t *testing.T, workDir string) string {
	t.Helper()
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

// assertMarkerSurvived fails the test if a bad state-dir guard let
// bootstrap's os.RemoveAll(StateDir) delete the checkout.
func assertMarkerSurvived(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("checkout marker did not survive: %v", err)
	}
}

// TestPRTextIsRedacted checks that an agent-written pr.md never publishes a
// secret value in the PR title or body.
func TestPRTextIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if werr := os.WriteFile(pr, []byte("# Add feature "+secret+"\n\nThe key is "+secret+"."), 0o644); werr != nil {
			t.Fatal(werr)
		}
		return res, err
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	pr := onlyPR(t, h.provider)
	if pr.Title != "Add feature [REDACTED]" || gitprov.StripStatus(pr.Body) != "The key is [REDACTED]." {
		t.Fatalf("PR text is not redacted: title %q, body %q", pr.Title, pr.Body)
	}
}

// TestAgentStderrIsRedacted checks that the agent's stderr, which the runner
// logs line by line, never carries a secret value in the clear.
func TestAgentStderrIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	var logs strings.Builder
	h.deps.Log = runner.NewLogger(&logs)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Stderr, "debug: using key %s\n", secret)
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("agent stderr leaks the secret into the log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "debug: using key [REDACTED]") {
		t.Fatalf("agent stderr line missing from the log: %s", logs.String())
	}
}

func TestAgentEventsRelayedRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	talk := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Transcript.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"my key is test-key"}]}}` + "\n"))
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, talk, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, `"event":"text"`) || !strings.Contains(out, "my key is [REDACTED]") || strings.Contains(out, "test-key") {
		t.Fatalf("logs = %s", out)
	}
}

// TestAgentToolEventsNeverLeakEscapedSecrets checks the relay behind the
// transcript redactor: a secret the agent's tool_use and tool_result carry
// \u-escaped slips past the byte-level redactor and must still be redacted
// once decoded, in the live log as it is in the transcript's view.
func TestAgentToolEventsNeverLeakEscapedSecrets(t *testing.T) {
	h := newHarness(t, "", nil)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	var esc strings.Builder
	for _, c := range []byte(secret) {
		fmt.Fprintf(&esc, `\u%04x`, c)
	}
	e := esc.String()
	talk := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for _, l := range []string{
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo ` + e + `"}}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"content":"KEY=` + e + `"}}]}}`,
			`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"denied: ` + e + `"}]}}`,
			`{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"ANTHROPIC_API_KEY=` + secret + `"}]}}`,
		} {
			_, _ = req.Transcript.Write([]byte(l + "\n"))
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, talk, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if strings.Contains(out, secret) || strings.Contains(out, e) || strings.Contains(out, strings.ReplaceAll(e, `\`, `\\`)) {
		t.Fatalf("log leaks the secret: %s", out)
	}
	for _, want := range []string{"tool Bash: echo [REDACTED]", `tool Write: {\"content\":\"KEY=[REDACTED]\"}`, "tool error: denied: [REDACTED]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q: %s", want, out)
		}
	}
}

// TestAgentBashTimeoutCoversVerify checks that the agent's Bash tool may run
// `fugaro verify` for the whole verify timeout (1m in the fixture) plus
// slack, instead of Claude Code's default 2-minute cap cutting it short.
func TestAgentBashTimeoutCoversVerify(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	env := h.agent.calls[0].Env
	for _, k := range []string{"BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS"} {
		if got := envValue(env, k); got != "180000" {
			t.Errorf("%s = %q, want 180000 (timeouts.verify 1m + 2m)", k, got)
		}
	}
}

// TestStateDirKeepsForeignFiles checks that bootstrap clears only Fugaro's
// own entries from an operator-chosen state dir, leaving anything else there
// alone, while stale verify records and pr.md from an earlier run are gone.
func TestStateDirKeepsForeignFiles(t *testing.T) {
	h := newHarness(t, "", nil)
	testutil.WriteFiles(t, h.deps.StateDir, map[string]string{
		"operator-notes.txt": "keep me\n",
		"verify/0001.json":   `{"n":1,"kind":"test","passed":true}`,
		"pr.md":              "# Stale title\n",
	})
	nothing := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if _, err := os.Stat(filepath.Join(h.deps.StateDir, "pr.md")); !os.IsNotExist(err) {
			t.Errorf("stale pr.md survived bootstrap: %v", err)
		}
		recs, err := verify.Records(h.deps.StateDir)
		if err != nil || len(recs) != 0 {
			t.Errorf("stale verify records survived bootstrap: %+v, %v", recs, err)
		}
		return agent.Result{}, nil
	}
	if _, err := h.run(t, nothing, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(h.deps.StateDir, "operator-notes.txt")); err != nil || string(data) != "keep me\n" {
		t.Fatalf("bootstrap deleted an unrelated file in the state dir: %q, %v", data, err)
	}
}

// TestStateDirSymlinkIntoCheckoutRejected checks that the state-dir guard
// resolves symlinks, so a state dir that is really inside the checkout is
// refused even though its path looks outside.
func TestStateDirSymlinkIntoCheckoutRejected(t *testing.T) {
	h := newHarness(t, "", nil)
	seedCheckoutMarker(t, h.deps.WorkDir)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(h.deps.WorkDir, link); err != nil {
		t.Fatal(err)
	}
	for _, sd := range []string{link, filepath.Join(link, "state")} {
		h.deps.StateDir = sd
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "outside the checkout") {
			t.Fatalf("state dir %s: rec = %+v, err = %v", sd, rec, err)
		}
	}
}

// TestVerifyRecordsUploaded checks that finalize stores each verify record
// as runs/<slug>/<id>/verify/<n>.json (design §3.3), not only inside
// result.json.
func TestVerifyRecordsUploaded(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	data, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"verify/1.json")
	if err != nil {
		t.Fatal(err)
	}
	var got verify.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.N != 1 || got.Kind != verify.KindTest || got.HeadSHA != rec.Verify[0].HeadSHA || !got.Passed {
		t.Fatalf("uploaded verify record = %+v, want %+v", got, rec.Verify[0])
	}
}

func TestCostBreakdown(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Prices = &backend.Prices{VCPUSecondUSD: 0.001, GiBSecondUSD: 0}
	// The clock moves only when a stage says so, 30s per stage, well inside
	// the fixture's 5m total, so the budget can never run out under the test.
	// It starts at the real time because stage deadlines become real
	// context deadlines; a fixed past date would expire them at once.
	var mu sync.Mutex
	clock := time.Now()
	h.deps.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	tick := func(s step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			mu.Lock()
			clock = clock.Add(30 * time.Second)
			mu.Unlock()
			return s(t, ctx, req)
		}
	}
	rec, err := h.run(t, tick(implement("feature")), tick(review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	c := rec.Cost
	// 60s × 4 vCPU × $0.001 = $0.24 of compute; totals are compared to the cent.
	if c == nil || !c.ComputeEstimated || c.ModelUSD != 1.5 || c.ModelBasis != "api-list" || math.Abs(c.ComputeUSD-0.24) > 1e-9 || math.Abs(c.TotalUSD-1.74) > 0.005 {
		t.Fatalf("cost = %+v", c)
	}
	if rec.CostUSD != c.ModelUSD {
		t.Fatalf("cost_usd %v must equal the model sum %v", rec.CostUSD, c.ModelUSD)
	}
	if !strings.Contains(onlyPR(t, h.provider).Comments[0], "(model $1.50 + compute $") {
		t.Fatalf("report = %s", onlyPR(t, h.provider).Comments[0])
	}
}

func TestCostWithoutPricesIsNotEstimated(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if c := rec.Cost; c == nil || c.ComputeEstimated || c.ComputeUSD != 0 || c.ModelUSD != 1.5 {
		t.Fatalf("cost = %+v", rec.Cost)
	}
	if report := onlyPR(t, h.provider).Comments[0]; !strings.Contains(report, "**Cost:** model $1.50 (compute not estimated)") {
		t.Fatalf("report = %s", report)
	}
}

// A run reports what its image was built from: the baked checkout's HEAD
// as the run found it, before the sync, and the build time from the image's
// own record.
func TestRunnerRecordsImageAge(t *testing.T) {
	h := newHarness(t, "", nil)
	testutil.Git(t, filepath.Dir(h.deps.WorkDir), "clone", "--quiet", h.remote, h.deps.WorkDir)
	baked := testutil.Git(t, h.deps.WorkDir, "rev-parse", "HEAD")
	// The remote moves on after the image was built; the run syncs to it.
	pusher := filepath.Join(t.TempDir(), "pusher")
	testutil.Git(t, filepath.Dir(pusher), "clone", "--quiet", h.remote, pusher)
	testutil.Git(t, pusher, "commit", "--quiet", "--allow-empty", "-m", "later")
	testutil.Git(t, pusher, "push", "--quiet", "origin", "HEAD:main")
	builtAt := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	h.deps.ImageInfoPath = filepath.Join(t.TempDir(), "image.json")
	if err := os.WriteFile(h.deps.ImageInfoPath, []byte(`{"built_at":"`+builtAt.Format(time.RFC3339)+`","commit":"ffffffff"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v", rec)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Image == nil || stored.Image.BakedCommit != baked || stored.Image.BuiltAt == nil || !stored.Image.BuiltAt.Equal(builtAt) {
		t.Fatalf("stored image = %+v, %v (baked %s)", stored.Image, err, baked)
	}
	if stored.Image.BakedCommit == rec.HeadSHA {
		t.Fatalf("the baked commit is the run's own head %s", rec.HeadSHA)
	}
}

// Neither the image's record nor a baked checkout is needed: the run
// succeeds and reports no image block.
func TestRunnerImageAgeOptional(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.ImageInfoPath = filepath.Join(t.TempDir(), "missing.json")
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Image != nil || stored.Image != nil {
		t.Fatalf("record = %+v, stored image = %+v, %v", rec, stored.Image, err)
	}
	if strings.Contains(logs.String(), "build record") {
		t.Fatalf("a missing record was logged as a problem: %s", logs.String())
	}

	// A record that can't be used is a warning, not a failure.
	h = newHarness(t, "", nil)
	h.deps.ImageInfoPath = filepath.Join(t.TempDir(), "image.json")
	if err := os.WriteFile(h.deps.ImageInfoPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	h.deps.Log = runner.NewLogger(&logs)
	rec, err = h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Image != nil {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if !strings.Contains(logs.String(), "the image's build record is not valid") {
		t.Fatalf("no warning: %s", logs.String())
	}
}
