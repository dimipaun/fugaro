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
	b := make([]byte, size)
	// An image encoder's output is not text: its header carries NULs well
	// inside the window the rule reads.
	copy(b, "\x89PNG\r\n\x1a\n\x00\x00")
	if err := os.WriteFile(full, b, 0o644); err != nil {
		t.Fatal(err)
	}
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
