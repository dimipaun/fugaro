package cli

// The licensing statement (plan task L1.3; decision D301 as amended by the
// owner's O2 answer, D305 as ruled by T1, and D310): the core stays under the
// MIT licence in an untouched `LICENSE`, the scope statement lives in a new
// root `LICENSING.md`, the README's Licensing section states the split and
// carries the trademark paragraph, and the vendored skills carry their own
// upstream licence under `.agents/`.
//
// Nothing here reads a licence key, a subscription or a network: the four
// files are the whole subject. The one thing a test cannot read is what
// GitHub's detectors make of the pair `LICENSE` and `LICENSING.md` side by
// side, and that is the owner-run Check 130, not this file.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// rootLicenceSHA256 is `LICENSE`'s SHA-256 as it stood at e924481cd, the commit
// the licensing reconciliation was written against, and the hash every task of
// that work has to leave alone (O2).
const rootLicenceSHA256 = "93ad9237c1340274471654c42fc6e2a72be2cc68b915401ab22f1b98dff6ef99"

// rootLicenceFirstLine is the line GitHub's licence detector reads a licence's
// name from, and the line the website's licenceIdentifier() reads the same fact
// from: a scope statement above it is what made a published repository answer
// `other` where this one has to keep answering MIT (F27, U1).
const rootLicenceFirstLine = "MIT License"

// docsTrademarkHeading is the README heading the statement's trademark link
// points at (D310). Check 130 follows that link as a fragment, and a fragment
// naming no heading lands at the top of the README with no error at all, so the
// heading and the fragment are derived from this one string rather than written
// twice.
const docsTrademarkHeading = "### Trademarks"

// TestDocsLicensingRootLicenceUnchanged is the guard the open-core skeleton is
// built on. GitHub classifies a repository's licence from the text of the file
// it finds, so `LICENSE` is not edited by any task of this work: the statement
// that `ee/` is excluded and reserved goes in `LICENSING.md` beside it, and
// this file stays byte for byte what it was (O2, D301 as amended).
func TestDocsLicensingRootLicenceUnchanged(t *testing.T) {
	// Read with CRLF folded to LF: git checks a Windows clone out with CRLF
	// endings, and a checkout's line endings are not a licensing decision.
	licence := strings.ReplaceAll(readDoc(t, "LICENSE"), "\r\n", "\n")
	sum := sha256.Sum256([]byte(licence))
	if got := hex.EncodeToString(sum[:]); got != rootLicenceSHA256 {
		t.Errorf(`LICENSE hashes to %s, want %s -- its hash at e924481cd, the commit the licensing reconciliation was written against.

No licensing task edits LICENSE (O2). The reason is the detector, not tidiness: a scope statement inside this file makes GitHub answer "other" instead of MIT for the whole repository, which is the reading the owner's O2 answer took the alternative to avoid, and the reading the website's licenceIdentifier() depends on. If the file really must change -- counsel naming an entity for the copyright line is the one case the plan allows -- the change and this hash move together in one commit, on the owner's explicit go.`, got, rootLicenceSHA256)
	}
	if first, _, _ := strings.Cut(licence, "\n"); first != rootLicenceFirstLine {
		t.Errorf("LICENSE's first line is %q, want %q: the first line is the licence's name to GitHub's detector and to the website's licenceIdentifier(), so a preamble above it renames the repository's licence", first, rootLicenceFirstLine)
	}
}

