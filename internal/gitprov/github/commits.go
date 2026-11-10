package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

var (
	_ gitprov.CommitReader = (*Provider)(nil)
	_ gitprov.TreeReader   = (*Provider)(nil)
)

// maxTreeEntries bounds the listing a tree read walks: GitHub answers
// /git/trees with at most 100,000 entries and 7 MB, and says so with
// "truncated" rather than by failing. A caller's own limit is checked against
// the entries that arrive, so this is the ceiling past which the reply itself
// is incomplete and nothing may be read out of it.
const maxTreeEntries = 100_000

// maxBlobBytes is the largest file this adapter can read out of a tree: one
// blob reply carries the file base64-encoded inside a JSON object, and the
// client that reads the reply bounds it at httpjson.MaxReplyBytes, so a file
// over three quarters of that has no reply that holds it. The caller's own
// ceiling can be larger — a context's is — and this is the one this adapter
// cannot go past: a file over it is refused by name before the read rather
// than left to surface as a truncated reply's decoding error.
const maxBlobBytes = httpjson.MaxReplyBytes / 4 * 3

// Commit implements gitprov.CommitReader: the commit a ref names, through
// GET /repos/{owner}/{repo}/commits/{ref} — the ref one escaped path segment,
// so a branch named "release/1.0" stays one — whose "sha" is the commit.
//
// An empty ref is read as HEAD, which is what GitHub resolves to the
// repository's default branch: a source that names no branch is read at the
// repository's own, and this adapter has no second call to learn its name from
// (Bitbucket's has to ask, because its API has no HEAD).
//
// The value is not validated here: what shape it must have is the caller's
// rule (catalog.resolveRef holds it to a full commit), because the refusal a
// person reads has to name the catalog entry the ref came from.
func (p *Provider) Commit(ctx context.Context, ref string) (string, error) {
	at := pluginwire.Printable(ref)
	if ref == "" {
		ref = "HEAD"
	}
	var c struct {
		SHA string `json:"sha"`
	}
	if err := p.api.Do(ctx, "GET", p.repoPath("/commits/"+url.PathEscape(ref)), nil, &c); err != nil {
		return "", fmt.Errorf("resolving %s: %w", at, err)
	}
	if c.SHA == "" {
		return "", fmt.Errorf("resolving %s: the reply names no commit", at)
	}
	return c.SHA, nil
}

