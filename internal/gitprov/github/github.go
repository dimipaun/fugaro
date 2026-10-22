// Package github implements gitprov.Provider against the GitHub REST and
// GraphQL APIs, authenticated as a GitHub App through installation tokens
// scoped to one repository (design §6.1, §6.2).
package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	// ServiceHeldKey says the App's private key is not this process's to
	// hold: the installation's credentials service holds it and mints the
	// run's installation tokens (C2.2, D145). New then needs no PrivateKey
	// and refuses every token until UseServiceTokens installs the source
	// the service's tokens come from, so a provider built this way can never
	// fall back to a key it was not given.
	ServiceHeldKey bool
	// Warn, when set, receives adapter warnings meant for the run's log,
	// such as a failed lookup of the App's own identity. Nil drops them.
	Warn func(string)
}

// Provider is a GitHub repository reached through a GitHub App.
type Provider struct {
	owner, repo string
	api         *httpjson.Client // authenticated with the installation token
	graphqlURL  string
	// tokens is where installation tokens come from: the in-job mint
	// (tokenSource, when this process holds the App key) or the credentials
	// service (ServiceTokens, when it does not -- C2.2).
	tokens tokenMinter
	// app is the client authenticated AS the App (JWT), used for the reads
	// only an App's own key authorizes (GET /app). It is nil when the key is
	// the service's: see appSlug.
	app  *httpjson.Client
	warn func(string)

	identityMu     sync.Mutex
	identityDone   bool   // GET /app gave a definite answer
	identityWarned bool   // a failed GET /app has been warned about
	slug           string // the App's slug; "" until GET /app gives it
}

// tokenMinter is what a provider asks for an installation token. Both
// tokenSource (the App's key is in this process) and ServiceTokens (the
// credentials service holds it, C2.2) are one.
type tokenMinter interface {
	Token(ctx context.Context, minValid time.Duration) (string, time.Time, error)
}

// noTokenSource refuses every token until the real source is installed. It
// is what a provider with ServiceHeldKey starts with, so that a provider
// whose tokens are meant to come from the service cannot mint one anywhere
// else even for a moment, and a missing install is a refusal rather than a
// call that skipped the service.
type noTokenSource struct{}

func (noTokenSource) Token(context.Context, time.Duration) (string, time.Time, error) {
	return "", time.Time{}, fmt.Errorf("%w: this provider's tokens come from the credentials service and no source has been installed", ErrServiceTokensIdentity)
}

// New returns a Provider for o.
func New(o Options) (*Provider, error) {
	if o.Owner == "" || o.Repo == "" {
		return nil, errors.New("github: owner and repository are required")
	}
	if o.AppID == "" {
		return nil, errors.New("github: an App ID is required")
	}
	if o.PrivateKey == nil && !o.ServiceHeldKey {
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
	// The App-authenticated client exists only where the key does: with the
	// key held by the service this process cannot sign a JWT, and a client
	// that could not authenticate would fail later, further from the reason.
	// ServiceHeldKey wins over a PrivateKey that is somehow also passed: a
	// provider told its tokens are the service's must not mint one in-job,
	// whatever it was handed, so a caller that set both gets the refusal
	// rather than the key's path.
	var app *httpjson.Client
	var tokens tokenMinter = noTokenSource{}
	if o.PrivateKey != nil && !o.ServiceHeldKey {
		app = &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: appAuth(o.AppID, o.PrivateKey, o.Now)}
		tokens = &tokenSource{app: app, owner: o.Owner, repo: o.Repo, now: o.Now}
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	p := &Provider{
		owner: o.Owner, repo: o.Repo, graphqlURL: graphqlURL(o.BaseURL), warn: o.Warn,
		tokens: tokens, app: app,
	}
	p.api = &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: func(ctx context.Context) (string, error) {
		tok, _, err := p.tokens.Token(ctx, apiMinValid)
		return "Bearer " + tok, err
	}}
	return p, nil
}

