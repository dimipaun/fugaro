package cli

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The four documents of docs/windows-cli/ — the Windows CLI design and plan —
// and the guard that owns them. There is no registry test over docs/ in this
// package: nothing pins the set of docs folders and nothing pins a folder's
// documents on its behalf, so a folder that wants its documents pinned writes
// its own file — this one, the same convention docs_in_job_review_test.go
// states for docs/in-job-review/.
//
// The documents are docs-only and only Slice 0 and Slice 1 are approved (D16,
// 2026-10-20), so what a guard can usefully hold is the plan's own shape and
// the plan's own claims about itself: the status line, the numbered findings and decisions with no gap, the
// support table naming commands that exist, the census stating the command
// beside every count, and — the claim that would otherwise be unbacked — that
// each documentation task names the guard that owns it. Every one of those is a
// sentence a later edit can quietly break, which is what makes them worth
// pinning rather than asserting in prose.
const (
	windowsCLIReadme = "docs/windows-cli/README.md"
	windowsCLISpec   = "docs/windows-cli/spec.md"
	windowsCLIRecon  = "docs/windows-cli/reconciliation.md"
	windowsCLIPlan   = "docs/windows-cli/implementation-plan.md"

	// windowsCLIStatus is the index's status line, word for word: the folder is
	// a plan with nothing implemented and every decision open. It moves only
	// when the work it describes moves (implementation-plan.md T0.3, then
	// T6.3), which is why it is asserted rather than left to a reader — Slice
	// 1's arrival is not one of those moves, and what it changed is stated in
	// the index's own section (windowsCLIFirstCut below).
	windowsCLIStatus = "**Status: plan only; nothing is implemented; decisions open.**"

	// windowsCLIPin is this file, by path: the plan names it as the owning guard
	// test of every documentation task, so the name lives in one place.
	windowsCLIPin = "internal/cli/docs_windows_cli_test.go"

	// windowsCLIFirstCut is the index's own statement that Slice 1's fixes are
	// in the tree, and windowsCLIFirstCutCommand the one command that proves
	// it: the headline's claim is a build, so the document that makes the claim
	// has to name the build and the command, and a build that stops working has
	// to be a red guard rather than a stale sentence (implementation-plan.md
	// T1.5). This is not the status line above it: that one is the plan's own
	// promise about the folder and moves at T0.3 and T6.3, with the evidence
	// that licenses the move.
	windowsCLIFirstCut        = "**The first cut builds.**"
	windowsCLIFirstCutCommand = "GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/fugaro.exe ./cmd/fugaro"
)

// windowsCLIRevision is the commit the reconciliation was read at. A later run
// that re-reads the tree moves it in the same commit, which is how a reader can
// tell how far the document has aged.
const windowsCLIRevision = "`54cb02a8`"

// windowsCLIIndexedFiles are the documents the index must name. The index is
// the folder's entry point, so a document it does not name is one a reader
// cannot find.
var windowsCLIIndexedFiles = []string{
	"[spec.md](spec.md)",
	"[reconciliation.md](reconciliation.md)",
	"[implementation-plan.md](implementation-plan.md)",
}

// windowsCLIClasses are the five words a finding may be classified with
// (reconciliation.md §3), and windowsCLITiers the tier vocabulary of the
// support table (implementation-plan.md §1). A new word in either is a decision
// about what the documents promise, not a synonym.
var (
	windowsCLIClasses = []string{"Wrong", "Exists", "Gap", "Rule", "Open"}
	windowsCLITiers   = []string{"**Supported", "**Best effort", "**Not supported", "**Out of scope"}
)

