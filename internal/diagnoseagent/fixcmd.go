package diagnoseagent

import (
	"errors"
	"regexp"
	"strings"
)

// The fix-command check (D1614). A diagnosis is model output about text nobody
// here wrote (F5, F20): the log holds Terraform and gcloud bodies, API
// responses and file names, and anyone who can influence one of those can put
// words in front of the model. A configuration answer's whole value is the
// command it hands back, and the person pastes that command into a shell — so
// the one thing this file decides is which printed commands Fugaro has checked
// against its own command tree and which it has not.
//
// The check is one-directional on purpose. A command that passes is shown as
// runnable; a command that fails, and everything that is not a Fugaro command
// at all, is shown as text marked as unchecked, so what the person reads is
// "Fugaro does not know this command" rather than a silent claim of safety.
// Fugaro never runs one of these commands itself (D1602): this decides only
// what is printed.
//
// The tree the check walks belongs to internal/cli, which is why the check
// arrives as a Checker: the command imports this package, so importing it back
// would be a cycle. A nil Checker therefore checks nothing and marks every
// command as unchecked, which is the safe direction rather than a caller that
// forgot being read as agreement.

// fixLineRE matches a line that is a command and nothing else — what a model
// prints as a fix, whether it is a paragraph of its own, an item of a list or a
// line of a fenced block, with or without the backticks a model often wraps a
// command in.
//
// A command printed inside a sentence is deliberately not matched: a line that
// also holds prose is not a line a person copies and runs, and leaving it as
// text is D1614's own safe direction.
var fixLineRE = regexp.MustCompile("^[ \t]*(?:[-*+] |[0-9]+[.)] )?`{0,3}(fugaro(?: [^\n`]*)?)`{0,3}[ \t]*$")

// UncheckedFlag is the sentence printed beside a command Fugaro did not check
// (D1614). It is the same words the troubleshooting guide uses.
const UncheckedFlag = "not checked: do not run blindly"

// Fix is one command a diagnosis printed, and Fugaro's verdict on it.
type Fix struct {
	// Command is the line as it was printed, with the list marker and the
	// backticks removed and outer space trimmed: what a person would paste.
	Command string
	// Argv is Command split into words, which is what Checker is asked about.
	Argv []string
	// Checked is true when the command is one the CLI has: the subcommand
	// path exists in the tree and every flag it names is a flag of that
	// command. Only a checked command is shown as runnable.
	Checked bool
	// Reason is why the command was not checked, "" for one that was. It is
	// what the flag beside the command says beyond UncheckedFlag.
	Reason string
}

// Checker reports whether one `fugaro ...` command line is a command this CLI
// has. argv[0] is `fugaro`; the rest are the subcommand path, its arguments and
// its flags. A nil error is "the tree has this command and these flags".
//
// The real checker is internal/cli's, which walks the command tree this binary
// was built with, so the answer is about the CLI the person is running rather
// than about a list kept here.
type Checker func(argv []string) error

// errFix is the reason a refused command carries.
func errFix(s string) error { return errors.New(s) }

var (
	// errNoChecker is one command's reason when there was no tree to check it
	// against.
	errNoChecker = errFix("no command tree was available to check it against")
	// errNoSubcommand is a bare `fugaro`'s reason: the line names the program
	// rather than a command, so there is nothing to run.
	errNoSubcommand = errFix("it names no fugaro subcommand")
)

// Fixes returns every command line text holds, in the order it printed them,
// each with whether Fugaro checked it. The reading is line-oriented
// (fixLineRE): a command inside a sentence is not one a person runs, so it is
// left to the prose around it.
func Fixes(text string, check Checker) []Fix {
	var out []Fix
	for _, line := range fixLines(text) {
		argv, ok := fixArgv(line)
		if !ok {
			continue
		}
		f := Fix{Command: strings.Join(argv, " "), Argv: argv}
		switch err := checkArgv(check, argv); {
		case err != nil:
			f.Reason = err.Error()
		default:
			f.Checked = true
		}
		out = append(out, f)
	}
	return out
}

// fixArgv is one line's command as words: ok is false for a line that is not a
// command of its own.
func fixArgv(line string) ([]string, bool) {
	m := fixLineRE.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	argv := strings.Fields(strings.TrimSpace(m[1]))
	if len(argv) == 0 {
		return nil, false
	}
	return argv, true
}

// checkArgv asks check about one command, treating a missing checker as "not
// checked" rather than as a pass.
func checkArgv(check Checker, argv []string) error {
	if check == nil {
		return errNoChecker
	}
	if len(argv) < 2 {
		return errNoSubcommand
	}
	return check(argv)
}

// FixText is text with every command line D1614's check refused followed by the
// flag that says so. A line the check accepted is left exactly as it was
// printed.
func FixText(text string, check Checker) string {
	lines := fixLines(text)
	fixes := Fixes(text, check)
	if len(fixes) == 0 {
		return text
	}
	out := make([]string, 0, len(lines))
	// The fixes are in line order, so one walk over both lists pairs each
	// command with the line it came from without reading the line twice.
	next := 0
	for _, line := range lines {
		out = append(out, line)
		if _, ok := fixArgv(line); !ok {
			continue
		}
		f := fixes[next]
		next++
		if !f.Checked {
			out = append(out, "  ("+UncheckedFlag+" — "+f.Reason+")")
		}
	}
	return strings.Join(out, "\n")
}

// fixLines splits text the way Fixes and FixText both read it, so the two
// cannot disagree about which line is which.
func fixLines(text string) []string { return strings.Split(text, "\n") }