// UseServiceTokens installs the credentials service as this provider's source
// of installation tokens (gitprov.ServiceTokenSetter). It is how the runner
// hands a ServiceHeldKey provider the service it was built for (C2.2); it is
// called once, before the run's first git call, and it takes effect for every
// call after it -- the API client above reads p.tokens per call, so a token
// minted before the install is never reused as one minted here.
var _ gitprov.ServiceTokenSetter = (*Provider)(nil)

func (p *Provider) UseServiceTokens(src gitprov.TokenSource) {
	if src == nil {
		return
	}
	p.tokens = src
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
	Body    string `json:"body"` // null for an empty body
}

func (p *Provider) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo) + suffix
}

// EnsurePR implements gitprov.Provider. spec.Reviewers are user logins, or
// "org/team" for a team. Retrying after a failure is safe: find locates
// what an earlier attempt created, so a run never ends with two pull
// requests for the same branch.
func (p *Provider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	if spec.Number != 0 {
		// An update by number must never fall through to find-or-create.
		return p.updateByNumber(ctx, spec)
	}
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
	if spec.Draft {
		// A draft carries no reviewers and no labels: ApplyReady adds them
		// once the PR is ready.
		return pr, nil
	}
	if err := p.applyReady(ctx, pr.Number, spec.Reviewers, spec.Labels, nil, nil); err != nil {
		return pr, err
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
	return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: spec.Draft, DraftFallback: spec.Draft && body["draft"] == false}, nil
}

// draftUnsupported reports whether err is GitHub refusing a draft pull
// request ("Draft pull requests are not supported in this repository.").
// Only that message counts: a 422 that merely echoes a branch or title with
// "draft" in it must not downgrade the PR.
func draftUnsupported(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(se.Body), "draft pull requests are not supported")
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
				return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft, DraftFallback: true}, nil
			}
			if perr := p.retitle(ctx, pr.Number, title); perr != nil {
				return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: pr.Draft},
					fmt.Errorf("could not mark pull request #%d as a draft (graphql: %w) and could not retitle it either: %v", pr.Number, err, perr)
			}
			return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft, DraftFallback: true}, nil
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
	return p.graphqlDo(ctx, query, map[string]any{"id": id}, nil)
}

// graphqlDo runs one GraphQL request, and decodes its data into out
// unless out is nil. GraphQL errors, which come with HTTP 200, are errors.
func (p *Provider) graphqlDo(ctx context.Context, query string, vars map[string]any, out any) error {
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": query, "variables": vars}
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
	if out != nil {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("graphql: decoding the response: %w", err)
		}
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

// Repository implements gitprov.Provider.
func (p *Provider) Repository(ctx context.Context) (gitprov.RepoInfo, error) {
	var repo struct {
		Private *bool `json:"private"`
	}
	if err := p.api.Do(ctx, "GET", p.repoPath(""), nil, &repo); err != nil {
		return gitprov.RepoInfo{}, fmt.Errorf("reading the repository: %w", err)
	}
	if repo.Private == nil {
		// Never guess: a public repository taken for private would let a
		// follow-up run there without allow_public.
		return gitprov.RepoInfo{}, errors.New("reading the repository: the response has no private field")
	}
	return gitprov.RepoInfo{Private: *repo.Private}, nil
}

// restUser is a comment's or pull request's author in the REST API; null
// for a deleted account.
type restUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

func (u *restUser) id() string {
	if u == nil || u.ID == 0 {
		return ""
	}
	return strconv.FormatInt(u.ID, 10)
}

func (u *restUser) login() string {
	if u == nil {
		return ""
	}
	return u.Login
}

// pullDetail is GET /repos/{owner}/{repo}/pulls/{number}.
type pullDetail struct {
	pull
	State             string    `json:"state"` // open or closed
	Merged            bool      `json:"merged"`
	User              *restUser `json:"user"`
	AuthorAssociation string    `json:"author_association"`
	Labels            []struct {
		Name string `json:"name"`
	} `json:"labels"`
	RequestedReviewers []restUser `json:"requested_reviewers"`
	RequestedTeams     []struct {
		Slug string `json:"slug"`
	} `json:"requested_teams"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"` // null when the head repository (a fork) was deleted
	} `json:"head"`
}

