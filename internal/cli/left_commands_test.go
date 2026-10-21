package cli

// The left-for-you matrix's other half: the commands it prints are commands.
//
// testdata/init_commands.golden is every left this package hands a user who
// has to finish something by hand -- the prompt to type the project's name, the
// one-line opt-in, the secrets to set -- pinned so a change to any of them is
// reviewed. What it could not say while `fugaro init` was live is whether the
// command still exists: a rename that missed one row would leave a row the
// matrix happily pins and a user who types a command that refuses (W1.2's
// tombstone exits 1, which is the failure this file exists to make loud), and
// a flag rename would leave one that errors out instead.
//
// So this test resolves them: every command the golden prints starts with
// `fugaro <verb>`, and <verb> is a command of this build's root -- the same
// root the binary is assembled from (NewRootCmd), not a list kept here, which
// would be a second copy of the command tree and would drift from it.
// `init` is singled out and refused by name, because it DOES resolve: it is
// the hidden tombstone, one message and exit 1, so a check that only asked
// whether the verb exists would pass on exactly the row this is for.
//
// Only the commands are read, not the rows' prose: a row's text is a sentence
// ("type the project's name at each apply") and holds words that are not
// commands, and a walk over every `fugaro ` in the file would have to be told
// which prose is a command -- a judgement this test would then be making in
// the one place it must not, the place the matrix is reviewed from.
//
// It reads the golden rather than running the stages because the golden is
// what a reviewer reads: a row that stops being printed is a golden diff
// (TestPrintedCommandsGolden), and a row that prints a command nobody can run
// is this.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goldenRowRE is one row of the printed-commands golden: its name, the kind of
// left it is (command, console or prompt), and its text. The commands a row
// prints are the text's own when it is one (`command: fugaro update-skills`)
// and the four-space-indented lines under it otherwise.
var goldenRowRE = regexp.MustCompile(`^\[([^\]]+)\] (command|console|prompt): (.*)$`)

// TestEveryLeftForYouNamesACommandThatExists resolves every command in the
// left-for-you matrix against the root command, and refuses the tombstone.
func TestEveryLeftForYouNamesACommandThatExists(t *testing.T) {
	known := map[string]bool{}
	for _, c := range NewRootCmd().Commands() {
		known[c.Name()] = true
		for _, a := range c.Aliases {
			known[a] = true
		}
	}
	data, err := os.ReadFile(filepath.Join("testdata", "init_commands.golden"))
	if err != nil {
		t.Fatal(err)
	}
	commands := printedCommands(string(data))
	if len(commands) == 0 {
		// A golden this test cannot read a command out of would pass by
		// finding nothing, which is the one way a test like this fails
		// silently: the shape changed and nobody noticed.
		t.Fatal("no commands read out of the left-for-you matrix: its shape changed")
	}
	for _, c := range commands {
		verb := strings.Fields(c)[1] // the golden's commands are `fugaro <verb> ...`
		switch {
		case verb == "init":
			t.Errorf("%s: the matrix hands the user `fugaro init`, which is the retired tombstone (it exits 1); name `fugaro setup` or `fugaro update`, whichever the reader is doing", c)
		case !known[verb]:
			t.Errorf("%s: `fugaro %s` is not a command of this build", c, verb)
		}
	}
}

// printedCommands is every command line of the golden: a row whose text is one
// (`command: fugaro ...`), and every four-space-indented line under a row,
// which is where the matrix prints the command to run -- a prompt's line is
// the command that asks, and a multi-line console's lines are the commands
// themselves. Text that starts with anything else (a sentence, a `gcloud`
// line, a `git switch`) is left alone: those are other tools' commands and
// not this build's to resolve.
func printedCommands(golden string) []string {
	var out []string
	inRow := false
	for _, line := range strings.Split(golden, "\n") {
		if m := goldenRowRE.FindStringSubmatch(line); m != nil {
			inRow = true
			if text := m[3]; strings.HasPrefix(text, "fugaro ") {
				out = append(out, text)
			}
			continue
		}
		if !inRow {
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			continue
		}
		if text := strings.TrimSpace(line); strings.HasPrefix(text, "fugaro ") {
			out = append(out, text)
		}
	}
	return out
}
