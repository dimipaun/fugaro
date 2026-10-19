package repourl_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/repourl"
)

// The deployer writes this answer into a per-repository job's environment
// (internal/infra's repoURL, FUGARO_REMOTE) and the runner computes a project
// job's clone URL with the same call (plan task 3.3 step 3), so what it accepts
// is what every run clones from. The tests below are the two halves of the
// property the security review reads: a repository with no URL of its own
// clones from its provider's usual host, and a URL that is given must be this
// repository on this provider's host with no credentials in it.

// A given URL that is this repository, spelled the way operators and deploy
// tooling spell it: with or without the .git suffix, in any case. The answer is
// the URL as given (byte for byte), not a re-rendering of it — the deployer
// wrote it into the environment and a run must clone from exactly that.
func TestCloneURLAcceptsThisRepository(t *testing.T) {
	for _, tc := range []struct{ name, provider, repo, given, want string }{
		{"github, canonical", "github", "acme/app", "https://github.com/acme/app.git", "https://github.com/acme/app.git"},
		{"github, no suffix", "github", "acme/app", "https://github.com/acme/app", "https://github.com/acme/app"},
		{"github, mixed case", "github", "acme/app", "https://github.com/ACME/App.git", "https://github.com/ACME/App.git"},
		{"github, trailing slash", "github", "acme/app", "https://github.com/acme/app/", "https://github.com/acme/app/"},
		{"bitbucket", "bitbucket", "acme/app", "https://bitbucket.org/acme/app.git", "https://bitbucket.org/acme/app.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.CloneURL(tc.provider, tc.repo, tc.given)
			if err != nil {
				t.Fatalf("CloneURL(%q, %q, %q) = %v, want it accepted", tc.provider, tc.repo, tc.given, err)
			}
			if got != tc.want {
				t.Errorf("CloneURL(%q, %q, %q) = %q, want %q", tc.provider, tc.repo, tc.given, got, tc.want)
			}
		})
	}
}

// With no URL of its own, a repository clones from its provider's usual host,
// built from the repository as given. That is what every repository does today
// and what a policy entry is an exception to.
func TestCloneURLWithoutAGivenURL(t *testing.T) {
	for _, tc := range []struct{ provider, repo, want string }{
		{"github", "acme/app", "https://github.com/acme/app.git"},
		{"bitbucket", "acme/app", "https://bitbucket.org/acme/app.git"},
		// The repository is canonicalized (lower-cased, .git stripped), so the
		// usual URL names the repository everything else keys by.
		{"github", "ACME/App", "https://github.com/acme/app.git"},
		{"github", "acme/app.git", "https://github.com/acme/app.git"},
		// And the URL is not doubled into "...git.git".
		{"bitbucket", "ACME/App.git", "https://bitbucket.org/acme/app.git"},
	} {
		got, err := repourl.CloneURL(tc.provider, tc.repo, "")
		if err != nil {
			t.Fatalf("CloneURL(%q, %q, \"\") = %v, want it accepted", tc.provider, tc.repo, err)
		}
		if got != tc.want {
			t.Errorf("CloneURL(%q, %q, \"\") = %q, want %q", tc.provider, tc.repo, got, tc.want)
		}
	}
}

// Every refusal is the security property. A URL that names another repository,
// or another provider's host, is a run that would clone, cache, lock and
// account under its own slug while doing another repository's work — the very
// thing internal/runner's own remote checks refuse when a launcher writes
// FUGARO_REMOTE (remoteOfAnotherKind, remoteOfAnotherRepo). A URL carrying
// credentials is refused because the URL is data on the policy node, which
// every run token can read (P2), and because a credential belongs in a mounted
// secret, never in a value the diff and `policy show` print.
func TestCloneURLRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, provider, repo, given, want string
	}{
		{"http", "github", "acme/app", "http://github.com/acme/app.git", "https URL without credentials"},
		{"no scheme", "github", "acme/app", "github.com/acme/app.git", "https URL without credentials"},
		{"ssh", "github", "acme/app", "ssh://git@github.com/acme/app.git", "https URL without credentials"},
		{"userinfo", "github", "acme/app", "https://user:tok@github.com/acme/app.git", "https URL without credentials"},
		{"token as userinfo", "github", "acme/app", "https://x-access-token:ghs_secret@github.com/acme/app.git", "https URL without credentials"},
		{"port", "github", "acme/app", "https://github.com:443/acme/app.git", "https URL without credentials"},
		{"query", "github", "acme/app", "https://github.com/acme/app.git?token=x", "https URL without credentials"},
		{"fragment", "github", "acme/app", "https://github.com/acme/app.git#main", "https URL without credentials"},
		{"empty host", "github", "acme/app", "https:///acme/app.git", "https URL without credentials"},
		{"unparseable", "github", "acme/app", "https://github.com/%zz", "https URL without credentials"},
		{"another repository", "github", "acme/app", "https://github.com/acme/other.git", "acme/other"},
		{"another owner", "github", "acme/app", "https://github.com/evil/app.git", "evil/app"},
		{"the same name under another owner's path", "github", "acme/app", "https://github.com/acme/evil/app.git", "acme/evil/app"},
		{"a repository prefix", "github", "acme/app", "https://github.com/acme/app-other.git", "acme/app-other"},
		{"another provider's host", "bitbucket", "acme/app", "https://github.com/acme/app.git", "not on bitbucket's host"},
		{"github against bitbucket", "github", "acme/app", "https://bitbucket.org/acme/app.git", "not on github's host"},
		{"unknown host", "github", "acme/app", "https://gitlab.com/acme/app.git", "not on github's host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.CloneURL(tc.provider, tc.repo, tc.given)
			if err == nil {
				t.Fatalf("CloneURL(%q, %q, %q) = %q, want a refusal", tc.provider, tc.repo, tc.given, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("CloneURL(%q, %q, %q) = %v, want it to name %q", tc.provider, tc.repo, tc.given, err, tc.want)
			}
			// The refusal must not echo a credential it refused for carrying
			// one: the message is written to a job's log and to a terminal.
			if strings.Contains(err.Error(), "ghs_secret") {
				t.Errorf("the refusal echoes the URL's credentials: %v", err)
			}
		})
	}
}

// A provider this deployment does not serve, and a repository that is not one:
// neither has a usual host or a canonical form, so both are refused rather than
// turned into a URL that would clone nothing.
func TestCloneURLRefusesAnUnknownProviderOrRepository(t *testing.T) {
	for _, tc := range []struct{ name, provider, repo, want string }{
		{"unknown provider", "gitlab", "acme/app", "not bitbucket or github"},
		{"empty provider", "", "acme/app", "not bitbucket or github"},
		{"repository with no owner", "github", "app", "owner/name"},
		{"repository with a trailing slash", "github", "acme/app/", "owner/name"},
		{"repository reaching past its owner", "github", "acme/../app", "owner/name"},
		{"empty repository", "github", "", "owner/name"},
		{"repository escaping its owner", "github", "../app", "owner/name"},
		{"repository with a dot segment", "github", "acme/.", "owner/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.CloneURL(tc.provider, tc.repo, "")
			if err == nil {
				t.Fatalf("CloneURL(%q, %q, \"\") = %q, want a refusal", tc.provider, tc.repo, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("CloneURL(%q, %q, \"\") = %v, want it to name %q", tc.provider, tc.repo, err, tc.want)
			}
		})
	}
}