func (p *Provider) getPull(ctx context.Context, number int) (pullDetail, gitprov.PRInfo, error) {
	if number <= 0 {
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: not a pull request number", number)
	}
	var d pullDetail
	if err := p.api.Do(ctx, "GET", p.repoPath(fmt.Sprintf("/pulls/%d", number)), nil, &d); err != nil {
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: %w", number, err)
	}
	if d.Number != number {
		// Never act on another pull request than the one asked for.
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: the response is for #%d", number, d.Number)
	}
	// Where GitHub refuses drafts, the adapter marks them with DraftPrefix.
	draft := d.Draft || strings.HasPrefix(d.Title, gitprov.DraftPrefix)
	info := gitprov.PRInfo{Number: d.Number, URL: d.HTMLURL, Draft: draft, Title: d.Title, Body: d.Body, AuthorID: d.User.id(),
		SourceBranch: d.Head.Ref, HeadSHA: d.Head.SHA}
	if d.Head.Repo != nil {
		info.SourceRepo = d.Head.Repo.FullName
	}
	switch {
	case d.State == "open":
		info.State = gitprov.PROpen
	case d.State == "closed" && d.Merged:
		info.State = gitprov.PRMerged
	case d.State == "closed":
		info.State = gitprov.PRClosed
	default:
		return pullDetail{}, gitprov.PRInfo{}, fmt.Errorf("reading pull request #%d: unknown state %q", number, d.State)
	}
	return d, info, nil
}

// PullRequest implements gitprov.Provider.
func (p *Provider) PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error) {
	_, info, err := p.getPull(ctx, number)
	return info, err
}

// FetchPRHead implements gitprov.Provider (design generic-tool §3.6, G12).
// The head commit is reachable from origin through the PR's own ref
// (refs/pull/<number>/head), which GitHub keeps on the base repository
// whether or not the head is in a fork, so review mode fetches it by SHA
// alone (gitops.Repo.CheckoutDetached/Worktree) without adding the fork as
// a remote. Fork is decided the same way a follow-up's own check does
// (followup.go, runner.checkPullRequest): the head repository's full name
// differs from this Provider's own, which fails closed (Fork: true) when
// GitHub reports no head repository at all (a deleted fork). Collaborator
// reuses the same author_association classification Comments already uses
// (collaborator, comments.go); Self reuses the same App-identity lookup
// (appSlug) Comments' own Self already does, so review mode's
// trusted-author gate (reviewmode.go) and a follow-up's comment trust
// agree on both signals. Self is always false when Fork is true
// (PRHead's own documented invariant, gitprov.go): a deleted fork (no
// head repository at all, so SourceRepo is "" and Fork fails closed to
// true) could otherwise still carry a "<slug>[bot]"-shaped login and
// read as Self, which the invariant forbids outright, regardless of
// whether today's only consumer happens to re-check Fork itself.
func (p *Provider) FetchPRHead(ctx context.Context, number int) (gitprov.PRHead, error) {
	d, info, err := p.getPull(ctx, number)
	if err != nil {
		return gitprov.PRHead{}, err
	}
	fork := !strings.EqualFold(info.SourceRepo, p.owner+"/"+p.repo)
	slug, known := p.appSlug(ctx)
	self := known && d.User.login() == slug+"[bot]" && !fork
	return gitprov.PRHead{
		SHA: info.HeadSHA, Fork: fork, Open: info.State == gitprov.PROpen,
		AuthorID: d.User.id(), AuthorLogin: d.User.login(),
		Association: d.AuthorAssociation, Collaborator: collaborator(d.AuthorAssociation),
		Self: self, SourceRepo: info.SourceRepo,
	}, nil
}

// updateByNumber is EnsurePR for spec.Number: it reads that pull request
// and changes its draft state only when it is still open on spec.Branch.
// It never looks a pull request up by branch, and never creates one.
// It doesn't check the head repository (SourceRepo): the runner checks it
// at bootstrap, and a pull request's head repository can't change after.
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
	return p.update(ctx, d.pull, spec.Draft)
}

