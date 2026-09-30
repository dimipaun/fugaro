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

// GitUsername is the HTTPS username git uses with a repository access
// token; Cloud Build image builds use it too.
const GitUsername = "x-token-auth"

// Options configure a Provider.
type Options struct {
	Workspace string // the repository's workspace
	Slug      string // the repository slug
	Token     string // repository access token: Repositories read/write, Pull requests read/write
	BaseURL   string // API root; empty means DefaultBaseURL
	HTTP      *http.Client
	// Warn, when set, receives adapter warnings meant for the run's log:
	// once, the first time a PRSpec with non-empty Labels is seen
	// (Bitbucket Cloud pull requests have no labels, so they are silently
	// dropped otherwise), and once if the token may not read its own
	// identity (GET /user). Nil means no-op.
	Warn func(string)
}

// Provider is a Bitbucket Cloud repository.
type Provider struct {
	o        Options
	api      *httpjson.Client
	warnOnce sync.Once

	identityMu   sync.Mutex
	identityDone bool   // GET /user has given its answer
	selfUUID     string // the token's user's uuid; "" when GET /user was refused
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
	if spec.Number != 0 {
		// An update by number must never fall through to find-or-create.
		return p.updateByNumber(ctx, spec)
	}
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
		return p.update(ctx, *existing, spec.Draft)
	}
	return p.create(ctx, spec)
}

// update puts an existing pull request (one an earlier attempt or the
// agent opened) in the wanted draft state, leaving its title and
// description alone apart from the fallback "[DRAFT] " prefix. A pull
// request already a draft either way (Bitbucket's draft flag, or the
// prefix) is left as is when a draft is wanted: stripping the prefix only
// to re-add it could leave it looking ready if the second request failed.
//
// A failed PUT returns the pull request as currently seen. When ready was
// wanted it is still a draft, the conservative direction, so the failure
// is a *gitprov.PartialError; when a draft was wanted it still looks
// ready, so the failure is a plain error the runner retries.
func (p *Provider) update(ctx context.Context, existing pullRequest, draft bool) (gitprov.PR, error) {
	seenDraft := existing.isDraft()
	title := existing.Title
	if !draft {
		title = gitprov.DraftTitle(existing.Title, false)
	}
	if seenDraft == draft && title == existing.Title {
		// Nothing would change: skip the PUT.
		return gitprov.PR{Number: existing.ID, URL: existing.Links.HTML.Href, Draft: seenDraft}, nil
	}
	var pr pullRequest
	body := map[string]any{"title": title, "draft": draft}
	if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", existing.ID)), body, &pr); err != nil {
		out := gitprov.PR{Number: existing.ID, URL: existing.Links.HTML.Href, Draft: seenDraft}
		wrapped := fmt.Errorf("updating pull request #%d: %w", existing.ID, err)
		if !draft && seenDraft {
			return out, &gitprov.PartialError{Err: wrapped}
		}
		return out, wrapped
	}
	return p.finish(ctx, pr, draft)
}

