package runner_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The two artifact recipe shapes (schemas/recipe.schema.json): a render
// alone, and a plan stage before it.
const (
	sizzlePlanRenderYAML = "version: 1\nname: sizzle\nmode: artifact\nsteps:\n  - plan: {}\n  - render: {}\noutputs: { globs: [out/*.mp4] }\n"
	sizzleRenderOnlyYAML = "version: 1\nname: sizzle\nmode: artifact\nsteps:\n  - render: {}\noutputs: { globs: [out/*.mp4] }\n"
	// sizzleDefaultYAML is the render-only shape under the DEFAULT recipe
	// name: the file a run reads when its task names no recipe, which is
	// what a follow-up that inherits none does (recipe.go's resolveRecipe
	// falls back to recipe.DefaultName).
	sizzleDefaultYAML = "version: 1\nname: default\nmode: artifact\nsteps:\n  - render: {}\noutputs: { globs: [out/*.mp4] }\n"
)

// artifactSpec is a task spec for a mode: artifact recipe, with the mode in
// the task exactly as the launcher writes it.
func artifactSpec(yaml string) *task.Spec {
	s := recipeSpec(embeddedTask("sizzle", yaml))
	s.Mode = recipe.ModeArtifact
	return s
}

// renderCfg is the fixture's fugaro.yaml with commands.render set (to
// "sh render.sh" unless render is given) and, when timeouts is non-empty,
// with the workflow's own timeouts line replaced by it.
func renderCfg(t *testing.T, render, timeouts string) string {
	t.Helper()
	if render == "" {
		render = "sh render.sh"
	}
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	out := strings.Replace(cfg, "      build: sh build.sh\n", "      build: sh build.sh\n      render: "+render+"\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no commands.build line to add commands.render beside")
	}
	if timeouts != "" {
		old := "    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }"
		if !strings.Contains(out, old) {
			t.Fatal("the fixture's timeouts line moved")
		}
		out = strings.Replace(out, old, "    timeouts: "+timeouts, 1)
	}
	return out
}

// renderScript is the repository file commands.render runs, committed to the
// remote: the render command is repo text, and its invocation comes from
// fugaro.yaml (never from the state directory).
func renderScript(body string) map[string]string {
	return map[string]string{"render.sh": body}
}

const sizzleScript = "#!/bin/sh\nmkdir -p out && echo sizzle > out/sizzle.mp4\n"

// planStage is the artifact run's optional plan stage: it writes a plan file
// the way the prompt asks, and returns without rendering anything.
func planStage(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	if !strings.Contains(req.Prompt, ".fugaro/render-plan.json") {
		t.Errorf("the plan prompt does not name the plan file: %q", req.Prompt)
	}
	if strings.Contains(req.Prompt, "FUGARO_INPUTS_DIR") {
		t.Errorf("the plan prompt names an inputs directory that does not exist yet: %q", req.Prompt)
	}
	if err := os.MkdirAll(filepath.Join(req.Dir, ".fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(req.Dir, ".fugaro", "render-plan.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 0.25}, nil
}

// TestArtifactRunNeverPushesAndHasNoGitToken is Review Focus 2, the two
// things an artifact run must never do: it pushes no branch (checkpoint
// pushes and finalize's own push alike) and its agent's stage environment
// carries no git credential, exactly as a review run's does not. The
// deliverable goes to the runs bucket, so a push credential there would be
// reachable by the plan stage's prompt injection for nothing.
//
// Mutation (drop `&& !r.artifact` from either of runner.go's two agent-env
// sites, :635 and the Set/authVars block): this test fails, finding
// GH_TOKEN, FUGARO_GIT_TOKEN or GIT_CONFIG_COUNT in the plan stage's
// environment. Mutation (drop `!r.artifact` from checkpointsOn): the plan
// stage commits a file, its boundary checkpoint pushes, and the push count
// is non-zero.
func TestArtifactRunNeverPushesAndHasNoGitToken(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-access-token", Token: "ghs-fixture", Env: map[string]string{"GH_TOKEN": "ghs-fixture"}}
	}
	pushes := countPushes(t, h)
	var envAtPlan []string
	plan := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		envAtPlan = req.Env
		shell(t, req, "echo wip > wip.txt && git add -A && git commit -qm 'plan work'")
		return planStage(t, ctx, req)
	}
	rec, err := h.run(t, plan)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts || rec.PR != nil {
		t.Fatalf("record = %+v", rec)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	for _, k := range []string{"GH_TOKEN", "FUGARO_GIT_TOKEN", "GIT_CONFIG_COUNT"} {
		if envValue(envAtPlan, k) != "" {
			t.Errorf("the artifact run's agent has %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "out", "sizzle.mp4")); err != nil {
		t.Fatalf("the render did not produce its file: %v", err)
	}
}

// TestArtifactRunKeepsTheWorkflowsSecrets is the deliberate NON-change:
// runner.go's secretEnvs block keeps its `if !r.review`, so an artifact run's
// agent still gets the workflow's secrets (Global Constraints, design §8.6,
// D10: the blast radius is documented, not narrowed). Asserted so a later
// refactor cannot change it silently.
//
// Mutation (extend the `if !r.review` guard to `&& !r.artifact`): this test
// fails, finding FIXTURE_FAILS_FILE absent from the plan stage's
// environment.
func TestArtifactRunKeepsTheWorkflowsSecrets(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	var envAtPlan []string
	plan := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		envAtPlan = req.Env
		return planStage(t, ctx, req)
	}
	if _, err := h.run(t, plan); err != nil {
		t.Fatal(err)
	}
	if envValue(envAtPlan, "FIXTURE_FAILS_FILE") == "" {
		t.Fatal("the artifact run's agent lost the workflow's secret")
	}
}

// TestArtifactRunDoesNotCheckpoint is the real never-pushes gate: while a
// plan stage runs with committed work in the checkout, the run must have no
// checkpointer goroutine at all (checkpointsOn is the single gate both the
// mid-stage poll and the stage-boundary push start from), whatever
// fugaro.yaml's git.pr.checkpoints says.
//
// Mutation (drop `!r.artifact` from checkpoint.go's checkpointsOn): a
// checkpointer is running during the plan stage and this test fails —
// TestCheckpointPushesCommittedWorkMidStage shows the push that follows.
func TestArtifactRunDoesNotCheckpoint(t *testing.T) {
	cfg := strings.Replace(renderCfg(t, "", ""), "total: 5m", "total: 1h", 1)
	h := newHarnessFiles(t, cfg, artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	pushes := countPushes(t, h)
	plan := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo wip > wip.txt && git add -A && git commit -qm 'plan work'")
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpoint goroutines during an artifact run's stage, want none", n)
		}
		return planStage(t, ctx, req)
	}
	if _, err := h.run(t, plan); err != nil {
		t.Fatal(err)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none: an artifact run pushes no branch", n)
	}
	if tip := remoteTip(t, h); tip != "" {
		t.Fatalf("the artifact run's branch is on the remote (%s)", tip)
	}
	if head := pushedHead(t, h); head != "" {
		t.Fatalf("pushed_head = %q, want none", head)
	}
}

