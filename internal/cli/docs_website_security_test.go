package cli

// The security page's source (website plan, Task W0.5).
//
// The site's security page is SECURITY.md rendered, and nothing else. The
// reconciliation refuses a paraphrase of a security claim (finding 41, D99)
// because the document's value is that its limits are stated exactly: it
// places "an agent doing harmful things inside its own container with the
// credentials that job's service account is allowed to hold" out of scope,
// and it says a trusted author's own prompt injection is "still code
// execution under --dangerously-skip-permissions". A page that softens
// either sentence is a false claim about the product, and a summary is
// exactly where the softening happens.
//
// So website/content/security.md is generated: a header naming its source
// and the one command that rewrites it, then SECURITY.md's own bytes, title,
// headings, links and all. The three tests below hold it there, and each one
// reads SECURITY.md rather than the generator's output -- a test written
// against the generator agrees with whatever the generator does, which is
// the one thing a drift test must not do (plan Review Focus).
//
// W0.5's file list names the page and this test and no generator program, so
// the renderer and its update flag live here: `go test ./internal/cli -run
// TestWebsiteSecurityPageIsByteIdenticalToSecurityMd -update-website-security`
// is the whole regeneration step, and it is hermetic (no network, no clock,
// no credential).

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	// websiteSecurityPath is the generated page, and websiteSecuritySource is
	// the document it is generated from. Both are repo-relative; readDoc
	// resolves them, and the page is written at the same path.
	websiteSecurityPath   = "website/content/security.md"
	websiteSecuritySource = "SECURITY.md"
)

// websiteSecurityHeader is the page's first line. It names the source
// (Global Constraint 6: every generated file names its source in a header
// comment) and the command that rewrites the file, so a reader who finds the
// page stale has the fix in front of them. It is a Markdown HTML comment, so
// the rendered page keeps it out of the reader's way.
const websiteSecurityHeader = "<!-- generated from SECURITY.md; do not edit. " +
	"Rewrite it with: go test ./internal/cli -run TestWebsiteSecurityPageIsByteIdenticalToSecurityMd -update-website-security -->"

// updateWebsiteSecurity rewrites website/content/security.md from
// SECURITY.md. The flag name is this file's own, as each of the package's
// other goldens has its own: a second flag.Bool of the same name in one
// package panics at init.
var updateWebsiteSecurity = flag.Bool("update-website-security", false, "rewrite website/content/security.md from SECURITY.md")

// renderWebsiteSecurityPage is the whole generator: the header, a blank
// line, and the source document byte for byte. The page keeps the document's
// own `# Security policy` title, so nothing here decides what the title is.
func renderWebsiteSecurityPage(source []byte) []byte {
	return append([]byte(websiteSecurityHeader+"\n\n"), source...)
}