// TestDocsLicensingStatement: `LICENSING.md` is the scope statement the root
// `LICENSE` no longer carries, and it is what a reader who wants to know what
// the MIT licence does not cover opens. It names the two excluded parts in the
// words the rest of the tree uses for them, says that the versions already
// published keep the terms they were published under (R4), and carries exactly
// the links a reader needs to check each claim.
//
// It reads the real `ee/` through readRealEE, so its name is in eeReadingTests
// and the "core without ee/" workflow skips it there.
func TestDocsLicensingStatement(t *testing.T) {
	const path = "LICENSING.md"
	doc := readDoc(t, path)
	// Read flattened and folded, so a re-wrapped sentence or a capitalised
	// heading is not what this test is about: the facts stated are.
	flat := strings.ToLower(flattenDoc(doc))

	for _, want := range []string{
		// The split, in the statement's own words: the core's licence, the
		// business-licensed directory's exclusion, the vendored code's own
		// terms. The word for `ee/` is the one its own page uses since D532:
		// the folder holds the business-licensed features, new ones and those
		// moved out of the core, so "reserved for net-new organisation
		// features" is no longer what it says (E3.1).
		"`ee/`",
		"excluded",
		"business-licensed",
		"`.agents/`",
		"third-party",
		// R4, in the one sentence the README and the 0.11.0 notes carry too.
		"every release before 0.11.0 is MIT",
		// D311: the console, the mobile app and ee/ under the business
		// licence, the console from 0.113.0 with what came before staying MIT,
		// and counsel's text naming the two documents, of which only the
		// Subscription Terms is still unpublished. The console's own path is
		// left out of this file on purpose: no Go file outside
		// docs_console_boundary_test.go's allow-list names it.
		"the desktop console",
		"`mobile/`",
		"0.113.0",
		"up to and including 0.112.0",
		"not published yet",
	} {
		if !strings.Contains(flat, strings.ToLower(want)) {
			t.Errorf("%s never says %q: the statement exists to name the parts the MIT licence does not cover, and a reader who has to infer them from the directory listing is the reader it is written for", path, want)
		}
	}

	// The links, each one a claim a reader can check: the licence itself, the
	// reserved directory's own README, the vendored code's licence text, and
	// the README's trademark paragraph. The last is a fragment, so it is
	// derived from the heading it has to land on.
	trademark := "](README.md#" + mdHeadingSlug(strings.TrimLeft(docsTrademarkHeading, "# ")) + ")"
	for _, want := range []string{
		"](LICENSE)",
		"](ee/README.md)",
		"](.agents/LICENSE)",
		"](ee/LICENSE)",
		"](mobile/LICENSE.md)",
		"](LICENSES/LicenseRef-Fugaro-Subscription.txt)",
		trademark,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s carries no %q link: Check 130's guard greps this file's links and follows each one by hand, so a claim here that names no file is a claim nobody can check -- and the trademark fragment in particular lands at the top of the README, with no error at all, when it names no heading", path, want)
		}
	}

	// The link to `ee/README.md` is checked against the page it points at: a
	// statement that pointed a reader at a page contradicting it would read as
	// correct on both sides. D532 (2026-10-29) replaced the page's "reserved
	// for net-new organisation features" with what the folder now holds, and
	// the pins here are that statement -- the business-licensed features, new
	// ones and those moved out of the core, with the released MIT versions
	// staying MIT -- plus the unpublished Subscription Terms, so the page
	// cannot drift back to a folder that takes nothing out of the core while
	// this cross-check is on record. Task E3.1 realigned `LICENSING.md` to the
	// same words (the website's licence wording is E3.2's, and still carries
	// "the reserved `ee/` directory" on the community page until it lands).
	// This read is why the test's name is in eeReadingTests.
	eePage := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{
		"business-licensed features",
		"moved out of the core",
		"released mit versions stay mit",
		"not published yet",
	} {
		if !strings.Contains(eePage, want) {
			t.Errorf("ee/README.md, the page %s links for what `ee/` is, never says %q: the statement and the page it points at have to agree on what the folder holds -- the business-licensed features, new ones and those the owner moved out of the core -- and on the Subscription Terms being the document the licence names and does not publish, or one of the two is describing a directory that does not exist", path, want)
		}
	}
}

// TestDocsLicensingNamesTheMovedFeatures (ee-reorg E3.1, D521, D528, D529,
// T11): three parts of the core moved under `ee/`, and a reader who meets one
// of them has to be able to tell which licence it is under. So the statement
// names each by the directory it now sits in and by what a reader meets it as,
// and names what stayed MIT beside them: the reading side.
//
// The halves are pinned together on purpose. A statement that named only the
// moved features would leave a reader of `fugaro diagnose` or of the digest
// unable to tell whether the build they hold may run them, and a statement that
// named only the MIT half would leave `governance:` a block whose enforcement
// has no licence written down at all.
func TestDocsLicensingNamesTheMovedFeatures(t *testing.T) {
	const path = "LICENSING.md"
	flat := strings.ToLower(flattenDoc(readDoc(t, path)))
	for _, want := range []string{
		// The three moved parts, each by the directory it lives in and by the
		// name a reader meets it under: governance's policy and its stage
		// tripwires, the report command, the coordinator.
		"business-licensed",
		"`ee/governance/`",
		"`ee/report/`",
		"`ee/coordinator/`",
		"fugaro report",
		"the coordinator",
		"stage tripwire",
		// What stayed MIT with them, and why the split is visible at all: a
		// build without the tag still reads the halts and checkpoints a tagged
		// build wrote, through the digest and `diagnose` (D521, D528, D529).
		"fugaro runs digest",
		"`internal/digest`",
		"stay mit",
	} {
		if !strings.Contains(flat, strings.ToLower(want)) {
			t.Errorf("%s never says %q: the statement is where a reader finds out which parts of the repository are business-licensed, and the three that moved under `ee/` -- governance's policy and stage tripwires, `fugaro report`, the coordinator -- are the ones a reader meets in a run or a command rather than in a directory listing. Its other half is what stayed MIT beside them: the digest and the reading side, which work in a build without the tag", path, want)
		}
	}
}

