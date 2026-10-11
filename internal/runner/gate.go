package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// gateResult is what the check steps found (design generic-tool.md §1, G10,
// G11).
type gateResult struct {
	// Clean is true when every check passed and autofix (if any) changed
	// nothing: there is nothing for implement or a review to do, so the run
	// ends with no PR.
	Clean bool
	// Fixed is true when autofix committed a change and every check then
	// passed: the run skips implement and goes straight to the review steps.
	Fixed bool
	// Failures are the checks that did not pass, in step order, each
	// redacted like every other published verify record.
	Failures []verify.Record
	// Tails are Failures' redacted log tails (at most logtail.DefaultLines
	// lines each), same index as Failures.
	Tails []string
	// FixProblem, non-"" when a step's autofix could not be judged: its
	// commands.fix timed out. The step's check command is then skipped
	// entirely (running it against a tree a timed-out, possibly
	// half-written fix left behind would judge nothing meaningful), and
	// the gate is red (neither Clean nor Fixed). This never quotes
	// commands.fix's own output: runAutofix discards it and records only
	// the exit code and whether it timed out.
	FixProblem string
}

// checkGateSteps returns rcp's leading check steps. recipe.Parse allows at
// most one today ("check may appear at most once", and check steps must
// come before first_line and review), but this holds for however many a
// later recipe version allows.
func checkGateSteps(rcp *recipe.Recipe) []recipe.Step {
	var out []recipe.Step
	for _, s := range rcp.Steps {
		if s.Kind == recipe.StepCheck {
			out = append(out, s)
		}
	}
	return out
}

// gateCommand returns kind's commands.* value from cmds. recipe.Parse only
// ever lets a check step's Command be build, test or lint, so the "" branch
// below is unreachable for an already-valid recipe.
func gateCommand(kind recipe.CheckCommand, cmds config.Commands) string {
	switch kind {
	case recipe.CheckBuild:
		return cmds.Build
	case recipe.CheckTest:
		return cmds.Test
	case recipe.CheckLint:
		return cmds.Lint
	default:
		return ""
	}
}

// gateVerifyKind maps a check step's command to the verify.Kind it runs.
func gateVerifyKind(kind recipe.CheckCommand) verify.Kind {
	switch kind {
	case recipe.CheckBuild:
		return verify.KindBuild
	case recipe.CheckLint:
		return verify.KindLint
	default:
		return verify.KindTest
	}
}

// checkGateCommands refuses a check step whose commands.* key is empty (or
// all whitespace) in cmds, and an autofix step whose commands.fix is empty
// (or all whitespace): a check step the recipe named never silently skips
// (design G10), and autofix: true never silently does nothing (G11) — with
// no commands.fix, runAutofix would otherwise run `sh -c ""`, change
// nothing, and a clean check would report the gate "clean" for a repository
// that was never actually judged as a hand-written fix would be.
func checkGateCommands(steps []recipe.Step, cmds config.Commands, ref string) error {
	for _, msg := range gateCommandProblems(steps, cmds) {
		return fmt.Errorf("the recipe's %s in fugaro.yaml at %s", msg, ref)
	}
	return nil
}

// gateCommandProblems is checkGateCommands' and RecipeGateProblems' shared
// list of what is wrong between steps and cmds, each already worded to
// follow "the recipe's ": "check step needs commands.lint" or "check step
// has autofix: true but no commands.fix".
func gateCommandProblems(steps []recipe.Step, cmds config.Commands) []string {
	var out []string
	for _, s := range steps {
		if strings.TrimSpace(gateCommand(s.Command, cmds)) == "" {
			out = append(out, fmt.Sprintf("check step needs commands.%s", s.Command))
		}
		if s.Autofix && strings.TrimSpace(cmds.Fix) == "" {
			out = append(out, "check step has autofix: true but no commands.fix")
		}
	}
	return out
}

