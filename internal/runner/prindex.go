package runner

import (
	"context"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// writePRIndex records this run in the pull-request index, so `runs ls
// --pr N` finds the runs of a pull request without scanning the repository
// (docs/runs-ls-speed, D2962).
//
// It is called at the two moments a run comes to be on a pull request: the
// run that opens one writes PRIndexOpened the instant its number is known —
// the early draft a checkpoint pushes as much as the pull request finalize
// opens, both of which go through notePR — and a follow-up writes
// PRIndexFollowed at bootstrap, where the provider confirms the pull request
// is the one its task names (checkPullRequest).
//
// The object is the run's own, and the write is create-if-absent, so a
// retry, a second finalize or the launcher's own write of the same run
// (`fugaro run --pr N`, which covers a follow-up started on a job image
// older than the index) leaves what the first write said.
//
// A failure is a warning in the run's log and never the run's own: the index
// is a hint, and a reader checks every run it names against that run's own
// records before it shows a row (D2963), so nothing here may hold up a stage
// or end a run.
func (r *run) writePRIndex(ctx context.Context, pr int, kind runstore.PRIndexKind) {
	// A run with no bucket has no index to write to (a local run), and a
	// run that opened no pull request has no number to write.
	if pr < 1 || r.d.Bucket == nil {
		return
	}
	e := runstore.PRIndexEntry{
		RunID: r.d.Store.RunID(),
		Slug:  r.d.Store.Slug(),
		PR:    pr,
		Kind:  kind,
		At:    r.d.Now().UTC(),
	}
	if err := runstore.PutPRIndex(ctx, r.d.Bucket.Bucket, e); err != nil {
		r.d.Log.Warn("writing the pull-request index entry failed", "pr", pr, "err", r.redact(err.Error()))
	}
}
