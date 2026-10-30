package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsSharedImagesDesign: docs/forge-authorship-images/shared-images.md
// must carry the design the plan's task C7 asks for and C8 builds.
//
// The assertions are two lists, and the difference between them is the point.
//
// The first is made of sentences the design states, each naming the control
// that carries a fact — the four normalisation inputs, the same-versions
// requirement on superset reuse, the propose-then-confirm rule and the three
// choices it offers, the unattended refusal and the three spellings of its
// flag, fork-never-mutate, the prune's job-pointer exception, the ruled cap
// and retention and the layer the retention belongs to, the lock's home and the schema C8
// adds, the create-if-absent first write, the clone shape a source-less image
// needs, and each sub-task's red step. Emptying the control that carries one
// of them fails its own assertion, rather than passing on a heading that is
// still there.
//
// The second is structural anchors — headings, sub-task labels and the plan's
// test names — and they are checked so the document keeps the shape C7 asks
// for and names the tests C8 must make pass. A mutation of a behaviour does
// not, and cannot be made to, fail on an anchor.
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

	// The facts: each string is a control the design states.
	sharedImagesMustContain(t, section, path, "fact", []string{
		// D618 as answered, plus O6's three image-count controls.
		"only resolved package names, versions, toolchain and base image count",
		"only when it holds the whole set at the same versions",
		// T6(c) as resolved: propose-then-confirm, and the unattended refusal.
		"proposed and confirmed, never forked silently",
		"refuses to build when there is no terminal",
		"`--on-divergence=fork`, `--on-divergence=adopt=<repository>`, `--on-divergence=pin`",
		"when that image is a superset of this repository's set at the same versions",
		"keeps the image the repository is on, digest unchanged, and warns",
		// T6(c): the shared image is never mutated.
		"never rebuilt or overwritten in place",
		// The prune's one exception, and its two conditions.
		"never one a job still points at",
		"no refresh has placed a repository on",
		// The retention key, its default, its ruling and its layer.
		"`images.retention_days`, default 30, is confirmed (ruled 2026-10-29)",
		"it is shared policy, and only the project layer sets shared policy",
		// The cap as ruled: 20, a warning and never a refusal.
		"The cap is **20** images per installation (ruled 2026-10-29)",
		"It is a warning and never a refusal",
		// The lock: its home, the schema C8 adds, and its first write.
		"under `builds/` in the runs bucket",
		"`schemas/image-lock.schema.json`, which C8 adds",
		"which is create-if-absent and answers `blobx.ErrExists` when another refresh made it first",
		// A source-less image needs a clone-shaped job, not a baked one.
		"`FUGARO_CHECKOUT=clone` and `FUGARO_REMOTE` on a `checkout: clone` workflow's job and on no",
		"moving its image \"would leave a job expecting a baked checkout\"",
		"finds no checkout and no remote",
		"it is a Cloud Build build argument",
		"Route A is the design's recommendation",
		// C3's ordering is what lets the shared image hold no source, and C8
		// can ship without C4's warning but not without C3.
		"key files before the source layer",
		"the digest compared against the lock for the repository's key",
		"C8 cannot ship without it",
		"C8 can ship without it",
		// The gap list and the owner's items are in the document.
		"SECURITY.md and its website copy",
		"a third `checkout:` kind",
		"route B, the markers written onto the job",
		// Each sub-task's red step, which is the behaviour it starts from.
		"`TestKeyIdenticalForIdenticalDependencySets` fails: there is no `imagecheck.Key`",
		"`TestRefreshBuildsOnceForSharedKey` fails: two repositories with one set build twice",
		"`TestPruneKeepsAnImageAJobPointsAt` fails: there is no `fugaro image prune`",
	})

	// The structure: headings, sub-task labels and the plan's test names.
	sharedImagesMustContain(t, section, path, "structure", []string{
		"C7",
		"## C8 split and its dependencies",
		"### C8a. The key, the superset and the lock",
		"### C8b. Refresh, proposal and the per-repository job update",
		"### C8c. `image ls`, prune, the cap warning, the reference and the skill",
		"## Owner rulings",
		"`TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands`",
		"`TestDivergenceNonInteractiveRefusesWithoutFlag`",
		"`TestPruneKeepsAnImageAJobPointsAt`",
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

// sharedImagesMustContain fails the test for each want absent from the design,
// so a mutation of any single control is one failing assertion rather than one
// indistinguishable failure. part names which list the string came from, so a
// failure says whether a fact or the document's structure went missing.
func sharedImagesMustContain(t *testing.T, got, path, part string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the design's %s is missing %q (%s)", part, w, path)
		}
	}
}
