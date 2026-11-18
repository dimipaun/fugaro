package github

// The issue side of this adapter (D2718, B11): the orchestrator's backlog
// reads the open issues of the repository it works on, reads one back, and
// opens one of its own. The credentials service is what reaches GitHub here,
// with a token it mints for itself for that one repository and never hands
// over, so nothing below holds a credential of the daemon's.
//
// Nothing here decides what an issue may carry into the state: the bounds an
// opened issue's title and body are held to are the service route's
// (D2718), the mirror marker and the external id are the sync's (D2728), and
// this file is only how GitHub is asked and what its answer says.
//
// GitHub's issue endpoints are the pull request endpoints' as well -- a pull
// request is an issue -- and both listings report one with a `pull_request`
// field. An item is never a pull request (D2720), so the listing drops what
// carries the field and the single read refuses it rather than handing a
// caller a review thread's title and body where an issue's belongs. (This is
// not internal/issueresolve, which asks GitHub anonymously about Fugaro's
// own product issues and returns a verdict and never their text.)

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Issue is one issue of this repository, as this adapter reads and opens it.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	// State is GitHub's own word: "open" or "closed". The listing returns
	// open issues only, so every entry of it carries "open"; the single read
	// answers for a closed issue too, which is what keeps a caller from
	// reading "the read worked" as "the issue is still open".
	State string
	// AuthorLogin and AuthorID name the account the issue was opened with,
	// and AppSlug and AppID are the GitHub App the action was performed
	// through, empty when it was performed by a person. D2728 reads the
	// mirror marker only on an issue this installation's own App opened, so
	// the sync has to tell such an issue from a stranger's: its signals are
	// the App's own bot login (AppIdentity), the slug, and the id — which is
	// the one that says WHICH App, since the slug names an App within the
	// host and not within an installation (localcfg records the App by id).
	AuthorLogin string
	AuthorID    string
	AppSlug     string
	AppID       string
}

// issue is the REST issue object, in the fields above. Both the listing and
// the single read answer with this subset; the many fields they do not share
// are ignored on purpose, as decoding strictly would refuse every answer.
type issue struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	Body    string    `json:"body"` // null for an empty body
	HTMLURL string    `json:"html_url"`
	State   string    `json:"state"`
	User    *restUser `json:"user"` // null for a deleted account
	// PullRequest is present, and not null, exactly on an issue that is
	// really a pull request.
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	// PerformedViaGitHubApp names the App an action was performed through,
	// null for an action somebody took with their own account.
	PerformedViaGitHubApp *performedApp `json:"performed_via_github_app"`
}

// performedApp is the App an action was performed through, as GitHub's
// `performed_via_github_app` carries it: the App's own id and its slug. The id
// is what tells THIS installation's App from any other App the repository has
// installed — a workflow's own App opens issues too, and its bot login looks
// exactly like ours — and it is the spelling the installation records
// (localcfg's github_app_id) and the one the sync compares an issue against
// (D2728).
type performedApp struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

// id is the App's id as the string a config and a route carry, empty for an
// App GitHub answered without one.
func (a *performedApp) id() string {
	if a == nil || a.ID == 0 {
		return ""
	}
	return strconv.FormatInt(a.ID, 10)
}

func (i issue) isPullRequest() bool { return i.PullRequest != nil }

// read is the issue as a caller is given it.
func (i issue) read() Issue {
	out := Issue{Number: i.Number, Title: i.Title, Body: i.Body, URL: i.HTMLURL, State: i.State,
		AuthorLogin: i.User.login(), AuthorID: i.User.id()}
	if app := i.PerformedViaGitHubApp; app != nil {
		out.AppSlug, out.AppID = app.Slug, app.id()
	}
	return out
}

// ListOpenIssues returns every open issue of this repository, in the order
// GitHub lists them (newest first), following the listing's pages.
//
// Pull requests are dropped, one by one: GitHub's issue list carries them,
// and nothing about an item is a pull request. The listing asks for open
// issues only, so an issue closed since the last read is absent from the
// answer rather than reported as closed; the single read is what answers for
// one (D2720).
func (p *Provider) ListOpenIssues(ctx context.Context) ([]Issue, error) {
	q := url.Values{"state": {"open"}, "per_page": {"100"}}
	all, err := listAll[issue](ctx, p.api, "the open issues", p.repoPath("/issues?"+q.Encode()))
	if err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(all))
	for _, i := range all {
		if i.isPullRequest() {
			continue
		}
		out = append(out, i.read())
	}
	return out, nil
}

// ReadIssue returns one issue by number, an open or a closed one. A number
// that names a pull request is refused rather than answered with: this
// endpoint reports pull requests, and a caller given one would carry a
// review's title and body into a brief.
func (p *Provider) ReadIssue(ctx context.Context, number int) (Issue, error) {
	if number <= 0 {
		return Issue{}, fmt.Errorf("reading issue #%d: not an issue number", number)
	}
	var raw issue
	if err := p.api.Do(ctx, "GET", p.repoPath(fmt.Sprintf("/issues/%d", number)), nil, &raw); err != nil {
		return Issue{}, fmt.Errorf("reading issue #%d: %w", number, err)
	}
	if raw.Number != number {
		// Never answer for another issue than the one asked for.
		return Issue{}, fmt.Errorf("reading issue #%d: the response is for #%d", number, raw.Number)
	}
	if raw.isPullRequest() {
		return Issue{}, fmt.Errorf("reading issue #%d: GitHub reports it as a pull request", number)
	}
	return raw.read(), nil
}

// OpenIssue opens one issue with title and body, and returns it as GitHub
// answered. It is this file's only write, and the credentials service is its
// only caller: an item the sync mirrors is opened as an issue (D2719), and
// the number of the answer is what the item's external id is written from
// (D2728).
//
// What a title and a body may be is the route's rule and not this call's
// (D2718): the text given is carried as it is, and a limit this call
// invented would be a shorter one the route's own check never sees.
func (p *Provider) OpenIssue(ctx context.Context, title, body string) (Issue, error) {
	var raw issue
	if err := p.api.Do(ctx, "POST", p.repoPath("/issues"), map[string]string{"title": title, "body": body}, &raw); err != nil {
		return Issue{}, fmt.Errorf("opening an issue: %w", err)
	}
	if raw.Number == 0 {
		// A creation answered without a number created nothing a caller can
		// name, and the external id written from it would be "#0" -- an id
		// every later read would miss (D2728). Never guess which issue it is.
		return Issue{}, fmt.Errorf("opening an issue: the answer names no issue number")
	}
	return raw.read(), nil
}
