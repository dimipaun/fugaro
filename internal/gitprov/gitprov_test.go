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
		// A host naming a port is not the provider's host: KindForHost's own
		// doc says so, and SplitRemote keeping the port is what makes the two
		// agree here (the review finding on this change).
		"https://github.com:8443/acme/web.git": "",
		"ssh://git@github.com:22/acme/web.git": "",
		// A URL with an empty authority names no host this reader can read, so
		// it names no provider either — and the caller refuses it rather than
		// skipping the host check, because git reads the host out of the path.
		"https:///github.com/acme/web.git":  "",
		"https:////github.com/acme/web.git": "",
		" https://github.com/acme/web.git":  "",
		// A trailing dot is not folded into the provider's host, and the
		// userinfo's second "@" does not hide the host ssh connects to.
		"https://github.com./acme/web.git": "",
		"a@b@github.com:acme/web.git":      KindGitHub,
		"/tmp/remote.git":                  "",
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
		// The userinfo alone names no host, and neither does a path whose
		// colon is inside it ("/tmp/x:y" is a local path, not a host).
		"@:acme/web.git":      "",
		"git@:acme/web.git":   "",
		"/tmp/x:y/remote.git": "",
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
		// The host is the authority's host as written, so a port stays on it:
		// KindForHost answers "" for "github.com:8443", which is what its own
		// doc promises ("a host with a port") and what the run's binding needs
		// (git connects to that port, and the git credential is scoped to it).
		// Stripping the port read this remote as github.com's, so an https
		// remote off the provider's endpoint passed the host-kind half of the
		// check the endpoint half exists to refuse (the review finding on this
		// change).
		{"https://github.com:8443/acme/web.git", "github.com:8443", "/acme/web.git", true},
		{"ssh://git@github.com:22/acme/web.git", "github.com:22", "/acme/web.git", true},
		// The host is written as it was: an uppercase host is github.com's to
		// KindForHost and to git, and a trailing dot is a host this parser does
		// not fold into github.com (a refusal, never an admission).
		{"https://GitHub.com/acme/web.git", "GitHub.com", "/acme/web.git", true},
		{"git@github.com.:acme/web.git", "github.com.", "acme/web.git", true},
		// The userinfo ends at the LAST "@", where libcurl ends it: ssh
		// connects "a@b@github.com:acme/web.git" to github.com, so cutting at
		// the first "@" read the host as "b@github.com" and refused a remote
		// git accepts (the review finding on this change). A URL's authority
		// with two "@" is refused outright instead, because libcurl does
		// ("Bad hostname") and a host this reader cannot name is not one the
		// run may clone from.
		{"a@b@github.com:acme/web.git", "github.com", "acme/web.git", true},
		{"a@b@github.com:/acme/web.git", "github.com", "/acme/web.git", true},
		{"https://a@b@github.com/acme/web.git", "", "", false},
		// A URL written with an empty authority, and one whose scheme is not a
		// scheme: git reads the first as the host in its path (libcurl connects
		// https:///github.com/acme/web.git to github.com), so "no host" is a
		// dangerous answer to give it — this reader says it cannot read the
		// shape, and the caller refuses rather than skips.
		{"https:///github.com/acme/web.git", "", "", false},
		{"https:////github.com/acme/web.git", "", "", false},
		{" https://github.com/acme/web.git", "", "", false},
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
		// An scp-like host holding a control byte is read *as written* rather
		// than refused here: git never connects to it ("Malformed input to a
		// URL function" is libcurl's answer to each of them), so the run refuses
		// the remote for the host it names — KindForHost answers "" for
		// "github.com\n" — and the caller that quotes the host makes the
		// refusal one line (internal/runner's remoteAdmissionErr, over
		// safetext.OneLine). Refusing it *here* would answer "names no
		// host at all", which is the one answer this check may not give a
		// spelling a refusal has to quote (the review finding on this change).
		{"git@github.com\n:acme/web.git", "github.com\n", "acme/web.git", true},
		{"git@github.com\x1b[31m:acme/web.git", "github.com\x1b[31m", "acme/web.git", true},
		{"git@github.com\u0085:acme/web.git", "github.com\u0085", "acme/web.git", true},
		{"git@github.com\u2028:acme/web.git", "github.com\u2028", "acme/web.git", true},
		{"evil.example\r\nX: y:acme/web.git", "evil.example\r\nX", " y:acme/web.git", true},
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
		// A second "@" in the authority: libcurl ends the userinfo at the last
		// one ("a@b" is the userinfo of github.com) but refuses a two-"@"
		// authority before it gets that far, and a helper asking SplitRemoteRaw
		// for "the host git connects to" must not hand back one git refuses to
		// connect with at all (the review finding on this change). Cutting at
		// the first "@" would have answered "b@github.com", which is why the
		// shared cut is the last one.
		{"https://a@b@github.com/acme/web%ZZ.git", "", "", false},
		{"ssh://a@b@github.com:22/acme/web.git", "", "", false},
		// A scheme that is not a scheme, and a control byte in the authority:
		// "https" here is preceded by a space, so this is not a URL and libcurl
		// says so ("Malformed input"); the second is a host no reader may quote
		// in a one-line refusal. Both are "cannot read it" rather than "names no
		// host" (the review finding on this change).
		{" https://evil.example/acme/web%ZZ.git", "", "", false},
		{"https://evil.example\x00/acme/web%ZZ.git", "", "", false},
		{"https://evil.example\n%ZZ/acme/web.git", "", "", false},
		// The control byte is checked over the whole authority, not the host
		// alone: a credential url git cannot parse is no clone either (git
		// aborts with "credential url cannot be parsed" for
		// https://x\n@github.com/acme/web.git), and this reader exists to name
		// the host git *uses* — a userinfo holding a newline, an ESC or a C1
		// control is a spelling url.Parse refuses (so SplitRemote is no help
		// here) whose host it must not report as a host the run may clone from
		// (the review finding on this change). A space is still read (the
		// display quotes "ho st"), because a space is not a control byte and no
		// spelling of a control byte is what makes this the safer answer.
		{"https://x\n@github.com/acme/web.git", "", "", false},
		{"https://x\x1b[31m@github.com/acme/web.git", "", "", false},
		{"https://x\u0085@github.com/acme/web.git", "", "", false},
		{"https://evil.example\n/acme/web%ZZ.git", "", "", false},
		{"git@github.com\n:acme/web%ZZ.git", "", "", false},
		// A port on the host this reader hands back, and an uppercase host: the
		// kind is the caller's question, and the host is as written either way.
		{"https://github.com:8443/acme/web%ZZ.git", "github.com:8443", "/acme/web%ZZ.git", true},
		{"https://GitHub.com./acme/web%ZZ.git", "GitHub.com.", "/acme/web%ZZ.git", true},
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

// SplitRemoteUserinfo is the one cut of an authority's userinfo, shared by
// SplitRemote, SplitRemoteRaw and internal/runner's remoteSplitUserinfo, so the
// three cannot read one authority two ways (the review finding on this change:
// the first cut at the first "@" and the other two at the last). It answers
// "the userinfo, the host, whether there was a userinfo at all"; an empty
// userinfo before the "@" is one, and the caller decides what a second "@" in
// what it hands back means.
func TestSplitRemoteUserinfo(t *testing.T) {
	for _, tc := range []struct {
		authority, userinfo, host string
		ok                        bool
	}{
		{"github.com", "", "github.com", false},
		{"git@github.com", "git", "github.com", true},
		{"a@b@github.com", "a@b", "github.com", true},
		{"@github.com", "", "github.com", true},
		{"x:tok@github.com:8443", "x:tok", "github.com:8443", true},
		{"@", "", "", true},
		{"", "", "", false},
	} {
		userinfo, host, ok := SplitRemoteUserinfo(tc.authority)
		if userinfo != tc.userinfo || host != tc.host || ok != tc.ok {
			t.Errorf("SplitRemoteUserinfo(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.authority, userinfo, host, ok, tc.userinfo, tc.host, tc.ok)
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
