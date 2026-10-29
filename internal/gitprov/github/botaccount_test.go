package github

// The App's bot account, which is what a run's commits are attributed to
// (docs/git-providers.md). It is read with the installation token the run
// already has -- the same read, and the same strictness, as the identity
// behind Self (service_identity_test.go) -- and the fixtures here are the
// ones that read already uses: the App's own bot, an answer that names a
// person instead, and a refusal.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestBotAccountIsTheAppsOwnBot: the answer names the App's bot account, and
// both halves of it are what the commit identity is built from -- the numeric
// user ID and the login GitHub spells the bot by.
func TestBotAccountIsTheAppsOwnBot(t *testing.T) {
	got, err := openService(t, "bot_account.json", nil).BotAccount(ctx)
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
	got, err := openService(t, "bot_account_not_a_bot.json", nil).BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
}

// TestBotAccountReportsARefusedRead: a read the token may not make is an
// error the runner logs and falls back from -- never a guessed identity.
func TestBotAccountReportsARefusedRead(t *testing.T) {
	got, err := openService(t, "bot_account_refused.json", nil).BotAccount(ctx)
	if err == nil || got != (gitprov.BotAccount{}) {
		t.Fatalf("BotAccount = %+v, %v, want no account and an error", got, err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error is %q, want it to name the refusal's status", err)
	}
}

// TestBotAccountAsksWithTheRunsOwnToken: the read is GET /user, made with the
// installation token the run holds and never with an App JWT -- this provider
// has no key at all, and the fixture holds no other request, so a read that
// went anywhere else would fail the test (httpfixture refuses an unheld
// request).
func TestBotAccountAsksWithTheRunsOwnToken(t *testing.T) {
	p := openService(t, "bot_account.json", nil)
	if p.app != nil {
		t.Fatal("the fixture's provider holds an App key")
	}
	if _, err := p.BotAccount(ctx); err != nil {
		t.Fatalf("BotAccount: %v", err)
	}
}
