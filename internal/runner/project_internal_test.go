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
		// A query or a fragment is not part of the path git clones, so a
		// remote carrying one is still the repository its path names.
		{"https://github.com/acme/app.git?ref=x", "acme/app.git", true},
		{"https://github.com/acme/app.git#main", "acme/app.git", true},
		{"https://github.com/", "", false},
		{"https://github.com", "", false},
		{"http://127.0.0.1:8080/", "", false},
		{"/tmp/fixture.git", "", false},
		{"", "", false},
	} {
		got, named := remoteRepoPath(tc.remote)
		if got != tc.want || named != tc.named {
			t.Errorf("remoteRepoPath(%q) = (%q, %v), want (%q, %v)", tc.remote, got, named, tc.want, tc.named)
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