// TestRenderFailureEndsTheRunFailedWithNoArtifacts: a render that exits
// non-zero fails the run, records its exit code, and claims no artifacts.
//
// Mutation (treat any exit code as success): the record's status becomes
// succeeded/artifacts and this test fails.
func TestRenderFailureEndsTheRunFailedWithNoArtifacts(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzleRenderOnlyYAML),
		renderScript("#!/bin/sh\necho 'render blew up' >&2\nexit 3\n"))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "commands.render exited 3") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if rec.Render == nil || rec.Render.ExitCode != 3 || rec.Render.TimedOut {
		t.Fatalf("render = %+v", rec.Render)
	}
	if rec.PR != nil {
		t.Fatal("a failed artifact run opened a pull request")
	}
}

// TestRenderTimeoutIsRecordedAndFailsTheRun is Review Focus 7: the render
// runs under timeouts.render, and a render killed by it is recorded as
// timed out rather than as a plain exit code.
//
// Mutation (run the render under the stage timeout, or under the run's whole
// total): the render outlives timeouts.render, the record's TimedOut is
// false, and this test fails.
func TestRenderTimeoutIsRecordedAndFailsTheRun(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", "{ total: 5m, stage: 2m, verify: 1m, render: 1s, finalize_reserve: 30s }"),
		artifactSpec(sizzleRenderOnlyYAML), renderScript("#!/bin/sh\nsleep 30\n"))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Render == nil || !rec.Render.TimedOut {
		t.Fatalf("render = %+v, want timed out", rec.Render)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "timed out after 1s") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if rec.Render.DurationS > 20 {
		t.Fatalf("the render ran for %s: timeouts.render did not bound it", time.Duration(rec.Render.DurationS*float64(time.Second)))
	}
}

// TestRenderCommandComesFromTheWorkflowNotTheSettingsFile is Review Focus 1.
// The command that runs is commands.render as bootstrap read it from
// fugaro.yaml at the task's ref (the runner's in-memory workflow config),
// never the render key of <StateDir>/workflow.json — a 0644 file inside the
// directory the plan-stage agent writes, since it is the same user in the
// same container. The plan stage here rewrites exactly that file; the
// command that runs and is recorded is unchanged.
//
// Mutation (load the command through verify.LoadSettings instead of r.wf):
// the rewritten command runs, out/HACKED.mp4 exists, and this test fails.
func TestRenderCommandComesFromTheWorkflowNotTheSettingsFile(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	rewrite := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		settings := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "workflow.json")
		body := `{"build":"sh build.sh","test":"sh test.sh","render":"mkdir -p out && echo hacked > out/HACKED.mp4"}`
		if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return planStage(t, ctx, req)
	}
	rec, err := h.run(t, rewrite)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Render == nil || rec.Render.Command != "sh render.sh" {
		t.Fatalf("render command = %+v, want the fugaro.yaml command", rec.Render)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "out", "HACKED.mp4")); err == nil {
		t.Fatal("the render ran the command the plan stage wrote into the state directory")
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "out", "sizzle.mp4")); err != nil {
		t.Fatalf("the render did not run the workflow's own command: %v", err)
	}
}

// TestRenderCommandSHA256IsOfTheStringThatRan: the hash is of the
// commands.render text the runner ran, which is what a reviewer can
// recompute from the repository at the run's ref (design D8).
//
// Mutation (hash the redacted string, or the record's own Command): the hash
// no longer matches the repository text and this test fails.
func TestRenderCommandSHA256IsOfTheStringThatRan(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzleRenderOnlyYAML), renderScript(sizzleScript))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("sh render.sh"))
	if rec.Render == nil || rec.Render.CommandSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("render = %+v, want the sha256 of the fugaro.yaml command", rec.Render)
	}
	if rec.Render.Tree != "working" {
		t.Fatalf("tree = %q, want working", rec.Render.Tree)
	}
}

// TestRenderCommandAndItsHashDivergeOnlyWhenRedactionFires: the record's
// Command is redacted, its CommandSHA256 is the hash of the string the
// runner RAN, so the two are deliberately not a hash pair when a secret is
// in the command. Pinned here so a later "fix" cannot quietly move the hash
// onto the redacted text and leave D8's claim false.
//
// Mutation (set CommandSHA256 from the redacted string): the hash matches
// sha256(Command) and this test fails.
func TestRenderCommandAndItsHashDivergeOnlyWhenRedactionFires(t *testing.T) {
	const token = "rendersecret-token-123"
	h := newHarnessFiles(t, renderCfg(t, `"sh render.sh # `+token+`"`, ""), artifactSpec(sizzleRenderOnlyYAML), renderScript(sizzleScript))
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-access-token", Token: token, Env: map[string]string{"GH_TOKEN": token}}
	}
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Render == nil {
		t.Fatal("no render recorded")
	}
	if strings.Contains(rec.Render.Command, token) {
		t.Fatalf("the recorded command holds the secret: %q", rec.Render.Command)
	}
	if !strings.Contains(rec.Render.Command, "[REDACTED]") {
		t.Fatalf("the recorded command is not the redacted one: %q", rec.Render.Command)
	}
	raw := sha256.Sum256([]byte("sh render.sh # " + token))
	if rec.Render.CommandSHA256 != hex.EncodeToString(raw[:]) {
		t.Fatalf("CommandSHA256 = %q, want the hash of the unredacted string", rec.Render.CommandSHA256)
	}
	redacted := sha256.Sum256([]byte(rec.Render.Command))
	if rec.Render.CommandSHA256 == hex.EncodeToString(redacted[:]) {
		t.Fatal("CommandSHA256 hashes the redacted text: D8's claim would be false")
	}
}

// TestBlankRenderCommandRefusesTheRun is Review Focus 1's fail-closed half: a
// recipe whose render step has no commands.render is refused at bootstrap,
// before the agent runs and before anything is written.
//
// Mutation (skip gateCommandProblems' render arm): the run reaches the render
// step and ends failed there rather than infra_error at bootstrap.
func TestBlankRenderCommandRefusesTheRun(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, `""`, ""), artifactSpec(sizzleRenderOnlyYAML), renderScript(sizzleScript))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "render step needs commands.render") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}

// TestPlanStageIsOptional: steps: [render] runs with no agent call at all —
// a recipe whose deliverable needs no plan and whose render needs no model.
func TestPlanStageIsOptional(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzleRenderOnlyYAML), renderScript(sizzleScript))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts || rec.Render == nil {
		t.Fatalf("record = %+v", rec)
	}
}

