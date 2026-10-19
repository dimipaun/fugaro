package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The two documents this work added, and the index that names them. There is
// no test in this package that walks docs/ or pins the set of docs folders
// (each docs package is pinned by its own file, like this one), so the folder
// and its file set are pinned here.
const (
	inJobReviewREADME  = "docs/in-job-review/README.md"
	inJobReviewSpec    = "docs/in-job-review/spec.md"
	inJobReviewRecon   = "docs/in-job-review/reconciliation.md"
	inJobReviewStatus  = "**Status: reconciliation only; no code is implemented; Q1, Q4 and Q9 answered 2026-10-19; the plan waits for the rest.**"
	inJobReviewSubject = "Fugaro: In-Job Review Spec"
	// gcpLiveChecklist is where an owner-run live check claims its number. This
	// work claims none, so the file must name neither this folder nor the spec.
	gcpLiveChecklist = "docs/gcp-live-checklist.md"
)

// TestDocsInJobReviewIsWritten: the folder exists with its index and both
// documents, the index carries the brief's status line word for word and
// names the other two files, and the reconciliation carries the structure the
// brief asked for — a headline table, findings classified Wrong / Exists /
// Gap / Rule / Open and numbered from #1 without a gap, a section "Open for
// the owner", and an UNVERIFIED table. Its red state is this test: the folder
// did not exist.
func TestDocsInJobReviewIsWritten(t *testing.T) {
	readme := readDoc(t, inJobReviewREADME)
	spec := readDoc(t, inJobReviewSpec)
	recon := readDoc(t, inJobReviewRecon)

	for _, want := range []string{
		inJobReviewStatus,
		// The owner and the dates, as the brief states them.
		"**Owner:** Dimi Paun",
		"2026-10-18",
		"2026-10-19",
		// The index names both documents it indexes.
		"[spec.md](spec.md)",
		"[reconciliation.md](reconciliation.md)",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", inJobReviewREADME, want)
		}
	}

	if !strings.Contains(spec, inJobReviewSubject) {
		t.Errorf("%s is not the owner's in-job review spec", inJobReviewSpec)
	}

	for _, want := range []string{
		// The headline table and its columns.
		"| Spec concept | What exists, and under what name | New? |",
		// Every section the brief asked for.
		"## 2. Findings",
		"## 4. Open for the owner",
		"## 5. Owner's answers (2026-10-19)",
		"## 7. UNVERIFIED",
		// The revision this was read at, so a later run can tell when it ages.
		"Checked against:** `origin/main` at `82ba600",
		"2026-10-19",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", inJobReviewRecon, want)
		}
	}

	// The findings: numbered from #1 with no gap, and each classified with one
	// of the five words. A skipped number or a word outside the set fails. The
	// class word opens the parenthesis and may be followed by more text (some
	// findings say why they are classified as they are).
	label := regexp.MustCompile(`(?m)^\*\*#([0-9]+) \(([A-Za-z]+)`)
	known := map[string]bool{"Wrong": true, "Exists": true, "Gap": true, "Rule": true, "Open": true}
	classes := map[string]int{}
	nums := []int{}
	for _, m := range label.FindAllStringSubmatch(recon, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("finding label %q does not carry a number", m[0])
		}
		nums = append(nums, n)
		if !known[m[2]] {
			t.Errorf("finding #%d is classified %q: every finding carries one of Wrong, Exists, Gap, Rule, Open", n, m[2])
			continue
		}
		classes[m[2]]++
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no finding of the form **#N (Class).**", inJobReviewRecon)
	}
	for i, n := range nums {
		if n != i+1 {
			t.Fatalf("finding #%d is out of order: findings are %v (want #1..#%d with no gap)", n, nums, len(nums))
		}
	}
	for _, c := range []string{"Wrong", "Exists", "Gap", "Rule", "Open"} {
		if classes[c] == 0 {
			t.Errorf("%s classifies no finding as %s: every finding carries one of Wrong, Exists, Gap, Rule, Open", inJobReviewRecon, c)
		}
	}
	// The questions: numbered from Q1 with no gap, so "Open for the owner" is a
	// list a reader can count, and the index's own "Q1–Q17" is true.
	question := regexp.MustCompile(`(?m)^\*\*Q([0-9]+)\.`)
	qnums := []int{}
	for _, m := range question.FindAllStringSubmatch(recon, -1) {
		n, _ := strconv.Atoi(m[1])
		qnums = append(qnums, n)
	}
	if len(qnums) == 0 {
		t.Fatalf("%s states no question of the form **QN.**", inJobReviewRecon)
	}
	for i, n := range qnums {
		if n != i+1 {
			t.Fatalf("question Q%d is out of order: questions are %v (want Q1..Q%d with no gap)", n, qnums, len(qnums))
		}
	}
	if !strings.Contains(readme, "**Q1–Q"+strconv.Itoa(len(qnums))+"**") {
		t.Errorf("%s does not state the range Q1–Q%d the reconciliation actually carries (%d questions)",
			inJobReviewREADME, len(qnums), len(qnums))
	}
	if !strings.Contains(readme, "**#1–#"+strconv.Itoa(len(nums))+"**") {
		t.Errorf("%s does not state the range #1–#%d the reconciliation actually carries (%d findings)",
			inJobReviewREADME, len(nums), len(nums))
	}
}

