package image

import (
	"net/url"
	"regexp"
	"strings"
)

// scpLikeRE matches git's scp-like SSH remote syntax, such as
// git@bitbucket.org:team/repo.git, capturing the host and the path.
var scpLikeRE = regexp.MustCompile(`^(?:[^@/:]+@)?([^/:]+):(.+)$`)

// HTTPSOrigin returns the origin URL a derived image's checkout keeps under
// the baked-checkout contract: an https URL on the provider host, with no
// credentials, to which Fugaro's runner adds credentials at run time. SSH
// remotes become https on the same host, and userinfo is dropped. Anything
// else, such as a local path or file:// URL (tests only), is returned
// unchanged.
func HTTPSOrigin(origin string) string {
	// u.Opaque == "" excludes scp-like origins that net/url happily parses
	// as scheme:opaque (such as "bitbucket.org:team/repo.git", where "." is
	// a legal scheme character), so they fall through to scpLikeRE below.
	if u, err := url.Parse(origin); err == nil && u.Scheme != "" && u.Opaque == "" {
		switch u.Scheme {
		case "https", "http", "ssh", "git+ssh", "ssh+git":
			if u.Host != "" {
				return "https://" + u.Hostname() + "/" + strings.TrimPrefix(u.Path, "/")
			}
		}
		return origin
	}
	if m := scpLikeRE.FindStringSubmatch(origin); m != nil {
		return "https://" + m[1] + "/" + strings.TrimPrefix(m[2], "/")
	}
	return origin
}

// originProblem says why origin breaks the baked-checkout contract, or
// returns "" when it doesn't. An absolute path or file:// URL passes because
// tests use local remotes. The message never includes the URL, which could
// hold a token.
func originProblem(origin string) string {
	switch u, err := url.Parse(origin); {
	case origin == "":
		return "the checkout has no origin remote"
	case err == nil && u.Scheme != "":
		switch {
		case (u.Scheme == "https" || u.Scheme == "http") && u.User != nil:
			return "the origin URL embeds credentials"
		case u.Scheme != "https" && u.Scheme != "file":
			return "the origin URL must be https, not " + u.Scheme
		}
		return ""
	case scpLikeRE.MatchString(origin):
		return "the origin URL must be https, not an SSH remote"
	case !strings.HasPrefix(origin, "/"):
		return "the origin must be an https URL"
	}
	return ""
}
