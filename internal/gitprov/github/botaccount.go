package github

// The App's own bot account, which is what a run's commits are attributed to:
// GitHub links a commit's author and committer to accounts separately, each
// when its address is that account's noreply identity (docs/git-providers.md).
//
// The App's slug comes from comments.go's appSlug -- the same cached read
// (GET /app with the App's JWT) that the comment and review-mode identity
// uses, so a run makes one GET /app and not two -- and the account behind it
// is then read by name, because an installation token cannot read the App's
// own identity itself: GitHub refuses it GET /user ("Resource not accessible
// by integration") and no endpoint it may call names the App (readAppSlug).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// BotAccount implements gitprov.BotAccountReader: the account this App's
// commits are linked to.
//
// The read is two steps: the App's slug from appSlug (GET /app with the App's
// JWT, read once per Provider), then GET /users/<slug>[bot] with the
// installation token the run already has, for the bot account's numeric ID.
// Without the App's key in this process (the credentials service holds it)
// there is no JWT and so no slug, and the read fails: the run keeps the
// default identity. It is deliberately strict about the answer, for the same
// reason appSlug is: only a bot account is this App's own identity, and it
// must be this App's own bot -- the login exactly "<slug>[bot]" -- because an
// answer naming anything else would attribute a run's commits to an account
// that is not the App's.
//
// It is not cached: the runner asks once per run, before the first commit.
func (p *Provider) BotAccount(ctx context.Context) (gitprov.BotAccount, error) {
	slug, known := p.appSlug(ctx)
	if !known {
		// appSlug has already warned with the reason it could not read the
		// App's identity, and the runner logs this error beside it.
		return gitprov.BotAccount{}, errors.New("reading the App's bot account: the App's identity could not be read")
	}
	route := "GET /users/" + slug + botSuffix
	var bot struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := p.api.Do(ctx, "GET", "/users/"+url.PathEscape(slug+botSuffix), nil, &bot); err != nil {
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): %w", route, err)
	}
	switch {
	case bot.Type != "Bot":
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): the answer names a %s, not a bot account: %w", route, bot.Type, errNoIdentity)
	case bot.ID <= 0:
		// A bot without a numeric ID has no noreply address to be linked by.
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): the bot account %q has no id to be linked by: %w", route, bot.Login, errNoIdentity)
	case bot.Login != slug+botSuffix:
		// The answer must be this App's own bot, and exactly it: a login that
		// merely ends in "[bot]" is some other account's, which would make the
		// run commit as an identity that owns nothing of this App's.
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): the answer names %q, not this App's bot %q: %w", route, bot.Login, slug+botSuffix, errNoIdentity)
	}
	return gitprov.BotAccount{ID: strconv.FormatInt(bot.ID, 10), Login: bot.Login}, nil
}

var _ gitprov.BotAccountReader = (*Provider)(nil)
