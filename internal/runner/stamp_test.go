package runner_test

// The stamp a run's commits carry (A3, D601-D603): the author and the
// Fugaro-* trailers, written when the commit is made. Every test here runs a
// real run over a real checkout, because what is being pinned is what git
// stores in the commit.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// trailersOf reads the trailer lines of a commit message the way these tests
// ask about them: keyed by the lower-cased key git matches by, keeping every
// value, so a duplicate is two entries and a replacement is one.
func trailersOf(msg string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(msg, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			continue
		}
		if strings.ContainsFunc(key, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
		}) {
			continue
		}
		out[strings.ToLower(key)] = append(out[strings.ToLower(key)], strings.TrimSpace(value))
	}
	return out
}

// branchCommit is the message of the commit a run's branch tip holds.
func branchCommit(t *testing.T, h *harness) string {
	t.Helper()
	return testutil.Git(t, h.remote, "log", "-1", "--format=%B", "fugaro/"+runID)
}

// configPairs reads the environment configuration entries an environment
// carries: every GIT_CONFIG_KEY_n with the GIT_CONFIG_VALUE_n it is numbered
// against. The namespace is shared by everything an environment configures —
// the credential helper's two entries (gitops.CredentialVars) and the stamp's
// core.hooksPath among them (stamp.go) — which is why a test that asks about
// one of them asks about its key and value and never about the namespace.
func configPairs(env []string) [][2]string {
	values := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if i, ok := strings.CutPrefix(k, "GIT_CONFIG_VALUE_"); ok {
				values[i] = v
			}
		}
	}
	var out [][2]string
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		i, ok := strings.CutPrefix(k, "GIT_CONFIG_KEY_")
		if !ok {
			continue
		}
		out = append(out, [2]string{v, values[i]})
	}
	return out
}

// credentialConfigFindings names the environment entries that configure a git
// credential, or that hold one of the secrets: what a test asking whether an
// agent can reach a credential is about.
func credentialConfigFindings(env []string, secrets ...string) []string {
	var out []string
	for _, kv := range env {
		for _, s := range secrets {
			if s != "" && strings.Contains(kv, s) {
				out = append(out, kv)
			}
		}
	}
	for _, pair := range configPairs(env) {
		if strings.Contains(strings.ToLower(pair[0]+" "+pair[1]), "credential") {
			out = append(out, pair[0]+"="+pair[1])
		}
	}
	return out
}

// credentialHelperConfigured reports whether env configures a git credential
// helper at all.
func credentialHelperConfigured(env []string) bool {
	for _, pair := range configPairs(env) {
		if strings.HasPrefix(pair[0], "credential.") && strings.HasSuffix(pair[0], ".helper") {
			return true
		}
	}
	return false
}

// hooksPath is the core.hooksPath a stage's environment configures, "" when it
// configures none.
func hooksPath(env []string) string {
	keys, values := map[string]string{}, map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if i, ok := strings.CutPrefix(k, "GIT_CONFIG_KEY_"); ok {
			keys[i] = v
		}
		if i, ok := strings.CutPrefix(k, "GIT_CONFIG_VALUE_"); ok {
			values[i] = v
		}
	}
	for i, k := range keys {
		if k == "core.hooksPath" {
			return values[i]
		}
	}
	return ""
}

// TestRunnerStampsAgentCommits: the commit the agent makes carries the run's
// trailers as it is made — the run ID and the rigor mode — and nothing
// rewrites it afterwards (D603, constraint 1).
//
// Mutation (drop the stamp from the stage's environment): the commit the
// branch tip holds carries no Fugaro-Run trailer.
func TestRunnerStampsAgentCommits(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	got := trailersOf(branchCommit(t, h))
	if want := []string{runID}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the agent's commit is not stamped", got["fugaro-run"], want)
	}
	if rec.Rigor == nil {
		t.Fatal("the run recorded no rigor")
	}
	if want := []string{string(rec.Rigor.Mode)}; !slices.Equal(got["fugaro-rigor"], want) {
		t.Errorf("fugaro-rigor = %v, want %v", got["fugaro-rigor"], want)
	}
}

