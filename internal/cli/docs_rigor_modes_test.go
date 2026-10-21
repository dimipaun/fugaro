package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The four documents of docs/rigor-modes/ — the owner's spec, the
// reconciliation, the plan and the index — and the guard that owns them.
//
// There is no registry test over docs/ in this package: nothing pins the set of
// docs folders and nothing pins a folder's documents on its behalf, so a folder
// that wants its documents pinned writes its own file — this one, the same
// convention docs_windows_cli_test.go states for docs/windows-cli/ and
// docs_in_job_review_test.go for docs/in-job-review/.
//
// The folder is a plan whose owner approval gate is passed: the owner answered
// every decision on 2026-10-20 — D1–D25 as answers to questions asked
// independently, and D26–D32 in the backlog ruling he gave the same day, whose
// one unnamed value is D29's designated repository. Nothing in it is implemented
// yet, and every decision in it carries an answer rather than a recommendation
// alone. So what a guard can usefully hold is the plan's own shape
// and the plan's own claims about itself — the status line, the gate, the
// numbered findings, decisions, items and unverified rows with no gap, an answer
// on every decision, the census stating the command beside every count, every
// task carrying the seven things a task must state, and the folder's rule that
// the owner's spec is answered in the reconciliation rather than edited. Every
// one of those is a sentence a later edit can quietly break, which is what makes
// them worth pinning rather than asserting in prose.
const (
	rigorModesReadme = "docs/rigor-modes/README.md"
	rigorModesSpec   = "docs/rigor-modes/spec.md"
	rigorModesRecon  = "docs/rigor-modes/reconciliation.md"
	rigorModesPlan   = "docs/rigor-modes/implementation-plan.md"

	// rigorModesStatus is the index's status line, word for word: the owner
	// answered the decisions on 2026-10-20 and the plan is approved to build.
	// It moves only when the work it describes moves, which is why it is
	// asserted rather than left to a reader.
	rigorModesStatus = "**Status: approved to build on the branch `rigor-modes`; the owner approval gate is passed.**"

	// rigorModesGatePassed is the plan's gate sentence, word for word, and
	// rigorModesAnswered the marker every decision in the reconciliation carries
	// once the owner has answered it. The two move together: a gate that is
	// passed while a decision still reads as a recommendation is the one
	// inconsistency this folder exists to avoid.
	rigorModesGatePassed = "**This gate is passed: the owner answered §5's decisions (D1–D32) on 2026-10-20, and the plan is approved to build on the branch `rigor-modes`.**"
	rigorModesAnswered   = "**Answered 2026-10-20"
	// rigorModesAnsweredIntro is the reconciliation's own statement that the
	// block carries answers, which is the sentence the "not taken" one became.
	rigorModesAnsweredIntro = "**Every decision here is answered"

	// rigorModesNotImplemented is the index's first "what this folder is not"
	// bullet, which replaced "Not an approval" when the gate passed: the
	// approval is a go to build, not a claim that anything is built.
	rigorModesNotImplemented = "**Not implemented.**"

	// rigorModesGateHeading is the plan's gate, and rigorModesSlicesHeading the
	// section whose tasks the completeness guard counts.
	rigorModesGateHeading   = "## OWNER APPROVAL GATE"
	rigorModesSlicesHeading = "## 8. The slices"

	// rigorModesCensusHeading is the reconciliation's census, whose rows the
	// census guard reads.
	rigorModesCensusHeading = "## 4. The census, and what it is scoped to"

	// rigorModesQuestionsHeading is the spec's own section of open questions, the
	// one the spec guard counts and no document here may answer.
	rigorModesQuestionsHeading = "## Open questions"

	// rigorModesBacklogHeading is the reconciliation's section for the owner's
	// backlog ruling of 2026-10-20, and rigorModesBacklogFolder the folder every
	// document in the set puts the items in. The ruling is a deliverable of that
	// amendment the way the scope ruling was of the one before it, so it is held
	// the same way: by the section and the path a later edit would quietly drop,
	// not by the prose around them.
	rigorModesBacklogHeading = "### 1.3 The backlog ruling"
	rigorModesBacklogFolder  = "docs/backlog/"
)

// rigorModesRevision is the commit the reconciliation was read at. A later run
// that re-reads the tree moves it in the same commit, which is how a reader can
// tell how far the document has aged.
const (
	rigorModesRevision = "`1d325928`"
	rigorModesHash     = "1d32592836a089cce2d72ff53e29116a00e1c433"
)

// rigorModesIndexedFiles are the documents the index must name. The index is
// the folder's entry point, so a document it does not name is one a reader
// cannot find.
var rigorModesIndexedFiles = []string{
	"[spec.md](spec.md)",
	"[reconciliation.md](reconciliation.md)",
	"[implementation-plan.md](implementation-plan.md)",
}

// rigorModesClasses are the five words a finding may be classified with
// (reconciliation.md §3). A sixth word is a decision about what the documents
// promise, not a synonym.
var rigorModesClasses = []string{"Wrong", "Exists", "Gap", "Rule", "Open"}

// rigorModesTaskLabels are the seven things every task in the plan must state:
// what it changes, how it is tested, what fails first, how an agent verifies it,
// what it waits for, who reviews it, and what the commit says. A task missing
// one of them is a task an agent cannot pick up.
var rigorModesTaskLabels = []string{
	"**Files.**", "**Tests.**", "**Red step.**", "**Verification.**",
	"**Depends on.**", "**Reviewer.**", "**Commit.**",
}

