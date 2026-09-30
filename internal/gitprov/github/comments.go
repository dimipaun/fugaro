package github

// The follow-up reads of a pull request's comments. The shapes, and the
// test fixtures, follow GitHub's documentation (no live check exists yet):
//
//   - review threads, GraphQL: https://docs.github.com/en/graphql/reference/objects#pullrequestreviewthread
//     and https://docs.github.com/en/graphql/reference/objects#pullrequestreviewcomment
//   - GraphQL paging: https://docs.github.com/en/graphql/guides/using-pagination-in-the-graphql-api
//   - review summaries: https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request
//   - general comments: https://docs.github.com/en/rest/issues/comments#list-issue-comments
//   - REST paging (the Link header): https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api
//   - the App's identity: https://docs.github.com/en/rest/apps/apps#get-the-authenticated-app
//   - author_association: https://docs.github.com/en/graphql/reference/enums#commentauthorassociation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// maxPages bounds every listing: a pull request with more pages than this
// of threads, reviews or comments is refused rather than read without end.
const maxPages = 20

// reviewThreadsQuery reads a pull request's review threads, 50 per page,
// with up to 50 comments each.
const reviewThreadsQuery = `query($owner:String!,$name:String!,$n:Int!,$after:String){repository(owner:$owner,name:$name){pullRequest(number:$n){` +
	`reviewThreads(first:50,after:$after){pageInfo{hasNextPage endCursor}nodes{isResolved isOutdated path line ` +
	`comments(first:50){pageInfo{hasNextPage}nodes{id body createdAt url authorAssociation ` +
	`author{__typename login ... on User{databaseId} ... on Bot{databaseId}}}}}}}}}`

// collaborator reports whether an author_association is one GitHub gives
// to people with a role on the repository.
func collaborator(association string) bool {
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

// appSlug returns the App's slug, reading it with GET /app (as the App,
// with its JWT). ok is false when that read failed. A definite answer (the
// slug, or a 4xx refusal other than 429) is remembered for the Provider;
// a passing failure (a cancelled context, a network error, a 429 or 5xx)
// is not, so the next call asks again. A failure is warned about once.
func (p *Provider) appSlug(ctx context.Context) (slug string, ok bool) {
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	if p.identityDone {
		return p.slug, p.slug != ""
	}
	var app struct {
		Slug string `json:"slug"`
	}
	err := p.tokens.app.Do(ctx, "GET", "/app", nil, &app)
	switch {
	case err == nil && app.Slug != "":
		p.identityDone, p.slug = true, app.Slug
		return p.slug, true
	case err == nil:
		err = errors.New("the response has no slug")
		p.identityDone = true
	case definite(ctx, err):
		p.identityDone = true
	}
	if !p.identityWarned {
		p.identityWarned = true
		p.warn(fmt.Sprintf("github: could not read the App's identity (GET /app): %v; Fugaro's own comments can't be told apart from others", err))
	}
	return "", false
}

// definite reports whether err is an answer worth remembering rather than
// a passing failure: an HTTP 4xx other than 429, with ctx still live.
func definite(ctx context.Context, err error) bool {
	var se *httpjson.StatusError
	return ctx.Err() == nil && errors.As(err, &se) && se.Status >= 400 && se.Status < 500 && se.Status != http.StatusTooManyRequests
}

// Comments implements gitprov.Provider: the review threads' comments, the
// review summaries with a body, and the general comments, oldest first.
func (p *Provider) Comments(ctx context.Context, number int) ([]gitprov.Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("listing the comments of pull request #%d: not a pull request number", number)
	}
	slug, known := p.appSlug(ctx)
	inline, err := p.threadComments(ctx, number, slug, known)
	if err != nil {
		return nil, err
	}
	reviews, err := p.reviewComments(ctx, number, slug, known)
	if err != nil {
		return nil, err
	}
	general, err := p.issueComments(ctx, number, slug, known)
	if err != nil {
		return nil, err
	}
	all := append(append(inline, reviews...), general...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })
	return all, nil
}

