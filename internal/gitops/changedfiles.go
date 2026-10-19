package gitops

import (
	"context"
	"fmt"
	"strings"
)

// ChangedFiles lists, sorted, the paths the commits since...HEAD changed:
// the run's own work, and not the commits a person put on the branch before
// the run started (since is origin/<base> for a first run and the commit a
// follow-up started from — the runner's workBase).
//
// It is the path set a run records in its own record (console v2 design D27,
// F17) and so the input of the overlap comparison that reads two of those
// records. Paths only: the diff is read with --name-only, so no content, no
// diff and no commit message leaves this call. A rename is read as the delete
// and the add it is (--no-renames), because a path that moved is a path two
// runs can collide on. --no-ext-diff and --no-textconv keep the operator's
// own git config out of it, and --end-of-options keeps a ref name from being
// read as one.
//
// since is checked the way checkRange checks it — a full commit SHA or a
// plain ref name, never something git could read as an option or a revision
// expression — and HEAD is the upper bound, so the answer is the working
// run's own commits and never a commit the agent has not made yet.
func (r *Repo) ChangedFiles(ctx context.Context, since string) ([]string, error) {
	if !IsFullSHA(since) && !plainRef(since) {
		return nil, fmt.Errorf("refusing to read the changed paths since %q: not a full commit SHA or a plain ref name", since)
	}
	out, err := r.gitRaw(ctx, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--end-of-options", since+"...HEAD")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil // git lists paths in sorted order
}
