package cli

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The modes' product document — `docs/rigor-modes.md` — and the guard that owns
// it.
//
// The folder `docs/rigor-modes/` holds the design, the reconciliation that read
// the tree back and the plan, and it has its own guard
// (docs_rigor_modes_test.go). This file is the other half of the split the
// folder's index states: the product document is the promise, it lives outside
// the folder, and it is written for a reader who wants to use the modes rather
// than to build them. So what it holds is the promise's own shape — the status
// line, the claim that it is a product document and not a plan, the mode table
// against the vocabulary the code has, and every command, flag and key it names
// against the ones that exist. Each is a sentence a later edit can quietly
// break, which is what makes them worth pinning rather than asserting in prose.
//
// It reuses the folder guard's helpers (readDoc, rigorModesTableCells,
// rigorModesTaskLabels) rather than copying them: the two guards read one
// repository and one vocabulary, and a second spelling of a table-cell splitter
// is a second thing to keep right.
const (
	rigorModesProductDoc = "docs/rigor-modes.md"

	// rigorModesProductStatus is the document's status line, word for word. It
	// is the first thing a reader meets and the one line that says what the page
	// is: a document about a built surface, and not a plan for one.
	rigorModesProductStatus = "**Status: the modes are built and decide runs today; this is their product document — the design, its reconciliation and its plan live in [rigor-modes/](rigor-modes/README.md), and this page is not a plan.**"

	// rigorModesProductModesHeading is the section the mode table hangs from,
	// and it is what tells the table apart from any other table a later edit
	// adds: the guard reads the rows under this heading and nowhere else, so a
	// second table cannot be read as this one.
	rigorModesProductModesHeading = "## The modes"

	// rigorModesProductDesignFolder is the folder a reader is sent to for the
	// design, the reconciliation and the plan. T7.1's own reason for the
	// separate page is that a folder ages into a design record and a product
	// document is a promise; a page that stops naming it leaves a reader with
	// the promise and no way back to its reasons.
	rigorModesProductDesignFolder = "[rigor-modes/](rigor-modes/README.md)"
)

// rigorModesProductScopeKeys are the configuration keys the document names,
// spelled the way the scope table spells them. Each one has to exist there —
// a document that names a key no layer accepts is a reader's failed edit — and
// each one has to be named in the document, because a mode's own knobs are what
// the page is about.
//
// The project layer's `defaults.rigor.default` and `defaults.rigor.areas` are
// the same key in its project spelling and are held with the commands below;
// `review_floor` and `max_rounds` are not scope rows — the first is the local
// project config's own key and the second is a recipe's — so neither is here.
var rigorModesProductScopeKeys = []string{
	"agent.first_line_review",
	"agent.first_line_rounds",
	"agent.review_rounds",
	"agent.review_stop_fraction",
	"agent.triage",
	"budget.per_run_usd",
	"rigor.default",
	"rigor.areas",
}

// rigorModesProductTable reads the mode table under
// rigorModesProductModesHeading, one row per mode and its cells in order. The
// header row and the separator are named by no backticked first cell, so they
// fall out of it, and a row whose shape moved is an error rather than a row
// this guard quietly skips: a table the guard reads short is a table it would
// pass on a document that is wrong.
func rigorModesProductTable(t *testing.T, doc string) [][]string {
	t.Helper()
	i := strings.Index(doc, rigorModesProductModesHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q: the mode table hangs from that heading", rigorModesProductDoc, rigorModesProductModesHeading)
	}
	body := doc[i:]
	if j := strings.Index(body[1:], "\n## "); j >= 0 {
		body = body[:j+1]
	}
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := rigorModesTableCells(line)
		if len(cells) == 0 || !strings.HasPrefix(strings.TrimSpace(cells[0]), "`") {
			continue // the header and the separator, which name no mode
		}
		if len(cells) != 5 {
			t.Errorf("%s: the mode table's row %q has %d cells, want 5 (Mode | What it is for | Rounds | What blocks | Share of the cap)", rigorModesProductDoc, line, len(cells))
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: the mode table under %q lists no mode", rigorModesProductDoc, rigorModesProductModesHeading)
	}
	return rows
}