// UpdatePR implements gitprov.Provider. It reads the pull request, then
// sends one PATCH carrying only the fields that differ (GitHub leaves the
// others alone), then, if u.Draft asks for another state, the draft flip.
func (p *Provider) UpdatePR(ctx context.Context, number int, u gitprov.PRUpdate) (gitprov.PR, error) {
	d, info, err := p.getPull(ctx, number)
	if err != nil {
		return gitprov.PR{}, err
	}
	pr := gitprov.PR{Number: info.Number, URL: info.URL, Draft: info.Draft, DraftFallback: strings.HasPrefix(d.Title, gitprov.DraftPrefix)}
	if info.State != gitprov.PROpen {
		return pr, fmt.Errorf("pull request #%d is %s: %w", info.Number, info.State, gitprov.ErrPRNotOpen)
	}
	patch := map[string]any{}
	if u.Title != nil {
		title := *u.Title
		if strings.HasPrefix(d.Title, gitprov.DraftPrefix) && (u.Draft == nil || *u.Draft) {
			// A prefix-marked draft keeps its marker through a retitle.
			title = gitprov.DraftTitle(title, true)
		}
		if title != d.Title {
			patch["title"] = title
			d.Title = title
		}
	}
	if u.Body != nil && *u.Body != d.Body {
		patch["body"] = *u.Body
	}
	if len(patch) > 0 {
		if err := p.api.Do(ctx, "PATCH", p.repoPath(fmt.Sprintf("/pulls/%d", number)), patch, nil); err != nil {
			return pr, fmt.Errorf("updating pull request #%d: %w", number, err)
		}
	}
	if u.Draft != nil && *u.Draft != info.Draft {
		return p.update(ctx, d.pull, *u.Draft)
	}
	return pr, nil
}

// ApplyReady implements gitprov.Provider: labels and requested reviewers
// not already on the pull request, each in one request.
func (p *Provider) ApplyReady(ctx context.Context, number int, reviewers, labels []string) error {
	d, info, err := p.getPull(ctx, number)
	if err != nil {
		return err
	}
	if info.State != gitprov.PROpen {
		return fmt.Errorf("pull request #%d is %s: %w", info.Number, info.State, gitprov.ErrPRNotOpen)
	}
	var haveLabels, haveReviewers []string
	for _, l := range d.Labels {
		haveLabels = append(haveLabels, l.Name)
	}
	for _, u := range d.RequestedReviewers {
		haveReviewers = append(haveReviewers, u.Login)
	}
	for _, t := range d.RequestedTeams {
		haveReviewers = append(haveReviewers, p.owner+"/"+t.Slug)
	}
	return p.applyReady(ctx, number, reviewers, labels, haveReviewers, haveLabels)
}

// applyReady adds labels and requests reviewers, skipping those in have*.
// Failures are collected into one *gitprov.PartialError: the PR exists.
func (p *Provider) applyReady(ctx context.Context, number int, reviewers, labels, haveReviewers, haveLabels []string) error {
	labels = missing(labels, haveLabels)
	reviewers = missing(reviewers, haveReviewers)
	var errs []error
	if len(labels) > 0 {
		if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/labels", number)), map[string]any{"labels": labels}, nil); err != nil {
			errs = append(errs, fmt.Errorf("adding labels: %w", err))
		}
	}
	if len(reviewers) > 0 {
		body, err := p.reviewers(reviewers)
		if err != nil {
			errs = append(errs, err)
		} else if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/pulls/%d/requested_reviewers", number)), body, nil); err != nil {
			errs = append(errs, fmt.Errorf("requesting reviewers: %w", err))
		}
	}
	if len(errs) > 0 {
		return &gitprov.PartialError{Err: errors.Join(errs...)}
	}
	return nil
}

// missing returns the names in want that are not in have (compared
// case-insensitively, as GitHub logins, team slugs and label names are).
func missing(want, have []string) []string {
	var out []string
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.EqualFold(w, h) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, w)
		}
	}
	return out
}
