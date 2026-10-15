package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// renderTree is the tree a render runs in: the working checkout, uncommitted
// work included, which the plan stage shared. It is recorded in
// runstore.RenderRecord.Tree, whose only legal value it is today — the
// design's D8 is what would add a second one (plan P2).
const renderTree = "working"

// renderPlanPath is where a mode: artifact run's plan stage is asked to leave
// its plan: a file in the checkout, read by nobody but a person (the render
// the runner runs is commands.render, never a plan the model wrote).
const renderPlanPath = ".fugaro/render-plan.json"

// runArtifactMode runs artifactMode, recovering a panic so the run always
// ends with a record, the same guarantee runAgentLoop and runReviewMode
// give their own modes.
func (r *run) runArtifactMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			// The same priority every other end branch gives a halt: it
			// decides the run whatever else is true, a panic mid-stage
			// included.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			r.failArtifactInfra(ctx, fmt.Errorf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.artifactMode(ctx)
}

// artifactMode runs a mode: artifact run (design artifact-runs.md §3): the
// optional plan stage, then the one render step. The run ends with outcome
// artifacts on success, or failed/none otherwise; it never pushes, never
// opens a pull request and sets r.skipPR, so finalize, which still runs
// after, does nothing more.
//
// An artifact run's agent gets no git credentials (the agent env block and
// refreshGitAuth both skip r.artifact, exactly as they skip r.review): the
// deliverable goes to the runs bucket, never to a branch, so a push
// credential in its environment would only be reachable by the plan stage's
// own prompt injection for nothing.
func (r *run) artifactMode(ctx context.Context) {
	if r.artifactPlan {
		r.rec.Stage = "plan"
		r.save(ctx)
		req := agent.Request{Prompt: r.artifactPlanPrompt(), SessionID: agent.NewSessionID()}
		res, ok, _ := r.stage(ctx, "plan", req, stageOpts{})
		r.countTokens(res)
		if !ok {
			// The same priority gate.go's own error branch follows: a halt
			// decides the run whatever else is true, before cancel is even
			// asked about.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			if r.isCancelled() {
				r.endArtifactCancelled(ctx, "cancelled during the plan stage")
				return
			}
			r.endArtifactFailed(ctx)
			return
		}
		// r.stage checked haltValue() right before returning ok=true, but a
		// halt can land in the window between that check and here.
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
	}
	rec, err := r.runRender(ctx)
	r.renderRec, r.rec.Render = &rec, &rec
	r.updateCost()
	switch {
	case err == nil:
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeArtifacts
	case r.haltValue() != nil:
		// A halt that landed while the render ran: recorded by the stage's
		// own machinery (the budget backend polls independently), and a halt
		// decides the run whatever the render did.
		r.endArtifactHalted(ctx, r.haltValue())
	case r.isCancelled():
		r.endArtifactCancelled(ctx, "cancelled during render")
	default:
		r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(err.Error())
		r.d.Log.Warn("the render step failed", "err", r.rec.Reason)
	}
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// runRender runs commands.render once, in the checkout, under
// timeouts.render and the run's own finalize reserve, with the agent's own
// environment (the workflow's secrets included, design §8.6/D10). Its exit
// code decides the run.
//
// The command string and the timeout come from the runner's IN-MEMORY
// workflow config (r.wf.Commands.Render, r.wf.Timeouts.Render), which
// bootstrap set once at runner.go and nothing re-reads: this function is
// NOT a verify.LoadSettings caller. That is deliberate, not an omission —
// <StateDir>/workflow.json is 0644 inside a directory the plan-stage agent
// can write (it is the same user in the same container), and
// verify.LoadSettings re-reads it per invocation. A render that loaded its
// command from there could be made to run a string the reviewer never read,
// while render.command_sha256 attested to it (design §3, Review Focus 1).
//
// The record's Command is the redacted string and CommandSHA256 the hash of
// the string the runner RAN (the unredacted one), so the two are one hash
// pair exactly when the redactor did not fire (design D8).
func (r *run) runRender(ctx context.Context) (runstore.RenderRecord, error) {
	command := r.wf.Commands.Render
	started := r.d.Now()
	rec := runstore.RenderRecord{
		Command:       r.redact(command),
		CommandSHA256: sha256Hex(command),
		Tree:          renderTree,
		ExitCode:      -1,
		StartedAt:     started.UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(command) == "" {
		// Fail closed: bootstrap's checkGateCommands already refuses a
		// recipe whose render step has no commands.render, but a caller is
		// never trusted to have checked first, and an empty command would
		// otherwise run as a successful no-op.
		return rec, errors.New("commands.render is empty")
	}
	r.rec.Stage = "render"
	r.save(ctx)
	stageCtx, cancel := r.budget.StepContext(ctx, r.wf.Timeouts.Render.Duration)
	defer cancel()
	// The render's output is a repository's own command's, so it goes
	// through the same redactor the agent's streams do, whole lines at a
	// time, and only its tail is kept.
	tail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	out := agent.NewRedactor(tail, r.secretList())
	code, err := procgroup.Run(stageCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: r.d.WorkDir, Env: r.env, Stdout: out, Stderr: out,
	})
	rec.DurationS = r.d.Now().Sub(started).Seconds()
	rec.ExitCode = code
	rec.TimedOut = errors.Is(stageCtx.Err(), context.DeadlineExceeded)
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: "render", StartedAt: started, DurationS: rec.DurationS})
	log := r.d.Log.With("stage", "render")
	switch {
	case rec.TimedOut:
		// procgroup.Run reports a command killed by ctx as ctx.Err().
		log.Warn("commands.render timed out", "timeout", r.wf.Timeouts.Render.Duration, "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render timed out after %s", r.wf.Timeouts.Render.Duration)
	case err != nil:
		return rec, fmt.Errorf("running commands.render: %w", err)
	case code != 0:
		log.Warn("commands.render failed", "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render exited %d", code)
	}
	log.Info("commands.render finished", "exit_code", code, "duration_s", rec.DurationS)
	for _, l := range tail.Lines() {
		log.Info("render output", "line", l)
	}
	return rec, nil
}