// TestDocsRigorModesProductIsAProductDocument: the page carries its status line
// as the first thing under its title, it says in its own words that it is a
// product document and not a plan, it sends a reader to the folder its design
// lives in, and it carries none of the seven labels a task of either plan in
// that folder has to state. A page that grows a task block is a plan, and a
// reader who came for the promise is then reading a work plan.
func TestDocsRigorModesProductIsAProductDocument(t *testing.T) {
	doc := readDoc(t, rigorModesProductDoc)

	// The status line is the document's own first line after its title: a
	// status line a later paragraph has been slipped in front of is not the
	// line a reader meets, and the mistake is invisible in a diff of prose.
	lines := strings.Split(doc, "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "# ") {
		t.Fatalf("%s does not open with a heading, so the status line below is not a document's", rigorModesProductDoc)
	}
	if got := strings.TrimSpace(lines[2]); got != rigorModesProductStatus {
		t.Errorf("%s's status line is\n  %q\nand this guard pins\n  %q: the status line is the one line that says what the page is", rigorModesProductDoc, got, rigorModesProductStatus)
	}

	// It is a product document and not a plan, in its own words. The two halves
	// are separate claims: a page can be about a built surface and still be a
	// plan for more of it.
	for _, want := range []string{
		"this is their product document",
		"this page is not a plan",
		rigorModesProductDesignFolder,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: a reader has to be able to tell a product document from a plan, and to find the design it promises", rigorModesProductDoc, want)
		}
	}

	// And it carries none of a plan's task labels. rigorModesTaskLabels are the
	// seven things a task of this folder's plans states (docs_rigor_modes_test.go),
	// so their presence here is what a page becomes when it stops being the
	// promise and goes back to being work to do.
	for _, label := range rigorModesTaskLabels {
		if strings.Contains(doc, label) {
			t.Errorf("%s carries a plan's %s label: this page is the product document, and the folder's plans are where a task is written", rigorModesProductDoc, label)
		}
	}
}

// TestDocsRigorModesProductTableMatchesTheCode: the mode table has one row per
// mode the code has and no row for a mode it does not, in the vocabulary's own
// order, and each row's two figures are the code's.
//
// Both directions are the point. A mode added to `internal/rigor` without a row
// here is a mode the document does not promise; a row here for a mode the code
// never had is a promise nothing keeps — and the vocabulary is read out of
// `internal/rigor.Modes` rather than restated, so neither can drift from the
// other silently. The figures are read the same way: the rounds column is the
// mode's own default pair (`rigor.DefaultRounds`) and the last column is the
// share of the per-run cap the mode carries (`config.RigorBudgetFraction`),
// which is a promise about money that a remembered number would get wrong the
// day either table moves.
func TestDocsRigorModesProductTableMatchesTheCode(t *testing.T) {
	doc := readDoc(t, rigorModesProductDoc)
	rows := rigorModesProductTable(t, doc)

	if len(rows) != len(rigor.Modes) {
		t.Errorf("%s's mode table has %d rows and internal/rigor has %d modes: a mode the code has without a row is a mode the document does not promise, and a row for a mode the code lost is a promise nothing keeps", rigorModesProductDoc, len(rows), len(rigor.Modes))
	}
	for i, mode := range rigor.Modes {
		if i >= len(rows) {
			t.Errorf("%s's mode table has no row for %q, which internal/rigor.Modes holds", rigorModesProductDoc, mode)
			continue
		}
		if got := strings.TrimSpace(rows[i][0]); got != "`"+string(mode)+"`" {
			t.Errorf("%s's mode table row %d names %s and internal/rigor.Modes holds %q there: the table and the vocabulary are read in the same order", rigorModesProductDoc, i+1, got, mode)
			continue
		}
		rounds := rigor.DefaultRounds(mode)
		if want := fmt.Sprintf("%d / %d", rounds.Inner, rounds.Senior); strings.TrimSpace(rows[i][2]) != want {
			t.Errorf("%s: %s's rounds cell is %q and rigor.DefaultRounds(%s) is %s: the document promises the default the code supplies", rigorModesProductDoc, mode, strings.TrimSpace(rows[i][2]), mode, want)
		}
		fraction := fmt.Sprintf("%.2f", config.RigorBudgetFraction(mode))
		if got := strings.TrimSpace(rows[i][4]); !strings.HasPrefix(got, fraction) {
			t.Errorf("%s: %s's share of the per-run cap is %q, which does not start with %s — the figure config.RigorBudgetFraction(%s) answers: a mode's share is money, and a remembered number gets it wrong the day the table moves", rigorModesProductDoc, mode, got, fraction, mode)
		}
	}
}

