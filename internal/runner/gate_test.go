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

// fileInTree reports whether path exists in ref's tree in remote.
func fileInTree(t *testing.T, remote, ref, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", remote, "cat-file", "-e", ref+":"+path)
	return cmd.Run() == nil
}

// fileEverCommitted reports whether path appears in any commit reachable
// from ref in remote: a tighter check than "not in the tip", which a later
// commit covering it up (or, here, the agent's own unrelated commit
// landing after it) would still pass.
func fileEverCommitted(t *testing.T, remote, ref, path string) bool {
	t.Helper()
	return testutil.Git(t, remote, "log", "--oneline", ref, "--", path) != ""
}

// TestGateFixTimeoutIsDiscardedAndRed: commands.fix that runs longer than
// the workflow's timeouts.verify is killed partway through writing a file;
// its changes are discarded (reset to HEAD, untracked files removed), never
// committed — neither by the gate itself nor by the implement stage that
// follows, whose own `git add -A` would otherwise pick up and commit
// whatever a half-written fix left lying around, exactly as a human
// reading only the latest commit's message could miss. The check command
// is skipped (there is nothing meaningful to judge), the step ends red
// (reaching implement, with a plain explanation), and the run is never
// infra_error over it.
func TestGateFixTimeoutIsDiscardedAndRed(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "echo half > half.txt; sleep 5")
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
	if rec.Autofix == nil || !rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want TimedOut true", rec.Autofix)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a timed-out fix never reached implement")
	}
	if p := h.agent.calls[0].Prompt; !strings.Contains(p, "commands.fix timed out") {
		t.Fatalf("implement prompt = %q", p)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "half.txt")); err == nil {
		t.Fatal("the timed-out fix's half-written file survived in the working tree")
	}
	branch := "fugaro/" + runID
	if fileInTree(t, h.remote, branch, "half.txt") {
		t.Fatal("the timed-out fix's half-written file reached the pushed branch's tree")
	}
	if fileEverCommitted(t, h.remote, branch, "half.txt") {
		t.Fatal("the timed-out fix's half-written file was committed at some point in the branch's history")
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

// TestGateHaltDuringCancelDoesNotBecomeInfraError: WatchCancel cancels its
// context with ErrCancelled unconditionally once a cancel marker is seen,
// even when its onCancel hook (markCancelled) loses the race to a halt
// recorded first (haltNow) — so the check gate's command, killed by that
// cancellation, returns a plain context error indistinguishable by err
// alone from any other failure. agentLoop's gate error branch must still
// check the halt before falling back to "was this a cancel, or
// infra_error", the same halt-first convention the g.Clean branch (just
// above it) follows, or the halt — and rec.Halt — is lost to infra_error.
//
// Mutation (drop the `if h := r.haltValue(); h != nil { r.endHalted(...);
// return }` check just added to the err != nil branch): this test fails,
// ending infra_error with rec.Halt nil.
func TestGateHaltDuringCancelDoesNotBecomeInfraError(t *testing.T) {
	handles := make(chan *runner.RunHandle, 1)
	runner.OnRun(t, func(h *runner.RunHandle) { handles <- h })
	cfg := gateCfg(t, "test-and-fix", 2, "touch gate-halt-cancel-marker && sleep 2", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("test-and-fix", testAndFixRecipe))
	done := make(chan struct{})
	go func() {
		defer close(done)
		handle := <-handles
		marker := filepath.Join(h.deps.WorkDir, "gate-halt-cancel-marker")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				// Order matters: the halt must be recorded before the cancel
				// marker is polled, so markCancelled() (called inside
				// WatchCancel, then again in agentLoop's own check) finds
				// r.halt already set and returns false both times — exactly
				// the race the finding describes, made deterministic.
				handle.HaltNow(runCapHalt)
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

// TestGateRecordsAutofixExitCode: commands.fix's exit code and whether it
// timed out are written to the run's record — never its output — so
// "ran and changed nothing" can be told apart from a bad exit without
// reading the gate's own log.
//
// Mutation (drop the r.rec.Autofix assignment in runAutofix): this test
// fails, Autofix stays nil.
func TestGateRecordsAutofixExitCode(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "exit 3")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Autofix == nil || rec.Autofix.ExitCode != 3 || rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want {ExitCode:3 TimedOut:false}", rec.Autofix)
	}
}

// TestGateFailedFixDiscardsChanges: commands.fix that exits non-zero without
// timing out is discarded exactly like a timed-out one (an owner ruling that
// supersedes the earlier design, which committed a merely-failed fix on the
// theory that whatever it left was still worth judging): a fix that appends
// to a tracked file and fails must not produce a runner-identity commit or a
// "fixed" gate, and the tracked file must come back exactly as it was.
//
// Mutation (in runAutofix, change `case code != 0:` to commit and judge the
// change instead of discarding it, as the pre-this-round code did): this
// test fails, seeing rec.Gate == "fixed" and README.md left modified.
func TestGateFailedFixDiscardsChanges(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "echo more >> README.md; exit 2")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status == runstore.StatusInfraError {
		t.Fatalf("a failed fix was treated as infra_error: %+v", rec)
	}
	if rec.Gate == "fixed" || rec.Gate == "clean" {
		t.Fatalf("gate = %q, want red (a failed fix is discarded, never fixed)", rec.Gate)
	}
	if rec.Autofix == nil || rec.Autofix.ExitCode != 2 || rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want {ExitCode:2 TimedOut:false}", rec.Autofix)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a failed fix never reached implement")
	}
	if p := h.agent.calls[0].Prompt; !strings.Contains(p, "commands.fix failed (exit 2)") {
		t.Fatalf("implement prompt = %q", p)
	}
	data, err := os.ReadFile(filepath.Join(h.deps.WorkDir, "README.md"))
	if err != nil || string(data) != "Fixture repository used by Fugaro's tests.\n" {
		t.Fatalf("README.md = %q, %v, want it restored to its original content", data, err)
	}
	branch := "fugaro/" + runID
	if lastCommitMsg(t, h.remote, branch) == "fugaro: autofix (commands.fix)" {
		t.Fatal("a failed fix's changes were committed under the runner's identity")
	}
	if got := fileContentAt(t, h.remote, branch, "README.md"); got != "Fixture repository used by Fugaro's tests." {
		t.Fatalf("README.md on the pushed branch = %q, want it restored", got)
	}
}

