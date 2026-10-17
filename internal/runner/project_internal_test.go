package runner

import (
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

// The host a remote names is the whole of "this provider" (review C3), and
// the scp-like form is a way of naming a host that a check keyed on the "@"
// misses: git ssh's to evil.example for "evil.example:acme/app.git" as it does
// for "git@evil.example:acme/app.git", and the run would have read the first
// as a remote that names no host at all — which is the answer that admits it.
// The userinfo is cut at the last "@", where git ends it, and the userinfo
// alone is no host, which is a local path's answer: no host, no credential.
func TestRemoteOffTheKindsHost(t *testing.T) {
	for _, tc := range []struct{ kind, remote, want string }{
		{"github", "https://github.com/acme/app.git", ""},
		{"github", "git@github.com:acme/app.git", ""},
		{"github", "github.com:acme/app.git", ""},
		{"github", "evil.example:acme/app.git", "evil.example"},
		{"github", "git@evil.example:acme/app.git", "evil.example"},
		{"github", "a@b@evil.example:acme/app.git", "evil.example"},
		{"github", "https://github.com:8443/acme/app.git", "github.com:8443"},
		{"github", "https://github.com./evil/other.git", "github.com."},
		{"github", "https://www.github.com/evil/other.git", "www.github.com"},
		{"github", "@:acme/app.git", ""},
		{"github", "git@:acme/app.git", ""},
		{"github", "/tmp/fixture.git", ""},
		{"github", "file:///srv/git/acme/app.git", ""},
	} {
		if got := remoteOffTheKindsHost(tc.kind, tc.remote); got != tc.want {
			t.Errorf("remoteOffTheKindsHost(%q, %q) = %q, want %q", tc.kind, tc.remote, got, tc.want)
		}
	}
}

// The kind is not the whole question (review finding): a provider's host
// carries every repository of that provider, so a remote that agrees with the
// derived kind may still be another repository's. internal/infra's repoURL
// refuses exactly that when it builds the job ("it must be this repository,
// not only this provider"), and the run refuses it for the same reason: the
// slug keys its locks, its cache and its budget session.
//
// A remote that names no repository path — a local path, an httptest URL, a
// host no Fugaro knows, the root of a known host — is not a disagreement, as
// it is not for remoteOfAnotherKind, and another provider's host stays that
// function's business rather than being reported twice.
func TestRemoteOfAnotherRepo(t *testing.T) {
	for _, tc := range []struct{ kind, repo, remote, want string }{
		{"github", "acme/app", "https://github.com/acme/app.git", ""},
		{"github", "acme/app", "git@github.com:acme/app.git", ""},
		{"github", "acme/app", "https://github.com/ACME/App.GIT", ""}, // canonical: case, ".git"
		{"github", "acme/app", "https://github.com/evil/other.git", "evil/other"},
		{"github", "acme/app", "https://github.com/acme/other.git", "acme/other"},
		{"github", "acme/app", "https://github.com/evil/other", "evil/other"},
		{"github", "acme/app", "git@github.com:evil/other.git", "evil/other"},
		// The same scp-like remote with no user: git ssh's to github.com for
		// it too, so it is the provider's and its path is compared.
		{"github", "acme/app", "github.com:acme/app.git", ""},
		{"github", "acme/app", "github.com:evil/other.git", "evil/other"},
		{"github", "acme/app", "a@b@github.com:evil/other.git", "evil/other"},
		{"github", "acme/app", "https://github.com/evil/other/tree/main", "evil/other/tree/main"},
		{"bitbucket", "group/sub/repo", "https://bitbucket.org/group/sub/repo.git", ""},
		{"bitbucket", "group/sub/repo", "https://bitbucket.org/group/sub/other.git", "group/sub/other"},
		{"github", "acme/app", "https://bitbucket.org/evil/other.git", ""},
		{"github", "acme/app", "/tmp/fixture.git", ""},
		{"github", "acme/app", "http://127.0.0.1:8080/acme/app.git", ""},
		{"github", "acme/app", "https://github.com.evil.example/evil/other.git", ""},
		{"github", "acme/app", "https://github.com", ""},
		{"github", "acme/app", "https://github.com/", ""},
		{"", "acme/app", "https://github.com/evil/other.git", ""},
		{"github", "not a repository", "https://github.com/evil/other.git", ""}, // the slug check refuses it
	} {
		if got := remoteOfAnotherRepo(tc.kind, tc.repo, tc.remote); got != tc.want {
			t.Errorf("remoteOfAnotherRepo(%q, %q, %q) = %q, want %q", tc.kind, tc.repo, tc.remote, got, tc.want)
		}
	}
}

// remoteRepoPath is what remoteOfAnotherRepo compares, so it is checked on its
// own: the path of a URL, what follows the colon of an scp-like remote, and
// nothing at all for a remote that carries no repository path.
func TestRemoteRepoPath(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://github.com/acme/app.git", "acme/app.git", true},
		{"https://x:glpat-secret@github.com/acme/app.git", "acme/app.git", true},
		{"git@github.com:acme/app.git", "acme/app.git", true},
		{"x-token-auth@bitbucket.org:acme/app.git", "acme/app.git", true},
		// The scp-like form with no user is one too, and the userinfo is cut
		// where git ends it: the host is github.com, not "b@github.com".
		{"github.com:acme/app.git", "acme/app.git", true},
		{"a@b@github.com:acme/app.git", "acme/app.git", true},
		// The userinfo alone is no host, so there is no repository path either.
		{"@:acme/app.git", "", false},
		{"git@:acme/app.git", "", false},
		{"ssh://git@github.com/acme/app.git", "acme/app.git", true},
		{"https://github.com/", "", false},
		{"https://github.com", "", false},
		{"/srv/git/acme/app", "", false},
		{"file:///srv/git/acme/app.git", "", false},
		{"", "", false},
	} {
		got, ok := remoteRepoPath(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("remoteRepoPath(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// A refusal quotes the remote, and a remote URL is where a launcher may have
// put a token: the run never registers that one as a secret, so the display
// form drops the userinfo instead of relying on redaction later.
func TestRemoteDisplayDropsUserinfo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://github.com/acme/app.git", "https://github.com/acme/app.git"},
		{"https://x:glpat-secret@github.com/acme/app.git", "https://github.com/acme/app.git"},
		{"https://x-token-auth@bitbucket.org/acme/app.git", "https://bitbucket.org/acme/app.git"},
		{"git@github.com:acme/app.git", "github.com"},
		{"x-token-auth@bitbucket.org:acme/app.git", "bitbucket.org"},
		{"github.com:acme/app.git", "github.com"},
		{"a@b@github.com:acme/app.git", "github.com"},
		// Two colons are git's own reading and not a URL's: the first one ends
		// the host, so this is ssh to the host "x" and the rest is its path —
		// the token is not in the userinfo this function drops, and it is not
		// quoted either, because the host alone is what comes back.
		{"x:glpat-secret@github.com:acme/app.git", "x"},
	} {
		if got := remoteDisplay(tc.in); got != tc.want {
			t.Errorf("remoteDisplay(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(remoteDisplay(tc.in), "glpat-secret") {
			t.Errorf("remoteDisplay(%q) quotes the userinfo", tc.in)
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
