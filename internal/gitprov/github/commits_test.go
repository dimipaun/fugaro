package github

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// commitSHA is the commit the reads here resolve to: forty lower-hex
// characters, the shape gitops.ValidCommitSHA holds a value to.
const commitSHA = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"

// blobSHA is a git object id, as the tree listing names one. It is not a
// commit this tree holds: a blob read is by object id and nothing else.
const blobSHA = "b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5"

// TestCommitResolvesARefToItsCommit: the commit a ref names, through the
// commits read — and an empty ref read as HEAD, which is the repository's
// default branch, because this API has no other way to ask for it.
func TestCommitResolvesARefToItsCommit(t *testing.T) {
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/commits/release", 200, `{"sha": "`+commitSHA+`"}`),
		contentsExchange(t, "/repos/acme/web/commits/HEAD", 200, `{"sha": "`+commitSHA+`"}`),
	)...)
	got, err := p.Commit(ctx, "release")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got != commitSHA {
		t.Fatalf("Commit = %q, want %q", got, commitSHA)
	}
	if got, err = p.Commit(ctx, ""); err != nil || got != commitSHA {
		t.Fatalf("Commit(\"\") = %q, %v", got, err)
	}
}

// TestCommitRefusesAReplyThatNamesNoCommit: a 200 whose sha is missing is not
// a commit, and the read says so rather than handing an empty value on — the
// caller holds it to a full commit anyway, and this is the message that says
// which read answered nothing.
func TestCommitRefusesAReplyThatNamesNoCommit(t *testing.T) {
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/commits/release", 200, `{"sha": ""}`),
	)...)
	if got, err := p.Commit(ctx, "release"); err == nil {
		t.Fatalf("Commit = %q, want an error", got)
	}
}

// treeListing is the git tree API's reply: the entries, each with its path,
// its type and its own size, and whether GitHub truncated the listing.
func treeListing(truncated bool, entries ...string) string {
	return fmt.Sprintf(`{"sha": %q, "truncated": %t, "tree": [%s]}`, commitSHA, truncated, strings.Join(entries, ","))
}

// treeEntryJSON is one entry of a listing: a file with the object id and size
// its read answers with, or a "tree"/"commit" entry, which is not a file.
func treeEntryJSON(path, kind string, size int) string {
	return fmt.Sprintf(`{"path": %q, "mode": "100644", "type": %q, "size": %d, "sha": %q}`, path, kind, size, blobSHA)
}

// blobReply is the git blob API's answer: the file's bytes, base64, wrapped
// at 60 characters as GitHub wraps them.
func blobReply(b []byte) string {
	return fmt.Sprintf(`{"sha": %q, "encoding": "base64", "size": %d, "content": %q}`,
		blobSHA, len(b), wrappedBase64(b))
}

// TestTreeReadsTheFilesUnderThePathAtTheCommit: the directory's files, at the
// commit, each read by the object id the listing names — with the paths
// relative to the directory that was read, the entries that are not files left
// out, and the repository's other files left out with them.
func TestTreeReadsTheFilesUnderThePathAtTheCommit(t *testing.T) {
	def := []byte("FROM example.invalid/base\n")
	src := []byte("package main\n")
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(false,
			treeEntryJSON("ci/Dockerfile", "blob", len(def)),
			treeEntryJSON("ci/src", "tree", 0),
			treeEntryJSON("ci/src/main.go", "blob", len(src)),
			treeEntryJSON("ci/vendor", "commit", 0),
			treeEntryJSON("web/index.html", "blob", 5),
		)),
		contentsExchange(t, "/repos/acme/web/git/blobs/"+blobSHA, 200, blobReply(def)),
		contentsExchange(t, "/repos/acme/web/git/blobs/"+blobSHA, 200, blobReply(src)),
	)...)
	files, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	want := []gitprov.TreeFile{{Path: "Dockerfile", Data: def}, {Path: "src/main.go", Data: src}}
	if len(files) != len(want) {
		t.Fatalf("Tree = %d files (%v), want %d", len(files), files, len(want))
	}
	for i, f := range files {
		if f.Path != want[i].Path || string(f.Data) != string(want[i].Data) {
			t.Errorf("Tree[%d] = %q %q, want %q %q", i, f.Path, f.Data, want[i].Path, want[i].Data)
		}
	}
}

