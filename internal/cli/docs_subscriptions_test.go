package cli

// S0.1 pins the first global constraint of the subscriptions work: no price,
// currency amount, discount offer, fee rate or Enterprise minimum enters this
// repository (D430, T22). The repository is public and the business figures are
// not -- they live in the owner's private business repository, and the Team
// price is inserted at the owner's publication (S7.4). The files a figure would
// land in are the subscription documents, which carry every other commercial
// term with "price: private" where an answer held one, and the website's pages,
// where S7.2 adds the pricing page.
//
// The scan is mechanical rather than a reading of the text: every price,
// discount, fee rate and minimum is written as a currency amount (a sign or a
// code beside a digit) or as a percentage, and a gate that tried to understand
// the sentence would be the gate a reworded figure walks past. It flags a
// currency sign ($, EUR sign, pound, yen) or an ISO 4217 code beside a digit,
// the English currency words beside a digit, and a digit before "%" or the word
// "percent".
//
// What it does not cover, so that no reader takes it for more than it is:
//
//   - commit messages and pull-request bodies, which the plan's Interfaces line
//     hands to each PR's independent review;
//   - a page's stylesheet and its client script: <style> and <script> blocks are
//     dropped before the scan, because a layout width and a CSS value a script
//     carries are not figures. A page's frontmatter is read like its copy -- that
//     is where a page's data lives, and where S7.2's pricing page writes the Team
//     price -- so a percentage a page needs for layout goes in its stylesheet,
//     which the scan does not read;
//   - the components a page composes, which are outside the walk;
//   - the renderings the exact list below names: the values the subscription
//     documents already carry, and one page's own code.

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// priceFigureAllow is the exact list of renderings already on `main` that the
// scan must not fail on, each one judged and named rather than pattern-blessed.
//
// Six are the figures the subscription documents carry, which is what S0.1
// allows: the Terms' own values are contract terms a buyer must see (T24 sends
// the prices, discounts, fees and the Enterprise minimum to the private Order
// Form and keeps the rest), and the licensing census restates two of them while
// saying that they went in. The seventh is not a figure at all: the security
// page's own code, whose JavaScript replacement argument reads like an amount,
// and which the scan only sees because a page's frontmatter is read.
//
// An entry is a rendering and one file, never a licence to write that figure
// anywhere: the scan drops a hit only inside its own occurrence, only in the
// file the entry names, and the test below fails if the rendering is missing
// from that file or appears there more than once -- so a Terms figure that moved,
// doubled or was replaced fails here rather than riding along under the entry
// that was written for the old one.
var priceFigureAllow = []struct {
	path string // the one file that may carry it
	text string // the exact rendering, as that file writes it
	what string // what the figure is, so a reader judges the exception rather than trusting it
}{
	{
		"docs/licensing/subscription-terms.md",
		"owning more than 50% of the voting interests",
		"the Affiliate control threshold (Terms section 2)",
	},
	{
		"docs/licensing/subscription-terms.md",
		"interest at 12% per year",
		"the late-payment interest rate (Terms section 12.2), which counsel sets (B17)",
	},
	{
		"docs/licensing/subscription-terms.md",
		"US$100",
		"the Community, Free Plan and Beta liability cap (Terms section 10.5)",
	},
	{
		"docs/licensing/subscription-terms.md",
		"underpayment of more than 5%",
		"the audit threshold (Terms section 20)",
	},
	{
		"docs/licensing/status-2026-11-07.md",
		"(12% per year)",
		"the licensing census restating the Terms' interest rate: S0.1's one file is this test, so it records the figure instead of editing another plan's census",
	},
	{
		"docs/licensing/status-2026-11-07.md",
		"(US$100)",
		"the same census restating the Terms' liability cap (L4.3)",
	},
	{
		"website/src/pages/security.astro",
		"'$1'",
		"not a figure: the page's own Markdown-link stripper, whose replacement argument is JavaScript's first capture group (documentText.replace(/\\[([^\\]]*)\\]\\([^)]*\\)/g, '$1'))",
	},
}

// priceFigureDocGlobs are the documents a figure would be written into: the
// subscriptions and licensing folders, the two the plan names.
var priceFigureDocGlobs = []string{
	"docs/subscriptions/*.md",
	"docs/licensing/*.md",
}

// priceFigurePagesDir is the website's pages directory, walked recursively so
// that the legal/ pages are read too. The plan's Interfaces line names "the
// website's pages", which is where S7.2 adds the pricing page; the components
// and the data files a page renders from are outside it.
const priceFigurePagesDir = "website/src/pages"

