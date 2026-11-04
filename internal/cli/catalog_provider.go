package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The credential a catalog build reads an entry's Dockerfile with (issue
// #1701). The copy of a repository-sourced entry is read through the project's
// existing git credential, which for GitHub is the App: providers.FromEnv
// wants that App's ID and its private key in the environment, and an
// operator's shell has neither -- the ID is the repository's own field in the
// local config and the key is a Secret Manager value -- so `fugaro image
// build <entry>` failed on the first entry whose Dockerfile lives in a GitHub
// repository, before anything was billed. catalogProviderEnv supplies both
// from where they actually are, and keeps the environment as the override.

// catalogKeyRead names the App key's Secret Manager read in the refusal a
// coding agent's session gets. `fugaro image build` has no flag that allows
// the read -- it happens as part of building an entry whose Dockerfile is in a
// GitHub repository -- so the command itself is what the refusal names, rather
// than borrowing a flag this command does not take.
const catalogKeyRead = "fugaro image build"

// catalogGitHubAppID is the GitHub App ID a catalog build of repo is named
// with: the repository's own, which the local config records
// (localcfg.Repo.GitHubAppID, read through the storedAppID the repository
// stage resolves the checkout's own repository with, so a config that names
// the repository another way still answers), else FUGARO_GITHUB_APP_ID.
//
// The configured ID wins over a different value in the environment, and the
// environment is only the fallback when the config holds none. The config is
// what onboarding wrote from the App this repository was set up with, so it is
// the project's own answer; the environment is whatever this shell happens to
// carry, and an exported ID -- another App's, another project's -- must not
// silently point this build, and the Cloud Build clone it submits, at a
// repository the installation never onboarded with that App.
//
// With neither set the refusal names both places, because they are the two
// ways this ID ever gets here: the config key, where the fix is, and the
// variable, where an automation with no local config puts it. The command it
// gives is the one that records an ID for a repository this machine already
// knows -- the same fix internal/cli/init_firebase.go and
// internal/infra/projectjobs.go give for the same missing field -- because
// --github-app-id, which infra.Repo names for the same state, is not a flag
// this command takes.
func catalogGitHubAppID(lc *localcfg.Config, repo string, getenv func(string) string) (string, error) {
	if id := storedAppID(lc, repo); id != "" {
		return id, nil
	}
	if id := getenv(providers.EnvGitHubAppID); id != "" {
		return id, nil
	}
	return "", userErr("%s has no GitHub App ID: record it in the project's local config as repos.%s.github_app_id with %s update --repo %s --github-app-id <id>, or export %s (the App's numeric ID, not a secret)",
		pluginwire.Printable(repo), pluginwire.Printable(repo), selfCommand(), pluginwire.Printable(repo), providers.EnvGitHubAppID)
}

// catalogAppKeySecretID is the secret a catalog build reads the App's private
// key from: what live state resolves for the repository's github-app-key,
// checked against the grant its build account actually holds -- the identical
// resolution and comparison fugaro doctor --check-github-app makes
// (resolveGitSecretID). The repository's own copy is not named
// unconditionally, because once that name is project-scoped the repository's
// own copy has no version at all. Tests replace it.
var catalogAppKeySecretID = func(ctx context.Context, lc *localcfg.Config, rs infra.RepoSpec) (string, error) {
	logical := config.GitHubAppKeySecret
	projectID, _ := infra.ProjectSecretCandidate(lc, logical, rs.Name)
	return resolveGitSecretID(ctx, lc, lc.GCPProject, rs.Name, logical, gcp.SecretID(rs.Slug, logical), projectID,
		buildAccountMember(rs.BuildServiceAccountEmail), "the catalog build of "+rs.Name)
}

// catalogProviderEnv is the environment a catalog build hands OpenProvider for
// the copy it makes of an entry's Dockerfile (issue #1701): the process
// environment, with the App ID and, when the environment holds no key of its
// own, the App's private key added to it.
//
// The App ID is rs's own: catalogGitHubAppID resolved it for this repository
// (config first, environment only when the config holds none) and infra.Repo
// carried that same answer into the spec, so the credential this command reads
// the Dockerfile with and the one the submitted build clones with cannot
// differ. The key stays overridable: an exported one wins and no secret is read
// at all. The key read is the pre-check's own -- the repository's
// github-app-key secret, with the person's own credentials, in memory, behind
// the coding agent's refusal -- and it is announced in the pre-check's own
// line before it happens. The value joins the environment as a string, which
// is the lifetime the job side gives the key it mounts; what readAppKey
// returned as bytes is cleared, and nothing here prints the key, logs it, puts
// it in an error or writes it to disk.
//
// Only an entry whose source is a Dockerfile in a repository opens a provider
// (an inline definition is copied with no repository read at all), so a build
// whose entries are all inline takes the environment as it stands and reads no
// secret.
func catalogProviderEnv(ctx context.Context, w io.Writer, lc *localcfg.Config, rs infra.RepoSpec, entries []config.Entry, getenv func(string) string) ([]string, error) {
	env := os.Environ()
	if rs.Provider != gitprov.KindGitHub || !slices.ContainsFunc(entries, func(e config.Entry) bool {
		return e.Source.Kind == config.SourceDockerfile
	}) {
		return env, nil
	}
	// rs.GitHubAppID is the one answer catalogGitHubAppID gave for this
	// repository, which the spec the build is submitted with carries too: the
	// ID is appended after the process environment, so it is the one
	// providers.envVars keeps of the two, and the credential this command
	// reads the Dockerfile with cannot be named differently from the one the
	// build clones with.
	env = append(env, providers.EnvGitHubAppID+"="+rs.GitHubAppID)
	if getenv(providers.EnvGitHubAppKey) != "" || getenv(providers.EnvGitHubAppKeyFile) != "" {
		return env, nil
	}
	if err := refuseAppKeyReadInAgent(getenv, catalogKeyRead); err != nil {
		return nil, err
	}
	secretID, err := catalogAppKeySecretID(ctx, lc, rs)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(w, appKeyReadNotice)
	pem, err := readAppKey(ctx, lc, secretID)
	switch {
	case errors.Is(err, gcp.ErrNoVersion):
		return nil, userErr("the GitHub App's private key is not stored yet: no secret this project resolves for %s has a version, and reading %s's Dockerfile is what the App's own key is for. Store it with %s secrets set %s --repo %s < PATH-TO-THE-PEM-FILE",
			config.GitHubAppKeySecret, pluginwire.Printable(rs.Name), selfCommand(), config.GitHubAppKeySecret, pluginwire.Printable(rs.Name))
	case errors.Is(err, gcp.ErrNoAccess):
		return nil, userErr("you may not read secret values (a project owner can), and reading %s's Dockerfile signs with the GitHub App's key", pluginwire.Printable(rs.Name))
	case err != nil:
		return nil, userErr("the GitHub App's private key could not be read: %s", oneLineCLI(err.Error()))
	}
	env = append(env, providers.EnvGitHubAppKey+"="+string(pem))
	clear(pem)
	return env, nil
}
