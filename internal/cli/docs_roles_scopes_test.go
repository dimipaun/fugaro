package cli

// User roles and scopes (docs/roles-scopes): the folder that gives the roles
// design a scope — the project, all repos, or a list of patterns — and the
// resolver that decides who holds what where. RS1 starts this file, and what it
// holds is what the plan promises about itself: the number block it reserves
// (D1580–D1599, Checks 400–409), that every decision in it is answered rather
// than left open, and that the files and the tests RS1 names are really there.
// RS10 extends it with the stated-limit sentences the design has to keep in
// print ("a scoped role is a policy check, not a cloud barrier").
//
// The two guards that matter here are the block and the answers. The block,
// because a sibling plan holds D1540–D1579 and D1560–D1579 and a number that
// wandered into this folder's range would collide with them silently. The
// answers, because an answer is what makes a plan buildable: a decision left
// open is work nobody may start, and a row that quietly reads as settled
// without the owner's date on it is the one thing §4 says this folder is not.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const rolesScopesDir = "docs/roles-scopes/"

// rolesScopesDecisionRowRE matches one row of reconciliation.md's decisions
// table: its first cell is a decision number in bold. It is anchored on the
// closing pipe, so a number cited inside another row's prose is not a row of
// its own.
var rolesScopesDecisionRowRE = regexp.MustCompile(`(?m)^\| \*\*D([0-9]{4})\*\* \|`)

// rolesScopesDecision is one decision row of §3: the number without its `D`,
// and the owner's answer — the row's last cell, which is the column a reader
// checks to see whether the work may start.
type rolesScopesDecision struct {
	id     string
	answer string
}

// rolesScopesDecisions are the folder's decisions in the order §3 writes them.
// The list is here rather than derived so that a row that disappears, or two
// that swap places, is a failure and not a smaller table.
var rolesScopesDecisions = []string{
	"1580", "1581", "1582", "1583", "1584", "1585", "1586", "1587",
	"1588", "1589", "1590", "1591", "1592", "1593",
}

// The owner's answers of 2026-11-07, split by how each was answered: the six
// the owner ruled on, and the eight recorded as the recommendation with the
// reading delegated to the PR. Both are answered — neither is open — which is
// what README.md's "No decision is open." states, and the split is what a
// reader checking a claim against its ruling needs.
var (
	rolesScopesAnswered  = []string{"1580", "1581", "1582", "1583", "1585", "1591"}
	rolesScopesDelegated = []string{"1584", "1586", "1587", "1588", "1589", "1590", "1592", "1593"}
)

// rolesScopesOpenAnswer matches the words an unanswered decision uses. It is
// matched against the answer cell alone: §3's recommendation cells discuss
// what stays open to an operator, and a pin over the whole row would read those
// as an unanswered decision.
var rolesScopesOpenAnswer = regexp.MustCompile(`(?i)\bopen\b|UNVERIFIED`)

// rolesScopesDecisionRows reads §3's decisions table out of reconciliation.md:
// one entry per row whose first cell is a decision number, in the order the
// table writes them.
func rolesScopesDecisionRows(t *testing.T, doc string) []rolesScopesDecision {
	t.Helper()
	var out []rolesScopesDecision
	for _, line := range strings.Split(doc, "\n") {
		m := rolesScopesDecisionRowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// §3's rows are `| # | Question | Recommendation | Alternative |
		// Owner's answer |`, so the answer is the fifth cell. A row of another
		// shape would put a different cell there, and reading the last cell of
		// a row that is not this table is how a pin ends up checking the wrong
		// column.
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 5 {
			t.Errorf("%s: the row for D%s has %d cells, want 5 (`# | Question | Recommendation | Alternative | Owner's answer`)", rolesScopesDir+"reconciliation.md", m[1], len(cells))
			continue
		}
		out = append(out, rolesScopesDecision{id: m[1], answer: strings.TrimSpace(cells[4])})
	}
	return out
}

