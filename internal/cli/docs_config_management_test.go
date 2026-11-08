package cli

// The configuration-management docs folder, pinned.
//
// docs/config-management/ is a design-only folder: README.md (the index, the
// owner's ruling verbatim and the reading of the number block), spec.md (the
// design: what "managed" means for a configuration, the generic writer's
// extension, the dedicated commands that remain, the registry's owner column
// and the managed gate's rows), reconciliation.md (the audit F1-F14 against the
// tree, the corrections C1-C8 to the brief's own reading, and this work's
// decisions D1160-D1183) and implementation-plan.md (the tasks G0-G13). It adds
// no code and no task in it is approved, so nothing here pins a command, a flag
// or an option.
//
// What it does pin is the half of these documents that rots silently: the
// decision block, the citations the audit rests on, and the two figures the
// audit's own argument is made of -- the registry's row count and the count of
// rows the generic writer's allowlist carries.
//
//  * TestDocsConfigManagementIsWritten -- the four files exist, the index
//    carries the status line and the owner's ruling, and the plan states the
//    order it orders every task by.
//  * TestDocsConfigManagementDecisionsAreInRange -- D1160-D1183 are written,
//    each exactly once and in order, every one is open with a recommendation,
//    and D1184-D1189, the reserved tail, is written as no decision at all.
//  * TestDocsConfigManagementCitationsResolve -- every `path:line` citation
//    resolves to a file of this tree and a line inside it, measured against an
//    independent census so a regexp that stopped matching fails rather than
//    passing vacuously.
//  * TestDocsConfigManagementRelativeCitationsResolve -- the same rule for the
//    `:NNN` shape, which names no file and resolves against the nearest
//    citation before it in its own paragraph.
//  * TestDocsConfigManagementAllowlistCounts -- the audit's central figure: the
//    registry really holds the rows the documents claim, and the allowlist
//    really carries the rows they claim it carries.
//  * TestDocsRigorModesReconciliationCountsTheConfigManagementPin -- the census
//    row this folder moves is moved, and the figure in it is the live count of
//    the folders' pins rather than a number somebody typed.
//  * TestDocsConfigManagementNoSecondWritePath -- the property the plan states
//    for every task it adds: one write path, and the ten commands of its fifth
//    section are the ones it names.
//  * TestDocsConfigManagementKeepsTheSettingsWriteVersion -- the settings-write
//    document's version, pinned against the console that refuses any other, so
//    the design can never plan a bump that breaks the console's own edit.
//  * TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter -- the four
//    repo-layer budget keys, pinned against the scope table that makes them
//    repository rows, so the design can never hand them to the command that
//    writes the budget database instead.
//
// The citation resolver is the cloud senior review's (`csrBareNames`,
// `csrResolveCitation`), through the console-settings-edit folder's
// (`cseCitationRE`'s shapes): three docs tests asking one question of one tree
// should not each carry their own, and a second copy is the one that drifts.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

const (
	cfmReadme     = "docs/config-management/README.md"
	cfmSpec       = "docs/config-management/spec.md"
	cfmReconciled = "docs/config-management/reconciliation.md"
	cfmPlan       = "docs/config-management/implementation-plan.md"
)

// cfmDocs is every file of the folder that cites code, in the index's own
// order. The index summarises and links, so a citation census over it would
// only ever read zero.
var cfmDocs = []string{cfmSpec, cfmReconciled, cfmPlan}

// The block this work holds, as the documents write it: D1160-D1183 written,
// D1184-D1189 reserved and unused. Every decision in it is open with a
// recommendation -- the owner has answered none of them, which is why the plan's
// tasks do not start.
const (
	cfmBlockFirst    = 1160
	cfmBlockLast     = 1183
	cfmReservedFirst = 1184
	cfmReservedLast  = 1189
)

// The audit's two figures (F1): the registry's rows, and the rows the generic
// write path's allowlist carries -- eighty-eight by name and the rest through
// the nine prefixes. They are re-counted below rather than trusted, because the
// whole of §1's argument is that the rows no writer carries are the defect.
//
// The named figure moved with G0, which is what the audit's §5 says a figure in
// a document does: the allowlist gained the wildcard blocks D1161 hands this
// path and the two budget keys D1179 does, and the two documents whose argument
// is made of this count carry the number the count produced.
const (
	cfmRegistryRows   = 375
	cfmAllowlistNamed = 88
)