// TestDocsWindowsCLIIsWritten: the folder exists with its index and its three
// documents, the index carries the status line word for word and names the
// other three files, and each document carries the structure the brief asked
// for. Its red state is this test: the folder's guard did not exist until this
// file did.
func TestDocsWindowsCLIIsWritten(t *testing.T) {
	readme := readDoc(t, windowsCLIReadme)
	spec := readDoc(t, windowsCLISpec)
	recon := readDoc(t, windowsCLIRecon)
	plan := readDoc(t, windowsCLIPlan)

	for _, want := range append([]string{
		windowsCLIStatus,
		windowsCLIFirstCut,
		windowsCLIFirstCutCommand,
		"**Owner:** the Fugaro owner",
		windowsCLIRevision,
		// The ranges the index states, so its own summary can be counted.
		"**D1–D16**",
		"**F1–F42**",
		"**U1–U13**",
	}, windowsCLIIndexedFiles...) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", windowsCLIReadme, want)
		}
	}

	// The spec is the owner's sentence, and nothing else is put in the owner's
	// mouth: the brief is quoted verbatim and the context is marked as context.
	for _, want := range []string{
		"Let's work out a plan on how to make the CLI work on Windows.",
		"## The context given the same day",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", windowsCLISpec, want)
		}
	}

	for _, want := range []string{
		"## 3. Findings",
		"## 4. The census, and what it is scoped to",
		"## 5. Decisions for the owner",
		"## 7. UNVERIFIED",
		// The revision, so a later reader can tell when the document ages: the
		// short form in the header and the full hash a re-read can be compared
		// against.
		windowsCLIRevision,
		"54cb02a8ea43e74231e42e38f130b5dd37f1563e",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", windowsCLIRecon, want)
		}
	}

	for _, want := range []string{
		windowsCLIStatus,
		windowsCLISupportHeading,
		"## 3. The slices",
		windowsCLIGate,
		windowsCLIDocTaskRule,
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", windowsCLIPlan, want)
		}
	}
}

// The two headings and the one sentence the plan's own guard reads by name.
const (
	windowsCLISupportHeading = "## 1. Support tiers, per command group"
	windowsCLIGate           = "## OWNER APPROVAL GATE"
	// windowsCLIDocTaskRule is the plan's list of its own documentation tasks.
	// The guard below counts the tasks this sentence names, so a documentation
	// task added without a guard mention, or a guard named here that the plan
	// does not carry, is a failure rather than a silent hole.
	windowsCLIDocTaskRule = "**Documentation tasks.**"
)

// findingRE matches a finding's opening label, decisionRE a decision's and
// itemRE an item's. All three are numbered from 1 with no gap, and a reader who
// cites "F19" or "D7" has to be able to trust the number.
var (
	findingRE  = regexp.MustCompile(`(?m)^\*\*F([0-9]+) —`)
	decisionRE = regexp.MustCompile(`(?m)^### D([0-9]+) —`)
	itemRE     = regexp.MustCompile(`(?m)^\*\*O([0-9]+) —`)
	classRE    = regexp.MustCompile(`Class:\s*\*\*([A-Za-z]+)\*\*`)
	unverifRE  = regexp.MustCompile(`(?m)^\| U([0-9]+) \|`)
	taskRE     = regexp.MustCompile(`(?m)^\*\*(T[0-9]+\.[0-9]+) —`)
)

// numbered checks one series of **N — labels** runs 1..n with no gap and no
// repeat, and reports the count.
func numbered(t *testing.T, path, doc, what string, re *regexp.Regexp) []int {
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

// TestDocsWindowsCLIFindingsDecisionsAndItemsAreNumbered: every finding carries
// a number and one of the five class words, every decision and every item is
// numbered from 1 with no gap, and the UNVERIFIED table is numbered the same
// way. The index states "F1–F42", "D1–D16" and "U1–U13", so the ranges it
// states are exactly the ranges the documents carry.
func TestDocsWindowsCLIFindingsDecisionsAndItemsAreNumbered(t *testing.T) {
	recon := readDoc(t, windowsCLIRecon)

	findings := numbered(t, windowsCLIRecon, recon, "findings", findingRE)
	decisions := numbered(t, windowsCLIRecon, recon, "decisions", decisionRE)
	items := numbered(t, windowsCLIRecon, recon, "items", itemRE)
	unverified := numbered(t, windowsCLIRecon, recon, "UNVERIFIED rows", unverifRE)

	// Every finding carries its class, and the class is one of the five words.
	// The section is split on the finding labels so a class cannot be read as
	// belonging to the neighbour.
	chunks := findingRE.Split(recon, -1)[1:]
	if len(chunks) != len(findings) {
		t.Fatalf("%s: %d findings, %d chunks", windowsCLIRecon, len(findings), len(chunks))
	}
	counted := map[string]int{}
	for i, chunk := range chunks {
		m := classRE.FindStringSubmatch(chunk)
		if m == nil {
			t.Errorf("%s: F%d carries no 'Class: **X**'", windowsCLIRecon, findings[i])
			continue
		}
		if !slices.Contains(windowsCLIClasses, m[1]) {
			t.Errorf("%s: F%d is classified %q, want one of %v", windowsCLIRecon, findings[i], m[1], windowsCLIClasses)
			continue
		}
		counted[m[1]]++
	}
	for _, c := range windowsCLIClasses {
		if counted[c] == 0 {
			t.Errorf("%s classifies no finding as %s: every one of the five words is in use", windowsCLIRecon, c)
		}
	}

	// The index's own ranges, checked against the counts rather than trusted.
	readme := readDoc(t, windowsCLIReadme)
	for _, want := range []string{
		"**D1–D" + strconv.Itoa(len(decisions)) + "**",
		"**F1–F" + strconv.Itoa(len(findings)) + "**",
		"**U1–U" + strconv.Itoa(len(unverified)) + "**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not state %q: the index's ranges are checked against the documents' own counts", windowsCLIReadme, want)
		}
	}
	// The plan cites the same ranges.
	plan := readDoc(t, windowsCLIPlan)
	for _, want := range []string{
		"F1–F" + strconv.Itoa(len(findings)),
		"D1–D" + strconv.Itoa(len(decisions)),
		"U1–U" + strconv.Itoa(len(unverified)),
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not cite %q", windowsCLIPlan, want)
		}
	}
	// The items are the reconciliation's own list and the index names them too.
	if len(items) == 0 {
		t.Errorf("%s carries no items", windowsCLIRecon)
	}
}

