package runner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// testAndFixRecipe is a repo recipe with one check step (command: test, no
// autofix) followed by the mandatory review step.
const testAndFixRecipe = "version: 1\nname: test-and-fix\nsteps:\n  - check: { command: test }\n  - review: {}\n"

// lintFixRecipe is a repo recipe with one check step (command: lint,
// autofix: true) followed by the mandatory review step.
const lintFixRecipe = "version: 1\nname: lint-fix\nsteps:\n  - check: { command: lint, autofix: true }\n  - review: {}\n"

// lintOnlyRecipe is a repo recipe with one check step (command: lint, no
// autofix) followed by the mandatory review step.
const lintOnlyRecipe = "version: 1\nname: lint-only\nsteps:\n  - check: { command: lint }\n  - review: {}\n"

// yamlQuote double-quotes s for a YAML scalar, so a shell command with
// metacharacters (";", "|", ...) is always read as a single string value.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// gateCfg is the fixture config with agent.recipe set to recipeName,
// agent.review_rounds to rounds, and commands.test, commands.lint and
// commands.fix replaced (each left out, or at the fixture's own value for
// test, when ""): the check-gate tests' config.
func gateCfg(t *testing.T, recipeName string, rounds int, test, lint, fix string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	out := strings.Replace(cfg, "  review_rounds: 2\n",
		"  recipe: "+recipeName+"\n  review_rounds: "+string(rune('0'+rounds))+"\n", 1)
	if out == cfg {
		t.Fatal("fixture has no agent.review_rounds line")
	}
	if test != "" {
		next := strings.Replace(out, "      test: sh test.sh\n", "      test: "+yamlQuote(test)+"\n", 1)
		if next == out {
			t.Fatal("fixture has no commands.test line")
		}
		out = next
	}
	var extra string
	if lint != "" {
		extra += "      lint: " + yamlQuote(lint) + "\n"
	}
	if fix != "" {
		extra += "      fix: " + yamlQuote(fix) + "\n"
	}
	if extra != "" {
		next := strings.Replace(out, "      rerun_failed:\n", extra+"      rerun_failed:\n", 1)
		if next == out {
			t.Fatal("fixture has no commands.rerun_failed line")
		}
		out = next
	}
	return out
}

// gateFiles is the repo file map for a single named recipe.
func gateFiles(name, text string) map[string]string {
	return map[string]string{".fugaro/recipes/" + name + ".yaml": text}
}

// lastCommitMsg is ref's latest commit subject in remote.
func lastCommitMsg(t *testing.T, remote, ref string) string {
	t.Helper()
	return testutil.Git(t, remote, "log", "--format=%s", "-n", "1", ref)
}

// hasBranch reports whether remote has a branch named ref.
func hasBranch(t *testing.T, remote, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", remote, "show-ref", "--verify", "--quiet", "refs/heads/"+ref)
	return cmd.Run() == nil
}

// TestGateCleanOpensNoPR: a check-and-fix recipe on a green repository ends
// at once: succeeded, outcome none, gate "clean", no push, no PR, no agent
// stage.
func TestGateCleanOpensNoPR(t *testing.T) {
	cfg := gateCfg(t, "test-and-fix", 2, "", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone || rec.Gate != "clean" {
		t.Fatalf("record = %+v", rec)
	}
	if len(stageNames(rec)) != 0 || len(h.provider.State.PRs) != 0 || hasBranch(t, h.remote, "fugaro/"+runID) {
		t.Fatalf("a clean gate started a stage, opened a PR or pushed (stages=%v, prs=%+v)", stageNames(rec), h.provider.State.PRs)
	}
}

// TestGateRedRunsImplementWithTheFailure: a failing commands.test reaches
// the implement prompt as untrusted output, redacted, and the loop goes on
// as standard.
func TestGateRedRunsImplementWithTheFailure(t *testing.T) {
	// The fixture harness always sets ANTHROPIC_API_KEY=test-key (see
	// newHarnessFiles); it is registered as a secret during bootstrap,
	// before the gate ever runs.
	const secret = "test-key"
	cfg := gateCfg(t, "test-and-fix", 1, "echo FAIL-MARKER "+secret+"; exit 1", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("implement never ran")
	}
	p := h.agent.calls[0].Prompt
	if !strings.Contains(p, "FAIL-MARKER") || !strings.Contains(p, "untrusted output of commands.test") {
		t.Fatalf("implement prompt = %q", p)
	}
	if strings.Contains(p, secret) {
		t.Fatalf("implement prompt leaked the secret unredacted: %q", p)
	}
}

// TestGateCommandsComeFromTheRef: the gate runs the fugaro.yaml at the
// task's ref, never anything from the task (a project recipe cannot carry a
// command at all: recipe.CheckCommand only ever names a commands.* key,
// Task 8).
func TestGateCommandsComeFromTheRef(t *testing.T) {
	cfg := gateCfg(t, "test-and-fix", 2, "touch ran-ref-test", "", "")
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "commands.test: touch ran-task-text"}
	h := newHarnessFiles(t, cfg, spec, gateFiles("test-and-fix", testAndFixRecipe))
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "ran-ref-test")); err != nil {
		t.Fatal("the gate did not run the ref's commands.test")
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "ran-task-text")); err == nil {
		t.Fatal("the gate ran something other than the ref's commands.test")
	}
}

