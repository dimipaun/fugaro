package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The release notes of 0.9.0 (docs/releases/v0.9.0.md) are the Highlights the
// release page publishes, and they are the one document nothing else in the
// tree reads: a claim in them that the tree does not hold is a claim no
// compiler, no schema and no other test can catch, and the file is written
// once, before the tag, by someone reading a branch. These tests are the pin:
// the first holds the file to the format docs/releases/README.md states (3 to
// 6 user bullets, `###` headings and no `##`, the two section titles the
// website's changelog check recognises), and the second holds each of its
// claims about this release to the symbol the tree carries it in, so a claim
// whose symbol moves — `sealSince`, `cloneShipped`, the policy version that
// says 0.9.0 — fails here rather than shipping on the release page.
const releaseNotesV090 = "../../docs/releases/v0.9.0.md"

// releaseNotesSections are the two headings docs/releases/README.md names, and
// website/src/lib/changelog.mjs recognises, in full: any other `###` line in a
// release file is text the site renders as written and its own check refuses.
var releaseNotesSections = []string{"For operators", "Known limits"}

// readReleaseNotesV090 reads the file and fails with its path, so a moved or
// deleted file names itself rather than one of the assertions below.
func readReleaseNotesV090(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(releaseNotesV090)
	if err != nil {
		t.Fatalf("reading %s: %v (the release notes are a required artifact from 0.4.0 on: scripts/release.sh and the release workflow both refuse without them)", releaseNotesV090, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("%s is empty; the release workflow refuses to publish an empty one", releaseNotesV090)
	}
	return string(data)
}

// TestReleaseNotesV090FollowsTheFormat holds the file to docs/releases/README.md:
// 3 to 6 bullets for users before the first section, both section headings
// written as `###` (a `##` inside the file would be a second top-level heading
// beside GoReleaser's own `## Highlights`), no other heading, and the
// operators' steps numbered from 1 in order, each one labelled with the place
// it is run in.
func TestReleaseNotesV090FollowsTheFormat(t *testing.T) {
	lines := strings.Split(readReleaseNotesV090(t), "\n")

	var bullets int
	var headings []string
	var steps []string
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "### "):
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")))
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			continue
		case strings.HasPrefix(trimmed, "## "), trimmed == "##", strings.HasPrefix(trimmed, "# "):
			t.Errorf("%s carries the top-level heading %q: the file's headings are `###`, because GoReleaser puts the file under its own `## Highlights`", releaseNotesV090, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if section == "" {
				bullets++
			}
			continue
		}
		if section == "For operators" && releaseNotesStepRE.MatchString(trimmed) {
			steps = append(steps, trimmed)
		}
	}

	if bullets < 3 || bullets > 6 {
		t.Errorf("%s has %d bullets for users; docs/releases/README.md asks for 3 to 6", releaseNotesV090, bullets)
	}
	for _, h := range headings {
		if h != releaseNotesSections[0] && h != releaseNotesSections[1] {
			t.Errorf("%s carries the heading %q; the two a release file may carry are %q and %q, and the site's changelog check refuses any other", releaseNotesV090, h, releaseNotesSections[0], releaseNotesSections[1])
		}
	}
	for _, want := range releaseNotesSections {
		if !strings.Contains(strings.Join(headings, "\n"), want) {
			t.Errorf("%s has no `### %s` section", releaseNotesV090, want)
		}
	}
	if len(steps) == 0 {
		t.Fatalf("%s has no numbered step under `### For operators`", releaseNotesV090)
	}
	for i, step := range steps {
		if want := strings.TrimSpace(releaseNotesStepRE.FindString(step)); want != strconv.Itoa(i+1)+"." {
			t.Errorf("the operators' steps are out of order at %q: want step %d, got %q", step, i+1, want)
		}
		if !releaseNotesStepPlace(step) {
			t.Errorf("the operators' step %q names no place it is run in: every command is **per machine**, **per repo checkout** or **per installation**, because the three are different objects and a reader who runs one where another belongs changes the wrong thing", step)
		}
	}
}

// releaseNotesStepRE is a numbered step of the operators' list, and its number
// is what the order is read from.
var releaseNotesStepRE = regexp.MustCompile(`^(\d+)\. `)

