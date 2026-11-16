package token_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// contextRepos is a plausible set of context repositories: the five a task may
// name at most, each a real owner/name.
var contextRepos = []string{"acme/lib", "acme/other", "acme/third", "acme/fourth", "acme/fifth"}

// longContextRepo is one repository name of the length the byte bound is
// about: an owner/name no host would hand out, and one the task's own shape
// rule still admits, which is exactly the value the bound has to catch.
func longContextRepo() string { return "acme/" + strings.Repeat("r", 180) }

// TestTokenContextClaimWithinByteLimit: the context repositories a launcher
// binds a run to travel as the fcr claim (C5a), and the claim set they make
// stays inside the bound Firebase puts on a custom token's claims. A set that
// would exceed it is refused before the signer is called, so nothing is signed
// for a token the identity provider would refuse.
func TestTokenContextClaimWithinByteLimit(t *testing.T) {
	e := newEnv(t)
	c := claims()
	c.ContextRepos = contextRepos
	cl := payload(t, e.mint(t, c, t0))["claims"].(map[string]any)
	repos, ok := cl["fcr"].([]any)
	if !ok || len(repos) != len(contextRepos) {
		t.Fatalf("fcr = %v, want %v", cl["fcr"], contextRepos)
	}
	for i, want := range contextRepos {
		if repos[i] != want {
			t.Errorf("fcr[%d] = %v, want %q", i, repos[i], want)
		}
	}
	if n := token.ClaimsSize(c); n > token.MaxClaimsBytes {
		t.Fatalf("the claim set of five context repositories is %d bytes, over the %d Firebase allows", n, token.MaxClaimsBytes)
	}

	// The same claim set with the names a host would never give out is over
	// the bound, and the mint refuses it rather than send it.
	signs := len(e.iam.SignCalls())
	over := claims()
	for i := 0; i < token.MaxContextRepos; i++ {
		over.ContextRepos = append(over.ContextRepos, longContextRepo())
	}
	if n := token.ClaimsSize(over); n <= token.MaxClaimsBytes {
		t.Fatalf("the claim set with five long names is %d bytes, not over the %d this test needs", n, token.MaxClaimsBytes)
	}
	if _, err := token.MintAt(context.Background(), e.signer, over, t0); err == nil {
		t.Fatal("a claim set over the byte bound was minted")
	}
	if n := len(e.iam.SignCalls()); n != signs {
		t.Fatalf("a refused mint still called signJwt (%d calls, want %d)", n, signs)
	}

	// A claim that names more repositories than a task may is refused too, in
	// the same place: the claim's own bound, since a task that got past the
	// spec's rule must not be able to mint what that rule refuses.
	tooMany := claims()
	tooMany.ContextRepos = append(append([]string(nil), contextRepos...), "acme/sixth")
	if _, err := token.MintAt(context.Background(), e.signer, tooMany, t0); err == nil {
		t.Fatalf("a claim naming %d context repositories was minted", len(tooMany.ContextRepos))
	}
	if n := len(e.iam.SignCalls()); n != signs {
		t.Fatalf("signJwt calls = %d, want %d: no refused claim reaches the signer", n, signs)
	}
}

// TestSessionClaimsReadsContextRepos: what the launcher minted comes back out
// of the session (the runner and the credentials service read it there), and
// an identity minted without the claim reads back empty rather than as a
// one-element list.
func TestSessionClaimsReadsContextRepos(t *testing.T) {
	e := newEnv(t)
	c := claims()
	c.ContextRepos = contextRepos[:2]
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Claims().ContextRepos; len(got) != 2 || got[0] != "acme/lib" || got[1] != "acme/other" {
		t.Errorf("context repos = %v, want %v", got, c.ContextRepos)
	}
	plain, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, claims(), t0))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Claims().ContextRepos; len(got) != 0 {
		t.Errorf("an identity minted without fcr reads back %v, want none", got)
	}
	if n := token.ClaimsSize(claims()); n >= token.MaxClaimsBytes {
		t.Errorf("a claim set with no context repository is %d bytes, and the bound is %d", n, token.MaxClaimsBytes)
	}
}
