package cli

// The Project-tab settings-edit docs package, pinned.
//
// docs/console-settings-edit/ is a design-only folder: README.md (the index),
// spec.md (the owner's ask, verbatim, with his rulings on the design),
// reconciliation.md (findings F1-F16 against the tree and against the
// settings-write command, and this work's decisions D1100-D1121) and
// implementation-plan.md (the CLI prerequisites P1-P4 and the console tasks
// C1-C6). It adds no code, and no task in it is approved, so nothing here pins
// a command, a flag or a console operation.
//
// What it does pin is the half of these documents that rots silently: the
// decision block, and the citations the findings rest on. A reconciliation
// whose citations have moved is a reconciliation nobody can check, and half of
// this folder cites a command that landed while the design was in review, so a
// drifted line is exactly the failure a reader cannot see.
//
//  * TestDocsConsoleSettingsEditIsWritten -- the four files exist, the index
//    carries the status line and the owner as the spec owner, and the plan
//    states the dependency that orders every task in it: the settings-write
//    pull request, which has to be on `main` before any of them.
//  * TestDocsConsoleSettingsEditDecisionsAreInRange -- D1100-D1121 are written,
//    each exactly once and in order; every one is answered, and an answered one
//    cites the ask it was answered in; and D1122-D1139, the reserved tail, is
//    written as no decision at all.
//  * TestDocsConsoleSettingsEditCitationsResolve -- every `path:line` citation
//    resolves to a file of this tree and a line inside it, measured against an
//    independent census so a regexp that stopped matching fails rather than
//    passing vacuously.
//  * TestDocsConsoleSettingsEditRelativeCitationsResolve -- the same rule for
//    the `:NNN` shape, which names no file and resolves against the nearest
//    citation before it in its own paragraph.
//
// The citation resolver is the cloud senior review's (`csrBareNames`,
// `csrResolveCitation`): two docs tests asking one question of one tree should
// not each carry their own, and a second copy is the one that drifts. The
// regexps are this folder's own, because half of what it cites is TypeScript --
// the console's argv tables, its window and its spawn allow-list -- and a
// pattern written for `.go` alone would leave every one of those unchecked
// while the census below still passed.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	cseReadme     = "docs/console-settings-edit/README.md"
	cseSpec       = "docs/console-settings-edit/spec.md"
	cseReconciled = "docs/console-settings-edit/reconciliation.md"
	csePlan       = "docs/console-settings-edit/implementation-plan.md"
)

// cseDocs is every file of the folder that cites code, in the index's own
// order. The index summarises and links, so a citation census over it would
// only ever read zero.
var cseDocs = []string{cseSpec, cseReconciled, csePlan}

// The block this work holds, as the documents write it: D1100-D1121 written,
// D1122-D1139 reserved and unused. The owner has answered all of D1100-D1121:
// D1100 and D1101 in the ask itself, D1102-D1106 and D1108-D1121 as the design
// recommended, and D1107 in his own words, against the recommendation.
const (
	cseBlockFirst    = 1100
	cseBlockLast     = 1121
	cseReservedFirst = 1122
	cseReservedLast  = 1139
)

// TestDocsConsoleSettingsEditIsWritten: the folder exists, and the two
// documents a reader meets first state the facts they have to have -- what
// state the design is in, who owns the spec, and the dependency that orders the
// whole plan.
func TestDocsConsoleSettingsEditIsWritten(t *testing.T) {
	for _, path := range []string{cseReadme, cseSpec, cseReconciled, csePlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, cseReadme))
	for _, want := range []string{
		"**Status: plan only; nothing is implemented; no task approved.**",
		"**Spec owner:** the owner, 2026-11-01.",
		// The ask itself, so the index cannot drift into a paraphrase of it.
		"a little pencil icon to trigger an edit",
		"**all edits, including widenings, from day one**",
		// The block, and the reservation above it that keeps two works from
		// taking adjacent numbers. How D1107 was answered is pinned because it
		// is the one thing a reader takes from the index without reading the
		// decisions: the widening path is settled by allowing `--yes` and by
		// giving the console no hook of its own, and an index that lost that
		// would read as if a console-only flag were still coming.
		"**D1100–D1139**",
		"**D1100–D1121 are written and ANSWERED** by the owner on 2026-11-01",
		"**D1107 in his own words — `--yes` is allowed, and the console gets no hook**",
		"**D1122–D1139 are reserved and unused**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", cseReadme, want)
		}
	}
	// The plan carries the dependency that orders every task in it, and the
	// pull request it names by number. Without it the plan reads as buildable
	// today, which is the one thing it is not.
	plan := flattenDoc(readDoc(t, csePlan))
	for _, want := range []string{
		"**Nothing here is approved.**",
		"**The dependency that orders everything: S5, which has merged.**",
		"pull request **#1525**",
		"**S5 (#1525) has merged → P1 → C1 → C2 → C3 → C4 → C5 → C6.**",
		// How the widening is confirmed, which is the whole of what the owner's
		// D1107 ruling changed in this plan: the CLI accepts `--yes` for it and
		// the console gets no flag of its own.
		"`settings set|unset` accept `--yes` for a widening, outside an agent session (**D1107**)",
		"**There is no console side to this task.**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q: the plan is written against a command that landed while the design was in review, and that is the first thing a reader of it has to know", csePlan, want)
		}
	}
}