// priceFigurePageExts are the page files the walk reads: the site writes its
// pages in .astro, and a page a later task writes as Markdown is a page too.
var priceFigurePageExts = map[string]bool{".astro": true, ".md": true}

// priceCurrencyCodes are the ISO 4217 codes a price is written in. A price in
// an unlisted currency still carries a sign or a word, so the list is a lower
// bound on the scan rather than a boundary of it.
const priceCurrencyCodes = `USD|EUR|GBP|CAD|AUD|JPY|CHF|CNY|INR|NZD|SEK|NOK|DKK|SGD|HKD|MXN|BRL|ZAR`

// priceCurrencyRE matches a currency amount, in any order: a sign or a code
// beside a digit, and the English word a writer uses instead of either. A sign
// counts only beside a digit, so "$XDG_CONFIG_HOME" and a shell's "$(" are not
// amounts; a code counts only as a whole word beside the amount, so "European"
// is not EUR.
var priceCurrencyRE = regexp.MustCompile(
	`[$€£¥]\s?` + priceAmount +
		`|` + priceAmount + `\s?[$€£¥]` +
		`|(?:^|[^A-Za-z])(?:` + priceCurrencyCodes + `)\s?` + priceAmount +
		`|` + priceAmount + `\s?(?:` + priceCurrencyCodes + `)(?:[^A-Za-z]|$)` +
		`|` + priceAmount + `\s?\b(?:dollars?|euros?|pounds?|cents?)\b`)

// pricePercentRE matches a percentage: a discount, a fee rate or a share
// written as a digit before a sign or the word.
var pricePercentRE = regexp.MustCompile(priceAmount + `\s?%|` + priceAmount + `\s?(?:percent|per cent)\b`)

// priceAmount is a figure's number: a digit, thousands separators and an
// optional fraction, and never the full stop that ends the sentence it sits in
// -- so a hit quotes the amount and the allow list can name one without the
// punctuation around it.
const priceAmount = `[0-9][0-9,]*(?:\.[0-9]+)?`

// priceFigureKinds are the shapes refused, each with the word a hit is reported
// under, so a reader of the failure learns which rule caught the figure.
var priceFigureKinds = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a currency amount", priceCurrencyRE},
	{"a percentage", pricePercentRE},
}

var (
	priceAstroStyleRE  = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	priceAstroScriptRE = regexp.MustCompile(`(?is)<script\b.*?</script>`)
)

// TestDocsSubscriptionsCarryNoPriceFigures is the gate: no figure in the
// subscription documents or on a website page, outside the exact list above.
//
// It also holds the allow list itself to the tree. An entry whose rendering
// vanished, or whose file the scan no longer reads, is a stale exception that
// would silently widen the list; an entry whose rendering now appears twice is a
// figure the list is allowing at a second site nobody decided on.
func TestDocsSubscriptionsCarryNoPriceFigures(t *testing.T) {
	scan := priceFigureScanTree(t, testutil.ModuleRoot())

	// The scan reads what it found, so an empty or narrowed walk would pass
	// every check below by looking at nothing.
	for _, want := range []string{
		"docs/subscriptions/implementation-plan.md",
		"docs/subscriptions/reconciliation.md",
		"docs/subscriptions/spec.md",
		"docs/licensing/subscription-terms.md",
		"docs/licensing/reconciliation.md",
		"website/src/pages/index.astro",
		"website/src/pages/legal/subscription-terms.astro",
	} {
		if !slices.Contains(scan.files, want) {
			t.Fatalf("the scan read %d files and no %s: the walk is looking at the wrong tree, and every check here is judged against what it found (%v)", len(scan.files), want, scan.files)
		}
	}

	for _, allow := range priceFigureAllow {
		text, ok := scan.text[allow.path]
		if !ok {
			t.Errorf("the allow list holds %q for %s, and the scan does not read that file: either the file moved out of the walk or the entry is stale, and an entry nobody reads allows the rendering nowhere", allow.text, allow.path)
			continue
		}
		switch n := strings.Count(text, allow.text); {
		case n == 0:
			t.Errorf("%s no longer carries %q (%s), and the allow list still allows it: drop the entry, or the list stops being the exact one S0.1 describes", allow.path, allow.text, allow.what)
		case n > 1:
			t.Errorf("%s carries %q %d times (%s), and the allow list allows it once: a figure the list was written for has been copied to a second site, which is a decision nobody took", allow.path, allow.text, n, allow.what)
		}
	}

	for _, gap := range scan.gaps {
		t.Error(gap)
	}
}

