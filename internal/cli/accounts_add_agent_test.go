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
//
// Two refusals answer a session, and the world decides which. On a project
// whose marks SAY it is not managed the fence refuses first — the sentence a
// person at their own terminal reads on that project too, because what is
// wrong is the project and not the session — and that is the first arm. On the
// path a managed project takes, where the marks cannot be READ, a read that
// broke is not an answer (D2218): the fence warns and goes on, and this
// command's own gate then refuses the session, naming the marker and printing
// the diff it did not write — the second arm, and the only one where the
// marker is what makes the difference.
func TestAccountsAddRefusesInAnAgentSession(t *testing.T) {
	// The ordinary installation, by name. One row and not a loop over the
	// markers: the fence answers a project that is not managed whether or not a
	// session is running, so running it once per marker would prove nothing
	// about any of them.
	t.Run("an unmanaged project is refused by the fence", func(t *testing.T) {
		path := ordinaryInstallationRig(t, accountsAddLocal).cfg
		before := readFileString(t, path)
		t.Setenv(accountsAddAgent, "1")

		out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "acme/app")
		if err == nil {
			t.Fatalf("the command wrote on a project that is not managed:\n%s", out)
		}
		checkFenceRefusal(t, err, "accounts add")
		if after := readFileString(t, path); after != before {
			t.Errorf("the file changed under the fence:\n%s", after)
		}
	})

	// A managed project whose marks cannot be read: the fence warns and goes
	// on, and the command's own fence answers the session — the diff below is
	// the half the fence never prints.
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			r := newManagedAddRig(t, addRigLocal, addMarksManaged)
			r.gcs.RefusePath("managed.json", 500, "InternalServerError", "", "the creation fact did not answer")
			before := readFileString(t, r.cfg)
			t.Setenv(marker, "1")

			out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "owner/repo-a")
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
			if !strings.Contains(out, r.cfg) {
				t.Errorf("the refusal does not name the file it refused to write:\n%s", out)
			}
			if !strings.Contains(out, "  + ") {
				t.Errorf("the refusal does not print the diff:\n%s", out)
			}
			if after := readFileString(t, r.cfg); after != before {
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
// The invocation passes neither --ask nor --yes, and it is the owner's own
// terminal — the one place a managed project lets this command make the
// change, and the shape the sentence was written for: what a person gets
// instead of a question is the sentence naming every change, printed before
// the first of them is written.
func TestAccountsAddAsksNothing(t *testing.T) {
	r := newAddRig(t)
	ownerAtTheTerminal(t)
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
			// A managed project whose marks cannot be read: the fence warns
			// and goes on (D2218), the gate is left standing nowhere, and the
			// refusal below is this command's own — the one the file's own
			// write path returns early from on this path, and the reason this
			// test exists at all. On an ordinary installation the fence refuses
			// the command whole, which says nothing about the re-run: a build
			// with no fence on this path would pass there.
			r := newManagedAddRig(t, rotationLocal(t), addMarksManaged)
			r.gcs.RefusePath("managed.json", 500, "InternalServerError", "", "the creation fact did not answer")
			writes := r.secretWrites(t)
			before := readFileString(t, r.cfg)
			t.Setenv(marker, "1")

			out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a")
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
// neither --ask nor --yes and stands at the owner's own terminal: the sentence
// naming the converge is printed in full, nothing waits on an answer to it, and
// the token reaches the store.
func TestAccountsAddRotationRerunAsksNothing(t *testing.T) {
	r := newAddRigWith(t, rotationLocal(t))
	ownerAtTheTerminal(t)
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

// TestAccountsAddWideningAllowDataToIsRefusedInASession is G9's fourth red step
// (docs/config-management/implementation-plan.md §G9, D1172, spec §3.3): what
// `accounts add` writes is `claude_accounts.NAME.allow_data_to`, so a widening
// of it — a --repos naming a repository beyond the checkout this command runs
// in — is refused in a coding agent's session, where the bounded declaration
// beside it is admitted. The pair is the claim: what refuses is the widening
// and not the command, which is the half D868 gives this family and the gate
// keeps (accounts_managed.go).
//
// The two halves run in the same managed world on purpose. In an ordinary
// installation the session is refused the command whole
// (TestAccountsAddRefusesInAnAgentSession above), which says nothing about the
// widening: a build that refused the bounded declaration too would pass there.
// The deeper halves of each — the record the gate files, the rules it names —
// are TestAccountsAddManagedAdmitsTheDeclarationInAnAgentSession's and
// TestAccountsAddManagedRefusesAWideningInAnAgentSession's; this is the
// sentence a session reads, against the same command that is admitted.
func TestAccountsAddWideningAllowDataToIsRefusedInASession(t *testing.T) {
	t.Run("the bounded declaration is admitted", func(t *testing.T) {
		r := newManagedAddRig(t, addRigLocal, addMarksManaged)
		t.Setenv(accountsAddAgent, "1")

		out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a")
		if lc := r.addLocal(t); !slices.Equal(lc.ClaudeAccounts["team-a"].AllowDataTo, []string{"owner/repo-a"}) {
			t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the bounded list this checkout's origin names: %v\n%s", lc.ClaudeAccounts["team-a"].AllowDataTo, err, out)
		}
		// The session's run ends at the one step no gate admits — the token —
		// and that is the admitted declaration's cost, not this test's subject:
		// what it is here for is the file, which a widening never writes.
		if err == nil || err.Error() != accountsAddRefusalToken {
			t.Errorf("the admitted declaration did not end at the token step: %v\n%s", err, out)
		}
	})

	t.Run("the widening is refused", func(t *testing.T) {
		r := newManagedAddRig(t, managedAddLocalWide(t), addMarksManaged)
		before := readFileString(t, r.cfg)
		t.Setenv(accountsAddAgent, "1")

		out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a,acme/other")
		if err == nil {
			t.Fatalf("a session widened allow_data_to beyond the checkout it runs in:\n%s", out)
		}
		if err.Error() != accountsAddRefusalWidening {
			t.Errorf("the widening's refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalWidening)
		}
		if after := readFileString(t, r.cfg); after != before {
			t.Errorf("the refused widening wrote %s:\n%s", r.cfg, after)
		}
	})
}