// TestDocsLicensingNamesTheFeaturesBornInEE (S0.3, D435, T20): the statement
// says which parts are business-licensed, and two of them were never in the
// core. The central orchestrators and the hosted MCP server are born under
// `ee/`, so a reader who meets `fugaro orchestrator` and looks for the move
// that put it there finds none: the sentence has to say they were born here.
//
// It reads the born half, and TestDocsLicensingNamesTheMovedFeatures above
// reads the moved half. Only together do they say what the folder holds: a
// statement that named the moved parts alone would leave the orchestrators
// looking like core code that nobody had got round to licensing.
func TestDocsLicensingNamesTheFeaturesBornInEE(t *testing.T) {
	const path = "LICENSING.md"
	// The sentence the README and the rule file state too, read here as one
	// phrase: the statement, the folder's page and the folder's rule say the
	// same thing about which features were born there.
	const bornHere = "the central orchestrators and the hosted mcp server are born here"
	flat := strings.ToLower(flattenDoc(readDoc(t, path)))
	if !strings.Contains(flat, bornHere) {
		t.Errorf("%s never says %q: D435/T20 puts the central orchestrators and the hosted MCP server in the business-licensed folder from the start, so a reader who meets `fugaro orchestrator` has to learn here that they are paid rather than conclude they are core code nothing has moved", path, bornHere)
	}
}

// TestDocsLicensingReadmeSection: the README's `## Licensing` section is where
// most readers meet the split, so it states it in two or three sentences, links
// both files a reader can go on to, carries the earlier-releases sentence (R4),
// and ends on the trademark paragraph (D310) that `LICENSING.md` links back to.
func TestDocsLicensingReadmeSection(t *testing.T) {
	const path = "README.md"
	doc := readDoc(t, path)
	raw := docSection(t, path, doc, "## Licensing")
	section := strings.ToLower(flattenDoc(raw))

	// The sentences, read case-insensitively: which words the section says is
	// what this test is about, not how they are capitalised.
	for _, want := range []string{
		"every release before 0.11.0 is MIT",
		"grant no right to the Fugaro name or logo",
	} {
		if !strings.Contains(section, strings.ToLower(want)) {
			t.Errorf("%s's `## Licensing` section never says %q: the section is the reader's way into the licensing files, and a split stated without the file that governs it is a split a reader has to guess at", path, want)
		}
	}
	// The links, read as written: a path is case-sensitive, and both of these
	// are files a reader is meant to open.
	for _, want := range []string{"](LICENSE)", "](LICENSING.md)"} {
		if !strings.Contains(raw, want) {
			t.Errorf("%s's `## Licensing` section carries no %q link: the section states the split, and the files that govern it are where a reader checks the statement against", path, want)
		}
	}

	// The heading the statement's fragment has to land on. It is checked here
	// rather than only as a link target: a fragment that names no heading is
	// silently ignored by GitHub, so a renamed heading breaks Check 130's step 3
	// without breaking anything a reader would notice.
	if !strings.Contains(doc, docsTrademarkHeading) {
		t.Errorf("%s carries no %q heading: LICENSING.md's trademark link points at it, and a fragment naming no heading lands at the top of the page", path, docsTrademarkHeading)
	}
}

