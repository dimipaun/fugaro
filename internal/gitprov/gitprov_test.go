package gitprov

import (
	"errors"
	"fmt"
	"testing"
)

func TestDraftTitle(t *testing.T) {
	for _, tc := range []struct {
		in    string
		draft bool
		want  string
	}{
		{"Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", false, "Add x"},
		{"Add x", false, "Add x"},
	} {
		if got := DraftTitle(tc.in, tc.draft); got != tc.want {
			t.Errorf("DraftTitle(%q, %v) = %q, want %q", tc.in, tc.draft, got, tc.want)
		}
	}
}

func TestKindForURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bitbucket.org/acme/web.git":      KindBitbucket,
		"https://x-token-auth@bitbucket.org/a/b":  KindBitbucket,
		"git@bitbucket.org:acme/web.git":          KindBitbucket,
		"https://github.com/acme/web.git":         KindGitHub,
		"ssh://git@GitHub.com/acme/web.git":       KindGitHub,
		"https://github.example.com/acme/web.git": "",
		"http://127.0.0.1:8080/remote.git":        "",
		"/tmp/remote.git":                         "",
		// The scp-like form git reads as ssh, with and without the userinfo
		// git makes optional: both are the host's, and a reader that keyed on
		// "@" saw neither (the review finding on the run's remote binding).
		"github.com:acme/web.git":      KindGitHub,
		"github.com:/acme/web.git":     KindGitHub,
		"bitbucket.org:acme/web.git":   KindBitbucket,
		"git@github.com:/acme/web.git": KindGitHub,
		// Not scp-like: a URL's authority is "//", and a local path has no
		// colon before its first "/".
		"file:///tmp/remote.git": "",
		"/tmp/acme:web.git":      "",
	} {
		if got := KindForURL(in); got != want {
			t.Errorf("KindForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// SplitRemote is the one reader of a git remote's shape, and both halves of the
// run's remote binding (the host kind through KindForURL, the owner/name in
// internal/runner) ask it: the two must not disagree about what a remote names.
// The scp-like form is a colon before the first "/" and not a URL's "//",
// with or without the userinfo git makes optional.
func TestSplitRemote(t *testing.T) {
	for _, tc := range []struct {
		remote, host, path string
		ok                 bool
	}{
		{"https://github.com/acme/web.git", "github.com", "/acme/web.git", true},
		{"ssh://git@github.com/acme/web.git", "github.com", "/acme/web.git", true},
		{"https://github.com/acme%2Fweb.git", "github.com", "/acme%2Fweb.git", true},
		{"github.com:acme/web.git", "github.com", "acme/web.git", true},
		{"github.com:/acme/web.git", "github.com", "/acme/web.git", true},
		{"git@github.com:acme/web.git", "github.com", "acme/web.git", true},
		{"x-token-auth@bitbucket.org:acme/web.git", "bitbucket.org", "acme/web.git", true},
		{"https://github.com/", "github.com", "/", true},
		{"https://github.com", "github.com", "", true},
		// No host: a URL without an authority, a local path, an empty string.
		// "file:///tmp/x" is not scp-like (its colon is followed by "//"), and
		// a local path's colon comes after its first "/".
		{"file:///tmp/web.git", "", "", false},
		// "file:/tmp/web.git" has no authority, so it is read by the scp-like
		// rule and its "host" is "file" — no provider is named that, so the
		// host kind is still "", and the path is compared as it was written
		// rather than dropped. Git does not treat it as a local path either
		// (that needs no colon before the first "/"), and refusing a remote
		// that names another repository is the direction this check may err in.
		{"file:/tmp/web.git", "file", "/tmp/web.git", true},
		{"/tmp/web.git", "", "", false},
		{"/tmp/acme:web.git", "", "", false},
		{"", "", "", false},
	} {
		host, path, ok := SplitRemote(tc.remote)
		if host != tc.host || path != tc.path || ok != tc.ok {
			t.Errorf("SplitRemote(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.remote, host, path, ok, tc.host, tc.path, tc.ok)
		}
	}
}

func TestSplitRepo(t *testing.T) {
	if o, n, ok := SplitRepo("acme/web"); !ok || o != "acme" || n != "web" {
		t.Fatalf("SplitRepo = %q %q %v", o, n, ok)
	}
	for _, bad := range []string{"acme", "/web", "acme/", "a/b/c"} {
		if _, _, ok := SplitRepo(bad); ok {
			t.Errorf("SplitRepo(%q) ok", bad)
		}
	}
}

func TestPartialErrorUnwraps(t *testing.T) {
	inner := errors.New("labels: HTTP 403")
	err := fmt.Errorf("ensure: %w", &PartialError{Err: inner})
	var pe *PartialError
	if !errors.As(err, &pe) || !errors.Is(err, inner) {
		t.Fatalf("err = %v", err)
	}
}