// writeWebsiteSecurityPage is the generator's one write, under the update
// flag only.
func writeWebsiteSecurityPage(t *testing.T, source string) {
	t.Helper()
	path := filepath.Join("..", "..", websiteSecurityPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, renderWebsiteSecurityPage([]byte(source)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// websiteSecurityBody returns the page below its generated header, and fails
// when the header is not there: the header is what tells a reader the file is
// generated, so a page without it is the hand-kept copy this task exists to
// prevent.
func websiteSecurityBody(t *testing.T, page string) string {
	t.Helper()
	body, ok := strings.CutPrefix(page, websiteSecurityHeader+"\n\n")
	if !ok {
		t.Fatalf("%s does not start with its generated-file header, so it is not the generated page:\n%s",
			websiteSecurityPath, websiteSecurityHeader)
	}
	return body
}

// TestWebsiteSecurityPageIsByteIdenticalToSecurityMd is the load-bearing one
// (D99): below the generated header the page IS SECURITY.md, byte for byte.
// The title is the document's own `#` heading rather than a page title
// written here, the headings are the document's headings (F40: the anchors
// its own cross-references name have to survive the copy), and every
// sentence of the trust model and its limits is the sentence the document
// states. A byte comparison, not a substring check: "the page contains the
// paragraph" would pass a page that also rewrote it.
//
// Run with -update-website-security it regenerates the file, which is the
// only thing that writes it.
func TestWebsiteSecurityPageIsByteIdenticalToSecurityMd(t *testing.T) {
	if *updateWebsiteSecurity {
		writeWebsiteSecurityPage(t, readDoc(t, websiteSecuritySource))
	}
	page := readDoc(t, websiteSecurityPath)
	source := readDoc(t, websiteSecuritySource)
	body := websiteSecurityBody(t, page)
	if body != source {
		t.Errorf("%s is not %s byte for byte below its header: the page is %d bytes, the document %d; rewrite it with:\n  go test ./internal/cli -run TestWebsiteSecurityPageIsByteIdenticalToSecurityMd -update-website-security\n%s",
			websiteSecurityPath, websiteSecuritySource, len(body), len(source),
			firstLineDifference(body, source))
	}
}

// TestWebsiteSecurityPageKeepsEveryHeading: the page's headings are
// SECURITY.md's headings, the same lines in the same order. F40 is why they
// are compared as the document's own sequence rather than as a set: the
// document sends its own readers from one limit to another by name (see "the
// assumption" above, see "Known limits" below), and a heading reworded on the
// way to the site breaks a reference written in the repository.
//
// The second half is the anchors those references name: every name a
// cross-reference points at has to be an anchor of the page, because the page
// is what a reader of the site lands on. The extraction is the one
// TestSecurityMdCrossReferencesPointTheRightWay uses -- a `##`-`####` heading
// line, or a bullet whose bold run begins with the name -- so the two tests
// cannot disagree about what an anchor is.
func TestWebsiteSecurityPageKeepsEveryHeading(t *testing.T) {
	source := readDoc(t, websiteSecuritySource)
	body := websiteSecurityBody(t, readDoc(t, websiteSecurityPath))

	headingRE := regexp.MustCompile(`(?m)^#{1,6} .+$`)
	fromSource, fromPage := headingRE.FindAllString(source, -1), headingRE.FindAllString(body, -1)
	if len(fromSource) < 8 {
		t.Fatalf("read %d headings out of %s: the document has moved, so this check saw nothing",
			len(fromSource), websiteSecuritySource)
	}
	if !slices.Equal(fromPage, fromSource) {
		t.Errorf("%s's headings are not %s's, in order:\n  page:   %q\n  source: %q",
			websiteSecurityPath, websiteSecuritySource, fromPage, fromSource)
	}

	anchors := websiteSecurityAnchors(body)
	refs := regexp.MustCompile(`see "([^"]+)" (above|below)`).FindAllStringSubmatch(source, -1)
	if len(refs) < 5 {
		t.Fatalf("read %d cross-references out of %s: the document has moved, so this check saw nothing",
			len(refs), websiteSecuritySource)
	}
	for _, ref := range refs {
		name := ref[1]
		if !slices.ContainsFunc(anchors, func(a string) bool { return a == name || strings.HasPrefix(a, name) }) {
			t.Errorf("%s sends a reader to %q and %s has no such anchor: the page is what the reference lands on, so a heading or a bold lead that did not survive the copy is a dead end",
				websiteSecuritySource, name, websiteSecurityPath)
		}
	}
}

// TestWebsiteSecurityPageAddsNoSentenceOfItsOwn states D99's rule the other
// way round from the byte test, and it is the half that notices the one edit
// a page is always tempted by: a lead sentence saying what the security model
// "really" means. Every paragraph on the page has to be a paragraph
// SECURITY.md carries -- which the generated file satisfies trivially, and a
// page someone edited by hand almost never does.
func TestWebsiteSecurityPageAddsNoSentenceOfItsOwn(t *testing.T) {
	source := readDoc(t, websiteSecuritySource)
	body := websiteSecurityBody(t, readDoc(t, websiteSecurityPath))
	paras := 0
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		paras++
		if !strings.Contains(source, para) {
			t.Errorf("%s carries a paragraph %s does not carry, and a security sentence written for the site is a claim the document does not make:\n%s",
				websiteSecurityPath, websiteSecuritySource, clipSecurityLine(para))
		}
	}
	if paras < 8 {
		t.Fatalf("read %d paragraphs out of %s: the page has moved, so this check saw nothing",
			paras, websiteSecurityPath)
	}
}

// websiteSecurityAnchors lists the names a reader can be sent to in a
// Markdown document: every `##`-`####` heading and every bullet whose bold
// run begins with the name, with the bullet's trailing period trimmed. It is
// TestSecurityMdCrossReferencesPointTheRightWay's extraction over
// SECURITY.md, applied to the page instead.
func websiteSecurityAnchors(doc string) []string {
	headingRE := regexp.MustCompile(`^#{2,4} (.+?)\s*$`)
	bulletRE := regexp.MustCompile(`^- \*\*(.+?)\*\*`)
	var anchors []string
	for _, line := range strings.Split(doc, "\n") {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			anchors = append(anchors, m[1])
		}
		if m := bulletRE.FindStringSubmatch(line); m != nil {
			anchors = append(anchors, strings.TrimSuffix(m[1], "."))
		}
	}
	return anchors
}

// firstLineDifference names the first line two documents disagree on, so the
// byte comparison above reports the edit rather than two byte counts.
func firstLineDifference(got, want string) string {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			return fmt.Sprintf("first difference, line %d:\n  page:   %s\n  source: %s",
				i+1, clipSecurityLine(g), clipSecurityLine(w))
		}
	}
	return "the two documents differ in length only"
}

// clipSecurityLine shortens a line for a failure message: SECURITY.md's
// paragraphs are long, and the message that names the difference should not
// be buried under one of them.
func clipSecurityLine(s string) string {
	const limit = 240
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
