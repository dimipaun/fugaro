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
// existing git credential, and providers.FromEnv wants that credential in the
// environment while an operator's shell holds none of it: for GitHub the App's
// ID, which is the repository's own field in the local config, and the App's
// private key, which is a Secret Manager value; for Bitbucket the access
// token, which is one too. So `fugaro image build <entry>` failed on the first
// entry whose Dockerfile lives in a repository, before anything was billed --
// with "FUGARO_GITHUB_APP_ID is not set" on GitHub and "FUGARO_BITBUCKET_TOKEN
// is not set" on Bitbucket, though every cloud run of the same repository
// already holds that credential. catalogProviderEnv supplies it from where it
// actually is, and keeps the environment as the override.

// catalogKeyRead names the Secret Manager read in the refusal a coding agent's
// session gets, for either provider's credential. `fugaro image build` has no
// flag that allows the read -- it happens as part of building an entry whose
// Dockerfile is in a repository -- so the command itself is what the refusal
// names, rather than borrowing a flag this command does not take.
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

// catalogTokenSecretID is the secret a catalog build reads the Bitbucket
// access token from: what live state resolves for the repository's
// bitbucket-token, checked against the grant its build account actually holds
// -- the identical resolution, by the identical function, that the build this
// command submits is handed as its GitSecretID (runImageBuildCatalog's
// gitSecretID call). The token this command reads the Dockerfile with and the
// one that build clones with are therefore one credential by construction and
// not by two lookups agreeing. Tests replace it.
var catalogTokenSecretID = func(ctx context.Context, lc *localcfg.Config, rs infra.RepoSpec) (string, error) {
	return gitSecretID(ctx, lc, rs, "the catalog build of "+rs.Name)
}

// tokenReadNotice is the one line the catalog build prints before it reads the
// project's bitbucket-token secret with the person's own credentials. It is
// the App key's notice (appKeyReadNotice) for the other provider's credential:
// the value is read once, held in memory, and reaches nothing but the provider
// this command opens with it.
const tokenReadNotice = "reads the project's " + config.BitbucketSecret + " secret with your own credentials and holds it in memory only, to read the entry's Dockerfile out of the repository; it is never printed, logged, or written to disk"

// tokenReadHint is the one sentence a catalog build appends to the provider's
// own "is not set" failure when it could not read the token for the operator
// (issue #1701's Bitbucket half). The provider's text is left exactly as it
// is -- the operator still reads the sentence that names the variable -- and
// this says where the token actually is and what reading it needs, which is
// what the command did not tell them before.
func tokenReadHint(cause error) string {
	switch {
	case errors.Is(cause, gcp.ErrNoVersion):
		return fmt.Sprintf("; the project's %s secret is where it belongs: store the token with %s secrets set %s --repo <owner/name> (or --project-wide), and reading it needs %s on that secret, which a project owner can grant",
			config.BitbucketSecret, selfCommand(), config.BitbucketSecret, gcp.SecretAccessorRole)
	case errors.Is(cause, gcp.ErrNoAccess):
		return fmt.Sprintf("; the project's %s secret holds it, and reading it needs %s on that secret, which a project owner can grant",
			config.BitbucketSecret, gcp.SecretAccessorRole)
	default:
		return fmt.Sprintf("; the project's %s secret holds it, and reading it needs %s on that secret, which a project owner can grant (%s)",
			config.BitbucketSecret, gcp.SecretAccessorRole, oneLineCLI(cause.Error()))
	}
}

// catalogProviderEnv is the environment a catalog build hands OpenProvider for
// the copy it makes of an entry's Dockerfile (issue #1701): the process
// environment, with the credential the repository's provider needs added to it
// when the environment holds none of its own -- the App ID and the App's
// private key for GitHub, the access token for Bitbucket.
//
// The App ID is rs's own: catalogGitHubAppID resolved it for this repository
// (config first, environment only when the config holds none) and infra.Repo
// carried that same answer into the spec, so the credential this command reads
// the Dockerfile with and the one the submitted build clones with cannot
// differ. Either credential stays overridable: an exported one wins and no
// secret is read at all. The read is with the person's own credentials, in
// memory, behind the coding agent's refusal, and it is announced in one line
// before it happens. The value joins the environment as a string, which is the
// lifetime the job side gives the credential it mounts; what readSecretValue
// returned as bytes is cleared, and nothing here prints the value, logs it,
// puts it in an error or writes it to disk. The environment is handed to
// OpenProvider in this process and to no child process: the one thing that
// reads it is the provider this command's own copy uses.
//
// The second answer is the sentence to append to the provider's own "is not
// set" failure, and it is non-empty only where that failure can follow: a
// Bitbucket token the read did not produce. GitHub's own read failures are
// refusals of their own (the App key is not a value the operator can export a
// second way without the command still failing), so they stay errors.
//
// Only an entry whose source is a Dockerfile in a repository opens a provider
// (an inline definition is copied with no repository read at all), so a build
// whose entries are all inline takes the environment as it stands and reads no
// secret. Neither does a GitHub build in an installation whose credentials
// service is on: the key the service holds is the one the provider uses, so no
// key of this command's is read and no notice about reading one is printed.
func catalogProviderEnv(ctx context.Context, w io.Writer, lc *localcfg.Config, rs infra.RepoSpec, entries []config.Entry, getenv func(string) string, stdinTerminal bool) ([]string, string, error) {
	env := os.Environ()
	if !slices.ContainsFunc(entries, func(e config.Entry) bool {
		return e.Source.Kind == config.SourceDockerfile
	}) {
		return env, "", nil
	}
	switch rs.Provider {
	case gitprov.KindGitHub:
		return githubProviderEnv(ctx, w, lc, rs, env, getenv, stdinTerminal)
	case gitprov.KindBitbucket:
		return bitbucketProviderEnv(ctx, w, lc, rs, env, getenv, stdinTerminal)
	}
	return env, "", nil
}

