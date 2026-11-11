//go:build darwin || linux

package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// TestAccountsAllowRefusesInAnAgentSession is SD9's rule on this pair
// (spec §2.4): a coding agent's session writes nothing here — --yes included.
// The list is what decides which repository may spend an account's token, and
// the person who reads the diff is the one who may widen it. The loop is over
// agentenv.Markers itself rather than a copy, so a marker the tree adds is one
// these commands are held to.
//
// Both directions are in the loop because both are writes: a removal narrows
// where the token may be spent, and a session that could make one silently
// would take a repository out of the project's runs with nobody reading it.
// The refusal prints the command to run at a terminal, and that command names
// the form the session reached for — allow for allow, deny for deny.
func TestAccountsAllowRefusesInAnAgentSession(t *testing.T) {
	cases := []struct {
		verb string
		repo string
		want string
	}{
		{verb: "allow", repo: "owner/repo-b", want: "accounts allow team-a owner/repo-b"},
		{verb: "deny", repo: "owner/repo-a", want: "accounts deny team-a owner/repo-a"},
	}
	for _, tc := range cases {
		for _, marker := range agentenv.Markers {
			t.Run(tc.verb+"/"+marker, func(t *testing.T) {
				_, path := settingsWriteHomeWith(t, accountsAllowLocal)
				before := string(accountsAllowBytes(t, path))
				t.Setenv(marker, "1")

				out, _, err := executeStdin(t, "", "accounts", tc.verb, "team-a", tc.repo)
				if err == nil {
					t.Fatal("the command wrote in a coding agent's session")
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
				}
				// The diff is printed and the bytes are not written: the owner
				// reads what the command would have done and does it themselves.
				if !strings.Contains(out, path) {
					t.Errorf("the refusal does not name the file it refused to write:\n%s", out)
				}
				if !strings.Contains(out, "  + ") && !strings.Contains(out, "  - ") {
					t.Errorf("the refusal does not print the diff:\n%s", out)
				}
				if after := string(accountsAllowBytes(t, path)); after != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", after)
				}
			})
		}
	}
}
