package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// docsProjectLayerFuture lists the one command docs/project-layer.md
// deliberately names as not yet built (section 10, "Not yet"): a reference
// to real work that does not exist yet, not a typo to flag.
var docsProjectLayerFuture = map[string]bool{"config extract": true}

// cmdHasFlag reports whether c, or an ancestor of c (a persistent flag),
// declares a flag named name.
func cmdHasFlag(c *cobra.Command, name string) bool {
	for cur := c; cur != nil; cur = cur.Parent() {
		if cur.Flags().Lookup(name) != nil || cur.PersistentFlags().Lookup(name) != nil {
			return true
		}
	}
	return false
}

// TestDocsProjectLayerCommandsExist keeps every `fugaro <command...>` and
// every --flag docs/project-layer.md shows real: the command must resolve
// in the cobra tree, and the flag must belong to that command or one of
// its ancestors. Renaming or dropping a flag or a subcommand that the doc
// still names fails this test, instead of leaving the doc describing a
// command line nobody can actually run.
func TestDocsProjectLayerCommandsExist(t *testing.T) {
	data, err := os.ReadFile("../../docs/project-layer.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	root := NewRootCmd()

	wordRE := regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	cmdRE := regexp.MustCompile("`(fugaro [^`]*)`")

	for _, m := range cmdRE.FindAllStringSubmatch(doc, -1) {
		fields := strings.Fields(m[1])
		var path []string
		for _, f := range fields[1:] { // fields[0] is "fugaro"
			if !wordRE.MatchString(f) {
				break
			}
			path = append(path, f)
		}
		if len(path) == 0 || docsProjectLayerFuture[strings.Join(path, " ")] {
			continue
		}
		c, _, err := root.Find(path)
		if err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("docs/project-layer.md names %q, which is not a command", m[1])
			continue
		}
		for _, fm := range flagRE.FindAllStringSubmatch(m[1], -1) {
			if !cmdHasFlag(c, fm[1]) {
				t.Errorf("docs/project-layer.md's %q names --%s, which fugaro %s has no such flag", m[1], fm[1], strings.Join(path, " "))
			}
		}
	}
}
