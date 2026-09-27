package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

const runID = "20260926-221530-abcd"

type step func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error)

type scriptedAgent struct {
	t     *testing.T
	steps []step
	calls []agent.Request
}

func (a *scriptedAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.calls = append(a.calls, req)
	i := len(a.calls) - 1
	if i >= len(a.steps) {
		a.t.Errorf("unexpected agent call #%d", i+1)
		return agent.Result{}, errors.New("unexpected call")
	}
	return a.steps[i](a.t, ctx, req)
}

type harness struct {
	deps      runner.Deps
	store     *runstore.Store
	bucket    *blob.Bucket
	provider  *fake.Provider
	agent     *scriptedAgent
	remote    string
	failsFile string
}

// newHarness seeds a remote with the fixture repo (cfg replaces fugaro.yaml
// when non-empty) and stores a task spec.
func newHarness(t *testing.T, cfg string, spec *task.Spec) *harness {
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
		spec = &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature"}
	}
	store := runstore.Open(bucket, "acme-app", spec.RunID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	failsFile := filepath.Join(tmp, "fails")
	a := &scriptedAgent{t: t}
	p := &fake.Provider{}
	return &harness{
		deps: runner.Deps{
			Store: store, Provider: p, Agent: a,
			WorkDir: filepath.Join(tmp, "work"), Remote: remote, StateDir: filepath.Join(tmp, "state"),
			Env:        []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "ANTHROPIC_API_KEY=test-key", "FIXTURE_FAILS_FILE=" + failsFile},
			CancelPoll: 20 * time.Millisecond,
		},
		store: store, bucket: bucket, provider: p, agent: a, remote: remote, failsFile: failsFile,
	}
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
	if pr.Draft || pr.Spec.Title != "Add feature" || pr.Spec.Body != "Adds feature.txt." || pr.Spec.Base != "main" || pr.Spec.Branch != "fugaro/"+runID {
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
	if rec.Reason != "no verified test run on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "fugaro: uncommitted work at finalize" {
		t.Fatalf("last pushed commit = %q", got)
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
	t.Run("follow-up", func(t *testing.T) {
		spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Branch: "fugaro/20260925-000000-0000", PR: 3, PreviousRun: "20260925-000000-0000"}
		h := newHarness(t, "", spec)
		if rec, err := h.run(t); err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "follow-up") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
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
