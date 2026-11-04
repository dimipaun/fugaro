package cli

// The repository-image-removal docs folder, pinned.
//
// docs/repo-image-removal/ is a design-only folder: README.md (the index, the
// owner's ruling verbatim and the reading of the number block), spec.md (the
// design: what is removed, what stays, how a repository migrates and in what
// order, the retirement, the schemas and the goldens, the managed gate's rows
// and the owner-run live check that gates the tag), reconciliation.md (the audit
// F1-F25 against the tree, the corrections C1-C6 to the brief's own reading, and
// this work's decisions D1190-D1208) and implementation-plan.md (the tasks
// R0-R10). It adds no code and no task in it is approved, so nothing here pins a
// command, a flag or an option.
//
// What it does pin is the half of these documents that rots silently: the
// decision block, the citations the audit rests on, the figures the audit's own
// argument is made of -- the fixtures the removal invalidates, the rows of the
// scope table, the settings registry and the settings schema that make the
// removed keys settable -- and the two orders the folder's whole value is: the
// dogfood before the removal, and D1148 before both.
//
// The four figures are the tree of **the design's own commit**: they are what a
// repository can set today, and the removal is what takes them away. So the task
// that moves a figure re-points this file in the same commit -- the plan's §4
// names which (R2 the three settable surfaces, R3 the fixture figure) -- and a
// task that lands without it fails a test nothing else in the tree names. The
// Terraform half is written the same way, against the guards that **survive** the
// removal: the workflow job's `deletion_protection`, the registry's
// `prevent_destroy`, and the retirement flow's own typed confirmation. It does
// not pin the check job's protection line, because a planned task deletes that
// line with the job, and a pin on a line a task is going to delete is a pin that
// breaks on the task rather than on a mistake.
//
//  * TestDocsRepoImageRemovalIsWritten -- the four files exist, the index
//    carries the status line and the owner's ruling verbatim, the spec carries
//    the order that orders every task, and the plan carries its own signature.
//  * TestDocsRepoImageRemovalDecisionsAreInRange -- D1190-D1208 are written,
//    each exactly once and in order, every one is open with a recommendation and
//    the cost of both the alternative and the recommendation, and D1209-D1219,
//    the reserved tail, is written as no decision at all.
//  * TestDocsRepoImageRemovalCitationsResolve -- every `path:line` citation
//    resolves to a file of this tree and a line inside it, measured against an
//    independent census so a regexp that stopped matching fails rather than
//    passing vacuously.
//  * TestDocsRepoImageRemovalRelativeCitationsResolve -- the same rule for the
//    `:NNN` shape, which names no file and resolves against the nearest citation
//    before it in its own paragraph.
//  * TestDocsRepoImageRemovalCountsTheRemovedSurfaces -- the audit's four
//    figures, re-counted from the tree: the testdata fixtures carrying a removed
//    key, the scope table's rows for them, the settings registry's rows and the
//    settings schema's members. The design's whole subject is that these are what
//    a repository can set today, and a figure that moved with the code is a
//    document nobody can check.
//  * TestDocsRigorModesReconciliationCountsTheRepoImageRemovalPin -- the census
//    row this folder moves is moved, and the figure in it is the live count of
//    the folders' pins rather than a number somebody typed.
//  * TestDocsRepoImageRemovalKeepsTheRetirementOwnerRun -- the property the
//    brief's fourth settle point asks for: retirement is owner-run, never
//    automatic, never admitted by the gate, and the two protections that refuse
//    an ordinary converge are unchanged by every task of the plan.
//
// The citation resolver is the cloud senior review's (`csrBareNames`,
// `csrResolveCitation`), through the configuration-management folder's: four
// docs tests asking one question of one tree should not each carry their own.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	rirReadme     = "docs/repo-image-removal/README.md"
	rirSpec       = "docs/repo-image-removal/spec.md"
	rirReconciled = "docs/repo-image-removal/reconciliation.md"
	rirPlan       = "docs/repo-image-removal/implementation-plan.md"
)

// rirDocs is every file of the folder that cites code, in the index's own
// order. The index summarises and links, so a citation census over it would
// only ever read zero.
var rirDocs = []string{rirSpec, rirReconciled, rirPlan}

