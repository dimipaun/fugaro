// Package bitbucket implements gitprov.Provider against the Bitbucket Cloud
// REST API (2.0), authenticated with a repository access token (design §6.1).
package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// DefaultBaseURL is the Bitbucket Cloud API root.
const DefaultBaseURL = "https://api.bitbucket.org/2.0"

// gitUsername is the HTTPS username git uses with a repository access token.
const gitUsername = "x-token-auth"

// Options configure a Provider.
type Options struct {
	Workspace string // the repository's workspace
	Slug      string // the repository slug
	Token     string // repository access token: Repositories read/write, Pull requests read/write
	BaseURL   string // API root; empty means DefaultBaseURL
	HTTP      *http.Client
	// Warn, when set, receives one message the first time a PRSpec with
	// non-empty Labels is seen: Bitbucket Cloud pull requests have no
	// labels, so they are silently dropped otherwise. Nil means no-op.
	Warn func(string)
}

// Provider is a Bitbucket Cloud repository.
type Provider struct {
	o        Options
	api      *httpjson.Client
	warnOnce sync.Once
}

// New returns a Provider for o.
func New(o Options) (*Provider, error) {
	if o.Workspace == "" || o.Slug == "" {
		return nil, errors.New("bitbucket: workspace and repository slug are required")
	}
	if o.Token == "" {
		return nil, errors.New("bitbucket: a repository access token is required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	token := o.Token
	return &Provider{o: o, api: &httpjson.Client{
		BaseURL: o.BaseURL, HTTP: o.HTTP,
		Header: http.Header{"Accept": {"application/json"}},
		Auth:   func(context.Context) (string, error) { return "Bearer " + token, nil },
	}}, nil
}

type pullRequest struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Draft bool   `json:"draft"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type endpoint struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

func branch(name string) endpoint {
	var e endpoint
	e.Branch.Name = name
	return e
}

func (p *Provider) prPath(suffix string) string {
	return "/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug) + "/pullrequests" + suffix
}

// EnsurePR implements gitprov.Provider. Bitbucket Cloud pull requests have
// no labels, so spec.Labels is ignored (logged once per Provider instance).
// spec.Reviewers are account UUIDs ("{…}") or account IDs.
func (p *Provider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	if len(spec.Labels) > 0 {
		p.warnOnce.Do(func() {
			p.o.Warn(fmt.Sprintf("bitbucket: pull request labels aren't supported; ignoring labels %v", spec.Labels))
		})
	}
	existing, err := p.find(ctx, spec.Branch)
	if err != nil {
		return gitprov.PR{}, err
	}
	if existing != nil {
		title := gitprov.DraftTitle(existing.Title, false)
		if title == existing.Title && existing.Draft == spec.Draft {
			// Nothing would change: no fallback prefix to add or remove,
			// and the draft state already matches. Skip the PUT.
			return gitprov.PR{Number: existing.ID, URL: existing.Links.HTML.Href, Draft: existing.Draft}, nil
		}
		var pr pullRequest
		body := map[string]any{"title": title, "draft": spec.Draft}
		if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", existing.ID)), body, &pr); err != nil {
			return gitprov.PR{}, fmt.Errorf("updating pull request #%d: %w", existing.ID, err)
		}
		return p.finish(ctx, pr, spec.Draft)
	}
	return p.create(ctx, spec)
}

func (p *Provider) find(ctx context.Context, branch string) (*pullRequest, error) {
	q := url.Values{"q": {fmt.Sprintf(`source.branch.name="%s" AND source.repository.full_name="%s/%s" AND state="OPEN"`,
		escapeBBQL(branch), escapeBBQL(p.o.Workspace), escapeBBQL(p.o.Slug))}}
	var page struct {
		Values []pullRequest `json:"values"`
	}
	if err := p.api.Do(ctx, "GET", p.prPath("?"+q.Encode()), nil, &page); err != nil {
		return nil, fmt.Errorf("finding the pull request for %s: %w", branch, err)
	}
	if len(page.Values) == 0 {
		return nil, nil
	}
	return &page.Values[0], nil
}

// escapeBBQL escapes s for embedding in a double-quoted BBQL string literal
// (Bitbucket's query language, used by find's ?q= parameter): backslashes
// and double quotes in a branch name must not be allowed to break out of
// the literal or, worse, splice extra query clauses in.
func escapeBBQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func (p *Provider) create(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	body := map[string]any{
		"title": spec.Title, "description": spec.Body,
		"source": branch(spec.Branch), "destination": branch(spec.Base),
		"draft": spec.Draft, "close_source_branch": true,
	}
	if len(spec.Reviewers) > 0 {
		body["reviewers"] = reviewers(spec.Reviewers)
	}
	var pr pullRequest
	err := p.api.Do(ctx, "POST", p.prPath(""), body, &pr)
	var rejected error
	var se *httpjson.StatusError
	if err != nil && len(spec.Reviewers) > 0 && errors.As(err, &se) && se.Status == http.StatusBadRequest {
		// Bitbucket rejects the whole pull request over one bad reviewer
		// (an unknown account, or the token's own). That must not cost the
		// run its pull request: open it without reviewers instead.
		rejected = fmt.Errorf("reviewers %s were rejected: %w", strings.Join(spec.Reviewers, ", "), err)
		delete(body, "reviewers")
		err = p.api.Do(ctx, "POST", p.prPath(""), body, &pr)
	}
	if err != nil {
		return gitprov.PR{}, fmt.Errorf("creating the pull request for %s: %w", spec.Branch, err)
	}
	out, ferr := p.finish(ctx, pr, spec.Draft)
	return out, joinRejected(rejected, ferr)
}

// joinRejected reports rejected (a reviewer-fallback failure from create)
// alongside err (whatever finish returned), so that neither is silently
// dropped. The pull request was created either way, so a non-nil result is
// always a *gitprov.PartialError.
func joinRejected(rejected, err error) error {
	if rejected == nil {
		return err
	}
	if err == nil {
		return &gitprov.PartialError{Err: rejected}
	}
	var pe *gitprov.PartialError
	if errors.As(err, &pe) {
		return &gitprov.PartialError{Err: fmt.Errorf("%w; %s", pe.Err, rejected)}
	}
	return fmt.Errorf("%w (also, %s)", err, rejected)
}

func reviewers(ids []string) []map[string]string {
	out := make([]map[string]string, len(ids))
	for i, id := range ids {
		if strings.HasPrefix(id, "{") {
			out[i] = map[string]string{"uuid": id}
		} else {
			out[i] = map[string]string{"account_id": id}
		}
	}
	return out
}

// finish returns pr as a gitprov.PR in the wanted draft state. If
// Bitbucket did not make it a real draft, the draft is marked in the title
// instead (design §15); a title prefix left from that is removed once the
// pull request is ready. If a ready pull request is wanted but Bitbucket
// still reports it as a draft (e.g. it does not honor the draft field and
// this is the first time the run has asked it to go ready), the pull
// request is still returned — it exists and its URL and number are valid —
// alongside a *gitprov.PartialError, matching the Provider contract that an
// existing pull request is always returned even when some setting could
// not be applied.
func (p *Provider) finish(ctx context.Context, pr pullRequest, draft bool) (gitprov.PR, error) {
	if !draft && pr.Draft {
		out := gitprov.PR{Number: pr.ID, URL: pr.Links.HTML.Href, Draft: true}
		return out, &gitprov.PartialError{Err: fmt.Errorf(
			"could not mark it ready: pull request #%d is still a draft after asking Bitbucket to mark it ready", pr.ID)}
	}
	if title := gitprov.DraftTitle(pr.Title, draft && !pr.Draft); title != pr.Title {
		if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", pr.ID)), map[string]any{"title": title}, nil); err != nil {
			return gitprov.PR{}, fmt.Errorf("retitling pull request #%d: %w", pr.ID, err)
		}
	}
	return gitprov.PR{Number: pr.ID, URL: pr.Links.HTML.Href, Draft: draft}, nil
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	in := map[string]any{"content": map[string]string{"raw": body}}
	if err := p.api.Do(ctx, "POST", p.prPath(fmt.Sprintf("/%d/comments", pr.Number)), in, nil); err != nil {
		return fmt.Errorf("commenting on pull request #%d: %w", pr.Number, err)
	}
	return nil
}

// GitAuth implements gitprov.Provider. A repository access token is used
// as is (design §6.2); it does not expire during a run.
func (p *Provider) GitAuth(context.Context, time.Duration) (gitprov.GitAuth, error) {
	return gitprov.GitAuth{Username: gitUsername, Token: p.o.Token}, nil
}