// TestDocsLicensingVendoredNotice: `.agents/` is vendored third-party code, so
// its own licence text sits beside it (R1, F2). The MIT terms ask for the
// copyright notice to travel with the copies, and the tree carried none before
// this file: the skills' frontmatter says `license: MIT` and names an author,
// which is not a licence text.
func TestDocsLicensingVendoredNotice(t *testing.T) {
	const path = ".agents/LICENSE"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)

	for _, want := range []string{
		"MIT License",
		"Permission is hereby granted, free of charge",
		"to deal in the Software without restriction",
		`THE SOFTWARE IS PROVIDED "AS IS"`,
		"WITHOUT WARRANTY OF ANY KIND",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q: the file is the vendored project's own licence text, so it is an MIT permission notice and not a summary of one", path, want)
		}
	}

	// The notice names its own copyright holder, and it is not the core
	// LICENSE's: the vendored code is not the owner's to relicense, which is
	// why the statement names `.agents/` as third-party at all. The two lines
	// are compared to each other rather than to a name written here.
	core := licenceCopyrightLine(readDoc(t, "LICENSE"))
	vendored := licenceCopyrightLine(doc)
	if vendored == "" {
		t.Fatalf("%s carries no Copyright line: the notice is what makes the vendored code's holder known, and MIT asks for it in the copies", path)
	}
	if vendored == core {
		t.Errorf("%s carries the core LICENSE's copyright line (%q): `.agents/` is another project's code, so a notice claiming it for the core's holder relicenses work that is not the owner's to relicense (F2)", path, vendored)
	}
}

// licenceCopyrightLine is the first line of a licence file that begins with
// "Copyright", or "" when the file carries none. The two licence files are
// compared through it rather than against a name written into this test: what
// the comparison is about is that the vendored notice names its own holder, not
// whose name that is.
func licenceCopyrightLine(doc string) string {
	for _, line := range strings.Split(doc, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "Copyright") {
			return line
		}
	}
	return ""
}

// licenceRefCopy is where REUSE, the convention the notice's SPDX identifier
// belongs to, expects a custom licence's text: one file per identifier under a
// root `LICENSES/`, named for the identifier. The notice on every file under
// ee/, the console and the mobile app carries `LicenseRef-Fugaro-Subscription`, and a
// reader or a tool that follows that identifier has to land on the terms
// themselves rather than on a directory of prose about them.
const licenceRefCopy = "LICENSES/LicenseRef-Fugaro-Subscription.txt"

// TestDocsLicensingLicenceRefCopy: counsel's text is one wording in three
// files, and this is the fourth copy the SPDX identifier points at. It is
// compared to `ee/LICENSE` byte for byte, both ways -- so the copy cannot
// drift from the licence the notice's identifier names, and a fifth copy
// cannot quietly answer to the same identifier with different terms. Three
// files of identical wording are checked against each other by
// TestDocsLicensingOneWording; this is the one that has to be identical to
// them, not merely similar.
//
// It reads the real `ee/` through readRealEE, so its name is in
// eeReadingTests and the "core without ee/" workflow skips it there.
func TestDocsLicensingLicenceRefCopy(t *testing.T) {
	copyOf := readDoc(t, licenceRefCopy)
	licence := readRealEE(t, "LICENSE")
	if copyOf != licence {
		t.Errorf(`%s is not ee/LICENSE byte for byte (%d bytes against %d).

The file exists so that the identifier every notice under ee/, the console and the mobile app carries resolves to the terms. Two files answering to LicenseRef-Fugaro-Subscription with different wording is the one outcome the identifier cannot express, and a copy that has drifted from ee/LICENSE is the copy a licence scanner hands a reader instead of the licence. Copy ee/LICENSE over it; do not edit either text.`, licenceRefCopy, len(copyOf), len(licence))
	}
	// The copy has to name the identifier, not merely hold the same sentences:
	// the file's name and the notice's are the two halves of one fact, and a
	// reader who opens the copy to find out what `LicenseRef-Fugaro-Subscription`
	// means has to see the identifier in it.
	if !strings.Contains(licence, "LicenseRef-Fugaro-Subscription") {
		t.Errorf("%s never names LicenseRef-Fugaro-Subscription: the file is named for the identifier a reader arrives with, and the two halves have to be visible in one page", licenceRefCopy)
	}
}

// claURL is the contributor licence agreement's published URL, as the owner gave
// it (plan task L0.2's last item, 2026-10-29). It is a raw Gist link pinned to
// one revision, so editing the Gist later does not change what the licence files
// say: a new revision needs a new URL written into them. The URL is spelled once
// in this package -- docs_ee_reserved_test.go derives its scheme-less pin from
// this constant -- so the two pins cannot name different revisions.
const claURL = "https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md"

