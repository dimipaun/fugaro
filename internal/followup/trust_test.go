package followup

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

const (
	listedID   = "1001"
	unlistedID = "2002"
	botID      = "9009" // Fugaro's identity: the PR's author
	bbID       = "557058:00000000-0000-0000-0000-000000000001"
)

func TestTrustAllowlist(t *testing.T) {
	cfg := config.Followup{Trusted: []string{listedID, bbID, botID}}
	pr := gitprov.PRInfo{Number: 7, AuthorID: botID}
	tr := NewTrust(cfg, pr)
	cases := []struct {
		name string
		c    gitprov.Comment
		want bool
	}{
		{"listed collaborator", gitprov.Comment{AuthorID: listedID, Collaborator: true}, true},
		{"unlisted collaborator", gitprov.Comment{AuthorID: unlistedID, Collaborator: true}, false},
		{"listed GitHub non-collaborator", gitprov.Comment{AuthorID: listedID, Collaborator: false}, false},
		{"listed Bitbucket author", gitprov.Comment{AuthorID: bbID, Collaborator: true}, true},
		{"PR author even when listed", gitprov.Comment{AuthorID: botID, Collaborator: true}, false},
		{"Fugaro's own identity", gitprov.Comment{AuthorID: listedID, Collaborator: true, Self: true, SelfKnown: true}, false},
		{"no author ID", gitprov.Comment{AuthorID: "", Collaborator: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tr.Allows(c.c); got != c.want {
				t.Fatalf("Allows = %v, want %v", got, c.want)
			}
		})
	}

	// A nil trusted list trusts nobody.
	none := NewTrust(config.Followup{}, gitprov.PRInfo{AuthorID: botID})
	if none.Allows(gitprov.Comment{AuthorID: listedID, Collaborator: true}) {
		t.Fatal("nil trusted list allowed a comment")
	}
}

func TestTrustUnknownPRAuthorTrustsNobody(t *testing.T) {
	// The PR's author is unknown and so is Fugaro's identity: the bot's own
	// ID, listed by mistake, must not steer the follow-up, so nobody does.
	tr := NewTrust(config.Followup{Trusted: []string{listedID, botID}}, gitprov.PRInfo{})
	for _, c := range []gitprov.Comment{
		{AuthorID: botID, Collaborator: true},
		{AuthorID: listedID, Collaborator: true},
		{Collaborator: true},
	} {
		if tr.Allows(c) {
			t.Fatalf("PR author unknown, yet %q is trusted", c.AuthorID)
		}
	}
	sel := Select([]gitprov.Comment{
		{ID: "1", Kind: gitprov.CommentGeneral, Author: "Alice", AuthorID: listedID, Collaborator: true, Body: "fix it", CreatedAt: t0.Add(time.Minute)},
	}, t0, tr, DefaultLimits, keep)
	if len(sel.Comments) != 0 || sel.Omitted["untrusted_author"] != 1 {
		t.Fatalf("kept %d, omitted %v", len(sel.Comments), sel.Omitted)
	}
}

func TestTrustSelfDropsPRAuthorUnmarked(t *testing.T) {
	// The identity lookup failed (no comment is SelfKnown), and the PR's
	// author posted an unmarked comment: it is Fugaro's own, dropped as self.
	tr := NewTrust(config.Followup{Trusted: []string{botID, listedID}}, gitprov.PRInfo{AuthorID: botID})
	all := []gitprov.Comment{
		{ID: "1", Kind: gitprov.CommentGeneral, AuthorID: botID, Author: "fugaro-bot", Collaborator: true, Body: "an unmarked note", CreatedAt: t0.Add(time.Hour)},
	}
	sel := Select(all, t0, tr, DefaultLimits, keep)
	if len(sel.Comments) != 0 || sel.Omitted["self"] != 1 {
		t.Fatalf("got %d comments, omitted %v; want the PR author's comment dropped as self", len(sel.Comments), sel.Omitted)
	}
}
