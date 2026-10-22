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

// TestDocsCredentialsRunbookPointsStepOneAtSomethingThatAnswers: §1 is what an
// operator runs to find out whether the service is on at all, and the code
// review of this change found it pointing at `fugaro doctor` — which has no
// credentials-service check. Doctor's three credentials checks
// (internal/cli/doctor.go's creds-broken, creds-unknown-state and creds-rules)
// read the credential marks and the deployed rules' marker, and no doctor file
// mentions the service, its account, the container or the grant at all — so the
// operator would have read that absent output as "the service is off".
//
// The step now names `fugaro creds ls`, which reads the one piece a command can
// read from here (the bundle container, and the document the service issues
// from), and the section says outright that the Cloud Run service itself is
// answered by a `gcloud` read rather than by any Fugaro command.
//
// Mutation (verified, run and restored): the old §1 put back — the
// `fugaro doctor` step and the sentence about the pieces it "names" — fails
// this on the banned claim and on four of the five pins. It does *not* fail on
// the `fugaro creds ls` pin, because the old text named the command too, in its
// last sentence about the bundle's own view; that is why the ban is the
// load-bearing half here and the pins are the shape around it.
func TestDocsCredentialsRunbookPointsStepOneAtSomethingThatAnswers(t *testing.T) {
	section := flattenDoc(docSection(t, credentialsRunbook, readDoc(t, credentialsRunbook), "## 1. Is the service on at all?"))
	for _, want := range []string{
		"fugaro creds ls",
		// What doctor does cover, so the section does not simply drop it.
		"creds-broken",
		"creds-unknown-state",
		"creds-rules",
		// What no command covers, and the reads that do cover it. The section
		// first answered this with "Check 94", which is Task C1.23's and is
		// not in docs/gcp-live-checklist.md on this release (that file's
		// checks stop at 91): a runbook may not answer a question with a check
		// nobody can open, so the three gcloud reads are in the section itself
		// and the check is named as the register's version of them, with the
		// fact that it is not there yet (code review of C1.22).
		"What no Fugaro command answers is whether the Cloud Run service itself is deployed",
		"gcloud run services describe fugaro-credentials",
		"gcloud secrets get-iam-policy <bundle-container>",
		"gcloud run services get-iam-policy fugaro-credentials",
		"roles/secretmanager.secretAccessor",
		"roles/run.invoker",
		"Check 94",
		"on this release yet",
		// What a launch actually refuses on, which §1 stated the wrong way
		// round: the gate reads the switch, not the service (code review of
		// C1.22, and the same correction in docs/multi-model.md).
		"launch gate reads the project's published policy",
		"never this service or its bundle",
		"halts",
		"budget_unavailable",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §1 does not say %q: this is the step an operator reads first, and the one thing it may not claim is that a Fugaro command reports the service itself", credentialsRunbook, want)
		}
	}
	for _, banned := range []string{
		// Doctor has no such check.
		"names the credentials service's pieces",
		// And the launch does not read the service: cascadeLaunchRefusal
		// (internal/cli/run.go) judges the published switch, the gateway's
		// mode and the rules marker. The sentence this bans claimed the
		// service's absence is itself what refuses the launch, which is the
		// reading that would send an operator to apply the service when the
		// switch is what is missing — and would promise a launch refusal for
		// an installation that has the switch but no service.
		"is refused at launch, before any model call, rather than halting later",
	} {
		if strings.Contains(section, banned) {
			t.Errorf("%s §1 says %q", credentialsRunbook, banned)
		}
	}
}

