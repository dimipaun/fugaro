package cli

// accounts_allow_test.go is `fugaro accounts allow|deny`'s world: the local
// config's claude_accounts block, edited through the settings write path's one
// helper, on the fixture `settings set`'s own writes run on (a project config
// on this machine and no coding-agent marker). No cloud, no checkout: these
// two forms write one local document and nothing else, and the line they end
// on is the whole of what they say about the rest of the world.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// accountsAllowLocal is the fixture: one declared account whose token may be
// used by one repository. It is the state `accounts add` leaves behind, written
// here rather than reached through that command because these tests are about
// the second edit of the pair.
const accountsAllowLocal = `claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
`

// TestAccountsAllowAppendsTheRepository is S8's red step: the subcommand does
// not exist, and every assertion below is about what it does once it does.
//
// The list is what the account's token may be spent from, so appending a
// repository is the widening this form is for — and the repository the entry
// already names is not lost, because the edit is one item added to the list the
// file holds rather than a list written from the flag.
func TestAccountsAllowAppendsTheRepository(t *testing.T) {
	_, path := settingsWriteHomeWith(t, accountsAllowLocal)

	out, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b")
	if err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b: %v", err)
	}

	lc := accountsAllowLoad(t, path)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the entry's own repository and the one the flag named", got)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run does not end on the line that says what is left:\n%s", out)
	}
}

// accountsAllowLoad reads the local config this fixture's commands wrote.
func accountsAllowLoad(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("the config this command wrote is one its own loader refuses: %v", err)
	}
	return lc
}

// accountsAllowBytes is the file as it is, for the tests that assert a refusal
// left it byte-identical.
func accountsAllowBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
