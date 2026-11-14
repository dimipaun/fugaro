package bitbucket

import (
	"reflect"
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

// TestSelfUserKnowsTheRunWhenTheTokenHasNoAccountScope: GET /user answers a
// repository access token with HTTP 403 (R14), and on its own that answer
// leaves every comment SelfKnown false, which hands every author's marker
// to the run (followup.MarkersFromAnyone). The node's identity set says who
// Fugaro is without that endpoint, so the run still recognises its own
// comment and the fallback never opens.
func TestSelfUserKnowsTheRunWhenTheTokenHasNoAccountScope(t *testing.T) {
	var warnings []string
	p := openOwn(t, "user_forbidden.json", "{00000000-0000-4000-8000-00000000b07e}")
	p.o.Warn = func(m string) { warnings = append(warnings, m) }
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Self || !got[0].SelfKnown || got[0].AuthorID != botID {
		t.Fatalf("comments = %+v", got)
	}
	// What the warning says — that the run cannot tell its own comments
	// apart — the set makes untrue.
	if _, err := p.Comments(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %q, want none about GET /user", warnings)
	}
}
