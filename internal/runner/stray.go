package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// binaryScanBytes is how much of a file is read to decide whether it is text:
// git's own test, a NUL byte in the first 8000 bytes. The window is what makes
// the test cheap — one read, never the whole file — and 8000 is where a
// compiler's output declares itself: a binary format's magic or a header's
// first padding puts a NUL well inside it.
const binaryScanBytes = 8000

// strayArtifact is a file finalize refuses to commit, with the signal that
// refused it: what a reader of the run's record needs to delete the file,
// ignore it, or commit it deliberately themselves.
type strayArtifact struct {
	path string
	why  string
}

// strayArtifacts lists what the run left untracked that finalize will not
// commit: a file that is not text, unless the project's own policy admits it
// at that path and size.
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
// The rule's second input is the project's own grant (binaryRefusal): a task
// whose work *is* an asset writes bytes nobody can read, and the owner says in
// which paths a binary may be added and how large one may be. Which paths are
// admitted is not read from the checkout — no repository file may carry the
// rule (D986) — so a run cannot widen it.
//
// Only the files a commit would carry that no commit has are read
// (gitops.NewPaths): the untracked ones and the ones the agent staged itself,
// because a file `git add`-ed and never committed is a build's artifact just
// the same and `git add -A` puts it in the commit. A file the run's base commit
// already holds is the run's own — a binary it edited, an icon it replaced —
// and an ignored file can never reach a commit, whatever it holds.
func (r *run) strayArtifacts(ctx context.Context) ([]strayArtifact, error) {
	paths, err := r.repo.NewPaths(ctx)
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
	why := r.binaryRefusal(path, info.Size())
	if why == "" {
		return nil
	}
	return &strayArtifact{path: path, why: why}
}

// binaryRefusal is why the project's binary rule refuses a file of size bytes
// at path, or "" when it admits it (D988, spec.md §4.1).
//
// The rule is two answers and they are asked in this order: a path no entry of
// binaries.allow matches is refused whatever its size, and a path an entry
// matches is refused only above the project's cap. A text file is never asked
// about — the caller reads the bytes first — so an admitted path holds a text
// file of any size, which is the reviewer's to read like any other diff line.
//
// The reason for an over-sized file carries the size and the cap, because
// nothing else in the record can tell the person reading it which of the two
// the rule refused: the fix is a different one for the file that is too big
// and the file that is in the wrong place.
func (r *run) binaryRefusal(path string, size int64) string {
	if !matchesBinaryAllow(r.policy.BinariesAllow, path) {
		return "it is not text"
	}
	if cap := r.binaryCap(); size > cap {
		return fmt.Sprintf("it is %d bytes, over the project's %d-byte cap", size, cap)
	}
	return ""
}

// matchesBinaryAllow reports whether any entry of the project's list admits
// path.
//
// The entries are doublestar globs, matched as globs and never compared as
// prefixes: `website/public/*.png` admits neither the directories below that
// one nor another extension inside it. A pattern the matcher cannot read
// admits nothing — the node refuses such an entry where it is written, and one
// that arrived anyway is a lock that is not there, which is the fail-closed
// reading of it (spec.md §2.1).
func matchesBinaryAllow(allow []string, path string) bool {
	for _, pattern := range allow {
		if ok, err := doublestar.Match(pattern, path); err == nil && ok {
			return true
		}
	}
	return false
}

// binaryCap is the size the run holds an admitted binary to: the cap the run's
// policy resolved, or the shipped 2 MiB when no layer wrote one (D983, spec.md
// §2.2).
//
// A zero here is the absent member and never a cap of zero: the node's own
// block may name no max_bytes, and a written 0 or null is refused where that
// block is decoded rather than read as a wider answer than the default.
func (r *run) binaryCap() int64 {
	if r.policy.BinariesMaxBytes > 0 {
		return r.policy.BinariesMaxBytes
	}
	return projectpolicy.DefaultBinariesMaxBytes
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
// with the reason it was refused, so the file can be dealt with by name, and
// the rule it met — the project's binaries.allow, which admits the paths a
// binary may be added at, within its cap.
func strayRefusal(strays []strayArtifact) string {
	parts := make([]string, 0, len(strays))
	for _, a := range strays {
		parts = append(parts, a.path+" ("+a.why+")")
	}
	return "finalize refused to commit what the run left untracked, so the branch does not carry it: " +
		strings.Join(parts, ", ") + "; a binary is admitted by the project's binaries.allow, at the paths it names and within its cap, and what it does not admit is a build's artifact rather than the run's work"
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
