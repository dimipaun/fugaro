package github

// The App's own bot account, which is what a run's commits are attributed to:
// GitHub links a commit to the bot only when the commit's author and
// committer are that account's noreply identity (docs/git-providers.md).
//
// The App's slug comes from the read comments.go's appSlug makes when this
// process holds the key (GET /app with the App's JWT); the account behind it
// is then read by name, because GitHub refuses GET /user to an installation
// token ("Resource not accessible by integration").

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// BotAccount implements gitprov.BotAccountReader: the account this App's
// commits are linked to.
//
// The read is two steps: the App's slug from GET /app with the App's JWT, then
// GET /users/<slug>[bot] with the installation token the run already has, for
// the bot account's numeric ID. Without the App's key in this process (the
// credentials service holds it) there is no JWT and so no slug, and the read
// fails: the run keeps the default identity. It is deliberately strict about
// the answer, for the same reason appSlug is: only a bot account is this App's
// own identity, and an answer naming anything else is refused rather than
// used, because the identity it would build attributes a run's commits to an
// account that is not the App's.
//
// It is not cached: the runner asks once per run, before the first commit.
func (p *Provider) BotAccount(ctx context.Context) (gitprov.BotAccount, error) {
	if p.app == nil {
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account: the App's key is not in this process, so its slug cannot be read: %w", errNoIdentity)
	}
	slug, what, err := p.readAppSlug(ctx)
	if err != nil {
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): %w", what, err)
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
	case bot.ID <= 0 || !strings.HasSuffix(bot.Login, botSuffix):
		// A bot without a numeric ID has no noreply address to be linked by,
		// and a login without the suffix is not the shape GitHub spells its
		// own bots with: either would be an identity that owns nothing.
		return gitprov.BotAccount{}, fmt.Errorf("reading the App's bot account (%s): the bot account %q has no id to be linked by: %w", route, bot.Login, errNoIdentity)
	}
	return gitprov.BotAccount{ID: strconv.FormatInt(bot.ID, 10), Login: bot.Login}, nil
}

var _ gitprov.BotAccountReader = (*Provider)(nil)