// cseDecisionRE finds a decision entry's opening, and nothing else: the
// reserved numbers are checked against it too, so a written decision in the
// tail fails here rather than passing as prose.
var cseDecisionRE = regexp.MustCompile(`(?m)^- \*\*D([0-9]{4}) \(`)

// TestDocsConsoleSettingsEditDecisionsAreInRange: this work's block is written,
// every decision in it is either open with a recommendation or answered with
// the ask it was answered in, and the block does not run past the numbers this
// work reserved.
//
// The two answered ones are pinned by their citation of spec.md, because
// "answered" is the claim a later edit softens first: an answer the owner gave
// in the ask is not a decision this design took, and a document that lost the
// citation would read as if the design had answered them itself.
func TestDocsConsoleSettingsEditDecisionsAreInRange(t *testing.T) {
	raw := readDoc(t, cseReconciled)
	matches := cseDecisionRE.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) != cseBlockLast-cseBlockFirst+1 {
		t.Fatalf("%s writes %d decisions and this work's block is D%d-D%d, %d of them: a decision number outside the block is a number taken without the owner giving it",
			cseReconciled, len(matches), cseBlockFirst, cseBlockLast, cseBlockLast-cseBlockFirst+1)
	}
	seen := map[int]bool{}
	for i, m := range matches {
		n, err := strconv.Atoi(raw[m[2]:m[3]])
		if err != nil {
			t.Fatalf("%s writes a decision number that is not a number: %v", cseReconciled, err)
		}
		if n < cseBlockFirst || n > cseBlockLast {
			t.Errorf("%s writes D%d, outside this work's block D%d-D%d", cseReconciled, n, cseBlockFirst, cseBlockLast)
		}
		if seen[n] {
			t.Errorf("%s writes D%d twice: a decision id is unique, and a second entry is either a duplicate or a renumbering", cseReconciled, n)
		}
		seen[n] = true
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := flattenDoc(raw[m[0]:end])
		switch {
		case strings.Contains(block, "OPEN, recommended"):
			if !strings.Contains(block, "Recommendation:") {
				t.Errorf("%s' D%d is open and carries no recommendation: an open decision without one is a question, not a decision, and the owner has nothing to answer", cseReconciled, n)
			}
		case strings.Contains(block, "ANSWERED by the owner"):
			if !strings.Contains(block, "[spec.md](spec.md)") {
				t.Errorf("%s' D%d records an answer without citing the ask it was given in: an answer the owner gave is not a decision this design took", cseReconciled, n)
			}
		default:
			t.Errorf("%s' D%d is written without an answer field: every decision says whether it is open, with a recommendation, or answered, with the ask it was answered in", cseReconciled, n)
		}
	}
	for n := cseBlockFirst; n <= cseBlockLast; n++ {
		if !seen[n] {
			t.Errorf("%s does not write D%d: this work's block is D%d-D%d and every number in it is a decision a reader is owed", cseReconciled, n, cseBlockFirst, cseBlockLast)
		}
	}
	// The reserved tail is written as no decision at all. Prose may name the
	// range; a heading or a bullet for one of its numbers would be a decision
	// this work took without the owner giving it.
	for n := cseReservedFirst; n <= cseReservedLast; n++ {
		if shape := "- **D" + strconv.Itoa(n) + " ("; strings.Contains(raw, shape) {
			t.Errorf("%s writes %q: D%d-D%d are reserved and unused, and a written decision in that range is a number this work took without the owner giving it", cseReconciled, shape, cseReservedFirst, cseReservedLast)
		}
	}
}

// cseCitationRE finds a backtick-quoted repository path with a line number. The
// extension list carries the console's own (`ts`, `tsx`, `css`), because this
// folder's findings are half about the console: a pattern written for `.go`
// alone would leave the main process's `ipc.ts:431` and every citation like it
// unchecked while still passing its own census.
var cseCitationRE = regexp.MustCompile("`([A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl))(:[0-9]+)`")