// TestRunnerVerifiedCommitIsTheStampedCommit: what the run verified, what it
// recorded as its head and what it pushed are the same commit, and that commit
// is stamped. A stamp that arrived by rewriting the commit after verification
// would leave the verified commit unstamped and the pushed one unverified
// (D603).
func TestRunnerVerifiedCommitIsTheStampedCommit(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	records, err := verify.Records(h.deps.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("the run recorded no verification")
	}
	if last := records[len(records)-1]; last.HeadSHA != rec.HeadSHA {
		t.Fatalf("the verified commit is %s, the run's head is %s", last.HeadSHA, rec.HeadSHA)
	}
	tip := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID)
	if tip != rec.HeadSHA {
		t.Fatalf("the pushed commit is %s, the run's head is %s", tip, rec.HeadSHA)
	}
	if got := trailersOf(testutil.Git(t, h.remote, "log", "-1", "--format=%B", tip)); !slices.Equal(got["fugaro-run"], []string{runID}) {
		t.Errorf("fugaro-run = %v, want %v: the verified commit is not the stamped one", got["fugaro-run"], []string{runID})
	}
}

// TestRunnerStampedCommitsReachTheCheckpointPush: a commit made mid-stage is
// stamped where it is made, so the copy a checkpoint push puts on the remote
// while the stage is still running carries the trailers too.
//
// Mutation (make the checkpoint push rewrite the commit it pushes): the tip on
// the remote is no longer the commit the stage made.
func TestRunnerStampedCommitsReachTheCheckpointPush(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	var pushed string
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.settle()
		pushed = remoteTip(t, g.harness)
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if pushed == "" {
		t.Fatal("the stage's committed work was never pushed")
	}
	got := trailersOf(testutil.Git(t, g.remote, "log", "-1", "--format=%B", pushed))
	if !slices.Equal(got["fugaro-run"], []string{runID}) {
		t.Errorf("fugaro-run = %v, want %v: the checkpoint-pushed commit is not stamped", got["fugaro-run"], []string{runID})
	}
}

// TestRunnerDefaultAuthorUnchanged is the deliberate NON-change: with no
// git.author in fugaro.yaml — the key absent, which is the same as fugaro —
// the commits are authored and committed by Fugaro's own identity (D604, D645).
//
// Mutation (stamp the author whenever Spec.Author is set, whatever the
// configuration says): this test fails with the launcher's name in the
// author line.
func TestRunnerDefaultAuthorUnchanged(t *testing.T) {
	h := newHarness(t, "", authorSpec())
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	who := testutil.Git(t, h.remote, "log", "-1", "--format=%an <%ae>|%cn <%ce>", "fugaro/"+runID)
	const fugaro = "Fugaro <fugaro@users.noreply.invalid>"
	if want := fugaro + "|" + fugaro; who != want {
		t.Errorf("the commit is by %q, want %q", who, want)
	}
}

// TestRunnerAuthorOptIn: with git.author: launcher and a task that names an
// author, the agent's commits are authored by that person and committed by the
// run (D645, D604: the author is a claim the launcher made, which nothing
// verified and no account owns).
//
// Mutation (drop StampEnv's GIT_AUTHOR_* entries): the author is Fugaro's own
// identity and this test fails.
func TestRunnerAuthorOptIn(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"  base_branch: main\n", "  base_branch: main\n  author: launcher\n", 1)
	h := newHarness(t, cfg, authorSpec())
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	who := testutil.Git(t, h.remote, "log", "-1", "--format=%an <%ae>|%cn <%ce>", "fugaro/"+runID)
	if want := "person-1 <person-1@example.invalid>|Fugaro <fugaro@users.noreply.invalid>"; who != want {
		t.Errorf("the commit is by %q, want %q", who, want)
	}
}

// authorSpec is a task whose launcher named an author: the run's commits are
// authored by that person when the repository opted in, whoever filled the
// field (A2b).
func authorSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Add a feature", Author: &task.Person{Name: "person-1", Email: "person-1@example.invalid"}}
}

// TestRunnerModelTrailerIsTheStagePin: Fugaro-Model holds the model the stage
// was configured with — the pin, which is what a reader of the log can check —
// and not the model that happened to serve it (D601).
//
// Mutation (stamp the model from the agent's result instead of the
// configuration): the trailer is empty or names another model.
func TestRunnerModelTrailerIsTheStagePin(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"  auth: api-key\n", "  auth: api-key\n  model: "+sonnet+"\n", 1)
	h := newHarness(t, cfg, nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	got := trailersOf(branchCommit(t, h))
	if want := []string{sonnet}; !slices.Equal(got["fugaro-model"], want) {
		t.Errorf("fugaro-model = %v, want %v", got["fugaro-model"], want)
	}
}

// TestRunnerModelTrailerOmittedWithoutPin: a stage that pinned no model gets
// no Fugaro-Model trailer at all, rather than one holding the model the
// gateway routed to (D602).
func TestRunnerModelTrailerOmittedWithoutPin(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if got := trailersOf(branchCommit(t, h))["fugaro-model"]; len(got) != 0 {
		t.Errorf("fugaro-model = %v, want none: the fixture pins no model", got)
	}
}