// Tree implements gitprov.TreeReader: the files under path, at the commit ref,
// read through the git tree API and then one blob read per file.
//
// The listing comes first and is where the limit is enforced: GET
// /repos/{owner}/{repo}/git/trees/{commit}?recursive=1 answers every entry of
// the commit's tree, each with its path, its type and — for a file — its size,
// so a tree over the caller's ceiling is refused before a byte of it is
// fetched. The listing is the whole repository's, not the directory's: this
// API has no subtree read that names a path, so the entries under path are
// taken out of it, and a reply GitHub truncated is refused rather than read as
// the whole tree (maxTreeEntries).
//
// What comes back is each file's path relative to path — the identity a
// context's digest is taken over, which must not move when the context is
// moved inside its repository — and its bytes, read one at a time from
// /git/blobs/{sha}. A submodule is a "commit" entry rather than a "blob" and
// is left out: the tree holds a link to another repository, and what a build
// would find at that path is an empty directory.
//
// A path that is not a directory is not an error of this read's to tell from
// an empty one: both answer with no entries, and the caller refuses a context
// with no files in it.
//
// The path is read with its trailing slash taken off: a context names a
// directory, and the catalog's own grammar accepts one written the way its
// example spells it ("ci/", D1391), where ValidRepoPath's rule is a file's and
// refuses that slash.
func (p *Provider) Tree(ctx context.Context, path, ref string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	at := pluginwire.Printable(path)
	dir := strings.TrimSuffix(path, "/")
	if dir != "" && dir != "." && !gitprov.ValidRepoPath(dir) {
		return nil, fmt.Errorf("reading %s: the path is not a relative directory path inside the repository", at)
	}
	var listing struct {
		Tree      []treeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := p.api.Do(ctx, "GET", p.repoPath("/git/trees/"+url.PathEscape(ref))+"?recursive=1", nil, &listing); err != nil {
		return nil, fmt.Errorf("reading %s: listing the tree at %s: %w", at, pluginwire.Printable(ref), err)
	}
	if listing.Truncated {
		return nil, fmt.Errorf("reading %s: the tree of %s holds more than the %d entries this listing returns: %w",
			at, pluginwire.Printable(ref), maxTreeEntries, gitprov.ErrTreeTooLarge)
	}
	prefix := ""
	if dir != "" && dir != "." {
		prefix = dir + "/"
	}
	var files []gitprov.TreeFile
	var total int64
	for _, e := range listing.Tree {
		if e.Type != "blob" {
			continue
		}
		rel, ok := strings.CutPrefix(e.Path, prefix)
		if !ok || rel == "" {
			continue
		}
		// The listing's size is the file's own — git knows it without reading
		// the object — so the ceiling is decided here, before the read.
		if len(files)+1 > limit.MaxFiles || total+e.Size > limit.MaxBytes {
			return nil, overLimit(at, ref, limit)
		}
		if e.Size > maxBlobBytes {
			return nil, fmt.Errorf("reading %s: the file is %d bytes and one blob read carries at most %d: %w",
				pluginwire.Printable(e.Path), e.Size, maxBlobBytes, gitprov.ErrTreeTooLarge)
		}
		data, err := p.blob(ctx, e.SHA, e.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, gitprov.TreeFile{Path: rel, Data: data})
		total += int64(len(data))
		// The bytes that arrived are what the ceiling is about, and the
		// listing is only what they were checked against: a host that
		// understated a file's size is over the caller's ceiling all the same.
		if total > limit.MaxBytes {
			return nil, overLimit(at, ref, limit)
		}
	}
	return files, nil
}

// overLimit is the refusal a tree read makes when what it found is over the
// caller's ceiling: the same sentence for the count the listing announced and
// for the bytes that arrived, because the caller has one ceiling and one thing
// to do about it.
func overLimit(at, ref string, limit gitprov.TreeLimit) error {
	return fmt.Errorf("reading %s: the tree at %s holds more than %d files or more than %d bytes: %w",
		at, pluginwire.Printable(ref), limit.MaxFiles, limit.MaxBytes, gitprov.ErrTreeTooLarge)
}

// treeEntry is one entry of the git tree API's reply. Size is a file's own and
// is absent for anything that is not one.
type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	SHA  string `json:"sha"`
}

// blob reads one file of a tree by its git object id, through
// GET /repos/{owner}/{repo}/git/blobs/{sha}: the same base64 shape a contents
// read answers with, wrapped the same way. The file's path is named in a
// failure rather than the object id, because the path is what a person reading
// a refused context has to find.
//
// The caller's ceiling is what decides whether this read happens at all, and
// it was checked against the listing before it — along with this adapter's own
// per-file bound, which is smaller than a context's (maxBlobBytes), so a file
// too large for one reply is refused by Tree and never reaches here.
func (p *Provider) blob(ctx context.Context, sha, path string) ([]byte, error) {
	at := pluginwire.Printable(path)
	if sha == "" {
		return nil, fmt.Errorf("reading %s: the listing names no object for it", at)
	}
	var f struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := p.api.Do(ctx, "GET", p.repoPath("/git/blobs/"+url.PathEscape(sha)), nil, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}
	if f.Encoding != "base64" {
		return nil, fmt.Errorf("reading %s: the reply is encoded as %s, not base64", at, pluginwire.Printable(f.Encoding))
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("reading %s: decoding the reply: %w", at, err)
	}
	return data, nil
}
