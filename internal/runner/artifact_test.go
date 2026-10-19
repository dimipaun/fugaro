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
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"gocloud.dev/blob"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

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
	// A run that ends without a pull request still records what it changed:
	// the set is read before finalize's early return, which is the only
	// place an artifact run's own commit could be missed (design D27).
	if rec.ChangedFiles == nil || !slices.Contains(rec.ChangedFiles.Paths, "wip.txt") {
		t.Fatalf("changed files = %+v, want wip.txt", rec.ChangedFiles)
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

// TestAPassingRendersOutputIsRedactedInTheLog is the leak a SUCCESSFUL step
// has and a failing one does not. runRender streams the render's output
// through agent.NewRedactor(tail, r.secretList()) and then logs every line of
// the tail it kept — log.Info("render output", "line", l) — so a render that
// exits 0 and echoes a workflow secret (D10: the render runs with the
// workflow's own secret environment, the same as the agent) has exactly one
// place its output goes, and the redactor is the only thing in front of it. A
// failed render stores a redacted reason and a redacted report tail; a passing
// one stores neither.
//
// Mutation (agent.NewRedactor(tail, nil), or logging the raw line instead of
// the redacted tail's): the runner's log carries the secret and this test
// fails. TestProbeOutputIsRedacted cannot see that mutation: it fails the
// probe, and its reason and report are redacted again on the way out
// (r.redact, agent.Redact), so the layered redaction hides a missing one.
func TestAPassingRendersOutputIsRedactedInTheLog(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nprintf 'the fixture secret is %s\\n' \"$FIXTURE_FAILS_FILE\"\n"+
			"mkdir -p out && printf 'sizzle' > out/sizzle.png\n"))
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	assertTheSecretIsNotPublished(t, h, logs.String(), "render")
}

// TestAPassingProbesOutputIsRedactedInTheLog is the same test for the probe,
// which is the step with the sharper version of the leak: the probe's whole
// job is to inspect what the render produced, so a probe that echoes its
// environment — by accident, in a debug line — is the ordinary shape of a
// repository command, and a passing probe's lines reach the runner's log
// (log.Info("probe output", "line", l)) and nowhere else at all.
//
// Mutation (agent.NewRedactor(tail, nil) in runProbe): the log carries the
// secret and this test fails.
func TestAPassingProbesOutputIsRedactedInTheLog(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nprintf 'the fixture secret is %s\\n' \"$FIXTURE_FAILS_FILE\"\nexit 0\n")))
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 1 || rec.Artifacts[0].Probe == nil || !rec.Artifacts[0].Probe.Ran || rec.Artifacts[0].Probe.ExitCode != 0 {
		t.Fatalf("artifacts = %+v, want one passed probe", rec.Artifacts)
	}
	assertTheSecretIsNotPublished(t, h, logs.String(), "probe")
}

// assertTheSecretIsNotPublished checks the three places a PASSING step's own
// output can reach: the runner's log, result.json and report.md. The log must
// hold the step's line — otherwise a mutation that simply stops logging the
// tail would pass this test by publishing nothing — and none of the three may
// hold the workflow's secret.
func assertTheSecretIsNotPublished(t *testing.T, h *harness, logs, step string) {
	t.Helper()
	if strings.Contains(logs, h.failsFile) {
		t.Fatalf("the runner's log publishes the workflow's secret:\n%s", logs)
	}
	if !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("the runner's log does not hold the %s step's own output at all:\n%s", step, logs)
	}
	for _, key := range []string{"result.json", "report.md"} {
		body, err := h.store.ReadFile(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), h.failsFile) {
			t.Fatalf("%s publishes the workflow's secret:\n%s", key, body)
		}
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

// artifactObjects is the run's artifacts as the bucket holds them, by name:
// the objects under the run's artifacts/ prefix and their bytes. The run's own
// account of itself — result.json, report.md, the render log — is not one of
// them. A scan for bytes that must not have been uploaded is about the files,
// and a refused run's record names the path it refused, which is the record
// doing its job rather than a leak.
func artifactObjects(t *testing.T, h *harness) map[string]string {
	t.Helper()
	prefix := h.store.Prefix() + "artifacts/"
	out := map[string]string{}
	for _, k := range bucketKeys(t, h.bucket) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		out[strings.TrimPrefix(k, prefix)] = string(objectBytes(t, h.bucket, k))
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
		// Every control character, not just the two that would break a line:
		// the name is printed in the report and typed at a shell, and an
		// escape sequence or a bidi override is a name that reads as something
		// other than what it is (TestTheReportEscapesANameThatCouldBreakTheTable
		// is the other half of this: punctuation stays, and is escaped).
		{"a\tb.png", "tab"},
		{"a\x1b[31mb.png", "control"},
		{"a\x07b.png", "control"},
		{"a\x7fb.png", "control"},
		{"a‮b.png", "control"},
		{strings.Repeat("a", 201), "200"},
		{".hidden.mp4", "begins with a dot"},
		{".hidden/a.png", "begins with a dot"},
		// A backslash is a path separator on Windows and an ordinary
		// character everywhere else, so a name carrying one is two different
		// paths depending on the machine that reads it: it is refused on
		// every platform, and the retriever inherits the rule.
		{"a\\b.png", "backslash"},
		{"frames\\..\\..\\x.png", "backslash"},
		{"..\\x.png", "backslash"},
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
		// Punctuation is the render's to choose; only control characters are
		// refused for what they do to a terminal, and the report escapes these.
		{"a|b`c.png", "a|b`c.png"},
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

// TestAProbeThatRewritesAStagedFileFailsTheUpload is the same hash check from
// the probe's side, and it is the one window the probe itself opens: the probe
// runs AFTER the set is staged and hashed and BEFORE the upload (design §3),
// with FUGARO_OUT_DIR naming the staging directory — so a probe is the one
// repository command that is handed the staged bytes by name, and the bytes it
// leaves there need not be the bytes the record describes. Task 4's upload
// compares each staged file's hash with the one taken at staging time, and
// that comparison is what must refuse this: the run ends failed with nothing
// in the bucket, rather than uploading a file whose recorded sha256 is a lie
// about what a reader downloads.
//
// Mutation (upload the staged file without comparing it with the hash taken at
// staging time, or record that hash without checking the upload): the run
// succeeds and the bucket holds bytes the record's sha256 does not describe,
// so this test fails.
func TestAProbeThatRewritesAStagedFileFailsTheUpload(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nprintf 'rewritten by the probe' > \"$FUGARO_OUT_DIR/sizzle.png\"\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a failed run", rec)
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

// TestAStagedPathThatIsASymlinkIsRefused is the staging copy's own
// O_EXCL|O_NOFOLLOW, tested where a test can reach it: the staging directory
// sits in a state directory the plan stage's agent can write (same user, same
// container), so a link planted at a staged path — between the check that
// sized the match and the copy that writes it, which is the window
// artifactCheckedSeam stands in — must fail the create rather than choose what
// the run writes through. The link here points at a file in the checkout; what
// the test asserts is the property, not the errno: the run fails, nothing is
// uploaded, and the target is untouched. (Removing both flags lets the copy
// follow the link and write the render's bytes through it, and the run then
// succeeds with the target rewritten.)
//
// writeArtifactNames carries the same two flags, behind the RemoveAll that
// clears its path first — see TestTheProbesNamesFileIsNotWrittenThroughAPlantedLink.
func TestAStagedPathThatIsASymlinkIsRefused(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	victim := filepath.Join(h.deps.WorkDir, "victim.txt")
	runner.SetArtifactCheckedSeam(t, func() {
		dir := filepath.Join(h.deps.StateDir, "artifacts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Errorf("creating the staging directory: %v", err)
			return
		}
		if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
			t.Errorf("writing the victim file: %v", err)
			return
		}
		if err := os.Symlink(victim, filepath.Join(dir, "sizzle.png")); err != nil {
			t.Errorf("planting the link: %v", err)
		}
	})
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a failed run", rec)
	}
	// The assertion is the property, not the errno: the refusal may come from
	// the copy's own create (EEXIST/ELOOP) or from the upload's Lstat seeing
	// the link still there, and both are the run refusing a link. What must
	// never happen is the third outcome — the copy following it.
	if got := readText(t, victim); got != "untouched" {
		t.Fatalf("the copy wrote through the planted link: the victim holds %q", got)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record claims %+v", rec.Artifacts)
	}
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the run left %v in the bucket", keys)
	}
}

