package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// rebaseResolves bounds the conflict-resolution stages one rebase run buys.
// A rebase replays the branch's commits one at a time and each one can stop
// on a conflict of its own, so the step asks again while git stops again; the
// bound is what keeps a branch the base has diverged from from buying stage
// after stage, and three is more than a branch of a handful of commits
// against a base that moved needs. A run that spends it ends refused, with
// the conflicts it could not resolve named in the reason.
const rebaseResolves = 3

// rebaseStep is the rebase recipe's whole run (recipe.StepRebase, E5): rung
// R0 of the escalation ladder, which puts the branch of an open pull request
// back on its base so the work can be tried again on what the base is now.
// It is the runner's own machinery with one model stage per conflict and no
// plan walk at all — recipe.Parse admits the step only alone in a mode:
// implement recipe, so the plan is the step and agentLoop dispatches it in
// place of the walk — and it ends the run itself either way it goes: no
// review level runs, so nothing decides readiness for the pull request, and
// the branch it pushed is what a run of its own reviews (G13, D2043).
func (r *run) rebaseStep(ctx context.Context) {
	branch, base := r.rec.Branch, r.cfg.Git.BaseBranch
	if err := r.repo.FetchBase(ctx, base); err != nil {
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("fetching %s before the rebase: %w", base, err).Error(), nil)
		return
	}
	baseSHA, err := r.repo.CommitOf(ctx, "origin/"+base)
	if err != nil {
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("reading origin/%s before the rebase: %w", base, err).Error(), nil)
		return
	}
	err = r.repo.Rebase(ctx, base)
	for resolves := 0; errors.Is(err, gitops.ErrRebaseConflict); resolves++ {
		if resolves >= rebaseResolves {
			// The paths are read before the abort: it is the abort that takes
			// the conflict out of the checkout, and the reason names what
			// nobody resolved.
			conflicts := conflicted(r.unmergedPaths(ctx))
			r.abortRebase(ctx)
			r.endRebase(ctx, runstore.StatusFailed, failure.Unclassified, fmt.Sprintf(
				"rebasing %s onto origin/%s still conflicts%s after %d resolutions: the branch was left as it was and nothing was pushed",
				branch, base, conflicts, rebaseResolves), nil)
			return
		}
		if !r.rebaseResolve(ctx, base) {
			// The stage did not finish: a halt, a cancel or a stage error
			// has already recorded why the run ends, so the step only puts
			// the checkout back and lets that ending stand.
			r.abortRebase(ctx)
			return
		}
		err = r.repo.RebaseContinue(ctx)
	}
	if err != nil {
		r.abortRebase(ctx)
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("rebasing %s onto origin/%s: %w", branch, base, err).Error(), nil)
		return
	}
	head, err := r.repo.HeadSHA(ctx)
	if err != nil {
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("reading the rebased head: %w", err).Error(), nil)
		return
	}
	// The step's own contract, checked rather than assumed: a rebase that
	// left the branch somewhere else — a resolution that rewound it, a git
	// that replayed nothing — has not put it on its base, and pushing it
	// would carry whatever it did land on into the pull request.
	if on, err := r.repo.IsAncestor(ctx, baseSHA, head); err != nil || !on {
		r.endRebase(ctx, runstore.StatusFailed, failure.Unclassified, fmt.Sprintf(
			"the branch is at %s after the rebase onto origin/%s (%s), which is not on the base: nothing was pushed", head, base, baseSHA), nil)
		return
	}
	rec, err := verify.Run(ctx, verify.Options{StateDir: r.d.StateDir, Kind: verify.KindTest, Env: r.env})
	if err != nil {
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("running the workflow's tests after the rebase: %w", err).Error(), nil)
		return
	}
	if !rec.Passed {
		// The tests are re-run for this: a branch whose tests fail on the
		// base it was just put on is exactly what rung 0 was launched to
		// fix, and pushing it would move the pull request's head onto a red
		// commit for nothing. The branch on origin is untouched.
		r.endRebase(ctx, runstore.StatusFailed, failure.TestsFailed, fmt.Sprintf(
			"%s is on origin/%s at %s but the workflow's tests fail after the rebase (%s): nothing was pushed",
			branch, base, head, rec.Summary()), []verify.Record{rec})
		return
	}
	switch err := r.repo.PushExisting(ctx, branch, r.follow.startSHA); {
	case err == nil:
	case errors.Is(err, gitops.ErrBranchGone):
		r.endRebase(ctx, runstore.StatusFailed, failure.BranchMoved, fmt.Sprintf(
			"%s no longer exists on origin; nothing was pushed, and the branch was not recreated", branch), []verify.Record{rec})
		return
	case errors.Is(err, gitops.ErrForeignTip):
		r.endRebase(ctx, runstore.StatusFailed, failure.BranchMoved, fmt.Sprintf(
			"someone pushed to %s during the run; the rebased branch was not pushed over it", branch), []verify.Record{rec})
		return
	default:
		if rej := r.asRefusal(err); rej != nil {
			// The host refused the push for a reason retrying won't change
			// (a workflow file, a protected branch): endRefused records that
			// ending itself, saves the work and posts the note that says so,
			// and its error is the reason it just recorded.
			_ = r.endRefused(ctx, rej, r.spec.PR, []verify.Record{rec})
			return
		}
		r.endRebase(ctx, runstore.StatusInfraError, failure.InfraError, fmt.Errorf("pushing %s: %w", branch, err).Error(), []verify.Record{rec})
		return
	}
	// The head the run pushed, on the record: rung 1 of the ladder is
	// launched at it (E10a, G13), and it is the one figure the next rung
	// cannot recompute from the branch, which may move again by then.
	r.rec.HeadSHA, r.rec.PushedHead = head, head
	r.recordEnd(runstore.StatusSucceeded, runstore.OutcomeNone, "", fmt.Sprintf(
		"%s was put on origin/%s and pushed: %s (the tests pass)", branch, base, head))
	r.skipPR = true
	r.storeEnded(ctx, []verify.Record{rec})
}

