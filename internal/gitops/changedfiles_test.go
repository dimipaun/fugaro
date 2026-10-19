package gitops

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestChangedFilesListsTheRunsOwnPaths: the set is the run's own commits
// (since...HEAD) and not the branch's history — a person's commit made
// before the run started is out of it, and in it when since is the base the
// branch started from. Sorted, path-only, and a rename reads as the delete
// and the add it is, because a path that moved is a path two runs can
// collide on.
func TestChangedFilesListsTheRunsOwnPaths(t *testing.T) {
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

	// The run's own work: two files and a rename of the person's.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"src/b.go": "package src\n", "src/a.go": "package src\n"})
	testutil.Git(t, repo.Dir, "mv", "earlier.txt", "renamed.txt")
	if _, err := repo.CommitAll(ctx, "the run's work"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ChangedFiles(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"earlier.txt", "renamed.txt", "src/a.go", "src/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("since the run's base: %v, want %v", got, want)
	}

	// The whole branch, base included: earlier.txt is gone from the branch
	// too (it was renamed away), so it is in no diff — which is exactly why
	// the run records the narrower range and not this one.
	all, err := repo.ChangedFiles(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"renamed.txt", "src/a.go", "src/b.go"}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("since origin/main: %v, want %v", all, want)
	}

	// A clean range: the base's own tip changed nothing.
	if got, err := repo.ChangedFiles(ctx, "HEAD"); err != nil || len(got) != 0 {
		t.Fatalf("since HEAD: %v, %v; want nothing", got, err)
	}
}

// TestChangedFilesRefusesAnythingButARef: since reaches a git command line,
// so anything git could read as an option or a revision expression is
// refused rather than passed through.
func TestChangedFilesRefusesAnythingButARef(t *testing.T) {
	repo, _ := setup(t)
	for _, since := range []string{"", "-x", "--all", "HEAD~1", "HEAD^{commit}", "main..other", "a b"} {
		got, err := repo.ChangedFiles(ctx, since)
		if err == nil {
			t.Errorf("since %q was accepted and gave %v", since, got)
			continue
		}
		if !strings.Contains(err.Error(), "refusing to read the changed paths") {
			t.Errorf("since %q: %v", since, err)
		}
	}
}
