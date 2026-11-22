package cli

// apply_enforce_test.go is AP9's half of the composite design
// (docs/composite-commands/implementation-plan.md, the section "AP9:
// enforcement"): the two checks that keep the rule true now that the tasks that
// built it have landed.
//
// The first is the census of the commands themselves. management_commands.golden
// holds every command D1566 calls a management command, with the change it
// makes, the steps the table declares for that change, and what a run of the
// command without --no-apply ends on in this package's own fakes — and the
// commands D1566 leaves out, with the cloud write that is why. The test runs
// each of them: the steps the change owes run in process, the run files nothing
// for `fugaro apply`, and the state the command's own report claims is read off
// the cloud fake the step wrote into and never off the absence of a line.
//
// The second is the text lint. No message of this package hands one of the
// commands F11's audit names — `fugaro update`, `fugaro config publish`,
// `fugaro project policy publish`, `fugaro image build`, `fugaro image refresh`
// — to the reader as a step of their own. It is F11's own file set (the
// package's non-test sources) and F11's own grep, widened to the shapes the
// audit names by hand: a directive ("next: fugaro update", "run `fugaro image
// build`") and a claim that the command carries the change the message is about
// ("`fugaro update` publishes it"). What the audit left is recorded in
// testdata/next_step_allow.golden, and the ratchet is strict in both
// directions: a phrasing the tree says that the file does not list fails, and a
// line for a phrasing the tree has moved past fails too.

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// cliPackageDir is this package's own directory, absolute. It is resolved once
// because the rigs move the process's working directory into a checkout
// (t.Chdir), and a relative path read after that names a directory inside it
// rather than the package's own sources.
var cliPackageDir = func() string {
	dir, err := filepath.Abs(".")
	if err != nil {
		panic(err)
	}
	return dir
}()

// nextStepAllowPath is where the audit's residue is recorded, one site per
// line, sorted: the file, a tab, and the phrasing.
var nextStepAllowPath = func() string {
	path, err := filepath.Abs("testdata/next_step_allow.golden")
	if err != nil {
		panic(err)
	}
	return path
}()

// updateNextStepAllow regenerates that file. A second flag.Bool of the same
// name panics, so the name is this test's alone.
var updateNextStepAllow = flag.Bool("update-next-step-allow", false, "rewrite testdata/next_step_allow.golden")

// nextStepFollowedAlt is F11's own list of the commands a message must not hand
// to the reader as a step of their own (reconciliation.md F11:
// `git grep -lE "fugaro (update|config publish|project policy publish|image
// build|image refresh)"`). The commands are matched as they are spelled.
const nextStepFollowedAlt = `fugaro (?:update|config publish|project policy publish|image build|image refresh)`

