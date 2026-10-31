package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// binaryScanBytes is how much of a file is read to decide whether it is text:
// git's own test, a NUL byte in the first 8000 bytes, which is also how this
// repository reads a diff (reviewinput.go).
const binaryScanBytes = 8000

// strayArtifact is a file finalize refuses to commit, with the signal that
// refused it: what a reader of the run's record needs to delete the file,
// ignore it, or commit it deliberately themselves.
type strayArtifact struct {
	path string
	why  string
}

// strayArtifacts lists what the run left untracked that finalize will not
// commit: a file that is not text.
//
// The signal is the content and not the mode. The mode was the first rule
// tried, and it refuses too much: an executable uncommitted file is as likely
// to be a script the run wrote and forgot to commit — which is the work the
// leftover commit exists to save — as a build's product, and nothing in a
// permission bit tells the two apart. A compiler's output, on the other hand,
// always carries a NUL byte: an ELF's own header has one in its first 16 bytes
// whatever its mode, and so does every archive, image and font. What this
// leaves out is text that happens to be executable, which is a file a reviewer
// can read and judge.
//
// Only untracked files are read, and only the ones git itself would add
// (gitops.Untracked): a file the run's base commit already holds is the run's
// own — a binary it edited, an icon it replaced — and an ignored file can never
// reach a commit, whatever it holds.
func (r *run) strayArtifacts(ctx context.Context) ([]strayArtifact, error) {
	paths, err := r.repo.Untracked(ctx)
	if err != nil {
		return nil, err
	}
	var strays []strayArtifact
	for _, path := range paths {
		if a := r.strayArtifact(path); a != nil {
			strays = append(strays, *a)
		}
	}
	return strays, nil
}

// strayArtifact reports the file at path as one finalize refuses, or nil when
// it is the run's own work. A path the checkout no longer holds is not one —
// nothing keeps the tree still between the listing and this read — and a file
// this cannot read is refused rather than committed unseen.
func (r *run) strayArtifact(path string) *strayArtifact {
	full := filepath.Join(r.repo.Dir, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &strayArtifact{path: path, why: "it could not be read"}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A symlink commits the path it names, which is text whatever it
		// points at.
		return nil
	}
	head, err := readHead(full, binaryScanBytes)
	if err != nil {
		return &strayArtifact{path: path, why: "it could not be read"}
	}
	if bytes.IndexByte(head, 0) < 0 {
		return nil
	}
	return &strayArtifact{path: path, why: "it is not text"}
}

// readHead reads at most n bytes of the file at path.
func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(n)))
}

// strayRefusal is finalize's reason for the artifacts it refused: every path
// with the reason it was refused, so the file can be dealt with by name.
func strayRefusal(strays []strayArtifact) string {
	parts := make([]string, 0, len(strays))
	for _, a := range strays {
		parts = append(parts, a.path+" ("+a.why+")")
	}
	return "finalize refused to commit what the run left untracked, so the branch does not carry it: " +
		strings.Join(parts, ", ") + "; a file that is not text is a build's artifact, not the run's work"
}

// strayPaths is the paths of the artifacts finalize holds back from its own
// commit.
func strayPaths(strays []strayArtifact) []string {
	paths := make([]string, 0, len(strays))
	for _, a := range strays {
		paths = append(paths, a.path)
	}
	return paths
}
