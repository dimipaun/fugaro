// Package gitprov abstracts the git hosting provider (design §3.1). The
// github and bitbucket subpackages implement it against the real REST APIs;
// fake is a file-backed stand-in for tests.
package gitprov

import (
	"context"
	"errors"
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
	// Number, when non-zero, makes EnsurePR update pull request Number's
	// draft state only: it never creates one, and never touches the title
	// or body beyond the draft prefix. It returns ErrPRNotOpen (wrapped,
	// with the PR) when that PR isn't open, or its source branch isn't
	// Branch.
	Number int    `json:"number,omitempty"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
	// Labels and Reviewers are applied only to a pull request EnsurePR
	// creates with Draft false; a draft carries neither, and ApplyReady
	// is how a PR that became ready gets them.
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
	// DraftFallback is set when Draft is true only because the title
	// carries DraftPrefix: the host refused real draft pull requests, so
	// the PR is an ordinary one that merely looks like a draft (and the
	// host may already have requested reviews, such as CODEOWNERS).
	DraftFallback bool `json:"draft_fallback,omitempty"`
}

// PRUpdate is a change to an existing pull request by UpdatePR; a nil
// field is left alone.
type PRUpdate struct {
	Title *string
	Body  *string
	// Draft, when set, also moves the PR to that draft state (the same
	// semantics as EnsurePR by number, including the DraftPrefix fallback).
	Draft *bool
}

// ErrPRNotOpen means an update by number found the pull request merged
// or closed, or on another source branch: a follow-up must never bring
// back or reuse a pull request that isn't open on its branch.
var ErrPRNotOpen = errors.New("pull request is not open on this branch")

// RepoInfo is what a follow-up needs to know about the repository.
type RepoInfo struct {
	Private bool
}

// PRState is the lifecycle state of a pull request.
type PRState string

const (
	PROpen   PRState = "open"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed" // closed unmerged: declined or superseded on Bitbucket
)

// PRInfo is a pull request as the provider shows it.
type PRInfo struct {
	Number       int
	URL          string
	State        PRState
	Draft        bool
	Title        string // as the host shows it: with DraftPrefix on a prefix-marked draft
	Body         string // GitHub's body, Bitbucket's description; untrusted text
	AuthorID     string // the PR author's account ID (GitHub numeric user ID; Bitbucket account_id)
	SourceBranch string
	SourceRepo   string // owner/name of the head repository, as the provider spells it
	HeadSHA      string // may be abbreviated (Bitbucket gives 12 hex); compare with SameCommit
}

// CommentKind is where a comment sits on a pull request.
type CommentKind string

const (
	CommentInline  CommentKind = "inline"  // on a line of the diff, in a review thread
	CommentReview  CommentKind = "review"  // a review's top-level body
	CommentGeneral CommentKind = "general" // on the pull request's conversation
)

// Comment is one comment on a pull request. Its Body is untrusted text.
type Comment struct {
	ID           string      `json:"id"`
	Kind         CommentKind `json:"kind"`
	Author       string      `json:"author,omitempty"`       // display name or login, for the report
	AuthorID     string      `json:"author_id,omitempty"`    // stable account ID, for the trust rule
	Collaborator bool        `json:"collaborator,omitempty"` // GitHub: author_association OWNER, MEMBER or COLLABORATOR; Bitbucket: always true
	Self         bool        `json:"self,omitempty"`         // posted as the identity Fugaro uses
	SelfKnown    bool        `json:"self_known,omitempty"`   // the adapter could tell (its identity lookup worked)
	Resolved     bool        `json:"resolved,omitempty"`     // inline only: its thread is resolved
	Outdated     bool        `json:"outdated,omitempty"`     // inline only: the line it was on has changed
	Truncated    bool        `json:"truncated,omitempty"`    // inline only: its thread had more comments than were read
	Deleted      bool        `json:"deleted,omitempty"`
	Path         string      `json:"path,omitempty"`
	Line         int         `json:"line,omitempty"`
	Body         string      `json:"body"`
	CreatedAt    time.Time   `json:"created_at"`
	URL          string      `json:"url,omitempty"`
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
	// With spec.Number set it only updates that pull request's draft
	// state, and never creates one (see PRSpec.Number).
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
	// GitAuth returns git credentials that stay valid for at least
	// minValid, refreshing them first if needed (design §6.2).
	GitAuth(ctx context.Context, minValid time.Duration) (GitAuth, error)
	// Repository reads the repository's visibility.
	Repository(ctx context.Context) (RepoInfo, error)
	// UpdatePR changes pull request number's title and/or body (and, when
	// u.Draft is set, its draft state) and nothing else: reviewers, labels,
	// the draft state when u.Draft is nil, and any field u leaves nil keep
	// their values. It reads the PR first: a PR that isn't open is not
	// written and gives ErrPRNotOpen (wrapped, with the PR), and an update
	// that would change nothing sends no write. A title update keeps the
	// DraftPrefix on a prefix-marked draft. The returned PR's Draft is the
	// state after the call.
	UpdatePR(ctx context.Context, number int, u PRUpdate) (PR, error)
	// ApplyReady requests reviewers and applies labels on pull request
	// number, which the caller has just made ready (a draft carries
	// neither; EnsurePR with Draft true never applies them). It is
	// idempotent: reviewers or labels already on the PR are not asked for
	// again, so a retry is safe. Bitbucket has no labels: they are dropped
	// with a warning, once per Provider. A failure, such as an unknown
	// reviewer, returns a *PartialError: the PR stays as it is (ready) and
	// the caller reports it. A PR that isn't open gives ErrPRNotOpen.
	ApplyReady(ctx context.Context, number int, reviewers, labels []string) error
	// PullRequest reads pull request number, whatever its state.
	PullRequest(ctx context.Context, number int) (PRInfo, error)
	// Comments returns every published comment on pull request number,
	// oldest first; filtering is the caller's. Unpublished ones (pending
	// reviews and drafts) and reviews with no body are left out.
	//
	// Self and SelfKnown say whether a comment was posted as the identity
	// Fugaro uses, which the adapter looks up (GitHub: the App, GET /app;
	// Bitbucket: the token's user, GET /user). A lookup the provider
	// answers definitely (a 4xx other than 408 and 429) leaves SelfKnown
	// false on every comment, and the answer is kept. A passing failure (a
	// network error, a 408, 429 or 5xx, a cancelled context) is not kept:
	// the GitHub adapter then returns the comments with SelfKnown false,
	// the Bitbucket adapter fails the call. Callers must work either way:
	// trust never rests on Self.
	Comments(ctx context.Context, number int) ([]Comment, error)
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