// rigorModesLabels matches a finding's, a decision's, an item's and an
// unverified row's opening label. All four are numbered from 1 with no gap, and
// a reader who cites "F19" or "D7" has to be able to trust the number.
var (
	rigorModesFindingRE  = regexp.MustCompile(`(?m)^\*\*F([0-9]+) —`)
	rigorModesDecisionRE = regexp.MustCompile(`(?m)^### D([0-9]+) —`)
	rigorModesItemRE     = regexp.MustCompile(`(?m)^\*\*O([0-9]+) —`)
	rigorModesUnverifRE  = regexp.MustCompile(`(?m)^\| U([0-9]+) \|`)
	rigorModesClassRE    = regexp.MustCompile(`Class:\s*\*\*([A-Za-z]+)\*\*`)
	rigorModesTaskRE     = regexp.MustCompile(`(?m)^\*\*(T[0-9]+\.[0-9]+) — `)
	rigorModesSliceRE    = regexp.MustCompile(`(?m)^### Slice ([0-9]+) — `)
)

// TestDocsRigorModesIsWritten: the folder exists with its index and its three
// documents, the index carries the status line word for word and names the other
// three files, and each document carries the structure the brief asked for. Its
// red state is this test: the folder's guard did not exist until this file did.
func TestDocsRigorModesIsWritten(t *testing.T) {
	readme := readDoc(t, rigorModesReadme)
	recon := readDoc(t, rigorModesRecon)
	plan := readDoc(t, rigorModesPlan)

	for _, want := range append([]string{
		rigorModesStatus,
		"**Owner:** the Fugaro owner",
		rigorModesRevision,
		rigorModesHash,
		// The claim that this folder decides nothing for the owner, which
		// became a claim about what is built rather than about what is approved
		// when the gate passed.
		rigorModesNotImplemented,
		// The check number this work proposes, in the index, where a reader
		// looks for the numbers a work holds. It is 119 rather than 110-118
		// because every one of those is held by an open claim at the base
		// (110 named by the mobile P5.3 design, 111 the setup wizard's, 112
		// the Windows CLI's, 113-118 the voice session's six).
		"**Check 119**",
		// The backlog the owner ruled on, named in the index by the folder its
		// items live in: an index that stops naming it is an index a reader
		// cannot find the backlog in.
		rigorModesBacklogFolder,
	}, rigorModesIndexedFiles...) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", rigorModesReadme, want)
		}
	}

	for _, want := range []string{
		rigorModesStatus,
		"## 3. Findings",
		rigorModesCensusHeading,
		rigorModesBacklogHeading,
		rigorModesBacklogFolder,
		"## 5. Decisions for the owner",
		"## 9. UNVERIFIED",
		// The preamble that replaced "**None of these is taken.**" when the
		// owner answered: the decisions section states its own state, and a
		// reader who finds a recommendation where an answer belongs has found
		// this sentence broken.
		rigorModesAnsweredIntro,
		// The revision, in both forms, so a later reader can compare a re-read
		// against the commit this document was written at.
		rigorModesRevision,
		rigorModesHash,
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", rigorModesRecon, want)
		}
	}

	for _, want := range []string{
		rigorModesStatus,
		rigorModesSlicesHeading,
		rigorModesGateHeading,
		rigorModesBacklogFolder,
		rigorModesGatePassed,
		// The live check this work proposes, named in the plan as well as in the
		// index, because a number claimed in two places is a number to check.
		"Check 119",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", rigorModesPlan, want)
		}
	}
}

// TestDocsRigorModesSpecIsTheOwners: the spec is the owner's document, and the
// owner's answers are recorded beside it rather than in it. The spec's four open
// questions stay open *in the spec* — it is verbatim, so it carries no answer
// marker and no answer text, and the sibling folder's marker ("**Answered
// 2026-10-19: option 1**") appears only in the reconciliation — and each of the
// four has a decision behind it, answered on 2026-10-20 the way every other
// decision in the block is. This is the mirror of the rule the guard carried
// while the questions were open: then it refused an answer anywhere, and now it
// refuses an answer in the spec and requires one on every decision.
func TestDocsRigorModesSpecIsTheOwners(t *testing.T) {
	spec := readDoc(t, rigorModesSpec)
	recon := readDoc(t, rigorModesRecon)
	plan := readDoc(t, rigorModesPlan)

	for _, want := range []string{
		"# Fugaro Rigor Modes Spec",
		"Oct 20, 2026 · @Dimi",
		// The five modes, as the owner named them.
		"**POC**", "**Startup**", "**Pro**", "**Critical**", "**Hotfix**",
		rigorModesQuestionsHeading,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", rigorModesSpec, want)
		}
	}
	// The index is looked up and checked before it is sliced: a guard over a
	// heading that has been removed fails, and does not panic on spec[-1:].
	i := strings.Index(spec, rigorModesQuestionsHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q: a document with no question section has no questions to count", rigorModesSpec, rigorModesQuestionsHeading)
	}
	if got := strings.Count(spec[i:], "\n- "); got != 4 {
		t.Errorf("%s: the owner's open questions are %d, want 4", rigorModesSpec, got)
	}

	// The spec stays the owner's document: the answers are recorded in the
	// reconciliation, and an answer pasted into the spec would make it this
	// folder's text.
	for _, want := range []string{rigorModesAnswered, "the owner answered"} {
		if strings.Contains(spec, want) {
			t.Errorf("%s records %q: the spec is verbatim, and the answers live in the reconciliation", rigorModesSpec, want)
		}
	}

	// Every decision carries the owner's answer, and the four questions the spec
	// left open are among them: the scale (D1), the calibration (O1 behind D1's
	// anchors), where the config lives (D5) and who may invoke Hotfix (D12).
	idx := rigorModesDecisionRE.FindAllStringSubmatchIndex(recon, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no decisions to answer", rigorModesRecon)
	}
	for n, m := range idx {
		id := recon[m[2]:m[3]]
		end := len(recon)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		if body := recon[m[1]:end]; !strings.Contains(body, rigorModesAnswered) {
			t.Errorf("%s: D%s carries no %q marker: the owner answered every decision on 2026-10-20", rigorModesRecon, id, rigorModesAnswered)
		}
	}
	for _, want := range []string{"### D1 —", "**O1 —", "### D5 —", "### D12 —"} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: every one of the owner's open questions has a decision or an item behind it", rigorModesRecon, want)
		}
	}

	// The plan says the gate is passed, and the reconciliation says why: a gate
	// passed in the plan and not recorded where the decisions are is the one
	// half-sentence a reader cannot check.
	for _, want := range []string{"the owner answered", "2026-10-20"} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: the answers' date and owner are what the gate rests on", rigorModesRecon, want)
		}
	}
	if !strings.Contains(plan, rigorModesGatePassed) {
		t.Errorf("%s lacks the passed gate sentence %q", rigorModesPlan, rigorModesGatePassed)
	}
}

