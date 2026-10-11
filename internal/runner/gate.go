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

// checkGateCommands refuses a check step whose commands.* key is empty in
// cmds (the fugaro.yaml the run read at ref): a check step the recipe named
// never silently skips (design G10).
func checkGateCommands(steps []recipe.Step, cmds config.Commands, ref string) error {
	for _, s := range steps {
		if gateCommand(s.Command, cmds) == "" {
			return fmt.Errorf("the recipe's check step needs commands.%s in fugaro.yaml at %s", s.Command, ref)
		}
	}
	return nil
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
			changed, err := r.runAutofix(ctx)
			if err != nil {
				return gateResult{}, err
			}
			fixed = fixed || changed
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

// runAutofix runs commands.fix and reports whether it left the working tree
// changed, committing the change with the runner's identity (the same
// gitops call finalize's own leftover commit uses) before returning. A
// command that exits non-zero is not itself an error here: whatever it left
// is still committed, and the check step that follows judges the result, the
// same as it would judge a hand-written fix that didn't fully work.
func (r *run) runAutofix(ctx context.Context) (bool, error) {
	fctx, cancel := context.WithTimeout(ctx, r.wf.Timeouts.Verify.Duration)
	defer cancel()
	_, err := procgroup.Run(fctx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", r.wf.Commands.Fix}, Dir: r.d.WorkDir, Env: r.env,
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	clean, err := r.repo.IsClean(ctx)
	if err != nil {
		return false, err
	}
	if clean {
		return false, nil
	}
	if _, err := r.repo.CommitAll(ctx, "fugaro: autofix (commands.fix)"); err != nil {
		return false, err
	}
	return true, nil
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
// nothing more (r.skipPR).
func (r *run) failInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("the check gate failed", "err", r.rec.Reason)
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