// RecipeGateProblems is what `fugaro validate` reports for rcp's check
// steps against cmds (one workflow's commands.*): the same two refusals
// checkGateCommands makes at launch, so a broken pairing shows up before a
// launch, not only at it.
func RecipeGateProblems(rcp *recipe.Recipe, cmds config.Commands) []string {
	return gateCommandProblems(checkGateSteps(rcp), cmds)
}

// runGate runs steps (the recipe's check steps) in order. Every command it
// runs comes from Settings, written at bootstrap from the fugaro.yaml the
// run read at its ref: never from the task or the recipe (a check step
// only ever names a commands.* key, recipe.CheckCommand). For a step with
// Autofix, commands.fix runs first (through the agent's own environment,
// r.env); any change it leaves is committed with the runner's identity
// before commands.lint judges it.
func (r *run) runGate(ctx context.Context, steps []recipe.Step) (gateResult, error) {
	var g gateResult
	fixed := false
	for _, s := range steps {
		if s.Autofix {
			out, err := r.runAutofix(ctx)
			if err != nil {
				return gateResult{}, err
			}
			if out.TimedOut {
				// The tree is whatever a half-written fix left, uncommitted
				// (runAutofix never commits a timed-out fix's changes):
				// judging it with the check command would judge nothing
				// meaningful, so the step ends here, red.
				g.FixProblem = fmt.Sprintf("commands.fix timed out after %s; its changes were not committed", r.wf.Timeouts.Verify.Duration)
				return g, nil
			}
			fixed = fixed || out.Changed
		}
		rec, err := verify.Run(ctx, verify.Options{StateDir: r.d.StateDir, Kind: gateVerifyKind(s.Command), Env: r.env})
		if err != nil {
			return gateResult{}, err
		}
		if rec.Passed {
			continue
		}
		g.Failures = append(g.Failures, r.redactRecords([]verify.Record{rec})[0])
		tail, err := verify.LogTail(r.d.StateDir, rec.N)
		if err != nil {
			return gateResult{}, err
		}
		g.Tails = append(g.Tails, r.redactTail(tail))
	}
	switch {
	case len(g.Failures) > 0:
		// Red: neither Clean nor Fixed, whatever autofix did.
	case fixed:
		g.Fixed = true
	default:
		g.Clean = true
	}
	return g, nil
}

// autofixOutcome is what running commands.fix found, without its output
// (gate.go never collects it: procgroup.Cmd's Stdout and Stderr are left
// nil, which discards).
type autofixOutcome struct {
	// Changed is whether the working tree differed afterward, and so was
	// committed.
	Changed bool
	// ExitCode is commands.fix's exit code; meaningless (procgroup.Run's own
	// placeholder, -1 or negative) when TimedOut.
	ExitCode int
	// TimedOut is whether the workflow's timeouts.verify killed commands.fix
	// before it exited on its own. A timed-out fix's changes are never
	// committed, whatever they are: runGate ends the step red instead of
	// judging a half-written tree.
	TimedOut bool
}

// runAutofix runs commands.fix and reports what it did, committing any
// change it leaves with the runner's identity (the same gitops call
// finalize's own leftover commit uses) — unless it timed out, in which case
// nothing is committed. A command that exits non-zero but does not time out
// is not itself an error here: whatever it left is still committed, and the
// check step that follows judges the result, the same as it would judge a
// hand-written fix that didn't fully work. Fails closed: with no
// commands.fix (bootstrap's checkGateCommands should already have refused
// this recipe, but a caller is never trusted to have checked first), this
// refuses rather than run an empty command that would look like a no-op fix.
func (r *run) runAutofix(ctx context.Context) (autofixOutcome, error) {
	if strings.TrimSpace(r.wf.Commands.Fix) == "" {
		return autofixOutcome{}, errors.New("commands.fix is empty")
	}
	fctx, cancel := context.WithTimeout(ctx, r.wf.Timeouts.Verify.Duration)
	defer cancel()
	code, err := procgroup.Run(fctx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", r.wf.Commands.Fix}, Dir: r.d.WorkDir, Env: r.env,
	})
	timedOut := errors.Is(err, context.DeadlineExceeded)
	if err != nil && !timedOut {
		return autofixOutcome{}, err
	}
	// The exit code and whether it timed out are the only facts about this
	// run ever recorded or logged: never commands.fix's output, which may
	// hold whatever the repository's own command prints.
	if timedOut {
		r.d.Log.Warn("commands.fix timed out; its changes were not committed", "timeout", r.wf.Timeouts.Verify.Duration)
		return autofixOutcome{TimedOut: true}, nil
	}
	if code == 127 {
		r.d.Log.Warn("commands.fix could not run", "exit_code", code)
	} else {
		r.d.Log.Info("commands.fix ran", "exit_code", code)
	}
	clean, err := r.repo.IsClean(ctx)
	if err != nil {
		return autofixOutcome{}, err
	}
	if clean {
		return autofixOutcome{ExitCode: code}, nil
	}
	if _, err := r.repo.CommitAll(ctx, "fugaro: autofix (commands.fix)"); err != nil {
		return autofixOutcome{}, err
	}
	return autofixOutcome{Changed: true, ExitCode: code}, nil
}

