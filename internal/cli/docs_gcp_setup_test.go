package cli

import (
	"strings"
	"testing"
)

// docs/gcp-setup.md is the guide a first-time user follows, and until this file
// it had no pin of its own: nothing asserted a word of it. The one thing that
// made that a defect rather than a gap is O3 of docs/windows-cli/reconciliation.md
// — `grep -n -i windows docs/gcp-setup.md` answered nothing — while two of the
// commands the page has the user run are shell commands, whose form depends on
// the shell the reader is in: the ADC login of precondition 1, and the `Fix`
// lines `init` and `fugaro doctor` print, which say "unset K"
// (internal/preflight/preflight.go) and are POSIX today (F6, F27).
// docs/windows-cli/implementation-plan.md T0.2 is the task; this is its guard,
// and the guard is the task's first deliverable because the document had none.
const gcpSetupDoc = "docs/gcp-setup.md"

// gcpSetupWindowsSection is the heading the two forms live under. The name is
// pinned rather than the position: a reader who has just read the preconditions
// needs to be able to find the section that says which of these commands is
// theirs, and a rename is what would take that away.
const gcpSetupWindowsSection = "## On Windows and in WSL: the two commands you run yourself"

// TestDocsGCPSetupNamesTheWindowsForm: the guide carries both forms of the two
// commands it has a Windows reader run. The login's two *files* are the claim
// that makes the environment rule necessary — a Windows gcloud and a WSL gcloud
// write different files, so signing in on one does not sign the user in on the
// other — and the `unset` half is the Fix line's own command in each shell the
// page names, with F6 recorded so the page cannot present the Windows form as
// what the CLI prints while the printed line is still POSIX.
func TestDocsGCPSetupNamesTheWindowsForm(t *testing.T) {
	doc := readDoc(t, gcpSetupDoc)

	for _, want := range []string{
		gcpSetupWindowsSection,
		// The two directories. They are the reason both forms have to be here:
		// the file a Windows gcloud writes is not the file a WSL gcloud writes.
		`%APPDATA%\gcloud\application_default_credentials.json`,
		"~/.config/gcloud/application_default_credentials.json",
		// The login itself, which is the same line in both environments.
		"gcloud auth application-default login",
		// The Fix line's "unset K", once per shell the page names.
		"unset GOOGLE_IMPERSONATE_SERVICE_ACCOUNT",
		"Remove-Item Env:GOOGLE_IMPERSONATE_SERVICE_ACCOUNT",
		"set GOOGLE_IMPERSONATE_SERVICE_ACCOUNT=",
		"PowerShell",
		"cmd.exe",
		// The finding behind the Fix half: the printed line is POSIX, so the
		// page states the Windows form as the reader's own command, not as the
		// CLI's output.
		"**F6**",
		// The route that works today, which is where the page sends a reader
		// who wants to know what runs natively.
		"windows-cli/wsl.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: T0.2's two commands need both forms in the guide, and this is one of them", gcpSetupDoc, want)
		}
	}

	// The sentences, flattened so a re-wrap does not break the check: what the
	// page tells the reader to do, and why the two files are two files.
	flat := flattenDoc(doc)
	for _, want := range []string{
		"in the environment that will run `fugaro`",
		"different files",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q (whitespace flattened): the environment rule and the two-files fact are the section's whole reason", gcpSetupDoc, want)
		}
	}
}