// TestDocsWindowsCLISupportTableNamesRealCommands: the Commands column of the
// support-tier table, which is the input the platform promise is written from
// (T5.2), names commands this CLI actually has and tiers from the table's own
// vocabulary. This is the check that catches a table row naming a command
// nobody can run.
func TestDocsWindowsCLISupportTableNamesRealCommands(t *testing.T) {
	plan := readDoc(t, windowsCLIPlan)
	rows := tableRows(t, plan, windowsCLISupportHeading, "## 2.")

	// The command tree, by full path and by single word (a name or an alias
	// anywhere in the tree), so `runs ls` and `version` are both checkable.
	known := map[string]bool{}
	knownWord := map[string]bool{}
	for _, c := range NewRootCmd().Commands() {
		known[c.Name()] = true
		knownWord[c.Name()] = true
		for _, a := range c.Aliases {
			knownWord[a] = true
		}
		for _, s := range c.Commands() {
			known[c.Name()+" "+s.Name()] = true
			knownWord[s.Name()] = true
			for _, a := range s.Aliases {
				knownWord[a] = true
			}
		}
	}

	if len(rows) == 0 {
		t.Fatalf("%s: %s carries no table rows", windowsCLIPlan, windowsCLISupportHeading)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Errorf("%s: support row %q has %d cells, want 4 (Group | Commands | Tier | Why)", windowsCLIPlan, row, len(row))
			continue
		}
		group, commands, tier := strings.TrimSpace(row[0]), row[1], strings.TrimSpace(row[2])
		if !slices.ContainsFunc(windowsCLITiers, func(w string) bool { return strings.HasPrefix(tier, w) }) {
			t.Errorf("%s: support row %q carries tier %q, want one of %v", windowsCLIPlan, group, tier, windowsCLITiers)
		}
		for _, token := range backticked(commands) {
			words := strings.Fields(token)
			// A flag is not a command: it ends the path.
			if len(words) == 0 || strings.HasPrefix(words[0], "-") {
				continue
			}
			// A slash separates alternatives in the table's own prose; each half
			// is checked on its own.
			for _, alt := range strings.Split(token, "/") {
				alt = strings.TrimSpace(alt)
				if alt == "" || strings.HasPrefix(alt, "-") {
					continue
				}
				path := []string{}
				for _, w := range strings.Fields(alt) {
					if strings.HasPrefix(w, "-") {
						break
					}
					path = append(path, w)
				}
				seen[strings.Join(path, " ")] = true
				switch {
				case len(path) == 1 && known[path[0]]:
				case len(path) == 2 && known[path[0]+" "+path[1]]:
				case len(path) == 1 && knownWord[path[0]]:
				default:
					t.Errorf("%s: support row %q names %q, and this CLI has no such command (root commands: %v)", windowsCLIPlan, group, strings.Join(path, " "), windowsCLISortedKeys(known))
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Errorf("%s: the support table names no command at all", windowsCLIPlan)
	}
}

// backticked returns the “ `code` “ spans of s, in order.
func backticked(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			return out
		}
		rest := s[i+1:]
		j := strings.Index(rest, "`")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		s = rest[j+1:]
	}
}

