package bitbucket

// Listing the repositories a workspace holds. It is the one read in this
// package that is not about a single repository: its credential is a workspace
// or project access token, and the workspace it is scoped to is the listing's
// whole reach. `fugaro project sync` makes it to offer the repositories the
// published allow-list does not admit (D623).
//
// The shapes follow Bitbucket Cloud's documentation:
//
//   - repositories: https://developer.atlassian.com/cloud/bitbucket/rest/api-group-repositories/#api-repositories-workspace-get
//   - paging (the body's "next"): https://developer.atlassian.com/cloud/bitbucket/rest/intro/#pagination

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// listPageSize is the page size the listing asks for. It is the largest
// Bitbucket Cloud serves, so a workspace of any size is read in as few calls
// as the API allows.
const listPageSize = 100

// maxListPages bounds the listing: a workspace with more pages than this is
// refused rather than read without end. It is the comments listing's own
// bound, and for its reason: a listing that follows "next" forever is a
// command that never returns.
const maxListPages = 20

// ListOptions configure ListRepositories.
type ListOptions struct {
	// Workspace is the workspace whose repositories are listed.
	Workspace string
	// Project, when set, narrows the listing to that project of the workspace
	// (Bitbucket's project key, as the workspace's own project page spells it).
	Project string
	// Token is a Bitbucket access token -- a workspace or project one. It is
	// sent as the bearer credential of every call, and it is never quoted in
	// an error: the value is a credential the caller read from a person.
	Token string
	// BaseURL is the API root; "" means DefaultBaseURL.
	BaseURL string
	// HTTP is the client the calls go through; nil means the default one.
	HTTP *http.Client
}

// ListRepositories returns the repositories of o's workspace -- or, when o
// names one, of that project of it -- as "<workspace>/<slug>", sorted and
// deduplicated.
//
// The names are the provider's own ("acme/sandbox", the workspace-qualified
// slug a Bitbucket URL is built from), not allow-list entries: the caller is
// the one that knows the names are about to be written into a policy, and it
// is the caller's canonicalisation (projectpolicy.Canonical) that has to
// agree with the runner's. What this read owes it is the pair Bitbucket
// itself would answer with, and every call here is scoped to the workspace
// the caller named.
func ListRepositories(ctx context.Context, o ListOptions) ([]string, error) {
	if o.Workspace == "" {
		return nil, errors.New("bitbucket: a workspace is required to list its repositories")
	}
	if o.Token == "" {
		return nil, errors.New("bitbucket: an access token is required to list a workspace's repositories")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	api := &httpjson.Client{
		BaseURL: strings.TrimSuffix(o.BaseURL, "/"),
		HTTP:    o.HTTP,
		Header:  http.Header{"Accept": {"application/json"}},
		Auth:    func(context.Context) (string, error) { return "Bearer " + o.Token, nil },
	}
	q := url.Values{"pagelen": {strconv.Itoa(listPageSize)}}
	if o.Project != "" {
		// BBQL, Bitbucket's query language: the project filter is the one the
		// API documents for this endpoint. escapeBBQL is the same escaping the
		// pull-request lookup uses, so a project key cannot break out of the
		// literal and splice clauses into the filter.
		q.Set("q", `project.key="`+escapeBBQL(o.Project)+`"`)
	}
	path := "/repositories/" + url.PathEscape(o.Workspace) + "?" + q.Encode()
	var out []string
	for pages := 0; path != ""; pages++ {
		if pages == maxListPages {
			return nil, fmt.Errorf("listing the repositories of %s: more than %d pages", o.Workspace, maxListPages)
		}
		var page struct {
			Values []struct {
				Slug     string `json:"slug"`
				FullName string `json:"full_name"`
			} `json:"values"`
			Next string `json:"next"`
		}
		if err := api.Do(ctx, "GET", path, nil, &page); err != nil {
			return nil, fmt.Errorf("listing the repositories of %s: %w", o.Workspace, err)
		}
		for _, r := range page.Values {
			if name := repoName(r.FullName, r.Slug, o.Workspace); name != "" {
				out = append(out, name)
			}
		}
		path = ""
		if page.Next != "" {
			// The body's "next" is an absolute URL, and it is followed only on
			// the API's own scheme, host and path: a bearer token is on this
			// request, and a next page anywhere else would take it there.
			next, err := api.PagePath(page.Next)
			if err != nil {
				return nil, fmt.Errorf("listing the repositories of %s: %w", o.Workspace, err)
			}
			path = next
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// repoName is one listed repository's name, "<workspace>/<slug>".
//
// full_name is Bitbucket's own name for the repository and the field this
// package's other reads compare against (find), so it is preferred where the
// answer carries a well-formed one. A repository whose answer omits it is
// still listed, from the slug and the workspace the request named: dropping it
// would read to an operator as a repository the workspace does not hold.
func repoName(fullName, slug, workspace string) string {
	if parts := strings.Split(fullName, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1]
	}
	if slug == "" {
		return ""
	}
	return workspace + "/" + slug
}
