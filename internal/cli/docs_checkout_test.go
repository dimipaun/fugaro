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

// TestDocsCheckoutSaysWhetherCloneIsShipped (review M6/H2): the doc's claim
// about the clone gate has to match the gate. While cloneShipped is false
// (`internal/cli/recipes_skew.go`) no clone job can be deployed — `fugaro
// init --repo` and `fugaro image refresh` refuse — so a doc that describes
// the switch as something a user can do today is wrong. When Task 18 flips
// the gate, this fails until the doc says so too.
func TestDocsCheckoutSaysWhetherCloneIsShipped(t *testing.T) {
	doc := docsCheckoutDoc(t)
	saysNotShipped := strings.Contains(doc, "is not shipped yet")
	if saysNotShipped == cloneShipped {
		t.Fatalf("docs/checkout.md says clone is not shipped = %v, but cloneShipped = %v", saysNotShipped, cloneShipped)
	}
}