// TestDocsInJobReviewSpecIsVerbatim: spec.md is the owner's text of 2026-10-18,
// unchanged — the brief's rule is that only obvious typos may be fixed, and
// none needed fixing, so the title, the status line and every section heading
// are pinned here. A later edit that "improves" the spec, drops a section or
// reorders one fails, which is the point: the spec is the owner's, and the
// document that may change is the reconciliation.
func TestDocsInJobReviewSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, inJobReviewSpec)
	for _, want := range []string{
		"# " + inJobReviewSubject,
		"Status: Draft · Oct 18, 2026 · Owner: Dimi Paun",
		"## 1. Problem",
		"## 2. Goal",
		"## 3. Job flow",
		"## 4. Review plans",
		"## 5. Running reviews",
		"## 6. Findings and verdict",
		"## 7. Review-only jobs",
		"## 8. Rebase and stale base",
		"## 9. Timeouts and time budget",
		"## 10. Calibration",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q: the spec is the owner's text, unchanged", inJobReviewSpec, want)
		}
	}
	// The brief's own marker must not have been copied in with the text.
	for _, absent := range []string{"=== SPEC TEXT ===", "=== END OF SPEC TEXT ==="} {
		if strings.Contains(spec, absent) {
			t.Errorf("%s carries the brief's own marker %q: only the spec text between the markers belongs here", inJobReviewSpec, absent)
		}
	}
	// The reconciliation is the document that states the spec was not changed.
	recon := readDoc(t, inJobReviewRecon)
	if !strings.Contains(strings.ToLower(recon), "no typo needed fixing") &&
		!strings.Contains(strings.ToLower(recon), "nothing was changed") &&
		!strings.Contains(strings.ToLower(recon), "verbatim") {
		t.Errorf("%s never states that %s is the owner's text unchanged", inJobReviewRecon, inJobReviewSpec)
	}
}

// TestDocsInJobReviewStatesTheBriefCorrections: two claims the brief this work
// was written from makes are wrong about this tree, and both corrections are
// load-bearing — the first decides what a "Rule" finding can cite, the second
// decides whether the spec's section 8 is an assumption or a question for the
// owner. Each must be stated in the reconciliation and summarised in the
// index, so a later reader cannot act on the brief's version by accident.
func TestDocsInJobReviewStatesTheBriefCorrections(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	readme := readDoc(t, inJobReviewREADME)
	if _, err := os.Stat("../../CLAUDE.md"); err == nil {
		t.Errorf("CLAUDE.md now exists: %s's correction that this repository has none is stale, and the Rule class must say what it cites",
			inJobReviewRecon)
	}
	if _, err := os.Stat("../../.claude/rules"); err == nil {
		t.Errorf(".claude/rules now exists: %s's correction that this repository has none is stale", inJobReviewRecon)
	}
	// What each document must carry, as the substance of each correction and
	// not merely a mention of the symbol: a document that says the runner never
	// force-pushes while quoting --force-with-lease in an example must fail.
	for _, doc := range []struct {
		path string
		text string
		want []string
	}{
		{inJobReviewRecon, recon, []string{
			// Correction 1: no CLAUDE.md, no .claude/rules here.
			"CLAUDE.md",
			".claude/rules",
			// Correction 2: the runner does force-push its own run branch, with
			// a lease; checkpoint pushes are the fast-forward-only ones; the
			// base branch is never pushed at all.
			"does** force-push",
			"force-with-lease",
			"fast-forward only",
			"base branch is never pushed",
			"PushFastForward",
		}},
		{inJobReviewREADME, readme, []string{
			"CLAUDE.md",
			".claude/rules",
			"force-with-lease",
			"fast-forward only",
			"base branch is never pushed",
			"PushFastForward",
		}},
	} {
		for _, want := range doc.want {
			if !strings.Contains(doc.text, want) {
				t.Errorf("%s lacks %q: the brief's wrong version of this fact must not be left standing alone", doc.path, want)
			}
		}
	}
}

