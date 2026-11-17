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
	"slices"
	"sort"
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
//
// The admitted paths come back beside the refused ones: they are the other
// answer of the same question, and the status section reports them so a reader
// of the pull request knows an unreadable file is in it (D993, prflow.go's
// binariesPart).
func (r *run) strayArtifacts(ctx context.Context) (refused []strayArtifact, admitted []string, err error) {
	paths, err := r.repo.NewPaths(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		a, ok := r.strayArtifact(path)
		switch {
		case a != nil:
			refused = append(refused, *a)
		case ok:
			// The file is the run's own work, admitted by the project's list:
			// finalize commits it, and the section names it.
			admitted = append(admitted, path)
		}
	}
	return refused, admitted, nil
}

// strayArtifact reports the file at path as one finalize refuses, or nil when
// it is the run's own work; admitted is true when the project's rule is what
// let it through, which is what the status section reports. A path the checkout
// no longer holds is not one — nothing keeps the tree still between the listing
// and this read — and a file this cannot read is refused rather than committed
// unseen.
func (r *run) strayArtifact(path string) (refused *strayArtifact, admitted bool) {
	full := filepath.Join(r.repo.Dir, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		return &strayArtifact{path: path, why: "it could not be read"}, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A symlink commits the path it names, which is text whatever it
		// points at.
		return nil, false
	}
	head, err := readHead(full, binaryScanBytes)
	if err != nil {
		return &strayArtifact{path: path, why: "it could not be read"}, false
	}
	if bytes.IndexByte(head, 0) < 0 {
		return nil, false
	}
	why := r.binaryRefusal(path, info.Size())
	if why == "" {
		return nil, true
	}
	return &strayArtifact{path: path, why: why}, false
}

// committedBinaries is the same predicate over the other reading of the run:
// the paths the run's own diff adds that carry bytes no reviewer can read and
// that the project's policy does not admit (spec.md §4.2, D997).
//
// It is not a second rule. strayArtifacts reads what a commit finalize would
// make — the untracked files and the ones the agent staged — and a path the
// agent committed itself is in neither listing, because it is in a commit;
// this reads the run's own diff (`committedRange`...HEAD, the range the review
// stage is handed) and asks the same question of the paths it adds, less the
// paths a base the run works from already tracks (withoutBasePaths). A binary
// the run merely *edited* is not an addition and is left alone, the same
// tolerance §2.1 gives a file the base commit already holds.
//
// The admitted additions come back beside the refused ones, as they do from the
// untracked reading: the two readings are one answer to one question, and the
// status section reports both (D993).
func (r *run) committedBinaries(ctx context.Context) (refused []strayArtifact, admitted []string, err error) {
	if r.repo == nil {
		return nil, nil, nil
	}
	rng := r.committedRange(ctx) + "...HEAD"
	raw, err := rawChanges(ctx, r.repo, rng)
	if err != nil {
		return nil, nil, err
	}
	numstat, err := reviewChanges(ctx, r.repo, rng)
	if err != nil {
		return nil, nil, err
	}
	added, binary := addedBinaries(raw, numstat)
	// The bases the run works from are subtracted before anything is asked
	// about the rows that are left: a path one of them already holds a file at
	// is not a path the run's commits added, whatever the range calls it.
	added = r.withoutBasePaths(ctx, added)
	// Which added paths are binary is read from the bytes and not from git's
	// own answer: that answer is the checkout's to make, and one committed
	// .gitattributes line (`test_dup_case2 diff`) makes numstat count a
	// payload's lines and report it as text, which is the widening D986 exists
	// to stop. Every path git called text is therefore read again, and what is
	// read is the head of the blob the row adds — the bytes the branch carries,
	// which is the same window the untracked reading takes of the file in the
	// tree (blobCarriesBytes). A path git called binary is left as it is: that
	// answer refuses more rather than less, and a repository that marks its own
	// text file binary has refused its own file.
	//
	// A typechange is read whatever git answered, and the read is the add's:
	// numstat's counts for a `T` row cover the side the range *removes* as well
	// as the side it adds, so a base holding binary bytes answers "binary" for a
	// path the range replaced with a symlink — and what the branch carries there
	// is the path name that arrived, not the bytes on their way out.
	//
	// A gitlink is the row with nothing to read at all: its entry holds a commit
	// id where a blob would be, so it is text by the reading and refused by
	// nothing, whatever numstat says of the pair and whatever letter the row
	// carries. This is answered before the bytes are asked for, since there are
	// none: `git cat-file blob` on a commit id is not a read that succeeds.
	for _, c := range added {
		if c.newMode == modeGitlink {
			binary[c.path] = false
			continue
		}
		if b, named := binary[c.path]; !named || (b && c.status != statusTypeChanged) {
			// Unnamed is a row the numstat reading never paired, which the caller
			// refuses rather than trusting a path nothing read; named binary on a
			// row that is not a typechange is git's own answer, which refuses
			// more rather than less.
			continue
		}
		carries, err := blobCarriesBytes(ctx, r.repo, c.newBlob)
		if err != nil {
			return nil, nil, err
		}
		binary[c.path] = carries
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
			return nil, nil, err
		}
	}
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
			refused = append(refused, strayArtifact{path: c.path, why: "it could not be read"})
			continue
		}
		if why := r.binaryRefusal(c.path, size); why != "" {
			refused = append(refused, strayArtifact{path: c.path, why: why})
			continue
		}
		admitted = append(admitted, c.path)
	}
	return refused, admitted, nil
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
//
// A base that moved on while the run worked is inside this range even so, its
// commit having landed after the run was cut from it: the paths that base holds
// are subtracted from the rows the range reports (withoutBasePaths), which is
// what keeps the range's own start from deciding whether a file is the run's.
func (r *run) committedRange(ctx context.Context) string {
	if nb := r.narrowed(); nb != "" && r.onOrigin(ctx, nb) {
		return nb
	}
	return r.workBase()
}

