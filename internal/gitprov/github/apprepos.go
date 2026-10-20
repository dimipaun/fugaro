package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/task"
)

// listingPageSize is what the two listings ask for per page: GitHub's own
// maximum. A project with more repositories than one page is paged through,
// never truncated.
const listingPageSize = 100

// maxListingPages bounds both listings. The cap is not about cost: an
// installation whose Link header never ends would otherwise make this call
// loop forever inside a command that a person is waiting on.
const maxListingPages = 100

// appRepoPermissions is what the installation token this listing mints asks
// for: GET /installation/repositories needs Metadata read and nothing else.
// Asking for no permissions at all would grant the installation's *whole*
// permission set — including the code-push permissions a run's token holds —
// to a command whose only job is to read a list of names (design §4.2's
// "least privilege" for everything that is not a run).
var appRepoPermissions = map[string]string{"metadata": "read"}

// AppRepositories lists the repositories this App is installed on, across
// every installation of the App (plan task 4.1; design §4.3's "provider-side
// access: the GitHub App installed on the repository"). Names are GitHub's own
// full names ("owner/name"), lower-cased and deduplicated, sorted; two
// installations of one App may both grant one repository, and the caller reads
// the answer as a set.
//
// It is the App that is asked, not a person: the JWT signed with the App's
// private key lists the installations, and a metadata-only installation token
// reads each installation's repositories. The key stays in memory — it signs
// the JWT and no error, message or returned value carries it or a token.
func AppRepositories(ctx context.Context, o Options) ([]string, error) {
	if o.AppID == "" || o.PrivateKey == nil {
		return nil, errors.New("github: an App ID and private key are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	app := &httpjson.Client{BaseURL: strings.TrimSuffix(o.BaseURL, "/"), HTTP: o.HTTP, Auth: appAuth(o.AppID, o.PrivateKey, o.Now), Header: http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}}
	ids, err := appInstallations(ctx, app, o.AppID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		token, err := appInstallationToken(ctx, app, id)
		if err != nil {
			return nil, err
		}
		names, err := installationRepositories(ctx, app, token, id)
		if err != nil {
			return nil, err
		}
		out = append(out, names...)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// appInstallations pages GET /app/installations as the App.
func appInstallations(ctx context.Context, app *httpjson.Client, appID string) ([]int64, error) {
	var out []int64
	path := fmt.Sprintf("/app/installations?per_page=%d", listingPageSize)
	for i := 0; i < maxListingPages; i++ {
		var page []struct {
			ID int64 `json:"id"`
		}
		next, err := app.DoPage(ctx, "GET", path, nil, &page)
		if err != nil {
			if refusedCredentials(err) {
				return nil, &BadCredentialsError{AppID: appID}
			}
			return nil, fmt.Errorf("listing the github app installations: %w", err)
		}
		for _, inst := range page {
			if inst.ID != 0 {
				out = append(out, inst.ID)
			}
		}
		if next == "" {
			return out, nil
		}
		path = next
	}
	return nil, fmt.Errorf("listing the github app installations: more than %d pages", maxListingPages)
}

// appInstallationToken mints one installation token for every repository the
// installation grants, with appRepoPermissions and nothing else.
func appInstallationToken(ctx context.Context, app *httpjson.Client, id int64) (string, error) {
	var tok struct {
		Token string `json:"token"`
	}
	if err := app.Do(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", id), map[string]any{"permissions": appRepoPermissions}, &tok); err != nil {
		return "", fmt.Errorf("minting an installation token for installation %d: %w", id, err)
	}
	if tok.Token == "" {
		return "", fmt.Errorf("github returned an empty installation token for installation %d", id)
	}
	return tok.Token, nil
}

// installationRepositories pages GET /installation/repositories with the
// installation's own token (that endpoint authenticates as the installation,
// not as the App).
func installationRepositories(ctx context.Context, app *httpjson.Client, token string, id int64) ([]string, error) {
	inst := &httpjson.Client{BaseURL: app.BaseURL, HTTP: app.HTTP, Header: app.Header, Auth: func(context.Context) (string, error) {
		return "Bearer " + token, nil
	}}
	var out []string
	path := fmt.Sprintf("/installation/repositories?per_page=%d", listingPageSize)
	for i := 0; i < maxListingPages; i++ {
		var page struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		next, err := inst.DoPage(ctx, "GET", path, nil, &page)
		if err != nil {
			return nil, fmt.Errorf("listing the repositories of github app installation %d: %w", id, err)
		}
		for _, r := range page.Repositories {
			// ASCII-only folding (task.ASCIILower), not strings.ToLower: every
			// name this listing returns is compared against — and offered to an
			// operator as — a repository in task's canonical form, which folds
			// A–Z and nothing else. Unicode folding merges names that are not
			// one name (a Kelvin sign K becomes an ASCII "k"), so a repository
			// GitHub returns with one would come out of here spelled as a
			// repository nobody has, and the next comparison would not find it.
			if name := task.ASCIILower(strings.TrimSpace(r.FullName)); name != "" {
				out = append(out, name)
			}
		}
		if next == "" {
			return out, nil
		}
		path = next
	}
	return nil, fmt.Errorf("listing the repositories of github app installation %d: more than %d pages", id, maxListingPages)
}

// refusedCredentials reports whether err is GitHub refusing the App's own JWT:
// 401, which is the private key not being that App's (or a badly wrong clock).
func refusedCredentials(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnauthorized
}