// rigorModesNumbered checks one series of **N — labels** runs 1..n with no gap
// and no repeat, and reports the count. It is this file's own helper rather than
// the one docs_windows_cli_test.go defines, so neither folder's guard depends on
// the other's file.
func rigorModesNumbered(t *testing.T, path, doc, what string, re *regexp.Regexp) []int {
	t.Helper()
	var nums []int
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: %s label %q does not carry a number", path, what, m[0])
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no %s", path, what)
	}
	for i, n := range nums {
		if n != i+1 {
			t.Fatalf("%s: %s are %v, want 1..%d with no gap: a citation has to mean one thing", path, what, nums, len(nums))
		}
	}
	return nums
}

// TestDocsRigorModesFindingsDecisionsAndItemsAreNumbered: every finding carries
// a number and one of the five class words, every decision and every item is
// numbered from 1 with no gap, and the UNVERIFIED table is numbered the same
// way. This test reads the blocks' own numbers and counts nothing else, so a
// summary of them stated anywhere in the folder is that other document's claim:
// TestDocsRigorModesEveryBlockRangeIsCurrent is what holds every stated range —
// and every cited number — to the block it summarises.
func TestDocsRigorModesFindingsDecisionsAndItemsAreNumbered(t *testing.T) {
	recon := readDoc(t, rigorModesRecon)

	findings := rigorModesNumbered(t, rigorModesRecon, recon, "findings", rigorModesFindingRE)
	decisions := rigorModesNumbered(t, rigorModesRecon, recon, "decisions", rigorModesDecisionRE)
	items := rigorModesNumbered(t, rigorModesRecon, recon, "items", rigorModesItemRE)
	unverified := rigorModesNumbered(t, rigorModesRecon, recon, "UNVERIFIED rows", rigorModesUnverifRE)

	// Every finding carries its class, and the class is one of the five words.
	// The section is split on the finding labels so a class cannot be read as
	// belonging to the neighbour.
	chunks := rigorModesFindingRE.Split(recon, -1)[1:]
	if len(chunks) != len(findings) {
		t.Fatalf("%s: %d findings, %d chunks", rigorModesRecon, len(findings), len(chunks))
	}
	counted := map[string]int{}
	for i, chunk := range chunks {
		m := rigorModesClassRE.FindStringSubmatch(chunk)
		if m == nil {
			t.Errorf("%s: F%d carries no 'Class: **X**'", rigorModesRecon, findings[i])
			continue
		}
		if !slices.Contains(rigorModesClasses, m[1]) {
			t.Errorf("%s: F%d is classified %q, want one of %v", rigorModesRecon, findings[i], m[1], rigorModesClasses)
			continue
		}
		counted[m[1]]++
	}
	for _, c := range rigorModesClasses {
		if counted[c] == 0 {
			t.Errorf("%s classifies no finding as %s: every one of the five words is in use", rigorModesRecon, c)
		}
	}

	// The index's own ranges, checked against the counts rather than trusted,
	// and the plan's citations of the same ranges.
	readme := readDoc(t, rigorModesReadme)
	for _, want := range []string{
		"**D1–D" + strconv.Itoa(len(decisions)) + "**",
		"**F1–F" + strconv.Itoa(len(findings)) + "**",
		"**U1–U" + strconv.Itoa(len(unverified)) + "**",
		"**O1–O" + strconv.Itoa(len(items)) + "**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not state %q: the index's ranges are checked against the documents' own counts", rigorModesReadme, want)
		}
	}
	plan := readDoc(t, rigorModesPlan)
	for _, want := range []string{
		"F1–F" + strconv.Itoa(len(findings)),
		"D1–D" + strconv.Itoa(len(decisions)),
		"U1–U" + strconv.Itoa(len(unverified)),
		"O1–O" + strconv.Itoa(len(items)),
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not cite %q", rigorModesPlan, want)
		}
	}
}