// TestRunnerOwnCommitsCarryTrailers: the commits the runner itself makes — a
// report run's summary, an autofix, the uncommitted work finalize commits —
// carry the run's trailers too, and no Fugaro-Model, because no stage pinned
// the model that made them (D602).
//
// It is the noHooks follow-up of #1312's review: `-c core.hooksPath=/dev/null`
// on every runner-owned git call outranks the stamp's environment, so without
// a hook directory of its own the runner's own commits were the unstamped
// ones.
//
// Mutation (CommitAll back on the noHooks path): the tip carries no trailer.
func TestRunnerOwnCommitsCarryTrailers(t *testing.T) {
	h := newHarness(t, "", nil)
	dirty := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(req.Dir, "left.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return res, err
	}
	if _, err := h.run(t, dirty, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	msg := branchCommit(t, h)
	if !strings.Contains(msg, "uncommitted work at finalize") {
		t.Fatalf("the tip is not the runner's own leftover commit: %q", msg)
	}
	got := trailersOf(msg)
	if want := []string{runID}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the runner's own commit is not stamped", got["fugaro-run"], want)
	}
	if got := got["fugaro-model"]; len(got) != 0 {
		t.Errorf("fugaro-model = %v, want none: no stage pinned the model that made this commit", got)
	}
}

// TestRunnerLogsUnstampedCommitCount: finalize says how many of the run's own
// commits carry no Fugaro-Run trailer, which is the only way an installation
// notices a stamp that silently stopped working (D603).
//
// Mutation (drop logUnstampedCommits from finalize): the line is absent.
func TestRunnerLogsUnstampedCommitCount(t *testing.T) {
	h := newHarness(t, "", nil)
	logs := &bytes.Buffer{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	// Two commits, the second made with the hooks off, as an agent that
	// bypasses the stamp leaves the branch: the count is what a reader of the
	// log reconciles against the branch itself.
	half := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo feature > feature.txt && git add -A && git commit -qm 'Add feature'")
		shell(t, req, "git -c core.hooksPath=/dev/null commit -q --allow-empty -m 'No stamp'")
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add feature\n\nAdds feature.txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	if _, err := h.run(t, half, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, want := range []string{"commits=2", "unstamped=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the finalize log does not say %s:\n%s", want, out)
		}
	}
}

// TestRunnerReviewAndArtifactNeverStamp: neither a review run nor an artifact
// run stamps. Neither pushes a branch of its own, so a stamp hook directory
// and the trailers that go with it would be machinery a run that makes no
// commit the repository sees has no use for.
//
// Mutation (drop the `!r.review && !r.artifact` guard from the bootstrap call):
// the stage environment carries the stamp.
func TestRunnerReviewAndArtifactNeverStamp(t *testing.T) {
	assertNoStamp := func(t *testing.T, env []string) {
		t.Helper()
		if got := hooksPath(env); got != "" {
			t.Errorf("the stage environment points core.hooksPath at %s", got)
		}
		for _, k := range []string{"FUGARO_STAMP_RUN", "FUGARO_STAMP_RIGOR", "FUGARO_STAMP_MODEL"} {
			if got := envValue(env, k); got != "" {
				t.Errorf("the stage environment carries %s=%s", k, got)
			}
		}
	}
	t.Run("review", func(t *testing.T) {
		h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
		pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
		})
		addPR(h, 7, "someone-elses-change", "")
		var env []string
		rev := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			env = req.Env
			return review("changes", 1)(t, ctx, req)
		}
		if _, err := h.run(t, rev); err != nil {
			t.Fatal(err)
		}
		if env == nil {
			t.Fatal("the review stage never ran")
		}
		assertNoStamp(t, env)
	})
	t.Run("artifact", func(t *testing.T) {
		h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
		var env []string
		plan := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			env = req.Env
			return planStage(t, ctx, req)
		}
		if _, err := h.run(t, plan); err != nil {
			t.Fatal(err)
		}
		if env == nil {
			t.Fatal("the plan stage never ran")
		}
		assertNoStamp(t, env)
	})
}

