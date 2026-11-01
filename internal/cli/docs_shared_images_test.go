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
		"only resolved package names, versions and their integrities, toolchain",
		// D618 in §2.3: the integrity is part of the fact, a non-registry
		// source is unkeyed, and an unnamed key file is unkeyed rather than
		// "no dependencies" — the three controls the reduction is built to.
		"carries the integrity or checksum the file states for that version's artifact",
		"**a source that is not the ecosystem's registry**",
		"the default for a key file is unkeyed, never \"no dependencies\"",
		// C8b1b's read bound: Reduce is handed bytes that are already in
		// memory, so the caller bounds its own read.
		"The bound is on bytes already in memory, so the caller bounds its own read",
		// §2.4's escaping rule: a fact is one line whatever it holds.
		"Each literal is a **quoted Go string literal**",
		// §2.4's encoding rule, which review round 2 found missing: the three
		// fields are each their own literal, the third always written, so two
		// different triples can never be one fact — and the key's version moved
		// with the change, as it did again for §2.3's Yarn patch rule.
		"key v3",
		"Each of the three is its own quoted literal, one space between",
		"The three fields are encoded apart because a fact must be injective",
		"which the spelling without per-field quoting could not say",
		// The form's bytes, as the block writes them: each line is a prefix
		// and then its whole fact as one quoted literal, so the `pkg:` line's
		// inner quotes are escaped. A block that showed the three literals
		// bare would be a sketch of the form, and a reader taking it as the
		// bytes would hash a line the reduction never writes.
		"pkg:\"\\\"<name>\\\" \\\"<version>\\\" \\\"<integrity>\\\"\"",
		"a prefix and then its whole fact written as **one quoted Go",
		// §2.3's empty-integrity rule and the table that names each ecosystem's
		// default registry, which is what "this source is the default registry's"
		// means per format.
		"A package a file states no integrity for is keyed only when the file also says it came from the",
		"| Ecosystem | The default registry, and what the format writes for it |",
		"and a key that is a bare `name@version` is the default registry's by the format's convention",
		// §2.3's no-facts paragraph: those files run during the install and are
		// not facts of the key — the claim C6 and SECURITY.md repeat.
		"**The no-facts table is a statement about the key, not about what those files do.**",
		// The three source shapes read one step earlier, in the header or in the
		// version: a Yarn alias, a Yarn git or codeload dependency, npm v1's
		// source-in-version.
		"states the source in its `version` field",
		"its header states the git specifier or the `owner/repo`",
		// The two reductions round 2 fixed, and the claim round 3 corrected:
		// go.sum's two hashes are joined, the order a file writes them in is
		// not a fact, and a pnpm block resolution is read.
		"both are joined into one integrity, **sorted and deduplicated**",
		"would be another key",
		"A `resolution:` written as a block",
		// Round 3's three gaps: a pnpm v9 snapshot key's peers name the same
		// package as the packages entry, every yarn descriptor is a source,
		// and a Pipfile entry with no hashes is read through its own index.
		"and the `packages` entry for the same x are one package and one fact",
		"**every** range it lists, not only the first",
		"An entry that states no hashes is keyed only when the lock says it came from PyPI's own index",
		"That convention is the one place the key rests on a file it does not read",
		// Round 3's follow-up: a `@registry:` key names a registry rather than a
		// version and is a source like the other pnpm markers, and a lock older
		// than the two sections states its whole resolved set where the reducer
		// does not look, so it is refused rather than read as one that holds
		// nothing.
		"`@patch:` or `@registry:`",
		"a pnpm lock older than the `packages` map writes each",
		"is refused rather than read as one that holds nothing (§2.5)",
		// The review's last four: each key form is named by the lockfileVersion that
		// writes it — the leading `/` is lockfileVersion 6.0's, and a
		// lockfileVersion 5.x lock's `/name/version` key is refused rather than
		// read as a package whose name holds the version's own text — and a
		// source line is read as a URL, whose host is the one its authority
		// names.
		"with the leading `/` a lockfileVersion 6.0 lock (pnpm 8) writes",
		"so reading one would key a fact for a package like `react-dom/18.0.0_react`",
		"lockfileVersion 5.x lock (pnpm 5, 6 and 7) writes `/name/version` there, hanging the peer suffixes",
		"the host is the one the URL's authority names",
		// §3.1's entries are the three quoted literals, quotes and all.
		"a package entry is the fact's three quoted literals",
		"\"\\\"left-pad\\\" \\\"1.3.0\\\" \\\"sha512-aaa\\\"\"",
		// §3.1's one-job rule reads the repository and the name, and says what
		// that cannot catch — the rename C8b1b's refresh owns.
		"catch is one job **renamed** between two entries",
		// §4 compares the whole facts.
		"resolved to the same artifacts, compared as the three quoted literals",
		// Reuse compares whole facts, so the integrity is part of it (D618).
		"only when it holds the whole set at the same versions and the\nsame integrities",
		// T6(c) as resolved: propose-then-confirm, and the unattended refusal.
		"proposed and confirmed, never forked silently",
		"refuses to build when there is no terminal",
		"`--on-divergence=fork`, `--on-divergence=adopt=<repository>`, `--on-divergence=pin`",
		"when that image is a superset of this repository's set at the same versions and integrities",
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
		// D622's split: the operator's machine computes the key and passes it
		// in, and the build's record step writes what it was given (§2.5,
		// §10). Each pin is one line of the document, so the fact is stated
		// in both places that have to state it.
		"it computed to the build as an input, and the build's record step writes the value it was given",
		"and never recomputes one of its own (D622)",
		"The build is handed the key the refresh",
		"computed and its record step writes that value as it is (D622): the build never recomputes a key",
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