func (p *Provider) find(ctx context.Context, branch string) (*pullRequest, error) {
	q := url.Values{"q": {fmt.Sprintf(`source.branch.name="%s" AND source.repository.full_name="%s/%s" AND state="OPEN"`,
		// Bitbucket stores workspace and repository slugs in lowercase,
		// and full_name is compared as a string.
		escapeBBQL(branch), escapeBBQL(strings.ToLower(p.o.Workspace)), escapeBBQL(strings.ToLower(p.o.Slug)))}}
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
//
// A failure to apply the title's "[DRAFT] " prefix is split by direction
// (design §4.2): a PR left looking more like a draft than requested is
// safe, since a retry heals it (EnsurePR finds the PR and re-runs finish),
// so adding the prefix that fails is reported as a plain, retryable error.
// A PR left looking more ready than requested is not safe to leave for a
// retry — the runner must not treat it as done — so removing the prefix
// that fails stays a *gitprov.PartialError, the conservative direction.
func (p *Provider) finish(ctx context.Context, pr pullRequest, draft bool) (gitprov.PR, error) {
	if !draft && pr.Draft {
		out := gitprov.PR{Number: pr.ID, URL: pr.Links.HTML.Href, Draft: true}
		return out, &gitprov.PartialError{Err: fmt.Errorf(
			"could not mark it ready: pull request #%d is still a draft after asking Bitbucket to mark it ready", pr.ID)}
	}
	addPrefix := draft && !pr.Draft
	if title := gitprov.DraftTitle(pr.Title, addPrefix); title != pr.Title {
		// The requested draft state goes along with the title, so this
		// retitle re-asserts what was asked for rather than countermanding
		// it (or, should Bitbucket ever treat an omitted field as false,
		// clearing it).
		body := map[string]any{"title": title, "draft": draft}
		if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", pr.ID)), body, nil); err != nil {
			// The pull request still exists with its title unchanged; the
			// Provider contract (design, gitprov.go) requires it be returned
			// alongside the failure rather than dropped.
			out := gitprov.PR{Number: pr.ID, URL: pr.Links.HTML.Href, Draft: pr.Draft}
			wrapped := fmt.Errorf("retitling pull request #%d: %w", pr.ID, err)
			if addPrefix {
				// The PR still looks ready (unmarked), which a retry can
				// safely correct, so this is a plain error the runner will
				// retry EnsurePR over.
				return out, wrapped
			}
			// The prefix is still there, so the PR is still a draft.
			out.Draft = true
			return out, &gitprov.PartialError{Err: wrapped}
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
	return gitprov.GitAuth{Username: GitUsername, Token: p.o.Token}, nil
}

// Repository implements gitprov.Provider.
func (p *Provider) Repository(ctx context.Context) (gitprov.RepoInfo, error) {
	var repo struct {
		IsPrivate *bool `json:"is_private"`
	}
	path := "/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug)
	if err := p.api.Do(ctx, "GET", path, nil, &repo); err != nil {
		return gitprov.RepoInfo{}, fmt.Errorf("reading the repository: %w", err)
	}
	if repo.IsPrivate == nil {
		// Never guess: a public repository taken for private would let a
		// follow-up run there without allow_public.
		return gitprov.RepoInfo{}, errors.New("reading the repository: the response has no is_private field")
	}
	return gitprov.RepoInfo{Private: *repo.IsPrivate}, nil
}

// pullDetail is GET …/pullrequests/{id}.
type pullDetail struct {
	pullRequest
	State  string `json:"state"`  // OPEN, MERGED, DECLINED or SUPERSEDED
	Author *user  `json:"author"` // author.account_id is <digits>:<uuid>
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Commit *struct {
			Hash string `json:"hash"` // abbreviated, 12 hex
		} `json:"commit"`
		Repository *struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"source"`
}

func (p *Provider) getPull(ctx context.Context, number int) (pullDetail, gitprov.PRInfo, error) {
	if number <= 0 {
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: not a pull request number", number)
	}
	var d pullDetail
	if err := p.api.Do(ctx, "GET", p.prPath(fmt.Sprintf("/%d", number)), nil, &d); err != nil {
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: %w", number, err)
	}
	if d.ID != number {
		// Never act on another pull request than the one asked for.
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: the response is for #%d", number, d.ID)
	}
	info := gitprov.PRInfo{Number: d.ID, URL: d.Links.HTML.Href, Draft: d.isDraft(), AuthorID: d.Author.accountID(),
		SourceBranch: d.Source.Branch.Name}
	if d.Source.Commit != nil {
		info.HeadSHA = d.Source.Commit.Hash
	}
	if d.Source.Repository != nil {
		info.SourceRepo = d.Source.Repository.FullName
	}
	switch d.State {
	case "OPEN":
		info.State = gitprov.PROpen
	case "MERGED":
		info.State = gitprov.PRMerged
	case "DECLINED", "SUPERSEDED":
		info.State = gitprov.PRClosed
	default:
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: unknown state %q", number, d.State)
	}
	return d, info, nil
}

// isDraft reports whether the pull request is a draft, by Bitbucket's
// flag or by the fallback title prefix (design §15).
func (pr pullRequest) isDraft() bool {
	return pr.Draft || strings.HasPrefix(pr.Title, gitprov.DraftPrefix)
}

// PullRequest implements gitprov.Provider.
func (p *Provider) PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error) {
	_, info, err := p.getPull(ctx, number)
	return info, err
}

// updateByNumber is EnsurePR for spec.Number: it reads that pull request
// and changes its draft state only when it is still open on spec.Branch.
// It never looks a pull request up by branch, and never creates one. It
// doesn't check the source repository: a pull request's source can't
// change, and the follow-up checked it when it started.
func (p *Provider) updateByNumber(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	d, info, err := p.getPull(ctx, spec.Number)
	if err != nil {
		return gitprov.PR{}, err
	}
	pr := gitprov.PR{Number: info.Number, URL: info.URL, Draft: info.Draft}
	if info.State != gitprov.PROpen {
		return pr, fmt.Errorf("pull request #%d is %s: %w", info.Number, info.State, gitprov.ErrPRNotOpen)
	}
	if info.SourceBranch != spec.Branch {
		return pr, fmt.Errorf("pull request #%d is on %s, not %s: %w", info.Number, info.SourceBranch, spec.Branch, gitprov.ErrPRNotOpen)
	}
	return p.update(ctx, d.pullRequest, spec.Draft)
}
