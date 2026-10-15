package runner

import (
	"slices"
	"strings"
	"testing"

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

// The remote the run clones from is a launcher's to set, so a remote on
// another provider's host — the same owner and name is the interesting case —
// is the one disagreement the run refuses. A remote that names no host this
// Fugaro knows is not a disagreement: the end-to-end and unit test remotes are
// local paths or an httptest URL, self-hosted installations are theirs, and
// any of them is the derived kind's by the launcher's own refusals
// (internal/infra's repoURL).
func TestRemoteOfAnotherKind(t *testing.T) {
	for _, tc := range []struct{ kind, remote, want string }{
		{"bitbucket", "https://github.com/acme/app.git", "github"},
		{"github", "https://bitbucket.org/acme/app.git", "bitbucket"},
		{"github", "git@bitbucket.org:acme/app.git", "bitbucket"},
		{"github", "https://github.com/acme/app.git", ""},
		{"github", "git@github.com:acme/app.git", ""},
		{"github", "/tmp/fixture.git", ""},
		{"github", "/srv/git/acme/app", ""},
		{"github", "http://127.0.0.1:8080/acme/app.git", ""},
		{"github", "https://git.example.com/acme/app.git", ""},
		{"github", "https://github.com.evil.example/acme/app.git", ""},
	} {
		if got := remoteOfAnotherKind(tc.kind, tc.remote); got != tc.want {
			t.Errorf("remoteOfAnotherKind(%q, %q) = %q, want %q", tc.kind, tc.remote, got, tc.want)
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
		{"https://bitbucket.org/acme/app.git?access_token=ATBBsecret", "https://bitbucket.org/acme/app.git"},
		{"https://github.com/acme/app.git#glpat-secret", "https://github.com/acme/app.git"},
		{"https://x:glpat-secret@github.com/acme/app.git?access_token=ATBBsecret#one", "https://github.com/acme/app.git"},
		{"https://ho st/acme/app.git", "the remote"},
		// An escape url.Parse rejects does not make it fail: the query it
		// cannot read is dropped like any other (u.Query() is what would
		// report it, and addRemoteSecrets ignores that).
		{"https://github.com/acme/app.git?access_token=%zz", "https://github.com/acme/app.git"},
	} {
		got := remoteDisplay(tc.in)
		if got != tc.want {
			t.Errorf("remoteDisplay(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, secret := range []string{"glpat-secret", "ATBBsecret"} {
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