// TestDocsLicensingOneWording: counsel's text is one wording in `ee/LICENSE`,
// the console's own `LICENSE.md` and the mobile app's (D311), and clause 2.1 --
// the sentence that requires the contributor licence agreement, and so the one
// this work edited -- carries the same bytes in all three. Until this test, only
// `ee/LICENSE` and the `LICENSES/` copy were read by any test, so a URL, a
// revision or a path written differently into the console's or the mobile app's
// file passed every gate; the drift `TestDocsLicensingLicenceRefCopy` says is
// "checked against each other elsewhere in this package" had no such check.
//
// The two Markdown files are also the same file: their differences from
// `ee/LICENSE` are decoration (headings, bullets, bold) and never wording, so an
// edit to one that the other does not carry is reported here.
//
// It reads the real `ee/` through readRealEE, so its name is in eeReadingTests
// and the "core without ee/" workflow skips it there.
func TestDocsLicensingOneWording(t *testing.T) {
	// The console's path is spelled through consoleDirName for the reason
	// docs_ee_reserved_test.go gives: no Go file outside
	// docs_console_boundary_test.go's allow-list names it.
	consoleLicence := consoleDirName + "/LICENSE.md"
	const mobileLicence = "mobile/LICENSE.md"

	fold := func(doc string) string { return strings.ReplaceAll(doc, "\r\n", "\n") }
	console, mobile := fold(readDoc(t, consoleLicence)), fold(readDoc(t, mobileLicence))
	if console != mobile {
		t.Errorf("%s and %s are not the same file: counsel's text is one wording in the three files, and these two carry the same Markdown, so an edit to one belongs in the other too", consoleLicence, mobileLicence)
	}

	// clause21 is the clause-2.1 line: the one sentence of the three files that
	// names the contributor licence agreement, and the line this work edited.
	clause21 := func(path, doc string) string {
		for _, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(line, "2.1 You may modify the Software") {
				return line
			}
		}
		t.Fatalf("%s has no clause 2.1 line: the agreement's URL lives in that sentence, so a rewrite that loses the line takes the licence's own reference to the agreement with it", path)
		return ""
	}
	want := clause21("ee/LICENSE", fold(readRealEE(t, "LICENSE")))
	for _, one := range []struct{ path, doc string }{{consoleLicence, console}, {mobileLicence, mobile}} {
		if got := clause21(one.path, one.doc); got != want {
			t.Errorf("%s's clause 2.1 reads %q, want ee/LICENSE's %q: counsel's text is one wording in the three files, and a difference here is drift a reader of either file cannot see", one.path, got, want)
		}
	}
	if !strings.Contains(want, claURL) {
		t.Errorf("clause 2.1 of the three licence files does not name %s: the bracketed placeholder it replaced is gone, and the agreement the Licensor publishes has to be reachable from the sentence that requires it", claURL)
	}
}

