package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestDocsCheckoutCommandsExist keeps every `fugaro <command...>` and every
// --flag docs/checkout.md shows real: the command must resolve in the cobra
// tree, and the flag must belong to that command or one of its ancestors.
// Renaming or dropping a flag or a subcommand that the doc still names fails
// this test, instead of leaving the doc describing a command line nobody can
// actually run.
func TestDocsCheckoutCommandsExist(t *testing.T) {
	data, err := os.ReadFile("../../docs/checkout.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	root := NewRootCmd()

	wordRE := regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	cmdRE := regexp.MustCompile("`(fugaro [^`]*)`")

	found := 0
	for _, m := range cmdRE.FindAllStringSubmatch(doc, -1) {
		fields := strings.Fields(m[1])
		var path []string
		for _, f := range fields[1:] { // fields[0] is "fugaro"
			if !wordRE.MatchString(f) {
				break
			}
			path = append(path, f)
		}
		if len(path) == 0 {
			continue
		}
		found++
		c, _, err := root.Find(path)
		if err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("docs/checkout.md names %q, which is not a command", m[1])
			continue
		}
		for _, fm := range flagRE.FindAllStringSubmatch(m[1], -1) {
			if !cmdHasFlag(c, fm[1]) {
				t.Errorf("docs/checkout.md's %q names --%s, which fugaro %s has no such flag", m[1], fm[1], strings.Join(path, " "))
			}
		}
	}
	if found == 0 {
		t.Error("docs/checkout.md names no fugaro command at all")
	}
}

// docsCheckoutDoc is the user-facing doc under test.
func docsCheckoutDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../docs/checkout.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDocsCheckoutMiseConfigsAreListed (review M6): the doc enumerates the
// mise config files a clone job's provision stage installs from, and that
// list has to be the one the code actually reads — a short list sends a
// reader to restructure a repository into a file nothing installs, and a
// long one promises an install that never happens.
func TestDocsCheckoutMiseConfigsAreListed(t *testing.T) {
	doc := docsCheckoutDoc(t)
	for _, path := range append(append([]string{}, config.MiseConfigPaths...), config.MiseConfigDirs...) {
		if !strings.Contains(doc, path) {
			t.Errorf("docs/checkout.md does not name %s, which config.MiseConfigFiles reads", path)
		}
	}
	// The build's own final check is what provision repeats; the doc says so,
	// because it is the difference between "mise install ran" and "the tools
	// are there".
	if !strings.Contains(doc, "mise ls --current --missing") {
		t.Error("docs/checkout.md does not mention the `mise ls --current --missing` check provision makes")
	}
}

// TestDocsCheckoutSaysWhatOldRevisionsCost (review N1, M2, L1): a clone
// job's checkout holds the checked-out revision's file contents and the ones
// the runner prefetches for the agent's reads — no more — and only a
// follow-up's and a review run's agent is without the git credentials to
// fetch the rest on a private repository. The doc has to say which commands
// are covered and which are not, so a reader is neither sent to `git blame`
// on a checkout that cannot answer it nor left expecting `git show HEAD` to
// fail, and it must not claim the modes work like `baked`.
func TestDocsCheckoutSaysWhatOldRevisionsCost(t *testing.T) {
	doc := docsCheckoutDoc(t)
	// The reads the runner prefetches for: the branch's diff, its own
	// commits, and the diff against the base tip.
	for _, cmd := range []string{"git log -p", "git show", "git diff origin/<base>"} {
		if !strings.Contains(doc, cmd) {
			t.Errorf("docs/checkout.md does not name %s among what the runner fetches for the agent", cmd)
		}
	}
	for _, cmd := range []string{"git blame", "git show <old-revision>:<path>"} {
		if !strings.Contains(doc, cmd) {
			t.Errorf("docs/checkout.md does not name %s among what a clone's checkout cannot fetch", cmd)
		}
	}
	if strings.Contains(doc, "work the same way") {
		t.Error("docs/checkout.md claims checkout: clone works the same way as baked; it does not, for anything the runner did not prefetch")
	}
	// L1: the credential claim is the follow-up's and the review run's, not
	// every mode's — a first run's agent is given the run's git credentials.
	if strings.Contains(doc, "in every mode") {
		t.Error("docs/checkout.md claims the agent has no git credentials in every mode; a first run's agent has them")
	}
}

// TestDocsCheckoutSaysWhetherCloneIsShipped (review M6/H2): the doc's claim
// about the clone gate has to match the gate. cloneShipped
// (`internal/cli/recipes_skew.go`) has been true since 0.9.0 — a clone job
// can be deployed and a clone run launches — so a doc that still tells the
// reader the option is not shipped sends them away from something that
// works, which is the mirror image of the failure this test was written
// for (a doc promising a switch the gate refuses). Flipping the gate back
// fails it until the doc says so too.
//
// Mutation (run, restore): set cloneShipped back to false in
// recipes_skew.go, or drop the "is shipped" sentence from docs/checkout.md
// — the test fails on either, which is the point: the two have to move
// together.
func TestDocsCheckoutSaysWhetherCloneIsShipped(t *testing.T) {
	doc := docsCheckoutDoc(t)
	saysShipped := strings.Contains(doc, "**`checkout: clone` is shipped**")
	if saysShipped != cloneShipped {
		t.Fatalf("docs/checkout.md says clone is shipped = %v, but cloneShipped = %v", saysShipped, cloneShipped)
	}
	if strings.Contains(doc, "is not shipped yet") {
		t.Error("docs/checkout.md still says checkout: clone is not shipped yet")
	}
}