// TestAnEmptySpecModeTakesTheRecipesMode is the runner-chooses case: the
// launcher leaves the recipe to the runner when it cannot resolve one
// (runnerChooses), so the spec carries no mode at all — and the rule is
// "spec.Mode when set, the resolved recipe's mode when not", never "empty
// means implement". This folds in Task 1's deleted
// TestAModeArtifactRecipeIsRefusedByARunnerThatDoesNotImplementIt: the same
// task shape (no mode in the spec, an artifact recipe resolved) that used to
// be refused now runs as an artifact run, and must still never take the
// implement path (zero agent calls beyond the plan stage, zero pushes).
//
// Mutation (treat an empty spec.Mode as implement): the run takes the
// implement loop, the scripted agent is called with an implement request, a
// branch is pushed and this test fails.
func TestAnEmptySpecModeTakesTheRecipesMode(t *testing.T) {
	spec := recipeSpec(embeddedTask("sizzle", sizzleRenderOnlyYAML))
	if spec.Mode != "" {
		t.Fatal("the fixture spec carries a mode")
	}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none (implement must not run)", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}

// TestAModeArtifactSpecThatDisagreesWithTheRecipeIsRefused is Review Focus
// 10's first direction: a spec whose Mode disagrees with the resolved
// recipe's mode is refused at bootstrap, before any agent call or push.
//
// Mutation (compare only r.review and drop the mode comparison): the
// disagreement falls through and this test fails.
func TestAModeArtifactSpecThatDisagreesWithTheRecipeIsRefused(t *testing.T) {
	spec := recipeSpec(embeddedTask("two-rounds", twoRounds))
	spec.Mode = recipe.ModeArtifact
	h := newHarness(t, renderCfg(t, "", ""), spec)
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "but the task says mode: artifact") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}

// TestAModeArtifactRecipeWithAnImplementSpecModeIsRefused is Review Focus
// 10's other direction: a resolved artifact recipe the spec does not claim
// (the spec says implement) is refused rather than run as an implement loop,
// which would push a branch and open a pull request for a recipe that asked
// for a file.
//
// Mutation (drop the mode comparison): this test fails, implement runs and a
// pull request opens.
func TestAModeArtifactRecipeWithAnImplementSpecModeIsRefused(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	spec.Mode = recipe.ModeImplement
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "is mode: artifact, but the task says mode: implement") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}

// TestReportSaysArtifactsNotDraft: report.go's outcome switch has an arm for
// the new outcome, so an artifact run never reads as a draft pull request
// with an empty reason (its `default:` arm) — the one thing it is not.
//
// Mutation (delete the OutcomeArtifacts arm): the report says "draft — "
// with an empty reason and this test fails.
func TestReportSaysArtifactsNotDraft(t *testing.T) {
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts,
		Render: &runstore.RenderRecord{Command: "sh render.sh", CommandSHA256: strings.Repeat("a", 64),
			Tree: "working", ExitCode: 0, DurationS: 12, StartedAt: "2026-10-15T10:00:00Z"}}
	rep := runner.Report(rec, "gs://bucket/runs/acme-app/"+runID, nil)
	if !strings.Contains(rep, "**Outcome:** artifacts") {
		t.Fatalf("report lacks the artifacts outcome:\n%s", rep)
	}
	if strings.Contains(rep, "draft —") {
		t.Fatalf("report claims a draft pull request:\n%s", rep)
	}
}

// TestReportShowsTheRenderAndTheComputeEstimate is Task 2's half of the
// Artifacts section: the render's command, tree, exit code and wall clock,
// and the compute estimate. The per-file table is the upload step's (its
// types are its own), so a record without one says so rather than printing a
// header over nothing.
//
// The compute line names the run's wall clock as the basis, and the render's
// duration only as a part of it: the figure is priced from the run's own
// elapsed time (updateCost), so a short render inside a long run must not
// read as its price.
//
// Mutation (drop the Artifacts section): this test fails; mutation (put the
// "for %s" wording back, the render's own duration as the priced span): this
// test fails on the compute line.
func TestReportShowsTheRenderAndTheComputeEstimate(t *testing.T) {
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts,
		Cost: &runstore.Cost{ComputeUSD: 0.0312, ComputeEstimated: true},
		Render: &runstore.RenderRecord{Command: "sh render.sh", CommandSHA256: strings.Repeat("b", 64),
			Tree: "working", ExitCode: 0, DurationS: 305, StartedAt: "2026-10-15T10:00:00Z"}}
	rep := runner.Report(rec, "gs://bucket/runs/acme-app/"+runID, nil)
	for _, want := range []string{"#### Artifacts", "**Render:** `sh render.sh` in the working tree, exit 0, 5m5s",
		"**Command sha256:**", "**Compute:** $0.0312 estimated from the run's wall clock, of which the render is 5m5s", strings.Repeat("b", 64)} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	// A record whose artifact run ended before the render step: the section
	// says so rather than showing nothing.
	rec.Render, rec.Outcome = nil, runstore.OutcomeNone
	rec.Status, rec.Reason = runstore.StatusFailed, "commands.render is empty"
	rep = runner.Report(rec, "gs://bucket/runs/acme-app/"+runID, nil)
	if !strings.Contains(rep, "**Outcome:** stopped — commands.render is empty") {
		t.Fatalf("report = %s", rep)
	}
}

// TestArtifactRunOutcomeIsArtifacts: a render that exits zero ends the run
// succeeded with outcome artifacts — the value the report, cancel and watch
// all switch on — and the record carries the render.
//
// Mutation (set the outcome to OutcomeDraft, the implement loop's ending): the
// assertion fails, and the run reads as a draft pull request it never opened.
func TestArtifactRunOutcomeIsArtifacts(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	rec, err := h.run(t, planStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (%s)", rec.Status, rec.Reason)
	}
	if rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("outcome = %q, want %q", rec.Outcome, runstore.OutcomeArtifacts)
	}
	if rec.Render == nil || rec.Render.ExitCode != 0 {
		t.Fatalf("render = %+v, want the successful render", rec.Render)
	}
	if rec.Reason != "" {
		t.Fatalf("reason = %q, want empty for a succeeded run", rec.Reason)
	}
}

