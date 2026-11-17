package cli

import (
	"context"
	"io"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// troubleshootTierEvidence is D2400's fourth tier as the document's tier field
// spells it: the tier that answers when the cloud diagnosis is declined or does
// not answer, and what a person is left with is the evidence where it already
// is. It is a value a reader outside this binary consumes (fugaro troubleshoot
// --json, the history, the console), so the word is written once, here, rather
// than spelled again at every reader.
const troubleshootTierEvidence = "evidence"

// troubleshootDeclineCloud is D2401's decline, in one line: the cloud diagnosis
// is not available, the gate's own sentence as that gate wrote it, and that the
// evidence follows. It is what a person reads when the cloud tier is the tier
// that declined — the local credential is absent and the launch path itself is
// broken, which is the case the tier order exists for (D2400): a job launched
// through that path would meet the same gate and fail a second time instead of
// explaining the first.
//
// The reason is quoted and never reworded, because it is the gate's own
// sentence and it already names the command that fixes it (fugaro update in the
// repository's checkout, fugaro image refresh, --no-budget-check): a paraphrase
// here would be a second sentence about the refusal, and the fix a person needs
// is the one the gate wrote. oneLine is what keeps the whole decline one line —
// a reason carrying a newline would otherwise hand the printer a second line to
// wrap, and that line is not part of the refusal.
//
// Nothing here exits or prints the evidence: this returns the sentence, and the
// caller reaches the offline path with it, which prints the evidence and exits
// non-zero (D1608).
func troubleshootDeclineCloud(reason error) string {
	return "the cloud diagnosis is not available: " + oneLine(reason.Error()) + ". The evidence follows."
}

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
//
// The step the diagnosis's recipe holds has a gate of its own beside the
// variant's image (checkDiagnoseImage, D2401 and F18): the runner of an image
// that predates the diagnose step refuses the whole recipe at bootstrap, after
// the container was started and the bundle uploaded, so a diagnosis is refused
// here at the terminal instead, naming the release that carries the step and
// fugaro update. It is judged the same way and from the same read as the
// variant's image gate above — the two are one build record apart in what they
// require, not in what they read — and it is asked after it, so an image too
// old for the variant is refused with the variant's sentence first.
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
		if err := checkCloneDiagnoseImage(env.lc, spec, io.Discard); err != nil {
			return err
		}
	} else {
		if err := checkReviewJobImage(ctx, env, slug, spec, kind, io.Discard); err != nil {
			return err
		}
		if err := checkDiagnoseImage(ctx, env, slug, spec, kind, io.Discard); err != nil {
			return err
		}
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