func TestDocsConfigManagementIsWritten(t *testing.T) {
	for _, path := range []string{cfmReadme, cfmSpec, cfmReconciled, cfmPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, cfmReadme))
	for _, want := range []string{
		"**Status: a design, and nothing is built.**",
		// The owner's ruling, so the index cannot drift into a paraphrase of it.
		"not to touch any of the configuration files by hand, be it repo, project, or local level",
		"we should have management commands in Fugaro just like with settings",
		// The block, and the fact that no decision in it is answered: a reader
		// who takes one thing from the index takes that.
		"**D1160 is therefore free.**",
		"D1160–D1183 are written here, and D1184–D1189 are reserved and unused.",
		"Fourteen tasks **G0–G13**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", cfmReadme, want)
		}
	}
	spec := flattenDoc(readDoc(t, cfmSpec))
	for _, want := range []string{
		"**Status: a design, and nothing is built.**",
		// Which document wins when the two disagree, because the spec is the
		// one a reader meets first and the reconciliation is the one that is
		// checked against the tree.
		"Where this file and [reconciliation.md](reconciliation.md) disagree, the reconciliation wins",
		"Recommendation: extend `fugaro settings set|unset` before any dedicated command is written** (D1160)",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", cfmSpec, want)
		}
	}
	// The plan carries the order that orders every task in it. Without it the
	// plan reads as fourteen independent tasks, which is the one thing it is not.
	plan := flattenDoc(readDoc(t, cfmPlan))
	for _, want := range []string{
		"**Status: plan only; nothing is implemented; no task approved.**",
		"**G0 → G1 → G2 → G3 → G4 → G5 → G6 → G7 → G8 → G9 → G10 → G11 → G12 → G13.**",
		// The slot, and why it is that one: the owner asked for this design
		// built behind managed mode.
		"**CLI 0.18.0**",
		"built behind managed mode",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", cfmPlan, want)
		}
	}
}

// cfmDecisionRE finds a decision entry's opening, and nothing else: the
// reserved numbers are checked against it too, so a written decision in the
// tail fails here rather than passing as prose.
var cfmDecisionRE = regexp.MustCompile(`(?m)^- \*\*D([0-9]{4}) \(`)

// TestDocsConfigManagementDecisionsAreInRange: this work's block is written,
// every decision in it is open with a recommendation, and the block does not
// run past the numbers this work reserved.
func TestDocsConfigManagementDecisionsAreInRange(t *testing.T) {
	raw := readDoc(t, cfmReconciled)
	matches := cfmDecisionRE.FindAllStringSubmatchIndex(raw, -1)
	want := cfmBlockLast - cfmBlockFirst + 1
	if len(matches) != want {
		t.Fatalf("%s writes %d decisions and this work's block is D%d-D%d, %d of them: a decision number outside the block is a number taken without the owner giving it",
			cfmReconciled, len(matches), cfmBlockFirst, cfmBlockLast, want)
	}
	seen := map[int]bool{}
	for i, m := range matches {
		n, err := strconv.Atoi(raw[m[2]:m[3]])
		if err != nil {
			t.Fatalf("%s writes a decision number that is not a number: %v", cfmReconciled, err)
		}
		if n < cfmBlockFirst || n > cfmBlockLast {
			t.Errorf("%s writes D%d, outside this work's block D%d-D%d", cfmReconciled, n, cfmBlockFirst, cfmBlockLast)
		}
		if seen[n] {
			t.Errorf("%s writes D%d twice: a decision id is unique, and a second entry is either a duplicate or a renumbering", cfmReconciled, n)
		}
		seen[n] = true
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := flattenDoc(raw[m[0]:end])
		if !strings.Contains(block, "OPEN, recommended") {
			t.Errorf("%s' D%d is not written as open: every decision in this work's block is open with a recommendation, and an entry that reads as answered is a decision the design took without the owner", cfmReconciled, n)
			continue
		}
		if !strings.Contains(block, "Recommendation:") {
			t.Errorf("%s' D%d is open and carries no recommendation: an open decision without one is a question, not a decision, and the owner has nothing to answer", cfmReconciled, n)
		}
		if !strings.Contains(block, "Cost of the alternative:") {
			t.Errorf("%s' D%d carries no cost of the alternative: the brief asks every open decision for the price of the other reading, and a decision without one is a preference", cfmReconciled, n)
		}
	}
	for n := cfmBlockFirst; n <= cfmBlockLast; n++ {
		if !seen[n] {
			t.Errorf("%s does not write D%d: this work's block is D%d-D%d and every number in it is a decision a reader is owed", cfmReconciled, n, cfmBlockFirst, cfmBlockLast)
		}
	}
	// The reserved tail is written as no decision at all. Prose may name the
	// range; a bullet for one of its numbers would be a decision this work took
	// without the owner giving it.
	for n := cfmReservedFirst; n <= cfmReservedLast; n++ {
		if shape := "- **D" + strconv.Itoa(n) + " ("; strings.Contains(raw, shape) {
			t.Errorf("%s writes %q: D%d-D%d are reserved and unused, and a written decision in that range is a number this work took without the owner giving it", cfmReconciled, shape, cfmReservedFirst, cfmReservedLast)
		}
	}
}