// TestGateFixTimeoutOnTrackedFileIsRestored: TestGateFixTimeoutIsDiscardedAndRed
// covers a timed-out fix that only ever creates an untracked file; a fix
// that instead half-writes a change to a file the repository already
// tracks must be restored exactly the same way (git reset --hard, not just
// a clean of untracked files, is what undoes this one).
//
// Mutation (same as TestGateFixTimeoutIsDiscardedAndRed: drop the ResetTo
// call in the timeout branch of runAutofix): this test fails, seeing
// README.md left with "half-written" appended.
func TestGateFixTimeoutOnTrackedFileIsRestored(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed", "echo half-written >> README.md; sleep 5")
	cfg = strings.Replace(cfg, "verify: 1m", "verify: 300ms", 1)
	if !strings.Contains(cfg, "verify: 300ms") {
		t.Fatal("fixture has no timeouts.verify: 1m to shorten")
	}
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Autofix == nil || !rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want TimedOut true", rec.Autofix)
	}
	data, err := os.ReadFile(filepath.Join(h.deps.WorkDir, "README.md"))
	if err != nil || string(data) != "Fixture repository used by Fugaro's tests.\n" {
		t.Fatalf("README.md = %q, %v, want it restored to its original content", data, err)
	}
	branch := "fugaro/" + runID
	if got := fileContentAt(t, h.remote, branch, "README.md"); got != "Fixture repository used by Fugaro's tests." {
		t.Fatalf("README.md on the pushed branch = %q, want it restored", got)
	}
}

// fileContentAt is path's blob content at ref in remote.
func fileContentAt(t *testing.T, remote, ref, path string) string {
	t.Helper()
	return testutil.Git(t, remote, "show", ref+":"+path)
}