// TestDocsRigorModesCensusStatesItsCommands: the census carries a command in
// every row, and running that command reproduces the count the row states. A
// count without its command is a figure a later reader cannot re-derive, and a
// command that no longer produces its figure is worse than no command at all:
// it is a claim that looks checked. The other 51 rows this repository's folders
// assert in prose; this one is the only place the figure can be re-derived by
// the test itself, so it is re-derived.
//
// The commands are POSIX shell on purpose — `sed`, `grep`, `ls`, `wc` and
// `git grep` — because that is what a reader copies out of the table, so the
// guard runs them the way a reader would, from the repository root.
func TestDocsRigorModesCensusStatesItsCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the census's commands are POSIX shell: sed, grep, ls, wc and git grep")
	}
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	recon := readDoc(t, rigorModesRecon)

	i := strings.Index(recon, rigorModesCensusHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q", rigorModesRecon, rigorModesCensusHeading)
	}
	body := recon[i:]
	if j := strings.Index(body, "\n## 5."); j >= 0 {
		body = body[:j]
	}
	rows, commands, checked, skipped := 0, 0, 0, 0
	// Seven rows are `git grep`, and the census states its commands run from the
	// repository root. A tree that is not the root of a git work tree is not
	// that root, and `git grep` there fails -- exit 128, nothing on stdout --
	// rather than printing a figure. That tree is not hypothetical: it is the
	// copy docs_desktop_console_test.go's TestStagedLinuxFilesSurviveTheCopyIn
	// makes and runs this family inside, a copy of the files deliberately
	// without `.git` (the copy-in proof needs no repository, no branch and no
	// index). So a row whose command reads the tree through git is skipped
	// there and every row that reads the files directly still runs; the count
	// is logged below rather than swallowed, because a skip is a row this run
	// did not check.
	_, statErr := os.Stat(filepath.Join(censusRoot, ".git"))
	workTree := statErr == nil
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := rigorModesTableCells(line)
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == "Construct" {
			continue // the header
		}
		if rigorModesAllRule(cells) {
			continue // the separator
		}
		if len(cells) != 3 {
			t.Errorf("%s: census row %q has %d cells, want 3 (Construct | Count | Command)", rigorModesRecon, line, len(cells))
			continue
		}
		rows++
		if strings.Count(cells[2], "`") < 2 {
			t.Errorf("%s: census row %q carries no command: where a count is volatile state the command, not the figure", rigorModesRecon, line)
			continue
		}
		commands++

		// The row's own figure, and the command that has to produce it.
		want, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			t.Errorf("%s: census row %q states %q as its count, which is not a number", rigorModesRecon, line, cells[1])
			continue
		}
		cmd := rigorModesCommand(cells[2])
		if cmd == "" {
			t.Errorf("%s: census row %q carries no command inside its code span", rigorModesRecon, line)
			continue
		}
		if !workTree && rigorModesCommandNeedsGit(cmd) {
			skipped++
			continue
		}
		c := exec.Command(sh, "-c", cmd)
		c.Dir = censusRoot // the census states its commands run from the repository root
		out, err := c.Output()
		// `grep` and `git grep` exit 1 when they match nothing, which is what a
		// row stating 0 legitimately does; the figure is the output, so only a
		// command that produced none is an error.
		if err != nil && len(out) == 0 {
			t.Errorf("%s: census row %q: running %s: %v", rigorModesRecon, line, cmd, err)
			continue
		}
		got := strings.TrimSpace(string(out))
		checked++
		if got != strconv.Itoa(want) {
			t.Errorf("%s: census row %q says %d, but %s prints %q: the command has to reproduce the figure it sits beside",
				rigorModesRecon, line, want, cmd, got)
		}
	}
	if rows == 0 {
		t.Errorf("%s: the census carries no rows", rigorModesRecon)
	}
	if commands != rows {
		t.Errorf("%s: %d of %d census rows carry a command, want all of them", rigorModesRecon, commands, rows)
	}
	if checked == 0 {
		t.Errorf("%s: no census command was run: the figures are the point", rigorModesRecon)
	}
	if skipped > 0 {
		t.Logf("%d of %d census rows were not run: their commands read the tree through git, and %s is not the root of a git work tree (the copy-in proof runs this family in a copy of the files, without a repository)", skipped, rows, censusRoot)
	}
}

// rigorModesCommandNeedsGit reports whether a census command reads the tree
// through git rather than reading its files directly. `git grep` is the only
// such command in the census today, and its seven rows all spell it at the front
// of a command or of a pipeline; the slack in matching the word rather than the
// prefix is for the row that grows one further along -- inside a `{ …; }` group
// or after a pipe -- so a `git` anywhere a shell would run it is covered.
// "Anywhere a shell would run it" is the rule, not "anywhere the three letters
// appear": `git` has to sit at the start of a command, after `|`, `;`, `&`, `(`
// or `{`, and be followed by whitespace or the end. That is what keeps
// `.gitignore`, `.gitattributes` and docs/git-providers.md out of it -- paths a
// census command may perfectly well read without a repository, and a row that
// reads one is not skipped in a tree that has none.
var rigorModesNeedsGitRE = regexp.MustCompile(`(?:^\s*|[|;&({]\s*)git(?:\s|$)`)

func rigorModesCommandNeedsGit(cmd string) bool {
	return rigorModesNeedsGitRE.MatchString(cmd)
}

// TestDocsRigorModesCensusSkipsGitOnlyWhereAShellRunsIt pins the skip rule above,
// because its two mistakes are not symmetric. A command that really runs `git`
// and is not skipped fails in a tree without a repository rather than printing a
// figure, which is the failure this rule exists to prevent; a command that only
// reads a path with the three letters in it and *is* skipped loses a row the
// copy-in tree could have checked. So both directions are pinned: the shapes the
// census's own `git` rows have, and the paths a census row may read.
func TestDocsRigorModesCensusSkipsGitOnlyWhereAShellRunsIt(t *testing.T) {
	runsGit := []string{
		`git grep -l 'Severity' -- internal/runner ':!*_test.go' | wc -l`,
		`git ls-tree --name-only 1d325928:internal/cli | grep -c '^docs_.*_test\.go$'`,
		`{ sed -n '/^type Spec struct {/,/^}/p' internal/task/task.go; git grep -c Path internal/recipe/recipe.go; } | wc -l`,
		`sed -n '/^type Spec struct {/,/^}/p' internal/task/task.go | git grep -c Path`,
		`grep -c x docs/a.md && git grep -c y docs/b.md`,
	}
	for _, cmd := range runsGit {
		if !rigorModesCommandNeedsGit(cmd) {
			t.Errorf("rigorModesCommandNeedsGit(%q) = false: the command runs git, so in a tree without a repository it prints a failure instead of a figure, and the guard has to skip it there rather than fail on it", cmd)
		}
	}
	readsFiles := []string{
		`ls internal/cli/docs_*_test.go | wc -l`,
		`sed -n 's/^const Version = //p' internal/projectpolicy/version.go`,
		`grep -c gitignore .gitignore`,
		`grep -c git docs/git-providers.md`,
		`sed -n '/^## /p' .gitattributes | wc -l`,
	}
	for _, cmd := range readsFiles {
		if rigorModesCommandNeedsGit(cmd) {
			t.Errorf("rigorModesCommandNeedsGit(%q) = true: the command reads its files directly and needs no repository, so skipping it there would drop a row the copy-in tree can check", cmd)
		}
	}
}