// TestDocsRolesScopesDecisionBlock: the folder's decision numbers are exactly
// D1580–D1593, in order, and both documents a reader might land on state the
// block the work reserves. Numbers outside it would collide with a sibling
// plan's, and a block stated in one document only is a reader's trap: README.md
// is where a newcomer starts, and reconciliation.md is where the table is.
func TestDocsRolesScopesDecisionBlock(t *testing.T) {
	rec := readDoc(t, rolesScopesDir+"reconciliation.md")
	for _, doc := range []struct{ path, text, want string }{
		{rolesScopesDir + "README.md", flattenDoc(readDoc(t, rolesScopesDir+"README.md")), "**Numbers reserved:** decisions D1580–D1599, checks 400–409."},
		{rolesScopesDir + "reconciliation.md", flattenDoc(rec), "**Numbers.** This work holds **D1580–D1599** and **Checks 400–409** and reserves all of them."},
	} {
		if !strings.Contains(doc.text, doc.want) {
			t.Errorf("%s no longer says %q: the block is what keeps this folder's numbers out of its siblings' (docs/no-prompt-policy holds D1540–D1559, docs/composite-commands D1560–D1579), and it is stated where a reader starts and where the table is", doc.path, doc.want)
		}
	}

	rows := rolesScopesDecisionRows(t, rec)
	if len(rows) == 0 {
		t.Fatalf("%s's decisions table has no rows whose first cell is a decision number: F1–F29 have consequences here, and a table that empty has settled them by omission", rolesScopesDir+"reconciliation.md")
	}
	for i, row := range rows {
		id, err := strconv.Atoi(row.id)
		if err != nil {
			t.Errorf("the row %q carries a decision number that is not a number", row.id)
			continue
		}
		if id < 1580 || id > 1599 {
			t.Errorf("decision D%s is outside the block this work reserves (D1580–D1599): a number outside it belongs to a sibling plan, and two works cannot hold one number", row.id)
		}
		if i < len(rolesScopesDecisions) && row.id != rolesScopesDecisions[i] {
			t.Errorf("§3 writes D%s where D%s belongs: the table is read top to bottom, and a row out of order hides the one it displaced", row.id, rolesScopesDecisions[i])
		}
	}
	if len(rows) != len(rolesScopesDecisions) {
		t.Errorf("§3 carries %d decision rows, want %d (D1580–D1593): a decision that left the table took its question with it, and the plan's tasks are written against these numbers", len(rows), len(rolesScopesDecisions))
	}

	if !strings.Contains(flattenDoc(readDoc(t, rolesScopesDir+"README.md")), "No decision is open.") {
		t.Errorf("%s no longer says %q: every task in the plan may start only once its decisions are answered, and that sentence is what a reader checks first", rolesScopesDir+"README.md", "No decision is open.")
	}
}

// TestDocsRolesScopesDecisionsAreAnswered: every decision row carries an answer
// — the owner's date, or the recommendation with the reading delegated to the
// PR — and no row's answer cell is open or unverified. The list of which is
// which is checked in both directions: a row that acquired an answer the owner
// did not give is the failure §4 exists to prevent, and one that lost its
// answer is a task nobody may start.
func TestDocsRolesScopesDecisionsAreAnswered(t *testing.T) {
	rows := rolesScopesDecisionRows(t, readDoc(t, rolesScopesDir+"reconciliation.md"))
	answered, delegated := map[string]bool{}, map[string]bool{}
	for _, id := range rolesScopesAnswered {
		answered[id] = true
	}
	for _, id := range rolesScopesDelegated {
		delegated[id] = true
	}

	seenAnswered, seenDelegated := 0, 0
	for _, row := range rows {
		if m := rolesScopesOpenAnswer.FindString(row.answer); m != "" {
			t.Errorf("D%s's answer cell says %q:\n\n%s\n\nthe owner answered every decision of this work on 2026-11-07 (D1584, D1586–D1590, D1592 and D1593 as the recommendation, with the reading delegated to the PR), so a row that reads as open or UNVERIFIED is a task whose first step nobody has taken", row.id, m, row.answer)
			continue
		}
		switch {
		case answered[row.id]:
			seenAnswered++
			if !strings.Contains(row.answer, "2026-11-07:") {
				t.Errorf("D%s's answer cell does not carry the owner's date %q:\n\n%s\n\nthe six the owner ruled on are the decisions this work is built on, and an answer without its date is one a reader cannot tell from the plan's own recommendation", row.id, "2026-11-07:", row.answer)
			}
		case delegated[row.id]:
			seenDelegated++
			if !strings.Contains(row.answer, "owner delegated reading to the PR") {
				t.Errorf("D%s's answer cell does not say %q:\n\n%s\n\nthe eight recorded as the recommendation are answered as such — the owner reads them in the pull request — and a cell that claims more than that is an answer the owner did not give", row.id, "owner delegated reading to the PR", row.answer)
			}
		default:
			t.Errorf("D%s is in neither list: %s names the decisions the owner ruled on and the ones delegated to the PR, and a row in neither is a decision whose status this file does not know", row.id, "docs_roles_scopes_test.go")
		}
	}
	if seenAnswered != len(rolesScopesAnswered) || seenDelegated != len(rolesScopesDelegated) {
		t.Errorf("§3 carries %d answered rows and %d delegated rows, want %d and %d: the two lists name every decision of the block, and one that lost its row is a decision the table no longer records", seenAnswered, seenDelegated, len(rolesScopesAnswered), len(rolesScopesDelegated))
	}
}

// rolesScopesCheckRowRE matches a row of the plan's Checks table: its first
// cell is a three-digit check number in bold.
var rolesScopesCheckRowRE = regexp.MustCompile(`(?m)^\| \*\*([0-9]{3})\*\* \|`)