// TestGateFixThatCommitsThenTimesOutIsDiscarded: a fix that commits on its
// own and then runs long enough to be killed by timeouts.verify must lose
// that commit too, not just whatever it changed afterward — ResetTo is
// given the commit read before the fix ran, never the literal "HEAD" (which
// the fix's own commit has by then moved), so the reset reaches back past
// it.
//
// Mutation (in runAutofix, capture preFixHEAD but pass "HEAD" to ResetTo
// instead, reproducing the pre-this-round bug): this test fails, finding
// evil.txt in the pushed branch's tree and history.
func TestGateFixThatCommitsThenTimesOutIsDiscarded(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed",
		"echo X > evil.txt; git add evil.txt; git commit -q -m evil; sleep 5")
	cfg = strings.Replace(cfg, "verify: 1m", "verify: 300ms", 1)
	if !strings.Contains(cfg, "verify: 300ms") {
		t.Fatal("fixture has no timeouts.verify: 1m to shorten")
	}
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Autofix == nil || !rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want TimedOut true", rec.Autofix)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "evil.txt")); err == nil {
		t.Fatal("the self-committed file survived in the working tree")
	}
	branch := "fugaro/" + runID
	if fileInTree(t, h.remote, branch, "evil.txt") {
		t.Fatal("the fix's self-commit reached the pushed branch's tree")
	}
	if fileEverCommitted(t, h.remote, branch, "evil.txt") {
		t.Fatal("the fix's self-commit survived somewhere in the branch's history")
	}
}

// TestGateFixThatCommitsThenExitsZeroIsDiscarded: a fix that commits on its
// own and then exits 0 is not trusted just because it reported success: a
// commit the check step never had a chance to judge first must be
// discarded exactly like a timed-out or failed fix, not accepted because
// nothing else looks wrong with it.
//
// Mutation (in runAutofix, drop the `headAfter != preFixHEAD` check — the
// self-commit case entirely — so a fix that exits 0 is always committed as
// Changed): this test fails, finding evil.txt in the pushed branch's tree
// and history, and the prompt missing "committed on its own".
func TestGateFixThatCommitsThenExitsZeroIsDiscarded(t *testing.T) {
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f fixed",
		"echo X > evil.txt; git add evil.txt; git commit -q -m evil")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Gate == "fixed" || rec.Gate == "clean" {
		t.Fatalf("gate = %q, want red (a self-committing fix is discarded, never fixed)", rec.Gate)
	}
	if rec.Autofix == nil || rec.Autofix.ExitCode != 0 || rec.Autofix.TimedOut {
		t.Fatalf("Autofix = %+v, want {ExitCode:0 TimedOut:false}", rec.Autofix)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a self-committing fix never reached implement")
	}
	if p := h.agent.calls[0].Prompt; !strings.Contains(p, "committed on its own") {
		t.Fatalf("implement prompt = %q", p)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "evil.txt")); err == nil {
		t.Fatal("the self-committed file survived in the working tree")
	}
	branch := "fugaro/" + runID
	if fileInTree(t, h.remote, branch, "evil.txt") {
		t.Fatal("the fix's self-commit reached the pushed branch's tree")
	}
	if fileEverCommitted(t, h.remote, branch, "evil.txt") {
		t.Fatal("the fix's self-commit survived somewhere in the branch's history")
	}
}

// followUpWithRecipe is fuHarness.followUp, but the task carries rcp: a
// follow-up never reads agent.recipe (resolveRecipe only does for a first
// run), so embedding the recipe in the task is how a follow-up actually
// gets a check-step recipe.
func (h *fuHarness) followUpWithRecipe(t *testing.T, id, previous, text string, rcp *task.Recipe) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Task: text,
		Branch: "fugaro/" + runID, PR: 1, PreviousRun: previous, Workflow: h.first.Workflow, Recipe: rcp}
	store := runstore.Open(h.bucket, "acme-app", id)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	h.store, h.deps.Store = store, store
	for _, dir := range []string{h.deps.WorkDir, h.deps.StateDir} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	h.deps.Env = append(filterEnv(h.deps.Env, "HOME"), "HOME="+t.TempDir())
	h.agent = &scriptedAgent{t: t}
	h.deps.Agent = h.agent
}