// rigorModesCommand is the shell command inside a census row's code span, with
// the table's own escaping undone: a markdown cell cannot carry a bare `|`, so
// a command that pipes writes `\|`, and what runs is the `|` a reader copies.
func rigorModesCommand(cell string) string {
	cell = strings.TrimSpace(cell)
	i := strings.Index(cell, "`")
	j := strings.LastIndex(cell, "`")
	if i < 0 || j <= i {
		return ""
	}
	return strings.ReplaceAll(cell[i+1:j], `\|`, "|")
}

// rigorModesCitedTaskRE matches a citation of one task, as the documents write
// it ("plan T3.2", "T8.1's"). It is deliberately the whole id and not a range:
// a range ("T3.1-T3.5") is checked endpoint by endpoint, because a range whose
// far end does not exist is the same broken citation.
var rigorModesCitedTaskRE = regexp.MustCompile(`T([0-9]+)\.([0-9]+)`)

// TestDocsRigorModesEveryCitedTaskExists: every `T<n>.<m>` the reconciliation
// and the index cite resolves to a task heading in the plan. This is the guard
// the review asked for and the folder did not have: a document that cites a
// task that does not exist is a document whose reader cannot find what it
// points at, and a plan is edited by renumbering far more often than by
// rewriting. It is a check over the *citation*, not over the prose around it, so
// a reworded sentence cannot hide a dangling reference.
func TestDocsRigorModesEveryCitedTaskExists(t *testing.T) {
	plan := readDoc(t, rigorModesPlan)
	exists := map[string]bool{}
	for _, m := range rigorModesTaskRE.FindAllStringSubmatch(plan, -1) {
		exists[m[1]] = true
	}
	if len(exists) == 0 {
		t.Fatalf("%s lists no tasks to cite", rigorModesPlan)
	}

	cited := 0
	for _, path := range []string{rigorModesRecon, rigorModesReadme, rigorModesPlan} {
		doc := readDoc(t, path)
		for _, m := range rigorModesCitedTaskRE.FindAllStringSubmatch(doc, -1) {
			id := "T" + m[1] + "." + m[2]
			cited++
			if !exists[id] {
				t.Errorf("%s cites %s, which is no task in %s: a citation has to resolve to a heading", path, id, rigorModesPlan)
			}
		}
	}
	if cited == 0 {
		t.Fatalf("no document cites a task: the plan and the reconciliation are meant to point at each other")
	}
}

// rigorModesBlockRangeRE matches a document stating a whole block's range as
// this folder writes one: the prefix, a 1, the dash, and the last number, with
// the prefix repeated or not — "F1–F41" and "D1–D32". A range is a summary
// claim, and it is the one number in these documents a reader takes without
// counting, so a range left behind by an edit ("D1–D19" after D20 landed) is a
// claim the folder no longer has.
var rigorModesBlockRangeRE = regexp.MustCompile(`([FDOU])1 ?[–-] ?(?:[FDOU])?([0-9]+)`)

// rigorModesAnyNumberRE matches one citation of a block's number anywhere it is
// written ("F19", "D7", "U10", "O3"), which is how the documents point at each
// other.
var rigorModesAnyNumberRE = regexp.MustCompile(`([FDOU])([0-9]+)`)

// rigorModesForeignNumber is a number above this folder's own blocks that a
// document here may still cite, because it belongs to a register the document
// names: the coordinator's allocation (D192), the free range its row states
// (D193), the two sibling folders' D-blocks (docs/windows-cli/'s D1–D16 and
// docs/setup-wizard/'s D1–D12), and the block the folder's own O7 reads out of
// the register — docs/mobile/voice-session/'s D201–D220, the numbers around them
// that voice-session left alone (D200, written nowhere here), and the next free
// block at this folder's base (D221), which O7 recommends by name.
//
// Each is allowed only where the document names its owner, because "D16" beside
// no name is a citation of a decision this folder does not have. The register's
// figures are quoted with the anchor they were read at (docs/coordinator/README.md's
// row says "at `c6009b5f`"), which is why the block above is read from this
// folder's base commit rather than trusted as current: at `1d325928` the highest
// number written anywhere in docs/ is D220, not D192.
var rigorModesForeignNumber = []struct {
	prefix, owner string
	end           int
}{
	{"D", "owner's allocation", 192},
	{"D", "D193 onward", 193},
	{"D", "docs/windows-cli/", 16},
	{"D", "docs/setup-wizard/", 12},
	{"D", "docs/mobile/voice-session/", 200},
	{"D", "docs/mobile/voice-session/", 201},
	{"D", "docs/mobile/voice-session/", 219},
	{"D", "docs/mobile/voice-session/", 220},
	{"D", "next free block", 221},
}