// TestDocsInJobReviewAllocatesNoDecisionsOrChecks: the index's own "numbers
// this work holds" table says no D-number is allocated and no check number is
// claimed, because every finding that needs the owner is a Q. This asserts the
// claim rather than trusting it: no decision-row label (**D123**) appears in
// either document, the table says so in words, and docs/gcp-live-checklist.md
// — where an owner-run check claims its number — carries no section for this
// work.
func TestDocsInJobReviewAllocatesNoDecisionsOrChecks(t *testing.T) {
	decisionRow := regexp.MustCompile(`(?m)\*\*D[0-9]+\*\*`)
	for _, path := range []string{inJobReviewRecon, inJobReviewREADME} {
		if m := decisionRow.FindAllString(readDoc(t, path), -1); len(m) > 0 {
			t.Errorf("%s carries decision labels %v: this work allocates no D-number, so every finding that needs the owner is a Q",
				path, m)
		}
	}
	readme := readDoc(t, inJobReviewREADME)
	for _, want := range []string{
		"| **D-numbers** | **none allocated.**",
		"| **Check numbers** | **none claimed.**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q: the numbers this work holds must say it holds none", inJobReviewREADME, want)
		}
	}
	// The "none claimed" line is a claim about a file this package can read, so
	// it is checked and not just stated: a check number is claimed by writing a
	// section in the live checklist, so neither this folder nor the spec's title
	// may appear there. A work that claimed one fails here.
	checklist := readDoc(t, gcpLiveChecklist)
	for _, absent := range []string{"in-job-review", inJobReviewSubject} {
		if strings.Contains(checklist, absent) {
			t.Errorf("%s mentions %q: this work claims no check number, so it must have written no section there — a check number is claimed by a section in that file",
				gcpLiveChecklist, absent)
		}
	}
}

// TestDocsInJobReviewRecordsTheOwnersAnswers: the owner answered Q1, Q4 and Q9
// on 2026-10-19, and the documents have to say so where a reader looks — in the
// index's status line, in a section of its own, and on the question itself.
// The answered set is pinned exactly, and so is the set of questions an answer
// constrains: a marker removed, a marker moved to another question, a fourth
// question quietly marked answered, or a question losing the line that says how
// an answer constrains it all fail. Q9 is the answer with the largest
// consequence — it gates in-job Opus review on the credentials service — so its
// substance is asserted, not just its presence.
func TestDocsInJobReviewRecordsTheOwnersAnswers(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	blocks := questionBlocks(t, recon)

	// The answered set, exactly: the three the owner answered on 2026-10-19 and
	// no others, each marked in place with the date.
	answered := []int{}
	constrained := []int{}
	for n, b := range blocks {
		if strings.Contains(b, "**Answered 2026-10-19") {
			answered = append(answered, n)
		}
		if strings.Contains(b, "**Constrained by") {
			constrained = append(constrained, n)
		}
	}
	sort.Ints(answered)
	sort.Ints(constrained)
	if got, want := numbers(answered), "[1 4 9]"; got != want {
		t.Errorf("the questions marked answered 2026-10-19 are %s, want %s: the owner answered Q1, Q4 and Q9, each marked in place in %s",
			got, want, inJobReviewRecon)
	}
	// Every question an answer constrains says how, so a reader cannot act on the
	// superseded reading by accident.
	if got, want := numbers(constrained), "[2 3 5 6 7 8 10 13 14 15 16]"; got != want {
		t.Errorf("the questions carrying a \"Constrained by\" line are %s, want %s: each question an answered one constrains must say how in %s",
			got, want, inJobReviewRecon)
	}

	// Q9's answer, as substance: the gate, what it withholds, and what the first
	// slice can still deliver.
	for _, want := range []string{
		"## 5. Owner's answers (2026-10-19)",
		"In-job Opus review is gated on the credentials service",
		"cannot put an Opus reviewer in a cloud job",
		"serial multi-review runner loop",
		"`watch` rendering fix",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: the owner's Q9 answer, its gate and what the first slice still delivers are what the next run plans from",
				inJobReviewRecon, want)
		}
	}
}

