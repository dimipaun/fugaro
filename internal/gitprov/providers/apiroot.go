package providers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

// How a Bitbucket API root is compared, and what a holder of a Bitbucket token
// says when the root it was handed is one it may not use. It is here, beside the
// opener that would honour the variable whatever it named, because every reader
// of FUGARO_BITBUCKET_API_URL has to ask the same question of it and answer with
// the same sentence: the run (internal/runner) and the coordinator's
// project-wide read (ee/coordinator/cli).

// CanonicalAPIRoot is how a Bitbucket API root is compared: scheme and host
// case-folded and one trailing "/" trimmed, the normalization the node's own
// validator uses (projectpolicy.validBitbucketAPIRoots). It is an equality,
// never a prefix test -- a root that merely begins with an allowed one
// (https://api.bitbucket.org.evil.example/2.0) is another host and is refused.
// A value with no scheme or host canonicalizes to "" and matches nothing.
func CanonicalAPIRoot(raw string) string {
	u := parseAPIRoot(raw)
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
}

// APIRootHost is the host of raw, "" when raw is not a URL: it is what a refusal
// names, so that the message carries the host a token would have reached and
// never the whole root.
func APIRootHost(raw string) string {
	u := parseAPIRoot(raw)
	if u == nil {
		return ""
	}
	return u.Host
}

// BitbucketDefaultAPIRoot reports whether raw is Bitbucket's own API: the root
// every Bitbucket token may be sent to, whatever the project's policy says.
func BitbucketDefaultAPIRoot(raw string) bool {
	c := CanonicalAPIRoot(raw)
	return c != "" && c == CanonicalAPIRoot(bitbucket.DefaultBaseURL)
}

// BitbucketAPIRootAllowed reports whether a Bitbucket token may be sent to root:
// Bitbucket's own API, or an exact (canonical) member of allowed. allowed is the
// project's published bitbucket_api_roots, and nil is a project that has
// published none.
func BitbucketAPIRootAllowed(root string, allowed []string) bool {
	if BitbucketDefaultAPIRoot(root) {
		return true
	}
	canonical := CanonicalAPIRoot(root)
	if canonical == "" {
		return false
	}
	for _, a := range allowed {
		if c := CanonicalAPIRoot(a); c != "" && c == canonical {
			return true
		}
	}
	return false
}

// BitbucketAPIRootRefusal is the refusal of an API root that is neither
// Bitbucket's own API nor a root the project's published policy allows. actor
// names who holds the token ("a cloud run", "the coordinator") and remedy names
// that holder again in the clause telling the owner how to keep the override
// without it.
func BitbucketAPIRootRefusal(root, actor, remedy string) error {
	where := "a root with no host"
	if h := APIRootHost(root); h != "" {
		where = "the host " + strconv.Quote(h)
	}
	return fmt.Errorf("%s names %s, which is neither Bitbucket's own API (%s) nor a root this project's published policy allows: %s sends its Bitbucket token to no other root. The owner allows the root by listing it in bitbucket_api_roots and running 'fugaro project policy publish'; until then, unset %s so %s uses Bitbucket's own API",
		EnvBitbucketAPIURL, where, bitbucket.DefaultBaseURL, actor, EnvBitbucketAPIURL, remedy)
}

// parseAPIRoot parses raw, nil when it is not a URL: a value's host is read for
// a refusal's message and its parts compared by CanonicalAPIRoot, and neither is
// done on a string that is not a URL.
func parseAPIRoot(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}
