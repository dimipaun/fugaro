// Package gitprov abstracts the git hosting provider (design §3.1). The
// github and bitbucket subpackages implement it against the real REST APIs;
// fake is a file-backed stand-in for tests.
package gitprov

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// Provider kinds, as git.provider names them in fugaro.yaml.
const (
	KindGitHub    = "github"
	KindBitbucket = "bitbucket"
)

// PRSpec describes the pull request a run wants.
type PRSpec struct {
	Branch    string   `json:"branch"`
	Base      string   `json:"base"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Draft     bool     `json:"draft"`
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// GitAuth is how git, the runner's and the agent's, authenticates to the
// provider over HTTPS, plus any extra variables the agent's tools need.
type GitAuth struct {
	Username string            // HTTPS username, such as x-token-auth or x-access-token
	Token    string            // HTTPS password; empty means git needs no credentials
	Expires  time.Time         // zero when the token does not expire
	Env      map[string]string // extra agent variables, such as GH_TOKEN
}

// Provider is what the runner needs from the git host.
type Provider interface {
	// EnsurePR creates the pull request for spec.Branch or, if one already
	// exists (the agent may have opened it), updates only its draft state.
	// It is safe to call again after a failure: it finds what an earlier
	// call created. A *PartialError means the pull request exists (the
	// returned PR is valid) but some settings, such as labels or
	// reviewers, could not be applied — including a draft or title state
	// that could not be fully applied in the conservative direction (a PR
	// left looking more like a draft than requested is safe to leave for
	// a retry to heal; a PR left looking more ready than requested is not,
	// so that direction is reported as a plain, retryable error instead).
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
	// GitAuth returns git credentials that stay valid for at least
	// minValid, refreshing them first if needed (design §6.2).
	GitAuth(ctx context.Context, minValid time.Duration) (GitAuth, error)
}

// Opener opens the provider of kind (KindGitHub or KindBitbucket) for repo
// ("owner/name"). It returns the provider and the credential values it
// holds, which the runner adds to its redaction list.
type Opener func(ctx context.Context, kind, repo string) (Provider, []string, error)

// Static returns an Opener that always yields p, whatever the kind.
func Static(p Provider) Opener {
	return func(context.Context, string, string) (Provider, []string, error) { return p, nil, nil }
}

// PartialError reports that EnsurePR produced the pull request but could
// not apply all of its settings, in a direction safe to leave for a retry
// to heal (design §4.2) — for example a draft or title state that still
// looks more like a draft than requested, never more ready than requested.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return "pull request opened, but " + e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// DraftPrefix marks a draft on a repository that cannot make real draft
// pull requests (design §15).
const DraftPrefix = "[DRAFT] "

// DraftTitle returns title with DraftPrefix when draft is true, and without
// it otherwise, so the prefix can be added and removed as a PR's state changes.
func DraftTitle(title string, draft bool) string {
	title = strings.TrimPrefix(title, DraftPrefix)
	if draft {
		return DraftPrefix + title
	}
	return title
}

// KindForURL names the provider hosting a git remote URL: KindGitHub for
// github.com, KindBitbucket for bitbucket.org, and "" for anything else
// (a local path, or another host).
func KindForURL(remote string) string {
	var host string
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		host = u.Hostname()
	} else if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		host, _, _ = strings.Cut(rest, ":") // scp-like: git@host:owner/repo.git
	}
	switch strings.ToLower(host) {
	case "github.com":
		return KindGitHub
	case "bitbucket.org":
		return KindBitbucket
	}
	return ""
}

// SplitRepo splits "owner/name" into its two parts.
func SplitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}