// withoutBasePaths drops the added rows whose path a base the run works from
// already holds a file at, at its tip.
//
// The range starts at the commit the run was cut from, and a base can move on
// while the run works: main's own commit is then inside the range, every path
// it added is an `A` row, and a binary it added at a path binaries.allow does
// not name refused the run for a file it never added — a refusal for work that
// is not the run's, which the workflow guard has left out since it learned to
// subtract a base's own commits (refused.go, guardBounds). This is that
// subtraction read at the paths rather than at the commits: a path a base
// already holds a file at is a file the repository has, and the range calls its
// row an add only because the range starts before that base's own commit. §2.1
// leaves a tracked file's bytes alone whenever the same state is read over a
// range that starts after it — the row is an `M` then — so the verdict must not
// turn on where the range starts.
//
// Only an entry that *is* a file counts (baseHoldsBytes): a base's symlink or
// gitlink at a path holds no bytes there, and a run that puts a binary where
// one of those was is adding them — the typechange §4.2 refuses, which a base's
// own entry must not be read as admitting.
//
// Both bases the run works from are read (workBases), each as origin names the
// branch now (`ls-remote`) and as the commit its tip is, never the checkout's
// origin/<base>, which the agent's own git can repoint. A base that cannot be
// read — no such branch on origin, a tip this checkout does not hold —
// subtracts nothing rather than failing the reading, which leaves those rows
// in and so refuses more rather than less: the direction a bound that cannot be
// read has to fail in, the one readableBounds takes.
func (r *run) withoutBasePaths(ctx context.Context, added []rawChange) []rawChange {
	if len(added) == 0 {
		return added
	}
	paths := make([]string, 0, len(added))
	for _, c := range added {
		paths = append(paths, c.path)
	}
	held := make(map[string]bool, len(paths))
	for _, base := range r.workBases() {
		for path := range r.baseFiles(ctx, base, paths) {
			held[path] = true
		}
	}
	if len(held) == 0 {
		return added
	}
	own := make([]rawChange, 0, len(added))
	for _, c := range added {
		if !held[c.path] {
			own = append(own, c)
		}
	}
	return own
}

// baseFiles is the paths base holds a regular file at (baseHoldsBytes), read
// from the branch as origin names it now. A base this cannot read as a commit
// on origin, or whose tree cannot be read, holds nothing: the answer is empty
// or partial rather than an error, which subtracts less and so refuses more.
func (r *run) baseFiles(ctx context.Context, base string, paths []string) map[string]bool {
	files := make(map[string]bool)
	tip, err := r.repo.RemoteTip(ctx, base)
	if err != nil || !gitops.IsFullSHA(tip) {
		why := "origin has no branch by that name"
		if err != nil {
			why = r.redact(err.Error())
		}
		r.d.Log.Warn("a base branch could not be read on origin; a binary the base's own commits added is refused as the run's", "base", base, "why", why)
		return nil
	}
	for _, batch := range reviewBatches(paths, reviewPathsPerCall) {
		// The paths are the pull request's own and are passed as literals for
		// the reason treeSizes passes them so: a changed file named
		// ":(exclude)icon.png" is otherwise magic, and ls-tree fails outright
		// on it.
		out, err := sessionGit(ctx, r.repo, append([]string{"--literal-pathspecs", "ls-tree", "-z", "--end-of-options", tip, "--"}, batch...)...)
		if err != nil {
			r.d.Log.Warn("a base branch's tree could not be read; a binary the base's own commits added is refused as the run's", "base", base, "tip", tip, "err", r.redact(err.Error()))
			return files
		}
		for _, entry := range strings.Split(out, "\x00") {
			if entry == "" {
				continue
			}
			meta, path, ok := strings.Cut(entry, "\t")
			if !ok {
				r.d.Log.Warn("a base branch's tree read back an entry with no path; a binary the base's own commits added is refused as the run's", "base", base, "tip", tip)
				return files
			}
			if mode, _, _ := strings.Cut(meta, " "); baseHoldsBytes(mode) {
				files[path] = true
			}
		}
	}
	return files
}