// TestTheProbesNamesFileIsNotWrittenThroughAPlantedLink is the same threat at
// the probe's own file: FUGARO_ARTIFACTS_FILE names a path in the state
// directory, which the plan stage's agent can write, so a link planted there
// must not become the file the runner writes the staged names into — and must
// not become what the probe reads either. writeArtifactNames clears the path
// before it creates it (RemoveAll, then O_EXCL|O_NOFOLLOW), so the link is
// removed and a fresh regular file takes its place; what this test asserts is
// the property that matters: the probe reads the staged names, and the link's
// target is exactly as the test left it.
//
// Mutation (create the names file with O_CREATE|O_TRUNC and no RemoveAll, so
// the planted link is followed): the names are written into the victim and
// this test fails. It does NOT see a mutation that drops only the two flags
// while keeping the RemoveAll — the link is already gone by then — which is
// why the same flags are pinned on the staging copy instead
// (TestAStagedPathThatIsASymlinkIsRefused).
func TestTheProbesNamesFileIsNotWrittenThroughAPlantedLink(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\ncat \"$FUGARO_ARTIFACTS_FILE\" > probe-names.txt\n")))
	victim := filepath.Join(h.deps.WorkDir, "victim.txt")
	runner.SetArtifactStagedSeam(t, func() {
		if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
			t.Errorf("writing the victim file: %v", err)
			return
		}
		if err := os.Symlink(victim, filepath.Join(h.deps.StateDir, "artifact-names.txt")); err != nil {
			t.Errorf("planting the link: %v", err)
		}
	})
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v, want a successful run", rec)
	}
	if got := readText(t, filepath.Join(h.deps.WorkDir, "probe-names.txt")); got != "sizzle.png\n" {
		t.Fatalf("the probe read %q, want the staged names", got)
	}
	if got := readText(t, victim); got != "untouched" {
		t.Fatalf("the runner wrote the names through the planted link: the victim holds %q", got)
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

// TestArtifactNameCollisionAfterCleaningIsRefused: two of the render's files
// whose names collide are refused rather than deduplicated or overwritten — a
// silently dropped artifact is a record that lies about what the render
// produced. The two files here differ only in case, which is the collision
// that survives everything else: two paths, two files, two sets of bytes, and
// one name once a case-insensitive filesystem writes it back — where
// `fugaro artifacts get` would have the second artifact overwrite the first.
//
// (Two globs that match the SAME file are not a collision: they are one
// artifact, which TestOverlappingGlobsSelectOneFileOnce pins.)
//
// Mutation (key the collision map on the name as written, not folded): the run
// succeeds with two artifacts and this test fails; mutation (let the second
// match overwrite the first): the record carries one artifact where the render
// produced two.
//
// It is skipped where the filesystem folds case — macOS's APFS, the default
// there — because the fixture cannot then hold the premise: the render's two
// writes land on one file, so there is one match and no collision to refuse.
// The skip is the fixture's, not the rule's: the rule is about what a
// case-insensitive filesystem would make of two names, which is exactly why it
// cannot be staged on one.
func TestArtifactNameCollisionAfterCleaningIsRefused(t *testing.T) {
	// Two names that differ only in case are one file on a case-insensitive
	// filesystem, APFS among them, and the render's own write merges them
	// there before any table can refuse two artifacts: the assertion is
	// about the KEY, so it needs a filesystem that holds both. On Linux (CI)
	// it does; on a mac this skips rather than fail for the machine.
	requireDistinctNames(t, "Sizzle.png", "sizzle.png")
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "Sizzle.png", body: "one"}, outFile{name: "sizzle.png", body: "two"}))
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "same name") || !strings.Contains(rec.Reason, "Sizzle.png") || !strings.Contains(rec.Reason, "sizzle.png") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	assertNothingUploaded(t, h)
}

// requireDistinctNames skips a test whose point needs two names this
// filesystem keeps apart. A case-insensitive, normalization-insensitive one
// (APFS, where a mac developer runs this suite) stores s.png and ſ.png as one
// file, so the render's own write merges them and there is no collision left
// for the uploader's table to refuse. The skip keeps the assertion about the
// KEY rather than about the machine: on Linux (CI) it runs, and elsewhere it
// says so instead of failing.
func requireDistinctNames(t *testing.T, names ...string) {
	t.Helper()
	probe := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(probe, n), nil, 0o600); err != nil {
			t.Skipf("this filesystem cannot hold %q and its folded twin: %v", names[0], err)
		}
	}
	if ents, err := os.ReadDir(probe); err != nil || len(ents) != len(names) {
		t.Skipf("this filesystem stores %q and %q as one file (%d entries, %v)", names[0], names[1], len(ents), err)
	}
}

// TestArtifactCollisionKeyIsTheOneFoldedName pins the key itself, because two
// callers ask the "are these two names one file" question with it: the
// uploader's duplicate table below and `fugaro artifacts get`'s
// artifactSetConflict (internal/cli). A key that is not the same on both sides
// is the drift this pairs with: a run stages a set its own retriever then
// refuses wholesale.
//
// The unequal pairs are in the table for the same reason as the equal ones.
// U+0130 (İ) is the rune that makes strings.ToLower the wrong key: ToLower maps
// it to 'i', which strings.EqualFold does NOT equate with it, so a lower-cased
// key collides two names that are two files, and refuses İ.png beside i.png.
// Mutation caught: folding with strings.ToLower, or without the NFC pass.
func TestArtifactCollisionKeyIsTheOneFoldedName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a, b  string
		equal bool
	}{
		{"a name with itself", "sizzle.png", "sizzle.png", true},
		{"case", "sizzle.png", "SIZZLE.PNG", true},
		{"the Kelvin sign", "K.png", "K.png", true},
		{"long s", "s.png", "ſ.png", true},
		{"final sigma", "ς.png", "σ.png", true},
		{"NFC and NFD, as macOS stores them", "café.png", "café.png", true},
		{"NFC and NFD, the other way round", "café.png", "café.png", true},
		// The full-folding half (CaseFolding.txt 'F' entries): one rune, two
		// letters, one file on a filesystem that folds the way APFS does.
		// A simple-folded key — strings.EqualFold, the orbit minimum — gives
		// each pair two keys, so a record listing both downloads one file.
		{"sharp s", "straße.png", "strasse.png", true},
		{"sharp s, upper case", "STRAẞE.png", "STRASSE.png", true},
		{"fi ligature", "ﬁle.png", "file.png", true},
		{"fl ligature", "ﬂag.png", "flag.png", true},
		{"ffi ligature", "oﬃce.png", "office.png", true},
		{"ff ligature", "ﬀort.png", "ffort.png", true},
		{"U+0130 folds to i plus a dot, not to i", "I.png", "İ.png", false},
		{"U+0130 is not i either", "İ.png", "i.png", false},
		// The Cherokee pairs, where cases.Fold's tables answer with the other
		// member of the pair instead of CaseFolding.txt's one target: without
		// cherokeeTarget these spellings of one name get a key each.
		{"Cherokee case (U+13A0 / U+AB70)", "Ꭰ.png", "ꭰ.png", true},
		{"Cherokee small letter ye (U+13F0 / U+13F8)", "Ᏸ.png", "ᏸ.png", true},
		{"Cherokee inside a longer name", "aᎥb.png", "aꭵb.png", true},
		// Normalization on both sides of the fold: the order of combining
		// marks, and the pieces a precomposed letter folds into. NFC-then-fold
		// gave each of these pairs two keys, and APFS — one file for each pair
		// — is where the collision would have been written over.
		{"mark order, and a precomposed letter", "ǰ̣.png", "J̣̌.png", true},
		{"Greek with ypogegrammeni", "ᾼ͂.png", "ᾶι.png", true},
		{"a different name", "sizzle.png", "sizzle2.png", false},
		{"a different path", "clips/a.png", "a.png", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ka, kb := runner.ArtifactCollisionKey(tc.a), runner.ArtifactCollisionKey(tc.b)
			if (ka == kb) != tc.equal {
				t.Errorf("ArtifactCollisionKey(%q) = %q, ArtifactCollisionKey(%q) = %q: equal = %v, want %v",
					tc.a, ka, tc.b, kb, ka == kb, tc.equal)
			}
		})
	}
}

// TestArtifactCollisionKeySettlesEveryCherokeePair walks all 172 Cherokee code
// points cases.Fold is not idempotent on, rather than sampling them: the pairs
// are what ArtifactCollisionKey's cherokeeTarget is for, and three rows of the
// table above would pass for a mapping that settled three.
//
// Each pair is asserted both ways round — one key for the two spellings, and
// that key a settled point of the fold (ArtifactCollisionKey of it is itself).
// A key equal on a pair but not settled is a key that moves if anything ever
// folds it again, and this function is handed folded strings as readily as raw
// names.
//
// Mutation caught: dropping the cherokeeTarget pass (returning the fold
// itself), or mapping only one of the two ranges.
func TestArtifactCollisionKeySettlesEveryCherokeePair(t *testing.T) {
	// CaseFolding.txt's Cherokee pairs: the two capital ranges, and the
	// small-letter range each of them folds to.
	ranges := []struct {
		capital, small rune
		runes          int
	}{
		{0x13A0, 0xAB70, 0x50}, // U+13A0–U+13EF and U+AB70–U+ABBF
		{0x13F0, 0x13F8, 0x06}, // U+13F0–U+13F5 and U+13F8–U+13FD
	}
	pairs := 0
	for _, rg := range ranges {
		for i := 0; i < rg.runes; i++ {
			cn, sn := string(rg.capital+rune(i))+".png", string(rg.small+rune(i))+".png"
			kc, ks := runner.ArtifactCollisionKey(cn), runner.ArtifactCollisionKey(sn)
			if kc != ks {
				t.Errorf("ArtifactCollisionKey(%q) = %q, ArtifactCollisionKey(%q) = %q: two spellings of one Cherokee name must share one key",
					cn, kc, sn, ks)
			}
			if again := runner.ArtifactCollisionKey(kc); again != kc {
				t.Errorf("ArtifactCollisionKey(%q) = %q, and folding that key gives %q: the key must be a settled point of the fold",
					cn, kc, again)
			}
			pairs++
		}
	}
	// The pairs checked have to be all of them — 80 + 6, the 86 pairs that the
	// 172 code points of U+13A0–U+13F5, U+AB70–U+ABBF and U+13F8–U+13FD make
	// — so a range that quietly stops short cannot pass as full coverage.
	if pairs != 86 {
		t.Errorf("checked %d Cherokee pairs, want 86 (the 172 code points of U+13A0–U+13F5, U+13F8–U+13FD and U+AB70–U+ABBF)", pairs)
	}
}

