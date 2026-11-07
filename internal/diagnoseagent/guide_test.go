package diagnoseagent

// The guide is a document of this repository and a copy inside the binary
// (D1612). A document cannot be embedded from another directory -- a go:embed
// pattern may not contain ".." -- so docs/troubleshooting-for-agents.md is the
// text a person edits and guide.md is the copy the binary embeds, and these
// two tests are what holds them together: one that the embedded copy is the
// guide, and one that the copy is the document.

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// updateGuide rewrites the embedded copy from the document, the way the other
// golden files of this repository are rewritten:
//
//	go test ./internal/diagnoseagent -run Guide -update-guide
var updateGuide = flag.Bool("update-guide", false, "rewrite "+guideCopyPath+" from "+guideSourcePath)

// maxGuideBytes bounds the guide. It is read into the prompt of every
// diagnosis, so it stays short enough to leave room for the evidence.
const maxGuideBytes = 32 << 10

func TestGuideIsEmbeddedAndNonEmpty(t *testing.T) {
	guide := Guide()
	if strings.TrimSpace(guide) == "" {
		t.Fatal("Guide() is empty: the embedded copy is what a diagnosis reads")
	}
	if n := len(guide); n > maxGuideBytes {
		t.Errorf("the guide is %d bytes and the bound is %d: it rides in every prompt", n, maxGuideBytes)
	}
	// The document's own title, so a copy of some other file fails here rather
	// than in a diagnosis nobody can reproduce.
	if !strings.Contains(guide, "# Troubleshooting guide for agents") {
		t.Errorf("Guide() carries no %q title", "# Troubleshooting guide for agents")
	}
	// The generated header, so a copy that stopped naming its source is a copy
	// the next person edits by hand.
	for _, want := range []string{guideHeader, guideSource} {
		if !strings.Contains(guide, want) {
			t.Errorf("Guide() does not name %q", want)
		}
	}
}

func TestGuideCopyMatchesTheDocument(t *testing.T) {
	doc, err := os.ReadFile(guideSourcePath)
	if err != nil {
		t.Fatalf("read %s: %v (run this from the repository)", guideSourcePath, err)
	}
	want := renderGuideCopy(doc)
	if *updateGuide {
		if err := os.WriteFile(guideCopyPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(guideCopyPath)
	if err != nil {
		t.Fatalf("read %s: %v", guideCopyPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not %s: regenerate it with `go test ./internal/diagnoseagent -run Guide -update-guide` and commit both", guideCopyPath, guideSource)
	}
	if embedded := Guide(); embedded != string(want) {
		t.Errorf("the embedded bytes are not %s: the binary would ship a guide that is not the document", guideCopyPath)
	}
}
