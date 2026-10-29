package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsSharedImagesDesign: docs/forge-authorship-images/shared-images.md
// must carry the design the plan's task C7 asks for and C8 builds. The
// assertions are per-fact so that a reader of the doc and a reader of this
// test read the same list: the normalised key and its four inputs, superset
// reuse and its version requirement, the propose-then-confirm flow and its
// non-interactive refusal, fork-never-mutate, the prune's one exception, the
// proposed retention, and the three sub-tasks C8 splits into with what C8
// needs from C3 and C4.
//
// Each string below is a sentence the design states, not a label: deleting
// the control that carries a fact (the normalisation list, the
// same-versions requirement, the `--on-divergence` refusal, the never-mutate
// rule, the job-still-points-at exception, the proposed retention) fails this
// test rather than passing on the section's heading alone.
func TestDocsSharedImagesDesign(t *testing.T) {
	const path = "../../docs/forge-authorship-images/shared-images.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	// The whole document: C7's design is its own file. The title is an H1 and
	// the file holds no other H1, so the section docSection cuts is the file.
	section := docSection(t, path, doc, "# Shared images keyed by the resolved dependency set")
	if !strings.Contains(section, "C7") {
		t.Errorf("%s does not name the plan task C7 it is the design for", path)
	}

	sharedImagesMustContain(t, section, path, []string{
		// D618 as answered, plus O6's three image-count controls.
		"only resolved package names, versions, toolchain and base image count",
		"only when it holds the whole set at the same versions",
		// T6(c) as resolved: propose-then-confirm, and the unattended refusal.
		"proposed and confirmed, never forked silently",
		"refuses to build when there is no terminal",
		"`--on-divergence`",
		"`adopt <repository>`",
		"stays pinned",
		// T6(c): the shared image is never mutated.
		"never rebuilt or overwritten in place",
		// The prune's one exception.
		"never one a job still points at",
		// The retention key, its default and its status.
		"`images.retention_days`, default 30, is proposed, owner to confirm",
		// The lock and the schema C8 adds.
		"under `builds/` in the runs bucket",
		"`schemas/image-lock.schema.json`, which C8 adds",
		// The C8 split and what C8 needs from C3 and C4.
		"## C8 split and its dependencies",
		"C8a.",
		"C8b.",
		"C8c.",
		"`TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands`",
		"`TestDivergenceNonInteractiveRefusesWithoutFlag`",
		"`TestPruneKeepsAnImageAJobPointsAt`",
		"key files before the source layer",
		"the digest compared against the lock",
		// The open items are the owner's, not the design's.
		"## Open for the owner",
	})

	// The folder's index gains one row, and the row links the file it names.
	readme, err := os.ReadFile("../../docs/forge-authorship-images/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "| [shared-images.md](shared-images.md) |") {
		t.Error("docs/forge-authorship-images/README.md has no index row for shared-images.md")
	}
}

// sharedImagesMustContain fails the test for each want absent from the
// design, so a mutation of any single control is one failing assertion rather
// than one indistinguishable failure.
func sharedImagesMustContain(t *testing.T, got, path string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s is missing %q", path, w)
		}
	}
}
