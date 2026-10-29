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
		// reserved directory's exclusion, the vendored code's own terms.
		"`ee/`",
		"excluded",
		"reserved",
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
	// this cross-check is on record. `LICENSING.md` and the website's licence
	// wording still carry the older "reserved for net-new organisation
	// features"; task E3.1 and E3.2 realign them, and this cross-check can pin
	// their agreement again once they have. This read is why the test's name is
	// in eeReadingTests.
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