// TestArtifactRunRecordsNoBranchAndNoPR: an artifact run takes the implement
// checkout arm (an artifact recipe has no pull request whose fugaro.yaml
// could be untrusted, so there is nothing to read from a base branch), so
// r.rec.Branch is the branch lock's key — and it stays a key: nothing is
// pushed to it, no pull request is opened on it, and the record says so.
//
// Mutation (let finalize's PR path run, or drop `&& !r.artifact` from
// checkpointsOn): a push or a PR appears and this test fails.
func TestArtifactRunRecordsNoBranchAndNoPR(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	pushes := countPushes(t, h)
	rec, err := h.run(t, planStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Branch == "" {
		t.Fatal("the branch lock's key is missing from the record")
	}
	if rec.PushedHead != "" || rec.PR != nil {
		t.Fatalf("record claims a push or a pull request: pushed_head %q, pr %+v", rec.PushedHead, rec.PR)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	if prs := h.provider.Snapshot().PRs; len(prs) != 0 {
		t.Fatalf("the provider was asked for a pull request: %+v", prs)
	}
	if tip := remoteTip(t, h); tip != "" {
		t.Fatalf("the remote has a branch %q", tip)
	}
}

// TestHaltAdviceForAHaltedArtifactRunSaysStartAgain is the deliberate
// NON-change (plan Task 2, halt.go:177): a halted artifact run is
// OutcomeNone — OutcomeArtifacts is the success value the run writes when it
// finishes — so haltLines already prints "start the run again" rather than
// "fugaro run --pr N", and an added `|| rec.Outcome == OutcomeArtifacts` arm
// would be dead code. Pinned so a later "fix" cannot add one.
//
// Mutation: none can fail this test from halt.go, and that is the finding.
// Adding the `|| rec.Outcome == runstore.OutcomeArtifacts` arm to haltLines
// was done and left this test green — the arm is unreachable, because a
// halted run's record never carries OutcomeArtifacts. What does fail it is
// removing the `rec.Outcome == runstore.OutcomeNone` arm, which is what
// routes an artifact run to "start the run again" instead of the
// "--pr N" default.
func TestHaltAdviceForAHaltedArtifactRunSaysStartAgain(t *testing.T) {
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusHalted, Outcome: runstore.OutcomeNone,
		Reason: "halted", Halt: &runstore.Halt{Reason: runstore.HaltNoCap, Scope: "run", At: time.Now().UTC(), Detail: "no cap"}}
	got := runner.Report(rec, "runs/acme-app/"+runID, nil)
	if strings.Contains(got, "--pr") {
		t.Fatalf("a halted artifact run is told to continue on a pull request:\n%s", got)
	}
	if !strings.Contains(got, "start the run again") {
		t.Fatalf("a halted artifact run is not told to start again:\n%s", got)
	}
}

// TestFollowUpWhoseRecipeIsModeArtifactIsRefused is the mode-confusion hole
// a follow-up left open (design artifact-runs.md §8.2): a follow-up does not
// CHOOSE its recipe, it inherits the previous run's, so a mode: artifact
// recipe reaches it without anyone asking for one — and its spec carries no
// mode either (the launcher writes mode only for a run it resolved a recipe
// for), so an empty spec.Mode used to take the artifact recipe's mode and run
// the render, ending ARTIFACTS, NO PR for a task that continues PR #1.
// Bootstrap therefore refuses the pair before any stage, keyed on the
// RESOLVED recipe. The arms are the three ways a follow-up reaches one: the
// previous run's text embedded in task.json, a repository recipe carried by
// name, which the runner reads at this task's base (the launcher cannot: it
// has the name alone), and — naming no recipe at all — the repository's own
// .fugaro/recipes/default.yaml, which is what the runner falls back to for a
// follow-up. That last one is why the refusal cannot be keyed on the task's
// recipe either: there is none.
//
// Mutations: delete the `r.follow != nil && rcp.Mode == recipe.ModeArtifact`
// refusal in runner.go and every arm runs the render instead: the record ends
// succeeded/artifacts, the workdir holds out/sizzle.mp4 (asserted below), and
// the run that was launched to continue PR #1 opened none. Narrow the refusal
// to `spec.Mode == recipe.ModeArtifact` (key it on the spec rather than on
// the RESOLVED recipe) and every arm is green again, which is the whole bug:
// a follow-up's spec carries no mode. For the default-recipe arm the runner's
// name must also come from the DEFAULT and not agent.recipe: recipe.go reads
// agent.recipe for a first run only, so a follow-up with no recipe of its own
// reads .fugaro/recipes/default.yaml (mutate the switch to apply agent.recipe
// to follow-ups too and this arm stops being refused).
func TestFollowUpWhoseRecipeIsModeArtifactIsRefused(t *testing.T) {
	cases := []struct {
		name string
		// recipe is what the task inherits; nil is a follow-up with no
		// recipe at all.
		recipe *task.Recipe
		// agentRecipe, when set, goes into the BASE's fugaro.yaml
		// (agent.recipe), which the runner applies to first runs only: the
		// default-recipe arm proves that by naming a different recipe there.
		agentRecipe string
		extra       map[string]string
	}{
		{"the previous run's recipe text", &task.Recipe{Name: "sizzle", Source: string(recipe.SourceProject),
			YAML: sizzleRenderOnlyYAML, SHA256: recipe.Sum([]byte(sizzleRenderOnlyYAML))}, "", nil},
		{"a repository recipe kept by name", &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}, "",
			map[string]string{".fugaro/recipes/sizzle.yaml": sizzleRenderOnlyYAML}},
		{"neither, so the repository's default", nil, "solo",
			map[string]string{".fugaro/recipes/default.yaml": sizzleDefaultYAML}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The render is written into fugaro.yaml's commands.render rather
			// than run from a script: with the refusal deleted it really runs,
			// so the mutation shows as a delivered deliverable and not as a
			// missing file standing in for one.
			const render = "mkdir -p out && echo sizzle > out/sizzle.mp4"
			h := followUpHarness(t, renderCfg(t, render, ""), nil)
			extra := c.extra
			if c.agentRecipe != "" {
				// Written to the remote, not to the first run's checkout, so
				// the first run (which WOULD apply agent.recipe) is
				// unaffected: a follow-up reads this one from origin/<ref>
				// (readBaseConfig).
				base := strings.Replace(renderCfg(t, render, ""), "  auth: api-key\n", "  auth: api-key\n  recipe: "+c.agentRecipe+"\n", 1)
				extra = map[string]string{"fugaro.yaml": base}
				for k, v := range c.extra {
					extra[k] = v
				}
			}
			if extra != nil {
				// Committed to main, where a follow-up reads its repository
				// recipe from (readRepoRecipe, origin/<ref>).
				commitToRemote(t, h.harness, func(dir string) { testutil.WriteFiles(t, dir, extra) })
			}
			h.followUpRecipe(t, followID, runID, "Render the trailer.", c.recipe)
			pushes := countPushes(t, h.harness)
			rec, err := h.run(t)
			if err == nil || rec.Status != runstore.StatusInfraError {
				t.Fatalf("rec = %+v, err = %v; want an infra_error", rec, err)
			}
			if !strings.Contains(rec.Reason, "mode: artifact") || !strings.Contains(rec.Reason, "pull request #1") {
				t.Fatalf("the reason does not name the mode and the rule: %q", rec.Reason)
			}
			if len(h.agent.calls) != 0 {
				t.Fatalf("the agent ran %d times, want none", len(h.agent.calls))
			}
			if n := pushes(); n != 0 {
				t.Fatalf("%d pushes, want none", n)
			}
			if rec.Outcome != runstore.OutcomeNone || rec.Render != nil {
				t.Fatalf("the refused run claims artifacts: %+v", rec)
			}
			if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "out", "sizzle.mp4")); err == nil {
				t.Fatal("the render ran for a refused follow-up")
			}
		})
	}
}

