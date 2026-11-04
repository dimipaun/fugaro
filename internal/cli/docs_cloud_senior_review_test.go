package cli

// The cloud senior reviewer's docs package, pinned.
//
// docs/cloud-senior-review/ is a design-only folder: README.md (the index),
// spec.md (the design), reconciliation.md (every claim of the spec against the
// code on `main`, and the owner's decisions) and implementation-plan.md (the
// tasks CSR1-CSR7, with CSR4a and CSR4b where the old CSR4 split). It adds no
// code, and the whole folder is a draft pull request
// the owner has not approved, so nothing here pins a command or a flag.
//
// What it does pin is the half of these documents that is a claim about the
// tree, because that half rots silently. A reconciliation whose citations have
// moved is a reconciliation nobody can check: a line number that drifted is a
// reader sent to the wrong function, and nothing in the document looks wrong.
// This is not hypothetical for this folder -- the review of the round that
// answered D892 and D893 found two citations into
// internal/gitprov/github/apptoken.go that named lines 8 to 14 above the symbol
// they meant, and a third that pointed at a doc comment's continuation rather
// than at the type below it. All three are fixed, and the citation test below
// is what keeps the next one from landing.
//
//  * TestDocsCloudSeniorReviewIsWritten -- the four files exist and carry the
//    sentences a reader has to have before they trust anything in them: the
//    index's status, that an answer is not an approval, the owner's block of
//    decision numbers and the check number claimed, and the plan's own "nothing
//    here is approved", the gate outside this design named beside the
//    observation that used to be the second.
//  * TestDocsCloudSeniorReviewDecisionsAreAnswered -- D880-D894 are written and
//    answered, D895-D899 reserved and unused, and the four the later rounds
//    raised (D891, D892, D893, D894) each carry the option the owner took, in
//    the section's own marker. The index's numbers row says the same thing, so
//    the table and the section cannot drift apart -- which is the defect an
//    earlier round fixed in the table.
//  * TestDocsCloudSeniorReviewCitationsResolve -- every `path:line` citation in
//    all three documents resolves to a file that exists and a line that is in
//    it, measured against an independent census so a regexp that stops matching
//    the document's shapes fails rather than passing vacuously.
//  * TestDocsCloudSeniorReviewRelativeCitationsResolve -- the other half of the
//    same rule: every `:NNN` reference, which names no file, resolves against
//    the nearest citation before it in its own paragraph, to a line that is in
//    that file. A paragraph cites `internal/runner/reviewmode.go:444` and then
//    `:465`, and the second reference's whole meaning is the first.
//  * TestDocsCloudSeniorReviewTreeClaimsHold -- the load-bearing claims about
//    code that the design rests on are still true of the tree: the review mode's
//    invalid-verdict ending, the web tools DenyRules denies, the credentials
//    service's keyless token source and the permission set its mint serves
//    today. Each is a sentence in the design that a rename would falsify with no
//    other symptom.
//
// Mutation (verified, run and restored), one per way these documents can rot:
//
//	TestDocsCloudSeniorReviewCitationsResolve
//	  cite `internal/gitprov/github/apptoken.go:99999` in the plan -> fails,
//	  naming the file and its length: the path still resolves, which is the half
//	  a path-only check cannot see;
//	  shorten a citation to `credsvc/github.go:263` -> fails: the shortened name
//	  is carried by more than one file in the tree, and the check refuses to
//	  choose one for it. This was a real defect in the document, found by
//	  writing the check and fixed with it.
//	TestDocsCloudSeniorReviewRelativeCitationsResolve
//	  change spec.md's `:142` (the exported accessor beside
//	  `apptoken.go:128`) to `:99999` -> fails, naming the anchor it resolved
//	  against: a relative reference is checked against its paragraph's own file,
//	  not against whichever file the last full path in the document named.
//	TestDocsCloudSeniorReviewDecisionsAreAnswered
//	  put the index's numbers row back to "D892 and D893 open" -> fails: the row
//	  and the reconciliation's §4 are one claim, and a row that disagrees with
//	  the section is exactly the defect an earlier round fixed in the table;
//	  put D894's entry back to "- **D894 (OPEN, recommended option (a))." while
//	  the row and the section's own summary still call the block answered ->
//	  fails, naming the heading: an answer the owner gave cannot drift back into
//	  the open tail without the test saying so.
//
// The counts in the log lines are a reading of the documents at the commit that
// last edited this comment, not a constant: anyone re-running a mutation should
// take the live numbers from the log rather than from here.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	csrReadme     = "docs/cloud-senior-review/README.md"
	csrSpec       = "docs/cloud-senior-review/spec.md"
	csrReconciled = "docs/cloud-senior-review/reconciliation.md"
	csrPlan       = "docs/cloud-senior-review/implementation-plan.md"
)