// nextStepShapes are the two ways a message hands one of them over.
//
// The directive shape is a word that makes the command the reader's next move
// ("next: fugaro update", "then run `fugaro update`", "re-run `fugaro config
// publish`") within one sentence of the command's own name. The claim shape is
// the command named as what carries the change the message is about ("`fugaro
// update` publishes it", "fugaro update writes this"), which is a next step
// said as a fact about the change rather than as an instruction.
//
// Both are narrower than "mentions the command", which is the whole of F11's
// grep and would flag every description of what a stage does ("the stages
// `fugaro update` runs"): the audit's question is whether a user-facing message
// sends the reader to run one of these by hand.
var nextStepShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:next|then|now|run|rerun|re-run)\b[^\n]{0,60}?` + nextStepFollowedAlt),
	regexp.MustCompile(`(?i)` + nextStepFollowedAlt + `[^\n]{0,20}?\b(?:publishes|writes|applies|deploys)\s+(?:it|this|that|them|these|your)\b`),
}

// nextStepSite is one message that phrases one of them: the file it is in and
// the phrasing read out of it.
type nextStepSite struct {
	file   string
	phrase string
}

// line is the site as the golden spells it.
func (s nextStepSite) line() string { return s.file + "\t" + s.phrase }

// nextStepSites reads every string literal of this package's non-test sources —
// F11's own file set, so that a word in a comment or an identifier is not a
// message — and answers every site that phrases one of the commands.
//
// A file is read through the parser rather than by a text match so that a
// literal is a literal: the lint is about what a command prints, and a comment
// explaining the rule (this file is full of them) is not a message.
func nextStepSites(t *testing.T) []nextStepSite {
	t.Helper()
	entries, err := os.ReadDir(cliPackageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", cliPackageDir, err)
	}
	var out []nextStepSite
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		path := filepath.Join(cliPackageDir, name)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		var found []string
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, re := range nextStepShapes {
				if m := re.FindString(text); m != "" {
					found = append(found, nextStepPhrase(m))
				}
			}
			return true
		})
		slices.Sort(found)
		for _, phrase := range slices.Compact(found) {
			out = append(out, nextStepSite{file: name, phrase: phrase})
		}
	}
	if files < 100 {
		// A parse that silently read nothing would make every check below pass
		// on a tree it never looked at.
		t.Fatalf("only %d sources were read under %s: the F11 file set is the package's non-test sources, which are many more", files, cliPackageDir)
	}
	slices.SortFunc(out, func(a, b nextStepSite) int {
		if a.file != b.file {
			return strings.Compare(a.file, b.file)
		}
		return strings.Compare(a.phrase, b.phrase)
	})
	return out
}

// nextStepPhrase is a match as the golden records it: one line, with the
// quoting a message uses around a command's name taken out, so that "run
// `fugaro update`" and "run 'fugaro update" are one phrasing rather than three.
// What is left is the words and the command, which is what a reader of the file
// has to judge.
func nextStepPhrase(match string) string {
	line := strings.Join(strings.Fields(match), " ")
	return strings.NewReplacer("`", "", "'", "", `"`, "").Replace(line)
}

// TestNoMessageHandsOverAFollowedUpCommand is the lint: every phrasing of a
// followed-up command that this package's messages still carry is in the
// golden, and every line of the golden is still a phrasing the tree says.
//
// It is the strictness in both directions that makes the file worth reading. A
// new message that hands one over — a command that writes and still tells the
// reader to run `fugaro update`, a help text that promises a later publish —
// fails on its first run, and the fix is the command's own chain (AP4-AP7) and
// not a line in this file. A line whose text is gone fails too, so the file
// only shrinks and every line in it is a text somebody read and left.
func TestNoMessageHandsOverAFollowedUpCommand(t *testing.T) {
	measured := nextStepSites(t)
	if *updateNextStepAllow {
		nextStepAllowWrite(t, measured)
		return
	}
	recorded := nextStepAllowGolden(t)
	measuredLines := make(map[string]bool, len(measured))
	for _, site := range measured {
		measuredLines[site.line()] = true
		if !recorded[site.line()] {
			t.Errorf("%s phrases %q: a user-facing message may not send the reader to a command that is a follow-up of a change fugaro makes itself (spec, acceptance: no user-facing message tells the user to run publish or update by hand). Run the step from the command that made the change, or, when the text is one the audit left, record it in %s with -update-next-step-allow",
				site.file, site.phrase, nextStepAllowPath)
		}
	}
	for line := range recorded {
		if measuredLines[line] {
			continue
		}
		t.Errorf("%s records %q and no message says it any more: delete the line — a golden line that passes fails this test", nextStepAllowPath, line)
	}
}

