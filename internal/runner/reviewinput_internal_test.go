package runner

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// pngFixture is a tiny generated PNG: the format's own eight-byte signature,
// an IHDR chunk header and a scrap of IDAT. It is not a real image and
// decodes to nothing — what matters is the NUL byte in it, git's own test
// for "this blob is binary" (a NUL in the first 8000 bytes), which is what
// makes the file take the path under test. Run 20261021-002046-31cc failed
// on two real PNGs under mobile/assets/; no real image is needed to
// reproduce how git and the gateway treat one.
const pngFixture = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0aIDAT\x08\xd7\x63\x00\x01\x00\x00\x05\x00\x01\x0d\x0a\x2d\xb4\x00\x00\x00\x00IEND\xae\x42\x60\x82"

// reviewInputRepo is a checkout whose base branch holds README.md and a
// binary file (old-icon.bin), and whose branch fugaro/x adds a text file, an
// added PNG and a deleted PNG — the shape of the pull request that failed.
func reviewInputRepo(t *testing.T) *gitops.Repo {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "old-icon.bin": pngFixture})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	write(t, repo.Dir, "src/mobile.go", "package mobile\n")
	write(t, repo.Dir, "mobile/assets/icon.png", pngFixture)
	if err := os.Remove(filepath.Join(repo.Dir, "old-icon.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitAll(context.Background(), "the run's work"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// write puts content at path under dir, creating directories.
func write(t *testing.T, dir, path, content string) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lines is the input's text, one entry per line.
func inputLines(in reviewInput) []string {
	return strings.Split(strings.TrimSuffix(in.Text(), "\n"), "\n")
}

// TestReviewInputOfAPullRequestThatAddsAPngIsTextOnly is the failing run's
// shape end to end through the review stage's input builder: a diff that adds
// a PNG, deletes a binary file and edits a text file. The built input names
// each binary file in one line with its size, carries no image part and no
// image bytes in any form — raw or base64 — and never asks git for a binary
// path (git's own "Binary files ... differ" notice is not in the text either,
// which is what proves the paths that carry binary content were left to the
// marker alone).
//
// Mutation (make the builder's binary part carry `ShowFile`'s bytes, or its
// base64, instead of binaryMarker): the NUL and base64 assertions fail.
// Mutation (drop the binary part, leaving the diff and the text paths):
// the marker assertion fails. Mutation (pass every changed path to
// reviewTextDiff, so binaries are diffed too): the "Binary files" assertion
// fails. Mutation (report the size of head's blob without falling back to
// base for a deletion): the deleted file's size assertion fails.
func TestReviewInputOfAPullRequestThatAddsAPngIsTextOnly(t *testing.T) {
	repo := reviewInputRepo(t)
	in, err := buildReviewInput(context.Background(), repo, "origin/main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if in.Base != "origin/main" {
		t.Fatalf("Base = %q, want origin/main", in.Base)
	}
	for i, p := range in.Parts {
		if p.Kind != reviewPartText {
			t.Fatalf("part %d has kind %q, want text", i, p.Kind)
		}
	}
	text := in.Text()
	if strings.ContainsRune(text, 0) {
		t.Fatalf("the input carries a NUL byte:\n%q", text)
	}
	if strings.Contains(text, pngFixture) {
		t.Fatal("the input carries the PNG's bytes")
	}
	if b64 := base64.StdEncoding.EncodeToString([]byte(pngFixture)); strings.Contains(text, b64) {
		t.Fatal("the input carries the PNG's bytes, base64-encoded")
	}
	if strings.Contains(text, `"type":"image"`) || strings.Contains(text, "media_type") {
		t.Fatalf("the input carries an image block:\n%s", text)
	}
	if strings.Contains(text, "Binary files") {
		t.Fatalf("the input carries git's own binary notice:\n%s", text)
	}
	size := len(pngFixture)
	want := []string{
		"binary file mobile/assets/icon.png changed, " + strconv.Itoa(size) + " bytes",
		"binary file old-icon.bin changed, " + strconv.Itoa(size) + " bytes",
	}
	for _, line := range want {
		if !slicesContain(inputLines(in), line) {
			t.Fatalf("the input has no line %q:\n%s", line, text)
		}
	}
	if !strings.Contains(text, "diff --git a/src/mobile.go b/src/mobile.go") ||
		!strings.Contains(text, "+package mobile") {
		t.Fatalf("the input carries no text diff:\n%s", text)
	}
	if !strings.Contains(text, "Fugaro built the changes under review into this prompt") {
		t.Fatalf("the input does not say what it is:\n%s", text)
	}
	if !strings.Contains(text, binaryRule) {
		t.Fatalf("the input does not carry the rule about binary files:\n%s", text)
	}
}

// TestReviewInputMarksAnOddPathInOneLine: a path holding a newline — which
// git is willing to carry, and reads verbatim from numstat -z — is quoted, so
// a binary file is always exactly one line of the input and can never break
// the input's line structure.
//
// Mutation (drop binaryMarker's quoting): the marker becomes two lines and
// the one-line assertion fails.
func TestReviewInputMarksAnOddPathInOneLine(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	write(t, repo.Dir, "odd\nname.bin", pngFixture)
	if _, err := repo.CommitAll(context.Background(), "an odd path"); err != nil {
		t.Fatal(err)
	}
	in, err := buildReviewInput(context.Background(), repo, "origin/main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := `binary file "odd\nname.bin" changed, ` + strconv.Itoa(len(pngFixture)) + " bytes"
	if !slicesContain(inputLines(in), want) {
		t.Fatalf("no line %q in:\n%s", want, in.Text())
	}
}

// TestReviewInputBoundsAnOversizedDiff: the input's diff is cut at
// maxReviewDiffBytes, at a line boundary, with a note saying where it stopped
// and how to read the rest. Without the cut a wide change would put a diff
// larger than the reviewer's context into every review call.
//
// Mutation (return diff uncut from cutReviewDiff): the "cut the diff here"
// assertion fails.
func TestReviewInputBoundsAnOversizedDiff(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 60) + "\n"
	write(t, repo.Dir, "wide.txt", strings.Repeat(line, (maxReviewDiffBytes/len(line))+100))
	if _, err := repo.CommitAll(context.Background(), "a wide change"); err != nil {
		t.Fatal(err)
	}
	in, err := buildReviewInput(context.Background(), repo, "origin/main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	text := in.Text()
	if !strings.Contains(text, "Fugaro cut the diff here") {
		t.Fatalf("the diff is not cut:\n%s", text[len(text)-400:])
	}
	if len(text) > maxReviewDiffBytes+1024 {
		t.Fatalf("the input is %d bytes, want at most about %d", len(text), maxReviewDiffBytes)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("the input does not end in a newline")
	}
}

// TestReviewInputOfAnUnchangedBranch: a range that changes nothing still
// builds an input, and says so, rather than an input that claims to carry a
// diff and attaches none.
func TestReviewInputOfAnUnchangedBranch(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	in, err := buildReviewInput(context.Background(), repo, "origin/main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Text(), "the branch changes nothing against origin/main") {
		t.Fatalf("input = %q", in.Text())
	}
	if len(in.Parts) != 1 || in.Parts[0].Kind != reviewPartText {
		t.Fatalf("parts = %+v", in.Parts)
	}
}

// TestReviewInputBlockIsEmptyWithoutACheckout: a run with no checkout has no
// diff to build, and gets no block rather than an error — the review stage
// then reviews with the prompt it always had.
//
// Mutation (drop reviewInputBlock's r.repo == nil guard): this panics.
func TestReviewInputBlockIsEmptyWithoutACheckout(t *testing.T) {
	r := &run{cfg: &config.Config{Git: config.Git{BaseBranch: "main"}}, d: Deps{Log: slog.New(slog.DiscardHandler)}}
	if got := r.reviewInputBlock(context.Background()); got != "" {
		t.Fatalf("block = %q, want none", got)
	}
}

// TestReviewInputBlockOnARealCheckout is the same method over a repo: the
// block is the built input, and the base is the branch the run reviews
// against, spelled as the prompt spells it.
func TestReviewInputBlockOnARealCheckout(t *testing.T) {
	repo := reviewInputRepo(t)
	r := &run{repo: repo, cfg: &config.Config{Git: config.Git{BaseBranch: "main"}}, d: Deps{Log: slog.New(slog.DiscardHandler)}}
	got := r.reviewInputBlock(context.Background())
	if !strings.Contains(got, "binary file mobile/assets/icon.png changed, "+strconv.Itoa(len(pngFixture))+" bytes") {
		t.Fatalf("block:\n%s", got)
	}
	if !strings.Contains(got, "git diff origin/main...HEAD") {
		t.Fatalf("the block does not name the range it carries:\n%s", got)
	}
}

// slicesContain reports whether lines holds want as one whole line.
func slicesContain(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
