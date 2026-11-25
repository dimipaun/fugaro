package runner

// The guard on the removal of the run's own file-secret directory (G3).
// removeSecretFiles is called from Run's deferred handler with the directory
// secretFileRoot made, so no case driven through a run can reach its refusal:
// a run cannot be made to hold a directory it did not make. These two call it
// on the two shapes it decides between.

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveSecretFilesRefusesADirectoryThatIsNotTheRunsOwn: the removal is a
// recursive one, and the path is the run's own only when it is a directory
// directly under the temporary directory -- the shape secretFileRoot makes.
// Anything else is refused, whole tree and all: a path this run did not make is
// not one it may take out (the same terms as removeStampHooks).
//
// Hand mutation this fails on: deleting the guard (the directory and the file
// under it are removed, and nothing is said).
func TestRemoveSecretFilesRefusesADirectoryThatIsNotTheRunsOwn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "sub", "not-ours")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "kept")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	r := &run{d: Deps{Log: slog.New(slog.NewTextHandler(&logs, nil))}, secretFiles: dir}

	r.removeSecretFiles()

	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the file %s went with a directory the run did not make: %v", file, err)
	}
	if !strings.Contains(logs.String(), "refusing to remove a directory that is not the run's own") {
		t.Errorf("the run did not say why it left the directory:\n%s", logs.String())
	}
}

// TestRemoveSecretFilesTakesTheRunsOwnDirectory: the guard refuses what is not
// the run's own and nothing besides -- what secretFileRoot makes is a directory
// directly under the temporary directory, and it goes with the value written in
// it at run end (G3).
//
// Hand mutation this fails on: a guard that refuses every path (the directory
// and the credential in it outlive the run).
func TestRemoveSecretFilesTakesTheRunsOwnDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	r := &run{d: Deps{Log: slog.New(slog.DiscardHandler)}}
	root, err := r.secretFileRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "DEPLOY_CERT"), []byte("value"), secretFileMode); err != nil {
		t.Fatal(err)
	}

	r.removeSecretFiles()

	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the run's own directory %s is still there: %v", root, err)
	}
}
