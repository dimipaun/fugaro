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
		// The userinfo is optional and may be the whole of what is in front
		// of the colon: "@:path" and "git@:path" name no host at all, and
		// "no host" is the answer here — the same one a URL without an
		// authority gets, which is what this function's doc promises (the
		// review finding on this change).
		{"@:web.git", "", "", false},
		{"git@:web.git", "", "", false},
		{"", "", "", false},
	} {
		host, path, ok := SplitRemote(tc.remote)
		if host != tc.host || path != tc.path || ok != tc.ok {
			t.Errorf("SplitRemote(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.remote, host, path, ok, tc.host, tc.path, tc.ok)
		}
	}
}

// SplitRemote's "no host" and "a host this parser cannot read" are two
// answers, and git accepts spellings url.Parse refuses: it connects to the host
// in the authority all the same (libcurl reads a backslash before the "@" as
// the end of the userinfo, and a percent escape it will not decode fails no
// clone). SplitRemoteRaw is the reader of that shape, and the host it returns
// is the one git would connect to — the text after the authority's last "@" —
// so a caller that bounds what a remote may be refuses it instead of reading
// "ok = false" as nothing to check.
func TestSplitRemoteRaw(t *testing.T) {
	for _, tc := range []struct {
		remote, host, path string
		ok                 bool
	}{
		// A backslash before the "@": url.Parse refuses the userinfo, and
		// libcurl reads the host that follows the "@".
		{"https://github.com\\@evil.example/acme/web.git", "evil.example", "/acme/web.git", true},
		// An escape url.Parse will not decode: the host is github.com's and the
		// path is what it could not read, so the path check is the one that
		// refuses this remote.
		{"https://github.com/acme/web%ZZ.git", "github.com", "/acme/web%ZZ.git", true},
		{"https://evil.example/acme/web%ZZ.git", "evil.example", "/acme/web%ZZ.git", true},
		// A credential's userinfo goes with the host: what comes back is the
		// host alone, so a refusal quoting it quotes no token.
		{"https://x:tok@evil.example\\acme/web.git", "evil.example\\acme", "/web.git", true},
		// A "?" opens the path's end: what follows it is not the path, and the
		// parse that failed is the reason this reader exists at all.
		{"https://evil.example/acme/web%ZZ.git?x=%ZZ", "evil.example", "/acme/web%ZZ.git", true},
		// Everything SplitRemote already answers for, and an authority with
		// nothing in it: this reader's question is the failed parse alone.
		{"https://github.com/acme/web.git", "", "", false},
		{"ssh://git@github.com/acme/web.git", "", "", false},
		{"github.com:acme/web.git", "", "", false},
		{"git@github.com:acme/web.git", "", "", false},
		{"file:/tmp/web.git", "", "", false},
		{"/tmp/web.git", "", "", false},
		{"https://?x", "", "", false}, // no host in the authority at all
		{"https://evil.example#x=%ZZ", "evil.example", "", true},
		{"", "", "", false},
	} {
		host, path, ok := SplitRemoteRaw(tc.remote)
		if host != tc.host || path != tc.path || ok != tc.ok {
			t.Errorf("SplitRemoteRaw(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.remote, host, path, ok, tc.host, tc.path, tc.ok)
		}
	}
}

// KindForHost is the half of KindForURL that reads a host on its own, for the
// caller holding one SplitRemoteRaw read: the two must agree about which hosts
// are a provider's, and anything else — another host, a host with a port, a
// spelling that is not a host at all — is "" rather than a guess.
func TestKindForHost(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"github.com", KindGitHub},
		{"GitHub.com", KindGitHub},
		{"bitbucket.org", KindBitbucket},
		{"evil.example", ""},
		{"github.com.evil.example", ""},
		{"github.com:443", ""},
		{"github.com\\acme", ""},
		{"", ""},
	} {
		if got := KindForHost(tc.host); got != tc.want {
			t.Errorf("KindForHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
	for _, remote := range []string{"https://github.com/acme/web.git", "git@bitbucket.org:acme/web.git", "/tmp/web.git"} {
		host, _, _ := SplitRemote(remote)
		if got, want := KindForURL(remote), KindForHost(host); got != want {
			t.Errorf("KindForURL(%q) = %q but KindForHost(%q) = %q: the two readers of a host must agree", remote, got, host, want)
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
