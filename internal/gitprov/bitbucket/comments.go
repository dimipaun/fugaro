package bitbucket

// The follow-up reads of a pull request's comments. The shapes, and the
// test fixtures, follow Bitbucket Cloud's documentation:
//
//   - comments: https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-comments-get
//   - paging (the body's "next"): https://developer.atlassian.com/cloud/bitbucket/rest/intro/#pagination
//   - the token's user: https://developer.atlassian.com/cloud/bitbucket/rest/api-group-users/#api-user-get
//
// No live check has confirmed these yet. Each field marked "unverified
// against the live API" is hand-written from the documentation until the
// live follow-up check (live_test.go, followUpReads) records it: in
// bitbucket.go, is_private, author.account_id and source.commit.hash; here,
// resolution, inline.outdated, deleted, pending, user.uuid, user.account_id
// and user.display_name, a reply's missing inline field, and whether
// GET /user answers a repository access token.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// maxPages bounds the comment listing: a pull request with more pages
// than this is refused rather than read without end.
const maxPages = 20

// user is an account in a Bitbucket response; null for a deleted one.
type user struct {
	UUID        string `json:"uuid"`         // unverified against the live API
	AccountID   string `json:"account_id"`   // unverified against the live API
	DisplayName string `json:"display_name"` // unverified against the live API
}

func (u *user) accountID() string {
	if u == nil {
		return ""
	}
	return u.AccountID
}

func (u *user) displayName() string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

// inline is where an inline comment sits: To is the line in the new
// file, From the line in the old one (for a removed line); either may be
// null.
type inline struct {
	Path     string `json:"path"`
	From     *int   `json:"from"`
	To       *int   `json:"to"`
	Outdated bool   `json:"outdated"` // unverified against the live API
}

func (in *inline) line() int {
	switch {
	case in.To != nil:
		return *in.To
	case in.From != nil:
		return *in.From
	}
	return 0
}

type comment struct {
	ID        int       `json:"id"`
	CreatedOn time.Time `json:"created_on"`
	Content   struct {
		Raw string `json:"raw"`
	} `json:"content"`
	User    *user   `json:"user"`
	Inline  *inline `json:"inline"`
	Deleted bool    `json:"deleted"` // unverified against the live API
	Pending bool    `json:"pending"` // unverified against the live API: an unpublished draft comment
	Parent  *struct {
		ID int `json:"id"`
	} `json:"parent"`
	// Resolution is set on a resolved thread's first comment, and null
	// (or absent) otherwise. Unverified against the live API.
	Resolution json.RawMessage `json:"resolution"`
	Links      struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func (c *comment) resolved() bool {
	return len(c.Resolution) > 0 && string(c.Resolution) != "null"
}

// selfUser returns the uuid of the token's user, reading it with GET /user
// the first time. known is false when the API refused to say (a 4xx, such
// as a 403 for a token that may not read its user): that answer is kept
// for the Provider's lifetime and warned about once. Any other failure (a
// 5xx, a network error, a cancelled context) is returned and not kept, so
// the next listing tries again: a blip must not make every comment look
// like someone else's.
func (p *Provider) selfUser(ctx context.Context) (uuid string, known bool, err error) {
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	if p.identityDone {
		return p.selfUUID, p.selfUUID != "", nil
	}
	var u user
	err = p.api.Do(ctx, "GET", "/user", nil, &u)
	var se *httpjson.StatusError
	switch {
	case err == nil && u.UUID != "":
		p.identityDone, p.selfUUID = true, u.UUID
	case err == nil:
		p.identityDone = true
		p.o.Warn("bitbucket: the token's user (GET /user) has no uuid; Fugaro's own comments can't be told apart from others")
	case errors.As(err, &se) && se.Status >= 400 && se.Status < 500 &&
		se.Status != http.StatusRequestTimeout && se.Status != http.StatusTooManyRequests:
		p.identityDone = true
		p.o.Warn(fmt.Sprintf("bitbucket: could not read the token's user (GET /user): %v; Fugaro's own comments can't be told apart from others", err))
	default:
		return "", false, fmt.Errorf("reading the token's user: %w", err)
	}
	return p.selfUUID, p.selfUUID != "", nil
}

// Comments implements gitprov.Provider: every published comment on the
// pull request, oldest first. Bitbucket has no review summaries, so no
// comment is a gitprov.CommentReview. Collaborator is always true:
// Bitbucket has no author association, and followup.trusted is its only
// author rule.
func (p *Provider) Comments(ctx context.Context, number int) ([]gitprov.Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("listing the comments of pull request #%d: not a pull request number", number)
	}
	selfUUID, known, err := p.selfUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the comments of pull request #%d: %w", number, err)
	}
	list, err := p.listComments(ctx, number)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]*comment, len(list))
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	out := make([]gitprov.Comment, 0, len(list))
	for i := range list {
		c := &list[i]
		if c.Pending {
			continue // an unpublished draft, visible to its author only
		}
		root := threadRoot(c, byID)
		cm := gitprov.Comment{
			ID: strconv.Itoa(c.ID), Kind: gitprov.CommentGeneral,
			Author: c.User.displayName(), AuthorID: c.User.accountID(), Collaborator: true,
			Self:      known && c.User != nil && c.User.UUID == selfUUID,
			SelfKnown: known,
			Deleted:   c.Deleted,
			Body:      c.Content.Raw, CreatedAt: c.CreatedOn.UTC(), URL: c.Links.HTML.Href,
		}
		// A reply belongs to its thread: one without an inline field of
		// its own takes its thread's.
		in := c.Inline
		if in == nil {
			in = root.Inline
		}
		if in != nil {
			cm.Kind, cm.Path, cm.Line, cm.Outdated = gitprov.CommentInline, in.Path, in.line(), in.Outdated
			cm.Resolved = c.resolved() || root.resolved()
		}
		out = append(out, cm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// threadRoot follows c's parent chain to the first comment of its thread.
// A parent missing from the listing ends the chain where it is; a loop
// (which the API should never return) ends it too.
func threadRoot(c *comment, byID map[int]*comment) *comment {
	seen := map[int]bool{c.ID: true}
	for c.Parent != nil {
		parent, ok := byID[c.Parent.ID]
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		c = parent
	}
	return c
}

// listComments reads every page of the pull request's comments, following
// the body's "next" only on the API's own scheme and host, and at most
// maxPages pages.
func (p *Provider) listComments(ctx context.Context, number int) ([]comment, error) {
	var all []comment
	path := p.prPath(fmt.Sprintf("/%d/comments?pagelen=100", number))
	for pages := 0; path != ""; pages++ {
		if pages == maxPages {
			return nil, fmt.Errorf("listing the comments of pull request #%d: more than %d pages", number, maxPages)
		}
		var page struct {
			Values []comment `json:"values"`
			Next   string    `json:"next"`
		}
		if err := p.api.Do(ctx, "GET", path, nil, &page); err != nil {
			return nil, fmt.Errorf("listing the comments of pull request #%d: %w", number, err)
		}
		all = append(all, page.Values...)
		path = ""
		if page.Next != "" {
			next, err := p.api.PagePath(page.Next)
			if err != nil {
				return nil, fmt.Errorf("listing the comments of pull request #%d: %w", number, err)
			}
			path = next
		}
	}
	return all, nil
}
