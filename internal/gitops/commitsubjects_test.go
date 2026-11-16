package gitops

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestCommitSubjectsListsTheRunsOwnSubjects: the subjects are the run's own
// commits (since...HEAD) and not the branch's history — a person's commit made
// before the run started is out of the list, and in it when since is the base
// the branch started from. Newest first, and subjects only, because these lines
// are pasted into the checkpoint prompt's own facts (internal/runner/
// checkpoint_stage.go): a body is where a message runs long, and the prompt
// spends its input bound on what the run did and not on how it worded it.
//
// Mutation (--reverse): the order assertion fails. Mutation (%s → %B): the
// body's first line is not the subject any more and the list assertion fails.
func TestCommitSubjectsListsTheRunsOwnSubjects(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	// A person's commit on the branch, before the run's base.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"earlier.txt": "mine\n"})
	if _, err := repo.CommitAll(ctx, "a person's work"); err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")

	// The run's own work, one commit of it carrying a body.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"feature.txt": "the feature\n"})
	if _, err := repo.CommitAll(ctx, "Add the feature\n\nA body the prompt must not carry:\nit is where a message runs long.\n"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"tests.txt": "the tests\n"})
	if _, err := repo.CommitAll(ctx, "Fix the tests"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CommitSubjects(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Fix the tests", "Add the feature"}
	if !slices.Equal(got, want) {
		t.Fatalf("since the run's base: %v, want %v", got, want)
	}

	// The whole branch, base included: the person's commit is in this range,
	// which is exactly what reading the run's own facts from a narrower base
	// keeps out of the prompt.
	all, err := repo.CommitSubjects(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Fix the tests", "Add the feature", "a person's work"}; !slices.Equal(all, want) {
		t.Fatalf("since origin/main: %v, want %v", all, want)
	}

	// A range that holds no commit is an empty list and not a nil one:
	// checkpointWork reads "the run made no commit" and "the commits could not
	// be read" apart from exactly this.
	//
	// Mutation (var subjects []string): the non-nil assertion fails.
	none, err := repo.CommitSubjects(ctx, "HEAD")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("since HEAD: %v, %v; want an empty, non-nil list", none, err)
	}

	// A refs/replace/<sha> planted in the checkout the agent writes would
	// otherwise change what the range holds: the base would read as the commit
	// the planted ref names, and the list would be that traversal's rather than
	// the run's own commits.
	//
	// Mutation (drop --no-replace-objects): this read answers with a traversal
	// of the replaced graph, three subjects where the run made two.
	first := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	testutil.Git(t, repo.Dir, "replace", base, first)
	if got, err := repo.CommitSubjects(ctx, base); err != nil || !slices.Equal(got, want) {
		t.Fatalf("with a replacement ref on the base: %v, %v; want %v", got, err, want)
	}
}

// TestCommitSubjectsRefusesAnythingButARef: since reaches a git command line,
// so anything git could read as an option or a revision expression is refused
// rather than passed through, as ChangedFiles refuses it.
func TestCommitSubjectsRefusesAnythingButARef(t *testing.T) {
	repo, _ := setup(t)
	for _, since := range []string{"", "-x", "--all", "HEAD~1", "HEAD^{commit}", "main..other", "a b"} {
		got, err := repo.CommitSubjects(ctx, since)
		if err == nil {
			t.Errorf("since %q was accepted and gave %v", since, got)
			continue
		}
		if !strings.Contains(err.Error(), "refusing to read the commit subjects") {
			t.Errorf("since %q: %v", since, err)
		}
	}
}
