package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// Mutation (drop the Artifacts section): this test fails.
func TestReportShowsTheRenderAndTheComputeEstimate(t *testing.T) {
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts,
		Cost: &runstore.Cost{ComputeUSD: 0.0312, ComputeEstimated: true},
		Render: &runstore.RenderRecord{Command: "sh render.sh", CommandSHA256: strings.Repeat("b", 64),
			Tree: "working", ExitCode: 0, DurationS: 305, StartedAt: "2026-10-15T10:00:00Z"}}
	rep := runner.Report(rec, "gs://bucket/runs/acme-app/"+runID, nil)
	for _, want := range []string{"#### Artifacts", "**Render:** `sh render.sh` in the working tree, exit 0, 5m5s",
		"**Command sha256:**", "**Compute:** $0.0312 estimated for 5m5s", strings.Repeat("b", 64)} {
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
