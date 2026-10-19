package runner

import (
	"context"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// recordChangedFiles writes the paths this run changed into its own record
// (console v2 design D27, F17), bounded and path-only (runstore.NewChangedFiles).
// It is read once, at the top of finalize, and it is never a reason to fail a
// run: a git read that fails leaves the field absent and warns, which reads as
// "this run recorded none" rather than as an empty set.
//
// The base is the run's own start (workBase): a first run diffs against
// origin/<base> and a follow-up against the commit it started from, so a
// person's commits earlier on the branch are not this run's work — the same
// range the workflow guard and the work bundle use.
func (r *run) recordChangedFiles(ctx context.Context) {
	paths, err := r.repo.ChangedFiles(ctx, r.workBase())
	if err != nil {
		r.d.Log.Warn("reading the run's changed paths failed; the record carries none", "err", r.redact(err.Error()))
		return
	}
	r.rec.ChangedFiles = runstore.NewChangedFiles(paths)
}
