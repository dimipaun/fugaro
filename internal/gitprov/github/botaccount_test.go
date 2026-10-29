package github

// The App's bot account, which is what a run's commits are attributed to
// (docs/git-providers.md). Its slug comes from comments.go's appSlug, the
// cached read the comment and review-mode identity shares (GET /app with the
// App's JWT), and the account by that name with the installation token
// (GET /users/<slug>[bot]). The fixtures: the App's own bot, an answer that
// names a person instead, an answer that names another bot, a refusal, and a
// credentials-service run, which makes no read at all.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestBotAccountIsTheAppsOwnBot: the answer names the App's bot account, and
// both halves of it are what the commit identity is built from -- the numeric
// user ID and the login GitHub spells the bot by.
func TestBotAccountIsTheAppsOwnBot(t *testing.T) {
	got, err := open(t, "bot_account.json").BotAccount(ctx)
	if want := (gitprov.BotAccount{ID: "900001", Login: "fugaro-app[bot]"}); err != nil || got != want {
		t.Fatalf("BotAccount = %+v, %v, want %+v", got, err, want)
	}
}

// TestBotAccountRefusesAnAnswerThatIsNotABot: only a bot account is this
// App's own identity. An installation token that answers with a person (or
// with anything else) is refused rather than used, because attributing a
// run's commits to that account is exactly the mistake this read exists to
// avoid.
//
// Mutation (accept any answer's id and login): this test fails, and a run
// would commit as whoever the token happened to act as.
func TestBotAccountRefusesAnAnswerThatIsNotABot(t *testing.T) {
	got, err := open(t, "bot_account_not_a_bot.json").BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
}

// TestBotAccountRefusesAnotherAppsBot: the account must be this App's own
// bot, and exactly it. The fixture's answer is another bot's login, which a
// suffix check accepts -- and the login is what the commit identity is built
// from, so accepting it commits as an account that owns nothing of this
// App's.
//
// Mutation (compare with strings.HasSuffix(login, "[bot]") instead of
// equality with the slug read): this test fails, and a run would commit as
// other-app[bot] rather than fugaro-app[bot].
func TestBotAccountRefusesAnotherAppsBot(t *testing.T) {
	got, err := open(t, "bot_account_other_bot.json").BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
	if !strings.Contains(err.Error(), "other-app[bot]") {
		t.Errorf("the error is %q, want it to name the login it refused", err)
	}
}

// TestBotAccountReportsARefusedRead: a read the token may not make is an
// error the runner logs and falls back from -- never a guessed identity.
func TestBotAccountReportsARefusedRead(t *testing.T) {
	got, err := open(t, "bot_account_refused.json").BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error is %q, want it to name the refusal's status", err)
	}
}

// TestBotAccountAsksWithTheRunsOwnToken: the slug is read with the App's JWT
// and the account with the installation token the run holds; the fixture
// holds exactly those requests in that order, each with its own credential,
// and httpfixture refuses any other. GET /user is not among them: GitHub
// refuses it to an installation token.
func TestBotAccountAsksWithTheRunsOwnToken(t *testing.T) {
	p := open(t, "bot_account.json")
	if p.app == nil {
		t.Fatal("the fixture's provider holds no App key")
	}
	if _, err := p.BotAccount(ctx); err != nil {
		t.Fatalf("BotAccount: %v", err)
	}
}

// TestBotAccountSharesTheSlugReadWithTheCommentRead: the App's slug is read
// once per Provider (appSlug's own cache), so a run that reads its bot
// account at open and then asks for the slug again -- which is what the
// comment and review-mode reads do -- sends one GET /app, not two.
//
// Mutation (read the slug with readAppSlug from BotAccount, as before): this
// test fails, wanting one GET /app and getting two.
func TestBotAccountSharesTheSlugReadWithTheCommentRead(t *testing.T) {
	apps := 0
	p := fakeAPIWarn(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/app":
			apps++
			w.Write([]byte(`{"slug":"fugaro-app"}`))
			return true
		case "/users/fugaro-app[bot]":
			w.Write([]byte(`{"login":"fugaro-app[bot]","id":900001,"type":"Bot"}`))
			return true
		}
		return false
	}, nil)
	if _, err := p.BotAccount(ctx); err != nil {
		t.Fatalf("BotAccount: %v", err)
	}
	if slug, ok := p.appSlug(ctx); !ok || slug != "fugaro-app" {
		t.Fatalf("slug = %q, %t after BotAccount read it", slug, ok)
	}
	if apps != 1 {
		t.Fatalf("GET /app was sent %d times, want one per Provider", apps)
	}
}

// TestBotAccountWithoutTheKeyKeepsTheDefault: with the key in the credentials
// service there is no JWT to read the slug with, and no other route to it, so
// the read fails without a request (the fixture holds none) and the run keeps
// the default identity.
//
// Mutation (fall back to GET /user with the installation token): this test
// fails on the unheld request -- the route GitHub refuses.
func TestBotAccountWithoutTheKeyKeepsTheDefault(t *testing.T) {
	got, err := openService(t, "bot_account_service.json", nil).BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
}