// The block this work holds, as the documents write it: D1190-D1208 written,
// D1209-D1219 reserved and unused. Every decision in it is open with a
// recommendation -- the owner ruled on 2026-11-04 that the repository-level
// image goes, and answered none of the questions that ruling opens, which is why
// the plan's tasks do not start.
const (
	rirBlockFirst    = 1190
	rirBlockLast     = 1208
	rirReservedFirst = 1209
	rirReservedLast  = 1219
)

// The audit's four figures, re-counted below rather than trusted: the testdata
// fixtures that carry one of the removed keys (D1197), the scope table's rows
// for those keys, the settings registry's rows and the settings schema's
// members -- the four surfaces whose removal is what makes a removed key
// unwritable rather than merely undecodable.
const (
	rirFixtures      = 50
	rirScopeRows     = 11
	rirRegistryRows  = 21
	rirSchemaMembers = 21
)

// rirKeysRE is the removed keys, as a path under `workflows.*` or a scope
// table's `Key:` value. `image.node` and `image.jdk` are deliberately left out:
// they were removed in 0.7.0 and their tombstones stay, which is the mechanism
// D1195 reuses for these keys.
var rirKeysRE = regexp.MustCompile(`(?:image\.(?:apt|setup|skip_build_scripts|tools|key_files)|dockerfile|rebuild\.(?:check|max_age|lockfiles|base|paths))`)

func TestDocsRepoImageRemovalIsWritten(t *testing.T) {
	for _, path := range []string{rirReadme, rirSpec, rirReconciled, rirPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, rirReadme))
	for _, want := range []string{
		"**Status: a design, and nothing is built. The owner answered every decision on 2026-11-04.**",
		// The owner's ruling, verbatim, so the index cannot drift into a
		// paraphrase of it: every task of the plan answers this sentence.
		"Completely disable repo-level images for now to make things simpler.",
		// The block, and the fact that every decision in it is answered: a reader
		// who takes one thing from the index takes that.
		"**D1190 is therefore free.**",
		"D1190–D1208 are written here, and D1209–D1219 are reserved and unused.",
		"Eleven tasks **R0–R10**",
		// The safeguard, which is the difference between this removal and a
		// removal: the tag waits for a check the owner runs.
		"**An owner-run live check gates the 0.19.0 tag.**",
		"findings **F1–F25**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", rirReadme, want)
		}
	}
	spec := flattenDoc(readDoc(t, rirSpec))
	for _, want := range []string{
		"**Status: a design, and nothing is built. Every decision in it is answered.**",
		// Which document wins when the two disagree: the spec is the one a
		// reader meets first and the reconciliation is the one checked against
		// the tree.
		"where this file and that one disagree, the reconciliation wins",
		// The order, which is the requirement and not a preference.
		"**The order is the requirement, not a preference.**",
		// The two halves of the migration the design leans on: promote is the
		// migration and is itself removed.
		"already is the migration — and it is removed (D1192)",
		"## 7. The safeguard: the owner-run live check that gates the 0.19.0 tag",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", rirSpec, want)
		}
	}
	// The plan carries the order that orders every task in it. Without it the
	// plan reads as eleven independent tasks, which is the one thing it is not.
	plan := flattenDoc(readDoc(t, rirPlan))
	for _, want := range []string{
		"**Status: plan only; nothing is implemented; no task approved.**",
		"**catalog ships → R0 → D1148 (both tasks) → R1 → R2 → R3 → R4 → R5 → R6 → R7 → R8 → R9 → R10.**",
		"**0.19.0 — one release after D1148's two tasks",
		// The ceiling, and the one subject that exceeds it: a reader checking
		// the plan against the brief's twelve-file rule has to find the reason.
		"**The ceiling of twelve files is per commit.**",
		// The arithmetic, because a sweep written as "four commits" was wrong
		// twice over: 50 files do not fit in four commits of twelve, and the
		// biggest directory is 27 files, which is over the ceiling in one
		// commit whatever the commit count is.
		"**Files (51, in five commits of ≤12).**",
		"**27 files, twice the ceiling on its own**",
		// Four commands go whole, and the plan says so in the words the spec's
		// §1.2 table uses: a plan that keeps an arm of one of them is a plan
		// whose own red step asserts a command the spec removes. The count is
		// four and not five because `image check` keeps the catalog job's arm.
		"**Four commands go whole**",
		"the derived build's two steps `gate` and `record` (`internal/cli/image_record.go:60`, `:145`)",
		// This folder's own pin is a file the removal moves, and §4 has to name
		// the tasks that move it: a pin left asserting the pre-removal tree is
		// the first removal task's failing test, and no other test names it.
		"| `internal/cli/docs_repo_image_removal_test.go` | **R2**, **R3**, R4 (and R10 reads it) |",
		"**R2 moves `Config scope rows` (93 → 82)**",
		"## R0. The dogfood repository's entry, its build and its pin",
		"## R10. The owner-run live check, and the 0.19.0 tag (the final task)",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", rirPlan, want)
		}
	}
}