// releaseNotesStepLabelRE is the first bold span of a step, which is where the
// place it is run in is written.
var releaseNotesStepLabelRE = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// releaseNotesStepPlaces are the three places this release's steps
// distinguish, because the three are different objects: the CLI is installed
// per machine, the plugin pin and the images are per repository checkout, and
// the installation's cloud resources are per installation. A label may say
// more than the place — "**Per installation, in your own terminal**" is the
// place and how to run it — so the place is a prefix of the label rather than
// the whole of it.
var releaseNotesStepPlaces = []string{"Per machine", "Per repo checkout", "Per installation"}

func releaseNotesStepPlace(step string) bool {
	m := releaseNotesStepLabelRE.FindStringSubmatch(step)
	if m == nil {
		return false
	}
	for _, place := range releaseNotesStepPlaces {
		if strings.HasPrefix(m[1], place) {
			return true
		}
	}
	return false
}

// TestReleaseNotesV090ClaimsHoldInTheTree pins each claim the notes make about
// this release to the symbol the tree carries it in. The pairs are deliberate:
// the sentence is asserted beside the constant, so a claim and its symbol
// cannot drift apart in either direction — a feature that moves to another
// release fails here because the notes still say 0.9.0, and a claim rewritten
// while its symbol stands fails here because the sentence it was checked
// against is gone.
//
// Mutation (verified, each planted and reverted): flip `cloneShipped` to false;
// change `sealSince` to "0.8.5"; drop the row naming 0.9.0 from
// projectpolicy.VersionSince; delete the sealed token's object name from the
// first bullet; drop a step's `**Per installation**` label; add two bullets so
// the file carries eight; write a section as `## Known limits`; rename Check 60
// in the limits; drop the `fugaro managed flag` sentence; rewrite the
// project-export, provision-batch and scoped-role limits into descriptions of
// the commands; and replace the base image's pin with the copy it stopped being
// — each of which fails here.
func TestReleaseNotesV090ClaimsHoldInTheTree(t *testing.T) {
	notes := readReleaseNotesV090(t)

	// The seal: the notes say the token is encrypted under a per-launch key and
	// written under tokens/, and the tree says the release that reads a sealed
	// token is this one.
	if !strings.Contains(notes, "`tokens/<slug>/<run>/budget-token`") {
		t.Errorf("%s does not name the sealed token's object (tokens/<slug>/<run>/budget-token, internal/budget/token's ObjectKey)", releaseNotesV090)
	}
	if sealSince != "0.9.0" {
		t.Errorf("sealSince is %q: the notes say the seal ships in 0.9.0, and the constant is the release whose runner reads a sealed token", sealSince)
	}
	if !strings.Contains(notes, "seal") {
		t.Errorf("%s no longer claims the sealed budget token", releaseNotesV090)
	}

	// The clone gate: the notes say a `checkout: clone` workflow reaches a
	// shared job, which is true only while cloneShipped is true.
	if !strings.Contains(notes, "`checkout: clone`") {
		t.Errorf("%s no longer names `checkout: clone`", releaseNotesV090)
	}
	if !cloneShipped {
		t.Errorf("cloneShipped is false, so a `checkout: clone` launch is refused on every image a release can build: %s may not say one reaches a shared job", releaseNotesV090)
	}

	// The policy version: the notes' rollout is a republish, and the version the
	// node moves to is the one this release introduced.
	if got := projectpolicy.VersionSince[projectpolicy.Version]; got != "0.9.0" {
		t.Errorf("projectpolicy.Version is %d and VersionSince names %q for it; the notes describe this release's rollout, so the version it publishes must be the one this release introduced",
			projectpolicy.Version, got)
	}
	if !strings.Contains(notes, "fugaro project policy publish --delete") {
		t.Errorf("%s does not carry the republish the version bump needs (a publish refuses to overwrite a node written in another version)", releaseNotesV090)
	}

	// The rest of the release's surface, each named as the command or key a
	// reader would look for.
	for what, want := range map[string]string{
		"the named accounts":     "claude_accounts",
		"the managed flag":       "fugaro managed flag",
		"the managed apply":      "fugaro managed apply",
		"the managed label":      "fugaro_managed=true",
		"runs stats by person":   "fugaro runs stats --by user",
		"who launched a run":     "launched_by",
		"the BY column":          "BY column",
		"the launcher's grant":   "`tokens/`",
		"the installer's repair": "fugaro project repos add",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("%s does not name %s (%q), which this release ships", releaseNotesV090, what, want)
		}
	}

	// The images: this release's base is pinned, not copied, and the sentence
	// that said otherwise is one an earlier draft of these notes carried. The
	// base image stopped being copied before 0.9.0 — docs/release.md states it
	// in bold ("The base image is not copied any more") and internal/cli's
	// refresh resolves the release's `-pinned` tag, verifies the cosign
	// signature and pins the digest the installation's own remote repository
	// serves — so an operator step that describes a copy sends the reader to a
	// billable build that no longer happens.
	if !strings.Contains(notes, "`-pinned` tag") || !strings.Contains(notes, "nothing is copied") {
		t.Errorf("%s does not describe what `fugaro image refresh` does with the base image: it resolves this release's `-pinned` tag, verifies the cosign signature and pins the digest the installation's remote repository serves (nothing is copied, and nothing is pushed)", releaseNotesV090)
	}
	for _, absent := range []string{"copies this release's base image", "copies the base image", "copy of the base image"} {
		if strings.Contains(notes, absent) {
			t.Errorf("%s says %q: the base image is not copied any more (docs/release.md, internal/cli/image_refresh.go), so the notes may not describe a copy", releaseNotesV090, absent)
		}
	}

	// The known limits: the checks this release cannot run, and the three
	// things it deliberately does not claim. Each is a sentence a reader
	// relies on, so the wording that says it is pinned rather than the shape:
	// a limit rewritten into a feature — "`fugaro project export` is not in
	// this release" turned into a description of what it does — is the one
	// edit this list exists to refuse.
	for what, want := range map[string]string{
		"the sealed token's live check":  "Check 60",
		"the shared job's live check":    "Check 53",
		"the not-yet-run marker":         "NOT RUN",
		"the scoped deployer's absence":  "The managed deployer's scoped role is still not created",
		"the Batch backend still off":    "`fugaro init --provision-batch` refuses",
		"the project export not shipped": "`fugaro project export` is not in this release",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("%s does not say %s (%q) under `### Known limits`", releaseNotesV090, what, want)
		}
	}

	// The one direction the README states outright: a bullet is what a user can
	// do now, never the changelog's commit titles, and nothing internal-only.
	// The changelog's own prefixes are the words a bullet written from one
	// carries.
	for _, line := range strings.Split(notes, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		for _, prefix := range []string{"ci:", "chore:", "test:", "refactor:", "docs:"} {
			if strings.Contains(line, prefix) {
				t.Errorf("%s carries %q in a bullet: docs/releases/README.md asks for what a user can do now, not the commit titles the changelog already holds", releaseNotesV090, prefix)
			}
		}
	}
}