// redactTail is text (a verify.LogTail) redacted whole and then clipped per
// line to the published width, the same two-step order runner.go's own
// logTail uses: redacting a whole line first means a secret straddling the
// clip is never published half-redacted.
func (r *run) redactTail(text string) string {
	lines := strings.Split(r.redact(text), "\n")
	for i, l := range lines {
		lines[i] = logtail.Clip(l, logtail.DefaultLineBytes)
	}
	return strings.Join(lines, "\n")
}

// endWithoutPR ends a run whose check gate found nothing to fix: no stage
// ever ran, so there is nothing to commit further, push or review. Status
// succeeded, outcome none; finalize, which still runs after, does nothing
// more (r.skipPR).
func (r *run) endWithoutPR(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusSucceeded, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeEnded(ctx, nil)
}

// failInfra ends the run as infra_error: the check gate itself could not
// run (a command's process, a git or state-dir operation), not a check that
// ran and failed. Outcome none; finalize, which still runs after, does
// nothing more (r.skipPR). The caller checks first whether the gate's error
// is really a cancel (context.Cause(ctx), ErrCancelled) and calls
// endCancelled instead when it is: this is only for a genuine infra error.
func (r *run) failInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("the check gate failed", "err", r.rec.Reason)
	r.storeEnded(ctx, nil)
}

// endCancelled ends a run whose check gate was running when a cancel
// landed (design §4.2a E10, the same outcome a cancel during any other
// stage gets): status cancelled, outcome none, never infra_error — a
// cancelled `fugaro verify`/commands.fix returns a context error indistinct,
// to the gate, from any other process failure, so the caller tells them
// apart first (context.Cause(ctx), ErrCancelled) and calls this instead of
// failInfra when it really was a cancel. finalize, which still runs after,
// does nothing more (r.skipPR).
func (r *run) endCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeEnded(ctx, nil)
}

// endHalted ends a run whose check gate concluded clean, but a halt (a
// budget cap, a kill switch, the token cap) was recorded while its commands
// ran: status halted, outcome none — never succeeded, which would leave an
// internally contradictory record once the top-level deferred code in
// Run also carries Record.Halt. finalize, which still runs after, does
// nothing more (r.skipPR).
func (r *run) endHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeEnded(ctx, nil)
}

// verifyOnce runs a verify record for kind exactly as the agent's own
// `fugaro verify` would, so the readiness rule (Decide) has an ordinary
// verified record for the autofix commit a fixed gate just made. A failure
// here is not special-cased: it simply leaves no passing record for the
// commit, so the run ends a draft the same way a failed `fugaro verify`
// during implement would.
func (r *run) verifyOnce(ctx context.Context, kind verify.Kind) {
	if _, err := verify.Run(ctx, verify.Options{StateDir: r.d.StateDir, Kind: kind, Env: r.env}); err != nil {
		r.d.Log.Warn("verifying the check gate's autofix commit failed", "err", r.redact(err.Error()))
	}
}
