package runner

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// changedFilesRun builds the least a run needs to record its changed paths:
// a real checkout cloned from a real remote, its config's base branch, and a
// record to write into.
func changedFilesRun(t *testing.T, base string) (*run, *gitops.Repo) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	r := &run{
		repo: repo,
		cfg:  &config.Config{Git: config.Git{BaseBranch: base}},
		rec:  &runstore.Record{},
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
	}
	return r, repo
}

// TestRecordChangedFilesWritesTheRunsOwnPaths: the writer reads the run's own
// commits (origin/<base>...HEAD) and bounds what it stores, so the record
// carries the paths and nothing else — no contents, no diff.
func TestRecordChangedFilesWritesTheRunsOwnPaths(t *testing.T) {
	r, repo := changedFilesRun(t, "main")
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"src/a.go": "package src\n", "docs/b.md": "b\n"})
	if _, err := repo.CommitAll(context.Background(), "the run's work"); err != nil {
		t.Fatal(err)
	}

	r.recordChangedFiles(context.Background())
	if r.rec.ChangedFiles == nil {
		t.Fatal("the record carries no changed-file set")
	}
	if want := []string{"docs/b.md", "src/a.go"}; !reflect.DeepEqual(r.rec.ChangedFiles.Paths, want) {
		t.Fatalf("paths = %v, want %v", r.rec.ChangedFiles.Paths, want)
	}
	if r.rec.ChangedFiles.Total != 2 || r.rec.ChangedFiles.Truncated {
		t.Fatalf("a two-path run: %+v", r.rec.ChangedFiles)
	}
}

// TestRecordChangedFilesBoundsAnOversizedChange: the bound is the writer's
// too, end to end — a run that changes more paths than a record holds gets a
// truncated set that says so, which is the fact D27's security note turns on.
func TestRecordChangedFilesBoundsAnOversizedChange(t *testing.T) {
	r, repo := changedFilesRun(t, "main")
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for i := range runstore.MaxChangedFiles + 50 {
		files[filepath.Join("src", "pkg", string(rune('a'+i%26)), fmt.Sprintf("f%05d.go", i))] = "package pkg\n"
	}
	testutil.WriteFiles(t, repo.Dir, files)
	if _, err := repo.CommitAll(context.Background(), "a very wide change"); err != nil {
		t.Fatal(err)
	}

	r.recordChangedFiles(context.Background())
	cf := r.rec.ChangedFiles
	if cf == nil {
		t.Fatal("the record carries no changed-file set")
	}
	if len(cf.Paths) != runstore.MaxChangedFiles || !cf.Truncated || cf.Total != len(files) {
		t.Fatalf("recorded %d of %d paths, truncated %v; want %d of %d, true",
			len(cf.Paths), cf.Total, cf.Truncated, runstore.MaxChangedFiles, len(files))
	}
}

// TestRecordChangedFilesLeavesNoSetWhenGitCannotAnswer: a git read that fails
// is not an empty set. The field stays absent, which a reader shows as "no
// record of what this run changed" rather than as "it changed nothing".
func TestRecordChangedFilesLeavesNoSetWhenGitCannotAnswer(t *testing.T) {
	// A base branch git cannot resolve: the diff fails.
	r, _ := changedFilesRun(t, "no-such-branch")
	r.recordChangedFiles(context.Background())
	if r.rec.ChangedFiles != nil {
		t.Fatalf("a failed read recorded %+v, want none", r.rec.ChangedFiles)
	}
}