// githubProviderEnv is catalogProviderEnv's GitHub half.
func githubProviderEnv(ctx context.Context, w io.Writer, lc *localcfg.Config, rs infra.RepoSpec, env []string, getenv func(string) string, stdinTerminal bool) ([]string, string, error) {
	// rs.GitHubAppID is the one answer catalogGitHubAppID gave for this
	// repository, which the spec the build is submitted with carries too: the
	// ID is appended after the process environment, so it is the one
	// providers.envVars keeps of the two, and the credential this command
	// reads the Dockerfile with cannot be named differently from the one the
	// build clones with.
	env = append(env, providers.EnvGitHubAppID+"="+rs.GitHubAppID)
	if getenv(providers.EnvGitHubAppKey) != "" || getenv(providers.EnvGitHubAppKeyFile) != "" {
		return env, "", nil
	}
	// With the installation's credentials service on, the App's key is the
	// service's: providers.FromEnv opens the github provider keyless and reads
	// no key at all — not even one the environment holds, because a session
	// that reached for a key it was not given would be a credential the
	// installation meant to withhold (C2.2, D145). The secret below is
	// therefore one this command's own provider would never use, and the notice
	// this function prints before it (appKeyReadNotice: the key is read here,
	// in memory, and GODEBUG=http2debug would print the token it signs) would
	// describe a read this build does not make and a key it never holds. The ID
	// stays: FromEnv's keyless provider is constructed with it, and the spec
	// the build is submitted with carries it either way.
	if getenv(providers.EnvCredentialsServiceURL) != "" {
		return env, "", nil
	}
	if err := refuseKeyReadAt(getenv, stdinTerminal, catalogKeyRead, "the GitHub App's private key"); err != nil {
		return nil, "", err
	}
	secretID, err := catalogAppKeySecretID(ctx, lc, rs)
	if err != nil {
		return nil, "", err
	}
	fmt.Fprintln(w, appKeyReadNotice)
	pem, err := readSecretValue(ctx, lc, secretID)
	switch {
	case errors.Is(err, gcp.ErrNoVersion):
		return nil, "", userErr("the GitHub App's private key is not stored yet: no secret this project resolves for %s has a version, and reading %s's Dockerfile is what the App's own key is for. Store it with %s secrets set %s --repo %s < PATH-TO-THE-PEM-FILE",
			config.GitHubAppKeySecret, pluginwire.Printable(rs.Name), selfCommand(), config.GitHubAppKeySecret, pluginwire.Printable(rs.Name))
	case errors.Is(err, gcp.ErrNoAccess):
		return nil, "", userErr("you may not read secret values (a project owner can), and reading %s's Dockerfile signs with the GitHub App's key", pluginwire.Printable(rs.Name))
	case err != nil:
		return nil, "", userErr("the GitHub App's private key could not be read: %s", oneLineCLI(err.Error()))
	}
	env = append(env, providers.EnvGitHubAppKey+"="+string(pem))
	clear(pem)
	return env, "", nil
}

// bitbucketProviderEnv is catalogProviderEnv's Bitbucket half: the access
// token providers.FromEnv wants in FUGARO_BITBUCKET_TOKEN, from the project's
// own secret when the operator's shell has none -- which is every shell, and
// what made `fugaro image build <entry>` fail on a Bitbucket repository's
// first Dockerfile-sourced entry.
//
// A read that fails is not an error here. The token stays out of the
// environment, so the provider's own open fails with the sentence it has
// always failed with, and the hint says where the token is; failing here
// instead would replace that sentence with a new one, and the operator's next
// step is the same either way.
func bitbucketProviderEnv(ctx context.Context, w io.Writer, lc *localcfg.Config, rs infra.RepoSpec, env []string, getenv func(string) string, stdinTerminal bool) ([]string, string, error) {
	if getenv(providers.EnvBitbucketToken) != "" {
		return env, "", nil
	}
	if err := refuseKeyReadAt(getenv, stdinTerminal, catalogKeyRead, "the Bitbucket access token"); err != nil {
		return nil, "", err
	}
	secretID, err := catalogTokenSecretID(ctx, lc, rs)
	if err != nil {
		return nil, "", err
	}
	fmt.Fprintln(w, tokenReadNotice)
	token, err := readSecretValue(ctx, lc, secretID)
	if err != nil {
		return env, tokenReadHint(err), nil
	}
	env = append(env, providers.EnvBitbucketToken+"="+string(token))
	clear(token)
	return env, "", nil
}
