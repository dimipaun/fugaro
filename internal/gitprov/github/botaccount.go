package github

// The App's own bot account, which is what a run's commits are attributed to:
// GitHub links a commit to the bot only when the commit's author and
// committer are that account's noreply identity (docs/git-providers.md).
//
// It is a read of the same account comments.go's appSlug reads, for a
// different question: appSlug wants the slug a comment's author is compared
// with, and this wants the numeric user ID and the login a commit's headers
// are built from.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// BotAccount implements gitprov.BotAccountReader: the account this App's
// commits are linked to.
//
// The read is GET /user with the installation token the run already has --
// the one route that answers whether this process holds the App's key or the
// installation's credentials service does, and the route appSlug takes when
// the key is the service's. It is deliberately strict about the answer, for
// the same reason appSlug is: only a bot account is this App's own identity,
// and an answer naming anything else (a person the token acts for, on a host
// that does not report bot accounts as such) is refused rather than used,
// because the identity it would build attributes a run's commits to an
// account that is not the App's.
//
// It is not cached: the runner asks once per run, before the first commit.
func (p *Provider) BotAccount(ctx context.Context) (gitprov.BotAccount, error) {
	var bot struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := p.api.Do(ctx, "GET", "/user", nil, &bot); err != nil {
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (GET /user): %w", err)
	}
	switch {
	case bot.Type != "Bot":
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (GET /user): the answer names a %s, not a bot account: %w", bot.Type, errNoIdentity)
	case bot.ID <= 0 || !strings.HasSuffix(bot.Login, botSuffix):
		// A bot without a numeric ID has no noreply address to be linked by,
		// and a login without the suffix is not the shape GitHub spells its
		// own bots with: either would be an identity that owns nothing.
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (GET /user): the bot account %q has no id to be linked by: %w", bot.Login, errNoIdentity)
	}
	return gitprov.BotAccount{ID: strconv.FormatInt(bot.ID, 10), Login: bot.Login}, nil
}

var _ gitprov.BotAccountReader = (*Provider)(nil)