// TestRenderIsCutAtTheFinalizeReserve is Review Focus 7's other bound: the
// render runs under timeouts.render AND under the run's finalize reserve
// (design §4.5), and when the reserve comes first it is the reserve that
// ends the render. The budget below leaves about eight seconds between the
// run's start and finalizeAt, with timeouts.render asking for 60s, so a
// render that sleeps 30s can only be cut by the reserve — and the reason it
// records says so, rather than quoting a timeouts.render that had 58s left.
// The window is wide enough that a slow box's startup cannot spend it before
// the render begins, and the sleep long enough that even then the render
// cannot finish on its own.
//
// Mutations: drop the `f.Before(deadline)` arm of Budget.StepDeadline and the
// render sleeps its full 30s and then EXITS 0, so the run ends
// succeeded/artifacts and this test fails on the first assertion. Keep the
// cap and revert the message to "timed out after 60s" and it fails on the
// reason, which no longer names the bound that applied.
func TestRenderIsCutAtTheFinalizeReserve(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", "{ total: 30s, stage: 10s, verify: 5s, render: 60s, finalize_reserve: 22s }"),
		artifactSpec(sizzleRenderOnlyYAML), renderScript("#!/bin/sh\nsleep 30\n"))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a failed run with no artifacts", rec)
	}
	if rec.Render == nil || !rec.Render.TimedOut {
		t.Fatalf("render = %+v, want timed out", rec.Render)
	}
	if !strings.Contains(rec.Reason, "finalize reserve") {
		t.Fatalf("the reason does not name the bound that ended the render: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "timed out after 60s") {
		t.Fatalf("the reason blames a timeout that never fired: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "time budget exhausted") {
		t.Fatalf("the reserve did not hold: %q", rec.Reason)
	}
	if d := time.Duration(rec.Render.DurationS * float64(time.Second)); d > 20*time.Second {
		t.Fatalf("the render ran %s: it was not cut at the reserve", d)
	}
}

// TestRenderTailKeepsAFinalLineWithoutANewline: the render's tail is the one
// thing that explains a failed render to an owner, and the output goes
// through the redactor a line at a time — the redactor holds a partial line
// until a newline arrives, so runRender must flush it before reading the
// tail. A render whose failing message ends without a newline (a `printf`
// with no trailing \n is the ordinary shape of one) is in report.md with the
// flush and gone without it.
//
// Mutation: drop out.Flush() from runRender and report.md's log tail stops
// after the first line, so the assertion below fails.
func TestRenderTailKeepsAFinalLineWithoutANewline(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzleRenderOnlyYAML),
		renderScript("#!/bin/sh\necho 'first line'\nprintf 'the render blew up without a newline'\nexit 3\n"))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Render == nil || rec.Render.ExitCode != 3 {
		t.Fatalf("render = %+v, want exit 3", rec.Render)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "the render blew up without a newline") {
		t.Fatalf("the render's last line, which has no trailing newline, is not in the report:\n%s", report)
	}
}

// TestPlanStageFailureEndsTheRunFailed: the plan stage is the artifact run's
// only agent stage, and a plan the model never delivered ends the run
// failed/none with the stage's own reason — no pull request to fall back to
// and no artifacts to claim. The render never ran, so the record carries no
// render at all, and the report shows no Artifacts section.
//
// Mutation (drop the `if !ok` branch of artifactMode, running the render
// anyway): the run ends succeeded/artifacts, rendering a deliverable the plan
// stage never planned, and this test fails; mutation (set the outcome to
// draft in endArtifactFailed): the record's outcome is draft and it fails
// too.
func TestPlanStageFailureEndsTheRunFailed(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	boom := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{}, errors.New("boom")
	}
	pushes := countPushes(t, h)
	rec, err := h.run(t, boom)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "stage plan failed: boom") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if rec.Render != nil {
		t.Fatalf("a render that never ran is recorded: %+v", rec.Render)
	}
	if rec.PR != nil {
		t.Fatal("a failed artifact run opened a pull request")
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "**Outcome:** stopped — stage plan failed: boom") {
		t.Fatalf("the report does not carry the failure that ended the run:\n%s", report)
	}
	// No Artifacts section: the section is the outcome-arm artifact run's
	// (report.go), and a run that never rendered has no render to report.
	if strings.Contains(string(report), "#### Artifacts") {
		t.Fatalf("the report shows an artifacts section for a run that rendered nothing:\n%s", report)
	}
}

// TestArtifactPanicEndsTheRunInfraError is runArtifactMode's own recover():
// a panic anywhere in the artifact run — here the plan stage — must still end
// it with a record and a report, as infra_error/none, rather than escaping to
// the top-level recover with no report at all. The stage the run was in is
// named, so the reason says where it panicked.
//
// Mutation (drop the recover from runArtifactMode): the panic reaches Run's
// own recover, which returns the error without an artifact end — the report,
// which only storeArtifactEnded writes for this mode, is absent and this test
// fails.
func TestArtifactPanicEndsTheRunInfraError(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
	panicky := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		panic("boom")
	}
	pushes := countPushes(t, h)
	rec, err := h.run(t, panicky)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "stage plan panicked: boom") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if rec.Render != nil {
		t.Fatalf("a render that never ran is recorded: %+v", rec.Render)
	}
	if rec.PR != nil {
		t.Fatal("a panicking artifact run opened a pull request")
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "**Outcome:** stopped — stage plan panicked: boom") {
		t.Fatalf("the report does not carry the panic that ended the run:\n%s", report)
	}
}

