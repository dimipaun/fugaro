package infra

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"slices"

	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The GitHub App's installed repositories (plan task 4.1; design §4.3's third
// tie between a repository and a project: "provider-side access: the GitHub
// App installed on the repository"). It lives beside the installation
// discovery because it is the same kind of answer — what the provider side
// already holds — read with the App's own credentials rather than with the
// project's Google ones.

// GitHubApp is the project's GitHub App: what the App API is asked with.
type GitHubApp struct {
	AppID      string
	PrivateKey *rsa.PrivateKey
	// BaseURL is the REST API root; "" means github.com's.
	BaseURL string
	// HTTP is the client the calls go through; nil means the default one.
	HTTP *http.Client
}

// InstalledRepos lists the repositories the App is installed on as canonical
// names ("github/acme/app"), sorted, deduplicated.
//
// Every name is put through the same canonicalisation the allow-list itself
// uses, and a name that is not a repository name is refused: the only reader
// of this answer offers these names as allow-list entries, and an entry the
// publisher would refuse is a repository an operator would believe enrolled.
func InstalledRepos(ctx context.Context, app GitHubApp) ([]string, error) {
	names, err := github.AppRepositories(ctx, github.Options{AppID: app.AppID, PrivateKey: app.PrivateKey, BaseURL: app.BaseURL, HTTP: app.HTTP})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		c, err := projectpolicy.Canonical(providerGitHub, name)
		if err != nil {
			return nil, fmt.Errorf("github returned %q, which is not a repository name: %w", safetext.Field(name), err)
		}
		out = append(out, c)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// providerGitHub is the provider kind the App's repositories carry. The App is
// GitHub's own, so every name it answers with is a github repository; the
// kind is what makes the name an allow-list entry.
const providerGitHub = "github"