// The licence-key design note (subscriptions plan S2.1; D308 as extended by
// D438 and as ruled on 2026-10-27 by O4, T26 and the key binding, with
// D436/T12 for the lapse).
//
// The note is a design, so it compiles nothing and refuses nothing: every
// verifier task that follows it -- the Go one (S2.3), the console's (S2.4),
// the app's (S2.5), the two entry paths (S2.7, S2.8) and the key the
// coordinator job is handed (S1.9, carried as E1.4) -- is written against what
// this file says. That is the whole reason it is pinned here: a trust boundary
// that three languages implement from one page is the one page whose silent
// edit costs the most, and the plan's S2.1 entry names no test at all, so
// nothing else in this package would read it.
//
// It lives in this file rather than in a `docs_licence_key_test.go` of its own
// because the count of `internal/cli/docs_*_test.go` is pinned in
// `docs/rigor-modes/reconciliation.md`, a dated record four tests hold to its
// figure: a fifth file moves the count and edits another plan's record. This
// file is already the licensing folder's own pin, so the note joins it.
//
// What is pinned is the *content* the plan's S2.1 entry lists, in the plan's
// own order, plus the two cross-file halves a reader arrives by: L3.1 marked
// superseded in the licensing plan and the note's row in the licensing README
// table. Which words the note chooses is not what these tests are about:
// flattenDoc folds the whitespace, and every phrase is read
// case-insensitively. A missing phrase fails with the consequence of the claim
// rather than with the spelling, because the reader who loses it is a verifier
// author in another language, not a reader of the note.
//
// Each phrase is the fact and not the word for it: "refused", "production" and
// "grace" are words a rewritten sentence keeps while it drops the rule they
// were standing for, so the pins are the store's state table, the conditions in
// it, and the sentences that carry the rules ("a staging key is refused", "a
// test key can never license a customer", "the latest valid one"). A phrase is
// also never split by the note's own emphasis: where a rule is bolded, the pin
// is the span inside the markers, because flattenDoc folds whitespace and not
// markup.
//
// Mutations (verified, run and restored): cut "raw bytes before any JSON is
// parsed" out of the note -- TestDocsLicenceKeyDesign fails, naming that
// claim; put back the rotation rule as "until the last key it signed has
// expired" -- it fails, on a window that would refuse a key the state table
// below reads as `grace` (an id the build does not hold is refused before the
// payload is decoded, so an id has to outlive the grace and not only the term);
// delete the rule that no claim is read out of an unverified payload,
// the rule that a refusal names no claim, the rule that the signature is over
// the bytes as they are spelled, the rule that an id the build does not hold is
// refused before either other part is decoded, and the rule that the hand-off
// variable is never written to a file, a log or an error message -- it fails on
// each, which none of those five sentences did before they were pinned; put
// back the "reaches exactly one child" claim in place of the rule that the CLI
// takes the variable out of its own environment -- it fails, on a claim the
// console's allow-list alone does not make true (the CLI's own children inherit
// the environment it holds when they start: internal/gitops/gitops.go,
// internal/claudeplugin/claudeplugin.go, internal/cli/managed_apply.go and
// internal/cli/exec.go all pass os.Environ() to a child);
// put the hand-off back as the unqualified "reaches exactly one child" and cut
// the limit it leaves for an untagged build -- it fails; put the app's row back
// as `expo-secure-store.js`, "the module every credential the app keeps goes
// through" -- it fails, on a module that is the platform adapter and not the
// gate (`secure-store.js` refuses every name `secret.js`'s two-item
// `SECRET_ITEMS` does not carry, and D232 keeps that list at two);
// rewrite the test-mode sentence so it keeps "staging key id",
// "refused" and "production" while flipping the rule (a staging key is
// accepted, a test key can license a customer) -- it fails on the two
// sentences that carry the rule; put back the withdrawn claim that the tier
// maximum bounds a leaked signing key in place of what the bound does bound --
// it fails on that claim's replacement; change the state table's `expired`
// condition to "after it" -- it fails on that row; and delete the note's row
// from the licensing README's file table, leaving the note named in the
// amendment paragraph above it -- TestDocsLicenceKeyNoteIsInTheLicensingReadme
// fails, naming the table, which is the half no S2.1 test but these reaches.

const licenceKeyNotePath = "docs/licensing/licence-key.md"