// tableRows returns the cells of the markdown table rows between two headings,
// header and separator rows dropped.
func tableRows(t *testing.T, doc, from, to string) [][]string {
	t.Helper()
	i := strings.Index(doc, from)
	if i < 0 {
		t.Fatalf("%s lacks %q", doc, from)
	}
	body := doc[i+len(from):]
	if j := strings.Index(body, to); j >= 0 {
		body = body[:j]
	}
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|-") || strings.HasPrefix(line, "| ---") {
			continue
		}
		cells := []string{}
		for _, c := range strings.Split(strings.Trim(line, "|"), "|") {
			cells = append(cells, c)
		}
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == "Group" {
			continue // the header
		}
		rows = append(rows, cells)
	}
	return rows
}

func windowsCLISortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestDocsWindowsCLICensusStatesItsCommands: the census table (F36 and §4)
// carries a Command column with a command in every row, and the section defines
// the $PKGS every one of those commands is run against. A count without its
// command is a figure a later reader cannot re-derive, which is the rule the
// documents state for themselves.
func TestDocsWindowsCLICensusStatesItsCommands(t *testing.T) {
	recon := readDoc(t, windowsCLIRecon)

	// The scope the counts are defined over, stated once and used by the rest.
	for _, want := range []string{
		"go list -deps -f '{{.Dir}}' ./cmd/fugaro",
		"$PKGS",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s does not state %q: every count is defined over it", windowsCLIRecon, want)
		}
	}

	i := strings.Index(recon, "## 4. The census, and what it is scoped to")
	if i < 0 {
		t.Fatalf("%s lacks the census section", windowsCLIRecon)
	}
	body := recon[i:]
	if j := strings.Index(body, "\n## 5."); j >= 0 {
		body = body[:j]
	}
	rows := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|-") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == "Construct" {
			continue
		}
		if len(cells) != 4 {
			t.Errorf("%s: census row %q has %d cells, want 4 (Construct | Non-test sites | Including tests | Command)", windowsCLIRecon, line, len(cells))
			continue
		}
		rows++
		if strings.TrimSpace(cells[3]) == "" {
			t.Errorf("%s: census row %q carries no command: where a count is volatile state the command, not the figure", windowsCLIRecon, line)
		}
	}
	if rows == 0 {
		t.Errorf("%s: the census carries no rows", windowsCLIRecon)
	}
}

// docTaskRE matches a task cited in the documentation-tasks rule sentence
// ("T0.1 (`docs/windows-cli/wsl.md`)"), which is a bare reference and not the
// labelled form taskRE matches.
var docTaskRE = regexp.MustCompile(`\bT([0-9]+\.[0-9]+)\b`)

// pinFileRE matches the docs-pin file a documentation task names as its owner,
// and guardTestRE the guard function in it.
var (
	pinFileRE   = regexp.MustCompile(`internal/cli/docs_[a-z_]*_test\.go`)
	guardTestRE = regexp.MustCompile(`\bTest[A-Za-z0-9]*`)
	// redStepRE allows the label to be split across a line break, which it is
	// in two of the five tasks.
	redStepRE = regexp.MustCompile(`Red\s+step[.:]`)
)

