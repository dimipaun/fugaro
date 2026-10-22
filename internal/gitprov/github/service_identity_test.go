package github

// Fugaro's own identity when the App's key is the credentials service's
// (C2.2). This process holds no JWT to read GET /app with, so the identity
// comes from the installation token the run already has: GET /user answers
// an installation token with the bot account it acts as, and that account's
// login is <slug>[bot] -- the same slug GraphQL names the bot by, and the
// one every Self comparison uses.
//
// The three answers that matter are here: the bot's own pull request reads
// as Self, someone else's does not, and an identity that could not be
// established (a refusal, or an answer that is not a bot account at all) is
// Self=false rather than a guess.
//
// The two wants pin Base: "main" as well, which these fixtures have carried
// all along: FetchPRHead reads the pull request's base branch (the review
// stage diffs against it, not against a hardcoded main), so the whole struct
// is what the identity read returns, not the identity fields alone.

import (
	"context"
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

// TestGitHubSelfFromTheRunsOwnToken: with the service on, a review run on
// Fugaro's own pull request must still read Self=true. Its identity is read
// with the run's own installation token (GET /user), so no App key is
// involved and no GET /app is made -- the fixture has none, and httpfixture
// fails the test on a request it does not hold.
//
// Mutation (return "unknown" from appSlug whenever p.app is nil, the
// behaviour before this): this test fails, wanting Self=true and getting
// false.
func TestGitHubSelfFromTheRunsOwnToken(t *testing.T) {
	got, err := openService(t, "pull_open_head_service.json", nil).FetchPRHead(ctx, 12)
	want := gitprov.PRHead{SHA: headSHA, Base: "main", Fork: false, Open: true, AuthorID: "900001", AuthorLogin: "fugaro-app[bot]",
		Association: "NONE", Self: true, SourceRepo: "acme/web"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}
}

// TestGitHubSelfFalseForSomeoneElsesPRWithTheService: the same identity read
// must not make Self true for anyone else. The author here is another bot
// ("someone-else[bot]"), which is the shape the exact-slug comparison exists
// to refuse: a "...[bot]" login is not Fugaro's own.
//
// Mutation (compare only the "[bot]" suffix instead of the whole login):
// this test fails, wanting Self=false and getting true.
func TestGitHubSelfFalseForSomeoneElsesPRWithTheService(t *testing.T) {
	got, err := openService(t, "pull_other_author_head_service.json", nil).FetchPRHead(ctx, 12)
	want := gitprov.PRHead{SHA: headSHA, Base: "main", Fork: false, Open: true, AuthorID: "900002", AuthorLogin: "someone-else[bot]",
		Association: "NONE", SourceRepo: "acme/web"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}
}

// TestGitHubSelfFailsClosedWhenTheServiceIdentityIsRefused: a GET /user the
// token may not read is the identity unknown, and Self must then be false
// even though this fixture's author login is exactly what the App's own bot
// would be. It is remembered (a 4xx is definite) and warned about once, like
// every other failed identity read.
//
// Mutation (treat a failed read as Self=true, or as every "...[bot]" being
// Fugaro's): this test fails.
func TestGitHubSelfFailsClosedWhenTheServiceIdentityIsRefused(t *testing.T) {
	var warns []string
	got, err := openService(t, "pull_open_head_service_identity_refused.json", func(w string) { warns = append(warns, w) }).FetchPRHead(ctx, 12)
	if err != nil || got.Self {
		t.Fatalf("got %+v, %v, want Self=false (the run's identity could not be confirmed)", got, err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "GET /user") || !strings.Contains(warns[0], "403") {
		t.Fatalf("warnings = %q, want one naming GET /user and its 403", warns)
	}
}

// TestGitHubCommentsSelfWithTheService: the same identity answers the
// comment read, which is what a follow-up trusts a marker by. The comments
// and their expected Self are the ones TestGitHubCommentsSelf uses (the App's
// bot as REST spells it, as GraphQL spells it, a person whose login is the
// slug, and another bot); only where the slug comes from is different.
//
// Mutation (return "unknown" from appSlug whenever p.app is nil): this test
// fails, every comment coming back SelfKnown=false.
func TestGitHubCommentsSelfWithTheService(t *testing.T) {
	got, err := openService(t, "comments_self_service.json", nil).Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	self := map[string]bool{}
	for _, c := range got {
		if !c.SelfKnown {
			t.Errorf("%s: SelfKnown is false", c.ID)
		}
		self[c.ID] = c.Self
	}
	want := map[string]bool{"PRRC_21": true, "PRRC_22": false, "PRRC_23": false, "IC_kwDOA621": true, "IC_kwDOA622": false}
	if len(self) != len(want) {
		t.Fatalf("self = %v", self)
	}
	for id, w := range want {
		if self[id] != w {
			t.Errorf("%s: Self = %t, want %t", id, self[id], w)
		}
	}
}

// TestGitHubSelfFailsClosedWhenTheServiceAnswerIsNotABot: an answer that is
// not a bot account is not the App's identity, whatever its login is
// spelled like. This fixture answers 200 with an ordinary user account whose
// login is the App's bare slug -- the shape a naive TrimSuffix would accept
// and then compare a "<slug>[bot]" author against, making Self true for an
// account that is not the App's.
//
// Mutation (drop the Type != "Bot" check): this test fails, finding
// Self=true.
func TestGitHubSelfFailsClosedWhenTheServiceAnswerIsNotABot(t *testing.T) {
	got, err := openService(t, "pull_open_head_service_not_a_bot.json", nil).FetchPRHead(ctx, 12)
	if err != nil || got.Self {
		t.Fatalf("got %+v, %v, want Self=false (the answer is not the App's bot account)", got, err)
	}
}