// licenceKeyDesignGroups is the note's content, one group per subject the
// plan's S2.1 entry names. Each phrase is a fact a verifier implements, not a
// wording: the "why" is what a reader loses when the phrase is gone.
var licenceKeyDesignGroups = []struct {
	subject string
	why     string
	phrases []string
}{
	{
		subject: "the key",
		why:     "the format is what three verifiers agree on byte for byte, the claims are the licence's own terms, and the boundary rules -- the key id is what decides before anything else is decoded, the signature is over the bytes as they are spelled, a refusal names no attacker-chosen claim, and a rotated build keeps an old id until the keys it signed have passed their grace, or it refuses a key the state table reads as grace -- are where the scheme's safety and its consistency rest",
		phrases: []string{
			"ed25519",
			"base64url",
			"<key id>.<payload>.<signature>",
			"before any json is parsed",
			"signed as it is spelled",
			"fails the signature",
			"key id",
			"refused before either other part is decoded",
			"rotate",
			"passed its grace",
			"and not bare `expires`",
			"| `tier` | `community`, `team` or `enterprise` |",
			"| `features` |",
			"| `seats` |",
			"| `organisation` |",
			"| `issued` |",
			"| `expires` |",
			"not to a person or a machine",
			"no claim is read out of a payload whose signature did not verify",
			"names the key and the part that failed, never a claim",
		},
	},
	{
		subject: "expiry only (O4)",
		why:     "the covered-versions date D438 added named a version window that only the perpetual fallback gave a meaning to, and D436 dropped the fallback: a key that states a version window is a key the product has no rule for",
		phrases: []string{
			"expiry only",
			"no covered-versions date",
			"no release-date stamping",
		},
	},
	{
		subject: "the check",
		why:     "the check gates at the registration point and nowhere else, so the MIT core keeps building and running with no key at all",
		phrases: []string{
			"ee/register",
			"nothing in the core",
			"offline",
			"no phone-home",
			"| `valid` | `now <= expires` |",
			"| `grace` | `expires < now <= expires + 30 days` |",
			"| `expired` | past the grace |",
			"refused rather than given a state",
		},
	},
	{
		subject: "the lapse",
		why:     "read-only is what a lapse costs, and cancel and the kill switch are what it must never cost (T12, D436)",
		phrases: []string{
			"30-day grace",
			"read-only",
			"keeps reading, cancel and the kill switch",
			"loses launching and configuration changes",
			"never stopped by licence state",
		},
	},
	{
		subject: "renewal and overlap",
		why:     "the overlap is why an issuing outage of up to 60 days is survivable, and the latest valid key is the one a verifier acts on (T26)",
		phrases: []string{
			"30 days before expiry",
			"two keys are simply two keys the store holds",
			"the latest valid one",
			"past 60 days",
		},
	},
	{
		subject: "limits",
		why:     "these are the accepted limits, and the tier maximum is stated with what it bounds and what it does not, so a verifier author does not build the length check as a defence against a leaked signing key that it is not",
		phrases: []string{
			"maximum lifetime",
			"not the same as bounding a leaked signing key",
			"`issued` is the minter's to choose",
			"a tier's maximum is its term plus the 30-day issue lead",
			"one year and 30 days for every tier today",
			"without a change to the key format",
			"never in the verifier alone",
			"no revocation",
			"a refunded buyer keeps a working key until it expires",
			"a clock set back extends `expires`",
			"the hand-off reaches one child only where the build has the check",
			"carries no edition today",
		},
	},
	{
		subject: "test-mode keys",
		why:     "a staging key has to be unusable against a production installation, or the environment that exists to be broken is the environment that licenses customers",
		phrases: []string{
			"staging key id",
			"keyring holds production ids only",
			"a staging key is refused",
			"a test key can never license a customer",
		},
	},
	{
		subject: "storage and hand-off",
		why:     "a key that reaches a child in argv is a key every local process reads (F41), each product stores it where its own platform keeps a credential -- and for the app that is not the two-item secret store D111/D232 keep closed -- and the hand-off is one child where the build has the check: the console's allow-list is not enough on its own, because the CLI's own children -- git, the plugin install, Terraform and the runner in exec -- inherit the environment it holds when they start",
		phrases: []string{
			"licence.key",
			"never in argv",
			"fugaro_licence_key",
			"the one route to a spawned child is its **environment**",
			"the variable reaches one child and stops there where the build that receives it has the check",
			"reads `fugaro_licence_key` once",
			"takes it out of its own environment in the same step",
			"testtheenvironmentkeyisremovedbeforeanychildstarts",
			"a build with no registration point has no such step",
			"not the app's two-item secret store",
			"secure-store.js",
			"secret_items",
			"d232",
			"secret manager",
			"mounted into the job and never typed by a person",
			"never written to a file, a log or an error message",
		},
	},
	{
		subject: "states and minting",
		why:     "the no-key state is a real installation rather than an error (F43), minting is the owner's and off every machine an agent reaches (R8, T13), and the coordinator's job is handed its key rather than typing one",
		phrases: []string{
			"the store being empty rather than a fourth state",
			"minted by hand",
			"owner-run",
			"private signing key",
			"free keys expire too",
		},
	},
}

// TestDocsLicenceKeyDesign: `docs/licensing/licence-key.md` is the S2.1 note,
// and it states every subject the plan's S2.1 entry names.
func TestDocsLicenceKeyDesign(t *testing.T) {
	flat := strings.ToLower(flattenDoc(readDoc(t, licenceKeyNotePath)))
	for _, group := range licenceKeyDesignGroups {
		for _, want := range group.phrases {
			if !strings.Contains(flat, strings.ToLower(want)) {
				t.Errorf("%s never says %q (%s): %s", licenceKeyNotePath, want, group.subject, group.why)
			}
		}
	}
}