// TestLintAutofixCommitsAndSkipsImplement: lint-fix where fix makes lint
// pass: one autofix commit, no implement stage, the runner's own verify
// test on that commit, then the review.
func TestLintAutofixCommitsAndSkipsImplement(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "touch fixed")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "review") {
		t.Fatalf("stages = %v", got)
	}
	if lastCommitMsg(t, h.remote, "fugaro/"+runID) != "fugaro: autofix (commands.fix)" {
		t.Fatalf("branch log head = %q", lastCommitMsg(t, h.remote, "fugaro/"+runID))
	}
	if rec.Gate != "fixed" || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
}

// fixTrackedFile is a fix step for the Fixed-gate path: it modifies "fixed",
// the file autofix already committed (so `git commit -am` has something
// tracked to stage), rather than fixStep's "feature.txt", which only
// implement ever creates.
func fixTrackedFile(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo more >> fixed && git commit -qam 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

// TestLintAutofixReviewChangesStartsFreshFix: on the Fixed-gate path (no
// implement stage ever ran, so there is no session for fix to resume), a
// senior review that asks for changes must not try to resume a session
// that was never started (against the real agent binary, Resume: true on
// such a session raises agent.ErrNoSession, which fix's stage call has no
// Recoverable handling for, so the run would hard-fail instead of
// recovering).
func TestLintAutofixReviewChangesStartsFreshFix(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 2, "true", "test -f fixed", "touch fixed")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, review("changes", 1), fixTrackedFile, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "review", "fix", "review") {
		t.Fatalf("stages = %v", got)
	}
	fixReq := h.agent.calls[1]
	if fixReq.Resume || fixReq.SessionID == "" {
		t.Fatalf("fix resumed a session the gate never started: %+v", fixReq)
	}
	if rec.Gate != "fixed" || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateMissingCommandFailsTheRun: a check: {command: lint} on a