// TestThePatternCatchesTheShapesTheAuditNames is the other half of the lint:
// the two shapes the audit calls out by name are caught by the pattern, and a
// text that merely describes what a command does is not. Without the first half
// a pattern narrowed to "run `fugaro (...)`" would pass the ratchet above on a
// tree full of "next: fugaro update"; without the second the lint would be a
// ban on naming the commands at all, which every description of a stage does.
func TestThePatternCatchesTheShapesTheAuditNames(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
		why  string
	}{
		{"next: fugaro update", true, "the hand-off the audit names by hand"},
		{"fugaro: the pools are published. next: fugaro update", true, "the same line inside a longer message"},
		{"`fugaro update` publishes it.", true, "the claim the audit names by hand"},
		{"it is written here and fugaro update writes it", true, "the same claim without the quoting"},
		{"run `fugaro image build` and then `fugaro apply`", true, "a directive"},
		{"re-run `fugaro config publish", true, "a directive of another command of the list"},
		{"then run 'fugaro project policy publish'", true, "a directive inside a sentence"},
		{"your next fugaro update applies them", true, "a statement about the installation's next run, which the audit read as the same shape"},
		{"`fugaro update` re-deploys this", true, "the claim with a prefixed verb"},
		{"the stages `fugaro update` runs are the stage list", false, "a description of what the command does"},
		{"the image map is published by `fugaro update`", false, "the command as the agent of a passive sentence about the state"},
		{"fugaro update is the whole upgrade", false, "the CLAUDE.md rule, quoted in a help text"},
	} {
		got := false
		for _, re := range nextStepShapes {
			if re.MatchString(tc.text) {
				got = true
				break
			}
		}
		if got != tc.want {
			t.Errorf("%q: caught = %v, want %v (%s)", tc.text, got, tc.want, tc.why)
		}
	}
}

// nextStepAllowGolden is the recorded set, one line per site.
func nextStepAllowGolden(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(nextStepAllowPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with -update-next-step-allow)", nextStepAllowPath, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no site: a ledger that records nothing would pass this test on a tree that hands every command to the reader", nextStepAllowPath)
	}
	return out
}

// nextStepAllowHeader is the file's own comment block. It is written by the
// regeneration rather than kept beside it, so the file a run rewrites is the
// file a reader reads.
const nextStepAllowHeader = `# Every user-facing text of internal/cli that still phrases one of the commands
# F11's audit names as the reader's own next step, one per line and sorted: the
# file, a tab, and the phrasing read out of it.
#
# The audit (docs/composite-commands/reconciliation.md F11, run by AP7) read
# every such text and classified it. Nothing of class (a) is here — a chain a
# management command now runs itself — because those texts were removed by the
# command that took the chain over, and a text of that class left in the tree
# fails TestNoMessageHandsOverAFollowedUpCommand. What is left is:
#
#   (b) a stale-installation fix, which stays. fugaro update is the whole
#       upgrade (CLAUDE.md) and the commands that find an installation, a
#       checkout or a published node behind are the ones that tell a person to
#       run it: doctor's Fixes, init and setup's closing lines, update's own
#       report, the policy and image doors whose state a run has to be brought
#       to. AP7 classified these, and the hard rule keeps them.
#   (c) a text that names the command as what it is, without handing it to the
#       reader as a step: a claim about the state ("fugaro update re-deploys
#       this"), a sentence about what a command does, a line the reader of a
#       diagnostic is told the meaning of.
#
# A phrasing the tree says and this file does not list fails the test, and a
# line here whose text is gone fails too: the file is the audit's residue, and a
# line the tree has moved past is a line nobody re-read.
#
# Generated by
#   go test ./internal/cli -run TestNoMessageHandsOverAFollowedUpCommand -update-next-step-allow
`

// nextStepAllowWrite rewrites the golden from the measurement.
func nextStepAllowWrite(t *testing.T, measured []nextStepSite) {
	t.Helper()
	var b strings.Builder
	b.WriteString(nextStepAllowHeader)
	for _, site := range measured {
		fmt.Fprintln(&b, site.line())
	}
	if err := os.WriteFile(nextStepAllowPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", nextStepAllowPath, err)
	}
	files := map[string]bool{}
	for _, site := range measured {
		files[site.file] = true
	}
	t.Logf("%s: %d sites across %d files", nextStepAllowPath, len(measured), len(files))
}