// baseHoldsBytes reports whether a base's tree entry for a path is a regular
// file, which is the one entry that holds bytes at that path at all: a
// symlink's entry carries the path it points at, a gitlink's a commit id, and a
// directory's a tree.
func baseHoldsBytes(mode string) bool {
	return mode == "100644" || mode == "100755"
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

// blobCarriesBytes reports whether a blob's own bytes are ones a reviewer
// cannot read: a NUL in the first binaryScanBytes. That window is git's own
// test of a file and the rule's premise (spec.md §2.4), and it is the one the
// untracked reading takes of the file in the tree (readHead). One window, both
// readings, so a file is text or it is not, whichever way the run put it on the
// branch.
//
// The blob's head is read rather than the path's own diff, for two reasons.
// What the rule asks about is the bytes the branch carries, which are the
// object git itself tests; a diff is a rendering of them, whose headers and
// `+` prefixes put the file's own bytes at other offsets, and whose `T` rows
// carry the side the range removes beside the side it adds. And a diff per
// added text path, buffered whole, is a read whose cost is the file's — for a
// payload claimed as text, which is exactly what this re-read is for — where
// the blob's head is bounded by the window the answer is in.
func blobCarriesBytes(ctx context.Context, repo *gitops.Repo, blob string) (bool, error) {
	out, err := sessionGitHead(ctx, repo, binaryScanBytes, "cat-file", "blob", "--end-of-options", blob)
	if err != nil {
		return false, err
	}
	return bytes.IndexByte(out, 0) >= 0, nil
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

// admittedBinaries is the two readings' admitted paths as one list, sorted and
// with no path twice: it is what the status section reports (D993), and a list
// whose order came from whichever reading ran first would say the same work two
// ways. The two readings are disjoint by construction — one is the paths no
// commit holds, the other the paths a commit added — so the deduplication is
// for a tree that moved between them rather than for an ordinary run.
func admittedBinaries(untracked, committed []string) []string {
	out := slices.Concat(untracked, committed)
	sort.Strings(out)
	return slices.Compact(out)
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
//
// unread is the third thing a refusal can be about, and it is not a path: the
// run's own commits could not be read at all, so which binaries they added is
// unknown rather than none. Its clause is its own because there is no list of
// paths to give, and there is no rule sentence after it either — nothing was
// refused against binaries.allow, the reading itself is what failed.
//
// The closing sentence says where the fix is (D992, spec.md §5): the two
// readers who have one are a person — who adds a path in the owner's project
// config and publishes it, through either door — and a repository, which has
// none. Saying so is what stops an agent from "fixing" the refusal by writing
// the list into fugaro.yaml, a file this rule never reads and whose own decode
// refuses the key.
func strayRefusal(untracked, committed []strayArtifact, unread string) string {
	var parts []string
	if len(untracked) > 0 {
		parts = append(parts, "finalize refused to commit what the run left untracked, so the branch does not carry it: "+
			artifactsWithWhy(untracked))
	}
	if len(committed) > 0 {
		parts = append(parts, "finalize refused the run over what its own commits added, which the branch carries all the same, because the run does not rewrite the commits the agent made: "+
			artifactsWithWhy(committed))
	}
	if unread != "" {
		parts = append(parts, "finalize could not read the run's own diff, so whether its commits added a binary is unknown rather than answered, and the run is refused rather than passed: "+unread)
	}
	if len(untracked)+len(committed) == 0 {
		return strings.Join(parts, "; ")
	}
	return strings.Join(parts, "; ") +
		"; a binary is admitted by the project's binaries.allow, at the paths it names and within its cap, and what it does not admit is a build's artifact rather than the run's work. " +
		"The list is the project's own, and no file in the repository can widen it: a person adds a path to it in the project config and publishes it (`fugaro project policy publish`, or `fugaro update`, which publishes the same node), while `fugaro.yaml` carries no binaries key at all, so a list written there is read by nothing"
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
