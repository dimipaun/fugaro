package main

// The core's half of the CLI reference's drift check (website plan, Task W0.4).
//
// website/content/cli-reference.md is generated from the *tagged* command tree
// by `go run ./ee/clidoc`, because one binary ships and the page describes that
// binary (design docs/design/ee-boundary-moves.md §9). The core cannot render
// that tree -- the registrations live under ee/, and nothing outside ee/ may
// import them -- so the whole-file check lives with the tagged generator
// (ee/clidoc/cliref_test.go's TestCliReferenceIsCurrent) and this is the
// subset half: every command and visible flag the core's own tree documents
// appears in the file. The file may hold more than that, and does as soon as a
// paid command moves out of the core: those sections are the tagged tree's, and
// this test must not call them invented.
//
// The generator's own rules are beside the generator in
// internal/clidoc/cliref/cliref_test.go.

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/clidoc/cliref"
)

// coreRefPath is the generated reference, relative to the repository root.
const coreRefPath = "website/content/cli-reference.md"

// coreRefHeadingRE matches the one section-heading shape the generator writes:
// a command's full path, backticked, as an H2. Nothing else in the file is an
// H2.
var coreRefHeadingRE = regexp.MustCompile("^## `(.+)`$")

// coreRefFlagCellRE matches a flag-table row's first cell, "| `--name` |" or
// "| `-h`, `--help` |": the only place a flag is a fact rather than prose. A
// flag named inside a command's long description is read as text, not as a
// documented flag.
var coreRefFlagCellRE = regexp.MustCompile("^\\| (`-{1,2}[A-Za-z0-9][A-Za-z0-9-]*`[^|]*?) \\|")

// coreRefFlagTokenRE finds the flag tokens of a flag-table row's first cell.
var coreRefFlagTokenRE = regexp.MustCompile("`(-{1,2}[A-Za-z0-9][A-Za-z0-9-]*)`")

// coreRefSections splits the reference into its command sections, keyed by the
// command path in the heading, with every line below the heading up to the next
// one.
func coreRefSections(doc string) map[string][]string {
	sections := map[string][]string{}
	var current string
	for _, line := range strings.Split(doc, "\n") {
		if m := coreRefHeadingRE.FindStringSubmatch(line); m != nil {
			if _, seen := sections[m[1]]; !seen {
				sections[m[1]] = nil
			}
			current = m[1]
			continue
		}
		if current != "" {
			sections[current] = append(sections[current], line)
		}
	}
	return sections
}

// coreRefFlagRows returns the long flag names a section's flag table names,
// stripped of the dashes a reader types (pflag's own names carry none).
func coreRefFlagRows(lines []string) []string {
	var names []string
	for _, line := range lines {
		m := coreRefFlagCellRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, tok := range coreRefFlagTokenRE.FindAllStringSubmatch(m[1], -1) {
			if strings.HasPrefix(tok[1], "--") {
				names = append(names, tok[1][2:])
			}
		}
	}
	return names
}

// coreRefVisibleFlagNames returns a command's own flags a reader may be told
// about: local and non-inherited, so a flag is expected in the section of the
// command that defines it, with the hidden ones left out.
func coreRefVisibleFlagNames(c *cobra.Command) []string {
	var names []string
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			names = append(names, f.Name)
		}
	})
	slices.Sort(names)
	return names
}

// TestCoreCommandsAreInTheReference asserts the reference covers the core's
// command tree: every command cliref documents for the untagged tree, and every
// visible flag of each one, is in the file. It is a subset check by design --
// the file is the tagged tree's output and may carry the paid commands'
// sections too, which this test neither knows nor judges.
func TestCoreCommandsAreInTheReference(t *testing.T) {
	doc, err := os.ReadFile("../../" + coreRefPath)
	if err != nil {
		t.Fatalf("%s: %v (regenerate it with `go run ./ee/clidoc` at the repository root)", coreRefPath, err)
	}
	sections := coreRefSections(string(doc))
	root := cliref.Prepare(cli.NewRootCmd())
	documented := cliref.DocumentedCommands(root)
	if len(documented) == 0 {
		t.Fatal("cliref documents no command for the core tree, so an empty pass would prove nothing")
	}
	for path, cmd := range documented {
		lines, ok := sections[path]
		if !ok {
			t.Errorf("%s does not name command %q, which the core tree has and does not hide", coreRefPath, path)
			continue
		}
		names := coreRefFlagRows(lines)
		for _, name := range coreRefVisibleFlagNames(cmd) {
			if !slices.Contains(names, name) {
				t.Errorf("%s's %q section does not name the flag --%s, which the command has", coreRefPath, path, name)
			}
		}
	}
}