// TestCancelDuringTheRenderEndsTheRunCancelled: an artifact run cancelled
// while its render runs ends cancelled/none — never failed, and never
// artifacts — and still stores its report. The render did run, so its record
// stays: the entry the cancel ended is what the record describes.
//
// The cancel is requested only once the render's own first act, a marker file
// in the checkout, is on disk, so it lands while the render is running rather
// than during bootstrap (which ends a run "cancelled during bootstrap", a
// different branch).
//
// Mutation (drop the isCancelled arm of artifactMode's render switch): the
// killed render's error falls to the default arm and the run ends failed with
// "running commands.render: ... context canceled"; mutation (drop
// storeArtifactEnded from endArtifactCancelled): report.md is not stored.
func TestCancelDuringTheRenderEndsTheRunCancelled(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzleRenderOnlyYAML),
		renderScript("#!/bin/sh\ntouch render-started\nsleep 30\n"))
	// The watcher stops when the run returns, and is waited for before the
	// test ends: the store it writes to is closed by the harness's cleanup.
	stop := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		marker := filepath.Join(h.deps.WorkDir, "render-started")
		for i := 0; i < 1000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := os.Stat(marker); err == nil {
				_ = h.store.RequestCancel(context.Background())
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	close(stop)
	done.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Reason != "cancelled during render" {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if rec.Render == nil {
		t.Fatal("the render that ran is not recorded")
	}
	if rec.PR != nil {
		t.Fatal("a cancelled artifact run opened a pull request")
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "**Outcome:** stopped — cancelled during render") {
		t.Fatalf("the report does not carry the cancel that ended the run:\n%s", report)
	}
}

// --- Task 4: the outputs — select, sanitise, cap, hash, upload, record ---

// artifactRecipeYAML is a mode: artifact recipe whose outputs block is out,
// verbatim: the name, cap and upload tests differ only in it.
func artifactRecipeYAML(outputs string) string {
	return "version: 1\nname: sizzle\nmode: artifact\nsteps:\n  - render: {}\noutputs: " + outputs + "\n"
}

// outFile is one file a render script writes under out/: size bytes of
// zeroes, or body when body is set (a small file whose bytes a test can
// name).
type outFile struct {
	name string
	size int
	body string
}

// renderWrites is a render script writing files under out/, in the order
// given, so each one is exactly the size the test asked for.
func renderWrites(files ...outFile) map[string]string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, f := range files {
		fmt.Fprintf(&b, "mkdir -p \"$(dirname out/%s)\"\n", f.name)
		if f.body != "" {
			fmt.Fprintf(&b, "printf '%%s' '%s' > out/%s\n", f.body, f.name)
			continue
		}
		fmt.Fprintf(&b, "head -c %d /dev/zero > out/%s\n", f.size, f.name)
	}
	return renderScript(b.String())
}

// bucketKeys is every object key in the harness's bucket, sorted. A test
// asserts the key set rather than the slice the run returned, so an object
// written outside the run's own prefix is caught too.
func bucketKeys(t *testing.T, b *blob.Bucket) []string {
	t.Helper()
	var keys []string
	iter := b.List(nil)
	for {
		obj, err := iter.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, obj.Key)
	}
	sort.Strings(keys)
	return keys
}

// artifactKeys is the names uploaded under the run's own artifacts/ prefix.
func artifactKeys(t *testing.T, h *harness) []string {
	t.Helper()
	prefix := h.store.Prefix() + "artifacts/"
	var out []string
	for _, k := range bucketKeys(t, h.bucket) {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	return out
}

// objectBytes reads one of the bucket's objects.
func objectBytes(t *testing.T, b *blob.Bucket, key string) []byte {
	t.Helper()
	r, err := b.NewReader(context.Background(), key, nil)
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return data
}

// stagedFiles is what the run left in its staging directory, sorted: a
// refusal must leave it empty (Review Focus 4).
func stagedFiles(t *testing.T, h *harness) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.deps.StateDir, "artifacts"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// assertNothingUploaded is a refused set's whole claim: no object under the
// run's artifacts/ prefix, and nothing staged either — a partial set fails
// the test that calls this.
func assertNothingUploaded(t *testing.T, h *harness) {
	t.Helper()
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the run uploaded %v", keys)
	}
	if files := stagedFiles(t, h); len(files) != 0 {
		t.Fatalf("the run staged %v", files)
	}
}

// TestStageArtifactsRefusesEscapes is Review Focus 3's table: every name a
// render's match can produce that must not become an object key is refused
// with a reason that names it — an escape out of the outputs root, an
// absolute path, a NUL or a newline, an over-long name, a dot-named one (the
// paired rule: the retriever refuses those, so uploading one would record an
// artifact nobody can fetch back, design §9) — and the names that are fine
// are cleaned rather than refused.
//
// Mutation (drop the `..` element check): "../../etc/passwd" is accepted and
// this test fails; mutation (drop the dot rule): ".hidden.mp4" is accepted;
// mutation (return the name uncleaned): "clips/./a.png" does not come back as
// "clips/a.png"; mutation (compare the name's length in runes, or drop the
// cap): the 201-byte name is accepted.
func TestStageArtifactsRefusesEscapes(t *testing.T) {
	refused := []struct{ name, want string }{
		{"../../etc/passwd", "escapes the outputs root"},
		{"/etc/passwd", "absolute"},
		{"a/../../b", "escapes the outputs root"},
		{"..", "escapes the outputs root"},
		{"a/..", "names no file"},
		{"", "is empty"},
		{"a\x00b.png", "NUL"},
		{"a\nb.png", "newline"},
		{strings.Repeat("a", 201), "200"},
		{".hidden.mp4", "begins with a dot"},
		{".hidden/a.png", "begins with a dot"},
	}
	for _, c := range refused {
		got, err := runner.SanitiseName(c.name)
		if err == nil {
			t.Errorf("SanitiseName(%q) = %q, want a refusal", c.name, got)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("SanitiseName(%q): %v, want a reason naming %q", c.name, err, c.want)
		}
	}
	accepted := []struct{ in, want string }{
		{"sizzle.mp4", "sizzle.mp4"},
		{"clips/a.png", "clips/a.png"},
		{"sizzle.v2.mp4", "sizzle.v2.mp4"},
		{"clips/./a.png", "clips/a.png"},
		{strings.Repeat("a", 200), strings.Repeat("a", 200)},
		// The dot rule is the name's first character and nothing else
		// (design §9), so a dot inside a name and a file inside a dot-named
		// directory below the outputs root are both names the retriever
		// hands back, and both accepted here.
		{"clips/.hidden.png", "clips/.hidden.png"},
		{"clips/.hidden/a.png", "clips/.hidden/a.png"},
	}
	for _, c := range accepted {
		got, err := runner.SanitiseName(c.in)
		if err != nil {
			t.Errorf("SanitiseName(%q): %v, want %q", c.in, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("SanitiseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOutputsRefusesMoreFilesThanTheCap is Review Focus 4's count arm:
// max_files counts entries, so a glob matching one file more than it allows
// refuses the run, names the count, the cap and the globs, and leaves the
// bucket and the staging directory untouched.
//
// Mutation (drop the count check): three files are staged and uploaded and
// this test fails; mutation (stage before the caps are checked): the staging
// directory is not empty when the refusal is returned.
func TestOutputsRefusesMoreFilesThanTheCap(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], max_files: 2 }")),
		renderWrites(outFile{name: "a.png", size: 8}, outFile{name: "b.png", size: 8}, outFile{name: "c.png", size: 8}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	for _, want := range []string{"3 files", "out/*.png", "outputs.max_files 2"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q does not name %q", rec.Reason, want)
		}
	}
	assertNothingUploaded(t, h)
}

// TestOutputsRefusesAnOversizedArtifact is Review Focus 4's per-artifact
// arm: max_bytes is ONE artifact's cap, so a single file over it refuses the
// run even though the set as a whole is well under max_total_bytes.
//
// Mutation (compare the set's total against max_bytes instead of each file,
// or drop the per-file check): the oversized file is uploaded and this test
// fails.
func TestOutputsRefusesAnOversizedArtifact(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], max_bytes: 4096, max_total_bytes: 1048576 }")),
		renderWrites(outFile{name: "a.png", size: 64}, outFile{name: "big.png", size: 8192}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	for _, want := range []string{"big.png", "8192 bytes", "outputs.max_bytes 4096 bytes"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q does not name %q", rec.Reason, want)
		}
	}
	assertNothingUploaded(t, h)
}

// TestOutputsRefusesASetOverTheTotal is Review Focus 4's set arm: a set whose
// every member is comfortably under max_bytes can still be over
// max_total_bytes, which is its own key and not max_bytes × max_files. The
// refusal names the count, the total and both caps.
//
// Mutation (drop the set check, or derive max_total_bytes from max_bytes ×
// max_files): the set is uploaded and this test fails.
func TestOutputsRefusesASetOverTheTotal(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], max_bytes: 65536, max_total_bytes: 131072 }")),
		renderWrites(outFile{name: "a.png", size: 60000}, outFile{name: "b.png", size: 60000}, outFile{name: "c.png", size: 60000}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	for _, want := range []string{"3 files", "180000 bytes", "outputs.max_total_bytes 131072 bytes", "outputs.max_bytes 65536 bytes"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q does not name %q", rec.Reason, want)
		}
	}
	assertNothingUploaded(t, h)
}