// releaseFilesDir is the directory of Highlights files whose format
// docs/releases/README.md states, and which the website's changelog page reads.
const releaseFilesDir = "../../docs/releases"

// TestReleaseFilesCarryNoMarkdownLinks holds every release file to the
// renderer's contract: `website/src/components/Changelog.astro`'s inline()
// renders “ `code` “ and `**bold**` and nothing else — "The files carry no
// link, no image and no other construct — the generator hands the lines over as
// written" — so a markdown link in a release file is printed as its own literal
// text on the changelog page, and the repository-relative path it carries
// resolves nowhere in the GitHub release body either. No gate catches it: the
// site's link checker scans built HTML for hrefs (raw markdown produces none)
// and its changelog check asserts only that a rendered line is the file's own
// text verbatim. The rule is over the directory rather than over v0.9.0.md
// because the property is the format's — v0.9.0.md was the first release file to
// carry a link, and it is the only one that has. `README.md` is skipped by name,
// the way the site's own check skips it: the format document is where the
// documents a release may name are linked.
//
// Mutation (verified, planted and reverted): put
// `[docs/project-layer.md](../project-layer.md)` back into the 0.9.0 notes'
// known limits — the link this test was written for.
func TestReleaseFilesCarryNoMarkdownLinks(t *testing.T) {
	entries, err := os.ReadDir(releaseFilesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", releaseFilesDir, err)
	}
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		files++
		data, err := os.ReadFile(releaseFilesDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s/%s: %v", releaseFilesDir, name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "](") && !strings.Contains(line, "![") {
				continue
			}
			t.Errorf("%s/%s:%d carries a markdown link or image (%q): the changelog page renders a release file's lines as they are written — `code` and **bold** only, per website/src/components/Changelog.astro's inline() — so a link is printed as its literal text there and resolves nowhere in the release body; name the document in prose or in a `code` span instead",
				releaseFilesDir, name, i+1, strings.TrimSpace(line))
		}
	}
	if files == 0 {
		t.Errorf("%s holds no release file: the enumeration is broken, and a rule over no files is not a rule", releaseFilesDir)
	}
}
