package providers

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

// The comparison a Bitbucket API root is held to, on its own: it is asked by the
// run and by the coordinator's project-wide read, and the two must not answer
// differently (D474).

// TestCanonicalAPIRootFoldsSchemeHostAndOneTrailingSlash: the normalization the
// node's own validator uses, so that a root is refused or accepted by its host
// and path rather than by how it was spelled.
func TestCanonicalAPIRootFoldsSchemeHostAndOneTrailingSlash(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://api.bitbucket.org/2.0", "https://api.bitbucket.org/2.0"},
		{"HTTPS://API.Bitbucket.ORG/2.0/", "https://api.bitbucket.org/2.0"},
		{"  https://api.bitbucket.org/2.0  ", "https://api.bitbucket.org/2.0"},
		{"https://api.bitbucket.org", "https://api.bitbucket.org"},
		{"api.bitbucket.org/2.0", ""},
		{"", ""},
	} {
		if got := CanonicalAPIRoot(tc.in); got != tc.want {
			t.Errorf("CanonicalAPIRoot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBitbucketAPIRootAllowedIsAnEquality: an allowed root admits itself, the
// spellings of itself the comparison folds, and nothing that merely begins with
// it -- a host that starts with an allowed one is another host, and a root that
// hides its host behind userinfo is the host after the "@".
func TestBitbucketAPIRootAllowedIsAnEquality(t *testing.T) {
	allowed := []string{"https://bitbucket.example.com/api"}
	for _, root := range []string{
		bitbucket.DefaultBaseURL,
		"https://bitbucket.example.com/api",
		"HTTPS://Bitbucket.Example.COM/api/",
	} {
		if !BitbucketAPIRootAllowed(root, allowed) {
			t.Errorf("%q is not allowed, and it names the default root or an allowed one", root)
		}
	}
	for _, root := range []string{
		"https://bitbucket.example.com/apis",
		"https://bitbucket.example.com/api@other.example",
		"https://bitbucket.example.com.evil.example/api",
		"https://api.bitbucket.org.evil.example/2.0",
		"a root with no host",
	} {
		if BitbucketAPIRootAllowed(root, allowed) {
			t.Errorf("%q is allowed, and it names no root the list holds", root)
		}
	}
	if BitbucketAPIRootAllowed("https://bitbucket.example.com/api", nil) {
		t.Error("a project that has published no roots allows one")
	}
}

// TestBitbucketAPIRootRefusalNamesTheHostOnly: the refusal is read by whoever
// sees the failure, and the root it names is a value the environment wrote: the
// message carries the host and never the whole root.
func TestBitbucketAPIRootRefusalNamesTheHostOnly(t *testing.T) {
	const root = "https://bitbucket.example.com/2.0"
	err := BitbucketAPIRootRefusal(root, "a cloud run", "the run")
	for _, want := range []string{EnvBitbucketAPIURL, "bitbucket.example.com", bitbucket.DefaultBaseURL, "a cloud run", "fugaro project policy publish", "so the run uses"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), root) {
		t.Errorf("the refusal prints the whole root rather than its host: %v", err)
	}
	if got := BitbucketAPIRootRefusal("not a url", "the coordinator", "the coordinator").Error(); !strings.Contains(got, "a root with no host") {
		t.Errorf("a value with no host is named as one: %v", got)
	}
}
