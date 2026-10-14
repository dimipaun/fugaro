package runner_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// advanceMain commits change on top of main in a fresh clone of h's remote
// and pushes it: the base moves past the pull request's merge base, which
// is what makes the base-side blob of a file the pull request also changed
// one the checkout of the base never brings in.
func advanceMain(t *testing.T, h *harness, change func(dir string)) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-main")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	change(seed)
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "someone else's commit on main")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return testutil.Git(t, seed, "rev-parse", "HEAD")
}

// diffThenReview is the review stage running the prompt's own command —
// `git diff origin/<base>...HEAD` (prompts.go) — in the agent's environment,
// which is the allowlist with no git credentials of its own, before giving
// its verdict. shell fails the test if the diff fails.
func diffThenReview(verdict string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "git diff origin/main...HEAD")
		return review(verdict, 0)(t, ctx, req)
	}
}

// TestCloneReviewPrefetchesTheDiff (review N1): a clone job's checkout is
// blobless, so the file contents it holds are the checked-out revision's
// alone; `git diff origin/<base>...HEAD` also needs the base's side of
// every file the branch changed, and the agent's git has no credentials to
// fetch them with. The runner runs that diff itself, at bootstrap, while
// its own git still carries the run's credentials, and the objects it
// brings in are the ones the agent's diff then reads.
//
// The remote demands the run's token (useHTTPRemote), which is what the
// agent's environment lacks: its lazy fetch is refused, exactly as it is on
// a private repository. main is moved past the pull request's merge base
// first, so the base-side blob is genuinely one the checkout of the base
// tip did not bring in.
//
// Mutation (run, restore): make r.prefetchDiff return without calling
// PrefetchDiff — the review stage's diff fails with "could not fetch <oid>
// from promisor remote", which is the reported bug.
func TestCloneReviewPrefetchesTheDiff(t *testing.T) {
	readme := testutil.FixtureFiles(t)["README.md"]
	h := newHarness(t, cloneYAML, reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	remote := h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	// The server has to honour the filter (or the clone is not blobless at
	// all) and to serve the single object a lazy fetch asks for.
	testutil.Git(t, remote.Bare, "config", "uploadpack.allowFilter", "true")
	testutil.Git(t, remote.Bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	h.cloneJob(t)
	sha := pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"README.md": readme + "\ntheir change\n"})
	})
	advanceMain(t, h, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"README.md": readme + "\nthe base moved\n"})
	})
	addPR(h, 7, "someone-elses-change", "")

	rec, err := h.run(t, diffThenReview("ship"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	if len(rec.Reviewed) != 1 || rec.Reviewed[0].SHA != sha {
		t.Fatalf("reviewed = %+v, want the pull request's head %s", rec.Reviewed, sha)
	}
}
