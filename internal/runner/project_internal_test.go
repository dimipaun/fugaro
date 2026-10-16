package runner

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The provider is derived from the pair the run can trust: the full slug it
// was given (FUGARO_RUN, which keys the locks, the cache and the budget
// session) and the repository task.json names. One job serves one provider,
// and the slug hashes the kind, so exactly one kind can match.
func TestProviderForSlugDerivesEachKind(t *testing.T) {
	for _, kind := range []string{"github", "bitbucket"} {
		t.Run(kind, func(t *testing.T) {
			slug, err := task.Slug(kind, "acme/app")
			if err != nil {
				t.Fatal(err)
			}
			got, ok := providerForSlug(slug, "acme/app")
			if !ok || got != kind {
				t.Fatalf("providerForSlug(%q, %q) = %q, %v; want %q, true", slug, "acme/app", got, ok, kind)
			}
			// Another spelling of the same repository is the same slug, and
			// the same provider kind.
			if got, ok := providerForSlug(slug, "ACME/App.git"); !ok || got != kind {
				t.Fatalf("providerForSlug(%q, %q) = %q, %v; want %q, true", slug, "ACME/App.git", got, ok, kind)
			}
		})
	}
	// The fixture's own storage slug is the readable name alone, without the
	// hash suffix: no kind produces it, so it is not admitted.
	if got, ok := providerForSlug("acme-app", "acme/app"); ok {
		t.Errorf("a slug no kind produces was admitted as %q", got)
	}
}