// csrDocs is every file of the folder that cites code, in the index's own
// order. The index itself is not here: it summarises and links, and the claims
// about the tree live in the three documents below, so a citation census over
// it would only ever read zero.
var csrDocs = []string{csrSpec, csrReconciled, csrPlan}

// csrDecisionBlock is the range this work holds, as the documents write it.
// The upper end is the last written number and the reserved tail is named
// separately, because "D880-D894 written, D895-D899 reserved" is the claim --
// a block that quietly grew past D894 would be a decision number taken without
// the owner giving one. D894 is written and ANSWERED, with option (a) (the
// head's configuration, raised after D892 and D893 were answered and answered
// last); D880-D893 are answered too, and the owner's seven rulings of
// 2026-11-01 (O1-O7) amend the decisions they touch without taking a number.
const (
	csrBlockLow      = 880
	csrBlockHigh     = 894
	csrReservedFirst = 895
	csrReservedLast  = 899
)

// TestDocsCloudSeniorReviewIsWritten: the folder exists and the index states
// the facts a reader has to have first -- what state the design is in, that an
// answer is not an approval, which decision numbers this work holds, and which
// live-check number it claims.
func TestDocsCloudSeniorReviewIsWritten(t *testing.T) {
	for _, path := range []string{csrReadme, csrSpec, csrReconciled, csrPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, csrReadme))
	for _, want := range []string{
		"**Status: design only, nothing approved.**",
		// The claim two rounds had to restore: the table's own row said a
		// decision was still open while the header and the reconciliation
		// recorded it answered, so a reader of the table alone would have
		// believed D892, D893 or D894 unsettled. The round that answered D894
		// moved the whole block into the answered half, and the row says so.
		"D880–D894 written and answered",
		"**D895–D899 reserved and unused**",
		"**D893(c) is a precondition of the whole design, not a channel**",
		"**Check 125**",
		"the read-only token comes from the credentials service, by name",
		// The gate the design cannot pass from inside itself.
		"requires it to be enabled for the installation",
		"**No `fugaro update` can supply that half**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", csrReadme, want)
		}
	}
	// The index's status paragraph has to say both halves: the decisions are
	// answered, and answering one approves nothing. The second is the one a
	// later edit softens first, and it is what keeps "answered" from reading as
	// a permission to start R1.
	for _, want := range []string{
		"an answer is not an approval",
		"no task in [implementation-plan.md](implementation-plan.md) may start before the owner approves it",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s no longer says %q: answered decisions are not an approval, and this folder's whole status rests on the difference", csrReadme, want)
		}
	}
	// The plan carries the same status in its own words, plus the one gate
	// that is not a decision at all: E7 blocks CSR7, CSR2 and the coordinator's
	// merge path. U7 was the other one, and the probe it asked for has returned,
	// so the plan's row pins the answer rather than the gate -- a document that
	// drifted back to "gates CSR4b's launch" would be a gate this design has
	// already cleared, and CSR4b would be held behind work that is done. The
	// task ids are the CSR-prefixed ones the plan settled on.
	plan := flattenDoc(readDoc(t, csrPlan))
	for _, want := range []string{
		"**Nothing here is approved.**",
		"**The task ids are prefixed `CSR`, and that is deliberate.**",
		"**E7** (the credentials service enabled and admitting a project-scoped job) blocks CSR7, CSR2 and the coordinator's merge path",
		"**U7** (the owner-run live observation of the deployed fence) **is ANSWERED (2026-11-01)**",
		"**CSR4a carries D894(a)'s configuration posture and waits on nothing**",
		"**CSR4b carries D885's mutation machinery and U7's probe has returned, so it waits on CSR3 alone**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s no longer says %q", csrPlan, want)
		}
	}
}

