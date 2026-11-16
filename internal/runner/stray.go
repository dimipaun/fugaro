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

	"github.com/dimipaun/fugaro/internal/gitops"
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

// committedBinaries is the same predicate over the other reading of the run:
// the paths the run's own diff adds that carry bytes no reviewer can read and
// that the project's policy does not admit (spec.md §4.2, D997).
//
// It is not a second rule. strayArtifacts reads what a commit finalize would
// make — the untracked files and the ones the agent staged — and a path the
// agent committed itself is in neither listing, because it is in a commit;
// this reads the run's own diff (`committedRange`...HEAD, the range the review
// stage is handed) and asks the same question of the paths it adds. A binary
// the run merely *edited* is not an addition and is left alone, the same
// tolerance §2.1 gives a file the base commit already holds.
func (r *run) committedBinaries(ctx context.Context) ([]strayArtifact, error) {
	if r.repo == nil {
		return nil, nil
	}
	rng := r.committedRange(ctx) + "...HEAD"
	raw, err := rawChanges(ctx, r.repo, rng)
	if err != nil {
		return nil, err
	}
	numstat, err := reviewChanges(ctx, r.repo, rng)
	if err != nil {
		return nil, err
	}
	added, binary := addedBinaries(raw, numstat)
	// Which added paths are binary is read from the bytes and not from git's
	// own answer: that answer is the checkout's to make, and one committed
	// .gitattributes line (`test_dup_case2 diff`) makes numstat count a
	// payload's lines and report it as text, which is the widening D986 exists
	// to stop. Every path git called text is therefore read again, the way the
	// review input re-checks the diff it carries (reviewinput.go's
	// reviewTextDiff). A path git called binary is left as it is: that answer
	// refuses more rather than less, and a repository that marks its own text
	// file binary has refused its own file.
	//
	// A typechange is read on its own new bytes instead, whatever git answered:
	// numstat's counts for a `T` row cover the side the range *removes* as well
	// as the side it adds, so a base holding binary bytes answers "binary" for a
	// path the range replaced with a symlink, and the path is refused for bytes
	// that are on their way out rather than for the path name that arrived. What
	// is refused is what the branch carries, so the new blob is read — and a
	// gitlink carries no bytes at all, whatever numstat says of the pair.
	for _, c := range added {
		switch {
		case c.status != statusTypeChanged:
			if b, named := binary[c.path]; named && !b {
				carries, err := diffCarriesBytes(ctx, r.repo, rng, c.path)
				if err != nil {
					return nil, err
				}
				binary[c.path] = carries
			}
		case c.newMode == modeGitlink:
			binary[c.path] = false
		default:
			carries, err := blobCarriesBytes(ctx, r.repo, c.newBlob)
			if err != nil {
				return nil, err
			}
			binary[c.path] = carries
		}
	}
	sized := make([]string, 0, len(added))
	for _, c := range added {
		if b, named := binary[c.path]; named && b {
			sized = append(sized, c.path)
		}
	}
	var sizes map[string]int64
	if len(sized) > 0 {
		// The size of the blob at HEAD, which is the one the branch carries:
		// `git ls-tree -l` reads the size without reading the bytes.
		if sizes, err = treeSizes(ctx, r.repo, "HEAD", sized); err != nil {
			return nil, err
		}
	}
	var strays []strayArtifact
	for _, c := range added {
		isBinary, named := binary[c.path]
		if named && !isBinary {
			continue
		}
		size, sized := sizes[c.path]
		if !named || !sized {
			// Nothing read this path's bytes: the numstat reading does not name
			// it, or HEAD's tree does not hold it although the raw reading says
			// it added it there. An unreadable file is refused rather than
			// passed, the same as a file in the working tree that cannot be read.
			strays = append(strays, strayArtifact{path: c.path, why: "it could not be read"})
			continue
		}
		if why := r.binaryRefusal(c.path, size); why != "" {
			strays = append(strays, strayArtifact{path: c.path, why: why})
		}
	}
	return strays, nil
}

// committedRange is the revision the committed reading's range starts from.
//
// It is a commit, never the ref reviewRev names. origin/<base> is a
// remote-tracking ref in the agent's own checkout, and one `git update-ref`
// repointing it at the run's own tip makes `<it>...HEAD` empty: a reading that
// measures from the ref answers about a run which added nothing, while the
// branch carries the payload. So the range starts where every other question of
// "did this run change X" starts — the commit the run was cut from (workBase,
// read at bootstrap before the agent ever ran; refused.go states the rule for
// the workflow guard, and checkpoint.go's C6a for the tips it pushes).
//
// A run whose review range was narrowed to its own commits
// (diffbase.go, narrowReviewRange) keeps that commit instead: the reading stays
// over the range the review stage was handed rather than widening to another
// branch's commits the run's work was built on. It is kept only while origin
// really holds it — onOrigin reads origin, never the checkout, which is the
// same check guardBounds makes before it subtracts that commit — so a commit
// only the agent's checkout calls "on origin" cannot take the run's own commits
// out of the range. A start bootstrap could not resolve (startErr) leaves the
// base branch as the range's start: workBase's own fallback, and the one case
// with no recorded commit to measure from.
func (r *run) committedRange(ctx context.Context) string {
	if nb := r.narrowed(); nb != "" && r.onOrigin(ctx, nb) {
		return nb
	}
	return r.workBase()
}

