package runner

import "context"

// prefetchDiff brings in the file contents the agent's own diff needs,
// using the runner's credentials, at the one moment they are available:
// bootstrap, before any stage runs.
//
// A clone job's checkout is a blobless clone (gitops.CloneBlobless), and a
// checkout fills in the file contents of one revision — the one it checks
// out. The commands the prompts point the agent at, the review stage's
// `git diff origin/<base>...HEAD` above all (prompts.go), need the other
// side too: the base's version of every file the branch changed. Git
// fetches a missing blob lazily through the clone's promisor remote, which
// is a call to origin, and the agent's own git carries no credentials
// (agent.env's allowlist; the boundary is deliberate) — so on a private
// repository that diff dies with "could not fetch <oid> from promisor
// remote" (review N1). The runner's git does carry them, so the runner
// runs the diff once here, discarding the output, and the objects it
// fetches are what the agent's later diff reads.
//
// It is a no-op for a baked job: a full clone has every object already, so
// there is nothing to fetch and no reason to pay for the diff. A failure
// is a warning, not a bootstrap error — the objects may well be there
// already (a resumed worktree, a repository small enough that the checkout
// brought them in), and a run whose agent never diffs is not broken by a
// fill that could not happen.
func (r *run) prefetchDiff(ctx context.Context) {
	if !r.cloneJob() {
		return
	}
	base := "origin/" + r.cfg.Git.BaseBranch
	for _, head := range r.diffHeads() {
		if err := r.repo.PrefetchDiff(ctx, base, head); err != nil {
			r.d.Log.Warn("prefetching the diff's objects failed; the agent's own diff of the branch against the base may not work",
				"base", base, "head", head, "err", r.redact(err.Error()))
			return
		}
	}
}

// diffHeads is what the agent's diff will be run against the base: the
// checked-out HEAD for a first run and a follow-up, and, for a review run,
// each reviewed head — resolved in checkReviewPRs and not checked out
// until the stage starts, so the objects are brought in by SHA before
// there is a worktree over them.
func (r *run) diffHeads() []string {
	if !r.review {
		return []string{"HEAD"}
	}
	heads := make([]string, 0, len(r.reviewHeads))
	for _, h := range r.reviewHeads {
		heads = append(heads, h.SHA)
	}
	return heads
}