// rirDecisionRE finds a decision entry's opening, and nothing else: the
// reserved numbers are checked against it too, so a written decision in the tail
// fails here rather than passing as prose.
var rirDecisionRE = regexp.MustCompile(`(?m)^- \*\*D([0-9]{4}) \(`)

// TestDocsRepoImageRemovalDecisionsAreInRange: this work's block is written,
// every decision in it carries the owner's answer of 2026-11-04 -- the
// recommendation where the owner gave one, the design's own default where the
// owner answered nothing -- with the reason that answer rests on, a
// recommendation and the cost of both readings, and the block does not run past
// the numbers this work reserved.

// rirAnsweredRE and rirDefaultRE are the two ways a decision in this block can
// be settled. A third reading -- an entry that names neither -- is a decision
// the design took without the owner, which is the one thing the block exists to
// make impossible to write by accident.
var (
	rirAnsweredRE = regexp.MustCompile(`ANSWERED by the owner, 2026-11-04`)
	rirDefaultRE  = regexp.MustCompile(`default-by-owner: not objected, 2026-11-04`)
)

func TestDocsRepoImageRemovalDecisionsAreInRange(t *testing.T) {
	raw := readDoc(t, rirReconciled)
	matches := rirDecisionRE.FindAllStringSubmatchIndex(raw, -1)
	want := rirBlockLast - rirBlockFirst + 1
	if len(matches) != want {
		t.Fatalf("%s writes %d decisions and this work's block is D%d-D%d, %d of them: a decision number outside the block is a number taken without the owner giving it",
			rirReconciled, len(matches), rirBlockFirst, rirBlockLast, want)
	}
	seen := map[int]bool{}
	for i, m := range matches {
		n, err := strconv.Atoi(raw[m[2]:m[3]])
		if err != nil {
			t.Fatalf("%s writes a decision number that is not a number: %v", rirReconciled, err)
		}
		if n < rirBlockFirst || n > rirBlockLast {
			t.Errorf("%s writes D%d, outside this work's block D%d-D%d", rirReconciled, n, rirBlockFirst, rirBlockLast)
		}
		if seen[n] {
			t.Errorf("%s writes D%d twice: a decision id is unique, and a second entry is either a duplicate or a renumbering", rirReconciled, n)
		}
		seen[n] = true
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := flattenDoc(raw[m[0]:end])
		answered, byDefault := rirAnsweredRE.MatchString(block), rirDefaultRE.MatchString(block)
		if !answered && !byDefault {
			t.Errorf("%s' D%d carries no answer: every decision in this work's block carries the owner's answer of 2026-11-04, and an entry that names neither the answer nor the default is a decision the design took without the owner", rirReconciled, n)
			continue
		}
		if answered && byDefault {
			t.Errorf("%s' D%d is written as both answered and not objected: the two markers say different things about who settled it, and a reader cannot take both", rirReconciled, n)
		}
		if answered && !strings.Contains(block, "The reason") {
			t.Errorf("%s' D%d is answered and says no reason: an answer recorded without the reason it rests on is a ruling a later task cannot re-check against the tree", rirReconciled, n)
		}
		if byDefault && !strings.Contains(block, "the reason the recommendation itself gives below") {
			t.Errorf("%s' D%d is left to the default and says no reason: an unanswered decision still owes the reason its recommendation stands on, and it is the one a reader re-checks before building it", rirReconciled, n)
		}
		if !strings.Contains(block, "Recommendation:") {
			t.Errorf("%s' D%d is open and carries no recommendation: an open decision without one is a question, not a decision, and the owner has nothing to answer", rirReconciled, n)
		}
		if !strings.Contains(block, "Cost of the alternative:") {
			t.Errorf("%s' D%d carries no cost of the alternative: the brief asks every open decision for the price of the other reading, and a decision without one is a preference", rirReconciled, n)
		}
		if !strings.Contains(block, "Cost of the recommendation:") {
			t.Errorf("%s' D%d carries no cost of the recommendation: the brief asks for both sides, and the recommendation's own price is the half a reader deciding it needs", rirReconciled, n)
		}
	}
	for n := rirBlockFirst; n <= rirBlockLast; n++ {
		if !seen[n] {
			t.Errorf("%s does not write D%d: this work's block is D%d-D%d and every number in it is a decision a reader is owed", rirReconciled, n, rirBlockFirst, rirBlockLast)
		}
	}
	// The reserved tail is written as no decision at all. Prose may name the
	// range; a bullet for one of its numbers would be a decision this work took
	// without the owner giving it.
	for n := rirReservedFirst; n <= rirReservedLast; n++ {
		if shape := "- **D" + strconv.Itoa(n) + " ("; strings.Contains(raw, shape) {
			t.Errorf("%s writes %q: D%d-D%d are reserved and unused, and a written decision in that range is a number this work took without the owner giving it", rirReconciled, shape, rirReservedFirst, rirReservedLast)
		}
	}
}