// cseCensusRE is the independent count of how many path-looking tokens carry a
// line number anywhere in them, deliberately looser than the citation regexp
// above: it does not care where the path starts. It is the floor the citation
// check is measured against, so a regexp that has stopped matching the
// documents' shapes fails the test instead of passing it vacuously.
var cseCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl)[^`\\n]*:[0-9]+`")

// TestDocsConsoleSettingsEditCitationsResolve: every file and line these
// documents cite exists. The findings are only worth as much as the code behind
// them, and a citation whose line moved turns a checked finding into an
// unchecked one with no other symptom.
//
// Every one of them is checked, including the settings write path this folder
// was written against while it was still an open pull request (#1525): it has
// since merged, so there is no file left to except, and the two files that were
// excepted here are cited by line like everything else.
func TestDocsConsoleSettingsEditCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cseDocs {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens with
		// a line the document holds at all. The check below has to see at least
		// that many, so a regexp that stopped matching a shape the prose uses
		// fails here rather than quietly checking less.
		census := len(cseCensusRE.FindAllString(doc, -1))
		matches := cseCitationRE.FindAllStringSubmatch(doc, -1)
		if len(matches) < census {
			t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape the citation regexp no longer knows, so those citations are unchecked",
				path, census, len(matches))
		}
		for _, m := range matches {
			file, ok := csrResolveCitation(m[1], bare)
			if !ok {
				t.Errorf("%s cites %q, which is not a file in this tree: a reconciliation whose citations have moved is a reconciliation nobody can check", path, m[1])
				continue
			}
			line, err := strconv.Atoi(strings.TrimPrefix(m[2], ":"))
			if err != nil {
				t.Errorf("%s cites %q%s, whose line is not a number: %v", path, m[1], m[2], err)
				continue
			}
			body, err := os.ReadFile("../../" + filepath.FromSlash(file))
			if err != nil {
				t.Errorf("%s cites %q, which could not be read, so the line is unchecked: %v", path, m[1], err)
				continue
			}
			// The trailing newline every file ends with is not a line of it.
			lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
			if line < 1 || line > lines {
				t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
					path, m[1], m[2], file, lines, line)
			}
		}
		t.Logf("%s: %d citations carrying a line, against a census of %d path-looking tokens with one", path, len(matches), census)
	}
}

// TestDocsConsoleSettingsEditRelativeCitationsResolve: every `:NNN` reference
// resolves against the nearest citation before it in its own paragraph, to a
// line that is in that file. The findings lean on this shape -- a paragraph
// cites `settings_set.go:678` and then `:502` -- and a reference whose anchor
// moved sends a reader to a line of the wrong file while the paragraph still
// reads as checked.
func TestDocsConsoleSettingsEditRelativeCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cseDocs {
		doc := readDoc(t, path)
		refs := 0
		for _, block := range strings.Split(doc, "\n\n") {
			for _, loc := range csrRelativeRE.FindAllStringIndex(block, -1) {
				anchor := ""
				for _, m := range cseCitationRE.FindAllStringSubmatchIndex(block[:loc[0]], -1) {
					anchor = block[m[2]:m[3]] + block[m[4]:m[5]]
				}
				if anchor == "" {
					t.Errorf("%s holds %s with no citation before it in its own paragraph: a line reference resolves against the paragraph's nearest file, and there is none, so the reference names nothing",
						path, block[loc[0]:loc[1]])
					continue
				}
				cut := strings.LastIndex(anchor, ":")
				file, ok := csrResolveCitation(anchor[:cut], bare)
				if !ok {
					t.Errorf("%s holds %s anchored to %q, which is not a file in this tree", path, block[loc[0]:loc[1]], anchor[:cut])
					continue
				}
				refs++
				line, err := strconv.Atoi(strings.Trim(block[loc[0]:loc[1]], "`:"))
				if err != nil {
					t.Errorf("%s holds %s, whose line is not a number: %v", path, block[loc[0]:loc[1]], err)
					continue
				}
				body, err := os.ReadFile("../../" + filepath.FromSlash(file))
				if err != nil {
					t.Errorf("%s holds %s anchored to %q, which could not be read: %v", path, block[loc[0]:loc[1]], file, err)
					continue
				}
				lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
				if line < 1 || line > lines {
					t.Errorf("%s holds %s anchored to %q, but %s holds %d lines: the reference names a line that is not there",
						path, block[loc[0]:loc[1]], anchor, file, lines)
				}
			}
		}
		t.Logf("%s: %d relative references resolved against their paragraph's anchor", path, refs)
	}
}