// TestDocsRigorModesProductNamesWhatExists: every command, flag and
// configuration key the document names is one the CLI has. A product document is
// a promise a reader acts on, so a command that moved, a flag that was renamed
// and a key no layer accepts are the three ways it sends someone off to fail —
// and a page is edited by hand, which is exactly where a renamed flag survives.
//
// The commands are read out of the real command tree (`NewRootCmd`), the flags
// out of the flag set of the command the span names them on, and the keys out of
// `internal/config`'s scope table — the table every layer is validated against,
// so a key it does not hold is a key no `fugaro.yaml` may write.
func TestDocsRigorModesProductNamesWhatExists(t *testing.T) {
	doc := readDoc(t, rigorModesProductDoc)
	root := NewRootCmd()

	spans := rigorModesProductSpans(doc)
	if len(spans) == 0 {
		t.Fatalf("%s names no fugaro command: a product document that shows none is a page a reader cannot act on", rigorModesProductDoc)
	}
	for _, span := range spans {
		path, ok := rigorModesProductCommand(t, root, span)
		if !ok {
			continue
		}

		// Every flag a span names is looked up on the command that span names it
		// on, not on the tree as a whole: a flag that moved to another command
		// still exists somewhere, so the tree-wide set would pass a reader a
		// span that no longer runs.
		c, _, err := root.Find(path)
		if err != nil || c == nil {
			continue // reported by rigorModesProductCommand
		}
		for _, f := range rigorModesProductFlags(span) {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("%s names --%s on `fugaro %s`, which has no such flag", rigorModesProductDoc, f, strings.Join(path, " "))
			}
		}
	}

	// Every flag named outside a command span — a page explains `--mode` in
	// prose as often as it shows it in a command — against the whole tree, which
	// is the most a span-less mention can be held to.
	flags := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, m := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(doc, -1) {
		if !flags[m[1]] {
			t.Errorf("%s names --%s, which no command has", rigorModesProductDoc, m[1])
		}
	}

	// Every configuration key, against the scope table. Both halves are asked:
	// a key the doc names and the table does not is a reader's failed edit, and
	// a key the table holds and the doc stopped naming is a knob the page no
	// longer explains. A page may show a key's value beside it — the document
	// writes `agent.triage: false` — so a code span that opens with the key
	// names it, value or no value.
	scopes := map[string]bool{}
	for _, row := range config.Scopes {
		scopes[row.Key] = true
	}
	for _, key := range rigorModesProductScopeKeys {
		if !scopes[key] {
			t.Errorf("internal/config's scope table holds no %s, and this guard reads %s as naming it: the key moved, and the document has to move with it", key, rigorModesProductDoc)
		}
		if !strings.Contains(doc, "`"+key+"`") && !strings.Contains(doc, "`"+key+":") {
			t.Errorf("%s no longer names the key %s: a mode's knobs are what the page is about, and a key the scope table holds is one a reader may write", rigorModesProductDoc, key)
		}
	}
}

// TestDocsRigorModesProductStatesTheOptInBound: the document says that a mode's
// blocking row is applied only to a run whose repository opted in, and that a
// repository that configures nothing blocks on every finding it did not fix.
//
// It is pinned because every other claim on the page is false without it: the
// runner reads the table only where rigor.Config.Configured, so a page that
// promises `pro`'s table to a repository that configures nothing promises
// something no run does. The sentence and the code are asked in the same breath,
// since either one moving alone is the mistake.
func TestDocsRigorModesProductStatesTheOptInBound(t *testing.T) {
	doc := readDoc(t, rigorModesProductDoc)
	for _, want := range []string{
		"**The table applies to a run that opted in.**",
		"blocks on every finding it did not fix",
		"`rigor.Config.Configured`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: the mode's table is read only for a repository that opted in, and a page that promises it to one that configured nothing promises what no run does", rigorModesProductDoc, want)
		}
	}
	if (rigor.Config{}).Configured() {
		t.Errorf("rigor.Config{}.Configured() is true, and %s reads a repository that configures nothing as getting no row: the sentence and the gate have to move together", rigorModesProductDoc)
	}
	if !(rigor.Config{Repo: rigor.Pro}).Configured() {
		t.Errorf("a repository whose own rigor block names pro is not Configured, and %s reads a rigor: block as the opt-in", rigorModesProductDoc)
	}
}