type threadsPage struct {
	Repository *struct {
		PullRequest *struct {
			ReviewThreads struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					IsResolved bool   `json:"isResolved"`
					IsOutdated bool   `json:"isOutdated"`
					Path       string `json:"path"`
					Line       *int   `json:"line"` // null when the thread is outdated
					Comments   struct {
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID                string    `json:"id"`
							Body              string    `json:"body"`
							CreatedAt         time.Time `json:"createdAt"`
							URL               string    `json:"url"`
							AuthorAssociation string    `json:"authorAssociation"`
							Author            *struct {
								Typename   string `json:"__typename"`
								Login      string `json:"login"`
								DatabaseID int64  `json:"databaseId"`
							} `json:"author"` // null for a deleted account
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"nodes"`
			} `json:"reviewThreads"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

func (p *Provider) threadComments(ctx context.Context, number int, slug string, known bool) ([]gitprov.Comment, error) {
	var out []gitprov.Comment
	var after *string
	for pages := 0; ; pages++ {
		if pages == maxPages {
			return nil, fmt.Errorf("listing the review threads of pull request #%d: more than %d pages", number, maxPages)
		}
		var page threadsPage
		vars := map[string]any{"owner": p.owner, "name": p.repo, "n": number, "after": after}
		if err := p.graphqlDo(ctx, reviewThreadsQuery, vars, &page); err != nil {
			return nil, fmt.Errorf("listing the review threads of pull request #%d: %w", number, err)
		}
		if page.Repository == nil || page.Repository.PullRequest == nil {
			return nil, fmt.Errorf("listing the review threads of pull request #%d: no such pull request", number)
		}
		threads := page.Repository.PullRequest.ReviewThreads
		for _, t := range threads.Nodes {
			line := 0
			if t.Line != nil {
				line = *t.Line
			}
			for _, c := range t.Comments.Nodes {
				cm := gitprov.Comment{
					ID: c.ID, Kind: gitprov.CommentInline, Collaborator: collaborator(c.AuthorAssociation), SelfKnown: known,
					Resolved: t.IsResolved, Outdated: t.IsOutdated, Truncated: t.Comments.PageInfo.HasNextPage,
					Path: t.Path, Line: line, Body: c.Body, CreatedAt: c.CreatedAt, URL: c.URL,
				}
				if a := c.Author; a != nil {
					cm.Author = a.Login
					if a.DatabaseID != 0 {
						cm.AuthorID = strconv.FormatInt(a.DatabaseID, 10)
					}
					// GraphQL names the App's bot by its bare slug.
					cm.Self = known && a.Typename == "Bot" && a.Login == slug
				}
				out = append(out, cm)
			}
		}
		if !threads.PageInfo.HasNextPage {
			return out, nil
		}
		if threads.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("listing the review threads of pull request #%d: a next page without a cursor", number)
		}
		cursor := threads.PageInfo.EndCursor
		after = &cursor
	}
}

// listAll reads every page of a REST listing, following the Link header
// on the API's own host only, and at most maxPages pages.
func listAll[T any](ctx context.Context, c *httpjson.Client, what, path string) ([]T, error) {
	var all []T
	for pages := 0; path != ""; pages++ {
		if pages == maxPages {
			return nil, fmt.Errorf("listing %s: more than %d pages", what, maxPages)
		}
		var page []T
		next, err := c.DoPage(ctx, "GET", path, nil, &page)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", what, err)
		}
		all = append(all, page...)
		path = next
	}
	return all, nil
}

// restFields are the REST fields a review or an issue comment share.
type restFields struct {
	kind        gitprov.CommentKind
	nodeID      string
	id          int64
	user        *restUser
	association string
	body        string
	created     time.Time
	url         string
}

// restComment builds a Comment from REST fields. The App's bot is
// <slug>[bot] in the REST API.
func restComment(f restFields, slug string, known bool) gitprov.Comment {
	id := f.nodeID
	if id == "" {
		id = strconv.FormatInt(f.id, 10)
	}
	return gitprov.Comment{
		ID: id, Kind: f.kind, Author: f.user.login(), AuthorID: f.user.id(), Collaborator: collaborator(f.association),
		Self: known && f.user != nil && f.user.Login == slug+"[bot]", SelfKnown: known,
		Body: f.body, CreatedAt: f.created, URL: f.url,
	}
}

func (p *Provider) reviewComments(ctx context.Context, number int, slug string, known bool) ([]gitprov.Comment, error) {
	type review struct {
		ID                int64      `json:"id"`
		NodeID            string     `json:"node_id"`
		Body              string     `json:"body"`
		State             string     `json:"state"`
		SubmittedAt       *time.Time `json:"submitted_at"` // null while pending
		HTMLURL           string     `json:"html_url"`
		User              *restUser  `json:"user"`
		AuthorAssociation string     `json:"author_association"`
	}
	list, err := listAll[review](ctx, p.api, fmt.Sprintf("the reviews of pull request #%d", number),
		p.repoPath(fmt.Sprintf("/pulls/%d/reviews?per_page=100", number)))
	if err != nil {
		return nil, err
	}
	var out []gitprov.Comment
	for _, r := range list {
		if r.Body == "" || r.State == "PENDING" || r.SubmittedAt == nil {
			continue // no summary, or not submitted
		}
		out = append(out, restComment(restFields{kind: gitprov.CommentReview, nodeID: r.NodeID, id: r.ID, user: r.User,
			association: r.AuthorAssociation, body: r.Body, created: *r.SubmittedAt, url: r.HTMLURL}, slug, known))
	}
	return out, nil
}

func (p *Provider) issueComments(ctx context.Context, number int, slug string, known bool) ([]gitprov.Comment, error) {
	type comment struct {
		ID                int64     `json:"id"`
		NodeID            string    `json:"node_id"`
		Body              string    `json:"body"`
		CreatedAt         time.Time `json:"created_at"`
		HTMLURL           string    `json:"html_url"`
		User              *restUser `json:"user"`
		AuthorAssociation string    `json:"author_association"`
	}
	list, err := listAll[comment](ctx, p.api, fmt.Sprintf("the comments of pull request #%d", number),
		p.repoPath(fmt.Sprintf("/issues/%d/comments?per_page=100", number)))
	if err != nil {
		return nil, err
	}
	out := make([]gitprov.Comment, 0, len(list))
	for _, c := range list {
		out = append(out, restComment(restFields{kind: gitprov.CommentGeneral, nodeID: c.NodeID, id: c.ID, user: c.User,
			association: c.AuthorAssociation, body: c.Body, created: c.CreatedAt, url: c.HTMLURL}, slug, known))
	}
	return out, nil
}
