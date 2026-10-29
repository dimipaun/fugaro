package github

// The App's bot account, which is what a run's commits are attributed to
// (docs/git-providers.md). Its slug is read with the App's JWT (GET /app), as
// the identity behind Self is when the key is in this process, and the account
// by that name with the installation token (GET /users/<slug>[bot]). The
// fixtures: the App's own bot, an answer that names a person instead, a
// refusal, and a credentials-service run, which makes no read at all.

import (
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

// TestBotAccountWithoutTheKeyKeepsTheDefault: with the key in the credentials
// service there is no JWT to read the slug with, so the read fails without a
// request (the fixture holds none) and the run keeps the default identity.
//
// Mutation (fall back to GET /user with the installation token): this test
// fails on the unheld request -- the route GitHub refuses.
func TestBotAccountWithoutTheKeyKeepsTheDefault(t *testing.T) {
	got, err := openService(t, "bot_account_service.json", nil).BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
}
