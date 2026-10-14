package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
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
