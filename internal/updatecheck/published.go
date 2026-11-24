package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReleasesEndpoint is Repo's releases collection, the endpoint Published
// asks about one tag. Endpoint above is the "latest release" one and is not
// this: that question ("is there something newer") and this one ("is my own
// release published") have different answers, and an older-line hotfix is
// the case that tells them apart.
const ReleasesEndpoint = "https://api.github.com/repos/" + Repo + "/releases"

// Published reports whether version is a published release of Repo: the tag
// v<version> exists and its release is neither a draft nor a pre-release
// (D3269). It is a question about that one tag, never a comparison with the
// newest release, so a 0.27.5 hotfix published after 0.28.0 is published.
//
// It ignores everything that switches the daily check off: FUGARO_NO_UPDATE_CHECK,
// a project config's update_check: false (a gate internal/cli applies, and
// one this package never reads) and the update-check cache. An `update`
// must know whether its own release is published whatever the user asked to
// be nagged about, and a cache of the newest release answers a different
// question.
//
// It is the one read in this package that reports failure, because its
// caller writes something on a true: a network failure, a body that is
// oversized or is not a release object, an answer about a different tag, or
// any status other than 200 or 404 is an error. 404 is not one — it is
// GitHub's answer for a tag that names no published release (an unknown
// tag, and a draft to a caller without push access), which is exactly the
// question. A caller that gets an error leaves the stamp it would have
// written alone and says so.
//
// The tag is never followed to another host: Published requires a 200 and
// the client refuses redirects (refuseRedirect, internal/cli), so a 3xx is
// an unexpected status rather than something read through. The response is
// read bounded (maxBody). ctx bounds the request; Published wraps it with
// FetchTimeout as Refresh does, so a blackholed network cannot hang a
// caller that passes a bare context.
func Published(ctx context.Context, client Client, endpoint, version string) (bool, error) {
	tag, ok := CanonicalRelease(version)
	if !ok {
		return false, fmt.Errorf("checking whether fugaro %q is a published release: it is not an X.Y.Z release, so it has no release tag", version)
	}
	cctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, tagURL(endpoint, tag), nil)
	if err != nil {
		return false, fmt.Errorf("checking whether fugaro %s is a published release: %w", tag, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fugaro-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("checking whether fugaro %s is a published release: %w", tag, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return false, nil
	case http.StatusOK:
	default:
		// The status code alone, never resp.Status: the reason phrase is
		// whatever the responder wrote.
		return false, fmt.Errorf("checking whether fugaro %s is a published release: the releases API answered %d", tag, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return false, fmt.Errorf("checking whether fugaro %s is a published release: the response was not readable, or was over %d bytes", tag, maxBody)
	}
	var r release
	if json.Unmarshal(data, &r) != nil {
		return false, fmt.Errorf("checking whether fugaro %s is a published release: the response was not a release object", tag)
	}
	// The answer must be about the tag that was asked for. The endpoint
	// returns one release per tag, so a different tag_name is a responder
	// answering something else, and the one thing this caller must never do
	// is write a stamp on the strength of it. The tag itself is not quoted
	// back: it comes from the responder.
	if got, ok := CanonicalRelease(r.TagName); !ok || got != tag {
		return false, fmt.Errorf("checking whether fugaro %s is a published release: the answer named a different tag", tag)
	}
	return !r.Draft && !r.Prerelease, nil
}

// tagURL is endpoint plus GitHub's by-tag path for tag: the collection's own
// URL with one release's tag appended, so Published asks for that release
// alone rather than paging the whole list and picking through it.
func tagURL(endpoint, tag string) string {
	return strings.TrimSuffix(endpoint, "/") + "/tags/v" + tag
}