// questionBlocks splits §4 into each question's own block: from its **QN.
// heading to the next question's heading. Anything a question carries — its
// answer marker, its "Constrained by" line — is found inside its own block, so
// a marker that lands in the neighbouring question is seen as such.
func questionBlocks(t *testing.T, recon string) map[int]string {
	t.Helper()
	blocks := map[int]string{}
	head := regexp.MustCompile(`(?m)^\*\*Q([0-9]+)\.`)
	locs := head.FindAllStringSubmatchIndex(recon, -1)
	for i, m := range locs {
		n, err := strconv.Atoi(recon[m[2]:m[3]])
		if err != nil {
			t.Fatalf("question label %q does not carry a number", recon[m[0]:m[1]])
		}
		end := len(recon)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		blocks[n] = recon[m[0]:end]
	}
	if len(blocks) == 0 {
		t.Fatalf("%s states no question of the form **QN.", inJobReviewRecon)
	}
	return blocks
}

// constrainedLine returns a question block's own "Constrained by" line, empty
// when it carries none. It is a line and not the whole block because a block
// mentions other questions in its prose; what says which answer constrains it
// is the line that says so.
func constrainedLine(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "**Constrained by") {
			return line
		}
	}
	return ""
}

// countWord renders a small count the way the documents spell it in prose. The
// prose counts are asserted against the lists they describe, so the pin has to
// compare words with a count and not one literal with another.
func countWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six",
		"seven", "eight", "nine", "ten", "eleven", "twelve"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// bulletWith returns the body of the bullet starting with prefix: from that
// line to the next bullet or blank line. A count stated inside a bullet belongs
// to that bullet's own list, so it is read from the bullet and not the section.
func bulletWith(doc, prefix string) string {
	i := strings.Index(doc, prefix)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(prefix):]
	end := len(rest)
	for _, cut := range []string{"\n- ", "\n\n"} {
		if j := strings.Index(rest, cut); j >= 0 && j < end {
			end = j
		}
	}
	return rest[:end]
}

