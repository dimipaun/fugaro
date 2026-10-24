package cli

import (
	"strings"
	"testing"
)

// The page a first-time user follows is docs/gcp-setup.md, and the refuses it
// answers are named in the CLI: the project stage answers a refused creation
// with `See docs/gcp-setup.md#not-an-owner.` (refusedCreate,
// internal/cli/init_project.go), and a refused billing link sends the reader to
// the same anchor. The page keeps that anchor and the roles a reader asks for,
// named as their administrators name them: those are the words the reader
// repeats. The Windows forms and the other sections the page used to carry are
// gone with the fresh-setup-only page; docs/windows-cli/wsl.md is where the
// shell-dependent commands live now.
const gcpSetupDoc = "docs/gcp-setup.md"

func TestDocsGCPSetupNotAnOwnerSaysWhatCreatingAndLinkingNeed(t *testing.T) {
	doc := readDoc(t, gcpSetupDoc)
	sec := docSection(t, gcpSetupDoc, doc, "## If it fails")
	for _, want := range []string{
		"roles/resourcemanager.projectCreator",
		"roles/billing.user",
		"gcloud billing projects link",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s's \"If it fails\" section lacks %q: refusedCreate sends a reader with no project here for the roles to ask for", gcpSetupDoc, want)
		}
	}
	if !strings.Contains(doc, `<a id="not-an-owner"></a>`) {
		t.Errorf("%s has no not-an-owner anchor: refusedCreate names it in the line it prints", gcpSetupDoc)
	}
}

// The page is the short one the owner asked for: prerequisites, the command,
// the check and the refusals, and nothing else. A section of the old page
// reappearing here is how the page grew to 688 lines before.
func TestDocsGCPSetupStaysShort(t *testing.T) {
	lines, err := fileLines(gcpSetupDoc)
	if err != nil {
		t.Fatal(err)
	}
	if lines > 60 {
		t.Errorf("%s is %d lines: the page is meant to be one short page", gcpSetupDoc, lines)
	}
}