// TestTreeReadsADirectoryWrittenWithItsTrailingSlash: a context names a
// directory, and the catalog's grammar takes one written the way the key's own
// example spells it — "ci/" (D1391) — which is the same directory the same
// entry written "ci" names: the read answers the files under it, relative to
// it.
func TestTreeReadsADirectoryWrittenWithItsTrailingSlash(t *testing.T) {
	def := []byte("FROM example.invalid/base\n")
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(false,
			treeEntryJSON("ci/Dockerfile", "blob", len(def)),
			treeEntryJSON("web/index.html", "blob", 5),
		)),
		contentsExchange(t, "/repos/acme/web/git/blobs/"+blobSHA, 200, blobReply(def)),
	)...)
	files, err := p.Tree(ctx, "ci/", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Tree of a context written with its trailing slash: %v", err)
	}
	if len(files) != 1 || files[0].Path != "Dockerfile" || string(files[0].Data) != string(def) {
		t.Fatalf("Tree = %v, want the one file under ci, named relative to it", files)
	}
}

// TestTreeRefusesAFileTooLargeForOneBlobReply: a blob reply carries the file
// base64 inside a JSON object, and the client bounds one reply, so a file over
// three quarters of that has no reply that holds it whatever ceiling the
// caller gave — the caller's is larger. It is refused by name before the read,
// which is the exchange this fixture does not hold: a file left to the read
// would come back as a truncated reply and a decoding error that names no
// file.
func TestTreeRefusesAFileTooLargeForOneBlobReply(t *testing.T) {
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(false,
			treeEntryJSON("ci/big.bin", "blob", maxBlobBytes+1),
		)),
	)...)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 30})
	if err == nil {
		t.Fatal("a file larger than one blob reply was read")
	}
	if !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) || !strings.Contains(err.Error(), "big.bin") {
		t.Errorf("Tree error = %v, want it to name the file and carry %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesATreeOverItsLimit: a directory over the byte ceiling is
// refused as soon as the listing says so — the file that took it over is not
// fetched, which is the exchange this fixture does not hold.
func TestTreeRefusesATreeOverItsLimit(t *testing.T) {
	first := []byte("one\n")
	second := []byte("two\n")
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(false,
			treeEntryJSON("ci/a", "blob", len(first)),
			treeEntryJSON("ci/b", "blob", len(second)),
		)),
		contentsExchange(t, "/repos/acme/web/git/blobs/"+blobSHA, 200, blobReply(first)),
	)...)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: int64(len(first) + len(second) - 1)})
	if err == nil {
		t.Fatal("Tree over its byte limit = no error")
	}
	if !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Errorf("Tree error = %v, want it to carry %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesMoreFilesThanItsLimit: the same, by count — the second file
// is over the ceiling of one and is never read.
func TestTreeRefusesMoreFilesThanItsLimit(t *testing.T) {
	one := []byte("one\n")
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(false,
			treeEntryJSON("ci/a", "blob", len(one)),
			treeEntryJSON("ci/b", "blob", len(one)),
		)),
		contentsExchange(t, "/repos/acme/web/git/blobs/"+blobSHA, 200, blobReply(one)),
	)...)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 1, MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Fatalf("Tree over its file limit = %v, want %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesATruncatedListing: GitHub answers a tree larger than its own
// ceiling with a partial listing and "truncated": reading that as the whole
// tree would digest a context that is missing files, so it is refused, and no
// blob is read.
func TestTreeRefusesATruncatedListing(t *testing.T) {
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/git/trees/"+commitSHA+"?recursive=1", 200, treeListing(true,
			treeEntryJSON("ci/a", "blob", 4),
		)),
	)...)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Fatalf("Tree of a truncated listing = %v, want %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesAPathOutsideTheRepository: a path this read must not carry
// into an API path is refused before any request — this fixture holds none, so
// a call would fail the test as an unexpected request.
func TestTreeRefusesAPathOutsideTheRepository(t *testing.T) {
	p := contentsProvider(t)
	for _, path := range []string{"../etc", "/etc", "ci/../.."} {
		if files, err := p.Tree(ctx, path, commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20}); err == nil {
			t.Errorf("Tree(%q) = %d files, want an error", path, len(files))
		}
	}
}