// TestDocsInJobReviewProseCountsMatchTheBlocks: the documents state their own
// numbers in prose — how many questions Q9 constrains, how many resolutions
// there are, how many rows the UNVERIFIED table has — and a count in prose
// drifts silently when the list it describes gains or loses an item. That is
// how §5's list came to name eight of the nine questions Q9 constrains while
// three sentences also said eight. Each count is computed from the text it
// describes and compared with the word the prose uses, so a list and its count
// can no longer disagree.
func TestDocsInJobReviewProseCountsMatchTheBlocks(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	readme := readDoc(t, inJobReviewREADME)

	// The questions Q9 constrains, from the questions' own lines — the answer
	// marked in place is the authority, not the summary of it.
	q9 := []int{}
	for n, b := range questionBlocks(t, recon) {
		if strings.Contains(constrainedLine(b), "Q9") {
			q9 = append(q9, n)
		}
	}
	sort.Ints(q9)

	// §5's own list has to name exactly those, no more and no fewer.
	named := []int{}
	for _, m := range regexp.MustCompile(`\*\*Q([0-9]+)\*\*`).FindAllStringSubmatch(bulletWith(recon, "- **Which questions it changes.**"), -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("§5's list names %q, which is not a number", m[1])
		}
		named = append(named, n)
	}
	sort.Ints(named)
	if got, want := numbers(named), numbers(q9); got != want {
		t.Errorf("§5 lists %s as the questions Q9 changes, but %s carry a line naming Q9: the list and the questions it describes have to agree",
			got, want)
	}

	// And the two sentences that count them, in words.
	word := countWord(len(q9))
	if !strings.Contains(readme, "the "+word+" questions Q9 constrains") {
		t.Errorf("%s does not say Q9 constrains %s questions: %d carry a line naming Q9",
			inJobReviewREADME, word, len(q9))
	}
	if !strings.Contains(recon, "Q9 constrains "+word+" of them") {
		t.Errorf("%s does not say Q9 constrains %s of the open questions: %d carry a line naming Q9",
			inJobReviewRecon, word, len(q9))
	}

	// The other two counts the index states, each against its own range there.
	res := len(regexp.MustCompile(`(?m)^\*\*R[0-9]+\.`).FindAllString(recon, -1))
	if !strings.Contains(readme, countWord(res)+" **resolutions**") {
		t.Errorf("%s does not call the %d resolutions by that count", inJobReviewREADME, res)
	}
	if !strings.Contains(readme, "R1–R"+strconv.Itoa(res)+")") {
		t.Errorf("%s does not state the resolutions' range as R1–R%d", inJobReviewREADME, res)
	}
	u := len(regexp.MustCompile(`(?m)^\| U[0-9]+ \|`).FindAllString(recon, -1))
	if !strings.Contains(readme, "U1–U"+strconv.Itoa(u)+")") {
		t.Errorf("%s does not state the UNVERIFIED table's range as U1–U%d", inJobReviewREADME, u)
	}
}

// TestDocsInJobReviewLinksResolve: every relative link in the two documents
// resolves to a file that is in the tree. The citations test checks that a
// citation is *mentioned*, and a link one directory short is mentioned and dead
// at the same time — [SECURITY.md](../SECURITY.md) from docs/in-job-review/
// resolves to docs/SECURITY.md, which does not exist — so the target is
// resolved here, against the document that carries the link.
func TestDocsInJobReviewLinksResolve(t *testing.T) {
	link := regexp.MustCompile(`\]\(([^()]+)\)`)
	for _, doc := range []string{inJobReviewREADME, inJobReviewRecon} {
		src := readDoc(t, doc)
		dir := filepath.Dir(doc)
		rel := 0
		for _, m := range link.FindAllStringSubmatch(src, -1) {
			target := m[1]
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			// External links are not this test's business: only a target
			// relative to the document can be one directory short.
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			rel++
			if _, err := os.Stat(filepath.Join("../../", dir, target)); err != nil {
				t.Errorf("%s links %q, which resolves to %s from that document and is not in the tree: %v",
					doc, m[1], filepath.Join(dir, target), err)
			}
		}
		if rel == 0 {
			t.Errorf("%s carries no relative link, so this pin checks nothing", doc)
		}
	}
}

// numbers renders a sorted question-number list for a comparison message.
func numbers(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, strconv.Itoa(n))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestDocsInJobReviewStatesTheAuthenticityEvidence: finding #40's evidence is
// what makes it a Gap rather than a mistake — the record carries no signature,
// so a verdict cannot be a gate on its own. The first version of that sentence
// read "grep for hmac in internal/ returns nothing, and attest appears only in
// comments that deny it exists", which is false at the revision the
// reconciliation was read at: the GCP test fakes MAC their own JWTs, and
// "attest" is used of the record binding artifact bytes and of a buildx index's
// attestation entry. A reader cannot check a claim the tree contradicts, so
// this pins the corrected substance — where the tree's MACs actually are, and
// that the record's schema has no signature field — and fails if the retracted
// sentence comes back in either document.
func TestDocsInJobReviewStatesTheAuthenticityEvidence(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	for _, want := range []string{
		// The corrected evidence: the tree's MACs are the fakes', and the
		// record's schema carries no signature field.
		"internal/gcpfake/iamcredentials.go",
		"internal/gcpfake/identitytoolkit.go",
		"schemas/result.schema.json` has no field that is a signature",
		// Which search found what: the `pathMacro` substring is only a match
		// case-insensitively, so the sentence has to name the command a reader
		// would run rather than leave the case to be guessed.
		"the case-sensitive `git grep hmac -- internal/`",
		"`git grep -i hmac -- internal/`",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: finding #40 rests on the record carrying no signature, and the evidence has to say where the tree's MACs are instead",
				inJobReviewRecon, want)
		}
	}
	for _, path := range []string{inJobReviewRecon, inJobReviewREADME} {
		if strings.Contains(readDoc(t, path), "returns nothing") {
			t.Errorf("%s carries the retracted claim that a grep for hmac in internal/ returns nothing: it is false at the revision the reconciliation was read at (internal/gcpfake MACs its own JWTs), and this document's contract is that every claim was located with git grep against that revision",
				path)
		}
	}
}

