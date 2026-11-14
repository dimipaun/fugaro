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

// TestSelfUserKeepsTheFallbackWhenTheSetMayBeIncomplete: the proxy picks the
// token per request (D2355), so the account GET /user answers -- a read --
// need not be the account a comment is posted as, and a throttled or broken
// narrower token is served by a wider one (R12). A node holding a token
// whose identity could not be read (a repository access token, R14) knows
// its set is missing such an account and says so, and then GET /user naming
// an account of Fugaro's is no longer enough to claim the run can tell its
// own comments apart: every comment must come back SelfKnown false, so the
// markers-from-anyone fallback stays open and the run still reads the marker
// on the comment it wrote through a token the set does not name -- what
// FugaroRuns, checkStale and reportPosted rest on.
func TestSelfUserKeepsTheFallbackWhenTheSetMayBeIncomplete(t *testing.T) {
	// comments_partial.json: GET /user answers the account the set names,
	// comment 151 is by an account outside it and carries the run's report
	// marker, 152 is a person's.
	p := openOwn(t, "comments_partial.json", "{00000000-0000-4000-8000-00000000b07e}")
	before, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range before {
		if !c.SelfKnown {
			t.Errorf("comment %s: SelfKnown false with the set declared complete", c.ID)
		}
	}
	// And that is the hazard the flag exists for: with the set declared
	// complete, the run's own comment -- posted through a token the set does
	// not name -- reads as somebody else's while every comment is claimed to
	// be known, so the run skips its own marker.
	if !before[0].SelfKnown || before[0].Self {
		t.Fatalf("comment %s: Self=%v SelfKnown=%v, want the run's own comment unrecognised", before[0].ID, before[0].Self, before[0].SelfKnown)
	}
	p.o.OwnIdentitiesPartial = true
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "151" || got[1].ID != "152" {
		t.Fatalf("comments = %+v, want 151 then 152", got)
	}
	for _, c := range got {
		if c.Self || c.SelfKnown {
			t.Errorf("comment %s: Self=%v SelfKnown=%v, want the fallback left open", c.ID, c.Self, c.SelfKnown)
		}
	}
	if !strings.Contains(got[0].Body, "<!-- fugaro:report run=20260927-101500-abcd -->") {
		t.Fatalf("comment 151: body = %q, want the run's own marker on a comment by an unlisted account", got[0].Body)
	}
}
