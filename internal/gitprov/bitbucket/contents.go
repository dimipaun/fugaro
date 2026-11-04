package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

var _ gitprov.ContentsReader = (*Provider)(nil)

// contentsMaxRedirects bounds a chain of same-origin redirects, the way the
// standard client's own ten does; it is this adapter's number rather than the
// standard client's so that the rule and its bound are read together.
const contentsMaxRedirects = 10

// maxContentsErrorBody is how much of a refused reply's body an error keeps:
// enough to name what the API said, never a whole page of it.
const maxContentsErrorBody = 512

// Contents implements gitprov.ContentsReader: one file of this repository,
// read through GET /2.0/repositories/{workspace}/{repo}/src/{commit}/{path}
// at ref (D1140, PIC3 -- a catalog entry's dockerfile source is fetched this
// way before a build, so the build reads a copy rather than a checkout).
//
// The read is this repository's and only its own: the workspace, the slug and
// the path are the provider's, and the request carries the same access token
// the provider's other API calls carry (design §6.2; with the credentials
// service on, the token is the service's, C2.2), so no access beyond what the
// project already holds is used. The token goes in the Authorization header
// and nowhere else -- not a URL, not a query, not a message -- and the one
// value that could put it into an error is the reply's own body, which is
// redacted before it is quoted (replyExcerpt).
//
// The host is this provider's API root and stays it: the request is built
// from Options.BaseURL (the root the run's own admission has pinned to
// Bitbucket, runner.checkBitbucketAPIRoot, or one the owner allow-lists), and
// a redirect to any other origin is refused rather than followed. Making the
// client follow one would send the token, and this run's read, wherever a
// reply points -- a compromised or misconfigured API answering 302 is not a
// way to reach a host the run never named.
//
// An empty ref is read at the repository's own default branch, which is asked
// for rather than assumed: Bitbucket's src endpoint needs a commit, a branch
// or a tag, so a source that names no branch would otherwise be a read of the
// wrong revision or none at all. A repository that names no default branch is
// refused.
//
// The reply is the file's bytes and nothing else: a path that names a
// directory answers with a JSON listing, which is refused rather than handed
// on as a Dockerfile, and an empty body, or one over gitprov.MaxContentsBytes,
// is refused too -- a caller may put these bytes into an image's build, so
// what it gets is a bounded, non-empty regular file or an error.
//
// UNVERIFIED against the live API (no live Bitbucket access here): the shape
// of the src endpoint, and of the repository's mainbranch field the default
// branch is read from, follows Bitbucket's documented REST API, not a
// recorded response; the owner should confirm both in the sandbox the way
// resolveFullHash's comment asks for its own, before relying on
// `fugaro image build` against a real Bitbucket repository. A branch name
// holding a "/" is sent as one escaped segment, which is what keeps it one
// segment; whether the live endpoint resolves it is part of the same
// unverified shape.
func (p *Provider) Contents(ctx context.Context, path, ref string) ([]byte, error) {
	// The path in a message is the caller's value printed through
	// pluginwire.Printable, as every other value that reaches a log here is:
	// validContentsPath refuses control characters, and it deliberately
	// allows formatting (bidi) ones, so a path a person reads back in a
	// failure is escaped rather than taken at its word.
	at := pluginwire.Printable(path)
	if !validContentsPath(path) {
		return nil, fmt.Errorf("reading %s: the path is not a relative file path inside the repository", at)
	}
	if ref != "" && !validContentsRef(ref) {
		return nil, fmt.Errorf("reading %s: the ref is not a commit or a branch name (%s)", at, pluginwire.Printable(ref))
	}
	rev := ref
	if rev == "" {
		name, err := p.defaultBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", at, err)
		}
		rev = name
	}
	tok, _, err := p.tokens.Token(ctx, apiMinValid)
	if err != nil {
		return nil, fmt.Errorf("reading %s: authenticating: %w", at, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.contentsURL(path, rev), nil)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}
	// The file itself, not the API's JSON: this adapter reads a reply's body
	// as the file's bytes, and asking for them is what says so.
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := p.contentsClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, secretFree(err, tok))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, gitprov.MaxContentsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: reading the reply: %w", at, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("reading %s: %s: HTTP %d: %s", at, http.MethodGet, resp.StatusCode,
			secretsFree(replyExcerpt(data), tok))
	}
	if jsonContentType(resp.Header.Get("Content-Type")) {
		return nil, fmt.Errorf("reading %s: the repository holds a directory there, not a file", at)
	}
	switch {
	case len(data) == 0:
		return nil, fmt.Errorf("reading %s: the reply is empty", at)
	case len(data) > gitprov.MaxContentsBytes:
		return nil, fmt.Errorf("reading %s: it is more than the %d bytes a definition may be", at, gitprov.MaxContentsBytes)
	}
	return data, nil
}