// TestStampHookDirOutsideWorktree: the hook directory a run writes its stamp
// into is outside the working tree and outside the state directory, because
// the working tree is the run's agent's to write (constraint 8, follow-up 3):
// a hook a commit runs from a path the agent can rewrite is a commit stamp the
// agent decides the content of.
//
// Mutation (write the hooks into r.d.WorkDir or the state directory): this
// test fails naming the directory.
func TestStampHookDirOutsideWorktree(t *testing.T) {
	h := newHarness(t, "", nil)
	var hooks string
	capture := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		hooks = hooksPath(req.Env)
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, capture, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if hooks == "" {
		t.Fatal("the stage environment names no hook directory")
	}
	if !filepath.IsAbs(hooks) {
		t.Errorf("the hook directory %s is not absolute", hooks)
	}
	for _, root := range []string{h.deps.WorkDir, h.deps.StateDir} {
		rel, err := filepath.Rel(root, hooks)
		if err == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("the hook directory %s is inside %s", hooks, root)
		}
	}
	if _, err := os.Stat(filepath.Join(hooks, "prepare-commit-msg")); err != nil {
		t.Fatalf("the stamp hook is not in %s: %v", hooks, err)
	}
}

// TestStampHooksNameTheResolvedChain: the pass-throughs name the repository's
// own hook directory as it was resolved once at bootstrap, absolute, and do
// not look it up again — twenty-one pass-throughs each spawning a git of their
// own is the cost the resolved chain is there to avoid (follow-up 4).
//
// Mutation (write the chain as the empty string again): the hook resolves the
// directory at the commit, `rev-parse --git-path hooks` is in the script, and
// this test fails.
func TestStampHooksNameTheResolvedChain(t *testing.T) {
	h := newHarness(t, "", nil)
	var hooks string
	capture := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		hooks = hooksPath(req.Env)
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, capture, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if want := "fugaro_chain=" + filepath.Join(h.deps.WorkDir, ".git", "hooks") + "\n"; !strings.Contains(script, want) {
		t.Errorf("the pass-through does not name the resolved hook directory %q:\n%s", want, script)
	}
	if !strings.Contains(script, "fugaro_chain_later=0\n") {
		t.Errorf("the pass-through looks the repository's hook directory up at the commit:\n%s", script)
	}
}

// TestRunnerHookDirIsNotDerivableFromTheStageEnv: the agent's environment
// names the hook directory its own commits run under, so a runner-owned commit
// must not run under a directory the agent can derive from it. It plants a
// prepare-commit-msg exactly where an agent would put one — beside the
// directory its own core.hooksPath names — and the leftover commit finalize
// makes must not run it: the committed work is the runner's, and the runner's
// own commits run no code of the agent's (A3, follow-up 2a).
//
// Mutation (write the runner's hooks beside the agent's, under one temporary
// root): the planted hook runs inside the runner's own commit and this test
// fails with its marker file present.
func TestRunnerHookDirIsNotDerivableFromTheStageEnv(t *testing.T) {
	h := newHarness(t, "", nil)
	marker := filepath.Join(t.TempDir(), "ran")
	var hooks string
	dirty := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		hooks = hooksPath(req.Env)
		planted := filepath.Join(filepath.Dir(hooks), "runner")
		if err := os.MkdirAll(planted, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(planted, "prepare-commit-msg"), []byte("#!/bin/sh\necho ran >> "+marker+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := implement("feature")(t, ctx, req)
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(req.Dir, "left.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return res, err
	}
	if _, err := h.run(t, dirty, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a runner-owned commit ran a hook planted beside the directory the agent's own environment names: %v", err)
	}
}

// TestRunnerOwnCommitsCarryTheRigorOfTheirMoment: the Fugaro-Rigor of a
// commit is the mode the run had reached when the commit was made, for the
// runner's own commits as for the agent's (D601: the record's Rigor.Mode is
// the run's mode). The run here raises to Critical at the round moment — the
// fix's own commit adds to the file the area rule matches, and finalize
// resolves the same mode and leaves the block as the round left it — so the
// leftover commit finalize makes must carry Critical, and not the mode the run
// started at.
//
// Mutation (read the stamp once at bootstrap, as Repo.CommitStamp holding a
// Stamp did): the leftover commit carries Startup while the record says
// Critical.
func TestRunnerOwnCommitsCarryTheRigorOfTheirMoment(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"rigor:\n  default: startup\n  areas:\n    - { name: app, match: \"feature.txt\", mode: critical }\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review' && echo x > left.txt")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.At != runstore.RigorAtRound || string(rec.Rigor.Mode) != "critical" {
		t.Fatalf("the run's mode = %+v, want critical decided at the round: without a raise before finalize this test proves nothing", rec.Rigor)
	}
	msg := branchCommit(t, h)
	if !strings.Contains(msg, "uncommitted work at finalize") {
		t.Fatalf("the tip is not the runner's own leftover commit: %q", msg)
	}
	got := trailersOf(msg)["fugaro-rigor"]
	if want := []string{string(rec.Rigor.Mode)}; !slices.Equal(got, want) {
		t.Errorf("fugaro-rigor = %v, want %v: the runner's own commit carries the mode of another moment", got, want)
	}
}
