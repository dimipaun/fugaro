package runner_test

import (
	"context"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// prIndexObjects is every object name under a repository's GCS pull-request
// index prefix, whatever number it is filed under: what a test asserting that
// nothing was written has to look at.
func prIndexObjects(t *testing.T, b *blob.Bucket, slug string) []string {
	t.Helper()
	it := b.List(&blob.ListOptions{Prefix: "runs/" + slug + "/" + runstore.PRIndexDir + "/"})
	var out []string
	for {
		obj, err := it.Next(context.Background())
		if err != nil {
			break
		}
		if !obj.IsDir {
			out = append(out, obj.Key)
		}
	}
	return out
}

// TestRunnerWritesNoPRIndex: the runner writes nothing under
// runs/<slug>/pr-index/ from either of the two paths that wrote an entry for
// a run of its own — the run that opens the pull request (notePR) and the
// follow-up that starts on one (checkPullRequest) — and an entry a bucket
// already holds is left exactly as it was: the objects stay in place, and so
// does what they say (D3204). The review job's own path is pinned beside the
// writer whose line it was (TestRunnerWritesNoPRIndexFromTheReviewArtifact,
// reviewartifact_test.go), because that writer is the run's own code.
func TestRunnerWritesNoPRIndex(t *testing.T) {
	// A run that opens its pull request: the moment its number is known was
	// the first write, and it is the one `runs ls --pr N` used to find a live
	// run by.
	t.Run("the run that opens the pull request", func(t *testing.T) {
		h := prHarness(t, prCfg(t, 2, ""))
		b := withBucket(h)
		theirs := runstore.PRIndexEntry{RunID: "20260925-221530-0bad", Slug: repoSlug, PR: 1,
			Kind: runstore.PRIndexOpened, At: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
		if err := runstore.PutPRIndex(context.Background(), b.Bucket, theirs); err != nil {
			t.Fatal(err)
		}
		if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
			t.Fatal(err)
		} else if rec.PR == nil || rec.PR.Number != 1 {
			t.Fatalf("the run opened no PR 1: %+v", rec)
		}
		got, err := runstore.ListPRIndex(context.Background(), b.Bucket, repoSlug, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].RunID != theirs.RunID {
			t.Fatalf("entries = %+v, want only the one that was already there, %s", got, theirs.RunID)
		}
		if got[0].Kind != theirs.Kind || got[0].Slug != theirs.Slug || got[0].PR != theirs.PR || !got[0].At.Equal(theirs.At) {
			t.Errorf("the entry that was already there was rewritten: %+v, want %+v", got[0], theirs)
		}
		if objs := prIndexObjects(t, b.Bucket, repoSlug); len(objs) != 1 {
			t.Errorf("objects = %q, want only the entry the bucket already held", objs)
		}
	})

	// A follow-up: its entry went in at bootstrap, where the provider
	// confirmed the pull request its task names.
	t.Run("the follow-up that starts on one", func(t *testing.T) {
		h := followUpHarness(t, "", func(h *harness) { withBucket(h) })
		if objs := prIndexObjects(t, h.bucket, repoSlug); len(objs) != 0 {
			t.Fatalf("the run that opened the pull request wrote %q", objs)
		}
		h.followUp(t, followID, runID, "")
		if rec, err := h.run(t, withAnswer(implement("rename"), "Renamed the helper."), review("ship", 0)); err != nil {
			t.Fatal(err)
		} else if rec.PR == nil || rec.PR.Number != 1 {
			t.Fatalf("the follow-up is not on PR 1: %+v", rec)
		}
		if objs := prIndexObjects(t, h.bucket, repoSlug); len(objs) != 0 {
			t.Errorf("the follow-up wrote %q", objs)
		}
	})
}