// followUpYAMLWithLintFix is followUpYAML with commands.lint and
// commands.fix added, for a follow-up test that needs the gate's autofix.
func followUpYAMLWithLintFix(t *testing.T, lint, fix string) string {
	t.Helper()
	cfg := followUpYAML(t, "")
	extra := "      lint: " + yamlQuote(lint) + "\n      fix: " + yamlQuote(fix) + "\n"
	out := strings.Replace(cfg, "      rerun_failed:\n", extra+"      rerun_failed:\n", 1)
	if out == cfg {
		t.Fatal("fixture has no commands.rerun_failed line")
	}
	return out
}

// TestFollowUpCleanGateDoesNotShortCircuit: a follow-up (fugaro run --pr N)
// keeps its recipe, so the check gate runs for it too. A clean gate ending
// the run succeeded/none — correct for a first run, which asked only for
// the gate to pass — would otherwise silently swallow a follow-up the
// launcher specifically asked for (reply to comments, apply instructions):
// the design (generic-tool.md G10, G11) and the plan say nothing about
// follow-ups, so this run takes the conservative reading and never lets a
// clean gate end a follow-up without running it.
//
// Mutation (drop the `if r.follow == nil` guard on endWithoutPR, so every
// clean gate short-circuits): this test fails, back to outcome none with
// zero agent calls.
func TestFollowUpCleanGateDoesNotShortCircuit(t *testing.T) {
	const yaml = "version: 1\nname: gated\nsteps:\n  - check: { command: test }\n  - review: {}\n"
	h := followUpHarness(t, "", nil)
	h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
	h.followUpWithRecipe(t, followID, runID, "", embeddedTask("gated", yaml))
	rec, err := h.run(t, withAnswer(implement("rename"), "Renamed the helper to fooBar."), review("ship", 0))
	mustReady(t, rec, err)
	if rec.Gate != "clean" {
		t.Fatalf("gate = %q, want clean", rec.Gate)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a clean gate short-circuited the follow-up: implement never ran")
	}
	if impl := h.agent.calls[0]; !strings.Contains(impl.Prompt, "Please rename the helper to fooBar.") {
		t.Fatalf("implement prompt = %q, want the follow-up's own instructions", impl.Prompt)
	}
}

// TestFollowUpFixedGateDoesNotSkipImplement: the same reasoning as
// TestFollowUpCleanGateDoesNotShortCircuit, for a fixed gate (autofix
// committed a change and the check then passed): a first run skips
// implement because nothing else is asked of it, but a follow-up's own
// instructions must still run through implement.
//
// Mutation (drop the `if r.follow == nil` guard around skipImplement =
// true): this test fails, zero agent calls (stage review only).
func TestFollowUpFixedGateDoesNotSkipImplement(t *testing.T) {
	const yaml = "version: 1\nname: gatedfix\nsteps:\n  - check: { command: lint, autofix: true }\n  - review: {}\n"
	cfg := followUpYAMLWithLintFix(t, "test -f fixed", "touch fixed")
	h := followUpHarness(t, cfg, nil)
	h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
	h.followUpWithRecipe(t, followID, runID, "", embeddedTask("gatedfix", yaml))
	rec, err := h.run(t, withAnswer(implement("rename"), "Renamed the helper to fooBar."), review("ship", 0))
	mustReady(t, rec, err)
	if rec.Gate != "fixed" {
		t.Fatalf("gate = %q, want fixed", rec.Gate)
	}
	if len(h.agent.calls) == 0 {
		t.Fatal("a fixed gate short-circuited the follow-up: implement never ran")
	}
	if impl := h.agent.calls[0]; !strings.Contains(impl.Prompt, "Please rename the helper to fooBar.") {
		t.Fatalf("implement prompt = %q, want the follow-up's own instructions", impl.Prompt)
	}
}