// TestDocsLicenceKeyNoteSupersedesL31: the note is licensing task L3.1's
// successor rather than a second design beside it, and the licensing plan has
// to say so where a reader of L3.1 arrives. Two notes on one key, neither
// marked, is how a verifier gets written against the withdrawn text -- and the
// withdrawal is O4's, which dropped the covered-versions date and the
// release-date stamping D438 had added to L3.1's three design rules. L3.1's own
// text carries neither, so the supersession is stated as a ruling rather than
// as a difference between the two notes.
//
// The supersession line has to name the plan that carries the note and the
// note itself: a bare "superseded" leaves the reader of L3.1 with no later
// text to read, which is the state the subscriptions plan's own status census
// recorded against this task.
func TestDocsLicenceKeyNoteSupersedesL31(t *testing.T) {
	const plan = "docs/licensing/implementation-plan.md"
	doc := readDoc(t, plan)
	section := docSection(t, plan, doc, "#### L3.1 — The licence-key design note")
	flat := strings.ToLower(flattenDoc(section))

	for _, want := range []string{
		"superseded",
		"docs/subscriptions/implementation-plan.md",
		"licence-key.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s's L3.1 section never says %q: the note it used to commission is written now by the subscriptions plan's S2.1, whose note carries O4's ruling (the covered-versions date and the release-date stamping D438 added to L3.1's rules are dropped), so a reader who lands on L3.1 first has to be told where the later text is and that it exists", plan, want)
		}
	}
}

// TestDocsLicenceKeyNoteIsInTheLicensingReadme: the licensing README's table is
// the folder's map of what each file is for, so the note takes a row in it,
// and the row says what the file is: a design that is not built. The row is the
// only route to the note from the folder's front page, and a design the front
// page does not name is a design the next reader of the folder does not know
// exists (that table is where spec.md, reconciliation.md, the implementation
// plan and the Terms are reached from).
//
// The row is read out of the table's own span rather than out of the whole
// file: a mention of the note in the amendment paragraph above is not a row,
// and the half this test exists for is a reader scanning the table.
func TestDocsLicenceKeyNoteIsInTheLicensingReadme(t *testing.T) {
	const readme = "docs/licensing/README.md"
	doc := readDoc(t, readme)
	table := strings.ToLower(flattenDoc(docSection(t, readme, doc, "| File | Role |")))
	for _, want := range []string{"](licence-key.md)", "the licence key's design", "not built"} {
		if !strings.Contains(table, want) {
			t.Errorf("%s's file table carries no %q: the table is the folder's map, and the S2.1 note is a document a verifier author has to be able to find -- and to see is a design rather than a built feature -- from the folder's front page", readme, want)
		}
	}
}

// TestDocsSubscriptionsPlanBoundsWhatTheTierMaximumBounds: the subscriptions
// plan's constraint table is what an S2.3 or S3.2 reviewer reads as the
// mitigation the verifier owes, and the S2.1 row of it is pinned by this note.
// The row used to say a leaked signing key cannot mint keys old releases accept
// for ever, which the ruled check does not deliver -- the length check is over
// `expires - issued`, and `issued` is the minter's to choose -- so the two
// sentences the note holds for it are held here too: the constraint the check
// does satisfy, and the horizon as an accepted limit.
//
// The hand-off row is read here beside them because it is the other constraint
// this note is the pin for, and because the S2.3 test it names is what makes the
// console's allow-list entry sufficient: the CLI's own children inherit the
// environment it holds when they start, and none may inherit the key.
//
// The withdrawn claim is asserted absent rather than left unpinned: the plan is
// where a verifier author looks for what to implement, and a sentence that reads
// as a security property the design does not have is worse than no sentence.
func TestDocsSubscriptionsPlanBoundsWhatTheTierMaximumBounds(t *testing.T) {
	const plan = "docs/subscriptions/implementation-plan.md"
	flat := strings.ToLower(flattenDoc(docSection(t, plan, readDoc(t, plan), "## Global constraints and review focus")))
	for _, want := range []string{
		"no key is given a term longer than its tier's maximum",
		"a leaked signing key's horizon is an accepted limit",
		"the key reaches the cli it is handed to and no further",
		"the licence key does not join the app's two-item secret store",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s's constraint table never says %q: its S2.1 row is the note's own claim, and a constraint row that asks the verifier for more than the ruled check does is how TestKeyBeyondTheTierLifetimeIsRefused gets written against a property it cannot hold", plan, want)
		}
	}
	if strings.Contains(flat, "cannot mint keys old releases accept for ever") {
		t.Errorf("%s's constraint table still says a leaked signing key cannot mint keys old releases accept for ever: the length check bounds `expires - issued`, and `issued` is the minter's to choose, so the note it pins says the opposite", plan)
	}
}