// TestDocsSubscriptionsPriceScanCatchesPlants is the scan's own test: the tree
// is clean, and a clean tree looks exactly like a regex that matches nothing,
// a walk that reads no page and a scan that drops every hit. Each plant below
// is a real file at a path the walk reads with one figure in it, and the scan
// has to refuse it; the one accepted plant is the control that it is not simply
// refusing everything.
func TestDocsSubscriptionsPriceScanCatchesPlants(t *testing.T) {
	// scan writes the planted files under a temporary root at their real
	// relative paths and returns the scan's verdict, so a plant is judged the
	// way the tree is.
	scan := func(t *testing.T, files map[string]string) []string {
		t.Helper()
		root := t.TempDir()
		for _, rel := range slices.Sorted(maps.Keys(files)) {
			path := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(files[rel]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return priceFigureScanTree(t, root).gaps
	}
	refused := func(t *testing.T, what string, gaps []string, want string) {
		t.Helper()
		if len(gaps) == 0 {
			t.Fatalf("%s reports no figure: the scan would let it into a public repository", what)
		}
		if !strings.Contains(strings.Join(gaps, "\n"), want) {
			t.Errorf("%s reports %v, and no hit names %q", what, gaps, want)
		}
	}
	accepted := func(t *testing.T, what string, gaps []string) {
		t.Helper()
		if len(gaps) > 0 {
			t.Errorf("%s reports %v, and it carries no price figure: a scan that refuses this refuses the site's own pages", what, gaps)
		}
	}

	t.Run("a page's copy is read", func(t *testing.T) {
		body := "<p>Team is $20 per seat, billed monthly.</p>\n"
		refused(t, "a price on the pricing page", scan(t, map[string]string{"website/src/pages/pricing.astro": body}), "$20")
	})

	t.Run("a currency code beside a digit", func(t *testing.T) {
		body := "<p>Enterprise starts at USD 20000 a year.</p>\n"
		refused(t, "an Enterprise minimum in a code", scan(t, map[string]string{"website/src/pages/pricing.astro": body}), "USD 20000")
	})

	t.Run("a currency word beside a digit", func(t *testing.T) {
		body := "<p>The annual plan costs 900 dollars.</p>\n"
		refused(t, "a price written as a word", scan(t, map[string]string{"website/src/pages/pricing.astro": body}), "900 dollars")
	})

	t.Run("a percentage in a document", func(t *testing.T) {
		body := "# Discounts\n\nStartups get 20% off for the first year.\n"
		refused(t, "a discount in a subscription document", scan(t, map[string]string{"docs/subscriptions/spec.md": body}), "20%")
	})

	t.Run("the word percent", func(t *testing.T) {
		body := "# Fees\n\nThe merchant takes 5 percent of each sale.\n"
		refused(t, "a fee rate in the licensing folder", scan(t, map[string]string{"docs/licensing/reconciliation.md": body}), "5 percent")
	})

	t.Run("an allowed rendering in a file the list does not name", func(t *testing.T) {
		body := "# Subscriptions\n\nThe liability cap is US$100.\n"
		refused(t, "an allowed rendering outside the file it is allowed in", scan(t, map[string]string{"docs/subscriptions/README.md": body}), "US$100")
	})

	t.Run("a new figure beside an allowed one", func(t *testing.T) {
		body := "The cap is US$100, and the audit costs US$250.\n"
		refused(t, "a second amount in the file the first is allowed in", scan(t, map[string]string{"docs/licensing/subscription-terms.md": body}), "US$250")
	})

	t.Run("a figure in a page's data", func(t *testing.T) {
		body := "---\nconst TEAM_PRICE = '$20'\n---\n<p>Team is billed monthly.</p>\n"
		refused(t, "a price in a page's frontmatter", scan(t, map[string]string{"website/src/pages/pricing.astro": body}), "$20")
	})

	t.Run("a discount in a page's data", func(t *testing.T) {
		body := "---\nconst STARTUP_DISCOUNT = '20%'\n---\n<p>Startups pay less in their first year.</p>\n"
		refused(t, "a discount in a page's frontmatter", scan(t, map[string]string{"website/src/pages/pricing.astro": body}), "20%")
	})

	t.Run("a page that carries no figure is accepted", func(t *testing.T) {
		body := "---\nconst WIDTH = 'wide'\nconst LINKED = text.replace(/x/, 'one')\n---\n" +
			"<style>p { width: 100% }</style>\n<script>const rate = '100%'</script>\n" +
			"<p>Two plans, one price: the pricing page is published by the owner.</p>\n"
		accepted(t, "a page whose copy carries no figure, and whose stylesheet and script are not read", scan(t, map[string]string{"website/src/pages/pricing.astro": body}))
	})
}

// priceFigureScan is one run of the scan: the files it read, the text it read
// from each (a page's rendered text, not its code) and the figures it found
// outside the allowed list.
type priceFigureScan struct {
	files []string
	text  map[string]string
	gaps  []string
}

// priceFigureScanTree runs the scan over the tree at root. One reader, so the
// plants are judged the way the tree is.
func priceFigureScanTree(t *testing.T, root string) priceFigureScan {
	t.Helper()
	files, err := priceFigureFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	scan := priceFigureScan{files: files, text: make(map[string]string, len(files))}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		text := priceFigureProse(rel, string(data))
		scan.text[rel] = text
		scan.gaps = append(scan.gaps, priceFigureHits(rel, text)...)
	}
	return scan
}

// priceFigureFiles is every file the scan reads, relative to root,
// slash-separated and sorted: the two document globs and the recursive walk over
// the website's pages.
func priceFigureFiles(root string) ([]string, error) {
	var found []string
	for _, glob := range priceFigureDocGlobs {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			return nil, err
		}
		found = append(found, matches...)
	}
	pages := filepath.Join(root, priceFigurePagesDir)
	err := filepath.WalkDir(pages, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !priceFigurePageExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	files := make([]string, 0, len(found))
	for _, path := range found {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		files = append(files, filepath.ToSlash(rel))
	}
	slices.Sort(files)
	return files, nil
}

// priceFigureProse is the text the scan reads from one file. A Markdown
// document is read whole; a page is read whole but for its <style> and
// <script> blocks, which are replaced by blank lines so the line numbers a hit
// reports stay the file's own. A page's frontmatter is read, because that is
// where its data lives.
func priceFigureProse(rel, body string) string {
	if !strings.HasSuffix(rel, ".astro") {
		return body
	}
	body = priceAstroStyleRE.ReplaceAllStringFunc(body, priceBlankLines)
	return priceAstroScriptRE.ReplaceAllStringFunc(body, priceBlankLines)
}

// priceBlankLines stands in for a block the scan does not read: the block's own
// newlines, so every line after it keeps its number.
func priceBlankLines(block string) string {
	return strings.Repeat("\n", strings.Count(block, "\n"))
}

// priceFigureHits reports every figure in one file's text that the allow list
// does not cover, as a message naming the file, the line and the rule.
func priceFigureHits(rel, text string) []string {
	var hits []string
	for n, line := range strings.Split(text, "\n") {
		allowed := priceAllowedSpans(rel, line)
		for _, kind := range priceFigureKinds {
			for _, span := range kind.re.FindAllStringIndex(line, -1) {
				if priceSpanAllowed(allowed, span) {
					continue
				}
				hits = append(hits, fmt.Sprintf(
					"%s:%d carries %s (%q): the repository is public and the business figures are private (D430, T22), so a price, a currency amount, a discount, a fee rate or the Enterprise minimum belongs in the owner's private business repository; the Team price is inserted at the owner's publication (S7.4). Line: %s",
					rel, n+1, kind.what, strings.TrimLeft(line[span[0]:span[1]], " \t"), strings.TrimSpace(line)))
			}
		}
	}
	return hits
}

// priceAllowedSpans is each allowed rendering's span on one line of one file.
// The allow list is per file: the Terms' own sentences are allowed in the Terms
// and in nothing else.
func priceAllowedSpans(rel, line string) [][2]int {
	var spans [][2]int
	for _, allow := range priceFigureAllow {
		if allow.path != rel {
			continue
		}
		for i := 0; i < len(line); {
			j := strings.Index(line[i:], allow.text)
			if j < 0 {
				break
			}
			start := i + j
			spans = append(spans, [2]int{start, start + len(allow.text)})
			i = start + len(allow.text)
		}
	}
	return spans
}

// priceSpanAllowed reports whether a figure's span lies inside an allowed
// rendering, so that "the audit reveals an underpayment of more than 5%" is
// allowed while a new "5%" beside it is not.
func priceSpanAllowed(allowed [][2]int, span []int) bool {
	for _, a := range allowed {
		if span[0] >= a[0] && span[1] <= a[1] {
			return true
		}
	}
	return false
}