// TestDocsInJobReviewCitationsWereCorrected: the first revision carried five
// citations a reader could not check — a Go symbol that does not exist
// (runStageError; the real ones are the free function StageError and
// run.stageReason), a design-decision number that says something else
// (design/recipes.md's decision 5 is the agent.* knobs, not the reserved keys or
// the outcome rule, which are in §4), a claim that three CLI flags appear
// nowhere in the tree (they do, in voice.md's D54, as an option that document
// refuses), and a quote of git-providers.md whose fragments were in a different
// order from the source. Each corrected form is pinned here and each retracted
// form is pinned out, so the same five mistakes cannot come back.
func TestDocsInJobReviewCitationsWereCorrected(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	for _, absent := range []string{
		"runStageError",
		"decision 5",
		"anywhere in the tree",
	} {
		if strings.Contains(recon, absent) {
			t.Errorf("%s carries %q, a form this work retracted: it is wrong against the tree at the revision the document was read at, so it must not come back",
				inJobReviewRecon, absent)
		}
	}
	for _, want := range []string{
		// The symbols that do exist, cited by name.
		"`StageError` (`internal/runner/budget.go`)",
		"`run.stageReason` (`internal/runner/runner.go`)",
		"stage <stage> timed out after",
		// The right place in the design for the reserved keys and the rule.
		"§4's reserved-and-refused list",
		"Outcome rules are not configurable",
		// The three flags, where they really are.
		"only** in [voice.md](../design/voice.md) D54",
		// The quote, in the source's own order.
		"It overwrites the remote branch only when the branch is absent or its tip is one of the run's own commits",
		"The base branch is never pushed.",
		// And the catalog's own words, capitalised as it writes them.
		"High-stakes or subtle work (concurrency, security, data migrations)",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: this is the corrected form of one of the five citations the first revision got wrong", inJobReviewRecon, want)
		}
	}
}