// TestArtifactCollisionKeyIsCanonicalForEveryCaseVariant sweeps the blocks
// that hold letters a combining mark can be attached to — every code point in
// them, each with a mark, in both cases — rather than the handful of pairs the
// table names: the mark-order miss the key had was not one pair but a family of
// them (a sweep of this shape found 26 at the time, and the reviewer's wider
// one about 31k), and a key that is right on the two famous pairs and wrong on
// the rest is a key that still writes one file twice.
//
// The reference form is the standard's own definition (Unicode D145, canonical
// caseless match), written out here a second time. That is deliberate: the
// question this sweep asks is whether a spelling the standard calls equivalent
// gets one key, and the answer used to be no.
//
// Every key is also asserted to be in NFD, which is a property of the key
// alone: a key with a precomposed letter in it is a key that has not been
// normalized after the fold, however the pairs happen to fall.
//
// Mutation caught: NFC before the fold (26 of these pairs get two keys),
// normalizing only after the fold (6 of them do), and any key that is not NFD.
// Dropping the second NFD pass after an NFD input is NOT caught, because the
// fold of an NFD string is itself NFD at this version of x/text — the pass is
// the standard's form, not a load-bearing one, and the comment on the function
// says so.
func TestArtifactCollisionKeyIsCanonicalForEveryCaseVariant(t *testing.T) {
	// The standard's canonical caseless form, for the sweep's own use.
	caseless := func(s string) string {
		return norm.NFD.String(cases.Fold().String(norm.NFD.String(s)))
	}
	marks := []rune{0x0300, 0x030c, 0x0323, 0x0342}
	blocks := []struct {
		lo, hi rune
		what   string
	}{
		{0x00c0, 0x02ff, "Latin-1 letters, Latin Extended-A and -B, IPA"},
		{0x0300, 0x03ff, "combining marks, Greek and Coptic"},
		{0x1e00, 0x1fff, "Latin Extended Additional, Greek Extended"},
		{0xfb00, 0xfb4f, "the ligatures, which fold to sequences"},
	}
	checked := 0
	for _, b := range blocks {
		for r := b.lo; r <= b.hi; r++ {
			for _, m := range marks {
				for _, flip := range []func(rune) rune{unicode.ToUpper, unicode.ToLower} {
					x := norm.NFC.String(string(r) + string(m))
					y := norm.NFC.String(string(flip(r)) + string(m))
					if x == y || caseless(x) != caseless(y) {
						continue
					}
					checked++
					kx, ky := runner.ArtifactCollisionKey(x), runner.ArtifactCollisionKey(y)
					if kx != ky {
						t.Errorf("in %s: ArtifactCollisionKey(%q) = %q, ArtifactCollisionKey(%q) = %q: the standard calls these one name", b.what, x, kx, y, ky)
					}
					if nfd := norm.NFD.String(kx); nfd != kx {
						t.Errorf("in %s: ArtifactCollisionKey(%q) = %q is not its own NFD (%q): the key has to be normalized after the fold", b.what, x, kx, nfd)
					}
				}
			}
		}
	}
	// A sweep that quietly stops checking anything passes just as well as a
	// sweep that checks everything, so the count is asserted too.
	if checked < 1000 {
		t.Errorf("sweep checked %d case-variant pairs, want at least 1000: the blocks or the marks have gone missing", checked)
	}
}

// TestArtifactNameCollisionOnAFoldedNameIsRefused: the collision the uploader
// refuses has to be the retriever's, or a run stages a set its own download
// refuses wholesale (`fugaro artifacts get RUN` says the record's names are
// one file, in artifactSetConflict).
//
// The two pairs are the two keys that were wrong, one per direction:
//
//   - "s" and the long s U+017F, which unicode.ToLower leaves alone (so a
//     lower-cased key stages both and the download then writes one file while
//     reporting two).
//   - "straße" and "strasse", which case folding equates only in its FULL
//     form: one rune becomes two letters. strings.EqualFold and
//     unicode.SimpleFold keep them apart, and APFS — the filesystem this key
//     is about — does not: it folds both ways, which is why the uploader has
//     to as well.
//
// The filesystem has to keep the two names apart for the collision to be the
// table's rather than the render's own write, and a case-folding,
// normalization-insensitive one (APFS) does not; where it does not, the test
// skips rather than assert something the filesystem decided.
//
// Mutation (key the collision map on the name as written, on strings.ToLower,
// or on simple folding): the run succeeds with two artifacts and this test
// fails.
func TestArtifactNameCollisionOnAFoldedNameIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a, b  string
		bytes int
	}{
		{"long s", "s.png", "ſ.png", 4},
		{"sharp s (full folding: one rune, two letters)", "straße.png", "strasse.png", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireDistinctNames(t, tc.a, tc.b)
			h := newHarnessFiles(t, renderCfg(t, "", ""),
				artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
				renderWrites(outFile{name: tc.a, size: tc.bytes}, outFile{name: tc.b, size: tc.bytes}))
			rec, err := h.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
				t.Fatalf("record = %+v", rec)
			}
			if !strings.Contains(rec.Reason, "same name") {
				t.Fatalf("reason = %q, want the collision refusal", rec.Reason)
			}
			assertNothingUploaded(t, h)
		})
	}
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
	for name, body := range artifactObjects(t, h) {
		if strings.Contains(body, "secret") {
			t.Fatalf("%s holds the symlink's target's bytes", name)
		}
	}
	if !strings.Contains(logs.String(), "leak.png") {
		t.Fatalf("the refusal is not in the log:\n%s", logs.String())
	}
}

// runWithin runs the harness with a deadline of its own. Several tests below
// are about a run that must NOT block — a FIFO or a device swapped in under
// the staging step is exactly a run that never returns — and a test that hangs
// reports "panic: test timed out" with no name attached to it, or blocks the
// whole package behind go test's own timeout. This turns the same bug into a
// named failure in seconds. The run itself still happens on its own goroutine,
// so nothing in it may call t.Fatal (the seams report with t.Error).
func runWithin(t *testing.T, h *harness, d time.Duration) (*runstore.Record, error) {
	t.Helper()
	type result struct {
		rec *runstore.Record
		err error
	}
	done := make(chan result, 1)
	go func() {
		rec, err := h.run(t)
		done <- result{rec, err}
	}()
	select {
	case r := <-done:
		return r.rec, r.err
	case <-time.After(d):
		t.Fatalf("the run did not finish within %s: it blocked", d)
		return nil, nil
	}
}

// TestASymlinkedOutputsRootIsRefused: the outputs ROOT itself can be a link.
// The render leaves a directory outside the checkout and points out/ at it, so
// every match resolves to a file the repository does not hold. Refusing each
// match as "reached through a symlinked directory" is not enough — the root
// would then simply have moved outside the checkout, and the run would record
// bytes from anywhere the render could write as the repository's own outputs.
// The run is refused instead, before anything is staged, and the reason names
// the root and where it resolves to.
//
// Mutation (drop the within-checkout test on the resolved root): the run
// succeeds and uploads s.png from outside the checkout, and this test fails on
// the status and on the bucket scan.
func TestASymlinkedOutputsRootIsRefused(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p ../elsewhere\nprintf 'elsewhere' > ../elsewhere/s.png\nln -s ../elsewhere out\n"))
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "outputs root") || !strings.Contains(rec.Reason, "elsewhere") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	assertNothingUploaded(t, h)
	for name, body := range artifactObjects(t, h) {
		if strings.Contains(body, "elsewhere") {
			t.Fatalf("%s holds bytes from outside the checkout", name)
		}
	}
}

// TestOverlappingGlobsSelectOneFileOnce: two globs that both match one file
// are one artifact. The overlap is what a recipe naturally writes — out/* and
// out/*.png side by side — and treating the second match as a name collision
// refuses the whole run for a set the recipe asked for.
//
// Mutation (count every match, not every file): the run ends failed with "two
// of the render's files clean to the same name" and this test fails.
func TestOverlappingGlobsSelectOneFileOnce(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*, out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, []string{"sizzle.png"}) {
		t.Fatalf("the bucket holds %v, want only sizzle.png", got)
	}
	if len(rec.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
}

