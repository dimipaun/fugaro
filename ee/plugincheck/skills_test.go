// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

// Package plugincheck holds the checks that read the tagged command tree: the
// tree a released binary builds, the core's commands plus the ones the owner
// moved out of the core registered back into it (design
// docs/design/ee-boundary-moves.md §9, D536). The core's own skill checks
// (plugin/*_test.go) accept a paid command by name alone, because the core tree
// answers to it with the stub a build without that code shows, and the stub
// parses no flags; the flags those commands carry are checked here, against the
// tree that has them. The blank import of ee/register is what makes this tree
// the tagged one: its init is the only place the seams that put the moved
// commands back are set, so a test binary that links it is what a tagged build
// is (design §5.5).
package plugincheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	_ "github.com/dimipaun/fugaro/ee/register"
	"github.com/dimipaun/fugaro/internal/cli"
)

var (
	// shellSplitRE / shellNoiseRE take a skill line apart the way a shell
	// would: the noise is the markup a skill wraps a command in (backticks,
	// quotes, brackets), and a separator ends the command so a flag of another
	// tool in a pipeline is not read as this command's.
	shellSplitRE = regexp.MustCompile(`&&|\|\||[;|]`)
	shellNoiseRE = strings.NewReplacer("`", " ", "'", " ", `"`, " ", "(", " ", ")", " ", ",", " ", "[", " ", "]", " ", "{", " ", "}", " ")
	// flagWordRE is a flag as a skill writes one, with or without its value.
	flagWordRE = regexp.MustCompile(`^--?[a-zA-Z][a-zA-Z0-9-]*(=.*)?$`)
)

// skillLine is one place a skill runs a paid command: the command path it
// spells and every flag on that line.
type skillLine struct {
	line  int
	path  []string
	flags []string
}

// paidSkillLines returns every place text runs a paid command. A backquoted
// mention in prose and the same command in a fenced block read alike: both are
// lines a user's agent may copy.
func paidSkillLines(text string) []skillLine {
	var out []skillLine
	for i, raw := range strings.Split(text, "\n") {
		for _, seg := range shellSplitRE.Split(shellNoiseRE.Replace(raw), -1) {
			words := strings.Fields(seg)
			j := slices.Index(words, "fugaro")
			if j < 0 {
				continue
			}
			path, flags := paidPathAndFlags(words[j+1:])
			if len(path) == 0 {
				continue
			}
			out = append(out, skillLine{line: i + 1, path: path, flags: flags})
		}
	}
	return out
}

// paidPathAndFlags splits the words after `fugaro` into the command path, which
// ends at the first flag, and the flags on the line — but only when the path's
// first word is a paid name. A core command's flags are the core's own check
// (plugin/skills_lint_test.go), which reads them off the tree that carries
// them.
func paidPathAndFlags(words []string) (path, flags []string) {
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			break
		}
		path = append(path, w)
	}
	if len(path) == 0 || !slices.Contains(cli.PaidCommandNames(), path[0]) {
		return nil, nil
	}
	for _, w := range words {
		if flagWordRE.MatchString(w) {
			flags = append(flags, w)
		}
	}
	return path, flags
}

// paidFlagFindings reports every paid command a skill names that the tagged
// tree does not answer to, and every flag it names that the command it resolved
// to does not carry there, with the number of paid command lines it read.
func paidFlagFindings(root *cobra.Command, dir string) (findings []string, lines int, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		for _, l := range paidSkillLines(string(data)) {
			lines++
			cmd := commandAt(root, l.path)
			if cmd == nil {
				findings = append(findings, fmt.Sprintf("%s:%d: `fugaro %s` does not resolve in the tagged tree", rel, l.line, strings.Join(l.path, " ")))
				continue
			}
			for _, f := range l.flags {
				name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
				if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
					findings = append(findings, fmt.Sprintf("%s:%d: `fugaro %s %s`: %s has no --%s flag in the tagged tree", rel, l.line, strings.Join(l.path, " "), f, cmd.CommandPath(), name))
				}
			}
		}
		return nil
	})
	return findings, lines, err
}

// commandAt is the command path in root, or nil when the tree does not answer
// to it. The path is walked word by word rather than handed to cobra's Find,
// which answers the deepest command it found: a word the tree has no child for
// is what this refuses.
func commandAt(root *cobra.Command, path []string) *cobra.Command {
	cmd := root
	for _, w := range path {
		var next *cobra.Command
		for _, sub := range cmd.Commands() {
			if sub.Name() == w || slices.Contains(sub.Aliases, w) {
				next = sub
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	if cmd == root {
		return nil
	}
	return cmd
}

// TestSkillsNamePaidFlagsThatExist: every `fugaro <paid-name> [sub…] --flag` a
// skill under plugin/skills names resolves in the tagged tree and the flag is
// one that command carries there. The core's skill checks hand a paid name a
// pass on its flags — the core tree holds only the stub after the move, and the
// stub parses none — so without this check a skill could name `fugaro report
// --typo` and nothing would notice until a user's agent copied the line.
func TestSkillsNamePaidFlagsThatExist(t *testing.T) {
	root := cli.NewRootCmd()
	dir := filepath.Join(moduleRoot(t), "plugin", "skills")
	findings, lines, err := paidFlagFindings(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The floor: the working skill names `fugaro report --by repo`, so a parse
	// that read no paid command line would pass the checks below by reading
	// nothing.
	if lines == 0 {
		t.Fatal("no skill names a paid command, and plugin/skills/working/SKILL.md names `fugaro report --by repo`: a scan that found none is reading files the skills moved out from under")
	}
	for _, f := range findings {
		t.Errorf("%s: a skill names a paid command, and the tagged tree is the tree that has to answer for it (D536)", f)
	}

	// The planted line: a flag no command carries is caught here. Without it
	// the check could pass by never comparing a flag with the tree.
	planted := t.TempDir()
	writeSkill(t, planted, "fugaro report --no-such-flag")
	findings, lines, err = paidFlagFindings(root, planted)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 1 || len(findings) != 1 || !strings.Contains(findings[0], "--no-such-flag") {
		t.Errorf("a skill naming `fugaro report --no-such-flag` read %d paid line(s) and answered %q, want the one missing flag reported: this is the planted case, and a check that misses it misses a real skill's typo too", lines, findings)
	}
}

// moduleRoot walks up from the test's own directory to the one holding go.mod,
// so plugin/skills is found by the repository's layout rather than by a path
// written down here.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: this check finds plugin/skills from the module root", dir)
		}
		dir = parent
	}
}

// writeSkill writes one skill file into dir.
func writeSkill(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
