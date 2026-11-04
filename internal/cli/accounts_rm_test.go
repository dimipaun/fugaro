package cli

// accounts_rm_test.go is `fugaro accounts rm`'s world, built on the same rig
// `accounts add` runs in: the accounts these tests remove are ones the add half
// of the pair made, so what they read is the state that command leaves rather
// than a fixture written to look like it.

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestAccountsRmRefusesWhileAProfileListsIt is spec §4 step 1, and it is the
// step the whole order exists for. A profile's claude_accounts names a job the
// installation has deployed, so an account removed under a live job leaves a
// job the policy cannot describe: the config such a command would write is one
// ValidateProfileClaudeAccounts refuses, and the owner would be holding a file
// their own CLI will not read. The test asserts both halves — that the command
// refuses, and that the file it would otherwise leave is one its own reader
// refuses — because a refusal is only worth its text if the state behind it is
// a real one.
func TestAccountsRmRefusesWhileAProfileListsIt(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--yes")

	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the command wrote %s while a profile still listed the account:\n%s", r.cfg, after)
	}
	if _, err := localcfg.Load(r.cfg); err != nil {
		t.Fatalf("the config that command left is one its own next load refuses: %v", err)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the command deleted %v while a profile still listed the account", got)
	}
	if rmErr == nil {
		t.Fatalf("the account was removed while profiles.standard still listed it")
	}
	if !strings.Contains(rmErr.Error(), "standard") {
		t.Errorf("the refusal does not name the profile that lists the account: %v", rmErr)
	}
	if !strings.Contains(rmErr.Error(), "--profile") {
		t.Errorf("the refusal does not say how to take the name out of the profile: %v", rmErr)
	}
}