// TestDocsRigorModesEveryBlockRangeIsCurrent: every block range a document in
// this folder states either ends where the block does (counted from the
// reconciliation rather than trusted), or is explained: a *part* of a block cited
// beside a section reference (§1.1's table cites "D1–D4" as the decisions behind
// the score), or a range this folder quotes from somewhere else, whose owner the
// document names. Everything else is a claim about how far a block reaches that
// the block disagrees with — the shape an edit leaves behind, because a block is
// extended by appending to it and the table that summarises it is elsewhere.
//
// It also holds that no document cites a number a block does not have: a citation
// of "D26" is a pointer to a decision nobody wrote.
func TestDocsRigorModesEveryBlockRangeIsCurrent(t *testing.T) {
	recon := readDoc(t, rigorModesRecon)
	counts := map[string]int{
		"F": len(rigorModesNumbered(t, rigorModesRecon, recon, "findings", rigorModesFindingRE)),
		"D": len(rigorModesNumbered(t, rigorModesRecon, recon, "decisions", rigorModesDecisionRE)),
		"O": len(rigorModesNumbered(t, rigorModesRecon, recon, "items", rigorModesItemRE)),
		"U": len(rigorModesNumbered(t, rigorModesRecon, recon, "UNVERIFIED rows", rigorModesUnverifRE)),
	}
	foreign := func(prefix string, n int, doc string) bool {
		// The owner is looked for across the document's own line breaks: a
		// markdown paragraph wraps, and "the next free\nblock" still names the
		// register the number comes from.
		doc = strings.ReplaceAll(doc, "\n", " ")
		for _, f := range rigorModesForeignNumber {
			if f.prefix == prefix && f.end == n && strings.Contains(doc, f.owner) {
				return true
			}
		}
		return false
	}

	ranges, cites := 0, 0
	for _, path := range []string{rigorModesReadme, rigorModesRecon, rigorModesPlan} {
		doc := readDoc(t, path)
		for i, line := range strings.Split(doc, "\n") {
			for _, loc := range rigorModesBlockRangeRE.FindAllStringSubmatchIndex(line, -1) {
				m := rigorModesBlockRangeRE.FindStringSubmatch(line[loc[0]:loc[1]])
				end, err := strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("%s: unreadable range end %q", path, m[2])
				}
				ranges++
				// A *part* of a block is cited beside the section it belongs
				// to ("§5, D1–D4"): the § has to come before the range, or a
				// line that merely mentions a section somewhere would excuse
				// any range on it.
				if end == counts[m[1]] || foreign(m[1], end, doc) || strings.Contains(line[:loc[0]], "§") {
					continue
				}
				t.Errorf("%s line %d states %s1–%s%d, and the %s block reaches %d: a range has to end where the block does, cite a part of it beside a section reference, or name whose register it quotes",
					path, i+1, m[1], m[1], end, m[1], counts[m[1]])
			}
			for _, m := range rigorModesAnyNumberRE.FindAllStringSubmatch(line, -1) {
				n, err := strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("%s: unreadable citation %q", path, m[2])
				}
				cites++
				if n <= counts[m[1]] || foreign(m[1], n, doc) {
					continue
				}
				t.Errorf("%s line %d cites %s%d, and the %s block reaches %d: a citation has to resolve to a number the block has",
					path, i+1, m[1], n, m[1], counts[m[1]])
			}
		}
	}
	if ranges == 0 || cites == 0 {
		t.Fatalf("no document states a block range or cites a block number: the guard's own subjects are missing")
	}
}

// rigorModesRuleRE matches a markdown table's separator cell ("---", ":--", …).
var rigorModesRuleRE = regexp.MustCompile(`^:?-+:?$`)

