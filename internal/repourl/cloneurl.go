// Package repourl is the one answer to "where does this repository clone
// from": the deployer writes it into a per-repository job's environment
// (internal/infra's FUGARO_REMOTE) and the runner computes it for a run in a
// shared project job (plan task 3.3 step 3), and the two must give the same
// bytes for the same repository.
//
// It is a package of its own rather than a function of internal/gitprov or
// internal/task because it needs both and neither may import the other: the
// provider kinds are internal/gitprov's (KindForURL) and a repository's
// canonical form is internal/task's (CanonicalRepo), while internal/task sits
// above internal/config — whose own test holds config.Providers to
// internal/gitprov's kinds, so internal/gitprov may not import internal/task.
package repourl

import (
	"fmt"
	"net/url"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/task"
)

// CloneURL is the https clone URL of (provider, repo): given when there is
// one, which must be on the provider's own host, carry no credentials and
// name exactly this repository, else the provider's usual one.
//
// One function, two readers, byte for byte the same answer: the deployer,
// which writes the URL into a per-repository job's environment
// (internal/infra's repoURL, FUGARO_REMOTE), and the runner, which computes a
// project job's clone URL from task.json's repository, the provider the job's
// own name pins and the project's policy (plan task 3.3 step 3, design
// §4.1(1)) — FUGARO_REMOTE is a launcher-writable value and a shared job sets
// none. A deploy-time answer the runner would refuse, or the other way, is a
// job that cannot run.
//
// The refusals are the security property, not decoration: a URL naming
// another provider's host, or another repository on the same host, would make
// the run clone, cache, lock and account under its own slug while doing
// another repository's work — which is why internal/infra's own refusal says
// "it must be this repository, not only this provider", and why the run
// refuses a launcher-set remote that disagrees (internal/runner's
// remoteAdmissionRefusal).
func CloneURL(provider, repo, given string) (string, error) {
	if given == "" {
		host := ProviderHost(provider)
		if host == "" {
			return "", fmt.Errorf("git provider %q is not bitbucket or github", provider)
		}
		// The repository is held to its canonical form here too, so the
		// provider's usual URL always names a repository: without it a
		// misspelt or empty repository would be turned into a URL that
		// clones nothing ("https://github.com/.git"), which reads like the
		// provider's own answer. It is only lower-cased and stripped of a
		// trailing .git — the same spelling task.Slug keys everything else by.
		canonical, err := task.CanonicalRepo(repo)
		if err != nil {
			return "", err
		}
		return "https://" + host + "/" + canonical + ".git", nil
	}
	u, err := url.Parse(given)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("the repository URL %s is not an https URL without credentials", redactURL(given))
	}
	if gitprov.KindForURL(given) != provider {
		return "", fmt.Errorf("the repository URL %s is not on %s's host", given, provider)
	}
	// The URL is where the build and the run clone from, so it must be this
	// repository, not only this provider. EscapedPath, not Path: the run's own
	// admission compares the path git will send, which is the escaped one
	// (gitprov.SplitRemote returns EscapedPath for exactly this comparison), so
	// a URL whose path decodes to this repository but is spelled otherwise —
	// https://bitbucket.org/acme/sandb%6fx.git — would be accepted here and
	// refuse every run of the job it deployed. The path's punctuation is
	// trimmed by gitprov.TrimRepoPath, the same function the run's admission
	// asks (internal/runner's remoteRepoPath): trimming every "/" here read
	// https://bitbucket.org//acme/sandbox.git as this repository and wrote it
	// as the job's remote, where the admission's one-per-side trim leaves the
	// empty segment and refuses every run.
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return "", err
	}
	if got, err := task.CanonicalRepo(gitprov.TrimRepoPath(u.EscapedPath())); err != nil || got != want {
		return "", fmt.Errorf("the repository URL %s is not %s", given, repo)
	}
	return given, nil
}

// ProviderHost is the one host a provider kind's repositories are cloned
// from: github.com for github, bitbucket.org for bitbucket, "" for any other
// kind. The runner holds a launcher-set remote to exactly this host (review
// C3 of plan task 3.3), since the git credential is scoped to the remote's
// host and an alias spelling names a repository it cannot verify.
func ProviderHost(kind string) string {
	return map[string]string{gitprov.KindGitHub: "github.com", gitprov.KindBitbucket: "bitbucket.org"}[kind]
}

// redactURL is a URL as a refusal may quote it: without its userinfo, so a
// token embedded in a clone URL is never echoed. internal/backend/gcp's
// RedactURL says the same thing for the Cloud Run side; this package cannot
// import it (gcp imports gitprov).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable URL>"
	}
	u.User = nil
	return u.String()
}