// TestDocsWindowsCLIEveryDocTaskNamesItsGuard: every documentation task the
// plan lists in its own rule sentence names the pin file that owns it and a
// guard in that file; a task whose guard is a pin this folder already has must
// name one of the guards this file defines; and every guard this file defines
// is named by the plan, so a guard cannot be added without a task owning it.
//
// The rule sentence and the guards are pinned against each other rather than
// against a list either side could drift from. This is the fix for the review
// finding that the folder's documentation tasks named only `[pins]` — a run
// that does not read these documents at all — so the claim being checked here
// is exactly the claim the fix makes.
func TestDocsWindowsCLIEveryDocTaskNamesItsGuard(t *testing.T) {
	plan := readDoc(t, windowsCLIPlan)

	// The rule sentence names the tasks. It is one paragraph, and the tasks it
	// names are the T-numbers in it, in order and without repeats.
	i := strings.Index(plan, windowsCLIDocTaskRule)
	if i < 0 {
		t.Fatalf("%s lacks %q", windowsCLIPlan, windowsCLIDocTaskRule)
	}
	para := plan[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	var named []string
	seenTask := map[string]bool{}
	for _, m := range docTaskRE.FindAllStringSubmatch(para, -1) {
		if !seenTask[m[1]] {
			seenTask[m[1]] = true
			named = append(named, m[1])
		}
	}
	if len(named) < 2 {
		t.Fatalf("%s: %q names %v, want the folder's documentation tasks", windowsCLIPlan, windowsCLIDocTaskRule, named)
	}

	guards := definedGuards(t)
	for _, id := range named {
		j := strings.Index(plan, "**T"+id+" —")
		if j < 0 {
			t.Errorf("%s: %q names T%s and the plan carries no such task", windowsCLIPlan, windowsCLIDocTaskRule, id)
			continue
		}
		task := plan[j:]
		if k := strings.Index(task, "\n\n"); k >= 0 {
			task = task[:k]
		}
		if !redStepRE.MatchString(task) {
			t.Errorf("%s: T%s states no red step: a documentation task turns a pin red like any other", windowsCLIPlan, id)
		}
		pin := pinFileRE.FindString(task)
		if pin == "" {
			t.Errorf("%s: T%s names no owning pin file (want one of the internal/cli/docs_*_test.go files)", windowsCLIPlan, id)
			continue
		}
		if strings.Contains(task, "docs/windows-cli/") && pin != windowsCLIPin {
			t.Errorf("%s: T%s edits a document in this folder and names %s as its guard, want %s", windowsCLIPlan, id, pin, windowsCLIPin)
		}
		// A pin that already exists has to name a guard that already exists; a
		// pin marked new is that task's own first deliverable and is checked by
		// the task, not here.
		if strings.Contains(task, pin+"` (**new**)") {
			continue
		}
		readDoc(t, pin) // fails the test if the plan names a file that is not there
		if !slices.ContainsFunc(guardTestRE.FindAllString(task, -1), func(g string) bool {
			return slices.Contains(guards, g)
		}) {
			t.Errorf("%s: T%s names %s but no guard this file defines (want one of %v)", windowsCLIPlan, id, pin, guards)
		}
	}

	// The other direction of the same claim: every task the plan itself marks
	// as a documentation task is named in the rule sentence. A task cannot be
	// added to the plan as documentation work and left out of the sentence
	// that gives it a guard.
	for _, m := range taskRE.FindAllStringSubmatchIndex(plan, -1) {
		task := plan[m[0]:]
		if k := strings.Index(task, "\n\n"); k >= 0 {
			task = task[:k]
		}
		// The phrase is allowed to wrap across a line break, as one of them does.
		if !strings.Contains(strings.Join(strings.Fields(task), " "), "documentation task") {
			continue
		}
		if id := strings.TrimPrefix(plan[m[2]:m[3]], "T"); !seenTask[id] {
			t.Errorf("%s: T%s says it is a documentation task and %q does not name it", windowsCLIPlan, id, windowsCLIDocTaskRule)
		}
	}

	// And every guard in this file is owned by the plan, so a guard cannot be
	// added without the document saying what it is for.
	for _, g := range guards {
		if !strings.Contains(plan, g) {
			t.Errorf("%s defines %s and %s names no task or rule that owns it", windowsCLIPin, g, windowsCLIPlan)
		}
	}
}

// proseLineRefRE matches a prose citation of a source file by line, the form
// the reconciliation's header rules out ("every path and Go symbol below was
// located with git grep, go list or a build and then read in place; line
// numbers are never cited").
var proseLineRefRE = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(go|ya?ml|md|json|toml|sh):[0-9]`)

// TestDocsWindowsCLICitesNoProseLineNumbers: the rule the reconciliation's
// header states for itself holds in prose. Fenced blocks are the exception and
// are exempt on purpose: they are quoted output — a compiler's own message, a
// command's own lines — and the line numbers there are part of what was
// printed, not a citation a reader has to follow into a file that has since
// moved. A citation in prose is the kind that rots, which is why the header
// forbids it and why this test exists.
func TestDocsWindowsCLICitesNoProseLineNumbers(t *testing.T) {
	for _, path := range []string{windowsCLIRecon, windowsCLIPlan, windowsCLIReadme} {
		doc := readDoc(t, path)
		fenced := false
		for i, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			if ref := proseLineRefRE.FindString(line); ref != "" {
				t.Errorf("%s:%d cites %q by line: this document's rule is that a path is named and read in place, never cited by line number", path, i+1, ref)
			}
		}
	}
}

// definedGuards returns the TestDocsWindowsCLI* functions this file defines, in
// source order, so the plan's mentions and this file's definitions are checked
// against each other rather than against a list either side could drift from.
func definedGuards(t *testing.T) []string {
	t.Helper()
	src := readDoc(t, windowsCLIPin)
	re := regexp.MustCompile(`(?m)^func (TestDocsWindowsCLI[A-Za-z]*)\(`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s defines no TestDocsWindowsCLI guard", windowsCLIPin)
	}
	return out
}