// rigorModesAllRule reports whether every cell is a separator, which is the row
// under a markdown table's header and not a row of data.
func rigorModesAllRule(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !rigorModesRuleRE.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// rigorModesTableCells splits a markdown table row into cells, keeping an
// escaped pipe (`\|`, which a command inside a code span needs) inside its cell.
func rigorModesTableCells(line string) []string {
	const esc = "\x00"
	line = strings.ReplaceAll(line, `\|`, esc)
	line = strings.Trim(strings.TrimSpace(line), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.ReplaceAll(cells[i], esc, `\|`)
	}
	return cells
}

// TestDocsRigorModesEveryTaskIsComplete: every task in the plan states the seven
// things a task has to state, and the slices are numbered from 0 with no gap.
// This is the folder's own reading of the brief's requirement that each part
// carry its files, tests, a verification an agent can run and the reviewer that
// applies — a task missing one is a task nobody can start.
func TestDocsRigorModesEveryTaskIsComplete(t *testing.T) {
	plan := readDoc(t, rigorModesPlan)

	if i := strings.Index(plan, rigorModesSlicesHeading); i < 0 {
		t.Fatalf("%s lacks %q", rigorModesPlan, rigorModesSlicesHeading)
	}

	idx := rigorModesTaskRE.FindAllStringSubmatchIndex(plan, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no tasks", rigorModesPlan)
	}
	for n, m := range idx {
		id := plan[m[2]:m[3]]
		end := len(plan)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		body := plan[m[1]:end]
		for _, label := range rigorModesTaskLabels {
			if !strings.Contains(body, label) {
				t.Errorf("%s: %s states no %s", rigorModesPlan, id, label)
			}
		}
	}

	// The slices, numbered from 0 with no gap (a slice is a part of a plan, so
	// its numbering starts at zero, unlike a finding's), and every task
	// belonging to one: a task written before the first slice heading is a task
	// in no slice.
	var nums []int
	for _, m := range rigorModesSliceRE.FindAllStringSubmatch(plan, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: slice label %q does not carry a number", rigorModesPlan, m[0])
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no slices", rigorModesPlan)
	}
	for i, n := range nums {
		if n != i {
			t.Fatalf("%s: slices are %v, want 0..%d with no gap", rigorModesPlan, nums, len(nums)-1)
		}
	}
	if idx[0][0] < strings.Index(plan, rigorModesSlicesHeading) {
		t.Errorf("%s: a task is written above %q", rigorModesPlan, rigorModesSlicesHeading)
	}
}

// rigorModesCriticalPin is the one-line pin D19's answer gives a repository that
// wants today's behaviour after Pro became the default: without it in the
// documents, the change of behaviour has no stated way back.
const rigorModesCriticalPin = "`rigor.default: critical`"

// rigorModesUnchangedClaims are the phrases the folder used to make its
// strongest safety claim with, back when the default was Critical and an
// unconfigured repository was said not to change at all. D19's answer makes Pro
// the default, so each of these is true of *Critical mode* only — of a run whose
// resolved mode is Critical — and the guard holds each occurrence to a nearby
// "Critical mode" rather than to a reader's memory of which decision said what.
//
// The phrases are the ones that say a *run* is unchanged. A sentence about a
// golden or a transcript being unchanged ("an unscored finding rendering exactly
// as it does today", plan T4.1) is a claim about a test fixture, not about a
// mode, and is not one of these.
var rigorModesUnchangedClaims = []string{
	"relaxes nothing",
	"byte-identical to today",
	"behaves exactly as it does today",
}

// rigorModesScopeWindow is how far from such a claim the word "Critical" may sit
// and still count as scoping it. It is generous because markdown wraps: the
// claim and its scope are one sentence, and a sentence here runs to a few
// hundred characters.
const rigorModesScopeWindow = 600

// rigorModesMarksReadyAbsences are the phrasings the folder must not carry: a
// run's pull request is opened as a draft and marked ready by the runner on
// `runner.Decide`'s answer (D11's correction), so any sentence that puts a
// person in that path is a description of a practice this repository does not
// have.
// They are compared against a lower-cased document, so they are lower-case
// themselves, and against the flattened text, so a re-wrapped sentence does not
// slip past.
var rigorModesMarksReadyAbsences = []string{
	"a person marks ready",
	"a person marks it ready",
	"a person marks the pull request ready",
	"a person marks a pull request ready",
	"a human marks it ready",
	"the owner marks it ready",
	"an operator marks it ready",
}

// rigorModesCheckpointSource and rigorModesRunstoreSource are two files this
// folder's findings make claims about, and the guard reads them rather than
// trusting the document: F44 says what a checkpoint does and does not do, and
// F40 says a halt reason is planned rather than built.
const (
	rigorModesCheckpointSource = "internal/runner/checkpoint.go"
	rigorModesRunstoreSource   = "internal/runstore/runstore.go"
)

// rigorModesGuardTestRE matches a test function this guard file ships, so the
// plan's list of what the guard holds can be held to the file itself.
var rigorModesGuardTestRE = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\) \{`)

// rigorModesBlock returns the chunk of a numbered block a document starts at id
// and ends where the next one starts — a task of the plan or a decision of the
// reconciliation. It is how a guard asks a question about one task rather than
// about the document it sits in.
func rigorModesBlock(t *testing.T, path, doc, id string, re *regexp.Regexp) string {
	t.Helper()
	idx := re.FindAllStringSubmatchIndex(doc, -1)
	for n, m := range idx {
		if doc[m[2]:m[3]] != id {
			continue
		}
		end := len(doc)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		return doc[m[1]:end]
	}
	t.Fatalf("%s states no %s", path, id)
	return ""
}

// TestDocsRigorModesAnswersSayWhatTheyChange: the answers of 2026-10-20 changed
// four things about what this folder claims — the default mode (D19), the
// Hotfix gate's inputs (D14), the order the work is picked up in (D9) and who
// marks a pull request ready (D11) — and each of those changed a sentence that
// was true when it was written. This is the guard on the sentences rather than
// on the decisions: an answer recorded while the claim it displaced stays in the
// documents is the one failure a reader cannot see, because both halves read as
// deliberate.
func TestDocsRigorModesAnswersSayWhatTheyChange(t *testing.T) {
	readme := readDoc(t, rigorModesReadme)
	recon := readDoc(t, rigorModesRecon)
	plan := readDoc(t, rigorModesPlan)

	// Every check below is on the flattened text: the documents are hard-wrapped
	// prose, and a phrase a guard matches only in its unwrapped spelling stops
	// being checked the moment someone re-wraps the sentence. flattenDoc is this
	// package's helper for exactly that (docs_base_test.go), and the absence
	// checks fold case as well, so "A person marks it ready" is caught too.
	flat := map[string]string{
		rigorModesReadme: flattenDoc(readme),
		rigorModesRecon:  flattenDoc(recon),
		rigorModesPlan:   flattenDoc(plan),
	}
	flatLower := map[string]string{}
	for path, text := range flat {
		flatLower[path] = strings.ToLower(text)
	}

	// D19's answer, and the pin it owes a repository: the default is Pro, so
	// "nothing changes" is not the claim any more, and every one of the three
	// documents has to name the way back.
	for _, doc := range []struct{ path, text string }{
		{rigorModesReadme, flat[rigorModesReadme]},
		{rigorModesRecon, flat[rigorModesRecon]},
		{rigorModesPlan, flat[rigorModesPlan]},
	} {
		if !strings.Contains(doc.text, rigorModesCriticalPin) {
			t.Errorf("%s does not state %q: D19's answer makes Pro the default, and the one-line pin is what a repository keeps today's behaviour with", doc.path, rigorModesCriticalPin)
		}
	}

	// Every unchanged-behaviour claim is scoped to Critical mode. The window is
	// the sentence around the claim, not the document: a claim a reader meets
	// twenty lines away from its scope is the shape this catches.
	for _, path := range []string{rigorModesReadme, rigorModesRecon, rigorModesPlan} {
		doc := flat[path]
		for _, claim := range rigorModesUnchangedClaims {
			for at := strings.Index(doc, claim); at >= 0; {
				lo, hi := at-rigorModesScopeWindow, at+len(claim)+rigorModesScopeWindow
				if lo < 0 {
					lo = 0
				}
				if hi > len(doc) {
					hi = len(doc)
				}
				window := doc[lo:hi]
				if !strings.Contains(window, "Critical mode") && !strings.Contains(window, "Critical *mode*") {
					t.Errorf("%s states %q with no \"Critical mode\" in the sentence around it: D19's answer makes Pro the default, so the claim is true of Critical mode only", path, claim)
				}
				next := strings.Index(doc[at+1:], claim)
				if next < 0 {
					break
				}
				at += 1 + next
			}
		}
	}

	// D11's correction: the runner marks a run's pull request ready, and no
	// person is in that path in any mode. Both halves are pinned, because the
	// sentence the correction displaced said the opposite and read as practice.
	if !strings.Contains(flat[rigorModesRecon], "no person is in that path") {
		t.Errorf("%s does not state that no person is in the ready path: D11's answer corrects the folder's earlier reading of the practice", rigorModesRecon)
	}
	if !strings.Contains(flat[rigorModesPlan], "the runner is what marks it ready") {
		t.Errorf("%s does not state that the runner marks it ready: it is D11's answer as the task that changes the ready state has to hold it", rigorModesPlan)
	}
	for _, path := range []string{rigorModesReadme, rigorModesRecon, rigorModesPlan} {
		for _, phrase := range rigorModesMarksReadyAbsences {
			if strings.Contains(flatLower[path], phrase) {
				t.Errorf("%s states %q: no person marks a run's pull request ready in any mode (D11)", path, phrase)
			}
		}
	}

	// D9's settlement, in the decision's own words and in the plan's order: the
	// item is filed on the run's own branch and in the pull request the run is
	// already about, and the run loop that first tolerates a finding waits for
	// the task that files one.
	if !strings.Contains(flat[rigorModesRecon], "**same** run branch and the **same** pull request") {
		t.Errorf("%s does not settle where the item is filed: D9's answer puts it on the run's own branch and in its own pull request", rigorModesRecon)
	}
	t31 := flattenDoc(rigorModesBlock(t, rigorModesPlan, plan, "T3.1", rigorModesTaskRE))
	for _, want := range []string{"T4.5", "D9"} {
		if !strings.Contains(t31, want) {
			t.Errorf("%s: T3.1 does not name %s: Slice 3 may not tolerate a finding before T4.5 can file one (D9)", rigorModesPlan, want)
		}
	}
	if !strings.Contains(flat[rigorModesPlan], "the backlog comes first") {
		t.Errorf("%s does not state D9's order: the backlog's tasks are picked up before the run loop applies the mode", rigorModesPlan)
	}

	// D14's answer: the Hotfix gate is built from what the project configured,
	// never from this repository's own paths, and the agent's number is recorded
	// rather than trusted. The task that builds it has to say all three.
	t53 := flattenDoc(rigorModesBlock(t, rigorModesPlan, plan, "T5.3", rigorModesTaskRE))
	for _, want := range []string{"the project", "`rigor:`", "confidence", "never read"} {
		if !strings.Contains(t53, want) {
			t.Errorf("%s: T5.3 does not state %q: D14's gate reads what the project configured and records the agent's confidence without reading it", rigorModesPlan, want)
		}
	}

	// D29's value is the one the owner still owes, and the task that needs it
	// says so: a decision answered "in shape" is a decision a task cannot
	// silently fill in for itself.
	d29 := flattenDoc(rigorModesBlock(t, rigorModesRecon, recon, "29", rigorModesDecisionRE))
	if !strings.Contains(d29, "still the owner's") {
		t.Errorf("%s: D29 does not record that its value is still the owner's: the ruling settles the shape, not the repository", rigorModesRecon)
	}
	if t45 := flattenDoc(rigorModesBlock(t, rigorModesPlan, plan, "T4.5", rigorModesTaskRE)); !strings.Contains(t45, "D29") {
		t.Errorf("%s: T4.5 does not name D29 in its body: the value it assumes is the owner's to name", rigorModesPlan)
	}

	// Every guard test this file ships is named in the plan's own list of what
	// the folder's pin test holds. A test the plan does not list is a claim
	// nobody read before it was written, which is how the eighth one arrived.
	testSource := readDoc(t, "internal/cli/docs_rigor_modes_test.go")
	t02 := flattenDoc(rigorModesBlock(t, rigorModesPlan, plan, "T0.2", rigorModesTaskRE))
	for _, m := range rigorModesGuardTestRE.FindAllStringSubmatch(testSource, -1) {
		if !strings.Contains(t02, m[1]) {
			t.Errorf("%s: T0.2 does not name %s: every test this file ships is one the plan says the folder's guard holds", rigorModesPlan, m[1])
		}
	}

	// Two claims about the tree that the backlog ruling leans on are held to the
	// tree itself rather than to a reader's memory, because a finding that
	// overstates what exists is the one kind of error a later task inherits.
	//
	// F44: a checkpoint never commits. The claim is checkpoint.go's own, so the
	// document has to repeat it and must not say the checkpoints commit — which
	// is what the finding said before it was corrected, and what README's
	// "a run files into its own branch" rests on.
	checkpoint := flattenDoc(readDoc(t, rigorModesCheckpointSource))
	const checkpointFact = "never commits and never touches the working tree or the index"
	if !strings.Contains(checkpoint, checkpointFact) {
		t.Errorf("%s no longer states %q: this guard's F44 sentence quotes it, and the sentence has to move with it", rigorModesCheckpointSource, checkpointFact)
	}
	if !strings.Contains(flat[rigorModesRecon], checkpointFact) {
		t.Errorf("%s does not repeat %s's own claim that a checkpoint %q: F44 is what the ruling's same-branch rule is read from", rigorModesRecon, rigorModesCheckpointSource, checkpointFact)
	}
	if strings.Contains(flatLower[rigorModesRecon], "checkpoints commit") {
		t.Errorf("%s states that checkpoints commit: they push commits the run already made (%s)", rigorModesRecon, rigorModesCheckpointSource)
	}
	//
	// F40: HaltCredsRulesDenied is planned, not built. The vocabulary that would
	// carry it is runstore's, so the guard reads that file: a document that
	// names the halt has to say it is planned while the reason is not there.
	runstore := readDoc(t, rigorModesRunstoreSource)
	if strings.Contains(runstore, "HaltCredsRulesDenied") {
		t.Errorf("%s now carries HaltCredsRulesDenied: F40 marks it planned, and the sentence that says so has to move with this file", rigorModesRunstoreSource)
	}
	for at := strings.Index(flat[rigorModesRecon], "HaltCredsRulesDenied"); at >= 0; {
		lo, hi := at-rigorModesScopeWindow, at+len("HaltCredsRulesDenied")+rigorModesScopeWindow
		if lo < 0 {
			lo = 0
		}
		if hi > len(flat[rigorModesRecon]) {
			hi = len(flat[rigorModesRecon])
		}
		if !strings.Contains(strings.ToLower(flat[rigorModesRecon][lo:hi]), "planned") {
			t.Errorf("%s names HaltCredsRulesDenied with no \"planned\" beside it: %s has no such halt reason, so F40 may not read it as one the tree has", rigorModesRecon, rigorModesRunstoreSource)
		}
		next := strings.Index(flat[rigorModesRecon][at+1:], "HaltCredsRulesDenied")
		if next < 0 {
			break
		}
		at += 1 + next
	}
}
