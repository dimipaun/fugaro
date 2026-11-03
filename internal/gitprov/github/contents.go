package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// contentsFile is one entry of the contents API's reply, a regular file read
// at a ref: base64 content, its size, and the type the caller checks before
// decoding anything.
type contentsFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Size     int    `json:"size"`
	Content  string `json:"content"`
}

var _ gitprov.ContentsReader = (*Provider)(nil)

// Contents implements gitprov.ContentsReader: one file of this repository,
// read through GET /repos/{owner}/{repo}/contents/{path} at ref (D1140,
// PIC3 — a catalog entry's dockerfile source is fetched this way before a
// build, so the build reads a copy rather than a checkout).
//
// The read is this repository's and only its own: the owner, the name and
// the path are the provider's, and the request carries the installation
// token the provider's other API calls carry (scoped to this repository,
// design §6.2; with the credentials service on, the token is the service's,
// C2.2), so no access beyond what the project already holds is used. The
// reply is decoded into the file's bytes and nothing else: anything but a
// regular file (a symlink, a directory, a submodule) is refused rather than
// followed, and so is an answer that is not base64 or is larger than
// gitprov.MaxContentsBytes — a caller may put these bytes into an image's
// build, so what it gets is a bounded regular file or an error.
func (p *Provider) Contents(ctx context.Context, path, ref string) ([]byte, error) {
	if !gitprov.ValidRepoPath(path) {
		return nil, fmt.Errorf("reading %s: the path is not a relative file path inside the repository", path)
	}
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	var f contentsFile
	if err := p.api.Do(ctx, "GET", p.contentsPath(path, q), nil, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	switch {
	case f.Type != "file":
		return nil, fmt.Errorf("reading %s: the repository holds a %s there, not a file", path, pluginwire.Printable(f.Type))
	case f.Encoding != "base64":
		return nil, fmt.Errorf("reading %s: the reply is encoded as %s, not base64", path, pluginwire.Printable(f.Encoding))
	case f.Size <= 0:
		return nil, fmt.Errorf("reading %s: the reply is %d bytes", path, f.Size)
	case f.Size > gitprov.MaxContentsBytes:
		return nil, fmt.Errorf("reading %s: it is %d bytes, more than the %d a definition may be", path, f.Size, gitprov.MaxContentsBytes)
	}
	// GitHub wraps the base64 content at 60 characters with newlines; the
	// decoder reads it either way, and the size above has already bounded
	// what the decode produces.
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("reading %s: decoding the reply: %w", path, err)
	}
	if len(data) == 0 || len(data) > gitprov.MaxContentsBytes {
		return nil, fmt.Errorf("reading %s: the reply decoded to %d bytes", path, len(data))
	}
	return data, nil
}

// contentsPath is the contents API's path for one file: each segment escaped
// on its own, so a segment can never be read as part of the API's own path
// (the file's "a/b" stays two segments, and a segment's "?" or "#" stays in
// the file's name), and the ref as the query parameter GitHub reads it from.
func (p *Provider) contentsPath(path string, q url.Values) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	out := p.repoPath("/contents/" + strings.Join(segs, "/"))
	if len(q) > 0 {
		out += "?" + q.Encode()
	}
	return out
}