// TestDocsInJobReviewCitationsExist: every load-bearing citation in the two
// documents is a path a later implementer has to find — the symbols the
// findings rest on, the file that carries each rule the spec runs into, and
// the documents the reconciliation is read against. A substring assertion
// cannot see one of them move, so each is stat'ed here as well as required in
// the text.
//
// The list is the documents' own citations, so one that leaves the text must
// leave this test too (the test says so when it fails). Every path was read on
// `main` on 2026-10-19 and is in this tree.
func TestDocsInJobReviewCitationsExist(t *testing.T) {
	both := readDoc(t, inJobReviewREADME) + readDoc(t, inJobReviewRecon)
	for _, cited := range []string{
		// The loop the spec's section 3 describes, as it exists.
		"internal/recipe",
		"internal/runner/plan.go",
		"internal/runner/runner.go",
		"internal/runner/prompts.go",
		"internal/runner/outcome.go",
		"internal/runner/budget.go",
		"internal/runner/lockcache.go",
		// The verdict contract the spec's section 6 changes.
		"internal/runner/verdict.go",
		"internal/runstore/runstore.go",
		"internal/runstore/cost.go",
		"internal/runner/cost.go",
		"internal/runner/policy.go",
		"schemas/result.schema.json",
		// The reconciliation also cites the console's own json-contract.md and
		// its shared schema module (finding #26: the console reads the CLI's
		// JSON, never result.json, and keys its documents by `shape`). Those two
		// are deliberately NOT stat'ed here, and the path is not even spelled
		// out in this comment: this is a .go file of the module, and
		// TestConsoleIsOutsideTheGoModule's consoleBoundaryAllowList is a
		// one-entry exception (the console contract's own docs test) that a
		// second entry may only widen by argument. That boundary scans a Go
		// file's text, comments included, but not the markdown of docs/, so the
		// pin stops at the module's edge and the claim is carried by the prose.
		"internal/watch/plain.go",
		"internal/watch/render.go",
		"internal/watch/finished.go",
		"internal/watch/filter.go",
		// Where a review plan would have to ride, and what it costs to add.
		"internal/task",
		"internal/cli/recipes_skew.go",
		"internal/config/scope.go",
		"internal/config/docs_scope_test.go",
		"internal/config/defaults.go",
		"internal/config/validate.go",
		"internal/projectpolicy",
		// Models, credentials and the spend they carry.
		"internal/config/providers.go",
		"internal/config/routing.go",
		"internal/runner/gateway.go",
		"internal/runner/providerkey.go",
		"internal/gateway/route.go",
		"internal/pricing/table.go",
		"internal/budget/lease.go",
		"internal/budget/model.go",
		"internal/cli/budget_preview.go",
		"internal/infra/spec.go",
		"internal/localcfg",
		"fugaro.yaml",
		// Rebasing, pushing and the follow-up the spec's section 8 changes.
		"internal/gitops/gitops.go",
		"internal/gitops/checkpoint.go",
		"internal/gitops/rejected.go",
		"internal/runner/followup.go",
		"internal/cli/followup.go",
		"internal/followup",
		"deploy/terraform/gcp/modules/workflow/job.tf",
		"deploy/terraform/gcp/modules/project/job.tf",
		// What a reviewer is, and what it is not.
		"internal/agent/agent.go",
		"internal/agent/settings.go",
		"internal/agent/redact.go",
		"internal/runner/reviewmode.go",
		// What a verdict is not (finding #40): the record carries no signature,
		// and the tree's only MACs are the GCP test fakes' own JWTs, so the
		// evidence for "a verdict is advice, not a gate" is where the MACs are
		// not.
		"internal/gcpfake/iamcredentials.go",
		"internal/gcpfake/identitytoolkit.go",
		"internal/mirror/fake_test.go",
		"internal/runner/artifact.go",
		"internal/runstore/artifact.go",
		"internal/cli/artifacts.go",
		"internal/budget/rules",
		"internal/runview",
		"internal/backend/gcp/limits.go",
		"internal/backend/gcp/run.go",
		"internal/blobx",
		// Reading it back: the CLI surfaces the spec's verdict would reach.
		"internal/cli/run.go",
		"internal/cli/diagnose.go",
		"internal/cli/root.go",
		"internal/cli/exec.go",
		// The documents it is read against.
		"SECURITY.md",
		"docs/recipes.md",
		"docs/dogfooding.md",
		"docs/git-providers.md",
		"docs/multi-model.md",
		"docs/gcp-live-checklist.md",
		"docs/design/recipes.md",
		// Where the three tool-permission flags really are (finding #36), and the
		// frontmatter-key lint that refuses the dash-less spelling.
		"docs/design/voice.md",
		"plugin/skills_lint_test.go",
		"docs/design/generic-tool.md",
		"docs/design/mixed-credentials.md",
		"docs/design/project-centric.md",
		"docs/design/artifact-runs.md",
		"docs/design/named-oauth-tokens.md",
		"docs/design/budget-token-channel.md",
		"docs/plans/2026-10-14-oauth-reviewer-stage.md",
		"docs/credentials-service/README.md",
		"docs/credentials-service/reconciliation.md",
		"docs/credentials-service/implementation-plan.md",
	} {
		// The documents link their neighbours relatively (../recipes.md,
		// ../../SECURITY.md), so a citation counts in either form.
		rel, err := filepath.Rel("docs/in-job-review", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(both, cited) && !strings.Contains(both, rel) {
			t.Errorf("docs/in-job-review no longer cites %s: this list is the documents' own citations, so one that has left the text must leave this test", cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("docs/in-job-review cites %s, which is not in the tree: %v", cited, err)
		}
	}
}
