package cli

// The persistent --verbose (-v), and the reporter a command builds from it.
//
// The flag is the root's and persistent, so every subcommand takes it: the
// documented form is `fugaro update -v`, because a LEADING `fugaro -v` is the
// version (NormalizeVersionArgs rewrites it at the argv boundary before cobra
// parses anything). That rewrite is load-bearing here: the shorthand this flag
// takes is the one cobra would otherwise give the root's version flag, so
// `fugaro -v` stays the version only because the boundary sees it first (F4,
// D1353).
//
// reporterFor is the one place a command turns its flags, the environment and
// the package's terminal seam into a report.Reporter, so no command invents its
// own colour or spinner rule (D1354). CL4 wires no command to it: a command
// that prints its steps through the reporter does so in a later task, and every
// command's output is unchanged here.

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// verboseFlag is the persistent flag's name, read by reporterFor and declared
// by the root (newCoreRoot).
const verboseFlag = "verbose"

// openLogFor opens the log a run's full output is kept in (D1352). It is a
// variable because the commands that keep a log hand it a sink carrying the
// converge's own live redaction slots (CL5b) -- report.OpenLog is the
// implementation -- and CL4 is the flag plumbing alone: with the default, a run
// keeps no log, which is what a read command does for good (T6). An opener that
// fails is "no log" and never a failed command (G5).
var openLogFor = func(string) (report.Sink, error) { return nil, nil }

// reporterFor builds the reporter for one command's run: the persistent
// --verbose (a command's own --verbose of the same name shadows it, which is
// `recipes ls`'s), the environment the colour rule reads, the package's
// terminal seam, and the command's own writers. name is the command name the
// run's log is filed under (D1352).
//
// The reporter's colour and spinner rules read the OUTPUT writer: stdin's
// terminal-ness is what the confirmation paths ask before they read a line
// (G1), not something the reporter draws by.
func reporterFor(cmd *cobra.Command, name string) *report.Reporter {
	o := report.Options{
		Out:        cmd.OutOrStdout(),
		Err:        cmd.ErrOrStderr(),
		Getenv:     os.Getenv,
		IsTerminal: writerIsTerminal,
		Verbose:    boolFlag(cmd, verboseFlag),
		JSON:       boolFlag(cmd, "json"),
		NoColor:    boolFlag(cmd, "no-color"),
		ASCII:      boolFlag(cmd, "ascii"),
	}
	if sink, err := openLogFor(name); err == nil {
		o.Sink = sink
	}
	return report.New(o)
}

// boolFlag reads a boolean flag when the command has it, and is false when it
// does not: most commands have --json, and only watch has --no-color and
// --ascii.
func boolFlag(cmd *cobra.Command, name string) bool {
	if cmd.Flags().Lookup(name) == nil {
		return false
	}
	v, err := cmd.Flags().GetBool(name)
	return err == nil && v
}