// TestDocsCloudSeniorReviewDecisionsAreAnswered: this work's decision block is
// written, all of it is answered, and the block does not run past the numbers
// the owner gave it.
//
// The four decisions the later rounds raised are pinned individually, because
// each answer is a claim a reader acts on and the option letter is the whole of
// it: D891(a) is the mutation machinery CSR4b carries, D892(a) is the signature
// the coordinator verifies before a `ship` (over O3's frame), D893(c) is the
// credentials service -- not the per-launch secret this design recommended,
// which is exactly the answer a document could drift back to without any other
// symptom -- and D894(a) is the posture of the review stage's own session. All
// four are answered, so a later edit that moved one of them back into an open
// tail would be a row-and-section disagreement in reverse: the test pins the
// answered markers themselves, and the reserved range below them.
func TestDocsCloudSeniorReviewDecisionsAreAnswered(t *testing.T) {
	reconciled := flattenDoc(readDoc(t, csrReconciled))
	for _, want := range []string{
		"**D880–D894 are this work's block;",
		"D895–D899 are reserved and unused.**",
		"**All of them are answered, and the last of them is D894.**",
		"- **D891 (ANSWERED by the owner, option (a)).",
		"- **D892 (ANSWERED by the owner, option (a)).",
		"- **D893 (ANSWERED by the owner, option (c)).",
		// D894 was raised after those three and answered last, so a reader must
		// not find it waiting on the owner while the block around it is settled.
		"- **D894 (ANSWERED by the owner, option (a)).",
		// The rulings of 2026-11-01 amend the decisions above and take no
		// number of their own, which is what keeps the reserved tail true.
		"**The seven rulings of 2026-11-01 (O1–O7) take no decision number.**",
		// The precondition D893(c) turned the answer into, and what it blocks.
		"**D893(c) makes the credentials service a hard precondition of the whole design**",
		"**CSR7, CSR2 and the coordinator's `ship` → merge path stay blocked until it is met**",
	} {
		if !strings.Contains(reconciled, want) {
			t.Errorf("%s lacks %q: this work's block is D880–D894, all of it answered, and the answer to D893 is a precondition rather than a channel", csrReconciled, want)
		}
	}
	// No decision number past the block is written as one. A heading or a bullet
	// for D895-D899 would be a number taken without the owner giving it, which
	// is the failure the reserved tail exists to make visible.
	raw := readDoc(t, csrReconciled)
	for n := csrReservedFirst; n <= csrReservedLast; n++ {
		for _, shape := range []string{"**D" + strconv.Itoa(n) + ". ", "- **D" + strconv.Itoa(n) + " (ANSWERED"} {
			if strings.Contains(raw, shape) {
				t.Errorf("%s writes %q: D%d–D%d are reserved and unused, and a written decision in that range is a number this work took without the owner giving it", csrReconciled, shape, csrReservedFirst, csrReservedLast)
			}
		}
	}
	// The index's numbers row is a reading of the same block, and the two are
	// one claim: an earlier round found them disagreeing -- the row still called
	// D892 and D893 open while the section recorded them answered -- so the
	// row's own sentence is pinned beside the section's, option letter and all.
	readme := flattenDoc(readDoc(t, csrReadme))
	for _, want := range []string{
		"**this work's block: D880–D894 written",
		"**D892 with option (a)**",
		"**D893 with option (c)**",
		"**D894 with option (a)**",
		"with D895–D899 reserved and unused",
		// The rulings the block's answers rest on, named in the row beside it.
		"the owner's rulings **O1–O7** of 2026-11-01",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s' numbers row lacks %q: the row and the reconciliation's §4 are one claim about one block, and a row that disagrees with the section is how this round's defect read", csrReadme, want)
		}
	}
}

// csrCitationRE finds a backtick-quoted repository path with a line number, in
// the shapes these documents cite one in:
//
//	`internal/runner/reviewmode.go:444`      a path and one line
//	`internal/runner/runner.go:2675`         the same, in a finding
//	`apptoken.go:128`                        a shortened name, resolved below
//	`internal/gitprov/github/apptoken.go:381`
//
// The path character class carries `.` and `-` for the file names in this tree.
// A backticked token with no extension (a package name, a symbol on its own) is
// not a citation of a file and is left alone, which is why the extension is
// part of the pattern rather than a later filter.
var csrCitationRE = regexp.MustCompile("`([A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|tf|tmpl|yaml|yml))(:[0-9]+)`")

// csrCensusRE is the independent count of how many path-looking tokens carry a
// line number anywhere in them, deliberately looser than the citation regexp
// above: it does not care where the path starts or what the extension is. It is
// the floor the citation check is measured against, so a regexp that has stopped
// matching the documents' shapes fails the test instead of passing it vacuously
// -- the failure mode that lets a drifted citation go on reading as checked.
var csrCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|json|tf|tmpl|yaml|yml)[^`\\n]*:[0-9]+`")

// TestDocsCloudSeniorReviewCitationsResolve: every file and line these
// documents cite exists. This is the test that keeps a reconciliation honest --
// its findings are only worth as much as the code behind them, and a citation
// whose line moved turns a checked finding into an unchecked one with no other
// symptom.
//
// It is written because that is exactly what happened: the review of the round
// that answered D892 and D893 found `internal/gitprov/github/apptoken.go:128`
// naming a line 14 above the function it meant, and `apptoken.go:377` naming a
// doc comment's continuation rather than the type at :381. Both are corrected,
// and this is the check that would have caught them.
func TestDocsCloudSeniorReviewCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range csrDocs {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens with
		// a line the document holds at all. The check below has to see at least
		// that many, so a regexp that stopped matching a shape the prose uses
		// fails here rather than quietly checking less.
		census := len(csrCensusRE.FindAllString(doc, -1))
		matches := csrCitationRE.FindAllStringSubmatch(doc, -1)
		if len(matches) < census {
			t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape csrCitationRE no longer knows, so those citations are unchecked",
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
			// The trailing newline every file ends with is not a line of it:
			// counting it makes a citation to the phantom last line resolve and
			// still pass.
			lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
			if line < 1 || line > lines {
				t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
					path, m[1], m[2], file, lines, line)
			}
		}
		t.Logf("%s: %d citations carrying a line, against a census of %d path-looking tokens with one", path, len(matches), census)
	}
}