// rirCitationRE finds a backtick-quoted repository path with a line number. The
// extension list is the configuration-management folder's, plus the console's
// `ts` and this folder's two: `sh` and `tsv`, because the one thing a migrated
// Dockerfile must not skip is `images/common/finalize-checkout.sh`, and the base
// image's tool list is a `.tsv`. A pattern written for `.go` alone would leave
// `images/derived/cloudbuild.yaml:178` and both of those unchecked while still
// passing its own census. The leading `.` is this folder's too: the repository's
// own config sits at the root, its base name is carried by a dozen fixtures, and
// a shortened name that matches two files is refused rather than guessed at, so
// the documents name it from the root as `./fugaro.yaml`.
var rirCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl|sh|tsv))(:[0-9]+)`")

// rirCensusRE is the independent count of how many path-looking tokens carry a
// line number anywhere in them, deliberately looser than the citation regexp
// above: it does not care where the path starts. It is the floor the citation
// check is measured against, so a regexp that has stopped matching the
// documents' shapes fails the test instead of passing it vacuously.
var rirCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl|sh|tsv)[^`\\n]*:[0-9]+`")

// TestDocsRepoImageRemovalCitationsResolve: every file and line these documents
// cite exists. The audit is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom -- which is what this folder is most exposed to, because its
// findings are about a path being *removed* and a reader's whole job is to check
// that the line is still there.
func TestDocsRepoImageRemovalCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range rirDocs {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens with
		// a line the document holds at all. The check below has to see at least
		// that many, so a regexp that stopped matching a shape the prose uses
		// fails here rather than quietly checking less.
		census := len(rirCensusRE.FindAllString(doc, -1))
		matches := rirCitationRE.FindAllStringSubmatch(doc, -1)
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

// TestDocsRepoImageRemovalRelativeCitationsResolve: every `:NNN` reference
// resolves against the nearest citation before it in its own paragraph, to a
// line that is in that file. The findings and the decisions lean on this shape
// -- a paragraph cites `internal/config/scope.go:106` and then `:108` -- and a
// reference whose anchor moved sends a reader to a line of the wrong file while
// the paragraph still reads as checked.
func TestDocsRepoImageRemovalRelativeCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range rirDocs {
		doc := readDoc(t, path)
		refs := 0
		for _, block := range strings.Split(doc, "\n\n") {
			for _, loc := range csrRelativeRE.FindAllStringIndex(block, -1) {
				anchor := ""
				for _, m := range rirCitationRE.FindAllStringSubmatchIndex(block[:loc[0]], -1) {
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

// TestDocsRepoImageRemovalCountsTheRemovedSurfaces: the four figures the audit's
// argument is made of are re-counted here rather than trusted. A removed key is
// removable exactly where the surface that makes it settable carries it, and
// there are four such surfaces: `fugaro.yaml`'s scope table, the settings
// registry that classifies the key, the settings schema that describes a write
// to it, and the fixtures that assert what it does. A key that moved between
// them -- a registry row without a scope row, a schema member without a registry
// row -- changes what the removal has to delete without changing a sentence
// anybody reads, so each count is taken from the tree and then asked of the
// documents that state it.
func TestDocsRepoImageRemovalCountsTheRemovedSurfaces(t *testing.T) {
	// The fixtures: every file under testdata/ carrying one of the three keys as
	// a YAML key. The documents state this figure as D1197's own list.
	fixtures := 0
	err := filepath.WalkDir("../../testdata", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			for _, key := range []string{"image:", "dockerfile:", "rebuild:"} {
				if strings.HasPrefix(trimmed, key) {
					fixtures++
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("testdata could not be walked, so the fixtures carrying a removed key cannot be counted: %v", err)
	}
	if fixtures != rirFixtures {
		t.Errorf("testdata holds %d files carrying a removed key and every document of this folder states %d: D1197's own list is the figure the sweep is planned from", fixtures, rirFixtures)
	}

	// The three surfaces that make a key settable, counted by the shapes each
	// one spells a row in: the scope table's `Key:` rows, the registry's map
	// entries and its `layerRow` calls, and the schema's member names.
	scopeRows, registryRows, schemaMembers := 0, 0, 0
	for _, c := range []struct {
		path  string
		count *int
		res   []*regexp.Regexp
	}{
		{"../../internal/config/scope.go", &scopeRows, []*regexp.Regexp{
			regexp.MustCompile(`\{Key: "workflows\.\*\.` + rirKeysRE.String() + `"`),
		}},
		{"../../internal/settings/registry.go", &registryRows, []*regexp.Regexp{
			regexp.MustCompile(`^\s*"workflows\.\*\.` + rirKeysRE.String() + `"`),
			regexp.MustCompile(`layerRow\("profiles\.\*\.` + rirKeysRE.String() + `"`),
		}},
		{"../../schemas/settings.schema.json", &schemaMembers, []*regexp.Regexp{
			regexp.MustCompile(`^\s*"(?:workflows|layer\.profiles|profiles)\.\*\.` + rirKeysRE.String() + `"`),
		}},
	} {
		body, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s could not be read, so the removed keys' rows there are uncounted: %v", c.path, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			for _, re := range c.res {
				if re.MatchString(line) {
					*c.count++
					break
				}
			}
		}
	}
	for _, got := range []struct {
		what string
		n    int
		want int
	}{
		{"internal/config/scope.go holds %d rows for the removed keys", scopeRows, rirScopeRows},
		{"internal/settings/registry.go holds %d rows for them", registryRows, rirRegistryRows},
		{"schemas/settings.schema.json holds %d members for them", schemaMembers, rirSchemaMembers},
	} {
		if got.n != got.want {
			t.Errorf(strings.Replace(got.what, "%d", strconv.Itoa(got.n), 1)+" and the documents state %d: the surfaces the removal has to leave are the ones it has to delete from", got.want)
		}
	}

	// Each document states the figures it is the one that argues from, so each
	// is asked for its own wording.
	for path, wants := range map[string][]string{
		rirReconciled: {
			strconv.Itoa(rirFixtures) + "** files",
			"the twenty-one rows",
			"their twenty-one members",
		},
		rirSpec: {
			strconv.Itoa(rirFixtures) + "** files",
			"The eleven scope rows",
			"the twenty-one at",
		},
		rirPlan: {
			strconv.Itoa(rirFixtures) + "** files",
			"its twenty-one",
			"the eleven rows",
		},
	} {
		doc := flattenDoc(readDoc(t, path))
		for _, want := range wants {
			if !strings.Contains(doc, want) {
				t.Errorf("%s lacks %q: the four figures are re-counted here and each document has to carry the number the count produced (%d fixtures, %d scope rows, %d registry rows, %d schema members)",
					path, want, fixtures, scopeRows, registryRows, schemaMembers)
			}
		}
	}
}

// TestDocsRigorModesReconciliationCountsTheRepoImageRemovalPin: the census row
// that counts these folders' pins is a running total of other folders' guards,
// and its own text says a folder writing one moves the row in its own commit.
// This folder writes one, so the row is checked twice: that it carries the
// number the tree holds, and that its amendment sentences name this folder's pin
// -- a row moved without the sentence beside it is a figure a reader cannot
// trace to the commit that moved it.
func TestDocsRigorModesReconciliationCountsTheRepoImageRemovalPin(t *testing.T) {
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
	if !strings.Contains(doc, "the repository-image-removal folder's\npin, moved the table's row from 93 to 94.") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit, and an amendment the row does not record is the merge that did not", path)
	}
}

// TestDocsRepoImageRemovalKeepsTheRetirementOwnerRun: the fourth settle point is
// a property no single task owns -- retirement stays owner-run, is never
// automatic, is never admitted by the managed gate, and the two protections that
// refuse an ordinary converge are not touched by the removal. The documents are
// asked for it, and the two protections are read out of Terraform, so a task
// that quietly widened the removal into a destroy fails here rather than at a
// person's converge.
func TestDocsRepoImageRemovalKeepsTheRetirementOwnerRun(t *testing.T) {
	for _, path := range []string{rirSpec, rirReconciled, rirPlan} {
		doc := flattenDoc(readDoc(t, path))
		if !strings.Contains(doc, "--retire") {
			t.Errorf("%s never names `--retire`: the retirement of the surviving cloud objects is the one part of this design that destroys something, and a document that removes the code without naming how the object goes is a document a reader cannot act on", path)
		}
		if !strings.Contains(doc, "D1026") && !strings.Contains(doc, "Typed step is never admitted") {
			t.Errorf("%s does not state that a Typed step is never admitted (D1026): the gate's own first rule is that it covers no deletes, and the retirement is the removal's one delete", path)
		}
	}
	spec := flattenDoc(readDoc(t, rirSpec))
	for _, want := range []string{
		"Retirement is owner-run: `fugaro update --repo <r> --retire`",
		"**It is never automatic.**",
		// The two protections, and the fact that they stay: the removal takes
		// the resources out of the configuration and not the guard off the
		// objects.
		"deletion_protection` stays",
		"`prevent_destroy = true`",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q: the retirement is owner-run, and an ordinary converge must still refuse to destroy these objects", rirSpec, want)
		}
	}
	for _, c := range []struct {
		path string
		want string
		why  string
	}{
		{
			"deploy/terraform/gcp/modules/workflow/job.tf",
			"deletion_protection = !var.allow_job_delete",
			"the workflow job carries the guard the check job carries today, so the flag's meaning outlives the job the removal takes out of the configuration; the check job's own line leaves with the job and is not pinned here, because pinning a line a planned task deletes is a pin that breaks on the task",
		},
		{
			"deploy/terraform/gcp/modules/repo/registry.tf",
			"prevent_destroy = true",
			"the repository's Artifact Registry repository holds the images a rollback needs, and the design leaves the guard on it",
		},
		{
			"internal/infra/retire.go",
			`"check job"`,
			"the retirement flow already knows the check job as a kind of its own, which is what makes the removal from the configuration a retirement the owner consents to rather than an ordinary delete",
		},
		{
			"internal/cli/init_retire.go",
			"askTyped",
			"the retirement is the one flow that deletes a cloud object, and its confirmation is a typed one --askTyped, which --yes, --json, a pipe, --non-interactive and a coding agent's session all fail",
		},
	} {
		body, err := os.ReadFile("../../" + c.path)
		if err != nil {
			t.Errorf("%s could not be read, so %s is unchecked: %v", c.path, c.why, err)
			continue
		}
		if !strings.Contains(string(body), c.want) {
			t.Errorf("%s no longer holds %q: %s", c.path, c.want, c.why)
		}
	}
}