// TestDocsRigorModesProductStatesTheHotfixDoor: the document says that
// `fugaro run --mode hotfix` is what reaches `hotfix`, and that it needs no
// opt-in — the owner's ruling of 2026-11-13 dropped the gate the spec named
// rather than building it (docs/rigor-command/implementation-plan.md R7).
//
// The two halves are the resolver's own behaviour, so they are asked of it here:
// a page that named a hand-written layer as the only door would send an operator
// to a configuration edit for a mode a flag reaches, and one that promised an
// opt-in would send them looking for a key that does not exist.
func TestDocsRigorModesProductStatesTheHotfixDoor(t *testing.T) {
	doc := flattenDoc(readDoc(t, rigorModesProductDoc))
	for _, want := range []string{
		"`fugaro run --mode hotfix`",
		"no opt-in and no authorization read from the base branch",
		"dropped rather than built",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: the flag is the door to hotfix, and it needs no opt-in", rigorModesProductDoc, want)
		}
	}
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Pro}, rigor.Request{Mode: rigor.Hotfix}); got.Mode != rigor.Hotfix || got.From != rigor.FromRequest {
		t.Errorf("a request for hotfix resolved (%s, %s), and %s reads the flag as the door: the request and the resolver have to move together", got.Mode, got.From, rigorModesProductDoc)
	}
	if got := rigor.Resolve(rigor.Config{Repo: rigor.Hotfix}, rigor.Request{}); got.Mode != rigor.Hotfix {
		t.Errorf("a rigor block naming hotfix resolved %s, and %s reads a hand-written layer as a way in too", got.Mode, rigorModesProductDoc)
	}
}

// rigorModesProductSpans is every command the document shows: an inline code
// span that opens with `fugaro `, and every line of a fenced block that opens
// with one, which is where a page puts the commands a reader copies. A fenced
// line's trailing `#` comment is dropped first, since the fences are shell.
func rigorModesProductSpans(doc string) []string {
	spans := regexp.MustCompile("`fugaro [^`]*`").FindAllString(doc, -1)
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "fugaro ") {
			spans = append(spans, "`"+line+"`")
		}
	}
	return spans
}

// rigorModesProductArgument reports whether w is a word a command's own name does
// not hold: an uppercase placeholder the document writes for a value (`MODE`,
// `GLOB`, `RUN`, `N`), a mode from the vocabulary (`critical`), or a
// comma-separated list of review levels (`l1,l2,l3`). Every other word after a
// command path has to resolve as the next subcommand of it, which is what catches
// a rename anywhere in the path: read as "the longest prefix that resolves", a
// `fugaro rigor floors show` is `fugaro rigor` plus two words of arguments, and
// that is the rename this guard exists to catch.
func rigorModesProductArgument(w string) bool {
	if w != strings.ToLower(w) && w == strings.ToUpper(w) {
		return true
	}
	return rigor.Known(rigor.Mode(w)) || rigorModesProductLevelsRE.MatchString(w)
}

// rigorModesProductLevelsRE is the shape `fugaro rigor floor set` takes its
// levels in: `l1`, `l2`, `l3`, comma-separated (internal/cli/rigor_floor.go).
var rigorModesProductLevelsRE = regexp.MustCompile(`^l[123](,l[123])*$`)

// rigorModesProductCommand resolves the command a span names, reporting it as a
// failure when it is not one. The command is the span's leading words up to its
// first flag or its first argument word (rigorModesProductArgument), and every
// one of those words has to be a command: a span that stops resolving early is
// the renamed or removed command the document still promises.
func rigorModesProductCommand(t *testing.T, root *cobra.Command, span string) ([]string, bool) {
	t.Helper()
	words := strings.Fields(strings.Trim(span, "`"))
	var path []string
	for _, w := range words[1:] {
		if strings.HasPrefix(w, "-") || rigorModesProductArgument(w) {
			break
		}
		c, _, err := root.Find(append(append([]string{}, path...), w))
		if err != nil || c == nil || c.Name() != w {
			t.Errorf("%s names `fugaro %s`, which is not a command: the words up to the first flag or argument are a command's own path", rigorModesProductDoc, strings.Join(append(path, w), " "))
			return nil, false
		}
		path = append(path, w)
	}
	if len(path) == 0 {
		t.Errorf("%s names %s, and no command starts with that", rigorModesProductDoc, span)
		return nil, false
	}
	return path, true
}

// rigorModesProductFlags is every flag a span names, in the order it names them:
// the words that open with `--` once a bracketed optional one (`[--repo R]`) has
// had its brackets taken off, and `--name=value` has had its value.
func rigorModesProductFlags(span string) []string {
	var out []string
	for _, w := range strings.Fields(strings.Trim(span, "`")) {
		w = strings.Trim(w, "[]")
		if !strings.HasPrefix(w, "--") {
			continue
		}
		if name := strings.TrimPrefix(w, "--"); name != "" {
			out = append(out, strings.SplitN(name, "=", 2)[0])
		}
	}
	return out
}
