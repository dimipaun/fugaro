package gitops

// The identity a run's commits carry. The default one is Fugaro's own, which
// no account owns; a provider whose host links a commit to an account of its
// own (a GitHub App's bot) gives the run that account's identity instead, and
// it is here, in one value, that both are decided.

import (
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	// botID and botLogin are a GitHub App's bot account as GET /user spells
	// it: the account's numeric user ID, and the login GitHub links a commit
	// to when the commit's email carries both.
	botID    = "337521588"
	botLogin = "fugaro-dev[bot]"
)

// TestBotIdentityIsTheNoreplyAddress pins the pair GitHub attributes a commit
// by: the account's login as the name, and <id>+<login>@users.noreply.github.com
// as the email, which is the form GitHub's own bot accounts commit with
// (dependabot[bot]'s "49699333+dependabot[bot]@users.noreply.github.com").
//
// Mutation (any other address, a missing "[bot]" in it, or the ID left out):
// this test fails -- and GitHub links the commit to nobody, which is the bug
// the identity exists to fix.
func TestBotIdentityIsTheNoreplyAddress(t *testing.T) {
	const want = botID + "+" + botLogin + "@users.noreply.github.com"
	got := BotIdentity(botID, botLogin)
	if want := map[string]string{
		"GIT_AUTHOR_NAME":     botLogin,
		"GIT_AUTHOR_EMAIL":    want,
		"GIT_COMMITTER_NAME":  botLogin,
		"GIT_COMMITTER_EMAIL": want,
	}; !maps.Equal(got, want) {
		t.Fatalf("BotIdentity = %v, want %v", got, want)
	}
}

// TestNoBotIdentityKeepsTheDefault: an account that could not be read is no
// identity at all, and never half of one -- a name without an address, or an
// address without the ID GitHub links by, would be an identity that owns
// nothing. The run keeps Identity, which is what a Bitbucket run and a run
// whose read failed both carry.
func TestNoBotIdentityKeepsTheDefault(t *testing.T) {
	for _, tc := range []struct{ name, id, login string }{
		{"no ID", "", botLogin},
		{"no login", botID, ""},
		{"neither", "", ""},
	} {
		if got := BotIdentity(tc.id, tc.login); got != nil {
			t.Errorf("BotIdentity(%q, %q) = %v, want nil", tc.id, tc.login, got)
		}
	}
	if got := IdentityFor(nil); !maps.Equal(got, Identity) {
		t.Errorf("IdentityFor(nil) = %v, want the default Identity %v", got, Identity)
	}
	if got, want := IdentityEnvFor(nil), IdentityEnv(); !slices.Equal(got, want) {
		t.Errorf("IdentityEnvFor(nil) = %v, want IdentityEnv() %v", got, want)
	}
}

// TestCommitsCarryTheRunsIdentity: a run whose identity is its provider's bot
// commits as that account, end to end through git -- the author and committer
// of the commit on the remote are the bot's -- and the run still recognises
// the commit as its own. Every decision about whose a commit is (Push's
// lease, tipBelongsToRun, OwnCommits) is by branch, merge-base or reflog and
// never by the author, so a changed identity cannot cost a run its own
// commits: the second push here overwrites the run's own earlier tip, which
// is the lease tipBelongsToRun decides.
//
// Mutation (an identity the lease reads, or a push that drops the identity):
// this test fails on the author it finds on the remote, or on the second
// push.
func TestCommitsCarryTheRunsIdentity(t *testing.T) {
	repo, remote := setup(t)
	repo.Env = IdentityEnvFor(BotIdentity(botID, botLogin))
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "the run's own commit"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "the run's amended commit"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatalf("the run could not push over its own earlier tip under its own identity: %v", err)
	}
	const format = "%an <%ae>|%cn <%ce>"
	who := botLogin + " <" + botID + "+" + botLogin + "@users.noreply.github.com>"
	if got := testutil.Git(t, remote, "log", "-1", "--format="+format, "refs/heads/fugaro/x"); got != who+"|"+who {
		t.Fatalf("the commit on the remote is %q, want %q twice (author and committer)", got, who)
	}
}