// TestDocsCredentialsRunbookOrdersTheOptOut: the opt-out is ordered, and the order
// is the part that is easy to get wrong. Every repository root is re-applied
// *before* the two installation steps (re-review N2): the per-repository roots
// are what read the service's address, so removing the service first leaves
// them pointing at a host that is gone. This checks the order the runbook
// writes, not merely that it writes both.
//
// The pin is §9's OWN step and its command, not the loose phrase the section's
// preamble also carries. The first version of this test searched the flattened
// whole document for "re-apply every repository root", and that phrase is in
// the preamble ("This is an ordered procedure — re-apply every repository root
// first, then …"), which sits above every step: the guard stayed green with
// step 1 deleted or moved after step 2, and would have been green with the
// step written anywhere in the runbook at all (code review of C1.22). So the
// read is scoped to §9, the marker pinned is the step's own heading, the step
// is required to carry its command, and the marker is required to be unique in
// the section — a preamble sentence that repeats it would make the pin
// ambiguous again.
func TestDocsCredentialsRunbookOrdersTheOptOut(t *testing.T) {
	doc := readDoc(t, credentialsRunbook)
	section := flattenDoc(docSection(t, credentialsRunbook, doc, "## 9. Turning the service off"))
	// The step's own heading, and the command it runs.
	const step = "Re-apply every repository root, first."
	const cmd = "fugaro init --repo"
	const stateRmCmd = "terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'"
	repo, stateRm := strings.Index(section, step), strings.Index(section, stateRmCmd)
	if repo < 0 {
		t.Fatalf("§9 does not write the first step as %q: the opt-out's first step is the repository roots' re-apply, and a pin on the looser phrase is satisfied by the section's preamble alone", step)
	}
	if stateRm < 0 {
		t.Fatalf("§9 does not write the state rm (%q)", stateRmCmd)
	}
	if !strings.Contains(section[repo:], cmd) {
		t.Errorf("§9's first step does not run %q: the step is the re-apply itself, not a sentence about it", cmd)
	}
	if repo > stateRm {
		t.Error("§9 removes the bundle before re-applying the repository roots: the repository roots read the service's address, so they go first (re-review N2)")
	}
	if strings.Count(section, step) != 1 {
		t.Errorf("§9 writes %q %d times: the pin cannot tell the step from a sentence above it", step, strings.Count(section, step))
	}
	// The negative half, stated as the invariant the scoped read needs: the
	// preamble above the step carries the loose phrase, which is what made the
	// document-wide search match the wrong place.
	if pre := section[:repo]; !strings.Contains(pre, "re-apply every repository root") {
		t.Errorf("§9's preamble no longer carries the loose phrase the old document-wide search matched at %q, so this test's scoping is no longer what keeps it honest — re-read the pin above", "re-apply every repository root")
	}
}

// TestDocsCredentialsRunbookSaysWhatCredsLsAnswersWhenTheBundleIsMissing: §3's
// third step is the one that reads the bundle back, and the code review of
// C1.22 found it claiming an output `fugaro creds ls` cannot produce — "with no
// rows" for a container that holds no document or one the loader refuses.
//
// Neither is reachable. credsLs (internal/cli/creds.go) returns a NAMED
// failure for every one of them — apiErr's 404 ("is not in project"), the
// container with no version (errNoBundleVersion), a newest version DISABLED
// (credsDisabledRefusal), a document the loader refuses (credsDocRefusal) and
// a read the identity may not make (apiErr's 403) — and a listing it does
// print is never empty, because credsvc.ParseBundle refuses a document that
// holds no entry (ErrBundleEmpty). So the step must name those failures, not a
// shape of output that never arrives.
func TestDocsCredentialsRunbookSaysWhatCredsLsAnswersWhenTheBundleIsMissing(t *testing.T) {
	doc := readDoc(t, credentialsRunbook)
	section := flattenDoc(docSection(t, credentialsRunbook, doc, "## 3. The service is down, or refuses everything"))
	for _, want := range []string{
		// The claim itself: a named failure, never an empty list.
		"named failure",
		"never an empty list",
		// Every class credsLs prints for a missing bundle, as creds.go words
		// it (the id and the project are the command's own values, so the
		// runbook writes them as placeholders).
		"the credentials service's bundle container <id> is not in project",
		"the bundle container <id> holds no version: no credential has been set yet",
		"nothing was read: the newest version of the bundle container <id> is DISABLED",
		"is not a document the credentials service accepts",
		"this identity may not read the bundle container <id> in project",
		// The DISABLED state's own way out, which is the owner's and not a
		// `fugaro creds set` alone.
		"gcloud secrets versions enable",
		// And the first write, for the two states it repairs.
		"fugaro creds set NAME",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §3 does not say %q: this is the step an operator reads when the bundle is the problem, and the command's own words are the only thing that tells the five states apart", credentialsRunbook, want)
		}
	}
	// The claim the review found, banned document-wide: no output of `creds ls`
	// is "no rows", and a reader told to look for one waits for something that
	// never prints.
	if flat := flattenDoc(doc); strings.Contains(flat, "no rows") {
		t.Error("the runbook says `creds ls` answers \"no rows\": no such output exists — a missing container, a container with no version, a DISABLED version and a refused document are each a named failure, and a listing that does print holds at least one credential")
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
