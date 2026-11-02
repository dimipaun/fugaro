//go:build darwin || linux

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// TestAccountsAddRefusesInAnAgentSession is SD9's rule on this command
// (spec §2.4): a coding agent's session writes nothing here — --yes included,
// because this file decides where a credential may be sent and the person who
// reads the diff is the one who may make that decision. The loop is over
// agentenv.Markers itself rather than a copy, so a marker the tree adds is one
// this command is held to. The fake login on PATH is A3's child; it is here
// because the refusal must come before anything the command does, and a login
// that ran would leave its marker file behind.
func TestAccountsAddRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
			before := readFileString(t, path)
			fake := fakeClaude(t, t.TempDir())
			t.Setenv(marker, "1")

			out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
			if err == nil {
				t.Fatal("the command wrote in a coding agent's session")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro accounts add") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			// The diff is printed and the bytes are not written: the owner
			// reads what the command would have done and does it themselves.
			if !strings.Contains(out, path) {
				t.Errorf("the refusal does not name the file it refused to write:\n%s", out)
			}
			if !strings.Contains(out, "  + ") {
				t.Errorf("the refusal does not print the diff:\n%s", out)
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", after)
			}
			if _, statErr := os.Stat(fake.marker); statErr == nil {
				t.Error("the login ran: the refusal comes before anything the command does")
			}
		})
	}
}