// cfmCitationRE finds a backtick-quoted repository path with a line number. The
// extension list is the console-settings-edit folder's (`go`, `md`, `json`,
// `yaml`, `yml`, `tf`, `tmpl`, and the console's `ts`, `tsx`, `css`), because
// this folder's findings are about Go, about a JSON Schema, about Terraform's
// neighbours and about documents: a pattern written for `.go` alone would leave
// `schemas/settings-write.schema.json:20` unchecked while still passing its own
// census.
var cfmCitationRE = regexp.MustCompile("`([A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl))(:[0-9]+)`")

// cfmCensusRE is the independent count of how many path-looking tokens carry a
// line number anywhere in them, deliberately looser than the citation regexp
// above: it does not care where the path starts. It is the floor the citation
// check is measured against, so a regexp that has stopped matching the
// documents' shapes fails the test instead of passing it vacuously.
var cfmCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl)[^`\\n]*:[0-9]+`")

// TestDocsConfigManagementCitationsResolve: every file and line these documents
// cite exists. The audit is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom.
func TestDocsConfigManagementCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cfmDocs {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens with
		// a line the document holds at all. The check below has to see at least
		// that many, so a regexp that stopped matching a shape the prose uses
		// fails here rather than quietly checking less.
		census := len(cfmCensusRE.FindAllString(doc, -1))
		matches := cfmCitationRE.FindAllStringSubmatch(doc, -1)
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

// TestDocsConfigManagementRelativeCitationsResolve: every `:NNN` reference
// resolves against the nearest citation before it in its own paragraph, to a
// line that is in that file. The findings and the decisions lean on this shape
// -- a paragraph cites `internal/settings/registry.go:210` and then `:213` --
// and a reference whose anchor moved sends a reader to a line of the wrong file
// while the paragraph still reads as checked.
func TestDocsConfigManagementRelativeCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cfmDocs {
		doc := readDoc(t, path)
		refs := 0
		for _, block := range strings.Split(doc, "\n\n") {
			for _, loc := range csrRelativeRE.FindAllStringIndex(block, -1) {
				anchor := ""
				for _, m := range cfmCitationRE.FindAllStringSubmatchIndex(block[:loc[0]], -1) {
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

// TestDocsConfigManagementAllowlistCounts: the figure the audit's whole argument
// is made of is re-counted here rather than trusted. The registry holds 375 rows
// and the generic write path's allowlist carries eighty-eight of them by name
// plus the rows its nine prefixes reach; the documents state the registry's
// count and the allowlist's, and a row added to either -- which moves the gap
// between the two -- fails here rather than leaving §1's "210 rows have no
// generic writer" reading as checked when it is not.
//
// The count moved with G0, which gained the allowlist the wildcard blocks
// D1161 hands it and the two budget keys D1179 does, with the workspace rows
// main added, with O4, whose six orchestrator guardrail rows the generic path
// does not write -- they are the owner's policy, and a family that would write
// them does not exist yet -- and with R13, whose two jobs: rows it refuses for
// the same reason: turning a schedule on is the operator's own edit of the
// layer file.
//
// It counts through the same predicate the write path uses, so the test and the
// command cannot disagree about what "the allowlist carries" means: the three
// refusals settingsWriteRowHinted makes before the allowlist is asked are
// applied here in the same order.
func TestDocsConfigManagementAllowlistCounts(t *testing.T) {
	rows := settings.Rows()
	if len(rows) != cfmRegistryRows {
		t.Errorf("the registry holds %d rows and every document of this folder states %d: the figure the audit is argued from has moved", len(rows), cfmRegistryRows)
	}
	named, prefixed := 0, 0
	for _, r := range rows {
		if r.Sensitive {
			continue
		}
		if _, family := settingsFamilyCommands[r.Owner]; family {
			continue
		}
		if settingsGenericKeys[r.Key] {
			named++
			continue
		}
		if settingsGenericKey(r.Key) {
			prefixed++
		}
	}
	if named != cfmAllowlistNamed {
		t.Errorf("the allowlist names %d keys and the audit says %d: a key added to it is a key the audit's own subject changed", named, cfmAllowlistNamed)
	}
	carried := named + prefixed
	// Each document states the figure in its own sentence, so each is asked for
	// its own wording: the audit's finding states the allowlist's count, and the
	// index states the gap the ruling is about.
	for path, wants := range map[string][]string{
		cfmReconciled: {
			strconv.Itoa(len(rows)) + " rows",
			strconv.Itoa(carried) + " of them",
		},
		cfmReadme: {
			strconv.Itoa(len(rows)) + " rows",
			strconv.Itoa(carried) + " of the registry's",
			strconv.Itoa(len(rows)-carried) + " rows have no generic writer",
		},
	} {
		doc := flattenDoc(readDoc(t, path))
		for _, want := range wants {
			if !strings.Contains(doc, want) {
				t.Errorf("%s lacks %q: the audit's figure is re-counted here and the document has to carry the number the count produced (%d named + %d by prefix = %d, leaving %d with no writer)",
					path, want, named, prefixed, carried, len(rows)-carried)
			}
		}
	}
}

// TestDocsRigorModesReconciliationCountsTheConfigManagementPin: the census row
// that counts these folders' pins is a running total of other folders' guards,
// and its own text says a folder writing one moves the row in its own commit.
// This folder writes one, so the row is checked twice: that it carries the
// number the tree holds, and that its amendment sentences name this folder's pin
// -- a row moved without the sentence beside it is a figure a reader cannot
// trace to the commit that moved it.
func TestDocsRigorModesReconciliationCountsTheConfigManagementPin(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	// The count the row states, read out of the row itself rather than assumed:
	// the columns are `| construct | count | command |`.
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	stated := strings.TrimSpace(cols[2])
	if stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files: the row is a count a reader re-runs, so a folder that added a pin moves it", path, stated, len(pins))
	}
	if !strings.Contains(doc, "the configuration-management folder's\npin, moved the table's row from 92 to 93.") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit, and an amendment the row does not record is the merge that did not", path)
	}
}

// TestDocsConfigManagementNoSecondWritePath: the property the plan states for
// every task it adds -- one write path, the diff, the confirmation, the fence,
// the backup and the compare-and-swap, in `applySettingsEdit` -- and the ten
// commands whose tasks carry it. The property is a claim about code that does
// not exist yet, so this is a pin on the plan's own words and not a source
// check: what it asserts is that the plan states the property, names the helper,
// and names the same ten tasks in its fifth section as it writes headings for.
func TestDocsConfigManagementNoSecondWritePath(t *testing.T) {
	plan := readDoc(t, cfmPlan)
	for _, want := range []string{
		"*Every* write here goes through `applySettingsEdit`",
		"`internal/cli/settings_write.go:86`",
		"none of the ten commands in §5 carries a second one",
	} {
		if !strings.Contains(flattenDoc(plan), want) {
			t.Errorf("%s lacks %q: the one-write-path property is the reason no task here re-implements the diff, the fence or the compare-and-swap", cfmPlan, want)
		}
	}
	// §5 names the tasks that need the managed build's gate and its audit
	// writer, and the two that add to `settings set` without one. Each of the ten
	// is a task of this plan, and each has a heading -- a task a section names and
	// the plan never writes is a dependency on nothing.
	start := strings.Index(plan, "## 5. The tasks that need")
	end := strings.Index(plan, "## G0. ")
	if start < 0 || end < start {
		t.Fatalf("%s has no fifth section, or no first task heading after it: the section that says which tasks wait on the managed build is the one a reader checks the order against", cfmPlan)
	}
	section := plan[start:end]
	for _, task := range []string{"G0", "G1", "G3", "G4", "G6", "G7", "G8", "G9", "G10", "G11"} {
		if !regexp.MustCompile(`\b` + task + `\b`).MatchString(section) {
			t.Errorf("%s' §5 does not name %s: the section lists the tasks that call the managed build's gate or its audit writer and the tasks that need neither, so a task it leaves out is one whose dependency a reader cannot see", cfmPlan, task)
		}
		if !strings.Contains(plan, "\n## "+task+". ") {
			t.Errorf("%s names %s in §5 and writes no task heading for it", cfmPlan, task)
		}
	}
}

// TestDocsConfigManagementKeepsTheSettingsWriteVersion: the settings-write
// document is read by a settings view that pins its version -- its
// `SETTINGS_VIEW_DOC_VERSION` is 1 -- so the design may not bump it, and the
// two documents have to say so and name the file that pins it. A design that
// planned a version 2 would break every settings edit that view makes, with no
// other symptom. The file is pinned by its own name rather than by its path:
// this is Go in the CLI's module, and a reference under the console's directory
// is the boundary another test guards (Task 0.3's allow-list).
func TestDocsConfigManagementKeepsTheSettingsWriteVersion(t *testing.T) {
	for _, path := range []string{cfmSpec, cfmReconciled} {
		doc := flattenDoc(readDoc(t, path))
		if !strings.Contains(doc, "settingsview.ts:42") {
			t.Errorf("%s plans the settings-write document's members without naming the settings view's own version pin (`settingsview.ts:42`): the pin is why the version cannot move", path)
		}
		for _, stale := range []string{"gains a version 2", "moves to version 2", "reads a version-2 document"} {
			if strings.Contains(doc, stale) {
				t.Errorf("%s still plans a version 2 of the settings-write document (%q): the settings view refuses any version but 1, so a bump breaks the edit it spawns `settings set` behind", path, stale)
			}
		}
	}
}

// TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter: the four
// budget keys are a repository's own rows, and `fugaro budget set` writes the
// budget database's caps rather than the repository's fugaro.yaml. The rows are
// re-read from the scope table and the claim from the documents, so a design
// that hands the four to `fugaro budget set` -- which would refuse the two keys
// the generic path writes today -- fails here instead of at a person's settings
// edit.
func TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter(t *testing.T) {
	scopes := flattenDoc(readDoc(t, "internal/config/scope.go"))
	for _, key := range []string{"budget.mode", "budget.per_run_usd", "budget.allowed_models", "budget.per_day_usd"} {
		if !strings.Contains(scopes, `{Key: "`+key+`", In: InRepo`) {
			t.Errorf("internal/config/scope.go no longer holds %s as an InRepo row, so the design's reading of the four budget keys has moved", key)
		}
	}
	for _, path := range []string{cfmSpec, cfmReconciled} {
		doc := flattenDoc(readDoc(t, path))
		if !strings.Contains(doc, "stay the generic writer's") {
			t.Errorf("%s does not say the four budget keys stay the generic writer's: naming `fugaro budget set` for them refuses the two the allowlist carries today", path)
		}
		if strings.Contains(doc, "are owned by `fugaro budget set`") {
			t.Errorf("%s hands the budget caps to `fugaro budget set`, which writes the budget database and no registry row", path)
		}
	}
}