// repository with no commands.lint ends the run as infra_error at
// bootstrap; it never silently skips.
func TestGateMissingCommandFailsTheRun(t *testing.T) {
	cfg := gateCfg(t, "lint-only", 2, "", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-only", lintOnlyRecipe))
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the recipe's check step needs commands.lint in fugaro.yaml at main") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || len(h.provider.State.PRs) != 0 {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateAutofixNeedsFixCommand: autofix: true on a repository with
// commands.lint but no commands.fix ends the run as infra_error at
// bootstrap; it never silently runs an empty fix and reports "clean".
func TestGateAutofixNeedsFixCommand(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 2, "", "test -f fixed", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the recipe's check step has autofix: true but no commands.fix") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || len(h.provider.State.PRs) != 0 {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateCommandWhitespaceOnlyIsRefused: commands.lint set to only
// whitespace is the same as empty; it is not config.Validate's own
// "required" check (commands.lint has none), so this specifically proves
// checkGateCommands trims before comparing.
func TestGateCommandWhitespaceOnlyIsRefused(t *testing.T) {
	cfg := gateCfg(t, "lint-only", 2, "", "   ", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-only", lintOnlyRecipe))
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the recipe's check step needs commands.lint in fugaro.yaml at main") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateAutofixWhitespaceOnlyFixIsRefused: commands.fix set to only
// whitespace is the same as empty for autofix: true; config.Validate's own
// "fix needs lint" rule never fires for it (TrimSpace(fix) == "" there
// too), so this specifically proves checkGateCommands' own autofix check
// trims before comparing.
func TestGateAutofixWhitespaceOnlyFixIsRefused(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 2, "", "test -f fixed", "   ")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the recipe's check step has autofix: true but no commands.fix") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateCancelEndsCancelledNotInfraError: a cancel landing while the gate
// runs a long check command ends the run cancelled, not infra_error: the
// context error verify.Run/procgroup.Run returns when killed by a cancel
// looks, to the gate, like any other process failure, so the cause
// (ErrCancelled) is what tells it apart.
func TestGateCancelEndsCancelledNotInfraError(t *testing.T) {
	cfg := gateCfg(t, "test-and-fix", 2, "touch gate-cancel-marker && sleep 2", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	done := make(chan struct{})
	go func() {
		defer close(done)
		marker := filepath.Join(h.deps.WorkDir, "gate-cancel-marker")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				_ = h.store.RequestCancel(context.Background())
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	rec, err := h.run(t)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateFixTimeoutIsDiscardedAndRed: commands.fix that runs longer than
// the workflow's timeouts.verify is killed; its changes are never
// committed, the check command is skipped (there is nothing meaningful to
// judge), the step ends red (reaching implement, with a plain explanation),
// and the run is never infra_error over it.
func TestGateFixTimeoutIsDiscardedAndRed(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "sleep 5")
	cfg = strings.Replace(cfg, "verify: 1m", "verify: 300ms", 1)
	if !strings.Contains(cfg, "verify: 300ms") {
		t.Fatal("fixture has no timeouts.verify: 1m to shorten")
	}
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status == runstore.StatusInfraError {
		t.Fatalf("a fix timeout was treated as infra_error: %+v", rec)
	}
	if rec.Gate != "" {
		t.Fatalf("gate = %q, want neither clean nor fixed (red)", rec.Gate)
	}
	if lastCommitMsg(t, h.remote, "fugaro/"+runID) == "fugaro: autofix (commands.fix)" {
		t.Fatal("a timed-out fix's changes were committed")
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a timed-out fix never reached implement")
	}
	if p := h.agent.calls[0].Prompt; !strings.Contains(p, "commands.fix timed out") {
		t.Fatalf("implement prompt = %q", p)
	}
}

// TestGateHaltEndsHaltedNotSucceeded: a halt (a budget cap, a kill switch,
// the token cap) recorded while the check gate's command is running must
// not be shadowed by a clean gate's own "succeeded": the gate bypasses
// r.stage(), which is the only place a halt is otherwise re-checked, so a
// clean conclusion here is the one path that must check it for itself.
func TestGateHaltEndsHaltedNotSucceeded(t *testing.T) {
	// A channel, not newHandleBox's plain field: the background goroutine
	// below must not read the handle until runner.Run has actually sent it
	// (onNewRun fires on the harness's own goroutine, concurrently with
	// this one), or it's a data race, not just a timing one.
	handles := make(chan *runner.RunHandle, 1)
	runner.OnRun(t, func(h *runner.RunHandle) { handles <- h })
	cfg := gateCfg(t, "test-and-fix", 2, "touch gate-halt-marker && sleep 2", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	done := make(chan struct{})
	go func() {
		defer close(done)
		handle := <-handles
		marker := filepath.Join(h.deps.WorkDir, "gate-halt-marker")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				handle.HaltNow(runCapHalt)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	rec, err := h.run(t)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil {
		t.Fatalf("record = %+v", rec)
	}
}

// TestGateStageSavedBeforeRunning: the persisted record must show stage
// "check" while the gate's command runs (potentially a full build, test or
// lint), the same as every other stage transition in this file pairs its
// Stage assignment with an immediate save.
func TestGateStageSavedBeforeRunning(t *testing.T) {
	cfg := gateCfg(t, "test-and-fix", 2, "touch gate-stage-marker && sleep 1", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	done := make(chan struct{})
	var sawCheck bool
	go func() {
		defer close(done)
		marker := filepath.Join(h.deps.WorkDir, "gate-stage-marker")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				// r.save(ctx) runs before runGate (which runs this shell
				// command), so by the time the command has even started —
				// let alone created this marker — the save has already
				// completed: no race to win here, just a read.
				if rec, err := h.store.ReadRecord(context.Background()); err == nil && rec.Stage == "check" {
					sawCheck = true
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	<-done
	if !sawCheck {
		t.Fatal(`the persisted record never showed stage "check" while the gate's command ran`)
	}
}