// rebaseResolve is one conflict-resolution stage: the branch's conflicts,
// named, handed to the coder's model, which leaves its resolution in the
// working tree for RebaseContinue to carry into the commit being replayed.
// It is the run's implement stage — the coder's account and model, and the
// record's own stage name, because resolving a conflict is writing code —
// and the session is a fresh one: nothing before it ran in this run's
// checkout that a conversation could be resumed from. ok is false when the
// stage did not finish, which is the caller's cue to leave the ending to
// whoever recorded it.
func (r *run) rebaseResolve(ctx context.Context, base string) bool {
	paths, err := r.repo.UnmergedPaths(ctx)
	if err != nil {
		r.d.Log.Warn("reading the conflicted paths failed", "err", r.redact(err.Error()))
	}
	req := agent.Request{
		Prompt:             RebasePrompt(base, paths),
		SessionID:          agent.NewSessionID(),
		AppendSystemPrompt: SystemPrompt(r.systemPromptData(), r.instructions),
	}
	res, ok, _ := r.stage(ctx, "implement", req, stageOpts{})
	r.noteSession(req, res)
	r.countTokens(res)
	return ok
}

// abortRebase puts the checkout back where the rebase found it, so a run
// that leaves a rebase unfinished leaves the branch itself untouched too.
// Best effort: the container is going away with it, and a refusal that
// named this over the conflict would hide the conflict.
func (r *run) abortRebase(ctx context.Context) {
	if err := r.repo.RebaseAbort(ctx); err != nil {
		r.d.Log.Warn("aborting the rebase failed", "err", r.redact(err.Error()))
	}
}

// unmergedPaths names the paths git stopped on, for a reason a person reads;
// a read that fails names none rather than failing the refusal.
func (r *run) unmergedPaths(ctx context.Context) []string {
	paths, err := r.repo.UnmergedPaths(ctx)
	if err != nil {
		r.d.Log.Warn("reading the conflicted paths failed", "err", r.redact(err.Error()))
	}
	return paths
}

// conflicted is the conflicted paths as a reason reads them: nothing, or
// " in a.txt and b.txt".
func conflicted(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return " in " + paths[0]
	default:
		return " in " + strings.Join(paths[:len(paths)-1], ", ") + " and " + paths[len(paths)-1]
	}
}

// endRebase records the ending of a rebase run that pushed nothing: the
// record's status, outcome none and reason, skipPR (a rebase run opens no
// pull request, and this one has nothing to say about the branch it left
// where it was) and the stored report, with the tests' own record beside the
// log tail when the step got as far as running them.
func (r *run) endRebase(ctx context.Context, status runstore.Status, class failure.Class, reason string, records []verify.Record) {
	// A halt or a cancel that landed while the step ran is what happened to
	// this run, not the failure the step met on its way out (a git call a
	// kill interrupted, a test run a cancel ended). The check gate's own
	// commands end the same way, and for the same reason.
	if h := r.haltValue(); h != nil {
		r.endHalted(ctx, h)
		return
	}
	if errors.Is(context.Cause(ctx), ErrCancelled) && r.markCancelled() {
		r.endCancelled(ctx, "cancelled during the rebase step")
		return
	}
	r.recordEnd(status, runstore.OutcomeNone, class, r.redact(reason))
	r.skipPR = true
	r.d.Log.Warn("the rebase run ended without pushing", "status", string(status), "reason", r.rec.Reason)
	r.storeEnded(ctx, records)
}