// addedBinaries is §4.2's pairing of the two readings of one range: the rows
// the raw reading adds, and which of them the numstat reading calls binary. The
// pairing is by path and never by index (F14): the two readings are separate
// git invocations whose orders nothing promises to agree, and a pairing by
// index would answer about the wrong file with nothing to show for it. The rows
// themselves are returned rather than their paths because a typechange is read
// on its new side, and which bytes that side is — a blob, a path name, or a
// commit id — is the row that carries it (the caller's second reading).
//
// A path whose new blob is a blob a delete row in the same range carries is a
// move of a tracked file, not an addition: --no-renames reports a rename as a
// delete and an add, the branch's work is unchanged, and §2.1 leaves a tracked
// binary alone. Both ids come from the raw reading, so the rule costs no third
// read and no rename detection. A move that also edits the bytes has two
// different ids and is an addition of bytes nothing admits, which is the honest
// reading of it.
//
// A typechange is an addition: a path the base tracks as a symlink or a gitlink
// and the range replaces with a regular file holds bytes the base never had, at
// the one path a reviewer would look for them, and git calls that `T` rather
// than `A` — so reading adds alone carries a payload past the rule behind a
// status letter. It is not the modification §2.1 tolerates, which is a tracked
// file's own bytes replaced.
//
// A path the returned map does not name is one the numstat reading never
// paired: the caller refuses it rather than trusting a file nothing read.
func addedBinaries(raw []rawChange, numstat []reviewChange) ([]rawChange, map[string]bool) {
	moved := make(map[string]bool, len(raw))
	for _, c := range raw {
		if c.status == statusDeleted {
			moved[c.oldBlob] = true
		}
	}
	how := make(map[string]bool, len(numstat))
	for _, c := range numstat {
		how[c.path] = c.binary
	}
	var added []rawChange
	binary := make(map[string]bool)
	for _, c := range raw {
		if !addsBytes(c.status) || moved[c.newBlob] {
			continue
		}
		added = append(added, c)
		if b, named := how[c.path]; named {
			binary[c.path] = b
		}
	}
	return added, binary
}

// addsBytes reports whether a range's own status letter is the range putting
// bytes at a path that the base did not hold there: an add, or the typechange
// the reading would otherwise carry a payload past (see addedBinaries). Every
// other letter — `M` above all, and `D` — is the base's own file being edited
// or removed, which §2.1 leaves alone.
func addsBytes(status byte) bool {
	return status == statusAdded || status == statusTypeChanged
}

// diffCarriesBytes reports whether the range's own diff of path carries a byte
// git would not print as text: a NUL, the test the untracked reading makes of
// the file in the tree (readHead) and the review input makes of the diff it
// carries (reviewinput.go's reviewTextDiff).
//
// The path's own diff is what is read, and not the blob: the answer the rule
// needs is about the bytes the range added, the same reading the rest of this
// file works from. --no-textconv and --no-ext-diff so no filter the checkout
// configures can answer in the file's place, and the path is passed as a
// literal so a name that is pathspec magic is a name.
func diffCarriesBytes(ctx context.Context, repo *gitops.Repo, rng, path string) (bool, error) {
	out, err := sessionGit(ctx, repo, "--literal-pathspecs", "diff",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-renames",
		"--end-of-options", rng, "--", path)
	if err != nil {
		return false, err
	}
	return strings.ContainsRune(out, 0), nil
}

// blobCarriesBytes reports whether a blob's own bytes are ones a reviewer
// cannot read: a NUL in the first binaryScanBytes, which is git's own test and
// the window both other readings use (readHead of the file in the tree, and
// reviewinput.go's reviewTextDiff of the diff it carries).
//
// The blob is read rather than its path's diff, because the reading is about one
// side of a row: a typechange's diff carries the bytes the range removes beside
// the ones it adds, and the answer wanted here is about what arrives.
func blobCarriesBytes(ctx context.Context, repo *gitops.Repo, blob string) (bool, error) {
	out, err := sessionGit(ctx, repo, "cat-file", "blob", "--end-of-options", blob)
	if err != nil {
		return false, err
	}
	if len(out) > binaryScanBytes {
		out = out[:binaryScanBytes]
	}
	return strings.ContainsRune(out, 0), nil
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

// strayRefusal is finalize's reason for the binaries it refused: every path
// with the reason it was refused, so the file can be dealt with by name, and
// the rule it met — the project's binaries.allow, which admits the paths a
// binary may be added at, within its cap.
//
// Both readings of the rule are named in the one sentence, because a run can be
// refused by both at once and `fail` keeps the reason it is handed first: a
// sentence per reading, written where each is read, would drop one of the two
// lists from the record with nothing saying so. The two clauses are not the
// same news — what the run left untracked is held out of its own leftover
// commit, so the branch does not carry it, while what the run's own commits
// added is already on the branch, because the run does not rewrite the agent's
// history (D989) and a person has to delete the file.
func strayRefusal(untracked, committed []strayArtifact) string {
	var parts []string
	if len(untracked) > 0 {
		parts = append(parts, "finalize refused to commit what the run left untracked, so the branch does not carry it: "+
			artifactsWithWhy(untracked))
	}
	if len(committed) > 0 {
		parts = append(parts, "finalize refused the run over what its own commits added, which the branch carries all the same, because the run does not rewrite the commits the agent made: "+
			artifactsWithWhy(committed))
	}
	return strings.Join(parts, "; ") +
		"; a binary is admitted by the project's binaries.allow, at the paths it names and within its cap, and what it does not admit is a build's artifact rather than the run's work"
}

// artifactsWithWhy is refused paths with the reason each was refused.
func artifactsWithWhy(strays []strayArtifact) string {
	parts := make([]string, 0, len(strays))
	for _, a := range strays {
		parts = append(parts, a.path+" ("+a.why+")")
	}
	return strings.Join(parts, ", ")
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
