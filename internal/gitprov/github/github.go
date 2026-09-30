// Package github implements gitprov.Provider against the GitHub REST and
// GraphQL APIs, authenticated as a GitHub App through installation tokens
// scoped to one repository (design §6.1, §6.2).
package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// gitUsername is the HTTPS username git uses with an installation token.
const gitUsername = "x-access-token"

// apiMinValid is how long a token used for an API call must stay valid.
const apiMinValid = 2 * time.Minute

// GraphQL mutations for the draft state, which the REST API cannot change.
const (
	markReadyMutation      = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
	convertToDraftMutation = `mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
)

// Options configure a Provider.
type Options struct {
	Owner, Repo string
	AppID       string // the GitHub App's ID (or client ID)
	PrivateKey  *rsa.PrivateKey
	BaseURL     string // REST API root; empty means DefaultBaseURL
	HTTP        *http.Client
	Now         func() time.Time // nil means time.Now
}

// Provider is a GitHub repository reached through a GitHub App.
type Provider struct {
	owner, repo string
	api         *httpjson.Client // authenticated with the installation token
	graphqlURL  string
	tokens      *tokenSource
}

// New returns a Provider for o.
func New(o Options) (*Provider, error) {
	if o.Owner == "" || o.Repo == "" {
		return nil, errors.New("github: owner and repository are required")
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return nil, errors.New("github: an App ID and private key are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimSuffix(o.BaseURL, "/")
	if o.Now == nil {
		o.Now = time.Now
	}
	header := http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}
	app := &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: appAuth(o.AppID, o.PrivateKey, o.Now)}
	p := &Provider{
		owner: o.Owner, repo: o.Repo, graphqlURL: graphqlURL(o.BaseURL),
		tokens: &tokenSource{app: app, owner: o.Owner, repo: o.Repo, now: o.Now},
	}
	p.api = &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: func(ctx context.Context) (string, error) {
		tok, _, err := p.tokens.Token(ctx, apiMinValid)
		return "Bearer " + tok, err
	}}
	return p, nil
}

// graphqlURL derives the GraphQL endpoint from the REST root: GitHub
// Enterprise Server serves REST under /api/v3 and GraphQL at /api/graphql.
func graphqlURL(base string) string {
	if strings.HasSuffix(base, "/api/v3") {
		return strings.TrimSuffix(base, "/v3") + "/graphql"
	}
	return base + "/graphql"
}

type pull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	NodeID  string `json:"node_id"`
	Title   string `json:"title"`
}

func (p *Provider) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo) + suffix
}

// EnsurePR implements gitprov.Provider. spec.Reviewers are user logins, or
// "org/team" for a team. Retrying after a failure is safe: find locates
// what an earlier attempt created, so a run never ends with two pull
// requests for the same branch.
func (p *Provider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	existing, err := p.find(ctx, spec.Branch)
	if err != nil {
		return gitprov.PR{}, err
	}
	if existing != nil {
		return p.update(ctx, *existing, spec.Draft)
	}
	pr, err := p.create(ctx, spec)
	if err != nil {
		return gitprov.PR{}, err
	}
	var errs []error
	if len(spec.Labels) > 0 {
		if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/labels", pr.Number)), map[string]any{"labels": spec.Labels}, nil); err != nil {
			errs = append(errs, fmt.Errorf("adding labels: %w", err))
		}
	}
	if len(spec.Reviewers) > 0 {
		body, err := p.reviewers(spec.Reviewers)
		if err != nil {
			errs = append(errs, err)
		} else if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/pulls/%d/requested_reviewers", pr.Number)), body, nil); err != nil {
			errs = append(errs, fmt.Errorf("requesting reviewers: %w", err))
		}
	}
	if len(errs) > 0 {
		return pr, &gitprov.PartialError{Err: errors.Join(errs...)}
	}
	return pr, nil
}

func (p *Provider) find(ctx context.Context, branch string) (*pull, error) {
	q := url.Values{"head": {p.owner + ":" + branch}, "state": {"open"}, "per_page": {"1"}}
	var pulls []pull
	if err := p.api.Do(ctx, "GET", p.repoPath("/pulls?"+q.Encode()), nil, &pulls); err != nil {
		return nil, fmt.Errorf("finding the pull request for %s: %w", branch, err)
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

func (p *Provider) create(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	body := map[string]any{"title": spec.Title, "head": spec.Branch, "base": spec.Base, "body": spec.Body, "draft": spec.Draft}
	var pr pull
	err := p.api.Do(ctx, "POST", p.repoPath("/pulls"), body, &pr)
	if err != nil && spec.Draft && draftUnsupported(err) {
		// Private repositories on some plans cannot have draft pull
		// requests: open a normal one marked as a draft in its title.
		body["draft"], body["title"] = false, gitprov.DraftTitle(spec.Title, true)
		err = p.api.Do(ctx, "POST", p.repoPath("/pulls"), body, &pr)
	}
	if err != nil {
		return gitprov.PR{}, fmt.Errorf("creating the pull request for %s: %w", spec.Branch, err)
	}
	return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: spec.Draft}, nil
}

// draftUnsupported reports whether err is GitHub refusing a draft pull
// request ("Draft pull requests are not supported in this repository.").
func draftUnsupported(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(se.Body), "draft")
}

// update sets an existing pull request's draft state, and nothing else
// apart from the draft title prefix.
//
// The direction of a failure decides whether it is a *gitprov.PartialError
// or a plain, retryable error (gitprov.go, PartialError): a pull request
// left looking more like a draft than requested is safe to leave for a
// retry to heal, so that is a PartialError with the populated (still
// draft-looking) PR; a pull request left looking more ready than requested
// is not safe to leave, so that is a plain error, with the populated PR the
// caller can still inspect or retry EnsurePR over.
func (p *Provider) update(ctx context.Context, pr pull, draft bool) (gitprov.PR, error) {
	title := gitprov.DraftTitle(pr.Title, false)
	switch {
	case draft && !pr.Draft:
		if err := p.graphql(ctx, convertToDraftMutation, pr.NodeID); err != nil {
			if !draftUnsupportedGraphQL(err) {
				// Some other failure (a 5xx, a dropped connection, a
				// permissions error): the PR is unchanged, so return it
				// alongside a plain, retryable error. Trying the title
				// fallback here would be presumptuous — GitHub never said
				// drafts are unsupported, so the caller should retry the
				// real conversion instead.
				return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: pr.Draft},
					fmt.Errorf("marking pull request #%d a draft: %w", pr.Number, err)
			}
			// No draft support: say so in the title instead. If that also
			// fails, the PR is left looking fully ready — the unsafe
			// direction — so report a plain, retryable error rather than
			// silently returning success.
			// A title already carrying the prefix (an earlier run's
			// fallback) needs no PATCH.
			title = gitprov.DraftTitle(pr.Title, true)
			if title == pr.Title {
				return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft}, nil
			}
			if perr := p.retitle(ctx, pr.Number, title); perr != nil {
				return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: pr.Draft},
					fmt.Errorf("could not mark pull request #%d as a draft (graphql: %w) and could not retitle it either: %v", pr.Number, err, perr)
			}
			return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft}, nil
		}
	case !draft && pr.Draft:
		if err := p.graphql(ctx, markReadyMutation, pr.NodeID); err != nil {
			// The PR is still a draft: the conservative direction, safe for
			// a retry to heal, so this is a PartialError rather than an
			// error that hides the PR that already exists.
			return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: true},
				&gitprov.PartialError{Err: fmt.Errorf("marking pull request #%d ready: %w", pr.Number, err)}
		}
	}
	if title != pr.Title {
		// This step only ever strips the "[DRAFT] " prefix (the switch
		// above already returned for the case that adds it): a failure
		// here leaves the PR's title looking more draft-like than
		// requested, the conservative direction, so it is a PartialError
		// with the draft-looking state rather than a plain error.
		if err := p.retitle(ctx, pr.Number, title); err != nil {
			return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: true},
				&gitprov.PartialError{Err: fmt.Errorf("retitling pull request #%d: %w", pr.Number, err)}
		}
	}
	return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft}, nil
}

func (p *Provider) retitle(ctx context.Context, number int, title string) error {
	return p.api.Do(ctx, "PATCH", p.repoPath(fmt.Sprintf("/pulls/%d", number)), map[string]any{"title": title}, nil)
}

// draftUnsupportedGraphQL reports whether err is GitHub's GraphQL API
// refusing to convert a pull request to a draft because the repository's
// plan does not support draft pull requests ("Draft pull requests are not
// supported in this repository."). Unlike draftUnsupported (the REST
// create path, which has a reliable 422 status to key off), a GraphQL
// error carries no distinct status, so this matches on the message text;
// requiring both "draft" and "not supported" keeps it from firing on an
// unrelated draft-adjacent error.
func draftUnsupportedGraphQL(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "draft") && strings.Contains(msg, "not supported")
}

func (p *Provider) graphql(ctx context.Context, query, id string) error {
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": query, "variables": map[string]string{"id": id}}
	if err := p.api.Do(ctx, "POST", p.graphqlURL, in, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return errors.New("graphql: " + strings.Join(msgs, "; "))
	}
	return nil
}

// reviewers splits names (user logins, or "org/team" for a team) into the
// requested_reviewers request body. A team's org must be the repository's
// own owner: GitHub cannot request a review from another organization's
// team, and silently keeping just the team name would ask the wrong team
// (or a same-named one) instead of clearly failing.
func (p *Provider) reviewers(names []string) (map[string][]string, error) {
	out := map[string][]string{"reviewers": {}, "team_reviewers": {}}
	for _, n := range names {
		if org, team, ok := strings.Cut(n, "/"); ok {
			if !strings.EqualFold(org, p.owner) {
				return nil, fmt.Errorf("reviewer %q names a team outside %s; team reviewers must belong to the repository's own organization", n, p.owner)
			}
			out["team_reviewers"] = append(out["team_reviewers"], team)
		} else {
			out["reviewers"] = append(out["reviewers"], n)
		}
	}
	return out, nil
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/comments", pr.Number)), map[string]string{"body": body}, nil); err != nil {
		return fmt.Errorf("commenting on pull request #%d: %w", pr.Number, err)
	}
	return nil
}

// GitAuth implements gitprov.Provider: an installation token for git, and
// GH_TOKEN so the agent's gh works too. The minted token must be added to
// the caller's redaction list — it is a live credential, not just a
// convenience (design §6.1, global constraints).
func (p *Provider) GitAuth(ctx context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	tok, exp, err := p.tokens.Token(ctx, minValid)
	if err != nil {
		return gitprov.GitAuth{}, err
	}
	return gitprov.GitAuth{Username: gitUsername, Token: tok, Expires: exp, Env: map[string]string{"GH_TOKEN": tok}}, nil
}

// errFollowUpReads stands in for the follow-up reads until they are
// implemented against the github API.
var errFollowUpReads = errors.New("github: not implemented until M6 task 3")

// Repository implements gitprov.Provider.
func (p *Provider) Repository(context.Context) (gitprov.RepoInfo, error) {
	return gitprov.RepoInfo{}, errFollowUpReads
}

// PullRequest implements gitprov.Provider.
func (p *Provider) PullRequest(context.Context, int) (gitprov.PRInfo, error) {
	return gitprov.PRInfo{}, errFollowUpReads
}

// Comments implements gitprov.Provider.
func (p *Provider) Comments(context.Context, int) ([]gitprov.Comment, error) {
	return nil, errFollowUpReads
}