// defaultBranch is the repository's own default branch, the ref a source that
// names none is read at. Bitbucket names it in the repository object, and the
// read is fail-closed: a repository that names no branch, or one that is not
// a ref this adapter would read at, is refused rather than read at a name
// this code chose.
func (p *Provider) defaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		MainBranch *struct {
			Name string `json:"name"`
		} `json:"mainbranch"`
	}
	path := "/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug)
	if err := p.api.Do(ctx, http.MethodGet, path, nil, &repo); err != nil {
		return "", fmt.Errorf("reading the repository's default branch: %w", err)
	}
	if repo.MainBranch == nil || repo.MainBranch.Name == "" {
		return "", errors.New("the repository names no default branch: name the source's branch")
	}
	if !validContentsRef(repo.MainBranch.Name) {
		return "", errors.New("the repository's default branch is not a commit or a branch name")
	}
	return repo.MainBranch.Name, nil
}

// contentsURL is the src API's path for one file: each segment escaped on its
// own, so a segment can never be read as part of the API's own path (the
// file's "a/b" stays two segments, and a segment's "?" or "#" stays in the
// file's name), and the ref escaped the same way for the same reason -- a
// branch named "release/1.0" is one segment, not a path into another
// directory.
func (p *Provider) contentsURL(path, ref string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.TrimSuffix(p.o.BaseURL, "/") +
		"/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug) +
		"/src/" + url.PathEscape(ref) + "/" + strings.Join(segs, "/")
}

// contentsClient is the client this read uses: the provider's own, with its
// redirects pinned to the API root. The standard client follows a redirect
// anywhere and drops the Authorization header on another host, which is not
// enough here -- the reply that would be read is as much of the answer as the
// credential is, and a caller puts these bytes into a build.
func (p *Provider) contentsClient() *http.Client {
	hc := p.o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: httpjson.DefaultTimeout}
	}
	base, _ := url.Parse(p.o.BaseURL)
	return &http.Client{
		Transport: hc.Transport,
		Timeout:   hc.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= contentsMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", contentsMaxRedirects)
			}
			if !sameOrigin(req.URL, base) {
				return fmt.Errorf("refusing a redirect off the API root to %s", pluginwire.Printable(req.URL.Host))
			}
			return nil
		},
	}
}

// sameOrigin reports whether u is on the API root's own scheme and host.
// A root that does not parse is on nothing, which refuses every redirect
// rather than allowing one.
func sameOrigin(u, base *url.URL) bool {
	if base == nil || u == nil {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// validContentsPath reports whether p is a shape this adapter will put into
// the src API's path: gitprov.ValidRepoPath's one clean relative file path
// (non-empty, no "." or ".." segment, already clean, no control character --
// a NUL among them), and no backslash, which that shared rule leaves to the
// adapters because it is not a separator to every host.
//
// A backslash is refused rather than escaped here because this API's path is
// read by more than Go: a proxy in front of it may fold "\" into "/", so
// "a\..\b" would reach another path than the one this adapter escaped. What
// is escaped is escaped per segment (contentsURL), and a segment holding a
// backslash is the one thing that escaping cannot make mean what it says.
func validContentsPath(p string) bool {
	return gitprov.ValidRepoPath(p) && !strings.ContainsRune(p, '\\')
}

// validContentsRef reports whether ref names one revision the catalog can
// carry: a full commit SHA, or a branch name by the git ref grammar
// (config.ValidBranchName), the same rule the catalog's own validation
// applies to a source's branch -- so a ref this adapter reads and one the
// catalog accepts cannot disagree, and neither can this rule loosen or
// tighten without the other. A tag is read by the API the same way a branch
// is, and a tag name that is also a valid branch name is accepted as one.
func validContentsRef(ref string) bool {
	return gitops.ValidCommitSHA(ref) || config.ValidBranchName(ref)
}

// jsonContentType reports whether a reply's Content-Type is JSON, which is
// how this API answers a path that names a directory: its listing, not a
// file's bytes.
func jsonContentType(ct string) bool {
	media, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(media), "application/json")
}

// replyExcerpt is what an error quotes of a refused reply: whitespace-trimmed,
// bounded, and escaped to print (pluginwire.PrintableN), the way every value
// that reaches a log is.
func replyExcerpt(body []byte) string {
	return strings.TrimSpace(pluginwire.PrintableN(string(body), maxContentsErrorBody))
}

// secretFree returns err with the token replaced by a marker, for the one
// case a transport error can carry it: a message an intermediary built out of
// the request. An error that does not hold the token is returned as it is, so
// errors.Is and errors.As still see the client's own error type when there is
// nothing to redact.
func secretFree(err error, token string) error {
	if err == nil || token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(secretsFree(err.Error(), token))
}

// secretsFree replaces the token wherever it appears in s. The token is this
// provider's own credential, and a reply that echoes the request back is how
// it would otherwise reach a log through a message about the reply.
func secretsFree(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}
