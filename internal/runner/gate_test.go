package runner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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
	const lintOnly = "version: 1\nname: lint-only\nsteps:\n  - check: { command: lint }\n  - review: {}\n"
	cfg := gateCfg(t, "lint-only", 2, "", "", "")
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-only", lintOnly))
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the recipe's check step needs commands.lint in fugaro.yaml at main") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || len(h.provider.State.PRs) != 0 {
		t.Fatalf("record = %+v", rec)
	}
}
