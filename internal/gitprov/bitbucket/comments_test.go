package bitbucket

import (
	"reflect"
	"strings"
	"testing"
)

// openOwn is open with the node's identity set configured (T13): the
// account UUIDs Fugaro's own Bitbucket tokens have, which the credentials
// service may serve one run with in turn.
func openOwn(t *testing.T, fixture string, identities ...string) *Provider {
	t.Helper()
	p := open(t, fixture)
	p.o.OwnIdentities = identities
	return p
}

// TestSelfUserAcceptsAnyTokenIdentity: a project may hold several Bitbucket
// tokens and the service serves each request with the narrowest covering
// one (D2355), so a run's comments are written as more than one account.
// The node's identity set names them (T13, F20), and a comment by any of
// its members is the run's own — the account GET /user answers included,
// which is what a single-token provider has always compared against.
func TestSelfUserAcceptsAnyTokenIdentity(t *testing.T) {
	// comments_self.json: 131 is the token's own user, 132 Alice, 133 a
	// second app_user, 134 a deleted account with no user at all. The set
	// names 133's uuid, whose account_id is the token's own: an account_id
	// is not an identity here, so only the uuid may match, and an empty
	// member is nobody.
	p := openOwn(t, "comments_self.json", "{00000000-0000-4000-8000-0000000000ff}", "")
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	self := map[string]bool{}
	for _, c := range got {
		if !c.SelfKnown {
			t.Errorf("comment %s: SelfKnown false", c.ID)
		}
		self[c.ID] = c.Self
	}
	if want := map[string]bool{"131": true, "132": false, "133": true, "134": false}; !reflect.DeepEqual(self, want) {
		t.Fatalf("self = %v, want %v", self, want)
	}
	// The second listing answers the same: the fixture carries one GET /user.
	if _, err := p.Comments(ctx, 12); err != nil {
		t.Fatal(err)
	}
}

// TestSelfUserKeepsTheFallbackWhenTheRunsOwnTokenHasNoIdentity: GET /user
// answers a repository access token with HTTP 403 (R14), so that token has
// no identity to put in the node's set, and the run writes its comments
// through it. The set naming the project's other tokens must not turn that
// into a claim that the run knows its own comments: a comment from the
// repository token would come back SelfKnown false anyway, and the run
// needs the markers-from-anyone fallback open — FugaroRuns, checkStale and
// reportPosted all read it, and a follow-up that cannot see the run that
// overtook it pushes over that run's work.
func TestSelfUserKeepsTheFallbackWhenTheRunsOwnTokenHasNoIdentity(t *testing.T) {
	var warnings []string
	p := openOwn(t, "user_forbidden.json", "{00000000-0000-4000-8000-0000000000ff}")
	p.o.Warn = func(m string) { warnings = append(warnings, m) }
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	// The comment is the repository token's own, and it is outside the set.
	if len(got) != 1 || got[0].Self || got[0].SelfKnown || got[0].AuthorID != botID {
		t.Fatalf("comments = %+v, want the fallback kept", got)
	}
	if _, err := p.Comments(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "can't be told apart") {
		t.Fatalf("warnings = %q, want one that the run cannot tell its own comments apart", warnings)
	}
}
