package github

// Fugaro's own identity when the App's key is the credentials service's
// (C2.2). This process holds no JWT, and there is no other route to the
// App's own identity: GitHub refuses an installation token GET /user
// ("Resource not accessible by integration"), and the endpoints an
// installation access token may call hold nothing that names the App
// (docs.github.com/en/rest/authentication/endpoints-available-for-github-app-installation-access-tokens;
// GET /installation/repositories lists the installation's repositories, not
// the App, and GET /apps/{app_slug} needs the slug it would return). So the
// identity is unknown in every run below, and Self is false rather than a
// guess: a run under the service cannot tell its own comments from anyone
// else's, which is the fail-closed direction -- review mode's trusted-author
// gate then wants a collaborator (reviewmode.go), and a follow-up trusts no
// marker from an author it cannot name.
//
// httpfixture is what proves the "no route" half: these fixtures hold no
// identity read at all, and httpfixture fails the test on a request it does
// not hold, so a provider that tried GET /user -- or any other route to the
// App -- would fail here rather than quietly read its 403.
//
// The two wants pin Base: "main" as well, which these fixtures have carried
// all along: FetchPRHead reads the pull request's base branch (the review
// stage diffs against it, not against a hardcoded main), so the whole struct
// is what the identity read returns, not the identity fields alone.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// serviceFixtureToken is the installation token the service minted in these
// fixtures: what the fake source hands the provider, and what the fixture's
// exchanges expect to be authenticated with.
const serviceFixtureToken = "ghs_fixture_service_token"

// fixtureSource is a stand-in for github.ServiceTokens: the run's
// installation token, from wherever the service put it.
type fixtureSource struct{}

func (fixtureSource) Token(context.Context, time.Duration) (string, time.Time, error) {
	return serviceFixtureToken, time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC), nil
}

// openService is open for a provider whose App key is the credentials
// service's (C2.2): New is given no key, and the installation token the run
// holds is installed as the provider's token source, exactly as the runner
// installs it.
func openService(t *testing.T, fixture string, warn func(string)) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", ServiceHeldKey: true, BaseURL: srv.URL, Warn: warn,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if p.app != nil {
		t.Fatal("a service-held provider built an App-authenticated client")
	}
	p.UseServiceTokens(fixtureSource{})
	return p
}

// TestGitHubSelfFailsClosedWithTheService: a review run on Fugaro's own pull
// request with the service on must still read Self=false. The fixture's
// author login ("fugaro-app[bot]") is exactly what the App's own bot would
// be, and the run cannot confirm it: no JWT, no route to the App, so no slug
// to compare the login against. It is warned about once, naming the reason,
// and no request is made for it -- the fixture holds none, and httpfixture
// fails the test on a request it does not hold.
//
// Mutation (return a slug from appSlug whenever p.app is nil, or trust every
// "...[bot]" author when the identity is unknown): this test fails, wanting
// Self=false and getting true.
func TestGitHubSelfFailsClosedWithTheService(t *testing.T) {
	var warns []string
	got, err := openService(t, "pull_open_head_service.json", func(w string) { warns = append(warns, w) }).FetchPRHead(ctx, 12)
	want := gitprov.PRHead{SHA: headSHA, Base: "main", Fork: false, Open: true, AuthorID: "900001", AuthorLogin: "fugaro-app[bot]",
		Association: "NONE", SourceRepo: "acme/web"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "no App key in this process") ||
		!strings.Contains(warns[0], "can't be told apart") {
		t.Fatalf("warnings = %q, want one naming the missing key and what it costs", warns)
	}
}

// TestGitHubSelfFalseForSomeoneElsesPRWithTheService: the same run on another
// bot's pull request ("someone-else[bot]") reads Self=false too. The identity
// is unknown here, so the login's shape decides nothing either way: a
// "...[bot]" login is never Fugaro's own on its own.
//
// Mutation (fall back to the login's shape, `strings.HasSuffix(login,
// "[bot]")`, when the slug comparison does not match): this test fails,
// wanting Self=false and getting true.
func TestGitHubSelfFalseForSomeoneElsesPRWithTheService(t *testing.T) {
	got, err := openService(t, "pull_other_author_head_service.json", nil).FetchPRHead(ctx, 12)
	want := gitprov.PRHead{SHA: headSHA, Base: "main", Fork: false, Open: true, AuthorID: "900002", AuthorLogin: "someone-else[bot]",
		Association: "NONE", SourceRepo: "acme/web"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}
}

// TestGitHubCommentsSelfUnknownWithTheService: the same unknown identity
// answers the comment read, which is what a follow-up trusts a marker by.
// The comments are the ones TestGitHubCommentsSelf uses -- the App's bot as
// REST spells it, as GraphQL spells it, a person whose login is the slug, and
// another bot -- and with no route to the App every one of them comes back
// Self=false, SelfKnown=false, so a marker is honoured from none of them.
//
// Mutation (return a slug from appSlug whenever p.app is nil): this test
// fails, one of the five coming back Self=true.
func TestGitHubCommentsSelfUnknownWithTheService(t *testing.T) {
	got, err := openService(t, "comments_self_service.json", nil).Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PRRC_21": false, "PRRC_22": false, "PRRC_23": false, "IC_kwDOA621": false, "IC_kwDOA622": false}
	if len(got) != len(want) {
		t.Fatalf("comments = %+v", got)
	}
	for _, c := range got {
		if c.SelfKnown {
			t.Errorf("%s: SelfKnown is true with no route to the App's identity", c.ID)
		}
		if c.Self {
			t.Errorf("%s: Self is true with no route to the App's identity", c.ID)
		}
		if _, ok := want[c.ID]; !ok {
			t.Errorf("%s: not a comment of this fixture", c.ID)
		}
	}
}

// TestGitHubIdentityWithoutTheAppKeyAsksNothing: with the App's key in the
// credentials service there is no route to the App's identity, so the
// provider asks for nothing at all -- the server here fails the test on any
// request -- and the answer is warned about once, however often it is asked
// for.
//
// Mutation (fall back to GET /user, or any other request, in readAppSlug's
// no-key branch): this test fails on the request it did not expect.
func TestGitHubIdentityWithoutTheAppKeyAsksNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the provider sent %s %s with no App key to read the identity with", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	var warns []string
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", ServiceHeldKey: true, BaseURL: srv.URL,
		Warn: func(w string) { warns = append(warns, w) }, Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	p.UseServiceTokens(fixtureSource{})
	for range 2 {
		if slug, ok := p.appSlug(ctx); ok || slug != "" {
			t.Fatalf("slug = %q, %t with no App key in this process", slug, ok)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %q, want one", warns)
	}
}
