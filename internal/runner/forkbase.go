package runner

import (
	"context"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// A first run's base of work is the ref it was cut from (diffBase), and that
// is wrong when the work did not stay there. A run launched without --ref is
// cut from main; a brief that tells its agent to `git fetch origin
// <integration> && git checkout -B work origin/<integration>` moves the work
// onto the integration branch, the runner pushes that HEAD, and every diff
// measured against main is the whole of the integration branch's divergence
// plus the run's change: the review cost diffbase.go describes, round after
// round (the cost inspections of 2026-10-21 and 2026-10-25 found 30 and 7
// such review diffs in two runs).
//
// So the base is read from the history itself: the nearest of origin's
// branches whose remote-tracking tip is in HEAD's history, nearest meaning
// the fewest commits between that tip and HEAD (gitops.NearestBranches).
// For a run whose work stayed where it was cut, that is the ref it was cut
// from — no other branch's tip can be nearer than the start commit the
// run's own commits sit on — and the base is unchanged.
//
// The rules that keep the choice safe:
//
//   - only a branch whose tip is an ancestor of HEAD is ever chosen, and only
//     one that is a branch name (config.ValidBranchName): the name goes into
//     a git revision and into the reviewer's prompt;
//   - run branches (fugaro/...) are never a base: the run's own, pushed by a
//     checkpoint, would make the run's work vanish from its own review;
//   - when the commit the run was cut from is still in HEAD's history, the
//     base must contain it too: a run on main that merged in a branch main
//     has moved past would, measured against that branch, be blamed for
//     main's own commits since the fork. A merged branch that does hold the
//     start commit is a fair base — the diff against it is exactly the run's
//     own change — and the history cannot tell it from one moved onto;
//   - a tie goes to the ref the run was cut from, then to the first name in
//     byte order, so the same history always gives the same base;
//   - anything that cannot be read leaves today's base, refBase.
//
// It never moves the pull request's base (cfg.Git.BaseBranch): the pull
// request still opens against what the configuration says.

// settleForkBase decides a first run's base of work once, before the review
// stage names its range, and keeps it for every diff after: the review
// prompt, the input built for each round, and the guard reading the run's
// own commits at finalize all see the same base, whatever the fix rounds
// commit. A follow-up and a review run have bases of their own (diffBase)
// and are left alone.
func (r *run) settleForkBase(ctx context.Context) {
	if r.forkBaseSettled || r.follow != nil || r.review {
		return
	}
	base, why := r.nearestBase(ctx)
	r.forkBaseSettled = true
	if base == r.refBase() {
		r.d.Log.Info("the run's diffs are measured against the ref it was cut from", "base", r.redact(base), "why", why)
		return
	}
	r.forkBase = base
	r.d.Log.Info("the run's work grew from another branch than the ref it was cut from; its diffs are measured against that branch",
		"base", r.redact(base), "ref", r.redact(r.refBase()), "why", why)
}

// guardDiffBase is the base of work for the guard that reads the run's own
// commits (guardBounds): the settled one once the review stage has decided
// it, and before that — an early draft push during implement, or a run whose
// workflow has no review stage — the one the history gives now.
func (r *run) guardDiffBase(ctx context.Context) string {
	if r.forkBaseSettled || r.follow != nil || r.review {
		return r.diffBase()
	}
	base, _ := r.nearestBase(ctx)
	return base
}

// nearestBase is the branch a first run's work grew from (see above), and
// why it is that one, for the log. It falls back to refBase whenever the
// history cannot be read or names nothing better.
func (r *run) nearestBase(ctx context.Context) (base, why string) {
	fallback := r.refBase()
	if r.repo == nil || r.startSHA == "" {
		return fallback, "the run has no start commit to measure from"
	}
	head, err := r.repo.HeadSHA(ctx)
	if err != nil || !gitops.IsFullSHA(head) {
		return fallback, "HEAD could not be read"
	}
	if head == r.startSHA {
		return fallback, "the run has no commits of its own"
	}
	own := ""
	if r.rec != nil {
		own = r.rec.Branch
	}
	tips, err := r.repo.NearestBranches(ctx, head, func(name string) bool {
		return config.ValidBranchName(name) && name != own && !strings.HasPrefix(name, gitops.RunBranchPrefix)
	})
	if err != nil {
		r.d.Log.Warn("reading which branches the run's work grew from failed; measuring against the ref it was cut from",
			"err", r.redact(err.Error()))
		return fallback, "the branches could not be read"
	}
	startKept, err := r.repo.IsAncestor(ctx, r.startSHA, head)
	if err != nil {
		r.d.Log.Warn("reading whether the run's work still holds its start commit failed; measuring against the ref it was cut from",
			"err", r.redact(err.Error()))
		return fallback, "the start commit could not be compared"
	}
	if startKept {
		kept := tips[:0:0]
		for _, tip := range tips {
			ok, err := r.repo.IsAncestor(ctx, r.startSHA, tip.SHA)
			if err != nil {
				r.d.Log.Warn("comparing a branch with the run's start commit failed; measuring against the ref it was cut from",
					"branch", r.redact(tip.Name), "err", r.redact(err.Error()))
				return fallback, "a branch could not be compared"
			}
			if ok {
				kept = append(kept, tip)
			}
		}
		tips = kept
	}
	tip, ok := pickForkBase(tips, fallback)
	if !ok {
		return fallback, "no branch on origin is in the run's history"
	}
	return tip.Name, "the nearest branch in the run's history, " + strconv.Itoa(tip.Ahead) + " commit(s) before HEAD"
}

// pickForkBase is the nearest of tips, which NearestBranches sorted by
// distance and then by name: among those at the smallest distance, the
// run's own ref (fallback) when it is one of them, else the first. ok is
// false when there is none.
func pickForkBase(tips []gitops.BranchTip, fallback string) (gitops.BranchTip, bool) {
	if len(tips) == 0 {
		return gitops.BranchTip{}, false
	}
	for _, tip := range tips {
		if tip.Ahead != tips[0].Ahead {
			break
		}
		if tip.Name == fallback {
			return tip, true
		}
	}
	return tips[0], true
}
