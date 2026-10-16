package gitprov

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// CloneURL is the https clone URL of (provider, repo): given when there is
// one, which must be on the provider's own host, carry no credentials and
// name exactly this repository, else the provider's usual one.
//
// It lives here rather than in internal/infra because two readers must give
// the same answer byte for byte: the deployer, which writes the URL into a
// per-repository job's environment (internal/infra's repoURL, FUGARO_REMOTE),
// and the runner, which computes a project job's clone URL from task.json's
// repository, the provider the job's own name pins and the project's policy
// (plan task 3.3 step 3, design §4.1(1)) — FUGARO_REMOTE is a launcher-
// writable value and a shared job sets none. A deploy-time answer the runner
// would refuse, or the other way, is a job that cannot run; one function, so
// the two cannot differ.
//
// The refusals are the security property, not decoration: a URL naming
// another provider's host, or another repository on the same host, would make
// the run clone, cache, lock and account under its own slug while doing
// another repository's work — which is why internal/infra's own refusal says
// "it must be this repository, not only this provider", and why the run
// refuses a launcher-set remote that disagrees (internal/runner's
// remoteOfAnotherKind, remoteOfAnotherRepo).
func CloneURL(provider, repo, given string) (string, error) {
	if given == "" {
		host := map[string]string{KindGitHub: "github.com", KindBitbucket: "bitbucket.org"}[provider]
		if host == "" {
			return "", fmt.Errorf("git provider %q is not bitbucket or github", provider)
		}
		return "https://" + host + "/" + repo + ".git", nil
	}
	u, err := url.Parse(given)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("the repository URL %s is not an https URL without credentials", redactURL(given))
	}
	if KindForURL(given) != provider {
		return "", fmt.Errorf("the repository URL %s is not on %s's host", given, provider)
	}
	// The URL is where the build and the run clone from, so it must be this
	// repository, not only this provider.
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return "", err
	}
	if got, err := task.CanonicalRepo(strings.Trim(u.Path, "/")); err != nil || got != want {
		return "", fmt.Errorf("the repository URL %s is not %s", given, repo)
	}
	return given, nil
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