// TestAGlobThatMatchesNothingIsReportedNotFailed: a render that produces
// nothing the recipe's globs select is not an error by itself — a render may
// legitimately produce nothing — so the run still ends succeeded/artifacts,
// uploads nothing, writes no artifacts key at all, and says so in its report
// (design §8.4: "not an error by itself ... but it is reported"). Turning
// "nothing" into a failure is the probe step's job, when the recipe's owner
// asks for one.
//
// Mutation (fail the run when the globs match nothing): the status becomes
// failed and this test fails; mutation (write an empty artifacts array): the
// record gains a key an old reader never saw.
func TestAGlobThatMatchesNothingIsReportedNotFailed(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p out && echo not-a-png > out/notes.txt\n"))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	raw, err := h.store.ReadFile(context.Background(), "result.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"artifacts":`) {
		t.Fatalf("the record writes an artifacts key for a run that uploaded none: %s", raw)
	}
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the run uploaded %v", keys)
	}
	if files := stagedFiles(t, h); len(files) != 0 {
		t.Fatalf("the run staged %v", files)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "**Files:** none recorded") {
		t.Fatalf("the report does not say the run uploaded nothing:\n%s", report)
	}
}

// TestUploadArtifactsWritesUnderTheRunPrefix is the object-key claim: every
// artifact lands at runs/<slug>/<run>/artifacts/<name>, the name is the
// match's path relative to the deepest common root of the recipe's globs
// (here "out", so out/clips/a.png is "clips/a.png"), and nothing at all is
// written outside the run's own prefix. Asserted from the bucket's key set,
// never from the slice the run returned.
//
// Mutation (upload to the name rather than under the artifacts prefix, or
// use the match's absolute path as the name): the key set is not this set and
// this test fails; mutation (root the names at the checkout rather than at
// the globs' common root): the names gain a ".." or an "out/" element.
func TestUploadArtifactsWritesUnderTheRunPrefix(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png, out/clips/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}, outFile{name: "clips/a.png", body: "clip"}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	want := []string{"clips/a.png", "sizzle.png"}
	got := artifactKeys(t, h)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the bucket holds %v, want %v", got, want)
	}
	prefix := h.store.Prefix()
	for _, k := range bucketKeys(t, h.bucket) {
		if !strings.HasPrefix(k, prefix) {
			t.Errorf("the run wrote outside its own prefix: %s", k)
		}
	}
	var names []string
	for _, a := range rec.Artifacts {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("the record names %v, want %v", names, want)
	}
	if a := rec.Artifacts[0]; a.Bytes != 4 || a.ContentType != "image/png" || len(a.SHA256) != 64 {
		t.Fatalf("clips/a.png = %+v", a)
	}
	if a := rec.Artifacts[1]; a.Bytes != 6 || a.ContentType != "image/png" {
		t.Fatalf("sizzle.png = %+v", a)
	}
}

// TestArtifactHashMatchesTheUploadedBytes is Review Focus 5: the recorded
// sha256 and size are of the bytes actually in the bucket, read back through
// the harness rather than compared against the staging directory.
//
// Mutation (record the render's own path's hash, or the hash of the record's
// name): the read-back hash does not match and this test fails.
func TestArtifactHashMatchesTheUploadedBytes(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}, outFile{name: "b.png", size: 5000}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	for _, a := range rec.Artifacts {
		data := objectBytes(t, h.bucket, h.store.Prefix()+"artifacts/"+a.Name)
		sum := sha256.Sum256(data)
		if int64(len(data)) != a.Bytes {
			t.Errorf("%s: %d bytes in the bucket, the record says %d", a.Name, len(data), a.Bytes)
		}
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			t.Errorf("%s: the bucket's bytes hash to %s, the record says %s", a.Name, hex.EncodeToString(sum[:]), a.SHA256)
		}
	}
	if got := string(objectBytes(t, h.bucket, h.store.Prefix()+"artifacts/sizzle.png")); got != "sizzle" {
		t.Fatalf("sizzle.png = %q in the bucket", got)
	}
}

// TestAStagedFileReplacedBeforeTheUploadFailsTheRun is Review Focus 5's other
// half, and the reason the hash is taken in the staging directory: the bytes
// uploaded are not necessarily the bytes that were hashed, because the plan
// stage's agent is the same user in the same container. A staged file
// replaced between the hash and the upload must fail the run, and must leave
// nothing in the bucket for the hash to be a lie about.
//
// Mutation (upload without checking the staged hash, or record the staged
// hash without comparing it): the run succeeds, the record's sha256 names
// bytes that are not in the bucket, and this test fails.
func TestAStagedFileReplacedBeforeTheUploadFailsTheRun(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	staged := filepath.Join(h.deps.StateDir, "artifacts", "sizzle.png")
	runner.SetArtifactStagedSeam(t, func() {
		if err := os.WriteFile(staged, []byte("swapped after the hash"), 0o644); err != nil {
			t.Errorf("replacing the staged file: %v", err)
		}
	})
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "changed before the upload") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record claims %+v", rec.Artifacts)
	}
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the run left %v in the bucket", keys)
	}
}

// TestArtifactsAreUploadedInNameOrder: the set is uploaded, and recorded, in
// name order — not in glob order, which is what filepath.Glob returns and
// which puts every match of the first glob before every match of the second.
//
// Mutation (drop the sort): the record lists zz.png first and this test
// fails.
func TestArtifactsAreUploadedInNameOrder(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png, out/clips/*.png] }")),
		renderWrites(outFile{name: "zz.png", size: 4}, outFile{name: "aa.png", size: 4}, outFile{name: "clips/mm.png", size: 4}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range rec.Artifacts {
		names = append(names, a.Name)
	}
	want := []string{"aa.png", "clips/mm.png", "zz.png"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("the record names %v, want %v", names, want)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, want) {
		t.Fatalf("the bucket holds %v, want %v", got, want)
	}
}

// TestArtifactNameCollisionAfterCleaningIsRefused: two matches whose names
// collide after cleaning are refused rather than deduplicated or overwritten
// — a silently dropped artifact is a record that lies about what the render
// produced. The two globs here are the same glob written differently, so the
// same file is matched twice and cleans to one name twice.
//
// Mutation (let the second match overwrite the first): the run succeeds with
// one artifact and this test fails.
func TestArtifactNameCollisionAfterCleaningIsRefused(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png, out/./*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "same name") || !strings.Contains(rec.Reason, "sizzle.png") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	assertNothingUploaded(t, h)
}

// TestStageArtifactsRefusesADotNamedMatch is the paired rule: the retriever
// refuses a dot-named entry, and filepath.Glob — unlike a shell — matches a
// leading dot, so without the refusal out/*.png would upload and record a
// .hidden.png that can never be handed back. The whole run is refused, so the
// record never claims an artifact nobody can fetch.
//
// Mutation (drop the dot rule from the sanitiser): .hidden.png is uploaded
// and this test fails.
func TestStageArtifactsRefusesADotNamedMatch(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}, outFile{name: ".hidden.png", body: "hidden"}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, ".hidden.png") || !strings.Contains(rec.Reason, "begins with a dot") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	assertNothingUploaded(t, h)
}

// TestASymlinkOutOfTheOutputsRootIsRefusedNotFollowed: a match that is a
// symlink whose target is outside the outputs root is refused, not followed —
// the bytes would then be a file the render never wrote — and the run
// continues without it, with a warning that names the link. Asserted on the
// bucket: the target's bytes are in no object, and the only artifact is the
// file the render really wrote.
//
// Mutation (copy through the symlink): secret.txt's bytes are uploaded and
// this test fails; mutation (fail the whole run): the record is failed and
// the artifacts assertion fails.
func TestASymlinkOutOfTheOutputsRootIsRefusedNotFollowed(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p out\necho secret > secret.txt\nprintf 'ok' > out/sizzle.png\nln -s ../secret.txt out/leak.png\n"))
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, []string{"sizzle.png"}) {
		t.Fatalf("the bucket holds %v, want only sizzle.png", got)
	}
	for _, k := range bucketKeys(t, h.bucket) {
		if strings.Contains(string(objectBytes(t, h.bucket, k)), "secret") {
			t.Fatalf("%s holds the symlink's target's bytes", k)
		}
	}
	if !strings.Contains(logs.String(), "leak.png") {
		t.Fatalf("the refusal is not in the log:\n%s", logs.String())
	}
}

// TestResultJSONCarriesRenderAndArtifacts: the stored record is the whole
// account of an artifact run — the render step and the files it uploaded,
// each with the name, size, hash and recorded content type — and carries no
// probe key, which is the probe step's own field (Task 5).
//
// Mutation (write the record before the upload): the artifacts array is
// empty and this test fails; mutation (record the probe as a false verdict
// here): the probe key appears.
func TestResultJSONCarriesRenderAndArtifacts(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	raw, err := h.store.ReadFile(context.Background(), "result.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Render    map[string]any   `json:"render"`
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"command", "command_sha256", "tree", "exit_code", "duration_s", "started_at"} {
		if _, ok := doc.Render[key]; !ok {
			t.Errorf("the record's render has no %s: %v", key, doc.Render)
		}
	}
	if len(doc.Artifacts) != 1 {
		t.Fatalf("artifacts = %v", doc.Artifacts)
	}
	a := doc.Artifacts[0]
	if a["name"] != "sizzle.png" || a["bytes"] != float64(6) || a["content_type"] != "image/png" {
		t.Fatalf("artifact = %v", a)
	}
	if sum, _ := a["sha256"].(string); len(sum) != 64 {
		t.Fatalf("artifact sha256 = %v", a["sha256"])
	}
	if _, ok := a["probe"]; ok {
		t.Fatalf("the record carries a probe the probe step has not run: %v", a)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Artifacts) != 1 || stored.Artifacts[0].Name != "sizzle.png" {
		t.Fatalf("the typed record = %+v", stored.Artifacts)
	}
}

// TestReportListsTheArtifacts: the report's per-file table — name, size, a
// short sha256 and the recorded content type — is the upload step's own
// types, so it appears exactly when the record carries artifacts (a record
// without them prints no header over nothing, which Task 2's test pins).
//
// Mutation (drop the table): this test fails; mutation (print the full
// sha256): the short-hash row assertion fails.
func TestReportListsTheArtifacts(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("| `sizzle.png` | 6 | `%s` | image/png |", rec.Artifacts[0].SHA256[:12])
	if !strings.Contains(string(report), row) {
		t.Fatalf("the report has no row %q:\n%s", row, report)
	}
	if !strings.Contains(string(report), "| Artifact | Bytes | sha256 | Type |") {
		t.Fatalf("the report has no table header:\n%s", report)
	}
}

// TestAnOverMemoryArtifactRecipeIsRefusedAtBootstrap is the runner's own arm
// of Review Focus 12: the caps the recipe declares stage a copy beside the
// render's originals, and on Cloud Run that filesystem is memory, so a set
// that cannot fit resources.memory is a killed container with no result.json
// rather than a clean failure. The launcher refuses it too (Task 1, through
// gateCheckProblems and finishLaunch); this is the third arm, for a launch
// whose recipe the launcher could not resolve, and it refuses before the
// first step.
//
// Mutation (drop the outputsMemoryProblem call from RecipeGateProblems): the
// run reaches the render, ends failed with artifacts or not, and this test
// fails on the status and the reason.
func TestAnOverMemoryArtifactRecipeIsRefusedAtBootstrap(t *testing.T) {
	cfg := strings.Replace(renderCfg(t, "", ""), "    timeouts: {",
		"    resources: { memory: 3Gi }\n    timeouts: {", 1)
	if !strings.Contains(cfg, "memory: 3Gi") {
		t.Fatal("the fixture has no workflow block to add resources.memory to")
	}
	h := newHarnessFiles(t, cfg, artifactSpec(sizzleRenderOnlyYAML), renderScript(sizzleScript))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for _, want := range []string{"need 5 GiB of memory", "2 × 2 GiB of outputs", "resources.memory 3Gi"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q does not name %q", rec.Reason, want)
		}
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	assertNothingUploaded(t, h)
}