// TestAStagedFileSwappedForADeviceIsRefusedBeforeTheUpload: after the staging
// step has hashed what it wrote, the staged file is replaced by a link to
// /dev/zero. It is the same user and the same state directory, so this swap is
// available to anything the render left running. An upload that opens the path
// it is given and copies it to the end would read /dev/zero forever: no
// object, no result.json, a killed container. The open is what refuses it, so
// the run ends failed, with nothing in the bucket.
//
// The guard that fires here is the Lstat PutFileFrom makes on the path it was
// given: the swap is a moment old, and a staged file that is not a regular file
// is refused with blobx.ErrChanged before anything is opened. Behind it are the
// upload's own open (O_NOFOLLOW) and the copy bounded by the size of the file
// that open returned, which are what hold if the swap lands after the Lstat
// instead of before it. Behind both is the staged-hash comparison in
// PutFileFrom: even bytes read cleanly are refused if they are not the ones
// this run hashed.
//
// Mutation (drop the Lstat together with the open's flags and the bound): the
// run reads /dev/zero until the container is killed — the red run ends
// `signal: killed` with no result.json; mutation (drop only the staged-hash
// comparison): the record attests to bytes the render never wrote.
func TestAStagedFileSwappedForADeviceIsRefusedBeforeTheUpload(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	runner.SetArtifactStagedSeam(t, func() {
		p := filepath.Join(h.deps.StateDir, "artifacts", "sizzle.png")
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink("/dev/zero", p); err != nil {
			t.Error(err)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record attests to artifacts it did not upload: %+v", rec.Artifacts)
	}
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the failed run left %v in the bucket", keys)
	}
}

// TestAMatchSwappedForADeviceBetweenTheCheckAndTheCopy is the check-then-use
// window on the reading side, and the one the size guard has to close on its
// own: the match is a real 6-byte file when it is measured against the caps,
// and by the time it is copied it is a link to a device that never ends.
// Nothing about the caps changes — the copy is simply not the file that was
// measured. What refuses it is the open (O_NOFOLLOW) and, if that flag were
// missing, the bounded copy: reading one byte past the size that was checked
// is enough to know the file changed, so the guard n != want is the only thing
// standing, and without it this run reads a device to the end.
//
// Mutation (open with os.Open and io.Copy it): this test times out.
func TestAMatchSwappedForADeviceBetweenTheCheckAndTheCopy(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	runner.SetArtifactCheckedSeam(t, func() {
		p := filepath.Join(h.deps.WorkDir, "out", "sizzle.png")
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink("/dev/zero", p); err != nil {
			t.Error(err)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	assertNothingUploaded(t, h)
}

// TestAMatchThatGrowsBetweenTheCheckAndTheCopyIsRefused: a file that is still
// being written — a render that forked, a step that was never killed — passes
// every cap when it is measured and is a different file a moment later. The
// seam appends a megabyte, which is the case the old io.Copy to the end could
// not tell from the small file it had just approved: the staged copy was the
// megabyte, and the record's bytes and sha256 described it.
//
// What refuses it is the comparison against the checked size, and there are
// three of them — the fstat on the opened file, the bound on the copy (one
// byte past the size that was approved is enough to know), and the count the
// copy ends with. Any one of them alone catches this append, which is why the
// red step for this test is the honest mutation rather than a single line:
// stage what the copy read instead of comparing it with the size that was
// checked — drop the fstat's size comparison and the count — and the staged
// copy is a megabyte no cap approved.
//
// (TestAMatchSwappedForADeviceBetweenTheCheckAndTheCopy is the same window
// where the bound is not enough on its own, and is where the open is shown.)
//
// Mutation (record the bytes the copy read rather than checking them against
// the size that was measured): the run succeeds with the megabyte uploaded,
// and this test fails on the status and on the reason.
func TestAMatchThatGrowsBetweenTheCheckAndTheCopyIsRefused(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	runner.SetArtifactCheckedSeam(t, func() {
		p := filepath.Join(h.deps.WorkDir, "out", "sizzle.png")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		if _, err := f.Write(bytes.Repeat([]byte("x"), 1<<20)); err != nil {
			t.Error(err)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "changed while it was copied") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	assertNothingUploaded(t, h)
}

// TestAnUploadThatFailsPartwayLeavesNothingInTheBucket: two artifacts, and
// the second is unreadable by the time it is its turn. The first object is
// already written, and a run that ends failed must not leave it there: the
// record says this run uploaded no artifacts, the objects are create-only, and
// a re-execution of the same run would fail on the key it left behind. What
// this call wrote is deleted before the error is returned, and the failure is
// in name order, so the first file is the one that has to go.
//
// The unreadable second file is a FIFO, which is also the other hazard: it is
// the staged file that is not a regular file, and an upload that opens such a
// path without O_NONBLOCK never returns. PutFileFrom's Lstat refuses it here;
// the upload's own open is what would answer if the swap landed after that.
//
// Mutation (drop the cleanup): the bucket holds a.png and this test fails;
// mutation (drop the Lstat and the open's O_NONBLOCK together): the upload of
// b.png blocks on the open and the test fails on runWithin's deadline instead
// of hanging the package.
func TestAnUploadThatFailsPartwayLeavesNothingInTheBucket(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "a.png", body: "one"}, outFile{name: "b.png", body: "two"}))
	runner.SetArtifactStagedSeam(t, func() {
		p := filepath.Join(h.deps.StateDir, "artifacts", "b.png")
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if out, err := exec.Command("mkfifo", p).CombinedOutput(); err != nil {
			t.Errorf("mkfifo %s: %v: %s", p, err, out)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the failed run records %+v", rec.Artifacts)
	}
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the failed run left %v in the bucket", keys)
	}
}

// TestTheStagingDirectoryIsStagedIntoFromEmpty: the staging directory lives in
// the state directory the plan stage's agent can write, so whatever it left
// there is not this run's set. A file planted there must not be uploaded, and
// must not be found beside the staged set either: the run stages into an empty
// directory it made, every time.
//
// Mutation (stage without emptying the directory first): the planted file is
// still there when the set is staged, and this test fails on the listing.
func TestTheStagingDirectoryIsStagedIntoFromEmpty(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	if err := os.MkdirAll(filepath.Join(h.deps.StateDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.deps.StateDir, "artifacts", "planted.png"), []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, []string{"sizzle.png"}) {
		t.Fatalf("the bucket holds %v, want only sizzle.png", got)
	}
	if got := stagedFiles(t, h); !reflect.DeepEqual(got, []string{"sizzle.png"}) {
		t.Fatalf("the staging directory holds %v, want only sizzle.png", got)
	}
}

// TestARefusedSetLeavesTheStagingDirectoryEmpty is the same promise for a set
// that never gets as far as a copy: the render produced more files than the
// recipe allows, so the run is refused, and the staging directory — which the
// agent can write and a previous run of this state may have left something in
// — is left empty rather than holding what was there before plus nothing.
//
// Mutation (stage without emptying the directory first): the planted file
// survives the refusal and this test fails.
func TestARefusedSetLeavesTheStagingDirectoryEmpty(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], max_files: 1 }")),
		renderWrites(outFile{name: "a.png", body: "one"}, outFile{name: "b.png", body: "two"}))
	if err := os.MkdirAll(filepath.Join(h.deps.StateDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.deps.StateDir, "artifacts", "planted.png"), []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "max_files") {
		t.Fatalf("record = %+v", rec)
	}
	assertNothingUploaded(t, h)
	if files := stagedFiles(t, h); len(files) != 0 {
		t.Fatalf("the refused run left %v staged", files)
	}
}

// TestASetThatFailsWhileItIsCopiedLeavesNoStagingDirectory: a copy that fails
// partway must not leave the half-staged set behind — the next step of this
// run, or a later one reusing the state, would find files that no record
// describes. Two matches, and the second is swapped for a device between the
// check and the copy: a.png has been copied and hashed by the time b.png is
// refused, so it is a.png that the removal has to take back.
//
// Mutation (return without emptying the staging directory): a.png is still
// there and this test fails on the listing.
func TestASetThatFailsWhileItIsCopiedLeavesNoStagingDirectory(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderWrites(outFile{name: "a.png", body: "one"}, outFile{name: "b.png", body: "two"}))
	runner.SetArtifactCheckedSeam(t, func() {
		p := filepath.Join(h.deps.WorkDir, "out", "b.png")
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink("/dev/zero", p); err != nil {
			t.Error(err)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("record = %+v", rec)
	}
	if files := stagedFiles(t, h); len(files) != 0 {
		t.Fatalf("the failed run left %v staged", files)
	}
}

// TestAMatchUnderASymlinkedDirectoryIsRefusedNotFollowed is the half of the
// symlink rule that a check on the match's own mode cannot see: out/evil is a
// symlink to the checkout root, so out/evil/secret.png is a plain regular file
// that only exists because the DIRECTORY on the way to it is a link. Nothing
// about the match itself is remarkable, so the question has to be asked of the
// whole path — resolve every element and compare what is left against the
// resolved outputs root — or the run copies, uploads and records bytes from
// outside the root as the render's own output, which is the one thing the rule
// is for.
//
// Mutation (Lstat only the match's final component): secret.png is uploaded
// under the name evil/secret.png and this test fails on the bucket's key set
// and on the "secret" scan.
func TestAMatchUnderASymlinkedDirectoryIsRefusedNotFollowed(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p out/real\nprintf 'ok' > out/real/sizzle.png\nprintf 'secret' > secret.png\nln -s .. out/evil\n"))
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, []string{"real/sizzle.png"}) {
		t.Fatalf("the bucket holds %v, want only real/sizzle.png", got)
	}
	for name, body := range artifactObjects(t, h) {
		if strings.Contains(body, "secret") {
			t.Fatalf("%s holds bytes reached through a symlinked directory", name)
		}
	}
	if !strings.Contains(logs.String(), "out/evil/secret.png") {
		t.Fatalf("the refusal is not in the log:\n%s", logs.String())
	}
}

// TestASymlinkToADirectoryIsSkippedNotFatal pins what happens when a link's
// target IS inside the outputs root: the resolved path passing the root check
// is not the whole answer, because what it resolves TO still has to be a file
// whose bytes can be copied. out/alias -> real is a directory, which the
// plain-directory branch skips with a warning; keeping the resolved path
// instead fails the copy with EISDIR and turns a skipped match into a failed
// run. The record ends succeeded with the one file the render wrote, and the
// warning names the link.
//
// Mutation (keep a resolved target without checking it is a regular file): the
// run ends failed and this test fails on the status.
func TestASymlinkToADirectoryIsSkippedNotFatal(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*] }")),
		renderScript("#!/bin/sh\nmkdir -p out/real\nprintf 'ok' > out/sizzle.png\nln -s real out/alias\n"))
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
	if !strings.Contains(logs.String(), "alias") {
		t.Fatalf("the refusal is not in the log:\n%s", logs.String())
	}
}

// TestASymlinkToAFifoIsRefusedNotOpened is the same check where the old
// behaviour did not merely answer wrongly, it never answered: os.Open on a
// FIFO blocks until a writer appears, and the staging copy takes no context, so
// copying out/stream.png would hang the run until the job was killed and leave
// no result.json at all. The mode comes from metadata, which never blocks, so
// the link is skipped exactly like the plain FIFO beside it — and so is the
// plain FIFO, which is where the same question is asked of a file that is not
// a link at all.
//
// Three guards stand between this run and that hang, and the first one fires
// here: outputProblem's resolved-target check refuses the link before it is a
// copy (drop it alone and the run ends failed with "it is not a regular file",
// on the copy's own fstat). Drop that check, the copy's fstat check and the
// open's O_NONBLOCK together and the run blocks on the FIFO's open: this test
// then fails on the deadline runWithin gives it — by name, in thirty seconds,
// rather than as the package-wide "panic: test timed out after 10m" that says
// nothing about which step hung.
func TestASymlinkToAFifoIsRefusedNotOpened(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p out\nprintf 'ok' > out/sizzle.png\nmkfifo out/pipe\nln -s pipe out/stream.png\n"))
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if got := artifactKeys(t, h); !reflect.DeepEqual(got, []string{"sizzle.png"}) {
		t.Fatalf("the bucket holds %v, want only sizzle.png", got)
	}
	if !strings.Contains(logs.String(), "stream.png") {
		t.Fatalf("the refusal is not in the log:\n%s", logs.String())
	}
}

