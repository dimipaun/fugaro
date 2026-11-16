package cli

import (
	"context"
	"io"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// troubleshootLaunchPath is the cloud diagnosis's pre-flight (D2401): the
// launch path's own judgement of the spec a diagnosis would launch, made
// without launching any of it. It is `freshLaunchGates` itself and not a list
// of gates copied out of it (F8), so a gate the launch gains is met here
// without this file being revisited — which is the whole reason the
// alternative D2401 declines exists; the daemon's own launcher already calls
// that same block and then writes its task itself
// (orchestrator_launcher.go), so calling it without launching is a shape the
// launch path already has.
//
// Nothing here writes. `freshLaunchGates`'s one write is attachLaunch's, and
// this pre-flight passes no --attach — the diagnosis's bundle goes up after
// this judgement, through its own upload — so the call creates no task, mints
// no token, writes no claim and starts no execution. It takes no
// --no-budget-check escape either: that flag is an operator's at a launch, and
// a pre-flight that skipped the budget would say the path is healthy exactly
// where the launch it stands for would be refused.
//
// Two groups of checks the block does not carry for this spec are called by
// name below, and both are the review-only variant this diagnosis runs on
// (D2403). The variant's own image and the installation's admission of it sit
// inside `if spec.Mode == recipe.ModeReview`, which a `mode: artifact` spec
// never enters; and the three checks only checkReviewLaunch makes live in no
// block at all. checkReviewHeadIsStill, that function's fourth call, is
// deliberately not among them: it judges a pull request's head, and a
// diagnosis has no pull request.
func troubleshootLaunchPath(ctx context.Context, env *cloudEnv, o *runOptions, slug string, s *runstore.Store, spec *task.Spec) error {
	// A copy, so a caller's --attach is not silently carried into a launch
	// shape that uploads nothing: the block's own attach arm would gate and
	// prepare files this pre-flight has no use for.
	attachFree := *o
	attachFree.attach = nil
	compression, _ := compressionRequested(ctx, env, spec)
	if _, err := freshLaunchGates(ctx, env, &attachFree, slug, s, spec, compression || spec.CompressionOff, io.Discard, false); err != nil {
		return err
	}
	// The workflow shape is resolved the way the launch resolves it — the same
	// function, not a second read of the same document — because the variant's
	// image is judged from the local config for a checkout: clone workflow and
	// from the job's build record for any other.
	_, kind, clone, _, _, err := launchWorkflowShape(ctx, env, spec)
	if err != nil {
		return err
	}
	if clone {
		if err := checkCloneReviewJobImage(env.lc, spec, io.Discard); err != nil {
			return err
		}
	} else if err := checkReviewJobImage(ctx, env, slug, spec, kind, io.Discard); err != nil {
		return err
	}
	if err := checkReviewAdmission(env.lc, io.Discard); err != nil {
		return err
	}
	if err := checkReviewService(env); err != nil {
		return err
	}
	if err := checkReviewJobAdmitted(); err != nil {
		return err
	}
	return checkReviewAccountState(ctx, env, spec)
}
