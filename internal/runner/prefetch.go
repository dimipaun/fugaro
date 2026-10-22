package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/gitops"
)

// prefetchBound bounds each prefetch fetch, one at a time. The prefetch
// runs at bootstrap, before any stage has recorded time, while the runner
// still holds the run's git credentials; a promisor remote that accepts the
// connection and then stalls — a blackholed network, a hung server, a
// mirror that stopped answering — would otherwise hold the whole run at
// bootstrap for as long as the run's own total timeout allows, hours away,
// with nothing written and no stage ever started. The bound is per fetch,
// not for all of them together: one head that stalls must not eat the other
// heads' time (every head is attempted, below), and a blobless fetch that
// is merely slow still gets the whole bound. A var so a test can shorten
// it.
var prefetchBound = 2 * time.Minute

// The three fetches, named so the warning says which one failed.
const (
	prefetchDiff     = "the diff against the base"
	prefetchCommits  = "the branch's own commits"
	prefetchBaseDiff = "the diff against the base tip"
)

// prefetchFetch runs one of the three through the checkout's own git. It
// applies no bound of its own — fetchWithin does, so the bound is one place
// and every caller gets it — and it is a var so a test can put a stalled
// fetch or a failing one in its place: neither is something the suite can
// get out of a real git on demand.
var prefetchFetch = func(repo *gitops.Repo, ctx context.Context, what, base, head string) error {
	switch what {
	case prefetchDiff:
		return repo.PrefetchDiff(ctx, base, head)
	case prefetchCommits:
		return repo.PrefetchCommits(ctx, base, head)
	case prefetchBaseDiff:
		return repo.PrefetchBaseDiff(ctx, base, head)
	}
	return fmt.Errorf("unknown prefetch %q", what)
}

// fetchWithin runs one prefetch fetch under prefetchBound.
func fetchWithin(ctx context.Context, fetch func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, prefetchBound)
	defer cancel()
	return fetch(ctx)
}

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
// cannot fetch must not leave the rest unfetched. Each fetch is cut off
// after prefetchBound, so a remote that stops answering costs a warning and
// a bounded delay rather than the run.
func (r *run) prefetchObjects(ctx context.Context) {
	if !r.cloneJob() || (r.follow == nil && !r.review) {
		return
	}
	// Each range's own base, not the configuration's (diffbase.go): the
	// reviewer is handed `git diff origin/<the run's base>...HEAD`, so that
	// is the side the prefetch has to bring in.
	for _, rng := range r.diffRanges() {
		base, head := "origin/"+rng.base, rng.head
		for _, what := range []string{prefetchDiff, prefetchCommits, prefetchBaseDiff} {
			err := fetchWithin(ctx, func(ctx context.Context) error {
				return prefetchFetch(r.repo, ctx, what, base, head)
			})
			if err != nil {
				r.d.Log.Warn("prefetching what the agent reads failed; the agent's own git may not be able to read it",
					"what", what, "base", base, "head", head, "err", r.redact(err.Error()))
			}
		}
	}
}
