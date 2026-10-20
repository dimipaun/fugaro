package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPlanTestListsNamesOnlyTestsThatExist holds a task's **Tests.** list in
// docs/credentials-service/implementation-plan.md against the code.
//
// The list is what a reviewer of the task checks the work against, and nothing
// else in this repository reads it: every other doc guard in this package
// asserts a *phrase*, so a test renamed in the code leaves the plan naming one
// that does not exist and every guard stays green. That drift is not
// hypothetical — C1.2's list named TestPolicyCredentialsIgnoredByVersion1Reader
// while the implemented test is TestPolicyCredentialsCarryNoVersionBump, whose
// own comment records the rename; the re-review of C1.9 found it, and the same
// kind of drift was repaired by hand in C1.9's list before that.
//
// So this test reads the names out of a task's list and asks the tree whether
// each one is declared. It deliberately covers the tasks whose tests are
// *written* — C0.6, C1.2, C1.3 and C1.9 on this branch. The rest of the plan's
// lists name tests that do not exist yet and are not meant to (C1.1's
// TestValidateAcceptsCascade is one) because their tasks are unimplemented, so a
// general sweep over every list would fail by design; each task's list joins
// this test as its task lands. C1.9 is the task this branch implements, so its
// list — including the eight guards the re-review added — was the one left free
// to drift, which is the failure above.
//
// The count in the table is the number of names the list holds today, so a name
// quietly dropped from a list is an edit here too: a list that shrinks without
// saying so is the same drift as a list that names something that is not there.
//
// Mutations (both verified, run and restored): put C1.2's list back to
// TestPolicyCredentialsIgnoredByVersion1Reader — this test fails, naming the
// test that does not exist and the task; and drop C1.9's entry from the table
// below — renaming one of C1.9's eighteen names in the plan then goes
// unnoticed, which is what the entry is for.
func TestPlanTestListsNamesOnlyTestsThatExist(t *testing.T) {
	const plan = "docs/credentials-service/implementation-plan.md"

	// One entry per task whose tests are written: the heading that starts its
	// section, and how many names its list holds. All eighteen of C1.9's names
	// resolve in the tree today, its own eight included.
	for _, task := range []struct {
		heading string
		names   int
	}{
		{"### Task C0.6:", 4},
		{"### Task C1.2:", 8},
		{"### Task C1.3:", 14},
		{"### Task C1.9:", 18},
	} {
		section := taskSection(t, plan, task.heading)
		tests := testsLine(t, plan, task.heading, section)
		names := backtickedTestNames(tests)
		if len(names) != task.names {
			t.Errorf("%s: %s's **Tests.** list names %d tests and the table here says %d: the list is what a reviewer of the task checks the work against, so a name that left it is a deliberate edit — change the table with it", plan, task.heading, len(names), task.names)
			continue
		}
		inTree := testFuncsInTree(t)
		for _, name := range names {
			if _, ok := inTree[name]; !ok {
				t.Errorf("%s: %s's **Tests.** list names %s, which no _test.go in this tree declares: the plan and the code have drifted apart, and one of the two is wrong about what the task's suite is", plan, task.heading, name)
			}
		}
	}
}

// testsLine is one task section's **Tests.** sentence, which is where its list
// of test names lives.
func testsLine(t *testing.T, plan, heading, section string) string {
	t.Helper()
	const open, close = "**Tests.**", "**Red step.**"
	i := strings.Index(section, open)
	if i < 0 {
		t.Fatalf("%s: %s has no %q sentence, so its list of tests is gone", plan, heading, open)
	}
	rest := section[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s: %s's %q sentence does not end at %q, so the syntax of the plan has moved and this guard is reading prose as a list", plan, heading, open, close)
	}
	return rest[:j]
}

// backtickedTestNames is every `Test…` name a plan sentence spells.
func backtickedTestNames(s string) []string {
	var names []string
	for _, m := range regexp.MustCompile("`(Test[A-Za-z0-9_]*)`").FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names
}

// testFuncsInTree maps every test function name the tree declares to the file
// that declares it. It is a plain text scan and not a parse: a test function is
// a top-level `func TestX(t *testing.T)`, and this asks only whether the *same
// name* exists somewhere, which is the claim the plan makes.
func testFuncsInTree(t *testing.T) map[string]string {
	t.Helper()
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	found := map[string]string{}
	err := filepath.WalkDir("../../", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range decl.FindAllSubmatch(data, -1) {
			if _, seen := found[string(m[1])]; !seen {
				found[string(m[1])] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking this tree for the test functions the plan names: %v", err)
	}
	// A scan that read nothing would find no name at all and so fail the plan's
	// every name loudly; what it would NOT catch is a walk that stopped early
	// and a plan that named only tests from the part it read. This floor is that
	// half: the tree declares thousands of test functions, so a scan that comes
	// back with a handful is not reading the source it is meant to and must not
	// be trusted to say a name is missing.
	if len(found) < 1000 {
		t.Fatalf("the scan found %d test functions in this tree, and it declares thousands: the walk is not reading the source it is meant to", len(found))
	}
	return found
}
