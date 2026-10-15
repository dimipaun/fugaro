package runner

import "context"

// prefetchObjects brings in the file contents the agent's own git reads,
// using the runner's credentials, at the one moment they are available:
// bootstrap, before any stage runs.
//
// A clone job's checkout is a blobless clone (gitops.CloneBlobless), and a
// checkout fills in the file contents of one revision — the one it checks
// out. A follow-up's and a review run's agent environment carries no git
// credentials (the allowlist agent.BuildEnv builds from; the boundary is
// deliberate), so on a private repository a lazy fetch from the promisor
// remote is refused: `could not fetch <oid> from promisor remote` (review
// N1). The commands such an agent is pointed at need three sets of objects,
// and the runner fetches each, once, while its own git still carries the
// credentials the run was opened with:
//
//   - the branch's diff against the base, `git diff origin/<base>...HEAD`
//     (prompts.go): the base's side, at the merge base, of every file the
//     branch changed (PrefetchDiff);
//   - the branch's own revisions, which `git log -p origin/<base>..HEAD`,
//     `git show <a commit of the branch>` and `git diff HEAD~1` read and a
//     diff of the two endpoints does not touch (PrefetchCommits, review
//     M2);
//   - the base tip's side of `git diff origin/<base>`, wanted whenever the
//     base has moved on since the merge base (PrefetchBaseDiff, review M2).
//
// A baked job is a no-op: a full clone has every object already. A first
// run is skipped too, and for the opposite reason: its agent gets the run's
// git credentials (runner.go), so it fetches for itself, and prefetching
// there would pay the branch's whole divergence at bootstrap for nothing
// (review L1). Its agent's own diff still works — it just fetches it.
//
// A failure is a warning, not a bootstrap error: the objects may well be
// there already (a resumed worktree, a run whose checkout brought them in),
// and a run whose agent never reads them is not broken by a fill that could
// not happen. Every head is attempted even so (review L2): a run reviewing
// several pull requests gets each of them the same chance, and one head git
// cannot fetch must not leave the rest unfetched.
func (r *run) prefetchObjects(ctx context.Context) {
	if !r.cloneJob() || (r.follow == nil && !r.review) {
		return
	}
	base := "origin/" + r.cfg.Git.BaseBranch
	for _, head := range r.diffHeads() {
		for _, want := range []struct {
			what  string
			fetch func() error
		}{
			{"the diff against the base", func() error { return r.repo.PrefetchDiff(ctx, base, head) }},
			{"the branch's own commits", func() error { return r.repo.PrefetchCommits(ctx, base, head) }},
			{"the diff against the base tip", func() error { return r.repo.PrefetchBaseDiff(ctx, base, head) }},
		} {
			if err := want.fetch(); err != nil {
				r.d.Log.Warn("prefetching what the agent reads failed; the agent's own git may not be able to read it",
					"what", want.what, "base", base, "head", head, "err", r.redact(err.Error()))
			}
		}
	}
}

// diffHeads is what the agent's reads are run against the base: the
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