// TestDocsRolesScopesChecksStayInTheReservedBlock: every check the plan's table
// registers is inside the block this work reserves (400–409), which the plan's
// own heading states. A number outside it is a check nobody allocated, and the
// register is a shared document: two works may not hold one check number.
//
// The rows themselves are not pinned: a later task of this plan registers its
// own check inside the block, and this guard is about the block, not about
// which of its numbers are taken today.
func TestDocsRolesScopesChecksStayInTheReservedBlock(t *testing.T) {
	plan := readDoc(t, rolesScopesDir+"implementation-plan.md")
	if !strings.Contains(flattenDoc(plan), "## Checks (400–409 reserved)") {
		t.Errorf("%s no longer heads its table %q: the checks it registers are its own block, and the heading is what says so", rolesScopesDir+"implementation-plan.md", "## Checks (400–409 reserved)")
	}

	numbers := rolesScopesCheckRowRE.FindAllStringSubmatch(plan, -1)
	if len(numbers) == 0 {
		t.Fatalf("%s registers no check: the plan's checks are the owner-run proof this work ships, and a table that registers none has lost them", rolesScopesDir+"implementation-plan.md")
	}
	registered := map[string]bool{}
	for _, m := range numbers {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Errorf("the plan's check row %q does not carry a number", m[1])
			continue
		}
		registered[m[1]] = true
		if n < 400 || n > 409 {
			t.Errorf("the plan registers Check %d, outside the block it reserves (400–409): a check number outside the block belongs to another work", n)
		}
	}
	for _, want := range []string{"400", "401", "402"} {
		if !registered[want] {
			t.Errorf("the plan no longer registers Check %s: the three are what this work's slices ship, and a row that left the table is a check nobody would run", want)
		}
	}
}

// rolesScopesDeclared is the name of every top-level type and function a Go
// file declares. It is read with the parser rather than searched for as text:
// a name a doc comment mentions is not a declaration, and RS1's promises are
// about what the package declares.
func rolesScopesDeclared(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../../"+path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					out[ts.Name.Name] = true
				}
			}
		}
	}
	return out
}

// TestDocsRolesScopesRS1sFilesAndTestsExist: the files RS1 names are in the
// tree, the package declares the interfaces the plan promises, the role type
// is one type rather than two, and every test RS1 and the plan's global
// constraints attribute to it is declared. A plan is a set of promises about
// code that does not exist yet; this is the half of it that is checkable, and
// the alias is the half that is not: F1 says the resolver adds no second role
// set, which is a claim about a definition rather than about a name.
func TestDocsRolesScopesRS1sFilesAndTestsExist(t *testing.T) {
	for _, path := range []string{
		"internal/access/access.go",
		"internal/access/scope.go",
		"internal/access/resolve.go",
		"internal/access/access_test.go",
		"internal/mobileauth/mobileauth.go",
		"internal/cli/docs_roles_scopes_test.go",
	} {
		if !pathExists(path) {
			t.Errorf("RS1 names %s and it is not in the tree: the task's first line is the files it writes, and a reader sent to a missing file has no way to tell whether the work landed somewhere else", path)
		}
	}

	for path, want := range map[string][]string{
		"internal/access/access.go":  {"Role", "Grant", "Member"},
		"internal/access/scope.go":   {"Scope", "ParseScope", "SubsetOf", "KnownMatches"},
		"internal/access/resolve.go": {"Input", "Effective", "Allowed", "Caller"},
	} {
		declared := rolesScopesDeclared(t, path)
		for _, name := range want {
			if !declared[name] {
				t.Errorf("%s declares no %s: RS1's Interfaces line names it, and every later task of the plan is written against those names", path, name)
			}
		}
	}

	mobileauth := readDoc(t, "internal/mobileauth/mobileauth.go")
	if !strings.Contains(mobileauth, "type Role = access.Role") {
		t.Errorf("internal/mobileauth/mobileauth.go does not carry `type Role = access.Role`: the device node's role and a grant's role are one closed set with one parser and one rank order (F1), which is an alias rather than a second definition")
	}
	if strings.Contains(mobileauth, "type Role string") {
		t.Errorf("internal/mobileauth/mobileauth.go declares `type Role string` again: two definitions of the role set is the second vocabulary F1 exists to prevent, and a value built from one would no longer be assignable to the other")
	}

	// RS1's Tests line and the rows of the plan's global-constraints table that
	// name RS1, together: every failure mode the plan attributes to this slice
	// has a test, and every test the plan names is one a reviewer can run.
	declaredTests := rolesScopesDeclared(t, "internal/access/access_test.go")
	for _, name := range []string{
		"TestHighestRoleWins",
		"TestProjectScopeOnlyForProjectActions",
		"TestAllReposCoversLaterEnrolled",
		"TestPatternStarStaysInsideASegment",
		"TestBareStarIsRefusedAsScopedPattern",
		"TestKeywordsAreNeverRepositories",
		"TestPatternsAreCanonicalised",
		"TestGrantMustBeSubsetOfGrantorScope",
		"TestSubsetHostileCases",
		"TestAllReposAdminCannotGrantBareStar",
		"TestUnparseableGrantDeniesEverything",
		"TestCallerIsTheCredentialNotGitConfig",
	} {
		if !declaredTests[name] {
			t.Errorf("internal/access/access_test.go declares no %s: RS1 names it as the test that holds one of the failure modes the plan lists, and a slice whose named test is missing has a finding nothing pins", name)
		}
	}
}
