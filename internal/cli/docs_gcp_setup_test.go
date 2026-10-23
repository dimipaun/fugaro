package cli

import (
	"strings"
	"testing"
)

// docs/gcp-setup.md is the guide a first-time user follows, and it is not an
// unpinned page: its base-image section
// (TestGCPSetupBaseImageSectionNamesRealKinds in docs_base_test.go), its
// readiness-gate wording, its bucket-IAM phrasings, its project-wide git
// credential paragraph and its update-check section each have a guard that
// reads it. What none of those guards assert is the *shell* its commands are
// run in, which is the claim this file adds: two of the commands the page has
// the user run are shell commands whose form depends on the shell the reader is
// in — the ADC login of precondition 1, and the `Fix` lines `init` and `fugaro
// doctor` print, which say `unset` (internal/preflight/preflight.go) and are
// POSIX today (F6, F27). O3 of docs/windows-cli/reconciliation.md is what made
// that a defect rather than a gap — at the revision that document reads
// (`54cb02a8`), `grep -n -i windows docs/gcp-setup.md` answered nothing.
//
// The three `Fix` lines are not one line three times: the project-variable
// check prints the variable's name to substitute, while the impersonation and
// GODEBUG checks print "unset it" and leave the name to the `Problem` line
// above them. The pins below hold both shapes, because a page that told a
// Windows reader to substitute a name the CLI never prints would send them
// looking for one.
// docs/windows-cli/implementation-plan.md T0.2 is the task; this is its guard,
// and the guard is the task's first deliverable because the two shell-dependent
// forms had none — not because the page had none.
const gcpSetupDoc = "docs/gcp-setup.md"

// gcpSetupWindowsSection is the heading the two forms live under. The name is
// pinned rather than the position: a reader who has just read the preconditions
// needs to be able to find the section that says which of these commands is
// theirs, and a rename is what would take that away.
const gcpSetupWindowsSection = "## On Windows and in WSL: the two commands you run yourself"

// TestDocsGCPSetupNotAnOwnerSaysWhatCreatingAndLinkingNeed: the project stage
// answers a refused creation with `See docs/gcp-setup.md#not-an-owner.`
// (refusedCreate, internal/cli/init_project.go), and a refused billing link
// with the two roles that link needs, so #not-an-owner is where the reader
// lands. The section says what creating a project and linking billing need,
// and it names the roles as their administrators name them: those are the
// words the reader repeats.
func TestDocsGCPSetupNotAnOwnerSaysWhatCreatingAndLinkingNeed(t *testing.T) {
	doc := readDoc(t, gcpSetupDoc)
	sec := docSection(t, gcpSetupDoc, doc, "### If setup cannot create the project or link billing")
	for _, want := range []string{
		"roles/resourcemanager.projectCreator",
		"roles/billing.user",
		"billing.resourceAssociations.create",
		"Owner or Project Billing Manager",
		"gcloud billing projects link",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s's #not-an-owner section lacks %q: refusedCreate sends a reader with no project here for the roles to ask for", gcpSetupDoc, want)
		}
	}
	if !strings.Contains(doc, `<a id="not-an-owner"></a>`) {
		t.Errorf("%s has no not-an-owner anchor: refusedCreate names it in the line it prints", gcpSetupDoc)
	}
}

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
		// The Fix line's `unset`, once per shell the page names.
		"unset GOOGLE_IMPERSONATE_SERVICE_ACCOUNT",
		"Remove-Item Env:GOOGLE_IMPERSONATE_SERVICE_ACCOUNT",
		"set GOOGLE_IMPERSONATE_SERVICE_ACCOUNT=",
		"PowerShell",
		"cmd.exe",
		// The two `Fix` lines that print no name, quoted as
		// internal/preflight/preflight.go writes them, and the `Problem` line
		// that carries the name in their place. Only the project-variable
		// check's line has a name to substitute, and a page that says
		// otherwise is wrong about what the reader will see.
		// The one line that does print a name: the CLI's own template with the
		// project as the page's placeholder, since the name it prints is the
		// reader's.
		"unset GOOGLE_PROJECT (or set it to <gcp-project>) and rerun",
		"fugaro runs as your own credentials only; unset it and rerun",
		"unset it (or drop http2debug) and rerun",
		"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT is set",
		// The project variables are the ones whose line does print a name, and
		// the page has to say so for the substitution to mean anything.
		"GOOGLE_PROJECT",
		"GOOGLE_CLOUD_PROJECT",
		"CLOUDSDK_CORE_PROJECT",
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
		// Which preconditions are shell commands at all. An earlier page
		// claimed only the login and the environment, and said everything
		// else was `fugaro`, which is false of the quota project, billing,
		// serviceusage, Terraform and the build identity — gcloud and
		// terraform commands the reader runs in every subsection.
		"The `gcloud` lines under Install, Log in and Before you onboard a repository",
		"`terraform version` under Install",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q (whitespace flattened): the environment rule, the two-files fact and which preconditions are shell commands at all are what the section exists to say", gcpSetupDoc, want)
		}
	}
}
