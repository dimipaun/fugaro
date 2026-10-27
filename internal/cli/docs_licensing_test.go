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
		// and counsel's text with its two unpublished documents. The console's own path is
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
		trademark,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s carries no %q link: Check 130's guard greps this file's links and follows each one by hand, so a claim here that names no file is a claim nobody can check -- and the trademark fragment in particular lands at the top of the README, with no error at all, when it names no heading", path, want)
		}
	}

	// The link to `ee/README.md` is checked against the page it points at: what
	// "reserved" means is that page's to say, and a statement that pointed a
	// reader at a page contradicting it would read as correct on both sides.
	// This read is why the test's name is in eeReadingTests.
	reserved := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{"reserved", "not published yet"} {
		if !strings.Contains(reserved, want) {
			t.Errorf("ee/README.md, the page %s links for what `ee/` is, never says %q: the statement and the page it points at have to agree on what the folder is for and on the licence naming documents that are not published yet, or one of the two is describing a directory that does not exist", path, want)
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
