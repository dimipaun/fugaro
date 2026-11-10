package bitbucket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

var (
	_ gitprov.CommitReader = (*Provider)(nil)
	_ gitprov.TreeReader   = (*Provider)(nil)
)

// srcPageLen is how many entries of a directory listing one page asks for:
// the API's own maximum, so a context costs as few requests as it can.
const srcPageLen = 100

// maxSrcPages bounds the pages a listing walks. A context is bounded by its
// caller's own file ceiling long before this many pages of it arrive, so the
// bound is here for the case the caller's is not what stops the walk: an API
// that keeps answering with a next page. It is a page count and not a file
// count because the walk stops on the first page whose entries are over the
// caller's limit.
const maxSrcPages = 200

// Commit implements gitprov.CommitReader: the commit a ref names, through
// GET /2.0/repositories/{workspace}/{repo}/commit/{rev}, whose "hash" is the
// commit — the read resolveFullHash already makes for a pull request's
// abbreviated hash, which is the same endpoint and the same field.
//
// An empty ref is read at the repository's own default branch, which is asked
// for rather than assumed (defaultBranch, Contents' own rule): this API needs
// a revision, so a source that names no branch would otherwise be read at the
// wrong one or none at all.
//
// UNVERIFIED against the live API, like the file read beside it: the shape
// follows Bitbucket's documented commit endpoint, not a recorded response.
func (p *Provider) Commit(ctx context.Context, ref string) (string, error) {
	at := pluginwire.Printable(ref)
	rev := ref
	if rev == "" {
		name, err := p.defaultBranch(ctx)
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", at, err)
		}
		rev = name
	} else if !validContentsRef(rev) {
		return "", fmt.Errorf("resolving %s: the ref is not a commit or a branch name", at)
	}
	return p.commitHash(ctx, rev)
}

// Tree implements gitprov.TreeReader: the files under path, at the commit ref,
// read from the src endpoint's directory listing and then one file read each.
//
// The listing comes first and is where the limit is enforced: GET
// /2.0/repositories/{workspace}/{repo}/src/{commit}/{path}/?pagelen=100&recursive=true
// answers the directory's entries, each with its path, its type and its own
// size, so a tree over the caller's ceiling is refused before a file of it is
// read. The listing is walked page by page, bounded by maxSrcPages.
//
// What comes back is each file's path relative to path — the identity a
// context's digest is taken over, which must not move when the context is
// moved inside its repository — and its bytes, read one at a time through the
// same src endpoint a file read uses. An entry that is not a file (a
// "commit_directory") is left out.
//
// A path that is not a directory is not an error of this read's to tell from
// an empty one: both answer with no entries, and the caller refuses a context
// with no files in it.
func (p *Provider) Tree(ctx context.Context, path, ref string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	at := pluginwire.Printable(path)
	if path != "" && path != "." && !validContentsPath(path) {
		return nil, fmt.Errorf("reading %s: the path is not a relative directory path inside the repository", at)
	}
	if !validContentsRef(ref) {
		return nil, fmt.Errorf("reading %s: the ref is not a commit or a branch name (%s)", at, pluginwire.Printable(ref))
	}
	entries, err := p.srcListing(ctx, path, ref)
	if err != nil {
		return nil, fmt.Errorf("reading %s: listing the directory at %s: %w", at, pluginwire.Printable(ref), err)
	}
	prefix := ""
	if path != "" && path != "." {
		prefix = strings.TrimSuffix(path, "/") + "/"
	}
	var files []gitprov.TreeFile
	var total int64
	for _, e := range entries {
		if e.Type != "commit_file" {
			continue
		}
		rel, ok := strings.CutPrefix(e.Path, prefix)
		if !ok || rel == "" {
			continue
		}
		// The listing's size is the file's own, so the ceiling is decided
		// here, before the read.
		if len(files)+1 > limit.MaxFiles || total+e.Size > limit.MaxBytes {
			return nil, fmt.Errorf("reading %s: the directory at %s holds more than %d files or more than %d bytes: %w",
				at, pluginwire.Printable(ref), limit.MaxFiles, limit.MaxBytes, gitprov.ErrTreeTooLarge)
		}
		// The read is bounded by what the ceiling has left, so a listing that
		// understated a file's size cannot carry the tree over it.
		data, _, err := p.srcFile(ctx, e.Path, ref, limit.MaxBytes-total)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", pluginwire.Printable(e.Path), err)
		}
		if int64(len(data)) > limit.MaxBytes-total {
			return nil, fmt.Errorf("reading %s: the directory at %s holds more than %d files or more than %d bytes: %w",
				at, pluginwire.Printable(ref), limit.MaxFiles, limit.MaxBytes, gitprov.ErrTreeTooLarge)
		}
		files = append(files, gitprov.TreeFile{Path: rel, Data: data})
		total += int64(len(data))
	}
	return files, nil
}

// srcEntry is one entry of a directory listing: the file's path from the
// repository root, what the entry is, and — for a file — its own size.
type srcEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// srcListing walks the src endpoint's listing of one directory at rev, page by
// page, and answers every entry it holds. The listing is the directory's and
// its subdirectories' (recursive=true), each entry carrying its path from the
// repository root, which is the path a file read is made with.
func (p *Provider) srcListing(ctx context.Context, path, rev string) ([]srcEntry, error) {
	next := p.srcListingURL(path, rev)
	var entries []srcEntry
	for page := 0; next != ""; page++ {
		if page >= maxSrcPages {
			return nil, fmt.Errorf("the listing is longer than the %d pages this read walks", maxSrcPages)
		}
		var listing struct {
			Values []srcEntry `json:"values"`
		}
		link, err := p.api.DoPage(ctx, http.MethodGet, next, nil, &listing)
		if err != nil {
			return nil, err
		}
		entries = append(entries, listing.Values...)
		next = link
	}
	return entries, nil
}

// srcListingURL is the src API's path for one directory: each segment escaped
// on its own (contentsURL's own rule), the ref escaped the same way, and the
// trailing slash the listing is read from. The repository root is asked for by
// the ref alone, which is how this API names it.
func (p *Provider) srcListingURL(path, rev string) string {
	root := "/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug) + "/src/" + url.PathEscape(rev) + "/"
	if path == "" || path == "." {
		return root + "?pagelen=" + strconv.Itoa(srcPageLen) + "&recursive=true"
	}
	segs := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return root + strings.Join(segs, "/") + "/?pagelen=" + strconv.Itoa(srcPageLen) + "&recursive=true"
}
