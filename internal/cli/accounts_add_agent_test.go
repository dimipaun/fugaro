//go:build darwin || linux

package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// TestAccountsAddRefusesInAnAgentSession is SD9's rule on this command
// (spec §2.4): a coding agent's session writes nothing here — --yes included,
// because this file decides where a credential may be sent and the person who
// reads the diff is the one who may make that decision. The loop is over
// agentenv.Markers itself rather than a copy, so a marker the tree adds is one
// this command is held to.
//
// It runs without --claude, so it says nothing about act one: the login is not
// reached on this path and a login that ran here would prove nothing about it.
// TestAccountsAddClaudeRefusedInEveryAgentSession is the test that puts the
// fake login on PATH, passes --claude and asserts its marker file is absent, so
// a fence that moved off the login's own path fails there.
func TestAccountsAddRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
			before := readFileString(t, path)
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
		})
	}
}

// rotationLocal is the fixture's local config with team-a already declared and
// already served by the profile: the state a rotation re-run starts from, where
// neither of the two edits the command makes changes a byte.
func rotationLocal(t *testing.T) string {
	t.Helper()
	const profile = "    allow_repos:\n      - github/owner/repo-a\n"
	if !strings.Contains(addRigLocal, profile) {
		t.Fatalf("the fixture's profile block is not the one this rewrites:\n%s", addRigLocal)
	}
	return strings.Replace(addRigLocal, profile, profile+"    claude_accounts:\n      - team-a\n", 1) +
		"claude_accounts:\n  team-a:\n    allow_data_to:\n      - owner/repo-a\n"
}

// TestAccountsAddAsksNothing is this command's own reading of D1540: the
// declaration, the profile entry, the secret and the converge are one
// sentence, printed before the run makes them, and no question follows it.
// The invocation passes neither --ask nor --yes and has no terminal, which is
// the shape a script drives the command with — the deployment the old y/N was
// answered for is the same one either way, and what a person gets instead of a
// question is the sentence naming every change, printed before the first of
// them is written.
func TestAccountsAddAsksNothing(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "declare claude_accounts.team-a.allow_data_to = [owner/repo-a]") {
		t.Errorf("the run does not name the declaration it writes:\n%s", out)
	}
	if lc := r.addLocal(t); !slices.Equal(lc.ClaudeAccounts["team-a"].AllowDataTo, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want [owner/repo-a]", lc.ClaudeAccounts["team-a"].AllowDataTo)
	}
	if len(writes.created) == 0 && len(writes.added) == 0 {
		t.Errorf("the run asked nothing and stored nothing: created %v, added %v", writes.created, writes.added)
	}
}

// TestAccountsAddRotationRerunRefusesInAnAgentSession is B0 (spec §2.4): the
// refusal has to stand on the path where the file does not change. A rotation
// re-run — the account already declared, the profile already serving it, the
// same --repos — makes both edits no-ops, and applySettingsEdit returns on that
// before its own refusal; the converge after it reads the token from stdin,
// stores a new version of it project-wide and clears the account's mark. A
// fence that only guards the file would let a session rotate a project-wide
// secret by re-running a command that prints "no change". The loop is over
// agentenv.Markers itself, so a marker the tree adds is one this path is held
// to.
func TestAccountsAddRotationRerunRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			r := newAddRigWith(t, rotationLocal(t))
			writes := r.secretWrites(t)
			before := readFileString(t, r.cfg)
			t.Setenv(marker, "1")

			out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
			if err == nil {
				t.Fatalf("a rotation re-run wrote in a coding agent's session:\n%s", out)
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
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			if len(writes.created) != 0 || len(writes.added) != 0 {
				t.Errorf("the session stored a secret: created %v, added %v", writes.created, writes.added)
			}
			if after := readFileString(t, r.cfg); after != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", after)
			}
			if strings.Contains(out, "Nothing else to run") {
				t.Errorf("the refusal still ended on the command's ready line:\n%s", out)
			}
		})
	}
}

// TestAccountsAddRotationRerunAsksNothing is the other half of the same path:
// the file unchanged, so the change this run is about is the converge's — the
// token stored project-wide and the database the mark lives in — and the run
// names it instead of asking about it (D1540). The invocation below passes
// neither --ask nor --yes and has no terminal: the sentence naming the
// converge is printed in full, nothing waits on an answer to it, and the token
// reaches the store.
func TestAccountsAddRotationRerunAsksNothing(t *testing.T) {
	r := newAddRigWith(t, rotationLocal(t))
	writes := r.secretWrites(t)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	// The sentence says what the file's own bytes cannot: the declaration is
	// already there, and what this run writes is a new version of the token.
	if !strings.Contains(out, "a rotation: the account already exists, and the token written for it replaces the one in use") {
		t.Errorf("the run does not name the converge it is about:\n%s", out)
	}
	if len(writes.created) == 0 && len(writes.added) == 0 {
		t.Errorf("the run named a converge and stored nothing: created %v, added %v", writes.created, writes.added)
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the converged run does not close on its ready line:\n%s", out)
	}
}
