package runner_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// binariesNode is the project's published policy carrying the binaries rule the
// test names: allow is the JSON array of patterns and maxBytes the JSON cap.
// The block is the owner's own grant and the policy node is the only layer that
// carries one (D986) — nothing in the checkout can widen it, which is why
// these tests seed it on the database rather than in the fixture's fugaro.yaml.
func binariesNode(allow, maxBytes string) string {
	return fmt.Sprintf(`{"version":%d,"binaries":{"allow":[%s],"max_bytes":%s}}`, projectpolicy.Version, allow, maxBytes)
}

// imageFile is the run's own work when its task is to add an asset: bytes no
// reviewer can read, at the path the test names.
func imageFile(size int) leftOver {
	return func(t *testing.T, req agent.Request, name string) {
		t.Helper()
		writeImage(t, req, name, size)
	}
}

// writeImage writes an image file, creating the directories the path needs: a
// run that adds an asset writes it where the project admitted it, which is
// under a directory the checkout does not carry yet.
func writeImage(t *testing.T, req agent.Request, name string, size int) {
	t.Helper()
	full := filepath.Join(req.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, imageBytes(size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// imageBytes is the content of an image file of size bytes: an encoder's own
// header, whose NULs sit well inside the window the rule reads, and padding.
// The same bytes serve the two fixtures an image needs — one written into the
// run's tree, one a base commit already carries.
func imageBytes(size int) []byte {
	b := make([]byte, size)
	copy(b, "\x89PNG\r\n\x1a\n\x00\x00")
	return b
}

// TestAnAdmittedImageIsCommitted: the run whose task is to add an asset. The
// bytes are as unreadable as any build product's — that never changed — and
// the project's own policy admits that path and that size, so finalize
// commits the file with the rest of the run's work instead of refusing it
// (D988, spec.md §4.1).
//
// The ending is the one ordinary leftover work gets: finalize's own commit
// voids the verified test and the run ends the draft that leaves (a property
// of the leftover commit, not of this rule), which is what the second harness
// below runs the same steps for. What this test is about is that the rule did
// not fail the run over the file: the branch carries the image, and the record
// ends exactly as a run that left a text file does.
func TestAnAdmittedImageIsCommitted(t *testing.T) {
	const path = "website/public/images/logo.webp"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementLeaving(path, imageFile(512)), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Reason, "refused") {
		t.Fatalf("reason = %q; want an admitted image committed, not refused", rec.Reason)
	}
	files := branchFiles(t, b.harness, rec.Branch)
	if !slices.Contains(files, path) {
		t.Errorf("the branch does not carry the admitted image: %v", files)
	}
	if !slices.Contains(files, "feature.txt") {
		t.Errorf("the branch does not carry the run's own work: %v", files)
	}

	text := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	ordinary, err := text.run(t, implementLeaving("notes.txt", plainText), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != ordinary.Status || rec.Outcome != ordinary.Outcome || rec.Reason != ordinary.Reason {
		t.Errorf("an admitted image ends %q/%q (%q); want the ending a text leftover gets: %q/%q (%q)",
			rec.Status, rec.Outcome, rec.Reason, ordinary.Status, ordinary.Outcome, ordinary.Reason)
	}
}

// TestAnOverSizedAdmittedImageIsRefused: the path is admitted and the size is
// not. There is one cap for the whole list (D983, spec.md §2.2), and the
// refusal names the file with its size and the cap, so a person can tell which
// of the two the rule refused — and which cap, since a block may name none and
// take the shipped 2 MiB.
func TestAnOverSizedAdmittedImageIsRefused(t *testing.T) {
	shipped := int(projectpolicy.DefaultBinariesMaxBytes)
	const path = "website/public/images/hero.webp"
	for _, tt := range []struct {
		name string
		node string
		size int
		cap  string
	}{
		{"the cap the block names", binariesNode(`"website/public/**"`, "1024"), 4096, "1024"},
		{
			"the shipped cap a block names none for",
			fmt.Sprintf(`{"version":%d,"binaries":{"allow":["website/public/**"]}}`, projectpolicy.Version),
			shipped + 1,
			strconv.Itoa(shipped),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := policyRun(t, gwConfig(t, ""), "off", "", tt.node)
			rec, err := b.run(t, implementLeaving(path, imageFile(tt.size)), review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
				t.Fatalf("record = %+v; want a draft: the file is over the project's cap", rec)
			}
			for _, want := range []string{path, strconv.Itoa(tt.size), tt.cap} {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
				}
			}
			for _, f := range branchFiles(t, b.harness, rec.Branch) {
				if f == path {
					t.Errorf("the branch carries the file finalize refused: %v", branchFiles(t, b.harness, rec.Branch))
				}
			}
		})
	}
}

// TestAnUnadmittedBinaryIsStillRefused: #1476's behaviour, unchanged, and the
// test that keeps this change from becoming a loosening. A project that
// published no binaries block admits nothing, so the artifact a run left is
// refused exactly as it was before the rule existed.
func TestAnUnadmittedBinaryIsStillRefused(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", fmt.Sprintf(`{"version":%d}`, projectpolicy.Version))
	rec, err := b.run(t, implementLeaving(path, compiledBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v; want a draft: nothing admits a binary here", rec)
	}
	if !strings.Contains(rec.Reason, path) || !strings.Contains(rec.Reason, "it is not text") {
		t.Errorf("reason = %q; want it to name %q as not text", rec.Reason, path)
	}
	for _, f := range branchFiles(t, b.harness, rec.Branch) {
		if f == path {
			t.Errorf("the branch carries the artifact finalize refused: %v", branchFiles(t, b.harness, rec.Branch))
		}
	}
}

// The run's own diff (AR4, D997): the second reading of the same rule, over
// `<reviewRev>...HEAD`. What #1476 added reads the working tree — the untracked
// files and the ones the agent staged — and a path the agent committed is in
// neither listing, so a binary it committed reached the branch with the run
// ending ready. Every test below is about that range: what it refuses, what it
// deliberately leaves alone, and what its two readings are paired by.

// TestASelfCommittedBinaryFailsTheRun: the residual the untracked check cannot
// see, and the reason AR4 exists (D997, spec.md §4.2). #1476's reading lists
// what a commit would carry and no commit has; a payload the agent committed
// itself is in a commit, so it passes that reading untouched and this run used
// to end succeeded/ready with an unreadable megabyte in its pull request. The
// run's own diff is the reader that sees it.
//
// The commit stays on the branch: the run refuses the run, never the history
// (D989), which is why the ending is a draft a person can read and delete from
// rather than a push that never happened.
func TestASelfCommittedBinaryFailsTheRun(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, selfCommitBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: the run's own commits added a binary nothing admits", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, "it is not text"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
	if !slices.Contains(branchFiles(t, b.harness, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, b.harness, rec.Branch))
	}
}

// outsideThePattern is two files a prefix reading of an admitted directory
// would admit and its glob does not: a deeper path under it, and another
// extension directly in it.
func outsideThePattern(t *testing.T, req agent.Request, _ string) {
	t.Helper()
	writeImage(t, req, "website/public/deep/logo.png", 512)
	writeImage(t, req, "website/public/logo.webp", 512)
}

// TestAnAdmittedPathOutsideItsPatternIsRefused: the entry is matched as a
// doublestar glob and never compared as a prefix (spec.md §2.1). The project
// admitted `website/public/*.png` — one level, that extension — and a path
// beside or below it is a path the project did not admit.
func TestAnAdmittedPathOutsideItsPatternIsRefused(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/*.png"`, "1048576"))
	rec, err := b.run(t, implementLeaving("website/public/deep/logo.png", outsideThePattern), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v; want a draft: neither file is inside the admitted pattern", rec)
	}
	for _, path := range []string{"website/public/deep/logo.png", "website/public/logo.webp"} {
		if !strings.Contains(rec.Reason, path) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, path)
		}
	}
	for _, f := range branchFiles(t, b.harness, rec.Branch) {
		if f == "website/public/deep/logo.png" || f == "website/public/logo.webp" {
			t.Errorf("the branch carries a file finalize refused: %v", branchFiles(t, b.harness, rec.Branch))
		}
	}
}

// TestAGitAddIntentToAddBinaryFailsTheRun: F4's narrower bypass, closed. `git
// add -N` puts the path in the index without its content, which hides it from
// both of #1476's listings, and the commit that follows carries the bytes
// anyway — so the file is in a commit, and the run's own diff is what reads it.
// The path is the same one the untracked tests use, at an admitted path's
// neighbour: nothing the project published admits it.
func TestAGitAddIntentToAddBinaryFailsTheRun(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, commitIntentToAddBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: the commit carries the bytes `git add -N` hid", rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, path) {
		t.Errorf("reason = %q; want it to name %q", rec.Reason, path)
	}
	if !slices.Contains(branchFiles(t, b.harness, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, b.harness, rec.Branch))
	}
}

// TestAPayloadClaimedAsTextIsStillRefused: git's answer about a path is the
// checkout's to make, and one committed .gitattributes line is enough to change
// it — with `test_dup_case2 diff` in it, `git diff --numstat` counts the
// payload's lines and calls it text, so a reading that trusts that answer sees
// the run's ordinary work. The bytes are what the rule reads (the review input
// re-checks the same way, in reviewinput.go's reviewTextDiff), and what it
// finds is an unadmitted binary like any other.
func TestAPayloadClaimedAsTextIsStillRefused(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, selfCommitBinaryClaimedText), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: a .gitattributes line changes git's answer, never the bytes", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, "it is not text"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
	if !slices.Contains(branchFiles(t, b.harness, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, b.harness, rec.Branch))
	}
}

// TestAReplaceRefCannotHideThePayload: refs/replace is a ref the agent can
// plant in its own checkout, and a reader that follows it reads a commit the
// run never made — here one carrying the base's tree, so the range diffs as a
// run that added nothing while the commit the branch really carries holds the
// payload. Every read of the run's own range is replacement-blind for that
// reason, so the payload is found and the run refused over the object HEAD
// really is.
func TestAReplaceRefCannotHideThePayload(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, selfCommitBinaryReplaced), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: a replacement ref must not hide the run's own commit", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, "it is not text"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
}

// TestARepointedBaseCannotHideThePayload: the base the committed reading
// measures its range against is a ref the agent's own git can move. One
// `git update-ref refs/remotes/origin/main HEAD` after the payload's commit
// makes `origin/main...HEAD` empty — a run that added nothing, by the reading's
// own range — while the branch carries the payload and ends ready. So the
// range starts at the commit the run was cut from, which bootstrap read before
// the agent ever ran (workBase's rule, refused.go).
func TestARepointedBaseCannotHideThePayload(t *testing.T) {
	const path = "test_dup_case2"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, selfCommitBinaryRepointed), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: repointing the base ref must not empty the run's own range", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, "it is not text"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
	if !slices.Contains(branchFiles(t, b.harness, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, b.harness, rec.Branch))
	}
}

// TestABinaryCommittedOverATrackedSymlinkIsRefused: a path whose *type* the
// range changes is an addition of bytes, not a modification of them. The base
// tracks a symlink, the agent replaces it with a payload, and git reports that
// as `T` — the same new bytes at the same path as an `A` row, behind a letter
// a reader that takes only adds carries past the rule entirely.
func TestABinaryCommittedOverATrackedSymlinkIsRefused(t *testing.T) {
	const path = "assets/logo.webp"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, nil)
	symlinkInBase(t, h, path, "logo-target.bin")
	rec, err := h.run(t, implementCommitting(path, selfCommitOverSymlink), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: a payload committed over a tracked symlink is still a payload", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, "it is not text"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestABinaryReplacedByASymlinkIsNotRefused: the other side of the typechange,
// and the reason it is read on the new bytes rather than refused for its
// letter. The base's own binary is what leaves; what the path holds afterwards
// is a path name, which a reviewer reads like any other line, so a run that
// replaces an icon with a link is the repository's own file being changed — and
// refusing it would refuse a run for a typechange that added nothing.
func TestABinaryReplacedByASymlinkIsNotRefused(t *testing.T) {
	const path = "assets/logo.webp"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, map[string]string{path: string(imageBytes(1024))})
	rec, err := h.run(t, implementCommitting(path, selfCommitSymlinkOverBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: the typechange added a path name, not bytes", rec.Status, rec.Outcome, rec.Reason)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestAGitlinkCommittedOverABinaryIsNotRefused: the typechange whose new side
// carries no bytes at all. The base's binary leaves and a submodule pointer
// arrives, and a pointer is a commit id rather than a payload: there is nothing
// at that path for a reviewer to be given and nothing for the rule to refuse.
// Refusing it would fail a run for a pointer — and the reading has to say so, not
// read a blob that is not there, or size an entry `git ls-tree -l` gives no
// number for.
func TestAGitlinkCommittedOverABinaryIsNotRefused(t *testing.T) {
	const path = "assets/logo.webp"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, map[string]string{path: string(imageBytes(1024))})
	rec, err := h.run(t, implementCommitting(path, selfCommitGitlinkOverBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: a gitlink holds a commit id, not bytes", rec.Status, rec.Outcome, rec.Reason)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the agent made: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestAModifiedTrackedBinaryIsNotRefused: §2.1's deliberate tolerance, read
// from the committed side. `M` on a tracked binary is the repository's own file
// being replaced, and what reaches the branch is bytes the pull request's
// reviewer already had; refusing it would refuse a run for editing an icon. The
// case is at a path no list admits, so the tolerance is the only reason it
// passes.
func TestAModifiedTrackedBinaryIsNotRefused(t *testing.T) {
	const path = "assets/logo.webp"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, map[string]string{path: string(imageBytes(1024))})
	rec, err := h.run(t, implementReplacingBinary(path, 4096), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: a modified tracked binary is not an addition", rec.Status, rec.Outcome, rec.Reason)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry the file the run replaced: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestTheRefusalNamesThePathAndTheSize: the refusal has to be actionable by
// somebody who was not there. The path says which file, and the size says which
// of the rule's two answers refused it — the path is admitted and the file is
// over the cap — with the cap beside it, since a project that names no cap gets
// the shipped one.
//
// The size is the blob's at HEAD (`git ls-tree -l`), not the working tree's:
// the file the refusal is about is the one the branch carries.
func TestTheRefusalNamesThePathAndTheSize(t *testing.T) {
	const path = "website/public/images/hero.webp"
	const size, cap = 4096, 1024
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, strconv.Itoa(cap)))
	rec, err := b.run(t, implementCommitting(path, selfCommitImage(size)), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: the file is over the project's cap", rec.Status, rec.Outcome, rec.Reason)
	}
	for _, want := range []string{path, strconv.Itoa(size), strconv.Itoa(cap)} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q; want it to name %q", rec.Reason, want)
		}
	}
}

// TestAMovedBinaryIsNotRefused: a move is not an addition, and it is the false
// refusal D997's cost column is about. --no-renames reports the move of a
// tracked file as a delete and an add, and the two rows' blob ids are the same
// one, so the add is the same bytes at another path rather than new bytes
// nothing admits — which is what §2.1 leaves alone either way.
//
// Nothing here is admitted (there is no policy node at all), so the blob ids
// are the only reason the run is not refused.
func TestAMovedBinaryIsNotRefused(t *testing.T) {
	const from, to = "assets/logo.webp", "assets/images/logo.webp"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, map[string]string{from: string(imageBytes(1024))})
	rec, err := h.run(t, implementMovingBinary(from, to), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: the bytes are the tracked file's own, moved", rec.Status, rec.Outcome, rec.Reason)
	}
	files := branchFiles(t, h, rec.Branch)
	if !slices.Contains(files, to) || slices.Contains(files, from) {
		t.Errorf("the branch's files = %v; want the moved path and not the old one", files)
	}
}

// TestAnAddedThenDeletedBinaryIsNotRefused is the hole §4.2 names and keeps:
// the reading takes the two ends of the range and the `A` statuses among them,
// and a path the agent added and later deleted is at neither end — so no row
// names it and the run is not refused, while the bytes stay reachable in the
// branch's history. That is a deliberate limit and not an oversight: seeing it
// means reading every commit of the range, and a reviewer already sees the add
// in the pull request's own commit list.
//
// This test pins the answer rather than the hole, which is what the plan asks
// for: a later task that decides to close it changes this test on purpose
// instead of discovering it by accident. The history assertion below is the
// half that says what the residual costs.
func TestAnAddedThenDeletedBinaryIsNotRefused(t *testing.T) {
	const path = "test_dup_case2"
	h := newHarnessFiles(t, gwConfig(t, ""), nil, nil)
	rec, err := h.run(t, implementAddingThenDeletingBinary(path), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: the path is at neither end of the range", rec.Status, rec.Outcome, rec.Reason)
	}
	files := branchFiles(t, h, rec.Branch)
	if slices.Contains(files, path) {
		t.Errorf("the branch's tip carries the deleted path: %v", files)
	}
	if adds := testutil.Git(t, h.remote, "log", "--format=%s", "--diff-filter=A", rec.Branch); !strings.Contains(adds, "Add a vendored tool") {
		t.Errorf("the branch's history does not carry the commit that added %s (%q), so this case no longer pins the residual it is about", path, adds)
	}
}

// reportRunWithAPayload is a report run's work directory with a payload in it
// that survives the checkout: the job's image left it in the directory the run
// opens, an excludes file hides it from `git clean -fd` (which removes
// untracked files and keeps ignored ones), and the clean removes the excludes
// file itself — so the digest step's own `git add -A` stages the payload into
// its commit, and nothing the run's stages do put it there.
func reportRunWithAPayload(t *testing.T, h *harness, path string) {
	t.Helper()
	testutil.Git(t, filepath.Dir(h.deps.WorkDir), "clone", "--quiet", h.remote, h.deps.WorkDir)
	writeImage(t, agent.Request{Dir: h.deps.WorkDir}, path, 4096)
	excl := filepath.Join(h.deps.WorkDir, "excl")
	if err := os.WriteFile(excl, []byte(path+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, h.deps.WorkDir, "config", "core.excludesFile", excl)
}

// TestTheDigestStepCommitIsChecked: the third of the five ways a file reaches a
// branch (gate.go's autofix commit and digest.go's report step among them, F3).
// A report run's branch is the commit the digest step made, so a payload that
// commit carries is on the branch with no stage having written it and with the
// agent never having run: the run's own diff is the only reading that sees it,
// and the step's commit is the one the run is refused over.
//
// The payload is planted in the run's own work directory before it starts, the
// way a job whose image baked a build product leaves one.
func TestTheDigestStepCommitIsChecked(t *testing.T) {
	const own, path = "20261101-120000-d1a6", "test_dup_case2"
	spec := digestSpec(own)
	h := newHarnessFiles(t, gwConfig(t, ""), spec, nil)
	digestGCSBucket(t, h, spec)
	clk := digestClock(h)
	digestRecord(t, h.bucket, repoSlug, own, clk.Now().AddDate(0, 0, -1), 1.5, nil)
	reportRunWithAPayload(t, h, path)

	rec, err := h.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: the digest step's own commit carries a binary", rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, path) {
		t.Errorf("reason = %q; want it to name %q", rec.Reason, path)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry the commit the digest step made: %v", branchFiles(t, h, rec.Branch))
	}
	if shown := testutil.Git(t, h.remote, "show", "--name-only", "--format=", digestStepCommitAt(t, h, rec.Branch)); !strings.Contains(shown, path) {
		t.Errorf("the digest step's own commit does not carry %s (%q), so this case no longer pins the commit it is about", path, shown)
	}
}

// TestTheAutofixCommitIsChecked: the fourth, and the one that needs no agent
// commit at all. A check step with autofix runs commands.fix and commits what
// it finds (gate.go), implement is skipped because the gate fixed the tree, and
// a fix command that writes a build product leaves it in a commit the run's own
// diff is the only reader of.
func TestTheAutofixCommitIsChecked(t *testing.T) {
	const path = "fixed.bin"
	cfg := gateCfg(t, "lint-fix", 1, "true", "test -f "+path, `printf 'the fix wrote\000\000' > `+path)
	h := newHarnessFiles(t, cfg, nil, gateFiles("lint-fix", lintFixRecipe))
	rec, err := h.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %s, outcome %s, reason %q; want a draft: the autofix commit carries a binary", rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, path) {
		t.Errorf("reason = %q; want it to name %q", rec.Reason, path)
	}
	if lastCommitMsg(t, h.remote, rec.Branch) != "fugaro: autofix (commands.fix)" {
		t.Errorf("the branch's tip is %q, not the autofix commit, so this case no longer pins the path it is about",
			lastCommitMsg(t, h.remote, rec.Branch))
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), path) {
		t.Errorf("the branch does not carry what the autofix committed: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestTheChallengerArmIsChecked: the fifth committer is a challenger arm's own
// leftover commit (shadow.go), and the arm reads the rule exactly as its
// champion does. Nothing of the arm's reaches the developer, so the bytes are
// not the risk; what is, is a pair whose two records disagree about what the
// run did — one arm refused and the other reading clean is two scorers reading
// two runs of the same work (D559). Both arms are run here, and what this pins
// is that they name the same refusal.
func TestTheChallengerArmIsChecked(t *testing.T) {
	const path = "test_dup_case2"
	champ, _ := armGWSpec(t, armCfg(t), "50", champTask(), armLayer)
	champion, err := champ.run(t, implementCommitting(path, selfCommitBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	arm, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
	rec, err := arm.run(t, implementCommitting(path, selfCommitBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the arm failed with nothing published", rec.Status, rec.Outcome, rec.Reason)
	}
	if rec.Reason != champion.Reason {
		t.Errorf("the arm's reason = %q; want the champion's %q: the two arms read the same rule over the same range", rec.Reason, champion.Reason)
	}
	if len(publishes(arm.harness)) != 0 {
		t.Errorf("the refused arm published something: %v", publishes(arm.harness))
	}
	if remoteHasBranch(t, arm.harness) {
		t.Error("the refused arm pushed its branch")
	}
}