// csrRelativeRE finds a line reference that names no file: the `:465` in
// "(`internal/runner/reviewmode.go:444` and `:465`)". These documents use the
// shape heavily -- it is how a paragraph cites two lines of one file -- and it
// is the shape a citation check written for `path:line` cannot see at all.
var csrRelativeRE = regexp.MustCompile("`:[0-9]+`")

// TestDocsCloudSeniorReviewRelativeCitationsResolve: every `:NNN` reference
// resolves against the nearest full citation before it in its own paragraph, to
// a line that is in that file.
//
// This is the second half of the citation check and it exists because the first
// half cannot see this shape: a token that names no file is invisible to a
// regexp looking for an extension, and the documents lean on it -- a paragraph
// cites `internal/runner/reviewmode.go:444` and then `:465`, and the second
// reference's whole meaning is the paragraph's first. A reference whose anchor
// moved, or whose number drifted, sends a reader to a line of the wrong file
// while the paragraph still reads as checked.
func TestDocsCloudSeniorReviewRelativeCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range csrDocs {
		doc := readDoc(t, path)
		refs := 0
		for _, block := range strings.Split(doc, "\n\n") {
			for _, loc := range csrRelativeRE.FindAllStringIndex(block, -1) {
				// The anchor is the last citation that starts before the
				// reference: the paragraph's own nearest preceding file, which
				// is the rule the documents are written to.
				anchor := ""
				for _, m := range csrCitationRE.FindAllStringSubmatchIndex(block[:loc[0]], -1) {
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

// csrResolveCitation resolves a cited path against the tree: a path from the
// repository root, or a shortened name that exactly one file in the tree
// carries. A shortened name matching two files is refused rather than guessed
// at -- the documents shorten a name only when it is unambiguous, and that is a
// checked claim rather than a convention.
func csrResolveCitation(cited string, bare map[string][]string) (string, bool) {
	if strings.Contains(cited, "/") {
		if _, err := os.Stat("../../" + filepath.FromSlash(cited)); err == nil {
			return cited, true
		}
		return "", false
	}
	if matches := bare[cited]; len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// csrBareNames indexes every file in the tree by its base name, so that a
// shortened citation resolves to the one file it means. `.git` and
// node_modules are skipped: neither is this repository's own source, and a name
// matching a vendored copy would be an ambiguity that is not one.
func csrBareNames(t *testing.T) map[string][]string {
	t.Helper()
	index := map[string][]string{}
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel("../..", p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		index[filepath.Base(rel)] = append(index[filepath.Base(rel)], rel)
		return nil
	})
	if err != nil {
		t.Fatalf("the repository tree could not be walked, so a shortened citation cannot be resolved: %v", err)
	}
	for name := range index {
		sort.Strings(index[name])
	}
	return index
}

// TestDocsCloudSeniorReviewTreeClaimsHold: the sentences in these documents
// that are claims about code, held to the code. Each one is a fact the design
// leans on, and each fails silently if the code is renamed -- the document goes
// on reading as checked while the thing it names is gone.
func TestDocsCloudSeniorReviewTreeClaimsHold(t *testing.T) {
	for _, c := range []struct {
		file string
		want string
		why  string
	}{
		{
			"internal/runner/reviewmode.go",
			"func (r *run) endReviewInvalidVerdict(ctx context.Context, reason string) {",
			"spec §3's rule that an unreadable review artifact ends the run invalid rather than fabricating a finding is this function",
		},
		{
			"internal/agent/settings.go",
			`var deniedWebTools = []string{"WebSearch", "WebFetch"}`,
			"spec §7's claim that `denyWeb` denies Claude Code's web TOOLS and not the shell rests on this list",
		},
		{
			"internal/agent/settings.go",
			"func DenyRules(denyWeb, review bool) []string {",
			"CSR4b's Bash denial is a change to this function's contract, and the design names it",
		},
		{
			"internal/gitprov/github/apptoken.go",
			"type ServiceTokens struct {",
			"D893(c)'s keyless fetch path is this type; the whole answer rests on it existing",
		},
		{
			"internal/credsvc/github.go",
			"github.RunTokenPermissions()",
			"the reconciliation's claim that the service's mint serves the RUN's permission set today -- which is why R1's read-only set is new service work -- is this call",
		},
	} {
		if !strings.Contains(readDoc(t, c.file), c.want) {
			t.Errorf("%s no longer carries %q: %s", c.file, c.want, c.why)
		}
	}
}