// TestOneEmptyGlobAmongSeveralIsReported takes design §8.4's "it is reported"
// at the glob rather than at the run: the recipe names two patterns and the
// render produces one of them, so the run is not empty and the all-or-nothing
// warning said nothing about the pattern that matched nothing — the typo a
// reader most needs to see. The file still uploads and the run still succeeds;
// only the log gains a line, and that line names the empty glob and no other.
//
// Mutation (warn only when every glob matched nothing): no line names
// out/*.mp4 and this test fails.
func TestOneEmptyGlobAmongSeveralIsReported(t *testing.T) {
	var logs bytes.Buffer
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png, out/*.mp4] }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
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
	var line string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "matched nothing") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the empty glob is not reported:\n%s", logs.String())
	}
	if !strings.Contains(line, "out/*.mp4") {
		t.Fatalf("the report does not name the empty glob: %s", line)
	}
	if strings.Contains(line, "out/*.png") {
		t.Fatalf("the report names a glob that matched: %s", line)
	}
}

// TestArtifactContentTypeIsTheExtensionThenTheSniff covers both branches of
// the recorded type (design §8.1: recorded, never trusted): the name's
// extension when it means something, and the file's own first bytes when it
// does not — an extension-less name, a name whose extension the mime table
// does not know, a body shorter than the 512-byte sniff buffer and one longer
// than it (the head stops at the buffer, so a longer body is sniffed by its
// first 512 bytes). Where the two disagree the extension wins, because the
// record says what the artifact is called.
//
// The bytes the type is sniffed from are the ones the staging copy produces —
// the table copies each body through CopyFileHashed and hands over what it
// returns — which is the property that matters: the recorded type describes
// the bytes this run staged, and no second open of the staged path exists to
// be pointed at another file or at a FIFO.
//
// Mutation (sniff before the extension): the GIF called sizzle.png is recorded
// as image/gif and this test fails; mutation (drop the sniff): every
// extension-less case is recorded as ""; mutation (sniff the staged path again
// after the copy): a link planted at the staged name is followed, which
// TestTheStagedTypeIsNotSniffedFromTheStagedPath pins.
func TestArtifactContentTypeIsTheExtensionThenTheSniff(t *testing.T) {
	dir := t.TempDir()
	gif := "GIF89a" + strings.Repeat("\x00", 32)
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
	cases := []struct{ name, body, want string }{
		{"sizzle.png", gif, "image/png"},
		{"sizzle", png, "image/png"},
		{"sizzle", "sizzle", "text/plain; charset=utf-8"},
		{"sizzle.fugaroart", strings.Repeat("a", 600), "text/plain; charset=utf-8"},
		{"sizzle.fugaroart", strings.Repeat("a", 511) + "GIF89a", "text/plain; charset=utf-8"},
	}
	for i, c := range cases {
		src := filepath.Join(dir, fmt.Sprintf("src%d", i))
		if err := os.WriteFile(src, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, fmt.Sprintf("staged%d", i))
		_, _, head, err := runner.CopyFileHashed(src, dst, int64(len(c.body)))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, want := len(head), min(len(c.body), 512); got != want {
			t.Fatalf("the head is %d bytes for a %d-byte body, want %d", got, len(c.body), want)
		}
		if got := runner.ArtifactContentType(c.name, head); got != c.want {
			t.Errorf("ArtifactContentType(%q, %d bytes) = %q, want %q", c.name, len(c.body), got, c.want)
		}
	}
}

// TestAFifoAtTheStagedPathAfterTheCopyDoesNotBlockTheRun: the recorded type
// is taken from the bytes the copy read, so the staged path is opened exactly
// once — to create it — and never read back. That matters at the one window
// this test can stand in: between the staged file existing and the record
// describing it. A FIFO swapped in there and read back would block the open
// forever (the staging step takes no context, so nothing the caller does can
// interrupt it), and the run would hang until the container was killed and
// write no result.json at all — the failure this file's other tests are about.
//
// The run ends failed instead, in the ordinary way: the upload's own Lstat
// finds a staged file that is not a regular file and refuses it. It is the
// deadline that turns the mutation into a failure rather than a hang.
//
// Mutation (read the staged file back to sniff it): the open of the FIFO
// blocks — a link to a GIF in its place would answer for a file the run never
// staged — and this test fails on its deadline instead of on the run's own
// refusal.
func TestAFifoAtTheStagedPathAfterTheCopyDoesNotBlockTheRun(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.fugaroart] }")),
		renderWrites(outFile{name: "sizzle.fugaroart", body: "sizzle"}))
	runner.SetArtifactCopiedSeam(t, func() {
		p := filepath.Join(h.deps.StateDir, "artifacts", "sizzle.fugaroart")
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if out, err := exec.Command("mkfifo", p).CombinedOutput(); err != nil {
			t.Errorf("mkfifo %s: %v: %s", p, err, out)
		}
	})
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "not a regular file") {
		t.Fatalf("the run did not refuse the staged file it found: %q", rec.Reason)
	}
	// No artifact was uploaded. The FIFO the seam planted stays in the staging
	// directory — the run failed at the upload, not while staging — so this is
	// the bucket's own claim rather than assertNothingUploaded's.
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the failed run left %v in the bucket", keys)
	}
}

// TestTheStagedTypeIsAFunctionOfTheHead is the other side of that window: the
// type takes the bytes the copy read and nothing else, so there is no path in
// its signature for a later change to point at the staged file. Both hazards
// at the staged path are planted here anyway — a link to a GIF answers
// image/gif, and a writerless FIFO's open never returns — with an empty head
// for the FIFO, which is the plausible excuse a fallback would use for
// reaching for the path.
//
// Mutation (read the staged path when the head does not answer): the first
// case's type is image/gif and the second blocks; this test fails on both.
func TestTheStagedTypeIsAFunctionOfTheHead(t *testing.T) {
	dir := t.TempDir()
	const body = "sizzle"
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "sizzle.fugaroart")
	_, _, head, err := runner.CopyFileHashed(src, staged, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	gif := filepath.Join(dir, "other.gif")
	if err := os.WriteFile(gif, []byte("GIF89a"+strings.Repeat("\x00", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gif, staged); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "staged.fifo")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", fifo, err, out)
	}
	for i, tc := range []struct {
		at   string
		head []byte
		want string
	}{
		{staged, head, "text/plain; charset=utf-8"},
		{fifo, nil, "text/plain; charset=utf-8"},
	} {
		done := make(chan string, 1)
		go func() { done <- runner.ArtifactContentType("sizzle.fugaroart", tc.head) }()
		select {
		case got := <-done:
			if got != tc.want {
				t.Fatalf("case %d: ArtifactContentType with %s at the staged path = %q, want %q", i, filepath.Base(tc.at), got, tc.want)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("case %d: the type was read from %s: reading the staged path blocked", i, filepath.Base(tc.at))
		}
	}
}

// TestResultJSONCarriesRenderAndArtifacts: the stored record is the whole
// account of an artifact run — the render step and the files it uploaded,
// each with the name, size, hash, recorded content type and the probe
// verdict the run recorded for it. This recipe asks for no probe
// (outputs.probe is absent, and its default is none), so every artifact
// carries {"ran": false, "exit_code": -1}: "no probe ran" is stated in the
// record rather than left as a key a reader would have to interpret
// (Task 5, plan Review Focus 6 — the earlier shape of this test asserted the
// opposite, that the key is absent, and moved with the contract).
//
// Mutation (write the record before the upload): the artifacts array is
// empty and this test fails; mutation (leave the probe key off an artifact
// when no probe ran): the verdict assertion fails.
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
	p, ok := a["probe"].(map[string]any)
	if !ok {
		t.Fatalf("the record carries no probe verdict for the artifact: %v", a)
	}
	if p["ran"] != false || p["exit_code"] != float64(-1) {
		t.Fatalf("a recipe that asks for no probe must record {ran: false, exit_code: -1}, got %v", p)
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
// short sha256, the recorded content type and the probe's verdict (Task 5's
// column) — is the upload step's own types, so it appears exactly when the
// record carries artifacts (a record without them prints no header over
// nothing, which Task 2's test pins).
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
	// The recipe asks for no probe, so the verdict column says so — "none",
	// never an empty cell that reads as "passed" (Task 5).
	row := fmt.Sprintf("| `sizzle.png` | 6 | `%s` | image/png | none |", rec.Artifacts[0].SHA256[:12])
	if !strings.Contains(string(report), row) {
		t.Fatalf("the report has no row %q:\n%s", row, report)
	}
	if !strings.Contains(string(report), "| Artifact | Bytes | sha256 | Type | Probe |") {
		t.Fatalf("the report has no table header:\n%s", report)
	}
}

// TestTheReportEscapesANameThatCouldBreakTheTable: the sanitiser refuses
// control characters and keeps punctuation, so a name may hold a pipe or a
// backtick, and both are the report's problem rather than the name's. A bare
// pipe ends the cell — the row grows a column and every value after it moves —
// and a backtick ends the code span around it, so what a reader sees is the
// render's text where the run's account should be. The row is asserted by
// shape: five columns, six separators, and the pipe escaped inside the name.
//
// Mutation (put the name into the cell unescaped): the row has seven
// separators and this test fails.
func TestTheReportEscapesANameThatCouldBreakTheTable(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png] }")),
		renderScript("#!/bin/sh\nmkdir -p out\nprintf 'sizzle' > 'out/a|b`c.png'\n"))
	rec, err := runWithin(t, h, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, l := range strings.Split(string(report), "\n") {
		if strings.Contains(l, "c.png") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("the report has no row for the artifact:\n%s", report)
	}
	if !strings.Contains(row, `\|`) || !strings.Contains(row, "c.png") {
		t.Fatalf("the row does not carry the name, escaped: %s", row)
	}
	if cells := strings.Count(row, "|") - strings.Count(row, `\|`); cells != 6 {
		t.Fatalf("the row has %d cell separators, want 6 (five columns): %s", cells, row)
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

// --- Task 5: the probe — commands.probe, one invocation for the whole set ---

// probeScript is the repository file commands.probe runs, committed to the
// remote beside render.sh: repository text, like the render's (P2/D8).
func probeScript(body string) map[string]string {
	return map[string]string{"probe.sh": body}
}

// probeCfg is renderCfg with commands.probe set: the probe half of an
// artifact workflow. probe "" is a fugaro.yaml with no commands.probe at all,
// which is what outputs.probe: none over a workflow that never configured one
// looks like — and which the gate refuses the moment a recipe names
// outputs.probe: probe.
func probeCfg(t *testing.T, probe, timeouts string) string {
	t.Helper()
	cfg := renderCfg(t, "", timeouts)
	if probe == "" {
		return cfg
	}
	out := strings.Replace(cfg, "      render: sh render.sh\n", "      render: sh render.sh\n      probe: "+probe+"\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no commands.render line to add commands.probe beside")
	}
	return out
}

// mergeFiles joins the extra-file maps one harness fixture needs: an artifact
// run with a probe has both a render script and a probe script committed.
func mergeFiles(maps ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// assertNoUpload is assertNothingUploaded's bucket-only half, and the probe is
// why the other half cannot be used here: the probe runs AFTER the set is
// staged (it inspects the staged bytes, design §3), so a run it fails has a
// populated staging directory and no object. What must be empty is the
// bucket, and what must be absent is the record's claim.
func assertNoUpload(t *testing.T, h *harness) {
	t.Helper()
	if keys := artifactKeys(t, h); len(keys) != 0 {
		t.Fatalf("the run uploaded %v", keys)
	}
}

// readText is a file the fixture's own commands wrote, read for an assertion.
func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// envDump is one of the fixture's `env | sort` dumps, by variable name.
func envDump(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(readText(t, path), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestProbeFailureFailsTheRunAndUploadsNothing is Review Focus 6's first half:
// a probe that exits non-zero fails the run and the upload is refused even
// though the files are staged and within every cap. The probe is the only
// thing that can tell a 0-byte render from a video, so a verdict the runner
// ignored — or a failure it treated as a pass — is the whole feature failing
// open.
//
// Mutation (ignore the probe's exit code): the run ends succeeded/artifacts,
// the artifact is in the bucket, and this test fails on the status and on the
// bucket; mutation (fail the run but upload anyway): the bucket assertion
// fails.
func TestProbeFailureFailsTheRunAndUploadsNothing(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nprintf 'not a video\\n' >&2\nexit 1\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	// The reason names the exit code, and the probe's own last line with it:
	// a repository command's failure is the one thing that explains itself.
	for _, want := range []string{"commands.probe exited 1", "not a video"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q does not name %q", rec.Reason, want)
		}
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record claims %+v", rec.Artifacts)
	}
	// The stage a probe failure ends on is the run's own last step, not one
	// the probe made up: the probe is not a stage (no model turn, no timeout
	// of its own — design §3's rule about the render), result.json's stage
	// enum is a contract a schema-conformant reader validates, and what names
	// the probe is the reason asserted above. The property is enforced in
	// runProbe's shape — it assigns no stage at all — which is why there is
	// no mutation to pin here; this line is the guard that keeps it that way
	// from the record's side.
	if rec.Stage != "writeback" {
		t.Fatalf("stage = %q, want the artifact run's own writeback: a failed probe names itself in the reason, never in the stage", rec.Stage)
	}
	assertNoUpload(t, h)
	// The staged file was there to be inspected: the probe's refusal is about
	// the bytes the run would have uploaded, not about their absence.
	if files := stagedFiles(t, h); len(files) != 1 {
		t.Fatalf("the staging directory holds %v, want the staged set the probe refused", files)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "not a video") {
		t.Fatalf("the report does not quote the probe's output:\n%s", report)
	}
}

// TestProbeEnvironmentNamesTheStagedSet pins the two variables the design
// fixes (design §3): FUGARO_OUT_DIR is the FLAT staging root — so
// "$FUGARO_OUT_DIR/$name" is always the staged file, whatever shape the
// recipe's globs have — and FUGARO_ARTIFACTS_FILE is a file holding the
// sanitised names, one per line, in upload order.
//
// It also pins the environment, which is the other half of what the probe may
// see: the render's own environment plus exactly those two names, no more and
// no less. The probe is repository text like the render, so it gets what the
// render gets — the workflow's secrets included (D10) — and the run adds
// nothing to it beyond the two variables.
//
// Mutation (hand the probe the checkout's outputs root instead of the staging
// directory): the OUT_DIR assertion fails; mutation (a newline-joined list in
// an environment variable instead of a file): FUGARO_ARTIFACTS_FILE's contents
// are not the staged names and the addition assertion fails too; mutation
// (pass an empty env, or r.env plus the git credential): the added-key set is
// not exactly the two, or FIXTURE_FAILS_FILE is missing; mutation (assign
// &probe itself to every row instead of a copy each): the two rows are one
// object and this test fails on the aliasing assertion.
func TestProbeEnvironmentNamesTheStagedSet(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(
			renderScript("#!/bin/sh\nmkdir -p out\nprintf 'one' > out/a.png\nprintf 'two' > out/b.png\nenv | sort > render-env.txt\n"),
			probeScript("#!/bin/sh\nprintf '%s' \"$FUGARO_OUT_DIR\" > probe-out-dir.txt\ncat \"$FUGARO_ARTIFACTS_FILE\" > probe-names.txt\nenv | sort > probe-env.txt\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	for _, a := range rec.Artifacts {
		// One invocation for the whole set, so every file carries the same
		// verdict (design §3).
		if a.Probe == nil || !a.Probe.Ran || a.Probe.ExitCode != 0 || a.Probe.Warning != "" {
			t.Errorf("%s carries %+v, want a passed probe", a.Name, a.Probe)
		}
	}
	// One verdict each, not one verdict shared. Equal in value either way
	// today, because nothing writes to a recorded verdict — which is exactly
	// why the aliasing is worth pinning: the schema keeps a per-artifact
	// warning field for the per-artifact nuance this release defers, and the
	// first writer of one would otherwise change every row of the record and
	// every cell of the report at once. The record asserted here is the
	// runner's own, before result.json round-trips it through JSON, which
	// would give each row its own object regardless.
	if rec.Artifacts[0].Probe == rec.Artifacts[1].Probe {
		t.Fatal("both artifacts share one verdict object: a per-artifact write would change every row")
	}
	dir := filepath.Join(h.deps.StateDir, "artifacts")
	if got := readText(t, filepath.Join(h.deps.WorkDir, "probe-out-dir.txt")); got != dir {
		t.Errorf("FUGARO_OUT_DIR = %q, want the staging directory %q", got, dir)
	}
	if got := readText(t, filepath.Join(h.deps.WorkDir, "probe-names.txt")); got != "a.png\nb.png\n" {
		t.Errorf("FUGARO_ARTIFACTS_FILE holds %q, want the two staged names one per line in upload order", got)
	}
	// Two names, one root: the name the probe is handed is the staged file's
	// path under FUGARO_OUT_DIR, and the object's key leaf too.
	for name, body := range map[string]string{"a.png": "one", "b.png": "two"} {
		if got := readText(t, filepath.Join(dir, name)); got != body {
			t.Errorf("%s holds %q, want the rendered bytes %q", name, got, body)
		}
	}
	renderEnv := envDump(t, filepath.Join(h.deps.WorkDir, "render-env.txt"))
	probeEnv := envDump(t, filepath.Join(h.deps.WorkDir, "probe-env.txt"))
	var added []string
	for k := range probeEnv {
		if _, ok := renderEnv[k]; !ok {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	if want := []string{"FUGARO_ARTIFACTS_FILE", "FUGARO_OUT_DIR"}; !reflect.DeepEqual(added, want) {
		t.Errorf("the probe's environment adds %v to the render's, want %v", added, want)
	}
	for k := range renderEnv {
		if _, ok := probeEnv[k]; !ok {
			t.Errorf("the probe's environment lost %s, which the render had", k)
		}
	}
	if probeEnv["FIXTURE_FAILS_FILE"] == "" {
		t.Error("the probe does not have the workflow's secret the render has (design §8.6, D10)")
	}
	// And the report says the check passed, per file: this is the "ok" cell a
	// real passing probe produces, end to end (probeCell's other arms are in
	// TestReportProbeColumn).
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(report), "| image/png | ok |"); n != 2 {
		t.Fatalf("the report's Probe column says %d files passed, want 2:\n%s", n, report)
	}
}

// TestNoProbeIsRecordedAsRanFalse is Review Focus 6's second half: a recipe
// that asks for no probe (`outputs.probe: none`, the default) runs none, and
// the record SAYS so — {ran: false} on every artifact — rather than leaving a
// reader to infer it from a field that is not there. A commands.probe that is
// configured anyway is not run: the recipe decides, and a workflow that
// carries a probe command for its other recipes must not have it applied here.
//
// Mutation (run commands.probe whenever it is configured): the marker the
// probe would leave exists, the run fails, and this test fails; mutation
// (omit the record's probe object instead of writing {ran: false}): the
// Probe assertion fails.
func TestNoProbeIsRecordedAsRanFalse(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: none }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\ntouch probe-ran.txt\nexit 1\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	if p := rec.Artifacts[0].Probe; p == nil || p.Ran || p.ExitCode != -1 {
		t.Fatalf("the record's probe = %+v, want {ran: false}", rec.Artifacts[0].Probe)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "probe-ran.txt")); err == nil {
		t.Fatal("the probe ran although the recipe asked for none")
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "| Probe |") || !strings.Contains(string(report), "| none |") {
		t.Fatalf("the report's table does not say no probe ran:\n%s", report)
	}
}

// TestProbeTimeoutFailsTheRun: the probe runs under timeouts.verify (it is a
// check, not a render) and under the run's finalize reserve, and a probe the
// deadline kills fails the run rather than being read as a pass — the exit
// code of a killed process says nothing about the deliverable.
//
// Mutation (run the probe with no deadline, or under timeouts.render): the
// probe sleeps its 30s, the run ends succeeded/artifacts, and this test fails
// on the status and on the reason.
func TestProbeTimeoutFailsTheRun(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", "{ total: 5m, stage: 2m, verify: 1s, finalize_reserve: 30s }"),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nsleep 30\n")))
	start := time.Now()
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "commands.probe timed out after 1s") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("the run took %s: timeouts.verify did not bound the probe", d)
	}
	assertNoUpload(t, h)
}

// TestProbeRunsOnlyOnceForTheWholeSet: one process for the whole set, not one
// per artifact (design §3). The alternative — a probe per artifact with the
// path interpolated into a command line — is the one thing the design rejects
// outright, because it puts a runner-derived name inside a shell command.
//
// Mutation (invoke the probe once per staged file): the counter file holds
// two lines and this test fails.
func TestProbeRunsOnlyOnceForTheWholeSet(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "a.png", body: "one"}, outFile{name: "b.png", body: "two"}),
			probeScript("#!/bin/sh\nprintf 'x\\n' >> \"$FUGARO_STATE_DIR/probe-calls.txt\"\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", rec.Artifacts)
	}
	calls := readText(t, filepath.Join(h.deps.StateDir, "probe-calls.txt"))
	if calls != "x\n" {
		t.Fatalf("the probe ran %d times, want once for the whole set", strings.Count(calls, "x"))
	}
}

// TestBlankProbeCommandRefusesTheRun is the probe's fail-closed half, the twin
// of TestBlankRenderCommandRefusesTheRun: a recipe whose outputs.probe names
// commands.probe over a workflow that has none is refused at bootstrap, before
// the agent runs and before anything is rendered. Running the empty string
// through sh -c would exit 0 and record a bogus pass — the failure mode
// commands.lint, commands.fix and commands.render all refuse outright.
//
// Mutation (drop the probe arm of RecipeGateProblems): the run renders, probes
// nothing, and ends succeeded/artifacts with a record that attests to a check
// that never ran.
func TestBlankProbeCommandRefusesTheRun(t *testing.T) {
	h := newHarnessFiles(t, renderCfg(t, "", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		renderWrites(outFile{name: "sizzle.png", body: "sizzle"}))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "probe step needs commands.probe") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	assertNothingUploaded(t, h)
}

// TestProbeOutputIsRedacted is the probe's redaction half, and it is about a
// leak path the render does not have: the probe prints whatever it likes to
// stderr, and it runs with the workflow's own secret environment (D10), so a
// probe that echoes a secret — by accident, in a debug line — must not publish
// it into result.json's reason or report.md's log tail. The output goes
// through the same redactor the agent's streams do.
//
// Redaction here is deliberately layered, so no single mutation of the probe's
// own redactor is observable: the line the probe prints is redacted a second
// time when the run's reason is built and the whole report a third time before
// it is stored. The layers are what make the property survive a mistake in any
// one of them; this test pins the property itself, and the mutation it catches
// is the one that takes the layers away.
//
// Mutation (record the probe's raw tail and stop redacting the reason the
// probe's failure becomes): the secret reaches result.json's reason and this
// test fails on it.
func TestProbeOutputIsRedacted(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nprintf 'the fixture secret is %s\\n' \"$FIXTURE_FAILS_FILE\" >&2\nexit 3\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "commands.probe exited 3") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if !strings.Contains(rec.Reason, "[REDACTED]") {
		t.Fatalf("the reason does not carry the redacted probe line: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, h.failsFile) {
		t.Fatalf("the reason publishes the workflow's secret: %q", rec.Reason)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(report), h.failsFile) {
		t.Fatalf("the report publishes the workflow's secret:\n%s", report)
	}
	assertNoUpload(t, h)
}

// TestProbeCommandComesFromTheWorkflowNotTheSettingsFile is Review Focus 1's
// probe half, the twin of the render's test. The probe that runs is
// commands.probe as bootstrap read it from fugaro.yaml at the task's ref — the
// runner's in-memory r.wf — never a probe key of <StateDir>/workflow.json,
// which is 0644 inside the directory the plan-stage agent writes, because it
// is the same user in the same container. The plan stage here rewrites
// exactly that file: the command that runs is unchanged, and the repository
// text the plan stage substituted never executes. (verify.Settings has no
// Probe field at all, which is the point — the file cannot name a probe even
// if a later change adds one to the struct; what this pins is that the
// runner's command comes from the config it read once.)
//
// Mutation (take the probe's command from verify.LoadSettings, or from any
// file under the state directory, instead of r.wf): the substituted command
// runs, out/HACKED.png exists, and this test fails.
func TestProbeCommandComesFromTheWorkflowNotTheSettingsFile(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec("version: 1\nname: sizzle\nmode: artifact\nsteps:\n  - plan: {}\n  - render: {}\n"+
			"outputs: { globs: [out/*.png], probe: probe }\n"),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\ntouch \"$FUGARO_STATE_DIR/probe-ran.txt\"\n")))
	rewrite := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		settings := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "workflow.json")
		body := `{"build":"sh build.sh","test":"sh test.sh","probe":"mkdir -p out && echo hacked > out/HACKED.png"}`
		if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return planStage(t, ctx, req)
	}
	rec, err := h.run(t, rewrite)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(h.deps.StateDir, "probe-ran.txt")); err != nil {
		t.Fatalf("the workflow's own commands.probe did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "out", "HACKED.png")); err == nil {
		t.Fatal("the probe ran the command the plan stage wrote into the state directory")
	}
}

// TestProbeIsCutAtTheFinalizeReserve is the probe's other bound, the twin of
// TestRenderIsCutAtTheFinalizeReserve: the probe runs under timeouts.verify
// AND under the run's finalize reserve (design §4.5), and when the reserve
// comes first it is the reserve that ends the probe. The budget below leaves
// about eight seconds between the run's start and finalizeAt, with
// timeouts.verify asking for 60s, so a probe that sleeps 30s can only be cut
// by the reserve — and the reason the record carries names the bound that
// applied, rather than quoting a timeouts.verify that still had 58s to run.
//
// Mutations: drop the `f.Before(deadline)` arm of Budget.StepDeadline and
// nothing cuts the probe — it sleeps its full 30s, exits 0, and the run ends
// succeeded/artifacts, so this test fails on its first assertion. Keep the cap
// and revert the reason to "timed out after 60s" and it fails on the reason,
// which no longer names the bound that ended the probe.
func TestProbeIsCutAtTheFinalizeReserve(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", "{ total: 30s, stage: 10s, verify: 60s, finalize_reserve: 22s }"),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\nsleep 30\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a failed run with no artifacts", rec)
	}
	if !strings.Contains(rec.Reason, "finalize reserve") {
		t.Fatalf("the reason does not name the bound that ended the probe: %q", rec.Reason)
	}
	// Naming the STEP and not only the bound. The render's own reserve reason
	// uses the same words ("... was cut at the run's finalize reserve"), and
	// the budget here leaves the render only about eight seconds — so a slow
	// startup could make the RENDER the step the reserve cuts and leave every
	// other assertion in this test satisfied. This one cannot be satisfied
	// that way: it pins the probe as the command the reason names.
	if !strings.Contains(rec.Reason, "commands.probe was cut at the run's finalize reserve") {
		t.Fatalf("the reason does not name the probe as the step the reserve cut: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "timed out after 60s") {
		t.Fatalf("the reason blames a timeout that never fired: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "time budget exhausted") {
		t.Fatalf("the reserve did not hold: %q", rec.Reason)
	}
	assertNoUpload(t, h)
}

// TestProbeGraceBoundsAProbeThatIgnoresSIGTERM: timeouts.verify bounds the
// RUN, not the repository's willingness to stop. The probe runs through
// procgroup, which sends the group SIGTERM when the deadline ends and then
// SIGKILLs it after a grace — so a probe that ignores SIGTERM decides, by
// itself, how long past its deadline the run goes. The probe here traps TERM
// and spins in the shell: no child can die for it, which is the only shape
// that survives SIGTERM, and it costs one core for the few seconds the test
// takes. It records when it started, so what the assertion measures is the
// probe's own wall clock and not the run's (the clone and the render are not
// part of the bound).
//
// The command is `exec sh probe.sh`, and the exec is the point rather than
// decoration: procgroup signals and reaps the process it started, so a
// wrapper shell that does not trap TERM would die on the deadline itself and
// the grace would never be exercised. `exec` makes the trapped shell the
// tracked process, which is what a repository's own probe is when it names a
// binary directly (commands.probe: ./tools/check --slow).
//
// Mutation (drop Grace from the probe's procgroup.Cmd): the SIGKILL lands 10s
// after the deadline — procgroup's default — instead of the 5s verify.Run
// gives its own commands, and this test fails on the elapsed time.
func TestProbeGraceBoundsAProbeThatIgnoresSIGTERM(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "exec sh probe.sh", "{ total: 5m, stage: 2m, verify: 1s, finalize_reserve: 30s }"),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\ntrap '' TERM\ndate +%s%N > \"$FUGARO_STATE_DIR/probe-started\"\nwhile :; do :; done\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "commands.probe timed out after 1s") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(readText(t, filepath.Join(h.deps.StateDir, "probe-started"))), 10, 64)
	if err != nil {
		t.Fatalf("the probe's own start time: %v", err)
	}
	// The probe cannot end itself (it ignores TERM and spins), so this is the
	// deadline plus the grace the runner passed, plus the moment between the
	// SIGKILL and the run finishing. The ceiling is the deadline plus the 5s
	// grace verify uses, with room for a slow box — and well under the 10s
	// procgroup defaults to, which is what makes the mutation visible.
	if d := time.Since(time.Unix(0, ns)); d > 1*time.Second+5*time.Second+4*time.Second {
		t.Fatalf("the probe ran %s past its own start: timeouts.verify (1s) plus the grace the runner passes did not bound it", d)
	}
	assertNoUpload(t, h)
}

// TestProbeTailKeepsAFinalLineWithoutANewline is the probe's half of the
// render's flush test: a failed probe's output is what explains the failure to
// an owner, and it goes through the redactor a line at a time — the redactor
// holds a partial line until a newline arrives, so runProbe must flush it
// before reading the tail. A probe whose failing message ends without a
// newline (a printf with no trailing \n is the ordinary shape of one) is in
// report.md with the flush and gone without it.
//
// Mutation: drop out.Flush() from runProbe and the report's log tail stops
// after the lines that ended in a newline, so this test fails.
func TestProbeTailKeepsAFinalLineWithoutANewline(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\necho 'first line'\nprintf 'the probe failed without a newline'\nexit 4\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "commands.probe exited 4") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "the probe failed without a newline") {
		t.Fatalf("the probe's last line, which has no trailing newline, is not in the report:\n%s", report)
	}
	assertNoUpload(t, h)
}

// TestProbeReasonQuotesTheProbesLastLine: a run's reason is a sentence, so it
// carries the probe's LAST non-empty line — the one a repository's own check
// usually ends on, after whatever it printed while working — and the whole
// output is in the report's log tail instead. A reason that quoted the first
// line would send an owner to the top of a build log rather than to the line
// that explains the exit code.
//
// Mutation (return the first line, or the first non-empty one, from
// probeLastLine): the reason quotes "checking sizzle" and this test fails.
func TestProbeReasonQuotesTheProbesLastLine(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\necho 'checking sizzle'\necho 'sizzle.png is 0 bytes, not a video'\nexit 5\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Reason, "commands.probe exited 5: sizzle.png is 0 bytes, not a video") {
		t.Fatalf("the reason does not quote the probe's last line: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "checking sizzle") {
		t.Fatalf("the reason quotes the probe's first line: %q", rec.Reason)
	}
	// The whole output is still in the report, first line included: the
	// reason is a sentence, the report is the account.
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"checking sizzle", "sizzle.png is 0 bytes, not a video"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("the report does not hold %q:\n%s", want, report)
		}
	}
}

// TestProbeOverAnEmptyStagedSetFailsTheRun is the design's §8.4 headline: a
// render that legitimately produces nothing is not an error by itself, and
// the probe is what turns "nothing" into a failure when the recipe's owner
// asked for one. So the probe runs over an empty set exactly as it runs over
// a full one — one invocation, the same environment, the same deadline — and
// its verdict decides the run: a check that exits non-zero over nothing fails
// the run and uploads nothing (there is nothing to upload, and the record
// claims no artifacts either way).
//
// The probe also exercises the interface it is handed: FUGARO_OUT_DIR must
// name a directory that is really there, empty set included, or the design's
// §8.4 case — the one case where the probe is what judges an empty set — is
// exactly where a probe that cd's into it or lists it dies with a shell error
// instead. So the script below refuses to report its own verdict unless the
// directory exists (exit 3) and holds nothing (exit 4), and the reason the
// record carries is the only one of the three the test accepts.
//
// Mutation (short-circuit the probe when the staged set is empty, skipping it
// as if the recipe had asked for none): the run ends succeeded/artifacts with
// no artifacts and a record that attests to nothing, and this test fails on
// the status, on the reason and on the marker file the probe never wrote.
// Mutation (drop the MkdirAll that creates the staging directory for the arms
// that stage nothing): the probe's own check fails and the reason reads
// "commands.probe exited 3: FUGARO_OUT_DIR is not a directory".
func TestProbeOverAnEmptyStagedSetFailsTheRun(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderScript("#!/bin/sh\nmkdir -p out\n"),
			probeScript("#!/bin/sh\ntouch \"$FUGARO_STATE_DIR/probe-ran.txt\"\n"+
				"[ -d \"$FUGARO_OUT_DIR\" ] || { echo 'FUGARO_OUT_DIR is not a directory'; exit 3; }\n"+
				"if [ -n \"$(ls -A \"$FUGARO_OUT_DIR\")\" ]; then echo 'the staging directory is not empty'; exit 4; fi\n"+
				"echo 'the render produced nothing'\nexit 1\n")))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, want a failed run", rec)
	}
	if !strings.Contains(rec.Reason, "commands.probe exited 1: the render produced nothing") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record claims %+v", rec.Artifacts)
	}
	if _, err := os.Stat(filepath.Join(h.deps.StateDir, "probe-ran.txt")); err != nil {
		t.Fatalf("the probe did not run over the empty set: %v", err)
	}
	// The set it was handed was empty, and it was told so: the probe's own
	// script (above) got past both checks — the directory exists, and it held
	// nothing while the probe ran — since the reason is its exit 1 and not its
	// exit 3 or 4. The staging directory is still empty afterwards, and
	// stagedFiles cannot tell a missing directory from an empty one, which is
	// why the check belongs in the probe.
	if files := stagedFiles(t, h); len(files) != 0 {
		t.Fatalf("the staging directory holds %v, want nothing", files)
	}
	assertNoUpload(t, h)
}

// TestReportProbeColumn pins every arm of the report's Probe column from a
// record, including the two a run of this release cannot produce on its own:
// a non-zero verdict (a failing probe refuses the upload, so no artifact ever
// carries one) and no verdict at all (a record written before the probe step
// existed). Those two are exactly where the column could lie to a reader —
// "ok" for a check the record says failed, or "none" for a recipe whose
// intent the record cannot know — so they are asserted from the record the
// report is rendered from rather than left to whatever a run happens to make.
//
// Mutation (fold !Ran and nil together, or print "ok" for any recorded
// verdict): this test fails on the unprobed and failed rows.
func TestReportProbeColumn(t *testing.T) {
	at := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20261016-100000-abcd", Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeArtifacts,
		StartedAt: at,
		Artifacts: []runstore.ArtifactFile{
			{Name: "legacy.png", Bytes: 6, SHA256: strings.Repeat("a", 64), ContentType: "image/png"},
			{Name: "unprobed.png", Bytes: 6, SHA256: strings.Repeat("b", 64), ContentType: "image/png",
				Probe: &runstore.ProbeResult{Ran: false, ExitCode: -1}},
			{Name: "passed.png", Bytes: 6, SHA256: strings.Repeat("c", 64), ContentType: "image/png",
				Probe: &runstore.ProbeResult{Ran: true, ExitCode: 0}},
			{Name: "failed.png", Bytes: 6, SHA256: strings.Repeat("d", 64), ContentType: "image/png",
				Probe: &runstore.ProbeResult{Ran: true, ExitCode: 3}},
		},
	}
	report := runner.Report(&rec, "runs/acme-app/20261016-100000-abcd/", nil)
	for _, tc := range []struct{ name, sum, cell string }{
		{"legacy.png", "aaaaaaaaaaaa", "—"},
		{"unprobed.png", "bbbbbbbbbbbb", "none"},
		{"passed.png", "cccccccccccc", "ok"},
		{"failed.png", "dddddddddddd", "exit 3"},
	} {
		row := fmt.Sprintf("| `%s` | 6 | `%s` | image/png | %s |", tc.name, tc.sum, tc.cell)
		if !strings.Contains(report, row) {
			t.Errorf("the report has no row %q:\n%s", row, report)
		}
	}
}

// TestCancelDuringTheProbeEndsTheRunCancelled is the cancel branch's probe
// half. The branch reports one reason for the whole artifact step, and the
// probe now sits inside it — it can block for timeouts.verify plus its grace
// — so a run cancelled while a slow probe is checking the set must not be
// told it was cancelled while uploading: nothing had been uploaded, and the
// owner looking for what was running would be sent to the wrong step.
//
// Mutation (name the upload in the collect branch's reason again): the reason
// reads "cancelled while uploading the artifacts" and this test fails.
func TestCancelDuringTheProbeEndsTheRunCancelled(t *testing.T) {
	h := newHarnessFiles(t, probeCfg(t, "sh probe.sh", ""),
		artifactSpec(artifactRecipeYAML("{ globs: [out/*.png], probe: probe }")),
		mergeFiles(renderWrites(outFile{name: "sizzle.png", body: "sizzle"}),
			probeScript("#!/bin/sh\ntouch probe-started\nsleep 30\n")))
	stop := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		marker := filepath.Join(h.deps.WorkDir, "probe-started")
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
	if rec.Reason != "cancelled while collecting the artifacts" {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, "upload") {
		t.Fatalf("a run cancelled in its probe is reported as an upload cancellation: %q", rec.Reason)
	}
	if len(rec.Artifacts) != 0 {
		t.Fatalf("the record claims %+v", rec.Artifacts)
	}
	assertNoUpload(t, h)
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}