// noteRenderTail keeps the render's last lines as the report's log tail: a
// failed render's output is the one thing that explains it.
func (r *run) noteRenderTail(tail *logtail.Writer) {
	lines := tail.Lines()
	if len(lines) == 0 {
		return
	}
	r.tail = &LogTail{Source: "the render step", Lines: r.redactAll(lines)}
}

// artifactPlanPrompt is the plan stage's prompt (design §3): the task text,
// the recipe's own use_when, and what the run will render afterwards. It is
// the only place the run describes the deliverable to the agent, so it says
// plainly that the render is the runner's, not the agent's.
//
// It names no FUGARO_INPUTS_DIR and no attachment: that variable arrives
// with the task that adds attachments, beside FUGARO_STATE_DIR in the env
// this run builds, and until then a plan stage cannot see an input because
// nothing can attach one.
func (r *run) artifactPlanPrompt() string {
	var b strings.Builder
	b.WriteString("This is a Fugaro artifact run: the deliverable is a set of files uploaded to this run's bucket, not a pull request. There is no pull request to open, no branch is pushed, and the plan you write is not the deliverable.\n\n")
	name := r.rec.Recipe.Name
	fmt.Fprintf(&b, "The runner renders the deliverable itself, once, after this stage: it runs the workflow's own render command (commands.render in fugaro.yaml at %s) in this checkout and uploads the files the recipe's outputs.globs name. Do not run that render command, and do not try to produce the artifacts yourself.\n\n", r.spec.Ref)
	if u := r.useWhen; u != "" {
		fmt.Fprintf(&b, "The recipe `%s` is for: %s\n\n", name, u)
	}
	if r.out != nil && len(r.out.Globs) > 0 {
		fmt.Fprintf(&b, "The artifacts it expects are the files matching: %s (at most %d files, %s in total).\n\n",
			strings.Join(r.out.Globs, ", "), r.out.FileCount(), fmtBytes(r.out.MaxSetBytes()))
	}
	fmt.Fprintf(&b, "Write your plan for the render to `%s` in this checkout: what the render will have to produce, exactly which files it should leave behind and where, what inputs in the repository it needs, and what is likely to go wrong. Keep it short and concrete.\n\n", renderPlanPath)
	if t := strings.TrimSpace(r.spec.Task); t != "" {
		b.WriteString("**Task:**\n\n" + t + "\n")
	}
	return b.String()
}

// fmtBytes is an artifact cap for a prompt: a whole number of bytes with a
// human unit, never a bare digit string a reader has to decode.
func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// storeArtifactEnded stores an artifact run's report.md — the ordinary run
// report, whose outcome arm and Artifacts section are this task's (report.go)
// — under every terminal state below.
func (r *run) storeArtifactEnded(ctx context.Context) {
	report := agent.Redact(followUpReport(r.rec, r.d.Store.Prefix(), r.tail, nil, nil), r.secretList())
	r.storeReport(ctx, report, nil)
}

// endArtifactCancelled ends an artifact run that was cancelled: status
// cancelled, outcome none, never infra_error. finalize, which still runs
// after, does nothing more (r.skipPR).
func (r *run) endArtifactCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// endArtifactHalted ends an artifact run that a halt (a budget cap, a kill
// switch, the token cap) stopped. Its outcome is none — a halted artifact
// run has no artifacts, exactly as a halted review run posts no comment —
// which is why the halt advice in halt.go needs no arm of its own for this
// mode.
func (r *run) endArtifactHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// failArtifactInfra ends an artifact run whose own operation (the checkout,
// the state directory, the render's process) failed outright: status
// infra_error, outcome none.
func (r *run) failArtifactInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("the artifact run failed", "err", r.rec.Reason)
	r.storeArtifactEnded(ctx)
}

// endArtifactFailed ends an artifact run whose plan stage failed (the model
// timed out, crashed, or reported an error) or whose render ended non-zero:
// status failed, outcome none — no pull request to fall back to, and no
// artifacts to claim.
func (r *run) endArtifactFailed(ctx context.Context) {
	reason := r.failure()
	if reason == "" {
		reason = "the plan stage ended without a result"
	}
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(reason)
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// artifactHasPlan reports whether rcp's steps lead with a plan stage: the
// optional agent stage of a mode: artifact recipe (a recipe's artifact
// shape is exactly [plan], render or [render]).
func artifactHasPlan(rcp *recipe.Recipe) bool {
	for _, s := range rcp.Steps {
		if s.Kind == recipe.StepPlan {
			return true
		}
	}
	return false
}

// sha256Hex is the render record's CommandSHA256: lowercase hex of the
// sha256 of the string the runner ran, which is the text a reviewer can
// hash from the repository at the recorded ref.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