// The host half of the remote binding: the host a remote names, the provider
// kind that host belongs to, and whether it names a host at all. Three answers,
// not two, and that is the point: a remote naming no host (a local path, the
// empty string, a bare "/") is left to the checks that were always there, while
// a remote naming a host must name the derived kind's — a host no kind owns at
// all (https://git.example.com/..., https://github.com.evil.example/..., and the
// httptest URL the fixture would use if it ever carried an execution) is a
// refusal, not "no disagreement", because the run scopes this repository's git
// credential to the remote's host (the review finding on this change; the first
// shape of this check read KindForURL's "" as "unknown, therefore fine").
func TestRemoteHost(t *testing.T) {
	for _, tc := range []struct {
		remote, host, kind string
		named              bool
	}{
		{"https://github.com/acme/app.git", "github.com", "github", true},
		{"https://bitbucket.org/acme/app.git", "bitbucket.org", "bitbucket", true},
		{"git@bitbucket.org:acme/app.git", "bitbucket.org", "bitbucket", true},
		{"git@github.com:acme/app.git", "github.com", "github", true},
		{"github.com:acme/app.git", "github.com", "github", true},
		// A host no provider kind owns: another forge, a name that only looks
		// like github.com, and a loopback address — a real host either way, so
		// the credential would go to it.
		{"https://git.example.com/acme/app.git", "git.example.com", "", true},
		{"https://github.com.evil.example/acme/app.git", "github.com.evil.example", "", true},
		{"http://127.0.0.1:8080/acme/app.git", "127.0.0.1", "", true},
		// No host: a local path, an empty remote, a bare "/", and a file URL
		// (whose "file:/" is a scheme, not an scp-like host and path).
		{"/tmp/fixture.git", "", "", false},
		{"/srv/git/acme/app", "", "", false},
		{"", "", "", false},
		{"https://github.com/", "github.com", "github", true},
		{"file:///tmp/web.git", "", "", false},
		// A URL git accepts and url.Parse refuses names a host too, so it is
		// the second answer, not the third: gitprov.SplitRemoteRaw reads the
		// host off the authority as written (the text after its last "@", where
		// libcurl ends the userinfo), and no kind owns either of these hosts —
		// the refusal. Skipping them is what the first shape of this check did,
		// and it is fail-open: the host git connects to is the one the run
		// scopes this repository's git credential to (the review finding on
		// this change).
		{"https://github.com\\@evil.example/acme/app.git", "evil.example", "", true},
		{"https://evil.example/acme/app%ZZ.git", "evil.example", "", true},
		{"https://evil.example/acme/app.git#x=%ZZ", "evil.example", "", true},
	} {
		host, kind, named := remoteHost(tc.remote)
		if host != tc.host || kind != tc.kind || named != tc.named {
			t.Errorf("remoteHost(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.remote, host, kind, named, tc.host, tc.kind, tc.named)
		}
	}
}

// The endpoint half of the remote binding: the scheme and the port a run's git
// credential would be scoped to (internal/gitops' CredentialURL is scheme +
// "://" + the authority), read off the remote as git hands it to the
// credential helper rather than as url.Parse reads it. A remote that names no
// URL at all answers false: it names no endpoint for a credential to go to, and
// the shape it does name is gitprov's question, read the same way here as in
// SplitRemoteRaw (the cut at "//", the userinfo ending at the last "@" of the
// authority).
func TestRemoteEndpoint(t *testing.T) {
	for _, tc := range []struct {
		remote, scheme, port string
		endpoint             bool
	}{
		{"https://github.com/acme/app.git", "https", "", true},
		{"https://github.com/", "https", "", true},
		// The two the check exists for: the credential's endpoint is not the
		// launcher's.
		{"http://github.com/acme/app.git", "http", "", true},
		{"https://github.com:8443/acme/app.git", "https", "8443", true},
		{"http://github.com:80/acme/app.git", "http", "80", true},
		// The userinfo is not the host, and it is not the port: what is read
		// off here is what follows the last "@", where git and curl end it.
		{"https://x:token@github.com/acme/app.git", "https", "", true},
		{"https://x:token@github.com:8443/acme/app.git", "https", "8443", true},
		{"https://x-token-auth:t%6fk@github.com/repo.git", "https", "", true},
		// An IPv6 literal's own colons are the address's; a port is what
		// follows the "]".
		{"https://[2001:db8::1]/acme/app.git", "https", "", true},
		{"https://[2001:db8::1]:8443/acme/app.git", "https", "8443", true},
		// The query and the fragment are not the authority, and the path is
		// where the authority ends.
		{"https://github.com/acme/app.git?access_token=x", "https", "", true},
		{"https://github.com:8443", "https", "8443", true},
		{"https://github.com", "https", "", true},
		// No URL: the scp-like form (its colon is git's separator, and what
		// follows is not "//"), a local path, a bare host, an empty remote, a
		// file URL (nothing after its "//"), and a Windows path.
		{"git@github.com:acme/app.git", "", "", false},
		{"github.com:acme/app.git", "", "", false},
		{"/tmp/fixture.git", "", "", false},
		{"", "", "", false},
		{"github.com", "", "", false},
		{"file:///tmp/web.git", "", "", false},
		{"C:\\src\\app", "", "", false},
	} {
		scheme, port, endpoint := remoteEndpoint(tc.remote)
		if scheme != tc.scheme || port != tc.port || endpoint != tc.endpoint {
			t.Errorf("remoteEndpoint(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.remote, scheme, port, endpoint, tc.scheme, tc.port, tc.endpoint)
		}
	}
}

// The owner/name half of the remote binding: the same repository written the
// ways git writes it — .git or not, mixed case, an scp-like remote, a userinfo
// — is one repository, and a remote that names no repository path at all is
// left to the checks that were always there rather than refused here. A remote
// that names a repository is what admissionCheck compares with the task's own.
func TestRepoNamedByRemote(t *testing.T) {
	for _, tc := range []struct {
		remote, want string
		named        bool
	}{
		{"https://github.com/acme/app.git", "acme/app", true},
		{"https://github.com/acme/app", "acme/app", true},
		{"https://github.com/Acme/App.git", "acme/app", true},
		{"git@github.com:acme/app.git", "acme/app", true},
		// The same scp-like remote without the userinfo git makes optional,
		// and its colon-slash spelling: git ssh's to github.com and asks for
		// the same repository either way, so both name one here too (the
		// review finding on this change).
		{"github.com:acme/app.git", "acme/app", true},
		{"github.com:/acme/app.git", "acme/app", true},
		{"github.com:attacker/malware.git", "attacker/malware", true},
		{"ssh://git@github.com/acme/app.git", "acme/app", true},
		{"https://x:token@github.com/acme/app.git", "acme/app", true},
		{"https://github.com/attacker/malware.git", "attacker/malware", true},
		// A GitLab subgroup path is what a task's own Repo may be (repoRE
		// admits more than one "/"), so it is a name this check compares like
		// any other rather than one it refuses.
		{"https://gitlab.example.com/group/sub/app.git", "group/sub/app", true},
		// A remote that names no path: a local path, a bare host, an empty
		// string. Nothing to compare, and the checks that were always there
		// still run.
		{"/tmp/fixture.git", "", false},
		{"/srv/git/acme/app", "", false},
		{"http://127.0.0.1:8080/", "", false},
		{"https://github.com/", "", false},
		{"", "", false},
	} {
		got, named, err := repoNamedByRemote(tc.remote)
		if err != nil {
			t.Errorf("repoNamedByRemote(%q) = error %v, want (%q, %v, nil)", tc.remote, err, tc.want, tc.named)
			continue
		}
		if got != tc.want || named != tc.named {
			t.Errorf("repoNamedByRemote(%q) = (%q, %v), want (%q, %v)", tc.remote, got, named, tc.want, tc.named)
		}
	}
}

// A remote that names a repository path canonicalization refuses is an error,
// never a "nothing to compare": skipping it is what let
// https://github.com/acme/app.git/../../attacker/malware — which git's HTTP
// transport resolves to github.com/attacker/malware — through the owner/name
// binding, and the run cloned it under acme/app's slug. Every path here is one
// git or the server may read differently from this check, so every one of them
// must come back as a refusal (the review finding on this change).
func TestRepoNamedByRemoteRefusesAPathItCannotRead(t *testing.T) {
	for _, remote := range []string{
		// Dot segments: RFC 3986 §5.2.4 removes them, so this is
		// github.com/attacker/malware.
		"https://github.com/acme/app.git/../../attacker/malware",
		"https://github.com/acme/app.git/../../../attacker/malware",
		"https://github.com/acme/../attacker/malware.git",
		"https://github.com/./acme/app.git",
		"https://github.com/acme/app/..",
		"git@github.com:acme/../attacker/malware.git",
		"github.com:acme/../attacker/malware.git",
		"github.com:/acme/../attacker/malware.git",
		"github.com:/acme%2F..%2Fattacker/malware.git",
		// Encoded separators and encoded dots: url.Parse decodes "%2F" into a
		// separator, so reading Path instead of EscapedPath would compare a
		// repository the remote does not plainly name.
		"https://github.com/acme%2Fapp.git",
		"https://github.com/acme%2F..%2Fattacker/malware.git",
		"https://github.com/%2e%2e/attacker/malware.git",
		// An empty segment: two separators where a segment must be.
		"https://github.com//attacker/malware.git",
		"https://github.com/acme//app.git",
		// A segment a repository name cannot hold (task's repoRE): the
		// remote is not an owner/name, whatever the server makes of it.
		"https://github.com/acme/app.git;x",
		"https://github.com/acme/app%20x.git",
		"https://github.com/acme/app.git%00",
		// A URL url.Parse refuses and git does not: the host is this run's own,
		// so the host half of the binding has nothing to refuse, and the path
		// is read off the authority as written for exactly this check. An
		// unreadable path is a refusal here too, never a remote that goes
		// unasked (the review finding on this change).
		"https://github.com/acme/app%ZZ.git",
		"https://github.com\\@evil.example/acme/app%ZZ.git",
	} {
		got, named, err := repoNamedByRemote(remote)
		if err == nil {
			t.Errorf("repoNamedByRemote(%q) = (%q, %v, nil), want a refusal: a path this check cannot read is one git may resolve to another repository", remote, got, named)
			continue
		}
		if named {
			t.Errorf("repoNamedByRemote(%q) reported a name beside its refusal", remote)
		}
	}
}

// The path half on its own: what counts as "the remote names a repository
// path", and what the path is before canonicalization (which is the caller's
// refusal to make). EscapedPath is the source, so an encoded separator stays
// visible instead of decoding into one.
func TestRemoteRepoPath(t *testing.T) {
	for _, tc := range []struct {
		remote, want string
		named        bool
	}{
		{"https://github.com/acme/app.git", "acme/app.git", true},
		{"https://github.com/acme/app.git/", "acme/app.git", true},
		{"https://github.com/acme/app.git/../../attacker/malware", "acme/app.git/../../attacker/malware", true},
		{"https://github.com/acme%2Fapp.git", "acme%2Fapp.git", true},
		{"git@github.com:acme/app.git", "acme/app.git", true},
		{"github.com:acme/app.git", "acme/app.git", true},
		{"github.com:/acme/app.git", "acme/app.git", true},
		{"github.com:acme/app.git/", "acme/app.git", true},
		// Not scp-like: a URL's authority is "//" and a local path has no
		// colon before its first "/", so neither names a host and a path.
		{"file:///tmp/remote.git", "", false},
		{"/tmp/acme:web.git", "", false},
		// A query or a fragment is not part of the path git clones, so a
		// remote carrying one is still the repository its path names.
		{"https://github.com/acme/app.git?ref=x", "acme/app.git", true},
		{"https://github.com/acme/app.git#main", "acme/app.git", true},
		{"https://github.com/", "", false},
		{"https://github.com", "", false},
		{"http://127.0.0.1:8080/", "", false},
		{"/tmp/fixture.git", "", false},
		{"", "", false},
		// A URL url.Parse refuses is read, not skipped: git reads its path the
		// same way, so handing it on is what lets the owner/name check refuse
		// it (https://github.com/acme/app%ZZ.git names no repository this check
		// can compare) instead of the whole remote going unasked (the review
		// finding on this change).
		{"https://github.com/acme/app%ZZ.git", "acme/app%ZZ.git", true},
		{"https://evil.example/acme/app%ZZ.git", "acme/app%ZZ.git", true},
		{"https://github.com\\@evil.example/acme/app.git", "acme/app.git", true},
	} {
		got, named := remoteRepoPath(tc.remote)
		if got != tc.want || named != tc.named {
			t.Errorf("remoteRepoPath(%q) = (%q, %v), want (%q, %v)", tc.remote, got, named, tc.want, tc.named)
		}
	}
}

// A refusal quotes the remote, and a remote URL is where a launcher may have
// put a token: in its userinfo, in a query parameter (?access_token= is how
// Bitbucket's forge URLs are written) or in a fragment. The display form drops
// all three rather than relying on redaction later; addRemoteSecrets is the
// other half, for the paths that quote the remote without going through here.
func TestRemoteDisplayDropsUserinfoQueryAndFragment(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://github.com/acme/app.git", "https://github.com/acme/app.git"},
		{"https://x:glpat-secret@github.com/acme/app.git", "https://github.com/acme/app.git"},
		{"https://x-token-auth@bitbucket.org/acme/app.git", "https://bitbucket.org/acme/app.git"},
		{"git@github.com:acme/app.git", "github.com"},
		{"x-token-auth@bitbucket.org:acme/app.git", "bitbucket.org"},
		// The scp-like form without userinfo is still ssh's host and path, so
		// its display is the host alone like its userinfo-bearing sibling's.
		{"github.com:acme/app.git", "github.com"},
		{"github.com:/acme/app.git", "github.com"},
		// Neither shape: quoted as written.
		{"/tmp/fixture.git", "/tmp/fixture.git"},
		{"file:///tmp/fixture.git", "file:///tmp/fixture.git"},
		{"https://bitbucket.org/acme/app.git?access_token=ATBBsecret", "https://bitbucket.org/acme/app.git"},
		{"https://github.com/acme/app.git#glpat-secret", "https://github.com/acme/app.git"},
		{"https://x:glpat-secret@github.com/acme/app.git?access_token=ATBBsecret#one", "https://github.com/acme/app.git"},
		// A space in the host is a string url.Parse refuses and git never
		// connects with: the authority is still read off it, so the display
		// names it rather than falling back to "the remote" (and the admission
		// refuses it as the unknown host it is).
		{"https://ho st/acme/app.git", "ho st"},
		// A URL url.Parse refuses is quoted as the host git would connect to,
		// which is the text after the authority's last "@": the userinfo ahead
		// of it is a token's place, and a display that quoted the string as
		// written would put it in the refusal.
		{"https://github.com\\@evil.example/acme/app.git", "evil.example"},
		{"https://evil.example/acme/app%ZZ.git", "evil.example"},
		{"https://x:glpat-secret@evil.example\\acme/app.git", "evil.example\\acme"},
		// An escape url.Parse rejects does not make it fail: the query it
		// cannot read is dropped like any other (u.Query() is what would
		// report it, and addRemoteSecrets ignores that).
		{"https://github.com/acme/app.git?access_token=%zz", "https://github.com/acme/app.git"},
		// A remote with no scheme and no host as url.Parse reads it — a plain
		// path — where the "@" is still where git and curl end the userinfo:
		// a launcher that wrote a token in front of a host without a scheme
		// (the shape a hand-written remote takes) must not have it quoted back
		// in a refusal. The scp-like form's own userinfo is already gone (the
		// branches above quote the host alone), and this is the one shape that
		// fell through them.
		{"tok@host/repo.git", "host/repo.git"},
		{"glpat-secret@host/repo.git?access_token=ATBBsecret", "host/repo.git"},
		{"glpat-secret@host", "host"},
		// The userinfo ends at the last "@" of the authority, where git and
		// curl end it, and the authority ends at the first "/": a password
		// that itself holds an "@" is quoted back in full by a display that
		// cut at the first one (the tail of the credential, "en@host/repo.git"
		// here), and an "@" in the path is not the userinfo's to begin with
		// (the review finding on this change). The path stays in the display
		// of a remote written without a scheme, so the last "@" is what this
		// cuts at rather than the host alone.
		{"tok@en@host/repo.git", "host/repo.git"},
		{"tok@en@host", "host"},
		{"https://host/p@x", "https://host/p@x"},
	} {
		got := remoteDisplay(tc.in)
		if got != tc.want {
			t.Errorf("remoteDisplay(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// "en" is the tail of the credential in the "tok@en@host/repo.git"
		// case below: the part a display that cut the userinfo at the first
		// "@" would still quote.
		for _, secret := range []string{"glpat-secret", "ATBBsecret", "tok", "en", "x:glpat-secret"} {
			if strings.Contains(got, secret) {
				t.Errorf("remoteDisplay(%q) = %q quotes %q", tc.in, got, secret)
			}
		}
	}
}

// addRemoteSecrets is what redacts a remote that reaches an error or
// result.json without passing through remoteDisplay — git's own "unable to
// access ..." lines quote the URL it was given. What a remote can carry a
// credential in is registered; what it cannot, and the usernames forges put in
// front of a token (which are not secrets: registering "git" would redact the
// word out of every line the run publishes), is not.
func TestAddRemoteSecretsRegistersWhatARemoteCanCarryACredentialIn(t *testing.T) {
	for _, tc := range []struct {
		remote string
		want   []string
		absent []string
	}{
		{remote: "https://x:glpat-secret@github.com/acme/app.git", want: []string{"glpat-secret", "x:glpat-secret"}, absent: []string{"x"}},
		{remote: "https://ghp_0123456789@github.com/acme/app.git", want: []string{"ghp_0123456789"}},
		// And the spelling the URL was written with: git echoes the URL it was
		// given, escapes and all, so the encoded username is what a refusal
		// would quote back.
		{remote: "https://ghp%40x@github.com/acme/app.git", want: []string{"ghp@x", "ghp%40x"}},
		// Remotes url.Parse refuses, which is not a reason to register
		// nothing: git is handed these verbatim and echoes them back, so the
		// three places are read off the string as written (addRemoteSecretRaw).
		// An scp-like remote with a query (the parse fails on the colon of the
		// path's first segment), a password holding a "/" ("invalid port") and
		// an escape url.Parse will not decode ("invalid URL escape").
		{remote: "git@github.com:acme/app.git?access_token=SECRET", want: []string{"SECRET"}, absent: []string{"git", "acme/app"}},
		{remote: "git@github.com:acme/app.git#glpat-secret", want: []string{"glpat-secret"}, absent: []string{"git"}},
		{remote: "https://user:ghp_a/b@github.com/acme/app.git", want: []string{"ghp_a/b", "user:ghp_a/b"}, absent: []string{"user"}},
		{remote: "https://user:pa%zz@github.com/acme/app.git", want: []string{"pa%zz"}},
		// A password whose escapes url.Parse decodes happily: the parsed path
		// registers the decoded spelling (Go re-encodes the userinfo it read),
		// and git prints the URL it was handed, escapes and all, so the raw
		// spelling is registered too — "%6f" is "o", and a redactor holding
		// only "tok" would leave "t%6fk" in the line verbatim.
		{remote: "https://x-token-auth:t%6fk@github.com/acme/app.git", want: []string{"tok", "t%6fk", "x-token-auth:tok", "x-token-auth:t%6fk"}, absent: []string{"x-token-auth"}},
		// The same credential without a scheme: url.Parse reads the whole
		// remote as a path, so the parsed path registers nothing at all and
		// the userinfo is only there as written.
		{remote: "tok@host/repo.git", want: []string{"tok"}, absent: []string{"host", "repo.git"}},
		// The userinfo is the text between the authority's start and its last
		// "@", as curl and git read it, so a credential holding one is
		// registered whole: cutting at the first "@" registered the head of it
		// ("tok") and left "en" of "tok@en" in every line git writes about the
		// remote (the review finding on this change). The authority ends at
		// the first "/", so an "@" in the path is no authority's: reading
		// https://host/p@x from the first "@" registered "host/p", which is
		// the host and the head of the path, a credential in neither spelling.
		{remote: "tok@en@host/repo.git", want: []string{"tok@en"}, absent: []string{"host", "repo.git"}},
		{remote: "https://host/p@x", absent: []string{"host/p", "p@x"}},
		{remote: "x-token-auth:t%6fk@bitbucket.org/acme/app.git", want: []string{"t%6fk", "x-token-auth:t%6fk"}, absent: []string{"x-token-auth", "acme/app"}},
		{remote: "https://git@github.com/acme/app.git", absent: []string{"git"}},
		{remote: "https://x-access-token@bitbucket.org/acme/app.git", absent: []string{"x-access-token"}},
		{remote: "git@github.com:acme/app.git", absent: []string{"git", "acme/app"}},
		{remote: "https://bitbucket.org/acme/app.git?access_token=ATBBsecret&ref=main", want: []string{"ATBBsecret"}, absent: []string{"main"}},
		{remote: "https://gitlab.com/acme/app.git?private_token=glpat-secret", want: []string{"glpat-secret"}},
		{remote: "https://github.com/acme/app.git?a=1&access_token=ATBBsecret", want: []string{"ATBBsecret"}},
		{remote: "https://github.com/%zz", absent: []string{"%zz"}},
		{remote: "", absent: []string{}},
		// A fragment or query that is a bare token, with no name in front of
		// it: the shape remoteDisplay's test above takes for a credential, and
		// the only thing there is to register is the token itself.
		{remote: "https://github.com/acme/app.git#glpat-secret", want: []string{"glpat-secret"}},
		{remote: "https://github.com/acme/app.git?glpat-secret", want: []string{"glpat-secret"}},
		// And the floor below which nothing is registered, because redacting
		// three characters out of everything the run writes is the larger harm.
		{remote: "https://github.com/acme/app.git#v1", absent: []string{"v1"}},
	} {
		r := &run{}
		r.addRemoteSecrets(tc.remote)
		got := r.secretList()
		for _, w := range tc.want {
			if !slices.Contains(got, w) {
				t.Errorf("addRemoteSecrets(%q) registered %q, want %q among them", tc.remote, got, w)
			}
		}
		for _, a := range tc.absent {
			if slices.Contains(got, a) {
				t.Errorf("addRemoteSecrets(%q) registered %q: %v", tc.remote, a, got)
			}
		}
	}
}

// Zero matches and two matches are both refusals: the first is a run whose
// slug does not belong to the repository it was launched for, the second is
// the programming error the ambiguity branch exists to make visible (a hash
// collision, or a kind added without a domain separator). Neither is admitted
// on a guess, and neither is silently resolved to one kind.
func TestProviderForSlugRefusesNoMatchAndAmbiguity(t *testing.T) {
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	other, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("no match", func(t *testing.T) {
		if got, ok := providerForSlug(other, "acme/other"); ok {
			t.Fatalf("a slug of another repository matched kind %q", got)
		}
		if got, ok := providerForSlug("", "acme/app"); ok {
			t.Fatalf("an empty slug matched kind %q", got)
		}
		if got, ok := providerForSlug(slug, "not a repository"); ok {
			t.Fatalf("an unusable repository matched kind %q", got)
		}
	})

	t.Run("two kinds", func(t *testing.T) {
		// With the real kind set this slug resolves to exactly one kind.
		if got, ok := providerForSlug(slug, "acme/app"); !ok || got != "github" {
			t.Fatalf("providerForSlug(%q, %q) = %q, %v; want github, true", slug, "acme/app", got, ok)
		}
		// Two kinds producing one slug is not something a launcher can do, and
		// it must not be resolved by taking the first: the run stops.
		defer func(prev []string) { jobProviderKinds = prev }(jobProviderKinds)
		jobProviderKinds = []string{"github", "github"}
		if got, ok := providerForSlug(slug, "acme/app"); ok {
			t.Fatalf("a slug two kinds produce was admitted as %q", got)
		}
	})
}

// The admission check's scope is the run's platform, not the presence of an
// execution name: Deps.Local's zero value means the cloud (runner.go), so a
// process whose environment carries no Cloud Run execution and no local
// provenance is the one case a launcher can produce by editing a job — and it
// is the case the check exists for, not an exemption from it. Reading the
// absent execution as "local" let a process no backend claims skip the whole
// binding: the slug↔repository agreement, the remote's host and the policy's
// allow-list, with the git credential still handed to whatever remote the
// environment named.
func TestAdmissionCheckRefusesARunNoBackendClaims(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	defer b.Close()
	slug, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	store := runstore.Open(b, slug, "20260926-221530-abcd")
	newRun := func(d Deps) *run {
		d.Store, d.Bucket = store, blobx.Wrap(b)
		if d.Log == nil {
			d.Log = slog.New(slog.DiscardHandler)
		}
		return &run{d: d, spec: &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main", Task: "Add a feature"}}
	}

	t.Run("in the cloud, no execution", func(t *testing.T) {
		// Deps.Local unset: the cloud. Execution empty: the job environment
		// cannot say which execution this is. The slug is another
		// repository's, so the binding has something to refuse.
		err := newRun(Deps{}).admissionCheck(ctx)
		if err == nil {
			t.Fatal("a run no backend claims passed the admission check")
		}
		if !strings.Contains(err.Error(), "not the slug of the repository") {
			t.Fatalf("err = %v, want the slug/repository refusal", err)
		}
	})

	t.Run("local", func(t *testing.T) {
		// A local invocation is the one platform the binding does not apply
		// to (R2-9): fugaro exec --task-file keeps the path it always had.
		if err := newRun(Deps{Local: true}).admissionCheck(ctx); err != nil {
			t.Fatalf("a local run was refused: %v", err)
		}
	})
}
