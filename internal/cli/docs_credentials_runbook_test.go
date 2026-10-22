package cli

import (
	"strings"
	"testing"
)

// credentialsRunbook is where the credentials service's operator steps live.
// It is a new file for Task C1.22: SECURITY.md states what the service *is*,
// and this states what an operator *does* when it is down, when a run halted
// for a missing credential, when a mark is wrong, when the bundle rotates and
// when the installation turns the service off.
const credentialsRunbook = "docs/credentials-service/runbook.md"

// TestDocsCredentialsRunbookNamesEveryOwnerStep: the outage runbook exists and
// names, as a real command, every step the task requires. Each pin is a
// command the code already has — a runbook that names a command Fugaro does
// not have is worse than no runbook, because the operator reaches for it while
// something is already wrong.
func TestDocsCredentialsRunbookNamesEveryOwnerStep(t *testing.T) {
	flat := flattenDoc(readDoc(t, credentialsRunbook))
	for _, want := range []string{
		// The stop flag: the one command that stops the service issuing.
		"fugaro budget kill --all",
		"fugaro budget kill --repo",
		"fugaro budget resume",
		// The bundle's commands (C1.25) and what they write.
		"fugaro creds set",
		"fugaro creds ls",
		// The account commands an outage is read with.
		"fugaro accounts ls",
		// C1.21's clearing of a wrong mark.
		"fugaro accounts clear",
		// The opt-in, named so an operator can tell whether it is on at all.
		"fugaro init --provision-credentials",
		// The two halt reasons an operator sees from this service.
		"no_credential",
		"creds_rules_denied",
		// The opt-out's exact step: the module prefix is required, and the
		// single quotes are what keep the shell from reading the brackets.
		"terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'",
		// The container carries prevent_destroy, which is why the state rm
		// comes first.
		"prevent_destroy",
		// The audit-log step the owner runs (m3).
		"DATA_READ",
		// Taking a member off a published node: no CLI does it in this
		// release, so the runbook names the refusal's two ways out.
		"--delete",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not name %q: an operator reads this while the service is already refusing", credentialsRunbook, want)
		}
	}
	// The two wrong spellings of the opt-out's first step, which the
	// installation module's own guard forbids in the plan and which are just
	// as wrong here: the bare address names nothing in the installation
	// root's state, and the module-prefixed one unquoted is a glob zsh
	// refuses before terraform sees it (re-review N1).
	for _, forbidden := range []string{
		"terraform state rm google_secret_manager_secret.credentials_bundle[0]",
		"terraform state rm module.installation.google_secret_manager_secret.credentials_bundle[0]",
	} {
		if strings.Contains(flat, forbidden) {
			t.Errorf("%s spells the opt-out's first step %q, which does not run: the address is the installation root's, module-prefixed and single-quoted", credentialsRunbook, forbidden)
		}
	}
}

// TestDocsCredentialsRunbookIsOwnerRunOnly: every step is the owner's, and the
// runbook says so once, plainly. A step an agent might take — a credential
// set, a mark cleared, a project switched off — is exactly the class of action
// that must not be reachable from a run.
func TestDocsCredentialsRunbookIsOwnerRunOnly(t *testing.T) {
	flat := flattenDoc(readDoc(t, credentialsRunbook))
	for _, want := range []string{
		"owner runs every step",
		"none of them is runnable by a run",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q", credentialsRunbook, want)
		}
	}
	// The forbidden shape: a runbook that tells an agent to do it. The words
	// are banned as a phrase, so the honest sentence above ("an agent never
	// sets a credential") does not trip it.
	for _, banned := range []string{
		"the agent can set a credential",
		"the agent may clear the mark",
		"a run rotates the bundle",
	} {
		if strings.Contains(flat, banned) {
			t.Errorf("%s says %q: no run may take an operator step", credentialsRunbook, banned)
		}
	}
}

// TestDocsCredentialsRunbookOrdersTheOptOut: the opt-out is ordered, and the order
// is the part that is easy to get wrong. Every repository root is re-applied
// *before* the two installation steps (re-review N2): the per-repository roots
// are what read the service's address, so removing the service first leaves
// them pointing at a host that is gone. This checks the order the runbook
// writes, not merely that it writes both.
func TestDocsCredentialsRunbookOrdersTheOptOut(t *testing.T) {
	flat := flattenDoc(readDoc(t, credentialsRunbook))
	repo := strings.Index(flat, "re-apply every repository root")
	stateRm := strings.Index(flat, "terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'")
	if repo < 0 || stateRm < 0 {
		t.Fatalf("the runbook does not write both halves of the opt-out (repository re-apply at %d, state rm at %d)", repo, stateRm)
	}
	if repo > stateRm {
		t.Error("the runbook removes the bundle before re-applying the repository roots: the repository roots read the service's address, so they go first (re-review N2)")
	}
}

// TestDocsCredentialsRunbookSaysWhatARotationDoesInFlight: rotating the bundle is
// the one operator step that touches runs already going, and the honest answer
// is bounded rather than empty. A rotation is read at the next `latest` read,
// so a call in flight keeps the credential it was given, a run that has not
// called yet gets the new one, and a rotation that removes a credential a run
// is mid-way through is what the wait-or-switch path answers.
func TestDocsCredentialsRunbookSaysWhatARotationDoesInFlight(t *testing.T) {
	flat := flattenDoc(readDoc(t, credentialsRunbook))
	for _, want := range []string{
		"a run in flight",
		"the call already in flight keeps the credential it was given",
		"the next read of the bundle",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: an operator rotating the bundle needs to know what it does to runs already going", credentialsRunbook, want)
		}
	}
}
